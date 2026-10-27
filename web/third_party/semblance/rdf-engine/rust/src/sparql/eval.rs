//! Evaluation: patterns to solution rows (one term id per variable, 0 = unbound).
//!
//! `eval(p, sub)` evaluates `p` with the bindings in `sub` substituted, which is
//! both how bound query variables work and the semantics of EXISTS and LATERAL.
//! Joins pass rows sideways into "pure" patterns (triples, paths, VALUES, and
//! unions or joins of them), where substitution cannot change the result, and
//! otherwise join independently evaluated sides.

use super::expr::{R, Val};
use super::regex::Regex;
use super::*;
use crate::dict::{self, DEFAULT_GRAPH_ID, Dict, Key, NONE};
use crate::store::Store;
use crate::idmap::{IdMap, bytes_key};
use alloc::rc::Rc;
use core::cmp::Ordering;

pub type Row = Vec<u32>;

/// Compiled regexes by pattern and flags (an index into the list), kept
/// across queries: a rule's query runs once per focus, with the same patterns.
struct Regexes(core::cell::UnsafeCell<(IdMap, Vec<Rc<Regex>>)>);

// Wasm without threads: there is one caller at a time.
unsafe impl Sync for Regexes {}

static REGEXES: Regexes = Regexes(core::cell::UnsafeCell::new((IdMap::EMPTY, Vec::new())));

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Active {
    Default,
    Named(u32),
}

pub struct Ctx<'a> {
    pub dict: &'a mut Dict,
    pub store: &'a Store,
    /// The graphs whose union is the default graph.
    pub default: Vec<u32>,
    /// The named graphs; `None`: every graph in the store.
    pub named: Option<Vec<u32>>,
    pub base: &'a [u8],
    pub nvars: usize,
    pub now_ms: f64,
    /// Datatype kinds by datatype id (`Dt as u32`).
    pub dt_cache: IdMap,
    /// `xsd(...)` ids by local name (see `xsd`); NONE until first used.
    pub xsd_ids: [u32; 6],
    /// BNODE(str) by string, for the solution at `bnode_row` (rows are
    /// extended in place, so a solution keeps its address while its
    /// expressions are evaluated).
    bnodes: IdMap,
    bnode_row: usize,
    scan: Vec<u32>,
    /// Per pattern (by address): the variables a join may pass into it.
    side: IdMap,
    side_list: Vec<Option<Rc<[bool]>>>,
    /// The active graph of the pattern being evaluated (for EXISTS in expressions).
    active: Active,
}

impl<'a> Ctx<'a> {
    pub fn new(dict: &'a mut Dict, store: &'a Store, default: Vec<u32>, named: Option<Vec<u32>>, base: &'a [u8], nvars: usize) -> Self {
        Ctx {
            dict,
            store,
            default,
            named,
            base,
            nvars,
            now_ms: crate::host::now(),
            dt_cache: IdMap::new(),
            xsd_ids: [NONE; 6],
            bnodes: IdMap::new(),
            bnode_row: 0,
            scan: Vec::new(),
            side: IdMap::new(),
            side_list: Vec::new(),
            active: Active::Default,
        }
    }

    pub fn random(&mut self) -> f64 {
        crate::host::random()
    }

    pub fn uuid(&mut self) -> Vec<u8> {
        let mut b = [0u8; 16];
        for x in b.iter_mut() {
            *x = (self.random() * 256.0) as u8;
        }
        b[6] = (b[6] & 0x0f) | 0x40;
        b[8] = (b[8] & 0x3f) | 0x80;
        let mut out = Vec::with_capacity(36);
        for (i, x) in b.iter().enumerate() {
            if matches!(i, 4 | 6 | 8 | 10) {
                out.push(b'-');
            }
            out.push(b"0123456789abcdef"[(x >> 4) as usize]);
            out.push(b"0123456789abcdef"[(x & 15) as usize]);
        }
        out
    }

    pub fn now_lexical(&self) -> Vec<u8> {
        let ms = self.now_ms as i64;
        let (days, rem) = (ms.div_euclid(86_400_000), ms.rem_euclid(86_400_000));
        let (y, m, d) = super::expr::civil_from_days(days);
        let mut out = Vec::new();
        let two = |out: &mut Vec<u8>, n: u32| {
            out.push(b'0' + (n / 10) as u8);
            out.push(b'0' + (n % 10) as u8);
        };
        super::num::write_int(&mut out, y as i128);
        out.push(b'-');
        two(&mut out, m);
        out.push(b'-');
        two(&mut out, d);
        out.push(b'T');
        two(&mut out, (rem / 3_600_000) as u32);
        out.push(b':');
        two(&mut out, (rem / 60_000 % 60) as u32);
        out.push(b':');
        two(&mut out, (rem / 1000 % 60) as u32);
        out.push(b'.');
        let msec = (rem % 1000) as u32;
        out.push(b'0' + (msec / 100) as u8);
        two(&mut out, msec % 100);
        out.push(b'Z');
        out
    }

    pub fn regex(&mut self, pattern: &[u8], flags: &[u8]) -> R<Rc<Regex>> {
        let mut key = bytes_key(pattern);
        key.extend(bytes_key(flags));
        // Wasm without threads: one caller at a time, and nothing here re-enters.
        let c = unsafe { &mut *REGEXES.0.get() };
        if let Some(i) = c.0.get(&key) {
            return Ok(c.1[i as usize].clone());
        }
        let r = Rc::new(super::regex::compile(pattern, flags).ok_or(())?);
        if c.1.len() >= 256 {
            *c = (IdMap::new(), Vec::new());
        }
        c.1.push(r.clone());
        c.0.insert(&key, c.1.len() as u32 - 1);
        Ok(r)
    }

    /// The next BNODE(str) starts a new solution's nodes (compiled queries
    /// evaluate every solution in the same frame).
    pub fn bnode_new(&mut self) {
        self.bnode_row = 0;
    }

