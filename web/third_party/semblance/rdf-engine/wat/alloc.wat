;; Memory, allocator, the input and result buffers, errors, byte helpers.
;;
;; Layout: [0, 272) free-list heads, one i32 per size class (classes 6..64);
;; then static strings (build.ts); the heap starts at $HEAP and grows by
;; bumping $top. Blocks have no header: callers pass the size back to $free,
;; as they always know it (a string's length, a list's capacity, ...).
;;
;; Size classes are 2^k and 3*2^(k-1) bytes (8, 12, 16, 24, 32, 48, ...), so
;; a block wastes at most a third. A freed block goes on its class's list and
;; is reused by the next allocation of that class: O(1) both ways.

(import "env" "now" (func $now_ms (result f64)))
(import "env" "random" (func $random (result f64)))

(memory (export "memory") 2)

(global $top (mut i32) (i32.const 0))

;; The size class of an n-byte request.
(func $class (param $n i32) (result i32)
  (local $k i32)
  (if (i32.lt_u (local.get $n) (i32.const 8)) (then (local.set $n (i32.const 8))))
  ;; k = ceil(log2 n); the class is 2k, or 2k-1 when 3*2^(k-2) bytes suffice
  (local.set $k (i32.sub (i32.const 32) (i32.clz (i32.sub (local.get $n) (i32.const 1)))))
  (i32.sub (i32.shl (local.get $k) (i32.const 1))
           (i32.le_u (local.get $n) (i32.shl (i32.const 3) (i32.sub (local.get $k) (i32.const 2))))))

(func $class_size (param $c i32) (result i32)
  (if (result i32) (i32.and (local.get $c) (i32.const 1))
    (then (i32.shl (i32.const 3) (i32.shr_u (i32.sub (local.get $c) (i32.const 3)) (i32.const 1))))
    (else (i32.shl (i32.const 1) (i32.shr_u (local.get $c) (i32.const 1))))))

(func $alloc (param $n i32) (result i32)
  (local $head i32) (local $p i32) (local $need i32) (local $pages i32)
  (local.set $head (i32.shl (call $class (local.get $n)) (i32.const 2)))
  (local.set $p (i32.load (local.get $head)))
  (if (local.get $p) (then
    (i32.store (local.get $head) (i32.load (local.get $p)))
    (return (local.get $p))))
  (local.set $p (global.get $top))
  (global.set $top (i32.add (local.get $p) (call $class_size (i32.shr_u (local.get $head) (i32.const 2)))))
  (local.set $pages (memory.size))
  (if (i32.gt_u (global.get $top) (i32.shl (local.get $pages) (i32.const 16))) (then
    ;; grow by what is needed, or by half the memory when that is more
    (local.set $need (i32.shr_u (i32.add (i32.sub (global.get $top) (i32.shl (local.get $pages) (i32.const 16))) (i32.const 0xffff)) (i32.const 16)))
    (local.set $pages (i32.shr_u (local.get $pages) (i32.const 1)))
    (if (i32.lt_s (memory.grow (select (local.get $pages) (local.get $need) (i32.gt_u (local.get $pages) (local.get $need)))) (i32.const 0))
      (then (if (i32.lt_s (memory.grow (local.get $need)) (i32.const 0)) (then unreachable))))))
  (local.get $p))

(func $alloc0 (param $n i32) (result i32)
  (local $p i32)
  (memory.fill (local.tee $p (call $alloc (local.get $n))) (i32.const 0) (local.get $n))
  (local.get $p))

(func $free (param $p i32) (param $n i32)
  (local $head i32)
  (local.set $head (i32.shl (call $class (local.get $n)) (i32.const 2)))
  (i32.store (local.get $p) (i32.load (local.get $head)))
  (i32.store (local.get $head) (local.get $p)))

;; Move the first `used` bytes of an n-byte block to a new m-byte block.
(func $realloc (param $p i32) (param $n i32) (param $m i32) (param $used i32) (result i32)
  (local $q i32)
  (memory.copy (local.tee $q (call $alloc (local.get $m))) (local.get $p) (local.get $used))
  (call $free (local.get $p) (local.get $n))
  (local.get $q))

;; --- input and result buffers ------------------------------------------------

