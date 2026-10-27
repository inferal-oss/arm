;; MD5, SHA1, SHA256, SHA384, SHA512 of a string's UTF-8 bytes, as lowercase hex.

;; The message padded to whole blocks (64 or 128 bytes): 0x80, zeros, and the
;; bit length (big-endian unless le) in the last 8 bytes. Sets $md_n (bytes).
(global $md_n (mut i32) (i32.const 0))
(func $md_pad (param $p i32) (param $n i32) (param $block i32) (param $le i32) (result i32)
  (local $total i32) (local $buf i32) (local $bits i64)
  (local.set $total (i32.mul (i32.div_u (i32.add (i32.add (local.get $n) (i32.const 9)) (i32.sub (local.get $block) (i32.const 1)))
                                        (local.get $block)) (local.get $block)))
  (if (i32.eq (local.get $block) (i32.const 128)) (then
    (local.set $total (i32.mul (i32.div_u (i32.add (i32.add (local.get $n) (i32.const 17)) (i32.const 127)) (i32.const 128)) (i32.const 128)))))
  (local.set $buf (call $alloc0 (local.get $total)))
  (memory.copy (local.get $buf) (local.get $p) (local.get $n))
  (i32.store8 (i32.add (local.get $buf) (local.get $n)) (i32.const 0x80))
  (local.set $bits (i64.shl (i64.extend_i32_u (local.get $n)) (i64.const 3)))
  (if (local.get $le)
    (then (i64.store (i32.sub (i32.add (local.get $buf) (local.get $total)) (i32.const 8)) (local.get $bits)))
    (else (i64.store (i32.sub (i32.add (local.get $buf) (local.get $total)) (i32.const 8)) (call $bswap64 (local.get $bits)))))
  (global.set $md_n (local.get $total))
  (local.get $buf))

(func $bswap32 (param $x i32) (result i32)
  (i32.or (i32.or (i32.shl (local.get $x) (i32.const 24)) (i32.and (i32.shl (local.get $x) (i32.const 8)) (i32.const 0xff0000)))
          (i32.or (i32.and (i32.shr_u (local.get $x) (i32.const 8)) (i32.const 0xff00)) (i32.shr_u (local.get $x) (i32.const 24)))))

(func $bswap64 (param $x i64) (result i64)
  (i64.or (i64.shl (i64.extend_i32_u (call $bswap32 (i32.wrap_i64 (local.get $x)))) (i64.const 32))
          (i64.extend_i32_u (call $bswap32 (i32.wrap_i64 (i64.shr_u (local.get $x) (i64.const 32)))))))

;; Write n bytes at p as hex into the string builder.
(func $xb_hex (param $p i32) (param $n i32)
  (local $c i32)
  (loop $bytes (if (local.get $n) (then
    (local.set $c (i32.load8_u (local.get $p)))
    (call $xb_byte (call $hex_lower (i32.shr_u (local.get $c) (i32.const 4))))
    (call $xb_byte (call $hex_lower (i32.and (local.get $c) (i32.const 15))))
    (local.set $p (i32.add (local.get $p) (i32.const 1)))
    (local.set $n (i32.sub (local.get $n) (i32.const 1)))
    (br $bytes)))))

(func $hex_lower (param $x i32) (result i32)
  (i32.add (local.get $x) (select (i32.const 48) (i32.const 87) (i32.lt_u (local.get $x) (i32.const 10)))))

