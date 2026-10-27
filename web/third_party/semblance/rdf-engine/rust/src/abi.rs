//! The exports of `../ABI.md`. Wire types are i32; ids and counts are u32.

use crate::dict::{self, Dict, Key, lowercase};
use crate::lex;
use crate::sparql;
use crate::ntriples;
use crate::turtle;
use crate::store::{Quad, Store};
use alloc::vec::Vec;
use core::cell::UnsafeCell;

struct Prepared {
    query: alloc::boxed::Box<sparql::Query>,
    /// Bound variables: one term each, or several (run once per combination).
    binds: Vec<(sparql::Var, Vec<u32>)>,
    /// What compiled modules of this query refer to (see `compile`).
    cq: Option<alloc::boxed::Box<Cq>>,
}

struct State {
    queries: Vec<Option<Prepared>>,
    dict: Dict,
    store: Store,
    input: Vec<u8>,
    result: Vec<u32>,
    error: Vec<u8>,
    scratch: Vec<u8>,
}

struct Global(UnsafeCell<Option<State>>);

// Wasm without threads: there is one caller at a time.
unsafe impl Sync for Global {}

static STATE: Global = Global(UnsafeCell::new(None));

fn st() -> &'static mut State {
    unsafe {
        (*STATE.0.get()).get_or_insert_with(|| State {
            queries: Vec::new(),
            dict: Dict::new(),
            store: Store::default(),
            input: Vec::new(),
            result: Vec::new(),
            error: Vec::new(),
            scratch: Vec::new(),
        })
    }
}

fn fail(msg: &[u8]) -> i32 {
    let s = st();
    s.error.clear();
    s.error.extend_from_slice(msg);
    -1
}

fn fail_at(e: lex::Error) -> i32 {
    let s = st();
    s.error.clear();
    s.error.extend_from_slice(b"line ");
    let mut digits = [0u8; 10];
    let mut i = digits.len();
    let mut n = e.line;
    loop {
        i -= 1;
        digits[i] = b'0' + (n % 10) as u8;
        n /= 10;
        if n == 0 {
            break;
        }
    }
    s.error.extend_from_slice(&digits[i..]);
    s.error.extend_from_slice(b": ");
    s.error.extend_from_slice(e.msg.as_bytes());
    -1
}

/// The input buffer split into consecutive strings of the given lengths.
fn args<const N: usize>(input: &[u8], lens: [u32; N]) -> [&[u8]; N] {
    let mut at = 0;
    lens.map(|n| {
        let s = &input[at..at + n as usize];
        at += n as usize;
        s
    })
}

// --- housekeeping ------------------------------------------------------------

#[unsafe(no_mangle)]
pub extern "C" fn abi_version() -> i32 {
    1
}

#[unsafe(no_mangle)]
pub extern "C" fn input(len: u32) -> *mut u8 {
    let s = st();
    // The host overwrites what it asked for, so the bytes need no clearing.
    s.input.clear();
    s.input.reserve(len as usize);
    // SAFETY: u8 has no invalid values, and the capacity was just reserved.
    unsafe { s.input.set_len(len as usize) };
    s.input.as_mut_ptr()
}

#[unsafe(no_mangle)]
pub extern "C" fn result_ptr() -> *const u32 {
    st().result.as_ptr()
}

#[unsafe(no_mangle)]
pub extern "C" fn error_ptr() -> *const u8 {
    st().error.as_ptr()
}

#[unsafe(no_mangle)]
pub extern "C" fn error_len() -> u32 {
    st().error.len() as u32
}

// --- terms -------------------------------------------------------------------

fn lang_key<'a>(input: &'a [u8], scratch: &'a mut Vec<u8>, len: u32, lang_len: u32, dir: u32) -> Key<'a> {
    let [v, l] = args(input, [len, lang_len]);
    Key::lang_literal(v, lowercase(l, scratch), dir as u8)
}

#[unsafe(no_mangle)]
pub extern "C" fn intern_iri(len: u32) -> u32 {
    let s = st();
    s.dict.intern(Key::iri(&s.input[..len as usize]))
}

#[unsafe(no_mangle)]
pub extern "C" fn intern_bnode(len: u32) -> u32 {
    let s = st();
    s.dict.intern(Key::bnode(&s.input[..len as usize]))
}

#[unsafe(no_mangle)]
pub extern "C" fn fresh_bnode() -> u32 {
    st().dict.fresh_bnode()
}

#[unsafe(no_mangle)]
pub extern "C" fn intern_literal(len: u32, datatype: u32) -> u32 {
    let s = st();
    s.dict.intern(Key::literal(&s.input[..len as usize], datatype))
}

