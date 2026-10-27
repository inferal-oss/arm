;; SPARQL 1.2 queries: text -> query structure, on the tokens of lex.wat.
;;
;; Everything a prepared query keeps lives in its arena (freed by release):
;; 32-byte nodes (+0 kind, +4 a ... +24 f, +28 next), variable names, tables.
;; Terms written in the query are interned, so nothing points into the input.
;;
;; A pattern position ("ref") is an i32: > 0 a term id; -1 - slot for a
;; variable (slot < 2^20); a triple term pattern node pointer | 0x80000000.
;; Variables are slots in one row of the whole query: subqueries get their
;; own slots for the variables they do not project.

;; group graph patterns and their elements
(@define P_GROUP 1)     ;; a elements, b filters (P_FILTER), c certain-variable bitset, d/e list tails
(@define P_TRIPLE 2)    ;; a s, b p, c o (refs)
(@define P_PATH 3)      ;; a s, b path, c o
(@define P_OPTIONAL 4)  ;; a group
(@define P_UNION 5)     ;; a group, b group
(@define P_MINUS 6)     ;; a group
(@define P_GRAPH 7)     ;; a ref, b group
(@define P_BIND 8)      ;; a expression, b slot
(@define P_VALUES 9)    ;; a variable count, b slots, c row count, d ids (0 = UNDEF)
(@define P_SUB 10)      ;; a subquery
(@define P_LATERAL 11)  ;; a group
(@define P_FILTER 12)   ;; a expression
(@define P_TT 13)       ;; triple term pattern: a s, b p, c o (refs)
;; property paths
(@define X_LINK 20)     ;; a IRI
(@define X_INV 21)      ;; a path
(@define X_SEQ 22)      ;; a, b paths
(@define X_ALT 23)
(@define X_STAR 24)     ;; a path
(@define X_PLUS 25)
(@define X_OPT 26)
(@define X_NEG 27)      ;; a forward IRIs, b inverse IRIs (X_LINK lists)
;; expressions
(@define E_VAR 30)      ;; a slot
(@define E_CONST 31)    ;; a term
(@define E_CALL 32)     ;; a operator/function, b arguments, c count, d datatype or IRI for casts
(@define E_EXISTS 33)   ;; a group, b 1 for NOT EXISTS
(@define E_AGG 34)      ;; a function, b argument (0: *), c DISTINCT, d separator, e slot, f next aggregate
;; other
(@define N_ITEM 40)     ;; list item: a slot / expression / term, b expression / flag

;; operators and functions (E_CALL a)
(@define F_OR 1) (@define F_AND 2) (@define F_NOT 3) (@define F_EQ 4) (@define F_NE 5)
(@define F_LT 6) (@define F_GT 7) (@define F_LE 8) (@define F_GE 9) (@define F_ADD 10)
(@define F_SUB 11) (@define F_MUL 12) (@define F_DIV 13) (@define F_NEG 14) (@define F_PLUS 15)
(@define F_IN 16) (@define F_NOTIN 17) (@define F_CAST 18) (@define F_CUSTOM 19)
;; built-in functions are 30 and up, in the order of $init_functions
(@define F_STR 30) (@define F_LANG 31) (@define F_LANGMATCHES 32) (@define F_DATATYPE 33)
(@define F_BOUND 34) (@define F_IRI 35) (@define F_URI 36) (@define F_BNODE 37) (@define F_RAND 38)
(@define F_ABS 39) (@define F_CEIL 40) (@define F_FLOOR 41) (@define F_ROUND 42) (@define F_CONCAT 43)
(@define F_SUBSTR 44) (@define F_STRLEN 45) (@define F_REPLACE 46) (@define F_UCASE 47) (@define F_LCASE 48)
(@define F_ENCODE 49) (@define F_CONTAINS 50) (@define F_STRSTARTS 51) (@define F_STRENDS 52)
(@define F_STRBEFORE 53) (@define F_STRAFTER 54) (@define F_YEAR 55) (@define F_MONTH 56) (@define F_DAY 57)
(@define F_HOURS 58) (@define F_MINUTES 59) (@define F_SECONDS 60) (@define F_TIMEZONE 61) (@define F_TZ 62)
(@define F_NOW 63) (@define F_UUID 64) (@define F_STRUUID 65) (@define F_MD5 66) (@define F_SHA1 67)
(@define F_SHA256 68) (@define F_SHA384 69) (@define F_SHA512 70) (@define F_COALESCE 71) (@define F_IF 72)
(@define F_STRLANG 73) (@define F_STRDT 74) (@define F_SAMETERM 75) (@define F_ISIRI 76) (@define F_ISURI 77)
(@define F_ISBLANK 78) (@define F_ISLITERAL 79) (@define F_ISNUMERIC 80) (@define F_REGEX 81)
(@define F_ISTRIPLE 82) (@define F_TRIPLE 83) (@define FN_SUBJECT 84) (@define FN_PREDICATE 85)
(@define FN_OBJECT 86) (@define F_LANGDIR 87) (@define F_HASLANG 88) (@define F_HASLANGDIR 89)
(@define F_STRLANGDIR 90)
(@define F_LAST 90)
;; aggregates (E_AGG a)
(@define A_COUNT 1) (@define A_SUM 2) (@define A_MIN 3) (@define A_MAX 4) (@define A_AVG 5)
(@define A_SAMPLE 6) (@define A_CONCAT 7)