    pub fn bnode_for(&mut self, row: &[u32], s: &[u8]) -> u32 {
        if row.as_ptr() as usize != self.bnode_row {
            self.bnodes = IdMap::new();
            self.bnode_row = row.as_ptr() as usize;
        }
        let key = bytes_key(s);
        if let Some(b) = self.bnodes.get(&key) {
            return b;
        }
        let b = self.dict.fresh_bnode();
        self.bnodes.insert(&key, b);
        b
    }

    pub fn exists(&mut self, p: &Pattern, row: &[u32]) -> bool {
        let active = self.active;
        !self.eval(p, row, active).is_empty()
    }



    pub fn named_graphs(&self) -> Vec<u32> {
        match &self.named {
            Some(n) => n.clone(),
            None => self.store.graphs().into_iter().filter(|&g| g != DEFAULT_GRAPH_ID).collect(),
        }
    }

    // --- patterns ----------------------------------------------------------------

    pub fn eval(&mut self, p: &Pattern, sub: &[u32], active: Active) -> Vec<Row> {
        let outer = core::mem::replace(&mut self.active, active);
        let rows = self.eval_in(p, sub, active);
        self.active = outer;
        rows
    }

    fn eval_in(&mut self, p: &Pattern, sub: &[u32], active: Active) -> Vec<Row> {
        match p {
            Pattern::Unit => alloc::vec![sub.to_vec()],
            Pattern::Bgp(ts) => self.bgp(ts, sub, active),
            Pattern::Join(a, b) => {
                let left = self.eval(a, sub, active);
                if left.is_empty() {
                    return left;
                }
                let Some(safe) = self.sideways(b) else {
                    let mut out = Vec::new();
                    for r in &left {
                        out.extend(self.eval(b, r, active));
                    }
                    return out;
                };
                if !left.iter().any(|r| useful(r, sub, &safe)) {
                    let right = self.eval(b, sub, active);
                    return join_rows(left, &right);
                }
                let (mut whole, mut out) = (None, Vec::new());
                for r in &left {
                    self.join_one(b, sub, r, &safe, &mut whole, active, &mut out);
                }
                out
            }
            Pattern::LeftJoin(a, b, f) => {
                let left = self.eval(a, sub, active);
                let mut out = Vec::new();
                let safe = self.sideways(b);
                let (mut whole, mut ms) = (None, Vec::new());
                for r in left {
                    match &safe {
                        None => ms = self.eval(b, &r, active),
                        Some(safe) => {
                            ms.clear();
                            // join_one counts each pair it tries as a budget step.
                            self.join_one(b, sub, &r, safe, &mut whole, active, &mut ms);
                        }
                    }
                    let mut any = false;
                    for m in ms.drain(..) {
                        // An OPTIONAL pair is a budget step, as a join pair is (and as in WAT).
                        if safe.is_none() && crate::budget::step() {
                            break;
                        }
                        if f.as_ref().is_none_or(|f| self.test(f, &m)) {
                            out.push(m);
                            any = true;
                        }
                    }
                    if !any {
                        out.push(r);
                    }
                }
                out
            }
            Pattern::Filter(f, a) => {
                // A filter that reads only bound variables, which `a` keeps,
                // has one answer for every row: decide it before evaluating `a`.
                if let Some(reads) = self.early(p) {
                    if reads.iter().zip(sub).all(|(&r, &x)| !r || x != NONE) {
                        return if self.test(f, sub) { self.eval(a, sub, active) } else { Vec::new() };
                    }
                }
                let rows = self.eval(a, sub, active);
                rows.into_iter().filter(|r| self.test(f, r)).collect()
            }
            Pattern::Union(a, b) => {
                let mut rows = self.eval(a, sub, active);
                rows.extend(self.eval(b, sub, active));
                rows
            }
            Pattern::Minus(a, b) => {
                let left = self.eval(a, sub, active);
                let right = self.eval(b, sub, active);
                left.into_iter()
                    .filter(|l| {
                        !right.iter().any(|r| {
                            let mut shared = false;
                            for (x, y) in l.iter().zip(r) {
                                if *x != NONE && *y != NONE {
                                    if x != y {
                                        return false;
                                    }
                                    shared = true;
                                }
                            }
                            shared
                        })
                    })
                    .collect()
            }
            Pattern::Graph(g, a) => {
                let mut out = Vec::new();
                match resolve(g, sub) {
                    Some(id) => {
                        if self.named_graphs().contains(&id) {
                            out = self.eval(a, sub, Active::Named(id));
                        }
                    }
                    None => {
                        let Tv::Var(v) = g else { return out };
                        for id in self.named_graphs() {
                            for mut r in self.eval(a, sub, Active::Named(id)) {
                                let slot = &mut r[*v as usize];
                                if *slot == NONE || *slot == id {
                                    *slot = id;
                                    out.push(r);
                                }
                            }
                        }
                    }
                }
                out
            }
            Pattern::Extend(a, v, e) => {
                let mut rows = self.eval(a, sub, active);
                // A BIND cannot name a variable of its own group, so a value already in the
                // row came from the row it is joined with: the row stays only if they agree.
                let mut keep = 0;
                for i in 0..rows.len() {
                    if let Ok(val) = self.expr(e, &rows[i]) {
                        let id = self.to_id(&val);
                        let at = &mut rows[i][*v as usize];
                        if *at != NONE && *at != id {
                            continue;
                        }
                        *at = id;
                    }
                    rows.swap(keep, i);
                    keep += 1;
                }
                rows.truncate(keep);
                rows
            }
            Pattern::Values(vars, data) => {
                let mut out = Vec::new();
                'rows: for d in data {
                    let mut r = sub.to_vec();
                    for (v, &t) in vars.iter().zip(d) {
                        if t == NONE {
                            continue;
                        }
                        let slot = &mut r[*v as usize];
                        if *slot != NONE && *slot != t {
                            continue 'rows;
                        }
                        *slot = t;
                    }
                    out.push(r);
                }
                out
            }
            Pattern::Lateral(a, b) => {
                let left = self.eval(a, sub, active);
                let mut out = Vec::new();
                for r in &left {
                    for mut x in self.eval(b, r, active) {
                        if merge_into(&mut x, r) {
                            out.push(x);
                        }
                    }
                }
                out
            }
            Pattern::Sub(s) => {
                // The row's bindings stay with the subquery's solutions (projection drops
                // them), so a filter of the enclosing group still reads them: in a LATERAL,
                // `{ FILTER(?outer) { SELECT ... } }` keeps the rows ?outer allows.
                let mut rows = self.select(s, sub, active);
                if sub.iter().any(|&x| x != NONE) {
                    rows.retain_mut(|r| merge_into(r, sub));
                }
                rows
            }
            Pattern::Service(silent) => {
                if *silent {
                    alloc::vec![sub.to_vec()]
                } else {
                    Vec::new()
                }
            }
        }
    }

    /// None when `b` is pure (a row may pass it every binding); otherwise, for
    /// each variable, whether a left row may pass its binding into `b` (`b`
    /// mentions it and substituting it is safe). Cached per pattern.
    fn sideways(&mut self, b: &Pattern) -> Option<Rc<[bool]>> {
        let key = [b as *const Pattern as usize as u32];
        if let Some(i) = self.side.get(&key) {
            return self.side_list[i as usize].clone();
        }
        let safe: Option<Rc<[bool]>> = side_safe(b, self.nvars).map(Into::into);
        self.side_list.push(safe.clone());
        self.side.insert(&key, self.side_list.len() as u32 - 1);
        safe
    }

    /// For FILTER(f, a): the variables `f` reads, when it reads nothing else
    /// (no EXISTS) and `a` keeps each one as substituted; cached per pattern.
    fn early(&mut self, p: &Pattern) -> Option<Rc<[bool]>> {
        let Pattern::Filter(f, a) = p else { return None };
        let key = [p as *const Pattern as usize as u32];
        if let Some(i) = self.side.get(&key) {
            return self.side_list[i as usize].clone();
        }
        let mut m = alloc::vec![false; self.nvars];
        expr_vars(f, &mut m);
        let ok = !has_exists(f) && m.iter().enumerate().all(|(v, &r)| !r || keeps(a, v as Var));
        let reads = if ok { Some(m.into()) } else { None };
        self.side_list.push(reads.clone());
        self.side.insert(&key, self.side_list.len() as u32 - 1);
        reads
    }

    /// Join one left row with `b`: evaluated with the row's safe bindings
    /// substituted, or (when it has none) from `b` evaluated once, unsubstituted.
    #[allow(clippy::too_many_arguments)]
    fn join_one(&mut self, b: &Pattern, sub: &[u32], r: &[u32], safe: &[bool], whole: &mut Option<Vec<Row>>, active: Active, out: &mut Vec<Row>) {
        if useful(r, sub, safe) {
            let s: Row = sub.iter().zip(r).zip(safe).map(|((&x, &y), &ok)| if x == NONE && ok { y } else { x }).collect();
            for mut x in self.eval(b, &s, active) {
                if crate::budget::step() {
                    return;
                }
                if merge_into(&mut x, r) {
                    out.push(x);
                }
            }
        } else {
            let right = whole.get_or_insert_with(|| self.eval(b, sub, active));
            for x in right.iter() {
                // Each pair tried is a budget step, as in join_rows.
                if crate::budget::step() {
                    return;
                }
                if let Some(m) = merge(r, x) {
                    out.push(m);
                }
            }
        }
    }

    pub(crate) fn test(&mut self, f: &Expr, row: &[u32]) -> bool {
        self.expr(f, row).and_then(|v| self.ebv(&v)) == Ok(true)
    }

    /// A basic graph pattern: index nested loops, most-bound pattern first.
    fn bgp(&mut self, ts: &[Triple], sub: &[u32], active: Active) -> Vec<Row> {
        let mut bound: Vec<bool> = sub.iter().map(|&x| x != NONE).collect();
        let mut left: Vec<&Triple> = ts.iter().collect();
        let mut rows = alloc::vec![sub.to_vec()];
        while !left.is_empty() {
            let score = |t: &Triple, bound: &[bool]| {
                let b = |x: &Tv, w: u32| match x {
                    Tv::Term(_) => w,
                    Tv::Var(v) if bound[*v as usize] => w,
                    _ => 0,
                };
                // A path is costlier than a triple with the same ends bound.
                match t.path {
                    Some(_) => (b(&t.s, 4) + b(&t.o, 3)).saturating_sub(1),
                    None => b(&t.s, 4) + b(&t.o, 3) + b(&t.p, 1),
                }
            };
            let (i, _) = left.iter().enumerate().max_by_key(|(i, t)| (score(t, &bound), usize::MAX - i)).unwrap();
            let t = left.remove(i);
            let mut next = Vec::new();
            for r in &rows {
                match &t.path {
                    Some(p) => next.extend(self.path(&t.s, p, &t.o, r, active)),
                    None => self.triple(t, r, active, &mut next),
                }
            }
            rows = next;
            if rows.is_empty() {
                return rows;
            }
            for x in [&t.s, &t.p, &t.o] {
                mark(x, &mut bound);
            }
        }
        rows
    }

    /// Extend `row` with every match of one triple pattern.
    fn triple(&mut self, t: &Triple, row: &[u32], active: Active, out: &mut Vec<Row>) {
        // The id to scan for at a position: 0 when free; None when it cannot match.
        let pos = |x: &Tv, me: &Self| -> Option<u32> {
            match x {
                Tv::Term(i) => Some(*i),
                Tv::Var(v) => Some(row[*v as usize]),
                Tv::Triple(b) => match (resolve(&b[0], row), resolve(&b[1], row), resolve(&b[2], row)) {
                    // A ground triple term matches only if it was ever interned.
                    (Some(s), Some(p), Some(o)) => match me.dict.get(Key::triple(s, p, o)) {
                        NONE => None,
                        id => Some(id),
                    },
                    _ => Some(NONE),
                },
            }
        };
        let (Some(s), Some(p), Some(o)) = (pos(&t.s, self), pos(&t.p, self), pos(&t.o, self)) else { return };
        let mut one = [0];
        let graphs = graphs(&self.default, active, &mut one);
        let dedupe = graphs.len() > 1;
        let (mut seen, mut spare) = (IdMap::new(), None::<Row>);
        let mut scan = core::mem::take(&mut self.scan);
        for &g in graphs {
            scan.clear();
            self.store.scan([s, p, o, g], &mut scan);
            for q in scan.chunks_exact(4) {
                if crate::budget::step() {
                    break;
                }
                if dedupe && !seen.add(&q[..3]) {
                    continue;
                }
                // A row that failed to bind is reused for the next quad.
                let mut r = match spare.take() {
                    Some(mut r) => {
                        r.copy_from_slice(row);
                        r
                    }
                    None => row.to_vec(),
                };
                if bind(&t.s, q[0], &mut r, self.dict) && bind(&t.p, q[1], &mut r, self.dict) && bind(&t.o, q[2], &mut r, self.dict) {
                    out.push(r);
                } else {
                    spare = Some(r);
                }
            }
        }
        self.scan = scan;
    }

    // --- property paths ------------------------------------------------------------

    pub(crate) fn path(&mut self, s: &Tv, path: &Path, o: &Tv, sub: &[u32], active: Active) -> Vec<Row> {
        let (sb, ob) = (resolve(s, sub), resolve(o, sub));
        let pairs = self.pairs(path, sb, ob, active);
        let mut out = Vec::new();
        for (x, y) in pairs {
            // A zero-length match at a variable endpoint must be a node of the graph.
            // (A constant endpoint matches itself even outside the graph.)
            let constant = matches!(s, Tv::Term(_)) || matches!(o, Tv::Term(_));
            if x == y && !constant && !self.is_node(x, active) {
                continue;
            }
            let mut r = sub.to_vec();
            if bind(s, x, &mut r, self.dict) && bind(o, y, &mut r, self.dict) {
                out.push(r);
            }
        }
        out
    }

    fn scan_pairs(&mut self, s: u32, p: u32, o: u32, active: Active, out: &mut Vec<(u32, u32, u32)>) {
        let mut one = [0];
        let graphs = graphs(&self.default, active, &mut one);
        let dedupe = graphs.len() > 1;
        let mut seen = IdMap::new();
        let mut scan = core::mem::take(&mut self.scan);
        for &g in graphs {
            scan.clear();
            self.store.scan([s, p, o, g], &mut scan);
            for q in scan.chunks_exact(4) {
                if !dedupe || seen.add(&q[..3]) {
                    out.push((q[0], q[1], q[2]));
                }
            }
        }
        self.scan = scan;
    }

    /// The (start, end) pairs of a path, with either end optionally fixed.
    fn pairs(&mut self, path: &Path, s: Option<u32>, o: Option<u32>, active: Active) -> Vec<(u32, u32)> {
        let (s0, o0) = (s.unwrap_or(NONE), o.unwrap_or(NONE));
        match path {
            Path::Iri(p) => {
                let mut t = Vec::new();
                self.scan_pairs(s0, *p, o0, active, &mut t);
                t.into_iter().map(|(a, _, b)| (a, b)).collect()
            }
            Path::Inverse(q) => self.pairs(q, o, s, active).into_iter().map(|(a, b)| (b, a)).collect(),
            Path::Alt(a, b) => {
                let mut v = self.pairs(a, s, o, active);
                v.extend(self.pairs(b, s, o, active));
                v
            }
            Path::Seq(a, b) => {
                let mut out = Vec::new();
                // Each middle node's continuations, computed once.
                let (mut index, mut cached) = (IdMap::new(), Vec::<Vec<(u32, u32)>>::new());
                if s.is_some() || o.is_none() {
                    for (x, m) in self.pairs(a, s, None, active) {
                        let i = match index.get(&[m]) {
                            Some(i) => i as usize,
                            None => {
                                cached.push(self.pairs(b, Some(m), o, active));
                                index.insert(&[m], cached.len() as u32 - 1);
                                cached.len() - 1
                            }
                        };
                        out.extend(cached[i].iter().map(|&(_, y)| (x, y)));
                    }
                } else {
                    for (m, y) in self.pairs(b, None, o, active) {
                        let i = match index.get(&[m]) {
                            Some(i) => i as usize,
                            None => {
                                cached.push(self.pairs(a, None, Some(m), active));
                                index.insert(&[m], cached.len() as u32 - 1);
                                cached.len() - 1
                            }
                        };
                        out.extend(cached[i].iter().map(|&(x, _)| (x, y)));
                    }
                }
                out
            }
            Path::ZeroOrOne(q) => {
                let mut seen = IdMap::new();
                let mut out = Vec::new();
                let zero: Vec<(u32, u32)> = self.zero_nodes(s, o, active).into_iter().map(|n| (n, n)).collect();
                for (a, b) in zero.into_iter().chain(self.pairs(q, s, o, active)) {
                    if seen.add(&[a, b]) {
                        out.push((a, b));
                    }
                }
                out
            }
            Path::ZeroOrMore(q) | Path::OneOrMore(q) => {
                let zero = matches!(path, Path::ZeroOrMore(_));
                let mut out = Vec::new();
                if let Some(start) = s {
                    for n in self.closure(q, start, zero, false, active) {
                        if o.is_none_or(|o| o == n) {
                            out.push((start, n));
                        }
                    }
                } else if let Some(end) = o {
                    for n in self.closure(q, end, zero, true, active) {
                        out.push((n, end));
                    }
                } else {
                    for n in self.nodes(active) {
                        for m in self.closure(q, n, zero, false, active) {
                            out.push((n, m));
                        }
                    }
                }
                out
            }
            Path::Negated(fwd, inv) => {
                let mut out = Vec::new();
                if !fwd.is_empty() || inv.is_empty() {
                    let mut t = Vec::new();
                    self.scan_pairs(s0, NONE, o0, active, &mut t);
                    out.extend(t.into_iter().filter(|(_, p, _)| !fwd.contains(p)).map(|(a, _, b)| (a, b)));
                }
                if !inv.is_empty() {
                    let mut t = Vec::new();
                    self.scan_pairs(o0, NONE, s0, active, &mut t);
                    out.extend(t.into_iter().filter(|(_, p, _)| !inv.contains(p)).map(|(a, _, b)| (b, a)));
                }
                out
            }
        }
    }

    fn is_node(&mut self, n: u32, active: Active) -> bool {
        let mut scan = core::mem::take(&mut self.scan);
        let mut found = false;
        let mut one = [0];
        for &g in graphs(&self.default, active, &mut one) {
            scan.clear();
            self.store.scan([n, 0, 0, g], &mut scan);
            if scan.is_empty() {
                self.store.scan([0, 0, n, g], &mut scan);
            }
            if !scan.is_empty() {
                found = true;
                break;
            }
        }
        self.scan = scan;
        found
    }

    /// Nodes a zero-length path can start (and end) at.
    fn zero_nodes(&mut self, s: Option<u32>, o: Option<u32>, active: Active) -> Vec<u32> {
        match (s, o) {
            (Some(a), Some(b)) => if a == b { alloc::vec![a] } else { Vec::new() },
            (Some(a), None) | (None, Some(a)) => alloc::vec![a],
            (None, None) => self.nodes(active),
        }
    }

    fn nodes(&mut self, active: Active) -> Vec<u32> {
        let mut t = Vec::new();
        self.scan_pairs(NONE, NONE, NONE, active, &mut t);
        let mut seen = IdMap::new();
        let mut out = Vec::new();
        for (a, _, b) in t {
            for n in [a, b] {
                if seen.add(&[n]) {
                    out.push(n);
                }
            }
        }
        out
    }

    /// Nodes reachable from `start` by one or more steps of `q` (or zero, with `zero`).
    fn closure(&mut self, q: &Path, start: u32, zero: bool, backwards: bool, active: Active) -> Vec<u32> {
        let mut seen = IdMap::new();
        let mut out = Vec::new();
        if zero {
            seen.add(&[start]);
            out.push(start);
        }
        let mut frontier = alloc::vec![start];
        while let Some(n) = frontier.pop() {
            if crate::budget::step() {
                break;
            }
            let next = if backwards { self.pairs(q, None, Some(n), active).into_iter().map(|(a, _)| a).collect::<Vec<_>>() } else { self.pairs(q, Some(n), None, active).into_iter().map(|(_, b)| b).collect() };
            for m in next {
                if seen.add(&[m]) {
                    out.push(m);
                    frontier.push(m);
                }
            }
        }
        out
    }

    // --- queries -------------------------------------------------------------------

    pub fn select(&mut self, s: &Select, sub: &[u32], active: Active) -> Vec<Row> {
        let rows = self.eval(&s.pattern, sub, active);
        self.select_rows(s, rows, active)
    }

    /// What follows a SELECT's WHERE, on its solutions (a compiled subquery's
    /// too): grouping, VALUES, SELECT expressions, ORDER BY, projection, slice.
    pub fn select_rows(&mut self, s: &Select, mut rows: Vec<Row>, active: Active) -> Vec<Row> {
        if s.group.is_some() || !s.aggregates.is_empty() {
            rows = self.group(s, rows);
            rows.retain(|r| s.having.iter().all(|h| self.test_owned(h, r)));
        }
        if let Some(v) = &s.values {
            let data = self.eval(v, &alloc::vec![NONE; self.nvars], active);
            rows = join_rows(rows, &data);
        }
        // Row by row, so BNODE(str) gives one node per string in each solution.
        for r in rows.iter_mut() {
            for (v, e) in &s.extend {
                if let Ok(val) = self.expr(e, r) {
                    r[*v as usize] = self.to_id(&val);
                }
            }
        }
        if !s.order.is_empty() {
            let mut keyed: Vec<(Vec<Option<Val>>, Row)> = rows.into_iter().map(|r| (s.order.iter().map(|(e, _)| self.expr(e, &r).ok()).collect(), r)).collect();
            keyed.sort_unstable_by(|(a, _), (b, _)| {
                for (i, (_, desc)) in s.order.iter().enumerate() {
                    let o = self.sort_cmp(a[i].as_ref(), b[i].as_ref());
                    if o != Ordering::Equal {
                        return if *desc { o.reverse() } else { o };
                    }
                }
                Ordering::Equal
            });
            rows = keyed.into_iter().map(|(_, r)| r).collect();
        }
        // Projection: keep only the projected variables.
        let mut keep = alloc::vec![false; self.nvars];
        for &v in &s.project {
            keep[v as usize] = true;
        }
        for r in rows.iter_mut() {
            for (i, x) in r.iter_mut().enumerate() {
                if !keep[i] {
                    *x = NONE;
                }
            }
        }
        // REDUCED may keep duplicates; it does, as the WAT build does.
        if s.distinct {
            let mut seen = IdMap::new();
            rows.retain(|r| seen.add(r));
        }
        let rows: Vec<Row> = rows.into_iter().skip(s.offset).take(s.limit.unwrap_or(usize::MAX)).collect();
        rows
    }

    fn test_owned(&mut self, f: &Expr, row: &[u32]) -> bool {
        self.test(f, row)
    }

    fn group(&mut self, s: &Select, rows: Vec<Row>) -> Vec<Row> {
        let keys = s.group.as_deref().unwrap_or(&[]);
        // Groups in order of first appearance.
        let mut index = IdMap::new();
        let mut groups: Vec<(Row, Vec<Row>)> = Vec::new();
        for r in rows {
            let key: Row = keys.iter().map(|(e, _)| self.expr(e, &r).map(|v| self.to_id(&v)).unwrap_or(NONE)).collect();
            let (i, new) = index.insert(&key, groups.len() as u32);
            if new {
                groups.push((key, Vec::new()));
            }
            groups[i as usize].1.push(r);
        }
        if keys.is_empty() && groups.is_empty() {
            groups.push((Vec::new(), Vec::new()));
        }
        let mut out = Vec::new();
        for (key, members) in groups {
            let mut r = alloc::vec![NONE; self.nvars];
            for ((e, v), k) in keys.iter().zip(&key) {
                match (e, v) {
                    (_, Some(v)) => r[*v as usize] = *k,
                    (Expr::Var(v), None) => r[*v as usize] = *k,
                    _ => {}
                }
            }
            for (v, agg) in &s.aggregates {
                r[*v as usize] = self.aggregate(agg, &members).map_or(NONE, |val| self.to_id(&val));
            }
            out.push(r);
        }
        out
    }

    fn aggregate(&mut self, a: &Aggregate, rows: &[Row]) -> R<Val> {
        use super::num::Num;
        let Some(e) = &a.expr else {
            let n = if a.distinct {
                let mut seen = IdMap::new();
                rows.iter().filter(|r| seen.add(r)).count()
            } else {
                rows.len()
            };
            return Ok(Val::Num(Num::Int(n as i128)));
        };
        let mut vals: Vec<R<Val>> = rows.iter().map(|r| self.expr(e, r)).collect();
        if a.distinct {
            let mut seen = IdMap::new();
            let mut kept = Vec::new();
            for v in vals {
                match v {
                    Ok(v) => {
                        let id = self.to_id(&v);
                        if seen.add(&[id]) {
                            kept.push(Ok(Val::Id(id)));
                        }
                    }
                    Err(()) => kept.push(Err(())),
                }
            }
            vals = kept;
        }
        match a.kind {
            AggKind::Count => Ok(Val::Num(Num::Int(vals.iter().filter(|v| v.is_ok()).count() as i128))),
            AggKind::Sum | AggKind::Avg => {
                let mut total = Num::Int(0);
                let mut n = 0i128;
                for v in vals {
                    let x = self.num(&v?).ok_or(())?;
                    total = super::num::arith(Op::Add, total, x).ok_or(())?;
                    n += 1;
                }
                if a.kind == AggKind::Sum || n == 0 {
                    return Ok(Val::Num(total));
                }
                super::num::arith(Op::Div, total, Num::Int(n)).map(Val::Num).ok_or(())
            }
            AggKind::Min | AggKind::Max => {
                let mut best: Option<Val> = None;
                for v in vals.into_iter().flatten() {
                    best = Some(match best {
                        None => v,
                        Some(b) => {
                            let o = self.sort_cmp(Some(&v), Some(&b));
                            if (a.kind == AggKind::Min) == (o == Ordering::Less) && o != Ordering::Equal { v } else { b }
                        }
                    });
                }
                best.ok_or(())
            }
            AggKind::Sample => vals.into_iter().flatten().next().ok_or(()),
            AggKind::GroupConcat => {
                let mut out = Vec::new();
                for (i, v) in vals.into_iter().enumerate() {
                    let (s, _) = self.string(&v?)?;
                    if i > 0 {
                        out.extend_from_slice(&a.separator);
                    }
                    out.extend_from_slice(&s);
                }
                Ok(Val::Str(out, None))
            }
        }
    }
}

