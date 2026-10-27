//! Compiling a prepared query into a WebAssembly module (ABI.md, "Optional:
//! compiled queries"). Variables live in a row frame in the engine's memory at
//! a fixed address; a basic graph pattern becomes nested loops over index
//! scans, in an order chosen here from which variables are bound; OPTIONAL,
//! UNION, VALUES, LATERAL, FILTER and BIND are compiled around them, and the
//! cheap expressions over term ids (BOUND, isIRI, sameTerm, = with an IRI,
//! lang(x) = "", COALESCE, IF, ...) are inlined. The rest calls back into the
//! engine: other expressions (`cq_test`, `cq_value`), property paths
//! (`cq_path`), and patterns this compiler leaves to the interpreter (`cq_eval`:
//! GRAPH, MINUS, subqueries, triple terms, and the right side of a join that
//! cannot see the left side's row), whose rows are joined in.
//!
//! The code is generated in continuation-passing style: each step emits the
//! rest of the plan inside its innermost loop; a rest reached from two places
//! (after OPTIONAL, UNION, VALUES) becomes a function of its own.

use super::eval::{pure, side_safe};
use super::*;
use crate::dict::{self, Dict};
use crate::wasm::{self, *};
use alloc::vec;

// Function types: (params, results), all i32.
const T_I_V: u32 = 0;
const T_I_I: u32 = 1;
const T_III_I: u32 = 2;
const T_IIII_I: u32 = 3;
const T_V_I: u32 = 4;
const T_V_V: u32 = 5;
const T_III_V: u32 = 6;
const T_II_I: u32 = 7;
const T_IIIII_I: u32 = 8;
const TYPES: [(u32, u32); 9] = [(1, 0), (1, 1), (3, 1), (4, 1), (0, 1), (0, 0), (3, 0), (2, 1), (5, 1)];

// Imports, in this order (their function indices).
const BEGIN: u32 = 0;
const COMBO: u32 = 1;
const SCAN: u32 = 2;
const PATH: u32 = 3;
const EVAL: u32 = 4;
const STACK: u32 = 5;
const TEST: u32 = 6;
const VALUE: u32 = 7;
const SOLUTION: u32 = 8;
const TRIPLE: u32 = 9;
const END_RUN: u32 = 10;
const FRESH: u32 = 11;
const KIND: u32 = 12;
const DATATYPE: u32 = 13;
const LANG_LEN: u32 = 14;
const SUB: u32 = 15;
const KEEP: u32 = 16;
const GROUP: u32 = 17;
const BU_EVENT: u32 = 18;
// typed expressions (ctyped.rs)
const LEX: u32 = 19;
const NUM: u32 = 20;
const NUMTERM: u32 = 21;
const STRTERM: u32 = 22;
const IRI_OF: u32 = 23;
const STRFN: u32 = 24;
const CPLEN: u32 = 25;
const SUBSTR: u32 = 26;
const TONUM: u32 = 27;
const EBV: u32 = 28;
const IMPORTS: [(&[u8], u32); 29] = [
    (b"cq_begin", T_III_I),
    (b"cq_combo", T_I_V),
    (b"cq_scan", T_IIII_I),
    (b"cq_path", T_IIII_I),
    (b"cq_eval", T_II_I),
    (b"cq_stack", T_V_I),
    (b"cq_test", T_I_I),
    (b"cq_value", T_I_I),
    (b"cq_solution", T_V_V),
    (b"cq_triple", T_III_V),
    (b"cq_end", T_V_I),
    (b"fresh_bnode", T_V_I),
    (b"term_kind", T_I_I),
    (b"term_datatype", T_I_I),
    (b"term_lang_len", T_I_I),
    (b"cq_sub", T_V_V),
    (b"cq_keep", T_V_V),
    (b"cq_group", T_II_I),
    (b"cq_bu_event", T_V_I),
    (b"cq_lex", T_III_I),
    (b"cq_num", T_I_I),
    (b"cq_numterm", T_I_I),
    (b"cq_strterm", T_II_I),
    (b"cq_iri", T_II_I),
    (b"cq_strfn", T_IIIII_I),
    (b"cq_cplen", T_II_I),
    (b"cq_substr", T_III_I),
    (b"cq_tonum", T_IIII_I),
    (b"cq_ebv", T_I_I),
];
const NIMPORTS: u32 = IMPORTS.len() as u32;

const LT_S: u8 = 0x48;
const XOR: u8 = 0x73;
const RETURN: u8 = 0x0f;
const I32: u8 = 0x7f;

/// What a compiled module refers to by index, kept by the engine for its calls.
#[derive(Default)]
pub struct Tables {
    /// For `cq_test` / `cq_value`.
    pub exprs: Vec<*const Expr>,
    /// For `cq_path`.
    pub paths: Vec<*const Triple>,
    /// For `cq_eval`.
    pub patterns: Vec<*const Pattern>,
    /// For `cq_group`: subqueries whose WHERE runs in the module.
    pub subs: Vec<*const Select>,
    /// Constant strings typed expressions read (ctyped.rs), at fixed addresses.
    pub consts: Vec<alloc::boxed::Box<[u8]>>,
}

pub type Res<T> = Result<T, &'static str>;

/// Debugging switches (`cq_debug`), for bisecting a difference: compile without
/// (1) OPTIONAL right sides computed in the module, (2) join right sides
/// computed in the module, (4) filters pushed down, (8) EXISTS as functions,
/// (16) subqueries in the module, (32) typed expressions, (64) right sides in
/// line with the left row's bindings (masked).
pub static DEBUG: core::sync::atomic::AtomicU32 = core::sync::atomic::AtomicU32::new(0);
fn off(bit: u32) -> bool {
    DEBUG.load(core::sync::atomic::Ordering::Relaxed) & bit != 0
}

/// What is known about a variable at a point of the plan (0: unbound).
const MAYBE: u8 = 1;
const CERTAIN: u8 = 2;

