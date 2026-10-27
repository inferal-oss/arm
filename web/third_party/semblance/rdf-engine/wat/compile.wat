;; Compiled queries (ABI.md, "Optional: compiled queries"): `compile` writes a
;; WebAssembly module for a prepared query, and the cq_* exports are what the
;; module calls back.
;;
;; Variables live in a row frame (one id per slot, the interpreter's row) at a
;; fixed address. The plan follows the interpreter's algebra: a group's
;; elements in order, each FILTER tested as soon as no later element can change
;; what it reads (else at the group's end). Runs of triple and path patterns
;; become nested loops over the store's lists (cscan.wat) and walks (cpath.wat),
;; most bound first. A nested group, UNION branch or OPTIONAL is compiled in
;; line (seeing the whole row) when that gives the rows $join_group gives,
;; which masks the row to the group's certain variables; a group that would see
;; more is compiled in line with those slots cleared and joined after
;; ($masked). Subqueries run their WHERE in line, the engine finishing them
;; (cq_group: grouping, aggregates, modifiers). MINUS, GRAPH, OPTIONALs that
;; need masking and triple terms are left to the interpreter (cq_eval), whose
;; rows extend the frame. Expressions are typed where that pays (ctyped.wat);
;; the rest call cq_test / cq_value.
;;
;; Code is generated in continuation-passing style over a list of steps; a rest
;; reached from two places (after UNION, OPTIONAL, VALUES) becomes a function
;; of its own, taking the stack top.

;; --- bytes -------------------------------------------------------------------------
;; A byte buffer (24 bytes, in the compile arena): +0 data, +4 length,
;; +8 capacity; as a function: +12 type, +16 i32 locals besides its parameter.

(func $bb_new (result i32)
  (local $b i32)
  (local.set $b (call $anew (i32.const 24)))
  (i32.store (local.get $b) (call $anew (i32.const 64)))
  (i32.store offset=8 (local.get $b) (i32.const 64))
  (local.get $b))

(func $bput (param $b i32) (param $x i32)
  (local $n i32) (local $d i32)
  (local.set $n (i32.load offset=4 (local.get $b)))
  (if (i32.eq (local.get $n) (i32.load offset=8 (local.get $b))) (then
    (memory.copy (local.tee $d (call $anew (i32.shl (local.get $n) (i32.const 1)))) (i32.load (local.get $b)) (local.get $n))
    (i32.store (local.get $b) (local.get $d))
    (i32.store offset=8 (local.get $b) (i32.shl (local.get $n) (i32.const 1)))))
  (i32.store8 (i32.add (i32.load (local.get $b)) (local.get $n)) (local.get $x))
  (i32.store offset=4 (local.get $b) (i32.add (local.get $n) (i32.const 1))))

(func $bputs (param $b i32) (param $p i32) (param $n i32)
  (loop $bytes
    (if (local.get $n) (then
      (call $bput (local.get $b) (i32.load8_u (local.get $p)))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br $bytes)))))

(func $uleb (param $b i32) (param $v i32)
  (loop $more
    (if (i32.gt_u (local.get $v) (i32.const 127)) (then
      (call $bput (local.get $b) (i32.or (i32.and (local.get $v) (i32.const 127)) (i32.const 128)))
      (local.set $v (i32.shr_u (local.get $v) (i32.const 7)))
      (br $more))))
  (call $bput (local.get $b) (local.get $v)))

(func $sleb (param $b i32) (param $v i32)
  (loop $more
    (if (i32.gt_u (i32.add (local.get $v) (i32.const 64)) (i32.const 127)) (then
      (call $bput (local.get $b) (i32.or (i32.and (local.get $v) (i32.const 127)) (i32.const 128)))
      (local.set $v (i32.shr_s (local.get $v) (i32.const 7)))
      (br $more))))
  (call $bput (local.get $b) (i32.and (local.get $v) (i32.const 127))))

;; Buffer src with its length in front (a vector, a section body).
(func $bvec (param $b i32) (param $src i32)
  (call $uleb (local.get $b) (i32.load offset=4 (local.get $src)))
  (call $bputs (local.get $b) (i32.load (local.get $src)) (i32.load offset=4 (local.get $src))))

(func $p1 (param $p i32) (param $n i32) (result i32) (local.get $p))

;; Code from a template: one character per opcode. Mnemonics (the rest are
;; opcode bytes themselves: E eqz, F eq, G ne, H lt_s, O ge_u, j add, l mul,
;; q and, r or, s xor):
;;   { block  [ loop  ( if  < block i32  ? if i32  : else  } end  ^ return
;;   _ drop  ~ select;  with an operand: g local.get  v local.set  t local.tee
;;   C call  b br  f br_if  R global.get  W global.set  k i32.const  m i32.load
;;   M i32.store (offsets). An operand is a, b, c or d (the arguments), or a
;;   character c standing for c - '0' (0-9, then : 10, ; 11, < 12).
(func $em (param $f i32) (param $p i32) (param $n i32) (param $a i32) (param $b i32) (param $c i32) (param $d i32)
  (local $ch i32) (local $i i32) (local $v i32) (local $mp i32) (local $mn i32)
  (loop $chars
    (if (local.get $n) (then
      (local.set $ch (i32.load8_u (local.get $p)))
      (local.set $p (i32.add (local.get $p) (i32.const 1)))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (@str "{[(<?:}^_~gvtCbfRWkmM") (local.set $mn) (local.set $mp)
      (local.set $i (i32.const 0))
      (block $found
        (loop $find
          (br_if $found (i32.eq (i32.load8_u (i32.add (local.get $mp) (local.get $i))) (local.get $ch)))
          (br_if $find (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (local.get $mn))))
        (call $bput (local.get $f) (local.get $ch))
        (br $chars))
      ;; the opcode, plus 32 so that it is printable
      (call $bput (local.get $f) (i32.sub (i32.load8_u (i32.add (call $p1 (@str "\"#$\"$%+/:;@AB0,-CDaHV")) (local.get $i))) (i32.const 32)))
      (if (i32.lt_u (local.get $i) (i32.const 5)) (then (call $bput (local.get $f) (select (i32.const 0x40) (i32.const 0x7f) (i32.lt_u (local.get $i) (i32.const 3))))))
      (if (i32.ge_u (local.get $i) (i32.const 19)) (then (call $bput (local.get $f) (i32.const 2))))
      (if (i32.ge_u (local.get $i) (i32.const 10)) (then
        (local.set $ch (i32.load8_u (local.get $p)))
        (local.set $p (i32.add (local.get $p) (i32.const 1)))
        (local.set $n (i32.sub (local.get $n) (i32.const 1)))
        (local.set $v (i32.sub (local.get $ch) (i32.const '0')))
        (if (i32.eq (local.get $ch) (i32.const 'a')) (then (local.set $v (local.get $a))))
        (if (i32.eq (local.get $ch) (i32.const 'b')) (then (local.set $v (local.get $b))))
        (if (i32.eq (local.get $ch) (i32.const 'c')) (then (local.set $v (local.get $c))))
        (if (i32.eq (local.get $ch) (i32.const 'd')) (then (local.set $v (local.get $d))))
        (if (i32.eq (local.get $i) (i32.const 18))
          (then (call $sleb (local.get $f) (local.get $v)))
          (else (call $uleb (local.get $f) (local.get $v))))))
      (br $chars)))))

(func $em0 (param $f i32) (param $p i32) (param $n i32)
  (call $em (local.get $f) (local.get $p) (local.get $n) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0)))
(func $em1 (param $f i32) (param $p i32) (param $n i32) (param $a i32)
  (call $em (local.get $f) (local.get $p) (local.get $n) (local.get $a) (i32.const 0) (i32.const 0) (i32.const 0)))
(func $em2 (param $f i32) (param $p i32) (param $n i32) (param $a i32) (param $b i32)
  (call $em (local.get $f) (local.get $p) (local.get $n) (local.get $a) (local.get $b) (i32.const 0) (i32.const 0)))

;; --- compile state ------------------------------------------------------------------

;; Debugging switches, for bisecting a difference: bits a host may set in the
;; word at $bases + 56 to compile without (1) masked groups in line, (2) typed
;; expressions, (4) EXISTS as functions, (8) subqueries in line, (16) filters
;; pushed down, (32) paths ordered with triples.
(func (export "cq_debug") (param $bits i32) (i32.store offset=56 (global.get $bases) (local.get $bits)))
(func $cx_off (param $bit i32) (result i32)
  (i32.ne (i32.and (i32.load offset=56 (global.get $bases)) (local.get $bit)) (i32.const 0)))

(global $cx_q (mut i32) (i32.const 0))       ;; the query being compiled
(global $cx_frame (mut i32) (i32.const 0))   ;; its frame (also while a compiled query runs)
(global $cx_nv (mut i32) (i32.const 0))      ;; slots
(global $cx_bw (mut i32) (i32.const 0))      ;; bytes of a slot bitset
(global $cx_shape (mut i32) (i32.const 0))   ;; the bound slots (a bitset)
(global $cx_funcs (mut i32) (i32.const 0))   ;; the module's functions (a vector)
(global $cx_ng (mut i32) (i32.const 0))      ;; its globals
(global $cx_keep (mut i32) (i32.const 0))
;; Inside a LATERAL compiled in line: the row it substitutes (a copy of the frame
;; made on entry, which elements left to the interpreter see as $lat), and the
;; slots certainly bound on entry (a bitset), which nested groups see; else 0.
(global $cx_lat (mut i32) (i32.const 0))
(global $cx_latbits (mut i32) (i32.const 0))    ;; 1: cq_value(k | 1) keeps the last call's BNODE(s) nodes

;; Step lists (16 bytes: +0 kind, +4 a, +8 b, +12 next).
(@define S_LIST 1)    ;; a: the group elements from a on, b: the group's filter holder (or 0)
(@define S_EL 2)      ;; a: one element (not a triple pattern)
(@define S_TRIPLE 3)  ;; a: a triple pattern
(@define S_PATH 4)    ;; a: a path pattern
(@define S_FILT 5)    ;; a: a filter holder: the group's filters not tested yet
(@define S_PF 14)
(@define S_KEEP 15)   ;; a solution of a compiled subquery's WHERE: cq_keep
(@define S_EXIT 16)
(@define S_UNMASK 17) ;; a: a masked group's record (see $masked)   ;; a: a global set to 1, then return (EXISTS found a solution)     ;; a: a filter holder, b: the slots later elements of its group mention
(@define S_BIND 6)    ;; a expression, b slot: bound to it when unbound
(@define S_EVAL 7)    ;; a: an element the interpreter evaluates
(@define S_OPT 8)     ;; a: an OPTIONAL's group, in line
(@define S_FOUND 9)   ;; a: the global set when it matched
(@define S_CALL 10)   ;; a: a function to call with the stack top
(@define S_FINAL 11)  ;; a solution
(@define S_SEL 12)
(@define S_LATEND 13) ;; a, b: the LATERAL context (row, visible slots) of the rest    ;; S_BIND of a SELECT expression after another: BNODE(s) of the same solution
;; A budget step at a loop's head (right after its exit test, in its block and
;; loop): the countdown at $bases + 60 (budget.wat) less one; at 0, cq_bu_event
;; says whether the budget is spent, and if so the loop ends. Once spent, every
;; step ends its loop, so the run unwinds to cq_end, which returns -2.
(func $cx_step (param $f i32)
  (call $em (local.get $f) (@str "k0k0makdjtbMagbE(Ccf2}") (i32.add (global.get $bases) (i32.const 60)) (call $local (local.get $f))
    (i32.const CX_BU_EVENT) (i32.const -1)))

;; What is known about a variable (a byte per slot in a state, after +0 the
;; local holding the stack top): 0 unbound, maybe bound, certainly bound.
(@define MAYBE 1) (@define CERTAIN 2)

(func $cstep (param $k i32) (param $a i32) (param $b i32) (param $next i32) (result i32)
  (local $s i32)
  (i32.store (local.tee $s (call $anew (i32.const 16))) (local.get $k))
  (i32.store offset=4 (local.get $s) (local.get $a))
  (i32.store offset=8 (local.get $s) (local.get $b))
  (i32.store offset=12 (local.get $s) (local.get $next))
  (local.get $s))

(func $st_copy (param $st i32) (result i32)
  (local $c i32)
  (memory.copy (local.tee $c (call $anew (i32.add (global.get $cx_nv) (i32.const 4)))) (local.get $st) (i32.add (global.get $cx_nv) (i32.const 4)))
  (local.get $c))

(func $st_at (param $st i32) (param $s i32) (result i32)
  (i32.add (i32.add (local.get $st) (i32.const 4)) (local.get $s)))

;; A copy of st with the slots of bits at least at level.
(func $st_mark (param $st i32) (param $bits i32) (param $level i32) (result i32)
  (local $s i32) (local $at i32)
  (local.set $st (call $st_copy (local.get $st)))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (local.set $at (call $st_at (local.get $st) (local.get $s)))
      (if (i32.and (call $bit (local.get $bits) (local.get $s)) (i32.lt_u (i32.load8_u (local.get $at)) (local.get $level)))
        (then (i32.store8 (local.get $at) (local.get $level))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (local.get $st))

(func $cx_bits (result i32) (call $anew (global.get $cx_bw)))
(func $setbit (param $b i32) (param $s i32) (call $an_ref (local.get $b) (i32.sub (i32.const -1) (local.get $s))))

;; The frame address of a slot, or of a variable ref.
(func $addr (param $s i32) (result i32) (i32.add (global.get $cx_frame) (i32.shl (local.get $s) (i32.const 2))))
(func $ref_addr (param $ref i32) (result i32) (call $addr (i32.sub (i32.const -1) (local.get $ref))))

;; A new i32 local of function f. Locals of other types (ctyped.wat) are listed,
;; one type byte per local, in a buffer at +20 (0: all are i32).
(func $local (param $f i32) (result i32)
  (local $n i32)
  (i32.store offset=16 (local.get $f) (local.tee $n (i32.add (i32.load offset=16 (local.get $f)) (i32.const 1))))
  (if (i32.load offset=20 (local.get $f)) (then (call $bput (i32.load offset=20 (local.get $f)) (i32.const 0x7f))))
  (local.get $n))

;; A new local of type ty (0x7e i64, 0x7c f64).
(func $local_t (param $f i32) (param $ty i32) (result i32)
  (local $b i32) (local $i i32)
  (if (i32.eqz (local.tee $b (i32.load offset=20 (local.get $f)))) (then
    (i32.store offset=20 (local.get $f) (local.tee $b (call $bb_new)))
    (loop $i32s
      (if (i32.lt_u (local.get $i) (i32.load offset=16 (local.get $f))) (then
        (call $bput (local.get $b) (i32.const 0x7f))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $i32s))))))
  (call $bput (local.get $b) (local.get $ty))
  (i32.store offset=16 (local.get $f) (i32.add (i32.load offset=16 (local.get $f)) (i32.const 1)))
  (i32.load offset=16 (local.get $f)))

;; Function f's local declarations: runs of one type.
(func $locals_decl (param $body i32) (param $f i32)
  (local $b i32) (local $n i32) (local $i i32) (local $runs i32) (local $j i32) (local $ty i32)
  (local.set $n (i32.load offset=16 (local.get $f)))
  (if (i32.eqz (local.tee $b (i32.load offset=20 (local.get $f)))) (then
    (call $uleb (local.get $body) (i32.ne (local.get $n) (i32.const 0)))
    (if (local.get $n) (then
      (call $uleb (local.get $body) (local.get $n))
      (call $bput (local.get $body) (i32.const 0x7f))))
    (return)))
  ;; count the runs, then write them
  (loop $count
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (if (i32.or (i32.eqz (local.get $i)) (i32.ne (i32.load8_u (i32.add (i32.load (local.get $b)) (local.get $i))) (i32.load8_u (i32.add (i32.load (local.get $b)) (i32.sub (local.get $i) (i32.const 1))))))
        (then (local.set $runs (i32.add (local.get $runs) (i32.const 1)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $count))))
  (call $uleb (local.get $body) (local.get $runs))
  (local.set $i (i32.const 0))
  (loop $write
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $ty (i32.load8_u (i32.add (i32.load (local.get $b)) (local.get $i))))
      (local.set $j (local.get $i))
      (loop $same
        (if (i32.lt_u (local.get $j) (local.get $n)) (then
          (if (i32.eq (i32.load8_u (i32.add (i32.load (local.get $b)) (local.get $j))) (local.get $ty)) (then
            (local.set $j (i32.add (local.get $j) (i32.const 1)))
            (br $same))))))
      (call $uleb (local.get $body) (i32.sub (local.get $j) (local.get $i)))
      (call $bput (local.get $body) (local.get $ty))
      (local.set $i (local.get $j))
      (br $write)))))

