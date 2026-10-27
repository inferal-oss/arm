;; Typed expressions in compiled queries: strings and numbers in locals.
;;
;; An expression the interpreter would evaluate to a term (interning every
;; string and number it computes) is compiled to code that keeps its values in
;; locals of the module's function, with a kind known when compiling:
;;   ID   a term id (0: unbound or an error)
;;   STR  a simple literal's bytes: ptr, len (len < 0: an error), and its id
;;        when it is an existing term (else 0); bytes are the dictionary's own,
;;        or a CONCAT's in the engine's string buffer
;;   NUM  a number as num.wat keeps it: type t (0: an error; 1 integer,
;;        2 decimal, 3 float, 4 double), i64 ni, scale nsc, f64 nf, and its id
;;        when it is an existing term
;;   BOOL 0, 1, or 2 for an error
;; A term is made (interned) only at the end, when the value is bound or
;; output. What the code does not handle when it runs (a language-tagged
;; string, a decimal sum, a full buffer) leaves for a bail block, whose
;; callback (cq_value / cq_test) evaluates the whole expression in the engine;
;; expressions are pure, so that gives the same value. Subexpressions the
;; compiler does not type are callbacks themselves (an ID).

(@define TK_ID 1) (@define TK_STR 2) (@define TK_NUM 3) (@define TK_BOOL 4)

;; Blocks between the code being emitted and the bail block.
(global $txd (mut i32) (i32.const 0))
;; The module global holding the string buffer's use (-1: none yet).
(global $tx_used (mut i32) (i32.const -1))
;; The plan state where the expression being compiled is evaluated (0: unknown).
(global $tx_st (mut i32) (i32.const 0))

;; --- the engine side ------------------------------------------------------------------

(func $cx_put_num (param $t i32) (result i32)
  (i64.store offset=32 (global.get $bases) (global.get $ni))
  (f64.store offset=40 (global.get $bases) (global.get $nf))
  (i32.store offset=48 (global.get $bases) (global.get $nsc))
  (i32.store offset=52 (global.get $bases) (local.get $t))
  (local.get $t))

;; The string buffer: allocated once, kept.
(func $cx_sbuf
  (if (i32.eqz (i32.load offset=20 (global.get $bases))) (then
    (i32.store offset=20 (global.get $bases) (call $alloc (i32.const 65536)))
    (i32.store offset=24 (global.get $bases) (i32.const 65536)))))

(func (export "cq_str") (param $p i32) (param $n i32) (result i32) (call $simple (local.get $p) (local.get $n)))

(func (export "cq_iri") (param $p i32) (param $n i32) (result i32)
  (global.set $base_p (i32.load offset=108 (global.get $q)))
  (global.set $base_n (i32.load offset=112 (global.get $q)))
  (call $iri_ref (local.get $p) (local.get $n)))

;; Term id as a number: its type, the value in the record (0: not a number).
(func (export "cq_num") (param $id i32) (result i32)
  (global.set $nsc (i32.const 0))
  (global.set $ni (i64.const 0))
  (call $cx_put_num (call $num (local.get $id))))

;; A cast to xsd:integer (tc C_INTEGER) or xsd:double (C_DOUBLE) of term v, or
;; (v = 0) of the simple literal [p, p+n), as $cast computes it: the result's
;; type (1 or 4), its value in the record; 0 on an error.
(func (export "cq_tonum") (param $v i32) (param $p i32) (param $n i32) (param $tc i32) (result i32)
  (local $t i32) (local $b i32)
  (global.set $nsc (i32.const 0))
  (global.set $ni (i64.const 0))
  (if (local.get $v) (then
    (if (i32.ne (call $kind (local.get $v)) (i32.const 3)) (then (return (i32.const 0))))
    (local.set $p (call $tval (local.get $v)))
    (local.set $n (call $tlen (local.get $v)))
    (local.set $t (call $num (local.get $v)))
    (if (i32.and (i32.eqz (local.get $t)) (i32.eq (call $dtc (call $tdt (local.get $v))) (i32.const C_BOOLEAN))) (then
      (if (i32.lt_s (local.tee $b (call $bool_value (local.get $v))) (i32.const 0)) (then (return (i32.const 0))))
      (global.set $ni (i64.extend_i32_u (local.get $b)))
      (global.set $nf (f64.convert_i32_u (local.get $b)))
      (return (call $cx_put_num (select (i32.const 1) (i32.const 4) (i32.eq (local.get $tc) (i32.const C_INTEGER)))))))
    (if (i32.and (i32.eqz (local.get $t)) (i32.eqz (call $is_plain (local.get $v)))) (then (return (i32.const 0))))))
  (if (i32.eqz (local.get $t)) (then
    (global.set $nsc (i32.const 0))
    (global.set $ni (i64.const 0))
    (if (i32.eqz (call $parse_num (local.get $p) (local.get $n) (i32.sub (local.get $tc) (i32.const 1)))) (then (return (i32.const 0))))
    (local.set $t (i32.sub (local.get $tc) (i32.const 1)))))
  (if (i32.eq (local.get $tc) (i32.const C_INTEGER)) (then
    (if (i32.eq (local.get $t) (i32.const 2)) (then (global.set $ni (i64.div_s (global.get $ni) (call $i10 (global.get $nsc))))))
    (if (i32.ge_u (local.get $t) (i32.const 3)) (then
      (if (i32.or (f64.ne (global.get $nf) (global.get $nf)) (f64.gt (f64.abs (global.get $nf)) (f64.const 9e18))) (then (return (i32.const 0))))
      (global.set $ni (i64.trunc_f64_s (f64.trunc (global.get $nf))))))
    (global.set $nsc (i32.const 0))
    (global.set $nf (f64.convert_i64_s (global.get $ni)))
    (return (call $cx_put_num (i32.const 1)))))
  (call $cx_put_num (i32.const 4)))

;; The term of the record's number of type t (1 integer, 4 double; 5 a
;; boolean, ni its value).
(func (export "cq_term") (param $t i32) (result i32)
  (local $b i32)
  (if (i32.eq (local.get $t) (i32.const 1)) (then (return (call $int_term (i64.load offset=32 (global.get $bases))))))
  (if (i32.eq (local.get $t) (i32.const 4)) (then (return (call $dbl_term (f64.load offset=40 (global.get $bases)) (call $known (i32.const K_DOUBLE))))))
  (if (i32.eq (local.get $t) (i32.const 5)) (then (return (call $bool (i64.ne (i64.load offset=32 (global.get $bases)) (i64.const 0))))))
  (i32.const 0))

(func (export "cq_cplen") (param $p i32) (param $n i32) (result i32) (call $cp_count (local.get $p) (local.get $n)))
(func (export "cq_cpoff") (param $p i32) (param $n i32) (param $k i32) (result i32) (call $cp_offset (local.get $p) (local.get $n) (local.get $k)))

;; Strings a, b: mode 0 STRSTARTS, 1 STRENDS, 2 CONTAINS (0 or 1); 3 a <=> b (-1, 0, 1).
(func (export "cq_strfn") (param $mode i32) (param $pa i32) (param $na i32) (param $pb i32) (param $nb i32) (result i32)
  (if (i32.eq (local.get $mode) (i32.const 3)) (then (return (call $bytes_cmp (local.get $pa) (local.get $na) (local.get $pb) (local.get $nb)))))
  (if (i32.eq (local.get $mode) (i32.const 2)) (then (return (call $find (local.get $pa) (local.get $na) (local.get $pb) (local.get $nb)))))
  (if (i32.gt_u (local.get $nb) (local.get $na)) (then (return (i32.const 0))))
  (if (local.get $mode) (then (local.set $pa (i32.sub (i32.add (local.get $pa) (local.get $na)) (local.get $nb)))))
  (call $memeq (local.get $pa) (local.get $pb) (local.get $nb)))

;; Effective boolean value of a term: 0, 1, or 2 for an error.
(func (export "cq_ebv") (param $id i32) (result i32)
  (local $x i32)
  (local.set $x (call $ebv (local.get $id)))
  (select (i32.const 2) (local.get $x) (i32.lt_s (local.get $x) (i32.const 0))))

;; --- emitting ---------------------------------------------------------------------------
;; Imports a module calls for typed code (after cq_group, 18): see cx-header.ts.
(@define CI_STR 19) (@define CI_IRI 20) (@define CI_NUM 21) (@define CI_TONUM 22) (@define CI_TERM 23)
(@define CI_CPLEN 24) (@define CI_CPOFF 25) (@define CI_STRFN 26) (@define CI_EBV 27)

(func $op (param $f i32) (param $x i32) (call $bput (local.get $f) (local.get $x)))
(func $call_i (param $f i32) (param $i i32) (call $bput (local.get $f) (i32.const 0x10)) (call $uleb (local.get $f) (local.get $i)))
(func $lget (param $f i32) (param $l i32) (call $bput (local.get $f) (i32.const 0x20)) (call $uleb (local.get $f) (local.get $l)))
(func $lset (param $f i32) (param $l i32) (call $bput (local.get $f) (i32.const 0x21)) (call $uleb (local.get $f) (local.get $l)))
(func $iconst (param $f i32) (param $v i32) (call $bput (local.get $f) (i32.const 0x41)) (call $sleb (local.get $f) (local.get $v)))
;; A load or store: op, alignment, offset.
(func $memop (param $f i32) (param $x i32) (param $align i32) (param $off i32)
  (call $bput (local.get $f) (local.get $x)) (call $bput (local.get $f) (local.get $align)) (call $uleb (local.get $f) (local.get $off)))
(func $f64const (param $f i32) (param $x f64)
  (local $v i64) (local $i i32)
  (call $bput (local.get $f) (i32.const 0x44))
  (local.set $v (i64.reinterpret_f64 (local.get $x)))
  (loop $bytes
    (call $bput (local.get $f) (i32.wrap_i64 (i64.and (local.get $v) (i64.const 0xff))))
    (local.set $v (i64.shr_u (local.get $v) (i64.const 8)))
    (br_if $bytes (i32.ne (local.tee $i (i32.add (local.get $i) (i32.const 1))) (i32.const 8)))))