;; The query structure (256 bytes; list tails at +128 + the list's offset):
;;   +0 form (1 SELECT, 2 CONSTRUCT, 3 ASK, 4 DESCRIBE)  +4 flags (Q_*)
;;   +8 projection (N_ITEM a slot, b expression)  +12 WHERE group
;;   +16 GROUP BY (N_ITEM a expression, b slot)  +20 HAVING (P_FILTER)
;;   +24 ORDER BY (N_ITEM a expression, b 1 = DESC)  +28 LIMIT (-1 none)  +32 OFFSET
;;   +36 CONSTRUCT template (P_TRIPLE) or DESCRIBE refs (N_ITEM a ref)
;;   +40 trailing VALUES  +44 FROM (N_ITEM a IRI)  +48 FROM NAMED
;;   +52 slot count  +56 slot entries  +60 parameters (id per slot)  +64 arena
;;   +68 aggregates (E_AGG list)  +72 subquery: projected (N_ITEM a inner slot, b outer slot)
;;   +76 bind_values ids  +80 their count  +84 their slot
;;   +88 cached subquery result: run epoch, +92 table, +96 graph
;;   +100 first projected variable entry count (SELECT var count)  +104 projected entries
(@define Q_DISTINCT 1) (@define Q_REDUCED 2) (@define Q_STAR 4) (@define Q_GROUPED 8)

;; --- arena -----------------------------------------------------------------------

(global $ar_head (mut i32) (i32.const 0))
(global $ar_p (mut i32) (i32.const 0))
(global $ar_end (mut i32) (i32.const 0))

;; n zeroed bytes in the current arena.
(func $anew (param $n i32) (result i32)
  (local $c i32) (local $size i32)
  (local.set $n (i32.and (i32.add (local.get $n) (i32.const 7)) (i32.const -8)))
  (if (i32.gt_u (i32.add (global.get $ar_p) (local.get $n)) (global.get $ar_end)) (then
    (local.set $size (i32.add (local.get $n) (i32.const 8)))
    (if (i32.lt_u (local.get $size) (i32.const 4096)) (then (local.set $size (i32.const 4096))))
    (local.set $c (call $alloc (local.get $size)))
    (i32.store (local.get $c) (global.get $ar_head))
    (i32.store offset=4 (local.get $c) (local.get $size))
    (global.set $ar_head (local.get $c))
    (global.set $ar_p (i32.add (local.get $c) (i32.const 8)))
    (global.set $ar_end (i32.add (local.get $c) (local.get $size)))))
  (local.set $c (global.get $ar_p))
  (global.set $ar_p (i32.add (local.get $c) (local.get $n)))
  (memory.fill (local.get $c) (i32.const 0) (local.get $n))
  (local.get $c))

(func $arena_free (param $h i32)
  (local $next i32)
  (loop $chunks
    (if (local.get $h) (then
      (local.set $next (i32.load (local.get $h)))
      (call $free (local.get $h) (i32.load offset=4 (local.get $h)))
      (local.set $h (local.get $next))
      (br $chunks)))))

(func $node (param $kind i32) (result i32)
  (local $n i32)
  (i32.store (local.tee $n (call $anew (i32.const 32))) (local.get $kind))
  (local.get $n))

(func $node2 (param $kind i32) (param $a i32) (param $b i32) (result i32)
  (local $n i32)
  (local.set $n (call $node (local.get $kind)))
  (i32.store offset=4 (local.get $n) (local.get $a))
  (i32.store offset=8 (local.get $n) (local.get $b))
  (local.get $n))

(func $node3 (param $kind i32) (param $a i32) (param $b i32) (param $c i32) (result i32)
  (local $n i32)
  (local.set $n (call $node2 (local.get $kind) (local.get $a) (local.get $b)))
  (i32.store offset=12 (local.get $n) (local.get $c))
  (local.get $n))

;; Append node n to the list whose head is at `at`, its tail kept at `tail`.
(func $append (param $at i32) (param $tail i32) (param $n i32)
  (if (i32.load (local.get $at))
    (then (i32.store offset=28 (i32.load (local.get $tail)) (local.get $n)))
    (else (i32.store (local.get $at) (local.get $n))))
  (i32.store (local.get $tail) (local.get $n)))

;; Append to a query list (head at q + off); tails live at q + 128 + off.
(func $qappend (param $q i32) (param $off i32) (param $n i32)
  (call $append (i32.add (local.get $q) (local.get $off)) (i32.add (i32.add (local.get $q) (i32.const 128)) (local.get $off)) (local.get $n)))

;; A group's element and filter lists use $append with heads at +4 and +8 and
;; tails at +16 and +20.
(func $add_elem (param $g i32) (param $n i32)
  (call $append (i32.add (local.get $g) (i32.const 4)) (i32.add (local.get $g) (i32.const 16)) (local.get $n)))
(func $add_filter (param $g i32) (param $e i32)
  (call $append (i32.add (local.get $g) (i32.const 8)) (i32.add (local.get $g) (i32.const 20)) (call $node2 (i32.const P_FILTER) (local.get $e) (i32.const 0))))

;; --- variables -------------------------------------------------------------------
;; Entries (28 bytes): +0 name, +4 length, +8 slot, +12 scope, +16 next, +20 flags,
;; +24 the basic graph pattern of a blank node label.
;; Scopes: 0 the query, then one per subquery; -1 anonymous, -2 blank node
;; labels of the pattern, -3 blank node labels of a CONSTRUCT template.

(@define V_HIDDEN 1)     ;; not a SELECT * variable
(@define V_TEMPLATE 2)   ;; a CONSTRUCT template blank node: fresh per solution

(global $vars (mut i32) (i32.const 0))
(global $last_var (mut i32) (i32.const 0))   ;; entry $var_in found or made
(global $bgp (mut i32) (i32.const 1))        ;; the basic graph pattern being parsed
(global $nvars (mut i32) (i32.const 0))
(global $vscope (mut i32) (i32.const 0))
(global $nscopes (mut i32) (i32.const 0))

(func $var_in (param $p i32) (param $n i32) (param $scope i32) (param $flags i32) (result i32)
  (local $e i32) (local $name i32)
  (local.set $e (global.get $vars))
  (loop $find
    (if (local.get $e) (then
      (if (i32.and (i32.eq (i32.load offset=12 (local.get $e)) (local.get $scope))
                   (i32.eq (i32.load offset=4 (local.get $e)) (local.get $n))) (then
        (if (call $memeq (i32.load (local.get $e)) (local.get $p) (local.get $n))
          (then (global.set $last_var (local.get $e)) (return (i32.load offset=8 (local.get $e)))))))
      (local.set $e (i32.load offset=16 (local.get $e)))
      (br $find))))
  (call $new_var (local.get $p) (local.get $n) (local.get $scope) (local.get $flags)))

(func $new_var (param $p i32) (param $n i32) (param $scope i32) (param $flags i32) (result i32)
  (local $e i32) (local $name i32)
  (memory.copy (local.tee $name (call $anew (local.get $n))) (local.get $p) (local.get $n))
  (local.set $e (call $anew (i32.const 28)))
  (global.set $last_var (local.get $e))
  (i32.store (local.get $e) (local.get $name))
  (i32.store offset=4 (local.get $e) (local.get $n))
  (i32.store offset=8 (local.get $e) (global.get $nvars))
  (i32.store offset=12 (local.get $e) (local.get $scope))
  (i32.store offset=16 (local.get $e) (global.get $vars))
  (i32.store offset=20 (local.get $e) (local.get $flags))
  (global.set $vars (local.get $e))
  (global.set $nvars (i32.add (global.get $nvars) (i32.const 1)))
  (i32.load offset=8 (local.get $e)))

;; The slot of the current T_VAR token's variable in the current scope.
(func $var_tok (result i32)
  (call $var_in (global.get $str) (global.get $str_len) (global.get $vscope) (i32.const 0)))

(func $anon (result i32)
  (call $new_var (i32.const 0) (i32.const 0) (i32.const -1) (i32.const V_HIDDEN)))

(func $vref (param $slot i32) (result i32)
  (i32.sub (i32.const -1) (local.get $slot)))

;; --- helpers ---------------------------------------------------------------------

(func $is_kw (param $s i32) (param $k i32) (result i32)
  (call $word (local.get $s) (local.get $k) (i32.const 1)))

;; Consume token t, or fail with msg.
(func $eat (param $t i32) (param $msg i32) (param $n i32) (result i32)
  (if (i32.ne (global.get $tok) (local.get $t)) (then (return (call $expected (local.get $msg) (local.get $n)))))
  (call $lex)
  (i32.const 1))

;; Consume keyword k (case aside), or fail.
(func $eat_kw (param $s i32) (param $k i32) (result i32)
  (if (i32.eqz (call $is_kw (local.get $s) (local.get $k))) (then (return (call $expected (@str "expected a keyword")))))
  (call $lex)
  (i32.const 1))

;; An integer token's value (LIMIT, OFFSET), or -1.
(func $int_tok (result i32)
  (local $v i32) (local $p i32) (local $e i32)
  (if (i32.ne (global.get $tok) (i32.const T_INTEGER)) (then (return (i32.const -1))))
  (local.set $p (global.get $str))
  (local.set $e (i32.add (global.get $str) (global.get $str_len)))
  (if (i32.eq (i32.load8_u (local.get $p)) (i32.const '+')) (then (local.set $p (i32.add (local.get $p) (i32.const 1)))))
  (if (i32.eq (i32.load8_u (local.get $p)) (i32.const '-')) (then (return (i32.const -1))))
  (loop $digits
    (if (i32.lt_u (local.get $p) (local.get $e)) (then
      (local.set $v (i32.add (i32.mul (local.get $v) (i32.const 10)) (i32.sub (i32.load8_u (local.get $p)) (i32.const '0'))))
      (if (i32.gt_u (local.get $v) (i32.const 0x3fffffff)) (then (local.set $v (i32.const 0x3fffffff))))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (br $digits))))
  (call $lex)
  (local.get $v))

;; --- functions table -------------------------------------------------------------
;; $fnames: per code from 30, 12 bytes: name ptr, name len, min args | max args << 8.

(global $fnames (mut i32) (i32.const 0))

(func $fdef (param $code i32) (param $p i32) (param $n i32) (param $min i32) (param $max i32)
  (local $at i32)
  (local.set $at (i32.add (global.get $fnames) (i32.mul (i32.sub (local.get $code) (i32.const 30)) (i32.const 12))))
  (i32.store (local.get $at) (local.get $p))
  (i32.store offset=4 (local.get $at) (local.get $n))
  (i32.store offset=8 (local.get $at) (i32.or (local.get $min) (i32.shl (local.get $max) (i32.const 8)))))

(func $init_functions
  (global.set $fnames (call $alloc0 (i32.const 780)))
  (call $fdef (i32.const F_STR) (@str "STR") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_LANG) (@str "LANG") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_LANGMATCHES) (@str "LANGMATCHES") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_DATATYPE) (@str "DATATYPE") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_BOUND) (@str "BOUND") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_IRI) (@str "IRI") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_URI) (@str "URI") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_BNODE) (@str "BNODE") (i32.const 0) (i32.const 1))
  (call $fdef (i32.const F_RAND) (@str "RAND") (i32.const 0) (i32.const 0))
  (call $fdef (i32.const F_ABS) (@str "ABS") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_CEIL) (@str "CEIL") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_FLOOR) (@str "FLOOR") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_ROUND) (@str "ROUND") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_CONCAT) (@str "CONCAT") (i32.const 0) (i32.const 255))
  (call $fdef (i32.const F_SUBSTR) (@str "SUBSTR") (i32.const 2) (i32.const 3))
  (call $fdef (i32.const F_STRLEN) (@str "STRLEN") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_REPLACE) (@str "REPLACE") (i32.const 3) (i32.const 4))
  (call $fdef (i32.const F_UCASE) (@str "UCASE") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_LCASE) (@str "LCASE") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_ENCODE) (@str "ENCODE_FOR_URI") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_CONTAINS) (@str "CONTAINS") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_STRSTARTS) (@str "STRSTARTS") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_STRENDS) (@str "STRENDS") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_STRBEFORE) (@str "STRBEFORE") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_STRAFTER) (@str "STRAFTER") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_YEAR) (@str "YEAR") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_MONTH) (@str "MONTH") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_DAY) (@str "DAY") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_HOURS) (@str "HOURS") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_MINUTES) (@str "MINUTES") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_SECONDS) (@str "SECONDS") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_TIMEZONE) (@str "TIMEZONE") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_TZ) (@str "TZ") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_NOW) (@str "NOW") (i32.const 0) (i32.const 0))
  (call $fdef (i32.const F_UUID) (@str "UUID") (i32.const 0) (i32.const 0))
  (call $fdef (i32.const F_STRUUID) (@str "STRUUID") (i32.const 0) (i32.const 0))
  (call $fdef (i32.const F_MD5) (@str "MD5") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_SHA1) (@str "SHA1") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_SHA256) (@str "SHA256") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_SHA384) (@str "SHA384") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_SHA512) (@str "SHA512") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_COALESCE) (@str "COALESCE") (i32.const 0) (i32.const 255))
  (call $fdef (i32.const F_IF) (@str "IF") (i32.const 3) (i32.const 3))
  (call $fdef (i32.const F_STRLANG) (@str "STRLANG") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_STRDT) (@str "STRDT") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_SAMETERM) (@str "sameTerm") (i32.const 2) (i32.const 2))
  (call $fdef (i32.const F_ISIRI) (@str "isIRI") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_ISURI) (@str "isURI") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_ISBLANK) (@str "isBLANK") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_ISLITERAL) (@str "isLITERAL") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_ISNUMERIC) (@str "isNUMERIC") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_REGEX) (@str "REGEX") (i32.const 2) (i32.const 3))
  (call $fdef (i32.const F_ISTRIPLE) (@str "isTRIPLE") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_TRIPLE) (@str "TRIPLE") (i32.const 3) (i32.const 3))
  (call $fdef (i32.const FN_SUBJECT) (@str "SUBJECT") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const FN_PREDICATE) (@str "PREDICATE") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const FN_OBJECT) (@str "OBJECT") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_LANGDIR) (@str "LANGDIR") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_HASLANG) (@str "hasLANG") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_HASLANGDIR) (@str "hasLANGDIR") (i32.const 1) (i32.const 1))
  (call $fdef (i32.const F_STRLANGDIR) (@str "STRLANGDIR") (i32.const 3) (i32.const 3)))

;; The built-in function named by the current T_NAME, or 0.
(func $fn_code (result i32)
  (local $c i32) (local $at i32)
  (local.set $c (i32.const 30))
  (loop $each
    (local.set $at (i32.add (global.get $fnames) (i32.mul (i32.sub (local.get $c) (i32.const 30)) (i32.const 12))))
    (if (call $same (global.get $str) (global.get $str_len) (i32.load (local.get $at)) (i32.load offset=4 (local.get $at)) (i32.const 1))
      (then (return (local.get $c))))
    (br_if $each (i32.le_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.const F_LAST))))
  (i32.const 0))

;; The aggregate named by the current T_NAME, or 0.
(func $agg_code (result i32)
  (if (call $is_kw (@str "COUNT")) (then (return (i32.const A_COUNT))))
  (if (call $is_kw (@str "SUM")) (then (return (i32.const A_SUM))))
  (if (call $is_kw (@str "MIN")) (then (return (i32.const A_MIN))))
  (if (call $is_kw (@str "MAX")) (then (return (i32.const A_MAX))))
  (if (call $is_kw (@str "AVG")) (then (return (i32.const A_AVG))))
  (if (call $is_kw (@str "SAMPLE")) (then (return (i32.const A_SAMPLE))))
  (if (call $is_kw (@str "GROUP_CONCAT")) (then (return (i32.const A_CONCAT))))
  (i32.const 0))

;; --- expressions -----------------------------------------------------------------

(global $cq (mut i32) (i32.const 0))       ;; the (sub)query being parsed
(global $in_agg (mut i32) (i32.const 0))   ;; inside an aggregate's argument
(global $aggs_ok (mut i32) (i32.const 0))  ;; aggregates allowed here

(func $call1 (param $f i32) (param $a i32) (result i32)
  (call $node3 (i32.const E_CALL) (local.get $f) (local.get $a) (i32.const 1)))

(func $call2 (param $f i32) (param $a i32) (param $b i32) (result i32)
  (i32.store offset=28 (local.get $a) (local.get $b))
  (call $node3 (i32.const E_CALL) (local.get $f) (local.get $a) (i32.const 2)))

