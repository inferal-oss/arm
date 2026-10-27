;; XPath regular expressions (REGEX, REPLACE): parsed to a tree, compiled to a
;; small program, run by a backtracking machine over UTF-8. Compiled programs
;; are cached by (pattern, flags) term ids.
;;
;; Supported: literals, ., classes [a-z] [^...] with \d \s \w \i \c and their
;; negations, groups ( ) (?: ), alternation, * + ? {n} {n,} {n,m} (and lazy ?),
;; ^ $, flags i s m x q.

;; tree nodes (16 bytes): kind, a, b, c
(@define R_CHAR 1)    ;; a code point
(@define R_ANY 2)
(@define R_CLASS 3)   ;; a class
(@define R_CAT 4)     ;; a, b
(@define R_ALT 5)     ;; a, b
(@define R_GROUP 6)   ;; a child, b group number (0: not capturing)
(@define R_REP 7)     ;; a child, b min, c max (-1 any) | lazy << 31... see $re_rep
(@define R_BOL 8)
(@define R_EOL 9)
(@define R_EMPTY 10)
;; instructions
(@define I_CHAR 1) (@define I_ANY 2) (@define I_CLASS 3) (@define I_SPLIT 4) (@define I_JMP 5)
(@define I_SAVE 6) (@define I_BOL 7) (@define I_EOL 8) (@define I_MATCH 9)

(global $re_p (mut i32) (i32.const 0))
(global $re_e (mut i32) (i32.const 0))
(global $re_flags (mut i32) (i32.const 0))   ;; 1 i, 2 s, 4 m, 8 x, 16 q
(global $re_groups (mut i32) (i32.const 0))
(global $re_bad (mut i32) (i32.const 0))
(global $re_cache (mut i32) (i32.const 0))   ;; set of (pattern, flags)
(global $re_progs (mut i32) (i32.const 0))   ;; vector of programs, by set index

(func $rnode (param $k i32) (param $a i32) (param $b i32) (param $c i32) (result i32)
  (local $n i32)
  (local.set $n (call $alloc (i32.const 16)))
  (i32.store (local.get $n) (local.get $k))
  (i32.store offset=4 (local.get $n) (local.get $a))
  (i32.store offset=8 (local.get $n) (local.get $b))
  (i32.store offset=12 (local.get $n) (local.get $c))
  (local.get $n))

(func $rfree (param $n i32)
  (local $k i32)
  (if (i32.eqz (local.get $n)) (then (return)))
  (local.set $k (i32.load (local.get $n)))
  (if (i32.or (i32.eq (local.get $k) (i32.const R_CAT)) (i32.eq (local.get $k) (i32.const R_ALT))) (then
    (call $rfree (i32.load offset=4 (local.get $n)))
    (call $rfree (i32.load offset=8 (local.get $n)))))
  (if (i32.or (i32.eq (local.get $k) (i32.const R_GROUP)) (i32.eq (local.get $k) (i32.const R_REP)))
    (then (call $rfree (i32.load offset=4 (local.get $n)))))
  (call $free (local.get $n) (i32.const 16)))

(func $re_peek (result i32)
  (if (result i32) (i32.lt_u (global.get $re_p) (global.get $re_e)) (then (i32.load8_u (global.get $re_p))) (else (i32.const -1))))

;; The next code point of the pattern (consumed).
(func $re_cp (result i32)
  (local $c i32)
  (local.set $c (call $decode (global.get $re_p)))
  (global.set $re_p (i32.add (global.get $re_p) (global.get $cplen)))
  (local.get $c))

;; Alternation.
(func $re_alt (result i32)
  (local $a i32)
  (local.set $a (call $re_cat))
  (loop $alts
    (if (i32.eq (call $re_peek) (i32.const '|')) (then
      (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))
      (local.set $a (call $rnode (i32.const R_ALT) (local.get $a) (call $re_cat) (i32.const 0)))
      (br $alts))))
  (local.get $a))

(func $re_cat (result i32)
  (local $a i32) (local $b i32) (local $c i32)
  (local.set $a (call $rnode (i32.const R_EMPTY) (i32.const 0) (i32.const 0) (i32.const 0)))
  (loop $atoms
    (local.set $c (call $re_peek))
    (if (i32.and (i32.ge_s (local.get $c) (i32.const 0)) (i32.and (i32.ne (local.get $c) (i32.const '|')) (i32.ne (local.get $c) (i32.const ')')))) (then
      (local.set $b (call $re_quant (call $re_atom)))
      (local.set $a (call $rnode (i32.const R_CAT) (local.get $a) (local.get $b) (i32.const 0)))
      (br_if $atoms (i32.eqz (global.get $re_bad))))))
  (local.get $a))

;; An integer in a quantifier, or -1.
(func $re_int (result i32)
  (local $v i32) (local $any i32)
  (loop $d (if (call $is_digit (call $re_peek)) (then
    (local.set $v (i32.add (i32.mul (local.get $v) (i32.const 10)) (i32.sub (call $re_peek) (i32.const '0'))))
    (local.set $any (i32.const 1))
    (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))
    (br $d))))
  (select (local.get $v) (i32.const -1) (local.get $any)))

