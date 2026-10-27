;; SPARQL 1.1 Update. A request is parsed whole, then its operations run in
;; order; every change is logged, and on an error the log is undone, so a
;; request applies completely or not at all.
;;
;; Each operation is a query structure (form 1, so WHERE runs through $solve)
;; with: +204 operation, +208 SILENT, +212 source / target graph, +216
;; destination graph, +220 WITH graph, +224 INSERT template, +228 DELETE
;; template (P_TRIPLE nodes, d = graph ref, 0 = the default graph), +232 next.

(@define U_LOAD 1) (@define U_CLEAR 2) (@define U_DROP 3) (@define U_CREATE 4) (@define U_ADD 5)
(@define U_MOVE 6) (@define U_COPY 7) (@define U_INSERT_DATA 8) (@define U_DELETE_DATA 9)
(@define U_DELETE_WHERE 10) (@define U_MODIFY 11)
;; CLEAR / DROP targets (+212): a graph id, or one of these
(@define G_DEFAULT -1) (@define G_NAMED -2) (@define G_ALL -3)

(global $data_op (mut i32) (i32.const 0))   ;; parsing INSERT DATA: its number (blank node labels)
(global $made_graphs (mut i32) (i32.const 0))   ;; set of graphs made by CREATE (they exist while empty)

;; --- parsing -----------------------------------------------------------------------