(func $expr (result i32)
  (local $a i32) (local $b i32)
  (if (i32.eqz (call $deeper)) (then (return (i32.const 0))))
  (block $out
    (br_if $out (i32.eqz (local.tee $a (call $and_expr))))
    (loop $ors
      (if (i32.eq (global.get $tok) (i32.const T_OR)) (then
        (call $lex)
        (if (i32.eqz (local.tee $b (call $and_expr))) (then (local.set $a (i32.const 0)) (br $out)))
        (local.set $a (call $call2 (i32.const F_OR) (local.get $a) (local.get $b)))
        (br $ors)))))
  (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
  (local.get $a))

(func $and_expr (result i32)
  (local $a i32) (local $b i32)
  (if (i32.eqz (local.tee $a (call $rel_expr))) (then (return (i32.const 0))))
  (loop $ands
    (if (i32.eq (global.get $tok) (i32.const T_AND)) (then
      (call $lex)
      (if (i32.eqz (local.tee $b (call $rel_expr))) (then (return (i32.const 0))))
      (local.set $a (call $call2 (i32.const F_AND) (local.get $a) (local.get $b)))
      (br $ands))))
  (local.get $a))

(func $rel_expr (result i32)
  (local $a i32) (local $b i32) (local $op i32) (local $t i32)
  (if (i32.eqz (local.tee $a (call $add_expr))) (then (return (i32.const 0))))
  (local.set $t (global.get $tok))
  (local.set $op
    (if (result i32) (i32.eq (local.get $t) (i32.const '=')) (then (i32.const F_EQ))
    (else (if (result i32) (i32.eq (local.get $t) (i32.const T_NE)) (then (i32.const F_NE))
    (else (if (result i32) (i32.eq (local.get $t) (i32.const '<')) (then (i32.const F_LT))
    (else (if (result i32) (i32.eq (local.get $t) (i32.const '>')) (then (i32.const F_GT))
    (else (if (result i32) (i32.eq (local.get $t) (i32.const T_LE)) (then (i32.const F_LE))
    (else (if (result i32) (i32.eq (local.get $t) (i32.const T_GE)) (then (i32.const F_GE))
    (else (i32.const 0))))))))))))))
  (if (local.get $op) (then
    (call $lex)
    (if (i32.eqz (local.tee $b (call $add_expr))) (then (return (i32.const 0))))
    (return (call $call2 (local.get $op) (local.get $a) (local.get $b)))))
  ;; IN / NOT IN
  (if (call $is_kw (@str "NOT")) (then
    (call $lex)
    (if (i32.eqz (call $is_kw (@str "IN"))) (then (return (call $expected (@str "expected IN")))))
    (local.set $op (i32.const F_NOTIN))))
  (if (i32.or (local.get $op) (call $is_kw (@str "IN"))) (then
    (call $lex)
    (if (i32.eqz (local.get $op)) (then (local.set $op (i32.const F_IN))))
    (if (i32.eqz (local.tee $b (call $arg_list (local.get $a)))) (then (return (i32.const 0))))
    (i32.store offset=4 (local.get $b) (local.get $op))
    (return (local.get $b))))
  (local.get $a))

;; Whether the current token is a number written with a sign.
(func $signed_num (result i32)
  (local $c i32)
  (if (i32.eqz (i32.and (i32.ge_u (global.get $tok) (i32.const T_INTEGER)) (i32.le_u (global.get $tok) (i32.const T_DOUBLE))))
    (then (return (i32.const 0))))
  (local.set $c (i32.load8_u (global.get $str)))
  (i32.or (i32.eq (local.get $c) (i32.const '+')) (i32.eq (local.get $c) (i32.const '-'))))

(func $add_expr (result i32)
  (local $a i32) (local $b i32) (local $op i32)
  (if (i32.eqz (local.tee $a (call $mul_expr))) (then (return (i32.const 0))))
  (loop $terms
    (if (i32.or (i32.eq (global.get $tok) (i32.const '+')) (i32.eq (global.get $tok) (i32.const '-'))) (then
      (local.set $op (select (i32.const F_ADD) (i32.const F_SUB) (i32.eq (global.get $tok) (i32.const '+'))))
      (call $lex)
      (if (i32.eqz (local.tee $b (call $mul_expr))) (then (return (i32.const 0))))
      (local.set $a (call $call2 (local.get $op) (local.get $a) (local.get $b)))
      (br $terms)))
    ;; "?a -1": a signed number continues the sum
    (if (call $signed_num) (then
      (if (i32.eqz (local.tee $b (call $mul_rest (call $literal_expr)))) (then (return (i32.const 0))))
      (local.set $a (call $call2 (i32.const F_ADD) (local.get $a) (local.get $b)))
      (br $terms))))
  (local.get $a))

(func $mul_expr (result i32)
  (local $a i32)
  (if (i32.eqz (local.tee $a (call $unary_expr))) (then (return (i32.const 0))))
  (call $mul_rest (local.get $a)))

(func $mul_rest (param $a i32) (result i32)
  (local $b i32) (local $op i32)
  (if (i32.eqz (local.get $a)) (then (return (i32.const 0))))
  (loop $factors
    (if (i32.or (i32.eq (global.get $tok) (i32.const '*')) (i32.eq (global.get $tok) (i32.const '/'))) (then
      (local.set $op (select (i32.const F_MUL) (i32.const F_DIV) (i32.eq (global.get $tok) (i32.const '*'))))
      (call $lex)
      (if (i32.eqz (local.tee $b (call $unary_expr))) (then (return (i32.const 0))))
      (local.set $a (call $call2 (local.get $op) (local.get $a) (local.get $b)))
      (br $factors))))
  (local.get $a))

(func $unary_expr (result i32)
  (local $a i32) (local $op i32)
  (local.set $op
    (if (result i32) (i32.eq (global.get $tok) (i32.const '!')) (then (i32.const F_NOT))
    (else (if (result i32) (i32.eq (global.get $tok) (i32.const '-')) (then (i32.const F_NEG))
    (else (if (result i32) (i32.eq (global.get $tok) (i32.const '+')) (then (i32.const F_PLUS))
    (else (i32.const 0))))))))
  (if (i32.eqz (local.get $op)) (then (return (call $primary))))
  (call $lex)
  (if (i32.eqz (local.tee $a (call $unary_expr))) (then (return (i32.const 0))))
  (call $call1 (local.get $op) (local.get $a)))

;; A literal token as a constant (not consumed on failure).
(func $literal_expr (result i32)
  (local $id i32)
  (if (i32.eqz (local.tee $id (call $literal))) (then (return (i32.const 0))))
  (call $node2 (i32.const E_CONST) (local.get $id) (i32.const 0)))

;; '(' Expression (',' Expression)* ')' or NIL, as an E_CALL with `first`
;; (when not 0) as the first argument.
(func $arg_list (param $first i32) (result i32)
  (local $call i32) (local $e i32) (local $n i32) (local $tail i32)
  (local.set $call (call $node (i32.const E_CALL)))
  (if (local.get $first) (then
    (i32.store offset=8 (local.get $call) (local.get $first))
    (local.set $tail (local.get $first))
    (local.set $n (i32.const 1))))
  (if (i32.eqz (call $eat (i32.const '(') (@str "expected '('"))) (then (return (i32.const 0))))
  (if (i32.ne (global.get $tok) (i32.const ')')) (then
    (loop $args
      (if (i32.eqz (local.tee $e (call $expr))) (then (return (i32.const 0))))
      (if (local.get $tail)
        (then (i32.store offset=28 (local.get $tail) (local.get $e)))
        (else (i32.store offset=8 (local.get $call) (local.get $e))))
      (local.set $tail (local.get $e))
      (local.set $n (i32.add (local.get $n) (i32.const 1)))
      (if (i32.eq (global.get $tok) (i32.const ',')) (then (call $lex) (br $args))))))
  (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0))))
  (i32.store offset=12 (local.get $call) (local.get $n))
  (local.get $call))

(func $primary (result i32)
  (local $id i32) (local $c i32) (local $n i32) (local $g i32)
  (if (i32.eq (global.get $tok) (i32.const '(')) (then
    (call $lex)
    (if (i32.eqz (local.tee $id (call $expr))) (then (return (i32.const 0))))
    (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0))))
    (return (local.get $id))))
  (if (i32.eq (global.get $tok) (i32.const T_VAR)) (then
    (local.set $id (call $var_tok))
    (call $lex)
    (return (call $node2 (i32.const E_VAR) (local.get $id) (i32.const 0)))))
  (if (call $is_iri_tok) (then
    ;; an IRI, or a function call: a cast or an unknown function
    (if (i32.eqz (local.tee $id (call $iri_tok))) (then (return (i32.const 0))))
    (call $lex)
    (if (i32.ne (global.get $tok) (i32.const '(')) (then (return (call $node2 (i32.const E_CONST) (local.get $id) (i32.const 0)))))
    (if (i32.eqz (local.tee $c (call $arg_list (i32.const 0)))) (then (return (i32.const 0))))
    (i32.store offset=4 (local.get $c) (select (i32.const F_CAST) (i32.const F_CUSTOM) (call $is_cast (local.get $id))))
    (i32.store offset=16 (local.get $c) (local.get $id))
    (return (local.get $c))))
  (if (i32.eq (global.get $tok) (i32.const T_TT_OPEN)) (then (return (call $tt_expr))))
  (if (i32.eq (global.get $tok) (i32.const T_NAME)) (then
    (if (i32.or (call $is_kw (@str "EXISTS")) (call $is_kw (@str "NOT"))) (then
      (local.set $n (call $is_kw (@str "NOT")))
      (call $lex)
      (if (local.get $n) (then (if (i32.eqz (call $eat_kw (@str "EXISTS"))) (then (return (i32.const 0))))))
      (if (i32.eqz (local.tee $g (call $ggp))) (then (return (i32.const 0))))
      (return (call $node2 (i32.const E_EXISTS) (local.get $g) (local.get $n)))))
    (if (local.tee $c (call $agg_code)) (then (return (call $aggregate (local.get $c)))))
    (if (local.tee $c (call $fn_code)) (then
      (call $lex)
      (if (i32.eqz (local.tee $id (call $fn_args (local.get $c)))) (then (return (i32.const 0))))
      (i32.store offset=4 (local.get $id) (local.get $c))
      (return (local.get $id))))))
  (call $literal_expr))

;; The arguments of built-in function c, checked against its arity.
(func $fn_args (param $c i32) (result i32)
  (local $call i32) (local $a i32)
  (local.set $a (i32.load offset=8 (i32.add (global.get $fnames) (i32.mul (i32.sub (local.get $c) (i32.const 30)) (i32.const 12)))))
  ;; NIL for functions without arguments
  (if (i32.eqz (local.tee $call (call $arg_list (i32.const 0)))) (then (return (i32.const 0))))
  (if (i32.or (i32.lt_u (i32.load offset=12 (local.get $call)) (i32.and (local.get $a) (i32.const 0xff)))
              (i32.gt_u (i32.load offset=12 (local.get $call)) (i32.shr_u (local.get $a) (i32.const 8))))
    (then (return (call $syntax (@str "wrong number of arguments")))))
  (local.get $call))

;; Whether IRI id is an XSD datatype a cast can target.
(func $is_cast (param $id i32) (result i32)
  (local $r i32) (local $n i32)
  (local.set $r (call $rec (local.get $id)))
  (local.set $n (i32.load offset=12 (local.get $r)))
  (if (i32.le_u (local.get $n) (i32.const 33)) (then (return (i32.const 0))))
  (if (i32.eqz (call $memeq (i32.load offset=8 (local.get $r)) (@str "http://www.w3.org/2001/XMLSchema#"))) (then (return (i32.const 0))))
  (call $xsd_cast_type (i32.add (i32.load offset=8 (local.get $r)) (i32.const 33)) (i32.sub (local.get $n) (i32.const 33))))

;; <<( s p o )>> in an expression: TRIPLE(s, p, o).
(func $tt_expr (result i32)
  (local $call i32) (local $a i32) (local $b i32) (local $c i32)
  (call $lex)
  (if (i32.eqz (local.tee $a (call $tt_part))) (then (return (i32.const 0))))
  ;; the subject: a variable, an IRI or a blank node
  (if (i32.or (i32.eq (i32.load (local.get $a)) (i32.const E_CALL))
              (i32.and (i32.eq (i32.load (local.get $a)) (i32.const E_CONST)) (i32.ge_u (call $kind (i32.load offset=4 (local.get $a))) (i32.const 3))))
    (then (return (call $syntax (@str "bad triple term subject")))))
  (if (i32.eqz (local.tee $b (call $tt_part))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $c (call $tt_part))) (then (return (i32.const 0))))
  (if (i32.eqz (call $eat (i32.const T_TT_CLOSE) (@str "expected ')>>'"))) (then (return (i32.const 0))))
  (i32.store offset=28 (local.get $a) (local.get $b))
  (i32.store offset=28 (local.get $b) (local.get $c))
  (call $node3 (i32.const E_CALL) (i32.const F_TRIPLE) (local.get $a) (i32.const 3)))

(func $tt_part (result i32)
  (local $id i32)
  (if (i32.eq (global.get $tok) (i32.const T_VAR)) (then
    (local.set $id (call $var_tok))
    (call $lex)
    (return (call $node2 (i32.const E_VAR) (local.get $id) (i32.const 0)))))
  (if (i32.eq (global.get $tok) (i32.const T_TT_OPEN)) (then (return (call $tt_expr))))
  (if (call $word (@str "a") (i32.const 0)) (then
    (call $lex)
    (return (call $node2 (i32.const E_CONST) (call $known (i32.const K_TYPE)) (i32.const 0)))))
  (if (call $is_iri_tok) (then
    (if (i32.eqz (local.tee $id (call $iri_tok))) (then (return (i32.const 0))))
    (call $lex)
    (return (call $node2 (i32.const E_CONST) (local.get $id) (i32.const 0)))))
  (call $literal_expr))

;; An aggregate call (on its name).
(func $aggregate (param $f i32) (result i32)
  (local $n i32) (local $e i32)
  (if (i32.or (i32.eqz (global.get $aggs_ok)) (global.get $in_agg)) (then (return (call $syntax (@str "aggregate not allowed here")))))
  (local.set $n (call $node (i32.const E_AGG)))
  (i32.store offset=4 (local.get $n) (local.get $f))
  (call $lex)
  (if (i32.eqz (call $eat (i32.const '(') (@str "expected '('"))) (then (return (i32.const 0))))
  (if (call $is_kw (@str "DISTINCT")) (then (call $lex) (i32.store offset=12 (local.get $n) (i32.const 1))))
  (global.set $in_agg (i32.const 1))
  (if (i32.and (i32.eq (local.get $f) (i32.const A_COUNT)) (i32.eq (global.get $tok) (i32.const '*')))
    (then (call $lex))
    (else
      (if (i32.eqz (local.tee $e (call $expr))) (then (return (i32.const 0))))
      (i32.store offset=8 (local.get $n) (local.get $e))))
  (global.set $in_agg (i32.const 0))
  (if (i32.and (i32.eq (local.get $f) (i32.const A_CONCAT)) (i32.eq (global.get $tok) (i32.const ';'))) (then
    (call $lex)
    (if (i32.eqz (call $eat_kw (@str "SEPARATOR"))) (then (return (i32.const 0))))
    (if (i32.eqz (call $eat (i32.const '=') (@str "expected '='"))) (then (return (i32.const 0))))
    (if (i32.ne (global.get $tok) (i32.const T_STRING)) (then (return (call $expected (@str "expected a string")))))
    (i32.store offset=16 (local.get $n) (call $intern (i32.const 3) (global.get $str) (global.get $str_len) (i32.const 2) (i32.const 0) (i32.const 0) (i32.const 1)))
    (call $lex)))
  (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0))))
  (i32.store offset=20 (local.get $n) (call $anon))
  (i32.store offset=24 (local.get $n) (i32.load offset=68 (global.get $cq)))
  (i32.store offset=68 (global.get $cq) (local.get $n))
  (i32.store offset=4 (global.get $cq) (i32.or (i32.load offset=4 (global.get $cq)) (i32.const Q_GROUPED)))
  (local.get $n))

