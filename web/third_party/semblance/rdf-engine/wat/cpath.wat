;; Compiled property paths: p* and p+ (p an IRI or ^IRI) from a known end,
;; as a breadth-first walk in the module itself. The nodes reached are kept on
;; the stack: each step scans from one node, then compacts the scan in place
;; to the nodes not seen yet (a node is one word, a match three, so the writes
;; never overtake the reads). This is $reach: the start node is a solution
;; of p* when the query names it or it is a node of the graph (when it came
;; from a variable), and of p+ when a cycle reaches it again.

;; The step of a closure path node x (the IRI p, inverse flag in bit 31), or 0.
(func $cp_step (param $x i32) (result i32)
  (local $sub i32)
  (if (i32.and (i32.ne (i32.load (local.get $x)) (i32.const X_STAR)) (i32.ne (i32.load (local.get $x)) (i32.const X_PLUS))) (then (return (i32.const 0))))
  (local.set $sub (i32.load offset=4 (local.get $x)))
  (if (i32.eq (i32.load (local.get $sub)) (i32.const X_LINK)) (then (return (i32.load offset=4 (local.get $sub)))))
  (if (i32.eq (i32.load (local.get $sub)) (i32.const X_INV)) (then
    (local.set $sub (i32.load offset=4 (local.get $sub)))
    (if (i32.eq (i32.load (local.get $sub)) (i32.const X_LINK)) (then (return (i32.or (i32.load offset=4 (local.get $sub)) (i32.const 0x80000000)))))))
  (i32.const 0))

;; A ref's state: 3 a constant, else the variable's (0 unbound, MAYBE, CERTAIN).
(func $cp_known (param $ref i32) (param $st i32) (result i32)
  (if (result i32) (i32.gt_s (local.get $ref) (i32.const 0)) (then (i32.const 3))
    (else (i32.load8_u (call $st_at (local.get $st) (i32.sub (i32.const -1) (local.get $ref)))))))