;; GraphRef / GraphOrDefault: a graph id; DEFAULT gives 1.
(func $graph_ref (param $default_ok i32) (result i32)
  (local $id i32)
  (if (i32.and (local.get $default_ok) (call $is_kw (@str "DEFAULT"))) (then (call $lex) (return (i32.const 1))))
  (if (call $is_kw (@str "GRAPH")) (then (call $lex)))
  (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected a graph IRI")))))
  (if (local.tee $id (call $iri_tok)) (then (call $lex)))
  (local.get $id))

;; QuadPattern / QuadData (on '{'): a group of triples and GRAPH blocks.
(func $quad_pattern (result i32)
  (local $g i32) (local $ref i32) (local $inner i32)
  (if (i32.eqz (call $eat (i32.const '{') (@str "expected '{'"))) (then (return (i32.const 0))))
  (local.set $g (call $node (i32.const P_GROUP)))
  (global.set $bgp (i32.add (global.get $bgp) (i32.const 1)))
  (loop $items
    (if (i32.ne (global.get $tok) (i32.const '}')) (then
      (if (call $is_kw (@str "GRAPH"))
        (then
          (call $lex)
          (if (i32.eqz (local.tee $ref (call $var_or_iri))) (then (return (i32.const 0))))
          (local.set $inner (call $node (i32.const P_GROUP)))
          (if (i32.eqz (call $eat (i32.const '{') (@str "expected '{'"))) (then (return (i32.const 0))))
          (if (i32.eqz (call $triples_template (local.get $inner))) (then (return (i32.const 0))))
          (if (i32.eqz (call $eat (i32.const '}') (@str "expected '}'"))) (then (return (i32.const 0))))
          (call $add_elem (local.get $g) (call $node2 (i32.const P_GRAPH) (local.get $ref) (local.get $inner)))
          (if (i32.eq (global.get $tok) (i32.const '.')) (then (call $lex))))
        (else (if (i32.eqz (call $triples_template (local.get $g))) (then (return (i32.const 0))))))
      (br $items))))
  (call $lex)
  (local.get $g))

;; TriplesTemplate into g (up to '}' or GRAPH).
(func $triples_template (param $g i32) (result i32)
  (loop $triples
    (if (i32.and (i32.ne (global.get $tok) (i32.const '}')) (i32.eqz (call $is_kw (@str "GRAPH")))) (then
      (if (i32.eqz (call $ptriples (local.get $g))) (then (return (i32.const 0))))
      (if (i32.eq (global.get $tok) (i32.const '.')) (then (call $lex) (br $triples))))))
  (i32.const 1))

;; The template of a quad pattern: copies of its triple patterns with their
;; graph ref in d. Checks: no paths; DATA has no variables; DELETE no blank nodes.
(global $tl (mut i32) (i32.const 0))
(global $tl_tail (mut i32) (i32.const 0))
(func $template_of (param $g i32) (param $data i32) (param $delete i32) (result i32)
  (global.set $tl (i32.const 0))
  (if (i32.eqz (call $template_add (local.get $g) (i32.const 0) (local.get $data) (local.get $delete))) (then (return (i32.const -1))))
  (global.get $tl))

(func $template_add (param $g i32) (param $graph i32) (param $data i32) (param $delete i32) (result i32)
  (local $el i32) (local $n i32) (local $k i32)
  (local.set $el (i32.load offset=4 (local.get $g)))
  (loop $elements (if (local.get $el) (then
    (local.set $k (i32.load (local.get $el)))
    (if (i32.eq (local.get $k) (i32.const P_GRAPH)) (then
      (if (i32.and (local.get $data) (i32.lt_s (i32.load offset=4 (local.get $el)) (i32.const 0)))
        (then (return (call $syntax (@str "variable in DATA")))))
      (if (i32.eqz (call $template_add (i32.load offset=8 (local.get $el)) (i32.load offset=4 (local.get $el)) (local.get $data) (local.get $delete))) (then (return (i32.const 0))))))
    (if (i32.eq (local.get $k) (i32.const P_PATH)) (then (return (call $syntax (@str "paths are not allowed in a template")))))
    (if (i32.eq (local.get $k) (i32.const P_TRIPLE)) (then
      (if (i32.eqz (i32.and (i32.and (call $ref_ok (i32.load offset=4 (local.get $el)) (local.get $data) (local.get $delete))
                                     (call $ref_ok (i32.load offset=8 (local.get $el)) (local.get $data) (local.get $delete)))
                            (call $ref_ok (i32.load offset=12 (local.get $el)) (local.get $data) (local.get $delete))))
        (then
          (if (local.get $data) (then (return (call $syntax (@str "variable in DATA")))))
          (return (call $syntax (@str "blank node in DELETE")))))
      (local.set $n (call $node3 (i32.const P_TRIPLE) (i32.load offset=4 (local.get $el)) (i32.load offset=8 (local.get $el)) (i32.load offset=12 (local.get $el))))
      (i32.store offset=16 (local.get $n) (local.get $graph))
      (if (global.get $tl) (then (i32.store offset=28 (global.get $tl_tail) (local.get $n))) (else (global.set $tl (local.get $n))))
      (global.set $tl_tail (local.get $n))))
    (local.set $el (i32.load offset=28 (local.get $el)))
    (br $elements))))
  (i32.const 1))

;; Whether a template ref is allowed: in DATA only terms and blank nodes, in
;; DELETE no blank nodes.
(func $ref_ok (param $ref i32) (param $data i32) (param $delete i32) (result i32)
  (local $flags i32) (local $n i32)
  (if (i32.gt_s (local.get $ref) (i32.const 0)) (then (return (i32.const 1))))
  (if (i32.lt_s (local.get $ref) (i32.const -0x100000)) (then
    (local.set $n (i32.and (local.get $ref) (i32.const 0x7fffffff)))
    (return (i32.and (i32.and (call $ref_ok (i32.load offset=4 (local.get $n)) (local.get $data) (local.get $delete))
                              (call $ref_ok (i32.load offset=8 (local.get $n)) (local.get $data) (local.get $delete)))
                     (call $ref_ok (i32.load offset=12 (local.get $n)) (local.get $data) (local.get $delete))))))
  (local.set $n (call $slot_entry (i32.sub (i32.const -1) (local.get $ref))))
  (local.set $flags (i32.load offset=20 (local.get $n)))
  ;; blank nodes: template nodes, anonymous variables, labels
  (if (i32.or (i32.ne (i32.and (local.get $flags) (i32.const V_TEMPLATE)) (i32.const 0)) (i32.lt_s (i32.load offset=12 (local.get $n)) (i32.const 0)))
    (then (return (i32.eqz (local.get $delete)))))
  (i32.eqz (local.get $data)))

;; A quad pattern used as a template: blank nodes fresh per solution (scope
;; -3), or (DATA) per request.
(func $template_clause (param $data i32) (param $delete i32) (result i32)
  (local $g i32)
  (global.set $bscope (i32.const -3))
  (global.set $bflags (i32.const 3))   ;; V_HIDDEN | V_TEMPLATE
  (local.set $g (call $quad_pattern))
  (global.set $bscope (i32.const -2))
  (global.set $bflags (i32.const V_HIDDEN))
  (if (i32.eqz (local.get $g)) (then (return (i32.const -1))))
  (call $template_of (local.get $g) (local.get $data) (local.get $delete)))

;; One operation (on its keyword), or 0.
(func $update_op (param $n i32) (result i32)
  (local $q i32) (local $op i32) (local $t i32) (local $g i32)
  (global.set $cq (local.tee $q (call $q_new)))
  (i32.store (local.get $q) (i32.const 1))
  (block $done
    (if (call $is_kw (@str "LOAD")) (then
      (call $lex)
      (local.set $op (i32.const U_LOAD))
      (if (call $is_kw (@str "SILENT")) (then (call $lex) (i32.store offset=208 (local.get $q) (i32.const 1))))
      (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected an IRI")))))
      (if (i32.eqz (call $iri_tok)) (then (return (i32.const 0))))
      (call $lex)
      (if (call $is_kw (@str "INTO")) (then
        (call $lex)
        (if (i32.eqz (call $is_kw (@str "GRAPH"))) (then (return (call $expected (@str "expected GRAPH")))))
        (if (i32.eqz (call $graph_ref (i32.const 0))) (then (return (i32.const 0))))))
      (br $done)))
    (if (i32.or (call $is_kw (@str "CLEAR")) (call $is_kw (@str "DROP"))) (then
      (local.set $op (select (i32.const U_CLEAR) (i32.const U_DROP) (call $is_kw (@str "CLEAR"))))
      (call $lex)
      (if (call $is_kw (@str "SILENT")) (then (call $lex) (i32.store offset=208 (local.get $q) (i32.const 1))))
      (local.set $t
        (if (result i32) (call $is_kw (@str "DEFAULT")) (then (i32.const G_DEFAULT))
        (else (if (result i32) (call $is_kw (@str "NAMED")) (then (i32.const G_NAMED))
        (else (if (result i32) (call $is_kw (@str "ALL")) (then (i32.const G_ALL))
        (else (i32.const 0))))))))
      (if (local.get $t)
        (then (call $lex))
        (else
          (if (i32.eqz (call $is_kw (@str "GRAPH"))) (then (return (call $expected (@str "expected GRAPH, DEFAULT, NAMED or ALL")))))
          (if (i32.eqz (local.tee $t (call $graph_ref (i32.const 0)))) (then (return (i32.const 0))))))
      (i32.store offset=212 (local.get $q) (local.get $t))
      (br $done)))
    (if (call $is_kw (@str "CREATE")) (then
      (call $lex)
      (local.set $op (i32.const U_CREATE))
      (if (call $is_kw (@str "SILENT")) (then (call $lex) (i32.store offset=208 (local.get $q) (i32.const 1))))
      (if (i32.eqz (call $is_kw (@str "GRAPH"))) (then (return (call $expected (@str "expected GRAPH")))))
      (if (i32.eqz (local.tee $t (call $graph_ref (i32.const 0)))) (then (return (i32.const 0))))
      (i32.store offset=212 (local.get $q) (local.get $t))
      (br $done)))
    (if (i32.or (i32.or (call $is_kw (@str "ADD")) (call $is_kw (@str "MOVE"))) (call $is_kw (@str "COPY"))) (then
      (local.set $op (select (i32.const U_ADD) (select (i32.const U_MOVE) (i32.const U_COPY) (call $is_kw (@str "MOVE"))) (call $is_kw (@str "ADD"))))
      (call $lex)
      (if (call $is_kw (@str "SILENT")) (then (call $lex) (i32.store offset=208 (local.get $q) (i32.const 1))))
      (if (i32.eqz (local.tee $t (call $graph_ref (i32.const 1)))) (then (return (i32.const 0))))
      (i32.store offset=212 (local.get $q) (local.get $t))
      (if (i32.eqz (call $eat_kw (@str "TO"))) (then (return (i32.const 0))))
      (if (i32.eqz (local.tee $t (call $graph_ref (i32.const 1)))) (then (return (i32.const 0))))
      (i32.store offset=216 (local.get $q) (local.get $t))
      (br $done)))
    (if (call $is_kw (@str "WITH")) (then
      (call $lex)
      (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected an IRI")))))
      (if (i32.eqz (local.tee $t (call $iri_tok))) (then (return (i32.const 0))))
      (call $lex)
      (i32.store offset=220 (local.get $q) (local.get $t))
      (if (i32.eqz (i32.or (call $is_kw (@str "DELETE")) (call $is_kw (@str "INSERT")))) (then (return (call $expected (@str "expected DELETE or INSERT")))))))
    (if (call $is_kw (@str "INSERT")) (then
      (call $lex)
      (if (call $is_kw (@str "DATA")) (then
        (if (i32.load offset=220 (local.get $q)) (then (return (call $syntax (@str "WITH before INSERT DATA")))))
        (call $lex)
        (local.set $op (i32.const U_INSERT_DATA))
        (global.set $data_op (local.get $n))
        (local.set $t (call $template_clause (i32.const 1) (i32.const 0)))
        (global.set $data_op (i32.const 0))
        (if (i32.lt_s (local.get $t) (i32.const 0)) (then (return (i32.const 0))))
        (i32.store offset=224 (local.get $q) (local.get $t))
        (br $done)))
      (local.set $op (i32.const U_MODIFY))
      (local.set $t (call $template_clause (i32.const 0) (i32.const 0)))
      (if (i32.lt_s (local.get $t) (i32.const 0)) (then (return (i32.const 0))))
      (i32.store offset=224 (local.get $q) (local.get $t))
      (if (i32.eqz (call $using_where (local.get $q))) (then (return (i32.const 0))))
      (br $done)))
    (if (call $is_kw (@str "DELETE")) (then
      (call $lex)
      (if (call $is_kw (@str "DATA")) (then
        (if (i32.load offset=220 (local.get $q)) (then (return (call $syntax (@str "WITH before DELETE DATA")))))
        (call $lex)
        (local.set $op (i32.const U_DELETE_DATA))
        (local.set $t (call $template_clause (i32.const 1) (i32.const 1)))
        (if (i32.lt_s (local.get $t) (i32.const 0)) (then (return (i32.const 0))))
        (i32.store offset=228 (local.get $q) (local.get $t))
        (br $done)))
      (if (call $is_kw (@str "WHERE")) (then
        (if (i32.load offset=220 (local.get $q)) (then (return (call $syntax (@str "WITH before DELETE WHERE")))))
        (call $lex)
        (local.set $op (i32.const U_DELETE_WHERE))
        ;; the pattern is both the WHERE clause and the template
        (if (i32.eqz (local.tee $g (call $quad_pattern))) (then (return (i32.const 0))))
        (i32.store offset=12 (local.get $q) (local.get $g))
        (if (i32.lt_s (local.tee $t (call $template_of (local.get $g) (i32.const 0) (i32.const 1))) (i32.const 0)) (then (return (i32.const 0))))
        (i32.store offset=228 (local.get $q) (local.get $t))
        (br $done)))
      (local.set $op (i32.const U_MODIFY))
      (local.set $t (call $template_clause (i32.const 0) (i32.const 1)))
      (if (i32.lt_s (local.get $t) (i32.const 0)) (then (return (i32.const 0))))
      (i32.store offset=228 (local.get $q) (local.get $t))
      (if (call $is_kw (@str "INSERT")) (then
        (call $lex)
        (local.set $t (call $template_clause (i32.const 0) (i32.const 0)))
        (if (i32.lt_s (local.get $t) (i32.const 0)) (then (return (i32.const 0))))
        (i32.store offset=224 (local.get $q) (local.get $t))))
      (if (i32.eqz (call $using_where (local.get $q))) (then (return (i32.const 0))))
      (br $done)))
    (return (call $expected (@str "expected an update operation"))))
  (i32.store offset=204 (local.get $q) (local.get $op))
  (local.get $q))

;; USING clauses, then WHERE GroupGraphPattern.
(func $using_where (param $q i32) (result i32)
  (local $named i32) (local $id i32) (local $g i32)
  (loop $usings
    (if (call $is_kw (@str "USING")) (then
      (call $lex)
      (local.set $named (call $is_kw (@str "NAMED")))
      (if (local.get $named) (then (call $lex)))
      (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected an IRI")))))
      (if (i32.eqz (local.tee $id (call $iri_tok))) (then (return (i32.const 0))))
      (call $lex)
      (call $qappend (local.get $q) (select (i32.const 48) (i32.const 44) (local.get $named)) (call $node2 (i32.const N_ITEM) (local.get $id) (i32.const 0)))
      (br $usings))))
  (if (i32.eqz (call $eat_kw (@str "WHERE"))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $g (call $ggp))) (then (return (i32.const 0))))
  (i32.store offset=12 (local.get $q) (local.get $g))
  (i32.const 1))

;; Update (after the first token): the first operation (+232 links the rest),
;; a placeholder query for an empty request, or 0.
(func $parse_update (result i32)
  (local $first i32) (local $last i32) (local $op i32) (local $n i32)
  (global.set $vars (i32.const 0))
  (global.set $nvars (i32.const 0))
  (global.set $vscope (i32.const 0))
  (global.set $nscopes (i32.const 0))
  (global.set $bscope (i32.const -2))
  (global.set $bflags (i32.const V_HIDDEN))
  (global.set $in_agg (i32.const 0))
  (global.set $aggs_ok (i32.const 0))
  (loop $ops
    (if (i32.eqz (call $prologue)) (then (return (i32.const 0))))
    (if (i32.ne (global.get $tok) (i32.const T_EOF)) (then
      (local.set $n (i32.add (local.get $n) (i32.const 1)))
      (if (i32.eqz (local.tee $op (call $update_op (local.get $n)))) (then (return (i32.const 0))))
      (if (local.get $last) (then (i32.store offset=232 (local.get $last) (local.get $op))) (else (local.set $first (local.get $op))))
      (local.set $last (local.get $op))
      (if (i32.eq (global.get $tok) (i32.const ';')) (then (call $lex) (br $ops)))
      (if (i32.ne (global.get $tok) (i32.const T_EOF)) (then (return (call $expected (@str "expected ';'"))))))))
  (if (i32.eqz (local.get $first)) (then
    (local.set $first (call $q_new))
    (i32.store (local.get $first) (i32.const 1))))
  (local.get $first))

;; Parse the update in the input buffer: its first operation, or 0 (error set).
;; The arena is in $ar_head.
(func $prepare_update (param $len i32) (param $base_len i32) (result i32)
  (local $q i32) (local $op i32)
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
  (call $lex)
  (local.set $q (call $parse_update))
  (global.set $sparql (i32.const 0))
  (call $smap_clear (global.get $prefixes))
  (if (i32.eqz (local.get $q)) (then (call $arena_free (global.get $ar_head)) (return (i32.const 0))))
  ;; every operation shares the request's variables
  (global.set $subqueries (i32.const 0))
  (local.set $op (local.get $q))
  (loop $ops (if (local.get $op) (then
    (call $an_query (local.get $op))
    (local.set $op (i32.load offset=232 (local.get $op)))
    (br $ops))))
  (drop (call $analyze_slots (local.get $q)))
  (local.set $op (i32.load offset=232 (local.get $q)))
  (loop $ops (if (local.get $op) (then
    (i32.store offset=52 (local.get $op) (i32.load offset=52 (local.get $q)))
    (i32.store offset=56 (local.get $op) (i32.load offset=56 (local.get $q)))
    (i32.store offset=60 (local.get $op) (i32.load offset=60 (local.get $q)))
    (local.set $op (i32.load offset=232 (local.get $op)))
    (br $ops))))
  (memory.copy (local.tee $op (call $anew (global.get $base_n))) (global.get $base_p) (global.get $base_n))
  (i32.store offset=108 (local.get $q) (local.get $op))
  (i32.store offset=112 (local.get $q) (global.get $base_n))
  (local.get $q))

(func (export "parse_update") (param $len i32) (param $base_len i32) (result i32)
  (if (i32.eqz (call $prepare_update (local.get $len) (local.get $base_len))) (then (return (i32.const -1))))
  (call $arena_free (global.get $ar_head))
  (i32.const 0))

;; --- execution ---------------------------------------------------------------------

;; The change log: 5 words an entry: kind (1 added, 2 removed, 3 created, 4 dropped), s, p, o, g.
(global $ulog (mut i32) (i32.const 0))

(func $log (param $k i32) (param $s i32) (param $p i32) (param $o i32) (param $g i32)
  (call $v_push2 (global.get $ulog) (local.get $k) (local.get $s))
  (call $v_push2 (global.get $ulog) (local.get $p) (local.get $o))
  (call $v_push1 (global.get $ulog) (local.get $g)))

(func $u_add (param $s i32) (param $p i32) (param $o i32) (param $g i32)
  (if (call $add (local.get $s) (local.get $p) (local.get $o) (local.get $g))
    (then (call $log (i32.const 1) (local.get $s) (local.get $p) (local.get $o) (local.get $g)))))

(func $u_remove (param $s i32) (param $p i32) (param $o i32) (param $g i32)
  (if (call $remove (local.get $s) (local.get $p) (local.get $o) (local.get $g))
    (then (call $log (i32.const 2) (local.get $s) (local.get $p) (local.get $o) (local.get $g)))))

;; Undo the log, newest first.
(func $undo
  (local $i i32) (local $at i32) (local $k i32)
  (local.set $i (i32.load offset=4 (global.get $ulog)))
  (loop $entries (if (i32.gt_u (local.get $i) (i32.const 0)) (then
    (local.set $i (i32.sub (local.get $i) (i32.const 5)))
    (local.set $at (i32.add (i32.load (global.get $ulog)) (i32.shl (local.get $i) (i32.const 2))))
    (local.set $k (i32.load (local.get $at)))
    (if (i32.eq (local.get $k) (i32.const 1)) (then
      (drop (call $remove (i32.load offset=4 (local.get $at)) (i32.load offset=8 (local.get $at)) (i32.load offset=12 (local.get $at)) (i32.load offset=16 (local.get $at))))))
    (if (i32.eq (local.get $k) (i32.const 2)) (then
      (drop (call $add (i32.load offset=4 (local.get $at)) (i32.load offset=8 (local.get $at)) (i32.load offset=12 (local.get $at)) (i32.load offset=16 (local.get $at))))))
    (if (i32.eq (local.get $k) (i32.const 3)) (then (call $uncreate (i32.load offset=16 (local.get $at)))))
    (if (i32.eq (local.get $k) (i32.const 4)) (then (drop (call $set_add1 (global.get $made_graphs) (i32.load offset=16 (local.get $at))))))
    (br $entries)))))

;; Created graphs: a set of ids; removing one swaps it with 0 (never matched).
(func $uncreate (param $g i32)
  (local $i i32)
  (loop $keys (if (i32.lt_u (local.get $i) (i32.load offset=4 (global.get $made_graphs))) (then
    (if (i32.eq (i32.load (call $set_key (global.get $made_graphs) (local.get $i))) (local.get $g)) (then
      ;; keys stay in place; mark it dead
      (i32.store (call $set_key (global.get $made_graphs) (local.get $i)) (i32.const 0))))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $keys)))))

(func $is_created (param $g i32) (result i32)
  (local $i i32)
  (loop $keys (if (i32.lt_u (local.get $i) (i32.load offset=4 (global.get $made_graphs))) (then
    (if (i32.eq (i32.load (call $set_key (global.get $made_graphs) (local.get $i))) (local.get $g)) (then (return (i32.const 1))))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $keys))))
  (i32.const 0))

;; Whether graph g exists: the default graph, a graph with quads, or created.
(func $graph_exists (param $g i32) (result i32)
  (if (i32.eq (local.get $g) (i32.const 1)) (then (return (i32.const 1))))
  (if (i32.eqz (call $kind (local.get $g))) (then (return (i32.const 0))))
  (if (i32.load offset=12 (call $post_of (local.get $g))) (then (return (i32.const 1))))
  (call $is_created (local.get $g)))

;; The quads of graph g (16 bytes each) in a new vector.
(func $graph_quads (param $g i32) (result i32)
  (local $v i32) (local $l i32) (local $i i32) (local $r i32)
  (local.set $v (call $v_new))
  (if (i32.eqz (call $kind (local.get $g))) (then (return (local.get $v))))
  (if (local.tee $l (i32.load offset=12 (call $post_of (local.get $g)))) (then
    (loop $quads (if (i32.lt_u (local.get $i) (i32.load (local.get $l))) (then
      (local.set $r (call $list_quad (local.get $l) (local.get $i)))
      (call $v_push2 (local.get $v) (i32.load (local.get $r)) (i32.load offset=4 (local.get $r)))
      (call $v_push2 (local.get $v) (i32.load offset=8 (local.get $r)) (i32.load offset=12 (local.get $r)))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $quads))))))
  (local.get $v))

