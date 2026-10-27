;; Query evaluation: graph patterns over tables of rows.
;;
;; A row is one u32 per variable slot ($W bytes), 0 = unbound. A table is
;; 16 bytes: +0 rows, +4 count, +8 capacity (rows). Every pattern maps an input
;; table to an output table: Join(input, pattern). Triple patterns use the
;; bindings of each input row to look quads up by index (most-bound pattern
;; first, chosen per row); nested groups receive only the variables they
;; certainly bind themselves (so their filters, BINDs and OPTIONALs see what the
;; spec says they see) and are joined back; parameters (bound variables) are
;; constants everywhere.

(global $W (mut i32) (i32.const 0))          ;; row width in bytes
(global $NV (mut i32) (i32.const 0))         ;; slots
(global $params (mut i32) (i32.const 0))     ;; parameter row
(global $ag (mut i32) (i32.const 0))         ;; active graph; 0: the default graph(s)
(global $dg (mut i32) (i32.const 0))         ;; default graph ids
(global $dgn (mut i32) (i32.const 0))        ;; and their count
(global $ng (mut i32) (i32.const 0))         ;; named graph ids (0: not computed yet)
(global $ngn (mut i32) (i32.const 0))
(global $ng_bytes (mut i32) (i32.const 0))
(global $ng_from (mut i32) (i32.const 0))    ;; FROM NAMED given
(global $epoch (mut i32) (i32.const 0))
(global $q (mut i32) (i32.const 0))          ;; the top-level query being run

;; --- tables --------------------------------------------------------------------

(func $t_new (result i32)
  (local $t i32)
  (local.set $t (call $alloc (i32.const 16)))
  (i32.store (local.get $t) (call $alloc (i32.shl (global.get $W) (i32.const 2))))
  (i32.store offset=4 (local.get $t) (i32.const 0))
  (i32.store offset=8 (local.get $t) (i32.const 4))
  (local.get $t))

(func $t_free (param $t i32)
  (call $free (i32.load (local.get $t)) (i32.mul (i32.load offset=8 (local.get $t)) (global.get $W)))
  (call $free (local.get $t) (i32.const 16)))

(func $t_row (param $t i32) (param $i i32) (result i32)
  (i32.add (i32.load (local.get $t)) (i32.mul (local.get $i) (global.get $W))))

;; Room for one more row: its address (contents undefined).
(func $t_add (param $t i32) (result i32)
  (local $n i32) (local $cap i32)
  (local.set $n (i32.load offset=4 (local.get $t)))
  (if (i32.eq (local.get $n) (local.tee $cap (i32.load offset=8 (local.get $t)))) (then
    (i32.store (local.get $t) (call $realloc (i32.load (local.get $t)) (i32.mul (local.get $cap) (global.get $W))
      (i32.mul (i32.shl (local.get $cap) (i32.const 1)) (global.get $W)) (i32.mul (local.get $cap) (global.get $W))))
    (i32.store offset=8 (local.get $t) (i32.shl (local.get $cap) (i32.const 1)))))
  (i32.store offset=4 (local.get $t) (i32.add (local.get $n) (i32.const 1)))
  (call $t_row (local.get $t) (local.get $n)))

(func $t_push (param $t i32) (param $row i32)
  (memory.copy (call $t_add (local.get $t)) (local.get $row) (global.get $W)))

;; A table of one row (a copy).
(func $t_one (param $row i32) (result i32)
  (local $t i32)
  (call $t_push (local.tee $t (call $t_new)) (local.get $row))
  (local.get $t))