;; Whether ref is a term or a variable (not a triple term pattern).
(func $plain (param $ref i32) (result i32) (i32.gt_s (local.get $ref) (i32.const -0x100000)))

;; --- variables of patterns ----------------------------------------------------------

;; Set in b every slot that node n (pattern, group, expression; 0: none) mentions.
(func $deep (param $b i32) (param $n i32)
  (local $k i32) (local $i i32) (local $m i32)
  (if (i32.eqz (local.get $n)) (then (return)))
  (local.set $k (i32.load (local.get $n)))
  (if (i32.eq (local.get $k) (i32.const P_GROUP)) (then
    (call $deep_list (local.get $b) (i32.load offset=4 (local.get $n)))
    (call $deep_list (local.get $b) (i32.load offset=8 (local.get $n)))
    (return)))
  ;; a path's +8 is a pointer, which $an_ref ignores
  (if (i32.or (i32.eq (local.get $k) (i32.const P_TRIPLE)) (i32.eq (local.get $k) (i32.const P_PATH))) (then
    (call $an_ref (local.get $b) (i32.load offset=4 (local.get $n)))
    (call $an_ref (local.get $b) (i32.load offset=8 (local.get $n)))
    (call $an_ref (local.get $b) (i32.load offset=12 (local.get $n)))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_GRAPH)) (then
    (call $an_ref (local.get $b) (i32.load offset=4 (local.get $n)))
    (call $deep (local.get $b) (i32.load offset=8 (local.get $n)))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_VALUES)) (then
    (loop $vars (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $n))) (then
      (call $setbit (local.get $b) (i32.load (i32.add (i32.load offset=8 (local.get $n)) (i32.shl (local.get $i) (i32.const 2)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1))) (br $vars))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_SUB)) (then
    ;; its own slots aside: the outer slots it projects to
    (local.set $m (i32.load offset=72 (i32.load offset=4 (local.get $n))))
    (loop $map (if (local.get $m) (then
      (call $setbit (local.get $b) (i32.load offset=8 (local.get $m)))
      (local.set $m (i32.load offset=28 (local.get $m))) (br $map))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const E_VAR)) (then (call $setbit (local.get $b) (i32.load offset=4 (local.get $n))) (return)))
  (if (i32.eq (local.get $k) (i32.const E_CALL)) (then (call $deep_list (local.get $b) (i32.load offset=8 (local.get $n))) (return)))
  (if (i32.eq (local.get $k) (i32.const E_AGG)) (then
    (call $setbit (local.get $b) (i32.load offset=20 (local.get $n)))
    (call $deep (local.get $b) (i32.load offset=8 (local.get $n)))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_BIND)) (then (call $setbit (local.get $b) (i32.load offset=8 (local.get $n)))))
  (if (i32.eq (local.get $k) (i32.const P_UNION)) (then (call $deep (local.get $b) (i32.load offset=8 (local.get $n)))))
  ;; P_BIND, P_UNION, P_OPTIONAL, P_MINUS, P_LATERAL, P_FILTER, E_EXISTS: a
  (if (i32.ne (local.get $k) (i32.const E_CONST)) (then (call $deep (local.get $b) (i32.load offset=4 (local.get $n))))))

(func $deep_list (param $b i32) (param $l i32)
  (loop $each
    (if (local.get $l) (then
      (call $deep (local.get $b) (local.get $l))
      (local.set $l (i32.load offset=28 (local.get $l)))
      (br $each)))))