;; --- graph patterns --------------------------------------------------------------

;; GroupGraphPattern (on '{').
(func $ggp (result i32)
  (local $g i32) (local $q i32)
  (if (i32.eqz (call $deeper)) (then (return (i32.const 0))))
  (if (i32.eqz (call $eat (i32.const '{') (@str "expected '{'"))) (then (return (i32.const 0))))
  (local.set $g (call $node (i32.const P_GROUP)))
  (global.set $bgp (i32.add (global.get $bgp) (i32.const 1)))
  (if (call $is_kw (@str "SELECT"))
    (then
      (if (i32.eqz (local.tee $q (call $subselect))) (then (return (i32.const 0))))
      (call $add_elem (local.get $g) (call $node2 (i32.const P_SUB) (local.get $q) (i32.const 0))))
    (else (if (i32.eqz (call $ggp_sub (local.get $g))) (then (return (i32.const 0))))))
  (if (i32.eqz (call $eat (i32.const '}') (@str "expected '}'"))) (then (return (i32.const 0))))
  (global.set $bgp (i32.add (global.get $bgp) (i32.const 1)))
  (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
  (local.get $g))

(func $ggp_sub (param $g i32) (result i32)
  (local $n i32) (local $e i32) (local $s i32) (local $t i32) (local $need_dot i32)
  (loop $elements
    (local.set $t (global.get $tok))
    (if (i32.eq (local.get $t) (i32.const '}')) (then (return (i32.const 1))))
    (block $next
      (if (i32.eq (local.get $t) (i32.const '{')) (then
        ;; a group, or groups joined by UNION
        (if (i32.eqz (local.tee $n (call $ggp))) (then (return (i32.const 0))))
        (if (call $is_kw (@str "UNION")) (then
          (loop $unions
            (call $lex)
            (if (i32.eqz (local.tee $e (call $ggp))) (then (return (i32.const 0))))
            (local.set $n (call $node2 (i32.const P_UNION) (local.get $n) (local.get $e)))
            (br_if $unions (call $is_kw (@str "UNION"))))
          (call $add_elem (local.get $g) (local.get $n))
          (br $next)))
        (call $add_elem (local.get $g) (local.get $n))
        (br $next)))
      (if (i32.eq (local.get $t) (i32.const T_NAME)) (then
        (if (call $is_kw (@str "OPTIONAL")) (then
          (call $lex)
          (if (i32.eqz (local.tee $n (call $ggp))) (then (return (i32.const 0))))
          (call $add_elem (local.get $g) (call $node2 (i32.const P_OPTIONAL) (local.get $n) (i32.const 0)))
          (br $next)))
        (if (call $is_kw (@str "MINUS")) (then
          (call $lex)
          (if (i32.eqz (local.tee $n (call $ggp))) (then (return (i32.const 0))))
          (call $add_elem (local.get $g) (call $node2 (i32.const P_MINUS) (local.get $n) (i32.const 0)))
          (br $next)))
        (if (call $is_kw (@str "LATERAL")) (then
          (call $lex)
          (if (i32.eqz (local.tee $n (call $ggp))) (then (return (i32.const 0))))
          (call $add_elem (local.get $g) (call $node2 (i32.const P_LATERAL) (local.get $n) (i32.const 0)))
          (br $next)))
        (if (call $is_kw (@str "GRAPH")) (then
          (call $lex)
          (if (i32.eqz (local.tee $s (call $var_or_iri))) (then (return (i32.const 0))))
          (if (i32.eqz (local.tee $n (call $ggp))) (then (return (i32.const 0))))
          (call $add_elem (local.get $g) (call $node2 (i32.const P_GRAPH) (local.get $s) (local.get $n)))
          (br $next)))
        (if (call $is_kw (@str "SERVICE")) (then (return (call $syntax (@str "SERVICE is not supported")))))
        (if (call $is_kw (@str "FILTER")) (then
          (call $lex)
          (if (i32.eqz (local.tee $e (call $constraint))) (then (return (i32.const 0))))
          (call $add_filter (local.get $g) (local.get $e))
          ;; a filter does not end the basic graph pattern
          (local.set $need_dot (i32.const 0))
          (if (i32.eq (global.get $tok) (i32.const '.')) (then (call $lex)))
          (br $elements)))
        (if (call $is_kw (@str "BIND")) (then
          (call $lex)
          (if (i32.eqz (call $eat (i32.const '(') (@str "expected '('"))) (then (return (i32.const 0))))
          (if (i32.eqz (local.tee $e (call $expr))) (then (return (i32.const 0))))
          (if (i32.eqz (call $eat_kw (@str "AS"))) (then (return (i32.const 0))))
          (if (i32.ne (global.get $tok) (i32.const T_VAR)) (then (return (call $expected (@str "expected a variable")))))
          (local.set $s (call $var_tok))
          (call $lex)
          (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0))))
          (if (call $elems_bind (i32.load offset=4 (local.get $g)) (local.get $s))
            (then (return (call $syntax (@str "BIND to a variable already in scope")))))
          (call $add_elem (local.get $g) (call $node2 (i32.const P_BIND) (local.get $e) (local.get $s)))
          (global.set $bgp (i32.add (global.get $bgp) (i32.const 1)))
          (br $next)))
        (if (call $is_kw (@str "VALUES")) (then
          (call $lex)
          (if (i32.eqz (local.tee $n (call $data_block))) (then (return (i32.const 0))))
          (call $add_elem (local.get $g) (local.get $n))
          (br $next)))))
      ;; triples: then '.', '}', or a pattern that is not triples
      (if (local.get $need_dot) (then (return (call $expected (@str "expected '.'")))))
      (if (i32.eqz (call $ptriples (local.get $g))) (then (return (i32.const 0))))
      (br_if $elements (i32.eq (global.get $tok) (i32.const '}')))
      (if (i32.ne (global.get $tok) (i32.const '.')) (then
        (local.set $need_dot (i32.const 1))
        (br $elements)))
      (call $lex)
      (br $elements))
    ;; any other element ends the basic graph pattern
    (local.set $need_dot (i32.const 0))
    (global.set $bgp (i32.add (global.get $bgp) (i32.const 1)))
    (if (i32.eq (global.get $tok) (i32.const '.')) (then (call $lex)))
    (br $elements))
  (unreachable))

;; FILTER's argument: a bracketted expression, a built-in or a function call.
(func $constraint (result i32)
  (if (i32.or (i32.eq (global.get $tok) (i32.const '(')) (i32.or (i32.eq (global.get $tok) (i32.const T_NAME)) (call $is_iri_tok)))
    (then (return (call $primary))))
  (call $expected (@str "expected a constraint")))

(func $var_or_iri (result i32)
  (local $id i32)
  (if (i32.eq (global.get $tok) (i32.const T_VAR)) (then
    (local.set $id (call $var_tok))
    (call $lex)
    (return (call $vref (local.get $id)))))
  (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected a variable or an IRI")))))
  (if (local.tee $id (call $iri_tok)) (then (call $lex)))
  (local.get $id))

;; VALUES DataBlock (after VALUES): a P_VALUES node.
(func $data_block (result i32)
  (local $n i32) (local $vars i32) (local $nv i32) (local $rows i32) (local $nr i32) (local $single i32)
  (local $buf i32) (local $cap i32) (local $k i32) (local $id i32) (local $tmp i32)
  ;; variables, kept in a scratch list: at most 64
  (local.set $vars (call $anew (i32.const 256)))
  (if (i32.eq (global.get $tok) (i32.const T_VAR))
    (then
      (local.set $single (i32.const 1))
      (i32.store (local.get $vars) (call $var_tok))
      (local.set $nv (i32.const 1))
      (call $lex))
    (else
      (if (i32.eqz (call $eat (i32.const '(') (@str "expected '(' or a variable"))) (then (return (i32.const 0))))
      (loop $names
        (if (i32.eq (global.get $tok) (i32.const T_VAR)) (then
          (if (i32.ge_u (local.get $nv) (i32.const 64)) (then (return (call $syntax (@str "too many VALUES variables")))))
          (local.set $id (call $var_tok))
          (local.set $k (i32.const 0))
          (loop $dups (if (i32.lt_u (local.get $k) (local.get $nv)) (then
            (if (i32.eq (i32.load (i32.add (local.get $vars) (i32.shl (local.get $k) (i32.const 2)))) (local.get $id))
              (then (return (call $syntax (@str "variable twice in VALUES")))))
            (local.set $k (i32.add (local.get $k) (i32.const 1))) (br $dups))))
          (i32.store (i32.add (local.get $vars) (i32.shl (local.get $nv) (i32.const 2))) (local.get $id))
          (local.set $nv (i32.add (local.get $nv) (i32.const 1)))
          (call $lex)
          (br $names))))
      (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0))))))
  (if (i32.eqz (call $eat (i32.const '{') (@str "expected '{'"))) (then (return (i32.const 0))))
  ;; rows, into a growing scratch block, copied to the arena at the end
  (local.set $cap (i32.const 64))
  (local.set $buf (call $alloc (i32.const 256)))
  (block $fail
    (loop $row
      (if (i32.ne (global.get $tok) (i32.const '}')) (then
        (if (i32.eqz (local.get $single)) (then
          (br_if $fail (i32.eqz (call $eat (i32.const '(') (@str "expected '('"))))))
        (local.set $k (i32.const 0))
        (loop $values
          (if (i32.or (local.get $single) (i32.ne (global.get $tok) (i32.const ')'))) (then
            (if (call $is_kw (@str "UNDEF"))
              (then (call $lex) (local.set $id (i32.const 0)))
              (else
                (if (call $is_iri_tok)
                  (then
                    (br_if $fail (i32.eqz (local.tee $id (call $iri_tok))))
                    (call $lex))
                  (else
                    (if (i32.eq (global.get $tok) (i32.const T_TT_OPEN))
                      (then (br_if $fail (i32.eqz (local.tee $id (call $tt_const)))))
                      (else (br_if $fail (i32.eqz (local.tee $id (call $literal))))))))))
            (br_if $fail (i32.ge_u (local.get $k) (local.get $nv)))
            (if (i32.ge_u (i32.add (i32.mul (local.get $nr) (local.get $nv)) (local.get $k)) (local.get $cap)) (then
              (local.set $buf (call $realloc (local.get $buf) (i32.shl (local.get $cap) (i32.const 2))
                (i32.shl (local.get $cap) (i32.const 3)) (i32.shl (local.get $cap) (i32.const 2))))
              (local.set $cap (i32.shl (local.get $cap) (i32.const 1)))))
            (i32.store (i32.add (local.get $buf) (i32.shl (i32.add (i32.mul (local.get $nr) (local.get $nv)) (local.get $k)) (i32.const 2))) (local.get $id))
            (local.set $k (i32.add (local.get $k) (i32.const 1)))
            (br_if $values (i32.eqz (local.get $single))))))
        (br_if $fail (i32.ne (local.get $k) (local.get $nv)))
        (if (i32.eqz (local.get $single)) (then (call $lex)))
        (local.set $nr (i32.add (local.get $nr) (i32.const 1)))
        (br $row))))
    (call $lex)
    (local.set $n (call $node (i32.const P_VALUES)))
    (i32.store offset=4 (local.get $n) (local.get $nv))
    (i32.store offset=8 (local.get $n) (local.get $vars))
    (i32.store offset=12 (local.get $n) (local.get $nr))
    (local.set $tmp (call $anew (i32.shl (i32.mul (local.get $nr) (local.get $nv)) (i32.const 2))))
    (memory.copy (local.get $tmp) (local.get $buf) (i32.shl (i32.mul (local.get $nr) (local.get $nv)) (i32.const 2)))
    (i32.store offset=16 (local.get $n) (local.get $tmp))
    (call $free (local.get $buf) (i32.shl (local.get $cap) (i32.const 2)))
    (return (local.get $n)))
  (call $free (local.get $buf) (i32.shl (local.get $cap) (i32.const 2)))
  (call $expected (@str "bad VALUES block")))

