;; Dates, times and durations: XPath's arithmetic and comparisons on them
;; (op:add-dayTimeDuration-to-dateTime, op:subtract-dateTimes,
;; op:multiply-dayTimeDuration, ...). A value without a timezone is taken to be
;; in UTC, the implicit timezone. As in Oxigraph, an xsd:duration may also be
;; added to a dateTime, and durations of different kinds add to an xsd:duration.
;;
;; A moment (dateTime, date, time) is days since the epoch plus microseconds
;; into the day (a time is on 1972-12-31), and a timezone offset in minutes or
;; TZ_NONE. A duration is months plus microseconds, never of opposite signs.
;; Seconds are kept to the microsecond.

(@define TK_DT 1) (@define TK_DATE 2) (@define TK_TIME 3) (@define TK_DUR 4) (@define TK_DTD 5) (@define TK_YMD 6)
(@define TZ_NONE 100000)

;; What $tparse read.
(global $tm_days (mut i32) (i32.const 0))
(global $tm_secs (mut i64) (i64.const 0))
(global $tm_tz (mut i32) (i32.const 0))
(global $tm_months (mut i64) (i64.const 0))

;; The temporal kind (TK_*) of datatype dt, or 0.
(func $tkind (param $dt i32) (result i32)
  (local $p i32) (local $n i32) (local $c i32)
  (local.set $c (call $dtc (local.get $dt)))
  (if (i32.and (i32.ne (local.get $c) (i32.const C_OTHER))
               (i32.and (i32.ne (local.get $c) (i32.const C_DATETIME)) (i32.ne (local.get $c) (i32.const C_DATE))))
    (then (return (i32.const 0))))
  (local.set $n (i32.sub (call $tlen (local.get $dt)) (i32.const 33)))
  (local.set $p (call $tval (local.get $dt)))
  (if (i32.le_s (local.get $n) (i32.const 0)) (then (return (i32.const 0))))
  (if (i32.eqz (call $memeq (local.get $p) (@str "http://www.w3.org/2001/XMLSchema#"))) (then (return (i32.const 0))))
  (local.set $p (i32.add (local.get $p) (i32.const 33)))
  (if (i32.or (call $same (local.get $p) (local.get $n) (@str "dateTime") (i32.const 0))
              (call $same (local.get $p) (local.get $n) (@str "dateTimeStamp") (i32.const 0))) (then (return (i32.const TK_DT))))
  (if (call $same (local.get $p) (local.get $n) (@str "date") (i32.const 0)) (then (return (i32.const TK_DATE))))
  (if (call $same (local.get $p) (local.get $n) (@str "time") (i32.const 0)) (then (return (i32.const TK_TIME))))
  (if (call $same (local.get $p) (local.get $n) (@str "duration") (i32.const 0)) (then (return (i32.const TK_DUR))))
  (if (call $same (local.get $p) (local.get $n) (@str "dayTimeDuration") (i32.const 0)) (then (return (i32.const TK_DTD))))
  (if (call $same (local.get $p) (local.get $n) (@str "yearMonthDuration") (i32.const 0)) (then (return (i32.const TK_YMD))))
  (i32.const 0))