;; Remove every quad of graph g.
(func $clear_graph (param $g i32)
  (local $v i32) (local $i i32) (local $at i32)
  (local.set $v (call $graph_quads (local.get $g)))
  (loop $quads (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $v))) (then
    (local.set $at (i32.add (i32.load (local.get $v)) (i32.shl (local.get $i) (i32.const 2))))
    (call $u_remove (i32.load (local.get $at)) (i32.load offset=4 (local.get $at)) (i32.load offset=8 (local.get $at)) (i32.load offset=12 (local.get $at)))
    (local.set $i (i32.add (local.get $i) (i32.const 4))) (br $quads))))
  (call $v_free (local.get $v)))

;; Copy graph a's triples into graph b.
(func $copy_graph (param $a i32) (param $b i32)
  (local $v i32) (local $i i32) (local $at i32)
  (local.set $v (call $graph_quads (local.get $a)))
  (loop $quads (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $v))) (then
    (local.set $at (i32.add (i32.load (local.get $v)) (i32.shl (local.get $i) (i32.const 2))))
    (call $u_add (i32.load (local.get $at)) (i32.load offset=4 (local.get $at)) (i32.load offset=8 (local.get $at)) (local.get $b))
    (local.set $i (i32.add (local.get $i) (i32.const 4))) (br $quads))))
  (call $v_free (local.get $v)))