/// Whether evaluating with extra bindings substituted equals joining with them.
/// True for matching (triples, paths, VALUES, and joins and unions of them),
/// and for BIND / FILTER over such a pattern when the expression only reads
/// variables the pattern always binds: a substituted value then equals the
/// pattern's own, so the expression cannot tell.
/// What `sideways` gives, uncached: None when `b` is pure, else per variable
/// whether a left row may pass its binding into `b`.
pub(crate) fn side_safe(b: &Pattern, nvars: usize) -> Option<Vec<bool>> {
    if pure(b) {
        return None;
    }
    let mut m = alloc::vec![false; nvars];
    pattern_vars(b, &mut m);
    for (v, x) in m.iter_mut().enumerate() {
        *x = *x && subst_ok(b, v as Var);
    }
    Some(m)
}

pub(crate) fn pure(p: &Pattern) -> bool {
    match p {
        Pattern::Unit | Pattern::Bgp(_) | Pattern::Values(..) => true,
        Pattern::Join(a, b) | Pattern::Union(a, b) => pure(a) && pure(b),
        Pattern::Graph(_, a) => pure(a),
        Pattern::Extend(a, _, e) | Pattern::Filter(e, a) => pure(a) && reads_only(e, &certain(a)),
        _ => false,
    }
}