(func $digest (param $f i32) (param $s i32) (result i32)
  (local $h i32)
  (local.set $h (call $alloc (i32.const 64)))
  (call $xb_reset)
  (if (i32.eq (local.get $f) (i32.const F_MD5)) (then
    (call $md5 (call $tval (local.get $s)) (call $tlen (local.get $s)) (local.get $h))
    (call $xb_hex (local.get $h) (i32.const 16))))
  (if (i32.eq (local.get $f) (i32.const F_SHA1)) (then
    (call $sha1 (call $tval (local.get $s)) (call $tlen (local.get $s)) (local.get $h))
    (call $xb_hex (local.get $h) (i32.const 20))))
  (if (i32.eq (local.get $f) (i32.const F_SHA256)) (then
    (call $sha256 (call $tval (local.get $s)) (call $tlen (local.get $s)) (local.get $h))
    (call $xb_hex (local.get $h) (i32.const 32))))
  (if (i32.eq (local.get $f) (i32.const F_SHA384)) (then
    (call $sha512 (call $tval (local.get $s)) (call $tlen (local.get $s)) (local.get $h) (i32.const 1))
    (call $xb_hex (local.get $h) (i32.const 48))))
  (if (i32.eq (local.get $f) (i32.const F_SHA512)) (then
    (call $sha512 (call $tval (local.get $s)) (call $tlen (local.get $s)) (local.get $h) (i32.const 0))
    (call $xb_hex (local.get $h) (i32.const 64))))
  (call $free (local.get $h) (i32.const 64))
  (call $xb_string (i32.const 0)))

;; --- MD5 ---------------------------------------------------------------------------

