;; The quad store.
;;
;; Quads live densely in $quads, 32 bytes each:
;;   +0 s  +4 p  +8 o  +12 g    the ids
;;   +16 +20 +24 +28            the quad's position in each of its four lists
;; Every term has one list per position (s, p, o, g) of the quads using it
;; there: $post + id*16 holds four list pointers (0 = empty). A list block is
;;   +0 length  +4 capacity  +8 quad indexes
;; A hash set of quad indexes (+1; 0 = empty; open addressing, linear
;; probing, at most half full) answers add, remove and has in O(1) expected.
;;
;; match takes the shortest list among the bound positions, or every quad when
;; nothing is bound, and filters it. Removal is real and O(1): the quad leaves
;; its four lists by swapping in their last entry, the hash set by backward
;; shifting, and the table by moving the last quad into its slot.

(global $post (mut i32) (i32.const 0))
;; Where compiled queries find the store (see cscan.wat): +0 $post, +4 $quads,
;; +8 the default graphs, +12 their count (set when a compiled run begins),
;; +16 $terms; for typed expressions (ctyped.wat): +20 a string buffer, +24 its
;; size, +32 a number (i64 ni, +40 f64 nf, +48 i32 scale, +52 its type).
(global $bases i32 (@words32 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0))
(global $quads (mut i32) (i32.const 0))
(global $qcap (mut i32) (i32.const 0))   ;; in quads
(global $qn (mut i32) (i32.const 0))     ;; quads in the store
(global $qset (mut i32) (i32.const 0))
(global $qscap (mut i32) (i32.const 0))  ;; slots, a power of two

(func $post_of (param $id i32) (result i32)
  (i32.add (global.get $post) (i32.shl (local.get $id) (i32.const 4))))

(func $quad (param $q i32) (result i32)
  (i32.add (global.get $quads) (i32.shl (local.get $q) (i32.const 5))))