;; A constant triple term <<( s p o )>> (VALUES): its id.
(func $tt_const (result i32)
  (local $s i32) (local $p i32) (local $o i32)
  (call $lex)
  (if (i32.eqz (local.tee $s (call $const_term))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $p (call $const_term))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $o (call $const_term))) (then (return (i32.const 0))))
  (if (i32.eqz (call $eat (i32.const T_TT_CLOSE) (@str "expected ')>>'"))) (then (return (i32.const 0))))
  (if (i32.or (i32.gt_u (i32.sub (call $kind (local.get $s)) (i32.const 1)) (i32.const 1)) (i32.ne (call $kind (local.get $p)) (i32.const 1)))
    (then (return (call $syntax (@str "bad triple term")))))
  (call $intern_triple (local.get $s) (local.get $p) (local.get $o) (i32.const 1)))

(func $const_term (result i32)
  (local $id i32)
  (if (call $word (@str "a") (i32.const 0)) (then (call $lex) (return (call $known (i32.const K_TYPE)))))
  (if (i32.eq (global.get $tok) (i32.const T_TT_OPEN)) (then (return (call $tt_const))))
  (if (call $is_iri_tok) (then
    (if (local.tee $id (call $iri_tok)) (then (call $lex)))
    (return (local.get $id))))
  (call $literal))

;; --- triples ---------------------------------------------------------------------

;; TriplesSameSubjectPath, into group g.
(func $ptriples (param $g i32) (result i32)
  (local $s i32)
  (if (i32.eqz (local.tee $s (call $pterm (local.get $g) (i32.const F_OBJECT)))) (then (return (i32.const 0))))
  (if (i32.and (i32.ne (i32.and (global.get $form) (i32.const 19)) (i32.const 0))   ;; F_ALONE | F_COLL
               (i32.eqz (call $verb_start)))
    (then (return (i32.const 1))))
  (call $ppol (local.get $g) (local.get $s)))

(func $verb_start (result i32)
  (i32.or (i32.or (call $is_iri_tok) (call $word (@str "a") (i32.const 0)))
          (i32.or (i32.eq (global.get $tok) (i32.const T_VAR))
                  (call $one_of (global.get $tok) (@str "^!(")))))

;; A pattern term (forms f as in Turtle), adding the triples it implies to g.
(func $pterm (param $g i32) (param $f i32) (result i32)
  (local $id i32) (local $t i32) (local $form i32)
  (if (i32.eqz (call $deeper)) (then (return (i32.const 0))))
  (local.set $t (global.get $tok))
  (block $done
    (if (i32.eq (local.get $t) (i32.const T_VAR)) (then
      (local.set $id (call $vref (call $var_tok)))
      (call $lex)
      (br $done)))
    (if (call $is_iri_tok) (then
      (br_if $done (i32.eqz (local.tee $id (call $iri_tok))))
      (call $lex)
      (br $done)))
    (if (i32.eq (local.get $t) (i32.const T_BNODE)) (then
      (local.set $id (call $var_in (global.get $str) (global.get $str_len) (global.get $bscope) (global.get $bflags)))
      ;; a label of INSERT DATA belongs to one operation
      (if (i32.and (i32.eq (global.get $bscope) (i32.const -3)) (i32.ne (global.get $data_op) (i32.const 0))) (then
        (if (i32.load offset=24 (global.get $last_var))
          (then (if (i32.ne (i32.load offset=24 (global.get $last_var)) (global.get $data_op))
            (then (return (call $syntax (@str "blank node label used in two operations"))))))
          (else (i32.store offset=24 (global.get $last_var) (global.get $data_op))))))
      ;; a label belongs to one basic graph pattern
      (if (i32.eq (global.get $bscope) (i32.const -2)) (then
        (if (i32.load offset=24 (global.get $last_var))
          (then (if (i32.ne (i32.load offset=24 (global.get $last_var)) (global.get $bgp))
            (then (return (call $syntax (@str "blank node label used in two patterns"))))))
          (else (i32.store offset=24 (global.get $last_var) (global.get $bgp))))))
      (local.set $id (call $vref (local.get $id)))
      (call $lex)
      (br $done)))
    (if (i32.eq (local.get $t) (i32.const '[')) (then
      (call $lex)
      (local.set $id (call $vref (call $blank_var)))
      (br_if $done (i32.eq (global.get $tok) (i32.const ']')))
      (if (i32.eqz (i32.and (local.get $f) (i32.const F_BNPL))) (then (return (call $expected (@str "expected ']'")))))
      (if (i32.eqz (call $ppol (local.get $g) (local.get $id))) (then (return (i32.const 0))))
      (if (i32.ne (global.get $tok) (i32.const ']')) (then (return (call $expected (@str "expected ']'")))))
      (local.set $form (i32.const F_BNPL))
      (br $done)))
    (if (i32.eq (local.get $t) (i32.const '(')) (then
      (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_COLL))))
      (local.set $id (call $pcollection (local.get $g)))
      (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
      ;; () is a term, not a collection
      (global.set $form (select (i32.const 0) (i32.const F_COLL) (i32.eq (local.get $id) (call $known (i32.const K_NIL)))))
      (return (local.get $id))))
    (if (i32.eq (local.get $t) (i32.const T_LTLT)) (then
      (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_RT))))
      (local.set $id (call $preified (local.get $g)))
      (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
      (global.set $form (i32.const F_RT))
      (return (local.get $id))))
    (if (i32.eq (local.get $t) (i32.const T_TT_OPEN)) (then
      (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_TT))))
      (local.set $id (call $ptriple_term (local.get $g)))
      (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
      (global.set $form (i32.const F_TT))
      (return (local.get $id))))
    (br_if $done (i32.eqz (i32.and (local.get $f) (i32.const F_LIT))))
    (local.set $id (call $literal))
    (local.set $form (i32.const F_LIT))
    (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
    (global.set $form (local.get $form))
    (return (local.get $id)))
  (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
  (if (i32.and (i32.ne (local.get $id) (i32.const 0)) (i32.eq (local.get $t) (i32.const '['))) (then (call $lex)))
  (global.set $form (local.get $form))
  (if (result i32) (local.get $id) (then (local.get $id)) (else (call $expected (@str "unexpected token")))))

;; Blank node labels and [] in WHERE are hidden variables; in a CONSTRUCT
;; template they are fresh blank nodes per solution.
(global $bscope (mut i32) (i32.const -2))
(global $bflags (mut i32) (i32.const V_HIDDEN))
(func $blank_var (result i32)
  (call $new_var (i32.const 0) (i32.const 0) (i32.const -1) (global.get $bflags)))

;; A reifier the query leaves unnamed ({| |}, << >>, ~): a hidden variable, and in
;; CONSTRUCT WHERE also a template blank node, fresh per solution.
(global $reif_flags (mut i32) (i32.const 0))
(func $reifier_var (result i32)
  (call $new_var (i32.const 0) (i32.const 0) (i32.const -1) (i32.or (global.get $bflags) (global.get $reif_flags))))

;; Add triple pattern (s, p, o) to g.
(func $ptriple (param $g i32) (param $s i32) (param $p i32) (param $o i32)
  (call $add_elem (local.get $g) (call $node3 (i32.const P_TRIPLE) (local.get $s) (local.get $p) (local.get $o))))

;; Add (s, path, o) to g: plain triples for IRIs, inverses and sequences of them.
(func $ppath (param $g i32) (param $s i32) (param $x i32) (param $o i32)
  (local $k i32) (local $v i32)
  (local.set $k (i32.load (local.get $x)))
  (if (i32.eq (local.get $k) (i32.const X_LINK)) (then
    (call $ptriple (local.get $g) (local.get $s) (i32.load offset=4 (local.get $x)) (local.get $o))
    (return)))
  (if (i32.and (i32.eq (local.get $k) (i32.const X_INV)) (i32.eq (i32.load (i32.load offset=4 (local.get $x))) (i32.const X_LINK))) (then
    (call $ptriple (local.get $g) (local.get $o) (i32.load offset=4 (i32.load offset=4 (local.get $x))) (local.get $s))
    (return)))
  (if (i32.eq (local.get $k) (i32.const X_SEQ)) (then
    (local.set $v (call $vref (call $anon)))
    (call $ppath (local.get $g) (local.get $s) (i32.load offset=4 (local.get $x)) (local.get $v))
    (call $ppath (local.get $g) (local.get $v) (i32.load offset=8 (local.get $x)) (local.get $o))
    (return)))
  (call $add_elem (local.get $g) (call $node3 (i32.const P_PATH) (local.get $s) (local.get $x) (local.get $o))))

;; PropertyListPathNotEmpty for subject s.
(func $ppol (param $g i32) (param $s i32) (result i32)
  (local $v i32) (local $x i32) (local $o i32)
  (loop $verbs
    (local.set $x (i32.const 0))
    (if (i32.eq (global.get $tok) (i32.const T_VAR))
      (then
        (local.set $v (call $vref (call $var_tok)))
        (call $lex))
      (else
        (if (i32.eqz (local.tee $x (call $path))) (then (return (i32.const 0))))
        (local.set $v (i32.const 0))
        (if (i32.eq (i32.load (local.get $x)) (i32.const X_LINK)) (then
          (local.set $v (i32.load offset=4 (local.get $x)))
          (local.set $x (i32.const 0))))))
    (loop $objects
      (if (i32.eqz (local.tee $o (call $pterm (local.get $g) (i32.const F_OBJECT)))) (then (return (i32.const 0))))
      (if (local.get $x)
        (then (call $ppath (local.get $g) (local.get $s) (local.get $x) (local.get $o)))
        (else (call $ptriple (local.get $g) (local.get $s) (local.get $v) (local.get $o))))
      (if (i32.eqz (call $pannotation (local.get $g) (local.get $s) (local.get $v) (local.get $x) (local.get $o))) (then (return (i32.const 0))))
      (if (i32.eq (global.get $tok) (i32.const ',')) (then (call $lex) (br $objects))))
    (if (i32.ne (global.get $tok) (i32.const ';')) (then (return (i32.const 1))))
    (loop $semicolons
      (call $lex)
      (br_if $semicolons (i32.eq (global.get $tok) (i32.const ';'))))
    (br_if $verbs (call $verb_start)))
  (i32.const 1))

;; A triple term pattern node as a ref.
(func $tt_ref (param $s i32) (param $p i32) (param $o i32) (result i32)
  (i32.or (call $node3 (i32.const P_TT) (local.get $s) (local.get $p) (local.get $o)) (i32.const 0x80000000)))

;; Reifier and annotation blocks after (s, p, o); a path predicate cannot be reified.
(func $pannotation (param $g i32) (param $s i32) (param $p i32) (param $x i32) (param $o i32) (result i32)
  (local $r i32) (local $pending i32)
  (loop $more
    (if (i32.or (i32.eq (global.get $tok) (i32.const '~')) (i32.eq (global.get $tok) (i32.const T_ANN_OPEN))) (then
      (if (local.get $x) (then (return (call $syntax (@str "a path cannot be annotated")))))
      (if (i32.eq (global.get $tok) (i32.const '~'))
        (then
          (call $lex)
          (if (i32.or (i32.or (call $is_iri_tok) (i32.eq (global.get $tok) (i32.const T_VAR)))
                      (i32.or (i32.eq (global.get $tok) (i32.const T_BNODE)) (i32.eq (global.get $tok) (i32.const '['))))
            (then (if (i32.eqz (local.tee $r (call $pterm (local.get $g) (i32.const 0)))) (then (return (i32.const 0)))))
            (else (local.set $r (call $vref (call $reifier_var)))))
          (call $ptriple (local.get $g) (local.get $r) (call $known (i32.const K_REIFIES)) (call $tt_ref (local.get $s) (local.get $p) (local.get $o)))
          (local.set $pending (local.get $r)))
        (else
          (call $lex)
          (if (i32.eqz (local.tee $r (local.get $pending))) (then
            (local.set $r (call $vref (call $reifier_var)))
            (call $ptriple (local.get $g) (local.get $r) (call $known (i32.const K_REIFIES)) (call $tt_ref (local.get $s) (local.get $p) (local.get $o)))))
          (local.set $pending (i32.const 0))
          (if (i32.eqz (call $ppol (local.get $g) (local.get $r))) (then (return (i32.const 0))))
          (if (i32.eqz (call $eat (i32.const T_ANN_CLOSE) (@str "expected '|}'"))) (then (return (i32.const 0))))))
      (br $more))))
  (i32.const 1))