(func $re_quant (param $a i32) (result i32)
  (local $c i32) (local $min i32) (local $max i32)
  (loop $quants
    (local.set $c (call $re_peek))
    (local.set $min (i32.const -2))
    (if (i32.eq (local.get $c) (i32.const '*')) (then (local.set $min (i32.const 0)) (local.set $max (i32.const -1))))
    (if (i32.eq (local.get $c) (i32.const '+')) (then (local.set $min (i32.const 1)) (local.set $max (i32.const -1))))
    (if (i32.eq (local.get $c) (i32.const '?')) (then (local.set $min (i32.const 0)) (local.set $max (i32.const 1))))
    (if (i32.ne (local.get $min) (i32.const -2)) (then (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))))
    (if (i32.eq (local.get $c) (i32.const '{')) (then
      (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))
      (local.set $min (call $re_int))
      (local.set $max (local.get $min))
      (if (i32.eq (call $re_peek) (i32.const ',')) (then
        (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))
        (local.set $max (call $re_int))))
      (if (i32.or (i32.lt_s (local.get $min) (i32.const 0)) (i32.ne (call $re_peek) (i32.const '}'))) (then (global.set $re_bad (i32.const 1)) (return (local.get $a))))
      (if (i32.and (i32.ge_s (local.get $max) (i32.const 0)) (i32.lt_s (local.get $max) (local.get $min))) (then (global.set $re_bad (i32.const 1)) (return (local.get $a))))
      (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))))
    (if (i32.ne (local.get $min) (i32.const -2)) (then
      ;; lazy: ?
      (local.set $c (i32.eq (call $re_peek) (i32.const '?')))
      (if (local.get $c) (then (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))))
      (local.set $a (call $rnode (i32.const R_REP) (local.get $a) (i32.or (local.get $min) (i32.shl (local.get $c) (i32.const 30))) (local.get $max)))
      (br $quants))))
  (local.get $a))

;; Class items (12 bytes): kind (0 range, else an escape class), lo, hi.
;; A class: +0 negated, +4 item count, +8 items.
(func $re_escape_class (param $c i32) (result i32)
  (if (i32.eq (local.get $c) (i32.const 'd')) (then (return (i32.const 1))))
  (if (i32.eq (local.get $c) (i32.const 'D')) (then (return (i32.const 2))))
  (if (i32.eq (local.get $c) (i32.const 's')) (then (return (i32.const 3))))
  (if (i32.eq (local.get $c) (i32.const 'S')) (then (return (i32.const 4))))
  (if (i32.eq (local.get $c) (i32.const 'w')) (then (return (i32.const 5))))
  (if (i32.eq (local.get $c) (i32.const 'W')) (then (return (i32.const 6))))
  (if (i32.eq (local.get $c) (i32.const 'i')) (then (return (i32.const 7))))
  (if (i32.eq (local.get $c) (i32.const 'I')) (then (return (i32.const 8))))
  (if (i32.eq (local.get $c) (i32.const 'c')) (then (return (i32.const 9))))
  (if (i32.eq (local.get $c) (i32.const 'C')) (then (return (i32.const 10))))
  (i32.const 0))

;; A single-character escape: its code point, or -1.
(func $re_escape_char (param $c i32) (result i32)
  (if (i32.eq (local.get $c) (i32.const 'n')) (then (return (i32.const 10))))
  (if (i32.eq (local.get $c) (i32.const 'r')) (then (return (i32.const 13))))
  (if (i32.eq (local.get $c) (i32.const 't')) (then (return (i32.const 9))))
  (if (call $one_of (local.get $c) (@str "\\|.?*+(){}-[]^$")) (then (return (local.get $c))))
  (i32.const -1))

(func $class_new (result i32)
  (call $alloc0 (i32.const 8)))

;; Add an item to a class (reallocating it); returns the class.
(func $class_add (param $cl i32) (param $k i32) (param $lo i32) (param $hi i32) (result i32)
  (local $n i32) (local $new i32)
  (local.set $n (i32.load offset=4 (local.get $cl)))
  (local.set $new (call $realloc (local.get $cl) (i32.add (i32.const 8) (i32.mul (local.get $n) (i32.const 12)))
    (i32.add (i32.const 8) (i32.mul (i32.add (local.get $n) (i32.const 1)) (i32.const 12)))
    (i32.add (i32.const 8) (i32.mul (local.get $n) (i32.const 12)))))
  (i32.store (i32.add (i32.add (local.get $new) (i32.const 8)) (i32.mul (local.get $n) (i32.const 12))) (local.get $k))
  (i32.store offset=4 (i32.add (i32.add (local.get $new) (i32.const 8)) (i32.mul (local.get $n) (i32.const 12))) (local.get $lo))
  (i32.store offset=8 (i32.add (i32.add (local.get $new) (i32.const 8)) (i32.mul (local.get $n) (i32.const 12))) (local.get $hi))
  (i32.store offset=4 (local.get $new) (i32.add (local.get $n) (i32.const 1)))
  (local.get $new))