/// Variables a pattern binds in every solution.
fn certain(p: &Pattern) -> Vec<Var> {
    let mut out = Vec::new();
    match p {
        Pattern::Bgp(ts) => {
            for t in ts {
                for x in [&t.s, &t.p, &t.o] {
                    tv_vars(x, &mut out);
                }
            }
        }
        Pattern::Join(a, b) | Pattern::Lateral(a, b) => {
            out = certain(a);
            out.extend(certain(b));
        }
        Pattern::LeftJoin(a, ..) | Pattern::Minus(a, _) => out = certain(a),
        Pattern::Union(a, b) => {
            let right = certain(b);
            out = certain(a).into_iter().filter(|v| right.contains(v)).collect();
        }
        Pattern::Graph(g, a) => {
            out = certain(a);
            tv_vars(g, &mut out);
        }
        Pattern::Extend(a, _, _) | Pattern::Filter(_, a) => out = certain(a),
        Pattern::Values(vars, rows) => out = vars.iter().enumerate().filter(|(i, _)| rows.iter().all(|r| r[*i] != NONE)).map(|(_, v)| *v).collect(),
        _ => {}
    }
    out
}

/// Whether a left row's binding of `v` may be substituted into `p`: evaluating
/// with it then merging equals evaluating without it and joining. Matching
/// patterns take any binding; an expression must not read `v` unless the
/// pattern under it always binds `v` (then it reads the same value either way).
/// MINUS compares domains, which substitution changes, and a subquery scopes
/// its own variables, so neither takes a variable it mentions.
fn subst_ok(p: &Pattern, v: Var) -> bool {
    let sure = |a: &Pattern| certain(a).contains(&v);
    match p {
        Pattern::Unit | Pattern::Bgp(_) | Pattern::Values(..) | Pattern::Service(_) => true,
        Pattern::Join(a, b) | Pattern::Union(a, b) => subst_ok(a, v) && subst_ok(b, v),
        Pattern::Graph(_, a) => subst_ok(a, v),
        Pattern::Filter(e, a) => subst_ok(a, v) && (!reads(e, v) || sure(a)),
        Pattern::Extend(a, w, e) => *w != v && subst_ok(a, v) && (!reads(e, v) || sure(a)),
        Pattern::LeftJoin(a, b, f) => {
            subst_ok(a, v) && subst_ok(b, v) && (sure(a) || (!mentions(b, v) && f.as_ref().is_none_or(|f| !reads(f, v))))
        }
        Pattern::Lateral(a, b) => subst_ok(a, v) && subst_ok(b, v) && (sure(a) || !mentions(b, v)),
        Pattern::Sub(s) => {
            let mut m = alloc::vec![false; v as usize + 1];
            select_vars(s, &mut m);
            !m[v as usize] && subst_ok(&s.pattern, v)
        }
        Pattern::Minus(..) => false,
    }
}