(func $t_copy (param $t i32) (result i32)
  (local $c i32) (local $i i32)
  (local.set $c (call $t_new))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (call $t_push (local.get $c) (call $t_row (local.get $t) (local.get $i)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (local.get $c))

;; Scratch rows: a stack of $W-byte rows, for rows being built.
(global $rs_base (mut i32) (i32.const 0))
(global $rs_top (mut i32) (i32.const 0))
(global $rs_end (mut i32) (i32.const 0))

(func $row_get (result i32)
  (local $r i32)
  (if (i32.gt_u (i32.add (global.get $rs_top) (global.get $W)) (global.get $rs_end)) (then unreachable))
  (local.set $r (global.get $rs_top))
  (global.set $rs_top (i32.add (local.get $r) (global.get $W)))
  (local.get $r))

;; n scratch bytes on the same stack (release with $row_put).
(func $rs_alloc (param $n i32) (result i32)
  (local $r i32)
  (local.set $r (global.get $rs_top))
  (global.set $rs_top (i32.add (local.get $r) (i32.and (i32.add (local.get $n) (i32.const 7)) (i32.const -8))))
  (if (i32.gt_u (global.get $rs_top) (global.get $rs_end)) (then unreachable))
  (local.get $r))

(func $row_put (param $r i32)
  (global.set $rs_top (local.get $r)))

;; Whether rows a and b agree where both are bound (two slots at a time:
;; equal words need no look at their halves).
(func $compatible (param $a i32) (param $b i32) (result i32)
  (local $i i32) (local $x i64) (local $y i64)
  (loop $pairs
    (if (i32.le_u (i32.add (local.get $i) (i32.const 8)) (global.get $W)) (then
      (if (i64.ne (local.tee $x (i64.load (i32.add (local.get $a) (local.get $i)))) (local.tee $y (i64.load (i32.add (local.get $b) (local.get $i))))) (then
        (if (i32.eqz (call $agree (i32.wrap_i64 (local.get $x)) (i32.wrap_i64 (local.get $y)))) (then (return (i32.const 0))))
        (if (i32.eqz (call $agree (i32.wrap_i64 (i64.shr_u (local.get $x) (i64.const 32))) (i32.wrap_i64 (i64.shr_u (local.get $y) (i64.const 32)))))
          (then (return (i32.const 0))))))
      (local.set $i (i32.add (local.get $i) (i32.const 8)))
      (br $pairs))))
  (if (i32.lt_u (local.get $i) (global.get $W)) (then
    (return (call $agree (i32.load (i32.add (local.get $a) (local.get $i))) (i32.load (i32.add (local.get $b) (local.get $i)))))))
  (i32.const 1))

;; Whether slot values x and y agree: equal, or one unbound.
(func $agree (param $x i32) (param $y i32) (result i32)
  (i32.or (i32.or (i32.eqz (local.get $x)) (i32.eqz (local.get $y))) (i32.eq (local.get $x) (local.get $y))))

;; Push to t the merge of compatible rows a and b: where both are bound they
;; are equal, so the merge is their bitwise or (two slots at a time).
(func $push_merge (param $t i32) (param $a i32) (param $b i32)
  (local $i i32) (local $dst i32)
  (local.set $dst (call $t_add (local.get $t)))
  (loop $pairs
    (if (i32.le_u (i32.add (local.get $i) (i32.const 8)) (global.get $W)) (then
      (i64.store (i32.add (local.get $dst) (local.get $i))
        (i64.or (i64.load (i32.add (local.get $a) (local.get $i))) (i64.load (i32.add (local.get $b) (local.get $i)))))
      (local.set $i (i32.add (local.get $i) (i32.const 8)))
      (br $pairs))))
  (if (i32.lt_u (local.get $i) (global.get $W)) (then
    (i32.store (i32.add (local.get $dst) (local.get $i))
      (i32.or (i32.load (i32.add (local.get $a) (local.get $i))) (i32.load (i32.add (local.get $b) (local.get $i))))))))

(func $bit (param $bits i32) (param $s i32) (result i32)
  (i32.and (i32.shr_u (i32.load (i32.add (local.get $bits) (i32.shl (i32.shr_u (local.get $s) (i32.const 5)) (i32.const 2))))
                      (i32.and (local.get $s) (i32.const 31))) (i32.const 1)))

;; --- keyed sets ------------------------------------------------------------------
;; A set of fixed-width keys (k u32 words): 24 bytes: +0 keys, +4 count,
;; +8 key capacity, +12 slots (index + 1), +16 slot capacity, +20 k.
;; $set_add returns the key's index; $set_new tells whether it was added.

(global $set_new (mut i32) (i32.const 0))

(func $set_make (param $k i32) (result i32)
  (local $s i32)
  (local.set $s (call $alloc (i32.const 24)))
  (i32.store (local.get $s) (call $alloc (i32.shl (i32.shl (local.get $k) (i32.const 2)) (i32.const 4))))
  (i32.store offset=4 (local.get $s) (i32.const 0))
  (i32.store offset=8 (local.get $s) (i32.const 16))
  (i32.store offset=12 (local.get $s) (call $alloc0 (i32.const 128)))
  (i32.store offset=16 (local.get $s) (i32.const 32))
  (i32.store offset=20 (local.get $s) (local.get $k))
  (local.get $s))

(func $set_free (param $s i32)
  (call $free (i32.load (local.get $s)) (i32.shl (i32.mul (i32.load offset=8 (local.get $s)) (i32.load offset=20 (local.get $s))) (i32.const 2)))
  (call $free (i32.load offset=12 (local.get $s)) (i32.shl (i32.load offset=16 (local.get $s)) (i32.const 2)))
  (call $free (local.get $s) (i32.const 24)))

(func $set_key (param $s i32) (param $i i32) (result i32)
  (i32.add (i32.load (local.get $s)) (i32.shl (i32.mul (local.get $i) (i32.load offset=20 (local.get $s))) (i32.const 2))))

(func $set_slot (param $s i32) (param $key i32) (result i32)
  (local $h i32) (local $mask i32) (local $at i32) (local $v i32) (local $kb i32)
  (local.set $kb (i32.shl (i32.load offset=20 (local.get $s)) (i32.const 2)))
  (local.set $h (call $fmix (call $hash_bytes (i32.const 7) (local.get $key) (local.get $kb))))
  (local.set $mask (i32.sub (i32.load offset=16 (local.get $s)) (i32.const 1)))
  (loop $probe
    (local.set $at (i32.add (i32.load offset=12 (local.get $s)) (i32.shl (i32.and (local.get $h) (local.get $mask)) (i32.const 2))))
    (if (local.tee $v (i32.load (local.get $at))) (then
      (if (i32.eqz (call $memeq (call $set_key (local.get $s) (i32.sub (local.get $v) (i32.const 1))) (local.get $key) (local.get $kb))) (then
        (local.set $h (i32.add (local.get $h) (i32.const 1)))
        (br $probe))))))
  (local.get $at))

(func $set_add (param $s i32) (param $key i32) (result i32)
  (local $at i32) (local $n i32) (local $kb i32) (local $cap i32) (local $old i32) (local $i i32)
  (if (local.tee $n (i32.load (local.tee $at (call $set_slot (local.get $s) (local.get $key))))) (then
    (global.set $set_new (i32.const 0))
    (return (i32.sub (local.get $n) (i32.const 1)))))
  (global.set $set_new (i32.const 1))
  (local.set $kb (i32.shl (i32.load offset=20 (local.get $s)) (i32.const 2)))
  (local.set $n (i32.load offset=4 (local.get $s)))
  (if (i32.eq (local.get $n) (local.tee $cap (i32.load offset=8 (local.get $s)))) (then
    (i32.store (local.get $s) (call $realloc (i32.load (local.get $s)) (i32.mul (local.get $cap) (local.get $kb))
      (i32.mul (i32.shl (local.get $cap) (i32.const 1)) (local.get $kb)) (i32.mul (local.get $cap) (local.get $kb))))
    (i32.store offset=8 (local.get $s) (i32.shl (local.get $cap) (i32.const 1)))))
  (memory.copy (call $set_key (local.get $s) (local.get $n)) (local.get $key) (local.get $kb))
  (i32.store (local.get $at) (i32.add (local.get $n) (i32.const 1)))
  (i32.store offset=4 (local.get $s) (i32.add (local.get $n) (i32.const 1)))
  (if (i32.gt_u (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 1)) (i32.load offset=16 (local.get $s))) (then
    ;; double the slots and re-insert every key
    (local.set $old (i32.load offset=12 (local.get $s)))
    (local.set $cap (i32.load offset=16 (local.get $s)))
    (i32.store offset=16 (local.get $s) (i32.shl (local.get $cap) (i32.const 1)))
    (i32.store offset=12 (local.get $s) (call $alloc0 (i32.shl (local.get $cap) (i32.const 3))))
    (call $free (local.get $old) (i32.shl (local.get $cap) (i32.const 2)))
    (loop $keys
      (if (i32.le_u (local.get $i) (local.get $n)) (then
        (i32.store (call $set_slot (local.get $s) (call $set_key (local.get $s) (local.get $i))) (i32.add (local.get $i) (i32.const 1)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $keys))))))
  (local.get $n))

;; Add one id; whether it was new.
(func $set_add1 (param $s i32) (param $id i32) (result i32)
  (local $k i32)
  (local.set $k (call $row_get))
  (i32.store (local.get $k) (local.get $id))
  (drop (call $set_add (local.get $s) (local.get $k)))
  (call $row_put (local.get $k))
  (global.get $set_new))

;; --- vectors of u32 (pairs for paths) ---------------------------------------------
;; 12 bytes: +0 data, +4 length (words), +8 capacity.

(func $v_new (result i32)
  (local $v i32)
  (local.set $v (call $alloc (i32.const 12)))
  (i32.store (local.get $v) (call $alloc (i32.const 64)))
  (i32.store offset=4 (local.get $v) (i32.const 0))
  (i32.store offset=8 (local.get $v) (i32.const 16))
  (local.get $v))

(func $v_free (param $v i32)
  (call $free (i32.load (local.get $v)) (i32.shl (i32.load offset=8 (local.get $v)) (i32.const 2)))
  (call $free (local.get $v) (i32.const 12)))

(func $v_push2 (param $v i32) (param $a i32) (param $b i32)
  (local $n i32) (local $cap i32)
  (local.set $n (i32.load offset=4 (local.get $v)))
  (if (i32.ge_u (i32.add (local.get $n) (i32.const 2)) (local.tee $cap (i32.load offset=8 (local.get $v)))) (then
    (i32.store (local.get $v) (call $realloc (i32.load (local.get $v)) (i32.shl (local.get $cap) (i32.const 2))
      (i32.shl (local.get $cap) (i32.const 3)) (i32.shl (local.get $n) (i32.const 2))))
    (i32.store offset=8 (local.get $v) (i32.shl (local.get $cap) (i32.const 1)))))
  (i32.store (i32.add (i32.load (local.get $v)) (i32.shl (local.get $n) (i32.const 2))) (local.get $a))
  (i32.store offset=4 (i32.add (i32.load (local.get $v)) (i32.shl (local.get $n) (i32.const 2))) (local.get $b))
  (i32.store offset=4 (local.get $v) (i32.add (local.get $n) (i32.const 2))))

(func $v_push1 (param $v i32) (param $a i32)
  (call $v_push2 (local.get $v) (local.get $a) (i32.const 0))
  (i32.store offset=4 (local.get $v) (i32.sub (i32.load offset=4 (local.get $v)) (i32.const 1))))

(func $v_at (param $v i32) (param $i i32) (result i32)
  (i32.load (i32.add (i32.load (local.get $v)) (i32.shl (local.get $i) (i32.const 2)))))

;; --- quads -----------------------------------------------------------------------

;; Candidate quads for (s, p, o, g), 0 = any: sets $sc_list (a store list, or 0
;; for the whole table) and $sc_n. A bound term without quads gives none.
(global $sc_list (mut i32) (i32.const 0))
(global $sc_n (mut i32) (i32.const 0))

(func $scan (param $s i32) (param $p i32) (param $o i32) (param $g i32)
  (local $best i32) (local $l i32) (local $k i32) (local $id i32)
  (global.set $sc_n (i32.const 0))
  (global.set $sc_list (i32.const 0))
  (loop $positions
    (local.set $id (select (local.get $s) (select (local.get $p) (select (local.get $o) (local.get $g) (i32.eq (local.get $k) (i32.const 2))) (i32.eq (local.get $k) (i32.const 1))) (i32.eqz (local.get $k))))
    (if (local.get $id) (then
      (if (i32.eqz (call $kind (local.get $id))) (then (return)))
      (if (i32.eqz (local.tee $l (i32.load (i32.add (call $post_of (local.get $id)) (i32.shl (local.get $k) (i32.const 2)))))) (then (return)))
      (if (i32.or (i32.eqz (local.get $best)) (i32.lt_u (i32.load (local.get $l)) (i32.load (local.get $best))))
        (then (local.set $best (local.get $l))))))
    (br_if $positions (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 4))))
  (global.set $sc_list (local.get $best))
  (global.set $sc_n (if (result i32) (local.get $best) (then (i32.load (local.get $best))) (else (global.get $qn)))))

;; The i-th candidate quad record.
(func $sc_quad (param $i i32) (result i32)
  (call $list_quad (global.get $sc_list) (local.get $i)))

;; The i-th quad of store list l (0: of the whole table).
(func $list_quad (param $l i32) (param $i i32) (result i32)
  (call $quad (if (result i32) (local.get $l)
    (then (i32.load offset=8 (i32.add (local.get $l) (i32.shl (local.get $i) (i32.const 2)))))
    (else (local.get $i)))))

;; Whether quad record r matches (s, p, o) with 0 as any.
(func $quad_is (param $r i32) (param $s i32) (param $p i32) (param $o i32) (result i32)
  (i32.and (i32.and (i32.or (i32.eqz (local.get $s)) (i32.eq (i32.load (local.get $r)) (local.get $s)))
                    (i32.or (i32.eqz (local.get $p)) (i32.eq (i32.load offset=4 (local.get $r)) (local.get $p))))
           (i32.or (i32.eqz (local.get $o)) (i32.eq (i32.load offset=8 (local.get $r)) (local.get $o)))))

;; The graphs a pattern reads: the active graph, or the default graphs.
(func $graph_count (result i32)
  (if (result i32) (global.get $ag) (then (i32.const 1)) (else (global.get $dgn))))

(func $graph_at (param $i i32) (result i32)
  (if (result i32) (global.get $ag) (then (global.get $ag))
    (else (i32.load (i32.add (global.get $dg) (i32.shl (local.get $i) (i32.const 2)))))))

;; Whether (s, p, o) is in one of the first i graphs read (for the union of
;; several default graphs, which counts each triple once).
(func $seen_before (param $i i32) (param $r i32) (result i32)
  (local $j i32)
  (loop $graphs
    (if (i32.lt_u (local.get $j) (local.get $i)) (then
      (if (i32.load (call $qfind (i32.load (local.get $r)) (i32.load offset=4 (local.get $r)) (i32.load offset=8 (local.get $r)) (call $graph_at (local.get $j))))
        (then (return (i32.const 1))))
      (local.set $j (i32.add (local.get $j) (i32.const 1)))
      (br $graphs))))
  (i32.const 0))

;; Named graphs: FROM NAMED, or every graph with quads but the default graph.
(func $named_graphs
  (local $id i32) (local $n i32)
  (if (global.get $ng) (then (return)))
  (global.set $ng_bytes (i32.shl (global.get $next_id) (i32.const 2)))
  (global.set $ng (call $alloc (global.get $ng_bytes)))
  (local.set $id (i32.const 2))
  (loop $terms
    (if (i32.lt_u (local.get $id) (global.get $next_id)) (then
      (if (i32.load offset=12 (call $post_of (local.get $id))) (then
        (i32.store (i32.add (global.get $ng) (i32.shl (local.get $n) (i32.const 2))) (local.get $id))
        (local.set $n (i32.add (local.get $n) (i32.const 1)))))
      (local.set $id (i32.add (local.get $id) (i32.const 1)))
      (br $terms))))
  (global.set $ngn (local.get $n)))

;; --- refs ------------------------------------------------------------------------

;; The value of ref in row: a term, 0 if unbound, -1 if it cannot match.
(func $resolve_ref (param $ref i32) (param $row i32) (result i32)
  (local $n i32) (local $s i32) (local $p i32) (local $o i32)
  (if (i32.gt_s (local.get $ref) (i32.const 0)) (then (return (local.get $ref))))
  (if (i32.gt_s (local.get $ref) (i32.const -0x100000))
    (then (return (i32.load (i32.add (local.get $row) (i32.shl (i32.sub (i32.const -1) (local.get $ref)) (i32.const 2)))))))
  (local.set $n (i32.and (local.get $ref) (i32.const 0x7fffffff)))
  (if (i32.le_s (local.tee $s (call $resolve_ref (i32.load offset=4 (local.get $n)) (local.get $row))) (i32.const 0)) (then (return (local.get $s))))
  (if (i32.le_s (local.tee $p (call $resolve_ref (i32.load offset=8 (local.get $n)) (local.get $row))) (i32.const 0)) (then (return (local.get $p))))
  (if (i32.le_s (local.tee $o (call $resolve_ref (i32.load offset=12 (local.get $n)) (local.get $row))) (i32.const 0)) (then (return (local.get $o))))
  (select (local.tee $n (call $intern_triple (local.get $s) (local.get $p) (local.get $o) (i32.const 0))) (i32.const -1) (local.get $n)))

;; Match ref against term v, binding variables in row: whether it matched.
(func $unify (param $ref i32) (param $v i32) (param $row i32) (result i32)
  (local $at i32) (local $cur i32) (local $n i32) (local $r i32)
  (if (i32.gt_s (local.get $ref) (i32.const 0)) (then (return (i32.eq (local.get $ref) (local.get $v)))))
  (if (i32.gt_s (local.get $ref) (i32.const -0x100000)) (then
    (local.set $at (i32.add (local.get $row) (i32.shl (i32.sub (i32.const -1) (local.get $ref)) (i32.const 2))))
    (if (local.tee $cur (i32.load (local.get $at))) (then (return (i32.eq (local.get $cur) (local.get $v)))))
    (i32.store (local.get $at) (local.get $v))
    (return (i32.const 1))))
  (if (i32.ne (call $kind (local.get $v)) (i32.const 4)) (then (return (i32.const 0))))
  (local.set $n (i32.and (local.get $ref) (i32.const 0x7fffffff)))
  (local.set $r (call $rec (local.get $v)))
  (i32.and (i32.and (call $unify (i32.load offset=4 (local.get $n)) (i32.load offset=8 (local.get $r)) (local.get $row))
                    (call $unify (i32.load offset=8 (local.get $n)) (i32.load offset=12 (local.get $r)) (local.get $row)))
           (call $unify (i32.load offset=12 (local.get $n)) (i32.load offset=16 (local.get $r)) (local.get $row))))

;; --- basic graph patterns ----------------------------------------------------------

(global $bg_pats (mut i32) (i32.const 0))   ;; pattern pointers
(global $bg_done (mut i32) (i32.const 0))   ;; a byte per pattern
(global $bg_n (mut i32) (i32.const 0))
(global $bg_out (mut i32) (i32.const 0))

;; The consecutive triple patterns from el (n of them) joined with table t.
(func $eval_bgp (param $el i32) (param $n i32) (param $t i32) (result i32)
  (local $i i32) (local $row i32) (local $save_p i32) (local $save_d i32) (local $save_n i32) (local $save_o i32)
  (local.set $save_p (global.get $bg_pats))
  (local.set $save_d (global.get $bg_done))
  (local.set $save_n (global.get $bg_n))
  (local.set $save_o (global.get $bg_out))
  (global.set $bg_pats (call $alloc (i32.shl (local.get $n) (i32.const 2))))
  (global.set $bg_done (call $alloc0 (local.get $n)))
  (global.set $bg_n (local.get $n))
  (global.set $bg_out (call $t_new))
  (loop $pats
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (i32.store (i32.add (global.get $bg_pats) (i32.shl (local.get $i) (i32.const 2))) (local.get $el))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $pats))))
  (local.set $row (call $row_get))
  (local.set $i (i32.const 0))
  (loop $rows
    (if (i32.and (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (i32.eqz (global.get $bu_stop))) (then
      (memory.copy (local.get $row) (call $t_row (local.get $t) (local.get $i)) (global.get $W))
      (call $bgp_dfs (local.get $row) (local.get $n))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (call $row_put (local.get $row))
  (call $free (global.get $bg_pats) (i32.shl (local.get $n) (i32.const 2)))
  (call $free (global.get $bg_done) (local.get $n))
  (local.set $row (global.get $bg_out))
  (global.set $bg_pats (local.get $save_p))
  (global.set $bg_done (local.get $save_d))
  (global.set $bg_n (local.get $save_n))
  (global.set $bg_out (local.get $save_o))
  (local.get $row))

;; How well bound pattern pt is in row, to pick the next pattern: 2 per bound
;; position of a triple pattern; a path (whose predicate is fixed) scores as a
;; triple with its bound ends and predicate, less one, so a triple wins a tie
;; (-1: it cannot match).
(func $bound_count (param $pt i32) (param $row i32) (result i32)
  (local $k i32) (local $n i32) (local $v i32) (local $path i32)
  (local.set $path (i32.eq (i32.load (local.get $pt)) (i32.const P_PATH)))
  (loop $positions
    (if (i32.eqz (i32.and (local.get $path) (i32.eq (local.get $k) (i32.const 1)))) (then
      (local.set $v (call $resolve_ref (i32.load (i32.add (local.get $pt) (i32.shl (i32.add (local.get $k) (i32.const 1)) (i32.const 2)))) (local.get $row)))
      (if (i32.lt_s (local.get $v) (i32.const 0)) (then (return (i32.const -1))))
      (local.set $n (i32.add (local.get $n) (i32.ne (local.get $v) (i32.const 0))))))
    (br_if $positions (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 3))))
  (i32.sub (i32.shl (i32.add (local.get $n) (local.get $path)) (i32.const 1)) (local.get $path)))

;; Match the `left` remaining patterns, most bound first, extending row; row
;; is as it was on return.
(func $bgp_dfs (param $row i32) (param $left i32)
  (local $l i32) (local $ln i32) (local $i i32) (local $best i32) (local $score i32) (local $c i32) (local $pt i32)
  (local $s i32) (local $p i32) (local $o i32) (local $gi i32) (local $gn i32) (local $j i32) (local $r i32) (local $snap i32)
  (local $g i32) (local $ok i32)
  (if (i32.eqz (local.get $left)) (then (call $t_push (global.get $bg_out) (local.get $row)) (return)))
  (local.set $best (i32.const -1))
  (local.set $score (i32.const -1))
  (loop $choose
    (if (i32.lt_u (local.get $i) (global.get $bg_n)) (then
      (if (i32.eqz (i32.load8_u (i32.add (global.get $bg_done) (local.get $i)))) (then
        (local.set $c (call $bound_count (i32.load (i32.add (global.get $bg_pats) (i32.shl (local.get $i) (i32.const 2)))) (local.get $row)))
        (if (i32.lt_s (local.get $c) (i32.const 0)) (then (return)))
        (if (i32.gt_s (local.get $c) (local.get $score)) (then
          (local.set $score (local.get $c))
          (local.set $best (local.get $i))))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $choose))))
  (local.set $pt (i32.load (i32.add (global.get $bg_pats) (i32.shl (local.get $best) (i32.const 2)))))
  (i32.store8 (i32.add (global.get $bg_done) (local.get $best)) (i32.const 1))
  (local.set $s (call $resolve_ref (i32.load offset=4 (local.get $pt)) (local.get $row)))
  (local.set $o (call $resolve_ref (i32.load offset=12 (local.get $pt)) (local.get $row)))
  ;; a candidate binds the unbound positions: plain variables are unbound again
  ;; after it; a triple term pattern may bind more, so the row is copied back
  (local.set $snap (call $rs_alloc (i32.add (global.get $W) (i32.const 8))))
  (if (i32.eq (i32.load (local.get $pt)) (i32.const P_PATH)) (then
    (local.set $p (i32.const 1))
    ;; a path: its pairs from the bound ends (see $eval_path)
    (local.set $l (call $v_new))
    (global.set $zero_check (i32.or (i32.and (i32.lt_s (i32.load offset=4 (local.get $pt)) (i32.const 0)) (i32.ne (local.get $s) (i32.const 0)))
                                    (i32.and (i32.lt_s (i32.load offset=12 (local.get $pt)) (i32.const 0)) (i32.ne (local.get $o) (i32.const 0)))))
    (call $pp (i32.load offset=8 (local.get $pt)) (local.get $s) (local.get $o) (local.get $l))
    (global.set $zero_check (i32.const 0))
    (local.set $snap (call $undo_plan (local.get $pt) (local.get $row) (local.get $s) (local.get $p) (local.get $o) (local.get $snap)))
    (loop $pairs
      (if (if (result i32) (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $l))) (then (i32.eqz (call $step))) (else (i32.const 0))) (then
        (if (i32.and (call $unify (i32.load offset=4 (local.get $pt)) (call $v_at (local.get $l) (local.get $j)) (local.get $row))
                     (call $unify (i32.load offset=12 (local.get $pt)) (call $v_at (local.get $l) (i32.add (local.get $j) (i32.const 1))) (local.get $row)))
          (then (call $bgp_dfs (local.get $row) (i32.sub (local.get $left) (i32.const 1)))))
        (call $row_undo (local.get $row) (local.get $snap))
        (local.set $j (i32.add (local.get $j) (i32.const 2)))
        (br $pairs))))
    (call $v_free (local.get $l))
    (call $row_put (local.get $snap))
    (i32.store8 (i32.add (global.get $bg_done) (local.get $best)) (i32.const 0))
    (return)))
  (local.set $p (call $resolve_ref (i32.load offset=8 (local.get $pt)) (local.get $row)))
  (local.set $snap (call $undo_plan (local.get $pt) (local.get $row) (local.get $s) (local.get $p) (local.get $o) (local.get $snap)))
  (local.set $gn (call $graph_count))
  (loop $graphs
    (if (i32.lt_u (local.get $gi) (local.get $gn)) (then
      (local.set $g (call $graph_at (local.get $gi)))
      (call $scan (local.get $s) (local.get $p) (local.get $o) (local.get $g))
      ;; the recursion below scans too: keep this scan's list
      (local.set $l (global.get $sc_list))
      (local.set $ln (global.get $sc_n))
      (local.set $j (i32.const 0))
      (loop $quads
        (if (if (result i32) (i32.lt_u (local.get $j) (local.get $ln)) (then (i32.eqz (call $step))) (else (i32.const 0))) (then
          (local.set $r (call $list_quad (local.get $l) (local.get $j)))
          (if (i32.and (call $quad_is (local.get $r) (local.get $s) (local.get $p) (local.get $o))
                       (i32.eq (i32.load offset=12 (local.get $r)) (local.get $g))) (then
            (if (i32.eqz (if (result i32) (local.get $gi) (then (call $seen_before (local.get $gi) (local.get $r))) (else (i32.const 0)))) (then
              ;; bound positions matched already
              (local.set $ok (i32.const 1))
              (if (i32.eqz (local.get $s)) (then (local.set $ok (call $unify (i32.load offset=4 (local.get $pt)) (i32.load (local.get $r)) (local.get $row)))))
              (if (i32.and (local.get $ok) (i32.eqz (local.get $p))) (then (local.set $ok (call $unify (i32.load offset=8 (local.get $pt)) (i32.load offset=4 (local.get $r)) (local.get $row)))))
              (if (i32.and (local.get $ok) (i32.eqz (local.get $o))) (then (local.set $ok (call $unify (i32.load offset=12 (local.get $pt)) (i32.load offset=8 (local.get $r)) (local.get $row)))))
              (if (local.get $ok) (then (call $bgp_dfs (local.get $row) (i32.sub (local.get $left) (i32.const 1)))))
              (call $row_undo (local.get $row) (local.get $snap))))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $quads))))
      (local.set $gi (i32.add (local.get $gi) (i32.const 1)))
      (br $graphs))))
  (call $row_put (local.get $snap))
  (i32.store8 (i32.add (global.get $bg_done) (local.get $best)) (i32.const 0)))

;; How to put row back after a candidate for pattern pt with positions s, p, o
;; (0: unbound) binds, written to scratch u (W + 8 bytes): 0 and the slot
;; offsets to clear (ended by -1), or 1 and a copy of row to copy back.
(func $undo_plan (param $pt i32) (param $row i32) (param $s i32) (param $p i32) (param $o i32) (param $u i32) (result i32)
  (local $k i32) (local $ref i32) (local $w i32)
  (local.set $w (i32.add (local.get $u) (i32.const 4)))
  (loop $positions
    (if (i32.eqz (select (local.get $s) (select (local.get $p) (local.get $o) (i32.eq (local.get $k) (i32.const 1))) (i32.eqz (local.get $k)))) (then
      (local.set $ref (i32.load (i32.add (local.get $pt) (i32.shl (i32.add (local.get $k) (i32.const 1)) (i32.const 2)))))
      (if (i32.and (i32.lt_s (local.get $ref) (i32.const 0)) (i32.gt_s (local.get $ref) (i32.const -0x100000)))
        (then
          (i32.store (local.get $w) (i32.shl (i32.sub (i32.const -1) (local.get $ref)) (i32.const 2)))
          (local.set $w (i32.add (local.get $w) (i32.const 4))))
        (else
          ;; a triple term pattern
          (i32.store (local.get $u) (i32.const 1))
          (memory.copy (i32.add (local.get $u) (i32.const 4)) (local.get $row) (global.get $W))
          (return (local.get $u))))))
    (br_if $positions (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 3))))
  (i32.store (local.get $u) (i32.const 0))
  (i32.store (local.get $w) (i32.const -1))
  (local.get $u))

(func $row_undo (param $row i32) (param $u i32)
  (local $at i32)
  (if (i32.load (local.get $u)) (then
    (memory.copy (local.get $row) (i32.add (local.get $u) (i32.const 4)) (global.get $W))
    (return)))
  (loop $slots
    (if (i32.ge_s (local.tee $at (i32.load offset=4 (local.get $u))) (i32.const 0)) (then
      (i32.store (i32.add (local.get $row) (local.get $at)) (i32.const 0))
      (local.set $u (i32.add (local.get $u) (i32.const 4)))
      (br $slots)))))

;; --- groups ----------------------------------------------------------------------

;; Whether element el is a triple or path pattern.
(func $is_tp (param $el i32) (result i32)
  (i32.le_u (i32.sub (i32.load (local.get $el)) (i32.const P_TRIPLE)) (i32.const 1)))

;; Group g joined with table t (not freed): a new table. With nf, the group's
;; own filters are left to the caller (OPTIONAL's condition).
(func $eval_group (param $g i32) (param $t i32) (param $nf i32) (result i32)
  (local $el i32) (local $cur i32) (local $next i32) (local $n i32) (local $e i32) (local $applied i32)
  (local.set $cur (local.get $t))
  (local.set $el (i32.load offset=4 (local.get $g)))
  ;; filters on what the input binds already, before any element
  (if (i32.eqz (local.get $nf)) (then
    (if (call $early_ready (local.get $g) (local.get $t)) (then
      (local.set $cur (call $t_copy (local.get $t)))
      (local.set $applied (call $early_filters (local.get $g) (local.get $cur) (i32.const 0)))))))
  (block $done
    (loop $elements
      (br_if $done (i32.eqz (local.get $el)))
      (br_if $done (i32.eqz (i32.load offset=4 (local.get $cur))))
      (br_if $done (global.get $bu_stop))
      (if (call $is_tp (local.get $el))
        (then
          ;; a basic graph pattern: the run of triple and path patterns
          (local.set $n (i32.const 0))
          (local.set $e (local.get $el))
          (loop $run
            (if (local.get $e) (then
              (if (call $is_tp (local.get $e)) (then
                (local.set $n (i32.add (local.get $n) (i32.const 1)))
                (local.set $e (i32.load offset=28 (local.get $e)))
                (br $run))))))
          (local.set $next (call $eval_bgp (local.get $el) (local.get $n) (local.get $cur)))
          (local.set $el (local.get $e)))
        (else
          (if (i32.and (i32.eq (i32.load (local.get $el)) (i32.const P_BIND)) (i32.ne (local.get $cur) (local.get $t)))
            (then
              ;; BIND on a table of our own: in place
              (call $bind_rows (local.get $el) (local.get $cur))
              (local.set $next (local.get $cur)))
            (else (local.set $next (call $eval_elem (local.get $el) (local.get $cur)))))
          (local.set $el (i32.load offset=28 (local.get $el)))))
      (if (i32.and (i32.ne (local.get $cur) (local.get $t)) (i32.ne (local.get $cur) (local.get $next))) (then (call $t_free (local.get $cur))))
      (local.set $cur (local.get $next))
      ;; a filter whose variables every row binds holds the same now as at the
      ;; end (later elements keep the values rows have): apply it early
      (if (i32.and (i32.eqz (local.get $nf)) (i32.ne (local.get $cur) (local.get $t)))
        (then (local.set $applied (call $early_filters (local.get $g) (local.get $cur) (local.get $applied)))))
      (br $elements)))
  (if (i32.eq (local.get $cur) (local.get $t)) (then (local.set $cur (call $t_copy (local.get $t)))))
  (if (i32.eqz (local.get $nf)) (then (call $filter_mask (local.get $cur) (i32.load offset=8 (local.get $g)) (local.get $applied))))
  (local.get $cur))

;; Apply to t the filters of group g not yet applied (bits of applied, by
;; position) that t's rows decide; returns the new applied.
(func $early_filters (param $g i32) (param $t i32) (param $applied i32) (result i32)
  (local $i i32) (local $bit i32) (local $f i32)
  (local.set $f (i32.load offset=8 (local.get $g)))
  (loop $filters
    (if (i32.and (i32.ne (local.get $f) (i32.const 0)) (i32.lt_u (local.get $i) (i32.const 32))) (then
      (local.set $bit (i32.shl (i32.const 1) (local.get $i)))
      (if (i32.and (i32.eqz (i32.and (local.get $applied) (local.get $bit))) (i32.ne (i32.load offset=8 (local.get $f)) (i32.const 0))) (then
        (if (call $decided (local.get $t) (i32.load offset=8 (local.get $f)) (i32.load offset=24 (local.get $g))) (then
          (call $filter_one (local.get $t) (i32.load offset=4 (local.get $f)))
          (local.set $applied (i32.or (local.get $applied) (local.get $bit)))))))
      (local.set $f (i32.load offset=28 (local.get $f)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $filters))))
  (local.get $applied))

;; Whether some filter of group g could apply early to t.
(func $early_ready (param $g i32) (param $t i32) (result i32)
  (local $f i32)
  (local.set $f (i32.load offset=8 (local.get $g)))
  (loop $filters
    (if (local.get $f) (then
      (if (i32.load offset=8 (local.get $f)) (then
        (if (call $decided (local.get $t) (i32.load offset=8 (local.get $f)) (i32.load offset=24 (local.get $g))) (then (return (i32.const 1))))))
      (local.set $f (i32.load offset=28 (local.get $f)))
      (br $filters))))
  (i32.const 0))

;; Keep the rows of t for which expression e holds.
(func $filter_one (param $t i32) (param $e i32)
  (local $i i32) (local $k i32) (local $row i32)
  (loop $rows
    (if (i32.and (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (i32.eqz (global.get $bu_stop))) (then
      (local.set $row (call $t_row (local.get $t) (local.get $i)))
      (if (i32.eq (call $ebv (call $ev (local.get $e) (local.get $row))) (i32.const 1)) (then
        (if (i32.ne (local.get $i) (local.get $k)) (then (memory.copy (call $t_row (local.get $t) (local.get $k)) (local.get $row) (global.get $W))))
        (local.set $k (i32.add (local.get $k) (i32.const 1)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (i32.store offset=4 (local.get $t) (local.get $k)))

;; Whether the rows of t decide each variable of list vars (count, slots) as
;; the group's end will: every row binds it (later elements keep values), or
;; none does and the group's elements (bitset m) never mention it. The first
;; row rules most filters out.
(func $decided (param $t i32) (param $vars i32) (param $m i32) (result i32)
  (local $i i32) (local $j i32) (local $s i32) (local $b i32) (local $n i32) (local $first i32)
  (if (i32.eqz (local.get $m)) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $n (i32.load offset=4 (local.get $t)))) (then (return (i32.const 1))))
  (local.set $first (call $t_row (local.get $t) (i32.const 0)))
  (loop $vars
    (if (i32.lt_u (local.get $j) (i32.load (local.get $vars))) (then
      (local.set $s (i32.load offset=4 (i32.add (local.get $vars) (i32.shl (local.get $j) (i32.const 2)))))
      (if (i32.eqz (i32.load (i32.add (local.get $first) (i32.shl (local.get $s) (i32.const 2))))) (then
        (if (call $bit (local.get $m) (local.get $s)) (then (return (i32.const 0))))))
      (local.set $j (i32.add (local.get $j) (i32.const 1)))
      (br $vars))))
  ;; the other rows, like the first
  (local.set $j (i32.const 0))
  (loop $vars
    (if (i32.lt_u (local.get $j) (i32.load (local.get $vars))) (then
      (local.set $s (i32.shl (i32.load offset=4 (i32.add (local.get $vars) (i32.shl (local.get $j) (i32.const 2)))) (i32.const 2)))
      (local.set $b (i32.ne (i32.load (i32.add (local.get $first) (local.get $s))) (i32.const 0)))
      (local.set $i (i32.const 1))
      (loop $rows
        (if (i32.lt_u (local.get $i) (local.get $n)) (then
          (if (i32.ne (local.get $b) (i32.ne (i32.load (i32.add (call $t_row (local.get $t) (local.get $i)) (local.get $s))) (i32.const 0)))
            (then (return (i32.const 0))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $rows))))
      (local.set $j (i32.add (local.get $j) (i32.const 1)))
      (br $vars))))
  (i32.const 1))

;; Keep the rows of t for which every filter of the list holds.
(func $filter (param $t i32) (param $f i32)
  (call $filter_mask (local.get $t) (local.get $f) (i32.const 0)))

;; The same, skipping the filters whose position (below 32) has its bit in skip.
(func $filter_mask (param $t i32) (param $f i32) (param $skip i32)
  (local $i i32) (local $k i32) (local $row i32)
  (if (i32.eqz (local.get $f)) (then (return)))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $row (call $t_row (local.get $t) (local.get $i)))
      (if (call $filters_hold_mask (local.get $f) (local.get $row) (local.get $skip)) (then
        (if (i32.ne (local.get $i) (local.get $k)) (then (memory.copy (call $t_row (local.get $t) (local.get $k)) (local.get $row) (global.get $W))))
        (local.set $k (i32.add (local.get $k) (i32.const 1)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (i32.store offset=4 (local.get $t) (local.get $k)))

(func $filters_hold (param $f i32) (param $row i32) (result i32)
  (call $filters_hold_mask (local.get $f) (local.get $row) (i32.const 0)))

(func $filters_hold_mask (param $f i32) (param $row i32) (param $skip i32) (result i32)
  (loop $filters
    (if (local.get $f) (then
      (if (i32.eqz (i32.and (local.get $skip) (i32.const 1))) (then
        (if (i32.ne (call $ebv (call $ev (i32.load offset=4 (local.get $f)) (local.get $row))) (i32.const 1)) (then (return (i32.const 0))))))
      (local.set $skip (i32.shr_u (local.get $skip) (i32.const 1)))
      (local.set $f (i32.load offset=28 (local.get $f)))
      (br $filters))))
  (i32.const 1))

;; A nested group g joined with t: each row passes only g's certain variables
;; in; rows passing the same bindings share one evaluation (the last one, or,
;; once there are several, any earlier one: they are kept by key).
;; mode 0 join, 1 left join (OPTIONAL, with g's filters as its condition).
(func $join_group (param $g i32) (param $t i32) (param $mode i32) (param $out i32)
  (local $i i32) (local $mu i32) (local $key i32) (local $prev i32) (local $r i32) (local $j i32) (local $m i32) (local $matched i32)
  (local $memo i32) (local $tabs i32) (local $x i32) (local $offs i32) (local $k i32) (local $kb i32) (local $full i32)
  (local $diff i32) (local $bits i32) (local $ment i32) (local $o i32)
  ;; in one pass over the slots: the key, the values of the slots the group
  ;; sees (its certain variables, parameters, the substituted row's); and the
  ;; slots a result can bind, those the group's elements mention (every slot
  ;; when that is not known), ended by -1
  (local.set $bits (i32.load offset=12 (local.get $g)))
  (local.set $ment (i32.load offset=24 (local.get $g)))
  (local.set $offs (call $rs_alloc (i32.add (global.get $W) (i32.const 4))))
  (local.set $diff (call $rs_alloc (i32.add (global.get $W) (i32.const 4))))
  (local.set $x (local.get $diff))
  (loop $slots
    (if (i32.lt_u (local.get $j) (global.get $NV)) (then
      (local.set $o (i32.shl (local.get $j) (i32.const 2)))
      (if (i32.or (i32.or (call $bit (local.get $bits) (local.get $j)) (i32.load (i32.add (global.get $params) (local.get $o))))
                  (if (result i32) (global.get $lat) (then (i32.load (i32.add (global.get $lat) (local.get $o)))) (else (i32.const 0)))) (then
        (i32.store (i32.add (local.get $offs) (i32.shl (local.get $k) (i32.const 2))) (local.get $o))
        (local.set $k (i32.add (local.get $k) (i32.const 1)))))
      (if (i32.or (i32.eqz (local.get $ment)) (call $bit (local.get $ment) (local.get $j))) (then
        (i32.store (local.get $x) (local.get $o))
        (local.set $x (i32.add (local.get $x) (i32.const 4)))))
      (local.set $j (i32.add (local.get $j) (i32.const 1)))
      (br $slots))))
  (i32.store (local.get $x) (i32.const -1))
  (local.set $kb (i32.shl (select (local.get $k) (i32.const 1) (local.get $k)) (i32.const 2)))
  (local.set $key (call $rs_alloc (local.get $kb)))
  (local.set $prev (call $rs_alloc (local.get $kb)))
  (i32.store (local.get $key) (i32.const 0))
  (local.set $full (call $row_get))
  (loop $rows
    (if (i32.and (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (i32.eqz (global.get $bu_stop))) (then
      (local.set $mu (call $t_row (local.get $t) (local.get $i)))
      (local.set $j (i32.const 0))
      (loop $keys
        (if (i32.lt_u (local.get $j) (local.get $k)) (then
          (i32.store (i32.add (local.get $key) (i32.shl (local.get $j) (i32.const 2)))
            (i32.load (i32.add (local.get $mu) (i32.load (i32.add (local.get $offs) (i32.shl (local.get $j) (i32.const 2)))))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $keys))))
      (if (i32.or (i32.eqz (local.get $r)) (i32.eqz (call $memeq (local.get $key) (local.get $prev) (local.get $kb)))) (then
        (if (i32.and (i32.ne (local.get $r) (i32.const 0)) (i32.eqz (local.get $memo))) (then
          ;; a second key: keep results by key from now on
          (local.set $memo (call $set_make (i32.shr_u (local.get $kb) (i32.const 2))))
          (local.set $tabs (call $v_new))
          (drop (call $set_add (local.get $memo) (local.get $prev)))
          (call $v_push1 (local.get $tabs) (local.get $r))))
        (local.set $x (if (result i32) (local.get $memo) (then (call $set_add (local.get $memo) (local.get $key))) (else (i32.const 0))))
        (if (i32.or (i32.eqz (local.get $memo)) (global.get $set_new))
          (then
            ;; the row the group starts from: the key's slots of mu
            (memory.fill (local.get $full) (i32.const 0) (global.get $W))
            (local.set $j (i32.const 0))
            (loop $fill
              (if (i32.lt_u (local.get $j) (local.get $k)) (then
                (local.set $o (i32.load (i32.add (local.get $offs) (i32.shl (local.get $j) (i32.const 2)))))
                (i32.store (i32.add (local.get $full) (local.get $o)) (i32.load (i32.add (local.get $mu) (local.get $o))))
                (local.set $j (i32.add (local.get $j) (i32.const 1)))
                (br $fill))))
            (local.set $m (call $t_one (local.get $full)))
            (local.set $r (call $eval_group (local.get $g) (local.get $m) (local.get $mode)))
            (call $t_free (local.get $m))
            (if (local.get $memo) (then (call $v_push1 (local.get $tabs) (local.get $r)))))
          (else (local.set $r (call $v_at (local.get $tabs) (local.get $x)))))
        (memory.copy (local.get $prev) (local.get $key) (local.get $kb))))
      (local.set $matched (i32.const 0))
      (local.set $j (i32.const 0))
      (loop $results
        (if (if (result i32) (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $r))) (then (i32.eqz (call $step))) (else (i32.const 0))) (then
          (if (call $merge_diff (local.get $out) (local.get $mu) (call $t_row (local.get $r) (local.get $j)) (local.get $diff)) (then
            (if (local.get $mode) (then
              ;; OPTIONAL's condition, on the merged row
              (if (call $filters_hold (i32.load offset=8 (local.get $g)) (call $t_row (local.get $out) (i32.sub (i32.load offset=4 (local.get $out)) (i32.const 1))))
                (then (local.set $matched (i32.const 1)))
                (else (i32.store offset=4 (local.get $out) (i32.sub (i32.load offset=4 (local.get $out)) (i32.const 1)))))))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $results))))
      (if (i32.and (local.get $mode) (i32.eqz (local.get $matched))) (then (call $t_push (local.get $out) (local.get $mu))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (if (local.get $memo)
    (then
      (local.set $i (i32.const 0))
      (loop $tables
        (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $tabs))) (then
          (call $t_free (call $v_at (local.get $tabs) (local.get $i)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $tables))))
      (call $v_free (local.get $tabs))
      (call $set_free (local.get $memo)))
    (else (if (local.get $r) (then (call $t_free (local.get $r))))))
  (call $row_put (local.get $offs)))

;; Push to t the merge of row mu and result row b of a nested group, when they
;; are compatible: b binds only the slots of list diff and the key, where it
;; agrees with mu; whether it was pushed.
(func $merge_diff (param $t i32) (param $mu i32) (param $b i32) (param $diff i32) (result i32)
  (local $at i32) (local $x i32) (local $y i32) (local $d i32) (local $row i32)
  (local.set $d (local.get $diff))
  (loop $check
    (if (i32.ge_s (local.tee $at (i32.load (local.get $d))) (i32.const 0)) (then
      (local.set $x (i32.load (i32.add (local.get $mu) (local.get $at))))
      (local.set $y (i32.load (i32.add (local.get $b) (local.get $at))))
      (if (i32.eqz (call $agree (local.get $x) (local.get $y))) (then (return (i32.const 0))))
      (local.set $d (i32.add (local.get $d) (i32.const 4)))
      (br $check))))
  (memory.copy (local.tee $row (call $t_add (local.get $t))) (local.get $mu) (global.get $W))
  (local.set $d (local.get $diff))
  (loop $merge
    (if (i32.ge_s (local.tee $at (i32.load (local.get $d))) (i32.const 0)) (then
      (if (local.tee $y (i32.load (i32.add (local.get $b) (local.get $at))))
        (then (i32.store (i32.add (local.get $row) (local.get $at)) (local.get $y))))
      (local.set $d (i32.add (local.get $d) (i32.const 4)))
      (br $merge))))
  (i32.const 1))

;; A group as an element: P_GROUP, or a UNION of groups.
(func $join_union (param $n i32) (param $t i32) (param $out i32)
  (if (i32.eq (i32.load (local.get $n)) (i32.const P_UNION))
    (then
      (call $join_union (i32.load offset=4 (local.get $n)) (local.get $t) (local.get $out))
      (call $join_union (i32.load offset=8 (local.get $n)) (local.get $t) (local.get $out)))
    (else (call $join_group (local.get $n) (local.get $t) (i32.const 0) (local.get $out)))))

;; Element el (not a triple pattern) joined with t: a new table.
(func $eval_elem (param $el i32) (param $t i32) (result i32)
  (local $k i32) (local $out i32) (local $i i32) (local $j i32) (local $row i32) (local $r i32) (local $v i32) (local $saved i32)
  (local.set $k (i32.load (local.get $el)))
  (local.set $out (call $t_new))
  (block $done
    (if (i32.or (i32.eq (local.get $k) (i32.const P_GROUP)) (i32.eq (local.get $k) (i32.const P_UNION))) (then
      (call $join_union (local.get $el) (local.get $t) (local.get $out))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_OPTIONAL)) (then
      (call $join_group (i32.load offset=4 (local.get $el)) (local.get $t) (i32.const 1) (local.get $out))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_MINUS)) (then
      (call $minus (i32.load offset=4 (local.get $el)) (local.get $t) (local.get $out))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_GRAPH)) (then
      (call $graph_elem (local.get $el) (local.get $t) (local.get $out))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_LATERAL)) (then
      ;; the right side with each row substituted
      (loop $rows
        (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
          (local.set $row (call $t_one (call $t_row (local.get $t) (local.get $i))))
          (local.set $saved (global.get $lat))
          (global.set $lat (call $t_row (local.get $t) (local.get $i)))
          (local.set $r (call $eval_group (i32.load offset=4 (local.get $el)) (local.get $row) (i32.const 0)))
          (global.set $lat (local.get $saved))
          (local.set $j (i32.const 0))
          (loop $results
            (if (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $r))) (then
              (call $t_push (local.get $out) (call $t_row (local.get $r) (local.get $j)))
              (local.set $j (i32.add (local.get $j) (i32.const 1)))
              (br $results))))
          (call $t_free (local.get $r))
          (call $t_free (local.get $row))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $rows))))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_BIND)) (then
      (loop $rows
        (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
          (call $t_push (local.get $out) (call $t_row (local.get $t) (local.get $i)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $rows))))
      (call $bind_rows (local.get $el) (local.get $out))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_VALUES)) (then
      (call $join_values (local.get $el) (local.get $t) (local.get $out))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_SUB)) (then
      (local.set $r (call $subquery (i32.load offset=4 (local.get $el))))
      (call $join_tables (local.get $t) (local.get $r) (local.get $out))
      (br $done)))
    (if (i32.eq (local.get $k) (i32.const P_PATH)) (then
      (call $eval_path (local.get $el) (local.get $t) (local.get $out))
      (br $done))))
  (local.get $out))

;; BIND el: set its variable in each row of t where it is unbound. A row that
;; binds it already (passed into a nested group, which BIND then joins) keeps
;; it when the value is compatible and is dropped otherwise; a parameter or a
;; substituted variable is a constant. A pure expression (see $an_bind) is
;; evaluated once per distinct value of its variables.
(func $bind_rows (param $el i32) (param $t i32)
  (local $i i32) (local $at i32) (local $v i32) (local $vars i32) (local $k i32) (local $set i32) (local $vals i32)
  (local $key i32) (local $j i32) (local $x i32) (local $row i32) (local $new i32) (local $cur i32) (local $fixed i32) (local $n i32)
  (local.set $v (i32.shl (i32.load offset=8 (local.get $el)) (i32.const 2)))
  (local.set $fixed (i32.or (i32.ne (i32.load (i32.add (global.get $params) (local.get $v))) (i32.const 0))
                            (if (result i32) (global.get $lat) (then (i32.ne (i32.load (i32.add (global.get $lat) (local.get $v))) (i32.const 0))) (else (i32.const 0)))))
  (if (i32.and (i32.ne (local.tee $vars (i32.load offset=12 (local.get $el))) (i32.const 0))
               (i32.ge_u (i32.load offset=4 (local.get $t)) (i32.const 4))) (then
    (local.set $k (i32.load (local.get $vars)))
    (local.set $set (call $set_make (select (local.get $k) (i32.const 1) (local.get $k))))
    (local.set $vals (call $v_new))
    (local.set $key (call $rs_alloc (i32.shl (i32.add (local.get $k) (i32.const 1)) (i32.const 2))))
    (i32.store (local.get $key) (i32.const 0))))
  (loop $rows
    (if (i32.and (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (i32.eqz (global.get $bu_stop))) (then
      (local.set $row (call $t_row (local.get $t) (local.get $i)))
      (local.set $at (i32.add (local.get $row) (local.get $v)))
      (block $kept
        (if (i32.or (i32.eqz (local.tee $cur (i32.load (local.get $at)))) (i32.eqz (local.get $fixed))) (then
          (if (local.get $set)
            (then
              (local.set $j (i32.const 0))
              (loop $keys
                (if (i32.lt_u (local.get $j) (local.get $k)) (then
                  (i32.store (i32.add (local.get $key) (i32.shl (local.get $j) (i32.const 2)))
                    (i32.load (i32.add (local.get $row) (i32.shl (i32.load offset=4 (i32.add (local.get $vars) (i32.shl (local.get $j) (i32.const 2)))) (i32.const 2)))))
                  (local.set $j (i32.add (local.get $j) (i32.const 1)))
                  (br $keys))))
              (local.set $x (call $set_add (local.get $set) (local.get $key)))
              (local.set $new (global.get $set_new))
              (if (local.get $new) (then (call $v_push1 (local.get $vals) (call $ev (i32.load offset=4 (local.get $el)) (local.get $row)))))
              (local.set $x (call $v_at (local.get $vals) (local.get $x))))
            (else (local.set $x (call $ev (i32.load offset=4 (local.get $el)) (local.get $row)))))
          (if (i32.eqz (local.get $cur))
            (then (i32.store (local.get $at) (local.get $x)))
            (else (br_if $kept (i32.and (i32.ne (local.get $x) (i32.const 0)) (i32.ne (local.get $x) (local.get $cur))))))))
        (if (i32.ne (local.get $i) (local.get $n)) (then (memory.copy (call $t_row (local.get $t) (local.get $n)) (local.get $row) (global.get $W))))
        (local.set $n (i32.add (local.get $n) (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (i32.store offset=4 (local.get $t) (local.get $n))
  (if (local.get $set) (then
    (call $row_put (local.get $key))
    (call $v_free (local.get $vals))
    (call $set_free (local.get $set)))))

;; Push to out the compatible merges of t's and r's rows.
(func $join_tables (param $t i32) (param $r i32) (param $out i32)
  (local $i i32) (local $j i32) (local $a i32) (local $b i32)
  (loop $left
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $a (call $t_row (local.get $t) (local.get $i)))
      (local.set $j (i32.const 0))
      (loop $right
        (if (if (result i32) (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $r))) (then (i32.eqz (call $step))) (else (i32.const 0))) (then
          (local.set $b (call $t_row (local.get $r) (local.get $j)))
          (if (call $compatible (local.get $a) (local.get $b)) (then (call $push_merge (local.get $out) (local.get $a) (local.get $b))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $right))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $left)))))

;; MINUS: rows of t with no compatible row of g sharing a variable.
(func $minus (param $g i32) (param $t i32) (param $out i32)
  (local $one i32) (local $r i32) (local $i i32) (local $j i32) (local $a i32) (local $b i32) (local $drop i32)
  (local.set $one (call $t_one (global.get $params)))
  (local.set $r (call $eval_group (local.get $g) (local.get $one) (i32.const 0)))
  (call $t_free (local.get $one))
  (loop $left
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $a (call $t_row (local.get $t) (local.get $i)))
      (local.set $drop (i32.const 0))
      (local.set $j (i32.const 0))
      (loop $right
        (if (i32.and (i32.eqz (local.get $drop)) (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $r)))) (then
          (local.set $b (call $t_row (local.get $r) (local.get $j)))
          (local.set $drop (i32.and (call $compatible (local.get $a) (local.get $b)) (call $shares (local.get $a) (local.get $b))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $right))))
      (if (i32.eqz (local.get $drop)) (then (call $t_push (local.get $out) (local.get $a))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $left))))
  (call $t_free (local.get $r)))

;; Whether rows a and b bind a common variable (parameters are constants).
(func $shares (param $a i32) (param $b i32) (result i32)
  (local $i i32)
  (loop $slots
    (if (i32.lt_u (local.get $i) (global.get $W)) (then
      (if (i32.and (i32.and (i32.ne (i32.load (i32.add (local.get $a) (local.get $i))) (i32.const 0))
                            (i32.ne (i32.load (i32.add (local.get $b) (local.get $i))) (i32.const 0)))
                   (i32.eqz (i32.load (i32.add (global.get $params) (local.get $i)))))
        (then (return (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 4)))
      (br $slots))))
  (i32.const 0))

;; GRAPH ref { g }
(func $graph_elem (param $el i32) (param $t i32) (param $out i32)
  (local $i i32) (local $j i32) (local $mu i32) (local $gid i32) (local $one i32) (local $saved i32) (local $ref i32) (local $n i32)
  (local.set $saved (global.get $ag))
  (local.set $ref (i32.load offset=4 (local.get $el)))
  (call $named_graphs)
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $mu (call $t_row (local.get $t) (local.get $i)))
      (local.set $gid (call $resolve_ref (local.get $ref) (local.get $mu)))
      (if (local.get $gid)
        (then
          (if (call $is_named (local.get $gid)) (then
            (global.set $ag (local.get $gid))
            (local.set $one (call $t_one (local.get $mu)))
            (call $join_group (i32.load offset=8 (local.get $el)) (local.get $one) (i32.const 0) (local.get $out))
            (call $t_free (local.get $one)))))
        (else
          ;; every named graph, binding the variable
          (local.set $j (i32.const 0))
          (loop $graphs
            (if (i32.lt_u (local.get $j) (global.get $ngn)) (then
              (global.set $ag (local.tee $gid (i32.load (i32.add (global.get $ng) (i32.shl (local.get $j) (i32.const 2))))))
              (local.set $one (call $t_one (local.get $mu)))
              (drop (call $unify (local.get $ref) (local.get $gid) (call $t_row (local.get $one) (i32.const 0))))
              (call $join_group (i32.load offset=8 (local.get $el)) (local.get $one) (i32.const 0) (local.get $out))
              (call $t_free (local.get $one))
              (local.set $j (i32.add (local.get $j) (i32.const 1)))
              (br $graphs))))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (global.set $ag (local.get $saved)))

(func $is_named (param $id i32) (result i32)
  (local $j i32)
  (loop $graphs
    (if (i32.lt_u (local.get $j) (global.get $ngn)) (then
      (if (i32.eq (i32.load (i32.add (global.get $ng) (i32.shl (local.get $j) (i32.const 2)))) (local.get $id)) (then (return (i32.const 1))))
      (local.set $j (i32.add (local.get $j) (i32.const 1)))
      (br $graphs))))
  (i32.const 0))

;; VALUES: each row of t with each compatible data row.
(func $join_values (param $el i32) (param $t i32) (param $out i32)
  (local $i i32) (local $j i32) (local $k i32) (local $mu i32) (local $d i32) (local $nv i32) (local $row i32)
  (local $slot i32) (local $v i32) (local $ok i32)
  (local.set $nv (i32.load offset=4 (local.get $el)))
  (local.set $row (call $row_get))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $mu (call $t_row (local.get $t) (local.get $i)))
      (local.set $j (i32.const 0))
      (loop $data
        (if (if (result i32) (i32.lt_u (local.get $j) (i32.load offset=12 (local.get $el))) (then (i32.eqz (call $step))) (else (i32.const 0))) (then
          (memory.copy (local.get $row) (local.get $mu) (global.get $W))
          (local.set $d (i32.add (i32.load offset=16 (local.get $el)) (i32.shl (i32.mul (local.get $j) (local.get $nv)) (i32.const 2))))
          (local.set $ok (i32.const 1))
          (local.set $k (i32.const 0))
          (loop $vars
            (if (i32.lt_u (local.get $k) (local.get $nv)) (then
              (if (local.tee $v (i32.load (i32.add (local.get $d) (i32.shl (local.get $k) (i32.const 2))))) (then
                (local.set $ok (i32.and (local.get $ok)
                  (call $unify (call $vref (i32.load (i32.add (i32.load offset=8 (local.get $el)) (i32.shl (local.get $k) (i32.const 2)))))
                               (local.get $v) (local.get $row))))))
              (local.set $k (i32.add (local.get $k) (i32.const 1)))
              (br $vars))))
          (if (local.get $ok) (then (call $t_push (local.get $out) (local.get $row))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $data))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (call $row_put (local.get $row)))

;; The row substituted into the pattern being evaluated (LATERAL, EXISTS), or 0.
(global $lat (mut i32) (i32.const 0))

;; A subquery's solutions with its projected variables moved to the outer
;; slots; computed once per run and active graph, unless substitution binds
;; a projected variable: then those are constants of this solve.
(func $subquery (param $sq i32) (result i32)
  (local $t i32) (local $out i32) (local $i i32) (local $row i32) (local $src i32) (local $m i32)
  (local $sub i32) (local $v i32) (local $saved i32) (local $savedlat i32)
  (if (global.get $lat) (then
    (local.set $m (i32.load offset=72 (local.get $sq)))
    (loop $map
      (if (local.get $m) (then
        (if (local.tee $v (i32.load (i32.add (global.get $lat) (i32.shl (i32.load offset=8 (local.get $m)) (i32.const 2))))) (then
          (if (i32.eqz (local.get $sub)) (then
            (local.set $sub (call $row_get))
            (memory.copy (local.get $sub) (global.get $params) (global.get $W))))
          (i32.store (i32.add (local.get $sub) (i32.shl (i32.load offset=4 (local.get $m)) (i32.const 2))) (local.get $v))))
        (local.set $m (i32.load offset=28 (local.get $m)))
        (br $map))))))
  (if (i32.eqz (local.get $sub)) (then
    (if (i32.and (i32.eq (i32.load offset=88 (local.get $sq)) (global.get $epoch)) (i32.eq (i32.load offset=96 (local.get $sq)) (global.get $ag)))
      (then (return (i32.load offset=92 (local.get $sq)))))))
  (local.set $saved (global.get $params))
  (local.set $savedlat (global.get $lat))
  (if (local.get $sub) (then (global.set $params (local.get $sub))))
  (global.set $lat (local.get $sub))
  (local.set $t (call $solve (local.get $sq)))
  (global.set $params (local.get $saved))
  (global.set $lat (local.get $savedlat))
  (if (local.get $sub) (then (call $row_put (local.get $sub))))
  (local.set $out (call $t_new))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $src (call $t_row (local.get $t) (local.get $i)))
      (local.set $row (call $t_add (local.get $out)))
      (memory.copy (local.get $row) (global.get $params) (global.get $W))
      (local.set $m (i32.load offset=72 (local.get $sq)))
      (loop $map
        (if (local.get $m) (then
          (if (i32.eqz (i32.load (i32.add (local.get $row) (i32.shl (i32.load offset=8 (local.get $m)) (i32.const 2))))) (then
            (i32.store (i32.add (local.get $row) (i32.shl (i32.load offset=8 (local.get $m)) (i32.const 2)))
                       (i32.load (i32.add (local.get $src) (i32.shl (i32.load offset=4 (local.get $m)) (i32.const 2)))))))
          (local.set $m (i32.load offset=28 (local.get $m)))
          (br $map))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (call $t_free (local.get $t))
  (if (local.get $sub) (then (call $cache_keep (local.get $out)) (return (local.get $out))))
  ;; the cache keeps the table until the next run replaces it
  (if (i32.eq (i32.load offset=88 (local.get $sq)) (global.get $epoch)) (then (call $cache_drop (i32.load offset=92 (local.get $sq)))))
  (i32.store offset=88 (local.get $sq) (global.get $epoch))
  (i32.store offset=92 (local.get $sq) (local.get $out))
  (i32.store offset=96 (local.get $sq) (global.get $ag))
  (call $cache_keep (local.get $out))
  (local.get $out))

;; Tables cached during a run, freed at its end.
(global $cache (mut i32) (i32.const 0))   ;; a vector of tables

(func $cache_keep (param $t i32)
  (if (i32.eqz (global.get $cache)) (then (global.set $cache (call $v_new))))
  (call $v_push2 (global.get $cache) (local.get $t) (i32.const 0)))

(func $cache_drop (param $t i32)
  (local $i i32)
  (loop $tables
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (global.get $cache))) (then
      (if (i32.eq (call $v_at (global.get $cache) (local.get $i)) (local.get $t)) (then
        (call $t_free (local.get $t))
        (i32.store (i32.add (i32.load (global.get $cache)) (i32.shl (local.get $i) (i32.const 2))) (i32.const 0))
        (return)))
      (local.set $i (i32.add (local.get $i) (i32.const 2)))
      (br $tables)))))

(func $cache_clear
  (local $i i32) (local $t i32)
  (if (i32.eqz (global.get $cache)) (then (return)))
  (loop $tables
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (global.get $cache))) (then
      (if (local.tee $t (call $v_at (global.get $cache) (local.get $i))) (then (call $t_free (local.get $t))))
      (local.set $i (i32.add (local.get $i) (i32.const 2)))
      (br $tables))))
  (call $v_free (global.get $cache))
  (global.set $cache (i32.const 0)))

;; --- property paths --------------------------------------------------------------

(func $eval_path (param $el i32) (param $t i32) (param $out i32)
  (local $i i32) (local $j i32) (local $mu i32) (local $s i32) (local $o i32) (local $pairs i32) (local $row i32)
  (local.set $row (call $row_get))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $mu (call $t_row (local.get $t) (local.get $i)))
      (local.set $s (call $resolve_ref (i32.load offset=4 (local.get $el)) (local.get $mu)))
      (local.set $o (call $resolve_ref (i32.load offset=12 (local.get $el)) (local.get $mu)))
      (if (i32.and (i32.ge_s (local.get $s) (i32.const 0)) (i32.ge_s (local.get $o) (i32.const 0))) (then
        (local.set $pairs (call $v_new))
        ;; a zero-length path starts only at nodes of the graph, unless the
        ;; query names the node itself
        (global.set $zero_check (i32.or (i32.and (i32.lt_s (i32.load offset=4 (local.get $el)) (i32.const 0)) (i32.ne (local.get $s) (i32.const 0)))
                                        (i32.and (i32.lt_s (i32.load offset=12 (local.get $el)) (i32.const 0)) (i32.ne (local.get $o) (i32.const 0)))))
        (call $pp (i32.load offset=8 (local.get $el)) (local.get $s) (local.get $o) (local.get $pairs))
        (global.set $zero_check (i32.const 0))
        (local.set $j (i32.const 0))
        (loop $each
          (if (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $pairs))) (then
            (memory.copy (local.get $row) (local.get $mu) (global.get $W))
            (if (i32.and (call $unify (i32.load offset=4 (local.get $el)) (call $v_at (local.get $pairs) (local.get $j)) (local.get $row))
                         (call $unify (i32.load offset=12 (local.get $el)) (call $v_at (local.get $pairs) (i32.add (local.get $j) (i32.const 1))) (local.get $row)))
              (then (call $t_push (local.get $out) (local.get $row))))
            (local.set $j (i32.add (local.get $j) (i32.const 2)))
            (br $each))))
        (call $v_free (local.get $pairs))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (call $row_put (local.get $row)))

;; Append to v the pairs (a, b) with a path x from a to b, a = s and b = o when bound.
(func $pp (param $x i32) (param $s i32) (param $o i32) (param $v i32)
  (local $k i32) (local $tmp i32) (local $tmp2 i32) (local $i i32) (local $j i32) (local $gi i32) (local $gn i32) (local $r i32)
  (local.set $k (i32.load (local.get $x)))
  (if (i32.eq (local.get $k) (i32.const X_LINK)) (then
    (local.set $gn (call $graph_count))
    (loop $graphs
      (if (i32.lt_u (local.get $gi) (local.get $gn)) (then
        (call $scan (local.get $s) (i32.load offset=4 (local.get $x)) (local.get $o) (call $graph_at (local.get $gi)))
        (local.set $i (i32.const 0))
        (loop $quads
          (if (i32.lt_u (local.get $i) (global.get $sc_n)) (then
            (local.set $r (call $sc_quad (local.get $i)))
            (if (i32.and (call $quad_is (local.get $r) (local.get $s) (i32.load offset=4 (local.get $x)) (local.get $o))
                         (i32.eq (i32.load offset=12 (local.get $r)) (call $graph_at (local.get $gi)))) (then
              (if (i32.eqz (if (result i32) (local.get $gi) (then (call $seen_before (local.get $gi) (local.get $r))) (else (i32.const 0))))
                (then (call $v_push2 (local.get $v) (i32.load (local.get $r)) (i32.load offset=8 (local.get $r)))))))
            (local.set $i (i32.add (local.get $i) (i32.const 1)))
            (br $quads))))
        (local.set $gi (i32.add (local.get $gi) (i32.const 1)))
        (br $graphs))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const X_INV)) (then
    (local.set $tmp (call $v_new))
    (call $pp (i32.load offset=4 (local.get $x)) (local.get $o) (local.get $s) (local.get $tmp))
    (loop $pairs
      (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $tmp))) (then
        (call $v_push2 (local.get $v) (call $v_at (local.get $tmp) (i32.add (local.get $i) (i32.const 1))) (call $v_at (local.get $tmp) (local.get $i)))
        (local.set $i (i32.add (local.get $i) (i32.const 2)))
        (br $pairs))))
    (call $v_free (local.get $tmp))
    (return)))
  (if (i32.eq (local.get $k) (i32.const X_ALT)) (then
    (call $pp (i32.load offset=4 (local.get $x)) (local.get $s) (local.get $o) (local.get $v))
    (call $pp (i32.load offset=8 (local.get $x)) (local.get $s) (local.get $o) (local.get $v))
    (return)))
  (if (i32.eq (local.get $k) (i32.const X_SEQ)) (then
    (local.set $tmp (call $v_new))
    (if (i32.and (i32.eqz (local.get $s)) (i32.ne (local.get $o) (i32.const 0)))
      (then
        ;; from the end
        (call $pp (i32.load offset=8 (local.get $x)) (i32.const 0) (local.get $o) (local.get $tmp))
        (loop $mids
          (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $tmp))) (then
            (local.set $tmp2 (call $v_new))
            (call $pp (i32.load offset=4 (local.get $x)) (i32.const 0) (call $v_at (local.get $tmp) (local.get $i)) (local.get $tmp2))
            (local.set $j (i32.const 0))
            (loop $firsts
              (if (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $tmp2))) (then
                (call $v_push2 (local.get $v) (call $v_at (local.get $tmp2) (local.get $j)) (call $v_at (local.get $tmp) (i32.add (local.get $i) (i32.const 1))))
                (local.set $j (i32.add (local.get $j) (i32.const 2)))
                (br $firsts))))
            (call $v_free (local.get $tmp2))
            (local.set $i (i32.add (local.get $i) (i32.const 2)))
            (br $mids)))))
      (else
        (call $pp (i32.load offset=4 (local.get $x)) (local.get $s) (i32.const 0) (local.get $tmp))
        (loop $mids
          (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $tmp))) (then
            (local.set $tmp2 (call $v_new))
            (call $pp (i32.load offset=8 (local.get $x)) (call $v_at (local.get $tmp) (i32.add (local.get $i) (i32.const 1))) (local.get $o) (local.get $tmp2))
            (local.set $j (i32.const 0))
            (loop $lasts
              (if (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $tmp2))) (then
                (call $v_push2 (local.get $v) (call $v_at (local.get $tmp) (local.get $i)) (call $v_at (local.get $tmp2) (i32.add (local.get $j) (i32.const 1))))
                (local.set $j (i32.add (local.get $j) (i32.const 2)))
                (br $lasts))))
            (call $v_free (local.get $tmp2))
            (local.set $i (i32.add (local.get $i) (i32.const 2)))
            (br $mids))))))
    (call $v_free (local.get $tmp))
    (return)))
  (if (i32.eq (local.get $k) (i32.const X_NEG)) (then
    (call $pp_neg (local.get $x) (local.get $s) (local.get $o) (local.get $v))
    (return)))
  ;; *, + and ?: distinct pairs
  (call $pp_closure (local.get $x) (local.get $s) (local.get $o) (local.get $v)))

