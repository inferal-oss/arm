;; Prepared queries: analysis after parsing, the solution modifiers (grouping,
;; aggregates, ORDER BY, DISTINCT, slices), the query forms, and the exports.

;; --- analysis --------------------------------------------------------------------

(global $subqueries (mut i32) (i32.const 0))   ;; N_ITEM list of the subqueries met

;; Set in bitset b the variables of ref.
(func $an_ref (param $b i32) (param $ref i32)
  (local $s i32) (local $n i32)
  (if (i32.gt_s (local.get $ref) (i32.const 0)) (then (return)))
  (if (i32.gt_s (local.get $ref) (i32.const -0x100000)) (then
    (local.set $s (i32.sub (i32.const -1) (local.get $ref)))
    (local.set $n (i32.add (local.get $b) (i32.shl (i32.shr_u (local.get $s) (i32.const 5)) (i32.const 2))))
    (i32.store (local.get $n) (i32.or (i32.load (local.get $n)) (i32.shl (i32.const 1) (i32.and (local.get $s) (i32.const 31)))))
    (return)))
  (local.set $n (i32.and (local.get $ref) (i32.const 0x7fffffff)))
  (call $an_ref (local.get $b) (i32.load offset=4 (local.get $n)))
  (call $an_ref (local.get $b) (i32.load offset=8 (local.get $n)))
  (call $an_ref (local.get $b) (i32.load offset=12 (local.get $n))))

(func $bits_words (result i32)
  (i32.shl (i32.shr_u (i32.add (global.get $nvars) (i32.const 31)) (i32.const 5)) (i32.const 2)))

;; Compute each group's certain variables (c): those of its own triple
;; patterns and of the groups it joins.
(func $an_group (param $g i32) (result i32)
  (local $b i32) (local $el i32) (local $k i32) (local $i i32) (local $sub i32)
  (local.set $b (call $anew (call $bits_words)))
  (i32.store offset=12 (local.get $g) (local.get $b))
  (local.set $el (i32.load offset=4 (local.get $g)))
  (loop $elements
    (if (local.get $el) (then
      (local.set $k (i32.load (local.get $el)))
      (if (i32.or (i32.eq (local.get $k) (i32.const P_TRIPLE)) (i32.eq (local.get $k) (i32.const P_PATH))) (then
        (call $an_ref (local.get $b) (i32.load offset=4 (local.get $el)))
        (if (i32.eq (local.get $k) (i32.const P_TRIPLE)) (then (call $an_ref (local.get $b) (i32.load offset=8 (local.get $el)))))
        (call $an_ref (local.get $b) (i32.load offset=12 (local.get $el)))))
      (if (i32.eq (local.get $k) (i32.const P_GROUP)) (then
        ;; a joined group: its certain variables are ours too
        (local.set $sub (call $an_group (local.get $el)))
        (local.set $i (i32.const 0))
        (loop $words
          (if (i32.lt_u (local.get $i) (call $bits_words)) (then
            (i32.store (i32.add (local.get $b) (local.get $i))
              (i32.or (i32.load (i32.add (local.get $b) (local.get $i))) (i32.load (i32.add (local.get $sub) (local.get $i)))))
            (local.set $i (i32.add (local.get $i) (i32.const 4)))
            (br $words))))))
      (if (i32.eq (local.get $k) (i32.const P_UNION)) (then (call $an_union (local.get $el))))
      (if (i32.or (i32.or (i32.eq (local.get $k) (i32.const P_OPTIONAL)) (i32.eq (local.get $k) (i32.const P_MINUS)))
                  (i32.eq (local.get $k) (i32.const P_LATERAL)))
        (then (drop (call $an_group (i32.load offset=4 (local.get $el))))))
      (if (i32.eq (local.get $k) (i32.const P_GRAPH)) (then (drop (call $an_group (i32.load offset=8 (local.get $el))))))
      (if (i32.eq (local.get $k) (i32.const P_BIND)) (then
        (call $an_expr (i32.load offset=4 (local.get $el)))
        (call $an_bind (local.get $el))))
      (if (i32.eq (local.get $k) (i32.const P_SUB)) (then
        (call $an_query (i32.load offset=4 (local.get $el)))
        (local.set $sub (call $node3 (i32.const N_ITEM) (i32.load offset=4 (local.get $el)) (i32.const 0) (i32.const 0)))
        (i32.store offset=28 (local.get $sub) (global.get $subqueries))
        (global.set $subqueries (local.get $sub))))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (br $elements))))
  (call $an_filters (i32.load offset=8 (local.get $g)))
  ;; the variables its elements mention (+24), for early filters and joins
  (local.set $sub (call $anew (call $bits_words)))
  (local.set $el (i32.load offset=4 (local.get $g)))
  (loop $mentions
    (if (local.get $el) (then
      (call $mention_elem (local.get $el) (local.get $sub))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (br $mentions))))
  (i32.store offset=24 (local.get $g) (local.get $sub))
  (local.get $b))

;; Set in bitset m every variable element el mentions, at any depth.
(func $mention_elem (param $el i32) (param $m i32)
  (local $k i32) (local $i i32) (local $l i32)
  (local.set $k (i32.load (local.get $el)))
  (if (i32.eq (local.get $k) (i32.const P_TRIPLE)) (then
    (call $an_ref (local.get $m) (i32.load offset=4 (local.get $el)))
    (call $an_ref (local.get $m) (i32.load offset=8 (local.get $el)))
    (call $an_ref (local.get $m) (i32.load offset=12 (local.get $el)))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_PATH)) (then
    (call $an_ref (local.get $m) (i32.load offset=4 (local.get $el)))
    (call $an_ref (local.get $m) (i32.load offset=12 (local.get $el)))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_GROUP)) (then (call $mention_group (local.get $el) (local.get $m)) (return)))
  (if (i32.eq (local.get $k) (i32.const P_UNION)) (then
    (call $mention_elem (i32.load offset=4 (local.get $el)) (local.get $m))
    (call $mention_elem (i32.load offset=8 (local.get $el)) (local.get $m))
    (return)))
  (if (i32.or (i32.or (i32.eq (local.get $k) (i32.const P_OPTIONAL)) (i32.eq (local.get $k) (i32.const P_MINUS)))
              (i32.eq (local.get $k) (i32.const P_LATERAL)))
    (then (call $mention_group (i32.load offset=4 (local.get $el)) (local.get $m)) (return)))
  (if (i32.eq (local.get $k) (i32.const P_GRAPH)) (then
    (call $an_ref (local.get $m) (i32.load offset=4 (local.get $el)))
    (call $mention_group (i32.load offset=8 (local.get $el)) (local.get $m))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_BIND)) (then
    (drop (call $mention_expr (i32.load offset=4 (local.get $el)) (local.get $m)))
    (call $an_ref (local.get $m) (i32.sub (i32.const -1) (i32.load offset=8 (local.get $el))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_VALUES)) (then
    (loop $vars
      (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $el))) (then
        (call $an_ref (local.get $m) (call $vref (i32.load (i32.add (i32.load offset=8 (local.get $el)) (i32.shl (local.get $i) (i32.const 2))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $vars))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_SUB)) (then
    ;; its WHERE and the slots it projects to (inner slots are its own)
    (call $mention_group (i32.load offset=12 (i32.load offset=4 (local.get $el))) (local.get $m))
    (local.set $l (i32.load offset=72 (i32.load offset=4 (local.get $el))))
    (loop $items
      (if (local.get $l) (then
        (call $an_ref (local.get $m) (i32.sub (i32.const -1) (i32.load offset=4 (local.get $l))))
        (call $an_ref (local.get $m) (i32.sub (i32.const -1) (i32.load offset=8 (local.get $l))))
        (local.set $l (i32.load offset=28 (local.get $l)))
        (br $items))))
    (local.set $l (i32.load offset=8 (i32.load offset=4 (local.get $el))))
    (loop $proj
      (if (local.get $l) (then
        (call $an_ref (local.get $m) (i32.sub (i32.const -1) (i32.load offset=4 (local.get $l))))
        (local.set $l (i32.load offset=28 (local.get $l)))
        (br $proj))))
    (return))))

