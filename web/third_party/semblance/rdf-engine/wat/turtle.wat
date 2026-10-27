;; Turtle 1.2 and TriG 1.2: recursive descent over the tokens of lex.wat.
;;
;; The current token is in $tok; each parsing function starts on its first
;; token and returns on the token after it. Functions return a term id (or 1),
;; or 0 after setting the error. Triples go to the quad buffer in graph $tg.

;; The forms $term accepts beyond IRIs and blank nodes, and $form, the one it read.
(@define F_COLL 1)    ;; ( ... )
(@define F_BNPL 2)    ;; [ predicateObjectList ]
(@define F_LIT 4)     ;; literals
(@define F_TT 8)      ;; <<( s p o )>>
(@define F_RT 16)     ;; << s p o ~ r >>
(@define F_OBJECT 31)     ;; all of them
(@define F_SUBJECT 19)    ;; F_COLL | F_BNPL | F_RT, at the start of triples
(@define F_ALONE 18)      ;; F_BNPL | F_RT: subjects that need no predicateObjectList
(@define F_RT_OBJECT 28)  ;; F_LIT | F_TT | F_RT
(@define F_TT_OBJECT 12)  ;; F_LIT | F_TT

(global $form (mut i32) (i32.const 0))
(global $tg (mut i32) (i32.const 0))
(global $prefixes (mut i32) (i32.const 0))   ;; string map header: prefix -> IRI id

;; IRIs the syntax produces on its own, interned on first use. $knowns holds
;; 12 bytes per IRI: name ptr, name len, id (0 until interned; collect, in
;; phase 5, must reset the ids it frees).
(@define K_TYPE 0)
(@define K_FIRST 1)
(@define K_REST 2)
(@define K_NIL 3)
(@define K_REIFIES 4)
(@define K_INTEGER 5)
(@define K_DECIMAL 6)
(@define K_DOUBLE 7)
(@define K_BOOLEAN 8)
(global $knowns (mut i32) (i32.const 0))

(func $iri_const (param $p i32) (param $n i32) (result i32)
  (call $intern (i32.const 1) (local.get $p) (local.get $n) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))

(func $known (param $k i32) (result i32)
  (local $at i32) (local $id i32)
  (local.set $at (i32.add (global.get $knowns) (i32.mul (local.get $k) (i32.const 12))))
  (if (i32.eqz (local.tee $id (i32.load offset=8 (local.get $at)))) (then
    (local.set $id (call $iri_const (i32.load (local.get $at)) (i32.load offset=4 (local.get $at))))
    (i32.store offset=8 (local.get $at) (local.get $id))))
  (call $recorded (local.get $id)))

(func $known_name (param $k i32) (param $p i32) (param $n i32)
  (local $at i32)
  (local.set $at (i32.add (global.get $knowns) (i32.mul (local.get $k) (i32.const 12))))
  (i32.store (local.get $at) (local.get $p))
  (i32.store offset=4 (local.get $at) (local.get $n)))

(func $init_knowns
  (global.set $knowns (call $alloc0 (i32.const 108)))
  (call $known_name (i32.const K_TYPE) (@str "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"))
  (call $known_name (i32.const K_FIRST) (@str "http://www.w3.org/1999/02/22-rdf-syntax-ns#first"))
  (call $known_name (i32.const K_REST) (@str "http://www.w3.org/1999/02/22-rdf-syntax-ns#rest"))
  (call $known_name (i32.const K_NIL) (@str "http://www.w3.org/1999/02/22-rdf-syntax-ns#nil"))
  (call $known_name (i32.const K_REIFIES) (@str "http://www.w3.org/1999/02/22-rdf-syntax-ns#reifies"))
  (call $known_name (i32.const K_INTEGER) (@str "http://www.w3.org/2001/XMLSchema#integer"))
  (call $known_name (i32.const K_DECIMAL) (@str "http://www.w3.org/2001/XMLSchema#decimal"))
  (call $known_name (i32.const K_DOUBLE) (@str "http://www.w3.org/2001/XMLSchema#double"))
  (call $known_name (i32.const K_BOOLEAN) (@str "http://www.w3.org/2001/XMLSchema#boolean")))