(func $i64const (param $f i32) (param $x i64)
  (local $v i64) (local $b i32)
  (call $bput (local.get $f) (i32.const 0x42))
  (local.set $v (local.get $x))
  (loop $more
    (local.set $b (i32.wrap_i64 (i64.and (local.get $v) (i64.const 127))))
    (local.set $v (i64.shr_s (local.get $v) (i64.const 7)))
    (if (i32.or (i32.and (i64.eqz (local.get $v)) (i32.eqz (i32.and (local.get $b) (i32.const 64))))
                (i32.and (i64.eq (local.get $v) (i64.const -1)) (i32.ne (i32.and (local.get $b) (i32.const 64)) (i32.const 0))))
      (then (call $bput (local.get $f) (local.get $b)))
      (else (call $bput (local.get $f) (i32.or (local.get $b) (i32.const 128))) (br $more)))))

;; Blocks in typed code: opened ones count towards the bail block's depth.
(func $topen (param $f i32) (param $p i32) (param $n i32)
  (call $em0 (local.get $f) (local.get $p) (local.get $n))
  (global.set $txd (i32.add (global.get $txd) (i32.const 1))))
(func $tend (param $f i32)
  (call $em0 (local.get $f) (@str "}"))
  (global.set $txd (i32.sub (global.get $txd) (i32.const 1))))
;; Leave for the bail block (with a condition on the stack: when it holds).
(func $bail (param $f i32) (call $em1 (local.get $f) (@str "ba") (global.get $txd)))
(func $bail_if (param $f i32) (call $em1 (local.get $f) (@str "fa") (global.get $txd)))

;; A value's locals (+0 kind; ID: +4 id; STR: +4 ptr, +8 len, +12 id; NUM: +4 t,
;; +8 ni, +12 nsc, +16 nf, +20 id; BOOL: +4 v).
(func $tv (param $f i32) (param $k i32) (result i32)
  (local $v i32)
  (i32.store (local.tee $v (call $anew (i32.const 24))) (local.get $k))
  (i32.store offset=4 (local.get $v) (call $local (local.get $f)))
  (if (i32.eq (local.get $k) (i32.const TK_STR)) (then
    (i32.store offset=8 (local.get $v) (call $local (local.get $f)))
    (i32.store offset=12 (local.get $v) (call $local (local.get $f)))))
  (if (i32.eq (local.get $k) (i32.const TK_NUM)) (then
    (i32.store offset=8 (local.get $v) (call $local_t (local.get $f) (i32.const 0x7e)))
    (i32.store offset=12 (local.get $v) (call $local (local.get $f)))
    (i32.store offset=16 (local.get $v) (call $local_t (local.get $f) (i32.const 0x7c)))
    (i32.store offset=20 (local.get $v) (call $local (local.get $f)))))
  (local.get $v))
(func $tva (param $v i32) (result i32) (i32.load offset=4 (local.get $v)))
(func $tvb (param $v i32) (result i32) (i32.load offset=8 (local.get $v)))
(func $tvc (param $v i32) (result i32) (i32.load offset=12 (local.get $v)))
(func $tvd (param $v i32) (result i32) (i32.load offset=16 (local.get $v)))
(func $tve (param $v i32) (result i32) (i32.load offset=20 (local.get $v)))

;; Push whether v is an error (or unbound).
(func $tv_err (param $f i32) (param $v i32)
  (local $k i32)
  (local.set $k (i32.load (local.get $v)))
  (if (i32.eq (local.get $k) (i32.const TK_STR)) (then (call $em1 (local.get $f) (@str "gak0H") (call $tvb (local.get $v))) (return)))
  (if (i32.eq (local.get $k) (i32.const TK_BOOL)) (then (call $em1 (local.get $f) (@str "gak2F") (call $tva (local.get $v))) (return)))
  ;; ID: id 0; NUM: t 0
  (call $em1 (local.get $f) (@str "gaE") (call $tva (local.get $v))))

;; Set v to an error.
(func $tv_set_err (param $f i32) (param $v i32)
  (local $k i32)
  (local.set $k (i32.load (local.get $v)))
  (if (i32.eq (local.get $k) (i32.const TK_STR)) (then
    (call $em2 (local.get $f) (@str "kavb") (i32.const -1) (call $tvb (local.get $v)))
    (call $em1 (local.get $f) (@str "k0va") (call $tvc (local.get $v))) (return)))
  (if (i32.eq (local.get $k) (i32.const TK_BOOL)) (then (call $em1 (local.get $f) (@str "k2va") (call $tva (local.get $v))) (return)))
  (call $em1 (local.get $f) (@str "k0va") (call $tva (local.get $v))))

;; Copy src into dst (the same kind).
(func $tv_copy (param $f i32) (param $dst i32) (param $src i32)
  (local $i i32) (local $n i32)
  (local.set $n (select (i32.const 1) (select (i32.const 3) (i32.const 5) (i32.eq (i32.load (local.get $src)) (i32.const TK_STR)))
    (i32.or (i32.eq (i32.load (local.get $src)) (i32.const TK_ID)) (i32.eq (i32.load (local.get $src)) (i32.const TK_BOOL)))))
  (loop $locals
    (local.set $i (i32.add (local.get $i) (i32.const 4)))
    (call $lget (local.get $f) (i32.load (i32.add (local.get $src) (local.get $i))))
    (call $lset (local.get $f) (i32.load (i32.add (local.get $dst) (local.get $i))))
    (br_if $locals (i32.lt_u (local.get $i) (i32.shl (local.get $n) (i32.const 2))))))

;; The number in the engine's record (after cq_num, cq_tonum) into NUM v's locals.
(func $ld_num (param $f i32) (param $v i32)
  (call $iconst (local.get $f) (global.get $bases)) (call $memop (local.get $f) (i32.const 0x29) (i32.const 3) (i32.const 32)) (call $lset (local.get $f) (call $tvb (local.get $v)))
  (call $iconst (local.get $f) (global.get $bases)) (call $memop (local.get $f) (i32.const 0x2b) (i32.const 3) (i32.const 40)) (call $lset (local.get $f) (call $tvd (local.get $v)))
  (call $iconst (local.get $f) (global.get $bases)) (call $memop (local.get $f) (i32.const 0x28) (i32.const 2) (i32.const 48)) (call $lset (local.get $f) (call $tvc (local.get $v))))

;; NUM v's ni and nf into the record.
(func $st_num (param $f i32) (param $v i32)
  (call $iconst (local.get $f) (global.get $bases)) (call $lget (local.get $f) (call $tvb (local.get $v))) (call $memop (local.get $f) (i32.const 0x37) (i32.const 3) (i32.const 32))
  (call $iconst (local.get $f) (global.get $bases)) (call $lget (local.get $f) (call $tvd (local.get $v))) (call $memop (local.get $f) (i32.const 0x39) (i32.const 3) (i32.const 40)))