;; A group's elements and filters.
(func $mention_group (param $g i32) (param $m i32)
  (local $el i32)
  (local.set $el (i32.load offset=4 (local.get $g)))
  (loop $elements
    (if (local.get $el) (then
      (call $mention_elem (local.get $el) (local.get $m))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (br $elements))))
  (local.set $el (i32.load offset=8 (local.get $g)))
  (loop $filters
    (if (local.get $el) (then
      (drop (call $mention_expr (i32.load offset=4 (local.get $el)) (local.get $m)))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (br $filters)))))

;; Set in m the variables of expression e; whether it is free of RAND, BNODE
;; and UUID (whose values differ from call to call).
(func $mention_expr (param $e i32) (param $m i32) (result i32)
  (local $k i32) (local $f i32) (local $ok i32)
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_VAR)) (then
    (call $an_ref (local.get $m) (i32.sub (i32.const -1) (i32.load offset=4 (local.get $e))))
    (return (i32.const 1))))
  (if (i32.eq (local.get $k) (i32.const E_EXISTS)) (then
    (call $mention_group (i32.load offset=4 (local.get $e)) (local.get $m))
    (return (i32.const 1))))
  (if (i32.ne (local.get $k) (i32.const E_CALL)) (then (return (i32.eq (local.get $k) (i32.const E_CONST)))))
  (local.set $f (i32.load offset=4 (local.get $e)))
  (local.set $ok (i32.eqz (i32.or (i32.or (i32.eq (local.get $f) (i32.const F_RAND)) (i32.eq (local.get $f) (i32.const F_BNODE)))
                                  (i32.or (i32.eq (local.get $f) (i32.const F_UUID)) (i32.eq (local.get $f) (i32.const F_STRUUID))))))
  (local.set $e (i32.load offset=8 (local.get $e)))
  (loop $args
    (if (local.get $e) (then
      (local.set $ok (i32.and (local.get $ok) (call $mention_expr (local.get $e) (local.get $m))))
      (local.set $e (i32.load offset=28 (local.get $e)))
      (br $args))))
  (local.get $ok))

;; A BIND whose expression depends on its variables alone (no EXISTS, no
;; RAND, BNODE, UUID): the list of those (+12: count, then slots), so rows
;; with the same values share one evaluation.
(func $an_bind (param $el i32)
  (local $b i32)
  (local.set $b (call $anew (i32.shl (i32.add (global.get $nvars) (i32.const 1)) (i32.const 2))))
  (if (call $pure_vars (i32.load offset=4 (local.get $el)) (local.get $b))
    (then (i32.store offset=12 (local.get $el) (local.get $b)))))

;; Add the variables of e to list b; whether e is a function of them.
(func $pure_vars (param $e i32) (param $b i32) (result i32)
  (local $k i32) (local $s i32) (local $i i32) (local $f i32)
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_CONST)) (then (return (i32.const 1))))
  (if (i32.eq (local.get $k) (i32.const E_VAR)) (then
    (local.set $s (i32.load offset=4 (local.get $e)))
    (loop $known
      (if (i32.lt_u (local.get $i) (i32.load (local.get $b))) (then
        (if (i32.eq (i32.load offset=4 (i32.add (local.get $b) (i32.shl (local.get $i) (i32.const 2)))) (local.get $s)) (then (return (i32.const 1))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $known))))
    (i32.store offset=4 (i32.add (local.get $b) (i32.shl (local.get $i) (i32.const 2))) (local.get $s))
    (i32.store (local.get $b) (i32.add (local.get $i) (i32.const 1)))
    (return (i32.const 1))))
  (if (i32.ne (local.get $k) (i32.const E_CALL)) (then (return (i32.const 0))))
  (local.set $f (i32.load offset=4 (local.get $e)))
  (if (i32.or (i32.or (i32.eq (local.get $f) (i32.const F_RAND)) (i32.eq (local.get $f) (i32.const F_BNODE)))
              (i32.or (i32.eq (local.get $f) (i32.const F_UUID)) (i32.eq (local.get $f) (i32.const F_STRUUID))))
    (then (return (i32.const 0))))
  (local.set $e (i32.load offset=8 (local.get $e)))
  (loop $args
    (if (local.get $e) (then
      (if (i32.eqz (call $pure_vars (local.get $e) (local.get $b))) (then (return (i32.const 0))))
      (local.set $e (i32.load offset=28 (local.get $e)))
      (br $args))))
  (i32.const 1))

(func $an_union (param $n i32)
  (if (i32.eq (i32.load (local.get $n)) (i32.const P_UNION))
    (then
      (call $an_union (i32.load offset=4 (local.get $n)))
      (call $an_union (i32.load offset=8 (local.get $n))))
    (else (drop (call $an_group (local.get $n))))))

;; Each filter free of RAND, BNODE and UUID gets the list of the variables it
;; mentions at any depth (+8: count, then slots; EXISTS patterns included), so
;; a group can apply it as soon as its rows decide them.
(func $an_filters (param $f i32)
  (local $b i32) (local $l i32) (local $s i32) (local $n i32)
  (loop $each
    (if (local.get $f) (then
      (call $an_expr (i32.load offset=4 (local.get $f)))
      (local.set $b (call $anew (call $bits_words)))
      (if (call $mention_expr (i32.load offset=4 (local.get $f)) (local.get $b)) (then
        (local.set $l (call $anew (i32.shl (i32.add (global.get $nvars) (i32.const 1)) (i32.const 2))))
        (local.set $n (i32.const 0))
        (local.set $s (i32.const 0))
        (loop $slots
          (if (i32.lt_u (local.get $s) (global.get $nvars)) (then
            (if (call $bit (local.get $b) (local.get $s)) (then
              (local.set $n (i32.add (local.get $n) (i32.const 1)))
              (i32.store (i32.add (local.get $l) (i32.shl (local.get $n) (i32.const 2))) (local.get $s))))
            (local.set $s (i32.add (local.get $s) (i32.const 1)))
            (br $slots))))
        (i32.store (local.get $l) (local.get $n))
        (i32.store offset=8 (local.get $f) (local.get $l))))
      (local.set $f (i32.load offset=28 (local.get $f)))
      (br $each)))))

(func $an_expr (param $e i32)
  (local $k i32) (local $a i32)
  (if (i32.eqz (local.get $e)) (then (return)))
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_EXISTS)) (then (drop (call $an_group (i32.load offset=4 (local.get $e)))) (return)))
  (if (i32.eq (local.get $k) (i32.const E_AGG)) (then (call $an_expr (i32.load offset=8 (local.get $e))) (return)))
  (if (i32.eq (local.get $k) (i32.const E_CALL)) (then
    (local.set $a (i32.load offset=8 (local.get $e)))
    (loop $args
      (if (local.get $a) (then
        (call $an_expr (local.get $a))
        (local.set $a (i32.load offset=28 (local.get $a)))
        (br $args)))))))

;; Expressions in N_ITEM lists (a or b is the expression).
(func $an_items (param $l i32) (param $off i32)
  (loop $each
    (if (local.get $l) (then
      (if (i32.ne (i32.load (i32.add (local.get $l) (local.get $off))) (i32.const 0)) (then
        (if (i32.gt_u (i32.load (i32.add (local.get $l) (local.get $off))) (i32.const 0x1000)) (then
          (call $an_expr (i32.load (i32.add (local.get $l) (local.get $off))))))))
      (local.set $l (i32.load offset=28 (local.get $l)))
      (br $each)))))

(func $an_query (param $q i32)
  (drop (call $an_group (i32.load offset=12 (local.get $q))))
  (call $an_items (i32.load offset=8 (local.get $q)) (i32.const 8))
  (call $an_items (i32.load offset=16 (local.get $q)) (i32.const 4))
  (call $an_filters (i32.load offset=20 (local.get $q)))
  (call $an_items (i32.load offset=24 (local.get $q)) (i32.const 4)))