(func $md5 (param $p i32) (param $n i32) (param $out i32)
  (local $buf i32) (local $at i32) (local $a i32) (local $b i32) (local $c i32) (local $d i32) (local $f i32) (local $g i32) (local $i i32)
  (local $a0 i32) (local $b0 i32) (local $c0 i32) (local $d0 i32) (local $k i32) (local $s i32)
  (local.set $k (@words32
    0xd76aa478 0xe8c7b756 0x242070db 0xc1bdceee 0xf57c0faf 0x4787c62a 0xa8304613 0xfd469501
    0x698098d8 0x8b44f7af 0xffff5bb1 0x895cd7be 0x6b901122 0xfd987193 0xa679438e 0x49b40821
    0xf61e2562 0xc040b340 0x265e5a51 0xe9b6c7aa 0xd62f105d 0x02441453 0xd8a1e681 0xe7d3fbc8
    0x21e1cde6 0xc33707d6 0xf4d50d87 0x455a14ed 0xa9e3e905 0xfcefa3f8 0x676f02d9 0x8d2a4c8a
    0xfffa3942 0x8771f681 0x6d9d6122 0xfde5380c 0xa4beea44 0x4bdecfa9 0xf6bb4b60 0xbebfbc70
    0x289b7ec6 0xeaa127fa 0xd4ef3085 0x04881d05 0xd9d4d039 0xe6db99e5 0x1fa27cf8 0xc4ac5665
    0xf4292244 0x432aff97 0xab9423a7 0xfc93a039 0x655b59c3 0x8f0ccc92 0xffeff47d 0x85845dd1
    0x6fa87e4f 0xfe2ce6e0 0xa3014314 0x4e0811a1 0xf7537e82 0xbd3af235 0x2ad7d2bb 0xeb86d391))
  (local.set $s (@words32 7 12 17 22 5 9 14 20 4 11 16 23 6 10 15 21))
  (local.set $buf (call $md_pad (local.get $p) (local.get $n) (i32.const 64) (i32.const 1)))
  (local.set $a0 (i32.const 0x67452301)) (local.set $b0 (i32.const 0xefcdab89))
  (local.set $c0 (i32.const 0x98badcfe)) (local.set $d0 (i32.const 0x10325476))
  (loop $blocks (if (i32.lt_u (local.get $at) (global.get $md_n)) (then
    (local.set $a (local.get $a0)) (local.set $b (local.get $b0)) (local.set $c (local.get $c0)) (local.set $d (local.get $d0))
    (local.set $i (i32.const 0))
    (loop $rounds (if (i32.lt_u (local.get $i) (i32.const 64)) (then
      (if (i32.lt_u (local.get $i) (i32.const 16))
        (then (local.set $f (i32.or (i32.and (local.get $b) (local.get $c)) (i32.and (i32.xor (local.get $b) (i32.const -1)) (local.get $d))))
              (local.set $g (local.get $i)))
        (else (if (i32.lt_u (local.get $i) (i32.const 32))
          (then (local.set $f (i32.or (i32.and (local.get $d) (local.get $b)) (i32.and (i32.xor (local.get $d) (i32.const -1)) (local.get $c))))
                (local.set $g (i32.and (i32.add (i32.mul (local.get $i) (i32.const 5)) (i32.const 1)) (i32.const 15))))
          (else (if (i32.lt_u (local.get $i) (i32.const 48))
            (then (local.set $f (i32.xor (i32.xor (local.get $b) (local.get $c)) (local.get $d)))
                  (local.set $g (i32.and (i32.add (i32.mul (local.get $i) (i32.const 3)) (i32.const 5)) (i32.const 15))))
            (else (local.set $f (i32.xor (local.get $c) (i32.or (local.get $b) (i32.xor (local.get $d) (i32.const -1)))))
                  (local.set $g (i32.and (i32.mul (local.get $i) (i32.const 7)) (i32.const 15)))))))))
      (local.set $f (i32.add (i32.add (local.get $f) (local.get $a))
                             (i32.add (i32.load (i32.add (local.get $k) (i32.shl (local.get $i) (i32.const 2))))
                                      (i32.load (i32.add (i32.add (local.get $buf) (local.get $at)) (i32.shl (local.get $g) (i32.const 2)))))))
      (local.set $a (local.get $d))
      (local.set $d (local.get $c))
      (local.set $c (local.get $b))
      (local.set $b (i32.add (local.get $b) (i32.rotl (local.get $f)
        (i32.load (i32.add (local.get $s) (i32.shl (i32.or (i32.shl (i32.shr_u (local.get $i) (i32.const 4)) (i32.const 2)) (i32.and (local.get $i) (i32.const 3))) (i32.const 2)))))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rounds))))
    (local.set $a0 (i32.add (local.get $a0) (local.get $a))) (local.set $b0 (i32.add (local.get $b0) (local.get $b)))
    (local.set $c0 (i32.add (local.get $c0) (local.get $c))) (local.set $d0 (i32.add (local.get $d0) (local.get $d)))
    (local.set $at (i32.add (local.get $at) (i32.const 64)))
    (br $blocks))))
  (call $free (local.get $buf) (global.get $md_n))
  (i32.store (local.get $out) (local.get $a0)) (i32.store offset=4 (local.get $out) (local.get $b0))
  (i32.store offset=8 (local.get $out) (local.get $c0)) (i32.store offset=12 (local.get $out) (local.get $d0)))

;; --- SHA1, SHA256 --------------------------------------------------------------------

(func $sha1 (param $p i32) (param $n i32) (param $out i32)
  (local $buf i32) (local $w i32) (local $at i32) (local $t i32) (local $x i32)
  (local $a i32) (local $b i32) (local $c i32) (local $d i32) (local $e i32) (local $f i32) (local $k i32)
  (local $h0 i32) (local $h1 i32) (local $h2 i32) (local $h3 i32) (local $h4 i32)
  (local.set $buf (call $md_pad (local.get $p) (local.get $n) (i32.const 64) (i32.const 0)))
  (local.set $w (call $alloc (i32.const 320)))
  (local.set $h0 (i32.const 0x67452301)) (local.set $h1 (i32.const 0xefcdab89)) (local.set $h2 (i32.const 0x98badcfe))
  (local.set $h3 (i32.const 0x10325476)) (local.set $h4 (i32.const 0xc3d2e1f0))
  (loop $blocks (if (i32.lt_u (local.get $at) (global.get $md_n)) (then
    (local.set $t (i32.const 0))
    (loop $words (if (i32.lt_u (local.get $t) (i32.const 80)) (then
      (i32.store (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 2)))
        (if (result i32) (i32.lt_u (local.get $t) (i32.const 16))
          (then (call $bswap32 (i32.load (i32.add (i32.add (local.get $buf) (local.get $at)) (i32.shl (local.get $t) (i32.const 2))))))
          (else (i32.rotl (i32.xor (i32.xor (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 3)) (i32.const 2))))
                                            (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 8)) (i32.const 2)))))
                                   (i32.xor (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 14)) (i32.const 2))))
                                            (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 16)) (i32.const 2))))))
                          (i32.const 1)))))
      (local.set $t (i32.add (local.get $t) (i32.const 1))) (br $words))))
    (local.set $a (local.get $h0)) (local.set $b (local.get $h1)) (local.set $c (local.get $h2)) (local.set $d (local.get $h3)) (local.set $e (local.get $h4))
    (local.set $t (i32.const 0))
    (loop $rounds (if (i32.lt_u (local.get $t) (i32.const 80)) (then
      (if (i32.lt_u (local.get $t) (i32.const 20))
        (then (local.set $f (i32.or (i32.and (local.get $b) (local.get $c)) (i32.and (i32.xor (local.get $b) (i32.const -1)) (local.get $d)))) (local.set $k (i32.const 0x5a827999)))
        (else (if (i32.lt_u (local.get $t) (i32.const 40))
          (then (local.set $f (i32.xor (i32.xor (local.get $b) (local.get $c)) (local.get $d))) (local.set $k (i32.const 0x6ed9eba1)))
          (else (if (i32.lt_u (local.get $t) (i32.const 60))
            (then (local.set $f (i32.or (i32.or (i32.and (local.get $b) (local.get $c)) (i32.and (local.get $b) (local.get $d))) (i32.and (local.get $c) (local.get $d))))
                  (local.set $k (i32.const 0x8f1bbcdc)))
            (else (local.set $f (i32.xor (i32.xor (local.get $b) (local.get $c)) (local.get $d))) (local.set $k (i32.const 0xca62c1d6))))))))
      (local.set $x (i32.add (i32.add (i32.rotl (local.get $a) (i32.const 5)) (local.get $f))
                             (i32.add (i32.add (local.get $e) (local.get $k)) (i32.load (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 2)))))))
      (local.set $e (local.get $d)) (local.set $d (local.get $c)) (local.set $c (i32.rotl (local.get $b) (i32.const 30)))
      (local.set $b (local.get $a)) (local.set $a (local.get $x))
      (local.set $t (i32.add (local.get $t) (i32.const 1))) (br $rounds))))
    (local.set $h0 (i32.add (local.get $h0) (local.get $a))) (local.set $h1 (i32.add (local.get $h1) (local.get $b)))
    (local.set $h2 (i32.add (local.get $h2) (local.get $c))) (local.set $h3 (i32.add (local.get $h3) (local.get $d)))
    (local.set $h4 (i32.add (local.get $h4) (local.get $e)))
    (local.set $at (i32.add (local.get $at) (i32.const 64)))
    (br $blocks))))
  (call $free (local.get $buf) (global.get $md_n))
  (call $free (local.get $w) (i32.const 320))
  (i32.store (local.get $out) (call $bswap32 (local.get $h0))) (i32.store offset=4 (local.get $out) (call $bswap32 (local.get $h1)))
  (i32.store offset=8 (local.get $out) (call $bswap32 (local.get $h2))) (i32.store offset=12 (local.get $out) (call $bswap32 (local.get $h3)))
  (i32.store offset=16 (local.get $out) (call $bswap32 (local.get $h4))))