;; The term record of the id in local l: push its address (the store's $terms + 32 id).
(func $rec_of (param $f i32) (param $l i32)
  (call $em2 (local.get $f) (@str "k0magbkPlj") (i32.add (global.get $bases) (i32.const 16)) (local.get $l)))

;; --- conversions ------------------------------------------------------------------------

;; Push v as a term id (0: an error), making the term when it is a new value.
(func $to_id (param $f i32) (param $v i32)
  (local $k i32)
  (local.set $k (i32.load (local.get $v)))
  (if (i32.eq (local.get $k) (i32.const TK_ID)) (then (call $lget (local.get $f) (call $tva (local.get $v))) (return)))
  (if (i32.eq (local.get $k) (i32.const TK_STR)) (then
    (call $em (local.get $f) (@str "gbk0H?k0:gcE?gagbCC:gc}}") (call $tva (local.get $v)) (call $tvb (local.get $v)) (call $tvc (local.get $v)) (i32.const 0))
    (return)))
  (if (i32.eq (local.get $k) (i32.const TK_NUM)) (then
    (call $em2 (local.get $f) (@str "gaE?k0:gbE?") (call $tva (local.get $v)) (call $tve (local.get $v)))
    (call $st_num (local.get $f) (local.get $v))
    (call $em2 (local.get $f) (@str "gaCG:gb}}") (call $tva (local.get $v)) (call $tve (local.get $v)))
    (return)))
  ;; BOOL: true or false, from the record's ni
  (call $em1 (local.get $f) (@str "gak2F?k0:") (call $tva (local.get $v)))
  (call $iconst (local.get $f) (global.get $bases))
  (call $lget (local.get $f) (call $tva (local.get $v)))
  (call $op (local.get $f) (i32.const 0xad))
  (call $memop (local.get $f) (i32.const 0x37) (i32.const 3) (i32.const 32))
  (call $em0 (local.get $f) (@str "k5CG}")))

;; An ID value of v.
(func $as_id (param $f i32) (param $v i32) (result i32)
  (local $r i32)
  (if (i32.eq (i32.load (local.get $v)) (i32.const TK_ID)) (then (return (local.get $v))))
  (call $to_id (local.get $f) (local.get $v))
  (call $lset (local.get $f) (call $tva (local.tee $r (call $tv (local.get $f) (i32.const TK_ID)))))
  (local.get $r))

;; v as a string: a simple literal's bytes. A language-tagged string leaves for
;; the bail block; another term is an error (strict: leaves too).
(func $to_str (param $f i32) (param $v i32) (param $strict i32) (result i32)
  (local $r i32) (local $x i32) (local $id i32)
  (if (i32.eq (i32.load (local.get $v)) (i32.const TK_STR)) (then (return (local.get $v))))
  (local.set $r (call $tv (local.get $f) (i32.const TK_STR)))
  (call $tv_set_err (local.get $f) (local.get $r))
  (if (i32.ne (i32.load (local.get $v)) (i32.const TK_ID)) (then
    (if (local.get $strict) (then (call $bail (local.get $f))))
    (return (local.get $r))))
  (local.set $id (call $tva (local.get $v)))
  (local.set $x (call $local (local.get $f)))
  (call $lget (local.get $f) (local.get $id))
  (call $topen (local.get $f) (@str "("))
  (call $rec_of (local.get $f) (local.get $id))
  (call $lset (local.get $f) (local.get $x))
  (call $em1 (local.get $f) (@str "ga") (local.get $x))
  (call $memop (local.get $f) (i32.const 0x2d) (i32.const 0) (i32.const 0))
  (call $em0 (local.get $f) (@str "k3F"))
  (call $topen (local.get $f) (@str "("))
  ;; a literal: xsd:string, else a language (bail), else not a string
  (call $em1 (local.get $f) (@str "gam@k2F") (local.get $x))
  (call $topen (local.get $f) (@str "("))
  (call $em (local.get $f) (@str "gam8vbgam<vc") (local.get $x) (call $tva (local.get $r)) (call $tvb (local.get $r)) (i32.const 0))
  (call $em2 (local.get $f) (@str "gavb") (local.get $id) (call $tvc (local.get $r)))
  (call $em0 (local.get $f) (@str ":"))
  (if (local.get $strict)
    (then (call $bail (local.get $f)))
    (else
      (call $em1 (local.get $f) (@str "gam@k5I") (local.get $x))
      (call $bail_if (local.get $f))))
  (call $tend (local.get $f))
  (if (local.get $strict) (then (call $em0 (local.get $f) (@str ":")) (call $bail (local.get $f))))
  (call $tend (local.get $f))
  (call $tend (local.get $f))
  (local.get $r))

;; v as a number (an error, t 0, when it is not one).
(func $to_num (param $f i32) (param $v i32) (result i32)
  (local $r i32) (local $k i32)
  (local.set $k (i32.load (local.get $v)))
  (if (i32.eq (local.get $k) (i32.const TK_NUM)) (then (return (local.get $v))))
  (local.set $r (call $tv (local.get $f) (i32.const TK_NUM)))
  (if (i32.ne (local.get $k) (i32.const TK_ID)) (then
    (call $em2 (local.get $f) (@str "k0vak0vb") (call $tva (local.get $r)) (call $tve (local.get $r)))
    (return (local.get $r))))
  (call $em2 (local.get $f) (@str "gaCEvb") (call $tva (local.get $v)) (call $tva (local.get $r)))
  (call $ld_num (local.get $f) (local.get $r))
  (call $em2 (local.get $f) (@str "gavb") (call $tva (local.get $v)) (call $tve (local.get $r)))
  (local.get $r))

;; Push v's effective boolean value: 0, 1, or 2 for an error.
(func $to_bool (param $f i32) (param $v i32)
  (local $k i32)
  (local.set $k (i32.load (local.get $v)))
  (if (i32.eq (local.get $k) (i32.const TK_BOOL)) (then (call $lget (local.get $f) (call $tva (local.get $v))) (return)))
  (if (i32.eq (local.get $k) (i32.const TK_ID)) (then (call $em1 (local.get $f) (@str "gaCK") (call $tva (local.get $v))) (return)))
  (if (i32.eq (local.get $k) (i32.const TK_STR)) (then (call $em1 (local.get $f) (@str "gak0H?k2:gak0G}") (call $tvb (local.get $v))) (return)))
  ;; NUM: not 0 and not NaN
  (call $em1 (local.get $f) (@str "gaE?k2:") (call $tva (local.get $v)))
  (call $lget (local.get $f) (call $tvd (local.get $v)))
  (call $f64const (local.get $f) (f64.const 0))
  (call $op (local.get $f) (i32.const 0x62))
  (call $lget (local.get $f) (call $tvd (local.get $v)))
  (call $lget (local.get $f) (call $tvd (local.get $v)))
  (call $op (local.get $f) (i32.const 0x61))
  (call $em0 (local.get $f) (@str "q}")))

;; A value of kind k from v (v's own kind, or an ID).
(func $as_kind (param $f i32) (param $v i32) (param $k i32) (result i32)
  (if (i32.eq (i32.load (local.get $v)) (local.get $k)) (then (return (local.get $v))))
  (call $as_id (local.get $f) (local.get $v)))

;; --- kinds ------------------------------------------------------------------------------

;; Debugging switches (compile.wat, $cx_off) for typed function groups:
;; 64 comparisons, 128 + and -, 256 casts, 512 STRSTARTS / STRENDS / CONTAINS,
;; 1024 STR / CONCAT / SUBSTR / STRLEN / IRI, 2048 AND / OR / NOT / IF / COALESCE.
(func $tx_off (param $fn i32) (result i32)
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_EQ)) (i32.const 5)) (then (return (call $cx_off (i32.const 64)))))
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_ADD)) (i32.const 1)) (then (return (call $cx_off (i32.const 128)))))
  (if (i32.eq (local.get $fn) (i32.const F_CAST)) (then (return (call $cx_off (i32.const 256)))))
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_CONTAINS)) (i32.const 2)) (then (return (call $cx_off (i32.const 512)))))
  (if (i32.or (i32.or (i32.eq (local.get $fn) (i32.const F_STR)) (i32.eq (local.get $fn) (i32.const F_CONCAT)))
              (i32.or (i32.le_u (i32.sub (local.get $fn) (i32.const F_SUBSTR)) (i32.const 1)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_IRI)) (i32.const 1))))
    (then (return (call $cx_off (i32.const 1024)))))
  (if (i32.or (i32.le_u (i32.sub (local.get $fn) (i32.const F_OR)) (i32.const 2)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_COALESCE)) (i32.const 1)))
    (then (return (call $cx_off (i32.const 2048)))))
  (i32.const 0))

;; A typed cast: to xsd:integer (C_INTEGER) or xsd:double (C_DOUBLE), else 0.
(func $tx_cast (param $e i32) (result i32)
  (local $dt i32)
  (if (i32.ne (i32.load offset=12 (local.get $e)) (i32.const 1)) (then (return (i32.const 0))))
  (local.set $dt (i32.load offset=16 (local.get $e)))
  (if (i32.eq (local.get $dt) (call $known (i32.const K_INTEGER))) (then (return (i32.const C_INTEGER))))
  (if (i32.eq (local.get $dt) (call $known (i32.const K_DOUBLE))) (then (return (i32.const C_DOUBLE))))
  (i32.const 0))

(func $tk_merge (param $a i32) (param $b i32) (result i32)
  (select (local.get $a) (i32.const TK_ID) (i32.eq (local.get $a) (local.get $b))))

;; The kind of value $tx gives for e.
(func $tk (param $e i32) (result i32)
  (local $k i32) (local $fn i32) (local $a i32) (local $r i32) (local $id i32)
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_CONST)) (then
    (local.set $id (i32.load offset=4 (local.get $e)))
    (if (i32.and (i32.eq (call $kind (local.get $id)) (i32.const 3)) (i32.eq (call $tdt (local.get $id)) (i32.const 2))) (then (return (i32.const TK_STR))))
    (if (call $num (local.get $id)) (then (return (i32.const TK_NUM))))
    (return (i32.const TK_ID))))
  (if (i32.eq (local.get $k) (i32.const E_EXISTS)) (then (return (i32.const TK_BOOL))))
  (if (i32.ne (local.get $k) (i32.const E_CALL)) (then (return (i32.const TK_ID))))
  (local.set $fn (i32.load offset=4 (local.get $e)))
  (local.set $a (i32.load offset=8 (local.get $e)))
  (if (call $tx_off (local.get $fn)) (then (return (select (i32.const TK_BOOL) (i32.const TK_ID) (i32.or (i32.le_u (i32.sub (local.get $fn) (i32.const F_OR)) (i32.const 8)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_CONTAINS)) (i32.const 2)))))))
  (if (i32.or (i32.or (i32.eq (local.get $fn) (i32.const F_STR)) (i32.eq (local.get $fn) (i32.const F_CONCAT))) (i32.eq (local.get $fn) (i32.const F_SUBSTR)))
    (then (return (i32.const TK_STR))))
  (if (i32.eq (local.get $fn) (i32.const F_STRLEN)) (then (return (i32.const TK_NUM))))
  (if (i32.and (i32.le_u (i32.sub (local.get $fn) (i32.const F_ADD)) (i32.const 1)) (i32.eq (i32.load offset=12 (local.get $e)) (i32.const 2)))
    (then (return (i32.const TK_NUM))))
  (if (i32.eq (local.get $fn) (i32.const F_CAST)) (then (return (select (i32.const TK_NUM) (i32.const TK_ID) (call $tx_cast (local.get $e))))))
  (if (i32.or (i32.or (i32.le_u (i32.sub (local.get $fn) (i32.const F_OR)) (i32.const 8)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_CONTAINS)) (i32.const 2)))
              (i32.or (i32.eq (local.get $fn) (i32.const F_BOUND)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_SAMETERM)) (i32.const 4))))
    (then (return (i32.const TK_BOOL))))
  (if (i32.eq (local.get $fn) (i32.const F_IF)) (then
    (return (call $tk_merge (call $tk (i32.load offset=28 (local.get $a))) (call $tk (i32.load offset=28 (i32.load offset=28 (local.get $a))))))))
  (if (i32.eq (local.get $fn) (i32.const F_COALESCE)) (then
    (local.set $r (call $tk (local.get $a)))
    (loop $args
      (if (local.tee $a (i32.load offset=28 (local.get $a))) (then
        (local.set $r (call $tk_merge (local.get $r) (call $tk (local.get $a))))
        (br $args))))
    (return (local.get $r))))
  (i32.const TK_ID))

