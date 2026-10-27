;; Lexing, shared by N-Triples, N-Quads, Turtle and TriG (and SPARQL later).
;;
;; Input is [$p, $end), always the input buffer. Escapes are decoded in place:
;; a decoded IRI, string or local name is never longer than its source, so it
;; is written over the bytes it came from, and later tokens never write below
;; their own start. Decoded text stays valid for the whole load.
;;
;; The scanners ($scan_*) start on a token's first character, leave its text
;; in [$str, $str + $str_len) and return 1, or 0 after setting the error.
;; $lex (Turtle, TriG) skips white space and comments and reads one token:
;; its type goes to $tok, T_ERR after an error.

(@define T_ERR 0)
(@define T_EOF 1)
(@define T_IRI 2)       ;; $str: the IRI as written (relative or not)
(@define T_PNAME 3)     ;; $pfx: prefix without ':'; $str: local name, unescaped
(@define T_BNODE 4)     ;; $str: label
(@define T_STRING 5)    ;; $str: value; $str_long: whether """ or '''
(@define T_LANG 6)      ;; $str: tag as written, without '@' and direction; $dir
(@define T_INTEGER 7)   ;; numbers: $str is the lexical form
(@define T_DECIMAL 8)
(@define T_DOUBLE 9)
(@define T_NAME 10)     ;; a bare word: a, true, false, PREFIX, BASE, VERSION, GRAPH
(@define T_HATS 11)     ;; ^^
(@define T_LTLT 12)     ;; <<
(@define T_GTGT 13)     ;; >>
(@define T_TT_OPEN 14)  ;; <<(
(@define T_TT_CLOSE 15) ;; )>>
(@define T_ANN_OPEN 16) ;; {|
(@define T_ANN_CLOSE 17) ;; |}
;; SPARQL only ($sparql set):
(@define T_VAR 18)      ;; ?name or $name; $str: name
(@define T_LE 19)       ;; <=
(@define T_GE 20)       ;; >=
(@define T_NE 21)       ;; !=
(@define T_AND 22)      ;; &&
(@define T_OR 23)       ;; ||
;; Other punctuation is its own character: . ; , [ ] ( ) { } ~ and, in SPARQL,
;; ? ! = < > * / + - ^ |

(global $p (mut i32) (i32.const 0))
(global $end (mut i32) (i32.const 0))
(global $line (mut i32) (i32.const 0))
(global $cplen (mut i32) (i32.const 0))   ;; set by $decode: bytes in the code point
(global $tok (mut i32) (i32.const 0))
(global $str (mut i32) (i32.const 0))
(global $str_len (mut i32) (i32.const 0))
(global $str_long (mut i32) (i32.const 0))
(global $pfx (mut i32) (i32.const 0))
(global $pfx_len (mut i32) (i32.const 0))
(global $dir (mut i32) (i32.const 0))
(global $sparql (mut i32) (i32.const 0))   ;; lex SPARQL rather than Turtle

;; A syntax error: "line N: msg". Returns 0.
(func $syntax (param $msg i32) (param $n i32) (result i32)
  (local $w i32)
  (i64.store (global.get $errbuf) (i64.const 0x20656e696c))   ;; "line "
  (local.set $w (call $utoa (i32.add (global.get $errbuf) (i32.const 5)) (global.get $line)))
  (i32.store16 (local.get $w) (i32.const 0x203a))              ;; ": "
  (memory.copy (i32.add (local.get $w) (i32.const 2)) (local.get $msg) (local.get $n))
  (drop (call $fail (global.get $errbuf) (i32.sub (i32.add (i32.add (local.get $w) (i32.const 2)) (local.get $n)) (global.get $errbuf))))
  (global.set $tok (i32.const T_ERR))
  (i32.const 0))

(func $peek (result i32)
  (if (result i32) (i32.lt_u (global.get $p) (global.get $end))
    (then (i32.load8_u (global.get $p)))
    (else (i32.const -1))))

(func $peek_at (param $off i32) (result i32)
  (local $at i32)
  (if (result i32) (i32.lt_u (local.tee $at (i32.add (global.get $p) (local.get $off))) (global.get $end))
    (then (i32.load8_u (local.get $at)))
    (else (i32.const -1))))

(func $advance (param $n i32)
  (global.set $p (i32.add (global.get $p) (local.get $n))))

;; Whether the input at $p starts with the n bytes at s.
(func $starts (param $s i32) (param $n i32) (result i32)
  (if (result i32) (i32.le_u (local.get $n) (i32.sub (global.get $end) (global.get $p)))
    (then (call $memeq (global.get $p) (local.get $s) (local.get $n)))
    (else (i32.const 0))))

;; Whether the n bytes at p equal the k bytes at s, ASCII case aside when ci.
(func $same (param $p i32) (param $n i32) (param $s i32) (param $k i32) (param $ci i32) (result i32)
  (local $m i32)
  (if (i32.ne (local.get $n) (local.get $k)) (then (return (i32.const 0))))
  (local.set $m (select (i32.const 0x20) (i32.const 0) (local.get $ci)))
  (loop $l
    (if (local.get $n) (then
      (if (i32.ne (i32.or (i32.load8_u (local.get $p)) (local.get $m)) (i32.or (i32.load8_u (local.get $s)) (local.get $m)))
        (then (return (i32.const 0))))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br $l))))
  (i32.const 1))

;; --- characters ---------------------------------------------------------------

;; The code point of the UTF-8 sequence at p (valid UTF-8: the host encodes
;; it); its length goes to $cplen.
(func $decode (param $p i32) (result i32)
  (local $c i32)
  (local.set $c (i32.load8_u (local.get $p)))
  (if (i32.lt_u (local.get $c) (i32.const 0x80)) (then (global.set $cplen (i32.const 1)) (return (local.get $c))))
  (if (i32.lt_u (local.get $c) (i32.const 0xe0)) (then
    (global.set $cplen (i32.const 2))
    (return (i32.or (i32.shl (i32.and (local.get $c) (i32.const 0x1f)) (i32.const 6))
                    (i32.and (i32.load8_u offset=1 (local.get $p)) (i32.const 0x3f))))))
  (if (i32.lt_u (local.get $c) (i32.const 0xf0)) (then
    (global.set $cplen (i32.const 3))
    (return (i32.or (i32.or (i32.shl (i32.and (local.get $c) (i32.const 0x0f)) (i32.const 12))
                            (i32.shl (i32.and (i32.load8_u offset=1 (local.get $p)) (i32.const 0x3f)) (i32.const 6)))
                    (i32.and (i32.load8_u offset=2 (local.get $p)) (i32.const 0x3f))))))
  (global.set $cplen (i32.const 4))
  (i32.or (i32.or (i32.shl (i32.and (local.get $c) (i32.const 0x07)) (i32.const 18))
                  (i32.shl (i32.and (i32.load8_u offset=1 (local.get $p)) (i32.const 0x3f)) (i32.const 12)))
          (i32.or (i32.shl (i32.and (i32.load8_u offset=2 (local.get $p)) (i32.const 0x3f)) (i32.const 6))
                  (i32.and (i32.load8_u offset=3 (local.get $p)) (i32.const 0x3f)))))

;; Write code point cp as UTF-8 at w; returns the end.
(func $encode (param $w i32) (param $cp i32) (result i32)
  (if (i32.lt_u (local.get $cp) (i32.const 0x80)) (then
    (i32.store8 (local.get $w) (local.get $cp))
    (return (i32.add (local.get $w) (i32.const 1)))))
  (if (i32.lt_u (local.get $cp) (i32.const 0x800)) (then
    (i32.store8 (local.get $w) (i32.or (i32.const 0xc0) (i32.shr_u (local.get $cp) (i32.const 6))))
    (i32.store8 offset=1 (local.get $w) (i32.or (i32.const 0x80) (i32.and (local.get $cp) (i32.const 0x3f))))
    (return (i32.add (local.get $w) (i32.const 2)))))
  (if (i32.lt_u (local.get $cp) (i32.const 0x10000)) (then
    (i32.store8 (local.get $w) (i32.or (i32.const 0xe0) (i32.shr_u (local.get $cp) (i32.const 12))))
    (i32.store8 offset=1 (local.get $w) (i32.or (i32.const 0x80) (i32.and (i32.shr_u (local.get $cp) (i32.const 6)) (i32.const 0x3f))))
    (i32.store8 offset=2 (local.get $w) (i32.or (i32.const 0x80) (i32.and (local.get $cp) (i32.const 0x3f))))
    (return (i32.add (local.get $w) (i32.const 3)))))
  (i32.store8 (local.get $w) (i32.or (i32.const 0xf0) (i32.shr_u (local.get $cp) (i32.const 18))))
  (i32.store8 offset=1 (local.get $w) (i32.or (i32.const 0x80) (i32.and (i32.shr_u (local.get $cp) (i32.const 12)) (i32.const 0x3f))))
  (i32.store8 offset=2 (local.get $w) (i32.or (i32.const 0x80) (i32.and (i32.shr_u (local.get $cp) (i32.const 6)) (i32.const 0x3f))))
  (i32.store8 offset=3 (local.get $w) (i32.or (i32.const 0x80) (i32.and (local.get $cp) (i32.const 0x3f))))
  (i32.add (local.get $w) (i32.const 4)))

(func $is_digit (param $c i32) (result i32)
  (i32.lt_u (i32.sub (local.get $c) (i32.const '0')) (i32.const 10)))

(func $is_alpha (param $c i32) (result i32)
  (i32.lt_u (i32.sub (i32.or (local.get $c) (i32.const 0x20)) (i32.const 'a')) (i32.const 26)))

;; The value of a hex digit, or -1.
(func $hexval (param $c i32) (result i32)
  (if (call $is_digit (local.get $c)) (then (return (i32.sub (local.get $c) (i32.const '0')))))
  (if (i32.lt_u (i32.sub (i32.or (local.get $c) (i32.const 0x20)) (i32.const 'a')) (i32.const 6))
    (then (return (i32.sub (i32.or (local.get $c) (i32.const 0x20)) (i32.const 87)))))
  (i32.const -1))

(func $in (param $c i32) (param $lo i32) (param $hi i32) (result i32)
  (i32.le_u (i32.sub (local.get $c) (local.get $lo)) (i32.sub (local.get $hi) (local.get $lo))))

;; Whether byte c is one of the n bytes at s.
(func $one_of (param $c i32) (param $s i32) (param $n i32) (result i32)
  (loop $l
    (if (local.get $n) (then
      (if (i32.eq (i32.load8_u (local.get $s)) (local.get $c)) (then (return (i32.const 1))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br $l))))
  (i32.const 0))

;; PN_CHARS_U: PN_CHARS_BASE or '_'.
(func $pn_u (param $c i32) (result i32)
  (if (i32.or (call $is_alpha (local.get $c)) (i32.eq (local.get $c) (i32.const '_'))) (then (return (i32.const 1))))
  (if (i32.lt_u (local.get $c) (i32.const 0xc0)) (then (return (i32.const 0))))
  (i32.or
    (i32.or
      (i32.or (i32.or (call $in (local.get $c) (i32.const 0xc0) (i32.const 0xd6)) (call $in (local.get $c) (i32.const 0xd8) (i32.const 0xf6)))
              (i32.or (call $in (local.get $c) (i32.const 0xf8) (i32.const 0x2ff)) (call $in (local.get $c) (i32.const 0x370) (i32.const 0x37d))))
      (i32.or (i32.or (call $in (local.get $c) (i32.const 0x37f) (i32.const 0x1fff)) (call $in (local.get $c) (i32.const 0x200c) (i32.const 0x200d)))
              (i32.or (call $in (local.get $c) (i32.const 0x2070) (i32.const 0x218f)) (call $in (local.get $c) (i32.const 0x2c00) (i32.const 0x2fef)))))
    (i32.or
      (i32.or (call $in (local.get $c) (i32.const 0x3001) (i32.const 0xd7ff)) (call $in (local.get $c) (i32.const 0xf900) (i32.const 0xfdcf)))
      (i32.or (call $in (local.get $c) (i32.const 0xfdf0) (i32.const 0xfffd)) (call $in (local.get $c) (i32.const 0x10000) (i32.const 0xeffff))))))

;; PN_CHARS: PN_CHARS_U, '-', digits, U+00B7, U+0300-036F, U+203F-2040.
(func $pn_chars (param $c i32) (result i32)
  (i32.or
    (i32.or (call $pn_u (local.get $c)) (i32.or (i32.eq (local.get $c) (i32.const '-')) (call $is_digit (local.get $c))))
    (i32.or (i32.eq (local.get $c) (i32.const 0xb7))
            (i32.or (call $in (local.get $c) (i32.const 0x300) (i32.const 0x36f)) (call $in (local.get $c) (i32.const 0x203f) (i32.const 0x2040))))))

;; From $p, the run of (PN_CHARS | '.')* not ending in '.'; $p moves to its end.
(func $pn_run
  (local $last i32)
  (local.set $last (global.get $p))
  (block $done
    (loop $chars
      (br_if $done (i32.ge_u (global.get $p) (global.get $end)))
      (if (i32.eq (i32.load8_u (global.get $p)) (i32.const '.')) (then
        (call $advance (i32.const 1))
        (br $chars)))
      (br_if $done (i32.eqz (call $pn_chars (call $decode (global.get $p)))))
      (global.set $p (local.tee $last (i32.add (global.get $p) (global.get $cplen))))
      (br $chars)))
  (global.set $p (local.get $last)))

;; $p is at the 'u' or 'U' of a UCHAR: the code point, or -1 after an error.
(func $uchar (result i32)
  (local $n i32) (local $cp i32) (local $c i32)
  (local.set $n (select (i32.const 4) (i32.const 8) (i32.eq (call $peek) (i32.const 'u'))))
  (loop $hex
    (call $advance (i32.const 1))
    (if (i32.lt_s (local.tee $c (call $hexval (call $peek))) (i32.const 0))
      (then (drop (call $syntax (@str "bad hex digit in escape"))) (return (i32.const -1))))
    (local.set $cp (i32.or (i32.shl (local.get $cp) (i32.const 4)) (local.get $c)))
    (br_if $hex (local.tee $n (i32.sub (local.get $n) (i32.const 1)))))
  (call $advance (i32.const 1))
  (if (i32.or (i32.gt_u (local.get $cp) (i32.const 0x10ffff))
              (i32.eq (i32.and (local.get $cp) (i32.const 0xfffff800)) (i32.const 0xd800)))
    (then (drop (call $syntax (@str "escape is not a Unicode scalar value"))) (return (i32.const -1))))
  (local.get $cp))

;; The character an ECHAR stands for (c follows the backslash), or -1.
(func $echar (param $c i32) (result i32)
  (if (i32.eq (local.get $c) (i32.const 't')) (then (return (i32.const 9))))
  (if (i32.eq (local.get $c) (i32.const 'b')) (then (return (i32.const 8))))
  (if (i32.eq (local.get $c) (i32.const 'n')) (then (return (i32.const 10))))
  (if (i32.eq (local.get $c) (i32.const 'r')) (then (return (i32.const 13))))
  (if (i32.eq (local.get $c) (i32.const 'f')) (then (return (i32.const 12))))
  (if (i32.or (i32.eq (local.get $c) (i32.const '"'))
              (i32.or (i32.eq (local.get $c) (i32.const '\'')) (i32.eq (local.get $c) (i32.const '\\'))))
    (then (return (local.get $c))))
  (i32.const -1))

;; Characters IRIREF excludes: controls, space, <>"{}|^`\
(func $iri_bad (param $c i32) (result i32)
  (if (result i32) (i32.lt_u (local.get $c) (i32.const 64))
    (then (i32.or (i32.le_u (local.get $c) (i32.const 0x20))
                  (i32.wrap_i64 (i64.and (i64.shr_u (i64.const 0x5000000400000000) (i64.extend_i32_u (local.get $c))) (i64.const 1)))))
    (else (if (result i32) (i32.lt_u (local.get $c) (i32.const 128))
      (then (i32.wrap_i64 (i64.and (i64.shr_u (i64.const 0x3800000150000000) (i64.extend_i32_u (i32.sub (local.get $c) (i32.const 64)))) (i64.const 1))))
      (else (i32.const 0))))))

;; --- scanners --------------------------------------------------------------------

;; IRIREF (on '<'), unescaped. The scan also counts what $iri_check looks
;; at, and leaves the common IRI (a scheme, one ':', at most one '#', no '%',
;; brackets or escapes) unchecked.
(func $scan_iri (result i32)
  (local $w i32) (local $c i32) (local $q i32) (local $colons i32) (local $hashes i32) (local $rare i32)
  (call $advance (i32.const 1))
  (global.set $str (global.get $p))
  (local.set $w (global.get $p))
  (block $done
    (loop $chars
      ;; fast path: bytes that are neither excluded nor counted
      (local.set $q (global.get $p))
      (block $slow
        (loop $plain
          (br_if $slow (i32.ge_u (local.get $q) (global.get $end)))
          (local.set $c (i32.load8_u (local.get $q)))
          (br_if $slow (if (result i32) (i32.lt_u (local.get $c) (i32.const 64))
            (then (i32.wrap_i64 (i64.and (i64.shr_u (i64.const 0xd400802dffffffff) (i64.extend_i32_u (local.get $c))) (i64.const 1))))
            (else (i32.and (i32.lt_u (local.get $c) (i32.const 128))
              (i32.wrap_i64 (i64.and (i64.shr_u (i64.const 0x3800000178000000) (i64.extend_i32_u (i32.sub (local.get $c) (i32.const 64)))) (i64.const 1)))))))
          (i32.store8 (local.get $w) (local.get $c))
          (local.set $w (i32.add (local.get $w) (i32.const 1)))
          (local.set $q (i32.add (local.get $q) (i32.const 1)))
          (br $plain)))
      (global.set $p (local.get $q))
      (local.set $c (call $peek))
      (br_if $done (i32.eq (local.get $c) (i32.const '>')))
      (if (i32.lt_s (local.get $c) (i32.const 0)) (then (return (call $syntax (@str "unterminated IRI")))))
      (if (i32.eq (local.get $c) (i32.const '\\')) (then
        (call $advance (i32.const 1))
        (local.set $c (call $peek))
        (if (i32.eqz (i32.or (i32.eq (local.get $c) (i32.const 'u')) (i32.eq (local.get $c) (i32.const 'U'))))
          (then (return (call $syntax (@str "bad escape in IRI")))))
        (if (i32.lt_s (local.tee $c (call $uchar)) (i32.const 0)) (then (return (i32.const 0))))
        (if (call $iri_bad (local.get $c)) (then (return (call $syntax (@str "bad character in IRI")))))
        (local.set $w (call $encode (local.get $w) (local.get $c)))
        (local.set $rare (i32.const 1))
        (br $chars)))
      (if (call $iri_bad (local.get $c)) (then (return (call $syntax (@str "bad character in IRI")))))
      (if (call $iri_special (local.get $c)) (then
        (local.set $colons (i32.add (local.get $colons) (i32.eq (local.get $c) (i32.const ':'))))
        (local.set $hashes (i32.add (local.get $hashes) (i32.eq (local.get $c) (i32.const '#'))))
        (local.set $rare (i32.or (local.get $rare)
          (i32.or (i32.eq (local.get $c) (i32.const '%')) (i32.gt_u (local.get $c) (i32.const 64)))))))   ;; % [ ]
      (i32.store8 (local.get $w) (local.get $c))
      (local.set $w (i32.add (local.get $w) (i32.const 1)))
      (call $advance (i32.const 1))
      (br $chars)))
  (call $advance (i32.const 1))
  (global.set $str_len (i32.sub (local.get $w) (global.get $str)))
  (if (result i32) (i32.or (i32.or (local.get $rare) (i32.gt_u (local.get $colons) (i32.const 1)))
                           (i32.or (i32.gt_u (local.get $hashes) (i32.const 1))
                                   (i32.eqz (call $scheme_len (global.get $str) (global.get $str_len)))))
    (then (call $iri_check (global.get $str) (global.get $str_len)))
    (else (i32.const 1))))

;; Whether c (a byte) is one of # % / : ? [ ], the bytes $iri_check looks at.
(func $iri_special (param $c i32) (result i32)
  (if (result i32) (i32.lt_u (local.get $c) (i32.const 64))
    (then (i32.wrap_i64 (i64.and (i64.shr_u (i64.const 0x8400802800000000) (i64.extend_i32_u (local.get $c))) (i64.const 1))))
    (else (i32.or (i32.eq (local.get $c) (i32.const '[')) (i32.eq (local.get $c) (i32.const ']'))))))

;; The cheap parts of RFC 3987 that the IRIREF grammar leaves out: '%' starts
;; two hex digits; a reference without a scheme has no ':' in its first
;; segment; one '#' at most; an authority is [userinfo '@'] host [':' digits]
;; where the host may be an IP literal in '[' ']', and brackets appear nowhere
;; else. Returns 1, or 0 after an error.
(func $iri_check (param $p i32) (param $n i32) (result i32)
  (local $i i32) (local $c i32) (local $e i32) (local $seg i32) (local $frag i32)
  (local $h i32) (local $auth i32) (local $auth_end i32) (local $colon i32) (local $lit i32) (local $close i32)
  (local.set $e (i32.add (local.get $p) (local.get $n)))
  (local.set $i (call $scheme_len (local.get $p) (local.get $n)))
  (local.set $seg (i32.eqz (local.get $i)))   ;; in the first segment of a scheme-less reference
  (local.set $i (i32.add (local.get $p) (select (i32.add (local.get $i) (i32.const 1)) (i32.const 0) (local.get $i))))
  (local.set $auth (local.get $i))
  (local.set $auth_end (local.get $i))
  (block $bad
    (if (i32.and (i32.le_u (i32.add (local.get $i) (i32.const 2)) (local.get $e))
                 (i32.eq (i32.load16_u (local.get $i)) (i32.const 0x2f2f))) (then   ;; "//": the authority
      (local.set $seg (i32.const 0))
      (local.set $h (local.tee $i (i32.add (local.get $i) (i32.const 2))))
      (block $end
        (loop $chars
          (br_if $end (i32.ge_u (local.get $i) (local.get $e)))
          (local.set $c (i32.load8_u (local.get $i)))
          (br_if $end (i32.or (i32.eq (local.get $c) (i32.const '/'))
                              (i32.or (i32.eq (local.get $c) (i32.const '?')) (i32.eq (local.get $c) (i32.const '#')))))
          (if (i32.eq (local.get $c) (i32.const '@')) (then   ;; that was userinfo: the host starts after it
            (br_if $bad (i32.or (local.get $lit) (local.get $close)))
            (local.set $h (i32.add (local.get $i) (i32.const 1)))
            (local.set $colon (i32.const 0))))
          (if (i32.eq (local.get $c) (i32.const '[')) (then
            (br_if $bad (i32.ne (local.get $i) (local.get $h)))
            (local.set $lit (i32.const 1))))
          (if (i32.eq (local.get $c) (i32.const ']')) (then
            (br_if $bad (i32.eqz (local.get $lit)))
            (local.set $lit (i32.const 0))
            (local.set $close (i32.add (local.get $i) (i32.const 1)))))
          (if (i32.and (i32.eq (local.get $c) (i32.const ':')) (i32.eqz (i32.or (local.get $lit) (local.get $colon))))
            (then (local.set $colon (local.get $i))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $chars)))
      (local.set $auth_end (local.get $i))
      (br_if $bad (local.get $lit))
      ;; after an IP literal comes the end of the authority or the port
      (br_if $bad (i32.and (i32.ne (local.get $close) (i32.const 0))
                           (i32.and (i32.ne (local.get $close) (local.get $auth_end)) (i32.ne (local.get $close) (local.get $colon)))))
      (if (local.get $colon) (then
        (loop $port
          (if (i32.lt_u (local.tee $colon (i32.add (local.get $colon) (i32.const 1))) (local.get $auth_end)) (then
            (br_if $bad (i32.eqz (call $is_digit (i32.load8_u (local.get $colon)))))
            (br $port))))))))
    (local.set $i (local.get $auth))
    (loop $chars
      (if (i32.lt_u (local.get $i) (local.get $e)) (then
        (local.set $c (i32.load8_u (local.get $i)))
        (if (call $iri_special (local.get $c)) (then
          (if (i32.eq (local.get $c) (i32.const '%')) (then
            (br_if $bad (i32.ge_u (i32.add (local.get $i) (i32.const 2)) (local.get $e)))
            (br_if $bad (i32.lt_s (call $hexval (i32.load8_u offset=1 (local.get $i))) (i32.const 0)))
            (br_if $bad (i32.lt_s (call $hexval (i32.load8_u offset=2 (local.get $i))) (i32.const 0)))))
          (if (i32.eq (local.get $c) (i32.const '#')) (then
            (br_if $bad (local.get $frag))
            (local.set $frag (i32.const 1))))
          (br_if $bad (i32.and (local.get $seg) (i32.eq (local.get $c) (i32.const ':'))))
          (if (i32.or (i32.eq (local.get $c) (i32.const '[')) (i32.eq (local.get $c) (i32.const ']')))
            (then (br_if $bad (i32.ge_u (local.get $i) (local.get $auth_end)))))
          (local.set $seg (i32.and (local.get $seg) (i32.ne (local.get $c) (i32.const '/'))))
          (local.set $seg (i32.and (local.get $seg) (i32.and (i32.ne (local.get $c) (i32.const '?')) (i32.ne (local.get $c) (i32.const '#')))))))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $chars))))
    (return (i32.const 1)))
  (call $syntax (@str "bad IRI")))

;; A string (on '"' or '\''), short or long, unescaped.
(func $scan_string (result i32)
  (local $q i32) (local $w i32) (local $c i32) (local $i i32)
  (local.set $q (call $peek))
  (global.set $str_long (i32.and (i32.eq (call $peek_at (i32.const 1)) (local.get $q))
                                 (i32.eq (call $peek_at (i32.const 2)) (local.get $q))))
  (call $advance (select (i32.const 3) (i32.const 1) (global.get $str_long)))
  (global.set $str (global.get $p))
  (local.set $w (global.get $p))
  (block $done
    (loop $chars
      ;; fast path: bytes other than the quote, '\\' and line ends
      (local.set $i (global.get $p))
      (block $slow
        (loop $plain
          (br_if $slow (i32.ge_u (local.get $i) (global.get $end)))
          (local.set $c (i32.load8_u (local.get $i)))
          (br_if $slow (i32.or (i32.or (i32.eq (local.get $c) (local.get $q)) (i32.eq (local.get $c) (i32.const '\\')))
                               (i32.or (i32.eq (local.get $c) (i32.const '\n')) (i32.eq (local.get $c) (i32.const '\r')))))
          (i32.store8 (local.get $w) (local.get $c))
          (local.set $w (i32.add (local.get $w) (i32.const 1)))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $plain)))
      (global.set $p (local.get $i))
      (local.set $c (call $peek))
      (if (i32.eq (local.get $c) (local.get $q)) (then
        (br_if $done (i32.eqz (global.get $str_long)))
        (br_if $done (i32.and (i32.eq (call $peek_at (i32.const 1)) (local.get $q))
                              (i32.eq (call $peek_at (i32.const 2)) (local.get $q))))))
      (if (i32.lt_s (local.get $c) (i32.const 0)) (then (return (call $syntax (@str "unterminated string")))))
      (if (i32.or (i32.eq (local.get $c) (i32.const '\n')) (i32.eq (local.get $c) (i32.const '\r'))) (then
        (if (i32.eqz (global.get $str_long)) (then (return (call $syntax (@str "line break in string")))))
        (global.set $line (i32.add (global.get $line) (i32.eq (local.get $c) (i32.const '\n'))))))
      (if (i32.eq (local.get $c) (i32.const '\\')) (then
        (call $advance (i32.const 1))
        (local.set $c (call $peek))
        (if (i32.or (i32.eq (local.get $c) (i32.const 'u')) (i32.eq (local.get $c) (i32.const 'U'))) (then
          (if (i32.lt_s (local.tee $c (call $uchar)) (i32.const 0)) (then (return (i32.const 0))))
          (local.set $w (call $encode (local.get $w) (local.get $c)))
          (br $chars)))
        (if (i32.lt_s (local.tee $c (call $echar (local.get $c))) (i32.const 0))
          (then (return (call $syntax (@str "bad escape in string")))))))
      (i32.store8 (local.get $w) (local.get $c))
      (local.set $w (i32.add (local.get $w) (i32.const 1)))
      (call $advance (i32.const 1))
      (br $chars)))
  (call $advance (select (i32.const 3) (i32.const 1) (global.get $str_long)))
  (global.set $str_len (i32.sub (local.get $w) (global.get $str)))
  (i32.const 1))

;; BLANK_NODE_LABEL (on '_').
(func $scan_label (result i32)
  (local $c i32)
  (if (i32.ne (call $peek_at (i32.const 1)) (i32.const ':')) (then (return (call $syntax (@str "expected ':' after '_'")))))
  (call $advance (i32.const 2))
  (global.set $str (global.get $p))
  (if (i32.ge_u (global.get $p) (global.get $end)) (then (return (call $syntax (@str "bad blank node label")))))
  (local.set $c (call $decode (global.get $p)))
  (if (i32.eqz (i32.or (call $pn_u (local.get $c)) (call $is_digit (local.get $c))))
    (then (return (call $syntax (@str "bad blank node label")))))
  (call $advance (global.get $cplen))
  (call $pn_run)
  (global.set $str_len (i32.sub (global.get $p) (global.get $str)))
  (i32.const 1))

;; LANG_DIR (on '@'): [a-zA-Z]{1,8} ('-' [a-zA-Z0-9]{1,8})* ('--' ('ltr' | 'rtl'))?
(func $scan_lang (result i32)
  (local $k i32) (local $first i32)
  (global.set $str (i32.add (global.get $p) (i32.const 1)))
  (global.set $dir (i32.const 0))
  (local.set $first (i32.const 1))
  (loop $subtag
    (call $advance (i32.const 1))
    (local.set $k (global.get $p))
    (loop $chars
      (if (i32.or (call $is_alpha (call $peek)) (i32.and (i32.eqz (local.get $first)) (call $is_digit (call $peek)))) (then
        (call $advance (i32.const 1))
        (br $chars))))
    (if (i32.gt_u (i32.sub (i32.sub (global.get $p) (local.get $k)) (i32.const 1)) (i32.const 7))
      (then (return (call $syntax (@str "bad language tag")))))
    ;; BCP 47: a one-letter language is only x (private use) or i (grandfathered)
    (if (i32.and (local.get $first) (i32.eq (i32.sub (global.get $p) (local.get $k)) (i32.const 1)))
      (then (if (i32.eqz (call $one_of (i32.or (i32.load8_u (local.get $k)) (i32.const 0x20)) (@str "xi")))
        (then (return (call $syntax (@str "bad language tag")))))))
    (local.set $first (i32.const 0))
    (br_if $subtag (i32.and (i32.eq (call $peek) (i32.const '-')) (i32.ne (call $peek_at (i32.const 1)) (i32.const '-'))))
    ;; a singleton (extension or private use) introduces at least one more subtag
    (if (i32.eq (i32.sub (global.get $p) (local.get $k)) (i32.const 1))
      (then (return (call $syntax (@str "bad language tag"))))))
  (global.set $str_len (i32.sub (global.get $p) (global.get $str)))
  (if (i32.eq (call $peek) (i32.const '-')) (then
    (call $advance (i32.const 2))
    (if (call $starts (@str "ltr")) (then (global.set $dir (i32.const 1))))
    (if (call $starts (@str "rtl")) (then (global.set $dir (i32.const 2))))
    (call $advance (i32.const 3))
    (if (i32.or (i32.eqz (global.get $dir)) (i32.or (call $is_alpha (call $peek)) (call $is_digit (call $peek))))
      (then (return (call $syntax (@str "bad direction: expected ltr or rtl")))))))
  (i32.const 1))

;; Whether the input at $p + off is an exponent: [eE] [+-]? [0-9]
(func $exponent_at (param $off i32) (result i32)
  (local $c i32)
  (if (i32.ne (i32.or (call $peek_at (local.get $off)) (i32.const 0x20)) (i32.const 'e')) (then (return (i32.const 0))))
  (local.set $c (call $peek_at (i32.add (local.get $off) (i32.const 1))))
  (if (i32.or (i32.eq (local.get $c) (i32.const '+')) (i32.eq (local.get $c) (i32.const '-')))
    (then (local.set $c (call $peek_at (i32.add (local.get $off) (i32.const 2))))))
  (call $is_digit (local.get $c)))

(func $digits
  (loop $l (if (call $is_digit (call $peek)) (then (call $advance (i32.const 1)) (br $l)))))

;; INTEGER, DECIMAL or DOUBLE (on a sign, digit or '.'): the token type, or T_ERR.
(func $scan_number (result i32)
  (local $t i32) (local $int i32)
  (global.set $str (global.get $p))
  (if (i32.or (i32.eq (call $peek) (i32.const '+')) (i32.eq (call $peek) (i32.const '-')))
    (then (call $advance (i32.const 1))))
  (local.set $int (global.get $p))
  (call $digits)
  (local.set $int (i32.ne (global.get $p) (local.get $int)))
  (local.set $t (i32.const T_INTEGER))
  (if (i32.eq (call $peek) (i32.const '.')) (then
    (if (i32.or (call $is_digit (call $peek_at (i32.const 1)))
                (i32.and (local.get $int) (call $exponent_at (i32.const 1)))) (then
      (call $advance (i32.const 1))
      (call $digits)
      (local.set $t (i32.const T_DECIMAL))
      (local.set $int (i32.const 1))))))
  (if (i32.and (local.get $int) (call $exponent_at (i32.const 0))) (then
    (call $advance (i32.const 1))
    (if (i32.eqz (call $is_digit (call $peek))) (then (call $advance (i32.const 1))))
    (call $digits)
    (local.set $t (i32.const T_DOUBLE))))
  (if (i32.eqz (local.get $int)) (then (return (call $syntax (@str "bad number")))))
  (global.set $str_len (i32.sub (global.get $p) (global.get $str)))
  (local.get $t))

;; A prefixed name or a bare word (on ':' or a PN_CHARS_BASE character): T_PNAME
;; with the local name unescaped in place, T_NAME, or T_ERR.
(func $scan_name (result i32)
  (local $c i32) (local $w i32) (local $lastp i32) (local $lastw i32) (local $first i32)
  (global.set $pfx (global.get $p))
  (if (i32.ne (call $peek) (i32.const ':')) (then
    (drop (call $decode (global.get $p)))
    (call $advance (global.get $cplen))
    (call $pn_run)))
  (global.set $pfx_len (i32.sub (global.get $p) (global.get $pfx)))
  (if (i32.ne (call $peek) (i32.const ':')) (then
    (global.set $str (global.get $pfx))
    (global.set $str_len (global.get $pfx_len))
    (return (i32.const T_NAME))))
  (call $advance (i32.const 1))
  ;; PN_LOCAL: (PN_CHARS_U | ':' | [0-9] | PLX) ((PN_CHARS | '.' | ':' | PLX)* (PN_CHARS | ':' | PLX))?
  (global.set $str (global.get $p))
  (local.set $w (global.get $p))
  (local.set $lastp (global.get $p))
  (local.set $lastw (global.get $p))
  (local.set $first (i32.const 1))
  (block $done
    (loop $chars
      (br_if $done (i32.ge_u (global.get $p) (global.get $end)))
      (local.set $c (i32.load8_u (global.get $p)))
      (block $char
        ;; fast path: ASCII letters, digits, '_' and (not first) '-'
        (if (i32.or (i32.or (call $is_alpha (local.get $c)) (call $is_digit (local.get $c)))
                    (i32.or (i32.eq (local.get $c) (i32.const '_')) (i32.and (i32.eq (local.get $c) (i32.const '-')) (i32.eqz (local.get $first))))) (then
          (i32.store8 (local.get $w) (local.get $c))
          (local.set $w (i32.add (local.get $w) (i32.const 1)))
          (call $advance (i32.const 1))
          (br $char)))
        (if (i32.eq (local.get $c) (i32.const '%')) (then
          (if (i32.or (i32.lt_s (call $hexval (call $peek_at (i32.const 1))) (i32.const 0))
                      (i32.lt_s (call $hexval (call $peek_at (i32.const 2))) (i32.const 0)))
            (then (return (call $syntax (@str "bad %-escape in local name")))))
          (memory.copy (local.get $w) (global.get $p) (i32.const 3))
          (local.set $w (i32.add (local.get $w) (i32.const 3)))
          (call $advance (i32.const 3))
          (br $char)))
        (if (i32.eq (local.get $c) (i32.const '\\')) (then
          (local.set $c (call $peek_at (i32.const 1)))
          (if (i32.eqz (call $one_of (local.get $c) (@str "_~.-!$&'()*+,;=/?#@%")))
            (then (return (call $syntax (@str "bad escape in local name")))))
          (i32.store8 (local.get $w) (local.get $c))
          (local.set $w (i32.add (local.get $w) (i32.const 1)))
          (call $advance (i32.const 2))
          (br $char)))
        (if (i32.eq (local.get $c) (i32.const '.')) (then
          (br_if $done (local.get $first))
          (i32.store8 (local.get $w) (local.get $c))
          (local.set $w (i32.add (local.get $w) (i32.const 1)))
          (call $advance (i32.const 1))
          (br $chars)))
        (if (i32.or (i32.eq (local.get $c) (i32.const ':')) (call $is_digit (local.get $c))) (then
          (i32.store8 (local.get $w) (local.get $c))
          (local.set $w (i32.add (local.get $w) (i32.const 1)))
          (call $advance (i32.const 1))
          (br $char)))
        (local.set $c (call $decode (global.get $p)))
        (br_if $done (i32.eqz (if (result i32) (local.get $first)
                                (then (call $pn_u (local.get $c)))
                                (else (call $pn_chars (local.get $c))))))
        (memory.copy (local.get $w) (global.get $p) (global.get $cplen))
        (local.set $w (i32.add (local.get $w) (global.get $cplen)))
        (call $advance (global.get $cplen)))
      (local.set $lastp (global.get $p))
      (local.set $lastw (local.get $w))
      (local.set $first (i32.const 0))
      (br $chars)))
  (global.set $p (local.get $lastp))
  (global.set $str_len (i32.sub (local.get $lastw) (global.get $str)))
  (i32.const T_PNAME))

;; --- the tokenizer ---------------------------------------------------------------

;; Skip white space, line ends and comments.
(func $skip
  (local $c i32)
  (loop $l
    (local.set $c (call $peek))
    (if (i32.or (i32.or (i32.eq (local.get $c) (i32.const ' ')) (i32.eq (local.get $c) (i32.const '\t')))
                (i32.eq (local.get $c) (i32.const '\r')))
      (then (call $advance (i32.const 1)) (br $l)))
    (if (i32.eq (local.get $c) (i32.const '\n')) (then
      (global.set $line (i32.add (global.get $line) (i32.const 1)))
      (call $advance (i32.const 1))
      (br $l)))
    (if (i32.eq (local.get $c) (i32.const '#')) (then
      (loop $comment
        (local.set $c (call $peek))
        (if (i32.and (i32.ge_s (local.get $c) (i32.const 0))
                     (i32.and (i32.ne (local.get $c) (i32.const '\n')) (i32.ne (local.get $c) (i32.const '\r')))) (then
          (call $advance (i32.const 1))
          (br $comment))))
      (br $l)))))

;; A punctuation token of n characters.
(func $punct (param $t i32) (param $n i32)
  (global.set $tok (local.get $t))
  (call $advance (local.get $n)))

;; Whether an IRIREF starts at $p (on '<'): its characters run to a '>'.
;; In SPARQL, '<' is otherwise less-than.
(func $iri_ahead (result i32)
  (local $q i32) (local $c i32)
  (local.set $q (global.get $p))
  (loop $chars
    (if (i32.ge_u (local.tee $q (i32.add (local.get $q) (i32.const 1))) (global.get $end)) (then (return (i32.const 0))))
    (local.set $c (i32.load8_u (local.get $q)))
    (if (i32.eq (local.get $c) (i32.const '>')) (then (return (i32.const 1))))
    (br_if $chars (i32.or (i32.eq (local.get $c) (i32.const '\\')) (i32.eqz (call $iri_bad (local.get $c))))))
  (i32.const 0))

;; A variable (on '?' or '$'): 1 when a VARNAME follows, else 0 (no error).
(func $scan_var (result i32)
  (local $c i32) (local $q i32)
  (local.set $q (i32.add (global.get $p) (i32.const 1)))
  (if (i32.ge_u (local.get $q) (global.get $end)) (then (return (i32.const 0))))
  (local.set $c (call $decode (local.get $q)))
  (if (i32.eqz (i32.or (call $pn_u (local.get $c)) (call $is_digit (local.get $c)))) (then (return (i32.const 0))))
  (global.set $str (local.get $q))
  (loop $chars
    (local.set $q (i32.add (local.get $q) (global.get $cplen)))
    (if (i32.lt_u (local.get $q) (global.get $end)) (then
      (local.set $c (call $decode (local.get $q)))
      (br_if $chars (i32.and (call $pn_chars (local.get $c)) (i32.ne (local.get $c) (i32.const '-')))))))
  (global.set $p (local.get $q))
  (global.set $str_len (i32.sub (local.get $q) (global.get $str)))
  (i32.const 1))

;; A one-character token; returns 1.
(func $single (param $c i32) (result i32)
  (call $punct (local.get $c) (i32.const 1))
  (i32.const 1))

;; SPARQL's own tokens; 1 when one was read.
(func $lex_sparql (param $c i32) (param $c1 i32) (result i32)
  (if (i32.or (i32.eq (local.get $c) (i32.const '?')) (i32.eq (local.get $c) (i32.const '$'))) (then
    (if (call $scan_var) (then (global.set $tok (i32.const T_VAR)) (return (i32.const 1))))
    (if (i32.eq (local.get $c) (i32.const '?')) (then (return (call $single (local.get $c)))))
    (return (i32.const 0))))
  (if (i32.and (i32.eq (local.get $c) (i32.const '<')) (i32.ne (local.get $c1) (i32.const '<'))) (then
    (if (call $iri_ahead) (then (return (i32.const 0))))
    (if (i32.eq (local.get $c1) (i32.const '=')) (then (call $punct (i32.const T_LE) (i32.const 2)) (return (i32.const 1))))
    (return (call $single (local.get $c)))))
  (if (i32.and (i32.eq (local.get $c) (i32.const '>')) (i32.ne (local.get $c1) (i32.const '>'))) (then
    (if (i32.eq (local.get $c1) (i32.const '=')) (then (call $punct (i32.const T_GE) (i32.const 2)) (return (i32.const 1))))
    (return (call $single (local.get $c)))))
  (if (i32.eq (local.get $c) (i32.const '!')) (then
    (if (i32.eq (local.get $c1) (i32.const '=')) (then (call $punct (i32.const T_NE) (i32.const 2)) (return (i32.const 1))))
    (return (call $single (local.get $c)))))
  (if (i32.and (i32.eq (local.get $c) (i32.const '&')) (i32.eq (local.get $c1) (i32.const '&')))
    (then (call $punct (i32.const T_AND) (i32.const 2)) (return (i32.const 1))))
  (if (i32.and (i32.eq (local.get $c) (i32.const '|')) (i32.eq (local.get $c1) (i32.const '|')))
    (then (call $punct (i32.const T_OR) (i32.const 2)) (return (i32.const 1))))
  (if (i32.and (i32.eq (local.get $c) (i32.const '|')) (i32.ne (local.get $c1) (i32.const '}'))) (then (return (call $single (local.get $c)))))
  (if (i32.and (i32.eq (local.get $c) (i32.const '^')) (i32.ne (local.get $c1) (i32.const '^'))) (then (return (call $single (local.get $c)))))
  (if (call $one_of (local.get $c) (@str "=*/")) (then (return (call $single (local.get $c)))))
  ;; + and - not starting a number
  (if (i32.or (i32.eq (local.get $c) (i32.const '+')) (i32.eq (local.get $c) (i32.const '-'))) (then
    (if (i32.eqz (i32.or (call $is_digit (local.get $c1))
                         (i32.and (i32.eq (local.get $c1) (i32.const '.')) (call $is_digit (call $peek_at (i32.const 2))))))
      (then (return (call $single (local.get $c)))))))
  (i32.const 0))

;; Read the next token into $tok.
(func $lex
  (local $c i32) (local $c1 i32)
  (call $skip)
  (local.set $c (call $peek))
  (local.set $c1 (call $peek_at (i32.const 1)))
  (if (i32.lt_s (local.get $c) (i32.const 0)) (then (global.set $tok (i32.const T_EOF)) (return)))
  (if (global.get $sparql) (then
    (if (call $lex_sparql (local.get $c) (local.get $c1)) (then (return)))))
  (if (i32.eq (local.get $c) (i32.const '<')) (then
    (if (i32.ne (local.get $c1) (i32.const '<')) (then
      (global.set $tok (select (i32.const T_IRI) (i32.const T_ERR) (call $scan_iri)))
      (return)))
    (if (i32.eq (call $peek_at (i32.const 2)) (i32.const '(')) (then (call $punct (i32.const T_TT_OPEN) (i32.const 3)) (return)))
    (call $punct (i32.const T_LTLT) (i32.const 2))
    (return)))
  (if (i32.and (i32.eq (local.get $c) (i32.const '>')) (i32.eq (local.get $c1) (i32.const '>')))
    (then (call $punct (i32.const T_GTGT) (i32.const 2)) (return)))
  (if (i32.eq (local.get $c) (i32.const ')')) (then
    (if (i32.and (i32.eq (local.get $c1) (i32.const '>')) (i32.eq (call $peek_at (i32.const 2)) (i32.const '>')))
      (then (call $punct (i32.const T_TT_CLOSE) (i32.const 3)) (return)))
    (call $punct (local.get $c) (i32.const 1))
    (return)))
  (if (i32.eq (local.get $c) (i32.const '{')) (then
    (if (i32.eq (local.get $c1) (i32.const '|')) (then (call $punct (i32.const T_ANN_OPEN) (i32.const 2)) (return)))
    (call $punct (local.get $c) (i32.const 1))
    (return)))
  (if (i32.and (i32.eq (local.get $c) (i32.const '|')) (i32.eq (local.get $c1) (i32.const '}')))
    (then (call $punct (i32.const T_ANN_CLOSE) (i32.const 2)) (return)))
  (if (i32.and (i32.eq (local.get $c) (i32.const '^')) (i32.eq (local.get $c1) (i32.const '^')))
    (then (call $punct (i32.const T_HATS) (i32.const 2)) (return)))
  (if (i32.or (i32.eq (local.get $c) (i32.const '"')) (i32.eq (local.get $c) (i32.const '\''))) (then
    (global.set $tok (select (i32.const T_STRING) (i32.const T_ERR) (call $scan_string)))
    (return)))
  (if (i32.eq (local.get $c) (i32.const '_')) (then
    (global.set $tok (select (i32.const T_BNODE) (i32.const T_ERR) (call $scan_label)))
    (return)))
  (if (i32.eq (local.get $c) (i32.const '@')) (then
    (global.set $tok (select (i32.const T_LANG) (i32.const T_ERR) (call $scan_lang)))
    (return)))
  (if (i32.or (i32.or (call $is_digit (local.get $c)) (i32.eq (local.get $c) (i32.const '+')))
              (i32.or (i32.eq (local.get $c) (i32.const '-'))
                      (i32.and (i32.eq (local.get $c) (i32.const '.')) (call $is_digit (local.get $c1)))))
    (then (global.set $tok (call $scan_number)) (return)))
  (if (call $one_of (local.get $c) (@str ".;,[](}~")) (then (call $punct (local.get $c) (i32.const 1)) (return)))
  (if (i32.or (i32.eq (local.get $c) (i32.const ':')) (call $pn_u (call $decode (global.get $p))))
    (then (global.set $tok (call $scan_name)) (return)))
  (drop (call $syntax (@str "unexpected character"))))

;; Nesting depth of the term being parsed: $deeper enters one level (the caller
;; leaves it by decrementing $depth), or fails past 500.
(global $depth (mut i32) (i32.const 0))
(func $deeper (result i32)
  (if (i32.ge_u (global.get $depth) (i32.const 500)) (then (return (call $syntax (@str "nested too deeply")))))
  (global.set $depth (i32.add (global.get $depth) (i32.const 1)))
  (i32.const 1))

;; --- string maps -------------------------------------------------------------------
;; Bytes -> id, for document blank node labels and prefixes. A map is a 12-byte
;; header: +0 table (0 until first use), +4 capacity, +8 count. Entries are 16
;; bytes: hash, ptr, len, id (0 = empty); keys point into the input buffer.

(global $smap_h (mut i32) (i32.const 0))

;; The entry for the key, or the empty entry where it would go.
(func $smap_find (param $m i32) (param $ptr i32) (param $n i32) (result i32)
  (local $h i32) (local $e i32) (local $mask i32)
  (if (i32.eqz (i32.load (local.get $m))) (then
    (i32.store (local.get $m) (call $alloc0 (i32.const 1024)))
    (i32.store offset=4 (local.get $m) (i32.const 64))))
  (global.set $smap_h (local.tee $h (call $fmix (call $hash_bytes (i32.const 0) (local.get $ptr) (local.get $n)))))
  (local.set $mask (i32.sub (i32.load offset=4 (local.get $m)) (i32.const 1)))
  (loop $probe
    (local.set $e (i32.add (i32.load (local.get $m)) (i32.shl (i32.and (local.get $h) (local.get $mask)) (i32.const 4))))
    (if (i32.load offset=12 (local.get $e)) (then
      (if (i32.and (i32.eq (i32.load (local.get $e)) (global.get $smap_h)) (i32.eq (i32.load offset=8 (local.get $e)) (local.get $n))) (then
        (if (call $memeq (i32.load offset=4 (local.get $e)) (local.get $ptr) (local.get $n)) (then (return (local.get $e))))))
      (local.set $h (i32.add (local.get $h) (i32.const 1)))
      (br $probe))))
  (local.get $e))

;; Set the value of an entry $smap_find returned (right after it).
(func $smap_set (param $m i32) (param $e i32) (param $ptr i32) (param $n i32) (param $id i32)
  (local $old i32) (local $cap i32) (local $f i32) (local $i i32) (local $mask i32)
  (if (i32.load offset=12 (local.get $e)) (then (i32.store offset=12 (local.get $e) (local.get $id)) (return)))
  (i32.store (local.get $e) (global.get $smap_h))
  (i32.store offset=4 (local.get $e) (local.get $ptr))
  (i32.store offset=8 (local.get $e) (local.get $n))
  (i32.store offset=12 (local.get $e) (local.get $id))
  (i32.store offset=8 (local.get $m) (i32.add (i32.load offset=8 (local.get $m)) (i32.const 1)))
  (local.set $cap (i32.load offset=4 (local.get $m)))
  (if (i32.le_u (i32.shl (i32.load offset=8 (local.get $m)) (i32.const 1)) (local.get $cap)) (then (return)))
  ;; double the table
  (local.set $old (i32.load (local.get $m)))
  (i32.store (local.get $m) (call $alloc0 (i32.shl (local.get $cap) (i32.const 5))))
  (i32.store offset=4 (local.get $m) (i32.shl (local.get $cap) (i32.const 1)))
  (local.set $mask (i32.sub (i32.shl (local.get $cap) (i32.const 1)) (i32.const 1)))
  (local.set $e (local.get $old))
  (loop $each
    (if (i32.load offset=12 (local.get $e)) (then
      (local.set $i (i32.load (local.get $e)))
      (loop $probe
        (local.set $f (i32.add (i32.load (local.get $m)) (i32.shl (i32.and (local.get $i) (local.get $mask)) (i32.const 4))))
        (if (i32.load offset=12 (local.get $f)) (then
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $probe))))
      (memory.copy (local.get $f) (local.get $e) (i32.const 16))))
    (br_if $each (i32.lt_u (local.tee $e (i32.add (local.get $e) (i32.const 16)))
                           (i32.add (local.get $old) (i32.shl (local.get $cap) (i32.const 4))))))
  (call $free (local.get $old) (i32.shl (local.get $cap) (i32.const 4))))

(func $smap_clear (param $m i32)
  (if (i32.load (local.get $m)) (then
    (call $free (i32.load (local.get $m)) (i32.shl (i32.load offset=4 (local.get $m)) (i32.const 4)))
    (i64.store (local.get $m) (i64.const 0))
    (i32.store offset=8 (local.get $m) (i32.const 0)))))

;; The document's blank node for a label, made on first use.
(global $scope (mut i32) (i32.const 0))
(func $bnode_id (param $ptr i32) (param $n i32) (result i32)
  (local $e i32) (local $id i32)
  (if (local.tee $id (i32.load offset=12 (local.tee $e (call $smap_find (global.get $scope) (local.get $ptr) (local.get $n)))))
    (then (return (local.get $id))))
  (local.set $id (call $fresh_bnode))
  (call $smap_set (global.get $scope) (local.get $e) (local.get $ptr) (local.get $n) (local.get $id))
  (local.get $id))

;; The literal with value [v, v+n) and the language tag of the current $str/$dir.
(func $lang_lit (param $v i32) (param $n i32) (result i32)
  (call $lower (global.get $str) (global.get $str_len))
  (call $intern (i32.or (i32.const 3) (i32.shl (global.get $dir) (i32.const 8))) (local.get $v) (local.get $n)
                (select (i32.const 4) (i32.const 3) (global.get $dir)) (global.get $str) (global.get $str_len) (i32.const 1)))