;; Negated property set: triples whose predicate is not listed. Forward
;; members exclude (s, p, o); inverse members (^p) exclude (o, p, s).
(func $pp_neg (param $x i32) (param $s i32) (param $o i32) (param $v i32)
  (if (i32.or (i32.ne (i32.load offset=4 (local.get $x)) (i32.const 0)) (i32.eqz (i32.load offset=8 (local.get $x))))
    (then (call $pp_neg_dir (i32.load offset=4 (local.get $x)) (local.get $s) (local.get $o) (i32.const 0) (local.get $v))))
  (if (i32.load offset=8 (local.get $x))
    (then (call $pp_neg_dir (i32.load offset=8 (local.get $x)) (local.get $o) (local.get $s) (i32.const 1) (local.get $v)))))

;; Triples (a, p, b) with p not in links, as pairs (a, b), or (b, a) when swap.
(func $pp_neg_dir (param $links i32) (param $a i32) (param $b i32) (param $swap i32) (param $v i32)
  (local $gi i32) (local $gn i32) (local $i i32) (local $r i32)
  (local.set $gn (call $graph_count))
  (loop $graphs
    (if (i32.lt_u (local.get $gi) (local.get $gn)) (then
      (call $scan (local.get $a) (i32.const 0) (local.get $b) (call $graph_at (local.get $gi)))
      (local.set $i (i32.const 0))
      (loop $quads
        (if (i32.lt_u (local.get $i) (global.get $sc_n)) (then
          (local.set $r (call $sc_quad (local.get $i)))
          (if (i32.and (i32.and (i32.eq (i32.load offset=12 (local.get $r)) (call $graph_at (local.get $gi)))
                                (call $quad_is (local.get $r) (local.get $a) (i32.const 0) (local.get $b)))
                       (i32.eqz (i32.or (call $in_links (local.get $links) (i32.load offset=4 (local.get $r)))
                                        (if (result i32) (local.get $gi) (then (call $seen_before (local.get $gi) (local.get $r))) (else (i32.const 0)))))) (then
            (if (local.get $swap)
              (then (call $v_push2 (local.get $v) (i32.load offset=8 (local.get $r)) (i32.load (local.get $r))))
              (else (call $v_push2 (local.get $v) (i32.load (local.get $r)) (i32.load offset=8 (local.get $r)))))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $quads))))
      (local.set $gi (i32.add (local.get $gi) (i32.const 1)))
      (br $graphs)))))

(func $in_links (param $l i32) (param $id i32) (result i32)
  (loop $links
    (if (local.get $l) (then
      (if (i32.eq (i32.load offset=4 (local.get $l)) (local.get $id)) (then (return (i32.const 1))))
      (local.set $l (i32.load offset=28 (local.get $l)))
      (br $links))))
  (i32.const 0))

;; Set when a path's bound end came from a variable (see $eval_path).
(global $zero_check (mut i32) (i32.const 0))

;; Whether n is a subject or object in the graphs read.
(func $has_node (param $n i32) (result i32)
  (local $gi i32) (local $gn i32) (local $i i32) (local $r i32) (local $k i32)
  (local.set $gn (call $graph_count))
  (loop $graphs (if (i32.lt_u (local.get $gi) (local.get $gn)) (then
    (local.set $k (i32.const 0))
    (loop $ends
      (if (local.get $k)
        (then (call $scan (i32.const 0) (i32.const 0) (local.get $n) (call $graph_at (local.get $gi))))
        (else (call $scan (local.get $n) (i32.const 0) (i32.const 0) (call $graph_at (local.get $gi)))))
      (local.set $i (i32.const 0))
      (loop $quads (if (i32.lt_u (local.get $i) (global.get $sc_n)) (then
        (local.set $r (call $sc_quad (local.get $i)))
        (if (i32.and (i32.eq (i32.load offset=12 (local.get $r)) (call $graph_at (local.get $gi)))
                     (i32.eq (i32.load (i32.add (local.get $r) (i32.shl (local.get $k) (i32.const 3)))) (local.get $n)))
          (then (return (i32.const 1))))
        (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $quads))))
      (br_if $ends (i32.eq (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 1))))
    (local.set $gi (i32.add (local.get $gi) (i32.const 1))) (br $graphs))))
  (i32.const 0))

;; Every node of the graphs read: subjects and objects.
(func $all_nodes (param $set i32)
  (local $gi i32) (local $gn i32) (local $i i32) (local $r i32)
  (local.set $gn (call $graph_count))
  (loop $graphs
    (if (i32.lt_u (local.get $gi) (local.get $gn)) (then
      (call $scan (i32.const 0) (i32.const 0) (i32.const 0) (call $graph_at (local.get $gi)))
      (local.set $i (i32.const 0))
      (loop $quads
        (if (i32.lt_u (local.get $i) (global.get $sc_n)) (then
          (local.set $r (call $sc_quad (local.get $i)))
          (if (i32.eq (i32.load offset=12 (local.get $r)) (call $graph_at (local.get $gi))) (then
            (drop (call $set_add1 (local.get $set) (i32.load (local.get $r))))
            (drop (call $set_add1 (local.get $set) (i32.load offset=8 (local.get $r))))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $quads))))
      (local.set $gi (i32.add (local.get $gi) (i32.const 1)))
      (br $graphs)))))

;; x*, x+, x?: distinct pairs.
(func $pp_closure (param $x i32) (param $s i32) (param $o i32) (param $v i32)
  (local $nodes i32) (local $i i32) (local $reach i32) (local $k i32) (local $n i32)
  (local.set $k (i32.load (local.get $x)))
  (if (local.get $s) (then
    (local.set $reach (call $reach (local.get $x) (local.get $s) (i32.const 0)))
    (loop $each
      (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $reach))) (then
        (local.set $n (i32.load (call $set_key (local.get $reach) (local.get $i))))
        (if (i32.or (i32.eqz (local.get $o)) (i32.eq (local.get $o) (local.get $n))) (then (call $v_push2 (local.get $v) (local.get $s) (local.get $n))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $each))))
    (call $set_free (local.get $reach))
    (return)))
  (if (local.get $o) (then
    (local.set $reach (call $reach (local.get $x) (local.get $o) (i32.const 1)))
    (loop $each
      (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $reach))) (then
        (call $v_push2 (local.get $v) (i32.load (call $set_key (local.get $reach) (local.get $i))) (local.get $o))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $each))))
    (call $set_free (local.get $reach))
    (return)))
  ;; neither bound: from every node
  (local.set $nodes (call $set_make (i32.const 1)))
  (call $all_nodes (local.get $nodes))
  (loop $starts
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $nodes))) (then
      (call $pp_closure (local.get $x) (i32.load (call $set_key (local.get $nodes) (local.get $i))) (i32.const 0) (local.get $v))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $starts))))
  (call $set_free (local.get $nodes)))