;; Whether expression e is worth compiling typed: it computes strings or
;; numbers (or compares terms) somewhere, and gives the same value each time.
(func $tx_worth (param $e i32) (result i32)
  (local $fn i32) (local $a i32)
  ;; EXISTS: as a function of the module's own
  (if (i32.eq (i32.load (local.get $e)) (i32.const E_EXISTS)) (then (return (i32.eqz (call $cx_off (i32.const 4))))))
  (if (i32.ne (i32.load (local.get $e)) (i32.const E_CALL)) (then (return (i32.const 0))))
  (if (i32.or (call $cx_off (i32.const 2)) (call $impure (local.get $e))) (then (return (i32.const 0))))
  (local.set $fn (i32.load offset=4 (local.get $e)))
  (if (i32.or (i32.or (i32.eq (local.get $fn) (i32.const F_CONCAT)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_IRI)) (i32.const 1)))
              (i32.or (i32.le_u (i32.sub (local.get $fn) (i32.const F_SUBSTR)) (i32.const 1)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_CONTAINS)) (i32.const 2))))
    (then (return (i32.const 1))))
  (if (i32.or (i32.le_u (i32.sub (local.get $fn) (i32.const F_LT)) (i32.const 5)) (call $tx_cast (local.get $e))) (then (return (i32.const 1))))
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_EQ)) (i32.const 1)) (then
    ;; with a named constant or lang(x) = "", the id-level test of $cond
    (local.set $a (i32.load offset=8 (local.get $e)))
    (if (i32.and (i32.eqz (call $named (local.get $a))) (i32.eqz (call $named (i32.load offset=28 (local.get $a)))))
      (then (if (i32.eqz (i32.or (call $lang_of (local.get $a)) (call $lang_of (i32.load offset=28 (local.get $a))))) (then (return (i32.const 1))))))))
  (local.set $a (i32.load offset=8 (local.get $e)))
  (loop $args
    (if (local.get $a) (then
      (if (call $tx_worth (local.get $a)) (then (return (i32.const 1))))
      (local.set $a (i32.load offset=28 (local.get $a)))
      (br $args))))
  (i32.const 0))

;; --- expressions ------------------------------------------------------------------------

;; A callback for e: an ID (cq_value) or a BOOL (cq_test).
(func $tx_cb (param $f i32) (param $e i32) (param $k i32) (result i32)
  (local $r i32)
  (local.set $r (call $tv (local.get $f) (local.get $k)))
  (call $em2 (local.get $f) (select (call $p1 (@str "kaC7vb")) (call $p1 (@str "kaC6vb")) (i32.eq (local.get $k) (i32.const TK_ID))) (i32.const 6)
    (i32.or (local.get $e) (global.get $cx_keep)) (call $tva (local.get $r)))
  (local.get $r))

;; Emit e's value into locals: its descriptor.
(func $tx (param $f i32) (param $e i32) (result i32)
  (local $k i32) (local $fn i32) (local $a i32) (local $b i32) (local $r i32) (local $id i32) (local $x i32) (local $y i32)
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_VAR)) (then
    (local.set $r (call $tv (local.get $f) (i32.const TK_ID)))
    (call $em2 (local.get $f) (@str "k0mavb") (call $addr (i32.load offset=4 (local.get $e))) (call $tva (local.get $r)))
    (return (local.get $r))))
  (if (i32.eq (local.get $k) (i32.const E_CONST)) (then
    (local.set $id (i32.load offset=4 (local.get $e)))
    (local.set $x (call $tk (local.get $e)))
    (local.set $r (call $tv (local.get $f) (local.get $x)))
    (if (i32.eq (local.get $x) (i32.const TK_STR)) (then
      (call $em2 (local.get $f) (@str "kavb") (call $tval (local.get $id)) (call $tva (local.get $r)))
      (call $em2 (local.get $f) (@str "kavb") (call $tlen (local.get $id)) (call $tvb (local.get $r)))
      (call $em2 (local.get $f) (@str "kavb") (local.get $id) (call $tvc (local.get $r)))
      (return (local.get $r))))
    (if (i32.eq (local.get $x) (i32.const TK_NUM)) (then
      (local.set $x (call $num (local.get $id)))
      (call $em2 (local.get $f) (@str "kavb") (local.get $x) (call $tva (local.get $r)))
      (call $i64const (local.get $f) (global.get $ni)) (call $lset (local.get $f) (call $tvb (local.get $r)))
      (call $em2 (local.get $f) (@str "kavb") (global.get $nsc) (call $tvc (local.get $r)))
      (call $f64const (local.get $f) (global.get $nf)) (call $lset (local.get $f) (call $tvd (local.get $r)))
      (call $em2 (local.get $f) (@str "kavb") (local.get $id) (call $tve (local.get $r)))
      (return (local.get $r))))
    (call $em2 (local.get $f) (@str "kavb") (local.get $id) (call $tva (local.get $r)))
    (return (local.get $r))))
  (if (i32.eq (local.get $k) (i32.const E_EXISTS)) (then (return (call $tx_exists (local.get $f) (local.get $e)))))
  (if (i32.ne (local.get $k) (i32.const E_CALL)) (then (return (call $tx_cb (local.get $f) (local.get $e) (call $tk (local.get $e))))))
  (local.set $fn (i32.load offset=4 (local.get $e)))
  (local.set $a (i32.load offset=8 (local.get $e)))
  (if (local.get $a) (then (local.set $b (i32.load offset=28 (local.get $a)))))
  (if (call $tx_off (local.get $fn)) (then (return (call $tx_cb (local.get $f) (local.get $e) (call $tk (local.get $e))))))
  (if (i32.eq (local.get $fn) (i32.const F_STR)) (then (return (call $tx_str (local.get $f) (call $tx (local.get $f) (local.get $a))))))
  (if (i32.eq (local.get $fn) (i32.const F_CONCAT)) (then (return (call $tx_concat (local.get $f) (local.get $a)))))
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_IRI)) (i32.const 1)) (then (return (call $tx_iri (local.get $f) (call $tx (local.get $f) (local.get $a))))))
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_CONTAINS)) (i32.const 2)) (then
    (local.set $x (call $to_str (local.get $f) (call $tx (local.get $f) (local.get $a)) (i32.const 0)))
    (local.set $y (call $to_str (local.get $f) (call $tx (local.get $f) (local.get $b)) (i32.const 0)))
    (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
    (call $tv_err (local.get $f) (local.get $x))
    (call $tv_err (local.get $f) (local.get $y))
    (call $em0 (local.get $f) (@str "r?k2:"))
    ;; mode: 0 STRSTARTS, 1 STRENDS, 2 CONTAINS
    (call $iconst (local.get $f) (select (i32.const 2) (i32.sub (local.get $fn) (i32.const F_STRSTARTS)) (i32.eq (local.get $fn) (i32.const F_CONTAINS))))
    (call $em (local.get $f) (@str "gagbgcgdCJ") (call $tva (local.get $x)) (call $tvb (local.get $x)) (call $tva (local.get $y)) (call $tvb (local.get $y)))
    (call $em1 (local.get $f) (@str "}va") (call $tva (local.get $r)))
    (return (local.get $r))))
  (if (i32.eq (local.get $fn) (i32.const F_STRLEN)) (then
    (local.set $x (call $to_str (local.get $f) (call $tx (local.get $f) (local.get $a)) (i32.const 0)))
    (local.set $r (call $tv (local.get $f) (i32.const TK_NUM)))
    (call $em2 (local.get $f) (@str "k0vak0vb") (call $tve (local.get $r)) (call $tvc (local.get $r)))
    (call $em2 (local.get $f) (@str "gak0H(k0vb:") (call $tvb (local.get $x)) (call $tva (local.get $r)))
    (call $em2 (local.get $f) (@str "gagbCH") (call $tva (local.get $x)) (call $tvb (local.get $x)))
    (call $op (local.get $f) (i32.const 0xad))
    (call $lset (local.get $f) (call $tvb (local.get $r)))
    (call $lget (local.get $f) (call $tvb (local.get $r)))
    (call $op (local.get $f) (i32.const 0xb9))
    (call $lset (local.get $f) (call $tvd (local.get $r)))
    (call $em1 (local.get $f) (@str "k1va}") (call $tva (local.get $r)))
    (return (local.get $r))))
  (call $tx_rest (local.get $f) (local.get $e)))

;; STR(v): an IRI's or a literal's lexical form (an xsd:string keeps its term).
(func $tx_str (param $f i32) (param $v i32) (result i32)
  (local $r i32) (local $x i32) (local $id i32)
  (if (i32.eq (i32.load (local.get $v)) (i32.const TK_STR)) (then (return (local.get $v))))
  (local.set $id (call $tva (call $as_id (local.get $f) (local.get $v))))
  (local.set $r (call $tv (local.get $f) (i32.const TK_STR)))
  (call $tv_set_err (local.get $f) (local.get $r))
  (local.set $x (call $local (local.get $f)))
  (call $lget (local.get $f) (local.get $id))
  (call $topen (local.get $f) (@str "("))
  (call $rec_of (local.get $f) (local.get $id))
  (call $lset (local.get $f) (local.get $x))
  ;; kind 1 or 3
  (call $lget (local.get $f) (local.get $x))
  (call $memop (local.get $f) (i32.const 0x2d) (i32.const 0) (i32.const 0))
  (call $em0 (local.get $f) (@str "k2rk3F"))
  (call $topen (local.get $f) (@str "("))
  (call $em (local.get $f) (@str "gam8vbgam<vc") (local.get $x) (call $tva (local.get $r)) (call $tvb (local.get $r)) (i32.const 0))
  ;; the term itself when it is an xsd:string
  (call $em (local.get $f) (@str "gbk0gam@k2F~vc") (local.get $x) (local.get $id) (call $tvc (local.get $r)) (i32.const 0))
  (call $tend (local.get $f))
  (call $tend (local.get $f))
  (local.get $r))