(func $sha256 (param $p i32) (param $n i32) (param $out i32)
  (local $buf i32) (local $w i32) (local $h i32) (local $k i32) (local $at i32) (local $t i32) (local $i i32)
  (local $a i32) (local $b i32) (local $c i32) (local $d i32) (local $e i32) (local $f i32) (local $g i32) (local $hh i32)
  (local $t1 i32) (local $t2 i32) (local $x i32) (local $y i32)
  (local.set $k (@words32
    0x428a2f98 0x71374491 0xb5c0fbcf 0xe9b5dba5 0x3956c25b 0x59f111f1 0x923f82a4 0xab1c5ed5
    0xd807aa98 0x12835b01 0x243185be 0x550c7dc3 0x72be5d74 0x80deb1fe 0x9bdc06a7 0xc19bf174
    0xe49b69c1 0xefbe4786 0x0fc19dc6 0x240ca1cc 0x2de92c6f 0x4a7484aa 0x5cb0a9dc 0x76f988da
    0x983e5152 0xa831c66d 0xb00327c8 0xbf597fc7 0xc6e00bf3 0xd5a79147 0x06ca6351 0x14292967
    0x27b70a85 0x2e1b2138 0x4d2c6dfc 0x53380d13 0x650a7354 0x766a0abb 0x81c2c92e 0x92722c85
    0xa2bfe8a1 0xa81a664b 0xc24b8b70 0xc76c51a3 0xd192e819 0xd6990624 0xf40e3585 0x106aa070
    0x19a4c116 0x1e376c08 0x2748774c 0x34b0bcb5 0x391c0cb3 0x4ed8aa4a 0x5b9cca4f 0x682e6ff3
    0x748f82ee 0x78a5636f 0x84c87814 0x8cc70208 0x90befffa 0xa4506ceb 0xbef9a3f7 0xc67178f2))
  (local.set $buf (call $md_pad (local.get $p) (local.get $n) (i32.const 64) (i32.const 0)))
  (local.set $w (call $alloc (i32.const 256)))
  (local.set $h (call $alloc (i32.const 32)))
  (memory.copy (local.get $h) (@words32 0x6a09e667 0xbb67ae85 0x3c6ef372 0xa54ff53a 0x510e527f 0x9b05688c 0x1f83d9ab 0x5be0cd19) (i32.const 32))
  (loop $blocks (if (i32.lt_u (local.get $at) (global.get $md_n)) (then
    (local.set $t (i32.const 0))
    (loop $words (if (i32.lt_u (local.get $t) (i32.const 64)) (then
      (if (i32.lt_u (local.get $t) (i32.const 16))
        (then (i32.store (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 2)))
          (call $bswap32 (i32.load (i32.add (i32.add (local.get $buf) (local.get $at)) (i32.shl (local.get $t) (i32.const 2)))))))
        (else
          (local.set $x (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 15)) (i32.const 2)))))
          (local.set $y (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 2)) (i32.const 2)))))
          (i32.store (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 2)))
            (i32.add (i32.add (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 16)) (i32.const 2))))
                              (i32.xor (i32.xor (i32.rotr (local.get $x) (i32.const 7)) (i32.rotr (local.get $x) (i32.const 18))) (i32.shr_u (local.get $x) (i32.const 3))))
                     (i32.add (i32.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 7)) (i32.const 2))))
                              (i32.xor (i32.xor (i32.rotr (local.get $y) (i32.const 17)) (i32.rotr (local.get $y) (i32.const 19))) (i32.shr_u (local.get $y) (i32.const 10))))))))
      (local.set $t (i32.add (local.get $t) (i32.const 1))) (br $words))))
    (local.set $a (i32.load (local.get $h))) (local.set $b (i32.load offset=4 (local.get $h)))
    (local.set $c (i32.load offset=8 (local.get $h))) (local.set $d (i32.load offset=12 (local.get $h)))
    (local.set $e (i32.load offset=16 (local.get $h))) (local.set $f (i32.load offset=20 (local.get $h)))
    (local.set $g (i32.load offset=24 (local.get $h))) (local.set $hh (i32.load offset=28 (local.get $h)))
    (local.set $t (i32.const 0))
    (loop $rounds (if (i32.lt_u (local.get $t) (i32.const 64)) (then
      (local.set $t1 (i32.add (i32.add (i32.add (local.get $hh)
                                                (i32.xor (i32.xor (i32.rotr (local.get $e) (i32.const 6)) (i32.rotr (local.get $e) (i32.const 11))) (i32.rotr (local.get $e) (i32.const 25))))
                                       (i32.xor (i32.and (local.get $e) (local.get $f)) (i32.and (i32.xor (local.get $e) (i32.const -1)) (local.get $g))))
                              (i32.add (i32.load (i32.add (local.get $k) (i32.shl (local.get $t) (i32.const 2)))) (i32.load (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 2)))))))
      (local.set $t2 (i32.add (i32.xor (i32.xor (i32.rotr (local.get $a) (i32.const 2)) (i32.rotr (local.get $a) (i32.const 13))) (i32.rotr (local.get $a) (i32.const 22)))
                              (i32.xor (i32.xor (i32.and (local.get $a) (local.get $b)) (i32.and (local.get $a) (local.get $c))) (i32.and (local.get $b) (local.get $c)))))
      (local.set $hh (local.get $g)) (local.set $g (local.get $f)) (local.set $f (local.get $e))
      (local.set $e (i32.add (local.get $d) (local.get $t1)))
      (local.set $d (local.get $c)) (local.set $c (local.get $b)) (local.set $b (local.get $a))
      (local.set $a (i32.add (local.get $t1) (local.get $t2)))
      (local.set $t (i32.add (local.get $t) (i32.const 1))) (br $rounds))))
    (i32.store (local.get $h) (i32.add (i32.load (local.get $h)) (local.get $a)))
    (i32.store offset=4 (local.get $h) (i32.add (i32.load offset=4 (local.get $h)) (local.get $b)))
    (i32.store offset=8 (local.get $h) (i32.add (i32.load offset=8 (local.get $h)) (local.get $c)))
    (i32.store offset=12 (local.get $h) (i32.add (i32.load offset=12 (local.get $h)) (local.get $d)))
    (i32.store offset=16 (local.get $h) (i32.add (i32.load offset=16 (local.get $h)) (local.get $e)))
    (i32.store offset=20 (local.get $h) (i32.add (i32.load offset=20 (local.get $h)) (local.get $f)))
    (i32.store offset=24 (local.get $h) (i32.add (i32.load offset=24 (local.get $h)) (local.get $g)))
    (i32.store offset=28 (local.get $h) (i32.add (i32.load offset=28 (local.get $h)) (local.get $hh)))
    (local.set $at (i32.add (local.get $at) (i32.const 64)))
    (br $blocks))))
  (local.set $i (i32.const 0))
  (loop $out (if (i32.lt_u (local.get $i) (i32.const 32)) (then
    (i32.store (i32.add (local.get $out) (local.get $i)) (call $bswap32 (i32.load (i32.add (local.get $h) (local.get $i)))))
    (local.set $i (i32.add (local.get $i) (i32.const 4))) (br $out))))
  (call $free (local.get $buf) (global.get $md_n))
  (call $free (local.get $w) (i32.const 256))
  (call $free (local.get $h) (i32.const 32)))