;; Whether group g, entered with state st, may be compiled in line: as
;; $join_group masks the row to g's certain variables, no other variable of g
;; may be bound already (parameters aside). opt: an OPTIONAL's group, whose own
;; filters see the merged row anyway.
(func $inline_ok (param $g i32) (param $st i32) (param $opt i32) (result i32)
  (local $b i32) (local $s i32)
  (local.set $b (call $cx_bits))
  (if (local.get $opt)
    (then (call $deep_list (local.get $b) (i32.load offset=4 (local.get $g))))
    (else (call $deep (local.get $b) (local.get $g))))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (i32.and (i32.and (call $bit (local.get $b) (local.get $s)) (i32.ne (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const 0)))
                   (i32.eqz (i32.or (i32.or (call $bit (i32.load offset=12 (local.get $g)) (local.get $s)) (call $bit (global.get $cx_shape) (local.get $s)))
                                    (if (result i32) (global.get $cx_latbits) (then (call $bit (global.get $cx_latbits) (local.get $s))) (else (i32.const 0))))))
        (then (return (i32.const 0))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (i32.const 1))

;; Whether group g (a subquery's WHERE) is a cross product of independent
;; chains with BINDs: its triple, path, OPTIONAL and BIND elements fall into
;; two or more sets that share no variable but the parameters and what a
;; pattern from a bound subject reaches (`$this c:of ?scale`), so only its
;; filters relate them (the rank-by-COUNT shape). In line, the module would
;; evaluate the BINDs of an inner chain once per pair; the interpreter
;; evaluates a pure BIND once per distinct input, so such a subquery is left
;; to it. Any other element kind: not this shape.
(func $cross (param $g i32) (result i32)
  (local $known i32) (local $el i32) (local $k i32) (local $n i32) (local $sets i32) (local $i i32)
  (local $comp i32) (local $grew i32) (local $s i32) (local $binds i32) (local $ref i32)
  (local.set $known (call $cx_bits))
  (local.set $s (i32.const 0))
  (loop $params
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (call $bit (global.get $cx_shape) (local.get $s)) (then (call $setbit (local.get $known) (local.get $s))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $params))))
  ;; count the elements, and what bound subjects reach
  (local.set $el (i32.load offset=4 (local.get $g)))
  (loop $each
    (if (local.get $el) (then
      (local.set $k (i32.load (local.get $el)))
      (if (i32.eqz (i32.or (i32.or (i32.eq (local.get $k) (i32.const P_TRIPLE)) (i32.eq (local.get $k) (i32.const P_PATH)))
                           (i32.or (i32.eq (local.get $k) (i32.const P_OPTIONAL)) (i32.eq (local.get $k) (i32.const P_BIND)))))
        (then (return (i32.const 0))))
      (if (i32.eq (local.get $k) (i32.const P_BIND)) (then (local.set $binds (i32.add (local.get $binds) (i32.const 1)))))
      (if (i32.eq (local.get $k) (i32.const P_TRIPLE)) (then
        (local.set $ref (i32.load offset=4 (local.get $el)))
        ;; a constant or known subject (a variable: not a triple term pattern)
        (if (i32.and (call $plain (i32.load offset=12 (local.get $el)))
              (if (result i32) (i32.gt_s (local.get $ref) (i32.const 0)) (then (i32.const 1))
                (else (if (result i32) (i32.gt_s (local.get $ref) (i32.const -0x100000))
                  (then (call $bit (local.get $known) (i32.sub (i32.const -1) (local.get $ref))))
                  (else (i32.const 0))))))
          (then (call $an_ref (local.get $known) (i32.load offset=12 (local.get $el)))))))
      (local.set $n (i32.add (local.get $n) (i32.const 1)))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (br $each))))
  (if (i32.or (i32.lt_u (local.get $n) (i32.const 2)) (i32.eqz (local.get $binds))) (then (return (i32.const 0))))
  ;; each element's other variables
  (local.set $sets (call $anew (i32.shl (local.get $n) (i32.const 2))))
  (local.set $el (i32.load offset=4 (local.get $g)))
  (local.set $i (i32.const 0))
  (loop $each
    (if (local.get $el) (then
      (call $deep (local.tee $comp (call $cx_bits)) (local.get $el))
      (call $bits_andnot (local.get $comp) (local.get $known))
      (i32.store (i32.add (local.get $sets) (i32.shl (local.get $i) (i32.const 2))) (local.get $comp))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (local.set $el (i32.load offset=28 (local.get $el)))
      (br $each))))
  ;; the set the first element with variables reaches, through shared variables
  (local.set $comp (call $cx_bits))
  (local.set $grew (i32.const 1))
  (loop $grow
    (if (local.get $grew) (then
      (local.set $grew (i32.const 0))
      (local.set $i (i32.const 0))
      (loop $each
        (if (i32.lt_u (local.get $i) (local.get $n)) (then
          (local.set $el (i32.load (i32.add (local.get $sets) (i32.shl (local.get $i) (i32.const 2)))))
          (if (i32.and (call $bits_any (local.get $el))
                (i32.or (call $bits_meet (local.get $el) (local.get $comp)) (i32.eqz (call $bits_any (local.get $comp)))))
            (then
              (call $bits_or (local.get $comp) (local.get $el))
              (i32.store (i32.add (local.get $sets) (i32.shl (local.get $i) (i32.const 2))) (call $cx_bits))
              (local.set $grew (i32.const 1))))
          (local.set $i (i32.add (local.get $i) (i32.const 1)))
          (br $each))))
      (br $grow))))
  ;; anything left with variables of its own is another chain
  (local.set $i (i32.const 0))
  (loop $each
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (if (call $bits_any (i32.load (i32.add (local.get $sets) (i32.shl (local.get $i) (i32.const 2))))) (then (return (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $each))))
  (i32.const 0))

;; Slot bitsets of $cx_bw bytes (a multiple of 4).
(func $bits_any (param $a i32) (result i32)
  (local $i i32)
  (loop $w
    (if (i32.lt_u (local.get $i) (global.get $cx_bw)) (then
      (if (i32.load (i32.add (local.get $a) (local.get $i))) (then (return (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 4)))
      (br $w))))
  (i32.const 0))
(func $bits_meet (param $a i32) (param $b i32) (result i32)
  (local $i i32)
  (loop $w
    (if (i32.lt_u (local.get $i) (global.get $cx_bw)) (then
      (if (i32.and (i32.load (i32.add (local.get $a) (local.get $i))) (i32.load (i32.add (local.get $b) (local.get $i)))) (then (return (i32.const 1))))
      (local.set $i (i32.add (local.get $i) (i32.const 4)))
      (br $w))))
  (i32.const 0))
(func $bits_or (param $a i32) (param $b i32)
  (local $i i32)
  (loop $w
    (if (i32.lt_u (local.get $i) (global.get $cx_bw)) (then
      (i32.store (i32.add (local.get $a) (local.get $i)) (i32.or (i32.load (i32.add (local.get $a) (local.get $i))) (i32.load (i32.add (local.get $b) (local.get $i)))))
      (local.set $i (i32.add (local.get $i) (i32.const 4)))
      (br $w)))))
(func $bits_andnot (param $a i32) (param $b i32)
  (local $i i32)
  (loop $w
    (if (i32.lt_u (local.get $i) (global.get $cx_bw)) (then
      (i32.store (i32.add (local.get $a) (local.get $i)) (i32.and (i32.load (i32.add (local.get $a) (local.get $i))) (i32.xor (i32.load (i32.add (local.get $b) (local.get $i))) (i32.const -1))))
      (local.set $i (i32.add (local.get $i) (i32.const 4)))
      (br $w)))))

;; --- plans --------------------------------------------------------------------------

;; The run of triple patterns from el, most bound first (as the interpreter
;; scores them), then the elements after it, then rest. h: the group's filter
;; holder: after each triple, the filters that are ready (S_PF).
(func $order (param $el i32) (param $st i32) (param $rest i32) (param $h i32) (result i32)
  (local $n i32) (local $e i32) (local $picked i32) (local $b i32) (local $head i32) (local $last i32)
  (local $left i32) (local $i i32) (local $best i32) (local $bs i32) (local $be i32) (local $sc i32) (local $c i32) (local $ref i32) (local $w i32)
  (local $after i32) (local $later i32) (local $x i32)
  (local.set $e (local.get $el))
  (loop $count
    (if (local.get $e) (then
      (if (i32.le_u (i32.sub (i32.load (local.get $e)) (i32.const P_TRIPLE)) (select (i32.const 0) (i32.const 1) (call $cx_off (i32.const 32)))) (then
        (local.set $n (i32.add (local.get $n) (i32.const 1)))
        (local.set $e (i32.load offset=28 (local.get $e)))
        (br $count))))))
  (local.set $after (local.get $e))
  (local.set $rest (call $cstep (i32.const S_LIST) (local.get $e) (local.get $h) (local.get $rest)))
  (local.set $picked (call $anew (local.get $n)))
  (local.set $b (call $st_copy (local.get $st)))
  (local.set $last (local.tee $head (call $cstep (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0))))
  (local.set $left (local.get $n))
  (loop $picks
    (if (local.get $left) (then
      (local.set $bs (i32.const -1))
      (local.set $e (local.get $el))
      (local.set $i (i32.const 0))
      (loop $candidates
        (if (i32.eqz (i32.load8_u (i32.add (local.get $picked) (local.get $i)))) (then
          ;; bound subject 4, object 3, predicate 1
          (local.set $sc (i32.const 0))
          (local.set $c (i32.const 0))
          (loop $positions
            (local.set $ref (i32.load (i32.add (local.get $e) (i32.shl (i32.add (local.get $c) (i32.const 1)) (i32.const 2)))))
            ;; a path's predicate (a pointer) does not count
            (if (if (result i32) (i32.and (i32.eq (local.get $c) (i32.const 1)) (i32.ne (i32.load (local.get $e)) (i32.const P_TRIPLE))) (then (i32.const 0))
                  (else (if (result i32) (i32.gt_s (local.get $ref) (i32.const 0)) (then (i32.const 1))
                    (else (if (result i32) (call $plain (local.get $ref))
                      (then (i32.load8_u (call $st_at (local.get $b) (i32.sub (i32.const -1) (local.get $ref)))))
                      (else (i32.const 0)))))))
              (then (local.set $sc (i32.add (local.get $sc)
                (select (i32.const 4) (select (i32.const 1) (i32.const 3) (i32.eq (local.get $c) (i32.const 1))) (i32.eqz (local.get $c)))))))
            (br_if $positions (i32.ne (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.const 3))))
          (if (i32.gt_s (local.get $sc) (local.get $bs)) (then
            (local.set $bs (local.get $sc))
            (local.set $best (local.get $i))
            (local.set $be (local.get $e))))))
        (local.set $e (i32.load offset=28 (local.get $e)))
        (br_if $candidates (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (local.get $n))))
      (i32.store8 (i32.add (local.get $picked) (local.get $best)) (i32.const 1))
      ;; its variables are bound from here on
      (local.set $w (i32.const 1))
      (local.set $c (i32.const 0))
      (loop $positions
        (local.set $ref (i32.load (i32.add (local.get $be) (i32.shl (i32.add (local.get $c) (i32.const 1)) (i32.const 2)))))
        (if (i32.lt_s (local.get $ref) (i32.const 0)) (then
          (if (call $plain (local.get $ref))
            (then (i32.store8 (call $st_at (local.get $b) (i32.sub (i32.const -1) (local.get $ref))) (i32.const CERTAIN)))
            (else (local.set $w (i32.const 0))))))
        (br_if $positions (i32.ne (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.const 3))))
      (i32.store offset=12 (local.get $last)
        (local.tee $last (call $cstep (select (select (i32.const S_TRIPLE) (i32.const S_PATH) (i32.eq (i32.load (local.get $be)) (i32.const P_TRIPLE))) (i32.const S_EVAL) (local.get $w)) (local.get $be) (i32.const 0) (i32.const 0))))
      (local.set $left (i32.sub (local.get $left) (i32.const 1)))
      ;; filters that are ready now: what the triples left and the elements after mention
      (if (i32.and (i32.ne (local.get $h) (i32.const 0)) (i32.ne (local.get $left) (i32.const 0))) (then
        (call $deep_list (local.tee $later (call $cx_bits)) (local.get $after))
        (local.set $x (local.get $el))
        (local.set $i (i32.const 0))
        (loop $unpicked
          (if (i32.lt_u (local.get $i) (local.get $n)) (then
            (if (i32.eqz (i32.load8_u (i32.add (local.get $picked) (local.get $i)))) (then (call $deep (local.get $later) (local.get $x))))
            (local.set $x (i32.load offset=28 (local.get $x)))
            (local.set $i (i32.add (local.get $i) (i32.const 1)))
            (br $unpicked))))
        (i32.store offset=12 (local.get $last) (local.tee $last (call $cstep (i32.const S_PF) (local.get $h) (local.get $later) (i32.const 0))))))
      (br $picks))))
  (i32.store offset=12 (local.get $last) (local.get $rest))
  (i32.load offset=12 (local.get $head)))

;; The rest of a plan as a function of its own (taking the stack top): its index.
(func $cont (param $rest i32) (param $st i32) (result i32)
  (local $f i32) (local $k i32)
  (if (i32.and (i32.eq (i32.load (local.get $rest)) (i32.const S_CALL)) (i32.eqz (i32.load offset=12 (local.get $rest))))
    (then (return (i32.load offset=4 (local.get $rest)))))
  ;; the imports, then run
  (local.set $k (i32.add (i32.load offset=4 (global.get $cx_funcs)) (i32.const CX_IMPORTS)))
  (call $v_push1 (global.get $cx_funcs) (local.tee $f (call $bb_new)))
  (i32.store (local.tee $st (call $st_copy (local.get $st))) (i32.const 0))
  (call $cx_emit (local.get $f) (local.get $rest) (local.get $st))
  (local.get $k))

;; Emit the steps s on, in function f, with state st.
(func $cx_emit (param $f i32) (param $s i32) (param $st i32)
  (local $k i32) (local $a i32) (local $rest i32) (local $n i32) (local $e i32) (local $l i32) (local $x i32)
  (local.set $k (i32.load (local.get $s)))
  (local.set $a (i32.load offset=4 (local.get $s)))
  (local.set $rest (i32.load offset=12 (local.get $s)))
  (if (i32.eq (local.get $k) (i32.const S_LIST)) (then
    (if (i32.eqz (local.get $a)) (then (return (call $cx_emit (local.get $f) (local.get $rest) (local.get $st)))))
    ;; the group's filters that the elements from a on cannot change: tested now
    (local.set $x (i32.load offset=8 (local.get $s)))
    (if (local.get $x) (then
      (call $deep_list (local.tee $e (call $cx_bits)) (local.get $a))
      (local.set $n (call $flush (local.get $f) (local.get $x) (local.get $e) (local.get $st)))))
    (if (i32.le_u (i32.sub (i32.load (local.get $a)) (i32.const P_TRIPLE)) (select (i32.const 0) (i32.const 1) (call $cx_off (i32.const 32))))
      (then (call $cx_emit (local.get $f) (call $order (local.get $a) (local.get $st) (local.get $rest) (local.get $x)) (local.get $st)))
      (else (call $cx_emit (local.get $f)
        (call $cstep (i32.const S_EL) (local.get $a) (i32.const 0) (call $cstep (i32.const S_LIST) (i32.load offset=28 (local.get $a)) (local.get $x) (local.get $rest)))
        (local.get $st))))
    (return (call $close (local.get $f) (local.get $n)))))
  (if (i32.eq (local.get $k) (i32.const S_PF)) (then
    (local.set $n (call $flush2 (local.get $f) (local.get $a) (i32.load offset=8 (local.get $s)) (local.get $st) (i32.const 1)))
    (call $cx_emit (local.get $f) (local.get $rest) (local.get $st))
    (return (call $close (local.get $f) (local.get $n)))))
  (if (i32.eq (local.get $k) (i32.const S_EL)) (then (return (call $element (local.get $f) (local.get $a) (local.get $rest) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const S_LATEND)) (then
    (local.set $l (global.get $cx_lat))
    (local.set $x (global.get $cx_latbits))
    (global.set $cx_lat (local.get $a))
    (global.set $cx_latbits (i32.load offset=8 (local.get $s)))
    (call $cx_emit (local.get $f) (local.get $rest) (local.get $st))
    (global.set $cx_lat (local.get $l))
    (global.set $cx_latbits (local.get $x))
    (return)))
  (if (i32.eq (local.get $k) (i32.const S_PATH)) (then
    (if (call $closure_loop (local.get $f) (local.get $a) (local.get $rest) (local.get $st)) (then (return)))))
  (if (i32.le_u (i32.sub (local.get $k) (i32.const S_TRIPLE)) (i32.const 1))
    (then (return (call $scan_loop (local.get $f) (local.get $a) (i32.eq (local.get $k) (i32.const S_PATH)) (local.get $rest) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const S_EVAL)) (then (return (call $eval_loop (local.get $f) (local.get $a) (local.get $rest) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const S_FILT)) (then
    ;; the filters not tested yet, at the group's end
    (local.set $n (call $flush (local.get $f) (local.get $a) (i32.const 0) (local.get $st)))
    (call $cx_emit (local.get $f) (local.get $rest) (local.get $st))
    (return (call $close (local.get $f) (local.get $n)))))
  (if (i32.or (i32.eq (local.get $k) (i32.const S_BIND)) (i32.eq (local.get $k) (i32.const S_SEL))) (then
    ;; the interpreter keeps a bound variable
    (local.set $x (i32.load offset=8 (local.get $s)))
    (local.set $e (call $st_at (local.get $st) (local.get $x)))
    (if (i32.eq (i32.load8_u (local.get $e)) (i32.const CERTAIN)) (then (return (call $cx_emit (local.get $f) (local.get $rest) (local.get $st)))))
    (local.set $l (call $local (local.get $f)))
    (call $em2 (local.get $f) (@str "k0mavbgbE(k0") (call $addr (local.get $x)) (local.get $l))
    (global.set $cx_keep (i32.eq (local.get $k) (i32.const S_SEL)))
    (local.set $n (global.get $tx_st))
    (global.set $tx_st (local.get $st))
    (call $value (local.get $f) (local.get $a))
    (global.set $tx_st (local.get $n))
    (global.set $cx_keep (i32.const 0))
    (call $em1 (local.get $f) (@str "Ma}") (call $addr (local.get $x)))
    ;; certainly bound after it when it copies a constant or a certainly bound variable
    (local.set $n (i32.eq (i32.load (local.get $a)) (i32.const E_CONST)))
    (if (i32.eq (i32.load (local.get $a)) (i32.const E_VAR))
      (then (local.set $n (i32.eq (i32.load8_u (call $st_at (local.get $st) (i32.load offset=4 (local.get $a)))) (i32.const CERTAIN)))))
    (i32.store8 (call $st_at (local.tee $st (call $st_copy (local.get $st))) (local.get $x)) (select (i32.const CERTAIN) (i32.const MAYBE) (local.get $n)))
    (call $cx_emit (local.get $f) (local.get $rest) (local.get $st))
    (call $em2 (local.get $f) (@str "k0gbMa") (call $addr (local.get $x)) (local.get $l))
    (return)))
  (if (i32.eq (local.get $k) (i32.const S_OPT)) (then
    ;; the group in line, then rest; rest alone when nothing matched
    (local.set $e (call $cx_bits))
    (call $deep_list (local.get $e) (i32.load offset=4 (local.get $a)))
    (local.set $l (call $cont (local.get $rest) (call $st_mark (local.get $st) (local.get $e) (i32.const MAYBE))))
    (local.set $x (global.get $cx_ng))
    (global.set $cx_ng (i32.add (local.get $x) (i32.const 1)))
    (call $em1 (local.get $f) (@str "k0Wa") (local.get $x))
    (call $cx_emit (local.get $f)
      (call $grp (i32.load offset=4 (local.get $a)) (i32.load offset=8 (local.get $a))
        (call $cstep (i32.const S_FOUND) (local.get $x) (i32.const 0)
          (call $cstep (i32.const S_CALL) (local.get $l) (i32.const 0) (i32.const 0))))
      (local.get $st))
    (call $em (local.get $f) (@str "RaE(gbCc}") (local.get $x) (i32.load (local.get $st)) (local.get $l) (i32.const 0))
    (return)))
  (if (i32.eq (local.get $k) (i32.const S_FOUND)) (then
    (call $em1 (local.get $f) (@str "k1Wa") (local.get $a))
    (return (call $cx_emit (local.get $f) (local.get $rest) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const S_CALL)) (then
    (call $em2 (local.get $f) (@str "gaCb") (i32.load (local.get $st)) (local.get $a))
    (return)))
  (if (i32.eq (local.get $k) (i32.const S_KEEP)) (then (return (call $em0 (local.get $f) (@str "CA")))))
  (if (i32.eq (local.get $k) (i32.const S_UNMASK)) (then (return (call $unmask (local.get $f) (local.get $a) (local.get $rest) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const S_EXIT)) (then (return (call $em1 (local.get $f) (@str "k1Wa^") (local.get $a)))))
  (call $final (local.get $f)))

;; --- filters, pushed down --------------------------------------------------------------
;; A group's filters wait in a holder (+0 count, then the P_FILTER nodes, 0 once
;; tested): one is tested as soon as no later element of the group can change a
;; variable it reads (each is certainly bound, or not mentioned again), else at
;; the group's end. Steps of a group are emitted once, so the holder can change.

;; The group's elements, its filters, then rest.
(func $grp (param $els i32) (param $filters i32) (param $rest i32) (result i32)
  (local $h i32) (local $n i32) (local $l i32)
  (local.set $l (local.get $filters))
  (loop $count (if (local.get $l) (then
    (local.set $n (i32.add (local.get $n) (i32.const 1)))
    (local.set $l (i32.load offset=28 (local.get $l))) (br $count))))
  (i32.store (local.tee $h (call $anew (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2)))) (local.get $n))
  (local.set $n (i32.const 0))
  (local.set $l (local.get $filters))
  (loop $fill (if (local.get $l) (then
    (i32.store (i32.add (local.get $h) (i32.shl (local.tee $n (i32.add (local.get $n) (i32.const 1))) (i32.const 2))) (local.get $l))
    (local.set $l (i32.load offset=28 (local.get $l))) (br $fill))))
  (call $cstep (i32.const S_LIST) (local.get $els) (select (local.get $h) (i32.const 0) (local.get $n))
    (call $cstep (i32.const S_FILT) (local.get $h) (i32.const 0) (local.get $rest))))

;; Test the holder's filters that are ready (later: the slots later elements
;; mention; 0: all of them), each opening a block the row leaves when it fails:
;; how many.
(func $flush (param $f i32) (param $h i32) (param $later i32) (param $st i32) (result i32)
  (call $flush2 (local.get $f) (local.get $h) (local.get $later) (local.get $st) (i32.const 0)))

;; cheap: only filters without EXISTS (between the triples of a run, where a
;; triple still to come may be more selective than an EXISTS).
(func $flush2 (param $f i32) (param $h i32) (param $later i32) (param $st i32) (param $cheap i32) (result i32)
  (local $i i32) (local $at i32) (local $p i32) (local $n i32) (local $b i32) (local $s i32) (local $ok i32)
  (loop $filters
    (if (i32.lt_u (local.get $i) (i32.load (local.get $h))) (then
      (local.set $at (i32.add (local.get $h) (i32.shl (local.tee $i (i32.add (local.get $i) (i32.const 1))) (i32.const 2))))
      (if (local.tee $p (i32.load (local.get $at))) (then
        (local.set $ok (i32.const 1))
        (if (local.get $later) (then
          (local.set $ok (i32.eqz (i32.or (call $cx_off (i32.const 16)) (call $impure (i32.load offset=4 (local.get $p))))))
          (if (i32.and (local.get $cheap) (call $has_exists (i32.load offset=4 (local.get $p)))) (then (local.set $ok (i32.const 0))))
          (call $deep (local.tee $b (call $cx_bits)) (i32.load offset=4 (local.get $p)))
          (local.set $s (i32.const 0))
          (loop $slots
            (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
              (if (i32.and (call $bit (local.get $b) (local.get $s))
                  (i32.and (call $bit (local.get $later) (local.get $s)) (i32.ne (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const CERTAIN))))
                (then (local.set $ok (i32.const 0))))
              (local.set $s (i32.add (local.get $s) (i32.const 1)))
              (br $slots))))))
        (if (local.get $ok) (then
          (i32.store (local.get $at) (i32.const 0))
          ;; it must hold (be true, not false or an error)
          (call $em0 (local.get $f) (@str "{"))
          (local.set $s (global.get $tx_st))
          (global.set $tx_st (local.get $st))
          (call $cond (local.get $f) (i32.load offset=4 (local.get $p)))
          (global.set $tx_st (local.get $s))
          (call $em0 (local.get $f) (@str "k1Gf0"))
          (local.set $n (i32.add (local.get $n) (i32.const 1)))))))
      (br $filters))))
  (local.get $n))

(func $close (param $f i32) (param $n i32)
  (loop $ends
    (if (local.get $n) (then
      (call $em0 (local.get $f) (@str "}"))
      (local.set $n (i32.sub (local.get $n) (i32.const 1)))
      (br $ends)))))

;; Whether expression e has an EXISTS.
(func $has_exists (param $e i32) (result i32)
  (local $a i32)
  (if (i32.eq (i32.load (local.get $e)) (i32.const E_EXISTS)) (then (return (i32.const 1))))
  (if (i32.ne (i32.load (local.get $e)) (i32.const E_CALL)) (then (return (i32.const 0))))
  (local.set $a (i32.load offset=8 (local.get $e)))
  (loop $args
    (if (local.get $a) (then
      (if (call $has_exists (local.get $a)) (then (return (i32.const 1))))
      (local.set $a (i32.load offset=28 (local.get $a)))
      (br $args))))
  (i32.const 0))

;; Whether expression e calls what gives a new value each time (BNODE, RAND, UUID).
(func $impure (param $e i32) (result i32)
  (local $fn i32) (local $a i32)
  (if (i32.ne (i32.load (local.get $e)) (i32.const E_CALL)) (then (return (i32.const 0))))
  (local.set $fn (i32.load offset=4 (local.get $e)))
  (if (i32.or (i32.or (i32.eq (local.get $fn) (i32.const F_BNODE)) (i32.eq (local.get $fn) (i32.const F_RAND)))
              (i32.or (i32.eq (local.get $fn) (i32.const F_UUID)) (i32.eq (local.get $fn) (i32.const F_STRUUID))))
    (then (return (i32.const 1))))
  (local.set $a (i32.load offset=8 (local.get $e)))
  (loop $args
    (if (local.get $a) (then
      (if (call $impure (local.get $a)) (then (return (i32.const 1))))
      (local.set $a (i32.load offset=28 (local.get $a)))
      (br $args))))
  (i32.const 0))

;; One element of a group (not a triple pattern), then rest.
(func $element (param $f i32) (param $el i32) (param $rest i32) (param $st i32)
  (local $k i32) (local $b i32) (local $call i32)
  (local.set $k (i32.load (local.get $el)))
  (if (i32.eq (local.get $k) (i32.const P_GROUP)) (then
    (if (call $inline_ok (local.get $el) (local.get $st) (i32.const 0)) (then
      (return (call $cx_emit (local.get $f)
        (call $grp (i32.load offset=4 (local.get $el)) (i32.load offset=8 (local.get $el)) (local.get $rest))
        (local.get $st)))))
    (if (i32.eqz (call $cx_off (i32.const 1))) (then (return (call $masked (local.get $f) (local.get $el) (local.get $rest) (local.get $st)))))))
  (if (i32.eq (local.get $k) (i32.const P_UNION)) (then
    ;; both branches, then the same rest
    (call $deep (local.tee $b (call $cx_bits)) (local.get $el))
    (local.set $call (call $cstep (i32.const S_CALL) (call $cont (local.get $rest) (call $st_mark (local.get $st) (local.get $b) (i32.const MAYBE))) (i32.const 0) (i32.const 0)))
    (call $cx_emit (local.get $f) (call $cstep (i32.const S_EL) (i32.load offset=4 (local.get $el)) (i32.const 0) (local.get $call)) (local.get $st))
    (call $cx_emit (local.get $f) (call $cstep (i32.const S_EL) (i32.load offset=8 (local.get $el)) (i32.const 0) (local.get $call)) (local.get $st))
    (return)))
  (if (i32.eq (local.get $k) (i32.const P_OPTIONAL)) (then
    (if (call $inline_ok (i32.load offset=4 (local.get $el)) (local.get $st) (i32.const 1))
      (then (return (call $cx_emit (local.get $f) (call $cstep (i32.const S_OPT) (i32.load offset=4 (local.get $el)) (i32.const 0) (local.get $rest)) (local.get $st)))))))
  (if (i32.eq (local.get $k) (i32.const P_LATERAL)) (then (return (call $lateral (local.get $f) (local.get $el) (local.get $rest) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const P_BIND)) (then
    (return (call $cx_emit (local.get $f) (call $cstep (i32.const S_BIND) (i32.load offset=4 (local.get $el)) (i32.load offset=8 (local.get $el)) (local.get $rest)) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const P_VALUES)) (then (return (call $values (local.get $f) (local.get $el) (local.get $rest) (local.get $st)))))
  (if (i32.eq (local.get $k) (i32.const P_PATH)) (then
    (if (call $closure_loop (local.get $f) (local.get $el) (local.get $rest) (local.get $st)) (then (return)))
    (if (i32.and (call $plain (i32.load offset=4 (local.get $el))) (call $plain (i32.load offset=12 (local.get $el))))
      (then (return (call $scan_loop (local.get $f) (local.get $el) (i32.const 1) (local.get $rest) (local.get $st)))))))
  (call $eval_loop (local.get $f) (local.get $el) (local.get $rest) (local.get $st)))

;; A group that would see variables bound outside it that it does not bind
;; certainly (inline_ok fails): the interpreter joins it with the row masked
;; to its certain variables. In line, the same: those slots are cleared, the
;; group runs, and its solutions join the saved values (S_UNMASK). The record:
;; +0 count, then per slot 12 bytes: the slot, where its saved value is (in a
;; site's row: S_UNMASK may be emitted in another function), its state outside.
(func $masked (param $f i32) (param $g i32) (param $rest i32) (param $st i32)
  (local $b i32) (local $s i32) (local $rec i32) (local $n i32) (local $p i32) (local $inner i32) (local $state i32) (local $row i32) (local $m i32)
  (call $deep (local.tee $b (call $cx_bits)) (local.get $g))
  ;; a masked slot in a pattern would scan unbound per row, where the
  ;; interpreter evaluates the group once for rows that mask the same: left to it
  (local.set $m (call $cx_bits))
  (loop $which
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (call $mask_slot (local.get $g) (local.get $b) (local.get $st) (local.get $s))
        (then (call $setbit (local.get $m) (local.get $s))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $which))))
  (if (call $pat_mentions (local.get $g) (local.get $m)) (then (return (call $eval_loop (local.get $f) (local.get $g) (local.get $rest) (local.get $st)))))
  (local.set $s (i32.const 0))
  (local.set $row (i32.add (call $cx_site) (i32.const 12)))
  (local.set $rec (call $anew (i32.add (i32.const 4) (i32.mul (global.get $cx_nv) (i32.const 12)))))
  (local.set $inner (call $st_copy (local.get $st)))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (local.set $state (i32.load8_u (call $st_at (local.get $st) (local.get $s))))
      (if (call $mask_slot (local.get $g) (local.get $b) (local.get $st) (local.get $s)) (then
        (local.set $p (i32.add (i32.add (local.get $rec) (i32.const 4)) (i32.mul (local.get $n) (i32.const 12))))
        (i32.store (local.get $p) (local.get $s))
        (i32.store offset=4 (local.get $p) (i32.add (local.get $row) (i32.shl (local.get $n) (i32.const 2))))
        (i32.store offset=8 (local.get $p) (local.get $state))
        (call $em2 (local.get $f) (@str "k0k0maMb") (call $addr (local.get $s)) (i32.load offset=4 (local.get $p)))
        (if (call $lat_maybe (local.get $s))
          (then
            ;; bound in the LATERAL's row: kept, as the interpreter's mask keeps it
            (call $em2 (local.get $f) (@str "k0maE(k0k0Mb}") (i32.add (global.get $cx_lat) (i32.shl (local.get $s) (i32.const 2))) (call $addr (local.get $s)))
            (i32.store8 (call $st_at (local.get $inner) (local.get $s)) (i32.const MAYBE)))
          (else
            (call $em1 (local.get $f) (@str "k0k0Ma") (call $addr (local.get $s)))
            (i32.store8 (call $st_at (local.get $inner) (local.get $s)) (i32.const 0))))
        (local.set $n (i32.add (local.get $n) (i32.const 1)))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (i32.store (local.get $rec) (local.get $n))
  (call $cx_emit (local.get $f)
    (call $grp (i32.load offset=4 (local.get $g)) (i32.load offset=8 (local.get $g)) (call $cstep (i32.const S_UNMASK) (local.get $rec) (i32.const 0) (local.get $rest)))
    (local.get $inner))
  ;; restored
  (local.set $s (i32.const 0))
  (loop $restores
    (if (i32.lt_u (local.get $s) (local.get $n)) (then
      (local.set $p (i32.add (i32.add (local.get $rec) (i32.const 4)) (i32.mul (local.get $s) (i32.const 12))))
      (call $em2 (local.get $f) (@str "k0k0maMb") (i32.load offset=4 (local.get $p)) (call $addr (i32.load (local.get $p))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $restores)))))

;; Whether $masked clears slot s for group g (b: g's slots): bound, not one
;; of g's certain variables, a parameter, or bound in the LATERAL's row on entry.
(func $mask_slot (param $g i32) (param $b i32) (param $st i32) (param $s i32) (result i32)
  (i32.and (i32.and (call $bit (local.get $b) (local.get $s)) (i32.ne (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const 0)))
    (i32.eqz (i32.or (i32.or (call $bit (i32.load offset=12 (local.get $g)) (local.get $s)) (call $bit (global.get $cx_shape) (local.get $s)))
      (if (result i32) (global.get $cx_latbits) (then (call $bit (global.get $cx_latbits) (local.get $s))) (else (i32.const 0)))))))

;; Whether slot s may be bound in the LATERAL's row (maybe bound on entry).
(func $lat_maybe (param $s i32) (result i32)
  (if (result i32) (global.get $cx_latbits) (then (call $bit (i32.add (global.get $cx_latbits) (global.get $cx_bw)) (local.get $s))) (else (i32.const 0))))

;; Whether a triple or path pattern in node n (its groups, not its subqueries
;; or expressions) mentions a slot of bits.
(func $pat_mentions (param $n i32) (param $bits i32) (result i32)
  (local $k i32) (local $l i32) (local $c i32) (local $ref i32)
  (if (i32.eqz (local.get $n)) (then (return (i32.const 0))))
  (local.set $k (i32.load (local.get $n)))
  (if (i32.le_u (i32.sub (local.get $k) (i32.const P_TRIPLE)) (i32.const 1)) (then
    (loop $positions
      (local.set $ref (i32.load (i32.add (local.get $n) (i32.shl (i32.add (local.get $c) (i32.const 1)) (i32.const 2)))))
      (if (i32.and (i32.and (i32.lt_s (local.get $ref) (i32.const 0)) (call $plain (local.get $ref)))
                   (i32.or (i32.ne (local.get $c) (i32.const 1)) (i32.eq (local.get $k) (i32.const P_TRIPLE))))
        (then (if (call $bit (local.get $bits) (i32.sub (i32.const -1) (local.get $ref))) (then (return (i32.const 1))))))
      (br_if $positions (i32.ne (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.const 3))))
    (return (i32.const 0))))
  (if (i32.eq (local.get $k) (i32.const P_GROUP)) (then
    (local.set $l (i32.load offset=4 (local.get $n)))
    (loop $els
      (if (local.get $l) (then
        (if (call $pat_mentions (local.get $l) (local.get $bits)) (then (return (i32.const 1))))
        (local.set $l (i32.load offset=28 (local.get $l)))
        (br $els))))
    (return (i32.const 0))))
  (if (i32.eq (local.get $k) (i32.const P_UNION)) (then
    (return (i32.or (call $pat_mentions (i32.load offset=4 (local.get $n)) (local.get $bits)) (call $pat_mentions (i32.load offset=8 (local.get $n)) (local.get $bits))))))
  (if (i32.eq (local.get $k) (i32.const P_GRAPH)) (then (return (call $pat_mentions (i32.load offset=8 (local.get $n)) (local.get $bits)))))
  (if (i32.or (i32.or (i32.eq (local.get $k) (i32.const P_OPTIONAL)) (i32.eq (local.get $k) (i32.const P_MINUS))) (i32.eq (local.get $k) (i32.const P_LATERAL)))
    (then (return (call $pat_mentions (i32.load offset=4 (local.get $n)) (local.get $bits)))))
  (i32.const 0))

;; A masked group's solution joined with the saved values, then rest.
(func $unmask (param $f i32) (param $rec i32) (param $rest i32) (param $st i32)
  (local $i i32) (local $n i32) (local $p i32) (local $curs i32) (local $next i32) (local $a i32)
  (local.set $n (i32.load (local.get $rec)))
  (local.set $curs (call $anew (i32.shl (i32.add (local.get $n) (i32.const 1)) (i32.const 2))))
  (local.set $next (call $st_copy (local.get $st)))
  (call $em0 (local.get $f) (@str "{"))
  ;; checks: a saved value and a different one now: not compatible
  (loop $checks
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $p (i32.add (i32.add (local.get $rec) (i32.const 4)) (i32.mul (local.get $i) (i32.const 12))))
      (local.set $a (call $addr (i32.load (local.get $p))))
      (i32.store (i32.add (local.get $curs) (i32.shl (local.get $i) (i32.const 2))) (call $local (local.get $f)))
      (call $em (local.get $f) (@str "k0mavbk0mck0Ggbk0Gqgbk0mc")
        (local.get $a) (i32.load (i32.add (local.get $curs) (i32.shl (local.get $i) (i32.const 2)))) (i32.load offset=4 (local.get $p)) (i32.const 0))
      (call $em0 (local.get $f) (@str "Gqf0"))
      (if (i32.gt_u (i32.load offset=8 (local.get $p)) (i32.load8_u (call $st_at (local.get $next) (i32.load (local.get $p)))))
        (then (i32.store8 (call $st_at (local.get $next) (i32.load (local.get $p))) (i32.load offset=8 (local.get $p)))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $checks))))
  ;; the saved values where the group left the slot unbound
  (local.set $i (i32.const 0))
  (loop $sets
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $p (i32.add (i32.add (local.get $rec) (i32.const 4)) (i32.mul (local.get $i) (i32.const 12))))
      (call $em (local.get $f) (@str "gbE(k0k0maMc}")
        (i32.load offset=4 (local.get $p)) (i32.load (i32.add (local.get $curs) (i32.shl (local.get $i) (i32.const 2)))) (call $addr (i32.load (local.get $p))) (i32.const 0))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $sets))))
  (call $cx_emit (local.get $f) (local.get $rest) (local.get $next))
  (local.set $i (i32.const 0))
  (loop $resets
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $p (i32.add (i32.add (local.get $rec) (i32.const 4)) (i32.mul (local.get $i) (i32.const 12))))
      (call $em2 (local.get $f) (@str "k0gaMb") (i32.load (i32.add (local.get $curs) (i32.shl (local.get $i) (i32.const 2)))) (call $addr (i32.load (local.get $p))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $resets))))
  (call $em0 (local.get $f) (@str "}")))