;; A syntax error at the current token, unless the lexer already reported one.
(func $expected (param $msg i32) (param $n i32) (result i32)
  (if (result i32) (i32.eq (global.get $tok) (i32.const T_ERR))
    (then (i32.const 0))
    (else (call $syntax (local.get $msg) (local.get $n)))))

(func $emit (param $s i32) (param $p i32) (param $o i32)
  (call $push_quad (local.get $s) (local.get $p) (local.get $o) (global.get $tg)))

(func $is_iri_tok (result i32)
  (i32.or (i32.eq (global.get $tok) (i32.const T_IRI)) (i32.eq (global.get $tok) (i32.const T_PNAME))))

;; Whether the current token is the word at [s, s+k) (any case when ci).
(func $word (param $s i32) (param $k i32) (param $ci i32) (result i32)
  (if (result i32) (i32.eq (global.get $tok) (i32.const T_NAME))
    (then (call $same (global.get $str) (global.get $str_len) (local.get $s) (local.get $k) (local.get $ci)))
    (else (i32.const 0))))

;; The IRI of the current T_IRI or T_PNAME token (not consumed).
(func $iri_tok (result i32)
  (local $id i32) (local $r i32)
  (if (i32.eq (global.get $tok) (i32.const T_IRI))
    (then (return (call $iri_ref (global.get $str) (global.get $str_len)))))
  (if (i32.eqz (local.tee $id (i32.load offset=12 (call $smap_find (global.get $prefixes) (global.get $pfx) (global.get $pfx_len)))))
    (then (return (call $syntax (@str "undefined prefix")))))
  ;; the prefix IRI, then the local name
  (local.set $r (call $rec (local.get $id)))
  (call $sb_reserve (i32.add (i32.load offset=12 (local.get $r)) (global.get $str_len)))
  (memory.copy (global.get $sb) (i32.load offset=8 (local.get $r)) (i32.load offset=12 (local.get $r)))
  (memory.copy (i32.add (global.get $sb) (i32.load offset=12 (local.get $r))) (global.get $str) (global.get $str_len))
  (local.set $id (i32.add (i32.load offset=12 (local.get $r)) (global.get $str_len)))
  ;; some characters of a local name can make the IRI invalid
  (local.set $r (global.get $str))
  (loop $special
    (if (i32.lt_u (local.get $r) (i32.add (global.get $str) (global.get $str_len))) (then
      (if (call $iri_special (i32.load8_u (local.get $r))) (then
        (if (i32.eqz (call $iri_check (global.get $sb) (local.get $id))) (then (return (i32.const 0))))
        (local.set $r (i32.add (global.get $str) (global.get $str_len)))))
      (local.set $r (i32.add (local.get $r) (i32.const 1)))
      (br $special))))
  (call $iri_const (global.get $sb) (local.get $id)))

;; verb: an IRI or 'a'.
(func $verb (result i32)
  (local $id i32)
  (if (call $word (@str "a") (i32.const 0)) (then (call $lex) (return (call $known (i32.const K_TYPE)))))
  (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected a predicate")))))
  (if (local.tee $id (call $iri_tok)) (then (call $lex)))
  (local.get $id))

;; A term, in the forms f allows besides IRIs and blank nodes; sets $form.
;; Terms nest through collections, property lists, annotations and triple
;; terms; $depth bounds that, so deep input is an error, not a stack overflow.
(func $term (param $f i32) (result i32)
  (local $id i32)
  (if (i32.eqz (call $deeper)) (then (return (i32.const 0))))
  (local.set $id (call $term_in (local.get $f)))
  (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
  (local.get $id))

(func $term_in (param $f i32) (result i32)
  (local $id i32) (local $t i32)
  (local.set $t (global.get $tok))
  (block $done
    (if (call $is_iri_tok) (then
      (br_if $done (i32.eqz (local.tee $id (call $iri_tok))))
      (call $lex)
      (br $done)))
    (if (i32.eq (local.get $t) (i32.const T_BNODE)) (then
      (local.set $id (call $bnode_id (global.get $str) (global.get $str_len)))
      (call $lex)
      (br $done)))
    (if (i32.eq (local.get $t) (i32.const '[')) (then
      (call $lex)
      (local.set $id (call $fresh_bnode))
      (if (i32.eq (global.get $tok) (i32.const ']')) (then (call $lex) (br $done)))
      (if (i32.eqz (i32.and (local.get $f) (i32.const F_BNPL))) (then (return (call $expected (@str "expected ']'")))))
      (if (i32.eqz (call $pol (local.get $id))) (then (return (i32.const 0))))
      (if (i32.ne (global.get $tok) (i32.const ']')) (then (return (call $expected (@str "expected ']'")))))
      (call $lex)
      (global.set $form (i32.const F_BNPL))
      (return (local.get $id))))
    (if (i32.eq (local.get $t) (i32.const '(')) (then
      (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_COLL))))
      (local.set $id (call $collection))
      (global.set $form (i32.const F_COLL))
      (return (local.get $id))))
    (if (i32.eq (local.get $t) (i32.const T_LTLT)) (then
      (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_RT))))
      (local.set $id (call $reified))
      (global.set $form (i32.const F_RT))
      (return (local.get $id))))
    (if (i32.eq (local.get $t) (i32.const T_TT_OPEN)) (then
      (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_TT))))
      (local.set $id (call $triple_term))
      (global.set $form (i32.const F_TT))
      (return (local.get $id))))
    (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_LIT))))
    (local.set $id (call $literal))
    (global.set $form (i32.const F_LIT))
    (return (local.get $id)))
  (global.set $form (i32.const 0))
  (if (result i32) (local.get $id) (then (local.get $id)) (else (call $expected (@str "unexpected token")))))

