;; collect: free the terms nothing uses. Kept: the default graph and the
;; built-in datatypes, every term of a quad, of a live prepared query (and its
;; parameters), and what those reach (a literal's datatype, a triple term's
;; parts). Freed ids leave the interning table and are reused by $new_id.

(global $marks (mut i32) (i32.const 0))   ;; a byte per id

(func $mark (param $id i32)
  (local $r i32)
  (if (i32.eqz (call $kind (local.get $id))) (then (return)))
  (if (i32.load8_u (i32.add (global.get $marks) (local.get $id))) (then (return)))
  (i32.store8 (i32.add (global.get $marks) (local.get $id)) (i32.const 1))
  (local.set $r (call $rec (local.get $id)))
  (if (i32.eq (i32.load8_u (local.get $r)) (i32.const 3)) (then (call $mark (i32.load offset=16 (local.get $r)))))
  (if (i32.eq (i32.load8_u (local.get $r)) (i32.const 4)) (then
    (call $mark (i32.load offset=8 (local.get $r)))
    (call $mark (i32.load offset=12 (local.get $r)))
    (call $mark (i32.load offset=16 (local.get $r))))))

;; Remove id from the interning table (backward shift; no tombstones).
(func $hdel (param $id i32)
  (local $i i32) (local $j i32) (local $mask i32) (local $v i32) (local $home i32)
  (local.set $mask (i32.sub (global.get $hcap) (i32.const 1)))
  (local.set $i (i32.load offset=4 (call $rec (local.get $id))))
  (loop $find
    (local.set $i (i32.and (local.get $i) (local.get $mask)))
    (if (i32.ne (i32.load (i32.add (global.get $htab) (i32.shl (local.get $i) (i32.const 2)))) (local.get $id)) (then
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $find))))
  (local.set $j (local.get $i))
  (loop $scan
    (local.set $j (i32.and (i32.add (local.get $j) (i32.const 1)) (local.get $mask)))
    (if (local.tee $v (i32.load (i32.add (global.get $htab) (i32.shl (local.get $j) (i32.const 2))))) (then
      (local.set $home (i32.load offset=4 (call $rec (local.get $v))))
      (if (i32.ge_u (i32.and (i32.sub (local.get $j) (local.get $home)) (local.get $mask))
                    (i32.and (i32.sub (local.get $j) (local.get $i)) (local.get $mask)))
        (then
          (i32.store (i32.add (global.get $htab) (i32.shl (local.get $i) (i32.const 2))) (local.get $v))
          (local.set $i (local.get $j))))
      (br $scan))))
  (i32.store (i32.add (global.get $htab) (i32.shl (local.get $i) (i32.const 2))) (i32.const 0))
  (global.set $hcount (i32.sub (global.get $hcount) (i32.const 1))))

