;; IRIs: the absolute check of N-Triples, and RFC 3986 reference resolution
;; (section 5.2, with remove_dot_segments) for Turtle and TriG.
;;
;; The base is [$base_p, $base_p + $base_n): the load's base argument, or after
;; @base/BASE a copy in $basebuf. Results are built in the scratch buffer $sb.

(global $base_p (mut i32) (i32.const 0))
(global $base_n (mut i32) (i32.const 0))
(global $basebuf (mut i32) (i32.const 0))
(global $basebuf_cap (mut i32) (i32.const 0))
(global $sb (mut i32) (i32.const 0))
(global $sb_cap (mut i32) (i32.const 0))
(global $parts (mut i32) (i32.const 0))   ;; two 36-byte $split records

;; Make the scratch buffer at least n bytes (its contents are not kept).
(func $sb_reserve (param $n i32)
  (if (i32.gt_u (local.get $n) (global.get $sb_cap)) (then
    (if (global.get $sb_cap) (then (call $free (global.get $sb) (global.get $sb_cap))))
    (global.set $sb (call $alloc (local.get $n)))
    (global.set $sb_cap (call $class_size (call $class (local.get $n)))))))

;; The length of the scheme of [p, p+n) (ALPHA *(ALPHA / DIGIT / "+" / "-" / ".")
;; then ':'), or 0.
(func $scheme_len (param $p i32) (param $n i32) (result i32)
  (local $i i32) (local $c i32)
  (if (i32.or (i32.eqz (local.get $n)) (i32.eqz (call $is_alpha (i32.load8_u (local.get $p))))) (then (return (i32.const 0))))
  (loop $chars
    (if (i32.ge_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (local.get $n)) (then (return (i32.const 0))))
    (local.set $c (i32.load8_u (i32.add (local.get $p) (local.get $i))))
    (if (i32.eq (local.get $c) (i32.const ':')) (then (return (local.get $i))))
    (br_if $chars (i32.or (i32.or (call $is_alpha (local.get $c)) (call $is_digit (local.get $c)))
                          (call $one_of (local.get $c) (@str "+-.")))))
  (i32.const 0))

;; Split [p, p+n) into the record at o (pointers; a length of -1 = absent):
;;   +0 scheme length (0 = none)  +4 authority  +8 its length  +12 path
;;   +16 its length  +20 query  +24 its length  +28 fragment  +32 its length
(func $split (param $p i32) (param $n i32) (param $o i32)
  (local $q i32) (local $e i32) (local $a i32) (local $s i32)
  (local.set $e (i32.add (local.get $p) (local.get $n)))
  (local.set $s (call $scheme_len (local.get $p) (local.get $n)))
  (i32.store (local.get $o) (local.get $s))
  (local.set $q (i32.add (local.get $p) (select (i32.add (local.get $s) (i32.const 1)) (i32.const 0) (local.get $s))))
  (i32.store offset=8 (local.get $o) (i32.const -1))
  (if (i32.and (i32.le_u (i32.add (local.get $q) (i32.const 2)) (local.get $e))
               (i32.eq (i32.load16_u (local.get $q)) (i32.const 0x2f2f))) (then   ;; "//"
    (local.set $a (local.tee $q (i32.add (local.get $q) (i32.const 2))))
    (loop $auth
      (if (i32.lt_u (local.get $q) (local.get $e)) (then
        (if (i32.eqz (call $one_of (i32.load8_u (local.get $q)) (@str "/?#"))) (then
          (local.set $q (i32.add (local.get $q) (i32.const 1)))
          (br $auth))))))
    (i32.store offset=4 (local.get $o) (local.get $a))
    (i32.store offset=8 (local.get $o) (i32.sub (local.get $q) (local.get $a)))))
  (local.set $a (local.get $q))
  (loop $path
    (if (i32.lt_u (local.get $q) (local.get $e)) (then
      (if (i32.eqz (call $one_of (i32.load8_u (local.get $q)) (@str "?#"))) (then
        (local.set $q (i32.add (local.get $q) (i32.const 1)))
        (br $path))))))
  (i32.store offset=12 (local.get $o) (local.get $a))
  (i32.store offset=16 (local.get $o) (i32.sub (local.get $q) (local.get $a)))
  (i32.store offset=24 (local.get $o) (i32.const -1))
  (if (i32.and (i32.lt_u (local.get $q) (local.get $e)) (i32.eq (i32.load8_u (local.get $q)) (i32.const '?'))) (then
    (local.set $a (local.tee $q (i32.add (local.get $q) (i32.const 1))))
    (loop $query
      (if (i32.lt_u (local.get $q) (local.get $e)) (then
        (if (i32.ne (i32.load8_u (local.get $q)) (i32.const '#')) (then
          (local.set $q (i32.add (local.get $q) (i32.const 1)))
          (br $query))))))
    (i32.store offset=20 (local.get $o) (local.get $a))
    (i32.store offset=24 (local.get $o) (i32.sub (local.get $q) (local.get $a)))))
  (i32.store offset=32 (local.get $o) (i32.const -1))
  (if (i32.lt_u (local.get $q) (local.get $e)) (then
    (i32.store offset=28 (local.get $o) (i32.add (local.get $q) (i32.const 1)))
    (i32.store offset=32 (local.get $o) (i32.sub (i32.sub (local.get $e) (local.get $q)) (i32.const 1))))))

