;; Numbers and dates. Numeric types: 1 integer (i64), 2 decimal (i64 mantissa
;; and a scale of at most 18 digits), 3 float, 4 double (f64; floats rounded
;; through f32). Computed numbers are written as Oxigraph writes them (shortest
;; digits, plain notation): "2", "2.5", "0.30000000000000004".

(global $ni (mut i64) (i64.const 0))   ;; integer value, or decimal mantissa
(global $nsc (mut i32) (i32.const 0))  ;; decimal scale
(global $nf (mut f64) (f64.const 0))   ;; value as a double
(global $nbuf (mut i32) (i32.const 0)) ;; 400 bytes for formatting

(func $p10 (param $k i32) (result f64)
  (local $r f64)
  (local.set $r (f64.const 1))
  (loop $mul (if (i32.gt_s (local.get $k) (i32.const 0)) (then
    (local.set $r (f64.mul (local.get $r) (f64.const 10)))
    (local.set $k (i32.sub (local.get $k) (i32.const 1))) (br $mul))))
  (local.get $r))

(func $i10 (param $k i32) (result i64)
  (local $r i64)
  (local.set $r (i64.const 1))
  (loop $mul (if (i32.gt_s (local.get $k) (i32.const 0)) (then
    (local.set $r (i64.mul (local.get $r) (i64.const 10)))
    (local.set $k (i32.sub (local.get $k) (i32.const 1))) (br $mul))))
  (local.get $r))

;; x * 10^k, in steps that stay finite.
(func $scale10 (param $x f64) (param $k i32) (result f64)
  (loop $up (if (i32.gt_s (local.get $k) (i32.const 22)) (then
    (local.set $x (f64.mul (local.get $x) (f64.const 1e22)))
    (local.set $k (i32.sub (local.get $k) (i32.const 22))) (br $up))))
  (loop $down (if (i32.lt_s (local.get $k) (i32.const -22)) (then
    (local.set $x (f64.div (local.get $x) (f64.const 1e22)))
    (local.set $k (i32.add (local.get $k) (i32.const 22))) (br $down))))
  (if (result f64) (i32.ge_s (local.get $k) (i32.const 0))
    (then (f64.mul (local.get $x) (call $p10 (local.get $k))))
    (else (f64.div (local.get $x) (call $p10 (i32.sub (i32.const 0) (local.get $k)))))))

;; Parse a number's lexical form [p, p+n) as type t (1-4) into $ni/$nsc/$nf;
;; whether it is valid.
(func $parse_num (param $p i32) (param $n i32) (param $t i32) (result i32)
  (local $e i32) (local $neg i32) (local $c i32) (local $digits i32) (local $dot i32) (local $exp i32) (local $eneg i32)
  (local $m i64) (local $nd i32) (local $drop i32) (local $f f64)
  (local.set $e (i32.add (local.get $p) (local.get $n)))
  (if (i32.ge_u (local.get $t) (i32.const 3)) (then
    (if (i32.or (call $same (local.get $p) (local.get $n) (@str "INF") (i32.const 0)) (call $same (local.get $p) (local.get $n) (@str "+INF") (i32.const 0)))
      (then (global.set $nf (f64.const inf)) (return (i32.const 1))))
    (if (call $same (local.get $p) (local.get $n) (@str "-INF") (i32.const 0)) (then (global.set $nf (f64.const -inf)) (return (i32.const 1))))
    (if (call $same (local.get $p) (local.get $n) (@str "NaN") (i32.const 0)) (then (global.set $nf (f64.const nan)) (return (i32.const 1))))))
  (if (i32.lt_u (local.get $p) (local.get $e)) (then
    (local.set $c (i32.load8_u (local.get $p)))
    (if (i32.or (i32.eq (local.get $c) (i32.const '+')) (i32.eq (local.get $c) (i32.const '-'))) (then
      (local.set $neg (i32.eq (local.get $c) (i32.const '-')))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))))))
  (block $bad
    (loop $chars
      (if (i32.lt_u (local.get $p) (local.get $e)) (then
        (local.set $c (i32.load8_u (local.get $p)))
        (if (call $is_digit (local.get $c))
          (then
            (local.set $digits (i32.add (local.get $digits) (i32.const 1)))
            (if (i32.lt_u (local.get $nd) (i32.const 18))
              (then
                (if (i32.or (i64.ne (local.get $m) (i64.const 0)) (i32.ne (local.get $c) (i32.const '0'))) (then (local.set $nd (i32.add (local.get $nd) (i32.const 1)))))
                (local.set $m (i64.add (i64.mul (local.get $m) (i64.const 10)) (i64.extend_i32_u (i32.sub (local.get $c) (i32.const '0')))))
                (if (local.get $dot) (then (global.set $nsc (i32.add (global.get $nsc) (i32.const 1))))))
              (else
                ;; digits beyond what an i64 holds: scale only
                (if (i32.eqz (local.get $dot)) (then (local.set $drop (i32.add (local.get $drop) (i32.const 1)))))))
            (local.set $p (i32.add (local.get $p) (i32.const 1)))
            (br $chars)))
        (if (i32.and (i32.eq (local.get $c) (i32.const '.')) (i32.and (i32.eqz (local.get $dot)) (i32.ne (local.get $t) (i32.const 1)))) (then
          (local.set $dot (i32.const 1))
          (local.set $p (i32.add (local.get $p) (i32.const 1)))
          (br $chars)))
        (br_if $bad (i32.or (i32.lt_u (local.get $t) (i32.const 3)) (i32.ne (i32.or (local.get $c) (i32.const 0x20)) (i32.const 'e'))))
        ;; exponent
        (local.set $p (i32.add (local.get $p) (i32.const 1)))
        (if (i32.lt_u (local.get $p) (local.get $e)) (then
          (local.set $c (i32.load8_u (local.get $p)))
          (if (i32.or (i32.eq (local.get $c) (i32.const '+')) (i32.eq (local.get $c) (i32.const '-'))) (then
            (local.set $eneg (i32.eq (local.get $c) (i32.const '-')))
            (local.set $p (i32.add (local.get $p) (i32.const 1)))))))
        (br_if $bad (i32.ge_u (local.get $p) (local.get $e)))
        (loop $edigits
          (if (i32.lt_u (local.get $p) (local.get $e)) (then
            (br_if $bad (i32.eqz (call $is_digit (i32.load8_u (local.get $p)))))
            (if (i32.lt_u (local.get $exp) (i32.const 10000))
              (then (local.set $exp (i32.add (i32.mul (local.get $exp) (i32.const 10)) (i32.sub (i32.load8_u (local.get $p)) (i32.const '0'))))))
            (local.set $p (i32.add (local.get $p) (i32.const 1)))
            (br $edigits))))
        (if (local.get $eneg) (then (local.set $exp (i32.sub (i32.const 0) (local.get $exp))))))))
    (br_if $bad (i32.eqz (local.get $digits)))
    (if (local.get $neg) (then (local.set $m (i64.sub (i64.const 0) (local.get $m)))))
    (global.set $ni (local.get $m))
    (local.set $f (call $scale10 (f64.convert_i64_s (local.get $m)) (i32.sub (i32.add (local.get $exp) (local.get $drop)) (global.get $nsc))))
    (if (i32.eq (local.get $t) (i32.const 3)) (then (local.set $f (f64.promote_f32 (f32.demote_f64 (local.get $f))))))
    ;; the integer mantissa has no -0: "-0", "-0.0e0" keep their sign
    (if (i32.and (local.get $neg) (f64.eq (local.get $f) (f64.const 0))) (then (local.set $f (f64.const -0))))
    (global.set $nf (local.get $f))
    (return (i32.const 1)))
  (i32.const 0))

;; The numeric type of term id (0: not a valid number), its value in $ni/$nsc/$nf.
;; A literal's parse is kept in a table its record (+24) points into (index + 1).
(global $ncache (mut i32) (i32.const 0))     ;; 24 bytes each: type, scale, i64 value, f64 value
(global $ncache_n (mut i32) (i32.const 0))
(global $ncache_cap (mut i32) (i32.const 0))