/// Whether every row of `p`, evaluated with `v` substituted, has `v` as substituted:
/// nothing in it rebinds `v`, and no subquery projects it away.
fn keeps(p: &Pattern, v: Var) -> bool {
    match p {
        Pattern::Unit | Pattern::Bgp(_) | Pattern::Values(..) | Pattern::Service(_) => true,
        Pattern::Join(a, b) | Pattern::Union(a, b) | Pattern::Minus(a, b) | Pattern::Lateral(a, b) | Pattern::LeftJoin(a, b, _) => keeps(a, v) && keeps(b, v),
        Pattern::Graph(_, a) | Pattern::Filter(_, a) => keeps(a, v),
        Pattern::Extend(a, w, _) => *w != v && keeps(a, v),
        Pattern::Sub(_) => false,
    }
}

/// Whether an expression has EXISTS, or a function whose value differs per row
/// whatever the row binds (RAND, BNODE, UUID).
fn has_exists(e: &Expr) -> bool {
    match e {
        Expr::Exists(..) => true,
        Expr::Call(Func::Rand | Func::Bnode | Func::Uuid | Func::StrUuid, _) => true,
        Expr::Var(_) | Expr::Bound(_) | Expr::Const(_) | Expr::Unknown => false,
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => has_exists(a) || has_exists(b),
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => has_exists(a),
        Expr::In(a, l, _) => has_exists(a) || l.iter().any(has_exists),
        Expr::Call(_, l) | Expr::Coalesce(l) => l.iter().any(has_exists),
        Expr::If(a, b, c) => has_exists(a) || has_exists(b) || has_exists(c),
    }
}