(func $re_atom (result i32)
  (local $c i32) (local $k i32) (local $a i32) (local $g i32) (local $cl i32) (local $lo i32) (local $hi i32)
  (local.set $c (call $re_cp))
  (if (i32.eq (local.get $c) (i32.const '.')) (then (return (call $rnode (i32.const R_ANY) (i32.const 0) (i32.const 0) (i32.const 0)))))
  (if (i32.eq (local.get $c) (i32.const '^')) (then (return (call $rnode (i32.const R_BOL) (i32.const 0) (i32.const 0) (i32.const 0)))))
  (if (i32.eq (local.get $c) (i32.const '$')) (then (return (call $rnode (i32.const R_EOL) (i32.const 0) (i32.const 0) (i32.const 0)))))
  (if (i32.eq (local.get $c) (i32.const '(')) (then
    (if (i32.and (i32.eq (call $re_peek) (i32.const '?')) (i32.lt_u (i32.add (global.get $re_p) (i32.const 1)) (global.get $re_e))) (then
      (if (i32.eq (i32.load8_u offset=1 (global.get $re_p)) (i32.const ':')) (then
        (global.set $re_p (i32.add (global.get $re_p) (i32.const 2)))
        (local.set $g (i32.const -1))))))
    (if (i32.eqz (local.get $g)) (then
      (global.set $re_groups (i32.add (global.get $re_groups) (i32.const 1)))
      (local.set $g (global.get $re_groups))))
    (local.set $a (call $re_alt))
    (if (i32.ne (call $re_peek) (i32.const ')')) (then (global.set $re_bad (i32.const 1)) (return (local.get $a))))
    (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))
    (return (call $rnode (i32.const R_GROUP) (local.get $a) (select (i32.const 0) (local.get $g) (i32.lt_s (local.get $g) (i32.const 0))) (i32.const 0)))))
  (if (i32.eq (local.get $c) (i32.const '[')) (then (return (call $re_class))))
  (if (i32.eq (local.get $c) (i32.const '\\')) (then
    (if (i32.ge_u (global.get $re_p) (global.get $re_e)) (then (global.set $re_bad (i32.const 1)) (return (i32.const 0))))
    (local.set $c (call $re_cp))
    (if (local.tee $k (call $re_escape_class (local.get $c))) (then
      (return (call $rnode (i32.const R_CLASS) (call $class_add (call $class_new) (local.get $k) (i32.const 0) (i32.const 0)) (i32.const 0) (i32.const 0)))))
    (if (i32.lt_s (local.tee $c (call $re_escape_char (local.get $c))) (i32.const 0)) (then (global.set $re_bad (i32.const 1)) (return (i32.const 0))))
    (return (call $rnode (i32.const R_CHAR) (local.get $c) (i32.const 0) (i32.const 0)))))
  (if (call $one_of (local.get $c) (@str "*+?{")) (then (global.set $re_bad (i32.const 1)) (return (i32.const 0))))
  (call $rnode (i32.const R_CHAR) (local.get $c) (i32.const 0) (i32.const 0)))

;; [ ... ] (after '[').
(func $re_class (result i32)
  (local $cl i32) (local $c i32) (local $k i32) (local $lo i32) (local $hi i32) (local $first i32)
  (local.set $cl (call $class_new))
  (if (i32.eq (call $re_peek) (i32.const '^')) (then
    (i32.store (local.get $cl) (i32.const 1))
    (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))))
  (local.set $first (i32.const 1))
  (block $done
    (loop $items
      (if (i32.lt_s (call $re_peek) (i32.const 0)) (then (global.set $re_bad (i32.const 1)) (br $done)))
      (if (i32.and (i32.eq (call $re_peek) (i32.const ']')) (i32.eqz (local.get $first))) (then
        (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))
        (br $done)))
      (local.set $first (i32.const 0))
      (local.set $c (call $re_cp))
      (local.set $k (i32.const 0))
      (if (i32.eq (local.get $c) (i32.const '\\')) (then
        (local.set $c (call $re_cp))
        (if (local.tee $k (call $re_escape_class (local.get $c)))
          (then (local.set $cl (call $class_add (local.get $cl) (local.get $k) (i32.const 0) (i32.const 0))) (br $items)))
        (if (i32.lt_s (local.tee $c (call $re_escape_char (local.get $c))) (i32.const 0)) (then (global.set $re_bad (i32.const 1)) (br $done)))))
      (local.set $lo (local.get $c))
      (local.set $hi (local.get $c))
      ;; a range, unless '-' ends the class
      (if (i32.and (i32.eq (call $re_peek) (i32.const '-'))
                   (i32.and (i32.lt_u (i32.add (global.get $re_p) (i32.const 1)) (global.get $re_e))
                            (i32.ne (i32.load8_u offset=1 (global.get $re_p)) (i32.const ']')))) (then
        (global.set $re_p (i32.add (global.get $re_p) (i32.const 1)))
        (local.set $hi (call $re_cp))
        (if (i32.eq (local.get $hi) (i32.const '\\')) (then
          (if (i32.lt_s (local.tee $hi (call $re_escape_char (call $re_cp))) (i32.const 0)) (then (global.set $re_bad (i32.const 1)) (br $done)))))
        (if (i32.lt_u (local.get $hi) (local.get $lo)) (then (global.set $re_bad (i32.const 1)) (br $done)))))
      (local.set $cl (call $class_add (local.get $cl) (i32.const 0) (local.get $lo) (local.get $hi)))
      (br $items)))
  (call $rnode (i32.const R_CLASS) (local.get $cl) (i32.const 0) (i32.const 0)))

;; --- code generation ---------------------------------------------------------------

(global $rc (mut i32) (i32.const 0))       ;; code vector