;; A literal: a string with its tag or datatype, a number or a boolean; 0 for
;; any other token, without an error when it is not one.
(func $literal (result i32)
  (local $v i32) (local $n i32) (local $dt i32) (local $t i32)
  (local.set $t (global.get $tok))
  (local.set $v (global.get $str))
  (local.set $n (global.get $str_len))
  (if (i32.eq (local.get $t) (i32.const T_STRING)) (then
    (call $lex)
    (if (i32.eq (global.get $tok) (i32.const T_LANG)) (then
      (local.set $dt (call $lang_lit (local.get $v) (local.get $n)))
      (call $lex)
      (return (local.get $dt))))
    (local.set $dt (i32.const 2))
    (if (i32.eq (global.get $tok) (i32.const T_HATS)) (then
      (call $lex)
      (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected a datatype IRI")))))
      (if (i32.eqz (local.tee $dt (call $iri_tok))) (then (return (i32.const 0))))
      (if (i32.lt_u (i32.sub (local.get $dt) (i32.const 3)) (i32.const 2))
        (then (return (call $syntax (@str "language string datatype without a language tag")))))
      (call $lex)))
    (return (call $intern (i32.const 3) (local.get $v) (local.get $n) (local.get $dt) (i32.const 0) (i32.const 0) (i32.const 1)))))
  (block $lit
    (local.set $dt (i32.const K_INTEGER))
    (br_if $lit (i32.eq (local.get $t) (i32.const T_INTEGER)))
    (local.set $dt (i32.const K_DECIMAL))
    (br_if $lit (i32.eq (local.get $t) (i32.const T_DECIMAL)))
    (local.set $dt (i32.const K_DOUBLE))
    (br_if $lit (i32.eq (local.get $t) (i32.const T_DOUBLE)))
    (local.set $dt (i32.const K_BOOLEAN))
    ;; (SPARQL keywords, true and false among them, ignore case)
    (br_if $lit (i32.or (call $word (@str "true") (global.get $sparql)) (call $word (@str "false") (global.get $sparql))))
    (return (call $expected (@str "unexpected token"))))
  (if (i32.eq (local.get $dt) (i32.const K_BOOLEAN)) (then (call $lower (local.get $v) (local.get $n))))
  (call $lex)
  (call $intern (i32.const 3) (local.get $v) (local.get $n) (call $known (local.get $dt)) (i32.const 0) (i32.const 0) (i32.const 1)))

;; collection (on '('): its head, or rdf:nil.
(func $collection (result i32)
  (local $head i32) (local $cell i32) (local $next i32) (local $o i32)
  (call $lex)
  (if (i32.eq (global.get $tok) (i32.const ')')) (then (call $lex) (return (call $known (i32.const K_NIL)))))
  (local.set $cell (local.tee $head (call $fresh_bnode)))
  (loop $items
    (if (i32.eqz (local.tee $o (call $term (i32.const F_OBJECT)))) (then (return (i32.const 0))))
    (call $emit (local.get $cell) (call $known (i32.const K_FIRST)) (local.get $o))
    (if (i32.eq (global.get $tok) (i32.const ')')) (then
      (call $emit (local.get $cell) (call $known (i32.const K_REST)) (call $known (i32.const K_NIL)))
      (call $lex)
      (return (local.get $head))))
    (call $emit (local.get $cell) (call $known (i32.const K_REST)) (local.tee $next (call $fresh_bnode)))
    (local.set $cell (local.get $next))
    (br $items))
  (unreachable))

;; An optional reifier after '~' (consumed): an IRI or blank node, else a fresh one.
(func $reifier (result i32)
  (if (result i32) (i32.or (call $is_iri_tok) (i32.or (i32.eq (global.get $tok) (i32.const T_BNODE)) (i32.eq (global.get $tok) (i32.const '['))))
    (then (call $term (i32.const 0)))
    (else (call $fresh_bnode))))

;; reifiedTriple (on '<<'): << s p o (~ reifier?)? >>; its reifier.
(func $reified (result i32)
  (local $s i32) (local $v i32) (local $o i32) (local $r i32)
  (call $lex)
  (if (i32.eqz (local.tee $s (call $term (i32.const F_RT)))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $v (call $verb))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $o (call $term (i32.const F_RT_OBJECT)))) (then (return (i32.const 0))))
  (if (i32.eq (global.get $tok) (i32.const '~')) (then
    (call $lex)
    (if (i32.eqz (local.tee $r (call $reifier))) (then (return (i32.const 0))))))
  (if (i32.ne (global.get $tok) (i32.const T_GTGT)) (then (return (call $expected (@str "expected '>>'")))))
  (call $lex)
  (if (i32.eqz (local.get $r)) (then (local.set $r (call $fresh_bnode))))
  (call $emit (local.get $r) (call $known (i32.const K_REIFIES)) (call $intern_triple (local.get $s) (local.get $v) (local.get $o) (i32.const 1)))
  (local.get $r))

