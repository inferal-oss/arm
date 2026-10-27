;; The ABI (ABI.md): start-up and every export, as thin wrappers.

(start $init)
(func $init
  (global.set $top (global.get $HEAP))
  (global.set $errbuf (call $alloc (i32.const 160)))
  (call $init_load)
  (call $init_knowns)
  (call $init_functions)
  (global.set $nbuf (call $alloc (i32.const 400)))
  (call $init_terms)
  (call $init_store))

(func (export "abi_version") (result i32) (i32.const 1))

(func $ni (result i32) (call $fail (@str "not implemented")))

;; --- terms ---------------------------------------------------------------------

(func (export "intern_iri") (param $n i32) (result i32)
  (call $intern (i32.const 1) (global.get $in) (local.get $n) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))
(func (export "lookup_iri") (param $n i32) (result i32)
  (call $intern (i32.const 1) (global.get $in) (local.get $n) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0)))
(func (export "intern_bnode") (param $n i32) (result i32)
  (call $intern (i32.const 2) (global.get $in) (local.get $n) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 1)))
(func (export "lookup_bnode") (param $n i32) (result i32)
  (call $intern (i32.const 2) (global.get $in) (local.get $n) (i32.const 0) (i32.const 0) (i32.const 0) (i32.const 0)))
(func (export "fresh_bnode") (result i32) (call $fresh_bnode))

;; An error when interning (create = 1), 0 ("not found") when looking up.
(func $bad_arg (param $create i32) (result i32)
  (if (result i32) (local.get $create)
    (then (call $fail (@str "invalid argument")))
    (else (i32.const 0))))

(func $x_literal (param $n i32) (param $dt i32) (param $create i32) (result i32)
  (if (i32.eqz (local.get $dt)) (then (local.set $dt (i32.const 2))))
  (if (i32.ne (call $kind (local.get $dt)) (i32.const 1)) (then (return (call $bad_arg (local.get $create)))))
  (call $intern (i32.const 3) (global.get $in) (local.get $n) (local.get $dt) (i32.const 0) (i32.const 0) (local.get $create)))
(func (export "intern_literal") (param $n i32) (param $dt i32) (result i32)
  (call $x_literal (local.get $n) (local.get $dt) (i32.const 1)))
(func (export "lookup_literal") (param $n i32) (param $dt i32) (result i32)
  (call $x_literal (local.get $n) (local.get $dt) (i32.const 0)))

(func $x_lang_literal (param $n i32) (param $ln i32) (param $dir i32) (param $create i32) (result i32)
  (local $tag i32)
  (if (i32.or (i32.gt_u (local.get $dir) (i32.const 2)) (i32.eqz (local.get $ln)))
    (then (return (call $bad_arg (local.get $create)))))
  (call $lower (local.tee $tag (i32.add (global.get $in) (local.get $n))) (local.get $ln))
  (call $intern (i32.or (i32.const 3) (i32.shl (local.get $dir) (i32.const 8))) (global.get $in) (local.get $n)
                (select (i32.const 4) (i32.const 3) (local.get $dir)) (local.get $tag) (local.get $ln) (local.get $create)))
(func (export "intern_lang_literal") (param $n i32) (param $ln i32) (param $dir i32) (result i32)
  (call $x_lang_literal (local.get $n) (local.get $ln) (local.get $dir) (i32.const 1)))
(func (export "lookup_lang_literal") (param $n i32) (param $ln i32) (param $dir i32) (result i32)
  (call $x_lang_literal (local.get $n) (local.get $ln) (local.get $dir) (i32.const 0)))

(func $x_triple (param $s i32) (param $p i32) (param $o i32) (param $create i32) (result i32)
  (if (i32.eqz (i32.and (i32.and (i32.ne (call $kind (local.get $s)) (i32.const 0)) (i32.ne (call $kind (local.get $p)) (i32.const 0)))
                        (i32.ne (call $kind (local.get $o)) (i32.const 0))))
    (then (return (call $bad_arg (local.get $create)))))
  (call $intern_triple (local.get $s) (local.get $p) (local.get $o) (local.get $create)))
(func (export "intern_triple") (param $s i32) (param $p i32) (param $o i32) (result i32)
  (call $x_triple (local.get $s) (local.get $p) (local.get $o) (i32.const 1)))
(func (export "lookup_triple") (param $s i32) (param $p i32) (param $o i32) (result i32)
  (call $x_triple (local.get $s) (local.get $p) (local.get $o) (i32.const 0)))

(func (export "term_kind") (param $id i32) (result i32) (call $kind (local.get $id)))

;; Field `off` of the record of id when id has kind lo..hi, else 0.
(func $field (param $id i32) (param $lo i32) (param $hi i32) (param $off i32) (result i32)
  (if (result i32) (call $in (call $kind (local.get $id)) (local.get $lo) (local.get $hi))
    (then (i32.load (i32.add (call $rec (local.get $id)) (local.get $off))))
    (else (i32.const 0))))