;; The SELECT projection of q, as slots (+100 count, +104 slots); slots: the
;; slot table being built (entries by slot).
(func $an_projection (param $q i32) (param $slots i32)
  (local $e i32) (local $proj i32) (local $n i32) (local $s i32) (local $item i32)
  (local.set $proj (call $anew (i32.shl (i32.add (global.get $nvars) (i32.const 1)) (i32.const 2))))
  ;; SELECT * of the query (a subquery's SELECT * has its items listed)
  (if (i32.and (i32.eqz (i32.load offset=8 (local.get $q))) (i32.ne (i32.and (i32.load offset=4 (local.get $q)) (i32.const Q_STAR)) (i32.const 0)))
    (then
      (loop $all
        (if (i32.lt_u (local.get $s) (global.get $nvars)) (then
          (local.set $e (i32.load (i32.add (local.get $slots) (i32.shl (local.get $s) (i32.const 2)))))
          (if (i32.and (i32.eqz (i32.load offset=12 (local.get $e))) (i32.eqz (i32.and (i32.load offset=20 (local.get $e)) (i32.const V_HIDDEN)))) (then
            (i32.store (i32.add (local.get $proj) (i32.shl (local.get $n) (i32.const 2))) (local.get $s))
            (local.set $n (i32.add (local.get $n) (i32.const 1)))))
          (local.set $s (i32.add (local.get $s) (i32.const 1)))
          (br $all)))))
    (else
      (local.set $item (i32.load offset=8 (local.get $q)))
      (loop $items
        (if (local.get $item) (then
          (i32.store (i32.add (local.get $proj) (i32.shl (local.get $n) (i32.const 2))) (i32.load offset=4 (local.get $item)))
          (local.set $n (i32.add (local.get $n) (i32.const 1)))
          (local.set $item (i32.load offset=28 (local.get $item)))
          (br $items))))))
  (i32.store offset=100 (local.get $q) (local.get $n))
  (i32.store offset=104 (local.get $q) (local.get $proj)))

;; After parsing: slot table, parameters, projected variables.
(func $analyze (param $q i32) (result i32)
  (global.set $subqueries (i32.const 0))
  (call $an_query (local.get $q))
  (call $analyze_slots (local.get $q)))

(func $analyze_slots (param $q i32) (result i32)
  (local $e i32) (local $slots i32) (local $item i32)
  (i32.store offset=52 (local.get $q) (global.get $nvars))
  (local.set $slots (call $anew (i32.shl (i32.add (global.get $nvars) (i32.const 1)) (i32.const 2))))
  (local.set $e (global.get $vars))
  (loop $entries
    (if (local.get $e) (then
      (i32.store (i32.add (local.get $slots) (i32.shl (i32.load offset=8 (local.get $e)) (i32.const 2))) (local.get $e))
      (local.set $e (i32.load offset=16 (local.get $e)))
      (br $entries))))
  (i32.store offset=56 (local.get $q) (local.get $slots))
  (i32.store offset=60 (local.get $q) (call $anew (i32.shl (i32.add (global.get $nvars) (i32.const 1)) (i32.const 2))))
  (call $an_projection (local.get $q) (local.get $slots))
  ;; subqueries: their projections too
  (local.set $item (global.get $subqueries))
  (loop $subs (if (local.get $item) (then
    (call $an_projection (i32.load offset=4 (local.get $item)) (local.get $slots))
    (local.set $item (i32.load offset=28 (local.get $item))) (br $subs))))

  (i32.const 1))

;; --- solutions -------------------------------------------------------------------

;; The solutions of (sub)query q after its modifiers, as full rows.
(func $solve (param $q i32) (result i32)
  (local $one i32) (local $t i32)
  (local.set $one (call $t_one (global.get $params)))
  (local.set $t (call $eval_group (i32.load offset=12 (local.get $q)) (local.get $one) (i32.const 0)))
  (call $t_free (local.get $one))
  (call $solve_tail (local.get $q) (local.get $t)))

;; What follows a query's WHERE, on its solutions t (a compiled subquery's too):
;; grouping, the trailing VALUES, SELECT expressions, ORDER BY, DISTINCT, slice.
(func $solve_tail (param $q i32) (param $t i32) (result i32)
  (local $t2 i32) (local $item i32) (local $i i32) (local $row i32) (local $at i32)
  (if (i32.and (i32.load offset=4 (local.get $q)) (i32.const Q_GROUPED)) (then
    (local.set $t2 (call $group (local.get $q) (local.get $t)))
    (call $t_free (local.get $t))
    (local.set $t (local.get $t2))))
  ;; the trailing VALUES joins the (grouped) solutions
  (if (i32.load offset=40 (local.get $q)) (then
    (local.set $t2 (call $t_new))
    (call $join_values (i32.load offset=40 (local.get $q)) (local.get $t) (local.get $t2))
    (call $t_free (local.get $t))
    (local.set $t (local.get $t2))))
  ;; SELECT expressions, in order, row by row
  (local.set $i (i32.const 0))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $row (call $t_row (local.get $t) (local.get $i)))
      (local.set $item (i32.load offset=8 (local.get $q)))
      (loop $items
        (if (local.get $item) (then
          (if (i32.load offset=8 (local.get $item)) (then
            (local.set $at (i32.add (local.get $row) (i32.shl (i32.load offset=4 (local.get $item)) (i32.const 2))))
            (if (i32.eqz (i32.load (local.get $at))) (then (i32.store (local.get $at) (call $ev (i32.load offset=8 (local.get $item)) (local.get $row)))))))
          (local.set $item (i32.load offset=28 (local.get $item)))
          (br $items))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (if (i32.load offset=24 (local.get $q)) (then (call $sort (local.get $t) (i32.load offset=24 (local.get $q)))))
  (if (i32.and (i32.eq (i32.load (local.get $q)) (i32.const 1)) (i32.and (i32.load offset=4 (local.get $q)) (i32.const 3)))
    (then (call $distinct (local.get $t) (local.get $q))))
  (call $slice (local.get $t) (i32.load offset=32 (local.get $q)) (i32.load offset=28 (local.get $q)))
  (local.get $t))

;; Keep rows [offset, offset + limit) (limit -1: all).
(func $slice (param $t i32) (param $off i32) (param $lim i32)
  (local $n i32)
  (local.set $n (i32.load offset=4 (local.get $t)))
  (if (i32.ge_u (local.get $off) (local.get $n)) (then (i32.store offset=4 (local.get $t) (i32.const 0)) (return)))
  (if (local.get $off) (then
    (memory.copy (i32.load (local.get $t)) (call $t_row (local.get $t) (local.get $off)) (i32.mul (i32.sub (local.get $n) (local.get $off)) (global.get $W)))
    (local.set $n (i32.sub (local.get $n) (local.get $off)))))
  (if (i32.and (i32.ge_s (local.get $lim) (i32.const 0)) (i32.lt_u (local.get $lim) (local.get $n))) (then (local.set $n (local.get $lim))))
  (i32.store offset=4 (local.get $t) (local.get $n)))

;; DISTINCT: the first row of each projection.
(func $distinct (param $t i32) (param $q i32)
  (local $set i32) (local $key i32) (local $i i32) (local $k i32) (local $j i32) (local $np i32) (local $proj i32) (local $row i32)
  (local.set $np (i32.load offset=100 (local.get $q)))
  (local.set $proj (i32.load offset=104 (local.get $q)))
  (local.set $set (call $set_make (select (local.get $np) (i32.const 1) (local.get $np))))
  (local.set $key (call $row_get))
  (i32.store (local.get $key) (i32.const 0))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $row (call $t_row (local.get $t) (local.get $i)))
      (local.set $j (i32.const 0))
      (loop $cols
        (if (i32.lt_u (local.get $j) (local.get $np)) (then
          (i32.store (i32.add (local.get $key) (i32.shl (local.get $j) (i32.const 2)))
            (i32.load (i32.add (local.get $row) (i32.shl (i32.load (i32.add (local.get $proj) (i32.shl (local.get $j) (i32.const 2)))) (i32.const 2)))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $cols))))
      (drop (call $set_add (local.get $set) (local.get $key)))
      (if (global.get $set_new) (then
        (if (i32.ne (local.get $i) (local.get $k)) (then (memory.copy (call $t_row (local.get $t) (local.get $k)) (local.get $row) (global.get $W))))
        (local.set $k (i32.add (local.get $k) (i32.const 1)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (i32.store offset=4 (local.get $t) (local.get $k))
  (call $row_put (local.get $key))
  (call $set_free (local.get $set)))

;; --- ORDER BY --------------------------------------------------------------------

(global $o_keys (mut i32) (i32.const 0))
(global $o_nc (mut i32) (i32.const 0))
(global $o_desc (mut i32) (i32.const 0))   ;; a byte per condition

(func $o_cmp (param $a i32) (param $b i32) (result i32)
  (local $c i32) (local $r i32)
  (loop $conds
    (if (i32.lt_u (local.get $c) (global.get $o_nc)) (then
      (local.set $r (call $order_cmp
        (i32.load (i32.add (global.get $o_keys) (i32.shl (i32.add (i32.mul (local.get $a) (global.get $o_nc)) (local.get $c)) (i32.const 2))))
        (i32.load (i32.add (global.get $o_keys) (i32.shl (i32.add (i32.mul (local.get $b) (global.get $o_nc)) (local.get $c)) (i32.const 2))))))
      (if (local.get $r) (then
        (return (select (i32.sub (i32.const 0) (local.get $r)) (local.get $r) (i32.load8_u (i32.add (global.get $o_desc) (local.get $c)))))))
      (local.set $c (i32.add (local.get $c) (i32.const 1)))
      (br $conds))))
  (i32.const 0))

;; Sort t's rows by the ORDER BY conditions (stable merge sort of indexes).
(func $sort (param $t i32) (param $conds i32)
  (local $n i32) (local $nc i32) (local $c i32) (local $i i32) (local $l i32) (local $idx i32) (local $tmp i32)
  (local $w i32) (local $lo i32) (local $mid i32) (local $hi i32) (local $a i32) (local $b i32) (local $k i32) (local $out i32)
  (local.set $n (i32.load offset=4 (local.get $t)))
  (if (i32.lt_u (local.get $n) (i32.const 2)) (then (return)))
  (local.set $l (local.get $conds))
  (loop $count (if (local.get $l) (then (local.set $nc (i32.add (local.get $nc) (i32.const 1))) (local.set $l (i32.load offset=28 (local.get $l))) (br $count))))
  (global.set $o_nc (local.get $nc))
  (global.set $o_keys (call $alloc (i32.shl (i32.mul (local.get $n) (local.get $nc)) (i32.const 2))))
  (global.set $o_desc (call $alloc (local.get $nc)))
  (local.set $l (local.get $conds))
  (loop $cond
    (if (local.get $l) (then
      (i32.store8 (i32.add (global.get $o_desc) (local.get $c)) (i32.load offset=8 (local.get $l)))
      (local.set $i (i32.const 0))
      (loop $rows
        (if (i32.lt_u (local.get $i) (local.get $n)) (then
          (i32.store (i32.add (global.get $o_keys) (i32.shl (i32.add (i32.mul (local.get $i) (local.get $nc)) (local.get $c)) (i32.const 2)))
            (call $ev (i32.load offset=4 (local.get $l)) (call $t_row (local.get $t) (local.get $i))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $rows))))
      (local.set $c (i32.add (local.get $c) (i32.const 1)))
      (local.set $l (i32.load offset=28 (local.get $l)))
      (br $cond))))
  (local.set $idx (call $alloc (i32.shl (local.get $n) (i32.const 2))))
  (local.set $tmp (call $alloc (i32.shl (local.get $n) (i32.const 2))))
  (local.set $i (i32.const 0))
  (loop $init (if (i32.lt_u (local.get $i) (local.get $n)) (then
    (i32.store (i32.add (local.get $idx) (i32.shl (local.get $i) (i32.const 2))) (local.get $i))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $init))))
  (local.set $w (i32.const 1))
  (loop $widths
    (if (i32.lt_u (local.get $w) (local.get $n)) (then
      (local.set $lo (i32.const 0))
      (loop $runs
        (if (i32.lt_u (local.get $lo) (local.get $n)) (then
          (local.set $mid (i32.add (local.get $lo) (local.get $w)))
          (if (i32.gt_u (local.get $mid) (local.get $n)) (then (local.set $mid (local.get $n))))
          (local.set $hi (i32.add (local.get $mid) (local.get $w)))
          (if (i32.gt_u (local.get $hi) (local.get $n)) (then (local.set $hi (local.get $n))))
          (local.set $a (local.get $lo))
          (local.set $b (local.get $mid))
          (local.set $k (local.get $lo))
          (loop $merge
            (if (i32.lt_u (local.get $k) (local.get $hi)) (then
              ;; take from the left run (short-circuit: no comparison past a run's end)
              (if (if (result i32) (i32.ge_u (local.get $a) (local.get $mid)) (then (i32.const 0))
                    (else (if (result i32) (i32.ge_u (local.get $b) (local.get $hi)) (then (i32.const 1))
                      (else (i32.le_s (call $o_cmp (i32.load (i32.add (local.get $idx) (i32.shl (local.get $a) (i32.const 2))))
                                                   (i32.load (i32.add (local.get $idx) (i32.shl (local.get $b) (i32.const 2))))) (i32.const 0))))))
                (then
                  (i32.store (i32.add (local.get $tmp) (i32.shl (local.get $k) (i32.const 2))) (i32.load (i32.add (local.get $idx) (i32.shl (local.get $a) (i32.const 2)))))
                  (local.set $a (i32.add (local.get $a) (i32.const 1))))
                (else
                  (i32.store (i32.add (local.get $tmp) (i32.shl (local.get $k) (i32.const 2))) (i32.load (i32.add (local.get $idx) (i32.shl (local.get $b) (i32.const 2)))))
                  (local.set $b (i32.add (local.get $b) (i32.const 1)))))
              (local.set $k (i32.add (local.get $k) (i32.const 1)))
              (br $merge))))
          (local.set $lo (local.get $hi))
          (br $runs))))
      ;; swap idx and tmp
      (local.set $a (local.get $idx))
      (local.set $idx (local.get $tmp))
      (local.set $tmp (local.get $a))
      (local.set $w (i32.shl (local.get $w) (i32.const 1)))
      (br $widths))))
  ;; rows in order
  (local.set $out (call $alloc (i32.mul (local.get $n) (global.get $W))))
  (local.set $i (i32.const 0))
  (loop $copy (if (i32.lt_u (local.get $i) (local.get $n)) (then
    (memory.copy (i32.add (local.get $out) (i32.mul (local.get $i) (global.get $W)))
                 (call $t_row (local.get $t) (i32.load (i32.add (local.get $idx) (i32.shl (local.get $i) (i32.const 2))))) (global.get $W))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $copy))))
  (memory.copy (i32.load (local.get $t)) (local.get $out) (i32.mul (local.get $n) (global.get $W)))
  (call $free (local.get $out) (i32.mul (local.get $n) (global.get $W)))
  (call $free (local.get $idx) (i32.shl (local.get $n) (i32.const 2)))
  (call $free (local.get $tmp) (i32.shl (local.get $n) (i32.const 2)))
  (call $free (global.get $o_keys) (i32.shl (i32.mul (local.get $n) (local.get $nc)) (i32.const 2)))
  (call $free (global.get $o_desc) (local.get $nc)))

;; --- grouping and aggregates -------------------------------------------------------

;; GROUP BY and aggregates: one row per group, then HAVING.
(func $group (param $q i32) (param $t i32) (result i32)
  (local $n i32) (local $nk i32) (local $set i32) (local $key i32) (local $gid i32) (local $i i32) (local $k i32)
  (local $item i32) (local $ng i32) (local $start i32) (local $order i32) (local $out i32) (local $row i32) (local $g i32)
  (local $agg i32) (local $from i32) (local $to i32)
  (local.set $n (i32.load offset=4 (local.get $t)))
  (local.set $item (i32.load offset=16 (local.get $q)))
  (loop $count (if (local.get $item) (then (local.set $nk (i32.add (local.get $nk) (i32.const 1))) (local.set $item (i32.load offset=28 (local.get $item))) (br $count))))
  (local.set $set (call $set_make (select (local.get $nk) (i32.const 1) (local.get $nk))))
  (local.set $key (call $row_get))
  (i32.store (local.get $key) (i32.const 0))
  (local.set $gid (call $alloc (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2))))
  (loop $rows
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $item (i32.load offset=16 (local.get $q)))
      (local.set $k (i32.const 0))
      (loop $keys
        (if (local.get $item) (then
          (i32.store (i32.add (local.get $key) (i32.shl (local.get $k) (i32.const 2)))
            (call $ev (i32.load offset=4 (local.get $item)) (call $t_row (local.get $t) (local.get $i))))
          (local.set $k (i32.add (local.get $k) (i32.const 1)))
          (local.set $item (i32.load offset=28 (local.get $item)))
          (br $keys))))
      (i32.store (i32.add (local.get $gid) (i32.shl (local.get $i) (i32.const 2))) (call $set_add (local.get $set) (local.get $key)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (local.set $ng (i32.load offset=4 (local.get $set)))
  ;; no GROUP BY: one group, even of no rows
  (if (i32.and (i32.eqz (local.get $nk)) (i32.eqz (local.get $ng))) (then
    (drop (call $set_add (local.get $set) (local.get $key)))
    (local.set $ng (i32.const 1))))
  ;; rows by group: counting sort
  (local.set $start (call $alloc0 (i32.shl (i32.add (local.get $ng) (i32.const 1)) (i32.const 2))))
  (local.set $order (call $alloc (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2))))
  (local.set $i (i32.const 0))
  (loop $counts (if (i32.lt_u (local.get $i) (local.get $n)) (then
    (local.set $g (i32.add (local.get $start) (i32.shl (i32.add (i32.load (i32.add (local.get $gid) (i32.shl (local.get $i) (i32.const 2)))) (i32.const 1)) (i32.const 2))))
    (i32.store (local.get $g) (i32.add (i32.load (local.get $g)) (i32.const 1)))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $counts))))
  (local.set $i (i32.const 1))
  (loop $sums (if (i32.le_u (local.get $i) (local.get $ng)) (then
    (i32.store (i32.add (local.get $start) (i32.shl (local.get $i) (i32.const 2)))
      (i32.add (i32.load (i32.add (local.get $start) (i32.shl (local.get $i) (i32.const 2))))
               (i32.load (i32.add (local.get $start) (i32.shl (i32.sub (local.get $i) (i32.const 1)) (i32.const 2))))))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $sums))))
  ;; place rows (start[g] is advanced to the end of group g)
  (local.set $i (i32.const 0))
  (loop $place (if (i32.lt_u (local.get $i) (local.get $n)) (then
    (local.set $g (i32.add (local.get $start) (i32.shl (i32.load (i32.add (local.get $gid) (i32.shl (local.get $i) (i32.const 2)))) (i32.const 2))))
    (i32.store (i32.add (local.get $order) (i32.shl (i32.load (local.get $g)) (i32.const 2))) (local.get $i))
    (i32.store (local.get $g) (i32.add (i32.load (local.get $g)) (i32.const 1)))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $place))))
  (local.set $out (call $t_new))
  (local.set $g (i32.const 0))
  (loop $groups
    (if (i32.lt_u (local.get $g) (local.get $ng)) (then
      (local.set $to (i32.load (i32.add (local.get $start) (i32.shl (local.get $g) (i32.const 2)))))
      (local.set $from (if (result i32) (local.get $g) (then (i32.load (i32.add (local.get $start) (i32.shl (i32.sub (local.get $g) (i32.const 1)) (i32.const 2))))) (else (i32.const 0))))
      (local.set $row (call $t_add (local.get $out)))
      (memory.copy (local.get $row) (global.get $params) (global.get $W))
      ;; group keys
      (local.set $item (i32.load offset=16 (local.get $q)))
      (local.set $k (i32.const 0))
      (loop $keys
        (if (local.get $item) (then
          (i32.store (i32.add (local.get $row) (i32.shl (i32.load offset=8 (local.get $item)) (i32.const 2)))
            (i32.load (i32.add (call $set_key (local.get $set) (local.get $g)) (i32.shl (local.get $k) (i32.const 2)))))
          (local.set $k (i32.add (local.get $k) (i32.const 1)))
          (local.set $item (i32.load offset=28 (local.get $item)))
          (br $keys))))
      ;; aggregates
      (local.set $agg (i32.load offset=68 (local.get $q)))
      (loop $aggs
        (if (local.get $agg) (then
          (i32.store (i32.add (local.get $row) (i32.shl (i32.load offset=20 (local.get $agg)) (i32.const 2)))
            (call $agg_value (local.get $agg) (local.get $t) (local.get $order) (local.get $from) (local.get $to)))
          (local.set $agg (i32.load offset=24 (local.get $agg)))
          (br $aggs))))
      (local.set $g (i32.add (local.get $g) (i32.const 1)))
      (br $groups))))
  (call $row_put (local.get $key))
  (call $set_free (local.get $set))
  (call $free (local.get $gid) (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2)))
  (call $free (local.get $start) (i32.shl (i32.add (local.get $ng) (i32.const 1)) (i32.const 2)))
  (call $free (local.get $order) (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2)))
  (call $filter (local.get $out) (i32.load offset=20 (local.get $q)))
  (local.get $out))