;; tripleTerm (on '<<('): <<( s p o )>>
(func $triple_term (result i32)
  (local $s i32) (local $v i32) (local $o i32)
  (call $lex)
  (if (i32.eqz (local.tee $s (call $term (i32.const 0)))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $v (call $verb))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $o (call $term (i32.const F_TT_OBJECT)))) (then (return (i32.const 0))))
  (if (i32.ne (global.get $tok) (i32.const T_TT_CLOSE)) (then (return (call $expected (@str "expected ')>>'")))))
  (call $lex)
  (call $intern_triple (local.get $s) (local.get $v) (local.get $o) (i32.const 1)))

;; annotation after the object of (s, p, o): (reifier | annotationBlock)*.
;; A block describes the reifier right before it, or a fresh one.
(func $annotation (param $s i32) (param $p i32) (param $o i32) (result i32)
  (local $r i32) (local $pending i32) (local $tt i32)
  (loop $more
    (if (i32.eq (global.get $tok) (i32.const '~')) (then
      (call $lex)
      (if (i32.eqz (local.tee $r (call $reifier))) (then (return (i32.const 0))))
      (if (i32.eqz (local.get $tt)) (then (local.set $tt (call $intern_triple (local.get $s) (local.get $p) (local.get $o) (i32.const 1)))))
      (call $emit (local.get $r) (call $known (i32.const K_REIFIES)) (local.get $tt))
      (local.set $pending (local.get $r))
      (br $more)))
    (if (i32.eq (global.get $tok) (i32.const T_ANN_OPEN)) (then
      (call $lex)
      (if (i32.eqz (local.tee $r (local.get $pending))) (then
        (local.set $r (call $fresh_bnode))
        (if (i32.eqz (local.get $tt)) (then (local.set $tt (call $intern_triple (local.get $s) (local.get $p) (local.get $o) (i32.const 1)))))
        (call $emit (local.get $r) (call $known (i32.const K_REIFIES)) (local.get $tt))))
      (local.set $pending (i32.const 0))
      (if (i32.eqz (call $pol (local.get $r))) (then (return (i32.const 0))))
      (if (i32.ne (global.get $tok) (i32.const T_ANN_CLOSE)) (then (return (call $expected (@str "expected '|}'")))))
      (call $lex)
      (br $more))))
  (i32.const 1))