;; Path element el as a walk, when it fits: returns 0 if it does not (nothing emitted).
(func $closure_loop (param $f i32) (param $el i32) (param $rest i32) (param $st i32) (result i32)
  (local $step i32) (local $p i32) (local $out i32) (local $star i32) (local $sref i32) (local $oref i32) (local $from i32) (local $to i32)
  (local $ks i32) (local $ko i32) (local $kt i32) (local $node_check i32) (local $col i32) (local $next i32)
  (local $at i32) (local $n i32) (local $len i32) (local $len0 i32) (local $j i32) (local $cur i32) (local $cnt i32) (local $i i32)
  (local $node i32) (local $m i32) (local $base i32) (local $ok i32) (local $idx i32) (local $val i32) (local $first i32) (local $top i32)
  (local $m4 i32) (local $lst i32) (local $r i32) (local $hits i32)
  (if (i32.eqz (local.tee $step (call $cp_step (i32.load offset=8 (local.get $el))))) (then (return (i32.const 0))))
  (local.set $sref (i32.load offset=4 (local.get $el)))
  (local.set $oref (i32.load offset=12 (local.get $el)))
  (if (i32.eqz (i32.and (call $plain (local.get $sref)) (call $plain (local.get $oref)))) (then (return (i32.const 0))))
  (if (i32.and (i32.lt_s (local.get $sref) (i32.const 0)) (i32.eq (local.get $sref) (local.get $oref))) (then (return (i32.const 0))))
  (local.set $ks (call $cp_known (local.get $sref) (local.get $st)))
  (local.set $ko (call $cp_known (local.get $oref) (local.get $st)))
  ;; walk from a constant or certainly bound end; the other end must not be maybe bound
  (if (i32.ge_u (local.get $ks) (i32.const CERTAIN))
    (then (local.set $from (local.get $sref)) (local.set $to (local.get $oref)) (local.set $kt (local.get $ko)) (local.set $out (i32.const 1)))
    (else
      (if (i32.lt_u (local.get $ko) (i32.const CERTAIN)) (then (return (i32.const 0))))
      (local.set $from (local.get $oref)) (local.set $to (local.get $sref)) (local.set $kt (local.get $ks))))
  (if (i32.eq (local.get $kt) (i32.const MAYBE)) (then (return (i32.const 0))))
  (local.set $p (i32.and (local.get $step) (i32.const 0x7fffffff)))
  ;; along p (subject to object), or against it
  (local.set $out (i32.xor (local.get $out) (i32.shr_u (local.get $step) (i32.const 31))))
  (local.set $col (select (i32.const 8) (i32.const 0) (local.get $out)))
  (local.set $star (i32.eq (i32.load (i32.load offset=8 (local.get $el))) (i32.const X_STAR)))
  ;; $eval_path's zero check: an end that is a bound variable
  (local.set $node_check (i32.and (local.get $star)
    (i32.or (i32.and (i32.lt_s (local.get $sref) (i32.const 0)) (i32.ge_u (local.get $ks) (i32.const CERTAIN)))
            (i32.and (i32.lt_s (local.get $oref) (i32.const 0)) (i32.ge_u (local.get $ko) (i32.const CERTAIN))))))
  (local.set $at (i32.load (local.get $st)))
  (local.set $n (call $local (local.get $f))) (local.set $len (call $local (local.get $f))) (local.set $len0 (call $local (local.get $f)))
  (local.set $j (call $local (local.get $f))) (local.set $cur (call $local (local.get $f))) (local.set $cnt (call $local (local.get $f)))
  (local.set $i (call $local (local.get $f))) (local.set $node (call $local (local.get $f))) (local.set $m (call $local (local.get $f)))
  (local.set $base (call $local (local.get $f))) (local.set $ok (call $local (local.get $f))) (local.set $idx (call $local (local.get $f)))
  (local.set $val (call $local (local.get $f))) (local.set $first (call $local (local.get $f))) (local.set $top (call $local (local.get $f)))
  (local.set $hits (call $local (local.get $f)))
  (call $push_ref (local.get $f) (local.get $from))
  (call $em2 (local.get $f) (@str "vak0vb") (local.get $n) (local.get $len))
  ;; is the start a solution of a zero-length path
  ;; (with the check: decided by the first step below)
  (call $em2 (local.get $f) (@str "kavb") (i32.and (local.get $star) (i32.eqz (local.get $node_check))) (local.get $ok))
  ;; the walk: j counts the nodes expanded (the start, then the stack's)
  (call $em1 (local.get $f) (@str "k0va") (local.get $j))
  (call $em2 (local.get $f) (@str "{[gagbKf1") (local.get $j) (local.get $len))
  (call $cx_step (local.get $f))
  (call $em (local.get $f) (@str "gaE?gb:C5gcgajk/jk4ljm0}vd") (local.get $j) (local.get $n) (local.get $at) (local.get $cur))
  (call $em1 (local.get $f) (@str "gak1jva") (local.get $j))
;; the edges from cur: the store's list of cur (or of p), read in line (cscan.wat);
  ;; room on the stack for as many nodes more (cq_room)
  (local.set $m4 (call $anew (i32.const 24)))
  (i32.store (i32.add (local.get $m4) (select (i32.const 0) (i32.const 16) (local.get $out))) (i32.const 2))
  (i32.store offset=4 (i32.add (local.get $m4) (select (i32.const 0) (i32.const 16) (local.get $out))) (local.get $cur))
  (i32.store offset=8 (local.get $m4) (i32.const 1))
  (i32.store offset=12 (local.get $m4) (local.get $p))
  (local.set $lst (call $scan_list_m (local.get $f) (local.get $m4) (local.get $cnt)))
  (local.set $r (call $local (local.get $f)))
  (call $em (local.get $f) (@str "gagbjgcjCLvd") (local.get $at) (local.get $len) (local.get $cnt) (local.get $base))
  (call $em1 (local.get $f) (@str "k0va") (local.get $hits))
  (call $em2 (local.get $f) (@str "k0va{[gagbOf1") (local.get $i) (local.get $cnt))
  (call $cx_step (local.get $f))
  (call $scan_row (local.get $f) (local.get $el) (local.get $st) (local.get $lst) (local.get $i) (local.get $r))
  ;; node = the quad's object (or subject)
  (call $em2 (local.get $f) (@str "gak1jva") (local.get $hits) (i32.const 0))
  (if (local.get $col)
    (then (call $em2 (local.get $f) (@str "gam8vb") (local.get $r) (local.get $node)))
    (else (call $em2 (local.get $f) (@str "gam0vb") (local.get $r) (local.get $node))))
  (call $em0 (local.get $f) (@str "{"))
  ;; the start again: a solution of p+
  (call $em (local.get $f) (@str "gagbF(kcvdb1}") (local.get $node) (local.get $n) (i32.const 1) (local.get $ok))
  (call $em1 (local.get $f) (@str "k0va") (local.get $m))
  (call $em2 (local.get $f) (@str "{[gagbOf1") (local.get $m) (local.get $len))
  (call $em (local.get $f) (@str "gagbjk4lgcjm0gdFf2") (local.get $at) (local.get $m) (local.get $base) (local.get $node))
  (call $em1 (local.get $f) (@str "gak1jvab0}}") (local.get $m))
  (call $em (local.get $f) (@str "gagbjk4lgcjgdM0") (local.get $at) (local.get $len) (local.get $base) (local.get $node))
  (call $em1 (local.get $f) (@str "gak1jva}}b0}}") (local.get $len))
  ;; the first step found edges at the start: a node; else ask the graph
  (if (local.get $node_check) (then
    (call $em (local.get $f) (@str "gak1F(gbvc") (local.get $j) (local.get $hits) (local.get $ok) (i32.const 0))
    (call $em (local.get $f) (@str "gaE(gbk0k0gcC2k0Gk0k0gbgcC2k0Grva}}") (local.get $ok) (local.get $n) (local.get $at) (i32.const 0))))
  (call $em0 (local.get $f) (@str "b0}}"))
  ;; the solutions: the start (when ok), then the stack's nodes
  (call $em (local.get $f) (@str "gagbjvck0vd") (local.get $at) (local.get $len) (local.get $top) (local.get $idx))
  (call $em2 (local.get $f) (@str "{[gagbKf1") (local.get $idx) (local.get $len))
  (call $em (local.get $f) (@str "gaE?gb:C5gcgajk/jk4ljm0}vd") (local.get $idx) (local.get $n) (local.get $at) (local.get $val))
  (call $em (local.get $f) (@str "gaEgbEqvcgak1jva{gcf0") (local.get $idx) (local.get $ok) (local.get $first) (i32.const 0))
  (local.set $next (call $st_copy (local.get $st)))
  (i32.store (local.get $next) (local.get $top))
  (if (i32.ge_u (local.get $kt) (i32.const CERTAIN))
    (then
      (call $em1 (local.get $f) (@str "ga") (local.get $val))
      (call $push_ref (local.get $f) (local.get $to))
      (call $em0 (local.get $f) (@str "Gf0")))
    (else
      (call $em2 (local.get $f) (@str "k0gaMb") (local.get $val) (call $ref_addr (local.get $to)))
      (i32.store8 (call $st_at (local.get $next) (i32.sub (i32.const -1) (local.get $to))) (i32.const CERTAIN))))
  (call $cx_emit (local.get $f) (local.get $rest) (local.get $next))
  (call $em0 (local.get $f) (@str "}b0}}"))
  (if (i32.lt_u (local.get $kt) (i32.const CERTAIN)) (then (call $em1 (local.get $f) (@str "k0k0Ma") (call $ref_addr (local.get $to)))))
  (i32.const 1))