(func $put (param $w i32) (param $p i32) (param $n i32) (result i32)
  (memory.copy (local.get $w) (local.get $p) (local.get $n))
  (i32.add (local.get $w) (local.get $n)))

;; Write c ("//" for '/'), then the component whose pointer and length are at
;; o + off and o + off + 4, when it is present (length >= 0).
(func $put_part (param $w i32) (param $c i32) (param $o i32) (param $off i32) (result i32)
  (local $n i32)
  (if (i32.lt_s (local.tee $n (i32.load offset=4 (i32.add (local.get $o) (local.get $off)))) (i32.const 0))
    (then (return (local.get $w))))
  (i32.store8 (local.get $w) (local.get $c))
  (if (i32.eq (local.get $c) (i32.const '/')) (then   ;; authority: "//"
    (i32.store8 offset=1 (local.get $w) (local.get $c))
    (local.set $w (i32.add (local.get $w) (i32.const 1)))))
  (call $put (i32.add (local.get $w) (i32.const 1)) (i32.load (i32.add (local.get $o) (local.get $off))) (local.get $n)))

;; Whether the k bytes at s start [i, e) (exactly, when `whole`).
(func $at (param $i i32) (param $e i32) (param $s i32) (param $k i32) (param $whole i32) (result i32)
  (local $n i32)
  (local.set $n (i32.sub (local.get $e) (local.get $i)))
  (if (result i32) (select (i32.eq (local.get $n) (local.get $k)) (i32.ge_u (local.get $n) (local.get $k)) (local.get $whole))
    (then (call $memeq (local.get $i) (local.get $s) (local.get $k)))
    (else (i32.const 0))))

;; The end of the output [o, w) without its last segment and that segment's '/'.
(func $pop (param $o i32) (param $w i32) (result i32)
  (loop $back
    (if (i32.gt_u (local.get $w) (local.get $o)) (then
      (local.set $w (i32.sub (local.get $w) (i32.const 1)))
      (br_if $back (i32.ne (i32.load8_u (local.get $w)) (i32.const '/'))))))
  (local.get $w))