;; Push a pattern position's value (0 when an unbound variable).
(func $push_ref (param $f i32) (param $ref i32)
  (if (i32.gt_s (local.get $ref) (i32.const 0))
    (then (call $em1 (local.get $f) (@str "ka") (local.get $ref)))
    (else (call $em1 (local.get $f) (@str "k0ma") (call $ref_addr (local.get $ref))))))

;; A triple pattern (path: a path pattern, its ends only): a loop over its
;; matches, 3 words each on the stack, binding its variables for rest.
(func $scan_loop (param $f i32) (param $el i32) (param $path i32) (param $rest i32) (param $st i32)
  (local $at i32) (local $n i32) (local $i i32) (local $r i32) (local $top i32)
  (local $c i32) (local $ref i32) (local $s i32) (local $recs i32) (local $nr i32) (local $j i32) (local $p i32) (local $next i32) (local $lst i32)
  (local.set $at (i32.load (local.get $st)))
  (local.set $n (call $local (local.get $f)))
  (local.set $i (call $local (local.get $f)))
  (local.set $r (call $local (local.get $f)))
  (local.set $top (call $local (local.get $f)))
  (if (local.get $path)
    (then
      (call $em1 (local.get $f) (@str "ka") (local.get $el))
      (call $push_ref (local.get $f) (i32.load offset=4 (local.get $el)))
      (call $push_ref (local.get $f) (i32.load offset=12 (local.get $el)))
      (call $em2 (local.get $f) (@str "gaC3vb") (local.get $at) (local.get $n)))
    (else
      ;; a bound position: the store's lists, read in line (cscan.wat)
      (if (i32.eqz (local.tee $lst (call $scan_list (local.get $f) (local.get $el) (local.get $st) (local.get $n)))) (then
        (call $push_ref (local.get $f) (i32.load offset=4 (local.get $el)))
        (call $push_ref (local.get $f) (i32.load offset=8 (local.get $el)))
        (call $push_ref (local.get $f) (i32.load offset=12 (local.get $el)))
        (call $em2 (local.get $f) (@str "gaC2vb") (local.get $at) (local.get $n))))))
  (if (local.get $lst)
    (then (call $em (local.get $f) (@str "gavbk0vc") (local.get $at) (local.get $top) (local.get $i) (i32.const 0)))
    (else (call $em (local.get $f) (@str "gagbk3ljvck0vd") (local.get $at) (local.get $n) (local.get $top) (local.get $i))))
  ;; the variables it binds (records: column, slot, saved value's local, and
  ;; for a repeated variable the first column + 1)
  (local.set $recs (call $anew (i32.const 48)))
  (local.set $next (call $st_copy (local.get $st)))
  (i32.store (local.get $next) (local.get $top))
  (loop $columns
    (local.set $ref (i32.load (i32.add (local.get $el) (i32.shl (i32.add (local.get $c) (i32.const 1)) (i32.const 2)))))
    (if (i32.and (i32.lt_s (local.get $ref) (i32.const 0)) (i32.eqz (i32.and (local.get $path) (i32.eq (local.get $c) (i32.const 1))))) (then
      (local.set $s (i32.sub (i32.const -1) (local.get $ref)))
      (if (i32.ne (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const CERTAIN)) (then
        (local.set $p (i32.add (local.get $recs) (i32.shl (local.get $nr) (i32.const 4))))
        (i32.store (local.get $p) (local.get $c))
        (i32.store offset=4 (local.get $p) (local.get $s))
        (local.set $j (i32.const 0))
        (loop $earlier
          (if (i32.lt_u (local.get $j) (local.get $nr)) (then
            (if (i32.and (i32.eq (i32.load offset=4 (i32.add (local.get $recs) (i32.shl (local.get $j) (i32.const 4)))) (local.get $s))
                         (i32.eqz (i32.load offset=12 (i32.add (local.get $recs) (i32.shl (local.get $j) (i32.const 4))))))
              (then (i32.store offset=12 (local.get $p) (i32.add (i32.load (i32.add (local.get $recs) (i32.shl (local.get $j) (i32.const 4)))) (i32.const 1)))))
            (local.set $j (i32.add (local.get $j) (i32.const 1)))
            (br $earlier))))
        (if (i32.eqz (i32.load offset=12 (local.get $p))) (then
          (i32.store offset=8 (local.get $p) (call $local (local.get $f)))
          (call $em2 (local.get $f) (@str "k0mavb") (call $addr (local.get $s)) (i32.load offset=8 (local.get $p)))))
        (local.set $nr (i32.add (local.get $nr) (i32.const 1)))))
      (i32.store8 (call $st_at (local.get $next) (local.get $s)) (i32.const CERTAIN))))
    (br_if $columns (i32.ne (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.const 3))))
  ;; r = the stack + 4 at + 12 i, or the quad of list entry i
  (if (local.get $lst)
    (then
      (call $em2 (local.get $f) (@str "{[gagbOf1") (local.get $i) (local.get $n))
      (call $cx_step (local.get $f))
      (call $scan_row (local.get $f) (local.get $el) (local.get $st) (local.get $lst) (local.get $i) (local.get $r)))
    (else
      (call $em2 (local.get $f) (@str "{[gagbOf1") (local.get $i) (local.get $n))
      (call $cx_step (local.get $f))
      (call $em (local.get $f) (@str "C5gck4ljgak<ljvdgak1jva{") (local.get $i) (local.get $n) (local.get $at) (local.get $r))))
  (call $bind_recs (local.get $f) (local.get $recs) (local.get $nr) (local.get $r) (i32.const 1))
  (call $bind_recs (local.get $f) (local.get $recs) (local.get $nr) (local.get $r) (i32.const 0))
  (call $cx_emit (local.get $f) (local.get $rest) (local.get $next))
  (call $em0 (local.get $f) (@str "}b0}}"))
  (call $bind_recs (local.get $f) (local.get $recs) (local.get $nr) (local.get $r) (i32.const 2)))