/// Whether an expression reads `v` (EXISTS is taken to read every variable).
fn reads(e: &Expr, v: Var) -> bool {
    match e {
        Expr::Var(w) | Expr::Bound(w) => *w == v,
        Expr::Const(_) | Expr::Unknown => false,
        Expr::Exists(..) => true,
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => reads(a, v) || reads(b, v),
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => reads(a, v),
        Expr::In(a, l, _) => reads(a, v) || l.iter().any(|x| reads(x, v)),
        Expr::Call(_, l) | Expr::Coalesce(l) => l.iter().any(|x| reads(x, v)),
        Expr::If(a, b, c) => reads(a, v) || reads(b, v) || reads(c, v),
    }
}

fn mentions(p: &Pattern, v: Var) -> bool {
    let mut m = alloc::vec![false; v as usize + 1];
    pattern_vars(p, &mut m);
    m[v as usize]
}

/// Mark every variable a pattern mentions (in `m`, when it is in range).
fn pattern_vars(p: &Pattern, m: &mut [bool]) {
    fn tv(t: &Tv, m: &mut [bool]) {
        match t {
            Tv::Var(v) => set(*v, m),
            Tv::Triple(b) => b.iter().for_each(|x| tv(x, m)),
            Tv::Term(_) => {}
        }
    }
    match p {
        Pattern::Unit | Pattern::Service(_) => {}
        Pattern::Bgp(ts) => ts.iter().for_each(|t| [&t.s, &t.p, &t.o].iter().for_each(|x| tv(x, m))),
        Pattern::Join(a, b) | Pattern::Union(a, b) | Pattern::Minus(a, b) | Pattern::Lateral(a, b) => {
            pattern_vars(a, m);
            pattern_vars(b, m);
        }
        Pattern::LeftJoin(a, b, e) => {
            pattern_vars(a, m);
            pattern_vars(b, m);
            if let Some(e) = e {
                expr_vars(e, m);
            }
        }
        Pattern::Filter(e, a) => {
            expr_vars(e, m);
            pattern_vars(a, m);
        }
        Pattern::Extend(a, w, e) => {
            set(*w, m);
            expr_vars(e, m);
            pattern_vars(a, m);
        }
        Pattern::Graph(g, a) => {
            tv(g, m);
            pattern_vars(a, m);
        }
        Pattern::Values(vars, _) => vars.iter().for_each(|&v| set(v, m)),
        Pattern::Sub(s) => select_vars(s, m),
    }
}