;; Aggregate agg over rows order[from..to) of t.
(func $agg_value (param $agg i32) (param $t i32) (param $order i32) (param $from i32) (param $to i32) (result i32)
  (local $f i32) (local $i i32) (local $v i32) (local $acc i32) (local $n i32) (local $set i32) (local $row i32) (local $err i32)
  (local $w i32) (local $sep i32) (local $first i32)
  (local.set $f (i32.load offset=4 (local.get $agg)))
  (if (i32.load offset=12 (local.get $agg)) (then
    (local.set $set (call $set_make (select (i32.const 1) (i32.shr_u (global.get $W) (i32.const 2)) (i32.load offset=8 (local.get $agg)))))))
  (if (i32.eq (local.get $f) (i32.const A_SUM)) (then (local.set $acc (call $int_term (i64.const 0)))))
  (if (i32.eq (local.get $f) (i32.const A_AVG)) (then (local.set $acc (call $int_term (i64.const 0)))))
  (if (i32.eq (local.get $f) (i32.const A_CONCAT)) (then (local.set $sep (i32.load offset=16 (local.get $agg)))))
  (local.set $first (i32.const 1))
  (local.set $i (local.get $from))
  (loop $rows
    (if (i32.lt_u (local.get $i) (local.get $to)) (then
      (local.set $row (call $t_row (local.get $t) (i32.load (i32.add (local.get $order) (i32.shl (local.get $i) (i32.const 2))))))
      (block $next
        (if (i32.load offset=8 (local.get $agg))
          (then
            (local.set $v (call $ev (i32.load offset=8 (local.get $agg)) (local.get $row)))
            (if (i32.eqz (local.get $v)) (then
              (local.set $err (i32.const 1))
              (br $next)))
            (if (local.get $set) (then (br_if $next (i32.eqz (call $set_add1 (local.get $set) (local.get $v)))))))
          (else
            ;; COUNT(*)
            (if (local.get $set) (then
              (drop (call $set_add (local.get $set) (local.get $row)))
              (br_if $next (i32.eqz (global.get $set_new)))))))
        (local.set $n (i32.add (local.get $n) (i32.const 1)))
        (if (i32.or (i32.eq (local.get $f) (i32.const A_SUM)) (i32.eq (local.get $f) (i32.const A_AVG))) (then
          (if (local.get $acc) (then (local.set $acc (call $arith (i32.const F_ADD) (local.get $acc) (local.get $v)))))))
        (if (i32.eq (local.get $f) (i32.const A_MIN)) (then
          (if (i32.or (i32.eqz (local.get $acc)) (i32.lt_s (call $order_cmp (local.get $v) (local.get $acc)) (i32.const 0))) (then (local.set $acc (local.get $v))))))
        (if (i32.eq (local.get $f) (i32.const A_MAX)) (then
          (if (i32.or (i32.eqz (local.get $acc)) (i32.gt_s (call $order_cmp (local.get $v) (local.get $acc)) (i32.const 0))) (then (local.set $acc (local.get $v))))))
        (if (i32.eq (local.get $f) (i32.const A_SAMPLE)) (then
          (if (i32.eqz (local.get $acc)) (then (local.set $acc (local.get $v))))))
        (if (i32.eq (local.get $f) (i32.const A_CONCAT)) (then
          (if (i32.ne (call $kind (local.get $v)) (i32.const 3)) (then (local.set $err (i32.const 1)) (br $next)))
          (if (i32.eqz (local.get $w)) (then (local.set $w (call $v_new))))
          (call $v_push1 (local.get $w) (local.get $v)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (if (local.get $set) (then (call $set_free (local.get $set))))
  (if (i32.eq (local.get $f) (i32.const A_COUNT)) (then (return (call $int_term (i64.extend_i32_u (local.get $n))))))
  (if (i32.eq (local.get $f) (i32.const A_SUM)) (then (return (select (i32.const 0) (local.get $acc) (local.get $err)))))
  (if (i32.eq (local.get $f) (i32.const A_AVG)) (then
    (if (local.get $err) (then (return (i32.const 0))))
    (if (i32.eqz (local.get $n)) (then (return (call $int_term (i64.const 0)))))
    (return (call $arith (i32.const F_DIV) (local.get $acc) (call $int_term (i64.extend_i32_u (local.get $n)))))))
  (if (i32.eq (local.get $f) (i32.const A_CONCAT)) (then
    (call $xb_reset)
    (if (local.get $w) (then
      (local.set $i (i32.const 0))
      (loop $values
        (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $w))) (then
          (if (local.get $i) (then
            (if (local.get $sep) (then (call $xb_put_term (local.get $sep))) (else (call $xb_byte (i32.const ' '))))))
          (call $xb_put_term (call $v_at (local.get $w) (local.get $i)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $values))))
      (call $v_free (local.get $w))))
    (if (local.get $err) (then (return (i32.const 0))))
    (return (call $xb_string (i32.const 0)))))
  (local.get $acc))

;; --- query forms -----------------------------------------------------------------

(global $out (mut i32) (i32.const 0))      ;; result vector
(global $out_rows (mut i32) (i32.const 0)) ;; SELECT rows written
(global $gl_n (mut i32) (i32.const 0))
(global $dg_bytes (mut i32) (i32.const 0))
(global $tset (mut i32) (i32.const 0))     ;; CONSTRUCT / DESCRIBE triples already written

;; A template ref's term in row (making triple terms).
(func $inst_ref (param $ref i32) (param $row i32) (result i32)
  (local $n i32) (local $s i32) (local $p i32) (local $o i32)
  (if (i32.ge_s (local.get $ref) (i32.const -0x100000)) (then (return (call $resolve_ref (local.get $ref) (local.get $row)))))
  (local.set $n (i32.and (local.get $ref) (i32.const 0x7fffffff)))
  (if (i32.eqz (local.tee $s (call $inst_ref (i32.load offset=4 (local.get $n)) (local.get $row)))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $p (call $inst_ref (i32.load offset=8 (local.get $n)) (local.get $row)))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $o (call $inst_ref (i32.load offset=12 (local.get $n)) (local.get $row)))) (then (return (i32.const 0))))
  (if (i32.or (i32.gt_u (call $kind (local.get $s)) (i32.const 2)) (i32.ne (call $kind (local.get $p)) (i32.const 1))) (then (return (i32.const 0))))
  (call $intern_triple (local.get $s) (local.get $p) (local.get $o) (i32.const 1)))

;; Write triple (s, p, o) once.
(func $emit_triple (param $s i32) (param $p i32) (param $o i32)
  (local $k i32)
  (local.set $k (call $row_get))
  (i32.store (local.get $k) (local.get $s))
  (i32.store offset=4 (local.get $k) (local.get $p))
  (i32.store offset=8 (local.get $k) (local.get $o))
  (drop (call $set_add (global.get $tset) (local.get $k)))
  (if (global.get $set_new) (then
    (call $v_push2 (global.get $out) (local.get $s) (local.get $p))
    (call $v_push1 (global.get $out) (local.get $o))))
  (call $row_put (local.get $k)))

(func $construct (param $q i32) (param $t i32)
  (local $i i32) (local $row i32) (local $tp i32) (local $s i32) (local $p i32) (local $o i32) (local $slot i32) (local $e i32)
  (local.set $row (call $row_get))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (memory.copy (local.get $row) (call $t_row (local.get $t) (local.get $i)) (global.get $W))
      ;; fresh blank nodes for the template's
      (local.set $slot (i32.const 0))
      (loop $slots
        (if (i32.lt_u (local.get $slot) (global.get $NV)) (then
          (local.set $e (i32.load (i32.add (i32.load offset=56 (local.get $q)) (i32.shl (local.get $slot) (i32.const 2)))))
          (if (i32.and (i32.load offset=20 (local.get $e)) (i32.const V_TEMPLATE))
            (then (i32.store (i32.add (local.get $row) (i32.shl (local.get $slot) (i32.const 2))) (call $fresh_bnode))))
          (local.set $slot (i32.add (local.get $slot) (i32.const 1)))
          (br $slots))))
      (local.set $tp (i32.load offset=36 (local.get $q)))
      (loop $triples
        (if (local.get $tp) (then
          (local.set $s (call $inst_ref (i32.load offset=4 (local.get $tp)) (local.get $row)))
          (local.set $p (call $inst_ref (i32.load offset=8 (local.get $tp)) (local.get $row)))
          (local.set $o (call $inst_ref (i32.load offset=12 (local.get $tp)) (local.get $row)))
          (if (i32.and (i32.and (i32.le_u (i32.sub (call $kind (local.get $s)) (i32.const 1)) (i32.const 1))
                                (i32.eq (call $kind (local.get $p)) (i32.const 1)))
                       (i32.gt_s (local.get $o) (i32.const 0)))
            (then (call $emit_triple (local.get $s) (local.get $p) (local.get $o))))
          (local.set $tp (i32.load offset=28 (local.get $tp)))
          (br $triples))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (call $row_put (local.get $row)))

;; DESCRIBE: the triples about each resource, following blank nodes.
(func $describe (param $q i32) (param $t i32)
  (local $res i32) (local $item i32) (local $i i32) (local $s i32) (local $ref i32)
  (local.set $res (call $set_make (i32.const 1)))
  (if (i32.and (i32.load offset=4 (local.get $q)) (i32.const Q_STAR))
    (then
      (local.set $i (i32.const 0))
      (loop $rows
        (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
          (local.set $s (i32.const 0))
          (loop $slots
            (if (i32.lt_u (local.get $s) (i32.load offset=100 (local.get $q))) (then
              (if (local.tee $ref (i32.load (i32.add (call $t_row (local.get $t) (local.get $i))
                    (i32.shl (i32.load (i32.add (i32.load offset=104 (local.get $q)) (i32.shl (local.get $s) (i32.const 2)))) (i32.const 2)))))
                (then (drop (call $set_add1 (local.get $res) (local.get $ref)))))
              (local.set $s (i32.add (local.get $s) (i32.const 1)))
              (br $slots))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $rows)))))
    (else
      (local.set $item (i32.load offset=36 (local.get $q)))
      (loop $items
        (if (local.get $item) (then
          (local.set $ref (i32.load offset=4 (local.get $item)))
          (if (i32.gt_s (local.get $ref) (i32.const 0))
            (then (drop (call $set_add1 (local.get $res) (local.get $ref))))
            (else
              (local.set $i (i32.const 0))
              (loop $rows
                (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
                  (if (local.tee $s (call $resolve_ref (local.get $ref) (call $t_row (local.get $t) (local.get $i))))
                    (then (drop (call $set_add1 (local.get $res) (local.get $s)))))
                  (local.set $i (i32.add (local.get $i) (i32.const 1)))
                  (br $rows))))))
          (local.set $item (i32.load offset=28 (local.get $item)))
          (br $items))))))
  ;; resources, then blank node objects as they appear
  (local.set $i (i32.const 0))
  (loop $each
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $res))) (then
      (call $describe_one (i32.load (call $set_key (local.get $res) (local.get $i))) (local.get $res))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $each))))
  (call $set_free (local.get $res)))