;; The datatype IRI of kind k.
(func $tk_iri (param $k i32) (result i32)
  (if (i32.eq (local.get $k) (i32.const TK_DT)) (then (return (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#dateTime")))))
  (if (i32.eq (local.get $k) (i32.const TK_DATE)) (then (return (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#date")))))
  (if (i32.eq (local.get $k) (i32.const TK_TIME)) (then (return (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#time")))))
  (if (i32.eq (local.get $k) (i32.const TK_DUR)) (then (return (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#duration")))))
  (if (i32.eq (local.get $k) (i32.const TK_DTD)) (then (return (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#dayTimeDuration")))))
  (call $iri_const (@str "http://www.w3.org/2001/XMLSchema#yearMonthDuration")))

(func $days_in (param $y i32) (param $m i32) (result i32)
  (if (i32.eq (local.get $m) (i32.const 2)) (then
    (return (i32.add (i32.const 28)
      (i32.or (i32.and (i32.eqz (i32.rem_s (local.get $y) (i32.const 4))) (i32.ne (i32.rem_s (local.get $y) (i32.const 100)) (i32.const 0)))
              (i32.eqz (i32.rem_s (local.get $y) (i32.const 400))))))))
  (i32.sub (i32.const 31)
    (i32.or (i32.or (i32.eq (local.get $m) (i32.const 4)) (i32.eq (local.get $m) (i32.const 6)))
            (i32.or (i32.eq (local.get $m) (i32.const 9)) (i32.eq (local.get $m) (i32.const 11))))))

;; a decimal mantissa m of scale s, in microseconds (truncated)
(func $to_micro (param $m i64) (param $s i32) (result i64)
  (if (result i64) (i32.le_s (local.get $s) (i32.const 6))
    (then (i64.mul (local.get $m) (call $i10 (i32.sub (i32.const 6) (local.get $s)))))
    (else (i64.div_s (local.get $m) (call $i10 (i32.sub (local.get $s) (i32.const 6)))))))

;; floor(a / b) for b > 0
(func $fdiv (param $a i64) (param $b i64) (result i64)
  (i64.sub (i64.div_s (local.get $a) (local.get $b))
           (i64.extend_i32_u (i32.and (i64.lt_s (local.get $a) (i64.const 0)) (i64.ne (i64.rem_s (local.get $a) (local.get $b)) (i64.const 0))))))

;; Read literal id as a date, time or duration into $tm_*: its kind, or 0 when
;; it is none (or ill-formed).
(func $tparse (param $id i32) (result i32)
  (local $k i32)
  (if (i32.ne (call $kind (local.get $id)) (i32.const 3)) (then (return (i32.const 0))))
  (if (i32.eqz (local.tee $k (call $tkind (call $tdt (local.get $id))))) (then (return (i32.const 0))))
  (call $tparse_text (local.get $k) (call $tval (local.get $id)) (call $tlen (local.get $id))))

;; Read [p, p+n) as a value of kind k into $tm_*: k, or 0 when it is not one.
(func $tparse_text (param $k i32) (param $p i32) (param $n i32) (result i32)
  (local $s i64)
  (if (i32.ge_u (local.get $k) (i32.const TK_DUR)) (then
    (return (select (local.get $k) (i32.const 0) (call $dur_parse (local.get $p) (local.get $n) (local.get $k))))))
  (if (i32.eq (local.get $k) (i32.const TK_TIME)) (then
    (call $xb_reset)
    (call $xb_put (@str "1972-12-31T"))
    (call $xb_put (local.get $p) (local.get $n))
    (local.set $p (global.get $xb))
    (local.set $n (global.get $xb_len))))
  (if (i32.eqz (call $date_parse (local.get $p) (local.get $n))) (then (return (i32.const 0))))
  ;; a time of day exactly when not a date
  (if (i32.ne (i32.eqz (global.get $d_sec)) (i32.eq (local.get $k) (i32.const TK_DATE))) (then (return (i32.const 0))))
  (if (global.get $d_sec_len) (then
    (global.set $nsc (i32.const 0))
    (global.set $ni (i64.const 0))
    (if (i32.eqz (call $parse_num (global.get $d_sec) (global.get $d_sec_len) (i32.const 2))) (then (return (i32.const 0))))
    (local.set $s (call $to_micro (global.get $ni) (global.get $nsc)))))
  (if (i32.or (i64.ge_s (local.get $s) (i64.const 60000000))
              (i32.gt_u (global.get $d_day) (call $days_in (global.get $d_year) (global.get $d_month)))) (then (return (i32.const 0))))
  (if (i32.and (i32.eq (global.get $d_hour) (i32.const 24))
               (i32.or (i32.ne (global.get $d_min) (i32.const 0)) (i64.ne (local.get $s) (i64.const 0)))) (then (return (i32.const 0))))
  (global.set $tm_days (call $days_from_civil (global.get $d_year) (global.get $d_month) (global.get $d_day)))
  (local.set $s (i64.add (local.get $s)
    (i64.mul (i64.extend_i32_u (i32.add (i32.mul (global.get $d_hour) (i32.const 3600)) (i32.mul (global.get $d_min) (i32.const 60))))
             (i64.const 1000000))))
  ;; 24:00:00 is the start of the next day
  (if (i64.ge_s (local.get $s) (i64.const 86400000000)) (then
    (global.set $tm_days (i32.add (global.get $tm_days) (i32.const 1)))
    (local.set $s (i64.sub (local.get $s) (i64.const 86400000000)))))
  (global.set $tm_secs (local.get $s))
  (global.set $tm_tz (select (global.get $d_tzmin) (i32.const TZ_NONE) (global.get $d_tz)))
  (local.get $k))

;; -?P(nY)?(nM)?(nD)?(T(nH)?(nM)?(n(.n)?S)?)? into $tm_months and $tm_secs;
;; whether it is valid for kind k.
(func $dur_parse (param $p i32) (param $n i32) (param $k i32) (result i32)
  (local $e i32) (local $neg i32) (local $time i32) (local $last i32) (local $any i32) (local $q i32) (local $c i32)
  (local $rank i32) (local $dot i32) (local $u i64) (local $v i64) (local $months i64) (local $secs i64)
  (local.set $e (i32.add (local.get $p) (local.get $n)))
  (if (i32.and (i32.lt_u (local.get $p) (local.get $e)) (i32.eq (i32.load8_u (local.get $p)) (i32.const '-'))) (then
    (local.set $neg (i32.const 1))
    (local.set $p (i32.add (local.get $p) (i32.const 1)))))
  (if (i32.or (i32.ge_u (local.get $p) (local.get $e)) (i32.ne (i32.load8_u (local.get $p)) (i32.const 'P'))) (then (return (i32.const 0))))
  (local.set $p (i32.add (local.get $p) (i32.const 1)))
  (block $bad
    (loop $parts
      (if (i32.lt_u (local.get $p) (local.get $e)) (then
        (if (i32.eq (i32.load8_u (local.get $p)) (i32.const 'T')) (then
          (br_if $bad (i32.or (local.get $time) (i32.eq (i32.add (local.get $p) (i32.const 1)) (local.get $e))))
          (local.set $time (i32.const 1))
          (local.set $p (i32.add (local.get $p) (i32.const 1)))
          (br $parts)))
        ;; a number, then its unit
        (local.set $q (local.get $p))
        (local.set $dot (i32.const 0))
        (loop $digits (if (i32.lt_u (local.get $q) (local.get $e)) (then
          (local.set $c (i32.load8_u (local.get $q)))
          (if (i32.or (call $is_digit (local.get $c)) (i32.eq (local.get $c) (i32.const '.'))) (then
            (local.set $dot (i32.or (local.get $dot) (i32.eq (local.get $c) (i32.const '.'))))
            (local.set $q (i32.add (local.get $q) (i32.const 1)))
            (br $digits))))))
        (br_if $bad (i32.or (i32.eq (local.get $q) (local.get $p)) (i32.ge_u (local.get $q) (local.get $e))))
        (br_if $bad (i32.gt_u (i32.sub (local.get $q) (local.get $p)) (i32.const 18)))
        (local.set $c (i32.load8_u (local.get $q)))
        (local.set $rank (i32.const 0))
        (if (local.get $time)
          (then
            (if (i32.eq (local.get $c) (i32.const 'H')) (then (local.set $rank (i32.const 4))))
            (if (i32.eq (local.get $c) (i32.const 'M')) (then (local.set $rank (i32.const 5))))
            (if (i32.eq (local.get $c) (i32.const 'S')) (then (local.set $rank (i32.const 6)))))
          (else
            (if (i32.eq (local.get $c) (i32.const 'Y')) (then (local.set $rank (i32.const 1))))
            (if (i32.eq (local.get $c) (i32.const 'M')) (then (local.set $rank (i32.const 2))))
            (if (i32.eq (local.get $c) (i32.const 'D')) (then (local.set $rank (i32.const 3))))))
        (br_if $bad (i32.or (i32.eqz (local.get $rank)) (i32.le_u (local.get $rank) (local.get $last))))
        (br_if $bad (i32.and (local.get $dot) (i32.ne (local.get $rank) (i32.const 6))))
        (local.set $last (local.get $rank))
        (local.set $any (i32.const 1))
        (global.set $nsc (i32.const 0))
        (global.set $ni (i64.const 0))
        (br_if $bad (i32.eqz (call $parse_num (local.get $p) (i32.sub (local.get $q) (local.get $p)) (select (i32.const 2) (i32.const 1) (local.get $dot)))))
        ;; months, or microseconds, per unit; each part well within an i64
        (local.set $u (i64.load (i32.add (@words64 0 12 1 86400000000 3600000000 60000000 1000000) (i32.shl (local.get $rank) (i32.const 3)))))
        (br_if $bad (f64.ge (f64.mul (global.get $nf) (f64.convert_i64_u (local.get $u))) (f64.const 1e18)))
        (local.set $v (if (result i64) (i32.eq (local.get $rank) (i32.const 6))
          (then (call $to_micro (global.get $ni) (global.get $nsc)))
          (else (i64.mul (global.get $ni) (local.get $u)))))
        (if (i32.le_u (local.get $rank) (i32.const 2))
          (then (local.set $months (i64.add (local.get $months) (local.get $v))))
          (else (local.set $secs (i64.add (local.get $secs) (local.get $v)))))
        (local.set $p (i32.add (local.get $q) (i32.const 1)))
        (br $parts))))
    (br_if $bad (i32.eqz (local.get $any)))
    (br_if $bad (i32.and (i32.eq (local.get $k) (i32.const TK_YMD)) (i64.ne (local.get $secs) (i64.const 0))))
    (br_if $bad (i32.and (i32.eq (local.get $k) (i32.const TK_DTD)) (i64.ne (local.get $months) (i64.const 0))))
    (if (local.get $neg) (then
      (local.set $months (i64.sub (i64.const 0) (local.get $months)))
      (local.set $secs (i64.sub (i64.const 0) (local.get $secs)))))
    (global.set $tm_months (local.get $months))
    (global.set $tm_secs (local.get $secs))
    (return (i32.const 1)))
  (i32.const 0))

;; --- writing -------------------------------------------------------------------

;; The civil date of days since the epoch, in $cv_year, $cv_month, $cv_day.
(global $cv_year (mut i32) (i32.const 0))
(global $cv_month (mut i32) (i32.const 0))
(global $cv_day (mut i32) (i32.const 0))
(func $civil (param $days i32)
  (local $z i32) (local $era i32) (local $doe i32) (local $yoe i32) (local $doy i32) (local $mp i32) (local $m i32)
  (local.set $z (i32.add (local.get $days) (i32.const 719468)))
  (local.set $era (i32.div_s (select (local.get $z) (i32.sub (local.get $z) (i32.const 146096)) (i32.ge_s (local.get $z) (i32.const 0))) (i32.const 146097)))
  (local.set $doe (i32.sub (local.get $z) (i32.mul (local.get $era) (i32.const 146097))))
  (local.set $yoe (i32.div_u (i32.sub (i32.add (i32.sub (local.get $doe) (i32.div_u (local.get $doe) (i32.const 1460))) (i32.div_u (local.get $doe) (i32.const 36524)))
                                      (i32.div_u (local.get $doe) (i32.const 146096))) (i32.const 365)))
  (local.set $doy (i32.sub (local.get $doe) (i32.sub (i32.add (i32.mul (i32.const 365) (local.get $yoe)) (i32.div_u (local.get $yoe) (i32.const 4))) (i32.div_u (local.get $yoe) (i32.const 100)))))
  (local.set $mp (i32.div_u (i32.add (i32.mul (i32.const 5) (local.get $doy)) (i32.const 2)) (i32.const 153)))
  (global.set $cv_day (i32.add (i32.sub (local.get $doy) (i32.div_u (i32.add (i32.mul (i32.const 153) (local.get $mp)) (i32.const 2)) (i32.const 5))) (i32.const 1)))
  (local.set $m (select (i32.add (local.get $mp) (i32.const 3)) (i32.sub (local.get $mp) (i32.const 9)) (i32.lt_u (local.get $mp) (i32.const 10))))
  (global.set $cv_month (local.get $m))
  (global.set $cv_year (i32.add (i32.add (local.get $yoe) (i32.mul (local.get $era) (i32.const 400))) (i32.le_u (local.get $m) (i32.const 2)))))

;; Unsigned v in decimal; v in two digits; v followed by unit c unless 0.
(func $xb_num (param $v i64)
  (call $xb_room (i32.const 24))
  (global.set $xb_len (i32.sub (call $u64_digits (i32.add (global.get $xb) (global.get $xb_len)) (local.get $v)) (global.get $xb))))
(func $xb_2 (param $v i32)
  (call $xb_byte (i32.add (i32.div_u (local.get $v) (i32.const 10)) (i32.const '0')))
  (call $xb_byte (i32.add (i32.rem_u (local.get $v) (i32.const 10)) (i32.const '0'))))
(func $xb_part (param $v i64) (param $c i32)
  (if (i64.ne (local.get $v) (i64.const 0)) (then
    (call $xb_num (local.get $v))
    (call $xb_byte (local.get $c)))))

;; ".ddd" of f microseconds (0 <= f < 1e6), trailing zeros trimmed; nothing for 0.
(func $xb_frac (param $f i32)
  (local $k i32)
  (if (i32.eqz (local.get $f)) (then (return)))
  (call $xb_byte (i32.const '.'))
  (local.set $k (i32.const 100000))
  (loop $d (if (local.get $f) (then
    (call $xb_byte (i32.add (i32.div_u (local.get $f) (local.get $k)) (i32.const '0')))
    (local.set $f (i32.rem_u (local.get $f) (local.get $k)))
    (local.set $k (i32.div_u (local.get $k) (i32.const 10)))
    (br $d)))))

;; A moment of kind k as a literal: "2026-02-01T00:00:01.5Z", "2026-02-28", "00:15:00".
(func $moment_term (param $k i32) (param $days i32) (param $secs i64) (param $tz i32) (result i32)
  (local $dt i32) (local $y i32) (local $s i32)
  (local.set $dt (call $tk_iri (local.get $k)))
  (call $xb_reset)
  (if (i32.ne (local.get $k) (i32.const TK_TIME)) (then
    (call $civil (local.get $days))
    (local.set $y (global.get $cv_year))
    (if (i32.lt_s (local.get $y) (i32.const 0)) (then
      (call $xb_byte (i32.const '-'))
      (local.set $y (i32.sub (i32.const 0) (local.get $y)))))
    (if (i32.lt_u (local.get $y) (i32.const 1000)) (then (call $xb_byte (i32.const '0'))))
    (if (i32.lt_u (local.get $y) (i32.const 100)) (then (call $xb_byte (i32.const '0'))))
    (if (i32.lt_u (local.get $y) (i32.const 10)) (then (call $xb_byte (i32.const '0'))))
    (call $xb_num (i64.extend_i32_u (local.get $y)))
    (call $xb_byte (i32.const '-'))
    (call $xb_2 (global.get $cv_month))
    (call $xb_byte (i32.const '-'))
    (call $xb_2 (global.get $cv_day))))
  (if (i32.eq (local.get $k) (i32.const TK_DT)) (then (call $xb_byte (i32.const 'T'))))
  (if (i32.ne (local.get $k) (i32.const TK_DATE)) (then
    (local.set $s (i32.wrap_i64 (i64.div_u (local.get $secs) (i64.const 1000000))))
    (call $xb_2 (i32.div_u (local.get $s) (i32.const 3600)))
    (call $xb_byte (i32.const ':'))
    (call $xb_2 (i32.rem_u (i32.div_u (local.get $s) (i32.const 60)) (i32.const 60)))
    (call $xb_byte (i32.const ':'))
    (call $xb_2 (i32.rem_u (local.get $s) (i32.const 60)))
    (call $xb_frac (i32.wrap_i64 (i64.rem_u (local.get $secs) (i64.const 1000000))))))
  (if (i32.ne (local.get $tz) (i32.const TZ_NONE)) (then
    (if (i32.eqz (local.get $tz))
      (then (call $xb_byte (i32.const 'Z')))
      (else
        (call $xb_byte (select (i32.const '-') (i32.const '+') (i32.lt_s (local.get $tz) (i32.const 0))))
        (local.set $s (select (i32.sub (i32.const 0) (local.get $tz)) (local.get $tz) (i32.lt_s (local.get $tz) (i32.const 0))))
        (call $xb_2 (i32.div_u (local.get $s) (i32.const 60)))
        (call $xb_byte (i32.const ':'))
        (call $xb_2 (i32.rem_u (local.get $s) (i32.const 60)))))))
  (call $typed (global.get $xb) (global.get $xb_len) (local.get $dt)))

;; A duration of kind k as a literal: "P1Y2M", "-PT23H", "P1MT1H", "PT0.5S", "PT0S", "P0M".
(func $dur_term (param $k i32) (param $months i64) (param $secs i64) (result i32)
  (local $dt i32) (local $r i64) (local $start i32)
  (local.set $dt (call $tk_iri (local.get $k)))
  (call $xb_reset)
  (if (i32.or (i64.lt_s (local.get $months) (i64.const 0)) (i64.lt_s (local.get $secs) (i64.const 0))) (then
    (call $xb_byte (i32.const '-'))
    (local.set $months (call $abs64 (local.get $months)))
    (local.set $secs (call $abs64 (local.get $secs)))))
  (call $xb_byte (i32.const 'P'))
  (local.set $start (global.get $xb_len))
  (call $xb_part (i64.div_u (local.get $months) (i64.const 12)) (i32.const 'Y'))
  (call $xb_part (i64.rem_u (local.get $months) (i64.const 12)) (i32.const 'M'))
  (call $xb_part (i64.div_u (local.get $secs) (i64.const 86400000000)) (i32.const 'D'))
  (if (i64.ne (local.tee $r (i64.rem_u (local.get $secs) (i64.const 86400000000))) (i64.const 0)) (then
    (call $xb_byte (i32.const 'T'))
    (call $xb_part (i64.div_u (local.get $r) (i64.const 3600000000)) (i32.const 'H'))
    (call $xb_part (i64.rem_u (i64.div_u (local.get $r) (i64.const 60000000)) (i64.const 60)) (i32.const 'M'))
    (if (i64.ne (local.tee $r (i64.rem_u (local.get $r) (i64.const 60000000))) (i64.const 0)) (then
      (call $xb_num (i64.div_u (local.get $r) (i64.const 1000000)))
      (call $xb_frac (i32.wrap_i64 (i64.rem_u (local.get $r) (i64.const 1000000))))
      (call $xb_byte (i32.const 'S'))))))
  (if (i32.eq (global.get $xb_len) (local.get $start)) (then
    (if (i32.eq (local.get $k) (i32.const TK_YMD))
      (then (call $xb_put (@str "0M")))
      (else (call $xb_put (@str "T0S"))))))
  (call $typed (global.get $xb) (global.get $xb_len) (local.get $dt)))

;; --- arithmetic and comparisons ------------------------------------------------

;; The instant, as microseconds since the epoch in UTC (no timezone: UTC).
(func $t_utc (param $days i32) (param $secs i64) (param $tz i32) (result i64)
  (i64.sub (i64.add (i64.mul (i64.extend_i32_s (local.get $days)) (i64.const 86400000000)) (local.get $secs))
           (i64.mul (i64.extend_i32_s (select (i32.const 0) (local.get $tz) (i32.eq (local.get $tz) (i32.const TZ_NONE)))) (i64.const 60000000))))

;; A moment moved by a duration: months first (the day clamped to the month's
;; last), then seconds. A date keeps only its date; a time wraps around
;; midnight and takes no months.
(func $shift (param $k i32) (param $days i32) (param $secs i64) (param $tz i32) (param $months i64) (param $ds i64) (result i32)
  (local $t i64) (local $y i32) (local $m i32) (local $d i32)
  (if (i64.ne (local.get $months) (i64.const 0)) (then
    (if (i32.eq (local.get $k) (i32.const TK_TIME)) (then (return (i32.const 0))))
    (call $civil (local.get $days))
    (local.set $t (i64.add (i64.add (i64.mul (i64.extend_i32_s (global.get $cv_year)) (i64.const 12)) (i64.extend_i32_s (i32.sub (global.get $cv_month) (i32.const 1))))
                           (local.get $months)))
    (local.set $y (i32.wrap_i64 (call $fdiv (local.get $t) (i64.const 12))))
    (local.set $m (i32.add (i32.wrap_i64 (i64.sub (local.get $t) (i64.mul (i64.extend_i32_s (local.get $y)) (i64.const 12)))) (i32.const 1)))
    (local.set $d (call $days_in (local.get $y) (local.get $m)))
    (local.set $days (call $days_from_civil (local.get $y) (local.get $m)
      (select (local.get $d) (global.get $cv_day) (i32.gt_u (global.get $cv_day) (local.get $d)))))))
  (local.set $t (i64.add (local.get $secs) (local.get $ds)))
  (local.set $ds (call $fdiv (local.get $t) (i64.const 86400000000)))
  (if (i32.ne (local.get $k) (i32.const TK_TIME)) (then
    (local.set $days (i32.add (local.get $days) (i32.wrap_i64 (local.get $ds))))))
  (call $moment_term (local.get $k) (local.get $days)
    (select (i64.const 0) (i64.sub (local.get $t) (i64.mul (local.get $ds) (i64.const 86400000000))) (i32.eq (local.get $k) (i32.const TK_DATE)))
    (local.get $tz)))

;; A duration times or divided by number n; a yearMonthDuration rounds to whole
;; months (half up). NaN, the infinities and dividing by zero are errors.
(func $tscale (param $k i32) (param $months i64) (param $secs i64) (param $n i32) (param $div i32) (result i32)
  (local $x f64) (local $r f64)
  (if (i32.eqz (call $num (local.get $n))) (then (return (i32.const 0))))
  (local.set $x (global.get $nf))
  (if (i32.or (f64.ne (f64.sub (local.get $x) (local.get $x)) (f64.const 0))
              (i32.and (local.get $div) (f64.eq (local.get $x) (f64.const 0)))) (then (return (i32.const 0))))
  (if (i32.eq (local.get $k) (i32.const TK_DUR)) (then (return (i32.const 0))))
  (local.set $r (f64.convert_i64_s (select (local.get $secs) (local.get $months) (i32.eq (local.get $k) (i32.const TK_DTD)))))
  (local.set $r (if (result f64) (local.get $div) (then (f64.div (local.get $r) (local.get $x))) (else (f64.mul (local.get $r) (local.get $x)))))
  (local.set $r (if (result f64) (i32.eq (local.get $k) (i32.const TK_DTD))
    (then (f64.nearest (local.get $r)))
    (else (f64.floor (f64.add (local.get $r) (f64.const 0.5))))))
  (if (i32.eqz (f64.lt (f64.abs (local.get $r)) (f64.const 9e18))) (then (return (i32.const 0))))
  (if (i32.eq (local.get $k) (i32.const TK_DTD)) (then
    (return (call $dur_term (local.get $k) (i64.const 0) (i64.trunc_sat_f64_s (local.get $r))))))
  (call $dur_term (local.get $k) (i64.trunc_sat_f64_s (local.get $r)) (i64.const 0)))

;; a op b (F_ADD, F_SUB, F_MUL, F_DIV) where an operand is a date, time or
;; duration; 0 when undefined.
(func $temporal_arith (param $op i32) (param $a i32) (param $b i32) (result i32)
  (local $ka i32) (local $da i32) (local $sa i64) (local $za i32) (local $ma i64)
  (local $kb i32) (local $sub i32) (local $m i64) (local $s i64)
  (local.set $ka (call $tparse (local.get $a)))
  (local.set $da (global.get $tm_days)) (local.set $sa (global.get $tm_secs))
  (local.set $za (global.get $tm_tz)) (local.set $ma (global.get $tm_months))
  (local.set $kb (call $tparse (local.get $b)))
  (local.set $sub (i32.eq (local.get $op) (i32.const F_SUB)))
  (if (i32.le_u (i32.sub (local.get $op) (i32.const F_ADD)) (i32.const 1)) (then
    ;; moment ± duration
    (if (i32.and (i32.lt_u (i32.sub (local.get $ka) (i32.const 1)) (i32.const 3)) (i32.ge_u (local.get $kb) (i32.const TK_DUR))) (then
      (return (call $shift (local.get $ka) (local.get $da) (local.get $sa) (local.get $za)
        (select (i64.sub (i64.const 0) (global.get $tm_months)) (global.get $tm_months) (local.get $sub))
        (select (i64.sub (i64.const 0) (global.get $tm_secs)) (global.get $tm_secs) (local.get $sub))))))
    (if (i32.and (i32.eqz (local.get $sub))
                 (i32.and (i32.ge_u (local.get $ka) (i32.const TK_DUR)) (i32.lt_u (i32.sub (local.get $kb) (i32.const 1)) (i32.const 3)))) (then
      (return (call $shift (local.get $kb) (global.get $tm_days) (global.get $tm_secs) (global.get $tm_tz) (local.get $ma) (local.get $sa)))))
    ;; moment − moment of the same kind, through UTC
    (if (i32.and (local.get $sub) (i32.and (i32.eq (local.get $ka) (local.get $kb)) (i32.lt_u (i32.sub (local.get $ka) (i32.const 1)) (i32.const 3)))) (then
      (return (call $dur_term (i32.const TK_DTD) (i64.const 0)
        (i64.sub (call $t_utc (local.get $da) (local.get $sa) (local.get $za)) (call $t_utc (global.get $tm_days) (global.get $tm_secs) (global.get $tm_tz)))))))
    ;; duration ± duration: never of mixed signs
    (if (i32.and (i32.ge_u (local.get $ka) (i32.const TK_DUR)) (i32.ge_u (local.get $kb) (i32.const TK_DUR))) (then
      (local.set $m (select (i64.sub (local.get $ma) (global.get $tm_months)) (i64.add (local.get $ma) (global.get $tm_months)) (local.get $sub)))
      (local.set $s (select (i64.sub (local.get $sa) (global.get $tm_secs)) (i64.add (local.get $sa) (global.get $tm_secs)) (local.get $sub)))
      (if (i32.or (i32.and (i64.gt_s (local.get $m) (i64.const 0)) (i64.lt_s (local.get $s) (i64.const 0)))
                  (i32.and (i64.lt_s (local.get $m) (i64.const 0)) (i64.gt_s (local.get $s) (i64.const 0)))) (then (return (i32.const 0))))
      (return (call $dur_term (select (local.get $ka) (i32.const TK_DUR) (i32.eq (local.get $ka) (local.get $kb))) (local.get $m) (local.get $s)))))
    (return (i32.const 0))))
  ;; duration × or ÷ number
  (if (i32.and (i32.ge_u (local.get $ka) (i32.const TK_DUR)) (i32.eqz (local.get $kb))) (then
    (return (call $tscale (local.get $ka) (local.get $ma) (local.get $sa) (local.get $b) (i32.eq (local.get $op) (i32.const F_DIV))))))
  (if (i32.and (i32.eq (local.get $op) (i32.const F_MUL)) (i32.and (i32.eqz (local.get $ka)) (i32.ge_u (local.get $kb) (i32.const TK_DUR)))) (then
    (return (call $tscale (local.get $kb) (global.get $tm_months) (global.get $tm_secs) (local.get $a) (i32.const 0)))))
  ;; duration ÷ duration of the same kind: a decimal
  (if (i32.and (i32.eq (local.get $op) (i32.const F_DIV)) (i32.eq (local.get $ka) (local.get $kb))) (then
    (if (i32.eq (local.get $ka) (i32.const TK_DTD)) (then
      (return (call $dec_div (local.get $sa) (i32.const 6) (global.get $tm_secs) (i32.const 6)))))
    (if (i32.eq (local.get $ka) (i32.const TK_YMD)) (then
      (return (call $dec_div (local.get $ma) (i32.const 0) (global.get $tm_months) (i32.const 0)))))))
  (i32.const 0))

;; -a for a duration; 0 otherwise.
(func $temporal_neg (param $a i32) (result i32)
  (local $k i32)
  (if (i32.lt_u (local.tee $k (call $tparse (local.get $a))) (i32.const TK_DUR)) (then (return (i32.const 0))))
  (call $dur_term (local.get $k) (i64.sub (i64.const 0) (global.get $tm_months)) (i64.sub (i64.const 0) (global.get $tm_secs))))

;; a <=> b for two times (by UTC) or two durations: -1, 0, 1, 2 when they do not
;; compare, 3 when they are not dates, times or durations. With eq, durations
;; of any kinds are equal (0) when their months and seconds are, else 1;
;; otherwise only two dayTimeDurations or two yearMonthDurations are ordered.
(func $temporal_cmp (param $a i32) (param $b i32) (param $eq i32) (result i32)
  (local $ka i32) (local $kb i32) (local $x i64) (local $mx i64) (local $y i64)
  (if (i32.eqz (local.tee $ka (call $tparse (local.get $a)))) (then (return (i32.const 3))))
  (local.set $x (select (call $t_utc (global.get $tm_days) (global.get $tm_secs) (global.get $tm_tz)) (global.get $tm_secs) (i32.eq (local.get $ka) (i32.const TK_TIME))))
  (local.set $mx (global.get $tm_months))
  (if (i32.eqz (local.tee $kb (call $tparse (local.get $b)))) (then (return (i32.const 3))))
  (local.set $y (select (call $t_utc (global.get $tm_days) (global.get $tm_secs) (global.get $tm_tz)) (global.get $tm_secs) (i32.eq (local.get $kb) (i32.const TK_TIME))))
  (if (i32.and (i32.ge_u (local.get $ka) (i32.const TK_DUR)) (i32.ge_u (local.get $kb) (i32.const TK_DUR))) (then
    (if (local.get $eq) (then
      (return (i32.eqz (i32.and (i64.eq (local.get $mx) (global.get $tm_months)) (i64.eq (local.get $x) (local.get $y)))))))
    (if (i32.ne (local.get $ka) (local.get $kb)) (then (return (i32.const 2))))
    (if (i32.eq (local.get $ka) (i32.const TK_YMD)) (then
      (local.set $x (local.get $mx))
      (local.set $y (global.get $tm_months))))
    (if (i32.eq (local.get $ka) (i32.const TK_DUR)) (then (return (i32.const 2))))
    (return (i32.sub (i64.gt_s (local.get $x) (local.get $y)) (i64.lt_s (local.get $x) (local.get $y))))))
  (if (i32.and (i32.eq (local.get $ka) (i32.const TK_TIME)) (i32.eq (local.get $kb) (i32.const TK_TIME))) (then
    (return (i32.sub (i64.gt_s (local.get $x) (local.get $y)) (i64.lt_s (local.get $x) (local.get $y))))))
  (i32.const 2))

;; --- casts ---------------------------------------------------------------------

;; v cast to kind k (XPath's casting rules), canonical; 0 when it cannot be. A
;; string is read as a k (whitespace trimmed); a dateTime gives its date or its
;; time (with the timezone), a date its midnight; a duration gives any other
;; duration (a dayTimeDuration without months, a yearMonthDuration without
;; seconds). Nothing else casts to a date, time or duration.
(func $temporal_cast (param $k i32) (param $v i32) (result i32)
  (local $p i32) (local $e i32) (local $ks i32)
  (if (i32.ne (call $kind (local.get $v)) (i32.const 3)) (then (return (i32.const 0))))
  (if (call $is_plain (local.get $v))
    (then
      (local.set $p (call $tval (local.get $v)))
      (local.set $e (i32.add (local.get $p) (call $tlen (local.get $v))))
      (loop $lead (if (i32.and (i32.lt_u (local.get $p) (local.get $e)) (call $is_space (i32.load8_u (local.get $p)))) (then
        (local.set $p (i32.add (local.get $p) (i32.const 1))) (br $lead))))
      (loop $trail (if (i32.and (i32.lt_u (local.get $p) (local.get $e)) (call $is_space (i32.load8_u (i32.sub (local.get $e) (i32.const 1))))) (then
        (local.set $e (i32.sub (local.get $e) (i32.const 1))) (br $trail))))
      (if (i32.eqz (call $tparse_text (local.get $k) (local.get $p) (i32.sub (local.get $e) (local.get $p)))) (then (return (i32.const 0)))))
    (else
      (if (i32.eqz (local.tee $ks (call $tparse (local.get $v)))) (then (return (i32.const 0))))
      (if (i32.lt_u (local.get $k) (i32.const TK_DUR))
        (then
          ;; moments: the same kind, dateTime to date or time, date to dateTime
          (if (i32.eqz (i32.or (i32.eq (local.get $ks) (local.get $k))
                               (i32.or (i32.eq (local.get $ks) (i32.const TK_DT)) (i32.and (i32.eq (local.get $ks) (i32.const TK_DATE)) (i32.eq (local.get $k) (i32.const TK_DT))))))
            (then (return (i32.const 0))))
          (if (i32.eq (local.get $k) (i32.const TK_DATE)) (then (global.set $tm_secs (i64.const 0)))))
        (else
          (if (i32.lt_u (local.get $ks) (i32.const TK_DUR)) (then (return (i32.const 0))))
          (if (i32.eq (local.get $k) (i32.const TK_DTD)) (then (global.set $tm_months (i64.const 0))))
          (if (i32.eq (local.get $k) (i32.const TK_YMD)) (then (global.set $tm_secs (i64.const 0))))))))
  (if (result i32) (i32.lt_u (local.get $k) (i32.const TK_DUR))
    (then (call $moment_term (local.get $k) (global.get $tm_days) (global.get $tm_secs) (global.get $tm_tz)))
    (else (call $dur_term (local.get $k) (global.get $tm_months) (global.get $tm_secs)))))

;; ASCII whitespace, as Rust's trim_ascii: space, \t, \n, \f, \r.
(func $is_space (param $c i32) (result i32)
  (i32.or (i32.eq (local.get $c) (i32.const 32))
          (i32.and (i32.le_u (i32.sub (local.get $c) (i32.const 9)) (i32.const 4)) (i32.ne (local.get $c) (i32.const 11)))))