(global $in (mut i32) (i32.const 0))
(global $in_cap (mut i32) (i32.const 0))
(global $res (mut i32) (i32.const 0))
(global $res_cap (mut i32) (i32.const 0))

(func (export "input") (param $len i32) (result i32)
  (if (i32.gt_u (local.get $len) (global.get $in_cap)) (then
    (if (global.get $in_cap) (then (call $free (global.get $in) (global.get $in_cap))))
    (global.set $in (call $alloc (local.get $len)))
    (global.set $in_cap (call $class_size (call $class (local.get $len))))))
  (global.get $in))

(func (export "result_ptr") (result i32) (global.get $res))

;; Make room for n result bytes; the previous results are not kept.
(func $res_reserve (param $n i32)
  (if (i32.gt_u (local.get $n) (global.get $res_cap)) (then
    (if (global.get $res_cap) (then (call $free (global.get $res) (global.get $res_cap))))
    (global.set $res (call $alloc (local.get $n)))
    (global.set $res_cap (call $class_size (call $class (local.get $n)))))))

;; --- errors ------------------------------------------------------------------

(global $err_ptr (mut i32) (i32.const 0))
(global $err_len (mut i32) (i32.const 0))
(global $errbuf (mut i32) (i32.const 0))

(func (export "error_ptr") (result i32) (global.get $err_ptr))
(func (export "error_len") (result i32) (global.get $err_len))

;; Set the error message; returns -1 so callers can `(return (call $fail ...))`.
(func $fail (param $p i32) (param $n i32) (result i32)
  (global.set $err_ptr (local.get $p))
  (global.set $err_len (local.get $n))
  (i32.const -1))

;; --- bytes -------------------------------------------------------------------

(func $memeq (param $a i32) (param $b i32) (param $n i32) (result i32)
  (block $no
    (loop $words
      (if (i32.ge_u (local.get $n) (i32.const 8)) (then
        (br_if $no (i64.ne (i64.load (local.get $a)) (i64.load (local.get $b))))
        (local.set $a (i32.add (local.get $a) (i32.const 8)))
        (local.set $b (i32.add (local.get $b) (i32.const 8)))
        (local.set $n (i32.sub (local.get $n) (i32.const 8)))
        (br $words))))
    (loop $bytes
      (if (local.get $n) (then
        (br_if $no (i32.ne (i32.load8_u (local.get $a)) (i32.load8_u (local.get $b))))
        (local.set $a (i32.add (local.get $a) (i32.const 1)))
        (local.set $b (i32.add (local.get $b) (i32.const 1)))
        (local.set $n (i32.sub (local.get $n) (i32.const 1)))
        (br $bytes))))
    (return (i32.const 1)))
  (i32.const 0))

;; Write n in decimal at p; returns the end.
(func $utoa (param $p i32) (param $n i32) (result i32)
  (local $q i32) (local $end i32) (local $c i32)
  (local.set $q (local.get $p))
  (loop $digits
    (i32.store8 (local.get $q) (i32.add (i32.rem_u (local.get $n) (i32.const 10)) (i32.const '0')))
    (local.set $q (i32.add (local.get $q) (i32.const 1)))
    (br_if $digits (local.tee $n (i32.div_u (local.get $n) (i32.const 10)))))
  (local.set $end (local.get $q))
  ;; reverse in place
  (loop $rev
    (local.set $q (i32.sub (local.get $q) (i32.const 1)))
    (if (i32.lt_u (local.get $p) (local.get $q)) (then
      (local.set $c (i32.load8_u (local.get $p)))
      (i32.store8 (local.get $p) (i32.load8_u (local.get $q)))
      (i32.store8 (local.get $q) (local.get $c))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (br $rev))))
  (local.get $end))

;; Lowercase n ASCII bytes in place.
(func $lower (param $p i32) (param $n i32)
  (local $c i32)
  (loop $l
    (if (local.get $n) (then
      (local.set $c (i32.load8_u (local.get $p)))
      (if (i32.lt_u (i32.sub (local.get $c) (i32.const 'A')) (i32.const 26))
        (then (i32.store8 (local.get $p) (i32.or (local.get $c) (i32.const 0x20)))))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br $l)))))
