;; Compiled triple patterns read the store's lists in line, without cq_scan:
;; the shortest list of the bound positions (as $scan picks it), each entry's
;; quad checked against the bound positions and the run's default graphs, a
;; triple in several of them counted once (`has` on the earlier ones). Where
;; the lists are is read from $bases, which the store keeps current.

;; Per position of the pattern being compiled: mode (0 free, 1 constant, 2 a
;; certainly bound variable, 3 a maybe bound one) and value (the constant, or
;; the local holding the variable).
(global $sc_modes (mut i32) (i32.const 0))

;; Choose the list for pattern el (n: the local for its length): the local
;; holding the list, or 0 when no position is surely bound (cq_scan then).
(func $scan_list (param $f i32) (param $el i32) (param $st i32) (param $n i32) (result i32)
  (local $k i32) (local $ref i32) (local $mode i32) (local $any i32) (local $m i32) (local $v i32)
  (local $lst i32) (local $e i32) (local $tmp i32)
  (local.set $m (call $anew (i32.const 24)))
  (loop $positions
    (local.set $ref (i32.load (i32.add (local.get $el) (i32.shl (i32.add (local.get $k) (i32.const 1)) (i32.const 2)))))
    (if (i32.eqz (call $plain (local.get $ref))) (then (return (i32.const 0))))
    (local.set $mode (i32.const 1))
    (if (i32.lt_s (local.get $ref) (i32.const 0)) (then
      (local.set $mode (i32.add (i32.load8_u (call $st_at (local.get $st) (i32.sub (i32.const -1) (local.get $ref)))) (i32.const 1)))
      ;; 1 maybe -> 3, 2 certain -> 2, 0 unbound -> 0
      (local.set $mode (select (i32.const 0) (select (i32.const 3) (i32.const 2) (i32.eq (local.get $mode) (i32.const 2))) (i32.eq (local.get $mode) (i32.const 1))))))
    (local.set $any (i32.or (local.get $any) (i32.le_u (i32.sub (local.get $mode) (i32.const 1)) (i32.const 1))))
    (i32.store (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3))) (local.get $mode))
    (i32.store offset=4 (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3))) (local.get $ref))
    (br_if $positions (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 3))))
  (if (i32.eqz (local.get $any)) (then (return (i32.const 0))))
  ;; the variables' values, in locals
  (local.set $k (i32.const 0))
  (loop $vars
    (if (i32.ge_u (i32.load (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3)))) (i32.const 2)) (then
      (local.set $v (call $local (local.get $f)))
      (call $em2 (local.get $f) (@str "k0mavb") (call $ref_addr (i32.load offset=4 (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3))))) (local.get $v))
      (i32.store offset=4 (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3))) (local.get $v))))
    (br_if $vars (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 3))))
  (call $scan_list_m (local.get $f) (local.get $m) (local.get $n)))

;; The same, for modes m (+0 mode, +4 the constant or the local holding the value, per position).
(func $scan_list_m (param $f i32) (param $m i32) (param $n i32) (result i32)
  (local $k i32) (local $ref i32) (local $mode i32) (local $v i32) (local $lst i32) (local $e i32) (local $tmp i32)
  (global.set $sc_modes (local.get $m))
  (local.set $lst (call $local (local.get $f)))
  (local.set $e (call $local (local.get $f)))
  (local.set $tmp (call $local (local.get $f)))
  (call $em2 (local.get $f) (@str "k0vak0vb") (local.get $lst) (local.get $e))
  (local.set $k (i32.const 0))
  (loop $lists
    (local.set $mode (i32.load (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3)))))
    (local.set $ref (i32.load offset=4 (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3)))))
    (local.set $v (local.get $ref))
    (if (local.get $mode) (then
      (if (i32.eq (local.get $mode) (i32.const 3)) (then (call $em1 (local.get $f) (@str "ga(") (local.get $v))))
      ;; tmp = the list at position k: $post + 16 id + 4 k
      (call $em1 (local.get $f) (@str "k0ma") (global.get $bases))
      (if (i32.eq (local.get $mode) (i32.const 1))
        (then (call $em1 (local.get $f) (@str "ka") (local.get $ref)))
        (else (call $em1 (local.get $f) (@str "ga") (local.get $v))))
      (call $em (local.get $f) (@str "kaljmbvc") (i32.const 16) (i32.shl (local.get $k) (i32.const 2)) (local.get $tmp) (i32.const 0))
      ;; an empty list: no matches; else keep the shortest
      (call $em (local.get $f) (@str "gaE(k1vb}gcEgam0gcm0Ir(gavc}") (local.get $tmp) (local.get $e) (local.get $lst) (i32.const 0))
      (if (i32.eq (local.get $mode) (i32.const 3)) (then (call $em0 (local.get $f) (@str "}"))))))
    (br_if $lists (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 3))))
  ;; n = e ? 0 : the list's length
  (call $em (local.get $f) (@str "k0gbm0gc~va") (local.get $n) (local.get $lst) (local.get $e) (i32.const 0))
  (local.get $lst))

;; Entry i of the list: r = its quad; i += 1; then a block that the quad leaves
;; when it does not match (left open for the caller's checks and rest).
(func $scan_row (param $f i32) (param $el i32) (param $st i32) (param $lst i32) (param $i i32) (param $r i32)
  (local $k i32) (local $mode i32) (local $v i32) (local $m i32) (local $gi i32) (local $j i32)
  (local.set $m (global.get $sc_modes))
  (call $em (local.get $f) (@str "gagbk4ljm8kPlk0mcjvd") (local.get $lst) (local.get $i) (i32.add (global.get $bases) (i32.const 4)) (local.get $r))
  (call $em1 (local.get $f) (@str "gak1jva{") (local.get $i))
  (loop $positions
    (local.set $mode (i32.load (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3)))))
    (local.set $v (i32.load offset=4 (i32.add (local.get $m) (i32.shl (local.get $k) (i32.const 3)))))
    (if (i32.eq (local.get $mode) (i32.const 1))
      (then (call $em (local.get $f) (@str "gambkcGf0") (local.get $r) (i32.shl (local.get $k) (i32.const 2)) (local.get $v) (i32.const 0))))
    (if (i32.eq (local.get $mode) (i32.const 2))
      (then (call $em (local.get $f) (@str "gambgcGf0") (local.get $r) (i32.shl (local.get $k) (i32.const 2)) (local.get $v) (i32.const 0))))
    (if (i32.eq (local.get $mode) (i32.const 3))
      (then (call $em (local.get $f) (@str "gaEgbmcgaFrEf0") (local.get $v) (local.get $r) (i32.shl (local.get $k) (i32.const 2)) (i32.const 0))))
    (br_if $positions (i32.ne (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.const 3))))
  ;; the quad's graph among the default graphs: its index gi
  (local.set $gi (call $local (local.get $f)))
  (local.set $j (call $local (local.get $f)))
  (call $em1 (local.get $f) (@str "k0va") (local.get $gi))
  (call $em (local.get $f) (@str "{[gak0mbOf2k0mcgak4ljm0gdm<Ff1gak1jvab0}}") (local.get $gi) (i32.add (global.get $bases) (i32.const 12)) (i32.add (global.get $bases) (i32.const 8)) (local.get $r))
  ;; counted in an earlier default graph already: skip
  (call $em (local.get $f) (@str "k0va{[gagbOf1gdm0gdm4gdm8k0mcgak4ljm0C?f2gak1jvab0}}") (local.get $j) (local.get $gi) (i32.add (global.get $bases) (i32.const 8)) (local.get $r)))