(func $rc_emit (param $w i32) (result i32)
  (call $v_push1 (global.get $rc) (local.get $w))
  (i32.sub (i32.load offset=4 (global.get $rc)) (i32.const 1)))

(func $patch (param $at i32) (param $w i32)
  (i32.store (i32.add (i32.load (global.get $rc)) (i32.shl (local.get $at) (i32.const 2))) (local.get $w)))

(func $here (result i32) (i32.load offset=4 (global.get $rc)))

(func $gen (param $n i32)
  (local $k i32) (local $s i32) (local $j i32) (local $i i32) (local $min i32) (local $max i32) (local $lazy i32) (local $l i32)
  (local.set $k (i32.load (local.get $n)))
  (if (i32.eq (local.get $k) (i32.const R_CHAR)) (then
    (drop (call $rc_emit (i32.const I_CHAR)))
    (drop (call $rc_emit (select (call $case (i32.load offset=4 (local.get $n)) (i32.const 0)) (i32.load offset=4 (local.get $n)) (i32.and (global.get $re_flags) (i32.const 1)))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const R_ANY)) (then (drop (call $rc_emit (i32.const I_ANY))) (return)))
  (if (i32.eq (local.get $k) (i32.const R_CLASS)) (then
    (drop (call $rc_emit (i32.const I_CLASS)))
    (drop (call $rc_emit (i32.load offset=4 (local.get $n))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const R_BOL)) (then (drop (call $rc_emit (i32.const I_BOL))) (return)))
  (if (i32.eq (local.get $k) (i32.const R_EOL)) (then (drop (call $rc_emit (i32.const I_EOL))) (return)))
  (if (i32.eq (local.get $k) (i32.const R_CAT)) (then
    (call $gen (i32.load offset=4 (local.get $n)))
    (call $gen (i32.load offset=8 (local.get $n)))
    (return)))
  (if (i32.eq (local.get $k) (i32.const R_ALT)) (then
    ;; SPLIT L1 L2; L1: a; JMP L3; L2: b; L3:
    (local.set $s (call $rc_emit (i32.const I_SPLIT)))
    (drop (call $rc_emit (i32.const 0)))
    (drop (call $rc_emit (i32.const 0)))
    (call $patch (i32.add (local.get $s) (i32.const 1)) (call $here))
    (call $gen (i32.load offset=4 (local.get $n)))
    (local.set $j (call $rc_emit (i32.const I_JMP)))
    (drop (call $rc_emit (i32.const 0)))
    (call $patch (i32.add (local.get $s) (i32.const 2)) (call $here))
    (call $gen (i32.load offset=8 (local.get $n)))
    (call $patch (i32.add (local.get $j) (i32.const 1)) (call $here))
    (return)))
  (if (i32.eq (local.get $k) (i32.const R_GROUP)) (then
    (if (i32.load offset=8 (local.get $n)) (then
      (drop (call $rc_emit (i32.const I_SAVE)))
      (drop (call $rc_emit (i32.shl (i32.load offset=8 (local.get $n)) (i32.const 1))))))
    (call $gen (i32.load offset=4 (local.get $n)))
    (if (i32.load offset=8 (local.get $n)) (then
      (drop (call $rc_emit (i32.const I_SAVE)))
      (drop (call $rc_emit (i32.add (i32.shl (i32.load offset=8 (local.get $n)) (i32.const 1)) (i32.const 1))))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const R_REP)) (then
    (local.set $min (i32.and (i32.load offset=8 (local.get $n)) (i32.const 0x3fffffff)))
    (local.set $lazy (i32.shr_u (i32.load offset=8 (local.get $n)) (i32.const 30)))
    (local.set $max (i32.load offset=12 (local.get $n)))
    (loop $mins (if (i32.lt_s (local.get $i) (local.get $min)) (then
      (call $gen (i32.load offset=4 (local.get $n)))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $mins))))
    (if (i32.lt_s (local.get $max) (i32.const 0)) (then
      ;; L: SPLIT L1 L2; L1: child; JMP L; L2:
      (local.set $l (call $here))
      (local.set $s (call $rc_emit (i32.const I_SPLIT)))
      (drop (call $rc_emit (i32.const 0)))
      (drop (call $rc_emit (i32.const 0)))
      (call $patch (i32.add (local.get $s) (select (i32.const 2) (i32.const 1) (local.get $lazy))) (call $here))
      (call $gen (i32.load offset=4 (local.get $n)))
      (drop (call $rc_emit (i32.const I_JMP)))
      (drop (call $rc_emit (local.get $l)))
      (call $patch (i32.add (local.get $s) (select (i32.const 1) (i32.const 2) (local.get $lazy))) (call $here))
      (return)))
    ;; up to max - min optional copies
    (loop $opts (if (i32.lt_s (local.get $i) (local.get $max)) (then
      (local.set $s (call $rc_emit (i32.const I_SPLIT)))
      (drop (call $rc_emit (i32.const 0)))
      (drop (call $rc_emit (i32.const 0)))
      (call $patch (i32.add (local.get $s) (select (i32.const 2) (i32.const 1) (local.get $lazy))) (call $here))
      (call $gen (i32.load offset=4 (local.get $n)))
      (call $patch (i32.add (local.get $s) (select (i32.const 1) (i32.const 2) (local.get $lazy))) (call $here))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $opts))))
    (return))))

;; The compiled program for pattern (and flags) terms, or 0 when invalid.
;; Program: +0 group count, +4 code length, +8 code.
(func $re_compile (param $pat i32) (param $flags i32) (result i32)
  (local $key i32) (local $i i32) (local $f i32) (local $p i32) (local $e i32) (local $c i32) (local $tree i32) (local $prog i32) (local $buf i32) (local $n i32)
  (if (i32.eqz (global.get $re_cache)) (then
    (global.set $re_cache (call $set_make (i32.const 2)))
    (global.set $re_progs (call $v_new))))
  (local.set $key (call $rs_alloc (i32.const 8)))
  (i32.store (local.get $key) (local.get $pat))
  (i32.store offset=4 (local.get $key) (local.get $flags))
  (local.set $i (call $set_add (global.get $re_cache) (local.get $key)))
  (call $row_put (local.get $key))
  (if (i32.eqz (global.get $set_new)) (then (return (call $v_at (global.get $re_progs) (local.get $i)))))
  ;; flags
  (if (local.get $flags) (then
    (local.set $p (call $tval (local.get $flags)))
    (local.set $e (i32.add (local.get $p) (call $tlen (local.get $flags))))
    (loop $chars (if (i32.lt_u (local.get $p) (local.get $e)) (then
      (local.set $c (i32.load8_u (local.get $p)))
      (local.set $f (i32.or (local.get $f)
        (if (result i32) (i32.eq (local.get $c) (i32.const 'i')) (then (i32.const 1))
        (else (if (result i32) (i32.eq (local.get $c) (i32.const 's')) (then (i32.const 2))
        (else (if (result i32) (i32.eq (local.get $c) (i32.const 'm')) (then (i32.const 4))
        (else (if (result i32) (i32.eq (local.get $c) (i32.const 'x')) (then (i32.const 8))
        (else (if (result i32) (i32.eq (local.get $c) (i32.const 'q')) (then (i32.const 16))
        (else (i32.const 0x100)))))))))))))
      (local.set $p (i32.add (local.get $p) (i32.const 1))) (br $chars))))))
  (global.set $re_flags (local.get $f))
  (global.set $re_groups (i32.const 0))
  (global.set $re_bad (i32.and (local.get $f) (i32.const 0x100)))
  ;; x: drop white space outside classes
  (local.set $n (call $tlen (local.get $pat)))
  (local.set $buf (call $alloc (i32.add (local.get $n) (i32.const 1))))
  (memory.copy (local.get $buf) (call $tval (local.get $pat)) (local.get $n))
  (if (i32.and (local.get $f) (i32.const 8)) (then
    (local.set $p (i32.const 0))
    (local.set $e (i32.const 0))
    (local.set $c (i32.const 0))
    (loop $chars (if (i32.lt_u (local.get $p) (local.get $n)) (then
      (local.set $i (i32.load8_u (i32.add (local.get $buf) (local.get $p))))
      (if (i32.eq (local.get $i) (i32.const '[')) (then (local.set $c (i32.const 1))))
      (if (i32.eq (local.get $i) (i32.const ']')) (then (local.set $c (i32.const 0))))
      (if (i32.or (local.get $c) (i32.eqz (call $one_of (local.get $i) (@str " \t\n\r")))) (then
        (i32.store8 (i32.add (local.get $buf) (local.get $e)) (local.get $i))
        (local.set $e (i32.add (local.get $e) (i32.const 1)))))
      (local.set $p (i32.add (local.get $p) (i32.const 1))) (br $chars))))
    (local.set $n (local.get $e))))
  (global.set $re_p (local.get $buf))
  (global.set $re_e (i32.add (local.get $buf) (local.get $n)))
  (if (i32.and (local.get $f) (i32.const 16))
    (then
      ;; q: the pattern is a literal string
      (local.set $tree (call $rnode (i32.const R_EMPTY) (i32.const 0) (i32.const 0) (i32.const 0)))
      (loop $chars (if (i32.lt_u (global.get $re_p) (global.get $re_e)) (then
        (local.set $tree (call $rnode (i32.const R_CAT) (local.get $tree) (call $rnode (i32.const R_CHAR) (call $re_cp) (i32.const 0) (i32.const 0)) (i32.const 0)))
        (br $chars)))))
    (else (local.set $tree (call $re_alt))))
  (if (i32.lt_u (global.get $re_p) (global.get $re_e)) (then (global.set $re_bad (i32.const 1))))
  (call $free (local.get $buf) (i32.add (call $tlen (local.get $pat)) (i32.const 1)))
  (if (global.get $re_bad) (then
    (call $rfree (local.get $tree))
    (call $v_push1 (global.get $re_progs) (i32.const 0))
    (return (i32.const 0))))
  (global.set $rc (call $v_new))
  (call $gen (local.get $tree))
  (drop (call $rc_emit (i32.const I_MATCH)))
  (call $rfree (local.get $tree))
  (local.set $n (i32.load offset=4 (global.get $rc)))
  (local.set $prog (call $alloc (i32.add (i32.const 12) (i32.shl (local.get $n) (i32.const 2)))))
  (i32.store (local.get $prog) (global.get $re_groups))
  (i32.store offset=4 (local.get $prog) (local.get $n))
  (i32.store offset=8 (local.get $prog) (global.get $re_flags))
  (memory.copy (i32.add (local.get $prog) (i32.const 12)) (i32.load (global.get $rc)) (i32.shl (local.get $n) (i32.const 2)))
  (call $v_free (global.get $rc))
  (call $v_push1 (global.get $re_progs) (local.get $prog))
  (local.get $prog))

;; --- matching ----------------------------------------------------------------------

(global $caps (mut i32) (i32.const 0))     ;; 2 positions per group (offsets), up to 32 groups
(global $m_end (mut i32) (i32.const 0))    ;; end offset of the last match

(func $class_has (param $cl i32) (param $c i32) (result i32)
  (local $i i32) (local $it i32) (local $k i32) (local $in i32)
  (loop $items
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $cl))) (then
      (local.set $it (i32.add (i32.add (local.get $cl) (i32.const 8)) (i32.mul (local.get $i) (i32.const 12))))
      (local.set $k (i32.load (local.get $it)))
      (local.set $in
        (if (result i32) (i32.eqz (local.get $k))
          (then (call $in (local.get $c) (i32.load offset=4 (local.get $it)) (i32.load offset=8 (local.get $it))))
          (else (call $escape_has (local.get $k) (local.get $c)))))
      (if (local.get $in) (then (return (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $items))))
  (i32.const 0))

