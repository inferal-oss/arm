//! SPARQL 1.2: the algebra (this file), the parser, and the evaluator.
//! Constants are interned at parse time; variables are numbered per query,
//! and a solution is a row of term ids indexed by variable (0 = unbound).

pub mod compile;
pub mod eval;
pub mod expr;
pub mod md5;
pub mod num;
pub mod parse;
pub mod regex;
pub mod sha;
pub mod temporal;
pub mod update;

use alloc::boxed::Box;
use alloc::vec::Vec;

pub type Var = u32;

/// A position in a pattern: a term, a variable, or a triple term with variables in it.
#[derive(Clone)]
pub enum Tv {
    Term(u32),
    Var(Var),
    Triple(Box<[Tv; 3]>),
}

#[derive(Clone)]
pub struct Triple {
    pub s: Tv,
    pub p: Tv,
    pub o: Tv,
    /// A property path in place of `p`.
    pub path: Option<Box<Path>>,
}

#[derive(Clone)]
pub enum Path {
    Iri(u32),
    Inverse(Box<Path>),
    Seq(Box<Path>, Box<Path>),
    Alt(Box<Path>, Box<Path>),
    ZeroOrMore(Box<Path>),
    OneOrMore(Box<Path>),
    ZeroOrOne(Box<Path>),
    /// `!(...)`: forward and inverse IRIs excluded.
    Negated(Vec<u32>, Vec<u32>),
}

#[derive(Clone)]
pub enum Pattern {
    /// One solution binding nothing.
    Unit,
    Bgp(Vec<Triple>),
    Join(Box<Pattern>, Box<Pattern>),
    LeftJoin(Box<Pattern>, Box<Pattern>, Option<Expr>),
    Filter(Expr, Box<Pattern>),
    Union(Box<Pattern>, Box<Pattern>),
    Minus(Box<Pattern>, Box<Pattern>),
    Graph(Tv, Box<Pattern>),
    Extend(Box<Pattern>, Var, Expr),
    Values(Vec<Var>, Vec<Vec<u32>>),
    Lateral(Box<Pattern>, Box<Pattern>),
    Sub(Box<Select>),
    Service(bool),
}

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Func {
    Str, Lang, LangMatches, Datatype, Iri, Bnode, Rand, Abs, Ceil, Floor, Round, Concat, SubStr,
    StrLen, Replace, UCase, LCase, EncodeForUri, Contains, StrStarts, StrEnds, StrBefore, StrAfter,
    Year, Month, Day, Hours, Minutes, Seconds, Timezone, Tz, Now, Uuid, StrUuid, Md5, Sha1, Sha256,
    Sha384, Sha512, StrLang, StrDt, SameTerm, IsIri, IsBlank, IsLiteral, IsNumeric, Regex, Triple,
    Subject, Predicate, Object, IsTriple, LangDir, HasLang, HasLangDir, StrLangDir,
}