;; Every graph with quads (and the default graph), for NAMED and ALL.
(func $each_graph (param $all i32) (param $drop i32)
  (local $id i32) (local $i i32)
  (call $named_graphs)
  (loop $graphs (if (i32.lt_u (local.get $i) (global.get $ngn)) (then
    (local.set $id (i32.load (i32.add (global.get $ng) (i32.shl (local.get $i) (i32.const 2)))))
    (call $clear_graph (local.get $id))
    (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $graphs))))
  (if (local.get $all) (then (call $clear_graph (i32.const 1))))
  (if (local.get $drop) (then
    (local.set $i (i32.const 0))
    (loop $made (if (i32.lt_u (local.get $i) (i32.load offset=4 (global.get $made_graphs))) (then
      (if (local.tee $id (i32.load (call $set_key (global.get $made_graphs) (local.get $i)))) (then
        (call $log (i32.const 4) (i32.const 0) (i32.const 0) (i32.const 0) (local.get $id))
        (i32.store (call $set_key (global.get $made_graphs) (local.get $i)) (i32.const 0))))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $made)))))))

;; Instantiate template tl for each row of t into vector v (4 words a quad);
;; with fresh blank nodes per row when `fresh`.
(func $instantiate (param $q i32) (param $tl i32) (param $t i32) (param $v i32) (param $with i32)
  (local $i i32) (local $row i32) (local $tp i32) (local $s i32) (local $p i32) (local $o i32) (local $g i32) (local $slot i32) (local $e i32)
  (local.set $row (call $row_get))
  (loop $rows (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
    (memory.copy (local.get $row) (call $t_row (local.get $t) (local.get $i)) (global.get $W))
    (local.set $slot (i32.const 0))
    (loop $slots (if (i32.lt_u (local.get $slot) (global.get $NV)) (then
      (local.set $e (i32.load (i32.add (i32.load offset=56 (local.get $q)) (i32.shl (local.get $slot) (i32.const 2)))))
      (if (i32.and (i32.load offset=20 (local.get $e)) (i32.const V_TEMPLATE))
        (then (i32.store (i32.add (local.get $row) (i32.shl (local.get $slot) (i32.const 2))) (call $fresh_bnode))))
      (local.set $slot (i32.add (local.get $slot) (i32.const 1))) (br $slots))))
    (local.set $tp (local.get $tl))
    (loop $triples (if (local.get $tp) (then
      (local.set $s (call $inst_ref (i32.load offset=4 (local.get $tp)) (local.get $row)))
      (local.set $p (call $inst_ref (i32.load offset=8 (local.get $tp)) (local.get $row)))
      (local.set $o (call $inst_ref (i32.load offset=12 (local.get $tp)) (local.get $row)))
      (local.set $g (if (result i32) (i32.load offset=16 (local.get $tp))
        (then (call $resolve_ref (i32.load offset=16 (local.get $tp)) (local.get $row)))
        (else (local.get $with))))
      (if (i32.and (i32.and (i32.le_u (i32.sub (call $kind (local.get $s)) (i32.const 1)) (i32.const 1))
                            (i32.eq (call $kind (local.get $p)) (i32.const 1)))
                   (i32.and (i32.gt_s (local.get $o) (i32.const 0))
                            (i32.or (i32.eq (local.get $g) (i32.const 1)) (i32.eq (call $kind (local.get $g)) (i32.const 1)))))
        (then
          (call $v_push2 (local.get $v) (local.get $s) (local.get $p))
          (call $v_push2 (local.get $v) (local.get $o) (local.get $g))))
      (local.set $tp (i32.load offset=28 (local.get $tp)))
      (br $triples))))
    (local.set $i (i32.add (local.get $i) (i32.const 1)))
    (br $rows))))
  (call $row_put (local.get $row)))

