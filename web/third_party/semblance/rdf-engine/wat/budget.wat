;; The query budget (ABI.md, "Query budget"): a deadline on env.now and a
;; number of evaluation steps. A step is one row from a scan, one node of a
;; path walk or one pair a join tries. Once either runs out, $bu_stop is set,
;; the evaluator's loops end early, and "run" discards what they produced.
;; Only steps between $bu_start and the end of a run count: updates are not
;; budgeted.

(global $bu_deadline (mut f64) (f64.const 0))  ;; ms on env.now; 0: none
(global $bu_fuel (mut i32) (i32.const 0))      ;; steps (unsigned); 0: unlimited
(global $bu_left (mut i64) (i64.const -1))     ;; steps left in this run (unsigned)
(global $bu_ticks (mut i32) (i32.const 0))     ;; steps taken; the clock is read every 4096
(global $bu_stop (mut i32) (i32.const 0))
(global $bu_active (mut i32) (i32.const 0))

(func (export "set_deadline") (param $at f64) (global.set $bu_deadline (local.get $at)))
(func (export "set_fuel") (param $steps i32) (global.set $bu_fuel (local.get $steps)))

;; Steps are counted down to the next event, the first of: the fuel running
;; out, or a multiple of 4096 steps (a look at the clock). $step then does one
;; decrement; $bu_event does the rest.
;; Steps to the next event: the word at $bases + 60 (compiled modules count it
;; down in line, calling cq_bu_event when it reaches 0).
(global $bu_chunk (mut i32) (i32.const 0))     ;; steps from the last event to the next

;; At the start of a run: the whole budget again.
(func $bu_start
  (global.set $bu_left (select (i64.const -1) (i64.extend_i32_u (global.get $bu_fuel)) (i32.eqz (global.get $bu_fuel))))
  (global.set $bu_ticks (i32.const 0))
  (global.set $bu_stop (i32.const 0))
  (global.set $bu_active (i32.const 1))
  (call $bu_next))

(func $bu_next
  (local $n i32)
  (local.set $n (i32.sub (i32.const 4096) (i32.and (global.get $bu_ticks) (i32.const 4095))))
  (if (i64.lt_u (global.get $bu_left) (i64.extend_i32_u (local.get $n))) (then (local.set $n (i32.wrap_i64 (global.get $bu_left)))))
  (global.set $bu_chunk (local.get $n))
  (i32.store offset=60 (global.get $bases) (local.get $n)))

;; One step; whether the budget is spent.
(func $step (result i32)
  (local $cd i32)
  (if (i32.eqz (global.get $bu_active)) (then (return (i32.const 0))))
  (i32.store offset=60 (global.get $bases) (local.tee $cd (i32.sub (i32.load offset=60 (global.get $bases)) (i32.const 1))))
  (if (result i32) (local.get $cd) (then (i32.const 0)) (else (call $bu_event))))

;; The step that ends a chunk: the fuel used, the clock read on every 4096th step.
(func $bu_event (export "cq_bu_event") (result i32)
  (if (i32.eqz (global.get $bu_stop)) (then
    (global.set $bu_ticks (i32.add (global.get $bu_ticks) (global.get $bu_chunk)))
    (global.set $bu_left (i64.sub (global.get $bu_left) (i64.extend_i32_u (global.get $bu_chunk))))
    (if (i64.eqz (global.get $bu_left))
      (then (global.set $bu_stop (i32.const 1)))
      (else
        (if (i32.and (i32.eqz (i32.and (global.get $bu_ticks) (i32.const 4095))) (f64.gt (global.get $bu_deadline) (f64.const 0))) (then
          (global.set $bu_stop (f64.ge (call $now_ms) (global.get $bu_deadline)))))))
    (if (i32.eqz (global.get $bu_stop)) (then (call $bu_next) (return (i32.const 0))))))
  ;; spent: every later step ends here too
  (i32.store offset=60 (global.get $bases) (i32.const 1))
  (i32.const 1))