;; predicateObjectList: verb objectList (';' (verb objectList)?)*
(func $pol (param $s i32) (result i32)
  (local $v i32) (local $o i32)
  (loop $verbs
    (if (i32.eqz (local.tee $v (call $verb))) (then (return (i32.const 0))))
    (loop $objects
      (if (i32.eqz (local.tee $o (call $term (i32.const F_OBJECT)))) (then (return (i32.const 0))))
      (call $emit (local.get $s) (local.get $v) (local.get $o))
      (if (i32.eqz (call $annotation (local.get $s) (local.get $v) (local.get $o))) (then (return (i32.const 0))))
      (if (i32.eq (global.get $tok) (i32.const ',')) (then (call $lex) (br $objects))))
    (if (i32.ne (global.get $tok) (i32.const ';')) (then (return (i32.const 1))))
    (loop $semicolons
      (call $lex)
      (br_if $semicolons (i32.eq (global.get $tok) (i32.const ';'))))
    (br_if $verbs (i32.or (call $is_iri_tok) (call $word (@str "a") (i32.const 0)))))
  (i32.const 1))

;; triples: a subject and its predicateObjectList, which may be left out after
;; [ ... ] and << ... >>. With `trig`, an IRI or blank node subject followed by
;; '{' is a graph name instead: its block is read too.
(func $triples (param $trig i32) (result i32)
  (local $s i32)
  (if (i32.eqz (local.tee $s (call $term (i32.const F_SUBJECT)))) (then (return (i32.const 0))))
  (if (i32.and (local.get $trig) (i32.and (i32.eqz (global.get $form)) (i32.eq (global.get $tok) (i32.const '{'))))
    (then (return (call $graph (local.get $s)))))
  (if (i32.and (i32.ne (i32.and (global.get $form) (i32.const F_ALONE)) (i32.const 0))
               (i32.or (i32.eq (global.get $tok) (i32.const '.')) (i32.eq (global.get $tok) (i32.const '}'))))
    (then (return (i32.const 1))))
  (call $pol (local.get $s)))

;; wrappedGraph (on '{') for graph g: '{' (triples ('.' triples?)*)? '}'
(func $graph (param $g i32) (result i32)
  (local $outer i32)
  (if (i32.ne (global.get $tok) (i32.const '{')) (then (return (call $expected (@str "expected '{'")))))
  (call $lex)
  (local.set $outer (global.get $tg))
  (global.set $tg (local.get $g))
  (block $done
    (loop $statements
      (br_if $done (i32.eq (global.get $tok) (i32.const '}')))
      (if (i32.eqz (call $triples (i32.const 0))) (then (return (i32.const 0))))
      (br_if $done (i32.ne (global.get $tok) (i32.const '.')))
      (call $lex)
      (br $statements)))
  (if (i32.ne (global.get $tok) (i32.const '}')) (then (return (call $expected (@str "expected '}'")))))
  (call $lex)
  (global.set $tg (local.get $outer))
  ;; a graph block is a whole statement: tell the caller there is no '.' to read
  (global.set $form (i32.const -1))
  (i32.const 1))

;; @prefix / PREFIX (after the keyword): PNAME_NS IRIREF
(func $prefix_decl (result i32)
  (local $p i32) (local $n i32) (local $id i32)
  (if (i32.or (i32.ne (global.get $tok) (i32.const T_PNAME)) (global.get $str_len))
    (then (return (call $expected (@str "expected a prefix")))))
  (local.set $p (global.get $pfx))
  (local.set $n (global.get $pfx_len))
  (call $lex)
  (if (i32.ne (global.get $tok) (i32.const T_IRI)) (then (return (call $expected (@str "expected an IRI")))))
  (if (i32.eqz (local.tee $id (call $iri_ref (global.get $str) (global.get $str_len)))) (then (return (i32.const 0))))
  (call $smap_set (global.get $prefixes) (call $smap_find (global.get $prefixes) (local.get $p) (local.get $n))
                  (local.get $p) (local.get $n) (local.get $id))
  (call $lex)
  (i32.const 1))