;; For the records of $scan_loop: 1 the checks of repeated variables, 0 the
;; bindings from row r, 2 the restores.
(func $bind_recs (param $f i32) (param $recs i32) (param $nr i32) (param $r i32) (param $what i32)
  (local $p i32) (local $first i32)
  (loop $each
    (if (local.get $nr) (then
      (local.set $first (i32.load offset=12 (local.get $recs)))
      (if (i32.eq (local.get $what) (i32.const 1)) (then
        (if (local.get $first) (then
          (call $em (local.get $f) (@str "gambgamcGf0") (local.get $r) (i32.shl (i32.sub (local.get $first) (i32.const 1)) (i32.const 2))
            (i32.shl (i32.load (local.get $recs)) (i32.const 2)) (i32.const 0))))))
      (if (i32.eqz (i32.or (local.get $first) (local.get $what)))
        (then (call $em (local.get $f) (@str "k0gambMc") (local.get $r) (i32.shl (i32.load (local.get $recs)) (i32.const 2))
          (call $addr (i32.load offset=4 (local.get $recs))) (i32.const 0))))
      (if (i32.and (i32.eqz (local.get $first)) (i32.eq (local.get $what) (i32.const 2)))
        (then (call $em2 (local.get $f) (@str "k0gaMb") (i32.load offset=8 (local.get $recs)) (call $addr (i32.load offset=4 (local.get $recs))))))
      (local.set $recs (i32.add (local.get $recs) (i32.const 16)))
      (local.set $nr (i32.sub (local.get $nr) (i32.const 1)))
      (br $each)))))

;; An element the interpreter evaluates against the frame: a loop over its
;; rows (which extend the frame), copying the variables it may bind.
(func $eval_loop (param $f i32) (param $el i32) (param $rest i32) (param $st i32)
  (local $at i32) (local $n i32) (local $i i32) (local $r i32) (local $top i32)
  (local $b i32) (local $s i32) (local $saves i32) (local $next i32) (local $k i32) (local $sq i32)
  (local.set $at (i32.load (local.get $st)))
  (local.set $n (call $local (local.get $f)))
  (local.set $i (call $local (local.get $f)))
  (local.set $r (call $local (local.get $f)))
  (local.set $top (call $local (local.get $f)))
  (local.set $k (i32.load (local.get $el)))
  ;; (a cross product of chains with BINDs is the interpreter's: see $cross)
  (if (if (result i32) (i32.and (i32.eq (local.get $k) (i32.const P_SUB)) (i32.eqz (call $cx_off (i32.const 8))))
        (then (i32.or (i32.ne (global.get $cx_lat) (i32.const 0)) (i32.eqz (call $cross (i32.load offset=12 (i32.load offset=4 (local.get $el)))))))
        (else (i32.const 0)))
    (then (call $sub_rows (local.get $f) (i32.load offset=4 (local.get $el)) (local.get $st) (local.get $n)))
    (else (call $em (local.get $f) (@str "kagbC4vc") (call $eval_key (local.get $el)) (local.get $at) (local.get $n) (i32.const 0))))
  (call $em (local.get $f) (@str "gagbkcljvd") (local.get $at) (local.get $n) (i32.shr_u (call $width) (i32.const 2)) (local.get $top))
  (call $em1 (local.get $f) (@str "k0va") (local.get $i))
  ;; MINUS binds nothing
  (local.set $b (call $cx_bits))
  (if (i32.ne (local.get $k) (i32.const P_MINUS)) (then (call $deep (local.get $b) (local.get $el))))
  (local.set $saves (call $anew (i32.shl (global.get $cx_nv) (i32.const 2))))
  (local.set $s (i32.const 0))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (i32.and (call $bit (local.get $b) (local.get $s)) (i32.ne (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const CERTAIN))) (then
        (i32.store (i32.add (local.get $saves) (i32.shl (local.get $s) (i32.const 2))) (call $local (local.get $f)))
        (call $em (local.get $f) (@str "k0mavb") (call $addr (local.get $s)) (i32.load (i32.add (local.get $saves) (i32.shl (local.get $s) (i32.const 2)))) (i32.const 0) (i32.const 0))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (call $em2 (local.get $f) (@str "{[gagbOf1") (local.get $i) (local.get $n))
  (call $cx_step (local.get $f))
  (call $em (local.get $f) (@str "C5gck4lj") (local.get $i) (local.get $n) (local.get $at) (i32.const 0))
  (call $em (local.get $f) (@str "gakbljvcgak1jva") (local.get $i) (call $width) (local.get $r) (i32.const 0))
  (call $copy_saved (local.get $f) (local.get $saves) (local.get $r))
  ;; what is certain after it: a group's certain variables, a triple's or path's
  (local.set $next (call $st_mark (local.get $st) (local.get $b) (i32.const MAYBE)))
  (if (i32.eq (local.get $k) (i32.const P_GROUP)) (then (local.set $next (call $st_mark (local.get $next) (i32.load offset=12 (local.get $el)) (i32.const CERTAIN)))))
  (if (i32.or (i32.eq (local.get $k) (i32.const P_TRIPLE)) (i32.eq (local.get $k) (i32.const P_PATH)))
    (then (local.set $next (call $st_mark (local.get $next) (local.get $b) (i32.const CERTAIN)))))
  (i32.store (local.get $next) (local.get $top))
  (call $cx_emit (local.get $f) (local.get $rest) (local.get $next))
  (call $em0 (local.get $f) (@str "b0}}"))
  (call $copy_saved (local.get $f) (local.get $saves) (i32.const 0)))

;; A subquery's rows (n: the local for their count): its WHERE compiled here,
;; its solutions collected (cq_sub, cq_keep), then finished and joined by the
;; engine (cq_group). Inside a LATERAL compiled in line, the interpreter
;; substitutes the LATERAL's row for the subquery's projected variables, and
;; solves it with that row (parameters and the substitutions) as $lat and its
;; parameters: here a row of the site's, filled on entry, is that, and the
;; projected variables' inner slots start bound to the LATERAL's values.
(func $sub_rows (param $f i32) (param $sq i32) (param $st i32) (param $n i32)
  (local $key i32) (local $row i32) (local $rec i32) (local $s i32) (local $m i32) (local $bits i32) (local $inner i32)
  (local $saves i32) (local $sl i32) (local $sb i32) (local $l i32)
  (local.set $key (local.get $sq))
  (local.set $inner (local.get $st))
  (local.set $sl (global.get $cx_lat))
  (local.set $sb (global.get $cx_latbits))
  (if (global.get $cx_lat) (then
    (local.set $row (i32.add (call $cx_site) (i32.const 12)))
    (i32.store offset=4 (local.tee $rec (call $cx_site)) (local.get $sq))
    (i32.store offset=8 (local.get $rec) (local.get $row))
    (local.set $key (i32.or (local.get $rec) (i32.const 1)))
    (local.set $bits (call $anew (i32.shl (global.get $cx_bw) (i32.const 1))))
    (local.set $inner (call $st_copy (local.get $st)))
    ;; the parameters
    (loop $slots
      (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
        (if (call $bit (global.get $cx_shape) (local.get $s)) (then
          (call $setbit (local.get $bits) (local.get $s))
          (call $em2 (local.get $f) (@str "k0k0maMb") (call $addr (local.get $s)) (i32.add (local.get $row) (i32.shl (local.get $s) (i32.const 2))))))
        (local.set $s (i32.add (local.get $s) (i32.const 1)))
        (br $slots))))
    ;; the substitutions: inner slot = the LATERAL row's outer slot (saved first)
    (local.set $saves (call $anew (i32.shl (global.get $cx_nv) (i32.const 2))))
    (local.set $m (i32.load offset=72 (local.get $sq)))
    (loop $map
      (if (local.get $m) (then
        (local.set $s (i32.load offset=4 (local.get $m)))
        (i32.store (i32.add (local.get $saves) (i32.shl (local.get $s) (i32.const 2))) (local.tee $l (call $local (local.get $f))))
        (call $em2 (local.get $f) (@str "k0mavb") (call $addr (local.get $s)) (local.get $l))
        (call $em (local.get $f) (@str "k0k0maMbk0k0maMc")
          (i32.add (global.get $cx_lat) (i32.shl (i32.load offset=8 (local.get $m)) (i32.const 2))) (call $addr (local.get $s))
          (i32.add (local.get $row) (i32.shl (local.get $s) (i32.const 2))) (i32.const 0))
        (if (call $bit (global.get $cx_latbits) (i32.load offset=8 (local.get $m)))
          (then
            (call $setbit (local.get $bits) (local.get $s))
            (i32.store8 (call $st_at (local.get $inner) (local.get $s)) (i32.const CERTAIN)))
          (else
            (call $setbit (i32.add (local.get $bits) (global.get $cx_bw)) (local.get $s))
            (i32.store8 (call $st_at (local.get $inner) (local.get $s)) (i32.const MAYBE))))
        (local.set $m (i32.load offset=28 (local.get $m)))
        (br $map))))
    (global.set $cx_lat (local.get $row))
    (global.set $cx_latbits (local.get $bits))))
  ;; solved once per run (as the interpreter caches it), unless substituted
  (if (i32.eqz (global.get $cx_lat)) (then (call $em1 (local.get $f) (@str "kaCME(") (local.get $sq))))
  (call $em0 (local.get $f) (@str "C@"))
  (call $cx_emit (local.get $f)
    (call $grp (i32.load offset=4 (i32.load offset=12 (local.get $sq))) (i32.load offset=8 (i32.load offset=12 (local.get $sq)))
      (call $cstep (i32.const S_KEEP) (i32.const 0) (i32.const 0) (i32.const 0)))
    (local.get $inner))
  (if (i32.eqz (global.get $cx_lat)) (then (call $em0 (local.get $f) (@str "}"))))
  (global.set $cx_lat (local.get $sl))
  (global.set $cx_latbits (local.get $sb))
  (call $em (local.get $f) (@str "kagbCBvc") (local.get $key) (i32.load (local.get $st)) (local.get $n) (i32.const 0))
  (if (local.get $saves) (then (call $copy_saved (local.get $f) (local.get $saves) (i32.const 0)))))

;; Each slot with a saved value's local: copied from row r, or (r = 0) restored.
(func $copy_saved (param $f i32) (param $saves i32) (param $r i32)
  (local $s i32) (local $l i32)
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (local.tee $l (i32.load (i32.add (local.get $saves) (i32.shl (local.get $s) (i32.const 2))))) (then
        (if (local.get $r)
          (then (call $em (local.get $f) (@str "k0gambMc") (local.get $r) (i32.shl (local.get $s) (i32.const 2)) (call $addr (local.get $s)) (i32.const 0)))
          (else (call $em2 (local.get $f) (@str "k0gaMb") (local.get $l) (call $addr (local.get $s)))))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots)))))

;; Row width in bytes, as the interpreter's $W.
(func $width (result i32)
  (i32.shl (select (global.get $cx_nv) (i32.const 3) (i32.gt_u (global.get $cx_nv) (i32.const 3))) (i32.const 2)))

;; VALUES: each data row compatible with the frame binds its variables for rest.
(func $values (param $f i32) (param $el i32) (param $rest i32) (param $st i32)
  (local $nv i32) (local $j i32) (local $i i32) (local $k i32) (local $tmp i32) (local $after i32) (local $b i32)
  (local $v i32) (local $t i32) (local $state i32) (local $saves i32) (local $pass i32)
  (local.set $nv (i32.load offset=4 (local.get $el)))
  (call $deep (local.tee $b (call $cx_bits)) (local.get $el))
  (local.set $after (call $st_mark (local.get $st) (local.get $b) (i32.const MAYBE)))
  (if (i32.gt_u (i32.load offset=12 (local.get $el)) (i32.const 1)) (then (local.set $k (call $cont (local.get $rest) (local.get $after)))))
  (local.set $tmp (call $local (local.get $f)))
  (local.set $saves (call $anew (i32.shl (local.get $nv) (i32.const 2))))
  (loop $rows
    (if (i32.lt_u (local.get $j) (i32.load offset=12 (local.get $el))) (then
      (call $em0 (local.get $f) (@str "{"))
      ;; pass 0: checks and saves; 1: the values; 2: restores
      (local.set $pass (i32.const 0))
      (loop $passes
        (local.set $i (i32.const 0))
        (loop $vars
          (if (i32.lt_u (local.get $i) (local.get $nv)) (then
            (local.set $v (i32.load (i32.add (i32.load offset=8 (local.get $el)) (i32.shl (local.get $i) (i32.const 2)))))
            (local.set $t (i32.load (i32.add (i32.load offset=16 (local.get $el)) (i32.shl (i32.add (i32.mul (local.get $j) (local.get $nv)) (local.get $i)) (i32.const 2)))))
            (local.set $state (i32.load8_u (call $st_at (local.get $st) (local.get $v))))
            (if (local.get $t) (then
              (if (i32.eqz (local.get $pass)) (then
                ;; bound already: it must agree (or be unbound, when only maybe bound)
                (if (local.get $state)
                  (then (call $em (local.get $f) (@str "k0matbk0GgbkcGqf0") (call $addr (local.get $v)) (local.get $tmp) (local.get $t) (i32.const 0))))
                (if (i32.ne (local.get $state) (i32.const CERTAIN)) (then
                  (i32.store (i32.add (local.get $saves) (i32.shl (local.get $i) (i32.const 2))) (call $local (local.get $f)))
                  (call $em (local.get $f) (@str "k0mavb") (call $addr (local.get $v)) (i32.load (i32.add (local.get $saves) (i32.shl (local.get $i) (i32.const 2)))) (i32.const 0) (i32.const 0))))))
              (if (i32.ne (local.get $state) (i32.const CERTAIN)) (then
                (if (i32.eq (local.get $pass) (i32.const 1))
                  (then (call $em2 (local.get $f) (@str "k0kbMa") (call $addr (local.get $v)) (local.get $t))))
                (if (i32.eq (local.get $pass) (i32.const 2))
                  (then (call $em (local.get $f) (@str "k0gbMa") (call $addr (local.get $v)) (i32.load (i32.add (local.get $saves) (i32.shl (local.get $i) (i32.const 2)))) (i32.const 0) (i32.const 0))))))))
            (local.set $i (i32.add (local.get $i) (i32.const 1)))
            (br $vars))))
        (if (i32.eq (local.get $pass) (i32.const 1)) (then
          (if (local.get $k)
            (then (call $em2 (local.get $f) (@str "gaCb") (i32.load (local.get $st)) (local.get $k)))
            (else (call $cx_emit (local.get $f) (local.get $rest) (local.get $after))))))
        (br_if $passes (i32.ne (local.tee $pass (i32.add (local.get $pass) (i32.const 1))) (i32.const 3))))
      (call $em0 (local.get $f) (@str "}"))
      (local.set $j (i32.add (local.get $j) (i32.const 1)))
      (br $rows)))))