;; Write path [i, i+n) at w with dot segments removed (RFC 3986 5.2.4); returns the end.
(func $rds (param $w i32) (param $i i32) (param $n i32) (result i32)
  (local $e i32) (local $o i32)
  (local.set $e (i32.add (local.get $i) (local.get $n)))
  (local.set $o (local.get $w))
  (loop $seg
    (if (i32.ge_u (local.get $i) (local.get $e)) (then (return (local.get $w))))
    ;; A: "../" or "./"
    (if (call $at (local.get $i) (local.get $e) (@str "../") (i32.const 0)) (then (local.set $i (i32.add (local.get $i) (i32.const 3))) (br $seg)))
    (if (call $at (local.get $i) (local.get $e) (@str "./") (i32.const 0)) (then (local.set $i (i32.add (local.get $i) (i32.const 2))) (br $seg)))
    ;; B: "/./" or "/." -> "/"
    (if (call $at (local.get $i) (local.get $e) (@str "/./") (i32.const 0)) (then (local.set $i (i32.add (local.get $i) (i32.const 2))) (br $seg)))
    (if (call $at (local.get $i) (local.get $e) (@str "/.") (i32.const 1)) (then
      (i32.store8 (local.get $w) (i32.const '/'))
      (return (i32.add (local.get $w) (i32.const 1)))))
    ;; C: "/../" or "/.." -> "/", dropping the last output segment
    (if (call $at (local.get $i) (local.get $e) (@str "/../") (i32.const 0)) (then
      (local.set $i (i32.add (local.get $i) (i32.const 3)))
      (local.set $w (call $pop (local.get $o) (local.get $w)))
      (br $seg)))
    (if (call $at (local.get $i) (local.get $e) (@str "/..") (i32.const 1)) (then
      (local.set $w (call $pop (local.get $o) (local.get $w)))
      (i32.store8 (local.get $w) (i32.const '/'))
      (return (i32.add (local.get $w) (i32.const 1)))))
    ;; D: "." or ".."
    (if (i32.or (call $at (local.get $i) (local.get $e) (@str ".") (i32.const 1))
                (call $at (local.get $i) (local.get $e) (@str "..") (i32.const 1)))
      (then (return (local.get $w))))
    ;; E: move the first segment, with its leading '/', to the output
    (loop $chars
      (i32.store8 (local.get $w) (i32.load8_u (local.get $i)))
      (local.set $w (i32.add (local.get $w) (i32.const 1)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br_if $chars (i32.and (i32.lt_u (local.get $i) (local.get $e)) (i32.ne (i32.load8_u (local.get $i)) (i32.const '/')))))
    (br $seg))
  (unreachable))

;; Resolve the reference [p, p+n) against the base into $sb; returns its length.
(func $resolve (param $p i32) (param $n i32) (result i32)
  (local $r i32) (local $b i32) (local $w i32) (local $t i32) (local $tmp i32) (local $k i32)
  (local.set $k (i32.add (i32.add (local.get $n) (global.get $base_n)) (i32.const 8)))
  (call $sb_reserve (i32.shl (local.get $k) (i32.const 1)))
  (local.set $tmp (i32.add (global.get $sb) (local.get $k)))
  (local.set $r (global.get $parts))
  (local.set $b (i32.add (global.get $parts) (i32.const 36)))
  (call $split (local.get $p) (local.get $n) (local.get $r))
  (call $split (global.get $base_p) (global.get $base_n) (local.get $b))
  (local.set $w (global.get $sb))
  (block $query_r
    (if (local.tee $k (i32.load (local.get $r))) (then
      (local.set $w (call $put (local.get $w) (local.get $p) (i32.add (local.get $k) (i32.const 1))))
      (local.set $w (call $put_part (local.get $w) (i32.const '/') (local.get $r) (i32.const 4)))
      (local.set $w (call $rds (local.get $w) (i32.load offset=12 (local.get $r)) (i32.load offset=16 (local.get $r))))
      (br $query_r)))
    (if (local.tee $k (i32.load (local.get $b)))
      (then (local.set $w (call $put (local.get $w) (global.get $base_p) (i32.add (local.get $k) (i32.const 1))))))
    (if (i32.ge_s (i32.load offset=8 (local.get $r)) (i32.const 0)) (then
      (local.set $w (call $put_part (local.get $w) (i32.const '/') (local.get $r) (i32.const 4)))
      (local.set $w (call $rds (local.get $w) (i32.load offset=12 (local.get $r)) (i32.load offset=16 (local.get $r))))
      (br $query_r)))
    (local.set $w (call $put_part (local.get $w) (i32.const '/') (local.get $b) (i32.const 4)))
    (if (i32.eqz (i32.load offset=16 (local.get $r))) (then
      (local.set $w (call $put (local.get $w) (i32.load offset=12 (local.get $b)) (i32.load offset=16 (local.get $b))))
      ;; the base query, unless the reference has its own (written below)
      (if (i32.lt_s (i32.load offset=24 (local.get $r)) (i32.const 0))
        (then (local.set $w (call $put_part (local.get $w) (i32.const '?') (local.get $b) (i32.const 20)))))
      (br $query_r)))
    (if (i32.eq (i32.load8_u (i32.load offset=12 (local.get $r))) (i32.const '/')) (then
      (local.set $w (call $rds (local.get $w) (i32.load offset=12 (local.get $r)) (i32.load offset=16 (local.get $r))))
      (br $query_r)))
    ;; merge: the base path up to its last '/', then the reference path
    (local.set $t (local.get $tmp))
    (if (i32.and (i32.ge_s (i32.load offset=8 (local.get $b)) (i32.const 0)) (i32.eqz (i32.load offset=16 (local.get $b))))
      (then
        (i32.store8 (local.get $t) (i32.const '/'))
        (local.set $t (i32.add (local.get $t) (i32.const 1))))
      (else
        (local.set $k (i32.sub (call $pop (i32.load offset=12 (local.get $b))
                                          (i32.add (i32.load offset=12 (local.get $b)) (i32.load offset=16 (local.get $b))))
                               (i32.load offset=12 (local.get $b))))
        ;; keep the '/' $pop stopped on, if it found one
        (if (i32.lt_u (local.get $k) (i32.load offset=16 (local.get $b))) (then
          (local.set $k (i32.add (local.get $k)
            (i32.eq (i32.load8_u (i32.add (i32.load offset=12 (local.get $b)) (local.get $k))) (i32.const '/'))))))
        (local.set $t (call $put (local.get $t) (i32.load offset=12 (local.get $b)) (local.get $k)))))
    (local.set $t (call $put (local.get $t) (i32.load offset=12 (local.get $r)) (i32.load offset=16 (local.get $r))))
    (local.set $w (call $rds (local.get $w) (local.get $tmp) (i32.sub (local.get $t) (local.get $tmp)))))
  (local.set $w (call $put_part (local.get $w) (i32.const '?') (local.get $r) (i32.const 20)))
  (local.set $w (call $put_part (local.get $w) (i32.const '#') (local.get $r) (i32.const 28)))
  (i32.sub (local.get $w) (global.get $sb)))

;; The IRI term for the reference [p, p+n): resolved against the base, which
;; an absolute IRI without dot segments needs no copying for.
(func $iri_ref (param $p i32) (param $n i32) (result i32)
  (local $i i32)
  (block $resolve
    (br_if $resolve (i32.and (i32.ne (global.get $base_n) (i32.const 0))
                             (i32.eqz (call $scheme_len (local.get $p) (local.get $n)))))
    (loop $dots
      (if (i32.lt_u (i32.add (local.get $i) (i32.const 1)) (local.get $n)) (then
        (br_if $resolve (i32.eq (i32.load16_u (i32.add (local.get $p) (local.get $i))) (i32.const 0x2e2f)))   ;; "/."
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $dots))))
    (return (call $intern (i32.const 1) (local.get $p) (local.get $n) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1))))
  (local.set $i (call $resolve (local.get $p) (local.get $n)))   ;; first: it may move $sb
  (call $intern (i32.const 1) (global.get $sb) (local.get $i) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))
