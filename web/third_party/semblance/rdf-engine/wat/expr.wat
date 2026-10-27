;; Expressions: values are term ids (0 = unbound or error); computed terms are
;; interned. Special forms (&&, ||, IF, COALESCE, BOUND, IN, EXISTS) evaluate
;; their arguments themselves; every other function gets them evaluated and
;; fails when one fails.

;; --- terms -----------------------------------------------------------------------

(func $tval (param $id i32) (result i32) (i32.load offset=8 (call $rec (local.get $id))))
(func $tlen (param $id i32) (result i32) (i32.load offset=12 (call $rec (local.get $id))))
(func $tdt (param $id i32) (result i32)
  (if (result i32) (i32.eq (call $kind (local.get $id)) (i32.const 3)) (then (i32.load offset=16 (call $rec (local.get $id)))) (else (i32.const 0))))
(func $tlang_len (param $id i32) (result i32) (i32.load offset=20 (call $rec (local.get $id))))
(func $tlang (param $id i32) (result i32) (i32.add (call $tval (local.get $id)) (call $tlen (local.get $id))))
(func $tdir (param $id i32) (result i32) (i32.load8_u offset=1 (call $rec (local.get $id))))

;; A literal with a string value: simple, xsd:string or language-tagged.
(func $is_str (param $id i32) (result i32)
  (i32.le_u (i32.sub (call $tdt (local.get $id)) (i32.const 2)) (i32.const 2)))
;; A simple literal or xsd:string.
(func $is_plain (param $id i32) (result i32)
  (i32.eq (call $tdt (local.get $id)) (i32.const 2)))

;; Datatype classes, cached in the datatype IRI's record (+24).
(@define C_OTHER 1) (@define C_INTEGER 2) (@define C_DECIMAL 3) (@define C_FLOAT 4) (@define C_DOUBLE 5)
(@define C_BOOLEAN 6) (@define C_DATETIME 7) (@define C_STRING 8) (@define C_LANG 9) (@define C_DATE 10)

(func $dtc (param $dt i32) (result i32)
  (local $r i32) (local $c i32) (local $p i32) (local $n i32)
  (if (i32.eqz (local.get $dt)) (then (return (i32.const 0))))
  (local.set $r (call $rec (local.get $dt)))
  (if (local.tee $c (i32.load offset=24 (local.get $r))) (then (return (local.get $c))))
  (local.set $c (i32.const C_OTHER))
  (if (i32.eq (local.get $dt) (i32.const 2)) (then (local.set $c (i32.const C_STRING))))
  (if (i32.le_u (i32.sub (local.get $dt) (i32.const 3)) (i32.const 1)) (then (local.set $c (i32.const C_LANG))))
  (local.set $n (i32.load offset=12 (local.get $r)))
  (if (i32.and (i32.eq (local.get $c) (i32.const C_OTHER)) (i32.gt_u (local.get $n) (i32.const 33))) (then
    (if (call $memeq (i32.load offset=8 (local.get $r)) (@str "http://www.w3.org/2001/XMLSchema#")) (then
      (local.set $c (call $xsd_class (i32.add (i32.load offset=8 (local.get $r)) (i32.const 33)) (i32.sub (local.get $n) (i32.const 33))))))))
  (i32.store offset=24 (local.get $r) (local.get $c))
  (local.get $c))

(func $xsd_class (param $p i32) (param $n i32) (result i32)
  (if (call $same (local.get $p) (local.get $n) (@str "integer") (i32.const 0)) (then (return (i32.const C_INTEGER))))
  (if (call $same (local.get $p) (local.get $n) (@str "decimal") (i32.const 0)) (then (return (i32.const C_DECIMAL))))
  (if (call $same (local.get $p) (local.get $n) (@str "float") (i32.const 0)) (then (return (i32.const C_FLOAT))))
  (if (call $same (local.get $p) (local.get $n) (@str "double") (i32.const 0)) (then (return (i32.const C_DOUBLE))))
  (if (call $same (local.get $p) (local.get $n) (@str "boolean") (i32.const 0)) (then (return (i32.const C_BOOLEAN))))
  (if (call $same (local.get $p) (local.get $n) (@str "dateTime") (i32.const 0)) (then (return (i32.const C_DATETIME))))
  (if (call $same (local.get $p) (local.get $n) (@str "date") (i32.const 0)) (then (return (i32.const C_DATE))))
  (if (call $same (local.get $p) (local.get $n) (@str "string") (i32.const 0)) (then (return (i32.const C_STRING))))
  (if (i32.or (i32.or (i32.or (call $same (local.get $p) (local.get $n) (@str "int") (i32.const 0))
                              (call $same (local.get $p) (local.get $n) (@str "long") (i32.const 0)))
                      (i32.or (call $same (local.get $p) (local.get $n) (@str "short") (i32.const 0))
                              (call $same (local.get $p) (local.get $n) (@str "byte") (i32.const 0))))
              (i32.or (i32.or (call $same (local.get $p) (local.get $n) (@str "nonPositiveInteger") (i32.const 0))
                              (call $same (local.get $p) (local.get $n) (@str "negativeInteger") (i32.const 0)))
                      (i32.or (call $same (local.get $p) (local.get $n) (@str "nonNegativeInteger") (i32.const 0))
                              (call $same (local.get $p) (local.get $n) (@str "positiveInteger") (i32.const 0)))))
    (then (return (i32.const C_INTEGER))))
  (if (i32.or (i32.or (call $same (local.get $p) (local.get $n) (@str "unsignedLong") (i32.const 0))
                      (call $same (local.get $p) (local.get $n) (@str "unsignedInt") (i32.const 0)))
              (i32.or (call $same (local.get $p) (local.get $n) (@str "unsignedShort") (i32.const 0))
                      (call $same (local.get $p) (local.get $n) (@str "unsignedByte") (i32.const 0))))
    (then (return (i32.const C_INTEGER))))
  (i32.const C_OTHER))

;; Whether a cast to xsd:<local> is supported (parse time).
(func $xsd_cast_type (param $p i32) (param $n i32) (result i32)
  (if (i32.ne (call $xsd_class (local.get $p) (local.get $n)) (i32.const C_OTHER)) (then (return (i32.const 1))))
  ;; and the times and durations (temporal.wat)
  (i32.or (i32.or (call $same (local.get $p) (local.get $n) (@str "time") (i32.const 0))
                  (call $same (local.get $p) (local.get $n) (@str "duration") (i32.const 0)))
          (i32.or (call $same (local.get $p) (local.get $n) (@str "dayTimeDuration") (i32.const 0))
                  (call $same (local.get $p) (local.get $n) (@str "yearMonthDuration") (i32.const 0)))))

;; --- string building ------------------------------------------------------------

(global $xb (mut i32) (i32.const 0))
(global $xb_len (mut i32) (i32.const 0))
(global $xb_cap (mut i32) (i32.const 0))
;; Where the string being built starts: CONCAT appends its arguments in place,
;; and moves the base past what it has so far while an argument is evaluated.
(global $xb_base (mut i32) (i32.const 0))

(func $xb_reset (global.set $xb_len (global.get $xb_base)))
(func $xb_ptr (result i32) (i32.add (global.get $xb) (global.get $xb_base)))
(func $xb_n (result i32) (i32.sub (global.get $xb_len) (global.get $xb_base)))

(func $xb_room (param $n i32)
  (local $cap i32)
  (if (i32.gt_u (i32.add (global.get $xb_len) (local.get $n)) (global.get $xb_cap)) (then
    (local.set $cap (i32.shl (i32.add (i32.add (global.get $xb_len) (local.get $n)) (i32.const 64)) (i32.const 1)))
    (global.set $xb (if (result i32) (global.get $xb_cap)
      (then (call $realloc (global.get $xb) (global.get $xb_cap) (local.get $cap) (global.get $xb_len)))
      (else (call $alloc (local.get $cap)))))
    (global.set $xb_cap (local.get $cap)))))