;; One solution: a CONSTRUCT template instantiated, or a row.
(func $final (param $f i32)
  (local $q i32) (local $s i32) (local $map i32) (local $tp i32) (local $c i32) (local $ref i32) (local $l i32)
  (local.set $q (global.get $cx_q))
  (if (i32.ne (i32.load (local.get $q)) (i32.const 2)) (then
    (call $em0 (local.get $f) (@str "C8"))
    (return)))
  ;; fresh blank nodes for the template's
  (local.set $map (call $anew (i32.shl (global.get $cx_nv) (i32.const 2))))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (i32.and (i32.load offset=20 (i32.load (i32.add (i32.load offset=56 (local.get $q)) (i32.shl (local.get $s) (i32.const 2))))) (i32.const V_TEMPLATE)) (then
        (i32.store (i32.add (local.get $map) (i32.shl (local.get $s) (i32.const 2))) (local.tee $l (call $local (local.get $f))))
        (call $em1 (local.get $f) (@str "C;va") (local.get $l))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (local.set $tp (i32.load offset=36 (local.get $q)))
  (loop $triples
    (if (local.get $tp) (then
      (local.set $c (i32.const 0))
      (loop $positions
        (local.set $ref (i32.load (i32.add (local.get $tp) (i32.shl (i32.add (local.get $c) (i32.const 1)) (i32.const 2)))))
        (if (i32.lt_s (local.get $ref) (i32.const 0))
          (then
            (if (local.tee $l (i32.load (i32.add (local.get $map) (i32.shl (i32.sub (i32.const -1) (local.get $ref)) (i32.const 2)))))
              (then (call $em1 (local.get $f) (@str "ga") (local.get $l)))
              (else (call $push_ref (local.get $f) (local.get $ref)))))
          (else (call $push_ref (local.get $f) (local.get $ref))))
        (br_if $positions (i32.ne (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.const 3))))
      (call $em0 (local.get $f) (@str "C9"))
      (local.set $tp (i32.load offset=28 (local.get $tp)))
      (br $triples)))))

;; --- expressions --------------------------------------------------------------------

(func $cx_simple (param $e i32) (result i32)
  (i32.le_u (i32.sub (i32.load (local.get $e)) (i32.const E_VAR)) (i32.const 1)))

;; An IRI (or blank node) constant: = is then sameTerm (and never an error once bound).
(func $named (param $e i32) (result i32)
  (if (result i32) (i32.eq (i32.load (local.get $e)) (i32.const E_CONST))
    (then (i32.le_u (i32.sub (call $kind (i32.load offset=4 (local.get $e))) (i32.const 1)) (i32.const 1)))
    (else (i32.const 0))))

;; Push an expression's value: a term id, 0 for unbound or an error.
(func $value (param $f i32) (param $e i32)
  (local $k i32) (local $fn i32) (local $a i32) (local $t i32)
  (local.set $k (i32.load (local.get $e)))
  (if (i32.eq (local.get $k) (i32.const E_VAR)) (then
    (call $em1 (local.get $f) (@str "k0ma") (call $addr (i32.load offset=4 (local.get $e))))
    (return)))
  (if (i32.eq (local.get $k) (i32.const E_CONST)) (then
    (call $em1 (local.get $f) (@str "ka") (i32.load offset=4 (local.get $e)))
    (return)))
  (if (call $tx_worth (local.get $e)) (then (return (call $tx_top (local.get $f) (local.get $e) (i32.const 0)))))
  (if (i32.eq (local.get $k) (i32.const E_CALL)) (then
    (local.set $fn (i32.load offset=4 (local.get $e)))
    (local.set $a (i32.load offset=8 (local.get $e)))
    (local.set $t (call $local (local.get $f)))
    (if (i32.eq (local.get $fn) (i32.const F_COALESCE)) (then
      ;; the first that is bound
      (call $em0 (local.get $f) (@str "<"))
      (loop $args
        (if (local.get $a) (then
          (call $value (local.get $f) (local.get $a))
          (call $em1 (local.get $f) (@str "tagaf0_") (local.get $t))
          (local.set $a (i32.load offset=28 (local.get $a)))
          (br $args))))
      (call $em0 (local.get $f) (@str "k0}"))
      (return)))
    (if (i32.eq (local.get $fn) (i32.const F_IF)) (then
      ;; an error in the condition is one
      (call $cond (local.get $f) (local.get $a))
      (call $em1 (local.get $f) (@str "tak1F?") (local.get $t))
      (call $value (local.get $f) (local.tee $a (i32.load offset=28 (local.get $a))))
      (call $em1 (local.get $f) (@str ":gaE?") (local.get $t))
      (call $value (local.get $f) (i32.load offset=28 (local.get $a)))
      (call $em0 (local.get $f) (@str ":k0}}"))
      (return)))
    (if (i32.and (i32.eq (local.get $fn) (i32.const F_STR)) (call $cx_simple (local.get $a))) (then
      ;; STR of an xsd:string literal is itself
      (call $value (local.get $f) (local.get $a))
      (call $em1 (local.get $f) (@str "taC=k2F?ga:") (local.get $t))
      (call $em1 (local.get $f) (@str "kaC7}") (i32.or (local.get $e) (global.get $cx_keep)))
      (return)))))
  (call $em1 (local.get $f) (@str "kaC7") (i32.or (local.get $e) (global.get $cx_keep))))

;; An empty simple literal constant.
(func $empty_str (param $e i32) (result i32)
  (local $id i32)
  (if (i32.ne (i32.load (local.get $e)) (i32.const E_CONST)) (then (return (i32.const 0))))
  (local.set $id (i32.load offset=4 (local.get $e)))
  (i32.and (i32.eq (call $kind (local.get $id)) (i32.const 3))
    (i32.and (i32.eq (call $tdt (local.get $id)) (i32.const 2)) (i32.eqz (call $tlen (local.get $id))))))

;; lang(x), x a variable or constant: x (else 0).
(func $lang_of (param $e i32) (result i32)
  (local $a i32)
  (if (i32.and (i32.eq (i32.load (local.get $e)) (i32.const E_CALL)) (i32.eq (i32.load offset=4 (local.get $e)) (i32.const F_LANG))) (then
    (if (call $cx_simple (local.tee $a (i32.load offset=8 (local.get $e)))) (then (return (local.get $a))))))
  (i32.const 0))

;; Push a condition: 0 false, 1 true, 2 an error (the interpreter's three values).
(func $cond (param $f i32) (param $e i32)
  (local $fn i32) (local $a0 i32) (local $a1 i32) (local $x i32) (local $y i32) (local $d i32)
  (if (call $tx_worth (local.get $e)) (then (return (call $tx_top (local.get $f) (local.get $e) (i32.const 1)))))
  (block $generic
    (br_if $generic (i32.ne (i32.load (local.get $e)) (i32.const E_CALL)))
    (local.set $fn (i32.load offset=4 (local.get $e)))
    (local.set $a0 (i32.load offset=8 (local.get $e)))
    (br_if $generic (i32.eqz (local.get $a0)))
    (local.set $a1 (i32.load offset=28 (local.get $a0)))
    (if (i32.and (i32.eq (local.get $fn) (i32.const F_BOUND)) (i32.eq (i32.load (local.get $a0)) (i32.const E_VAR))) (then
      (call $em1 (local.get $f) (@str "k0mak0G") (call $addr (i32.load offset=4 (local.get $a0))))
      (return)))
    (local.set $x (call $local (local.get $f)))
    (if (i32.eq (local.get $fn) (i32.const F_NOT)) (then
      ;; an error stays one: select(x ^ 1, x, x != 2)
      (call $cond (local.get $f) (local.get $a0))
      (call $em1 (local.get $f) (@str "tak1sgagak2G~") (local.get $x))
      (return)))
    (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_OR)) (i32.const 1)) (then
      ;; AND: false decides; OR: true decides; else an error if either is one
      (local.set $d (i32.eq (local.get $fn) (i32.const F_OR)))
      (local.set $y (call $local (local.get $f)))
      (call $cond (local.get $f) (local.get $a0))
      (call $em2 (local.get $f) (@str "takbF?kb:") (local.get $x) (local.get $d))
      (call $cond (local.get $f) (local.get $a1))
      (call $em (local.get $f) (@str "takbF?kb:k2kcgdgark2q~}}") (local.get $y) (local.get $d) (i32.sub (i32.const 1) (local.get $d)) (local.get $x))
      (return)))
    (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_EQ)) (i32.const 1)) (then
      ;; lang(x) = "" either way round: 2 unless a literal, else whether it has no language
      (local.set $y (i32.const 0))
      (if (call $empty_str (local.get $a1)) (then (local.set $y (call $lang_of (local.get $a0)))))
      (if (i32.and (i32.eqz (local.get $y)) (call $empty_str (local.get $a0))) (then (local.set $y (call $lang_of (local.get $a1)))))
      (if (local.get $y) (then
        (call $value (local.get $f) (local.get $y))
        (call $em1 (local.get $f) (@str "taC<k3G?k2:gaC>") (local.get $x))
        (if (i32.eq (local.get $fn) (i32.const F_NE))
          (then (call $em0 (local.get $f) (@str "k0G}")))
          (else (call $em0 (local.get $f) (@str "E}"))))
        (return)))))
    (br_if $generic (i32.eqz (call $cx_simple (local.get $a0))))
    (if (i32.le_u (i32.sub (local.get $fn) (i32.const F_IN)) (i32.const 1)) (then
      ;; x IN (IRIs): an error when x is unbound, else whether it is one of them
      (local.set $y (local.get $a1))
      (loop $members
        (if (local.get $y) (then
          (br_if $generic (i32.eqz (call $named (local.get $y))))
          (local.set $y (i32.load offset=28 (local.get $y)))
          (br $members))))
      (call $value (local.get $f) (local.get $a0))
      (call $em1 (local.get $f) (@str "vak0") (local.get $x))
      (local.set $y (local.get $a1))
      (loop $members
        (if (local.get $y) (then
          (call $em2 (local.get $f) (@str "gakbFr") (local.get $x) (i32.load offset=4 (local.get $y)))
          (local.set $y (i32.load offset=28 (local.get $y)))
          (br $members))))
      (if (i32.eq (local.get $fn) (i32.const F_NOTIN)) (then (call $em0 (local.get $f) (@str "E"))))
      (call $em1 (local.get $f) (@str "k2gak0G~") (local.get $x))
      (return)))
    (if (i32.or (i32.eq (local.get $fn) (i32.const F_ISIRI)) (i32.le_u (i32.sub (local.get $fn) (i32.const F_ISURI)) (i32.const 2))) (then
      (call $value (local.get $f) (local.get $a0))
      (call $em (local.get $f) (@str "taE?k2:gaC<kbF}") (local.get $x)
        (select (i32.const 1) (i32.sub (local.get $fn) (i32.const 76)) (i32.le_u (local.get $fn) (i32.const F_ISURI))) (i32.const 0) (i32.const 0))
      (return)))
    (br_if $generic (i32.eqz (local.get $a1)))
    (br_if $generic (i32.eqz (call $cx_simple (local.get $a1))))
    (br_if $generic (i32.eqz (i32.or (i32.eq (local.get $fn) (i32.const F_SAMETERM))
      (i32.and (i32.le_u (i32.sub (local.get $fn) (i32.const F_EQ)) (i32.const 1)) (i32.or (call $named (local.get $a0)) (call $named (local.get $a1)))))))
    ;; 2 if either is unbound, else whether they are (not) the same term
    (local.set $y (call $local (local.get $f)))
    (call $value (local.get $f) (local.get $a0))
    (call $em1 (local.get $f) (@str "va") (local.get $x))
    (call $value (local.get $f) (local.get $a1))
    (call $em2 (local.get $f) (@str "vagbEgaEr?k2:gbga") (local.get $y) (local.get $x))
    (call $bput (local.get $f) (select (i32.const 0x47) (i32.const 0x46) (i32.eq (local.get $fn) (i32.const F_NE))))
    (call $em0 (local.get $f) (@str "}"))
    (return))
  (call $em1 (local.get $f) (@str "kaC6") (i32.or (local.get $e) (global.get $cx_keep))))

;; --- the module -----------------------------------------------------------------------

;; The module's bytes: header (types, imports: cx-header.ts writes it), functions,
;; globals, exports, code.
(@define CX_HEADER 640)
(@define CX_IMPORTS 31)
(@define CX_BU_EVENT 30)
(func $module (result i32)
  (local $out i32) (local $t i32) (local $i i32) (local $f i32) (local $body i32) (local $n i32)
  (local.set $out (call $bb_new))
  (call $bputs (local.get $out)
    (@words32 0x6d736100 0x00000001 0x60093501 0x60007f01 0x7f017f01 0x7f7f0360 0x607f017f 0x7f7f7f04 0x607f017f 0x607f0100 0x03600000 0x007f7f7f 0x7f7f0260 0x05607f01 0x7f7f7f7f 0x027f017f 0x062004be 0x69676e65 0x6d06656e 0x726f6d65 0x00000279 0x676e6506 0x08656e69 0x625f7163 0x6e696765 0x65060200 0x6e69676e 0x71630865 0x6d6f635f 0x00006f62 0x676e6506 0x07656e69 0x735f7163 0x006e6163 0x6e650603 0x656e6967 0x5f716307 0x68746170 0x65060300 0x6e69676e 0x71630765 0x6176655f 0x0607006c 0x69676e65 0x6308656e 0x74735f71 0x006b6361 0x6e650604 0x656e6967 0x5f716307 0x74736574 0x65060100 0x6e69676e 0x71630865 0x6c61765f 0x01006575 0x676e6506 0x0b656e69 0x735f7163 0x74756c6f 0x006e6f69 0x6e650605 0x656e6967 0x5f716309 0x70697274 0x0600656c 0x676e6506 0x06656e69 0x655f7163 0x0400646e 0x676e6506 0x0b656e69 0x73657266 0x6e625f68 0x0065646f 0x6e650604 0x656e6967 0x72657409 0x696b5f6d 0x0100646e 0x676e6506 0x0d656e69 0x6d726574 0x7461645f 0x70797461 0x06010065 0x69676e65 0x740d656e 0x5f6d7265 0x676e616c 0x6e656c5f 0x65060100 0x6e69676e 0x61680365 0x06030073 0x69676e65 0x6306656e 0x75735f71 0x06050062 0x69676e65 0x6307656e 0x656b5f71 0x05007065 0x676e6506 0x08656e69 0x675f7163 0x70756f72 0x65060700 0x6e69676e 0x71630665 0x7274735f 0x65060700 0x6e69676e 0x71630665 0x6972695f 0x65060700 0x6e69676e 0x71630665 0x6d756e5f 0x65060100 0x6e69676e 0x71630865 0x6e6f745f 0x03006d75 0x676e6506 0x07656e69 0x745f7163 0x006d7265 0x6e650601 0x656e6967 0x5f716308 0x656c7063 0x0607006e 0x69676e65 0x6308656e 0x70635f71 0x0066666f 0x6e650602 0x656e6967 0x5f716308 0x66727473 0x0608006e 0x69676e65 0x6306656e 0x62655f71 0x06010076 0x69676e65 0x6307656e 0x6f725f71 0x01006d6f 0x676e6506 0x09656e69 0x635f7163 0x65686361 0x06010064 0x69676e65 0x630b656e 0x75625f71 0x6576655f 0x0400746e)
    (i32.const CX_HEADER))
  (local.set $n (i32.load offset=4 (global.get $cx_funcs)))
  ;; functions: run (i32) -> i32, the rest (i32) -> ()
  (call $uleb (local.tee $t (call $bb_new)) (local.get $n))
  (loop $types
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (call $bput (local.get $t) (i32.eqz (local.get $i)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $types))))
  (call $section (local.get $out) (i32.const 3) (local.get $t))
  ;; mutable i32 globals, 0 initially
  (if (global.get $cx_ng) (then
    (call $uleb (local.tee $t (call $bb_new)) (global.get $cx_ng))
    (local.set $i (i32.const 0))
    (loop $globals
      (if (i32.lt_u (local.get $i) (global.get $cx_ng)) (then
        (call $bput (local.get $t) (i32.const 0x7f))
        (call $bput (local.get $t) (i32.const 1))
        (call $em0 (local.get $t) (@str "k0}"))
        (local.set $i (i32.add (local.get $i) (i32.const 1)))
        (br $globals))))
    (call $section (local.get $out) (i32.const 6) (local.get $t))))
  ;; export "run": the first function after the imports
  (call $uleb (local.tee $t (call $bb_new)) (i32.const 1))
  (call $uleb (local.get $t) (i32.const 3))
  (call $bputs (local.get $t) (@str "run"))
  (call $uleb (local.get $t) (i32.const 0))
  (call $uleb (local.get $t) (i32.const CX_IMPORTS))
  (call $section (local.get $out) (i32.const 7) (local.get $t))
  ;; code
  (call $uleb (local.tee $t (call $bb_new)) (local.get $n))
  (local.set $i (i32.const 0))
  (loop $funcs
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $f (call $v_at (global.get $cx_funcs) (local.get $i)))
      (local.set $body (call $bb_new))
      (call $locals_decl (local.get $body) (local.get $f))
      (call $bputs (local.get $body) (i32.load (local.get $f)) (i32.load offset=4 (local.get $f)))
      (call $bput (local.get $body) (i32.const 0x0b))
      (call $bvec (local.get $t) (local.get $body))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $funcs))))
  (call $section (local.get $out) (i32.const 10) (local.get $t))
  (local.get $out))