;; collection (on '('): its head, or rdf:nil.
(func $pcollection (param $g i32) (result i32)
  (local $head i32) (local $cell i32) (local $next i32) (local $o i32)
  (call $lex)
  (if (i32.eq (global.get $tok) (i32.const ')')) (then (call $lex) (return (call $known (i32.const K_NIL)))))
  (local.set $cell (local.tee $head (call $vref (call $blank_var))))
  (loop $items
    (if (i32.eqz (local.tee $o (call $pterm (local.get $g) (i32.const F_OBJECT)))) (then (return (i32.const 0))))
    (call $ptriple (local.get $g) (local.get $cell) (call $known (i32.const K_FIRST)) (local.get $o))
    (if (i32.eq (global.get $tok) (i32.const ')')) (then
      (call $ptriple (local.get $g) (local.get $cell) (call $known (i32.const K_REST)) (call $known (i32.const K_NIL)))
      (call $lex)
      (return (local.get $head))))
    (call $ptriple (local.get $g) (local.get $cell) (call $known (i32.const K_REST)) (local.tee $next (call $vref (call $blank_var))))
    (local.set $cell (local.get $next))
    (br $items))
  (unreachable))

;; reifiedTriple (on '<<'): << s p o (~ reifier?)? >>
(func $preified (param $g i32) (result i32)
  (local $s i32) (local $v i32) (local $o i32) (local $r i32)
  (call $lex)
  (if (i32.eqz (local.tee $s (call $pterm (local.get $g) (i32.const 24)))) (then (return (i32.const 0))))   ;; F_RT | F_TT
  (if (i32.eqz (local.tee $v (call $simple_verb))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $o (call $pterm (local.get $g) (i32.const F_RT_OBJECT)))) (then (return (i32.const 0))))
  (if (i32.eq (global.get $tok) (i32.const '~')) (then
    (call $lex)
    (if (i32.or (i32.or (call $is_iri_tok) (i32.eq (global.get $tok) (i32.const T_VAR)))
                (i32.or (i32.eq (global.get $tok) (i32.const T_BNODE)) (i32.eq (global.get $tok) (i32.const '['))))
      (then (if (i32.eqz (local.tee $r (call $pterm (local.get $g) (i32.const 0)))) (then (return (i32.const 0))))))))
  (if (i32.eqz (call $eat (i32.const T_GTGT) (@str "expected '>>'"))) (then (return (i32.const 0))))
  (if (i32.eqz (local.get $r)) (then (local.set $r (call $vref (call $reifier_var)))))
  (call $ptriple (local.get $g) (local.get $r) (call $known (i32.const K_REIFIES)) (call $tt_ref (local.get $s) (local.get $v) (local.get $o)))
  (local.get $r))

;; tripleTerm (on '<<('): a triple term pattern.
(func $ptriple_term (param $g i32) (result i32)
  (local $s i32) (local $v i32) (local $o i32)
  (call $lex)
  (if (i32.eqz (local.tee $s (call $pterm (local.get $g) (i32.const F_TT)))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $v (call $simple_verb))) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $o (call $pterm (local.get $g) (i32.const F_TT_OBJECT)))) (then (return (i32.const 0))))
  (if (i32.eqz (call $eat (i32.const T_TT_CLOSE) (@str "expected ')>>'"))) (then (return (i32.const 0))))
  ;; constant triple terms are terms
  (if (i32.and (i32.and (i32.gt_s (local.get $s) (i32.const 0)) (i32.gt_s (local.get $v) (i32.const 0))) (i32.gt_s (local.get $o) (i32.const 0)))
    (then (return (call $intern_triple (local.get $s) (local.get $v) (local.get $o) (i32.const 1)))))
  (call $tt_ref (local.get $s) (local.get $v) (local.get $o)))

;; A variable, an IRI or 'a' as a predicate.
(func $simple_verb (result i32)
  (local $id i32)
  (if (i32.eq (global.get $tok) (i32.const T_VAR)) (then
    (local.set $id (call $vref (call $var_tok)))
    (call $lex)
    (return (local.get $id))))
  (call $verb))

;; --- property paths --------------------------------------------------------------

(func $path (result i32)
  (local $a i32) (local $b i32)
  (if (i32.eqz (call $deeper)) (then (return (i32.const 0))))
  (block $out
    (br_if $out (i32.eqz (local.tee $a (call $path_seq))))
    (loop $alts
      (if (i32.eq (global.get $tok) (i32.const '|')) (then
        (call $lex)
        (if (i32.eqz (local.tee $b (call $path_seq))) (then (local.set $a (i32.const 0)) (br $out)))
        (local.set $a (call $node2 (i32.const X_ALT) (local.get $a) (local.get $b)))
        (br $alts)))))
  (global.set $depth (i32.sub (global.get $depth) (i32.const 1)))
  (local.get $a))

(func $path_seq (result i32)
  (local $a i32) (local $b i32)
  (if (i32.eqz (local.tee $a (call $path_elt))) (then (return (i32.const 0))))
  (loop $seq
    (if (i32.eq (global.get $tok) (i32.const '/')) (then
      (call $lex)
      (if (i32.eqz (local.tee $b (call $path_elt))) (then (return (i32.const 0))))
      (local.set $a (call $node2 (i32.const X_SEQ) (local.get $a) (local.get $b)))
      (br $seq))))
  (local.get $a))

;; PathEltOrInverse
(func $path_elt (result i32)
  (local $a i32) (local $inv i32) (local $t i32)
  (if (i32.eq (global.get $tok) (i32.const '^')) (then (call $lex) (local.set $inv (i32.const 1))))
  (if (i32.eqz (local.tee $a (call $path_primary))) (then (return (i32.const 0))))
  (local.set $t (global.get $tok))
  (if (i32.eq (local.get $t) (i32.const '*')) (then (call $lex) (local.set $a (call $node2 (i32.const X_STAR) (local.get $a) (i32.const 0)))))
  (if (i32.eq (local.get $t) (i32.const '+')) (then (call $lex) (local.set $a (call $node2 (i32.const X_PLUS) (local.get $a) (i32.const 0)))))
  (if (i32.eq (local.get $t) (i32.const '?')) (then (call $lex) (local.set $a (call $node2 (i32.const X_OPT) (local.get $a) (i32.const 0)))))
  (if (local.get $inv) (then (local.set $a (call $node2 (i32.const X_INV) (local.get $a) (i32.const 0)))))
  (local.get $a))

(func $path_primary (result i32)
  (local $id i32) (local $a i32) (local $fwd i32) (local $inv i32) (local $neg i32)
  (if (i32.eq (global.get $tok) (i32.const '(')) (then
    (call $lex)
    (if (i32.eqz (local.tee $a (call $path))) (then (return (i32.const 0))))
    (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0))))
    (return (local.get $a))))
  (if (i32.eq (global.get $tok) (i32.const '!')) (then
    (call $lex)
    (local.set $neg (call $node (i32.const X_NEG)))
    (if (i32.eq (global.get $tok) (i32.const '('))
      (then
        (call $lex)
        (if (i32.ne (global.get $tok) (i32.const ')')) (then
          (loop $members
            (if (i32.eqz (call $neg_member (local.get $neg))) (then (return (i32.const 0))))
            (if (i32.eq (global.get $tok) (i32.const '|')) (then (call $lex) (br $members))))))
        (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0)))))
      (else (if (i32.eqz (call $neg_member (local.get $neg))) (then (return (i32.const 0))))))
    (return (local.get $neg))))
  (if (i32.eqz (local.tee $id (call $verb))) (then (return (i32.const 0))))
  (call $node2 (i32.const X_LINK) (local.get $id) (i32.const 0)))

;; One IRI of a negated property set, into neg's forward (a) or inverse (b) list.
(func $neg_member (param $neg i32) (result i32)
  (local $inv i32) (local $id i32) (local $l i32)
  (if (i32.eq (global.get $tok) (i32.const '^')) (then (call $lex) (local.set $inv (i32.const 1))))
  (if (i32.eqz (local.tee $id (call $verb))) (then (return (i32.const 0))))
  (local.set $l (call $node2 (i32.const X_LINK) (local.get $id) (i32.const 0)))
  (i32.store offset=28 (local.get $l) (i32.load (i32.add (local.get $neg) (i32.shl (i32.add (local.get $inv) (i32.const 1)) (i32.const 2)))))
  (i32.store (i32.add (local.get $neg) (i32.shl (i32.add (local.get $inv) (i32.const 1)) (i32.const 2))) (local.get $l))
  (i32.const 1))

;; --- queries ---------------------------------------------------------------------

(func $q_new (result i32)
  (local $q i32)
  (local.set $q (call $anew (i32.const 256)))
  (i32.store offset=28 (local.get $q) (i32.const -1))
  (local.get $q))

;; Prologue: BASE, PREFIX, VERSION.
(func $prologue (result i32)
  (loop $decls
    (if (call $is_kw (@str "BASE")) (then
      (call $lex)
      (if (i32.eqz (call $base_decl)) (then (return (i32.const 0))))
      (br $decls)))
    (if (call $is_kw (@str "PREFIX")) (then
      (call $lex)
      (if (i32.eqz (call $prefix_decl)) (then (return (i32.const 0))))
      (br $decls)))
    (if (call $is_kw (@str "VERSION")) (then
      (call $lex)
      (if (i32.eqz (call $version_decl)) (then (return (i32.const 0))))
      (br $decls))))
  (i32.const 1))

;; SELECT clause (after SELECT) into q.
(func $select_clause (param $q i32) (result i32)
  (local $e i32) (local $slot i32) (local $item i32)
  (if (call $is_kw (@str "DISTINCT")) (then (call $lex) (i32.store offset=4 (local.get $q) (i32.const Q_DISTINCT))))
  (if (call $is_kw (@str "REDUCED")) (then (call $lex) (i32.store offset=4 (local.get $q) (i32.const Q_REDUCED))))
  (if (i32.eq (global.get $tok) (i32.const '*')) (then
    (call $lex)
    (i32.store offset=4 (local.get $q) (i32.or (i32.load offset=4 (local.get $q)) (i32.const Q_STAR)))
    (return (i32.const 1))))
  (loop $items
    (local.set $e (i32.const 0))
    (if (i32.eq (global.get $tok) (i32.const T_VAR))
      (then
        (local.set $slot (call $var_tok))
        (call $lex))
      (else
        (if (i32.eqz (call $eat (i32.const '(') (@str "expected a variable or '('"))) (then (return (i32.const 0))))
        (global.set $aggs_ok (i32.const 1))
        (if (i32.eqz (local.tee $e (call $expr))) (then (return (i32.const 0))))
        (if (i32.eqz (call $eat_kw (@str "AS"))) (then (return (i32.const 0))))
        (if (i32.ne (global.get $tok) (i32.const T_VAR)) (then (return (call $expected (@str "expected a variable")))))
        (local.set $slot (call $var_tok))
        (call $lex)
        (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0))))))
    (local.set $item (call $node2 (i32.const N_ITEM) (local.get $slot) (local.get $e)))
    (call $qappend (local.get $q) (i32.const 8) (local.get $item))
    (br_if $items (i32.or (i32.eq (global.get $tok) (i32.const T_VAR)) (i32.eq (global.get $tok) (i32.const '(')))))
  (i32.const 1))

;; FROM / FROM NAMED clauses.
(func $dataset (param $q i32) (result i32)
  (local $named i32) (local $id i32)
  (loop $froms
    (if (call $is_kw (@str "FROM")) (then
      (call $lex)
      (local.set $named (call $is_kw (@str "NAMED")))
      (if (local.get $named) (then (call $lex)))
      (if (i32.eqz (call $is_iri_tok)) (then (return (call $expected (@str "expected an IRI")))))
      (if (i32.eqz (local.tee $id (call $iri_tok))) (then (return (i32.const 0))))
      (call $lex)
      (call $qappend (local.get $q) (select (i32.const 48) (i32.const 44) (local.get $named))
                    (call $node2 (i32.const N_ITEM) (local.get $id) (i32.const 0)))
      (br $froms))))
  (i32.const 1))

;; WhereClause, then the solution modifiers.
(func $where_mods (param $q i32) (param $need_where i32) (result i32)
  (local $g i32)
  (if (call $is_kw (@str "WHERE")) (then (call $lex)))
  (global.set $aggs_ok (i32.const 0))
  (if (i32.eqz (local.tee $g (call $ggp))) (then (return (i32.const 0))))
  (i32.store offset=12 (local.get $q) (local.get $g))
  (call $modifiers (local.get $q)))