;; @base / BASE (after the keyword): IRIREF, resolved against the current base.
(func $base_decl (result i32)
  (local $n i32)
  (if (i32.ne (global.get $tok) (i32.const T_IRI)) (then (return (call $expected (@str "expected an IRI")))))
  (local.set $n (call $resolve (global.get $str) (global.get $str_len)))
  (if (i32.gt_u (local.get $n) (global.get $basebuf_cap)) (then
    (if (global.get $basebuf_cap) (then (call $free (global.get $basebuf) (global.get $basebuf_cap))))
    (global.set $basebuf (call $alloc (local.get $n)))
    (global.set $basebuf_cap (call $class_size (call $class (local.get $n))))))
  (memory.copy (global.get $basebuf) (global.get $sb) (local.get $n))
  (global.set $base_p (global.get $basebuf))
  (global.set $base_n (local.get $n))
  (call $lex)
  (i32.const 1))

;; @version / VERSION (after the keyword): a short string.
(func $version_decl (result i32)
  (if (i32.or (i32.ne (global.get $tok) (i32.const T_STRING)) (global.get $str_long))
    (then (return (call $expected (@str "expected a version string")))))
  (call $lex)
  (i32.const 1))

;; A directive at the current token, then (@-forms) its '.': 1 when one was
;; read, 0 after an error, -1 when the token starts no directive.
(func $directive (result i32)
  (local $ok i32) (local $at i32)
  (block $dispatch
    (if (i32.eq (global.get $tok) (i32.const T_LANG)) (then
      (local.set $at (i32.const 1))
      (if (i32.and (i32.eqz (global.get $dir)) (call $same (global.get $str) (global.get $str_len) (@str "prefix") (i32.const 0)))
        (then (call $lex) (local.set $ok (call $prefix_decl)) (br $dispatch)))
      (if (i32.and (i32.eqz (global.get $dir)) (call $same (global.get $str) (global.get $str_len) (@str "base") (i32.const 0)))
        (then (call $lex) (local.set $ok (call $base_decl)) (br $dispatch)))
      (if (i32.and (i32.eqz (global.get $dir)) (call $same (global.get $str) (global.get $str_len) (@str "version") (i32.const 0)))
        (then (call $lex) (local.set $ok (call $version_decl)) (br $dispatch)))
      (return (call $expected (@str "unexpected language tag")))))
    (if (call $word (@str "PREFIX") (i32.const 1)) (then (call $lex) (local.set $ok (call $prefix_decl)) (br $dispatch)))
    (if (call $word (@str "BASE") (i32.const 1)) (then (call $lex) (local.set $ok (call $base_decl)) (br $dispatch)))
    (if (call $word (@str "VERSION") (i32.const 1)) (then (call $lex) (local.set $ok (call $version_decl)) (br $dispatch)))
    (return (i32.const -1)))
  (if (i32.and (local.get $ok) (local.get $at)) (then
    (if (i32.ne (global.get $tok) (i32.const '.')) (then (return (call $expected (@str "expected '.'")))))
    (call $lex)))
  (local.get $ok))

;; A Turtle (trig = 0) or TriG (trig = 1) document; returns -1 after an error.
(func $turtle (param $trig i32) (result i32)
  (local $r i32)
  (call $lex)
  (loop $statements
    (if (i32.eq (global.get $tok) (i32.const T_EOF)) (then (return (i32.const 0))))
    (block $next
      (br_if $next (i32.ge_s (local.tee $r (call $directive)) (i32.const 0)))
      (if (i32.and (local.get $trig) (call $word (@str "GRAPH") (i32.const 1))) (then
        (call $lex)
        (if (i32.eqz (local.tee $r (call $term (i32.const 0)))) (then (return (i32.const -1))))
        (local.set $r (call $graph (local.get $r)))
        (br $next)))
      (if (i32.and (local.get $trig) (i32.eq (global.get $tok) (i32.const '{'))) (then
        (local.set $r (call $graph (global.get $tg)))
        (br $next)))
      (if (i32.eqz (local.tee $r (call $triples (local.get $trig)))) (then (return (i32.const -1))))
      (br_if $next (i32.eq (global.get $form) (i32.const -1)))
      (if (i32.ne (global.get $tok) (i32.const '.')) (then (return (i32.sub (call $expected (@str "expected '.'")) (i32.const 1)))))
      (call $lex))
    (br_if $statements (local.get $r))
    (return (i32.const -1)))
  (unreachable))