(func $describe_one (param $s i32) (param $res i32)
  (local $gi i32) (local $gn i32) (local $i i32) (local $r i32)
  (local.set $gn (call $graph_count))
  (loop $graphs
    (if (i32.lt_u (local.get $gi) (local.get $gn)) (then
      (call $scan (local.get $s) (i32.const 0) (i32.const 0) (call $graph_at (local.get $gi)))
      (local.set $i (i32.const 0))
      (loop $quads
        (if (i32.lt_u (local.get $i) (global.get $sc_n)) (then
          (local.set $r (call $sc_quad (local.get $i)))
          (if (i32.and (i32.eq (i32.load (local.get $r)) (local.get $s)) (i32.eq (i32.load offset=12 (local.get $r)) (call $graph_at (local.get $gi)))) (then
            (call $emit_triple (local.get $s) (i32.load offset=4 (local.get $r)) (i32.load offset=8 (local.get $r)))
            (if (i32.eq (call $kind (i32.load offset=8 (local.get $r))) (i32.const 2))
              (then (drop (call $set_add1 (local.get $res) (i32.load offset=8 (local.get $r))))))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $quads))))
      (local.set $gi (i32.add (local.get $gi) (i32.const 1)))
      (br $graphs)))))

;; One evaluation with the current parameters; results appended to $out.
;; Returns the ASK answer.
(func $run_once (param $q i32) (result i32)
  (local $t i32) (local $i i32) (local $j i32) (local $row i32) (local $n i32) (local $ans i32)
  (local.set $t (call $solve (local.get $q)))
  (local.set $ans (i32.ne (i32.load offset=4 (local.get $t)) (i32.const 0)))
  (if (i32.eq (i32.load (local.get $q)) (i32.const 1)) (then
    (local.set $n (i32.load offset=100 (local.get $q)))
    (loop $rows
      (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
        (local.set $row (call $t_row (local.get $t) (local.get $i)))
        (local.set $j (i32.const 0))
        (loop $cols
          (if (i32.lt_u (local.get $j) (local.get $n)) (then
            (call $v_push1 (global.get $out)
              (i32.load (i32.add (local.get $row) (i32.shl (i32.load (i32.add (i32.load offset=104 (local.get $q)) (i32.shl (local.get $j) (i32.const 2)))) (i32.const 2)))))
            (local.set $j (i32.add (local.get $j) (i32.const 1)))
            (br $cols))))
        (global.set $out_rows (i32.add (global.get $out_rows) (i32.const 1)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $rows))))))
  (if (i32.eq (i32.load (local.get $q)) (i32.const 2)) (then (call $construct (local.get $q) (local.get $t))))
  (if (i32.eq (i32.load (local.get $q)) (i32.const 4)) (then (call $describe (local.get $q) (local.get $t))))
  (call $t_free (local.get $t))
  (local.get $ans))

;; Graph ids from an N_ITEM list.
(func $graph_list (param $l i32) (result i32)
  (local $n i32) (local $ids i32) (local $k i32)
  (local.set $k (local.get $l))
  (loop $count (if (local.get $k) (then (local.set $n (i32.add (local.get $n) (i32.const 1))) (local.set $k (i32.load offset=28 (local.get $k))) (br $count))))
  (local.set $ids (call $alloc (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2))))
  (local.set $n (i32.const 0))
  (loop $each (if (local.get $l) (then
    (i32.store (i32.add (local.get $ids) (i32.shl (local.get $n) (i32.const 2))) (i32.load offset=4 (local.get $l)))
    (local.set $n (i32.add (local.get $n) (i32.const 1)))
    (local.set $l (i32.load offset=28 (local.get $l))) (br $each))))
  (global.set $gl_n (local.get $n))
  (global.set $dg_bytes (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2)))
  (local.get $ids))