(func $apply_quads (param $v i32) (param $remove i32)
  (local $i i32) (local $at i32)
  (loop $quads (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $v))) (then
    (local.set $at (i32.add (i32.load (local.get $v)) (i32.shl (local.get $i) (i32.const 2))))
    (if (local.get $remove)
      (then (call $u_remove (i32.load (local.get $at)) (i32.load offset=4 (local.get $at)) (i32.load offset=8 (local.get $at)) (i32.load offset=12 (local.get $at))))
      (else (call $u_add (i32.load (local.get $at)) (i32.load offset=4 (local.get $at)) (i32.load offset=8 (local.get $at)) (i32.load offset=12 (local.get $at)))))
    (local.set $i (i32.add (local.get $i) (i32.const 4))) (br $quads)))))

;; Run one operation: 0, or -1 after an error.
(func $run_op (param $q i32) (param $first i32) (result i32)
  (local $op i32) (local $t i32) (local $one i32) (local $del i32) (local $ins i32) (local $with i32) (local $a i32) (local $b i32)
  (local.set $op (i32.load offset=204 (local.get $q)))
  (local.set $a (i32.load offset=212 (local.get $q)))
  (local.set $b (i32.load offset=216 (local.get $q)))
  (if (i32.eq (local.get $op) (i32.const U_LOAD)) (then
    (if (i32.load offset=208 (local.get $q)) (then (return (i32.const 0))))
    (return (call $fail (@str "LOAD is not supported")))))
  (if (i32.or (i32.eq (local.get $op) (i32.const U_CLEAR)) (i32.eq (local.get $op) (i32.const U_DROP))) (then
    (if (i32.eq (local.get $a) (i32.const G_DEFAULT)) (then (call $clear_graph (i32.const 1)) (return (i32.const 0))))
    (if (i32.lt_s (local.get $a) (i32.const 0)) (then
      (call $each_graph (i32.eq (local.get $a) (i32.const G_ALL)) (i32.eq (local.get $op) (i32.const U_DROP)))
      (return (i32.const 0))))
    (if (i32.eqz (call $graph_exists (local.get $a))) (then
      (if (i32.load offset=208 (local.get $q)) (then (return (i32.const 0))))
      (return (call $fail (@str "no such graph")))))
    (call $clear_graph (local.get $a))
    (if (i32.and (i32.eq (local.get $op) (i32.const U_DROP)) (call $is_created (local.get $a))) (then
      (call $uncreate (local.get $a))
      (call $log (i32.const 4) (i32.const 0) (i32.const 0) (i32.const 0) (local.get $a))))
    (return (i32.const 0))))
  (if (i32.eq (local.get $op) (i32.const U_CREATE)) (then
    (if (call $graph_exists (local.get $a)) (then
      (if (i32.load offset=208 (local.get $q)) (then (return (i32.const 0))))
      (return (call $fail (@str "graph already exists")))))
    (drop (call $set_add1 (global.get $made_graphs) (local.get $a)))
    (call $log (i32.const 3) (i32.const 0) (i32.const 0) (i32.const 0) (local.get $a))
    (return (i32.const 0))))
  (if (i32.le_u (i32.sub (local.get $op) (i32.const U_ADD)) (i32.const 2)) (then
    (if (i32.eq (local.get $a) (local.get $b)) (then (return (i32.const 0))))
    (if (i32.eqz (call $graph_exists (local.get $a))) (then
      (if (i32.load offset=208 (local.get $q)) (then (return (i32.const 0))))
      (return (call $fail (@str "no such graph")))))
    (if (i32.ne (local.get $op) (i32.const U_ADD)) (then (call $clear_graph (local.get $b))))
    (call $copy_graph (local.get $a) (local.get $b))
    (if (i32.eq (local.get $op) (i32.const U_MOVE)) (then
      (call $clear_graph (local.get $a))
      (if (call $is_created (local.get $a)) (then
        (call $uncreate (local.get $a))
        (call $log (i32.const 4) (i32.const 0) (i32.const 0) (i32.const 0) (local.get $a))))))
    (return (i32.const 0))))
  ;; INSERT DATA, DELETE DATA, DELETE WHERE, DELETE/INSERT ... WHERE
  (local.set $with (select (i32.load offset=220 (local.get $q)) (i32.const 1) (i32.load offset=220 (local.get $q))))
  (if (i32.le_u (i32.sub (local.get $op) (i32.const U_INSERT_DATA)) (i32.const 1))
    (then (local.set $t (call $t_one (global.get $params))))
    (else
      ;; WHERE, with WITH as the default graph unless USING says otherwise
      (call $op_dataset (local.get $q) (local.get $first))
      (local.set $t (call $solve (local.get $q)))
      (call $cache_clear)))
  (local.set $del (call $v_new))
  (local.set $ins (call $v_new))
  (call $instantiate (local.get $first) (i32.load offset=228 (local.get $q)) (local.get $t) (local.get $del) (local.get $with))
  (call $instantiate (local.get $first) (i32.load offset=224 (local.get $q)) (local.get $t) (local.get $ins) (local.get $with))
  (call $t_free (local.get $t))
  (call $apply_quads (local.get $del) (i32.const 1))
  (call $apply_quads (local.get $ins) (i32.const 0))
  (call $v_free (local.get $del))
  (call $v_free (local.get $ins))
  (i32.const 0))

