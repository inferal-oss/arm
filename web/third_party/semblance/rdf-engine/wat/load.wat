;; load: parse a document from the input buffer, then add its quads.
;;
;; Parsers push quads to a buffer, which is added to the store only once the
;; whole document has parsed, so a syntax error adds nothing. Blank node labels
;; and prefixes live in string maps that are emptied after each load.

(global $qb (mut i32) (i32.const 0))
(global $qb_cap (mut i32) (i32.const 0))  ;; bytes
(global $qb_len (mut i32) (i32.const 0))  ;; bytes

(func $push_quad (param $s i32) (param $p i32) (param $o i32) (param $g i32)
  (local $at i32)
  (if (i32.eq (global.get $qb_len) (global.get $qb_cap)) (then
    (global.set $qb (call $realloc (global.get $qb) (global.get $qb_cap)
      (i32.shl (global.get $qb_cap) (i32.const 1)) (global.get $qb_len)))
    (global.set $qb_cap (i32.shl (global.get $qb_cap) (i32.const 1)))))
  (local.set $at (i32.add (global.get $qb) (global.get $qb_len)))
  (i32.store (local.get $at) (local.get $s))
  (i32.store offset=4 (local.get $at) (local.get $p))
  (i32.store offset=8 (local.get $at) (local.get $o))
  (i32.store offset=12 (local.get $at) (local.get $g))
  (global.set $qb_len (i32.add (global.get $qb_len) (i32.const 16))))

(func $init_load
  (global.set $qb_cap (i32.const 256))
  (global.set $qb (call $alloc (i32.const 256)))
  (global.set $scope (call $alloc0 (i32.const 12)))
  (global.set $prefixes (call $alloc0 (i32.const 12)))
  (global.set $parts (call $alloc (i32.const 72))))

;; fmt: 1 N-Triples, 2 N-Quads, 3 Turtle, 4 TriG. The text is len bytes of the
;; input buffer, the base IRI the base_len bytes after it.
(func $load (param $fmt i32) (param $len i32) (param $base_len i32) (param $graph i32) (result i32)
  (local $n i32) (local $q i32) (local $end i32)
  (if (i32.eqz (local.get $graph)) (then (local.set $graph (i32.const 1))))
  (if (i32.eqz (call $kind (local.get $graph))) (then (return (call $fail (@str "graph is not a term")))))
  (global.set $p (global.get $in))
  (global.set $end (i32.add (global.get $in) (local.get $len)))
  (global.set $base_p (global.get $end))
  (global.set $base_n (local.get $base_len))
  (global.set $line (i32.const 1))
  (global.set $depth (i32.const 0))
  (global.set $qb_len (i32.const 0))
  (if (i32.le_u (local.get $fmt) (i32.const 2))
    (then (local.set $n (call $ntriples (local.get $fmt) (local.get $graph))))
    (else
      (global.set $tg (local.get $graph))
      (local.set $n (call $turtle (i32.eq (local.get $fmt) (i32.const 4))))
      (if (i32.ge_s (local.get $n) (i32.const 0)) (then (local.set $n (i32.shr_u (global.get $qb_len) (i32.const 4)))))))
  (call $smap_clear (global.get $scope))
  (call $smap_clear (global.get $prefixes))
  (if (i32.lt_s (local.get $n) (i32.const 0)) (then (return (i32.const -1))))
  (call $quads_reserve (i32.add (global.get $qn) (i32.shr_u (global.get $qb_len) (i32.const 4))))
  (local.set $q (global.get $qb))
  (local.set $end (i32.add (global.get $qb) (global.get $qb_len)))
  (loop $each
    (if (i32.lt_u (local.get $q) (local.get $end)) (then
      (drop (call $add (i32.load (local.get $q)) (i32.load offset=4 (local.get $q))
                       (i32.load offset=8 (local.get $q)) (i32.load offset=12 (local.get $q))))
      (local.set $q (i32.add (local.get $q) (i32.const 16)))
      (br $each))))
  (local.get $n))