;; CONCAT of simple strings, into the engine's string buffer.
(func $tx_concat (param $f i32) (param $a i32) (result i32)
  (local $parts i32) (local $n i32) (local $i i32) (local $r i32) (local $w i32) (local $tot i32) (local $p i32) (local $g i32)
  (local.set $parts (call $anew (i32.const 256)))
  (loop $args
    (if (i32.and (i32.ne (local.get $a) (i32.const 0)) (i32.lt_u (local.get $n) (i32.const 64))) (then
      (i32.store (i32.add (local.get $parts) (i32.shl (local.get $n) (i32.const 2)))
        (call $to_str (local.get $f) (call $tx (local.get $f) (local.get $a)) (i32.const 0)))
      (local.set $n (i32.add (local.get $n) (i32.const 1)))
      (local.set $a (i32.load offset=28 (local.get $a)))
      (br $args))))
  (if (i32.eq (local.get $n) (i32.const 1)) (then (return (i32.load (local.get $parts)))))
  (local.set $r (call $tv (local.get $f) (i32.const TK_STR)))
  (call $tv_set_err (local.get $f) (local.get $r))
  ;; any error: an error
  (call $em0 (local.get $f) (@str "k0"))
  (loop $errs
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (call $tv_err (local.get $f) (i32.load (i32.add (local.get $parts) (i32.shl (local.get $i) (i32.const 2)))))
      (call $em0 (local.get $f) (@str "r"))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $errs))))
  (call $em0 (local.get $f) (@str "E"))
  (call $topen (local.get $f) (@str "("))
  (local.set $tot (call $local (local.get $f)))
  (local.set $w (call $local (local.get $f)))
  (call $em1 (local.get $f) (@str "k0va") (local.get $tot))
  (local.set $i (i32.const 0))
  (loop $lens
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (call $em2 (local.get $f) (@str "gagbjvb") (call $tvb (i32.load (i32.add (local.get $parts) (i32.shl (local.get $i) (i32.const 2))))) (local.get $tot))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $lens))))
  ;; w = the buffer + its use; past its end: bail
  (local.set $g (call $tx_used_global))
  (call $em (local.get $f) (@str "Ragbjk0mcK") (local.get $g) (local.get $tot) (i32.add (global.get $bases) (i32.const 24)) (i32.const 0))
  (call $bail_if (local.get $f))
  (call $em (local.get $f) (@str "k0mbRaj") (local.get $g) (i32.add (global.get $bases) (i32.const 20)) (i32.const 0) (i32.const 0))
  (call $em2 (local.get $f) (@str "tavb") (local.get $w) (call $tva (local.get $r)))
  (call $em2 (local.get $f) (@str "Ragbj") (local.get $g) (local.get $tot))
  (call $em1 (local.get $f) (@str "Wa") (local.get $g))
  (local.set $i (i32.const 0))
  (loop $copies
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $p (i32.load (i32.add (local.get $parts) (i32.shl (local.get $i) (i32.const 2)))))
      ;; memory.copy(w, ptr, len); w += len
      (call $em (local.get $f) (@str "gagbgc") (local.get $w) (call $tva (local.get $p)) (call $tvb (local.get $p)) (i32.const 0))
      (call $op (local.get $f) (i32.const 0xfc)) (call $op (local.get $f) (i32.const 10)) (call $op (local.get $f) (i32.const 0)) (call $op (local.get $f) (i32.const 0))
      (call $em2 (local.get $f) (@str "gagbjva") (local.get $w) (call $tvb (local.get $p)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $copies))))
  (call $em2 (local.get $f) (@str "gavb") (local.get $tot) (call $tvb (local.get $r)))
  (call $tend (local.get $f))
  (local.get $r))

;; The module global holding the string buffer's use.
(func $tx_used_global (result i32)
  (if (i32.lt_s (global.get $tx_used) (i32.const 0)) (then
    (global.set $tx_used (global.get $cx_ng))
    (global.set $cx_ng (i32.add (global.get $cx_ng) (i32.const 1)))))
  (global.get $tx_used))

;; IRI(v): an IRI is itself; a simple literal is resolved (cq_iri); else an error.
(func $tx_iri (param $f i32) (param $v i32) (result i32)
  (local $r i32) (local $x i32) (local $id i32)
  (local.set $r (call $tv (local.get $f) (i32.const TK_ID)))
  (if (i32.eq (i32.load (local.get $v)) (i32.const TK_STR)) (then
    (call $em (local.get $f) (@str "gbk0H?k0:gagbCD}vc") (call $tva (local.get $v)) (call $tvb (local.get $v)) (call $tva (local.get $r)) (i32.const 0))
    (return (local.get $r))))
  (local.set $id (call $tva (call $as_id (local.get $f) (local.get $v))))
  (local.set $x (call $local (local.get $f)))
  (call $em1 (local.get $f) (@str "k0va") (call $tva (local.get $r)))
  (call $lget (local.get $f) (local.get $id))
  (call $topen (local.get $f) (@str "("))
  (call $rec_of (local.get $f) (local.get $id))
  (call $lset (local.get $f) (local.get $x))
  (call $lget (local.get $f) (local.get $x))
  (call $memop (local.get $f) (i32.const 0x2d) (i32.const 0) (i32.const 0))
  (call $em0 (local.get $f) (@str "k1F"))
  (call $em2 (local.get $f) (@str "(gavb:") (local.get $id) (call $tva (local.get $r)))
  (call $lget (local.get $f) (local.get $x))
  (call $memop (local.get $f) (i32.const 0x2d) (i32.const 0) (i32.const 0))
  (call $em1 (local.get $f) (@str "k3Fgam@k2Fq") (local.get $x))
  (call $em2 (local.get $f) (@str "(gam8gam<CDvb}}") (local.get $x) (call $tva (local.get $r)))
  (call $tend (local.get $f))
  (local.get $r))