(func $num (param $id i32) (result i32)
  (local $c i32) (local $t i32) (local $r i32) (local $e i32)
  (if (i32.ne (call $kind (local.get $id)) (i32.const 3)) (then (return (i32.const 0))))
  (local.set $r (call $rec (local.get $id)))
  (if (local.tee $e (i32.load offset=24 (local.get $r))) (then
    (local.set $e (i32.add (global.get $ncache) (i32.mul (i32.sub (local.get $e) (i32.const 1)) (i32.const 24))))
    (global.set $nsc (i32.load offset=4 (local.get $e)))
    (global.set $ni (i64.load offset=8 (local.get $e)))
    (global.set $nf (f64.load offset=16 (local.get $e)))
    (return (i32.load (local.get $e)))))
  (local.set $c (call $dtc (call $tdt (local.get $id))))
  (global.set $nsc (i32.const 0))
  (global.set $ni (i64.const 0))
  (if (i32.le_u (i32.sub (local.get $c) (i32.const C_INTEGER)) (i32.const 3)) (then
    (local.set $t (i32.sub (local.get $c) (i32.const 1)))
    (if (i32.eqz (call $parse_num (call $tval (local.get $id)) (call $tlen (local.get $id)) (local.get $t))) (then (local.set $t (i32.const 0))))))
  ;; a dateTime or date (not a number, type 0) keeps its instant for $date_cmp: $ni whole
  ;; seconds, $nf the fraction's bits, $nsc 1 when valid, | 2 with a timezone
  (if (i32.or (i32.eq (local.get $c) (i32.const C_DATETIME)) (i32.eq (local.get $c) (i32.const C_DATE))) (then
    (if (call $date_parse (call $tval (local.get $id)) (call $tlen (local.get $id))) (then
      (call $date_key)
      (global.set $ni (global.get $dk_s))
      (global.set $nf (f64.reinterpret_i64 (global.get $dk_f)))
      (global.set $nsc (i32.or (i32.const 1) (i32.shl (i32.ne (global.get $d_tz) (i32.const 0)) (i32.const 1))))))))
  (if (i32.eq (global.get $ncache_n) (global.get $ncache_cap)) (then
    (local.set $c (i32.add (i32.shl (global.get $ncache_cap) (i32.const 1)) (i32.const 64)))
    (global.set $ncache (if (result i32) (global.get $ncache_cap)
      (then (call $realloc (global.get $ncache) (i32.mul (global.get $ncache_cap) (i32.const 24)) (i32.mul (local.get $c) (i32.const 24))
                           (i32.mul (global.get $ncache_cap) (i32.const 24))))
      (else (call $alloc (i32.mul (local.get $c) (i32.const 24))))))
    (global.set $ncache_cap (local.get $c))))
  (local.set $e (i32.add (global.get $ncache) (i32.mul (global.get $ncache_n) (i32.const 24))))
  (i32.store (local.get $e) (local.get $t))
  (i32.store offset=4 (local.get $e) (global.get $nsc))
  (i64.store offset=8 (local.get $e) (global.get $ni))
  (f64.store offset=16 (local.get $e) (global.get $nf))
  (global.set $ncache_n (i32.add (global.get $ncache_n) (i32.const 1)))
  (i32.store offset=24 (local.get $r) (global.get $ncache_n))
  (local.get $t))

;; a <=> b for numbers: -1, 0, 1, or 2 (not comparable, NaN).
(func $num_cmp (param $a i32) (param $b i32) (result i32)
  (local $ta i32) (local $ia i64) (local $sa i32) (local $fa f64) (local $tb i32)
  (if (i32.eqz (local.tee $ta (call $num (local.get $a)))) (then (return (i32.const 2))))
  (local.set $ia (global.get $ni)) (local.set $sa (global.get $nsc)) (local.set $fa (global.get $nf))
  (if (i32.eqz (local.tee $tb (call $num (local.get $b)))) (then (return (i32.const 2))))
  (if (i32.and (i32.le_u (local.get $ta) (i32.const 2)) (i32.le_u (local.get $tb) (i32.const 2))) (then
    (if (i32.eq (local.get $sa) (global.get $nsc)) (then
      (return (i32.sub (i64.gt_s (local.get $ia) (global.get $ni)) (i64.lt_s (local.get $ia) (global.get $ni))))))))
  (if (i32.or (f64.ne (local.get $fa) (local.get $fa)) (f64.ne (global.get $nf) (global.get $nf))) (then (return (i32.const 2))))
  (i32.sub (f64.gt (local.get $fa) (global.get $nf)) (f64.lt (local.get $fa) (global.get $nf))))

;; --- formatting --------------------------------------------------------------------

;; Write the digits of unsigned v at w; returns the end.
(func $u64_digits (param $w i32) (param $v i64) (result i32)
  (local $q i32) (local $e i32) (local $c i32)
  (local.set $q (local.get $w))
  (loop $digits
    (i32.store8 (local.get $q) (i32.add (i32.wrap_i64 (i64.rem_u (local.get $v) (i64.const 10))) (i32.const '0')))
    (local.set $q (i32.add (local.get $q) (i32.const 1)))
    (br_if $digits (i64.ne (local.tee $v (i64.div_u (local.get $v) (i64.const 10))) (i64.const 0))))
  (local.set $e (local.get $q))
  (loop $rev
    (local.set $q (i32.sub (local.get $q) (i32.const 1)))
    (if (i32.lt_u (local.get $w) (local.get $q)) (then
      (local.set $c (i32.load8_u (local.get $w)))
      (i32.store8 (local.get $w) (i32.load8_u (local.get $q)))
      (i32.store8 (local.get $q) (local.get $c))
      (local.set $w (i32.add (local.get $w) (i32.const 1)))
      (br $rev))))
  (local.get $e))

;; Write signed v at w; returns the end.
(func $i64_text (param $w i32) (param $v i64) (result i32)
  (if (i64.lt_s (local.get $v) (i64.const 0)) (then
    (i32.store8 (local.get $w) (i32.const '-'))
    (return (call $u64_digits (i32.add (local.get $w) (i32.const 1)) (i64.sub (i64.const 0) (local.get $v))))))
  (call $u64_digits (local.get $w) (local.get $v)))

(func $int_term (param $v i64) (result i32)
  (call $int_term_dt (local.get $v) (call $known (i32.const K_INTEGER))))

(func $int_term_dt (param $v i64) (param $dt i32) (result i32)
  (call $typed (global.get $nbuf) (i32.sub (call $i64_text (global.get $nbuf) (local.get $v)) (global.get $nbuf)) (local.get $dt)))

;; A decimal: mantissa m, scale s, written as Oxigraph does: "-1.5", "2" (no
;; trailing zeros, no exponent).
(func $dec_term (param $m i64) (param $s i32) (result i32)
  (local $w i32) (local $p i64) (local $f i64) (local $k i32)
  (loop $trim (if (i32.and (i32.gt_s (local.get $s) (i32.const 0)) (i64.eqz (i64.rem_s (local.get $m) (i64.const 10)))) (then
    (local.set $m (i64.div_s (local.get $m) (i64.const 10)))
    (local.set $s (i32.sub (local.get $s) (i32.const 1))) (br $trim))))
  (local.set $w (global.get $nbuf))
  (if (i64.lt_s (local.get $m) (i64.const 0)) (then
    (i32.store8 (local.get $w) (i32.const '-'))
    (local.set $w (i32.add (local.get $w) (i32.const 1)))
    (local.set $m (i64.sub (i64.const 0) (local.get $m)))))
  (local.set $p (call $i10 (local.get $s)))
  (local.set $w (call $u64_digits (local.get $w) (i64.div_u (local.get $m) (local.get $p))))
  (if (local.get $s) (then
    ;; the fraction, zero-padded to s digits
    (i32.store8 (local.get $w) (i32.const '.'))
    (local.set $w (i32.add (local.get $w) (i32.const 1)))
    (local.set $f (i64.rem_u (local.get $m) (local.get $p)))
    (local.set $k (local.get $s))
    (loop $digits (if (i32.gt_s (local.get $k) (i32.const 0)) (then
      (local.set $k (i32.sub (local.get $k) (i32.const 1)))
      (i32.store8 (i32.add (local.get $w) (local.get $k)) (i32.add (i32.wrap_i64 (i64.rem_u (local.get $f) (i64.const 10))) (i32.const '0')))
      (local.set $f (i64.div_u (local.get $f) (i64.const 10)))
      (br $digits))))
    (local.set $w (i32.add (local.get $w) (local.get $s)))))
  (call $typed (global.get $nbuf) (i32.sub (local.get $w) (global.get $nbuf)) (call $known (i32.const K_DECIMAL))))