(func $section (param $out i32) (param $id i32) (param $t i32)
  (call $bput (local.get $out) (local.get $id))
  (call $bvec (local.get $out) (local.get $t)))

;; Set in bitset b the slots bound now (bind or bind_values).
(func $bound_slots (param $q i32) (param $b i32)
  (local $s i32) (local $v i32)
  (if (local.tee $v (i32.load offset=80 (local.get $q)))
    (then (call $set_params (local.get $q) (i32.load offset=84 (local.get $q)) (i32.load offset=116 (local.get $q)) (i32.const 1))))
  (loop $slots
    (if (i32.lt_u (local.get $s) (i32.load offset=52 (local.get $q))) (then
      (if (i32.load (i32.add (i32.load offset=60 (local.get $q)) (i32.shl (local.get $s) (i32.const 2))))
        (then (call $setbit (local.get $b) (local.get $s))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (if (local.get $v)
    (then (call $set_params (local.get $q) (i32.load offset=84 (local.get $q)) (i32.load offset=116 (local.get $q)) (i32.const 0)))))

(func $cx_setup (param $q i32)
  (global.set $cx_q (local.get $q))
  (global.set $cx_nv (i32.load offset=52 (local.get $q)))
  (global.set $cx_bw (i32.shl (i32.shr_u (i32.add (global.get $cx_nv) (i32.const 31)) (i32.const 5)) (i32.const 2)))
  (global.set $cx_frame (i32.load offset=200 (local.get $q))))

;; Per query: +200 the frame, +204 the shapes compiled for (bitsets after a next pointer).
(func $cx_release (param $q i32)
  (local $s i32) (local $next i32)
  (call $cx_setup (local.get $q))
  (if (global.get $cx_frame) (then (call $free (global.get $cx_frame) (call $width))))
  (local.set $s (i32.load offset=236 (local.get $q)))
  (loop $sites
    (if (local.get $s) (then
      (local.set $next (i32.load (local.get $s)))
      (call $free (local.get $s) (i32.add (i32.const 12) (call $width)))
      (local.set $s (local.get $next))
      (br $sites))))
  (local.set $s (i32.load offset=204 (local.get $q)))
  (loop $shapes
    (if (local.get $s) (then
      (local.set $next (i32.load (local.get $s)))
      (call $free (local.get $s) (i32.add (global.get $cx_bw) (i32.const 4)))
      (local.set $s (local.get $next))
      (br $shapes)))))

(func (export "compile") (param $q i32) (result i32)
  (local $tp i32) (local $c i32) (local $shape i32) (local $save_head i32) (local $save_p i32) (local $save_end i32)
  (local $run i32) (local $steps i32) (local $last i32) (local $item i32) (local $st i32) (local $s i32) (local $out i32) (local $n i32)
  (if (i32.eq (i32.load (local.get $q)) (i32.const 4)) (then (return (call $fail (@str "not compilable: DESCRIBE")))))
  (if (i32.or (i32.or (i32.and (i32.load offset=4 (local.get $q)) (i32.const Q_GROUPED)) (i32.load offset=16 (local.get $q)))
              (i32.or (i32.load offset=20 (local.get $q)) (i32.load offset=68 (local.get $q))))
    (then (return (call $fail (@str "not compilable: aggregates")))))
  (if (i32.or (i32.or (i32.load offset=24 (local.get $q)) (i32.load offset=32 (local.get $q))) (i32.ne (i32.load offset=28 (local.get $q)) (i32.const -1)))
    (then (return (call $fail (@str "not compilable: ORDER BY, LIMIT or OFFSET")))))
  (if (i32.eq (i32.load (local.get $q)) (i32.const 2)) (then
    (local.set $tp (i32.load offset=36 (local.get $q)))
    (loop $triples
      (if (local.get $tp) (then
        (local.set $c (i32.const 4))
        (loop $positions
          (if (i32.eqz (call $plain (i32.load (i32.add (local.get $tp) (local.get $c)))))
            (then (return (call $fail (@str "not compilable: triple term in template")))))
          (br_if $positions (i32.ne (local.tee $c (i32.add (local.get $c) (i32.const 4))) (i32.const 16))))
        (local.set $tp (i32.load offset=28 (local.get $tp)))
        (br $triples))))))
  (call $cx_setup (local.get $q))
  (global.set $cx_lat (i32.const 0))
  (global.set $cx_latbits (i32.const 0))
  ;; the frame, once per query; this compile's shape
  (if (i32.eqz (global.get $cx_frame)) (then
    (global.set $cx_frame (call $alloc0 (call $width)))
    (i32.store offset=200 (local.get $q) (global.get $cx_frame))))
  (local.set $shape (call $alloc0 (i32.add (global.get $cx_bw) (i32.const 4))))
  (call $bound_slots (local.get $q) (i32.add (local.get $shape) (i32.const 4)))
  (i32.store (local.get $shape) (i32.load offset=204 (local.get $q)))
  (i32.store offset=204 (local.get $q) (local.get $shape))
  (global.set $cx_shape (i32.add (local.get $shape) (i32.const 4)))
  ;; everything else in an arena of its own
  (local.set $save_head (global.get $ar_head))
  (local.set $save_p (global.get $ar_p))
  (local.set $save_end (global.get $ar_end))
  (global.set $ar_head (i32.const 0))
  (global.set $ar_p (i32.const 0))
  (global.set $ar_end (i32.const 0))
  (global.set $cx_funcs (call $v_new))
  (global.set $cx_ng (i32.const 0))
  (global.set $tx_used (i32.const -1))
  ;; run(graphs): n = cq_begin(h, shape, graphs) (an error: return it); for each
  ;; combination i < n: cq_combo(i), the plan; then cq_end()
  (call $v_push1 (global.get $cx_funcs) (local.tee $run (call $bb_new)))
  (i32.store offset=12 (local.get $run) (i32.const 1))
  (i32.store offset=16 (local.get $run) (i32.const 3))
  (call $em (local.get $run) (@str "kakbg0C0tck0H(gc^}{[gdgcOf1gdC1") (local.get $q) (local.get $shape) (i32.const 1) (i32.const 2))
  ;; the plan: WHERE, its filters, the trailing VALUES, SELECT expressions, a solution
  (local.set $last (local.tee $steps (call $cstep (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0))))
  (local.set $item (i32.load offset=8 (local.get $q)))
  (local.set $c (i32.const 0))
  (loop $items
    (if (local.get $item) (then
      (if (i32.load offset=8 (local.get $item)) (then
        (i32.store offset=12 (local.get $last)
          (local.tee $last (call $cstep (select (i32.const S_SEL) (i32.const S_BIND) (local.get $c)) (i32.load offset=8 (local.get $item)) (i32.load offset=4 (local.get $item)) (i32.const 0))))
        ;; evaluated unless a parameter
        (local.set $c (i32.or (local.get $c) (i32.eqz (call $bit (global.get $cx_shape) (i32.load offset=4 (local.get $item))))))))
      (local.set $item (i32.load offset=28 (local.get $item)))
      (br $items))))
  (i32.store offset=12 (local.get $last) (call $cstep (i32.const S_FINAL) (i32.const 0) (i32.const 0) (i32.const 0)))
  (local.set $steps (i32.load offset=12 (local.get $steps)))
  (if (i32.load offset=40 (local.get $q)) (then (local.set $steps (call $cstep (i32.const S_EL) (i32.load offset=40 (local.get $q)) (i32.const 0) (local.get $steps)))))
  (local.set $steps
    (call $grp (i32.load offset=4 (i32.load offset=12 (local.get $q))) (i32.load offset=8 (i32.load offset=12 (local.get $q))) (local.get $steps)))
  ;; parameters are certain; the stack top is local 3
  (local.set $st (call $anew (i32.add (global.get $cx_nv) (i32.const 4))))
  (i32.store (local.get $st) (i32.const 3))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (call $bit (global.get $cx_shape) (local.get $s)) (then (i32.store8 (call $st_at (local.get $st) (local.get $s)) (i32.const CERTAIN))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (call $cx_emit (local.get $run) (local.get $steps) (local.get $st))
  (call $em1 (local.get $run) (@str "gak1jvab0}}C:") (i32.const 2))
  (local.set $out (call $module))
  (local.set $n (i32.load offset=4 (local.get $out)))
  (call $res_reserve (local.get $n))
  (memory.copy (global.get $res) (i32.load (local.get $out)) (local.get $n))
  (call $v_free (global.get $cx_funcs))
  (call $arena_free (global.get $ar_head))
  (global.set $ar_head (local.get $save_head))
  (global.set $ar_p (local.get $save_p))
  (global.set $ar_end (local.get $save_end))
  (local.get $n))

;; --- what a compiled module calls ------------------------------------------------------

(global $cs (mut i32) (i32.const 0))       ;; the stack (words)
(global $cs_n (mut i32) (i32.const 0))     ;; words used
(global $cs_cap (mut i32) (i32.const 0))   ;; words allocated
(global $cx_count (mut i32) (i32.const 0)) ;; solutions
(global $cx_dset (mut i32) (i32.const 0))  ;; DISTINCT rows of this combination

;; Room for the stack to hold `need` words (a run away: a trap).
(func $cs_room (param $need i32)
  (local $cap i32)
  (if (i32.gt_u (local.get $need) (i32.const 0x1000000)) (then unreachable))
  (if (i32.gt_u (local.get $need) (global.get $cs_cap)) (then
    (local.set $cap (i32.add (local.get $need) (global.get $cs_cap)))
    (global.set $cs (if (result i32) (global.get $cs_cap)
      (then (call $realloc (global.get $cs) (i32.shl (global.get $cs_cap) (i32.const 2)) (i32.shl (local.get $cap) (i32.const 2)) (i32.shl (global.get $cs_n) (i32.const 2))))
      (else (call $alloc (i32.shl (local.get $cap) (i32.const 2))))))
    (global.set $cs_cap (local.get $cap)))))

(func (export "cq_begin") (param $q i32) (param $shape i32) (param $graphs i32) (result i32)
  (local $b i32) (local $ok i32)
  (call $cx_setup (local.get $q))
  (call $bound_slots (local.get $q) (local.tee $b (call $alloc0 (global.get $cx_bw))))
  (local.set $ok (call $memeq (local.get $b) (i32.add (local.get $shape) (i32.const 4)) (global.get $cx_bw)))
  (call $free (local.get $b) (global.get $cx_bw))
  (if (i32.eqz (local.get $ok)) (then (return (call $fail (@str "bound variables changed since compile")))))
  ;; the run's budget (budget.wat): the module steps in line, cq_end says -2 once it is spent
  (call $bu_start)
  (call $run_begin (local.get $q) (local.get $graphs))
  (call $cx_sbuf)
  (i32.store offset=8 (global.get $bases) (global.get $dg))
  (i32.store offset=12 (global.get $bases) (global.get $dgn))
  (global.set $cx_count (i32.const 0))
  (global.set $cs_n (i32.const 0))
  (call $cs_room (i32.const 1024))
  (select (i32.load offset=80 (local.get $q)) (i32.const 1) (i32.load offset=80 (local.get $q))))

(func (export "cq_combo") (param $i i32)
  (if (i32.load offset=80 (global.get $q)) (then (call $run_value (global.get $q) (local.get $i))))
  (memory.copy (global.get $cx_frame) (global.get $params) (global.get $W))
  (if (global.get $cx_dset) (then (call $set_free (global.get $cx_dset)) (global.set $cx_dset (i32.const 0))))
  (if (i32.and (i32.eq (i32.load (global.get $q)) (i32.const 1)) (i32.and (i32.load offset=4 (global.get $q)) (i32.const 3)))
    (then (global.set $cx_dset (call $set_make (select (i32.load offset=100 (global.get $q)) (i32.const 1) (i32.load offset=100 (global.get $q))))))))

(func (export "cq_stack") (result i32) (global.get $cs))

;; Room for the stack to hold need words (a compiled walk writes nodes there
;; itself): where it is now.
(func (export "cq_room") (param $need i32) (result i32)
  (call $cs_room (local.get $need))
  (global.set $cs_n (local.get $need))
  (global.get $cs))

(func (export "cq_scan") (param $s i32) (param $p i32) (param $o i32) (param $at i32) (result i32)
  (local $gi i32) (local $gn i32) (local $j i32) (local $r i32) (local $g i32) (local $w i32)
  (global.set $cs_n (local.get $at))
  (local.set $gn (call $graph_count))
  (loop $graphs
    (if (i32.lt_u (local.get $gi) (local.get $gn)) (then
      (call $scan (local.get $s) (local.get $p) (local.get $o) (local.tee $g (call $graph_at (local.get $gi))))
      (local.set $j (i32.const 0))
      (loop $quads
        (if (i32.lt_u (local.get $j) (global.get $sc_n)) (then
          (local.set $r (call $sc_quad (local.get $j)))
          (if (i32.and (call $quad_is (local.get $r) (local.get $s) (local.get $p) (local.get $o)) (i32.eq (i32.load offset=12 (local.get $r)) (local.get $g))) (then
            (if (i32.eqz (if (result i32) (local.get $gi) (then (call $seen_before (local.get $gi) (local.get $r))) (else (i32.const 0)))) (then
              (call $cs_room (i32.add (global.get $cs_n) (i32.const 3)))
              (memory.copy (i32.add (global.get $cs) (i32.shl (global.get $cs_n) (i32.const 2))) (local.get $r) (i32.const 12))
              (global.set $cs_n (i32.add (global.get $cs_n) (i32.const 3)))))))
          (local.set $j (i32.add (local.get $j) (i32.const 1)))
          (br $quads))))
      (local.set $gi (i32.add (local.get $gi) (i32.const 1)))
      (br $graphs))))
  (i32.div_u (i32.sub (global.get $cs_n) (local.get $at)) (i32.const 3)))

;; The interpreter's rows for element k against the frame: a table.
(func $cx_rows (param $k i32) (param $path i32) (result i32)
  (local $t i32) (local $out i32)
  (local.set $t (call $t_one (global.get $cx_frame)))
  (if (local.get $path)
    (then (call $eval_path (local.get $k) (local.get $t) (local.tee $out (call $t_new))))
    (else
      (local.set $out (if (result i32) (i32.eq (i32.load (local.get $k)) (i32.const P_TRIPLE))
        (then (call $eval_bgp (local.get $k) (i32.const 1) (local.get $t)))
        (else (call $eval_elem (local.get $k) (local.get $t)))))))
  (call $t_free (local.get $t))
  (local.get $out))

(func (export "cq_path") (param $k i32) (param $s i32) (param $o i32) (param $at i32) (result i32)
  (local $t i32) (local $n i32) (local $i i32) (local $row i32) (local $w i32)
  (local.set $n (i32.load offset=4 (local.tee $t (call $cx_rows (local.get $k) (i32.const 1)))))
  (global.set $cs_n (local.get $at))
  (call $cs_room (i32.add (local.get $at) (i32.mul (local.get $n) (i32.const 3))))
  (loop $rows
    (if (i32.lt_u (local.get $i) (local.get $n)) (then
      (local.set $row (call $t_row (local.get $t) (local.get $i)))
      (local.set $w (i32.add (global.get $cs) (i32.shl (i32.add (local.get $at) (i32.mul (local.get $i) (i32.const 3))) (i32.const 2))))
      (i32.store (local.get $w) (call $resolve_ref (i32.load offset=4 (local.get $k)) (local.get $row)))
      (i32.store offset=4 (local.get $w) (i32.const 0))
      (i32.store offset=8 (local.get $w) (call $resolve_ref (i32.load offset=12 (local.get $k)) (local.get $row)))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (global.set $cs_n (i32.add (local.get $at) (i32.mul (local.get $n) (i32.const 3))))
  (call $t_free (local.get $t))
  (local.get $n))

(func (export "cq_eval") (param $k i32) (param $at i32) (result i32)
  (local $t i32) (local $n i32) (local $words i32) (local $saved i32)
  ;; k | 1: an element inside a LATERAL compiled in line (see $eval_key)
  (local.set $saved (global.get $lat))
  (if (i32.and (local.get $k) (i32.const 1)) (then
    (global.set $lat (i32.load offset=8 (i32.and (local.get $k) (i32.const -2))))
    (local.set $k (i32.load offset=4 (i32.and (local.get $k) (i32.const -2))))))
  (local.set $t (call $cx_rows (local.get $k) (i32.const 0)))
  (global.set $lat (local.get $saved))
  (local.set $n (i32.load offset=4 (local.get $t)))
  (local.set $words (i32.mul (local.get $n) (i32.shr_u (global.get $W) (i32.const 2))))
  (global.set $cs_n (local.get $at))
  (call $cs_room (i32.add (local.get $at) (local.get $words)))
  (memory.copy (i32.add (global.get $cs) (i32.shl (local.get $at) (i32.const 2))) (i32.load (local.get $t)) (i32.shl (local.get $words) (i32.const 2)))
  (global.set $cs_n (i32.add (local.get $at) (local.get $words)))
  (call $t_free (local.get $t))
  (local.get $n))

;; A subquery compiled in line: cq_sub starts collecting its WHERE's solutions
;; (the frame, at each cq_keep), cq_group finishes it as the interpreter would
;; (grouping, aggregates, SELECT expressions, ORDER BY, DISTINCT, slice), moves
;; its projected variables to the outer slots, and joins that with the frame:
;; n rows (as cq_eval's) from at. Collections nest (a vector of tables).
(global $cx_acc (mut i32) (i32.const 0))

(func (export "cq_sub")
  (if (i32.eqz (global.get $cx_acc)) (then (global.set $cx_acc (call $v_new))))
  (call $v_push1 (global.get $cx_acc) (call $t_new)))

(func (export "cq_keep")
  (memory.copy (call $t_add (call $v_at (global.get $cx_acc) (i32.sub (i32.load offset=4 (global.get $cx_acc)) (i32.const 1))))
    (global.get $cx_frame) (global.get $W)))

;; sq | 1: a record (+4 the subquery, +8 its substitution row: see $sub_rows).
(func (export "cq_group") (param $sq i32) (param $at i32) (result i32)
  (local $t i32) (local $mapped i32) (local $one i32) (local $out i32) (local $i i32) (local $row i32) (local $src i32) (local $m i32)
  (local $n i32) (local $words i32) (local $sub i32) (local $sp i32) (local $sl i32)
  (if (i32.and (local.get $sq) (i32.const 1)) (then
    (local.set $sub (i32.load offset=8 (i32.and (local.get $sq) (i32.const -2))))
    (local.set $sq (i32.load offset=4 (i32.and (local.get $sq) (i32.const -2))))))
  (if (i32.eqz (local.get $sub)) (then
    (if (call $sub_cached (local.get $sq)) (then
      (return (call $sub_join (i32.load offset=92 (local.get $sq)) (local.get $at) (i32.const 0)))))))
  (i32.store offset=4 (global.get $cx_acc) (local.tee $n (i32.sub (i32.load offset=4 (global.get $cx_acc)) (i32.const 1))))
  (local.set $sp (global.get $params))
  (local.set $sl (global.get $lat))
  (if (local.get $sub) (then (global.set $params (local.get $sub)) (global.set $lat (local.get $sub))))
  (local.set $t (call $solve_tail (local.get $sq) (call $v_at (global.get $cx_acc) (local.get $n))))
  (global.set $params (local.get $sp))
  (global.set $lat (local.get $sl))
  (local.set $mapped (call $t_new))
  (loop $rows
    (if (i32.lt_u (local.get $i) (i32.load offset=4 (local.get $t))) (then
      (local.set $src (call $t_row (local.get $t) (local.get $i)))
      (memory.copy (local.tee $row (call $t_add (local.get $mapped))) (global.get $params) (global.get $W))
      (local.set $m (i32.load offset=72 (local.get $sq)))
      (loop $map
        (if (local.get $m) (then
          (if (i32.eqz (i32.load (i32.add (local.get $row) (i32.shl (i32.load offset=8 (local.get $m)) (i32.const 2))))) (then
            (i32.store (i32.add (local.get $row) (i32.shl (i32.load offset=8 (local.get $m)) (i32.const 2)))
                       (i32.load (i32.add (local.get $src) (i32.shl (i32.load offset=4 (local.get $m)) (i32.const 2)))))))
          (local.set $m (i32.load offset=28 (local.get $m)))
          (br $map))))
      (local.set $i (i32.add (local.get $i) (i32.const 1)))
      (br $rows))))
  (call $t_free (local.get $t))
  (if (local.get $sub) (then (return (call $sub_join (local.get $mapped) (local.get $at) (i32.const 1)))))
  ;; kept for the run, as $subquery keeps it
  (if (i32.eq (i32.load offset=88 (local.get $sq)) (global.get $epoch)) (then (call $cache_drop (i32.load offset=92 (local.get $sq)))))
  (i32.store offset=88 (local.get $sq) (global.get $epoch))
  (i32.store offset=92 (local.get $sq) (local.get $mapped))
  (i32.store offset=96 (local.get $sq) (global.get $ag))
  (call $cache_keep (local.get $mapped))
  (call $sub_join (local.get $mapped) (local.get $at) (i32.const 0)))

(func $sub_cached (param $sq i32) (result i32)
  (i32.and (i32.eq (i32.load offset=88 (local.get $sq)) (global.get $epoch)) (i32.eq (i32.load offset=96 (local.get $sq)) (global.get $ag))))

(func (export "cq_cached") (param $sq i32) (result i32) (call $sub_cached (local.get $sq)))

;; A subquery's (mapped) rows joined with the frame: n rows from at (free: the
;; table is the caller's to free).
(func $sub_join (param $mapped i32) (param $at i32) (param $free i32) (result i32)
  (local $one i32) (local $out i32) (local $n i32) (local $words i32)
  (local.set $one (call $t_one (global.get $cx_frame)))
  (call $join_tables (local.get $one) (local.get $mapped) (local.tee $out (call $t_new)))
  (call $t_free (local.get $one))
  (if (local.get $free) (then (call $t_free (local.get $mapped))))
  (local.set $n (i32.load offset=4 (local.get $out)))
  (local.set $words (i32.mul (local.get $n) (i32.shr_u (global.get $W) (i32.const 2))))
  (global.set $cs_n (local.get $at))
  (call $cs_room (i32.add (local.get $at) (local.get $words)))
  (memory.copy (i32.add (global.get $cs) (i32.shl (local.get $at) (i32.const 2))) (i32.load (local.get $out)) (i32.shl (local.get $words) (i32.const 2)))
  (global.set $cs_n (i32.add (local.get $at) (local.get $words)))
  (call $t_free (local.get $out))
  (local.get $n))

(func (export "cq_test") (param $k i32) (result i32)
  (local $x i32)
  (local.set $k (call $cx_k (local.get $k)))
  (local.set $x (call $ebv (call $ev (local.get $k) (global.get $cx_frame))))
  (select (i32.const 2) (local.get $x) (i32.lt_s (local.get $x) (i32.const 0))))

;; k | 1: a SELECT expression of the solution the last call evaluated one of
;; (the interpreter's BNODE(s) nodes are per row).
(func (export "cq_value") (param $k i32) (result i32)
  (call $ev (call $cx_k (local.get $k)) (global.get $cx_frame)))

(func $cx_k (param $k i32) (result i32)
  (if (i32.eqz (i32.and (local.get $k) (i32.const 1))) (then (global.set $bn_row (i32.const -1))))
  (i32.and (local.get $k) (i32.const -2)))

(func (export "cq_solution")
  (local $np i32) (local $proj i32) (local $j i32) (local $key i32)
  (if (i32.eq (i32.load (global.get $q)) (i32.const 1)) (then
    (local.set $np (i32.load offset=100 (global.get $q)))
    (local.set $proj (i32.load offset=104 (global.get $q)))
    (local.set $key (call $row_get))
    (i32.store (local.get $key) (i32.const 0))
    (loop $cols
      (if (i32.lt_u (local.get $j) (local.get $np)) (then
        (i32.store (i32.add (local.get $key) (i32.shl (local.get $j) (i32.const 2)))
          (i32.load (call $addr (i32.load (i32.add (local.get $proj) (i32.shl (local.get $j) (i32.const 2)))))))
        (local.set $j (i32.add (local.get $j) (i32.const 1)))
        (br $cols))))
    (if (global.get $cx_dset) (then
      (drop (call $set_add (global.get $cx_dset) (local.get $key)))
      (if (i32.eqz (global.get $set_new)) (then (call $row_put (local.get $key)) (return)))))
    (local.set $j (i32.const 0))
    (loop $out
      (if (i32.lt_u (local.get $j) (local.get $np)) (then
        (call $v_push1 (global.get $out) (i32.load (i32.add (local.get $key) (i32.shl (local.get $j) (i32.const 2)))))
        (local.set $j (i32.add (local.get $j) (i32.const 1)))
        (br $out))))
    (call $row_put (local.get $key))
    (global.set $out_rows (i32.add (global.get $out_rows) (i32.const 1)))))
  (global.set $cx_count (i32.add (global.get $cx_count) (i32.const 1))))

(func (export "cq_triple") (param $s i32) (param $p i32) (param $o i32)
  (if (i32.and (i32.and (i32.le_u (i32.sub (call $kind (local.get $s)) (i32.const 1)) (i32.const 1))
                        (i32.eq (call $kind (local.get $p)) (i32.const 1)))
               (i32.ne (local.get $o) (i32.const 0)))
    (then (call $emit_triple (local.get $s) (local.get $p) (local.get $o)))))

(func (export "cq_end") (result i32)
  (local $n i32)
  (if (global.get $cx_dset) (then (call $set_free (global.get $cx_dset)) (global.set $cx_dset (i32.const 0))))
  (call $free (global.get $cs) (i32.shl (global.get $cs_cap) (i32.const 2)))
  (global.set $cs (i32.const 0))
  (global.set $cs_cap (i32.const 0))
  (local.set $n (call $run_end (global.get $q) (i32.ne (global.get $cx_count) (i32.const 0))))
  (global.set $bu_active (i32.const 0))
  (if (global.get $bu_stop) (then
    (drop (call $fail (@str "interrupted")))
    (return (i32.const -2))))
  (local.get $n))

;; --- LATERAL in line ---------------------------------------------------------------
;; The interpreter evaluates LATERAL's group once per row with the row substituted
;; ($lat): that is the group's elements in line, seeing the whole frame. Elements
;; inside it left to the interpreter must see that row as $lat: cq_eval gets a
;; record (tag 1) naming the element and the LATERAL's copy of the frame.

;; A query-lifetime record (released with the query, listed at +236): +0 next,
;; +4 an element, +8 a LATERAL's row, +12 room for a row.
(func $cx_site (result i32)
  (local $r i32)
  (local.set $r (call $alloc0 (i32.add (i32.const 12) (call $width))))
  (i32.store (local.get $r) (i32.load offset=236 (global.get $cx_q)))
  (i32.store offset=236 (global.get $cx_q) (local.get $r))
  (local.get $r))

;; cq_eval's key for element el: el itself, or inside a LATERAL a record (tag 1).
(func $eval_key (param $el i32) (result i32)
  (local $r i32)
  (if (i32.eqz (global.get $cx_lat)) (then (return (local.get $el))))
  (i32.store offset=4 (local.tee $r (call $cx_site)) (local.get $el))
  (i32.store offset=8 (local.get $r) (global.get $cx_lat))
  (i32.or (local.get $r) (i32.const 1)))

(func $lateral (param $f i32) (param $el i32) (param $rest i32) (param $st i32)
  (local $row i32) (local $bits i32) (local $s i32) (local $g i32) (local $sl i32) (local $sb i32)
  (local.set $row (i32.add (call $cx_site) (i32.const 12)))
  ;; the copy of the frame: memory.copy(row, frame, W)
  (call $em (local.get $f) (@str "kakbkc") (local.get $row) (global.get $cx_frame) (call $width) (i32.const 0))
  (call $bput (local.get $f) (i32.const 0xfc)) (call $bput (local.get $f) (i32.const 10))
  (call $bput (local.get $f) (i32.const 0)) (call $bput (local.get $f) (i32.const 0))
  ;; the slots certainly bound on entry; after them, those maybe bound
  (local.set $bits (call $anew (i32.shl (global.get $cx_bw) (i32.const 1))))
  (loop $slots
    (if (i32.lt_u (local.get $s) (global.get $cx_nv)) (then
      (if (i32.eq (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const CERTAIN)) (then (call $setbit (local.get $bits) (local.get $s))))
      (if (i32.eq (i32.load8_u (call $st_at (local.get $st) (local.get $s))) (i32.const MAYBE)) (then (call $setbit (i32.add (local.get $bits) (global.get $cx_bw)) (local.get $s))))
      (local.set $s (i32.add (local.get $s) (i32.const 1)))
      (br $slots))))
  (local.set $g (i32.load offset=4 (local.get $el)))
  (local.set $sl (global.get $cx_lat))
  (local.set $sb (global.get $cx_latbits))
  (global.set $cx_lat (local.get $row))
  (global.set $cx_latbits (local.get $bits))
  (call $cx_emit (local.get $f)
    (call $grp (i32.load offset=4 (local.get $g)) (i32.load offset=8 (local.get $g))
      (call $cstep (i32.const S_LATEND) (local.get $sl) (local.get $sb) (local.get $rest)))
    (local.get $st))
  (global.set $cx_lat (local.get $sl))
  (global.set $cx_latbits (local.get $sb)))