(func $run (param $q i32) (param $graphs i32) (result i32)
  (local $i i32) (local $ans i32) (local $cnt i32)
  (call $run_begin (local.get $q) (local.get $graphs))
  (if (local.tee $cnt (i32.load offset=80 (local.get $q)))
    (then
      ;; bind_values: once per term
      (loop $values
        (if (i32.and (i32.lt_u (local.get $i) (local.get $cnt)) (i32.eqz (global.get $bu_stop))) (then
          (call $run_value (local.get $q) (local.get $i))
          (local.set $ans (i32.or (local.get $ans) (call $run_once (local.get $q))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $values)))))
    (else (local.set $ans (call $run_once (local.get $q)))))
  (call $run_end (local.get $q) (local.get $ans)))

;; The bind_values term i as the parameter; each value is its own run:
;; subquery caches hold the previous parameters.
(func $run_value (param $q i32) (param $i i32)
  (call $set_params (local.get $q) (i32.load offset=84 (local.get $q)) (i32.load offset=116 (local.get $q))
    (i32.load (i32.add (i32.load offset=76 (local.get $q)) (i32.shl (local.get $i) (i32.const 2)))))
  (global.set $epoch (i32.add (global.get $epoch) (i32.const 1)))
  (call $cache_clear))

;; A run's set-up: globals, scratch rows, the dataset, the result sets.
(func $run_begin (param $q i32) (param $graphs i32)
  (local $i i32) (local $n i32) (local $id i32) (local $dg_size i32)
  (global.set $q (local.get $q))
  (global.set $NV (i32.load offset=52 (local.get $q)))
  (global.set $W (i32.shl (select (global.get $NV) (i32.const 3) (i32.gt_u (global.get $NV) (i32.const 3))) (i32.const 2)))
  (global.set $params (i32.load offset=60 (local.get $q)))
  (global.set $epoch (i32.add (global.get $epoch) (i32.const 1)))
  (global.set $lat (i32.const 0))
  (global.set $ag (i32.const 0))
  (global.set $now (i32.const 0))
  (global.set $bn_row (i32.const -1))
  ;; scratch rows
  (global.set $rs_base (call $alloc (i32.add (i32.const 65536) (i32.shl (global.get $W) (i32.const 8)))))
  (global.set $rs_top (global.get $rs_base))
  (global.set $rs_end (i32.add (global.get $rs_base) (i32.add (i32.const 65536) (i32.shl (global.get $W) (i32.const 8)))))
  ;; the default graph: FROM, the graphs given, or the store's default graph
  ;; FROM, or with FROM NAMED alone an empty default graph
  (if (i32.or (i32.ne (i32.load offset=44 (local.get $q)) (i32.const 0)) (i32.ne (i32.load offset=48 (local.get $q)) (i32.const 0)))
    (then (global.set $dg (call $graph_list (i32.load offset=44 (local.get $q)))) (global.set $dgn (global.get $gl_n)))
    (else
      (global.set $dg_bytes (i32.shl (i32.add (local.get $graphs) (i32.const 1)) (i32.const 2)))
      (global.set $dg (call $alloc (global.get $dg_bytes)))
      (if (local.get $graphs)
        (then
          (local.set $n (i32.const 0))
          (loop $given (if (i32.lt_u (local.get $i) (local.get $graphs)) (then
            (local.set $id (i32.load (i32.add (global.get $in) (i32.shl (local.get $i) (i32.const 2)))))
            (if (call $kind (local.get $id)) (then
              (i32.store (i32.add (global.get $dg) (i32.shl (local.get $n) (i32.const 2))) (local.get $id))
              (local.set $n (i32.add (local.get $n) (i32.const 1)))))
            (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $given))))
          (global.set $dgn (local.get $n)))
        (else
          (i32.store (global.get $dg) (i32.const 1))
          (global.set $dgn (i32.const 1))))))
  (global.set $ng (i32.const 0))
  ;; a dataset clause: the named graphs are those of FROM NAMED (maybe none)
  (if (i32.or (i32.ne (i32.load offset=44 (local.get $q)) (i32.const 0)) (i32.ne (i32.load offset=48 (local.get $q)) (i32.const 0))) (then
    (local.set $dg_size (global.get $dg_bytes))
    (global.set $ng (call $graph_list (i32.load offset=48 (local.get $q))))
    (global.set $ng_bytes (global.get $dg_bytes))
    (global.set $dg_bytes (local.get $dg_size))
    (global.set $ngn (global.get $gl_n))))
  (global.set $out (call $v_new))
  (global.set $out_rows (i32.const 0))
  (global.set $tset (call $set_make (i32.const 3))))

;; A run's end: the results (ans: the ASK answer), and the set-up freed.
(func $run_end (param $q i32) (param $ans i32) (result i32)
  (local $n i32) (local $res i32)
  (if (i32.load offset=80 (local.get $q))
    (then (call $set_params (local.get $q) (i32.load offset=84 (local.get $q)) (i32.load offset=116 (local.get $q)) (i32.const 0))))
  (call $cache_clear)
  ;; results
  (local.set $n (i32.load offset=4 (global.get $out)))
  (call $res_reserve (i32.shl (local.get $n) (i32.const 2)))
  (memory.copy (global.get $res) (i32.load (global.get $out)) (i32.shl (local.get $n) (i32.const 2)))
  (call $v_free (global.get $out))
  (call $set_free (global.get $tset))
  (call $free (global.get $dg) (global.get $dg_bytes))
  (if (global.get $ng) (then (call $free (global.get $ng) (global.get $ng_bytes))))
  (call $free (global.get $rs_base) (i32.add (i32.const 65536) (i32.shl (global.get $W) (i32.const 8))))
  (local.set $res (i32.load (local.get $q)))
  (if (i32.eq (local.get $res) (i32.const 3)) (then (return (local.get $ans))))
  (if (i32.eq (local.get $res) (i32.const 1))
    (then (return (global.get $out_rows))))
  (i32.div_u (local.get $n) (i32.const 3)))

;; --- exports -----------------------------------------------------------------------

;; Set parameter id for every variable named [p, p+n).
(func $set_params (param $q i32) (param $p i32) (param $n i32) (param $id i32)
  (local $s i32) (local $e i32)
  (loop $slots
    (if (i32.lt_u (local.get $s) (i32.load offset=52 (local.get $q))) (then
      (local.set $e (i32.load (i32.add (i32.load offset=56 (local.get $q)) (i32.shl (local.get $s) (i32.const 2)))))
      (if (i32.and (i32.ge_s (i32.load offset=12 (local.get $e)) (i32.const 0))
                   (call $same (i32.load (local.get $e)) (i32.load offset=4 (local.get $e)) (local.get $p) (local.get $n) (i32.const 0)))
        (then (i32.store (i32.add (i32.load offset=60 (local.get $q)) (i32.shl (local.get $s) (i32.const 2))) (local.get $id))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots)))))

(func (export "prepare") (param $len i32) (param $base_len i32) (result i32)
  (local $q i32) (local $b i32)
  (global.set $p (global.get $in))
  (global.set $end (i32.add (global.get $in) (local.get $len)))
  (global.set $base_p (global.get $end))
  (global.set $base_n (local.get $base_len))
  (global.set $line (i32.const 1))
  (global.set $depth (i32.const 0))
  (global.set $ar_head (i32.const 0))
  (global.set $ar_p (i32.const 0))
  (global.set $ar_end (i32.const 0))
  (global.set $sparql (i32.const 1))
  (global.set $rec_vec (call $v_new))
  (call $lex)
  (local.set $q (call $parse_query))
  (global.set $sparql (i32.const 0))
  (call $smap_clear (global.get $prefixes))
  (if (i32.eqz (local.get $q)) (then
    (call $v_free (global.get $rec_vec))
    (global.set $rec_vec (i32.const 0))
    (call $arena_free (global.get $ar_head))
    (return (i32.const -1))))
  ;; the terms it uses, and the list of live queries (for collect)
  (i32.store offset=244 (local.get $q) (global.get $rec_vec))
  (global.set $rec_vec (i32.const 0))
  (i32.store offset=240 (local.get $q) (global.get $queries))
  (if (global.get $queries) (then (i32.store offset=248 (global.get $queries) (local.get $q))))
  (global.set $queries (local.get $q))
  (drop (call $analyze (local.get $q)))
  ;; the base, for IRI()
  (memory.copy (local.tee $b (call $anew (global.get $base_n))) (global.get $base_p) (global.get $base_n))
  (i32.store offset=108 (local.get $q) (local.get $b))
  (i32.store offset=112 (local.get $q) (global.get $base_n))
  (i32.store offset=64 (local.get $q) (global.get $ar_head))
  (local.get $q))

(func (export "query_form") (param $q i32) (result i32) (i32.load (local.get $q)))
(func (export "query_var_count") (param $q i32) (result i32) (i32.load offset=100 (local.get $q)))
(func $proj_entry (param $q i32) (param $i i32) (result i32)
  (i32.load (i32.add (i32.load offset=56 (local.get $q))
    (i32.shl (i32.load (i32.add (i32.load offset=104 (local.get $q)) (i32.shl (local.get $i) (i32.const 2)))) (i32.const 2)))))
(func (export "query_var_ptr") (param $q i32) (param $i i32) (result i32) (i32.load (call $proj_entry (local.get $q) (local.get $i))))
(func (export "query_var_len") (param $q i32) (param $i i32) (result i32) (i32.load offset=4 (call $proj_entry (local.get $q) (local.get $i))))

(func $drop_values (param $q i32)
  (if (i32.load offset=80 (local.get $q)) (then
    (call $set_params (local.get $q) (i32.load offset=84 (local.get $q)) (i32.load offset=116 (local.get $q)) (i32.const 0))
    (call $free (i32.load offset=76 (local.get $q)) (i32.load offset=120 (local.get $q)))
    (call $free (i32.load offset=84 (local.get $q)) (i32.load offset=116 (local.get $q)))
    (i32.store offset=80 (local.get $q) (i32.const 0)))))

(func (export "bind") (param $q i32) (param $n i32) (param $id i32) (result i32)
  (call $drop_values (local.get $q))
  (call $set_params (local.get $q) (global.get $in) (local.get $n) (local.get $id))
  (i32.const 0))

(func (export "bind_values") (param $q i32) (param $count i32) (param $n i32) (result i32)
  (local $b i32)
  (call $drop_values (local.get $q))
  (if (i32.eqz (local.get $count)) (then (return (i32.const 0))))
  (i32.store offset=120 (local.get $q) (i32.shl (i32.add (local.get $count) (i32.const 1)) (i32.const 2)))
  (memory.copy (local.tee $b (call $alloc (i32.load offset=120 (local.get $q)))) (global.get $in) (i32.shl (local.get $count) (i32.const 2)))
  (i32.store offset=76 (local.get $q) (local.get $b))
  (memory.copy (local.tee $b (call $alloc (local.get $n))) (i32.add (global.get $in) (i32.shl (local.get $count) (i32.const 2))) (local.get $n))
  (i32.store offset=84 (local.get $q) (local.get $b))
  (i32.store offset=116 (local.get $q) (local.get $n))
  (i32.store offset=80 (local.get $q) (local.get $count))
  (i32.const 0))

;; A run within the budget (budget.wat): out of it, no results and -2.
(func (export "run") (param $q i32) (param $graphs i32) (result i32)
  (local $n i32)
  (call $bu_start)
  (local.set $n (call $run (local.get $q) (local.get $graphs)))
  (global.set $bu_active (i32.const 0))
  (if (global.get $bu_stop) (then
    (drop (call $fail (@str "interrupted")))
    (return (i32.const -2))))
  (local.get $n))

(global $queries (mut i32) (i32.const 0))   ;; live prepared queries (+240 next, +248 previous)

(func (export "release") (param $q i32) (result i32)
  (call $drop_values (local.get $q))
  (call $cx_release (local.get $q))
  (if (i32.load offset=248 (local.get $q))
    (then (i32.store offset=240 (i32.load offset=248 (local.get $q)) (i32.load offset=240 (local.get $q))))
    (else (global.set $queries (i32.load offset=240 (local.get $q)))))
  (if (i32.load offset=240 (local.get $q)) (then (i32.store offset=248 (i32.load offset=240 (local.get $q)) (i32.load offset=248 (local.get $q)))))
  (call $v_free (i32.load offset=244 (local.get $q)))
  (call $arena_free (i32.load offset=64 (local.get $q)))
  (i32.const 0))
