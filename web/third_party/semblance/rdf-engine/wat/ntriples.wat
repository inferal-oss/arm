;; N-Triples and N-Quads (RDF 1.2), on the scanners of lex.wat. Line based:
;; a statement ends its line, white space is only spaces and tabs, IRIs are
;; absolute, strings are short and double quoted.
;;
;; Parsing functions return a term id, or 0 after setting the error. Quads go
;; to the load's quad buffer (load.wat).

;; Skip spaces and tabs.
(func $ws
  (local $c i32)
  (loop $l
    (local.set $c (call $peek))
    (if (i32.or (i32.eq (local.get $c) (i32.const ' ')) (i32.eq (local.get $c) (i32.const '\t'))) (then
      (call $advance (i32.const 1))
      (br $l)))))

;; Skip spaces, tabs and a comment up to the end of the line.
(func $ws_comment
  (local $c i32)
  (call $ws)
  (if (i32.eq (call $peek) (i32.const '#')) (then
    (loop $l
      (local.set $c (call $peek))
      (if (i32.and (i32.ge_s (local.get $c) (i32.const 0))
                   (i32.and (i32.ne (local.get $c) (i32.const '\n')) (i32.ne (local.get $c) (i32.const '\r')))) (then
        (call $advance (i32.const 1))
        (br $l)))))))

;; An absolute IRI (on '<', not '<<').
(func $nt_iri (result i32)
  (if (i32.eqz (call $scan_iri)) (then (return (i32.const 0))))
  (if (i32.eqz (call $scheme_len (global.get $str) (global.get $str_len))) (then (return (call $syntax (@str "relative IRI")))))
  (call $intern (i32.const 1) (global.get $str) (global.get $str_len) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))

(func $nt_predicate (result i32)
  (if (result i32) (i32.and (i32.eq (call $peek) (i32.const '<')) (i32.ne (call $peek_at (i32.const 1)) (i32.const '<')))
    (then (call $nt_iri))
    (else (call $syntax (@str "expected an IRI")))))

;; An IRI or a blank node: a subject, a graph label, or a triple term's subject.
(func $nt_subject (result i32)
  (if (i32.eq (call $peek) (i32.const '_')) (then
    (if (i32.eqz (call $scan_label)) (then (return (i32.const 0))))
    (return (call $bnode_id (global.get $str) (global.get $str_len)))))
  (if (i32.and (i32.eq (call $peek) (i32.const '<')) (i32.ne (call $peek_at (i32.const 1)) (i32.const '<')))
    (then (return (call $nt_iri))))
  (call $syntax (@str "expected an IRI or a blank node")))

(func $nt_literal (result i32)
  (local $v i32) (local $n i32) (local $dt i32)
  (if (i32.eqz (call $scan_string)) (then (return (i32.const 0))))
  (if (global.get $str_long) (then (return (call $syntax (@str "long string in N-Triples")))))
  (local.set $v (global.get $str))
  (local.set $n (global.get $str_len))
  (call $ws)
  (if (i32.eq (call $peek) (i32.const '@')) (then
    (if (i32.eqz (call $scan_lang)) (then (return (i32.const 0))))
    (return (call $lang_lit (local.get $v) (local.get $n)))))
  (local.set $dt (i32.const 2))
  (if (i32.eq (call $peek) (i32.const '^')) (then
    (if (i32.ne (call $peek_at (i32.const 1)) (i32.const '^')) (then (return (call $syntax (@str "expected '^^'")))))
    (call $advance (i32.const 2))
    (call $ws)
    (if (i32.eqz (local.tee $dt (call $nt_predicate))) (then (return (i32.const 0))))
    (if (i32.lt_u (i32.sub (local.get $dt) (i32.const 3)) (i32.const 2))
      (then (return (call $syntax (@str "language string datatype without a language tag")))))))
  (call $intern (i32.const 3) (local.get $v) (local.get $n) (local.get $dt) (i32.const 0) (i32.const 0) (i32.const 1)))