#[unsafe(no_mangle)]
pub extern "C" fn intern_lang_literal(len: u32, lang_len: u32, dir: u32) -> u32 {
    let s = st();
    s.dict.intern(lang_key(&s.input, &mut s.scratch, len, lang_len, dir))
}

#[unsafe(no_mangle)]
pub extern "C" fn intern_triple(sub: u32, p: u32, o: u32) -> u32 {
    st().dict.intern(Key::triple(sub, p, o))
}

#[unsafe(no_mangle)]
pub extern "C" fn lookup_iri(len: u32) -> u32 {
    let s = st();
    s.dict.get(Key::iri(&s.input[..len as usize]))
}

#[unsafe(no_mangle)]
pub extern "C" fn lookup_bnode(len: u32) -> u32 {
    let s = st();
    s.dict.get(Key::bnode(&s.input[..len as usize]))
}

#[unsafe(no_mangle)]
pub extern "C" fn lookup_literal(len: u32, datatype: u32) -> u32 {
    let s = st();
    s.dict.get(Key::literal(&s.input[..len as usize], datatype))
}

#[unsafe(no_mangle)]
pub extern "C" fn lookup_lang_literal(len: u32, lang_len: u32, dir: u32) -> u32 {
    let s = st();
    s.dict.get(lang_key(&s.input, &mut s.scratch, len, lang_len, dir))
}

#[unsafe(no_mangle)]
pub extern "C" fn lookup_triple(sub: u32, p: u32, o: u32) -> u32 {
    st().dict.get(Key::triple(sub, p, o))
}

#[unsafe(no_mangle)]
pub extern "C" fn term_kind(id: u32) -> u32 {
    st().dict.term(id).map_or(dict::FREE, |t| t.kind) as u32
}

#[unsafe(no_mangle)]
pub extern "C" fn term_value_ptr(id: u32) -> *const u8 {
    st().dict.value(id).as_ptr()
}

#[unsafe(no_mangle)]
pub extern "C" fn term_value_len(id: u32) -> u32 {
    st().dict.value(id).len() as u32
}

#[unsafe(no_mangle)]
pub extern "C" fn term_datatype(id: u32) -> u32 {
    st().dict.term(id).filter(|t| t.kind == dict::LITERAL).map_or(0, |t| t.a)
}

#[unsafe(no_mangle)]
pub extern "C" fn term_lang_ptr(id: u32) -> *const u8 {
    st().dict.lang(id).as_ptr()
}

#[unsafe(no_mangle)]
pub extern "C" fn term_lang_len(id: u32) -> u32 {
    st().dict.lang(id).len() as u32
}

#[unsafe(no_mangle)]
pub extern "C" fn term_direction(id: u32) -> u32 {
    st().dict.term(id).map_or(0, |t| t.dir) as u32
}

fn triple_part(id: u32, k: usize) -> u32 {
    st().dict.term(id).filter(|t| t.kind == dict::TRIPLE).map_or(0, |t| [t.a, t.b, t.c][k])
}

#[unsafe(no_mangle)]
pub extern "C" fn triple_subject(id: u32) -> u32 {
    triple_part(id, 0)
}

#[unsafe(no_mangle)]
pub extern "C" fn triple_predicate(id: u32) -> u32 {
    triple_part(id, 1)
}

#[unsafe(no_mangle)]
pub extern "C" fn triple_object(id: u32) -> u32 {
    triple_part(id, 2)
}

#[unsafe(no_mangle)]
pub extern "C" fn term_count() -> u32 {
    st().dict.len()
}

// --- store -------------------------------------------------------------------

#[unsafe(no_mangle)]
pub extern "C" fn add(s: u32, p: u32, o: u32, g: u32) -> i32 {
    st().store.add([s, p, o, g]) as i32
}

#[unsafe(no_mangle)]
pub extern "C" fn remove(s: u32, p: u32, o: u32, g: u32) -> i32 {
    st().store.remove([s, p, o, g]) as i32
}

#[unsafe(no_mangle)]
pub extern "C" fn has(s: u32, p: u32, o: u32, g: u32) -> i32 {
    st().store.has([s, p, o, g]) as i32
}

#[unsafe(export_name = "match")]
pub extern "C" fn match_(s: u32, p: u32, o: u32, g: u32) -> i32 {
    let st = st();
    st.result.clear();
    st.store.scan([s, p, o, g], &mut st.result);
    (st.result.len() / 4) as i32
}