;; The dataset of an operation's WHERE: USING / USING NAMED, else the WITH
;; graph (or the default graph) and every named graph.
(func $op_dataset (param $q i32) (param $first i32)
  (local $with i32)
  (if (global.get $dg) (then (call $free (global.get $dg) (global.get $dg_bytes))))
  (if (global.get $ng) (then (call $free (global.get $ng) (global.get $ng_bytes))))
  (global.set $ng (i32.const 0))
  (global.set $epoch (i32.add (global.get $epoch) (i32.const 1)))
  (global.set $ag (i32.const 0))
  (if (i32.or (i32.ne (i32.load offset=44 (local.get $q)) (i32.const 0)) (i32.ne (i32.load offset=48 (local.get $q)) (i32.const 0)))
    (then
      (global.set $dg (call $graph_list (i32.load offset=44 (local.get $q))))
      (global.set $dgn (global.get $gl_n))
      (local.set $with (global.get $dg_bytes))
      (global.set $ng (call $graph_list (i32.load offset=48 (local.get $q))))
      (global.set $ngn (global.get $gl_n))
      (global.set $ng_bytes (global.get $dg_bytes))
      (global.set $dg_bytes (local.get $with)))
    (else
      (global.set $dg_bytes (i32.const 8))
      (global.set $dg (call $alloc (i32.const 8)))
      (i32.store (global.get $dg) (select (i32.load offset=220 (local.get $q)) (i32.const 1) (i32.load offset=220 (local.get $q))))
      (global.set $dgn (i32.const 1)))))