;; The nodes reachable from n by x* / x+ / x? (backwards when back): a set.
(func $reach (param $x i32) (param $n i32) (param $back i32) (result i32)
  (local $set i32) (local $k i32) (local $i i32) (local $j i32) (local $tmp i32) (local $cur i32) (local $next i32) (local $sub i32)
  (local.set $k (i32.load (local.get $x)))
  (local.set $sub (i32.load offset=4 (local.get $x)))
  (local.set $set (call $set_make (i32.const 1)))
  ;; zero length: the node itself (x* and x?)
  (if (i32.ne (local.get $k) (i32.const X_PLUS)) (then
    (if (i32.or (i32.eqz (global.get $zero_check)) (call $has_node (local.get $n)))
      (then (drop (call $set_add1 (local.get $set) (local.get $n)))))))
  (global.set $zero_check (i32.const 0))
  ;; frontier: a vector of nodes to expand; for x+ start from n itself
  (local.set $tmp (call $v_new))
  (call $v_push2 (local.get $tmp) (local.get $n) (i32.const 0))
  (block $done
    (loop $frontier
      (br_if $done (i32.ge_u (local.get $i) (i32.load offset=4 (local.get $tmp))))
      (br_if $done (call $step))
      (local.set $cur (call $v_at (local.get $tmp) (local.get $i)))
      (local.set $i (i32.add (local.get $i) (i32.const 2)))
      (local.set $next (call $v_new))
      (if (local.get $back)
        (then (call $pp (local.get $sub) (i32.const 0) (local.get $cur) (local.get $next)))
        (else (call $pp (local.get $sub) (local.get $cur) (i32.const 0) (local.get $next))))
      (local.set $j (i32.const 0))
      (loop $edges
        (if (i32.lt_u (local.get $j) (i32.load offset=4 (local.get $next))) (then
          (local.set $cur (call $v_at (local.get $next) (select (local.get $j) (i32.add (local.get $j) (i32.const 1)) (local.get $back))))
          (if (call $set_add1 (local.get $set) (local.get $cur)) (then
            (if (i32.ne (local.get $k) (i32.const X_OPT)) (then (call $v_push2 (local.get $tmp) (local.get $cur) (i32.const 0))))))
          (local.set $j (i32.add (local.get $j) (i32.const 2)))
          (br $edges))))
      (call $v_free (local.get $next))
      (br $frontier)))
  (call $v_free (local.get $tmp))
  (local.get $set))