#[unsafe(no_mangle)]
pub extern "C" fn apply(removes: u32, adds: u32) -> i32 {
    let s = st();
    let quad = |i: usize| -> Quad {
        let w = |k: usize| u32::from_le_bytes([s.input[k], s.input[k + 1], s.input[k + 2], s.input[k + 3]]);
        [w(i * 16), w(i * 16 + 4), w(i * 16 + 8), w(i * 16 + 12)]
    };
    let (removes, adds) = (removes as usize, adds as usize);
    if s.input.len() < (removes + adds) * 16 {
        return fail(b"input buffer too short");
    }
    for i in 0..removes {
        s.store.remove(quad(i));
    }
    for i in removes..removes + adds {
        s.store.add(quad(i));
    }
    0
}

#[unsafe(no_mangle)]
pub extern "C" fn quad_count() -> u32 {
    st().store.len() as u32
}

#[unsafe(no_mangle)]
pub extern "C" fn load(format: u32, text_len: u32, base_len: u32, graph: u32) -> i32 {
    let s = st();
    let [text, base] = args(&s.input, [text_len, base_len]);
    let mut quads = Vec::new();
    let parsed = match format {
        1 | 2 => ntriples::parse(text, format == 2, graph, &mut s.dict, &mut quads),
        3 | 4 => turtle::parse(text, format == 4, base, graph, &mut s.dict, &mut quads),
        _ => return fail(b"unknown format"),
    };
    if let Err(e) = parsed {
        return fail_at(e);
    }
    for &q in &quads {
        s.store.add(q);
    }
    quads.len() as i32
}

#[unsafe(no_mangle)]
pub extern "C" fn collect() -> i32 {
    let s = st();
    let mut alive = alloc::vec![false; s.dict.capacity()];
    let dict = &s.dict;
    s.store.each(|q| q.iter().for_each(|&id| dict.mark(id, &mut alive)));
    for p in s.queries.iter().flatten() {
        sparql::query_terms(&p.query, &mut |id| dict.mark(id, &mut alive));
        p.binds.iter().flat_map(|(_, v)| v).for_each(|&id| dict.mark(id, &mut alive));
    }
    let freed = s.dict.sweep(&alive);
    s.result.clear();
    s.result.extend(freed);
    s.result.len() as i32
}

// --- queries -----------------------------------------------------------------

fn prepared(h: u32) -> Option<&'static mut Prepared> {
    st().queries.get_mut((h as usize).wrapping_sub(1))?.as_mut()
}