(func (export "collect") (result i32)
  (local $id i32) (local $q i32) (local $r i32) (local $i i32) (local $v i32) (local $n i32) (local $out i32) (local $k i32)
  (global.set $marks (call $alloc0 (global.get $next_id)))
  (local.set $id (i32.const 1))
  (loop $builtin (if (i32.le_u (local.get $id) (i32.const 4)) (then
    (call $mark (local.get $id))
    (local.set $id (i32.add (local.get $id) (i32.const 1))) (br $builtin))))
  ;; quads
  (loop $quads (if (i32.lt_u (local.get $i) (global.get $qn)) (then
    (local.set $r (call $quad (local.get $i)))
    (call $mark (i32.load (local.get $r)))
    (call $mark (i32.load offset=4 (local.get $r)))
    (call $mark (i32.load offset=8 (local.get $r)))
    (call $mark (i32.load offset=12 (local.get $r)))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $quads))))
  ;; prepared queries: their terms, parameters and bound values
  (local.set $q (global.get $queries))
  (loop $queries (if (local.get $q) (then
    (local.set $v (i32.load offset=244 (local.get $q)))
    (local.set $i (i32.const 0))
    (loop $terms (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $v))) (then
      (call $mark (call $v_at (local.get $v) (local.get $i)))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $terms))))
    (local.set $i (i32.const 0))
    (loop $params (if (i32.lt_u (local.get $i) (i32.load offset=52 (local.get $q))) (then
      (call $mark (i32.load (i32.add (i32.load offset=60 (local.get $q)) (i32.shl (local.get $i) (i32.const 2)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $params))))
    (local.set $i (i32.const 0))
    (loop $values (if (i32.lt_u (local.get $i) (i32.load offset=80 (local.get $q))) (then
      (call $mark (i32.load (i32.add (i32.load offset=76 (local.get $q)) (i32.shl (local.get $i) (i32.const 2)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $values))))
    (local.set $q (i32.load offset=240 (local.get $q)))
    (br $queries))))
  ;; graphs made by CREATE
  (if (global.get $made_graphs) (then
    (local.set $i (i32.const 0))
    (loop $made (if (i32.lt_u (local.get $i) (i32.load offset=4 (global.get $made_graphs))) (then
      (call $mark (i32.load (call $set_key (global.get $made_graphs) (local.get $i))))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $made))))))
  ;; sweep
  (local.set $out (call $v_new))
  (local.set $id (i32.const 5))
  (loop $terms (if (i32.lt_u (local.get $id) (global.get $next_id)) (then
    (if (i32.and (i32.ne (call $kind (local.get $id)) (i32.const 0)) (i32.eqz (i32.load8_u (i32.add (global.get $marks) (local.get $id))))) (then
      (local.set $r (call $rec (local.get $id)))
      (call $hdel (local.get $id))
      (if (i32.le_u (i32.load8_u (local.get $r)) (i32.const 3))
        (then (call $free (i32.load offset=8 (local.get $r)) (i32.add (i32.load offset=12 (local.get $r)) (i32.load offset=20 (local.get $r))))))
      (memory.fill (local.get $r) (i32.const 0) (i32.const 28))
      (i32.store offset=28 (local.get $r) (global.get $free_ids))
      (global.set $free_ids (local.get $id))
      (global.set $live (i32.sub (global.get $live) (i32.const 1)))
      (call $v_push1 (local.get $out) (local.get $id))))
    (local.set $id (i32.add (local.get $id) (i32.const 1))) (br $terms))))
  (call $free (global.get $marks) (global.get $next_id))
  ;; parsed numbers: start over
  (local.set $id (i32.const 5))
  (loop $nums (if (i32.lt_u (local.get $id) (global.get $next_id)) (then
    (if (i32.eq (call $kind (local.get $id)) (i32.const 3)) (then (i32.store offset=24 (call $rec (local.get $id)) (i32.const 0))))
    (local.set $id (i32.add (local.get $id) (i32.const 1))) (br $nums))))
  (global.set $ncache_n (i32.const 0))
  ;; caches of term ids
  (local.set $n (i32.load offset=4 (local.get $out)))
  (if (local.get $n) (then
    ;; STR() of live terms
    (local.set $id (i32.const 5))
    (loop $strs (if (i32.lt_u (local.get $id) (global.get $next_id)) (then
      (local.set $r (call $rec (local.get $id)))
      (if (i32.and (i32.ne (call $kind (local.get $id)) (i32.const 0)) (i32.ne (i32.load offset=28 (local.get $r)) (i32.const 0))) (then
        (if (i32.eqz (call $kind (i32.load offset=28 (local.get $r)))) (then (i32.store offset=28 (local.get $r) (i32.const 0))))))
      (local.set $id (i32.add (local.get $id) (i32.const 1))) (br $strs))))
    (local.set $k (i32.const 0))
    (loop $knowns (if (i32.lt_u (local.get $k) (i32.const 9)) (then
      (if (i32.eqz (call $kind (i32.load offset=8 (i32.add (global.get $knowns) (i32.mul (local.get $k) (i32.const 12))))))
        (then (i32.store offset=8 (i32.add (global.get $knowns) (i32.mul (local.get $k) (i32.const 12))) (i32.const 0))))
      (local.set $k (i32.add (local.get $k) (i32.const 1))) (br $knowns))))
    ;; true and false are made together, when $t_true is 0
    (if (i32.or (i32.eqz (call $kind (global.get $t_true))) (i32.eqz (call $kind (global.get $t_false)))) (then (global.set $t_true (i32.const 0))))
    (if (i32.eqz (call $kind (global.get $t_float))) (then (global.set $t_float (i32.const 0))))
    ;; compiled regular expressions are keyed by term ids: start over
    (if (global.get $re_cache) (then
      (local.set $i (i32.const 0))
      (loop $progs (if (i32.lt_u (local.get $i) (i32.load offset=4 (global.get $re_progs))) (then
        (if (local.tee $v (call $v_at (global.get $re_progs) (local.get $i)))
          (then (call $free (local.get $v) (i32.add (i32.const 12) (i32.shl (i32.load offset=4 (local.get $v)) (i32.const 2))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $progs))))
      (call $set_free (global.get $re_cache))
      (call $v_free (global.get $re_progs))
      (global.set $re_cache (i32.const 0))))))
  (call $res_reserve (i32.shl (local.get $n) (i32.const 2)))
  (memory.copy (global.get $res) (i32.load (local.get $out)) (i32.shl (local.get $n) (i32.const 2)))
  (call $v_free (local.get $out))
  (local.get $n))