(func (export "update") (param $len i32) (param $base_len i32) (result i32)
  (local $q i32) (local $op i32) (local $r i32)
  (if (i32.eqz (local.tee $q (call $prepare_update (local.get $len) (local.get $base_len)))) (then (return (i32.const -1))))
  (if (i32.eqz (global.get $made_graphs)) (then (global.set $made_graphs (call $set_make (i32.const 1)))))
  (global.set $q (local.get $q))
  (global.set $NV (i32.load offset=52 (local.get $q)))
  (global.set $W (i32.shl (select (global.get $NV) (i32.const 3) (i32.gt_u (global.get $NV) (i32.const 3))) (i32.const 2)))
  (global.set $params (i32.load offset=60 (local.get $q)))
  (global.set $lat (i32.const 0))
  (global.set $now (i32.const 0))
  (global.set $bn_row (i32.const -1))
  (global.set $rs_base (call $alloc (i32.add (i32.const 65536) (i32.shl (global.get $W) (i32.const 8)))))
  (global.set $rs_top (global.get $rs_base))
  (global.set $rs_end (i32.add (global.get $rs_base) (i32.add (i32.const 65536) (i32.shl (global.get $W) (i32.const 8)))))
  (global.set $dg (i32.const 0))
  (global.set $ng (i32.const 0))
  (global.set $ulog (call $v_new))
  (local.set $op (local.get $q))
  (block $failed
    (loop $ops (if (local.get $op) (then
      (if (i32.lt_s (call $run_op (local.get $op) (local.get $q)) (i32.const 0)) (then (local.set $r (i32.const -1)) (br $failed)))
      ;; named graphs change with each operation
      (if (global.get $ng) (then (call $free (global.get $ng) (global.get $ng_bytes)) (global.set $ng (i32.const 0))))
      (local.set $op (i32.load offset=232 (local.get $op)))
      (br $ops)))))
  (if (local.get $r) (then (call $undo)))
  (call $v_free (global.get $ulog))
  (if (global.get $dg) (then (call $free (global.get $dg) (global.get $dg_bytes))))
  (if (global.get $ng) (then (call $free (global.get $ng) (global.get $ng_bytes))))
  (global.set $dg (i32.const 0))
  (global.set $ng (i32.const 0))
  (call $free (global.get $rs_base) (i32.add (i32.const 65536) (i32.shl (global.get $W) (i32.const 8))))
  (call $arena_free (global.get $ar_head))
  (local.get $r))