;; \d \D \s \S \w \W \i \I \c \C (k 1-10)
(func $escape_has (param $k i32) (param $c i32) (result i32)
  (local $r i32)
  (local.set $r
    (if (result i32) (i32.le_u (local.get $k) (i32.const 2)) (then (call $is_digit (local.get $c)))
    (else (if (result i32) (i32.le_u (local.get $k) (i32.const 4)) (then (call $one_of (local.get $c) (@str " \t\n\r")))
    (else (if (result i32) (i32.le_u (local.get $k) (i32.const 6))
      ;; \w: not punctuation, separators or controls
      (then (i32.or (i32.or (call $is_alpha (local.get $c)) (call $is_digit (local.get $c)))
                    (i32.and (i32.ge_u (local.get $c) (i32.const 0xc0)) (i32.ne (local.get $c) (i32.const 0xd7)))))
    (else (if (result i32) (i32.le_u (local.get $k) (i32.const 8))
      (then (i32.or (call $pn_u (local.get $c)) (i32.eq (local.get $c) (i32.const ':'))))
      (else (i32.or (call $pn_chars (local.get $c)) (call $one_of (local.get $c) (@str ":."))))))))))))
  ;; odd k: the class, even: its complement
  (select (local.get $r) (i32.eqz (local.get $r)) (i32.and (local.get $k) (i32.const 1))))

