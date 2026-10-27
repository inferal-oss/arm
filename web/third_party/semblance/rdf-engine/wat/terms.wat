;; Terms: the term table and the interning hash table.
;;
;; Term id i is the 32-byte record at $terms + i*32:
;;   +0  kind | direction << 8
;;   +4  hash
;;   +8  a: value ptr  (triple: subject)
;;   +12 b: value len  (triple: predicate)
;;   +16 c: datatype   (triple: object)
;;   +20 language length; the tag is stored lowercased right after the value
;;   +24 an IRI: its datatype class (expr.wat); a literal: its parsed number (num.wat)
;;   +28 next freed id; for a live IRI or literal, the id of its STR() (or 0)
;; Value and tag share one block of value_len + lang_len bytes.
;;
;; The interning table is open addressing with linear probing over term ids
;; (0 = empty), at most half full, so a lookup is O(1) expected; each probe
;; compares the stored hash before any bytes.

(global $terms (mut i32) (i32.const 0))
(global $terms_cap (mut i32) (i32.const 0))   ;; in records
(global $next_id (mut i32) (i32.const 1))
(global $live (mut i32) (i32.const 0))
(global $free_ids (mut i32) (i32.const 0))    ;; freed ids, linked through record +28
(global $htab (mut i32) (i32.const 0))
(global $hcap (mut i32) (i32.const 0))        ;; slots, a power of two
(global $hcount (mut i32) (i32.const 0))
(global $created (mut i32) (i32.const 0))     ;; set by $intern: whether it made a new term
(global $fresh (mut i32) (i32.const 0))       ;; counter for fresh blank node labels
(global $freshbuf (mut i32) (i32.const 0))

;; The ids interned at start.
;; 1 default graph, 2 xsd:string, 3 rdf:langString, 4 rdf:dirLangString

(func $rec (param $id i32) (result i32)
  (i32.add (global.get $terms) (i32.shl (local.get $id) (i32.const 5))))

;; The kind of id, 0 when it is not a live term.
(func $kind (param $id i32) (result i32)
  (if (result i32) (i32.lt_u (i32.sub (local.get $id) (i32.const 1)) (i32.sub (global.get $next_id) (i32.const 1)))
    (then (i32.load8_u (call $rec (local.get $id))))
    (else (i32.const 0))))

;; --- hashing (MurmurHash3 x86_32 rounds; byte strings eight bytes a step) -------------------------------------

(func $mix (param $h i32) (param $k i32) (result i32)
  (i32.add
    (i32.mul (i32.rotl (i32.xor (local.get $h)
      (i32.mul (i32.rotl (i32.mul (local.get $k) (i32.const 0xcc9e2d51)) (i32.const 15)) (i32.const 0x1b873593)))
      (i32.const 13)) (i32.const 5))
    (i32.const 0xe6546b64)))

(func $fmix (param $h i32) (result i32)
  (local.set $h (i32.mul (i32.xor (local.get $h) (i32.shr_u (local.get $h) (i32.const 16))) (i32.const 0x85ebca6b)))
  (local.set $h (i32.mul (i32.xor (local.get $h) (i32.shr_u (local.get $h) (i32.const 13))) (i32.const 0xc2b2ae35)))
  (i32.xor (local.get $h) (i32.shr_u (local.get $h) (i32.const 16))))