#[derive(Clone)]
pub enum Expr {
    Var(Var),
    Const(u32),
    Or(Box<Expr>, Box<Expr>),
    And(Box<Expr>, Box<Expr>),
    Not(Box<Expr>),
    Cmp(Cmp, Box<Expr>, Box<Expr>),
    In(Box<Expr>, Vec<Expr>, bool),
    Arith(Op, Box<Expr>, Box<Expr>),
    Neg(Box<Expr>),
    Plus(Box<Expr>),
    Call(Func, Vec<Expr>),
    Bound(Var),
    If(Box<Expr>, Box<Expr>, Box<Expr>),
    Coalesce(Vec<Expr>),
    Exists(Box<Pattern>, bool),
    /// `xsd:...(x)`: the target datatype's IRI id.
    Cast(u32, Box<Expr>),
    /// A function the engine does not know: always an error.
    Unknown,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Cmp {
    Eq, Ne, Lt, Gt, Le, Ge,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Op {
    Add, Sub, Mul, Div,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum AggKind {
    Count, Sum, Min, Max, Avg, Sample, GroupConcat,
}

#[derive(Clone)]
pub struct Aggregate {
    pub kind: AggKind,
    pub distinct: bool,
    /// `None`: `COUNT(*)`.
    pub expr: Option<Expr>,
    pub separator: Vec<u8>,
}

#[derive(Clone)]
pub struct Select {
    pub pattern: Pattern,
    /// GROUP BY: key expressions, each optionally bound to a variable.
    pub group: Option<Vec<(Expr, Option<Var>)>>,
    /// Aggregates, each computed into a hidden variable.
    pub aggregates: Vec<(Var, Aggregate)>,
    pub having: Vec<Expr>,
    /// SELECT expressions, in order: `(expr AS ?v)`.
    pub extend: Vec<(Var, Expr)>,
    pub order: Vec<(Expr, bool)>,
    /// The projected variables, in order (for `*`, every visible variable).
    pub project: Vec<Var>,
    pub distinct: bool,
    pub reduced: bool,
    pub offset: usize,
    pub limit: Option<usize>,
    /// A VALUES clause after the query: joined after grouping, before SELECT expressions.
    pub values: Option<Box<Pattern>>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Form {
    Select = 1,
    Construct = 2,
    Ask = 3,
    Describe = 4,
}

/// A position in a CONSTRUCT template: blank nodes are per-solution.
#[derive(Clone)]
pub enum Tt {
    Term(u32),
    Var(Var),
    Bnode(u32),
    Triple(Box<[Tt; 3]>),
}

pub struct Query {
    pub form: Form,
    /// Variable names by index; hidden variables (blank nodes, aggregates) start with `_`.
    pub vars: Vec<Vec<u8>>,
    pub select: Select,
    pub template: Vec<[Tt; 3]>,
    pub template_bnodes: u32,
    pub describe: Vec<Tv>,
    pub from: Vec<u32>,
    pub from_named: Vec<u32>,
    pub base: Vec<u8>,
}

impl Query {
    pub fn var(&self, name: &[u8]) -> Option<Var> {
        self.vars.iter().position(|v| v == name).map(|i| i as Var)
    }
}

pub enum Output {
    /// SELECT: rows of the projected variables, and how many (rows may be empty).
    Rows(Vec<u32>, usize),
    /// CONSTRUCT, DESCRIBE: triples.
    Triples(Vec<u32>),
    Bool(bool),
}

/// Run a query over `graphs` (their union is the default graph; empty: the
/// store's default graph), with variables bound to terms.
pub fn execute(q: &Query, dict: &mut crate::dict::Dict, store: &crate::store::Store, graphs: &[u32], binds: &[(Var, u32)]) -> Output {
    use crate::dict::{BNODE, DEFAULT_GRAPH_ID, IRI, TRIPLE};
    use eval::{Active, Ctx};
    let (default, named) = if !q.from.is_empty() || !q.from_named.is_empty() {
        (q.from.clone(), Some(q.from_named.clone()))
    } else if graphs.is_empty() {
        (alloc::vec![DEFAULT_GRAPH_ID], None)
    } else {
        (graphs.to_vec(), None)
    };
    let nvars = q.vars.len();
    let mut sub = alloc::vec![0u32; nvars];
    for &(v, id) in binds {
        sub[v as usize] = id;
    }
    let mut ctx = Ctx::new(dict, store, default, named, &q.base, nvars);
    let rows = ctx.select(&q.select, &sub, Active::Default);
    match q.form {
        Form::Select => {
            let mut out = Vec::with_capacity(rows.len() * q.select.project.len());
            for r in &rows {
                out.extend(q.select.project.iter().map(|&v| r[v as usize]));
            }
            Output::Rows(out, rows.len())
        }
        Form::Ask => Output::Bool(!rows.is_empty()),
        Form::Construct => {
            let mut seen = crate::idmap::IdMap::new();
            let mut out = Vec::new();
            for r in &rows {
                let bnodes: Vec<u32> = (0..q.template_bnodes).map(|_| ctx.dict.fresh_bnode()).collect();
                for t in &q.template {
                    let ids = [inst(&t[0], r, &bnodes, ctx.dict), inst(&t[1], r, &bnodes, ctx.dict), inst(&t[2], r, &bnodes, ctx.dict)];
                    let [Some(s), Some(p), Some(o)] = ids else { continue };
                    let kind = |id: u32| ctx.dict.term(id).map_or(0, |t| t.kind);
                    if !matches!(kind(s), IRI | BNODE) || kind(p) != IRI {
                        continue;
                    }
                    if seen.add(&[s, p, o]) {
                        out.extend_from_slice(&[s, p, o]);
                    }
                }
            }
            Output::Triples(out)
        }
        Form::Describe => {
            let mut todo: Vec<u32> = Vec::new();
            for d in &q.describe {
                match d {
                    Tv::Term(i) => todo.push(*i),
                    Tv::Var(v) => todo.extend(rows.iter().map(|r| r[*v as usize]).filter(|&x| x != 0)),
                    Tv::Triple(_) => {}
                }
            }
            if q.describe.is_empty() {
                for r in &rows {
                    todo.extend(q.select.project.iter().map(|&v| r[v as usize]).filter(|&x| x != 0));
                }
            }
            // Concise bounded description: outgoing triples, following blank nodes.
            let mut seen = crate::idmap::IdMap::new();
            let mut out = Vec::new();
            let mut done = crate::idmap::IdMap::new();
            let mut scan = Vec::new();
            while let Some(n) = todo.pop() {
                if !done.add(&[n]) {
                    continue;
                }
                for &g in &ctx.default.clone() {
                    scan.clear();
                    store.scan([n, 0, 0, g], &mut scan);
                    for q in scan.chunks_exact(4) {
                        if seen.add(&q[..3]) {
                            out.extend_from_slice(&q[..3]);
                            let k = ctx.dict.term(q[2]).map_or(0, |t| t.kind);
                            if k == BNODE || k == TRIPLE {
                                todo.push(q[2]);
                            }
                        }
                    }
                }
            }
            Output::Triples(out)
        }
    }
}

/// A template position's term under a solution (None when unbound or invalid).
pub fn inst(t: &Tt, r: &[u32], bnodes: &[u32], dict: &mut crate::dict::Dict) -> Option<u32> {
    use crate::dict::{BNODE, IRI, Key};
    match t {
        Tt::Term(i) => Some(*i),
        Tt::Var(v) => Some(r[*v as usize]).filter(|&x| x != 0),
        Tt::Bnode(k) => Some(bnodes[*k as usize]),
        Tt::Triple(b) => {
            let (s, p, o) = (inst(&b[0], r, bnodes, dict)?, inst(&b[1], r, bnodes, dict)?, inst(&b[2], r, bnodes, dict)?);
            if !matches!(dict.term(s)?.kind, IRI | BNODE) || dict.term(p)?.kind != IRI {
                return None;
            }
            Some(dict.intern(Key::triple(s, p, o)))
        }
    }
}

/// Graphs an update operation acts on.
#[derive(Clone, Copy)]
pub enum Target {
    Graph(u32),
    Default,
    Named,
    All,
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Transfer {
    Add,
    Move,
    Copy,
}

/// A quad template; graph `Tt::Term(0)` means the operation's default graph.
pub type QuadTemplate = [Tt; 4];

pub enum UpdateOp {
    Data { insert: bool, quads: Vec<QuadTemplate>, bnodes: u32 },
    Modify { delete: Vec<QuadTemplate>, insert: Vec<QuadTemplate>, bnodes: u32, with: Option<u32>, using: Option<(Vec<u32>, Vec<u32>)>, select: Select, nvars: usize },
    Load { silent: bool },
    Clear { target: Target },
    Create { silent: bool, graph: u32 },
    Transfer { op: Transfer, from: u32, to: u32 },
}

pub struct Update {
    pub ops: Vec<UpdateOp>,
    pub base: Vec<u8>,
}

/// Every term id a query holds (constants, VALUES data, templates).
pub fn query_terms(q: &Query, f: &mut dyn FnMut(u32)) {
    fn tv(t: &Tv, f: &mut dyn FnMut(u32)) {
        match t {
            Tv::Term(i) => f(*i),
            Tv::Var(_) => {}
            Tv::Triple(b) => b.iter().for_each(|x| tv(x, f)),
        }
    }
    fn tt(t: &Tt, f: &mut dyn FnMut(u32)) {
        match t {
            Tt::Term(i) => f(*i),
            Tt::Triple(b) => b.iter().for_each(|x| tt(x, f)),
            _ => {}
        }
    }
    fn path(p: &Path, f: &mut dyn FnMut(u32)) {
        match p {
            Path::Iri(i) => f(*i),
            Path::Inverse(a) | Path::ZeroOrMore(a) | Path::OneOrMore(a) | Path::ZeroOrOne(a) => path(a, f),
            Path::Seq(a, b) | Path::Alt(a, b) => {
                path(a, f);
                path(b, f);
            }
            Path::Negated(a, b) => a.iter().chain(b).for_each(|&i| f(i)),
        }
    }
    fn expr(e: &Expr, f: &mut dyn FnMut(u32)) {
        match e {
            Expr::Const(c) | Expr::Cast(c, _) => {
                f(*c);
                if let Expr::Cast(_, a) = e {
                    expr(a, f);
                }
            }
            Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => {
                expr(a, f);
                expr(b, f);
            }
            Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) => expr(a, f),
            Expr::In(a, l, _) => {
                expr(a, f);
                l.iter().for_each(|x| expr(x, f));
            }
            Expr::Call(_, l) | Expr::Coalesce(l) => l.iter().for_each(|x| expr(x, f)),
            Expr::If(a, b, c) => {
                expr(a, f);
                expr(b, f);
                expr(c, f);
            }
            Expr::Exists(p, _) => pattern(p, f),
            Expr::Var(_) | Expr::Bound(_) | Expr::Unknown => {}
        }
    }
    fn pattern(p: &Pattern, f: &mut dyn FnMut(u32)) {
        match p {
            Pattern::Unit | Pattern::Service(_) => {}
            Pattern::Bgp(ts) => ts.iter().for_each(|t| {
                [&t.s, &t.p, &t.o].iter().for_each(|x| tv(x, f));
                if let Some(p) = &t.path {
                    path(p, f);
                }
            }),
            Pattern::Join(a, b) | Pattern::Union(a, b) | Pattern::Minus(a, b) | Pattern::Lateral(a, b) => {
                pattern(a, f);
                pattern(b, f);
            }
            Pattern::LeftJoin(a, b, e) => {
                pattern(a, f);
                pattern(b, f);
                if let Some(e) = e {
                    expr(e, f);
                }
            }
            Pattern::Filter(e, a) | Pattern::Extend(a, _, e) => {
                expr(e, f);
                pattern(a, f);
            }
            Pattern::Graph(g, a) => {
                tv(g, f);
                pattern(a, f);
            }
            Pattern::Values(_, rows) => rows.iter().flatten().for_each(|&i| f(i)),
            Pattern::Sub(s) => select(s, f),
        }
    }
    fn select(s: &Select, f: &mut dyn FnMut(u32)) {
        pattern(&s.pattern, f);
        for (e, _) in s.group.iter().flatten() {
            expr(e, f);
        }
        for (_, a) in &s.aggregates {
            if let Some(e) = &a.expr {
                expr(e, f);
            }
        }
        s.having.iter().for_each(|e| expr(e, f));
        s.extend.iter().for_each(|(_, e)| expr(e, f));
        s.order.iter().for_each(|(e, _)| expr(e, f));
        if let Some(v) = &s.values {
            pattern(v, f);
        }
    }
    select(&q.select, f);
    q.template.iter().flatten().for_each(|t| tt(t, f));
    q.describe.iter().for_each(|t| tv(t, f));
    q.from.iter().chain(&q.from_named).for_each(|&i| f(i));
}