(func $xb_put (param $p i32) (param $n i32)
  (call $xb_room (local.get $n))
  (memory.copy (i32.add (global.get $xb) (global.get $xb_len)) (local.get $p) (local.get $n))
  (global.set $xb_len (i32.add (global.get $xb_len) (local.get $n))))

(func $xb_byte (param $c i32)
  (call $xb_room (i32.const 1))
  (i32.store8 (i32.add (global.get $xb) (global.get $xb_len)) (local.get $c))
  (global.set $xb_len (i32.add (global.get $xb_len) (i32.const 1))))

(func $xb_cp (param $cp i32)
  (call $xb_room (i32.const 4))
  (global.set $xb_len (i32.sub (call $encode (i32.add (global.get $xb) (global.get $xb_len)) (local.get $cp)) (global.get $xb))))

(func $xb_put_term (param $id i32)
  (call $xb_put (call $tval (local.get $id)) (call $tlen (local.get $id))))

;; The built string as a literal: simple, or with the language of `like`.
(func $xb_string (param $like i32) (result i32)
  (call $str_like (call $xb_ptr) (call $xb_n) (local.get $like)))

;; A string literal [p, p+n) with the language and direction of like (0: simple).
(func $str_like (param $p i32) (param $n i32) (param $like i32) (result i32)
  (if (i32.and (i32.ne (local.get $like) (i32.const 0)) (i32.ge_u (call $tdt (local.get $like)) (i32.const 3)))
    (then (return (call $intern (i32.or (i32.const 3) (i32.shl (call $tdir (local.get $like)) (i32.const 8))) (local.get $p) (local.get $n)
                        (call $tdt (local.get $like)) (call $tlang (local.get $like)) (call $tlang_len (local.get $like)) (i32.const 1)))))
  (call $intern (i32.const 3) (local.get $p) (local.get $n) (i32.const 2) (i32.const 0) (i32.const 0) (i32.const 1)))

(func $simple (param $p i32) (param $n i32) (result i32)
  (call $intern (i32.const 3) (local.get $p) (local.get $n) (i32.const 2) (i32.const 0) (i32.const 0) (i32.const 1)))

(func $typed (param $p i32) (param $n i32) (param $dt i32) (result i32)
  (call $intern (i32.const 3) (local.get $p) (local.get $n) (local.get $dt) (i32.const 0) (i32.const 0) (i32.const 1)))

;; --- booleans and effective boolean values -----------------------------------------

(global $t_true (mut i32) (i32.const 0))
(global $t_false (mut i32) (i32.const 0))

(func $bool (param $b i32) (result i32)
  (if (i32.eqz (global.get $t_true)) (then
    (global.set $t_true (call $typed (@str "true") (call $known (i32.const K_BOOLEAN))))
    (global.set $t_false (call $typed (@str "false") (call $known (i32.const K_BOOLEAN))))))
  (select (global.get $t_true) (global.get $t_false) (local.get $b)))

;; The value of a boolean literal: 1, 0, or -1 when ill-typed.
(func $bool_value (param $id i32) (result i32)
  (local $p i32) (local $n i32)
  (local.set $p (call $tval (local.get $id)))
  (local.set $n (call $tlen (local.get $id)))
  (if (i32.or (call $same (local.get $p) (local.get $n) (@str "true") (i32.const 0)) (call $same (local.get $p) (local.get $n) (@str "1") (i32.const 0)))
    (then (return (i32.const 1))))
  (if (i32.or (call $same (local.get $p) (local.get $n) (@str "false") (i32.const 0)) (call $same (local.get $p) (local.get $n) (@str "0") (i32.const 0)))
    (then (return (i32.const 0))))
  (i32.const -1))

;; Effective boolean value: 1, 0, or -1 for an error.
(func $ebv (param $id i32) (result i32)
  (local $c i32)
  (if (i32.ne (call $kind (local.get $id)) (i32.const 3)) (then (return (i32.const -1))))
  (local.set $c (call $dtc (call $tdt (local.get $id))))
  ;; ill-typed booleans and numbers are errors
  (if (i32.eq (local.get $c) (i32.const C_BOOLEAN)) (then (return (call $bool_value (local.get $id)))))
  (if (i32.eq (local.get $c) (i32.const C_STRING)) (then (return (i32.ne (call $tlen (local.get $id)) (i32.const 0)))))
  (if (i32.le_u (i32.sub (local.get $c) (i32.const C_INTEGER)) (i32.const 3)) (then
    (if (i32.eqz (call $num (local.get $id))) (then (return (i32.const -1))))
    (return (i32.and (f64.ne (global.get $nf) (f64.const 0)) (f64.eq (global.get $nf) (global.get $nf))))))
  (i32.const -1))

;; --- comparisons -------------------------------------------------------------------

;; Comparable classes of a and b for = and <: 1 numeric, 2 string, 3 boolean,
;; 4 dateTime, 0 none.
(func $pair_class (param $a i32) (param $b i32) (result i32)
  (local $ca i32) (local $cb i32)
  (local.set $ca (call $dtc (call $tdt (local.get $a))))
  (local.set $cb (call $dtc (call $tdt (local.get $b))))
  (if (i32.and (i32.le_u (i32.sub (local.get $ca) (i32.const C_INTEGER)) (i32.const 3))
               (i32.le_u (i32.sub (local.get $cb) (i32.const C_INTEGER)) (i32.const 3))) (then (return (i32.const 1))))
  (if (i32.ne (local.get $ca) (local.get $cb)) (then (return (i32.const 0))))
  (if (i32.eq (local.get $ca) (i32.const C_STRING)) (then (return (i32.const 2))))
  (if (i32.eq (local.get $ca) (i32.const C_BOOLEAN)) (then (return (i32.const 3))))
  (if (i32.eq (local.get $ca) (i32.const C_DATETIME)) (then (return (i32.const 4))))
  (if (i32.eq (local.get $ca) (i32.const C_DATE)) (then (return (i32.const 4))))
  (i32.const 0))