fn set(v: Var, m: &mut [bool]) {
    if let Some(x) = m.get_mut(v as usize) {
        *x = true;
    }
}

fn select_vars(s: &Select, m: &mut [bool]) {
    let vars = s.project.iter().chain(s.aggregates.iter().map(|(v, _)| v)).chain(s.extend.iter().map(|(v, _)| v));
    vars.chain(s.group.iter().flatten().filter_map(|(_, v)| v.as_ref())).for_each(|&v| set(v, m));
    pattern_vars(&s.pattern, m);
    for (e, _) in s.group.iter().flatten() {
        expr_vars(e, m);
    }
    s.aggregates.iter().filter_map(|(_, a)| a.expr.as_ref()).for_each(|e| expr_vars(e, m));
    s.having.iter().chain(s.extend.iter().map(|(_, e)| e)).chain(s.order.iter().map(|(e, _)| e)).for_each(|e| expr_vars(e, m));
    if let Some(v) = &s.values {
        pattern_vars(v, m);
    }
}

fn expr_vars(e: &Expr, m: &mut [bool]) {
    match e {
        Expr::Var(v) | Expr::Bound(v) => set(*v, m),
        Expr::Const(_) | Expr::Unknown => {}
        Expr::Exists(p, _) => pattern_vars(p, m),
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => {
            expr_vars(a, m);
            expr_vars(b, m);
        }
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => expr_vars(a, m),
        Expr::In(a, l, _) => {
            expr_vars(a, m);
            l.iter().for_each(|x| expr_vars(x, m));
        }
        Expr::Call(_, l) | Expr::Coalesce(l) => l.iter().for_each(|x| expr_vars(x, m)),
        Expr::If(a, b, c) => {
            expr_vars(a, m);
            expr_vars(b, m);
            expr_vars(c, m);
        }
    }
}