;; The other typed functions; anything else is a callback.
(func $tx_rest (param $f i32) (param $e i32) (result i32)
  (local $fn i32) (local $a i32) (local $b i32) (local $r i32) (local $x i32) (local $y i32) (local $k i32) (local $d i32) (local $l i32)
  (local.set $fn (i32.load offset=4 (local.get $e)))
  (local.set $a (i32.load offset=8 (local.get $e)))
  (if (local.get $a) (then (local.set $b (i32.load offset=28 (local.get $a)))))
  (if (i32.eq (local.get $fn) (i32.const F_SUBSTR)) (then (return (call $tx_substr (local.get $f) (local.get $e)))))
  (if (i32.and (i32.eq (local.get $fn) (i32.const F_CAST)) (i32.ne (call $tx_cast (local.get $e)) (i32.const 0)))
    (then (return (call $tx_tonum (local.get $f) (call $tx (local.get $f) (local.get $a)) (call $tx_cast (local.get $e))))))
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_EQ)) (i32.const 5)) (then
    (if (i32.or (i32.eq (call $tk (local.get $a)) (i32.const TK_BOOL)) (i32.eq (call $tk (local.get $b)) (i32.const TK_BOOL)))
      (then (return (call $tx_cb (local.get $f) (local.get $e) (i32.const TK_BOOL)))))
    (return (call $tx_compare (local.get $f) (local.get $fn)
      (call $tx_operand (local.get $f) (call $tx (local.get $f) (local.get $a)))
      (call $tx_operand (local.get $f) (call $tx (local.get $f) (local.get $b)))))))
  (if (i32.and (i32.le_u (i32.sub (local.get $fn) (i32.const F_ADD)) (i32.const 1)) (i32.eq (call $tk (local.get $e)) (i32.const TK_NUM))) (then
    ;; + and - (two arguments); a term that is not a number (a date, a
    ;; duration) leaves for the engine
    (return (call $tx_arith (local.get $f) (local.get $fn)
      (call $num_operand (local.get $f) (call $tx (local.get $f) (local.get $a)))
      (call $num_operand (local.get $f) (call $tx (local.get $f) (local.get $b)))))))
  (if (i32.eq (local.get $fn) (i32.const F_IF)) (then
    (local.set $k (call $tk (local.get $e)))
    (local.set $x (call $local (local.get $f)))
    (call $to_bool (local.get $f) (call $tx (local.get $f) (local.get $a)))
    (call $lset (local.get $f) (local.get $x))
    (call $tv_set_err (local.get $f) (local.tee $r (call $tv (local.get $f) (local.get $k))))
    (call $em1 (local.get $f) (@str "gak2G") (local.get $x))
    (call $topen (local.get $f) (@str "("))
    (call $lget (local.get $f) (local.get $x))
    (call $topen (local.get $f) (@str "("))
    (call $tv_copy (local.get $f) (local.get $r) (call $as_kind (local.get $f) (call $tx (local.get $f) (local.get $b)) (local.get $k)))
    (call $em0 (local.get $f) (@str ":"))
    (call $tv_copy (local.get $f) (local.get $r) (call $as_kind (local.get $f) (call $tx (local.get $f) (i32.load offset=28 (local.get $b))) (local.get $k)))
    (call $tend (local.get $f))
    (call $tend (local.get $f))
    (return (local.get $r))))
  (if (i32.eq (local.get $fn) (i32.const F_COALESCE)) (then
    (local.set $k (call $tk (local.get $e)))
    (local.set $r (call $tv (local.get $f) (local.get $k)))
    (call $topen (local.get $f) (@str "{"))
    (loop $args
      (if (local.get $a) (then
        (local.set $y (call $as_kind (local.get $f) (call $tx (local.get $f) (local.get $a)) (local.get $k)))
        (call $tv_err (local.get $f) (local.get $y))
        (call $em0 (local.get $f) (@str "E("))
        (call $tv_copy (local.get $f) (local.get $r) (local.get $y))
        (call $em0 (local.get $f) (@str "b1}"))
        (local.set $a (i32.load offset=28 (local.get $a)))
        (br $args))))
    (call $tv_set_err (local.get $f) (local.get $r))
    (call $tend (local.get $f))
    (return (local.get $r))))
  (if (i32.eq (local.get $fn) (i32.const F_NOT)) (then
    (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
    (call $to_bool (local.get $f) (call $tx (local.get $f) (local.get $a)))
    (call $em2 (local.get $f) (@str "tak1sgagak2G~vb") (call $local (local.get $f)) (call $tva (local.get $r)))
    (return (local.get $r))))
  (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_OR)) (i32.const 1)) (then
    ;; AND: false decides; OR: true decides; else an error if either is one
    (local.set $d (i32.eq (local.get $fn) (i32.const F_OR)))
    (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
    (local.set $x (call $local (local.get $f)))
    (local.set $y (call $local (local.get $f)))
    (call $to_bool (local.get $f) (call $tx (local.get $f) (local.get $a)))
    (call $lset (local.get $f) (local.get $x))
    (call $em2 (local.get $f) (@str "gakbF") (local.get $x) (local.get $d))
    (call $topen (local.get $f) (@str "?"))
    (call $iconst (local.get $f) (local.get $d))
    (call $em0 (local.get $f) (@str ":"))
    (call $to_bool (local.get $f) (call $tx (local.get $f) (local.get $b)))
    (call $lset (local.get $f) (local.get $y))
    (call $em (local.get $f) (@str "gbkcF?kc:gak2Fgbk2Fr?k2:kd}}") (local.get $x) (local.get $y) (local.get $d) (i32.sub (i32.const 1) (local.get $d)))
    (call $tend (local.get $f))
    (call $lset (local.get $f) (call $tva (local.get $r)))
    (return (local.get $r))))
  (if (i32.eq (local.get $fn) (i32.const F_BOUND)) (then
    (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
    (call $to_id (local.get $f) (call $tx (local.get $f) (local.get $a)))
    (call $em1 (local.get $f) (@str "k0Gva") (call $tva (local.get $r)))
    (return (local.get $r))))
  (if (i32.and (i32.ge_u (local.get $fn) (i32.const F_ISIRI)) (i32.le_u (local.get $fn) (i32.const F_ISLITERAL))) (then
    (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
    (local.set $x (call $tva (call $as_id (local.get $f) (call $tx (local.get $f) (local.get $a)))))
    (call $em (local.get $f) (@str "gaE?k2:gaC<kbF}vc") (local.get $x)
      (select (i32.const 1) (i32.sub (local.get $fn) (i32.const 76)) (i32.le_u (local.get $fn) (i32.const F_ISURI))) (call $tva (local.get $r)) (i32.const 0))
    (return (local.get $r))))
  (if (i32.eq (local.get $fn) (i32.const F_SAMETERM)) (then
    (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
    (local.set $x (call $tva (call $as_id (local.get $f) (call $tx (local.get $f) (local.get $a)))))
    (local.set $y (call $tva (call $as_id (local.get $f) (call $tx (local.get $f) (local.get $b)))))
    (call $em (local.get $f) (@str "gaEgbEr?k2:gagbF}vc") (local.get $x) (local.get $y) (call $tva (local.get $r)) (i32.const 0))
    (return (local.get $r))))
  (call $tx_cb (local.get $f) (local.get $e) (call $tk (local.get $e))))

;; SUBSTR(s, start[, length]) as the engine computes it (code points, rounded
;; positions): a slice of s's bytes, nothing copied.
(func $tx_substr (param $f i32) (param $e i32) (result i32)
  (local $a i32) (local $sv i32) (local $ns i32) (local $nl i32) (local $r i32) (local $s i32) (local $l i32) (local $n i32) (local $x i32) (local $y i32)
  (local.set $a (i32.load offset=8 (local.get $e)))
  (local.set $sv (call $to_str (local.get $f) (call $tx (local.get $f) (local.get $a)) (i32.const 0)))
  (local.set $ns (call $to_num (local.get $f) (call $tx (local.get $f) (i32.load offset=28 (local.get $a)))))
  (if (i32.eq (i32.load offset=12 (local.get $e)) (i32.const 3))
    (then (local.set $nl (call $to_num (local.get $f) (call $tx (local.get $f) (i32.load offset=28 (i32.load offset=28 (local.get $a))))))))
  (local.set $r (call $tv (local.get $f) (i32.const TK_STR)))
  (call $tv_set_err (local.get $f) (local.get $r))
  (local.set $s (call $local_t (local.get $f) (i32.const 0x7c)))
  (local.set $l (call $local_t (local.get $f) (i32.const 0x7c)))
  (local.set $n (call $local (local.get $f)))
  (local.set $x (call $local (local.get $f)))
  (local.set $y (call $local (local.get $f)))
  (call $tv_err (local.get $f) (local.get $sv))
  (call $tv_err (local.get $f) (local.get $ns))
  (call $em0 (local.get $f) (@str "r"))
  (if (local.get $nl) (then (call $tv_err (local.get $f) (local.get $nl)) (call $em0 (local.get $f) (@str "r"))))
  (call $em0 (local.get $f) (@str "E"))
  (call $topen (local.get $f) (@str "("))
  ;; s = floor(start + 0.5); l = s + (floor(length + 0.5) or infinity); s >= 1
  (call $lget (local.get $f) (call $tvd (local.get $ns))) (call $f64const (local.get $f) (f64.const 0.5)) (call $op (local.get $f) (i32.const 0xa0)) (call $op (local.get $f) (i32.const 0x9c))
  (call $lset (local.get $f) (local.get $s))
  (if (local.get $nl)
    (then (call $lget (local.get $f) (call $tvd (local.get $nl))) (call $f64const (local.get $f) (f64.const 0.5)) (call $op (local.get $f) (i32.const 0xa0)) (call $op (local.get $f) (i32.const 0x9c)))
    (else (call $f64const (local.get $f) (f64.const inf))))
  (call $lget (local.get $f) (local.get $s)) (call $op (local.get $f) (i32.const 0xa0)) (call $lset (local.get $f) (local.get $l))
  (call $lget (local.get $f) (local.get $s)) (call $f64const (local.get $f) (f64.const 1)) (call $op (local.get $f) (i32.const 0x63))
  (call $em0 (local.get $f) (@str "("))
  (call $f64const (local.get $f) (f64.const 1)) (call $lset (local.get $f) (local.get $s))
  (call $em0 (local.get $f) (@str "}"))
  ;; l <= the code points + 1
  (call $em (local.get $f) (@str "gagbCHvc") (call $tva (local.get $sv)) (call $tvb (local.get $sv)) (local.get $n) (i32.const 0))
  (call $lget (local.get $f) (local.get $l)) (call $em1 (local.get $f) (@str "gak1j") (local.get $n)) (call $op (local.get $f) (i32.const 0xb8)) (call $op (local.get $f) (i32.const 0x64))
  (call $em0 (local.get $f) (@str "("))
  (call $em1 (local.get $f) (@str "gak1j") (local.get $n)) (call $op (local.get $f) (i32.const 0xb8)) (call $lset (local.get $f) (local.get $l))
  (call $em0 (local.get $f) (@str "}"))
  ;; empty, unless s < l (neither NaN)
  (call $em2 (local.get $f) (@str "k0vak0vb") (call $tva (local.get $r)) (call $tvb (local.get $r)))
  (call $lget (local.get $f) (local.get $s)) (call $lget (local.get $f) (local.get $l)) (call $op (local.get $f) (i32.const 0x63))
  (call $em0 (local.get $f) (@str "("))
  (call $em2 (local.get $f) (@str "gagb") (call $tva (local.get $sv)) (call $tvb (local.get $sv)))
  (call $lget (local.get $f) (local.get $s)) (call $f64const (local.get $f) (f64.const 1)) (call $op (local.get $f) (i32.const 0xa1))
  (call $op (local.get $f) (i32.const 0xfc)) (call $op (local.get $f) (i32.const 3))
  (call $em1 (local.get $f) (@str "CIva") (local.get $x))
  (call $em2 (local.get $f) (@str "gagb") (call $tva (local.get $sv)) (call $tvb (local.get $sv)))
  (call $lget (local.get $f) (local.get $l)) (call $f64const (local.get $f) (f64.const 1)) (call $op (local.get $f) (i32.const 0xa1))
  (call $op (local.get $f) (i32.const 0xfc)) (call $op (local.get $f) (i32.const 3))
  (call $em1 (local.get $f) (@str "CIva") (local.get $y))
  (call $em (local.get $f) (@str "gbgajvc") (local.get $x) (call $tva (local.get $sv)) (call $tva (local.get $r)) (i32.const 0))
  (call $lget (local.get $f) (local.get $y)) (call $lget (local.get $f) (local.get $x)) (call $op (local.get $f) (i32.const 0x6b))
  (call $lset (local.get $f) (call $tvb (local.get $r)))
  (call $em0 (local.get $f) (@str "}"))
  (call $tend (local.get $f))
  (local.get $r))

;; A cast of v to xsd:integer or xsd:double (tc).
(func $tx_tonum (param $f i32) (param $v i32) (param $tc i32) (result i32)
  (local $r i32) (local $k i32) (local $id i32)
  (local.set $k (i32.load (local.get $v)))
  (local.set $r (call $tv (local.get $f) (i32.const TK_NUM)))
  (call $em2 (local.get $f) (@str "k0vak0vb") (call $tve (local.get $r)) (call $tvc (local.get $r)))
  (if (i32.eq (local.get $k) (i32.const TK_STR)) (then
    (call $em1 (local.get $f) (@str "gak0H?k0:") (call $tvb (local.get $v)))
    (call $em (local.get $f) (@str "gcgagbkdCF}") (call $tva (local.get $v)) (call $tvb (local.get $v)) (call $tvc (local.get $v)) (local.get $tc))
    (call $lset (local.get $f) (call $tva (local.get $r)))
    (call $ld_num (local.get $f) (local.get $r))
    (return (local.get $r))))
  (if (i32.eq (local.get $k) (i32.const TK_NUM)) (then
    ;; an existing term: as the engine casts it
    (call $lget (local.get $f) (call $tve (local.get $v)))
    (call $topen (local.get $f) (@str "("))
    (call $em2 (local.get $f) (@str "gak0k0kbCF") (call $tve (local.get $v)) (local.get $tc))
    (call $lset (local.get $f) (call $tva (local.get $r)))
    (call $ld_num (local.get $f) (local.get $r))
    (call $em0 (local.get $f) (@str ":"))
    ;; computed: an integer (1) or a double (4) already
    (call $tv_copy (local.get $f) (local.get $r) (local.get $v))
    (call $em1 (local.get $f) (@str "k0va") (call $tve (local.get $r)))
    (if (i32.eq (local.get $tc) (i32.const C_DOUBLE))
      (then (call $em1 (local.get $f) (@str "gaE?k0:k4}va") (call $tva (local.get $r))))
      (else
        (call $em1 (local.get $f) (@str "gak4F") (call $tva (local.get $r)))
        (call $topen (local.get $f) (@str "("))
        ;; NaN or out of range: an error; else truncated
        (call $lget (local.get $f) (call $tvd (local.get $r))) (call $lget (local.get $f) (call $tvd (local.get $r))) (call $op (local.get $f) (i32.const 0x62))
        (call $lget (local.get $f) (call $tvd (local.get $r))) (call $op (local.get $f) (i32.const 0x99)) (call $f64const (local.get $f) (f64.const 9e18)) (call $op (local.get $f) (i32.const 0x64))
        (call $em1 (local.get $f) (@str "r(k0va:") (call $tva (local.get $r)))
        (call $lget (local.get $f) (call $tvd (local.get $r))) (call $op (local.get $f) (i32.const 0x9d)) (call $op (local.get $f) (i32.const 0xb0))
        (call $lset (local.get $f) (call $tvb (local.get $r)))
        (call $lget (local.get $f) (call $tvb (local.get $r))) (call $op (local.get $f) (i32.const 0xb9)) (call $lset (local.get $f) (call $tvd (local.get $r)))
        (call $em1 (local.get $f) (@str "k1va}") (call $tva (local.get $r)))
        (call $tend (local.get $f))
        ;; a decimal or float computed here: not done
        (call $em1 (local.get $f) (@str "gak1K") (call $tva (local.get $r)))
        (call $bail_if (local.get $f))))
    (call $tend (local.get $f))
    (return (local.get $r))))
  (local.set $id (call $tva (call $as_id (local.get $f) (local.get $v))))
  (call $em2 (local.get $f) (@str "gak0k0kbCF") (local.get $id) (local.get $tc))
  (call $lset (local.get $f) (call $tva (local.get $r)))
  (call $ld_num (local.get $f) (local.get $r))
  (local.get $r))

;; A comparison's operand: +0 its class local (0 an error, 1 a number, 2 a
;; simple string, 3 another term: an IRI or blank node), +4 its STR, +8 its NUM
;; (0 when it cannot be one), +12 its id local (0: none).
(func $tx_operand (param $f i32) (param $v i32) (result i32)
  (local $o i32) (local $c i32) (local $k i32) (local $x i32) (local $id i32) (local $s i32) (local $n i32)
  (local.set $o (call $anew (i32.const 16)))
  (i32.store (local.get $o) (local.tee $c (call $local (local.get $f))))
  (local.set $k (i32.load (local.get $v)))
  (if (i32.eq (local.get $k) (i32.const TK_STR)) (then
    (i32.store offset=4 (local.get $o) (local.get $v))
    (i32.store offset=12 (local.get $o) (call $tvc (local.get $v)))
    (call $em2 (local.get $f) (@str "gak0H?k0:k2}vb") (call $tvb (local.get $v)) (local.get $c))
    (return (local.get $o))))
  (if (i32.eq (local.get $k) (i32.const TK_NUM)) (then
    (i32.store offset=8 (local.get $o) (local.get $v))
    (i32.store offset=12 (local.get $o) (call $tve (local.get $v)))
    (call $em2 (local.get $f) (@str "gaE?k0:k1}vb") (call $tva (local.get $v)) (local.get $c))
    (return (local.get $o))))
  (local.set $id (call $tva (call $as_id (local.get $f) (local.get $v))))
  (i32.store offset=12 (local.get $o) (local.get $id))
  (i32.store offset=4 (local.get $o) (local.tee $s (call $tv (local.get $f) (i32.const TK_STR))))
  (i32.store offset=8 (local.get $o) (local.tee $n (call $tv (local.get $f) (i32.const TK_NUM))))
  (local.set $x (call $local (local.get $f)))
  (call $em1 (local.get $f) (@str "k0va") (local.get $c))
  (call $lget (local.get $f) (local.get $id))
  (call $topen (local.get $f) (@str "("))
  (call $rec_of (local.get $f) (local.get $id))
  (call $lset (local.get $f) (local.get $x))
  (call $lget (local.get $f) (local.get $x))
  (call $memop (local.get $f) (i32.const 0x2d) (i32.const 0) (i32.const 0))
  (call $em0 (local.get $f) (@str "k3F"))
  (call $topen (local.get $f) (@str "("))
  ;; a literal: an xsd:string, else a number, else the engine decides
  (call $em1 (local.get $f) (@str "gam@k2F") (local.get $x))
  (call $topen (local.get $f) (@str "("))
  (call $em (local.get $f) (@str "gam8vbgam<vck2vd") (local.get $x) (call $tva (local.get $s)) (call $tvb (local.get $s)) (local.get $c))
  (call $em0 (local.get $f) (@str ":"))
  (call $em2 (local.get $f) (@str "gaCEvb") (local.get $id) (call $tva (local.get $n)))
  (call $ld_num (local.get $f) (local.get $n))
  (call $em2 (local.get $f) (@str "gavb") (local.get $id) (call $tve (local.get $n)))
  (call $em1 (local.get $f) (@str "ga") (call $tva (local.get $n)))
  (call $topen (local.get $f) (@str "("))
  (call $em1 (local.get $f) (@str "k1va:") (local.get $c))
  (call $bail (local.get $f))
  (call $tend (local.get $f))
  (call $tend (local.get $f))
  ;; not a literal: a triple term leaves, an IRI or blank node is itself
  (call $em0 (local.get $f) (@str ":"))
  (call $lget (local.get $f) (local.get $x))
  (call $memop (local.get $f) (i32.const 0x2d) (i32.const 0) (i32.const 0))
  (call $em0 (local.get $f) (@str "k4F"))
  (call $bail_if (local.get $f))
  (call $em1 (local.get $f) (@str "k3va") (local.get $c))
  (call $tend (local.get $f))
  (call $tend (local.get $f))
  (local.get $o))

;; Operands a, b compared by fn (= != < > <= >=), as rdf_eq and rel_cmp would.
(func $tx_compare (param $f i32) (param $fn i32) (param $a i32) (param $b i32) (result i32)
  (local $r i32) (local $x i32) (local $na i32) (local $nb i32) (local $sa i32) (local $sb i32) (local $c i32)
  (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
  (local.set $x (call $local (local.get $f)))
  (call $em1 (local.get $f) (@str "k2va") (call $tva (local.get $r)))
  (call $em2 (local.get $f) (@str "gak0Ggbk0Gq(") (i32.load (local.get $a)) (i32.load (local.get $b)))
  ;; x: -1, 0, 1, or 2 (unequal and not ordered)
  (call $em1 (local.get $f) (@str "k2va") (local.get $x))
  (local.set $sa (i32.load offset=4 (local.get $a)))
  (local.set $sb (i32.load offset=4 (local.get $b)))
  (if (i32.and (i32.ne (local.get $sa) (i32.const 0)) (i32.ne (local.get $sb) (i32.const 0))) (then
    (call $em2 (local.get $f) (@str "gak2Fgbk2Fq(k3") (i32.load (local.get $a)) (i32.load (local.get $b)))
    (call $em (local.get $f) (@str "gagbgcgdCJ") (call $tva (local.get $sa)) (call $tvb (local.get $sa)) (call $tva (local.get $sb)) (call $tvb (local.get $sb)))
    (call $em1 (local.get $f) (@str "va}") (local.get $x))))
  (local.set $na (i32.load offset=8 (local.get $a)))
  (local.set $nb (i32.load offset=8 (local.get $b)))
  (if (i32.and (i32.ne (local.get $na) (i32.const 0)) (i32.ne (local.get $nb) (i32.const 0))) (then
    (call $em2 (local.get $f) (@str "gak1Fgbk1Fq(") (i32.load (local.get $a)) (i32.load (local.get $b)))
    ;; num_cmp: integers or decimals of one scale by ni, else by nf (NaN: 2)
    (call $em (local.get $f) (@str "gak3Igbk3Iqgcgd") (call $tva (local.get $na)) (call $tva (local.get $nb)) (call $tvc (local.get $na)) (call $tvc (local.get $nb)))
    (call $em0 (local.get $f) (@str "Fq("))
    (call $em1 (local.get $f) (@str "k1kak0") (i32.const -1))
    (call $lget (local.get $f) (call $tvb (local.get $na))) (call $lget (local.get $f) (call $tvb (local.get $nb))) (call $op (local.get $f) (i32.const 0x53))
    (call $em0 (local.get $f) (@str "~"))
    (call $lget (local.get $f) (call $tvb (local.get $na))) (call $lget (local.get $f) (call $tvb (local.get $nb))) (call $op (local.get $f) (i32.const 0x55))
    (call $em1 (local.get $f) (@str "~va:") (local.get $x))
    (call $lget (local.get $f) (call $tvd (local.get $na))) (call $lget (local.get $f) (call $tvd (local.get $na))) (call $op (local.get $f) (i32.const 0x62))
    (call $lget (local.get $f) (call $tvd (local.get $nb))) (call $lget (local.get $f) (call $tvd (local.get $nb))) (call $op (local.get $f) (i32.const 0x62))
    (call $em1 (local.get $f) (@str "r(k2va:") (local.get $x))
    (call $em1 (local.get $f) (@str "k1kak0") (i32.const -1))
    (call $lget (local.get $f) (call $tvd (local.get $na))) (call $lget (local.get $f) (call $tvd (local.get $nb))) (call $op (local.get $f) (i32.const 0x63))
    (call $em0 (local.get $f) (@str "~"))
    (call $lget (local.get $f) (call $tvd (local.get $na))) (call $lget (local.get $f) (call $tvd (local.get $nb))) (call $op (local.get $f) (i32.const 0x64))
    (call $em1 (local.get $f) (@str "~va}}}") (local.get $x))))
  (if (i32.and (i32.le_u (i32.sub (local.get $fn) (i32.const F_EQ)) (i32.const 1))
               (i32.and (i32.ne (local.get $sa) (i32.const 0)) (i32.ne (local.get $na) (i32.const 0)))) (then
    (if (i32.and (i32.ne (local.get $sb) (i32.const 0)) (i32.ne (local.get $nb) (i32.const 0))) (then
      ;; two terms that are not literals: the same term or not
      (call $em2 (local.get $f) (@str "gak3Fgbk3Fq(") (i32.load (local.get $a)) (i32.load (local.get $b)))
      (call $em (local.get $f) (@str "k0k2gagbF~vc}") (i32.load offset=12 (local.get $a)) (i32.load offset=12 (local.get $b)) (local.get $x) (i32.const 0))))))
  ;; r from x
  (if (i32.eq (local.get $fn) (i32.const F_EQ)) (then (call $em2 (local.get $f) (@str "gak0Fvb") (local.get $x) (call $tva (local.get $r)))))
  (if (i32.eq (local.get $fn) (i32.const F_NE)) (then (call $em2 (local.get $f) (@str "gak0Gvb") (local.get $x) (call $tva (local.get $r)))))
  (if (i32.ge_u (local.get $fn) (i32.const F_LT)) (then
    (local.set $c (select (i32.const -1) (i32.const 1) (i32.or (i32.eq (local.get $fn) (i32.const F_LT)) (i32.eq (local.get $fn) (i32.const F_GE)))))
    (call $em (local.get $f) (select (call $p1 (@str "k2gakcFgak2F~vb")) (call $p1 (@str "k2gakcGgak2F~vb")) (i32.le_u (i32.sub (local.get $fn) (i32.const F_LT)) (i32.const 1))) (i32.const 15)
      (local.get $x) (call $tva (local.get $r)) (local.get $c) (i32.const 0))))
  (call $em0 (local.get $f) (@str "}"))
  (local.get $r))

;; a + b or a - b: integers, or with a double a double; else (decimals,
;; floats) the bail block.
(func $tx_arith (param $f i32) (param $fn i32) (param $a i32) (param $b i32) (result i32)
  (local $r i32)
  (local.set $r (call $tv (local.get $f) (i32.const TK_NUM)))
  (call $em (local.get $f) (@str "k0vak0vbk0vc") (call $tva (local.get $r)) (call $tvc (local.get $r)) (call $tve (local.get $r)) (i32.const 0))
  (call $em2 (local.get $f) (@str "gak0Ggbk0Gq") (call $tva (local.get $a)) (call $tva (local.get $b)))
  (call $topen (local.get $f) (@str "("))
  (call $em2 (local.get $f) (@str "gak1Fgbk1Fq") (call $tva (local.get $a)) (call $tva (local.get $b)))
  (call $topen (local.get $f) (@str "("))
  (call $lget (local.get $f) (call $tvb (local.get $a))) (call $lget (local.get $f) (call $tvb (local.get $b)))
  (call $op (local.get $f) (select (i32.const 0x7c) (i32.const 0x7d) (i32.eq (local.get $fn) (i32.const F_ADD))))
  (call $lset (local.get $f) (call $tvb (local.get $r)))
  (call $lget (local.get $f) (call $tvb (local.get $r))) (call $op (local.get $f) (i32.const 0xb9)) (call $lset (local.get $f) (call $tvd (local.get $r)))
  (call $em1 (local.get $f) (@str "k1va:") (call $tva (local.get $r)))
  (call $em2 (local.get $f) (@str "gak4Fgbk4Fr") (call $tva (local.get $a)) (call $tva (local.get $b)))
  (call $topen (local.get $f) (@str "("))
  (call $lget (local.get $f) (call $tvd (local.get $a))) (call $lget (local.get $f) (call $tvd (local.get $b)))
  (call $op (local.get $f) (select (i32.const 0xa0) (i32.const 0xa1) (i32.eq (local.get $fn) (i32.const F_ADD))))
  (call $lset (local.get $f) (call $tvd (local.get $r)))
  (call $em1 (local.get $f) (@str "k4va:") (call $tva (local.get $r)))
  (call $bail (local.get $f))
  (call $tend (local.get $f))
  (call $tend (local.get $f))
  (call $tend (local.get $f))
  (local.get $r))

;; --- top level --------------------------------------------------------------------------

;; Push e's value (cond: its condition, 0, 1 or 2; else its term id), typed,
;; with the engine's evaluation as the bail block.
(func $tx_top (param $f i32) (param $e i32) (param $cond i32)
  (local $u i32) (local $g i32) (local $saved i32) (local $v i32)
  (local.set $saved (global.get $txd))
  (global.set $txd (i32.const 0))
  (local.set $g (call $tx_used_global))
  (local.set $u (call $local (local.get $f)))
  (call $em2 (local.get $f) (@str "Ravb<{") (local.get $g) (local.get $u))
  (local.set $v (call $tx (local.get $f) (local.get $e)))
  (if (local.get $cond) (then (call $to_bool (local.get $f) (local.get $v))) (else (call $to_id (local.get $f) (local.get $v))))
  (call $em2 (local.get $f) (@str "gaWbb1}gaWb") (local.get $u) (local.get $g))
  (call $em2 (local.get $f) (select (call $p1 (@str "kaC6}")) (call $p1 (@str "kaC7}")) (local.get $cond)) (i32.const 5) (i32.or (local.get $e) (global.get $cx_keep)) (i32.const 0))
  (global.set $txd (local.get $saved)))

;; EXISTS { triples and paths, filters }: a function of its own that returns at
;; the first solution, having set a global. The slots it binds are restored
;; around the call (it returns from inside its loops). Other groups: cq_test.
(func $tx_exists (param $f i32) (param $e i32) (result i32)
  (local $g i32) (local $el i32) (local $st i32) (local $flag i32) (local $fn i32) (local $b i32) (local $s i32) (local $saves i32) (local $r i32)
  (local.set $g (i32.load offset=4 (local.get $e)))
  (if (i32.or (call $cx_off (i32.const 4)) (i32.eqz (local.tee $st (global.get $tx_st)))) (then (return (call $tx_cb (local.get $f) (local.get $e) (i32.const TK_BOOL)))))
  (local.set $el (i32.load offset=4 (local.get $g)))
  (loop $els
    (if (local.get $el) (then
      (if (i32.gt_u (i32.sub (i32.load (local.get $el)) (i32.const P_TRIPLE)) (i32.const 1))
        (then (return (call $tx_cb (local.get $f) (local.get $e) (i32.const TK_BOOL)))))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (br $els))))
  (local.set $flag (global.get $cx_ng))
  (global.set $cx_ng (i32.add (local.get $flag) (i32.const 1)))
  (local.set $fn (call $cont
    (call $grp (i32.load offset=4 (local.get $g)) (i32.load offset=8 (local.get $g)) (call $cstep (i32.const S_EXIT) (local.get $flag) (i32.const 0) (i32.const 0)))
    (local.get $st)))
  ;; save what it may bind
  (call $deep (local.tee $b (call $cx_bits)) (local.get $g))
  (local.set $saves (call $anew (i32.shl (global.get $cx_nv) (i32.const 2))))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (i32.and (call $bit (local.get $b) (local.get $s)) (i32.ne (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const CERTAIN))) (then
        (i32.store (i32.add (local.get $saves) (i32.shl (local.get $s) (i32.const 2))) (call $local (local.get $f)))
        (call $em2 (local.get $f) (@str "k0mavb") (call $addr (local.get $s)) (i32.load (i32.add (local.get $saves) (i32.shl (local.get $s) (i32.const 2)))))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (call $em (local.get $f) (@str "k0WagbCc") (local.get $flag) (i32.load (local.get $st)) (local.get $fn) (i32.const 0))
  (call $copy_saved (local.get $f) (local.get $saves) (i32.const 0))
  (local.set $r (call $tv (local.get $f) (i32.const TK_BOOL)))
  (call $em1 (local.get $f) (@str "Ra") (local.get $flag))
  (if (i32.load offset=8 (local.get $e)) (then (call $em0 (local.get $f) (@str "k1s"))))
  (call $lset (local.get $f) (call $tva (local.get $r)))
  (local.get $r))

;; An arithmetic operand: a number, an error when unbound or computed as one;
;; a bound term that is not a number leaves for the bail block (dates and
;; durations have arithmetic of their own in the engine).
(func $num_operand (param $f i32) (param $v i32) (result i32)
  (local $r i32)
  (local.set $r (call $to_num (local.get $f) (local.get $v)))
  (if (i32.eq (i32.load (local.get $v)) (i32.const TK_ID)) (then
    (call $em2 (local.get $f) (@str "gaEgbk0Gq") (call $tva (local.get $r)) (call $tva (local.get $v)))
    (call $bail_if (local.get $f))))
  (local.get $r))