(func $bytes_cmp (param $p i32) (param $n i32) (param $q i32) (param $m i32) (result i32)
  (local $i i32) (local $a i32) (local $b i32)
  (loop $bytes
    (if (i32.and (i32.lt_u (local.get $i) (local.get $n)) (i32.lt_u (local.get $i) (local.get $m))) (then
      (local.set $a (i32.load8_u (i32.add (local.get $p) (local.get $i))))
      (local.set $b (i32.load8_u (i32.add (local.get $q) (local.get $i))))
      (if (i32.ne (local.get $a) (local.get $b)) (then (return (select (i32.const -1) (i32.const 1) (i32.lt_u (local.get $a) (local.get $b))))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $bytes))))
  (i32.sub (i32.gt_u (local.get $n) (local.get $m)) (i32.lt_u (local.get $n) (local.get $m))))

;; a < b etc.: -1, 0, 1, or 2 when they do not compare (an error).
(func $rel_cmp (param $a i32) (param $b i32) (result i32)
  (local $c i32) (local $x i32) (local $y i32)
  (if (i32.or (i32.ne (call $kind (local.get $a)) (i32.const 3)) (i32.ne (call $kind (local.get $b)) (i32.const 3))) (then (return (i32.const 2))))
  (local.set $c (call $pair_class (local.get $a) (local.get $b)))
  (if (i32.eq (local.get $c) (i32.const 1)) (then (return (call $num_cmp (local.get $a) (local.get $b)))))
  (if (i32.eq (local.get $c) (i32.const 2)) (then
    (return (call $bytes_cmp (call $tval (local.get $a)) (call $tlen (local.get $a)) (call $tval (local.get $b)) (call $tlen (local.get $b))))))
  (if (i32.eq (local.get $c) (i32.const 3)) (then
    (local.set $x (call $bool_value (local.get $a)))
    (local.set $y (call $bool_value (local.get $b)))
    (if (i32.or (i32.lt_s (local.get $x) (i32.const 0)) (i32.lt_s (local.get $y) (i32.const 0))) (then (return (i32.const 2))))
    (return (i32.sub (local.get $x) (local.get $y)))))
  (if (i32.eq (local.get $c) (i32.const 4)) (then (return (call $date_cmp (local.get $a) (local.get $b)))))
  ;; times and durations
  (if (i32.and (i32.eq (call $dtc (call $tdt (local.get $a))) (i32.const C_OTHER)) (i32.eq (call $dtc (call $tdt (local.get $b))) (i32.const C_OTHER))) (then
    (local.set $c (call $temporal_cmp (local.get $a) (local.get $b) (i32.const 0)))
    (return (select (i32.const 2) (local.get $c) (i32.eq (local.get $c) (i32.const 3))))))
  (i32.const 2))

;; a = b: 1, 0, or -1 for an error. Literals of known, different types are
;; different; an unknown datatype or an ill-typed literal is an error.
(func $rdf_eq (param $a i32) (param $b i32) (result i32)
  (local $c i32) (local $r i32) (local $x i32) (local $y i32)
  (if (i32.or (i32.eqz (local.get $a)) (i32.eqz (local.get $b))) (then (return (i32.const -1))))
  (if (i32.and (i32.eq (call $kind (local.get $a)) (i32.const 3)) (i32.eq (call $kind (local.get $b)) (i32.const 3))) (then
    (local.set $x (call $dtc (call $tdt (local.get $a))))
    (local.set $y (call $dtc (call $tdt (local.get $b))))
    (local.set $c (call $pair_class (local.get $a) (local.get $b)))
    (if (local.get $c) (then
      (if (i32.and (i32.eq (local.get $a) (local.get $b)) (i32.ne (local.get $c) (i32.const 1))) (then (return (i32.const 1))))
      (local.set $r (call $rel_cmp (local.get $a) (local.get $b)))
      (if (i32.ne (local.get $r) (i32.const 2)) (then (return (i32.eqz (local.get $r)))))
      ;; NaN is unequal; ill-typed values and indeterminate dates are errors
      (if (i32.eq (local.get $c) (i32.const 1)) (then
        (if (i32.and (i32.ne (call $num (local.get $a)) (i32.const 0)) (i32.ne (call $num (local.get $b)) (i32.const 0))) (then (return (i32.const 0))))))
      (return (select (i32.const 1) (i32.const -1) (i32.eq (local.get $a) (local.get $b))))))
    (if (i32.eq (local.get $a) (local.get $b)) (then (return (i32.const 1))))
    (if (i32.or (i32.eq (local.get $x) (i32.const C_LANG)) (i32.eq (local.get $y) (i32.const C_LANG))) (then (return (i32.const 0))))
    ;; times and durations
    (if (i32.and (i32.eq (local.get $x) (i32.const C_OTHER)) (i32.eq (local.get $y) (i32.const C_OTHER))) (then
      (local.set $r (call $temporal_cmp (local.get $a) (local.get $b) (i32.const 1)))
      (if (i32.ne (local.get $r) (i32.const 3)) (then (return (select (i32.const -1) (i32.eqz (local.get $r)) (i32.eq (local.get $r) (i32.const 2))))))))
    (if (i32.or (i32.eq (local.get $x) (i32.const C_OTHER)) (i32.eq (local.get $y) (i32.const C_OTHER))) (then (return (i32.const -1))))
    (if (i32.or (i32.eqz (call $well_typed (local.get $a) (local.get $x))) (i32.eqz (call $well_typed (local.get $b) (local.get $y)))) (then (return (i32.const -1))))
    (return (i32.const 0))))
  (if (i32.eq (local.get $a) (local.get $b)) (then (return (i32.const 1))))
  ;; triple terms: by their parts
  (if (i32.and (i32.eq (call $kind (local.get $a)) (i32.const 4)) (i32.eq (call $kind (local.get $b)) (i32.const 4))) (then
    (local.set $x (call $rec (local.get $a)))
    (local.set $y (call $rec (local.get $b)))
    (local.set $r (call $rdf_eq (i32.load offset=8 (local.get $x)) (i32.load offset=8 (local.get $y))))
    (if (i32.ne (local.get $r) (i32.const 1)) (then (return (local.get $r))))
    (local.set $r (call $rdf_eq (i32.load offset=12 (local.get $x)) (i32.load offset=12 (local.get $y))))
    (if (i32.ne (local.get $r) (i32.const 1)) (then (return (local.get $r))))
    (return (call $rdf_eq (i32.load offset=16 (local.get $x)) (i32.load offset=16 (local.get $y))))))
  (i32.const 0))

;; Whether literal id of datatype class c has a valid lexical form.
(func $well_typed (param $id i32) (param $c i32) (result i32)
  (if (i32.le_u (i32.sub (local.get $c) (i32.const C_INTEGER)) (i32.const 3)) (then (return (i32.ne (call $num (local.get $id)) (i32.const 0)))))
  (if (i32.eq (local.get $c) (i32.const C_BOOLEAN)) (then (return (i32.ge_s (call $bool_value (local.get $id)) (i32.const 0)))))
  (if (i32.or (i32.eq (local.get $c) (i32.const C_DATETIME)) (i32.eq (local.get $c) (i32.const C_DATE)))
    (then (return (call $date_parse (call $tval (local.get $id)) (call $tlen (local.get $id))))))
  (i32.const 1))

;; ORDER BY: unbound < blank nodes < IRIs < literals < triple terms.
(func $order_cmp (param $a i32) (param $b i32) (result i32)
  (local $ka i32) (local $kb i32) (local $r i32)
  (if (i32.eq (local.get $a) (local.get $b)) (then (return (i32.const 0))))
  (local.set $ka (call $order_rank (local.get $a)))
  (local.set $kb (call $order_rank (local.get $b)))
  (if (i32.ne (local.get $ka) (local.get $kb)) (then (return (select (i32.const -1) (i32.const 1) (i32.lt_u (local.get $ka) (local.get $kb))))))
  (if (i32.eqz (local.get $ka)) (then (return (i32.const 0))))
  (if (i32.eq (local.get $ka) (i32.const 3)) (then
    (local.set $r (call $rel_cmp (local.get $a) (local.get $b)))
    (if (i32.and (i32.ne (local.get $r) (i32.const 2)) (i32.ne (local.get $r) (i32.const 0))) (then (return (local.get $r))))
    ;; then by lexical form, datatype and language
    (if (local.tee $r (call $bytes_cmp (call $tval (local.get $a)) (call $tlen (local.get $a)) (call $tval (local.get $b)) (call $tlen (local.get $b))))
      (then (return (local.get $r))))
    (if (i32.ne (call $tdt (local.get $a)) (call $tdt (local.get $b)))
      (then (return (call $order_cmp (call $tdt (local.get $a)) (call $tdt (local.get $b))))))
    (return (call $bytes_cmp (call $tlang (local.get $a)) (call $tlang_len (local.get $a)) (call $tlang (local.get $b)) (call $tlang_len (local.get $b))))))
  (if (i32.eq (local.get $ka) (i32.const 4)) (then
    (if (local.tee $r (call $order_cmp (i32.load offset=8 (call $rec (local.get $a))) (i32.load offset=8 (call $rec (local.get $b))))) (then (return (local.get $r))))
    (if (local.tee $r (call $order_cmp (i32.load offset=12 (call $rec (local.get $a))) (i32.load offset=12 (call $rec (local.get $b))))) (then (return (local.get $r))))
    (return (call $order_cmp (i32.load offset=16 (call $rec (local.get $a))) (i32.load offset=16 (call $rec (local.get $b)))))))
  (call $bytes_cmp (call $tval (local.get $a)) (call $tlen (local.get $a)) (call $tval (local.get $b)) (call $tlen (local.get $b))))

(func $order_rank (param $id i32) (result i32)
  (local $k i32)
  (if (i32.eqz (local.get $id)) (then (return (i32.const 0))))
  (local.set $k (call $kind (local.get $id)))
  (if (i32.eq (local.get $k) (i32.const 2)) (then (return (i32.const 1))))
  (if (i32.eq (local.get $k) (i32.const 1)) (then (return (i32.const 2))))
  (if (i32.eq (local.get $k) (i32.const 3)) (then (return (i32.const 3))))
  (i32.const 4))

;; --- evaluation --------------------------------------------------------------------

(func $ev (param $e i32) (param $row i32) (result i32)
  (local $k i32) (local $t i32) (local $r i32) (local $n i32)
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_VAR)) (then (return (i32.load (i32.add (local.get $row) (i32.shl (i32.load offset=4 (local.get $e)) (i32.const 2)))))))
  (if (i32.eq (local.get $k) (i32.const E_CONST)) (then (return (i32.load offset=4 (local.get $e)))))
  (if (i32.eq (local.get $k) (i32.const E_CALL)) (then (return (call $call (local.get $e) (local.get $row)))))
  (if (i32.eq (local.get $k) (i32.const E_AGG)) (then (return (i32.load (i32.add (local.get $row) (i32.shl (i32.load offset=20 (local.get $e)) (i32.const 2)))))))
  ;; EXISTS: the group with this row substituted
  (local.set $t (call $t_one (local.get $row)))
  (local.set $k (global.get $lat))
  (global.set $lat (local.get $row))
  (local.set $r (call $eval_group (i32.load offset=4 (local.get $e)) (local.get $t) (i32.const 0)))
  (global.set $lat (local.get $k))
  (local.set $n (i32.ne (i32.load offset=4 (local.get $r)) (i32.const 0)))
  (call $t_free (local.get $r))
  (call $t_free (local.get $t))
  (call $bool (i32.xor (local.get $n) (i32.load offset=8 (local.get $e)))))