#[unsafe(no_mangle)]
pub extern "C" fn prepare(text_len: u32, base_len: u32) -> i32 {
    let s = st();
    let [text, base] = args(&s.input, [text_len, base_len]);
    match sparql::parse::parse_query(text, base, &mut s.dict) {
        Ok(query) => {
            let p = Prepared { query: alloc::boxed::Box::new(query), binds: Vec::new(), cq: None };
            match s.queries.iter().position(Option::is_none) {
                Some(i) => {
                    s.queries[i] = Some(p);
                    i as i32 + 1
                }
                None => {
                    s.queries.push(Some(p));
                    s.queries.len() as i32
                }
            }
        }
        Err(e) => fail_at(e),
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn query_form(h: u32) -> u32 {
    prepared(h).map_or(0, |p| p.query.form as u32)
}

#[unsafe(no_mangle)]
pub extern "C" fn query_var_count(h: u32) -> u32 {
    prepared(h).filter(|p| p.query.form == sparql::Form::Select).map_or(0, |p| p.query.select.project.len() as u32)
}

fn var_name(h: u32, i: u32) -> &'static [u8] {
    prepared(h).and_then(|p| p.query.select.project.get(i as usize).map(|&v| &p.query.vars[v as usize][..])).unwrap_or(b"")
}

#[unsafe(no_mangle)]
pub extern "C" fn query_var_ptr(h: u32, i: u32) -> *const u8 {
    var_name(h, i).as_ptr()
}

#[unsafe(no_mangle)]
pub extern "C" fn query_var_len(h: u32, i: u32) -> u32 {
    var_name(h, i).len() as u32
}

fn set_binding(h: u32, name: &[u8], values: Vec<u32>) -> i32 {
    let Some(p) = prepared(h) else { return fail(b"no such query") };
    let Some(v) = p.query.var(name) else { return 0 };
    p.binds.retain(|(x, _)| *x != v);
    if !values.is_empty() {
        p.binds.push((v, values));
    }
    0
}

#[unsafe(no_mangle)]
pub extern "C" fn bind(h: u32, name_len: u32, id: u32) -> i32 {
    let name = st().input[..name_len as usize].to_vec();
    set_binding(h, &name, if id == 0 { Vec::new() } else { alloc::vec![id] })
}

#[unsafe(no_mangle)]
pub extern "C" fn bind_values(h: u32, count: u32, name_len: u32) -> i32 {
    let s = st();
    let n = count as usize;
    let ids: Vec<u32> = (0..n).map(|i| u32::from_le_bytes([s.input[i * 4], s.input[i * 4 + 1], s.input[i * 4 + 2], s.input[i * 4 + 3]])).collect();
    let name = s.input[n * 4..n * 4 + name_len as usize].to_vec();
    set_binding(h, &name, ids)
}

#[unsafe(no_mangle)]
pub extern "C" fn set_deadline(at_ms: f64) {
    crate::budget::set_deadline(at_ms);
}

#[unsafe(no_mangle)]
pub extern "C" fn set_fuel(steps: u32) {
    crate::budget::set_fuel(steps);
}

#[unsafe(no_mangle)]
pub extern "C" fn run(h: u32, graphs: u32) -> i32 {
    crate::budget::start();
    let n = run_query(h, graphs);
    if crate::budget::finish() {
        st().result.clear();
        fail(b"interrupted");
        return -2;
    }
    n
}

fn run_query(h: u32, graphs: u32) -> i32 {
    let s = st();
    let graph_ids: Vec<u32> = (0..graphs as usize).map(|i| u32::from_le_bytes([s.input[i * 4], s.input[i * 4 + 1], s.input[i * 4 + 2], s.input[i * 4 + 3]])).collect();
    let Some(p) = s.queries.get((h as usize).wrapping_sub(1)).and_then(Option::as_ref) else { return fail(b"no such query") };
    // One run per combination of bound values.
    let combos = combos(&p.binds);
    s.result.clear();
    let mut count = 0usize;
    let mut seen = crate::idmap::IdMap::new();
    for c in combos {
        if crate::budget::stopped() {
            break;
        }
        match sparql::execute(&p.query, &mut s.dict, &s.store, &graph_ids, &c) {
            sparql::Output::Rows(r, n) => {
                count += n;
                s.result.extend(r);
            }
            sparql::Output::Triples(t) => {
                for x in t.chunks_exact(3) {
                    if seen.add(x) {
                        s.result.extend_from_slice(x);
                        count += 1;
                    }
                }
            }
            sparql::Output::Bool(b) => {
                if b {
                    return 1;
                }
            }
        }
    }
    if p.query.form == sparql::Form::Ask {
        return 0;
    }
    count as i32
}

#[unsafe(no_mangle)]
pub extern "C" fn release(h: u32) -> i32 {
    if let Some(slot) = st().queries.get_mut((h as usize).wrapping_sub(1)) {
        *slot = None;
    }
    0
}

#[unsafe(no_mangle)]
pub extern "C" fn parse_update(text_len: u32, base_len: u32) -> i32 {
    let s = st();
    let [text, base] = args(&s.input, [text_len, base_len]);
    match sparql::parse::parse_update(text, base, &mut s.dict) {
        Ok(_) => 0,
        Err(e) => fail_at(e),
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn update(text_len: u32, base_len: u32) -> i32 {
    let s = st();
    let [text, base] = args(&s.input, [text_len, base_len]);
    let u = match sparql::parse::parse_update(text, base, &mut s.dict) {
        Ok(u) => u,
        Err(e) => return fail_at(e),
    };
    match sparql::update::execute(&u, &mut s.dict, &mut s.store) {
        Ok(()) => 0,
        Err(msg) => fail(msg.as_bytes()),
    }
}

/// Every combination of bound values.
fn combos(binds: &[(sparql::Var, Vec<u32>)]) -> Vec<Vec<(sparql::Var, u32)>> {
    let mut combos: Vec<Vec<(sparql::Var, u32)>> = alloc::vec![Vec::new()];
    for (v, values) in binds {
        combos = combos.into_iter().flat_map(|c| values.iter().map(move |&x| {
            let mut c = c.clone();
            c.push((*v, x));
            c
        })).collect();
    }
    combos
}

// --- compiled queries (ABI.md, "Optional: compiled queries") ------------------------

/// What a query's compiled modules refer to: the tables they index into, and
/// the row frame. The frame is allocated once and never resized: modules have
/// its address baked in. The query is boxed, so the tables' pointers into it stay put.
struct Cq {
    tables: sparql::compile::Tables,
    frame: Vec<u32>,
    /// The bound variables (sorted) each compiled module was made for.
    shapes: Vec<Vec<sparql::Var>>,
}

fn shape(p: &Prepared) -> Vec<sparql::Var> {
    let mut v: Vec<sparql::Var> = p.binds.iter().map(|(v, _)| *v).collect();
    v.sort_unstable();
    v
}

#[unsafe(no_mangle)]
pub extern "C" fn compile(h: u32) -> i32 {
    let Some(p) = prepared(h) else { return fail(b"no such query") };
    let bound = shape(p);
    let nvars = p.query.vars.len();
    let cq = p.cq.get_or_insert_with(|| alloc::boxed::Box::new(Cq { tables: Default::default(), frame: alloc::vec![0; nvars.max(1)], shapes: Vec::new() }));
    let id = match cq.shapes.iter().position(|s| *s == bound) {
        Some(i) => i,
        None => {
            cq.shapes.push(bound.clone());
            cq.shapes.len() - 1
        }
    };
    let t = &cq.tables;
    let base = [t.exprs.len() as u32, t.paths.len() as u32, t.patterns.len() as u32, t.subs.len() as u32];
    match sparql::compile::compile(&p.query, &st().dict, &bound, h, id as u32, cq.frame.as_ptr() as u32, base) {
        Ok((bytes, t)) => {
            cq.tables.exprs.extend(t.exprs);
            cq.tables.paths.extend(t.paths);
            cq.tables.patterns.extend(t.patterns);
            cq.tables.subs.extend(t.subs);
            cq.tables.consts.extend(t.consts);
            let r = &mut st().result;
            r.clear();
            r.resize(bytes.len().div_ceil(4), 0);
            unsafe { core::ptr::copy_nonoverlapping(bytes.as_ptr(), r.as_mut_ptr() as *mut u8, bytes.len()) };
            bytes.len() as i32
        }
        Err(msg) => fail(msg.as_bytes()),
    }
}

/// A compiled run, between `cq_begin` and `cq_end`.
struct Run {
    ctx: sparql::eval::Ctx<'static>,
    p: &'static Prepared,
    combos: Vec<Vec<(sparql::Var, u32)>>,
    count: usize,
    seen: crate::idmap::IdMap,
    distinct: crate::idmap::IdMap,
    stack: Vec<u32>,
    scan: Vec<u32>,
    /// Solutions of compiled subqueries' WHEREs being collected (nested).
    keep: Vec<Vec<sparql::eval::Row>>,
}

struct RunCell(UnsafeCell<Option<Run>>);
unsafe impl Sync for RunCell {}
static RUN: RunCell = RunCell(UnsafeCell::new(None));

fn run_state() -> &'static mut Run {
    unsafe { (*RUN.0.get()).as_mut().unwrap_or_else(|| core::arch::wasm32::unreachable()) }
}

fn cq(r: &Run) -> &'static Cq {
    unsafe { &*(&**r.p.cq.as_ref().unwrap() as *const Cq) }
}

/// The frame: one id per variable of the query.
fn frame(r: &Run) -> &'static mut [u32] {
    let cq = cq(r);
    unsafe { core::slice::from_raw_parts_mut(cq.frame.as_ptr() as *mut u32, r.p.query.vars.len()) }
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_begin(h: u32, shape_id: u32, graphs: u32) -> i32 {
    let s = st();
    let graph_ids: Vec<u32> = (0..graphs as usize).map(|i| u32::from_le_bytes([s.input[i * 4], s.input[i * 4 + 1], s.input[i * 4 + 2], s.input[i * 4 + 3]])).collect();
    let Some(p) = s.queries.get((h as usize).wrapping_sub(1)).and_then(Option::as_ref) else { return fail(b"no such query") };
    let Some(cq) = &p.cq else { return fail(b"query not compiled") };
    if cq.shapes.get(shape_id as usize) != Some(&shape(p)) {
        return fail(b"bound variables changed since compile");
    }
    let q = &p.query;
    let (default, named) = if !q.from.is_empty() || !q.from_named.is_empty() {
        (q.from.clone(), Some(q.from_named.clone()))
    } else if graph_ids.is_empty() {
        (alloc::vec![dict::DEFAULT_GRAPH_ID], None)
    } else {
        (graph_ids, None)
    };
    let combos = combos(&p.binds);
    let n = combos.len() as i32;
    let ctx = sparql::eval::Ctx::new(&mut s.dict, &s.store, default, named, &q.base, q.vars.len());
    s.result.clear();
    // The run's budget: the module steps in line (budget::cd_addr), cq_end says -2 once it is spent.
    crate::budget::start();
    unsafe {
        *RUN.0.get() = Some(Run { ctx, p, combos, count: 0, seen: crate::idmap::IdMap::new(), distinct: crate::idmap::IdMap::new(), stack: Vec::new(), scan: Vec::new(), keep: Vec::new() });
    }
    n
}

/// A compiled module's budget step reached the end of its countdown: whether the budget is spent.
#[unsafe(no_mangle)]
pub extern "C" fn cq_bu_event() -> i32 {
    crate::budget::event() as i32
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_combo(i: u32) {
    let r = run_state();
    let f = frame(r);
    f.fill(0);
    for &(v, x) in &r.combos[i as usize] {
        f[v as usize] = x;
    }
    if r.p.query.select.distinct || r.p.query.select.reduced {
        r.distinct = crate::idmap::IdMap::new();
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_scan(sub: u32, p: u32, o: u32, at: u32) -> u32 {
    let r = run_state();
    r.stack.truncate(at as usize);
    let dedupe = r.ctx.default.len() > 1;
    let mut seen = crate::idmap::IdMap::new();
    let mut n = 0;
    for &g in &r.ctx.default {
        r.scan.clear();
        r.ctx.store.scan([sub, p, o, g], &mut r.scan);
        for q in r.scan.chunks_exact(4) {
            if dedupe && !seen.add(&q[..3]) {
                continue;
            }
            r.stack.extend_from_slice(&q[..3]);
            n += 1;
        }
    }
    if r.stack.len() > (16 << 20) {
        core::arch::wasm32::unreachable()
    }
    n
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_stack() -> *const u32 {
    run_state().stack.as_ptr()
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_path(k: u32, _s: u32, _o: u32, at: u32) -> u32 {
    let r = run_state();
    r.stack.truncate(at as usize);
    let t = unsafe { &*cq(r).tables.paths[k as usize] };
    let rows = r.ctx.path(&t.s, t.path.as_ref().unwrap(), &t.o, frame(r), sparql::eval::Active::Default);
    for row in &rows {
        let x = sparql::eval::resolve(&t.s, row).unwrap_or(0);
        let y = sparql::eval::resolve(&t.o, row).unwrap_or(0);
        r.stack.extend_from_slice(&[x, 0, y]);
    }
    rows.len() as u32
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_eval(k: u32, at: u32) -> u32 {
    let r = run_state();
    r.stack.truncate(at as usize);
    let p = unsafe { &*cq(r).tables.patterns[k as usize] };
    let rows = r.ctx.eval(p, frame(r), sparql::eval::Active::Default);
    for row in &rows {
        r.stack.extend_from_slice(row);
    }
    if r.stack.len() > (16 << 20) {
        core::arch::wasm32::unreachable()
    }
    rows.len() as u32
}

/// A compiled subquery: `cq_sub` starts collecting its WHERE's solutions (the
/// frame at each `cq_keep`); `cq_group` finishes them as the interpreter does
/// (grouping, aggregates, modifiers, projection): rows as `cq_eval` gives them.
#[unsafe(no_mangle)]
pub extern "C" fn cq_debug(bits: u32) {
    sparql::compile::DEBUG.store(bits, core::sync::atomic::Ordering::Relaxed);
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_sub() {
    run_state().keep.push(Vec::new());
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_keep() {
    let r = run_state();
    let row = frame(r).to_vec();
    if let Some(k) = r.keep.last_mut() {
        k.push(row);
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_group(k: u32, at: u32) -> u32 {
    let r = run_state();
    r.stack.truncate(at as usize);
    let rows = r.keep.pop().unwrap_or_default();
    // k = -1: a pattern's rows, as they are
    let rows = if k == u32::MAX {
        rows
    } else {
        let s = unsafe { &*cq(r).tables.subs[k as usize] };
        r.ctx.select_rows(s, rows, sparql::eval::Active::Default)
    };
    for row in &rows {
        r.stack.extend_from_slice(row);
    }
    if r.stack.len() > (16 << 20) {
        core::arch::wasm32::unreachable()
    }
    rows.len() as u32
}

/// Expression k of a compiled module: with bit 31 (a SELECT expression after
/// another) BNODE(str) keeps the nodes of the call before, as the interpreter's
/// projection does row by row; else they are a new solution's.
fn same_solution(r: &mut Run, k: u32) -> u32 {
    if k & 0x8000_0000 == 0 {
        r.ctx.bnode_new();
    }
    k & 0x7fff_ffff
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_test(k: u32) -> u32 {
    let r = run_state();
    let k = same_solution(r, k);
    let e = unsafe { &*cq(r).tables.exprs[k as usize] };
    match r.ctx.expr(e, frame(r)).and_then(|v| r.ctx.ebv(&v)) {
        Ok(b) => b as u32,
        Err(()) => 2,
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_value(k: u32) -> u32 {
    let r = run_state();
    let k = same_solution(r, k);
    let e = unsafe { &*cq(r).tables.exprs[k as usize] };
    match r.ctx.expr(e, frame(r)) {
        Ok(v) => r.ctx.to_id(&v),
        Err(()) => 0,
    }
}

// --- typed expressions (sparql/ctyped.rs) ------------------------------------------
// Compiled code keeps strings as bytes in TX_BUF (term bytes are copied there: the
// dictionary's arena moves when a term is interned) and numbers in locals; these
// calls read and make terms. NUM: the number record, i64 at +0, f64 at +8 (a
// SUBSTR's start), i32 at +16 (a slice's length), f64 at +24 (a SUBSTR's length).

pub const TX_BUF_LEN: u32 = 65536;
pub struct TxBuf(pub UnsafeCell<[u8; TX_BUF_LEN as usize]>);
unsafe impl Sync for TxBuf {}
pub static TX_BUF: TxBuf = TxBuf(UnsafeCell::new([0; TX_BUF_LEN as usize]));
pub struct TxNum(pub UnsafeCell<[u64; 4]>);
unsafe impl Sync for TxNum {}
pub static TX_NUM: TxNum = TxNum(UnsafeCell::new([0; 4]));

pub fn tx_buf() -> u32 {
    TX_BUF.0.get() as u32
}
pub fn tx_num() -> u32 {
    TX_NUM.0.get() as u32
}
fn num_rec() -> &'static mut [u64; 4] {
    unsafe { &mut *TX_NUM.0.get() }
}
fn bytes_at(p: u32, n: u32) -> &'static [u8] {
    unsafe { core::slice::from_raw_parts(p as *const u8, n as usize) }
}

/// A number into the record: its type (1 an integer that fits in 64 bits, 4 a
/// double), or 6 for a number compiled code leaves to the engine.
fn put_num(n: sparql::num::Num) -> i32 {
    let r = num_rec();
    match n {
        sparql::num::Num::Int(i) if i64::try_from(i).is_ok() => {
            r[0] = i as i64 as u64;
            r[1] = (i as i64 as f64).to_bits();
            1
        }
        sparql::num::Num::Double(d) => {
            r[1] = d.to_bits();
            4
        }
        _ => 6,
    }
}

/// The lexical form of term id into the buffer at `at`: its length; -1 an
/// error, -2 left to the engine (a language-tagged string; no room). Mode 0:
/// STR (an IRI's or any literal's); 1: a string argument (xsd:string only).
#[unsafe(no_mangle)]
pub extern "C" fn cq_lex(id: u32, at: u32, mode: u32) -> i32 {
    let d = &st().dict;
    let Some(t) = d.term(id) else { return -1 };
    let ok = match mode {
        0 => t.kind == dict::IRI || t.kind == dict::LITERAL,
        _ if t.kind == dict::LITERAL && (t.a == dict::RDF_LANG_STRING || t.a == dict::RDF_DIR_LANG_STRING) => return -2,
        _ => t.kind == dict::LITERAL && t.a == dict::XSD_STRING,
    };
    if !ok {
        return -1;
    }
    let v = d.value(id);
    let end = tx_buf() + TX_BUF_LEN;
    if at < tx_buf() || at > end || v.len() as u32 > end - at {
        return -2;
    }
    unsafe { core::ptr::copy_nonoverlapping(v.as_ptr(), at as *mut u8, v.len()) };
    v.len() as i32
}

/// Term id as a number (into the record): its type, 0 when it is not one.
#[unsafe(no_mangle)]
pub extern "C" fn cq_num(id: u32) -> i32 {
    let r = run_state();
    match r.ctx.num(&sparql::expr::Val::Id(id)) {
        Some(n) => put_num(n),
        None => 0,
    }
}

/// The term of the record's number of type t (1 integer, 4 double; 5 a boolean,
/// the i64 its value).
#[unsafe(no_mangle)]
pub extern "C" fn cq_numterm(t: u32) -> u32 {
    let r = run_state();
    let n = num_rec();
    use sparql::expr::Val;
    let v = match t {
        1 => Val::Num(sparql::num::Num::Int(n[0] as i64 as i128)),
        4 => Val::Num(sparql::num::Num::Double(f64::from_bits(n[1]))),
        _ => Val::Bool(n[0] != 0),
    };
    r.ctx.to_id(&v)
}

/// The simple literal of n bytes at p.
#[unsafe(no_mangle)]
pub extern "C" fn cq_strterm(p: u32, n: u32) -> u32 {
    run_state().ctx.dict.intern(Key::literal(bytes_at(p, n), dict::NONE))
}

/// IRI(s) of the simple string of n bytes at p: resolved against the base.
#[unsafe(no_mangle)]
pub extern "C" fn cq_iri(p: u32, n: u32) -> u32 {
    let r = run_state();
    let mut out = Vec::new();
    crate::iri::resolve(r.ctx.base, bytes_at(p, n), &mut out);
    r.ctx.dict.intern(Key::iri(&out))
}

/// Strings a, b: mode 0 STRSTARTS, 1 STRENDS, 2 CONTAINS (0 or 1); 3 a <=> b (-1, 0, 1).
#[unsafe(no_mangle)]
pub extern "C" fn cq_strfn(mode: u32, pa: u32, na: u32, pb: u32, nb: u32) -> i32 {
    let (a, b) = (bytes_at(pa, na), bytes_at(pb, nb));
    match mode {
        0 => a.starts_with(b) as i32,
        1 => a.ends_with(b) as i32,
        2 => sparql::expr::find(a, b).is_some() as i32,
        _ => a.cmp(b) as i32,
    }
}

/// The number of characters (code points) of n bytes at p.
#[unsafe(no_mangle)]
pub extern "C" fn cq_cplen(p: u32, n: u32) -> u32 {
    bytes_at(p, n).iter().filter(|&&b| b & 0xc0 != 0x80).count() as u32
}

/// SUBSTR of n bytes at p from the record's start (f64 at +8), and length (f64
/// at +24) when k is 3: the slice's offset, its length at +16.
#[unsafe(no_mangle)]
pub extern "C" fn cq_substr(p: u32, n: u32, k: u32) -> u32 {
    let r = num_rec();
    let (from, to) = sparql::expr::substr_range(bytes_at(p, n), f64::from_bits(r[1]), if k == 3 { Some(f64::from_bits(r[3])) } else { None });
    r[2] = (to - from) as u64;
    from as u32
}

/// A cast to xsd:integer (tc 1) or xsd:double (tc 4) of: term a (src 0), the
/// simple string of b bytes at a (src 1), or the record's number of type a
/// (src 2). The result into the record: its type, 0 an error, 6 left to the engine.
#[unsafe(no_mangle)]
pub extern "C" fn cq_tonum(src: u32, a: u32, b: u32, tc: u32) -> i32 {
    use sparql::expr::Val;
    let r = run_state();
    let n = num_rec();
    let v = match src {
        0 => Val::Id(a),
        1 => Val::Str(bytes_at(a, b).to_vec(), None),
        _ if a == 1 => Val::Num(sparql::num::Num::Int(n[0] as i64 as i128)),
        _ => Val::Num(sparql::num::Num::Double(f64::from_bits(n[1]))),
    };
    let dt = r.ctx.xsd(if tc == 1 { b"integer" } else { b"double" });
    match r.ctx.cast(dt, &v) {
        Ok(Val::Num(x)) => put_num(x),
        Ok(_) => 6,
        Err(()) => 0,
    }
}

/// The effective boolean value of term id: 0, 1, or 2 for an error.
#[unsafe(no_mangle)]
pub extern "C" fn cq_ebv(id: u32) -> u32 {
    let r = run_state();
    match r.ctx.ebv(&sparql::expr::Val::Id(id)) {
        Ok(b) => b as u32,
        Err(()) => 2,
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_solution() {
    let r = run_state();
    let sel = &r.p.query.select;
    if r.p.query.form == sparql::Form::Select {
        let f = frame(r);
        let row: Vec<u32> = sel.project.iter().map(|&v| f[v as usize]).collect();
        // REDUCED keeps duplicates, as the interpreter does
        if sel.distinct && !r.distinct.add(&row) {
            return;
        }
        st().result.extend_from_slice(&row);
    }
    r.count += 1;
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_triple(sub: u32, p: u32, o: u32) {
    let r = run_state();
    if sub == 0 || p == 0 || o == 0 {
        return;
    }
    let kind = |id: u32| r.ctx.dict.term(id).map_or(0, |t| t.kind);
    if !matches!(kind(sub), dict::IRI | dict::BNODE) || kind(p) != dict::IRI {
        return;
    }
    if r.seen.add(&[sub, p, o]) {
        st().result.extend_from_slice(&[sub, p, o]);
        r.count += 1;
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn cq_end() -> i32 {
    let Some(r) = (unsafe { (*RUN.0.get()).take() }) else { return fail(b"no compiled run") };
    if crate::budget::finish() {
        st().result.clear();
        fail(b"interrupted");
        return -2;
    }
    if r.p.query.form == sparql::Form::Ask {
        return (r.count > 0) as i32;
    }
    r.count as i32
}