;; The shortest digits of x > 0 that read back as x (at float precision when
;; f32): mantissa in $ni (p digits), decimal exponent of the first digit in $nsc.
(func $shortest (param $x f64) (param $f32 i32) (result i32)
  (local $e i32) (local $y f64) (local $p i32) (local $m i64) (local $e2 i32) (local $back f64) (local $max i32)
  (local.set $y (local.get $x))
  (loop $big (if (f64.ge (local.get $y) (f64.const 10)) (then (local.set $y (f64.div (local.get $y) (f64.const 10))) (local.set $e (i32.add (local.get $e) (i32.const 1))) (br $big))))
  (loop $small (if (f64.lt (local.get $y) (f64.const 1)) (then (local.set $y (f64.mul (local.get $y) (f64.const 10))) (local.set $e (i32.sub (local.get $e) (i32.const 1))) (br $small))))
  (local.set $max (select (i32.const 9) (i32.const 17) (local.get $f32)))
  (local.set $p (i32.const 1))
  (loop $precision
    (local.set $e2 (local.get $e))
    (local.set $m (i64.trunc_sat_f64_u (f64.nearest (call $scale10 (local.get $x) (i32.sub (i32.sub (local.get $p) (i32.const 1)) (local.get $e))))))
    (if (i64.ge_u (local.get $m) (call $i10 (local.get $p))) (then
      (local.set $m (i64.div_u (local.get $m) (i64.const 10)))
      (local.set $e2 (i32.add (local.get $e) (i32.const 1)))))
    (local.set $back (call $scale10 (f64.convert_i64_u (local.get $m)) (i32.add (i32.sub (local.get $e2) (local.get $p)) (i32.const 1))))
    (if (i32.eqz (if (result i32) (local.get $f32)
                   (then (f32.eq (f32.demote_f64 (local.get $back)) (f32.demote_f64 (local.get $x))))
                   (else (f64.eq (local.get $back) (local.get $x)))))
      (then
        (if (i32.lt_u (local.get $p) (local.get $max)) (then
          (local.set $p (i32.add (local.get $p) (i32.const 1)))
          (br $precision))))))
  (global.set $ni (local.get $m))
  (global.set $nsc (local.get $e2))
  (local.get $p))

;; A float or double (dt) as Oxigraph writes it: the shortest digits that read
;; back, in plain notation ("1", "2.5", "0.00000015", "1000000000000000000000"),
;; or INF, -INF, NaN.
(func $dbl_term (param $v f64) (param $dt i32) (result i32)
  (local $w i32) (local $p i32) (local $e i32) (local $d i32) (local $i i32) (local $digits i32)
  (local.set $w (global.get $nbuf))
  (if (f64.ne (local.get $v) (local.get $v)) (then (return (call $typed (@str "NaN") (local.get $dt)))))
  (if (f64.eq (local.get $v) (f64.const inf)) (then (return (call $typed (@str "INF") (local.get $dt)))))
  (if (f64.eq (local.get $v) (f64.const -inf)) (then (return (call $typed (@str "-INF") (local.get $dt)))))
  ;; the sign bit, so -0 is "-0"
  (if (i64.lt_s (i64.reinterpret_f64 (local.get $v)) (i64.const 0)) (then
    (i32.store8 (local.get $w) (i32.const '-'))
    (local.set $w (i32.add (local.get $w) (i32.const 1)))
    (local.set $v (f64.neg (local.get $v)))))
  (if (f64.eq (local.get $v) (f64.const 0)) (then
    (i32.store8 (local.get $w) (i32.const '0'))
    (return (call $typed (global.get $nbuf) (i32.sub (i32.add (local.get $w) (i32.const 1)) (global.get $nbuf)) (local.get $dt)))))
  (local.set $p (call $shortest (local.get $v) (i32.eq (call $dtc (local.get $dt)) (i32.const C_FLOAT))))
  (local.set $e (global.get $nsc))
  ;; the p digits, at the end of the buffer
  (local.set $digits (i32.add (global.get $nbuf) (i32.const 370)))
  (drop (call $u64_digits (local.get $digits) (global.get $ni)))
  (if (i32.lt_s (local.get $e) (i32.const 0))
    (then
      ;; 0.000ddd
      (i32.store16 (local.get $w) (i32.const 0x2e30))   ;; "0."
      (local.set $w (i32.add (local.get $w) (i32.const 2)))
      (local.set $i (i32.const -1))
      (loop $zeros (if (i32.gt_s (local.get $i) (local.get $e)) (then
        (i32.store8 (local.get $w) (i32.const '0'))
        (local.set $w (i32.add (local.get $w) (i32.const 1)))
        (local.set $i (i32.sub (local.get $i) (i32.const 1)))
        (br $zeros))))
      (local.set $w (call $put (local.get $w) (local.get $digits) (local.get $p))))
    (else
      ;; ddd[000][.ddd]
      (local.set $i (i32.const 0))
      (loop $int (if (i32.le_s (local.get $i) (local.get $e)) (then
        (i32.store8 (local.get $w) (select (i32.load8_u (i32.add (local.get $digits) (local.get $i))) (i32.const '0') (i32.lt_s (local.get $i) (local.get $p))))
        (local.set $w (i32.add (local.get $w) (i32.const 1)))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $int))))
      (if (i32.lt_s (local.get $i) (local.get $p)) (then
        (i32.store8 (local.get $w) (i32.const '.'))
        (local.set $w (call $put (i32.add (local.get $w) (i32.const 1)) (i32.add (local.get $digits) (local.get $i)) (i32.sub (local.get $p) (local.get $i))))))))
  (call $typed (global.get $nbuf) (i32.sub (local.get $w) (global.get $nbuf)) (local.get $dt)))

;; --- arithmetic --------------------------------------------------------------------

;; 128-bit magnitudes for decimal products and quotients: ($wh, $wl).
(global $wh (mut i64) (i64.const 0))
(global $wl (mut i64) (i64.const 0))

;; ($wh, $wl) = a * b, both unsigned.
(func $wmul (param $a i64) (param $b i64)
  (local $a0 i64) (local $a1 i64) (local $b0 i64) (local $b1 i64) (local $p01 i64) (local $p10 i64) (local $mid i64) (local $p00 i64)
  (local.set $a0 (i64.and (local.get $a) (i64.const 0xffffffff))) (local.set $a1 (i64.shr_u (local.get $a) (i64.const 32)))
  (local.set $b0 (i64.and (local.get $b) (i64.const 0xffffffff))) (local.set $b1 (i64.shr_u (local.get $b) (i64.const 32)))
  (local.set $p00 (i64.mul (local.get $a0) (local.get $b0)))
  (local.set $p01 (i64.mul (local.get $a0) (local.get $b1)))
  (local.set $p10 (i64.mul (local.get $a1) (local.get $b0)))
  (local.set $mid (i64.add (i64.add (i64.shr_u (local.get $p00) (i64.const 32)) (i64.and (local.get $p01) (i64.const 0xffffffff))) (i64.and (local.get $p10) (i64.const 0xffffffff))))
  (global.set $wl (i64.or (i64.and (local.get $p00) (i64.const 0xffffffff)) (i64.shl (local.get $mid) (i64.const 32))))
  (global.set $wh (i64.add (i64.add (i64.mul (local.get $a1) (local.get $b1)) (i64.shr_u (local.get $mid) (i64.const 32)))
                           (i64.add (i64.shr_u (local.get $p01) (i64.const 32)) (i64.shr_u (local.get $p10) (i64.const 32))))))