;; Run program prog on [s, s+n) from offset at: whether it matches there; the
;; captures in $caps, the end in $m_end.
(func $re_run (param $prog i32) (param $s i32) (param $n i32) (param $at i32) (result i32)
  (local $stack i32) (local $pc i32) (local $pos i32) (local $op i32) (local $c i32) (local $code i32) (local $flags i32)
  (local $steps i32) (local $top i32) (local $k i32)
  (local.set $code (i32.add (local.get $prog) (i32.const 12)))
  (local.set $flags (i32.load offset=8 (local.get $prog)))
  (local.set $stack (call $v_new))
  (memory.fill (global.get $caps) (i32.const 0xff) (i32.const 264))
  (call $v_push2 (local.get $stack) (i32.const 0) (local.get $at))
  (block $fail
    (loop $threads
      (br_if $fail (i32.eqz (local.tee $top (i32.load offset=4 (local.get $stack)))))
      (local.set $pos (call $v_at (local.get $stack) (i32.sub (local.get $top) (i32.const 1))))
      (local.set $pc (call $v_at (local.get $stack) (i32.sub (local.get $top) (i32.const 2))))
      (i32.store offset=4 (local.get $stack) (i32.sub (local.get $top) (i32.const 2)))
      ;; an undo record: restore a capture
      (if (i32.lt_s (local.get $pc) (i32.const 0)) (then
        (i32.store (i32.add (global.get $caps) (i32.shl (i32.sub (i32.const -1) (local.get $pc)) (i32.const 2))) (local.get $pos))
        (br $threads)))
      (loop $step
        (local.set $steps (i32.add (local.get $steps) (i32.const 1)))
        (br_if $fail (i32.gt_u (local.get $steps) (i32.const 2000000)))
        (local.set $op (i32.load (i32.add (local.get $code) (i32.shl (local.get $pc) (i32.const 2)))))
        (if (i32.eq (local.get $op) (i32.const I_MATCH)) (then
          (global.set $m_end (local.get $pos))
          (call $v_free (local.get $stack))
          (return (i32.const 1))))
        (if (i32.eq (local.get $op) (i32.const I_SPLIT)) (then
          (call $v_push2 (local.get $stack) (i32.load offset=8 (i32.add (local.get $code) (i32.shl (local.get $pc) (i32.const 2)))) (local.get $pos))
          (local.set $pc (i32.load offset=4 (i32.add (local.get $code) (i32.shl (local.get $pc) (i32.const 2)))))
          (br $step)))
        (if (i32.eq (local.get $op) (i32.const I_JMP)) (then
          (local.set $pc (i32.load offset=4 (i32.add (local.get $code) (i32.shl (local.get $pc) (i32.const 2)))))
          (br $step)))
        (if (i32.eq (local.get $op) (i32.const I_SAVE)) (then
          (local.set $k (i32.load offset=4 (i32.add (local.get $code) (i32.shl (local.get $pc) (i32.const 2)))))
          (if (i32.lt_u (local.get $k) (i32.const 66)) (then
            (call $v_push2 (local.get $stack) (i32.sub (i32.const -1) (local.get $k)) (i32.load (i32.add (global.get $caps) (i32.shl (local.get $k) (i32.const 2)))))
            (i32.store (i32.add (global.get $caps) (i32.shl (local.get $k) (i32.const 2))) (local.get $pos))))
          (local.set $pc (i32.add (local.get $pc) (i32.const 2)))
          (br $step)))
        (if (i32.eq (local.get $op) (i32.const I_BOL)) (then
          (br_if $threads (i32.eqz (if (result i32) (i32.eqz (local.get $pos)) (then (i32.const 1))
            (else (i32.and (i32.ne (i32.and (local.get $flags) (i32.const 4)) (i32.const 0))
                           (i32.eq (i32.load8_u (i32.sub (i32.add (local.get $s) (local.get $pos)) (i32.const 1))) (i32.const 10)))))))
          (local.set $pc (i32.add (local.get $pc) (i32.const 1)))
          (br $step)))
        (if (i32.eq (local.get $op) (i32.const I_EOL)) (then
          (br_if $threads (i32.eqz (if (result i32) (i32.eq (local.get $pos) (local.get $n)) (then (i32.const 1))
            (else (i32.and (i32.ne (i32.and (local.get $flags) (i32.const 4)) (i32.const 0))
                           (i32.eq (i32.load8_u (i32.add (local.get $s) (local.get $pos))) (i32.const 10)))))))
          (local.set $pc (i32.add (local.get $pc) (i32.const 1)))
          (br $step)))
        ;; one character
        (br_if $threads (i32.ge_u (local.get $pos) (local.get $n)))
        (local.set $c (call $decode (i32.add (local.get $s) (local.get $pos))))
        (if (i32.eq (local.get $op) (i32.const I_ANY)) (then
          (br_if $threads (i32.and (i32.eqz (i32.and (local.get $flags) (i32.const 2))) (i32.or (i32.eq (local.get $c) (i32.const 10)) (i32.eq (local.get $c) (i32.const 13)))))
          (local.set $pos (i32.add (local.get $pos) (global.get $cplen)))
          (local.set $pc (i32.add (local.get $pc) (i32.const 1)))
          (br $step)))
        (if (i32.eq (local.get $op) (i32.const I_CHAR)) (then
          (if (i32.and (local.get $flags) (i32.const 1)) (then (local.set $c (call $case (local.get $c) (i32.const 0)))))
          (br_if $threads (i32.ne (local.get $c) (i32.load offset=4 (i32.add (local.get $code) (i32.shl (local.get $pc) (i32.const 2))))))
          (local.set $pos (i32.add (local.get $pos) (global.get $cplen)))
          (local.set $pc (i32.add (local.get $pc) (i32.const 2)))
          (br $step)))
        ;; I_CLASS
        (local.set $k (i32.load offset=4 (i32.add (local.get $code) (i32.shl (local.get $pc) (i32.const 2)))))
        (local.set $op (call $class_has (local.get $k) (local.get $c)))
        (if (i32.and (i32.eqz (local.get $op)) (i32.ne (i32.and (local.get $flags) (i32.const 1)) (i32.const 0))) (then
          (local.set $op (i32.or (call $class_has (local.get $k) (call $case (local.get $c) (i32.const 0)))
                                 (call $class_has (local.get $k) (call $case (local.get $c) (i32.const 1)))))))
        (br_if $threads (i32.eq (local.get $op) (i32.load (local.get $k))))
        (local.set $pos (i32.add (local.get $pos) (global.get $cplen)))
        (local.set $pc (i32.add (local.get $pc) (i32.const 2)))
        (br $step))))
  (call $v_free (local.get $stack))
  (i32.const 0))