(func $qhash (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (call $fmix (call $mix (call $mix (call $mix (call $mix (i32.const 0) (local.get $s)) (local.get $p)) (local.get $o)) (local.get $g))))

;; The address of the set slot holding (s, p, o, g), or of the empty slot
;; where it would go.
(func $qfind (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (local $i i32) (local $mask i32) (local $slot i32) (local $q i32)
  (local.set $mask (i32.sub (global.get $qscap) (i32.const 1)))
  (local.set $i (call $qhash (local.get $s) (local.get $p) (local.get $o) (local.get $g)))
  (loop $probe
    (local.set $slot (i32.add (global.get $qset) (i32.shl (i32.and (local.get $i) (local.get $mask)) (i32.const 2))))
    (if (local.tee $q (i32.load (local.get $slot))) (then
      (local.set $q (call $quad (i32.sub (local.get $q) (i32.const 1))))
      (if (i32.eqz (i32.and (i32.and (i32.eq (i32.load (local.get $q)) (local.get $s))
                                     (i32.eq (i32.load offset=4 (local.get $q)) (local.get $p)))
                            (i32.and (i32.eq (i32.load offset=8 (local.get $q)) (local.get $o))
                                     (i32.eq (i32.load offset=12 (local.get $q)) (local.get $g)))))
        (then
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $probe))))))
  (local.get $slot))

;; Room for n quads in all: the table and the set (at most half full), grown
;; once rather than doubling along the way.
(func $quads_reserve (param $n i32)
  (local $cap i32)
  (if (i32.gt_u (local.get $n) (global.get $qcap)) (then
    (local.set $cap (global.get $qcap))
    (loop $double (if (i32.gt_u (local.get $n) (local.get $cap)) (then (local.set $cap (i32.shl (local.get $cap) (i32.const 1))) (br $double))))
    (global.set $quads (call $realloc (global.get $quads) (i32.shl (global.get $qcap) (i32.const 5))
      (i32.shl (local.get $cap) (i32.const 5)) (i32.shl (global.get $qn) (i32.const 5))))
    (i32.store offset=4 (global.get $bases) (global.get $quads))
    (global.set $qcap (local.get $cap))))
  (local.set $cap (global.get $qscap))
  (loop $double (if (i32.gt_u (i32.shl (local.get $n) (i32.const 1)) (local.get $cap)) (then (local.set $cap (i32.shl (local.get $cap) (i32.const 1))) (br $double))))
  (if (i32.gt_u (local.get $cap) (global.get $qscap)) (then (call $qset_grow (local.get $cap)))))

;; Rehash the set into cap slots.
(func $qset_grow (param $cap i32)
  (local $mask i32) (local $i i32) (local $at i32) (local $q i32)
  (call $free (global.get $qset) (i32.shl (global.get $qscap) (i32.const 2)))
  (global.set $qscap (local.get $cap))
  (global.set $qset (call $alloc0 (i32.shl (local.get $cap) (i32.const 2))))
  (local.set $mask (i32.sub (local.get $cap) (i32.const 1)))
  ;; every quad once, in table order: no key comparisons, only empty slots to find
  (loop $each
    (if (i32.lt_u (local.get $i) (global.get $qn)) (then
      (local.set $q (call $quad (local.get $i)))
      (local.set $at (call $qhash (i32.load (local.get $q)) (i32.load offset=4 (local.get $q))
                                  (i32.load offset=8 (local.get $q)) (i32.load offset=12 (local.get $q))))
      (loop $probe
        (if (i32.load (i32.add (global.get $qset) (i32.shl (i32.and (local.get $at) (local.get $mask)) (i32.const 2)))) (then
          (local.set $at (i32.add (local.get $at) (i32.const 1)))
          (br $probe))))
      (i32.store (i32.add (global.get $qset) (i32.shl (i32.and (local.get $at) (local.get $mask)) (i32.const 2))) (i32.add (local.get $i) (i32.const 1)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $each)))))

;; Empty the set slot at `slot`, shifting back the entries probed past it.
(func $qset_delete (param $slot i32)
  (local $i i32) (local $j i32) (local $mask i32) (local $v i32) (local $q i32) (local $home i32)
  (local.set $mask (i32.sub (global.get $qscap) (i32.const 1)))
  (local.set $i (i32.shr_u (i32.sub (local.get $slot) (global.get $qset)) (i32.const 2)))
  (local.set $j (local.get $i))
  (loop $scan
    (local.set $j (i32.and (i32.add (local.get $j) (i32.const 1)) (local.get $mask)))
    (if (local.tee $v (i32.load (i32.add (global.get $qset) (i32.shl (local.get $j) (i32.const 2))))) (then
      (local.set $q (call $quad (i32.sub (local.get $v) (i32.const 1))))
      (local.set $home (call $qhash (i32.load (local.get $q)) (i32.load offset=4 (local.get $q))
                                    (i32.load offset=8 (local.get $q)) (i32.load offset=12 (local.get $q))))
      ;; the entry may fill the hole at i when i lies cyclically in [home, j)
      (if (i32.ge_u (i32.and (i32.sub (local.get $j) (local.get $home)) (local.get $mask))
                    (i32.and (i32.sub (local.get $j) (local.get $i)) (local.get $mask)))
        (then
          (i32.store (i32.add (global.get $qset) (i32.shl (local.get $i) (i32.const 2))) (local.get $v))
          (local.set $i (local.get $j))))
      (br $scan))))
  (i32.store (i32.add (global.get $qset) (i32.shl (local.get $i) (i32.const 2))) (i32.const 0)))

;; Append quad q to the list at `at`; returns its position there.
(func $list_push (param $at i32) (param $q i32) (result i32)
  (local $l i32) (local $n i32) (local $cap i32)
  (if (i32.eqz (local.tee $l (i32.load (local.get $at)))) (then
    (local.set $l (call $alloc (i32.const 24)))
    (i32.store (local.get $l) (i32.const 0))
    (i32.store offset=4 (local.get $l) (i32.const 4))
    (i32.store (local.get $at) (local.get $l))))
  (local.set $n (i32.load (local.get $l)))
  (if (i32.eq (local.get $n) (local.tee $cap (i32.load offset=4 (local.get $l)))) (then
    (local.set $l (call $realloc (local.get $l)
      (i32.add (i32.const 8) (i32.shl (local.get $cap) (i32.const 2)))
      (i32.add (i32.const 8) (i32.shl (local.get $cap) (i32.const 3)))
      (i32.add (i32.const 8) (i32.shl (local.get $cap) (i32.const 2)))))
    (i32.store offset=4 (local.get $l) (i32.shl (local.get $cap) (i32.const 1)))
    (i32.store (local.get $at) (local.get $l))))
  (i32.store offset=8 (i32.add (local.get $l) (i32.shl (local.get $n) (i32.const 2))) (local.get $q))
  (i32.store (local.get $l) (i32.add (local.get $n) (i32.const 1)))
  (local.get $n))

;; Remove the entry at position pos of the list at `at` (a list of position
;; k = 0..3), moving its last entry there.
(func $list_delete (param $at i32) (param $pos i32) (param $k i32)
  (local $l i32) (local $n i32) (local $last i32)
  (local.set $l (i32.load (local.get $at)))
  (local.set $n (i32.sub (i32.load (local.get $l)) (i32.const 1)))
  (if (i32.eqz (local.get $n)) (then
    (call $free (local.get $l) (i32.add (i32.const 8) (i32.shl (i32.load offset=4 (local.get $l)) (i32.const 2))))
    (i32.store (local.get $at) (i32.const 0))
    (return)))
  (local.set $last (i32.load offset=8 (i32.add (local.get $l) (i32.shl (local.get $n) (i32.const 2)))))
  (i32.store offset=8 (i32.add (local.get $l) (i32.shl (local.get $pos) (i32.const 2))) (local.get $last))
  (i32.store offset=16 (i32.add (call $quad (local.get $last)) (i32.shl (local.get $k) (i32.const 2))) (local.get $pos))
  (i32.store (local.get $l) (local.get $n)))

(func $add (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (local $slot i32) (local $q i32) (local $r i32)
  (if (i32.load (local.tee $slot (call $qfind (local.get $s) (local.get $p) (local.get $o) (local.get $g))))
    (then (return (i32.const 0))))
  (local.set $q (global.get $qn))
  (if (i32.eq (local.get $q) (global.get $qcap)) (then
    (global.set $quads (call $realloc (global.get $quads) (i32.shl (local.get $q) (i32.const 5))
      (i32.shl (local.get $q) (i32.const 6)) (i32.shl (local.get $q) (i32.const 5))))
    (global.set $qcap (i32.shl (local.get $q) (i32.const 1)))
    (i32.store offset=4 (global.get $bases) (global.get $quads))))
  (local.set $r (call $quad (local.get $q)))
  (i32.store (local.get $r) (local.get $s))
  (i32.store offset=4 (local.get $r) (local.get $p))
  (i32.store offset=8 (local.get $r) (local.get $o))
  (i32.store offset=12 (local.get $r) (local.get $g))
  (i32.store offset=16 (local.get $r) (call $list_push (call $post_of (local.get $s)) (local.get $q)))
  (i32.store offset=20 (local.get $r) (call $list_push (i32.add (call $post_of (local.get $p)) (i32.const 4)) (local.get $q)))
  (i32.store offset=24 (local.get $r) (call $list_push (i32.add (call $post_of (local.get $o)) (i32.const 8)) (local.get $q)))
  (i32.store offset=28 (local.get $r) (call $list_push (i32.add (call $post_of (local.get $g)) (i32.const 12)) (local.get $q)))
  (i32.store (local.get $slot) (i32.add (local.get $q) (i32.const 1)))
  (global.set $qn (i32.add (local.get $q) (i32.const 1)))
  (if (i32.gt_u (i32.shl (global.get $qn) (i32.const 1)) (global.get $qscap)) (then (call $qset_grow (i32.shl (global.get $qscap) (i32.const 1)))))
  (i32.const 1))

(func $remove (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (local $slot i32) (local $q i32) (local $r i32) (local $last i32) (local $k i32) (local $at i32)
  (if (i32.eqz (local.tee $q (i32.load (local.tee $slot (call $qfind (local.get $s) (local.get $p) (local.get $o) (local.get $g))))))
    (then (return (i32.const 0))))
  (local.set $q (i32.sub (local.get $q) (i32.const 1)))
  (call $qset_delete (local.get $slot))
  (local.set $r (call $quad (local.get $q)))
  (loop $lists
    (local.set $at (i32.add (local.get $r) (i32.shl (local.get $k) (i32.const 2))))
    (call $list_delete (i32.add (call $post_of (i32.load (local.get $at))) (i32.shl (local.get $k) (i32.const 2)))
                       (i32.load offset=16 (local.get $at)) (local.get $k))
    (br_if $lists (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 4))))
  (global.set $qn (local.tee $last (i32.sub (global.get $qn) (i32.const 1))))
  (if (i32.ne (local.get $q) (local.get $last)) (then
    ;; move the last quad into the hole and repoint its list entries and set slot
    (local.set $last (call $quad (local.get $last)))
    (memory.copy (local.get $r) (local.get $last) (i32.const 32))
    (local.set $k (i32.const 0))
    (loop $lists
      (local.set $at (i32.add (local.get $r) (i32.shl (local.get $k) (i32.const 2))))
      (i32.store offset=8
        (i32.add (i32.load (i32.add (call $post_of (i32.load (local.get $at))) (i32.shl (local.get $k) (i32.const 2))))
                 (i32.shl (i32.load offset=16 (local.get $at)) (i32.const 2)))
        (local.get $q))
      (br_if $lists (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 4))))
    (i32.store (call $qfind (i32.load (local.get $r)) (i32.load offset=4 (local.get $r))
                            (i32.load offset=8 (local.get $r)) (i32.load offset=12 (local.get $r)))
               (i32.add (local.get $q) (i32.const 1)))))
  (i32.const 1))

(func $match (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (local $best i32) (local $n i32) (local $i i32) (local $q i32) (local $out i32) (local $l i32)
  ;; the shortest list among bound positions; a bound term without a list matches nothing
  (if (local.get $s) (then
    (if (i32.eqz (call $kind (local.get $s))) (then (return (i32.const 0))))
    (if (i32.eqz (local.tee $best (i32.load (call $post_of (local.get $s))))) (then (return (i32.const 0))))))
  (if (local.get $p) (then
    (if (i32.eqz (call $kind (local.get $p))) (then (return (i32.const 0))))
    (if (i32.eqz (local.tee $l (i32.load offset=4 (call $post_of (local.get $p))))) (then (return (i32.const 0))))
    (if (i32.or (i32.eqz (local.get $best)) (i32.lt_u (i32.load (local.get $l)) (i32.load (local.get $best))))
      (then (local.set $best (local.get $l))))))
  (if (local.get $o) (then
    (if (i32.eqz (call $kind (local.get $o))) (then (return (i32.const 0))))
    (if (i32.eqz (local.tee $l (i32.load offset=8 (call $post_of (local.get $o))))) (then (return (i32.const 0))))
    (if (i32.or (i32.eqz (local.get $best)) (i32.lt_u (i32.load (local.get $l)) (i32.load (local.get $best))))
      (then (local.set $best (local.get $l))))))
  (if (local.get $g) (then
    (if (i32.eqz (call $kind (local.get $g))) (then (return (i32.const 0))))
    (if (i32.eqz (local.tee $l (i32.load offset=12 (call $post_of (local.get $g))))) (then (return (i32.const 0))))
    (if (i32.or (i32.eqz (local.get $best)) (i32.lt_u (i32.load (local.get $l)) (i32.load (local.get $best))))
      (then (local.set $best (local.get $l))))))
  (local.set $n (if (result i32) (local.get $best) (then (i32.load (local.get $best))) (else (global.get $qn))))
  (call $res_reserve (i32.shl (local.get $n) (i32.const 4)))
  (local.set $out (global.get $res))
  (loop $each
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $q (call $quad
        (if (result i32) (local.get $best)
          (then (i32.load offset=8 (i32.add (local.get $best) (i32.shl (local.get $i) (i32.const 2)))))
          (else (local.get $i)))))
      (if (i32.and (i32.and (i32.or (i32.eqz (local.get $s)) (i32.eq (i32.load (local.get $q)) (local.get $s)))
                            (i32.or (i32.eqz (local.get $p)) (i32.eq (i32.load offset=4 (local.get $q)) (local.get $p))))
                   (i32.and (i32.or (i32.eqz (local.get $o)) (i32.eq (i32.load offset=8 (local.get $q)) (local.get $o)))
                            (i32.or (i32.eqz (local.get $g)) (i32.eq (i32.load offset=12 (local.get $q)) (local.get $g)))))
        (then
          (i64.store (local.get $out) (i64.load (local.get $q)))
          (i64.store offset=8 (local.get $out) (i64.load offset=8 (local.get $q)))
          (local.set $out (i32.add (local.get $out) (i32.const 16)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $each))))
  (i32.shr_u (i32.sub (local.get $out) (global.get $res)) (i32.const 4)))

(func $init_store
  (global.set $qcap (i32.const 64))
  (global.set $quads (call $alloc (i32.const 2048)))
  (i32.store offset=4 (global.get $bases) (global.get $quads))
  (global.set $qscap (i32.const 128))
  (global.set $qset (call $alloc0 (i32.const 512))))