;; ($wh, $wl) /= 10, truncating.
(func $wdiv10
  (local $r i64) (local $x i64) (local $q1 i64)
  (local.set $r (i64.rem_u (global.get $wh) (i64.const 10)))
  (global.set $wh (i64.div_u (global.get $wh) (i64.const 10)))
  (local.set $x (i64.or (i64.shl (local.get $r) (i64.const 32)) (i64.shr_u (global.get $wl) (i64.const 32))))
  (local.set $q1 (i64.div_u (local.get $x) (i64.const 10)))
  (local.set $x (i64.or (i64.shl (i64.rem_u (local.get $x) (i64.const 10)) (i64.const 32)) (i64.and (global.get $wl) (i64.const 0xffffffff))))
  (global.set $wl (i64.or (i64.shl (local.get $q1) (i64.const 32)) (i64.div_u (local.get $x) (i64.const 10)))))

;; The decimal ±($wh, $wl) / 10^s, truncated to 18 fraction digits and to a
;; mantissa that fits; 0 when the integer part does not.
(func $dec_wide (param $neg i32) (param $s i32) (result i32)
  (loop $fit
    (if (i32.gt_s (local.get $s) (i32.const 0)) (then
      (if (i32.or (i32.gt_s (local.get $s) (i32.const 18))
                  (i32.or (i64.ne (global.get $wh) (i64.const 0)) (i64.lt_s (global.get $wl) (i64.const 0)))) (then
        (call $wdiv10)
        (local.set $s (i32.sub (local.get $s) (i32.const 1)))
        (br $fit))))))
  (if (i32.or (i64.ne (global.get $wh) (i64.const 0)) (i64.lt_s (global.get $wl) (i64.const 0))) (then (return (i32.const 0))))
  (call $dec_term (select (i64.sub (i64.const 0) (global.get $wl)) (global.get $wl) (local.get $neg)) (local.get $s)))

(func $dec_mul (param $a i64) (param $sa i32) (param $b i64) (param $sb i32) (result i32)
  (local $neg i32)
  (local.set $neg (i32.xor (i64.lt_s (local.get $a) (i64.const 0)) (i64.lt_s (local.get $b) (i64.const 0))))
  (call $wmul (call $abs64 (local.get $a)) (call $abs64 (local.get $b)))
  (call $dec_wide (local.get $neg) (i32.add (local.get $sa) (local.get $sb))))

(func $abs64 (param $x i64) (result i64)
  (select (i64.sub (i64.const 0) (local.get $x)) (local.get $x) (i64.lt_s (local.get $x) (i64.const 0))))

;; a/10^sa ÷ b/10^sb = (|a| ÷ |b|) / 10^(sa-sb): long division to 18 fraction digits.
(func $dec_div (param $a i64) (param $sa i32) (param $b i64) (param $sb i32) (result i32)
  (local $x i64) (local $y i64) (local $neg i32) (local $r i64) (local $s i32)
  (if (i64.eqz (local.get $b)) (then (return (i32.const 0))))
  (local.set $neg (i32.xor (i64.lt_s (local.get $a) (i64.const 0)) (i64.lt_s (local.get $b) (i64.const 0))))
  (local.set $x (call $abs64 (local.get $a)))
  (local.set $y (call $abs64 (local.get $b)))
  (global.set $wh (i64.const 0))
  (global.set $wl (i64.div_u (local.get $x) (local.get $y)))
  (local.set $r (i64.rem_u (local.get $x) (local.get $y)))
  (local.set $s (i32.sub (local.get $sa) (local.get $sb)))
  ;; a negative scale: multiply out
  (loop $up (if (i32.lt_s (local.get $s) (i32.const 0)) (then
    (if (i32.or (i64.ne (global.get $wh) (i64.const 0)) (i64.lt_s (global.get $wl) (i64.const 0))) (then (return (i32.const 0))))
    (call $wmul (global.get $wl) (i64.const 10))
    (local.set $r (i64.mul (local.get $r) (i64.const 10)))
    (global.set $wl (i64.add (global.get $wl) (i64.div_u (local.get $r) (local.get $y))))
    (local.set $r (i64.rem_u (local.get $r) (local.get $y)))
    (local.set $s (i32.add (local.get $s) (i32.const 1)))
    (br $up))))
  (loop $digits
    (if (i64.ne (local.get $r) (i64.const 0)) (then
      (if (i32.lt_s (local.get $s) (i32.const 18)) (then
        (if (i64.lt_u (global.get $wl) (i64.const 922337203685477580)) (then
          (local.set $r (i64.mul (local.get $r) (i64.const 10)))
          (global.set $wl (i64.add (i64.mul (global.get $wl) (i64.const 10)) (i64.div_u (local.get $r) (local.get $y))))
          (local.set $r (i64.rem_u (local.get $r) (local.get $y)))
          (local.set $s (i32.add (local.get $s) (i32.const 1)))
          (br $digits))))))))
  (call $dec_wide (local.get $neg) (local.get $s)))

;; a op b for + - * / with numeric type promotion.
(func $arith (param $op i32) (param $a i32) (param $b i32) (result i32)
  (local $ta i32) (local $ia i64) (local $sa i32) (local $fa f64) (local $tb i32) (local $ib i64) (local $sb i32) (local $fb f64)
  (local $t i32) (local $s i32) (local $r f64)
  (if (i32.eqz (local.tee $ta (call $num (local.get $a)))) (then (return (i32.const 0))))
  (local.set $ia (global.get $ni)) (local.set $sa (global.get $nsc)) (local.set $fa (global.get $nf))
  (if (i32.eqz (local.tee $tb (call $num (local.get $b)))) (then (return (i32.const 0))))
  (local.set $ib (global.get $ni)) (local.set $sb (global.get $nsc)) (local.set $fb (global.get $nf))
  (local.set $t (select (local.get $ta) (local.get $tb) (i32.gt_u (local.get $ta) (local.get $tb))))
  (if (i32.le_u (local.get $t) (i32.const 2)) (then
    (if (i32.eq (local.get $op) (i32.const F_DIV)) (then (return (call $dec_div (local.get $ia) (local.get $sa) (local.get $ib) (local.get $sb)))))
    (if (i32.eq (local.get $op) (i32.const F_MUL)) (then
      (if (i32.eq (local.get $t) (i32.const 1)) (then (return (call $int_term (i64.mul (local.get $ia) (local.get $ib))))))
      (return (call $dec_mul (local.get $ia) (local.get $sa) (local.get $ib) (local.get $sb)))))
    ;; + and -: align the scales
    (local.set $s (select (local.get $sa) (local.get $sb) (i32.gt_u (local.get $sa) (local.get $sb))))
    (local.set $ia (i64.mul (local.get $ia) (call $i10 (i32.sub (local.get $s) (local.get $sa)))))
    (local.set $ib (i64.mul (local.get $ib) (call $i10 (i32.sub (local.get $s) (local.get $sb)))))
    (local.set $ia (if (result i64) (i32.eq (local.get $op) (i32.const F_ADD)) (then (i64.add (local.get $ia) (local.get $ib))) (else (i64.sub (local.get $ia) (local.get $ib)))))
    (if (i32.eq (local.get $t) (i32.const 1)) (then (return (call $int_term (local.get $ia)))))
    (return (call $dec_term (local.get $ia) (local.get $s)))))
  (local.set $r
    (if (result f64) (i32.eq (local.get $op) (i32.const F_ADD)) (then (f64.add (local.get $fa) (local.get $fb)))
    (else (if (result f64) (i32.eq (local.get $op) (i32.const F_SUB)) (then (f64.sub (local.get $fa) (local.get $fb)))
    (else (if (result f64) (i32.eq (local.get $op) (i32.const F_MUL)) (then (f64.mul (local.get $fa) (local.get $fb)))
    (else (f64.div (local.get $fa) (local.get $fb)))))))))
  (if (i32.eq (local.get $t) (i32.const 3)) (then
    (return (call $dbl_term (f64.promote_f32 (f32.demote_f64 (local.get $r))) (call $known_float)))))
  (call $dbl_term (local.get $r) (call $known (i32.const K_DOUBLE))))