;; The first match at or after offset `from` (on code point starts): its start, or -1.
(func $re_find (param $prog i32) (param $s i32) (param $n i32) (param $from i32) (result i32)
  (loop $starts
    (if (i32.le_u (local.get $from) (local.get $n)) (then
      (if (call $re_run (local.get $prog) (local.get $s) (local.get $n) (local.get $from)) (then (return (local.get $from))))
      (if (i32.eq (local.get $from) (local.get $n)) (then (return (i32.const -1))))
      (drop (call $decode (i32.add (local.get $s) (local.get $from))))
      (local.set $from (i32.add (local.get $from) (global.get $cplen)))
      (br $starts))))
  (i32.const -1))

(func $re_search (param $prog i32) (param $s i32) (param $n i32) (param $from i32) (result i32)
  (if (i32.eqz (global.get $caps)) (then (global.set $caps (call $alloc (i32.const 272)))))
  (i32.ge_s (call $re_find (local.get $prog) (local.get $s) (local.get $n) (local.get $from)) (i32.const 0)))

;; REPLACE(str, pattern, replacement): every non-overlapping match replaced;
;; $n in the replacement is group n. A pattern that matches "" is not the
;; XPath error (FORX0003): as in Oxigraph, an empty match advances one character.
(func $re_replace (param $prog i32) (param $str i32) (param $repl i32) (result i32)
  (local $s i32) (local $n i32) (local $at i32) (local $m i32) (local $r i32) (local $rn i32) (local $i i32) (local $c i32) (local $g i32)
  (local $out i32) (local $outn i32)
  (if (i32.eqz (global.get $caps)) (then (global.set $caps (call $alloc (i32.const 272)))))
  (local.set $s (call $tval (local.get $str)))
  (local.set $n (call $tlen (local.get $str)))
  (local.set $r (call $tval (local.get $repl)))
  (local.set $rn (call $tlen (local.get $repl)))
  (call $xb_reset)
  (loop $matches
    (if (i32.ge_s (local.tee $m (call $re_find (local.get $prog) (local.get $s) (local.get $n) (local.get $at))) (i32.const 0)) (then
      (call $xb_put (i32.add (local.get $s) (local.get $at)) (i32.sub (local.get $m) (local.get $at)))
      ;; the replacement, with group references
      (local.set $i (i32.const 0))
      (loop $chars
        (if (i32.lt_u (local.get $i) (local.get $rn)) (then
          (local.set $c (i32.load8_u (i32.add (local.get $r) (local.get $i))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (if (i32.eq (local.get $c) (i32.const '\\')) (then
            (if (i32.ge_u (local.get $i) (local.get $rn)) (then (return (i32.const 0))))
            (local.set $c (i32.load8_u (i32.add (local.get $r) (local.get $i))))
            (if (i32.eqz (call $one_of (local.get $c) (@str "\\$"))) (then (return (i32.const 0))))
            (local.set $i (i32.add (local.get $i) (i32.const 1)))
            (call $xb_byte (local.get $c))
            (br $chars)))
          (if (i32.eq (local.get $c) (i32.const '$')) (then
            (if (i32.or (i32.ge_u (local.get $i) (local.get $rn)) (i32.eqz (call $is_digit (i32.load8_u (i32.add (local.get $r) (local.get $i))))))
              (then (return (i32.const 0))))
            (local.set $g (i32.sub (i32.load8_u (i32.add (local.get $r) (local.get $i))) (i32.const '0')))
            (local.set $i (i32.add (local.get $i) (i32.const 1)))
            ;; a second digit when there are that many groups
            (if (i32.lt_u (local.get $i) (local.get $rn)) (then
              (if (call $is_digit (i32.load8_u (i32.add (local.get $r) (local.get $i)))) (then
                (if (i32.le_u (i32.add (i32.mul (local.get $g) (i32.const 10)) (i32.sub (i32.load8_u (i32.add (local.get $r) (local.get $i))) (i32.const '0')))
                              (i32.load (local.get $prog))) (then
                  (local.set $g (i32.add (i32.mul (local.get $g) (i32.const 10)) (i32.sub (i32.load8_u (i32.add (local.get $r) (local.get $i))) (i32.const '0'))))
                  (local.set $i (i32.add (local.get $i) (i32.const 1)))))))))
            (if (i32.eqz (local.get $g))
              (then (call $xb_put (i32.add (local.get $s) (local.get $m)) (i32.sub (global.get $m_end) (local.get $m))))
              (else
                (if (i32.and (i32.le_u (local.get $g) (i32.load (local.get $prog))) (i32.le_u (local.get $g) (i32.const 32))) (then
                  (if (i32.ge_s (i32.load (i32.add (global.get $caps) (i32.shl (i32.shl (local.get $g) (i32.const 1)) (i32.const 2)))) (i32.const 0)) (then
                    (if (i32.ge_s (i32.load (i32.add (global.get $caps) (i32.shl (i32.add (i32.shl (local.get $g) (i32.const 1)) (i32.const 1)) (i32.const 2)))) (i32.const 0)) (then
                      (call $xb_put (i32.add (local.get $s) (i32.load (i32.add (global.get $caps) (i32.shl (i32.shl (local.get $g) (i32.const 1)) (i32.const 2)))))
                        (i32.sub (i32.load (i32.add (global.get $caps) (i32.shl (i32.add (i32.shl (local.get $g) (i32.const 1)) (i32.const 1)) (i32.const 2))))
                                 (i32.load (i32.add (global.get $caps) (i32.shl (i32.shl (local.get $g) (i32.const 1)) (i32.const 2))))))))))))))
            (br $chars)))
          (call $xb_byte (local.get $c))
          (br $chars))))
      (local.set $at (global.get $m_end))
      (if (i32.eq (global.get $m_end) (local.get $m)) (then
        ;; an empty match: copy one character and move on
        (if (i32.ge_u (local.get $at) (local.get $n)) (then (local.set $at (i32.add (local.get $n) (i32.const 1)))) (else
          (drop (call $decode (i32.add (local.get $s) (local.get $at))))
          (call $xb_put (i32.add (local.get $s) (local.get $at)) (global.get $cplen))
          (local.set $at (i32.add (local.get $at) (global.get $cplen)))))))
      (br_if $matches (i32.le_u (local.get $at) (local.get $n))))))
  (if (i32.lt_u (local.get $at) (local.get $n)) (then
    (call $xb_put (i32.add (local.get $s) (local.get $at)) (i32.sub (local.get $n) (local.get $at)))))
  (call $xb_string (local.get $str)))