(func $hash_bytes (param $h i32) (param $p i32) (param $n i32) (result i32)
  (local $x i64)
  ;; eight bytes a step (multiply, fold the high bits down), seeded with h and n;
  ;; the last 1-7 bytes as one overlapping word
  (local.set $x (i64.extend_i32_u (call $mix (local.get $h) (local.get $n))))
  (loop $words
    (if (i32.ge_u (local.get $n) (i32.const 8)) (then
      (local.set $x (call $mix64 (local.get $x) (i64.load (local.get $p))))
      (local.set $p (i32.add (local.get $p) (i32.const 8)))
      (local.set $n (i32.sub (local.get $n) (i32.const 8)))
      (br $words))))
  (if (i32.ge_u (local.get $n) (i32.const 4))
    (then (local.set $x (call $mix64 (local.get $x)
      (i64.or (i64.shl (i64.load32_u (local.get $p)) (i64.const 32)) (i64.load32_u (i32.sub (i32.add (local.get $p) (local.get $n)) (i32.const 4)))))))
    (else (if (local.get $n) (then (local.set $x (call $mix64 (local.get $x)
      (i64.extend_i32_u (i32.or (i32.or (i32.shl (i32.load8_u (local.get $p)) (i32.const 16))
                                        (i32.shl (i32.load8_u (i32.add (local.get $p) (i32.shr_u (local.get $n) (i32.const 1)))) (i32.const 8)))
                                (i32.load8_u (i32.sub (i32.add (local.get $p) (local.get $n)) (i32.const 1)))))))))))
  (i32.wrap_i64 (i64.xor (local.get $x) (i64.shr_u (local.get $x) (i64.const 32)))))

(func $mix64 (param $x i64) (param $w i64) (result i64)
  (local.set $x (i64.mul (i64.xor (local.get $x) (local.get $w)) (i64.const 0x9e3779b97f4a7c15)))
  (i64.xor (local.get $x) (i64.shr_u (local.get $x) (i64.const 29))))

;; --- the term table ----------------------------------------------------------

;; A new id with a zeroed record and zeroed index lists (see store.wat).
(func $new_id (result i32)
  (local $id i32) (local $cap i32)
  ;; a freed id first (collect)
  (if (local.tee $id (global.get $free_ids)) (then
    (global.set $free_ids (i32.load offset=28 (call $rec (local.get $id))))
    (memory.fill (call $rec (local.get $id)) (i32.const 0) (i32.const 32))
    (global.set $live (i32.add (global.get $live) (i32.const 1)))
    (return (local.get $id))))
  (local.set $id (global.get $next_id))
  (if (i32.eq (local.get $id) (global.get $terms_cap)) (then
    (local.set $cap (i32.shl (local.get $id) (i32.const 1)))
    (global.set $terms (call $realloc (global.get $terms) (i32.shl (local.get $id) (i32.const 5))
      (i32.shl (local.get $cap) (i32.const 5)) (i32.shl (local.get $id) (i32.const 5))))
    (global.set $post (call $realloc (global.get $post) (i32.shl (local.get $id) (i32.const 4))
      (i32.shl (local.get $cap) (i32.const 4)) (i32.shl (local.get $id) (i32.const 4))))
    (i32.store (global.get $bases) (global.get $post))
    (i32.store offset=16 (global.get $bases) (global.get $terms))
    (global.set $terms_cap (local.get $cap))))
  (memory.fill (call $rec (local.get $id)) (i32.const 0) (i32.const 32))
  (memory.fill (call $post_of (local.get $id)) (i32.const 0) (i32.const 16))
  (global.set $next_id (i32.add (local.get $id) (i32.const 1)))
  (global.set $live (i32.add (global.get $live) (i32.const 1)))
  (local.get $id))

;; Put id into the interning table at its hash; doubles the table at half full.
(func $hput (param $id i32)
  (local $i i32) (local $mask i32)
  (local.set $mask (i32.sub (global.get $hcap) (i32.const 1)))
  (local.set $i (i32.load offset=4 (call $rec (local.get $id))))
  (loop $probe
    (local.set $i (i32.and (local.get $i) (local.get $mask)))
    (if (i32.load (i32.add (global.get $htab) (i32.shl (local.get $i) (i32.const 2)))) (then
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $probe))))
  (i32.store (i32.add (global.get $htab) (i32.shl (local.get $i) (i32.const 2))) (local.get $id))
  (global.set $hcount (i32.add (global.get $hcount) (i32.const 1)))
  (if (i32.gt_u (i32.shl (global.get $hcount) (i32.const 1)) (global.get $hcap)) (then (call $hgrow))))