;; SELECT expressions bind new variables; with grouping, the projection uses
;; only group keys and aggregates.
(func $check_projection (param $q i32) (result i32)
  (local $item i32) (local $prev i32) (local $grouped i32) (local $slot i32)
  (local.set $grouped (i32.ne (i32.and (i32.load offset=4 (local.get $q)) (i32.const Q_GROUPED)) (i32.const 0)))
  (if (i32.and (local.get $grouped) (i32.ne (i32.and (i32.load offset=4 (local.get $q)) (i32.const Q_STAR)) (i32.const 0)))
    (then (return (call $syntax (@str "SELECT * with GROUP BY")))))
  (local.set $item (i32.load offset=8 (local.get $q)))
  (loop $items
    (if (local.get $item) (then
      (local.set $slot (i32.load offset=4 (local.get $item)))
      (if (i32.load offset=8 (local.get $item))
        (then
          ;; grouped: the WHERE variables are out of scope, the keys in it
          (if (i32.or (if (result i32) (local.get $grouped)
                        (then (call $is_group_key (local.get $q) (local.get $slot)))
                        (else (call $group_binds (i32.load offset=12 (local.get $q)) (local.get $slot))))
                      (call $projected_before (local.get $q) (local.get $item) (local.get $slot)))
            (then (return (call $syntax (@str "SELECT expression bound to a variable in scope")))))
          (if (local.get $grouped) (then
            (if (i32.eqz (call $grouped_expr (local.get $q) (local.get $item) (i32.load offset=8 (local.get $item))))
              (then (return (call $syntax (@str "variable not grouped"))))))))
        (else
          (if (local.get $grouped) (then
            (if (i32.eqz (i32.or (call $is_group_key (local.get $q) (local.get $slot)) (call $alias_before (local.get $q) (local.get $item) (local.get $slot))))
              (then (return (call $syntax (@str "variable not grouped")))))))))
      (local.set $item (i32.load offset=28 (local.get $item)))
      (br $items))))
  (i32.const 1))

;; Whether slot is a GROUP BY key: a grouped variable or an AS variable.
(func $is_group_key (param $q i32) (param $slot i32) (result i32)
  (local $item i32) (local $e i32)
  (if (i32.load offset=40 (local.get $q)) (then
    (if (call $binds (i32.load offset=40 (local.get $q)) (local.get $slot)) (then (return (i32.const 1))))))
  (local.set $item (i32.load offset=16 (local.get $q)))
  (loop $items
    (if (local.get $item) (then
      (local.set $e (i32.load offset=4 (local.get $item)))
      (if (i32.and (i32.eq (i32.load (local.get $e)) (i32.const E_VAR)) (i32.eq (i32.load offset=4 (local.get $e)) (local.get $slot)))
        (then (return (i32.const 1))))
      (if (i32.eq (i32.load offset=8 (local.get $item)) (local.get $slot))
        (then (if (i32.ne (i32.load (local.get $e)) (i32.const E_VAR)) (then (return (i32.const 1))))))
      (local.set $item (i32.load offset=28 (local.get $item)))
      (br $items))))
  (i32.const 0))

;; Whether a projection item before `stop` projects slot (projected_before:
;; any; alias_before: as an expression's variable).
(func $projected_before (param $q i32) (param $stop i32) (param $slot i32) (result i32)
  (local $item i32)
  (local.set $item (i32.load offset=8 (local.get $q)))
  (loop $items
    (if (i32.and (i32.ne (local.get $item) (i32.const 0)) (i32.ne (local.get $item) (local.get $stop))) (then
      (if (i32.eq (i32.load offset=4 (local.get $item)) (local.get $slot)) (then (return (i32.const 1))))
      (local.set $item (i32.load offset=28 (local.get $item)))
      (br $items))))
  (i32.const 0))

(func $alias_before (param $q i32) (param $stop i32) (param $slot i32) (result i32)
  (local $item i32)
  (local.set $item (i32.load offset=8 (local.get $q)))
  (loop $items
    (if (i32.and (i32.ne (local.get $item) (i32.const 0)) (i32.ne (local.get $item) (local.get $stop))) (then
      (if (i32.and (i32.eq (i32.load offset=4 (local.get $item)) (local.get $slot)) (i32.ne (i32.load offset=8 (local.get $item)) (i32.const 0)))
        (then (return (i32.const 1))))
      (local.set $item (i32.load offset=28 (local.get $item)))
      (br $items))))
  (i32.const 0))

;; Whether expression e (of projection item `stop`) uses, outside aggregates,
;; only group keys and earlier aliases.
(func $grouped_expr (param $q i32) (param $stop i32) (param $e i32) (result i32)
  (local $k i32) (local $a i32)
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_VAR)) (then
    (return (i32.or (call $is_group_key (local.get $q) (i32.load offset=4 (local.get $e)))
                    (call $alias_before (local.get $q) (local.get $stop) (i32.load offset=4 (local.get $e)))))))
  (if (i32.eq (local.get $k) (i32.const E_CALL)) (then
    (local.set $a (i32.load offset=8 (local.get $e)))
    (loop $args (if (local.get $a) (then
      (if (i32.eqz (call $grouped_expr (local.get $q) (local.get $stop) (local.get $a))) (then (return (i32.const 0))))
      (local.set $a (i32.load offset=28 (local.get $a)))
      (br $args))))))
  (i32.const 1))

;; Whether pattern element el (and so the list from it) puts slot in scope.
(func $elems_bind (param $el i32) (param $slot i32) (result i32)
  (loop $each (if (local.get $el) (then
    (if (call $binds (local.get $el) (local.get $slot)) (then (return (i32.const 1))))
    (local.set $el (i32.load offset=28 (local.get $el)))
    (br $each))))
  (i32.const 0))

(func $group_binds (param $g i32) (param $slot i32) (result i32)
  (if (result i32) (local.get $g) (then (call $elems_bind (i32.load offset=4 (local.get $g)) (local.get $slot))) (else (i32.const 0))))

(func $ref_has (param $ref i32) (param $slot i32) (result i32)
  (local $n i32)
  (if (i32.ge_s (local.get $ref) (i32.const 0)) (then (return (i32.const 0))))
  (if (i32.gt_s (local.get $ref) (i32.const -0x100000)) (then (return (i32.eq (local.get $ref) (call $vref (local.get $slot))))))
  (local.set $n (i32.and (local.get $ref) (i32.const 0x7fffffff)))
  (i32.or (i32.or (call $ref_has (i32.load offset=4 (local.get $n)) (local.get $slot)) (call $ref_has (i32.load offset=8 (local.get $n)) (local.get $slot)))
          (call $ref_has (i32.load offset=12 (local.get $n)) (local.get $slot))))

(func $binds (param $el i32) (param $slot i32) (result i32)
  (local $k i32) (local $i i32) (local $m i32)
  (local.set $k (i32.load (local.get $el)))
  (if (i32.eq (local.get $k) (i32.const P_TRIPLE)) (then
    (return (i32.or (i32.or (call $ref_has (i32.load offset=4 (local.get $el)) (local.get $slot)) (call $ref_has (i32.load offset=8 (local.get $el)) (local.get $slot)))
                    (call $ref_has (i32.load offset=12 (local.get $el)) (local.get $slot))))))
  (if (i32.eq (local.get $k) (i32.const P_PATH)) (then
    (return (i32.or (call $ref_has (i32.load offset=4 (local.get $el)) (local.get $slot)) (call $ref_has (i32.load offset=12 (local.get $el)) (local.get $slot))))))
  (if (i32.eq (local.get $k) (i32.const P_GROUP)) (then (return (call $group_binds (local.get $el) (local.get $slot)))))
  (if (i32.eq (local.get $k) (i32.const P_UNION)) (then
    (return (i32.or (call $binds (i32.load offset=4 (local.get $el)) (local.get $slot)) (call $binds (i32.load offset=8 (local.get $el)) (local.get $slot))))))
  (if (i32.or (i32.eq (local.get $k) (i32.const P_OPTIONAL)) (i32.eq (local.get $k) (i32.const P_LATERAL)))
    (then (return (call $group_binds (i32.load offset=4 (local.get $el)) (local.get $slot)))))
  (if (i32.eq (local.get $k) (i32.const P_GRAPH)) (then
    (return (i32.or (call $ref_has (i32.load offset=4 (local.get $el)) (local.get $slot)) (call $group_binds (i32.load offset=8 (local.get $el)) (local.get $slot))))))
  (if (i32.eq (local.get $k) (i32.const P_BIND)) (then (return (i32.eq (i32.load offset=8 (local.get $el)) (local.get $slot)))))
  (if (i32.eq (local.get $k) (i32.const P_VALUES)) (then
    (loop $vars (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $el))) (then
      (if (i32.eq (i32.load (i32.add (i32.load offset=8 (local.get $el)) (i32.shl (local.get $i) (i32.const 2)))) (local.get $slot)) (then (return (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $vars))))
    (return (i32.const 0))))
  (if (i32.eq (local.get $k) (i32.const P_SUB)) (then
    (local.set $m (i32.load offset=72 (i32.load offset=4 (local.get $el))))
    (loop $map (if (local.get $m) (then
      (if (i32.eq (i32.load offset=8 (local.get $m)) (local.get $slot)) (then (return (i32.const 1))))
      (local.set $m (i32.load offset=28 (local.get $m))) (br $map))))))
  (i32.const 0))

(func $modifiers (param $q i32) (result i32)
  (local $e i32) (local $slot i32) (local $desc i32) (local $v i32)
  (global.set $aggs_ok (i32.const 0))
  (if (call $is_kw (@str "GROUP")) (then
    (call $lex)
    (if (i32.eqz (call $eat_kw (@str "BY"))) (then (return (i32.const 0))))
    (i32.store offset=4 (local.get $q) (i32.or (i32.load offset=4 (local.get $q)) (i32.const Q_GROUPED)))
    (loop $conds
      (local.set $slot (i32.const -1))
      (if (i32.eq (global.get $tok) (i32.const T_VAR))
        (then
          (local.set $slot (call $var_tok))
          (local.set $e (call $node2 (i32.const E_VAR) (local.get $slot) (i32.const 0)))
          (call $lex))
        (else
          (if (i32.eq (global.get $tok) (i32.const '('))
            (then
              (call $lex)
              (if (i32.eqz (local.tee $e (call $expr))) (then (return (i32.const 0))))
              (if (call $is_kw (@str "AS")) (then
                (call $lex)
                (if (i32.ne (global.get $tok) (i32.const T_VAR)) (then (return (call $expected (@str "expected a variable")))))
                (local.set $slot (call $var_tok))
                (call $lex)))
              (if (i32.eqz (call $eat (i32.const ')') (@str "expected ')'"))) (then (return (i32.const 0)))))
            (else (if (i32.eqz (local.tee $e (call $constraint))) (then (return (i32.const 0))))))))
      (if (i32.lt_s (local.get $slot) (i32.const 0)) (then (local.set $slot (call $anon))))
      (call $qappend (local.get $q) (i32.const 16) (call $node2 (i32.const N_ITEM) (local.get $e) (local.get $slot)))
      (br_if $conds (i32.or (i32.or (i32.eq (global.get $tok) (i32.const T_VAR)) (i32.eq (global.get $tok) (i32.const '(')))
                            (i32.or (call $is_iri_tok)
                                    (i32.and (i32.eq (global.get $tok) (i32.const T_NAME))
                                             (i32.eqz (i32.or (i32.or (call $is_kw (@str "HAVING")) (call $is_kw (@str "ORDER")))
                                                              (i32.or (call $is_kw (@str "LIMIT")) (i32.or (call $is_kw (@str "OFFSET")) (call $is_kw (@str "VALUES")))))))))))))
  (global.set $aggs_ok (i32.const 1))
  (if (call $is_kw (@str "HAVING")) (then
    (call $lex)
    (loop $havings
      (if (i32.eqz (local.tee $e (call $constraint))) (then (return (i32.const 0))))
      (call $qappend (local.get $q) (i32.const 20) (call $node2 (i32.const P_FILTER) (local.get $e) (i32.const 0)))
      (br_if $havings (i32.or (i32.eq (global.get $tok) (i32.const '('))
                              (i32.or (call $is_iri_tok)
                                      (i32.and (i32.eq (global.get $tok) (i32.const T_NAME))
                                               (i32.eqz (i32.or (call $is_kw (@str "ORDER")) (i32.or (call $is_kw (@str "LIMIT"))
                                                        (i32.or (call $is_kw (@str "OFFSET")) (call $is_kw (@str "VALUES")))))))))))))
  (if (call $is_kw (@str "ORDER")) (then
    (call $lex)
    (if (i32.eqz (call $eat_kw (@str "BY"))) (then (return (i32.const 0))))
    (loop $orders
      (local.set $desc (i32.const 0))
      (if (i32.or (call $is_kw (@str "ASC")) (call $is_kw (@str "DESC")))
        (then
          (local.set $desc (call $is_kw (@str "DESC")))
          (call $lex)
          (if (i32.ne (global.get $tok) (i32.const '(')) (then (return (call $expected (@str "expected '('")))))
          (if (i32.eqz (local.tee $e (call $primary))) (then (return (i32.const 0)))))
        (else
          (if (i32.eq (global.get $tok) (i32.const T_VAR))
            (then
              (local.set $e (call $node2 (i32.const E_VAR) (call $var_tok) (i32.const 0)))
              (call $lex))
            (else (if (i32.eqz (local.tee $e (call $constraint))) (then (return (i32.const 0))))))))
      (call $qappend (local.get $q) (i32.const 24) (call $node2 (i32.const N_ITEM) (local.get $e) (local.get $desc)))
      (br_if $orders (i32.or (i32.or (i32.eq (global.get $tok) (i32.const T_VAR)) (i32.eq (global.get $tok) (i32.const '(')))
                             (i32.or (call $is_iri_tok)
                                     (i32.and (i32.eq (global.get $tok) (i32.const T_NAME))
                                              (i32.eqz (i32.or (call $is_kw (@str "LIMIT"))
                                                               (i32.or (call $is_kw (@str "OFFSET")) (call $is_kw (@str "VALUES"))))))))))))
  (global.set $aggs_ok (i32.const 0))
  (loop $slices
    (if (call $is_kw (@str "LIMIT")) (then
      (call $lex)
      (if (i32.lt_s (local.tee $v (call $int_tok)) (i32.const 0)) (then (return (call $expected (@str "expected an integer")))))
      (i32.store offset=28 (local.get $q) (local.get $v))
      (br $slices)))
    (if (call $is_kw (@str "OFFSET")) (then
      (call $lex)
      (if (i32.lt_s (local.tee $v (call $int_tok)) (i32.const 0)) (then (return (call $expected (@str "expected an integer")))))
      (i32.store offset=32 (local.get $q) (local.get $v))
      (br $slices))))
  (i32.const 1))