fn tv_vars(t: &Tv, out: &mut Vec<Var>) {
    match t {
        Tv::Var(v) => out.push(*v),
        Tv::Triple(b) => b.iter().for_each(|x| tv_vars(x, out)),
        Tv::Term(_) => {}
    }
}

/// Whether an expression reads only `vars` (and has no EXISTS, which sees more).
fn reads_only(e: &Expr, vars: &[Var]) -> bool {
    match e {
        Expr::Var(v) | Expr::Bound(v) => vars.contains(v),
        Expr::Const(_) | Expr::Unknown => true,
        Expr::Exists(..) => false,
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => reads_only(a, vars) && reads_only(b, vars),
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => reads_only(a, vars),
        Expr::In(a, l, _) => reads_only(a, vars) && l.iter().all(|x| reads_only(x, vars)),
        Expr::Call(_, l) | Expr::Coalesce(l) => l.iter().all(|x| reads_only(x, vars)),
        Expr::If(a, b, c) => reads_only(a, vars) && reads_only(b, vars) && reads_only(c, vars),
    }
}

/// The graphs whose union is the active graph.
fn graphs<'s>(default: &'s [u32], active: Active, one: &'s mut [u32; 1]) -> &'s [u32] {
    match active {
        Active::Default => default,
        Active::Named(g) => {
            *one = [g];
            one
        }
    }
}

/// Whether a row binds a variable, beyond `sub`, that may be passed into a pattern.
fn useful(r: &[u32], sub: &[u32], safe: &[bool]) -> bool {
    r.iter().zip(sub).zip(safe).any(|((&x, &s), &ok)| ok && s == NONE && x != NONE)
}

fn mark(x: &Tv, bound: &mut [bool]) {
    match x {
        Tv::Var(v) => bound[*v as usize] = true,
        Tv::Triple(b) => b.iter().for_each(|y| mark(y, bound)),
        Tv::Term(_) => {}
    }
}

/// A position's value under a row, if it is fixed.
pub(crate) fn resolve(x: &Tv, row: &[u32]) -> Option<u32> {
    match x {
        Tv::Term(i) => Some(*i),
        Tv::Var(v) => match row[*v as usize] {
            NONE => None,
            id => Some(id),
        },
        Tv::Triple(_) => None,
    }
}

/// Bind a pattern position to a term; false if it conflicts.
fn bind(x: &Tv, id: u32, row: &mut [u32], dict: &Dict) -> bool {
    match x {
        Tv::Term(t) => *t == id,
        Tv::Var(v) => {
            let slot = &mut row[*v as usize];
            if *slot == NONE {
                *slot = id;
                true
            } else {
                *slot == id
            }
        }
        Tv::Triple(b) => match dict.term(id) {
            Some(t) if t.kind == dict::TRIPLE => {
                let (s, p, o) = (t.a, t.b, t.c);
                bind(&b[0], s, row, dict) && bind(&b[1], p, row, dict) && bind(&b[2], o, row, dict)
            }
            _ => false,
        },
    }
}

/// Merge `a` into `b` in place; false if they conflict.
fn merge_into(b: &mut [u32], a: &[u32]) -> bool {
    for (y, &x) in b.iter_mut().zip(a) {
        if x != NONE {
            if *y == NONE {
                *y = x;
            } else if *y != x {
                return false;
            }
        }
    }
    true
}

pub fn merge(a: &[u32], b: &[u32]) -> Option<Row> {
    let mut r = a.to_vec();
    for (x, &y) in r.iter_mut().zip(b) {
        if y != NONE {
            if *x == NONE {
                *x = y;
            } else if *x != y {
                return None;
            }
        }
    }
    Some(r)
}

/// Join two sets of rows; hashes on a variable bound on both sides throughout.
pub fn join_rows(left: Vec<Row>, right: &[Row]) -> Vec<Row> {
    let mut out = Vec::new();
    if left.is_empty() || right.is_empty() {
        return out;
    }
    let n = left[0].len();
    let key = (0..n).find(|&v| left.iter().all(|r| r[v] != NONE) && right.iter().all(|r| r[v] != NONE));
    match key {
        Some(v) => {
            // Right rows by key value: (first, next) chains through `next`.
            let mut first = IdMap::new();
            let mut next = alloc::vec![u32::MAX; right.len()];
            for (i, r) in right.iter().enumerate().rev() {
                let (head, new) = first.insert(&[r[v]], i as u32);
                if !new {
                    next[i] = head;
                    first.set(&[r[v]], i as u32);
                }
            }
            for l in &left {
                if let Some(head) = first.get(&[l[v]]) {
                    let mut at = head;
                    while at != u32::MAX {
                        let i = at as usize;
                        at = next[i];
                        if crate::budget::step() {
                            return out;
                        }
                        if let Some(m) = merge(l, &right[i]) {
                            out.push(m);
                        }
                    }
                }
            }
        }
        None => {
            for l in &left {
                for r in right {
                    if crate::budget::step() {
                        return out;
                    }
                    if let Some(m) = merge(l, r) {
                        out.push(m);
                    }
                }
            }
        }
    }
    out
}