#[derive(Clone, Copy)]
enum Step<'a> {
    Pat(&'a Pattern),
    Triple(&'a Triple),
    Filter(&'a Expr),
    Extend(Var, &'a Expr),
    /// A SELECT expression after another: BNODE(str) as in the one before.
    Project(Var, &'a Expr),
    /// OPTIONAL over a pattern that may see the row (pure).
    Optional(&'a Pattern, Option<&'a Expr>),
    /// A pattern's rows, from the interpreter now, joined in.
    Eval(&'a Pattern),
    /// A pattern's rows computed earlier (by `cq_eval`; where: the globals g, g + 1), joined in.
    Rows(u32, &'a Pattern),
    /// The same, as OPTIONAL with its condition.
    OptRows(u32, &'a Pattern, Option<&'a Expr>),
    Found(u32),
    Call(u32),
    Final,
    /// A solution of a subquery's WHERE: `cq_keep`.
    Keep,
    /// EXISTS found a solution: global g set, return.
    Exit(u32),
    /// A join's right side, in line, seeing the left row's bindings the
    /// interpreter would pass into it (its `sideways`): the others are hidden
    /// (mask i: saved, cleared) while it runs, and checked against its rows at
    /// `Unmask(i)`.
    Masked(&'a Pattern, u32),
    Unmask(u32),
    /// OPTIONAL over such a right side, with its condition.
    OptMasked(&'a Pattern, Option<&'a Expr>, u32),
}

/// A mask: per hidden variable, the global holding its value and what is
/// known of it after the left side.
type Mask = Vec<(Var, u32, u8)>;

#[derive(Clone)]
struct St {
    bound: Vec<u8>,
    /// The local holding the stack's top (in words).
    top: u32,
}

struct Gen<'q> {
    q: &'q Query,
    dict: &'q Dict,
    frame: u32,
    width: u32,
    funcs: Vec<Body>,
    t: Tables,
    base: [u32; 4],
    globals: u32,
    /// The plan state where the expression being compiled is evaluated.
    here: Option<St>,
    /// Typed expressions (ctyped.rs): blocks opened since the bail block, whether
    /// one is being compiled, and the global holding the string buffer's use.
    txd: u32,
    in_tx: bool,
    tx_used: Option<u32>,
    masks: Vec<Mask>,
    /// Expressions compiled now are SELECT expressions after another (bit 31 of their index).
    same_solution: bool,
}

#[path = "ctyped.rs"]
mod ctyped;

/// Compile `q` for runs with the variables `bound` bound (to any values). `base`:
/// where the module's expressions, paths and patterns start in the engine's tables.
pub fn compile(q: &Query, dict: &Dict, bound: &[Var], h: u32, shape: u32, frame: u32, base: [u32; 4]) -> Res<(Vec<u8>, Tables)> {
    let s = &q.select;
    if q.form == Form::Describe {
        return Err("not compilable: DESCRIBE");
    }
    if s.group.is_some() || !s.aggregates.is_empty() || !s.having.is_empty() {
        return Err("not compilable: aggregates");
    }
    if !s.order.is_empty() || s.offset != 0 || s.limit.is_some() {
        return Err("not compilable: ORDER BY, LIMIT or OFFSET");
    }
    if q.template.iter().flatten().any(|t| matches!(t, Tt::Triple(_))) {
        return Err("not compilable: triple term in template");
    }
    let mut g = Gen { q, dict, frame, width: q.vars.len() as u32, funcs: Vec::new(), t: Tables::default(), base, globals: 0, here: None, txd: 0, in_tx: false, tx_used: None, masks: Vec::new(), same_solution: false };
    // run(graphs) -> n
    let mut run = Body::new(T_I_I, 1);
    let (n, i, top) = (run.local(), run.local(), run.local());
    run.i32(h as i32).i32(shape as i32).get(0).call(BEGIN).tee(n).i32(0).op(LT_S).open(IF).get(n).op(RETURN).op(END);
    run.open(BLOCK).open(LOOP).get(i).get(n).op(GE_U).br(BR_IF, 1).get(i).call(COMBO);
    g.funcs.push(run);
    let mut steps = vec![Step::Pat(&s.pattern)];
    if let Some(v) = &s.values {
        steps.push(Step::Pat(v));
    }
    for (i, (v, e)) in s.extend.iter().enumerate() {
        steps.push(if i == 0 { Step::Extend(*v, e) } else { Step::Project(*v, e) });
    }
    steps.push(Step::Final);
    let mut st = St { bound: vec![0; q.vars.len()], top };
    for &v in bound {
        st.bound[v as usize] = CERTAIN;
    }
    g.emit(0, &steps, &st)?;
    g.funcs[0].get(i).i32(1).op(ADD).set(i).br(BR, 0).op(END).op(END).call(END_RUN);
    let bytes = wasm::module(&TYPES, b"engine", &IMPORTS, &g.funcs, g.globals, &[(b"run", NIMPORTS)]);
    Ok((bytes, g.t))
}

fn tv_vars(t: &Tv, out: &mut Vec<Var>) {
    match t {
        Tv::Var(v) => out.push(*v),
        Tv::Triple(b) => b.iter().for_each(|x| tv_vars(x, out)),
        Tv::Term(_) => {}
    }
}

fn vars_of(p: &Pattern) -> Vec<Var> {
    let mut vs = Vec::new();
    super::parse::pattern_vars(p, &mut vs);
    vs.sort_unstable();
    vs.dedup();
    vs
}

/// The variable state after `p` matched, from `b` before it.
fn after(p: &Pattern, b: &[u8]) -> Vec<u8> {
    let mut out = b.to_vec();
    match p {
        Pattern::Bgp(ts) => {
            let mut vs = Vec::new();
            for t in ts {
                [&t.s, &t.p, &t.o].iter().for_each(|x| tv_vars(x, &mut vs));
            }
            vs.iter().for_each(|&v| out[v as usize] = CERTAIN);
        }
        Pattern::Join(a, c) | Pattern::Lateral(a, c) => out = after(c, &after(a, b)),
        Pattern::LeftJoin(a, c, _) => out = optional(&after(a, b), c),
        Pattern::Filter(_, a) => out = after(a, b),
        Pattern::Extend(a, v, _) => {
            out = after(a, b);
            out[*v as usize] = out[*v as usize].max(MAYBE);
        }
        Pattern::Union(a, c) => {
            let (x, y) = (after(a, b), after(c, b));
            out = x.iter().zip(&y).map(|(&x, &y)| if x == CERTAIN && y == CERTAIN { CERTAIN } else if x != 0 || y != 0 { MAYBE } else { 0 }).collect();
        }
        Pattern::Values(vars, rows) => {
            for (i, v) in vars.iter().enumerate() {
                let all = rows.iter().all(|r| r[i] != 0);
                let slot = &mut out[*v as usize];
                *slot = (*slot).max(if all { CERTAIN } else { MAYBE });
            }
        }
        _ => vars_of(p).iter().for_each(|&v| out[v as usize] = out[v as usize].max(MAYBE)),
    }
    out
}

/// The state after OPTIONAL { c } from `x`.
fn optional(x: &[u8], c: &Pattern) -> Vec<u8> {
    let y = after(c, x);
    x.iter().zip(&y).map(|(&x, &y)| if x == CERTAIN { CERTAIN } else if y != 0 { MAYBE } else { x }).collect()
}

/// Whether the compiler emits a pattern itself (else the interpreter evaluates it).
fn native(p: &Pattern) -> bool {
    match p {
        Pattern::Bgp(ts) => ts.iter().all(|t| ![&t.s, &t.p, &t.o].iter().any(|x| matches!(x, Tv::Triple(_)))),
        Pattern::Minus(..) | Pattern::Graph(..) | Pattern::Sub(_) | Pattern::Service(_) => false,
        _ => true,
    }
}

/// The conjuncts of a filter, as filter steps (a row passes all of them iff it passes their AND).
fn conjuncts<'q>(e: &'q Expr, out: &mut Vec<Step<'q>>) {
    match e {
        Expr::And(a, b) => {
            conjuncts(a, out);
            conjuncts(b, out);
        }
        _ => out.push(Step::Filter(e)),
    }
}

/// Every variable an expression reads, EXISTS patterns included; false if it is impure.
fn expr_reads(e: &Expr, out: &mut Vec<Var>) -> bool {
    match e {
        Expr::Var(v) | Expr::Bound(v) => {
            out.push(*v);
            true
        }
        Expr::Const(_) | Expr::Unknown => true,
        Expr::Exists(p, _) => pattern_reads(p, out),
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => expr_reads(a, out) & expr_reads(b, out),
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => expr_reads(a, out),
        Expr::In(a, l, _) => expr_reads(a, out) & l.iter().fold(true, |ok, x| expr_reads(x, out) & ok),
        Expr::Call(func, l) => !matches!(func, Func::Bnode | Func::Rand | Func::Uuid | Func::StrUuid) & l.iter().fold(true, |ok, x| expr_reads(x, out) & ok),
        Expr::Coalesce(l) => l.iter().fold(true, |ok, x| expr_reads(x, out) & ok),
        Expr::If(a, b, c) => expr_reads(a, out) & expr_reads(b, out) & expr_reads(c, out),
    }
}

/// Every variable a pattern mentions, its expressions' included; false if one is impure.
fn pattern_reads(p: &Pattern, out: &mut Vec<Var>) -> bool {
    match p {
        Pattern::Unit | Pattern::Service(_) => true,
        Pattern::Bgp(ts) => {
            ts.iter().for_each(|t| [&t.s, &t.p, &t.o].iter().for_each(|x| tv_vars(x, out)));
            true
        }
        Pattern::Join(a, b) | Pattern::Union(a, b) | Pattern::Lateral(a, b) | Pattern::Minus(a, b) => pattern_reads(a, out) & pattern_reads(b, out),
        Pattern::LeftJoin(a, b, c) => pattern_reads(a, out) & pattern_reads(b, out) & c.as_ref().is_none_or(|c| expr_reads(c, out)),
        Pattern::Filter(e, a) => expr_reads(e, out) & pattern_reads(a, out),
        Pattern::Extend(a, v, e) => {
            out.push(*v);
            pattern_reads(a, out) & expr_reads(e, out)
        }
        Pattern::Graph(g, a) => {
            tv_vars(g, out);
            pattern_reads(a, out)
        }
        Pattern::Values(vs, _) => {
            out.extend_from_slice(vs);
            true
        }
        Pattern::Sub(s) => {
            out.extend_from_slice(&s.project);
            pattern_reads(&s.pattern, out)
        }
    }
}

fn has_exists(e: &Expr) -> bool {
    match e {
        Expr::Exists(..) => true,
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => has_exists(a) || has_exists(b),
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => has_exists(a),
        Expr::In(a, l, _) => has_exists(a) || l.iter().any(has_exists),
        Expr::Call(_, l) | Expr::Coalesce(l) => l.iter().any(has_exists),
        Expr::If(a, b, c) => has_exists(a) || has_exists(b) || has_exists(c),
        _ => false,
    }
}

/// Variables a step may bind.
fn step_binds(s: &Step, out: &mut Vec<Var>) {
    match s {
        Step::Pat(p) | Step::Eval(p) | Step::Rows(_, p) | Step::OptRows(_, p, _) | Step::Optional(p, _) | Step::Masked(p, _) | Step::OptMasked(p, _, _) => super::parse::pattern_vars(p, out),
        Step::Triple(t) => [&t.s, &t.p, &t.o].iter().for_each(|x| tv_vars(x, out)),
        Step::Extend(v, _) | Step::Project(v, _) => out.push(*v),
        _ => {}
    }
}

/// The first filter after the first step that is ready now: each variable it
/// reads is certainly bound, or bound by none of the steps before it. One
/// with EXISTS does not pass a triple pattern (which may well be cheaper).
fn ready_filter(steps: &[Step], bound: &[u8]) -> Option<usize> {
    for j in 1..steps.len() {
        let Step::Filter(e) = steps[j] else { continue };
        let mut reads = Vec::new();
        if !expr_reads(e, &mut reads) {
            continue;
        }
        if has_exists(e) && steps[..j].iter().any(|s| matches!(s, Step::Triple(_) | Step::Pat(Pattern::Bgp(_)))) {
            continue;
        }
        let mut before = Vec::new();
        steps[..j].iter().for_each(|s| step_binds(s, &mut before));
        if reads.iter().all(|&v| bound[v as usize] == CERTAIN || !before.contains(&v)) {
            return Some(j);
        }
    }
    None
}

/// An EXISTS pattern the compiler runs itself: triples and filters.
fn exists_inline(p: &Pattern) -> bool {
    match p {
        Pattern::Unit => true,
        Pattern::Bgp(ts) => ts.iter().all(|t| ![&t.s, &t.p, &t.o].iter().any(|x| matches!(x, Tv::Triple(_)))),
        Pattern::Join(a, b) => exists_inline(a) && exists_inline(b),
        Pattern::Filter(e, a) => exists_inline(a) && !has_exists(e),
        _ => false,
    }
}

/// An expression whose value is a term id already: a variable or a constant.
fn simple(e: &Expr) -> bool {
    matches!(e, Expr::Var(_) | Expr::Const(_))
}

impl<'q> Gen<'q> {
    fn f(&mut self, f: usize) -> &mut Body {
        &mut self.funcs[f]
    }

    fn load_var(&mut self, f: usize, v: Var) {
        let a = self.frame + 4 * v;
        self.f(f).i32(0).load(a);
    }

    /// A budget step at a loop's head (right after its exit test, in its block
    /// and loop): the engine's countdown (budget::cd_addr) less one; at 0,
    /// cq_bu_event says whether the budget is spent, and if so the loop ends.
    /// Once spent, every step ends its loop, so the run unwinds to cq_end,
    /// which returns -2.
    fn step(&mut self, f: usize) {
        let cd = crate::budget::cd_addr();
        let b = self.f(f);
        let t = b.local();
        b.i32(0).i32(0).load(cd).i32(-1).op(ADD).tee(t).store(cd);
        b.get(t).op(EQZ).open(IF).call(BU_EVENT).br(BR_IF, 2).op(END);
    }

    /// Store the local `l` into variable v.
    fn store_var(&mut self, f: usize, v: Var, l: u32) {
        let a = self.frame + 4 * v;
        self.f(f).i32(0).get(l).store(a);
    }

    fn store_const(&mut self, f: usize, v: Var, c: u32) {
        let a = self.frame + 4 * v;
        self.f(f).i32(0).i32(c as i32).store(a);
    }

    fn global(&mut self) -> u32 {
        self.globals += 1;
        self.globals - 1
    }

    /// Push a pattern position's value (0 when an unbound variable).
    fn push_tv(&mut self, f: usize, t: &Tv) {
        match t {
            Tv::Term(c) => {
                self.f(f).i32(*c as i32);
            }
            Tv::Var(v) => self.load_var(f, *v),
            Tv::Triple(_) => unreachable!(),
        }
    }

    /// The rest of a plan as a function of its own (taking the stack top).
    fn cont(&mut self, rest: &[Step<'q>], bound: Vec<u8>) -> Res<u32> {
        if let [Step::Call(k)] = rest {
            return Ok(*k);
        }
        let idx = self.funcs.len();
        self.funcs.push(Body::new(T_I_V, 1));
        self.emit(idx, rest, &St { bound, top: 0 })?;
        Ok(NIMPORTS + idx as u32)
    }

    fn expr_index(&mut self, e: &Expr) -> u32 {
        self.t.exprs.push(e);
        (self.base[0] + self.t.exprs.len() as u32 - 1) | if self.same_solution { 0x8000_0000 } else { 0 }
    }

    fn emit(&mut self, f: usize, steps: &[Step<'q>], st: &St) -> Res<()> {
        // A filter further on whose variables nothing before it can change:
        // tested now (pushed down).
        if let Some(j) = ready_filter(steps, &st.bound).filter(|_| !off(4)) {
            let Step::Filter(e) = steps[j] else { unreachable!() };
            let mut rest = steps.to_vec();
            rest.remove(j);
            self.f(f).open(BLOCK);
            self.here = Some(st.clone());
            self.bool3(f, e);
            self.f(f).i32(1).op(NE).br(BR_IF, 0);
            self.emit(f, &rest, st)?;
            self.f(f).op(END);
            return Ok(());
        }
        let (first, rest) = (steps[0], &steps[1..]);
        let then = |head: &[Step<'q>]| -> Vec<Step<'q>> {
            let mut v = head.to_vec();
            v.extend_from_slice(rest);
            v
        };
        match first {
            Step::Pat(p) if !native(p) => self.emit(f, &then(&[Step::Eval(p)]), st),
            Step::Pat(p) => match p {
                Pattern::Unit => self.emit(f, rest, st),
                Pattern::Bgp(ts) => {
                    let order = self.order(ts, &st.bound);
                    let head: Vec<Step<'q>> = order.into_iter().map(Step::Triple).collect();
                    self.emit(f, &then(&head), st)
                }
                Pattern::Join(a, b) if pure(b) => self.emit(f, &then(&[Step::Pat(a), Step::Pat(b)]), st),
                // The left row's bindings make the right side cheap: it runs per row, as the interpreter's join_one.
                Pattern::Join(a, b) if self.hidden(a, b, st).is_some() => {
                    let m = self.mask(a, b, st);
                    self.emit(f, &then(&[Step::Pat(a), Step::Masked(b, m)]), st)
                }
                // Else the right side's rows, computed (as the interpreter does) before the left side runs.
                Pattern::Join(a, b) => {
                    let (g, top) = self.table(f, b, st)?;
                    let next = St { bound: st.bound.clone(), top };
                    self.emit(f, &then(&[Step::Pat(a), Step::Rows(g, b)]), &next)
                }
                Pattern::LeftJoin(a, b, c) if pure(b) => self.emit(f, &then(&[Step::Pat(a), Step::Optional(b, c.as_ref())]), st),
                Pattern::LeftJoin(a, b, c) if self.hidden(a, b, st).is_some() => {
                    let m = self.mask(a, b, st);
                    self.emit(f, &then(&[Step::Pat(a), Step::OptMasked(b, c.as_ref(), m)]), st)
                }
                Pattern::LeftJoin(a, b, c) => {
                    let (g, top) = self.table_as(f, b, st, 1)?;
                    let next = St { bound: st.bound.clone(), top };
                    self.emit(f, &then(&[Step::Pat(a), Step::OptRows(g, b, c.as_ref())]), &next)
                }
                // The right side sees each row of the left: exactly what a continuation sees.
                Pattern::Lateral(a, b) => self.emit(f, &then(&[Step::Pat(a), Step::Pat(b)]), st),
                Pattern::Filter(e, a) => {
                    let mut head = vec![Step::Pat(a)];
                    conjuncts(e, &mut head);
                    self.emit(f, &then(&head), st)
                }
                Pattern::Extend(a, v, e) => self.emit(f, &then(&[Step::Pat(a), Step::Extend(*v, e)]), st),
                Pattern::Union(a, b) => {
                    let k = self.cont(rest, after(p, &st.bound))?;
                    self.emit(f, &[Step::Pat(a), Step::Call(k)], st)?;
                    self.emit(f, &[Step::Pat(b), Step::Call(k)], st)
                }
                Pattern::Values(vars, rows) => self.values(f, vars, rows, after(p, &st.bound), rest, st),
                _ => unreachable!(),
            },
            Step::Triple(t) => self.triple(f, t, rest, st),
            Step::Filter(e) => {
                self.f(f).open(BLOCK);
                self.here = Some(st.clone());
                self.bool3(f, e);
                self.f(f).i32(1).op(NE).br(BR_IF, 0);
                self.emit(f, rest, st)?;
                self.f(f).op(END);
                Ok(())
            }
            Step::Extend(v, e) | Step::Project(v, e) => {
                let before = st.bound[v as usize];
                let (save, val) = (self.f(f).local(), self.f(f).local());
                if before != 0 {
                    self.load_var(f, v);
                    self.f(f).set(save);
                }
                self.here = Some(st.clone());
                self.same_solution = matches!(first, Step::Project(..));
                self.value(f, e);
                self.same_solution = false;
                self.f(f).tee(val).open(IF);
                self.store_var(f, v, val);
                self.f(f).op(END);
                let mut next = st.clone();
                next.bound[v as usize] = before.max(MAYBE);
                // A BIND onto a variable the row binds already (from the row it
                // is joined with): the row stays only if the values agree.
                let check = before != 0 && matches!(first, Step::Extend(..));
                self.f(f).open(BLOCK);
                if check {
                    self.f(f).get(val).i32(0).op(NE).get(save).i32(0).op(NE).op(AND).get(val).get(save).op(NE).op(AND).br(BR_IF, 0);
                }
                self.emit(f, rest, &next)?;
                self.f(f).op(END);
                // `save` is 0 when the variable was unbound before.
                self.store_var(f, v, save);
                Ok(())
            }
            Step::Optional(b, cond) | Step::OptMasked(b, cond, _) => {
                let k = self.cont(rest, optional(&st.bound, b))?;
                // A global: the inner pattern's matches may end in another function.
                let found = self.global();
                self.f(f).i32(0).global_set(found);
                let mut inner = vec![match first {
                    Step::OptMasked(_, _, m) => Step::Masked(b, m),
                    _ => Step::Pat(b),
                }];
                inner.extend(cond.map(Step::Filter));
                inner.extend([Step::Found(found), Step::Call(k)]);
                self.emit(f, &inner, st)?;
                let top = st.top;
                self.f(f).global_get(found).op(EQZ).open(IF).get(top).call(k).op(END);
                Ok(())
            }
            Step::Eval(p) => {
                let (g, top) = self.table(f, p, st)?;
                let next = St { bound: st.bound.clone(), top };
                self.emit(f, &then(&[Step::Rows(g, p)]), &next)
            }
            Step::Rows(g, p) => self.rows(f, g, p, rest, st),
            Step::OptRows(g, p, cond) => {
                let k = self.cont(rest, optional(&st.bound, p))?;
                let found = self.global();
                self.f(f).i32(0).global_set(found);
                let mut inner: Vec<Step<'q>> = cond.map(Step::Filter).into_iter().collect();
                inner.extend([Step::Found(found), Step::Call(k)]);
                self.rows(f, g, p, &inner, st)?;
                let top = st.top;
                self.f(f).global_get(found).op(EQZ).open(IF).get(top).call(k).op(END);
                Ok(())
            }
            Step::Found(g) => {
                self.f(f).i32(1).global_set(g);
                self.emit(f, rest, st)
            }
            Step::Call(k) => {
                let top = st.top;
                self.f(f).get(top).call(k);
                Ok(())
            }
            Step::Final => self.final_(f),
            Step::Keep => {
                self.f(f).call(KEEP);
                Ok(())
            }
            Step::Exit(g) => {
                self.f(f).i32(1).global_set(g).op(RETURN);
                Ok(())
            }
            Step::Masked(b, m) => {
                let mask = self.masks[m as usize].clone();
                let mut inner = st.clone();
                for &(v, g, _) in &mask {
                    self.load_var(f, v);
                    self.f(f).global_set(g);
                    self.store_const(f, v, 0);
                    inner.bound[v as usize] = 0;
                }
                self.emit(f, &then(&[Step::Pat(b), Step::Unmask(m)]), &inner)?;
                for &(v, g, _) in &mask {
                    let a = self.frame + 4 * v;
                    self.f(f).i32(0).global_get(g).store(a);
                }
                Ok(())
            }
            Step::Unmask(m) => {
                let mask = self.masks[m as usize].clone();
                let x = self.f(f).local();
                self.f(f).open(BLOCK);
                // a hidden binding the right side's row binds otherwise: not compatible
                for &(v, g, _) in &mask {
                    self.f(f).global_get(g).i32(0).op(NE);
                    self.load_var(f, v);
                    self.f(f).tee(x).i32(0).op(NE).op(AND).get(x).global_get(g).op(NE).op(AND).br(BR_IF, 0);
                }
                // merged: the hidden values back, what the right side had kept to restore after
                let mut next = st.clone();
                let mut keep = Vec::new();
                for &(v, g, known) in &mask {
                    let t = self.f(f).local();
                    let a = self.frame + 4 * v;
                    self.load_var(f, v);
                    self.f(f).set(t).i32(0).global_get(g).get(t).global_get(g).op(SELECT).store(a);
                    keep.push((v, t));
                    let b = &mut next.bound[v as usize];
                    *b = if known == CERTAIN { CERTAIN } else { (*b).max(known) };
                }
                self.emit(f, rest, &next)?;
                for (v, t) in keep {
                    self.store_var(f, v, t);
                }
                self.f(f).op(END);
                Ok(())
            }
        }
    }

    /// For a join of `a` and `b` (not pure) from state st: when the interpreter
    /// would pass some of the left row's certain bindings into `b`, the
    /// variables to hide while `b` runs in line after `a` (those it would not
    /// pass), with what is known of each after `a`.
    fn hidden(&self, a: &Pattern, b: &Pattern, st: &St) -> Option<Vec<(Var, u8)>> {
        if off(64) {
            return None;
        }
        let n = self.q.vars.len();
        let safe = side_safe(b, n)?;
        let aft = after(a, &st.bound);
        let mut m = Vec::new();
        super::parse::pattern_vars(b, &mut m);
        let mut mentioned = alloc::vec![false; n];
        m.iter().for_each(|&v| mentioned[v as usize] = true);
        if !(0..n).any(|v| mentioned[v] && safe[v] && st.bound[v] == 0 && aft[v] == CERTAIN) {
            return None;
        }
        let mut mask = Vec::new();
        for v in 0..n {
            // (b's expressions may read a variable its patterns do not mention)
            if !safe[v] && st.bound[v] != CERTAIN && aft[v] != 0 {
                // bound maybe before the join: the interpreter would pass it whatever its safety
                if st.bound[v] == MAYBE {
                    return None;
                }
                mask.push((v as Var, aft[v]));
            }
        }
        Some(mask)
    }

    /// The mask for `hidden`'s variables (each with a global for its value).
    fn mask(&mut self, a: &Pattern, b: &Pattern, st: &St) -> u32 {
        let vars = self.hidden(a, b, st).unwrap();
        let mask = vars.into_iter().map(|(v, k)| (v, self.global(), k)).collect();
        self.masks.push(mask);
        self.masks.len() as u32 - 1
    }

    /// Evaluate `p` with the interpreter now, onto the stack: the rows start at
    /// the word in global g, g + 1 holds their count. Returns g and the new top's local.
    fn table(&mut self, f: usize, p: &'q Pattern, st: &St) -> Res<(u32, u32)> {
        self.table_as(f, p, st, 2)
    }

    /// `opt`: the switch bit for where it is computed (1 OPTIONAL, 2 a join).
    fn table_as(&mut self, f: usize, p: &'q Pattern, st: &St, opt: u32) -> Res<(u32, u32)> {
        let g = self.global();
        self.global();
        let top = self.f(f).local();
        let (w, cur) = (self.width as i32, st.top);
        if let (Pattern::Sub(s), false) = (p, off(16)) {
            // A subquery: its WHERE here, against the frame (the interpreter's
            // substitution row); its solutions collected and finished by the engine.
            self.t.subs.push(&**s);
            let k = self.base[3] + self.t.subs.len() as u32 - 1;
            self.f(f).call(SUB);
            self.emit(f, &[Step::Pat(&s.pattern), Step::Keep], st)?;
            self.f(f).get(cur).global_set(g).i32(k as i32).get(cur).call(GROUP).global_set(g + 1);
        } else if !native(p) || matches!(p, Pattern::Sub(_)) || off(opt) {
            self.t.patterns.push(p);
            let k = self.base[2] + self.t.patterns.len() as u32 - 1;
            self.f(f).get(cur).global_set(g).i32(k as i32).get(cur).call(EVAL).global_set(g + 1);
        } else {
            // Any other pattern's rows: computed here too, collected as a
            // subquery's are, and kept as they are (cq_group(-1)).
            self.f(f).call(SUB);
            self.emit(f, &[Step::Pat(p), Step::Keep], st)?;
            self.f(f).get(cur).global_set(g).i32(-1).get(cur).call(GROUP).global_set(g + 1);
        }
        self.f(f).get(cur).global_get(g + 1).i32(w).op(MUL).op(ADD).set(top);
        Ok((g, top))
    }

    /// Loop over the rows of a table (globals g, g + 1): each row compatible
    /// with the frame is merged into it and runs `rest`.
    fn rows(&mut self, f: usize, g: u32, p: &'q Pattern, rest: &[Step<'q>], st: &St) -> Res<()> {
        let (i, r, x) = {
            let b = self.f(f);
            (b.local(), b.local(), b.local())
        };
        let w = self.width as i32 * 4;
        let vars = vars_of(p);
        self.f(f).i32(0).set(i).open(BLOCK).open(LOOP).get(i).global_get(g + 1).op(GE_U).br(BR_IF, 1);
        self.step(f);
        self.f(f).call(STACK).global_get(g).i32(4).op(MUL).op(ADD).get(i).i32(w).op(MUL).op(ADD).set(r);
        self.f(f).get(i).i32(1).op(ADD).set(i).open(BLOCK);
        // Every check before any merge: a mismatch leaves the block without restoring.
        for &v in &vars {
            if st.bound[v as usize] != 0 {
                // Both bound: they must agree.
                let a = self.frame + 4 * v;
                self.f(f).get(r).load(4 * v).tee(x).op(EQZ).i32(0).load(a).op(EQZ).op(OR).get(x).i32(0).load(a).op(EQ).op(OR).op(EQZ).br(BR_IF, 0);
            }
        }
        let mut saves = Vec::new();
        for &v in &vars {
            let a = self.frame + 4 * v;
            if st.bound[v as usize] == CERTAIN {
                continue;
            }
            let save = self.f(f).local();
            self.load_var(f, v);
            self.f(f).tee(save).op(EQZ).open(IF).i32(0).get(r).load(4 * v).store(a).op(END);
            saves.push((v, save));
        }
        let mut next = St { bound: st.bound.clone(), top: st.top };
        for &v in &vars {
            next.bound[v as usize] = next.bound[v as usize].max(MAYBE);
        }
        self.emit(f, rest, &next)?;
        for (v, save) in saves {
            self.store_var(f, v, save);
        }
        self.f(f).op(END).br(BR, 0).op(END).op(END);
        Ok(())
    }

    // --- expressions ---------------------------------------------------------------

    fn kind_of(&self, id: u32) -> u8 {
        self.dict.term(id).map_or(0, |t| t.kind)
    }

    /// Push an expression's value as a term id (0: unbound, or an error).
    fn value(&mut self, f: usize, e: &Expr) {
        if !self.in_tx && self.tx_worth(e) {
            return self.tx_top(f, e, false);
        }
        match e {
            Expr::Var(v) => self.load_var(f, *v),
            Expr::Const(c) => {
                self.f(f).i32(*c as i32);
            }
            Expr::Coalesce(list) => {
                let t = self.f(f).local();
                self.f(f).code.extend_from_slice(&[BLOCK, I32]);
                for x in list {
                    self.value(f, x);
                    self.f(f).tee(t).get(t).br(BR_IF, 0).op(DROP);
                }
                self.f(f).i32(0).op(END);
            }
            Expr::If(c, a, b) => {
                let t = self.f(f).local();
                self.bool3(f, c);
                self.f(f).tee(t).i32(1).op(EQ).code.extend_from_slice(&[IF, I32]);
                self.value(f, a);
                self.f(f).op(ELSE).get(t).op(EQZ).code.extend_from_slice(&[IF, I32]);
                self.value(f, b);
                self.f(f).op(ELSE).i32(0).op(END).op(END);
            }
            // STR of a simple literal is itself.
            Expr::Call(Func::Str, args) if simple(&args[0]) => {
                let t = self.f(f).local();
                self.value(f, &args[0]);
                self.f(f).tee(t).call(DATATYPE).i32(dict::XSD_STRING as i32).op(EQ).code.extend_from_slice(&[IF, I32]);
                self.f(f).get(t).op(ELSE);
                let k = self.expr_index(e);
                self.f(f).i32(k as i32).call(VALUE).op(END);
            }
            _ => {
                let k = self.expr_index(e);
                self.f(f).i32(k as i32).call(VALUE);
            }
        }
    }

    /// Push a condition's value: 0 false, 1 true, 2 error.
    fn bool3(&mut self, f: usize, e: &Expr) {
        if !self.in_tx && self.tx_worth(e) {
            return self.tx_top(f, e, true);
        }
        match e {
            // EXISTS over triples and filters: a function of its own that returns
            // at the first solution; what it binds is restored around the call.
            Expr::Exists(p, neg) if self.here.is_some() && exists_inline(p) && !off(8) => {
                let st = self.here.clone().unwrap();
                let flag = self.global();
                let p: &'q Pattern = unsafe { &*(&**p as *const Pattern) };
                let k = self.cont(&[Step::Pat(p), Step::Exit(flag)], st.bound.clone()).unwrap_or(u32::MAX);
                self.here = Some(st.clone());
                let mut saves = Vec::new();
                for v in vars_of(p) {
                    if st.bound[v as usize] != CERTAIN {
                        let l = self.f(f).local();
                        self.load_var(f, v);
                        self.f(f).set(l);
                        saves.push((v, l));
                    }
                }
                self.f(f).i32(0).global_set(flag).get(st.top).call(k);
                for (v, l) in saves {
                    self.store_var(f, v, l);
                }
                self.f(f).global_get(flag);
                if *neg {
                    self.f(f).i32(1).op(XOR);
                }
            }
            Expr::Bound(v) => {
                self.load_var(f, *v);
                self.f(f).i32(0).op(NE);
            }
            Expr::Not(a) => {
                let t = self.f(f).local();
                self.bool3(f, a);
                // An error stays one: select(t ^ 1, t, t != 2).
                self.f(f).tee(t).i32(1).op(XOR).get(t).get(t).i32(2).op(NE).op(SELECT);
            }
            Expr::And(a, b) | Expr::Or(a, b) => {
                // AND: false decides; OR: true decides; else an error if either is one.
                let decisive = if matches!(e, Expr::And(..)) { 0 } else { 1 };
                let (t, u) = (self.f(f).local(), self.f(f).local());
                self.bool3(f, a);
                self.f(f).tee(t).i32(decisive).op(EQ).code.extend_from_slice(&[IF, I32]);
                self.f(f).i32(decisive).op(ELSE);
                self.bool3(f, b);
                self.f(f).tee(u).i32(decisive).op(EQ).code.extend_from_slice(&[IF, I32]);
                self.f(f).i32(decisive).op(ELSE);
                self.f(f).i32(2).i32(1 - decisive).get(t).get(u).op(OR).i32(2).op(AND).op(SELECT);
                self.f(f).op(END).op(END);
            }
            Expr::Call(Func::SameTerm, args) if simple(&args[0]) && simple(&args[1]) => self.ids_equal(f, &args[0], &args[1], false),
            Expr::Cmp(op @ (Cmp::Eq | Cmp::Ne), a, b) if self.iri_compare(a, b) => self.ids_equal(f, a, b, *op == Cmp::Ne),
            Expr::Call(func @ (Func::IsIri | Func::IsBlank | Func::IsLiteral), args) if simple(&args[0]) => {
                let want = match func {
                    Func::IsIri => dict::IRI,
                    Func::IsBlank => dict::BNODE,
                    _ => dict::LITERAL,
                };
                let t = self.f(f).local();
                self.value(f, &args[0]);
                self.f(f).tee(t).op(EQZ).code.extend_from_slice(&[IF, I32]);
                self.f(f).i32(2).op(ELSE).get(t).call(KIND).i32(want as i32).op(EQ).op(END);
            }
            Expr::Cmp(op @ (Cmp::Eq | Cmp::Ne), a, b) if self.lang_empty(a, b).is_some() => {
                let x = self.lang_empty(a, b).unwrap();
                let t = self.f(f).local();
                self.value(f, x);
                // lang() of anything but a literal is an error.
                self.f(f).tee(t).call(KIND).i32(dict::LITERAL as i32).op(NE).code.extend_from_slice(&[IF, I32]);
                self.f(f).i32(2).op(ELSE).get(t).call(LANG_LEN);
                if *op == Cmp::Eq {
                    self.f(f).op(EQZ);
                } else {
                    self.f(f).i32(0).op(NE);
                }
                self.f(f).op(END);
            }
            _ => {
                let k = self.expr_index(e);
                self.f(f).i32(k as i32).call(TEST);
            }
        }
    }

    /// `lang(x) = ""` either way round, x a variable or constant: x.
    fn lang_empty<'e>(&self, a: &'e Expr, b: &'e Expr) -> Option<&'e Expr> {
        let empty = |e: &Expr| matches!(e, Expr::Const(c) if self.dict.term(*c).is_some_and(|t| t.kind == dict::LITERAL && t.a == dict::XSD_STRING) && self.dict.value(*c).is_empty());
        let lang_of = |e: &'e Expr| match e {
            Expr::Call(Func::Lang, args) if simple(&args[0]) => Some(&args[0]),
            _ => None,
        };
        if empty(b) {
            lang_of(a)
        } else if empty(a) {
            lang_of(b)
        } else {
            None
        }
    }

    /// `=` / `!=` between term ids where one side is an IRI or blank node constant:
    /// value equality is then term equality (and no error once both are bound).
    fn iri_compare(&self, a: &Expr, b: &Expr) -> bool {
        let named = |e: &Expr| matches!(e, Expr::Const(c) if matches!(self.kind_of(*c), dict::IRI | dict::BNODE));
        simple(a) && simple(b) && (named(a) || named(b))
    }

    /// 2 if either id is 0, else whether they are (not) equal.
    fn ids_equal(&mut self, f: usize, a: &Expr, b: &Expr, negate: bool) {
        let (x, y) = (self.f(f).local(), self.f(f).local());
        self.value(f, a);
        self.f(f).set(x);
        self.value(f, b);
        self.f(f).set(y);
        self.f(f).get(x).op(EQZ).get(y).op(EQZ).op(OR).code.extend_from_slice(&[IF, I32]);
        self.f(f).i32(2).op(ELSE).get(x).get(y).op(if negate { NE } else { EQ }).op(END);
    }

    // --- patterns ------------------------------------------------------------------

    /// The triples of a BGP in evaluation order: most bound first, as the interpreter scores them.
    fn order(&self, ts: &'q [Triple], bound: &[u8]) -> Vec<&'q Triple> {
        let mut bound = bound.to_vec();
        let mut left: Vec<&Triple> = ts.iter().collect();
        let mut out = Vec::new();
        while !left.is_empty() {
            let score = |t: &Triple, bound: &[u8]| {
                let b = |x: &Tv, w: u32| match x {
                    Tv::Term(_) => w,
                    Tv::Var(v) if bound[*v as usize] != 0 => w,
                    _ => 0,
                };
                match t.path {
                    Some(_) => (b(&t.s, 4) + b(&t.o, 3)).saturating_sub(1),
                    None => b(&t.s, 4) + b(&t.o, 3) + b(&t.p, 1),
                }
            };
            let (i, _) = left.iter().enumerate().max_by_key(|(i, t)| (score(t, &bound), usize::MAX - i)).unwrap();
            let t = left.remove(i);
            let mut vs = Vec::new();
            [&t.s, &t.p, &t.o].iter().for_each(|x| tv_vars(x, &mut vs));
            vs.iter().for_each(|&v| bound[v as usize] = CERTAIN);
            out.push(t);
        }
        out
    }

    /// A triple or path pattern: a loop over its matches (3 words each) on the stack.
    fn triple(&mut self, f: usize, t: &'q Triple, rest: &[Step<'q>], st: &St) -> Res<()> {
        let at = st.top;
        let (n, i, r, top) = {
            let x = self.f(f);
            (x.local(), x.local(), x.local(), x.local())
        };
        let positions: [(u32, &Tv); 3] = [(0, &t.s), (1, &t.p), (2, &t.o)];
        let positions = if t.path.is_some() {
            self.t.paths.push(t);
            let k = self.base[1] + self.t.paths.len() as u32 - 1;
            self.f(f).i32(k as i32);
            self.push_tv(f, &t.s);
            self.push_tv(f, &t.o);
            self.f(f).get(at).call(PATH).set(n);
            &[positions[0], positions[2]][..]
        } else {
            self.push_tv(f, &t.s);
            self.push_tv(f, &t.p);
            self.push_tv(f, &t.o);
            self.f(f).get(at).call(SCAN).set(n);
            &positions[..]
        };
        let cols: Vec<(u32, Var)> = positions.iter().filter_map(|&(c, x)| if let Tv::Var(v) = x { Some((c, *v)) } else { None }).collect();
        self.f(f).get(at).get(n).i32(3).op(MUL).op(ADD).set(top).i32(0).set(i);
        // Variables this pattern may bind, with their previous values saved.
        let mut binds: Vec<(u32, Var, Option<u32>)> = Vec::new();
        let mut checks: Vec<(u32, u32)> = Vec::new();
        for &(c, v) in &cols {
            if st.bound[v as usize] == CERTAIN {
                continue;
            }
            if let Some(&(c0, _, _)) = binds.iter().find(|b| b.1 == v) {
                checks.push((c0, c));
                continue;
            }
            let save = if st.bound[v as usize] == MAYBE {
                let l = self.f(f).local();
                self.load_var(f, v);
                self.f(f).set(l);
                Some(l)
            } else {
                None
            };
            binds.push((c, v, save));
        }
        self.f(f).open(BLOCK).open(LOOP).get(i).get(n).op(GE_U).br(BR_IF, 1);
        self.step(f);
        self.f(f).call(STACK).get(at).i32(4).op(MUL).op(ADD).get(i).i32(12).op(MUL).op(ADD).set(r);
        self.f(f).get(i).i32(1).op(ADD).set(i);
        self.f(f).open(BLOCK);
        for (a, b) in checks {
            self.f(f).get(r).load(4 * a).get(r).load(4 * b).op(NE).br(BR_IF, 0);
        }
        for &(c, v, _) in &binds {
            let a = self.frame + 4 * v;
            self.f(f).i32(0).get(r).load(4 * c).store(a);
        }
        let mut next = St { bound: st.bound.clone(), top };
        for &(_, v) in &cols {
            next.bound[v as usize] = CERTAIN;
        }
        self.emit(f, rest, &next)?;
        self.f(f).op(END).br(BR, 0).op(END).op(END);
        for (_, v, save) in binds {
            match save {
                Some(l) => self.store_var(f, v, l),
                None => self.store_const(f, v, 0),
            }
        }
        Ok(())
    }

    fn values(&mut self, f: usize, vars: &[Var], rows: &'q [Vec<u32>], after: Vec<u8>, rest: &[Step<'q>], st: &St) -> Res<()> {
        let k = if rows.len() > 1 { Some(self.cont(rest, after.clone())?) } else { None };
        let tmp = self.f(f).local();
        for row in rows {
            self.f(f).open(BLOCK);
            let mut set: Vec<(Var, u32, Option<u32>)> = Vec::new();
            for (&v, &t) in vars.iter().zip(row) {
                if t == 0 {
                    continue;
                }
                let state = st.bound[v as usize];
                if state != 0 {
                    // Bound already: it must agree (or be unbound, when only maybe bound).
                    self.load_var(f, v);
                    self.f(f).tee(tmp).i32(0).op(NE).get(tmp).i32(t as i32).op(NE).op(AND).br(BR_IF, 0);
                }
                if state == CERTAIN {
                    continue;
                }
                let save = if state == MAYBE {
                    let l = self.f(f).local();
                    self.f(f).get(tmp).set(l);
                    Some(l)
                } else {
                    None
                };
                set.push((v, t, save));
            }
            for &(v, t, _) in &set {
                self.store_const(f, v, t);
            }
            match k {
                Some(k) => {
                    let top = st.top;
                    self.f(f).get(top).call(k);
                }
                None => {
                    let next = St { bound: after.clone(), top: st.top };
                    self.emit(f, rest, &next)?;
                }
            }
            for (v, _, save) in set {
                match save {
                    Some(l) => self.store_var(f, v, l),
                    None => self.store_const(f, v, 0),
                }
            }
            self.f(f).op(END);
        }
        Ok(())
    }

    /// One solution: a CONSTRUCT template instantiated, or a row.
    fn final_(&mut self, f: usize) -> Res<()> {
        if self.q.form != Form::Construct {
            self.f(f).call(SOLUTION);
            return Ok(());
        }
        let bnodes: Vec<u32> = (0..self.q.template_bnodes).map(|_| self.f(f).local()).collect();
        for &l in &bnodes {
            self.f(f).call(FRESH).set(l);
        }
        let q = self.q;
        for t in &q.template {
            for x in t {
                match x {
                    Tt::Term(c) => {
                        self.f(f).i32(*c as i32);
                    }
                    Tt::Var(v) => self.load_var(f, *v),
                    Tt::Bnode(k) => {
                        self.f(f).get(bnodes[*k as usize]);
                    }
                    Tt::Triple(_) => unreachable!(),
                }
            }
            self.f(f).call(TRIPLE);
        }
        Ok(())
    }
}