;; A subquery (on SELECT): its own scope for the variables it does not project.
(func $subselect (result i32)
  (local $q i32) (local $outer i32) (local $scope i32) (local $item i32) (local $e i32) (local $inner i32)
  (local.set $outer (global.get $cq))
  (local.set $scope (global.get $vscope))
  (global.set $cq (local.tee $q (call $q_new)))
  (i32.store (local.get $q) (i32.const 1))
  (call $lex)
  (global.set $nscopes (i32.add (global.get $nscopes) (i32.const 1)))
  (global.set $vscope (local.tee $inner (global.get $nscopes)))
  (if (i32.eqz (call $select_clause (local.get $q))) (then (return (i32.const 0))))
  (if (i32.eqz (call $where_mods (local.get $q) (i32.const 1))) (then (return (i32.const 0))))
  (if (call $is_kw (@str "VALUES")) (then
    (call $lex)
    (if (i32.eqz (local.tee $e (call $data_block))) (then (return (i32.const 0))))
    (i32.store offset=40 (local.get $q) (local.get $e))))
  (if (i32.eqz (call $check_projection (local.get $q))) (then (return (i32.const 0))))
  (global.set $vscope (local.get $scope))
  (global.set $cq (local.get $outer))
  ;; SELECT *: every variable of the subquery's own scope
  (if (i32.and (i32.load offset=4 (local.get $q)) (i32.const Q_STAR)) (then
    (local.set $e (global.get $vars))
    (loop $vars
      (if (local.get $e) (then
        (if (i32.and (i32.eq (i32.load offset=12 (local.get $e)) (local.get $inner))
                     (i32.eqz (i32.and (i32.load offset=20 (local.get $e)) (i32.const V_HIDDEN))))
          (then (call $qappend (local.get $q) (i32.const 8)
            (call $node2 (i32.const N_ITEM) (i32.load offset=8 (local.get $e)) (i32.const 0)))))
        (local.set $e (i32.load offset=16 (local.get $e)))
        (br $vars))))))
  ;; projected variables: inner slot -> the outer variable of that name
  (local.set $item (i32.load offset=8 (local.get $q)))
  (loop $items
    (if (local.get $item) (then
      (local.set $e (call $slot_entry (i32.load offset=4 (local.get $item))))
      (call $qappend (local.get $q) (i32.const 72)
        (call $node2 (i32.const N_ITEM) (i32.load offset=4 (local.get $item))
                     (call $var_in (i32.load (local.get $e)) (i32.load offset=4 (local.get $e)) (local.get $scope) (i32.const 0))))
      (local.set $item (i32.load offset=28 (local.get $item)))
      (br $items))))
  (local.get $q))

;; The variable entry of a slot.
(func $slot_entry (param $slot i32) (result i32)
  (local $e i32)
  (local.set $e (global.get $vars))
  (loop $find
    (if (i32.ne (i32.load offset=8 (local.get $e)) (local.get $slot)) (then
      (local.set $e (i32.load offset=16 (local.get $e)))
      (br $find))))
  (local.get $e))

;; CONSTRUCT template (after '{'), as triple patterns of a scratch group.
(func $template (param $q i32) (result i32)
  (local $g i32)
  (local.set $g (call $node (i32.const P_GROUP)))
  (global.set $bscope (i32.const -3))
  (global.set $bflags (i32.const 3))   ;; V_HIDDEN | V_TEMPLATE
  (loop $triples
    (if (i32.ne (global.get $tok) (i32.const '}')) (then
      (if (i32.eqz (call $ptriples (local.get $g))) (then (return (i32.const 0))))
      (if (i32.eq (global.get $tok) (i32.const '.')) (then (call $lex) (br $triples))))))
  (global.set $bscope (i32.const -2))
  (global.set $bflags (i32.const V_HIDDEN))
  (i32.store offset=36 (local.get $q) (i32.load offset=4 (local.get $g)))
  ;; paths are not allowed in templates
  (local.set $g (i32.load offset=4 (local.get $g)))
  (loop $check
    (if (local.get $g) (then
      (if (i32.ne (i32.load (local.get $g)) (i32.const P_TRIPLE)) (then (return (call $syntax (@str "paths are not allowed in a template")))))
      (local.set $g (i32.load offset=28 (local.get $g)))
      (br $check))))
  (call $eat (i32.const '}') (@str "expected '}'")))

;; Query (after the first token): the top-level query structure, or 0.
(func $parse_query (result i32)
  (local $q i32) (local $e i32) (local $g i32)
  (global.set $vars (i32.const 0))
  (global.set $nvars (i32.const 0))
  (global.set $vscope (i32.const 0))
  (global.set $nscopes (i32.const 0))
  (global.set $bscope (i32.const -2))
  (global.set $bflags (i32.const V_HIDDEN))
  (global.set $reif_flags (i32.const 0))
  (global.set $in_agg (i32.const 0))
  (global.set $aggs_ok (i32.const 0))
  (global.set $cq (local.tee $q (call $q_new)))
  (if (i32.eqz (call $prologue)) (then (return (i32.const 0))))
  (block $form
    (if (call $is_kw (@str "SELECT")) (then
      (call $lex)
      (i32.store (local.get $q) (i32.const 1))
      (if (i32.eqz (call $select_clause (local.get $q))) (then (return (i32.const 0))))
      (if (i32.eqz (call $dataset (local.get $q))) (then (return (i32.const 0))))
      (if (i32.eqz (call $where_mods (local.get $q) (i32.const 1))) (then (return (i32.const 0))))
      (br $form)))
    (if (call $is_kw (@str "CONSTRUCT")) (then
      (call $lex)
      (i32.store (local.get $q) (i32.const 2))
      (if (i32.eq (global.get $tok) (i32.const '{')) (then
        (call $lex)
        (if (i32.eqz (call $template (local.get $q))) (then (return (i32.const 0))))
        (if (i32.eqz (call $dataset (local.get $q))) (then (return (i32.const 0))))
        (if (i32.eqz (call $where_mods (local.get $q) (i32.const 1))) (then (return (i32.const 0))))
        (br $form)))
      ;; CONSTRUCT WHERE { template }
      (if (i32.eqz (call $dataset (local.get $q))) (then (return (i32.const 0))))
      (if (i32.eqz (call $eat_kw (@str "WHERE"))) (then (return (i32.const 0))))
      (if (i32.eqz (call $eat (i32.const '{') (@str "expected '{'"))) (then (return (i32.const 0))))
      (local.set $g (call $node (i32.const P_GROUP)))
      (global.set $reif_flags (i32.const V_TEMPLATE))
      (loop $triples
        (if (i32.ne (global.get $tok) (i32.const '}')) (then
          (if (i32.eqz (call $ptriples (local.get $g))) (then (return (i32.const 0))))
          (if (i32.eq (global.get $tok) (i32.const '.')) (then (call $lex) (br $triples))))))
      (global.set $reif_flags (i32.const 0))
      (if (i32.eqz (call $eat (i32.const '}') (@str "expected '}'"))) (then (return (i32.const 0))))
      (i32.store offset=12 (local.get $q) (local.get $g))
      (i32.store offset=36 (local.get $q) (i32.load offset=4 (local.get $g)))
      (if (i32.eqz (call $modifiers (local.get $q))) (then (return (i32.const 0))))
      (br $form)))
    (if (call $is_kw (@str "ASK")) (then
      (call $lex)
      (i32.store (local.get $q) (i32.const 3))
      (if (i32.eqz (call $dataset (local.get $q))) (then (return (i32.const 0))))
      (if (i32.eqz (call $where_mods (local.get $q) (i32.const 1))) (then (return (i32.const 0))))
      (br $form)))
    (if (call $is_kw (@str "DESCRIBE")) (then
      (call $lex)
      (i32.store (local.get $q) (i32.const 4))
      (if (i32.eq (global.get $tok) (i32.const '*'))
        (then
          (call $lex)
          (i32.store offset=4 (local.get $q) (i32.const Q_STAR)))
        (else
          (loop $refs
            (if (i32.eqz (local.tee $e (call $var_or_iri))) (then (return (i32.const 0))))
            (call $qappend (local.get $q) (i32.const 36) (call $node2 (i32.const N_ITEM) (local.get $e) (i32.const 0)))
            (br_if $refs (i32.or (i32.eq (global.get $tok) (i32.const T_VAR)) (call $is_iri_tok))))))
      (if (i32.eqz (call $dataset (local.get $q))) (then (return (i32.const 0))))
      (if (i32.or (call $is_kw (@str "WHERE")) (i32.eq (global.get $tok) (i32.const '{')))
        (then (if (i32.eqz (call $where_mods (local.get $q) (i32.const 0))) (then (return (i32.const 0)))))
        (else
          (i32.store offset=12 (local.get $q) (call $node (i32.const P_GROUP)))
          (if (i32.eqz (call $modifiers (local.get $q))) (then (return (i32.const 0))))))
      (br $form)))
    (return (call $expected (@str "expected SELECT, CONSTRUCT, ASK or DESCRIBE"))))
  (if (call $is_kw (@str "VALUES")) (then
    (call $lex)
    (if (i32.eqz (local.tee $e (call $data_block))) (then (return (i32.const 0))))
    (local.set $g (i32.load offset=8 (local.get $q)))
    (loop $items (if (local.get $g) (then
      (if (i32.load offset=8 (local.get $g)) (then
        (if (call $binds (local.get $e) (i32.load offset=4 (local.get $g))) (then (return (call $syntax (@str "VALUES binds a SELECT expression's variable")))))))
      (local.set $g (i32.load offset=28 (local.get $g))) (br $items))))
    (i32.store offset=40 (local.get $q) (local.get $e))))
  (if (i32.ne (global.get $tok) (i32.const T_EOF)) (then (return (call $expected (@str "unexpected token after the query")))))
  (if (i32.eq (i32.load (local.get $q)) (i32.const 1)) (then
    (if (i32.eqz (call $check_projection (local.get $q))) (then (return (i32.const 0))))))
  (local.get $q))