(func $hgrow
  (local $old i32) (local $n i32) (local $i i32) (local $id i32)
  (local.set $old (global.get $htab))
  (local.set $n (global.get $hcap))
  (global.set $hcap (i32.shl (local.get $n) (i32.const 1)))
  (global.set $htab (call $alloc0 (i32.shl (global.get $hcap) (i32.const 2))))
  (global.set $hcount (i32.const 0))
  (loop $each
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (if (local.tee $id (i32.load (i32.add (local.get $old) (i32.shl (local.get $i) (i32.const 2)))))
        (then (call $hput (local.get $id))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $each))))
  (call $free (local.get $old) (i32.shl (local.get $n) (i32.const 2))))

;; Intern (or with create = 0, look up) an IRI, blank node or literal.
;; kd is kind | direction << 8; for literals dt is the datatype id and the
;; tag (already lowercase) is n2 bytes at p2. Returns the id, or 0.
;; While a query is prepared, every term it interns or looks up is recorded
;; (collect keeps them).
(global $rec_vec (mut i32) (i32.const 0))
(func $recorded (param $id i32) (result i32)
  (if (i32.and (i32.ne (global.get $rec_vec) (i32.const 0)) (i32.ne (local.get $id) (i32.const 0)))
    (then (call $v_push1 (global.get $rec_vec) (local.get $id))))
  (local.get $id))

(func $intern (param $kd i32) (param $p i32) (param $n i32) (param $dt i32) (param $p2 i32) (param $n2 i32) (param $create i32) (result i32)
  (call $recorded (call $intern_raw (local.get $kd) (local.get $p) (local.get $n) (local.get $dt) (local.get $p2) (local.get $n2) (local.get $create))))