;; --- SHA512 and SHA384 ---------------------------------------------------------------

(func $sha512 (param $p i32) (param $n i32) (param $out i32) (param $is384 i32)
  (local $buf i32) (local $w i32) (local $h i32) (local $k i32) (local $at i32) (local $t i32) (local $i i32)
  (local $a i64) (local $b i64) (local $c i64) (local $d i64) (local $e i64) (local $f i64) (local $g i64) (local $hh i64)
  (local $t1 i64) (local $t2 i64) (local $x i64) (local $y i64)
  (local.set $k (@words64
    0x428a2f98d728ae22 0x7137449123ef65cd 0xb5c0fbcfec4d3b2f 0xe9b5dba58189dbbc 0x3956c25bf348b538 0x59f111f1b605d019 0x923f82a4af194f9b 0xab1c5ed5da6d8118
    0xd807aa98a3030242 0x12835b0145706fbe 0x243185be4ee4b28c 0x550c7dc3d5ffb4e2 0x72be5d74f27b896f 0x80deb1fe3b1696b1 0x9bdc06a725c71235 0xc19bf174cf692694
    0xe49b69c19ef14ad2 0xefbe4786384f25e3 0x0fc19dc68b8cd5b5 0x240ca1cc77ac9c65 0x2de92c6f592b0275 0x4a7484aa6ea6e483 0x5cb0a9dcbd41fbd4 0x76f988da831153b5
    0x983e5152ee66dfab 0xa831c66d2db43210 0xb00327c898fb213f 0xbf597fc7beef0ee4 0xc6e00bf33da88fc2 0xd5a79147930aa725 0x06ca6351e003826f 0x142929670a0e6e70
    0x27b70a8546d22ffc 0x2e1b21385c26c926 0x4d2c6dfc5ac42aed 0x53380d139d95b3df 0x650a73548baf63de 0x766a0abb3c77b2a8 0x81c2c92e47edaee6 0x92722c851482353b
    0xa2bfe8a14cf10364 0xa81a664bbc423001 0xc24b8b70d0f89791 0xc76c51a30654be30 0xd192e819d6ef5218 0xd69906245565a910 0xf40e35855771202a 0x106aa07032bbd1b8
    0x19a4c116b8d2d0c8 0x1e376c085141ab53 0x2748774cdf8eeb99 0x34b0bcb5e19b48a8 0x391c0cb3c5c95a63 0x4ed8aa4ae3418acb 0x5b9cca4f7763e373 0x682e6ff3d6b2b8a3
    0x748f82ee5defb2fc 0x78a5636f43172f60 0x84c87814a1f0ab72 0x8cc702081a6439ec 0x90befffa23631e28 0xa4506cebde82bde9 0xbef9a3f7b2c67915 0xc67178f2e372532b
    0xca273eceea26619c 0xd186b8c721c0c207 0xeada7dd6cde0eb1e 0xf57d4f7fee6ed178 0x06f067aa72176fba 0x0a637dc5a2c898a6 0x113f9804bef90dae 0x1b710b35131c471b
    0x28db77f523047d84 0x32caab7b40c72493 0x3c9ebe0a15c9bebc 0x431d67c49c100d4c 0x4cc5d4becb3e42b6 0x597f299cfc657e2a 0x5fcb6fab3ad6faec 0x6c44198c4a475817))
  (local.set $buf (call $md_pad (local.get $p) (local.get $n) (i32.const 128) (i32.const 0)))
  (local.set $w (call $alloc (i32.const 640)))
  (local.set $h (call $alloc (i32.const 64)))
  (if (local.get $is384)
    (then (memory.copy (local.get $h) (@words64 0xcbbb9d5dc1059ed8 0x629a292a367cd507 0x9159015a3070dd17 0x152fecd8f70e5939
                                                0x67332667ffc00b31 0x8eb44a8768581511 0xdb0c2e0d64f98fa7 0x47b5481dbefa4fa4) (i32.const 64)))
    (else (memory.copy (local.get $h) (@words64 0x6a09e667f3bcc908 0xbb67ae8584caa73b 0x3c6ef372fe94f82b 0xa54ff53a5f1d36f1
                                                0x510e527fade682d1 0x9b05688c2b3e6c1f 0x1f83d9abfb41bd6b 0x5be0cd19137e2179) (i32.const 64))))
  (loop $blocks (if (i32.lt_u (local.get $at) (global.get $md_n)) (then
    (local.set $t (i32.const 0))
    (loop $words (if (i32.lt_u (local.get $t) (i32.const 80)) (then
      (if (i32.lt_u (local.get $t) (i32.const 16))
        (then (i64.store (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 3)))
          (call $bswap64 (i64.load (i32.add (i32.add (local.get $buf) (local.get $at)) (i32.shl (local.get $t) (i32.const 3)))))))
        (else
          (local.set $x (i64.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 15)) (i32.const 3)))))
          (local.set $y (i64.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 2)) (i32.const 3)))))
          (i64.store (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 3)))
            (i64.add (i64.add (i64.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 16)) (i32.const 3))))
                              (i64.xor (i64.xor (i64.rotr (local.get $x) (i64.const 1)) (i64.rotr (local.get $x) (i64.const 8))) (i64.shr_u (local.get $x) (i64.const 7))))
                     (i64.add (i64.load (i32.add (local.get $w) (i32.shl (i32.sub (local.get $t) (i32.const 7)) (i32.const 3))))
                              (i64.xor (i64.xor (i64.rotr (local.get $y) (i64.const 19)) (i64.rotr (local.get $y) (i64.const 61))) (i64.shr_u (local.get $y) (i64.const 6))))))))
      (local.set $t (i32.add (local.get $t) (i32.const 1))) (br $words))))
    (local.set $a (i64.load (local.get $h))) (local.set $b (i64.load offset=8 (local.get $h)))
    (local.set $c (i64.load offset=16 (local.get $h))) (local.set $d (i64.load offset=24 (local.get $h)))
    (local.set $e (i64.load offset=32 (local.get $h))) (local.set $f (i64.load offset=40 (local.get $h)))
    (local.set $g (i64.load offset=48 (local.get $h))) (local.set $hh (i64.load offset=56 (local.get $h)))
    (local.set $t (i32.const 0))
    (loop $rounds (if (i32.lt_u (local.get $t) (i32.const 80)) (then
      (local.set $t1 (i64.add (i64.add (i64.add (local.get $hh)
                                                (i64.xor (i64.xor (i64.rotr (local.get $e) (i64.const 14)) (i64.rotr (local.get $e) (i64.const 18))) (i64.rotr (local.get $e) (i64.const 41))))
                                       (i64.xor (i64.and (local.get $e) (local.get $f)) (i64.and (i64.xor (local.get $e) (i64.const -1)) (local.get $g))))
                              (i64.add (i64.load (i32.add (local.get $k) (i32.shl (local.get $t) (i32.const 3)))) (i64.load (i32.add (local.get $w) (i32.shl (local.get $t) (i32.const 3)))))))
      (local.set $t2 (i64.add (i64.xor (i64.xor (i64.rotr (local.get $a) (i64.const 28)) (i64.rotr (local.get $a) (i64.const 34))) (i64.rotr (local.get $a) (i64.const 39)))
                              (i64.xor (i64.xor (i64.and (local.get $a) (local.get $b)) (i64.and (local.get $a) (local.get $c))) (i64.and (local.get $b) (local.get $c)))))
      (local.set $hh (local.get $g)) (local.set $g (local.get $f)) (local.set $f (local.get $e))
      (local.set $e (i64.add (local.get $d) (local.get $t1)))
      (local.set $d (local.get $c)) (local.set $c (local.get $b)) (local.set $b (local.get $a))
      (local.set $a (i64.add (local.get $t1) (local.get $t2)))
      (local.set $t (i32.add (local.get $t) (i32.const 1))) (br $rounds))))
    (i64.store (local.get $h) (i64.add (i64.load (local.get $h)) (local.get $a)))
    (i64.store offset=8 (local.get $h) (i64.add (i64.load offset=8 (local.get $h)) (local.get $b)))
    (i64.store offset=16 (local.get $h) (i64.add (i64.load offset=16 (local.get $h)) (local.get $c)))
    (i64.store offset=24 (local.get $h) (i64.add (i64.load offset=24 (local.get $h)) (local.get $d)))
    (i64.store offset=32 (local.get $h) (i64.add (i64.load offset=32 (local.get $h)) (local.get $e)))
    (i64.store offset=40 (local.get $h) (i64.add (i64.load offset=40 (local.get $h)) (local.get $f)))
    (i64.store offset=48 (local.get $h) (i64.add (i64.load offset=48 (local.get $h)) (local.get $g)))
    (i64.store offset=56 (local.get $h) (i64.add (i64.load offset=56 (local.get $h)) (local.get $hh)))
    (local.set $at (i32.add (local.get $at) (i32.const 128)))
    (br $blocks))))
  (local.set $i (i32.const 0))
  (loop $out (if (i32.lt_u (local.get $i) (i32.const 64)) (then
    (i64.store (i32.add (local.get $out) (local.get $i)) (call $bswap64 (i64.load (i32.add (local.get $h) (local.get $i)))))
    (local.set $i (i32.add (local.get $i) (i32.const 8))) (br $out))))
  (call $free (local.get $buf) (global.get $md_n))
  (call $free (local.get $w) (i32.const 640))
  (call $free (local.get $h) (i32.const 64)))