;; Whether the bytes [p, p+n) contain [q, q+m); the offset in $found.
(global $found (mut i32) (i32.const 0))
(func $find (param $p i32) (param $n i32) (param $q i32) (param $m i32) (result i32)
  (local $i i32)
  (if (i32.gt_u (local.get $m) (local.get $n)) (then (return (i32.const 0))))
  (loop $at
    (if (i32.le_u (local.get $i) (i32.sub (local.get $n) (local.get $m))) (then
      (if (call $memeq (i32.add (local.get $p) (local.get $i)) (local.get $q) (local.get $m)) (then
        (global.set $found (local.get $i))
        (return (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $at))))
  (i32.const 0))

;; Whether string literals a and b are argument-compatible (b simple, or the
;; same language).
(func $compat (param $a i32) (param $b i32) (result i32)
  (if (i32.eqz (i32.and (call $is_str (local.get $a)) (call $is_str (local.get $b)))) (then (return (i32.const 0))))
  (if (call $is_plain (local.get $b)) (then (return (i32.const 1))))
  (i32.and (i32.eq (call $tdt (local.get $a)) (call $tdt (local.get $b)))
           (call $same (call $tlang (local.get $a)) (call $tlang_len (local.get $a)) (call $tlang (local.get $b)) (call $tlang_len (local.get $b)) (i32.const 0))))

;; Code points in [p, p+n).
(func $cp_count (param $p i32) (param $n i32) (result i32)
  (local $c i32)
  (loop $bytes
    (if (local.get $n) (then
      (local.set $c (i32.add (local.get $c) (i32.ne (i32.and (i32.load8_u (local.get $p)) (i32.const 0xc0)) (i32.const 0x80))))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br $bytes))))
  (local.get $c))

;; The byte offset of code point k (0-based) in [p, p+n), or n.
(func $cp_offset (param $p i32) (param $n i32) (param $k i32) (result i32)
  (local $i i32)
  (loop $bytes
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (if (i32.ne (i32.and (i32.load8_u (i32.add (local.get $p) (local.get $i))) (i32.const 0xc0)) (i32.const 0x80)) (then
        (if (i32.eqz (local.get $k)) (then (return (local.get $i))))
        (local.set $k (i32.sub (local.get $k) (i32.const 1)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $bytes))))
  (local.get $n))

;; Upper- (up) or lowercase of a code point: ASCII, Latin-1, Greek, Cyrillic.
(func $case (param $c i32) (param $up i32) (result i32)
  (if (local.get $up)
    (then
      (if (i32.or (call $in (local.get $c) (i32.const 0x61) (i32.const 0x7a))
                  (i32.or (i32.and (call $in (local.get $c) (i32.const 0xe0) (i32.const 0xfe)) (i32.ne (local.get $c) (i32.const 0xf7)))
                          (i32.or (i32.and (call $in (local.get $c) (i32.const 0x3b1) (i32.const 0x3c9)) (i32.ne (local.get $c) (i32.const 0x3c2)))
                                  (call $in (local.get $c) (i32.const 0x430) (i32.const 0x44f)))))
        (then (return (i32.sub (local.get $c) (i32.const 0x20)))))
      (if (call $in (local.get $c) (i32.const 0x450) (i32.const 0x45f)) (then (return (i32.sub (local.get $c) (i32.const 0x50))))))
    (else
      (if (i32.or (call $in (local.get $c) (i32.const 0x41) (i32.const 0x5a))
                  (i32.or (i32.and (call $in (local.get $c) (i32.const 0xc0) (i32.const 0xde)) (i32.ne (local.get $c) (i32.const 0xd7)))
                          (i32.or (i32.and (call $in (local.get $c) (i32.const 0x391) (i32.const 0x3a9)) (i32.ne (local.get $c) (i32.const 0x3a2)))
                                  (call $in (local.get $c) (i32.const 0x410) (i32.const 0x42f)))))
        (then (return (i32.add (local.get $c) (i32.const 0x20)))))
      (if (call $in (local.get $c) (i32.const 0x400) (i32.const 0x40f)) (then (return (i32.add (local.get $c) (i32.const 0x50)))))))
  (local.get $c))

;; Arguments: the i-th argument expression of a call.
(func $arg (param $e i32) (param $i i32) (result i32)
  (local $a i32)
  (local.set $a (i32.load offset=8 (local.get $e)))
  (loop $skip
    (if (local.get $i) (then
      (local.set $a (i32.load offset=28 (local.get $a)))
      (local.set $i (i32.sub (local.get $i) (i32.const 1)))
      (br $skip))))
  (local.get $a))

(func $call (param $e i32) (param $row i32) (result i32)
  (local $f i32) (local $a i32) (local $b i32) (local $c i32) (local $d i32) (local $x i32) (local $y i32) (local $n i32) (local $err i32)
  (local $p i32) (local $len i32) (local $i i32) (local $argc i32)
  (local.set $f (i32.load offset=4 (local.get $e)))
  (local.set $argc (i32.load offset=12 (local.get $e)))
  ;; special forms
  (if (i32.eq (local.get $f) (i32.const F_BOUND)) (then
    (local.set $a (call $arg (local.get $e) (i32.const 0)))
    (return (call $bool (i32.ne (call $ev (local.get $a) (local.get $row)) (i32.const 0))))))
  (if (i32.eq (local.get $f) (i32.const F_COALESCE)) (then
    (local.set $a (i32.load offset=8 (local.get $e)))
    (loop $args
      (if (local.get $a) (then
        (if (local.tee $x (call $ev (local.get $a) (local.get $row))) (then (return (local.get $x))))
        (local.set $a (i32.load offset=28 (local.get $a)))
        (br $args))))
    (return (i32.const 0))))
  (if (i32.eq (local.get $f) (i32.const F_IF)) (then
    (local.set $x (call $ebv (call $ev (call $arg (local.get $e) (i32.const 0)) (local.get $row))))
    (if (i32.lt_s (local.get $x) (i32.const 0)) (then (return (i32.const 0))))
    (return (call $ev (call $arg (local.get $e) (select (i32.const 1) (i32.const 2) (local.get $x))) (local.get $row)))))
  (if (i32.eq (local.get $f) (i32.const F_OR)) (then
    (local.set $x (call $ebv (call $ev (call $arg (local.get $e) (i32.const 0)) (local.get $row))))
    (if (i32.eq (local.get $x) (i32.const 1)) (then (return (call $bool (i32.const 1)))))
    (local.set $y (call $ebv (call $ev (call $arg (local.get $e) (i32.const 1)) (local.get $row))))
    (if (i32.eq (local.get $y) (i32.const 1)) (then (return (call $bool (i32.const 1)))))
    (if (i32.or (i32.lt_s (local.get $x) (i32.const 0)) (i32.lt_s (local.get $y) (i32.const 0))) (then (return (i32.const 0))))
    (return (call $bool (i32.const 0)))))
  (if (i32.eq (local.get $f) (i32.const F_AND)) (then
    (local.set $x (call $ebv (call $ev (call $arg (local.get $e) (i32.const 0)) (local.get $row))))
    (if (i32.eqz (local.get $x)) (then (return (call $bool (i32.const 0)))))
    (local.set $y (call $ebv (call $ev (call $arg (local.get $e) (i32.const 1)) (local.get $row))))
    (if (i32.eqz (local.get $y)) (then (return (call $bool (i32.const 0)))))
    (if (i32.or (i32.lt_s (local.get $x) (i32.const 0)) (i32.lt_s (local.get $y) (i32.const 0))) (then (return (i32.const 0))))
    (return (call $bool (i32.const 1)))))
  (if (i32.or (i32.eq (local.get $f) (i32.const F_IN)) (i32.eq (local.get $f) (i32.const F_NOTIN))) (then
    (local.set $a (i32.load offset=8 (local.get $e)))
    (if (i32.eqz (local.tee $x (call $ev (local.get $a) (local.get $row)))) (then (return (i32.const 0))))
    (loop $members
      (if (local.tee $a (i32.load offset=28 (local.get $a))) (then
        (local.set $y (call $rdf_eq (local.get $x) (call $ev (local.get $a) (local.get $row))))
        (if (i32.eq (local.get $y) (i32.const 1)) (then (return (call $bool (i32.eq (local.get $f) (i32.const F_IN))))))
        (if (i32.lt_s (local.get $y) (i32.const 0)) (then (local.set $err (i32.const 1))))
        (br $members))))
    (if (local.get $err) (then (return (i32.const 0))))
    (return (call $bool (i32.eq (local.get $f) (i32.const F_NOTIN))))))
  (if (i32.eq (local.get $f) (i32.const F_CONCAT)) (then
    (local.set $p (call $xb_open))
    (local.set $c (call $concat_parts (local.get $e) (local.get $row)))
    (local.set $x (if (result i32) (i32.lt_s (local.get $c) (i32.const 0)) (then (i32.const 0)) (else (call $xb_string (local.get $c)))))
    (call $xb_close (local.get $p))
    (return (local.get $x))))
  (if (i32.and (i32.le_u (i32.sub (local.get $f) (i32.const F_IRI)) (i32.const 1)) (i32.eq (local.get $argc) (i32.const 1))) (then
    (local.set $a (i32.load offset=8 (local.get $e)))
    (if (i32.and (i32.eq (i32.load (local.get $a)) (i32.const E_CALL)) (i32.eq (i32.load offset=4 (local.get $a)) (i32.const F_CONCAT))) (then
      ;; IRI(CONCAT(...)): the IRI straight from the built string
      (local.set $p (call $xb_open))
      (local.set $x (i32.const 0))
      (if (i32.eqz (call $concat_parts (local.get $a) (local.get $row))) (then
        (global.set $base_p (i32.load offset=108 (global.get $q)))
        (global.set $base_n (i32.load offset=112 (global.get $q)))
        (local.set $x (call $iri_ref (call $xb_ptr) (call $xb_n)))))
      (call $xb_close (local.get $p))
      (return (local.get $x))))))
  (if (i32.and (i32.eq (local.get $f) (i32.const F_CAST)) (i32.eq (local.get $argc) (i32.const 1))) (then
    (if (i32.eqz (local.tee $x (call $ev (call $arg (local.get $e) (i32.const 0)) (local.get $row)))) (then (return (i32.const 0))))
    (return (call $cast (local.get $x) (i32.load offset=16 (local.get $e))))))
  ;; evaluated arguments
  (if (local.get $argc) (then
    (if (i32.eqz (local.tee $a (call $ev (call $arg (local.get $e) (i32.const 0)) (local.get $row)))) (then (return (i32.const 0))))))
  (if (i32.gt_u (local.get $argc) (i32.const 1)) (then
    (if (i32.eqz (local.tee $b (call $ev (call $arg (local.get $e) (i32.const 1)) (local.get $row)))) (then (return (i32.const 0))))))
  (if (i32.gt_u (local.get $argc) (i32.const 2)) (then
    (if (i32.eqz (local.tee $c (call $ev (call $arg (local.get $e) (i32.const 2)) (local.get $row)))) (then (return (i32.const 0))))))
  (if (i32.gt_u (local.get $argc) (i32.const 3)) (then
    (if (i32.eqz (local.tee $d (call $ev (call $arg (local.get $e) (i32.const 3)) (local.get $row)))) (then (return (i32.const 0))))))
  (global.set $ev_row (local.get $row))
  (call $fn (local.get $f) (local.get $argc) (local.get $a) (local.get $b) (local.get $c) (local.get $d)))

(func $same_lang (param $a i32) (param $b i32) (result i32)
  (i32.and (i32.eq (call $tdt (local.get $a)) (call $tdt (local.get $b)))
           (i32.and (i32.eq (call $tdir (local.get $a)) (call $tdir (local.get $b)))
                    (call $same (call $tlang (local.get $a)) (call $tlang_len (local.get $a)) (call $tlang (local.get $b)) (call $tlang_len (local.get $b)) (i32.const 0)))))

;; Start a string after what is being built (returns the base to restore).
(func $xb_open (result i32)
  (local $b i32)
  (local.set $b (global.get $xb_base))
  (global.set $xb_base (global.get $xb_len))
  (local.get $b))

;; Drop the string started by $xb_open, back to base b.
(func $xb_close (param $b i32)
  (global.set $xb_len (global.get $xb_base))
  (global.set $xb_base (local.get $b)))

;; Append CONCAT e's arguments to the string being built: -1 for an error, else
;; the language source of the result (0: simple; a literal: its language).
(func $concat_parts (param $e i32) (param $row i32) (result i32)
  (local $a i32) (local $c i32) (local $r i32)
  (local.set $c (i32.const -2))
  (local.set $a (i32.load offset=8 (local.get $e)))
  (loop $args
    (if (local.get $a) (then
      (if (i32.lt_s (local.tee $r (call $ev_append (local.get $a) (local.get $row))) (i32.const 0)) (then (return (i32.const -1))))
      (if (i32.eq (local.get $c) (i32.const -2))
        (then (local.set $c (local.get $r)))
        (else (if (i32.and (i32.gt_s (local.get $c) (i32.const 0))
                           (i32.or (i32.eqz (local.get $r)) (i32.eqz (call $same_lang (local.get $c) (local.get $r)))))
          (then (local.set $c (i32.const 0))))))
      (local.set $a (i32.load offset=28 (local.get $a)))
      (br $args))))
  (select (i32.const 0) (local.get $c) (i32.eq (local.get $c) (i32.const -2))))

;; Append the string value of e (a string literal; STR() of an IRI or literal,
;; and nested CONCATs, without making their terms): as $concat_parts.
(func $ev_append (param $e i32) (param $row i32) (result i32)
  (local $x i32) (local $b i32) (local $str i32)
  (if (i32.eq (i32.load (local.get $e)) (i32.const E_CALL)) (then
    (if (i32.eq (i32.load offset=4 (local.get $e)) (i32.const F_CONCAT)) (then (return (call $concat_parts (local.get $e) (local.get $row)))))
    (if (i32.eq (i32.load offset=4 (local.get $e)) (i32.const F_STR)) (then
      (local.set $str (i32.const 1))
      (local.set $e (i32.load offset=8 (local.get $e)))))))
  ;; what an argument builds goes after ours, and is dropped
  (local.set $b (call $xb_open))
  (local.set $x (call $ev (local.get $e) (local.get $row)))
  (call $xb_close (local.get $b))
  (if (local.get $str) (then
    (if (i32.and (i32.ne (call $kind (local.get $x)) (i32.const 1)) (i32.ne (call $kind (local.get $x)) (i32.const 3))) (then (return (i32.const -1))))
    (call $xb_put_term (local.get $x))
    (return (i32.const 0))))
  (if (i32.eqz (call $is_str (local.get $x))) (then (return (i32.const -1))))
  (call $xb_put_term (local.get $x))
  (select (local.get $x) (i32.const 0) (i32.ge_u (call $tdt (local.get $x)) (i32.const 3))))

;; BNODE(s): the row the strings belong to, and their nodes.
(global $ev_row (mut i32) (i32.const 0))
(global $bn_row (mut i32) (i32.const 0))
(global $bn_set (mut i32) (i32.const 0))
(global $bn_ids (mut i32) (i32.const 0))

;; Functions of evaluated arguments.
(func $fn (param $f i32) (param $argc i32) (param $a i32) (param $b i32) (param $c i32) (param $d i32) (result i32)
  (local $x i32) (local $y i32) (local $p i32) (local $n i32) (local $i i32) (local $k i32) (local $s f64) (local $l f64)
  (if (i32.eq (local.get $f) (i32.const F_STR)) (then
    (if (i32.gt_u (i32.sub (call $kind (local.get $a)) (i32.const 1)) (i32.const 2)) (then (return (i32.const 0))))
    (if (i32.eq (call $kind (local.get $a)) (i32.const 2)) (then (return (i32.const 0))))
    (if (call $is_plain (local.get $a)) (then (return (local.get $a))))
    ;; the simple literal, kept in the term's record (+28; collect clears it)
    (if (i32.eqz (local.tee $x (i32.load offset=28 (call $rec (local.get $a))))) (then
      (i32.store offset=28 (call $rec (local.get $a)) (local.tee $x (call $simple (call $tval (local.get $a)) (call $tlen (local.get $a)))))))
    (return (local.get $x))))
  (if (i32.or (i32.eq (local.get $f) (i32.const F_IRI)) (i32.eq (local.get $f) (i32.const F_URI))) (then
    (if (i32.eq (call $kind (local.get $a)) (i32.const 1)) (then (return (local.get $a))))
    (if (i32.eqz (call $is_plain (local.get $a))) (then (return (i32.const 0))))
    (global.set $base_p (i32.load offset=108 (global.get $q)))
    (global.set $base_n (i32.load offset=112 (global.get $q)))
    (return (call $iri_ref (call $tval (local.get $a)) (call $tlen (local.get $a))))))
  (if (i32.le_u (i32.sub (local.get $f) (i32.const F_EQ)) (i32.const 1)) (then
    (local.set $x (call $rdf_eq (local.get $a) (local.get $b)))
    (if (i32.lt_s (local.get $x) (i32.const 0)) (then (return (i32.const 0))))
    (return (call $bool (i32.xor (local.get $x) (i32.eq (local.get $f) (i32.const F_NE)))))))
  (if (i32.le_u (i32.sub (local.get $f) (i32.const F_LT)) (i32.const 3)) (then
    (local.set $x (call $rel_cmp (local.get $a) (local.get $b)))
    (if (i32.eq (local.get $x) (i32.const 2)) (then (return (i32.const 0))))
    (if (i32.eq (local.get $f) (i32.const F_LT)) (then (return (call $bool (i32.lt_s (local.get $x) (i32.const 0))))))
    (if (i32.eq (local.get $f) (i32.const F_GT)) (then (return (call $bool (i32.gt_s (local.get $x) (i32.const 0))))))
    (if (i32.eq (local.get $f) (i32.const F_LE)) (then (return (call $bool (i32.le_s (local.get $x) (i32.const 0))))))
    (return (call $bool (i32.ge_s (local.get $x) (i32.const 0))))))
  ;; numbers, else dates, times and durations
  (if (i32.le_u (i32.sub (local.get $f) (i32.const F_ADD)) (i32.const 3)) (then
    (if (local.tee $x (call $arith (local.get $f) (local.get $a) (local.get $b))) (then (return (local.get $x))))
    (return (call $temporal_arith (local.get $f) (local.get $a) (local.get $b)))))
  (if (i32.eq (local.get $f) (i32.const F_NEG)) (then
    (if (local.tee $x (call $arith (i32.const F_MUL) (call $int_term (i64.const -1)) (local.get $a))) (then (return (local.get $x))))
    (return (call $temporal_neg (local.get $a)))))
  (if (i32.eq (local.get $f) (i32.const F_PLUS)) (then (return (select (local.get $a) (i32.const 0) (call $num (local.get $a))))))
  (if (i32.eq (local.get $f) (i32.const F_NOT)) (then
    (local.set $x (call $ebv (local.get $a)))
    (if (i32.lt_s (local.get $x) (i32.const 0)) (then (return (i32.const 0))))
    (return (call $bool (i32.eqz (local.get $x))))))
  (if (i32.eq (local.get $f) (i32.const F_LANG)) (then
    (if (i32.ne (call $kind (local.get $a)) (i32.const 3)) (then (return (i32.const 0))))
    (return (call $simple (call $tlang (local.get $a)) (call $tlang_len (local.get $a))))))
  (if (i32.eq (local.get $f) (i32.const F_DATATYPE)) (then (return (call $tdt (local.get $a)))))
  (if (i32.eq (local.get $f) (i32.const F_ISIRI)) (then (return (call $bool (i32.eq (call $kind (local.get $a)) (i32.const 1))))))
  (if (i32.eq (local.get $f) (i32.const F_ISURI)) (then (return (call $bool (i32.eq (call $kind (local.get $a)) (i32.const 1))))))
  (if (i32.eq (local.get $f) (i32.const F_ISBLANK)) (then (return (call $bool (i32.eq (call $kind (local.get $a)) (i32.const 2))))))
  (if (i32.eq (local.get $f) (i32.const F_ISLITERAL)) (then (return (call $bool (i32.eq (call $kind (local.get $a)) (i32.const 3))))))
  (if (i32.eq (local.get $f) (i32.const F_ISTRIPLE)) (then (return (call $bool (i32.eq (call $kind (local.get $a)) (i32.const 4))))))
  (if (i32.eq (local.get $f) (i32.const F_ISNUMERIC)) (then (return (call $bool (i32.ne (call $num (local.get $a)) (i32.const 0))))))
  (if (i32.eq (local.get $f) (i32.const F_SAMETERM)) (then (return (call $bool (i32.eq (local.get $a) (local.get $b))))))
  (if (i32.eq (local.get $f) (i32.const F_STRLEN)) (then
    (if (i32.eqz (call $is_str (local.get $a))) (then (return (i32.const 0))))
    (return (call $int_term (i64.extend_i32_u (call $cp_count (call $tval (local.get $a)) (call $tlen (local.get $a))))))))
  (if (i32.eq (local.get $f) (i32.const F_SUBSTR)) (then
    (if (i32.eqz (call $is_str (local.get $a))) (then (return (i32.const 0))))
    (if (i32.eqz (call $num (local.get $b))) (then (return (i32.const 0))))
    (local.set $s (f64.floor (f64.add (global.get $nf) (f64.const 0.5))))
    (local.set $l (f64.const inf))
    (if (i32.eq (local.get $argc) (i32.const 3)) (then
      (if (i32.eqz (call $num (local.get $c))) (then (return (i32.const 0))))
      (local.set $l (f64.floor (f64.add (global.get $nf) (f64.const 0.5))))))
    ;; positions p (1-based) with s <= p < s + l
    (local.set $l (f64.add (local.get $s) (local.get $l)))
    (if (f64.lt (local.get $s) (f64.const 1)) (then (local.set $s (f64.const 1))))
    (local.set $n (call $cp_count (call $tval (local.get $a)) (call $tlen (local.get $a))))
    (if (f64.gt (local.get $l) (f64.convert_i32_u (i32.add (local.get $n) (i32.const 1)))) (then (local.set $l (f64.convert_i32_u (i32.add (local.get $n) (i32.const 1))))))
    (if (i32.or (f64.ne (local.get $s) (local.get $s)) (f64.ne (local.get $l) (local.get $l))) (then
      (return (call $str_like (i32.const 0) (i32.const 0) (local.get $a)))))
    (if (f64.ge (local.get $s) (local.get $l)) (then (return (call $str_like (i32.const 0) (i32.const 0) (local.get $a)))))
    (local.set $x (call $cp_offset (call $tval (local.get $a)) (call $tlen (local.get $a)) (i32.trunc_sat_f64_u (f64.sub (local.get $s) (f64.const 1)))))
    (local.set $y (call $cp_offset (call $tval (local.get $a)) (call $tlen (local.get $a)) (i32.trunc_sat_f64_u (f64.sub (local.get $l) (f64.const 1)))))
    (return (call $str_like (i32.add (call $tval (local.get $a)) (local.get $x)) (i32.sub (local.get $y) (local.get $x)) (local.get $a)))))
  (if (i32.or (i32.eq (local.get $f) (i32.const F_UCASE)) (i32.eq (local.get $f) (i32.const F_LCASE))) (then
    (if (i32.eqz (call $is_str (local.get $a))) (then (return (i32.const 0))))
    (call $xb_reset)
    (local.set $p (call $tval (local.get $a)))
    (local.set $n (i32.add (local.get $p) (call $tlen (local.get $a))))
    (loop $cps
      (if (i32.lt_u (local.get $p) (local.get $n)) (then
        (call $xb_cp (call $case (call $decode (local.get $p)) (i32.eq (local.get $f) (i32.const F_UCASE))))
        (local.set $p (i32.add (local.get $p) (global.get $cplen)))
        (br $cps))))
    (return (call $xb_string (local.get $a)))))
  (if (i32.eq (local.get $f) (i32.const F_CONTAINS)) (then
    (if (i32.eqz (call $compat (local.get $a) (local.get $b))) (then (return (i32.const 0))))
    (return (call $bool (call $find (call $tval (local.get $a)) (call $tlen (local.get $a)) (call $tval (local.get $b)) (call $tlen (local.get $b)))))))
  (if (i32.eq (local.get $f) (i32.const F_STRSTARTS)) (then
    (if (i32.eqz (call $compat (local.get $a) (local.get $b))) (then (return (i32.const 0))))
    (return (call $bool (i32.and (i32.le_u (call $tlen (local.get $b)) (call $tlen (local.get $a)))
                                 (call $memeq (call $tval (local.get $a)) (call $tval (local.get $b)) (call $tlen (local.get $b))))))))
  (if (i32.eq (local.get $f) (i32.const F_STRENDS)) (then
    (if (i32.eqz (call $compat (local.get $a) (local.get $b))) (then (return (i32.const 0))))
    (return (call $bool (i32.and (i32.le_u (call $tlen (local.get $b)) (call $tlen (local.get $a)))
                                 (call $memeq (i32.sub (i32.add (call $tval (local.get $a)) (call $tlen (local.get $a))) (call $tlen (local.get $b)))
                                              (call $tval (local.get $b)) (call $tlen (local.get $b))))))))
  (if (i32.or (i32.eq (local.get $f) (i32.const F_STRBEFORE)) (i32.eq (local.get $f) (i32.const F_STRAFTER))) (then
    (if (i32.eqz (call $compat (local.get $a) (local.get $b))) (then (return (i32.const 0))))
    (if (i32.eqz (call $find (call $tval (local.get $a)) (call $tlen (local.get $a)) (call $tval (local.get $b)) (call $tlen (local.get $b))))
      (then (return (call $simple (i32.const 0) (i32.const 0)))))
    (if (i32.eq (local.get $f) (i32.const F_STRBEFORE))
      (then (return (call $str_like (call $tval (local.get $a)) (global.get $found) (local.get $a)))))
    (local.set $x (i32.add (global.get $found) (call $tlen (local.get $b))))
    (return (call $str_like (i32.add (call $tval (local.get $a)) (local.get $x)) (i32.sub (call $tlen (local.get $a)) (local.get $x)) (local.get $a)))))
  (if (i32.eq (local.get $f) (i32.const F_ENCODE)) (then
    (if (i32.eqz (call $is_str (local.get $a))) (then (return (i32.const 0))))
    (call $xb_reset)
    (local.set $p (call $tval (local.get $a)))
    (local.set $n (i32.add (local.get $p) (call $tlen (local.get $a))))
    (loop $bytes
      (if (i32.lt_u (local.get $p) (local.get $n)) (then
        (local.set $x (i32.load8_u (local.get $p)))
        (if (i32.or (i32.or (call $is_alpha (local.get $x)) (call $is_digit (local.get $x))) (call $one_of (local.get $x) (@str "-_.~")))
          (then (call $xb_byte (local.get $x)))
          (else
            (call $xb_byte (i32.const '%'))
            (call $xb_byte (call $hex_digit (i32.shr_u (local.get $x) (i32.const 4))))
            (call $xb_byte (call $hex_digit (i32.and (local.get $x) (i32.const 15))))))
        (local.set $p (i32.add (local.get $p) (i32.const 1)))
        (br $bytes))))
    (return (call $xb_string (i32.const 0)))))
  (if (i32.eq (local.get $f) (i32.const F_REGEX)) (then
    (if (i32.eqz (i32.and (call $is_str (local.get $a)) (call $is_plain (local.get $b)))) (then (return (i32.const 0))))
    (if (i32.and (i32.eq (local.get $argc) (i32.const 3)) (i32.eqz (call $is_plain (local.get $c)))) (then (return (i32.const 0))))
    (local.set $x (call $re_compile (local.get $b) (select (local.get $c) (i32.const 0) (i32.eq (local.get $argc) (i32.const 3)))))
    (if (i32.eqz (local.get $x)) (then (return (i32.const 0))))
    (return (call $bool (call $re_search (local.get $x) (call $tval (local.get $a)) (call $tlen (local.get $a)) (i32.const 0))))))
  (if (i32.eq (local.get $f) (i32.const F_REPLACE)) (then
    (if (i32.eqz (i32.and (call $is_str (local.get $a)) (i32.and (call $is_plain (local.get $b)) (call $is_plain (local.get $c))))) (then (return (i32.const 0))))
    (if (i32.and (i32.eq (local.get $argc) (i32.const 4)) (i32.eqz (call $is_plain (local.get $d)))) (then (return (i32.const 0))))
    (local.set $x (call $re_compile (local.get $b) (select (local.get $d) (i32.const 0) (i32.eq (local.get $argc) (i32.const 4)))))
    (if (i32.eqz (local.get $x)) (then (return (i32.const 0))))
    (return (call $re_replace (local.get $x) (local.get $a) (local.get $c)))))
  (if (i32.eq (local.get $f) (i32.const F_LANGMATCHES)) (then
    (if (i32.eqz (i32.and (call $is_plain (local.get $a)) (call $is_plain (local.get $b)))) (then (return (i32.const 0))))
    (return (call $bool (call $langmatches (local.get $a) (local.get $b))))))
  (if (i32.eq (local.get $f) (i32.const F_STRLANG)) (then
    (if (i32.eqz (i32.and (call $is_plain (local.get $a)) (call $is_plain (local.get $b)))) (then (return (i32.const 0))))
    (if (i32.eqz (call $tlen (local.get $b))) (then (return (i32.const 0))))
    (call $xb_reset)
    (call $xb_put_term (local.get $b))
    (call $lower (call $xb_ptr) (call $xb_n))
    (return (call $intern (i32.const 3) (call $tval (local.get $a)) (call $tlen (local.get $a)) (i32.const 3) (call $xb_ptr) (call $xb_n) (i32.const 1)))))
  (if (i32.eq (local.get $f) (i32.const F_STRLANGDIR)) (then
    (if (i32.eqz (i32.and (call $is_plain (local.get $a)) (i32.and (call $is_plain (local.get $b)) (call $is_plain (local.get $c))))) (then (return (i32.const 0))))
    (if (i32.eqz (call $tlen (local.get $b))) (then (return (i32.const 0))))
    (local.set $x (i32.const 0))
    (if (call $same (call $tval (local.get $c)) (call $tlen (local.get $c)) (@str "ltr") (i32.const 0)) (then (local.set $x (i32.const 1))))
    (if (call $same (call $tval (local.get $c)) (call $tlen (local.get $c)) (@str "rtl") (i32.const 0)) (then (local.set $x (i32.const 2))))
    (if (i32.eqz (local.get $x)) (then (return (i32.const 0))))
    (call $xb_reset)
    (call $xb_put_term (local.get $b))
    (call $lower (call $xb_ptr) (call $xb_n))
    (return (call $intern (i32.or (i32.const 3) (i32.shl (local.get $x) (i32.const 8))) (call $tval (local.get $a)) (call $tlen (local.get $a)) (i32.const 4) (call $xb_ptr) (call $xb_n) (i32.const 1)))))
  (if (i32.eq (local.get $f) (i32.const F_STRDT)) (then
    (if (i32.eqz (i32.and (call $is_plain (local.get $a)) (i32.eq (call $kind (local.get $b)) (i32.const 1)))) (then (return (i32.const 0))))
    (if (i32.le_u (i32.sub (local.get $b) (i32.const 3)) (i32.const 1)) (then (return (i32.const 0))))
    (return (call $typed (call $tval (local.get $a)) (call $tlen (local.get $a)) (local.get $b)))))
  (if (i32.eq (local.get $f) (i32.const F_LANGDIR)) (then
    (if (i32.ne (call $kind (local.get $a)) (i32.const 3)) (then (return (i32.const 0))))
    (local.set $x (call $tdir (local.get $a)))
    (if (i32.eq (local.get $x) (i32.const 1)) (then (return (call $simple (@str "ltr")))))
    (if (i32.eq (local.get $x) (i32.const 2)) (then (return (call $simple (@str "rtl")))))
    (return (call $simple (i32.const 0) (i32.const 0)))))
  (if (i32.eq (local.get $f) (i32.const F_HASLANG)) (then
    (if (i32.ne (call $kind (local.get $a)) (i32.const 3)) (then (return (call $bool (i32.const 0)))))
    (return (call $bool (i32.ne (call $tlang_len (local.get $a)) (i32.const 0))))))
  (if (i32.eq (local.get $f) (i32.const F_HASLANGDIR)) (then
    (if (i32.ne (call $kind (local.get $a)) (i32.const 3)) (then (return (call $bool (i32.const 0)))))
    (return (call $bool (i32.ne (call $tdir (local.get $a)) (i32.const 0))))))
  (if (i32.eq (local.get $f) (i32.const F_TRIPLE)) (then
    (if (i32.or (i32.gt_u (i32.sub (call $kind (local.get $a)) (i32.const 1)) (i32.const 1)) (i32.ne (call $kind (local.get $b)) (i32.const 1)))
      (then (return (i32.const 0))))
    (return (call $intern_triple (local.get $a) (local.get $b) (local.get $c) (i32.const 1)))))
  (if (i32.le_u (i32.sub (local.get $f) (i32.const FN_SUBJECT)) (i32.const 2)) (then
    (if (i32.ne (call $kind (local.get $a)) (i32.const 4)) (then (return (i32.const 0))))
    (return (i32.load (i32.add (call $rec (local.get $a)) (i32.shl (i32.add (i32.sub (local.get $f) (i32.const FN_SUBJECT)) (i32.const 2)) (i32.const 2)))))))
  (if (i32.le_u (i32.sub (local.get $f) (i32.const F_ABS)) (i32.const 3)) (then (return (call $round (local.get $f) (local.get $a)))))
  (if (i32.eq (local.get $f) (i32.const F_BNODE)) (then
    (if (i32.eqz (local.get $argc)) (then (return (call $fresh_bnode))))
    (if (i32.eqz (call $is_plain (local.get $a))) (then (return (i32.const 0))))
    ;; BNODE(s): one node per string within a solution
    (if (i32.ne (global.get $bn_row) (global.get $ev_row)) (then
      (if (global.get $bn_set) (then (call $set_free (global.get $bn_set)) (call $v_free (global.get $bn_ids))))
      (global.set $bn_set (call $set_make (i32.const 1)))
      (global.set $bn_ids (call $v_new))
      (global.set $bn_row (global.get $ev_row))))
    (local.set $p (call $rs_alloc (i32.const 4)))
    (i32.store (local.get $p) (local.get $a))
    (local.set $x (call $set_add (global.get $bn_set) (local.get $p)))
    (call $row_put (local.get $p))
    (if (global.get $set_new) (then (call $v_push1 (global.get $bn_ids) (call $fresh_bnode))))
    (return (call $v_at (global.get $bn_ids) (local.get $x)))))
  (if (i32.eq (local.get $f) (i32.const F_RAND)) (then (return (call $dbl_term (call $random) (call $known (i32.const K_DOUBLE))))))
  (if (i32.eq (local.get $f) (i32.const F_NOW)) (then (return (call $now_term))))
  (if (i32.or (i32.eq (local.get $f) (i32.const F_UUID)) (i32.eq (local.get $f) (i32.const F_STRUUID))) (then
    (call $xb_reset)
    (if (i32.eq (local.get $f) (i32.const F_UUID)) (then (call $xb_put (@str "urn:uuid:"))))
    (local.set $i (i32.const 0))
    (loop $digits
      (if (i32.lt_u (local.get $i) (i32.const 32)) (then
        (if (i32.or (i32.or (i32.eq (local.get $i) (i32.const 8)) (i32.eq (local.get $i) (i32.const 12)))
                    (i32.or (i32.eq (local.get $i) (i32.const 16)) (i32.eq (local.get $i) (i32.const 20))))
          (then (call $xb_byte (i32.const '-'))))
        (local.set $x (i32.trunc_sat_f64_u (f64.mul (call $random) (f64.const 16))))
        (if (i32.eq (local.get $i) (i32.const 12)) (then (local.set $x (i32.const 4))))
        (if (i32.eq (local.get $i) (i32.const 16)) (then (local.set $x (i32.or (i32.const 8) (i32.and (local.get $x) (i32.const 3))))))
        (call $xb_byte (call $hex_digit (local.get $x)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $digits))))
    (if (i32.eq (local.get $f) (i32.const F_UUID))
      (then (return (call $iri_const (call $xb_ptr) (call $xb_n)))))
    (return (call $xb_string (i32.const 0)))))
  (if (i32.le_u (i32.sub (local.get $f) (i32.const F_MD5)) (i32.const 4)) (then
    (if (i32.eqz (call $is_plain (local.get $a))) (then (return (i32.const 0))))
    (return (call $digest (local.get $f) (local.get $a)))))
  (if (i32.le_u (i32.sub (local.get $f) (i32.const F_YEAR)) (i32.const 7)) (then (return (call $date_part (local.get $f) (local.get $a)))))
  (i32.const 0))

(func $hex_digit (param $x i32) (result i32)
  (i32.add (local.get $x) (select (i32.const 48) (i32.const 55) (i32.lt_u (local.get $x) (i32.const 10)))))

;; langMatches(tag, range): "*" matches any tag; otherwise a case-insensitive
;; prefix ending at a subtag boundary.
(func $langmatches (param $tag i32) (param $range i32) (result i32)
  (local $t i32) (local $tn i32) (local $r i32) (local $rn i32) (local $i i32)
  (local.set $t (call $tval (local.get $tag)))
  (local.set $tn (call $tlen (local.get $tag)))
  (local.set $r (call $tval (local.get $range)))
  (local.set $rn (call $tlen (local.get $range)))
  (if (call $same (local.get $r) (local.get $rn) (@str "*") (i32.const 0)) (then (return (i32.ne (local.get $tn) (i32.const 0)))))
  (if (i32.gt_u (local.get $rn) (local.get $tn)) (then (return (i32.const 0))))
  (if (i32.eqz (call $same (local.get $t) (local.get $rn) (local.get $r) (local.get $rn) (i32.const 1))) (then (return (i32.const 0))))
  (i32.or (i32.eq (local.get $rn) (local.get $tn)) (i32.eq (i32.load8_u (i32.add (local.get $t) (local.get $rn))) (i32.const '-'))))