(func $nt_object (result i32)
  (local $c i32)
  (local.set $c (call $peek))
  (if (i32.eq (local.get $c) (i32.const '"')) (then (return (call $nt_literal))))
  (if (i32.and (i32.eq (local.get $c) (i32.const '<')) (i32.eq (call $peek_at (i32.const 1)) (i32.const '<'))) (then
    (if (i32.eq (call $peek_at (i32.const 2)) (i32.const '(')) (then (return (call $nt_triple_term))))
    (return (call $syntax (@str "expected '<<('")))))
  (call $nt_subject))

;; tripleTerm (on '<<(').
(func $nt_triple_term (result i32)
  (local $s i32) (local $pr i32) (local $o i32)
  (if (i32.eqz (call $deeper)) (then (return (i32.const 0))))
  (call $advance (i32.const 3))
  (call $ws)
  (if (i32.eqz (local.tee $s (call $nt_subject))) (then (return (i32.const 0))))
  (call $ws)
  (if (i32.eqz (local.tee $pr (call $nt_predicate))) (then (return (i32.const 0))))
  (call $ws)
  (if (i32.eqz (local.tee $o (call $nt_object))) (then (return (i32.const 0))))
  (call $ws)
  (if (i32.eqz (call $starts (@str ")>>"))) (then (return (call $syntax (@str "expected ')>>'")))))
  (call $advance (i32.const 3))
  (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
  (call $intern_triple (local.get $s) (local.get $pr) (local.get $o) (i32.const 1)))

;; A document (fmt 1 N-Triples, 2 N-Quads); returns the number of statements, or -1.
(func $ntriples (param $fmt i32) (param $graph i32) (result i32)
  (local $c i32) (local $n i32) (local $s i32) (local $pr i32) (local $o i32) (local $g i32)
  (loop $stmt
    (call $ws_comment)
    (local.set $c (call $peek))
    (if (i32.lt_s (local.get $c) (i32.const 0)) (then (return (local.get $n))))
    (if (i32.or (i32.eq (local.get $c) (i32.const '\n')) (i32.eq (local.get $c) (i32.const '\r'))) (then
      (global.set $line (i32.add (global.get $line) (i32.eq (local.get $c) (i32.const '\n'))))
      (call $advance (i32.const 1))
      (br $stmt)))
    (block $statement
      (if (call $starts (@str "VERSION")) (then
        (call $advance (i32.const 7))
        (call $ws)
        (if (i32.ne (call $peek) (i32.const '"')) (then (return (i32.sub (call $syntax (@str "expected a version string")) (i32.const 1)))))
        (if (i32.eqz (call $scan_string)) (then (return (i32.const -1))))
        (br_if $statement (i32.eqz (global.get $str_long)))
        (return (i32.sub (call $syntax (@str "long string in N-Triples")) (i32.const 1)))))
      (if (i32.eqz (local.tee $s (call $nt_subject))) (then (return (i32.const -1))))
      (call $ws)
      (if (i32.eqz (local.tee $pr (call $nt_predicate))) (then (return (i32.const -1))))
      (call $ws)
      (if (i32.eqz (local.tee $o (call $nt_object))) (then (return (i32.const -1))))
      (call $ws)
      (local.set $g (local.get $graph))
      (if (i32.ne (call $peek) (i32.const '.')) (then
        (if (i32.eq (local.get $fmt) (i32.const 1)) (then (return (i32.sub (call $syntax (@str "expected '.'")) (i32.const 1)))))
        (if (i32.eqz (local.tee $g (call $nt_subject))) (then (return (i32.const -1))))
        (call $ws)
        (if (i32.ne (call $peek) (i32.const '.')) (then (return (i32.sub (call $syntax (@str "expected '.'")) (i32.const 1)))))))
      (call $advance (i32.const 1))
      (call $push_quad (local.get $s) (local.get $pr) (local.get $o) (local.get $g))
      (local.set $n (i32.add (local.get $n) (i32.const 1))))
    ;; a statement ends its line
    (call $ws_comment)
    (local.set $c (call $peek))
    (br_if $stmt (i32.or (i32.lt_s (local.get $c) (i32.const 0))
                         (i32.or (i32.eq (local.get $c) (i32.const '\n')) (i32.eq (local.get $c) (i32.const '\r')))))
    (return (i32.sub (call $syntax (@str "expected the end of the line")) (i32.const 1))))
  (unreachable))