(func (export "term_value_ptr") (param $id i32) (result i32) (call $field (local.get $id) (i32.const 1) (i32.const 3) (i32.const 8)))
(func (export "term_value_len") (param $id i32) (result i32) (call $field (local.get $id) (i32.const 1) (i32.const 3) (i32.const 12)))
(func (export "term_datatype") (param $id i32) (result i32) (call $field (local.get $id) (i32.const 3) (i32.const 3) (i32.const 16)))
(func (export "term_lang_ptr") (param $id i32) (result i32)
  (i32.add (call $field (local.get $id) (i32.const 3) (i32.const 3) (i32.const 8))
           (call $field (local.get $id) (i32.const 3) (i32.const 3) (i32.const 12))))
(func (export "term_lang_len") (param $id i32) (result i32) (call $field (local.get $id) (i32.const 3) (i32.const 3) (i32.const 20)))
(func (export "term_direction") (param $id i32) (result i32)
  (i32.shr_u (call $field (local.get $id) (i32.const 3) (i32.const 3) (i32.const 0)) (i32.const 8)))
(func (export "triple_subject") (param $id i32) (result i32) (call $field (local.get $id) (i32.const 4) (i32.const 4) (i32.const 8)))
(func (export "triple_predicate") (param $id i32) (result i32) (call $field (local.get $id) (i32.const 4) (i32.const 4) (i32.const 12)))
(func (export "triple_object") (param $id i32) (result i32) (call $field (local.get $id) (i32.const 4) (i32.const 4) (i32.const 16)))
(func (export "term_count") (result i32) (global.get $live))

;; --- store ---------------------------------------------------------------------

(func $terms4 (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (i32.and (i32.and (i32.ne (call $kind (local.get $s)) (i32.const 0)) (i32.ne (call $kind (local.get $p)) (i32.const 0)))
           (i32.and (i32.ne (call $kind (local.get $o)) (i32.const 0)) (i32.ne (call $kind (local.get $g)) (i32.const 0)))))

(func (export "add") (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (if (result i32) (call $terms4 (local.get $s) (local.get $p) (local.get $o) (local.get $g))
    (then (call $add (local.get $s) (local.get $p) (local.get $o) (local.get $g)))
    (else (call $fail (@str "invalid argument")))))
(func (export "remove") (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (if (result i32) (call $terms4 (local.get $s) (local.get $p) (local.get $o) (local.get $g))
    (then (call $remove (local.get $s) (local.get $p) (local.get $o) (local.get $g)))
    (else (i32.const 0))))
(func (export "has") (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (if (result i32) (call $terms4 (local.get $s) (local.get $p) (local.get $o) (local.get $g))
    (then (i32.ne (i32.load (call $qfind (local.get $s) (local.get $p) (local.get $o) (local.get $g))) (i32.const 0)))
    (else (i32.const 0))))
(func (export "match") (param $s i32) (param $p i32) (param $o i32) (param $g i32) (result i32)
  (call $match (local.get $s) (local.get $p) (local.get $o) (local.get $g)))

(func (export "apply") (param $removes i32) (param $adds i32) (result i32)
  (local $q i32) (local $mid i32) (local $end i32)
  (local.set $q (global.get $in))
  (local.set $mid (i32.add (local.get $q) (i32.shl (local.get $removes) (i32.const 4))))
  (local.set $end (i32.add (local.get $mid) (i32.shl (local.get $adds) (i32.const 4))))
  ;; every id first, so a bad batch changes nothing
  (loop $check
    (if (i32.lt_u (local.get $q) (local.get $end)) (then
      (if (i32.eqz (call $kind (i32.load (local.get $q)))) (then (return (call $fail (@str "invalid argument")))))
      (local.set $q (i32.add (local.get $q) (i32.const 4)))
      (br $check))))
  (call $quads_reserve (i32.add (global.get $qn) (local.get $adds)))
  (local.set $q (global.get $in))
  (loop $each
    (if (i32.lt_u (local.get $q) (local.get $end)) (then
      (if (i32.lt_u (local.get $q) (local.get $mid))
        (then (drop (call $remove (i32.load (local.get $q)) (i32.load offset=4 (local.get $q))
                                  (i32.load offset=8 (local.get $q)) (i32.load offset=12 (local.get $q)))))
        (else (drop (call $add (i32.load (local.get $q)) (i32.load offset=4 (local.get $q))
                               (i32.load offset=8 (local.get $q)) (i32.load offset=12 (local.get $q))))))
      (local.set $q (i32.add (local.get $q) (i32.const 16)))
      (br $each))))
  (i32.const 0))

(func (export "quad_count") (result i32) (global.get $qn))

(func (export "load") (param $fmt i32) (param $len i32) (param $base_len i32) (param $graph i32) (result i32)
  (if (i32.gt_u (i32.sub (local.get $fmt) (i32.const 1)) (i32.const 3)) (then (return (call $fail (@str "unknown format")))))
  (call $load (local.get $fmt) (local.get $len) (local.get $base_len) (local.get $graph)))


;; --- queries (later phases) -------------------------------------------------------