(func $intern_raw (param $kd i32) (param $p i32) (param $n i32) (param $dt i32) (param $p2 i32) (param $n2 i32) (param $create i32) (result i32)
  (local $h i32) (local $i i32) (local $id i32) (local $r i32) (local $mask i32)
  (local.set $h (call $hash_bytes (local.get $kd) (local.get $p) (local.get $n)))
  (if (i32.eq (i32.and (local.get $kd) (i32.const 0xff)) (i32.const 3)) (then
    (local.set $h (call $hash_bytes (call $mix (local.get $h) (local.get $dt)) (local.get $p2) (local.get $n2)))))
  (local.set $h (call $fmix (local.get $h)))
  (global.set $created (i32.const 0))
  (local.set $mask (i32.sub (global.get $hcap) (i32.const 1)))
  (local.set $i (local.get $h))
  (block $absent
    (loop $probe
      (local.set $i (i32.and (local.get $i) (local.get $mask)))
      (br_if $absent (i32.eqz (local.tee $id (i32.load (i32.add (global.get $htab) (i32.shl (local.get $i) (i32.const 2)))))))
      (local.set $r (call $rec (local.get $id)))
      (if (i32.and (i32.and (i32.eq (i32.load offset=4 (local.get $r)) (local.get $h))
                            (i32.eq (i32.load (local.get $r)) (local.get $kd)))
                   (i32.and (i32.eq (i32.load offset=12 (local.get $r)) (local.get $n))
                            (i32.eq (i32.load offset=20 (local.get $r)) (local.get $n2))))
        (then
          (if (i32.eq (i32.load offset=16 (local.get $r)) (local.get $dt)) (then
            (if (call $memeq (i32.load offset=8 (local.get $r)) (local.get $p) (local.get $n)) (then
              (if (call $memeq (i32.add (i32.load offset=8 (local.get $r)) (local.get $n)) (local.get $p2) (local.get $n2))
                (then (return (local.get $id))))))))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $probe)))
  (if (i32.eqz (local.get $create)) (then (return (i32.const 0))))
  (local.set $id (call $new_id))
  (local.set $r (call $rec (local.get $id)))
  (i32.store (local.get $r) (local.get $kd))
  (i32.store offset=4 (local.get $r) (local.get $h))
  (i32.store offset=8 (local.get $r) (local.tee $i (call $alloc (i32.add (local.get $n) (local.get $n2)))))
  (memory.copy (local.get $i) (local.get $p) (local.get $n))
  (memory.copy (i32.add (local.get $i) (local.get $n)) (local.get $p2) (local.get $n2))
  (i32.store offset=12 (local.get $r) (local.get $n))
  (i32.store offset=16 (local.get $r) (local.get $dt))
  (i32.store offset=20 (local.get $r) (local.get $n2))
  (call $hput (local.get $id))
  (global.set $created (i32.const 1))
  (local.get $id))

;; Intern (or look up) the triple term (s, p, o).
(func $intern_triple (param $s i32) (param $p i32) (param $o i32) (param $create i32) (result i32)
  (call $recorded (call $intern_triple_raw (local.get $s) (local.get $p) (local.get $o) (local.get $create))))

(func $intern_triple_raw (param $s i32) (param $p i32) (param $o i32) (param $create i32) (result i32)
  (local $h i32) (local $i i32) (local $id i32) (local $r i32) (local $mask i32)
  (local.set $h (call $fmix (call $mix (call $mix (call $mix (i32.const 4) (local.get $s)) (local.get $p)) (local.get $o))))
  (local.set $mask (i32.sub (global.get $hcap) (i32.const 1)))
  (local.set $i (local.get $h))
  (block $absent
    (loop $probe
      (local.set $i (i32.and (local.get $i) (local.get $mask)))
      (br_if $absent (i32.eqz (local.tee $id (i32.load (i32.add (global.get $htab) (i32.shl (local.get $i) (i32.const 2)))))))
      (local.set $r (call $rec (local.get $id)))
      (if (i32.and (i32.and (i32.eq (i32.load offset=4 (local.get $r)) (local.get $h))
                            (i32.eq (i32.load (local.get $r)) (i32.const 4)))
                   (i32.and (i32.eq (i32.load offset=8 (local.get $r)) (local.get $s))
                            (i32.and (i32.eq (i32.load offset=12 (local.get $r)) (local.get $p))
                                     (i32.eq (i32.load offset=16 (local.get $r)) (local.get $o)))))
        (then (return (local.get $id))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $probe)))
  (if (i32.eqz (local.get $create)) (then (return (i32.const 0))))
  (local.set $id (call $new_id))
  (local.set $r (call $rec (local.get $id)))
  (i32.store (local.get $r) (i32.const 4))
  (i32.store offset=4 (local.get $r) (local.get $h))
  (i32.store offset=8 (local.get $r) (local.get $s))
  (i32.store offset=12 (local.get $r) (local.get $p))
  (i32.store offset=16 (local.get $r) (local.get $o))
  (call $hput (local.get $id))
  (local.get $id))

;; A blank node with a label no other blank node has: "b" and a counter.
(func $fresh_bnode (result i32)
  (local $id i32)
  (loop $try
    (global.set $fresh (i32.add (global.get $fresh) (i32.const 1)))
    (local.set $id (call $intern (i32.const 2) (global.get $freshbuf)
      (i32.sub (call $utoa (i32.add (global.get $freshbuf) (i32.const 1)) (global.get $fresh)) (global.get $freshbuf))
      (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))
    (br_if $try (i32.eqz (global.get $created))))
  (local.get $id))

(func $init_terms
  (global.set $terms_cap (i32.const 256))
  (global.set $terms (call $alloc (i32.const 8192)))
  (global.set $post (call $alloc (i32.const 4096)))
  (i32.store (global.get $bases) (global.get $post))
  (i32.store offset=16 (global.get $bases) (global.get $terms))
  (global.set $hcap (i32.const 512))
  (global.set $htab (call $alloc0 (i32.const 2048)))
  (global.set $freshbuf (call $alloc (i32.const 16)))
  (i32.store8 (global.get $freshbuf) (i32.const 'b'))
  (i32.store (call $rec (call $new_id)) (i32.const 5))
  (drop (call $intern (i32.const 1) (@str "http://www.w3.org/2001/XMLSchema#string") (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))
  (drop (call $intern (i32.const 1) (@str "http://www.w3.org/1999/02/22-rdf-syntax-ns#langString") (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))
  (drop (call $intern (i32.const 1) (@str "http://www.w3.org/1999/02/22-rdf-syntax-ns#dirLangString") (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1))))