(global $t_float (mut i32) (i32.const 0))
(func $known_float (result i32)
  (if (i32.eqz (global.get $t_float)) (then (global.set $t_float (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#float")))))
  (global.get $t_float))

;; ABS, CEIL, FLOOR, ROUND (round half up).
(func $round (param $f i32) (param $a i32) (result i32)
  (local $t i32) (local $m i64) (local $s i32) (local $p i64) (local $q i64) (local $x f64) (local $dt i32)
  (if (i32.eqz (local.tee $t (call $num (local.get $a)))) (then (return (i32.const 0))))
  (if (i32.eq (local.get $t) (i32.const 1)) (then
    (if (i32.and (i32.eq (local.get $f) (i32.const F_ABS)) (i64.lt_s (global.get $ni) (i64.const 0)))
      (then (return (call $int_term_dt (i64.sub (i64.const 0) (global.get $ni)) (call $tdt (local.get $a))))))
    (return (local.get $a))))
  (if (i32.eq (local.get $t) (i32.const 2)) (then
    (local.set $m (global.get $ni))
    (local.set $s (global.get $nsc))
    (if (i32.eq (local.get $f) (i32.const F_ABS)) (then
      (return (call $dec_term (select (i64.sub (i64.const 0) (local.get $m)) (local.get $m) (i64.lt_s (local.get $m) (i64.const 0))) (local.get $s)))))
    (local.set $p (call $i10 (local.get $s)))
    (if (i32.eq (local.get $f) (i32.const F_ROUND)) (then (local.set $m (i64.add (local.get $m) (i64.div_s (local.get $p) (i64.const 2))))))
    ;; floor division by 10^s
    (local.set $q (i64.div_s (local.get $m) (local.get $p)))
    (if (i32.and (i64.ne (i64.rem_s (local.get $m) (local.get $p)) (i64.const 0)) (i64.lt_s (local.get $m) (i64.const 0)))
      (then (local.set $q (i64.sub (local.get $q) (i64.const 1)))))
    (if (i32.and (i32.eq (local.get $f) (i32.const F_CEIL)) (i64.ne (i64.rem_s (local.get $m) (local.get $p)) (i64.const 0)))
      (then (local.set $q (i64.add (local.get $q) (i64.const 1)))))
    (return (call $dec_term (local.get $q) (i32.const 0)))))
  (local.set $x (global.get $nf))
  (local.set $x
    (if (result f64) (i32.eq (local.get $f) (i32.const F_ABS)) (then (f64.abs (local.get $x)))
    (else (if (result f64) (i32.eq (local.get $f) (i32.const F_CEIL)) (then (f64.ceil (local.get $x)))
    (else (if (result f64) (i32.eq (local.get $f) (i32.const F_FLOOR)) (then (f64.floor (local.get $x)))
    (else (f64.floor (f64.add (local.get $x) (f64.const 0.5))))))))))
  (call $dbl_term (local.get $x) (call $tdt (local.get $a))))

;; --- casts -----------------------------------------------------------------------

(func $cast (param $v i32) (param $dt i32) (result i32)
  (local $tc i32) (local $k i32) (local $t i32) (local $p i32) (local $n i32) (local $b i32) (local $sc i32) (local $m i64)
  (local.set $tc (call $dtc (local.get $dt)))
  ;; to a date, time or duration (temporal.wat)
  (if (local.tee $k (call $tkind (local.get $dt))) (then (return (call $temporal_cast (local.get $k) (local.get $v)))))
  (local.set $k (call $kind (local.get $v)))
  (if (i32.eq (local.get $tc) (i32.const C_STRING)) (then
    (if (i32.gt_u (i32.sub (local.get $k) (i32.const 1)) (i32.const 2)) (then (return (i32.const 0))))
    (if (i32.eq (local.get $k) (i32.const 2)) (then (return (i32.const 0))))
    ;; a number, boolean, date, time or duration is written in its canonical form:
    ;; the value cast to its own datatype ("1.0"^^xsd:decimal -> "1", "0"^^xsd:boolean -> "false")
    (if (i32.eq (local.get $k) (i32.const 3)) (then
      (local.set $t (call $tdt (local.get $v)))
      ;; an ill-formed number ("abc"^^xsd:integer) cannot be cast
      (if (i32.and (i32.le_u (i32.sub (call $dtc (local.get $t)) (i32.const C_INTEGER)) (i32.const 3)) (i32.eqz (call $num (local.get $v))))
        (then (return (i32.const 0))))
      (if (i32.or (i32.or (call $tkind (local.get $t)) (i32.eq (call $dtc (local.get $t)) (i32.const C_BOOLEAN))) (call $num (local.get $v))) (then
        ;; an ill-formed one ("maybe"^^xsd:boolean) cannot be cast
        (if (i32.eqz (local.tee $v (call $cast (local.get $v) (local.get $t)))) (then (return (i32.const 0))))))))
    (return (call $simple (call $tval (local.get $v)) (call $tlen (local.get $v))))))
  (if (i32.ne (local.get $k) (i32.const 3)) (then (return (i32.const 0))))
  (local.set $p (call $tval (local.get $v)))
  (local.set $n (call $tlen (local.get $v)))
  (local.set $t (call $num (local.get $v)))
  (if (i32.and (i32.eqz (local.get $t)) (i32.eq (call $dtc (call $tdt (local.get $v))) (i32.const C_BOOLEAN))) (then
    (if (i32.lt_s (local.tee $b (call $bool_value (local.get $v))) (i32.const 0)) (then (return (i32.const 0))))
    (if (i32.eq (local.get $tc) (i32.const C_BOOLEAN)) (then (return (call $bool (local.get $b)))))
    (if (i32.eq (local.get $tc) (i32.const C_INTEGER)) (then (return (call $int_term_dt (i64.extend_i32_u (local.get $b)) (local.get $dt)))))
    (if (i32.eq (local.get $tc) (i32.const C_DECIMAL)) (then (return (call $dec_term (i64.extend_i32_u (local.get $b)) (i32.const 0)))))
    (if (i32.ge_u (local.get $tc) (i32.const C_FLOAT)) (then
      (if (i32.le_u (local.get $tc) (i32.const C_DOUBLE)) (then (return (call $dbl_term (f64.convert_i32_u (local.get $b)) (local.get $dt)))))))
    (return (i32.const 0))))
  (if (i32.and (i32.eqz (local.get $t)) (i32.eqz (call $is_plain (local.get $v)))) (then
    ;; other datatypes: only to themselves (dateTime)
    (if (i32.and (i32.eq (local.get $tc) (i32.const C_DATETIME)) (i32.eq (call $dtc (call $tdt (local.get $v))) (i32.const C_DATETIME)))
      (then (return (local.get $v))))
    (return (i32.const 0))))
  (if (i32.eqz (local.get $t)) (then
    ;; from a string: parse it as the target type
    (if (i32.eq (local.get $tc) (i32.const C_BOOLEAN)) (then
      (if (i32.or (call $same (local.get $p) (local.get $n) (@str "true") (i32.const 0)) (call $same (local.get $p) (local.get $n) (@str "1") (i32.const 0)))
        (then (return (call $bool (i32.const 1)))))
      (if (i32.or (call $same (local.get $p) (local.get $n) (@str "false") (i32.const 0)) (call $same (local.get $p) (local.get $n) (@str "0") (i32.const 0)))
        (then (return (call $bool (i32.const 0)))))
      (return (i32.const 0))))
    (if (i32.eq (local.get $tc) (i32.const C_DATETIME)) (then
      (if (i32.eqz (call $date_parse (local.get $p) (local.get $n))) (then (return (i32.const 0))))
      (return (call $typed (local.get $p) (local.get $n) (local.get $dt)))))
    (if (i32.gt_u (i32.sub (local.get $tc) (i32.const C_INTEGER)) (i32.const 3)) (then (return (i32.const 0))))
    (global.set $nsc (i32.const 0))
    (global.set $ni (i64.const 0))
    (if (i32.eqz (call $parse_num (local.get $p) (local.get $n) (i32.sub (local.get $tc) (i32.const 1)))) (then (return (i32.const 0))))
    (local.set $t (i32.sub (local.get $tc) (i32.const 1)))))
  ;; from a number (in $ni/$nsc/$nf, type t)
  (if (i32.eq (local.get $tc) (i32.const C_BOOLEAN)) (then
    (return (call $bool (i32.and (f64.ne (global.get $nf) (f64.const 0)) (f64.eq (global.get $nf) (global.get $nf)))))))
  (if (i32.eq (local.get $tc) (i32.const C_INTEGER)) (then
    (if (i32.eq (local.get $t) (i32.const 2)) (then (return (call $int_term_dt (i64.div_s (global.get $ni) (call $i10 (global.get $nsc))) (local.get $dt)))))
    (if (i32.ge_u (local.get $t) (i32.const 3)) (then
      (if (i32.or (f64.ne (global.get $nf) (global.get $nf)) (f64.gt (f64.abs (global.get $nf)) (f64.const 9e18))) (then (return (i32.const 0))))
      (return (call $int_term_dt (i64.trunc_f64_s (f64.trunc (global.get $nf))) (local.get $dt)))))
    (return (call $int_term_dt (global.get $ni) (local.get $dt)))))
  (if (i32.eq (local.get $tc) (i32.const C_DECIMAL)) (then
    (if (i32.le_u (local.get $t) (i32.const 2)) (then (return (call $dec_term (global.get $ni) (global.get $nsc)))))
    (if (i32.or (f64.ne (global.get $nf) (global.get $nf)) (f64.gt (f64.abs (global.get $nf)) (f64.const 9e17))) (then (return (i32.const 0))))
    (if (f64.eq (global.get $nf) (f64.const 0)) (then (return (call $dec_term (i64.const 0) (i32.const 0)))))
    ;; the double's shortest digits as a decimal
    (local.set $sc (i32.sub (i32.sub (call $shortest (f64.abs (global.get $nf)) (i32.eq (local.get $t) (i32.const 3))) (i32.const 1)) (global.get $nsc)))
    (local.set $m (global.get $ni))
    (if (i32.lt_s (local.get $sc) (i32.const 0)) (then
      (local.set $m (i64.mul (local.get $m) (call $i10 (i32.sub (i32.const 0) (local.get $sc)))))
      (local.set $sc (i32.const 0))))
    (if (f64.lt (global.get $nf) (f64.const 0)) (then (local.set $m (i64.sub (i64.const 0) (local.get $m)))))
    (return (call $dec_term (local.get $m) (local.get $sc)))))
  (if (i32.eq (local.get $tc) (i32.const C_FLOAT)) (then
    (return (call $dbl_term (f64.promote_f32 (f32.demote_f64 (global.get $nf))) (local.get $dt)))))
  (if (i32.eq (local.get $tc) (i32.const C_DOUBLE)) (then (return (call $dbl_term (global.get $nf) (local.get $dt)))))
  (i32.const 0))

;; --- dates -----------------------------------------------------------------------
;; xsd:dateTime and xsd:date: -?YYYY-MM-DD(Thh:mm:ss(.s+)?)?(Z|(+|-)hh:mm)?

(global $d_year (mut i32) (i32.const 0))
(global $d_month (mut i32) (i32.const 0))
(global $d_day (mut i32) (i32.const 0))
(global $d_hour (mut i32) (i32.const 0))
(global $d_min (mut i32) (i32.const 0))
(global $d_sec (mut i32) (i32.const 0))    ;; start of the seconds text
(global $d_sec_len (mut i32) (i32.const 0))
(global $d_tz (mut i32) (i32.const 0))     ;; start of the timezone text (0: none)
(global $d_tz_len (mut i32) (i32.const 0))
(global $d_tzmin (mut i32) (i32.const 0))  ;; offset in minutes

;; n decimal digits at p, or -1.
(func $digits_at (param $p i32) (param $n i32) (result i32)
  (local $v i32) (local $c i32)
  (loop $d (if (local.get $n) (then
    (local.set $c (i32.load8_u (local.get $p)))
    (if (i32.eqz (call $is_digit (local.get $c))) (then (return (i32.const -1))))
    (local.set $v (i32.add (i32.mul (local.get $v) (i32.const 10)) (i32.sub (local.get $c) (i32.const '0'))))
    (local.set $p (i32.add (local.get $p) (i32.const 1)))
    (local.set $n (i32.sub (local.get $n) (i32.const 1)))
    (br $d))))
  (local.get $v))

(func $date_parse (param $p i32) (param $n i32) (result i32)
  (local $e i32) (local $neg i32) (local $q i32) (local $v i32)
  (local.set $e (i32.add (local.get $p) (local.get $n)))
  (global.set $d_hour (i32.const 0)) (global.set $d_min (i32.const 0)) (global.set $d_sec (i32.const 0)) (global.set $d_sec_len (i32.const 0))
  (global.set $d_tz (i32.const 0)) (global.set $d_tz_len (i32.const 0)) (global.set $d_tzmin (i32.const 0))
  (if (i32.and (i32.lt_u (local.get $p) (local.get $e)) (i32.eq (i32.load8_u (local.get $p)) (i32.const '-'))) (then
    (local.set $neg (i32.const 1))
    (local.set $p (i32.add (local.get $p) (i32.const 1)))))
  ;; the year: four or more digits
  (local.set $q (local.get $p))
  (loop $y (if (i32.and (i32.lt_u (local.get $q) (local.get $e)) (call $is_digit (i32.load8_u (local.get $q)))) (then (local.set $q (i32.add (local.get $q) (i32.const 1))) (br $y))))
  (if (i32.or (i32.lt_u (i32.sub (local.get $q) (local.get $p)) (i32.const 4)) (i32.gt_u (i32.sub (local.get $q) (local.get $p)) (i32.const 9))) (then (return (i32.const 0))))
  (global.set $d_year (call $digits_at (local.get $p) (i32.sub (local.get $q) (local.get $p))))
  (if (local.get $neg) (then (global.set $d_year (i32.sub (i32.const 0) (global.get $d_year)))))
  (local.set $p (local.get $q))
  (if (i32.lt_u (i32.sub (local.get $e) (local.get $p)) (i32.const 6)) (then (return (i32.const 0))))
  (if (i32.or (i32.ne (i32.load8_u (local.get $p)) (i32.const '-')) (i32.ne (i32.load8_u offset=3 (local.get $p)) (i32.const '-'))) (then (return (i32.const 0))))
  (global.set $d_month (call $digits_at (i32.add (local.get $p) (i32.const 1)) (i32.const 2)))
  (global.set $d_day (call $digits_at (i32.add (local.get $p) (i32.const 4)) (i32.const 2)))
  (if (i32.or (i32.gt_u (i32.sub (global.get $d_month) (i32.const 1)) (i32.const 11)) (i32.gt_u (i32.sub (global.get $d_day) (i32.const 1)) (i32.const 30)))
    (then (return (i32.const 0))))
  (local.set $p (i32.add (local.get $p) (i32.const 6)))
  (if (i32.and (i32.lt_u (local.get $p) (local.get $e)) (i32.eq (i32.load8_u (local.get $p)) (i32.const 'T'))) (then
    (if (i32.lt_u (i32.sub (local.get $e) (local.get $p)) (i32.const 9)) (then (return (i32.const 0))))
    (if (i32.or (i32.ne (i32.load8_u offset=3 (local.get $p)) (i32.const ':')) (i32.ne (i32.load8_u offset=6 (local.get $p)) (i32.const ':'))) (then (return (i32.const 0))))
    (global.set $d_hour (call $digits_at (i32.add (local.get $p) (i32.const 1)) (i32.const 2)))
    (global.set $d_min (call $digits_at (i32.add (local.get $p) (i32.const 4)) (i32.const 2)))
    (if (i32.or (i32.gt_u (global.get $d_hour) (i32.const 24)) (i32.gt_u (global.get $d_min) (i32.const 59))) (then (return (i32.const 0))))
    (global.set $d_sec (i32.add (local.get $p) (i32.const 7)))
    (if (i32.lt_s (call $digits_at (global.get $d_sec) (i32.const 2)) (i32.const 0)) (then (return (i32.const 0))))
    (local.set $p (i32.add (local.get $p) (i32.const 9)))
    (if (i32.and (i32.lt_u (local.get $p) (local.get $e)) (i32.eq (i32.load8_u (local.get $p)) (i32.const '.'))) (then
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (loop $frac (if (i32.and (i32.lt_u (local.get $p) (local.get $e)) (call $is_digit (i32.load8_u (local.get $p)))) (then (local.set $p (i32.add (local.get $p) (i32.const 1))) (br $frac))))))
    (global.set $d_sec_len (i32.sub (local.get $p) (global.get $d_sec)))))
  (if (i32.lt_u (local.get $p) (local.get $e)) (then
    (global.set $d_tz (local.get $p))
    (global.set $d_tz_len (i32.sub (local.get $e) (local.get $p)))
    (if (i32.eq (i32.load8_u (local.get $p)) (i32.const 'Z'))
      (then (if (i32.ne (global.get $d_tz_len) (i32.const 1)) (then (return (i32.const 0)))))
      (else
        (if (i32.or (i32.ne (global.get $d_tz_len) (i32.const 6)) (i32.ne (i32.load8_u offset=3 (local.get $p)) (i32.const ':'))) (then (return (i32.const 0))))
        (if (i32.eqz (call $one_of (i32.load8_u (local.get $p)) (@str "+-"))) (then (return (i32.const 0))))
        (local.set $v (i32.add (i32.mul (call $digits_at (i32.add (local.get $p) (i32.const 1)) (i32.const 2)) (i32.const 60))
                               (call $digits_at (i32.add (local.get $p) (i32.const 4)) (i32.const 2))))
        (global.set $d_tzmin (select (i32.sub (i32.const 0) (local.get $v)) (local.get $v) (i32.eq (i32.load8_u (local.get $p)) (i32.const '-'))))))))
  (i32.const 1))

;; Days from 1970-01-01 of a civil date.
(func $days_from_civil (param $y i32) (param $m i32) (param $d i32) (result i32)
  (local $era i32) (local $yoe i32) (local $doy i32)
  (local.set $y (i32.sub (local.get $y) (i32.le_u (local.get $m) (i32.const 2))))
  (local.set $era (i32.div_s (select (local.get $y) (i32.sub (local.get $y) (i32.const 399)) (i32.ge_s (local.get $y) (i32.const 0))) (i32.const 400)))
  (local.set $yoe (i32.sub (local.get $y) (i32.mul (local.get $era) (i32.const 400))))
  (local.set $doy (i32.add (i32.div_u (i32.add (i32.mul (i32.const 153) (select (i32.sub (local.get $m) (i32.const 3)) (i32.add (local.get $m) (i32.const 9)) (i32.gt_u (local.get $m) (i32.const 2)))) (i32.const 2)) (i32.const 5))
                           (i32.sub (local.get $d) (i32.const 1))))
  (i32.sub (i32.add (i32.mul (local.get $era) (i32.const 146097))
                    (i32.add (i32.sub (i32.add (i32.mul (local.get $yoe) (i32.const 365)) (i32.div_u (local.get $yoe) (i32.const 4))) (i32.div_u (local.get $yoe) (i32.const 100))) (local.get $doy)))
           (i32.const 719468)))

;; The instant of a parsed dateTime (no timezone: UTC), exactly: whole seconds
;; since the epoch in $dk_s and the fraction of a second in units of 1e-18 in
;; $dk_f, as Rust's scaled decimal.
(global $dk_s (mut i64) (i64.const 0))
(global $dk_f (mut i64) (i64.const 0))
(func $date_key
  (local $sc i64)
  (global.set $dk_f (i64.const 0))
  (global.set $dk_s (i64.add
    (i64.mul (i64.extend_i32_s (call $days_from_civil (global.get $d_year) (global.get $d_month) (global.get $d_day))) (i64.const 86400))
    (i64.extend_i32_s (i32.sub (i32.add (i32.mul (global.get $d_hour) (i32.const 3600)) (i32.mul (global.get $d_min) (i32.const 60)))
                               (i32.mul (global.get $d_tzmin) (i32.const 60))))))
  (if (global.get $d_sec_len) (then
    (global.set $nsc (i32.const 0))
    (global.set $ni (i64.const 0))
    (if (call $parse_num (global.get $d_sec) (global.get $d_sec_len) (i32.const 2)) (then
      (local.set $sc (call $i10 (global.get $nsc)))
      (global.set $dk_s (i64.add (global.get $dk_s) (i64.div_u (global.get $ni) (local.get $sc))))
      (global.set $dk_f (i64.rem_u (global.get $ni) (local.get $sc)))
      (global.set $dk_f (if (result i64) (i32.le_s (global.get $nsc) (i32.const 18))
        (then (i64.mul (global.get $dk_f) (call $i10 (i32.sub (i32.const 18) (global.get $nsc)))))
        (else (i64.div_u (global.get $dk_f) (call $i10 (i32.sub (global.get $nsc) (i32.const 18))))))))))))

;; (s, f) <=> (t, g): -1, 0, 1
(func $key_cmp (param $s i64) (param $f i64) (param $t i64) (param $g i64) (result i32)
  (if (i64.ne (local.get $s) (local.get $t)) (then (return (select (i32.const -1) (i32.const 1) (i64.lt_s (local.get $s) (local.get $t))))))
  (i32.sub (i64.gt_u (local.get $f) (local.get $g)) (i64.lt_u (local.get $f) (local.get $g))))

;; a <=> b for dateTimes (or dates): -1, 0, 1, or 2 (incomparable). A value
;; without a timezone stands for any between -14:00 and +14:00. Instants are
;; parsed once per literal ($num keeps them).
(func $date_cmp (param $a i32) (param $b i32) (result i32)
  (local $xs i64) (local $xf i64) (local $tx i32) (local $w i64)
  (drop (call $num (local.get $a)))
  (if (i32.eqz (i32.and (global.get $nsc) (i32.const 1))) (then (return (i32.const 2))))
  (local.set $xs (global.get $ni))
  (local.set $xf (i64.reinterpret_f64 (global.get $nf)))
  (local.set $tx (i32.shr_u (global.get $nsc) (i32.const 1)))
  (drop (call $num (local.get $b)))
  (if (i32.eqz (i32.and (global.get $nsc) (i32.const 1))) (then (return (i32.const 2))))
  (if (i32.ne (local.get $tx) (i32.shr_u (global.get $nsc) (i32.const 1))) (then
    ;; 14 hours either way
    (local.set $w (i64.const 50400))
    (if (i32.eq (call $key_cmp (local.get $xs) (local.get $xf) (i64.sub (global.get $ni) (local.get $w)) (i64.reinterpret_f64 (global.get $nf))) (i32.const -1))
      (then (return (i32.const -1))))
    (if (i32.eq (call $key_cmp (local.get $xs) (local.get $xf) (i64.add (global.get $ni) (local.get $w)) (i64.reinterpret_f64 (global.get $nf))) (i32.const 1))
      (then (return (i32.const 1))))
    (return (i32.const 2))))
  (call $key_cmp (local.get $xs) (local.get $xf) (global.get $ni) (i64.reinterpret_f64 (global.get $nf))))

;; YEAR ... TZ of a dateTime.
(func $date_part (param $f i32) (param $a i32) (result i32)
  (local $c i32) (local $w i32) (local $h i32) (local $m i32) (local $neg i32)
  (local.set $c (call $dtc (call $tdt (local.get $a))))
  (if (i32.and (i32.ne (local.get $c) (i32.const C_DATETIME)) (i32.ne (local.get $c) (i32.const C_DATE))) (then (return (i32.const 0))))
  (if (i32.eqz (call $date_parse (call $tval (local.get $a)) (call $tlen (local.get $a)))) (then (return (i32.const 0))))
  (if (i32.eq (local.get $f) (i32.const F_YEAR)) (then (return (call $int_term (i64.extend_i32_s (global.get $d_year))))))
  (if (i32.eq (local.get $f) (i32.const F_MONTH)) (then (return (call $int_term (i64.extend_i32_s (global.get $d_month))))))
  (if (i32.eq (local.get $f) (i32.const F_DAY)) (then (return (call $int_term (i64.extend_i32_s (global.get $d_day))))))
  (if (i32.eq (local.get $f) (i32.const F_HOURS)) (then (return (call $int_term (i64.extend_i32_s (global.get $d_hour))))))
  (if (i32.eq (local.get $f) (i32.const F_MINUTES)) (then (return (call $int_term (i64.extend_i32_s (global.get $d_min))))))
  (if (i32.eq (local.get $f) (i32.const F_SECONDS)) (then
    (if (i32.eqz (global.get $d_sec_len)) (then (return (call $dec_term (i64.const 0) (i32.const 0)))))
    (global.set $nsc (i32.const 0))
    (drop (call $parse_num (global.get $d_sec) (global.get $d_sec_len) (i32.const 2)))
    (return (call $dec_term (global.get $ni) (global.get $nsc)))))
  (if (i32.eq (local.get $f) (i32.const F_TZ)) (then (return (call $simple (global.get $d_tz) (global.get $d_tz_len)))))
  ;; TIMEZONE: an xsd:dayTimeDuration
  (if (i32.eqz (global.get $d_tz)) (then (return (i32.const 0))))
  (local.set $w (global.get $nbuf))
  (local.set $m (global.get $d_tzmin))
  (if (i32.lt_s (local.get $m) (i32.const 0)) (then
    (i32.store8 (local.get $w) (i32.const '-'))
    (local.set $w (i32.add (local.get $w) (i32.const 1)))
    (local.set $m (i32.sub (i32.const 0) (local.get $m)))))
  (i32.store16 (local.get $w) (i32.const 0x5450))   ;; "PT"
  (local.set $w (i32.add (local.get $w) (i32.const 2)))
  (local.set $h (i32.div_u (local.get $m) (i32.const 60)))
  (local.set $m (i32.rem_u (local.get $m) (i32.const 60)))
  (if (local.get $h) (then
    (local.set $w (call $utoa (local.get $w) (local.get $h)))
    (i32.store8 (local.get $w) (i32.const 'H'))
    (local.set $w (i32.add (local.get $w) (i32.const 1)))))
  (if (local.get $m) (then
    (local.set $w (call $utoa (local.get $w) (local.get $m)))
    (i32.store8 (local.get $w) (i32.const 'M'))
    (local.set $w (i32.add (local.get $w) (i32.const 1)))))
  (if (i32.eqz (i32.or (local.get $h) (local.get $m))) (then
    (i32.store16 (local.get $w) (i32.const 0x5330))   ;; "0S"
    (local.set $w (i32.add (local.get $w) (i32.const 2)))))
  (call $typed (global.get $nbuf) (i32.sub (local.get $w) (global.get $nbuf))
    (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#dayTimeDuration"))))

;; NOW(): the run's start, as an xsd:dateTime.
(global $now (mut i32) (i32.const 0))
(func $now_term (result i32)
  (local $ms f64) (local $days i32) (local $secs i32) (local $z i32) (local $era i32) (local $doe i32) (local $yoe i32)
  (local $doy i32) (local $mp i32) (local $y i32) (local $m i32) (local $d i32) (local $w i32)
  (if (global.get $now) (then (return (global.get $now))))
  (local.set $ms (call $now_ms))
  (local.set $days (i32.trunc_sat_f64_s (f64.floor (f64.div (local.get $ms) (f64.const 86400000)))))
  (local.set $secs (i32.trunc_sat_f64_s (f64.sub (f64.div (local.get $ms) (f64.const 1000)) (f64.mul (f64.convert_i32_s (local.get $days)) (f64.const 86400)))))
  (local.set $z (i32.add (local.get $days) (i32.const 719468)))
  (local.set $era (i32.div_s (select (local.get $z) (i32.sub (local.get $z) (i32.const 146096)) (i32.ge_s (local.get $z) (i32.const 0))) (i32.const 146097)))
  (local.set $doe (i32.sub (local.get $z) (i32.mul (local.get $era) (i32.const 146097))))
  (local.set $yoe (i32.div_u (i32.sub (i32.add (i32.sub (local.get $doe) (i32.div_u (local.get $doe) (i32.const 1460))) (i32.div_u (local.get $doe) (i32.const 36524)))
                                      (i32.div_u (local.get $doe) (i32.const 146096))) (i32.const 365)))
  (local.set $y (i32.add (local.get $yoe) (i32.mul (local.get $era) (i32.const 400))))
  (local.set $doy (i32.sub (local.get $doe) (i32.sub (i32.add (i32.mul (i32.const 365) (local.get $yoe)) (i32.div_u (local.get $yoe) (i32.const 4))) (i32.div_u (local.get $yoe) (i32.const 100)))))
  (local.set $mp (i32.div_u (i32.add (i32.mul (i32.const 5) (local.get $doy)) (i32.const 2)) (i32.const 153)))
  (local.set $d (i32.add (i32.sub (local.get $doy) (i32.div_u (i32.add (i32.mul (i32.const 153) (local.get $mp)) (i32.const 2)) (i32.const 5))) (i32.const 1)))
  (local.set $m (select (i32.add (local.get $mp) (i32.const 3)) (i32.sub (local.get $mp) (i32.const 9)) (i32.lt_u (local.get $mp) (i32.const 10))))
  (local.set $y (i32.add (local.get $y) (i32.le_u (local.get $m) (i32.const 2))))
  (local.set $w (global.get $nbuf))
  (local.set $w (call $pad (local.get $w) (local.get $y) (i32.const 4)))
  (i32.store8 (local.get $w) (i32.const '-'))
  (local.set $w (call $pad (i32.add (local.get $w) (i32.const 1)) (local.get $m) (i32.const 2)))
  (i32.store8 (local.get $w) (i32.const '-'))
  (local.set $w (call $pad (i32.add (local.get $w) (i32.const 1)) (local.get $d) (i32.const 2)))
  (i32.store8 (local.get $w) (i32.const 'T'))
  (local.set $w (call $pad (i32.add (local.get $w) (i32.const 1)) (i32.div_u (local.get $secs) (i32.const 3600)) (i32.const 2)))
  (i32.store8 (local.get $w) (i32.const ':'))
  (local.set $w (call $pad (i32.add (local.get $w) (i32.const 1)) (i32.rem_u (i32.div_u (local.get $secs) (i32.const 60)) (i32.const 60)) (i32.const 2)))
  (i32.store8 (local.get $w) (i32.const ':'))
  (local.set $w (call $pad (i32.add (local.get $w) (i32.const 1)) (i32.rem_u (local.get $secs) (i32.const 60)) (i32.const 2)))
  (i32.store8 (local.get $w) (i32.const 'Z'))
  (global.set $now (call $typed (global.get $nbuf) (i32.sub (i32.add (local.get $w) (i32.const 1)) (global.get $nbuf))
    (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#dateTime"))))
  (global.get $now))

;; v in decimal, zero-padded to n digits, at w; returns the end.
(func $pad (param $w i32) (param $v i32) (param $n i32) (result i32)
  (local $k i32)
  (local.set $k (local.get $n))
  (loop $d (if (local.get $k) (then
    (local.set $k (i32.sub (local.get $k) (i32.const 1)))
    (i32.store8 (i32.add (local.get $w) (local.get $k)) (i32.add (i32.rem_u (local.get $v) (i32.const 10)) (i32.const '0')))
    (local.set $v (i32.div_u (local.get $v) (i32.const 10)))
    (br $d))))
  (i32.add (local.get $w) (local.get $n)))
