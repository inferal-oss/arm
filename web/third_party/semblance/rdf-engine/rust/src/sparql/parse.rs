//! The SPARQL 1.2 query parser, straight to the algebra (SPARQL 1.1, 18.2).

use super::*;
use super::{QuadTemplate, Target, Transfer, Update, UpdateOp};
use crate::dict::{Dict, Key, NONE, RDF_DIR_LANG_STRING, RDF_LANG_STRING};
use crate::iri;
use crate::lex::{Cur, Res, has_scheme, pn_chars, pn_chars_base, pn_chars_u};
use crate::idmap::{IdMap, bytes_key};
use alloc::vec;

const RDF: &[u8] = b"http://www.w3.org/1999/02/22-rdf-syntax-ns#";
const XSD: &[u8] = b"http://www.w3.org/2001/XMLSchema#";

/// XSD datatypes that may be used as cast functions.
/// The first byte of the engine's own variables (blank nodes, unnamed reifiers): one no
/// SPARQL variable name has, so `?_1` or `?__i` stay the query's own (and in SELECT *).
const HIDDEN: u8 = b'#';

const CASTS: &[&[u8]] = &[
    b"string", b"boolean", b"integer", b"decimal", b"float", b"double", b"dateTime", b"date", b"time",
    b"duration", b"dayTimeDuration", b"yearMonthDuration",
    b"int", b"long", b"short", b"byte", b"nonNegativeInteger", b"positiveInteger", b"nonPositiveInteger",
    b"negativeInteger", b"unsignedInt", b"unsignedLong", b"unsignedShort", b"unsignedByte",
];

struct P<'a, 'd> {
    c: Cur<'a>,
    dict: &'d mut Dict,
    /// Namespaces by prefix (an index into `namespaces`).
    prefixes: IdMap,
    namespaces: Vec<Vec<u8>>,
    base: Vec<u8>,
    vars: Vec<Vec<u8>>,
    /// Blank node labels in patterns: their variable and the basic graph pattern they belong to.
    bnodes: IdMap,
    /// (variable, basic graph pattern) by `bnodes` value.
    bnode_vars: Vec<(Var, u32)>,
    /// Blank node labels in a CONSTRUCT template.
    tbnodes: IdMap,
    /// The keys of `tbnodes`, for the request-wide label check.
    tbnode_labels: Vec<Vec<u32>>,
    /// The current basic graph pattern; anything but FILTER between triples starts another.
    bgp: u32,
    hidden: u32,
    aggs: Vec<(Var, Aggregate)>,
    agg_ok: bool,
    /// Variables standing for template blank nodes (CONSTRUCT), by variable.
    template_bnodes: IdMap,
    in_template: bool,
    /// Variables standing for reifiers the query leaves unnamed (`{| |}`, `<< >>`, `~`).
    anon_reifiers: Vec<Var>,
    /// The top-level SELECT was `SELECT *`.
    top_star: bool,
}

/// One element of a triples block: a plain triple or a path.
enum Elt {
    T(Triple),
    P(Tv, Path, Tv),
}

pub fn parse_query(text: &[u8], base: &[u8], dict: &mut Dict) -> Res<Query> {
    new_parser(text, base, dict).query()
}

pub fn parse_update(text: &[u8], base: &[u8], dict: &mut Dict) -> Res<Update> {
    new_parser(text, base, dict).update()
}

fn new_parser<'a, 'd>(text: &'a [u8], base: &[u8], dict: &'d mut Dict) -> P<'a, 'd> {
    P {
        c: Cur::new(text),
        dict,
        prefixes: IdMap::new(),
        namespaces: Vec::new(),
        base: base.to_vec(),
        vars: Vec::new(),
        bnodes: IdMap::new(),
        bnode_vars: Vec::new(),
        tbnodes: IdMap::new(),
        tbnode_labels: Vec::new(),
        bgp: 0,
        hidden: 0,
        aggs: Vec::new(),
        agg_ok: false,
        template_bnodes: IdMap::new(),
        in_template: false,
        anon_reifiers: Vec::new(),
        top_star: false,
    }
}

fn concat(a: &[u8], b: &[u8]) -> Vec<u8> {
    let mut v = Vec::with_capacity(a.len() + b.len());
    v.extend_from_slice(a);
    v.extend_from_slice(b);
    v
}

fn varname_char(c: u32, first: bool) -> bool {
    pn_chars_u(c) || (0x30..=0x39).contains(&c) || !first && (c == 0xb7 || (0x300..=0x36f).contains(&c) || (0x203f..=0x2040).contains(&c))
}

fn join(a: Pattern, b: Pattern) -> Pattern {
    match (a, b) {
        (Pattern::Unit, b) => b,
        (a, Pattern::Unit) => a,
        (Pattern::Bgp(mut x), Pattern::Bgp(y)) => {
            x.extend(y);
            Pattern::Bgp(x)
        }
        (a, b) => Pattern::Join(Box::new(a), Box::new(b)),
    }
}

impl P<'_, '_> {
    // --- tokens ------------------------------------------------------------------

    fn ws(&mut self) {
        loop {
            match self.c.peek() {
                b' ' | b'\t' | b'\r' if !self.c.eof() => self.c.i += 1,
                b'\n' => {
                    self.c.i += 1;
                    self.c.line += 1;
                }
                b'#' => {
                    while !self.c.eof() && self.c.peek() != b'\n' {
                        self.c.i += 1;
                    }
                }
                _ => return,
            }
        }
    }

    /// A case-insensitive keyword, not followed by a name character.
    fn at_kw(&mut self, word: &[u8]) -> bool {
        self.ws();
        let s = &self.c.s[self.c.i..];
        if s.len() < word.len() || !s[..word.len()].eq_ignore_ascii_case(word) {
            return false;
        }
        let next = s.get(word.len()).copied().unwrap_or(b' ');
        !(next.is_ascii_alphanumeric() || next == b'_' || next == b':' || next >= 0x80)
    }

    fn kw(&mut self, word: &[u8]) -> bool {
        let ok = self.at_kw(word);
        if ok {
            self.c.i += word.len();
        }
        ok
    }

    fn expect_kw(&mut self, word: &[u8], msg: &'static str) -> Res<()> {
        if self.kw(word) { Ok(()) } else { self.c.err(msg) }
    }

    fn at(&mut self, s: &[u8]) -> bool {
        self.ws();
        self.c.starts(s)
    }

    fn eat(&mut self, s: &[u8]) -> bool {
        self.ws();
        self.c.eat_str(s)
    }

    fn expect(&mut self, s: &[u8], msg: &'static str) -> Res<()> {
        if self.eat(s) { Ok(()) } else { self.c.err(msg) }
    }

    fn at_var(&mut self) -> bool {
        self.ws();
        matches!(self.c.peek(), b'?' | b'$') && {
            let save = self.c.i;
            self.c.i += 1;
            let (ch, _) = self.c.ch();
            let ok = !self.c.eof() && varname_char(ch, true);
            self.c.i = save;
            ok
        }
    }

    fn var_named(&mut self, name: &[u8]) -> Var {
        match self.vars.iter().position(|v| v == name) {
            Some(i) => i as Var,
            None => {
                self.vars.push(name.to_vec());
                (self.vars.len() - 1) as Var
            }
        }
    }

    fn fresh_var(&mut self) -> Var {
        self.hidden += 1;
        let mut name = vec![HIDDEN];
        let mut n = self.hidden;
        while n > 0 {
            name.push(b'0' + (n % 10) as u8);
            n /= 10;
        }
        self.vars.push(name);
        let v = (self.vars.len() - 1) as Var;
        if self.in_template {
            let k = self.template_bnodes.len() as u32;
            self.template_bnodes.insert(&[v], k);
        }
        v
    }

    fn var(&mut self) -> Res<Var> {
        if !self.at_var() {
            return self.c.err("expected variable");
        }
        self.c.i += 1;
        let start = self.c.i;
        let mut first = true;
        loop {
            let (ch, n) = self.c.ch();
            if self.c.eof() || !varname_char(ch, first) {
                break;
            }
            self.c.i += n;
            first = false;
        }
        let name = self.c.s[start..self.c.i].to_vec();
        Ok(self.var_named(&name))
    }

    fn iri_id(&mut self, iri: &[u8]) -> u32 {
        self.dict.intern(Key::iri(iri))
    }

    fn rdf(&mut self, local: &[u8]) -> u32 {
        self.iri_id(&concat(RDF, local))
    }

    fn at_iri(&mut self) -> bool {
        self.ws();
        (self.c.peek() == b'<' && self.iriref_ahead()) || self.at_pname()
    }

    /// Whether `<` starts an IRI (and not a comparison).
    fn iriref_ahead(&self) -> bool {
        let mut i = self.c.i + 1;
        while let Some(&b) = self.c.s.get(i) {
            match b {
                b'>' => return true,
                0..=0x20 | b'<' | b'"' | b'{' | b'}' | b'|' | b'^' | b'`' => return false,
                _ => i += 1,
            }
        }
        false
    }

    fn at_pname(&self) -> bool {
        // PN_PREFIX? ':'
        let mut i = self.c.i;
        let s = self.c.s;
        let mut c = Cur { s, i, line: 0 };
        let (ch, n) = c.ch();
        if !c.eof() && pn_chars_base(ch) {
            c.i += n;
            loop {
                let (ch, n) = c.ch();
                if c.eof() || !(pn_chars(ch) || ch == '.' as u32) {
                    break;
                }
                c.i += n;
            }
            i = c.i;
        }
        s.get(i) == Some(&b':')
    }

    fn iri_string(&mut self) -> Res<Vec<u8>> {
        self.ws();
        if self.c.peek() == b'<' {
            let mut buf = Vec::new();
            self.c.iriref(&mut buf)?;
            if has_scheme(&buf) {
                return Ok(buf);
            }
            let mut out = Vec::new();
            iri::resolve(&self.base, &buf, &mut out);
            return Ok(out);
        }
        self.pname()
    }

    fn iri(&mut self) -> Res<u32> {
        let s = self.iri_string()?;
        Ok(self.iri_id(&s))
    }

    fn pname(&mut self) -> Res<Vec<u8>> {
        let start = self.c.i;
        let (ch, n) = self.c.ch();
        if !self.c.eof() && pn_chars_base(ch) {
            self.c.i += n;
            loop {
                let (ch, n) = self.c.ch();
                if self.c.eof() || !(pn_chars(ch) || ch == '.' as u32) {
                    break;
                }
                self.c.i += n;
            }
            if self.c.s[self.c.i - 1] == b'.' {
                return self.c.err("prefix ends with .");
            }
        }
        let prefix = self.c.s[start..self.c.i].to_vec();
        self.c.expect(b':', "expected prefixed name")?;
        let Some(ns) = self.prefixes.get(&bytes_key(&prefix)) else {
            return self.c.err("undefined prefix");
        };
        let mut out = self.namespaces[ns as usize].clone();
        let (mut keep_len, mut keep_i) = (out.len(), self.c.i);
        let mut first = true;
        loop {
            let (ch, n) = self.c.ch();
            if self.c.eof() {
                break;
            }
            if ch == '\\' as u32 {
                let e = self.c.at(1);
                if !b"_~.-!$&'()*+,;=/?#@%".contains(&e) {
                    return self.c.err("bad escape in local name");
                }
                out.push(e);
                self.c.i += 2;
            } else if ch == '%' as u32 {
                if !(self.c.at(1).is_ascii_hexdigit() && self.c.at(2).is_ascii_hexdigit()) {
                    return self.c.err("bad % escape in local name");
                }
                out.extend_from_slice(&self.c.s[self.c.i..self.c.i + 3]);
                self.c.i += 3;
            } else if ch == '.' as u32 && !first {
                out.push(b'.');
                self.c.i += 1;
                continue;
            } else if if first { pn_chars_u(ch) || ch == ':' as u32 || (0x30..=0x39).contains(&ch) } else { pn_chars(ch) || ch == ':' as u32 } {
                out.extend_from_slice(&self.c.s[self.c.i..self.c.i + n]);
                self.c.i += n;
            } else {
                break;
            }
            first = false;
            (keep_len, keep_i) = (out.len(), self.c.i);
        }
        out.truncate(keep_len);
        self.c.i = keep_i;
        Ok(out)
    }

    // --- queries -----------------------------------------------------------------

    fn prologue(&mut self) -> Res<()> {
        loop {
            if self.kw(b"BASE") {
                self.base = self.iri_string()?;
            } else if self.kw(b"PREFIX") {
                self.ws();
                let start = self.c.i;
                let (ch, _) = self.c.ch();
                if self.c.peek() != b':' && !pn_chars_base(ch) {
                    return self.c.err("bad prefix name");
                }
                while !self.c.eof() && self.c.peek() != b':' && !self.c.peek().is_ascii_whitespace() {
                    self.c.i += 1;
                }
                let prefix = self.c.s[start..self.c.i].to_vec();
                if prefix.last() == Some(&b'.') {
                    return self.c.err("bad prefix name");
                }
                self.c.expect(b':', "expected prefix name")?;
                let ns = self.iri_string()?;
                self.namespaces.push(ns);
                self.prefixes.set(&bytes_key(&prefix), self.namespaces.len() as u32 - 1);
            } else if self.kw(b"VERSION") {
                self.ws();
                match self.c.peek() {
                    q @ (b'"' | b'\'') => {
                        let mut buf = Vec::new();
                        self.c.string(q, &mut buf)?;
                    }
                    _ => return self.c.err("expected a version string"),
                }
            } else {
                return Ok(());
            }
        }
    }

    fn query(&mut self) -> Res<Query> {
        self.prologue()?;
        let mut q = Query {
            form: Form::Select,
            vars: Vec::new(),
            select: empty_select(),
            template: Vec::new(),
            template_bnodes: 0,
            describe: Vec::new(),
            from: Vec::new(),
            from_named: Vec::new(),
            base: Vec::new(),
        };
        if self.at_kw(b"SELECT") {
            q.select = self.select(true, &mut q)?;
        } else if self.kw(b"CONSTRUCT") {
            q.form = Form::Construct;
            let mut template = Vec::new();
            if self.at(b"{") {
                self.c.i += 1;
                self.in_template = true;
                self.triples_block_until(b"}", &mut template)?;
                self.in_template = false;
                self.expect(b"}", "expected }")?;
                self.dataset(&mut q)?;
                q.select = self.where_and_modifiers(false)?;
            } else {
                self.dataset(&mut q)?;
                self.expect_kw(b"WHERE", "expected WHERE")?;
                self.expect(b"{", "expected {")?;
                self.anon_reifiers.clear();
                self.triples_block_until(b"}", &mut template)?;
                // The template's unnamed reifiers are blank nodes: fresh for each solution.
                for v in core::mem::take(&mut self.anon_reifiers) {
                    let k = self.template_bnodes.len() as u32;
                    self.template_bnodes.insert(&[v], k);
                }
                self.expect(b"}", "expected }")?;
                let triples: Vec<Triple> = template.iter().map(|t| Triple { s: t[0].clone(), p: t[1].clone(), o: t[2].clone() , path: None }).collect();
                q.select = empty_select();
                q.select.pattern = Pattern::Bgp(triples);
                self.modifiers(&mut q.select)?;
            }
            q.template = template.into_iter().map(|t| t.map(|x| self.to_template(x))).collect();
            q.template_bnodes = self.template_bnodes.len() as u32;
        } else if self.kw(b"ASK") {
            q.form = Form::Ask;
            self.dataset(&mut q)?;
            q.select = self.where_and_modifiers(false)?;
        } else if self.kw(b"DESCRIBE") {
            q.form = Form::Describe;
            if !self.eat(b"*") {
                loop {
                    self.ws();
                    if self.at_var() {
                        let v = self.var()?;
                        q.describe.push(Tv::Var(v));
                    } else if self.at_iri() {
                        let i = self.iri()?;
                        q.describe.push(Tv::Term(i));
                    } else {
                        break;
                    }
                }
                if q.describe.is_empty() {
                    return self.c.err("expected DESCRIBE targets");
                }
            }
            self.dataset(&mut q)?;
            self.ws();
            if self.at_kw(b"WHERE") || self.at(b"{") {
                q.select = self.where_and_modifiers(false)?;
            } else {
                q.select = empty_select();
                self.modifiers(&mut q.select)?;
            }
        } else {
            return self.c.err("expected SELECT, CONSTRUCT, ASK or DESCRIBE");
        }
        let mut values_vars = Vec::new();
        if self.kw(b"VALUES") {
            let values = self.data_block()?;
            pattern_vars(&values, &mut values_vars);
            if self.top_star {
                q.select.project = self.visible_in(&q.select.pattern);
                for v in &values_vars {
                    if !q.select.project.contains(v) {
                        q.select.project.push(*v);
                    }
                }
            }
            q.select.values = Some(Box::new(values));
        }
        if q.form == Form::Select {
            for (v, _) in &q.select.extend {
                if values_vars.contains(v) {
                    return self.c.err("a SELECT expression binds a variable of the VALUES clause");
                }
            }
            if q.select.group.is_some() || !q.select.aggregates.is_empty() {
                self.check_grouped(&q.select, &values_vars)?;
            }
        }
        self.ws();
        if !self.c.eof() {
            return self.c.err("unexpected text after the query");
        }
        if q.form != Form::Select {
            // No projection for the other forms: templates may use hidden variables.
            q.select.project = (0..self.vars.len() as Var).collect();
        }
        q.vars = core::mem::take(&mut self.vars);
        q.base = core::mem::take(&mut self.base);
        Ok(q)
    }

    fn to_template(&self, t: Tv) -> Tt {
        match t {
            Tv::Term(i) => Tt::Term(i),
            Tv::Var(v) => match self.template_bnodes.get(&[v]) {
                Some(k) => Tt::Bnode(k),
                None => Tt::Var(v),
            },
            Tv::Triple(b) => {
                let [s, p, o] = *b;
                Tt::Triple(Box::new([self.to_template(s), self.to_template(p), self.to_template(o)]))
            }
        }
    }

    fn dataset(&mut self, q: &mut Query) -> Res<()> {
        while self.kw(b"FROM") {
            if self.kw(b"NAMED") {
                let i = self.iri()?;
                q.from_named.push(i);
            } else {
                let i = self.iri()?;
                q.from.push(i);
            }
        }
        Ok(())
    }

    /// SELECT ... WHERE {...} modifiers. `top` allows dataset clauses.
    fn select(&mut self, top: bool, q: &mut Query) -> Res<Select> {
        self.expect_kw(b"SELECT", "expected SELECT")?;
        let outer_aggs = core::mem::take(&mut self.aggs);
        let mut s = empty_select();
        s.distinct = self.kw(b"DISTINCT");
        if !s.distinct {
            s.reduced = self.kw(b"REDUCED");
        }
        let mut star = false;
        let mut projection: Vec<(Var, Option<Expr>)> = Vec::new();
        if self.eat(b"*") {
            star = true;
        } else {
            loop {
                self.ws();
                if self.at_var() {
                    let v = self.var()?;
                    projection.push((v, None));
                } else if self.eat(b"(") {
                    self.agg_ok = true;
                    let e = self.expr()?;
                    self.agg_ok = false;
                    self.expect_kw(b"AS", "expected AS")?;
                    let v = self.var()?;
                    self.expect(b")", "expected )")?;
                    if projection.iter().any(|(x, _)| *x == v) {
                        return self.c.err("variable projected twice");
                    }
                    projection.push((v, Some(e)));
                } else {
                    break;
                }
            }
            if projection.is_empty() {
                return self.c.err("expected projection");
            }
        }
        if top {
            self.dataset(q)?;
        }
        let first_var = self.vars.len();
        let body = self.where_and_modifiers(true)?;
        s.pattern = body.pattern;
        s.group = body.group;
        s.having = body.having;
        s.order = body.order;
        s.offset = body.offset;
        s.limit = body.limit;
        s.aggregates = core::mem::take(&mut self.aggs);
        s.aggregates.extend(body.aggregates);
        let _ = first_var;
        if top {
            self.top_star = star;
        }
        if star {
            if s.group.is_some() || !s.aggregates.is_empty() {
                return self.c.err("SELECT * with GROUP BY");
            }
            s.project = self.visible_in(&s.pattern);
        } else {
            for (v, e) in projection {
                if let Some(e) = e {
                    s.extend.push((v, e));
                }
                s.project.push(v);
            }
        }
        // A SELECT expression may not rebind a variable in scope: in a grouped
        // query, the group keys; otherwise the WHERE clause's variables.
        let grouped = s.group.is_some() || !s.aggregates.is_empty();
        let mut scope = if grouped { Vec::new() } else { self.visible_in(&s.pattern) };
        for (e, v) in s.group.iter().flatten() {
            match (e, v) {
                (_, Some(v)) | (Expr::Var(v), None) => scope.push(*v),
                _ => {}
            }
        }
        for (v, _) in &s.extend {
            if scope.contains(v) {
                return self.c.err("a SELECT expression binds a variable already in scope");
            }
        }
        if !top && (s.group.is_some() || !s.aggregates.is_empty()) {
            self.check_grouped(&s, &[])?;
        }
        self.aggs = outer_aggs;
        Ok(s)
    }

    /// In a grouped query, plain projected variables must be group keys.
    fn check_grouped(&self, s: &Select, outer: &[Var]) -> Res<()> {
        let keys: Vec<Var> = s
            .group
            .iter()
            .flatten()
            .filter_map(|(e, v)| match (e, v) {
                (_, Some(v)) => Some(*v),
                (Expr::Var(v), None) => Some(*v),
                _ => None,
            })
            .collect();
        let mut allowed = keys.clone();
        allowed.extend(s.aggregates.iter().map(|(v, _)| *v));
        allowed.extend_from_slice(outer);
        for (v, e) in &s.extend {
            if !expr_vars_within(e, &allowed) {
                return self.c.err("a non-grouped variable is used in a grouped query");
            }
            allowed.push(*v);
        }
        for v in &s.project {
            if !allowed.contains(v) {
                return self.c.err("a non-grouped variable is projected");
            }
        }
        Ok(())
    }

    /// Variables that a pattern can bind, in order of first appearance (SELECT *).
    fn visible_in(&self, p: &Pattern) -> Vec<Var> {
        let mut out = Vec::new();
        pattern_vars(p, &mut out);
        out.retain(|&v| self.vars[v as usize].first() != Some(&HIDDEN));
        out
    }

    fn where_and_modifiers(&mut self, _in_select: bool) -> Res<Select> {
        self.kw(b"WHERE");
        let mut s = empty_select();
        s.pattern = self.group_pattern()?;
        self.modifiers(&mut s)?;
        Ok(s)
    }

    fn modifiers(&mut self, s: &mut Select) -> Res<()> {
        if self.kw(b"GROUP") {
            self.expect_kw(b"BY", "expected BY")?;
            let mut keys = Vec::new();
            loop {
                self.ws();
                if self.at_var() {
                    let v = self.var()?;
                    keys.push((Expr::Var(v), None));
                } else if self.eat(b"(") {
                    let e = self.expr()?;
                    let v = if self.kw(b"AS") { Some(self.var()?) } else { None };
                    self.expect(b")", "expected )")?;
                    keys.push((e, v));
                } else if self.at_call() {
                    let e = self.primary()?;
                    keys.push((e, None));
                } else {
                    break;
                }
            }
            if keys.is_empty() {
                return self.c.err("expected GROUP BY condition");
            }
            s.group = Some(keys);
        }
        if self.kw(b"HAVING") {
            self.agg_ok = true;
            loop {
                self.ws();
                if !(self.c.peek() == b'(' || self.at_call()) {
                    break;
                }
                let e = self.constraint()?;
                s.having.push(e);
            }
            self.agg_ok = false;
            if s.having.is_empty() {
                return self.c.err("expected HAVING condition");
            }
        }
        if self.kw(b"ORDER") {
            self.expect_kw(b"BY", "expected BY")?;
            self.agg_ok = true;
            loop {
                self.ws();
                if self.kw(b"ASC") {
                    let e = self.bracketted()?;
                    s.order.push((e, false));
                } else if self.kw(b"DESC") {
                    let e = self.bracketted()?;
                    s.order.push((e, true));
                } else if self.at_var() {
                    let v = self.var()?;
                    s.order.push((Expr::Var(v), false));
                } else if self.c.peek() == b'(' || self.at_call() {
                    let e = self.constraint()?;
                    s.order.push((e, false));
                } else {
                    break;
                }
            }
            self.agg_ok = false;
            if s.order.is_empty() {
                return self.c.err("expected ORDER BY condition");
            }
        }
        loop {
            if self.kw(b"LIMIT") {
                s.limit = Some(self.integer()?);
            } else if self.kw(b"OFFSET") {
                s.offset = self.integer()?;
            } else {
                break;
            }
        }
        if !self.aggs.is_empty() && s.group.is_none() {
            // Aggregates in HAVING or ORDER BY of an ungrouped query: one group.
        }
        s.aggregates.extend(core::mem::take(&mut self.aggs));
        Ok(())
    }

    fn integer(&mut self) -> Res<usize> {
        self.ws();
        let start = self.c.i;
        while self.c.peek().is_ascii_digit() {
            self.c.i += 1;
        }
        if start == self.c.i {
            return self.c.err("expected integer");
        }
        let mut n: usize = 0;
        for &d in &self.c.s[start..self.c.i] {
            n = n.saturating_mul(10).saturating_add((d - b'0') as usize);
        }
        Ok(n)
    }

    // --- graph patterns ------------------------------------------------------------

    fn group_pattern(&mut self) -> Res<Pattern> {
        let (p, f) = self.group_parts()?;
        Ok(match f {
            Some(f) => Pattern::Filter(f, Box::new(p)),
            None => p,
        })
    }

    /// A group graph pattern, and the conjunction of its own FILTERs (kept
    /// apart so OPTIONAL can make them its condition).
    fn group_parts(&mut self) -> Res<(Pattern, Option<Expr>)> {
        self.expect(b"{", "expected {")?;
        if self.at_kw(b"SELECT") {
            let mut dummy = Query {
                form: Form::Select,
                vars: Vec::new(),
                select: empty_select(),
                template: Vec::new(),
                template_bnodes: 0,
                describe: Vec::new(),
                from: Vec::new(),
                from_named: Vec::new(),
                base: Vec::new(),
            };
            let mut s = self.select(false, &mut dummy)?;
            if self.kw(b"VALUES") {
                s.values = Some(Box::new(self.data_block()?));
            }
            self.expect(b"}", "expected }")?;
            return Ok((Pattern::Sub(Box::new(s)), None));
        }
        let mut g = Pattern::Unit;
        let mut filters: Vec<Expr> = Vec::new();
        self.bgp += 1;
        loop {
            self.ws();
            if self.eat(b"}") {
                break;
            }
            if !self.at_kw(b"FILTER") && !self.at_triple_start() {
                self.bgp += 1;
            }
            if self.c.eof() {
                return self.c.err("unterminated group");
            }
            if self.kw(b"OPTIONAL") {
                let (p, f) = self.group_parts()?;
                g = Pattern::LeftJoin(Box::new(g), Box::new(p), f);
            } else if self.kw(b"MINUS") {
                let p = self.group_pattern()?;
                g = Pattern::Minus(Box::new(g), Box::new(p));
            } else if self.kw(b"LATERAL") {
                let p = self.group_pattern()?;
                g = Pattern::Lateral(Box::new(g), Box::new(p));
            } else if self.kw(b"BIND") {
                self.expect(b"(", "expected (")?;
                let e = self.expr()?;
                self.expect_kw(b"AS", "expected AS")?;
                let v = self.var()?;
                self.expect(b")", "expected )")?;
                let mut bound = Vec::new();
                pattern_vars(&g, &mut bound);
                if bound.contains(&v) {
                    return self.c.err("BIND to a variable already in scope");
                }
                g = Pattern::Extend(Box::new(g), v, e);
            } else if self.kw(b"FILTER") {
                let e = self.constraint()?;
                filters.push(e);
            } else if self.kw(b"VALUES") {
                let v = self.data_block()?;
                g = join(g, v);
            } else if self.kw(b"GRAPH") {
                let t = self.var_or_iri()?;
                let p = self.group_pattern()?;
                g = join(g, Pattern::Graph(t, Box::new(p)));
            } else if self.kw(b"SERVICE") {
                let silent = self.kw(b"SILENT");
                self.var_or_iri()?;
                self.group_pattern()?;
                g = join(g, Pattern::Service(silent));
            } else if self.at(b"{") {
                let mut p = self.group_pattern()?;
                while self.kw(b"UNION") {
                    let q = self.group_pattern()?;
                    p = Pattern::Union(Box::new(p), Box::new(q));
                }
                g = join(g, p);
            } else {
                let mut elts = Vec::new();
                self.triples_block(&mut elts)?;
                // Paths are planned with the triples: the BGP evaluates the
                // most-bound element first, whichever kind it is.
                let triples: Vec<Triple> = elts
                    .into_iter()
                    .map(|e| match e {
                        Elt::T(t) => t,
                        Elt::P(s, p, o) => Triple { s, p: Tv::Term(NONE), o, path: Some(Box::new(p)) },
                    })
                    .collect();
                if !triples.is_empty() {
                    g = join(g, Pattern::Bgp(triples));
                }
                if !self.eat(b".") && self.at_triple_start() {
                    return self.c.err("expected .");
                }
                continue;
            }
            self.eat(b".");
        }
        self.bgp += 1;
        let mut f = filters.pop();
        while let Some(x) = filters.pop() {
            f = Some(Expr::And(Box::new(x), Box::new(f.unwrap())));
        }
        Ok((g, f))
    }

    fn var_or_iri(&mut self) -> Res<Tv> {
        self.ws();
        if self.at_var() { Ok(Tv::Var(self.var()?)) } else { Ok(Tv::Term(self.iri()?)) }
    }

    /// VALUES data (after the keyword).
    fn data_block(&mut self) -> Res<Pattern> {
        self.ws();
        let mut vars = Vec::new();
        let mut rows = Vec::new();
        if self.at_var() {
            vars.push(self.var()?);
            self.expect(b"{", "expected {")?;
            while !self.eat(b"}") {
                let t = self.data_value()?;
                rows.push(vec![t]);
            }
        } else {
            self.expect(b"(", "expected (")?;
            while !self.eat(b")") {
                let v = self.var()?;
                if vars.contains(&v) {
                    return self.c.err("a variable appears twice in VALUES");
                }
                vars.push(v);
            }
            self.expect(b"{", "expected {")?;
            while !self.eat(b"}") {
                self.expect(b"(", "expected (")?;
                let mut row = Vec::new();
                while !self.eat(b")") {
                    row.push(self.data_value()?);
                }
                if row.len() != vars.len() {
                    return self.c.err("VALUES row has the wrong number of values");
                }
                rows.push(row);
            }
        }
        Ok(Pattern::Values(vars, rows))
    }

    fn data_value(&mut self) -> Res<u32> {
        self.ws();
        if self.kw(b"UNDEF") {
            return Ok(NONE);
        }
        if self.c.eof() {
            return self.c.err("unterminated VALUES");
        }
        match self.term()? {
            // Data must be RDF terms: a triple term's subject is an IRI or blank node.
            Tv::Term(t) if !self.dict.term(t).is_some_and(|x| x.kind == crate::dict::TRIPLE && !self.dict.term(x.a).is_some_and(|s| matches!(s.kind, crate::dict::IRI | crate::dict::BNODE))) => Ok(t),
            Tv::Term(_) => self.c.err("the subject of a triple term must be an IRI or blank node"),
            _ => self.c.err("expected a term in VALUES"),
        }
    }

    // --- triples -------------------------------------------------------------------

    /// Triples up to (not including) `end`, for templates: plain triples only.
    fn triples_block_until(&mut self, end: &[u8], out: &mut Vec<[Tv; 3]>) -> Res<()> {
        let mut elts = Vec::new();
        loop {
            if self.at(end) {
                break;
            }
            self.triples_block(&mut elts)?;
            if !self.eat(b".") {
                break;
            }
        }
        for e in elts {
            match e {
                Elt::T(t) => out.push([t.s, t.p, t.o]),
                Elt::P(..) => return self.c.err("a property path is not allowed in a template"),
            }
        }
        Ok(())
    }

    /// TriplesSameSubjectPath ('.' TriplesSameSubjectPath)*, stopping before anything else.
    fn triples_block(&mut self, out: &mut Vec<Elt>) -> Res<()> {
        loop {
            self.ws();
            let subject = match self.c.peek() {
                b'[' if !self.anon_ahead() => {
                    let s = self.property_list_node(out)?;
                    self.ws();
                    if self.at_verb() {
                        self.property_list(s.clone(), out)?;
                    }
                    s
                }
                b'(' if !self.c.starts(b"()") && !self.nil_ahead() => {
                    let s = self.collection(out)?;
                    if self.at_verb() {
                        self.property_list(s.clone(), out)?;
                    }
                    s
                }
                b'<' if self.c.starts(b"<<") && !self.c.starts(b"<<(") => {
                    let s = self.reified(out)?;
                    self.ws();
                    if self.at_verb() {
                        self.property_list(s.clone(), out)?;
                    }
                    s
                }
                _ => {
                    let s = self.graph_node(out)?;
                    self.property_list(s.clone(), out)?;
                    s
                }
            };
            let _ = subject;
            self.ws();
            if !self.c.starts(b".") {
                return Ok(());
            }
            // A '.' ends this triples block if no further triple follows.
            let save = (self.c.i, self.c.line);
            self.c.i += 1;
            self.ws();
            if !self.at_triple_start() {
                (self.c.i, self.c.line) = save;
                return Ok(());
            }
        }
    }

    fn at_triple_start(&mut self) -> bool {
        self.ws();
        let c = self.c.peek();
        if self.c.eof() || c == b'}' || c == b'{' {
            return false;
        }
        for kw in [&b"OPTIONAL"[..], b"FILTER", b"BIND", b"VALUES", b"GRAPH", b"MINUS", b"SERVICE", b"LATERAL"] {
            if self.at_kw(kw) {
                return false;
            }
        }
        true
    }

    fn at_verb(&mut self) -> bool {
        self.ws();
        let c = self.c.peek();
        if self.c.eof() || matches!(c, b'.' | b'}' | b']' | b'|' | b';' | b')' | b'{' | b',') || self.c.starts(b">>") {
            return false;
        }
        for kw in [&b"OPTIONAL"[..], b"FILTER", b"BIND", b"VALUES", b"GRAPH", b"MINUS", b"SERVICE", b"LATERAL"] {
            if self.at_kw(kw) {
                return false;
            }
        }
        true
    }

    /// A subject or object inside a triple term or reified triple: no collections or property lists.
    fn inner_node(&mut self) -> Res<Tv> {
        self.ws();
        if self.c.peek() == b'(' || self.c.peek() == b'[' && !self.anon_ahead() {
            return self.c.err("a collection or property list is not allowed here");
        }
        self.var_or_term()
    }

    fn anon_ahead(&self) -> bool {
        let mut i = self.c.i + 1;
        while let Some(&b) = self.c.s.get(i) {
            match b {
                b' ' | b'\t' | b'\r' | b'\n' => i += 1,
                b']' => return true,
                _ => return false,
            }
        }
        false
    }

    fn nil_ahead(&self) -> bool {
        let mut i = self.c.i + 1;
        while let Some(&b) = self.c.s.get(i) {
            match b {
                b' ' | b'\t' | b'\r' | b'\n' => i += 1,
                b')' => return true,
                _ => return false,
            }
        }
        false
    }

    fn property_list(&mut self, s: Tv, out: &mut Vec<Elt>) -> Res<()> {
        loop {
            self.ws();
            if !self.at_verb() {
                return self.c.err("expected predicate");
            }
            let verb = self.verb()?;
            self.object_list(&s, verb, out)?;
            if !self.eat(b";") {
                return Ok(());
            }
            while self.eat(b";") {}
            if !self.at_verb() {
                return Ok(());
            }
        }
    }

    /// A verb: a variable, or a path (a plain IRI path is returned as a term).
    fn verb(&mut self) -> Res<Result<Tv, Path>> {
        self.ws();
        if self.at_var() {
            return Ok(Ok(Tv::Var(self.var()?)));
        }
        if self.in_template {
            if self.c.peek() == b'a' && self.a_ahead() {
                self.c.i += 1;
                return Ok(Ok(Tv::Term(self.rdf(b"type"))));
            }
            return Ok(Ok(Tv::Term(self.iri()?)));
        }
        match self.path()? {
            Path::Iri(i) => Ok(Ok(Tv::Term(i))),
            p => Ok(Err(p)),
        }
    }

    fn a_ahead(&self) -> bool {
        let n = self.c.at(1);
        !(n.is_ascii_alphanumeric() || matches!(n, b':' | b'_' | b'-' | b'.') || n >= 0x80)
    }

    fn object_list(&mut self, s: &Tv, verb: Result<Tv, Path>, out: &mut Vec<Elt>) -> Res<()> {
        loop {
            let o = self.graph_node(out)?;
            match &verb {
                Ok(p) => {
                    out.push(Elt::T(Triple { s: s.clone(), p: p.clone(), o: o.clone() , path: None }));
                    self.annotation(s, p, &o, out)?;
                }
                Err(path) => out.push(Elt::P(s.clone(), path.clone(), o)),
            }
            if !self.eat(b",") {
                return Ok(());
            }
        }
    }

    fn annotation(&mut self, s: &Tv, p: &Tv, o: &Tv, out: &mut Vec<Elt>) -> Res<()> {
        let mut reifier: Option<Tv> = None;
        loop {
            self.ws();
            if self.c.peek() == b'~' {
                let r = self.reifier()?;
                self.reifies(&r, s, p, o, out);
                reifier = Some(r);
            } else if self.c.starts(b"{|") {
                self.c.i += 2;
                let r = match reifier.take() {
                    Some(r) => r,
                    None => {
                        let r = self.anon_reifier();
                        self.reifies(&r, s, p, o, out);
                        r
                    }
                };
                self.property_list(r, out)?;
                self.expect(b"|}", "expected |}")?;
            } else {
                return Ok(());
            }
        }
    }

    fn reifies(&mut self, r: &Tv, s: &Tv, p: &Tv, o: &Tv, out: &mut Vec<Elt>) {
        let reifies = self.rdf(b"reifies");
        out.push(Elt::T(Triple { s: r.clone(), p: Tv::Term(reifies), o: Tv::Triple(Box::new([s.clone(), p.clone(), o.clone()])) , path: None }));
    }

    fn reifier(&mut self) -> Res<Tv> {
        self.c.i += 1;
        self.ws();
        if self.at_var() {
            return Ok(Tv::Var(self.var()?));
        }
        if self.at_iri() {
            return Ok(Tv::Term(self.iri()?));
        }
        if self.c.starts(b"_:") || self.c.peek() == b'[' && self.anon_ahead() {
            return self.bnode();
        }
        Ok(self.anon_reifier())
    }

    fn anon_reifier(&mut self) -> Tv {
        let v = self.fresh_var();
        self.anon_reifiers.push(v);
        Tv::Var(v)
    }

    fn reified(&mut self, out: &mut Vec<Elt>) -> Res<Tv> {
        self.c.i += 2;
        self.ws();
        let s = if self.c.starts(b"<<") && !self.c.starts(b"<<(") { self.reified(out)? } else { self.inner_node()? };
        self.ws();
        let p = match self.verb()? {
            Ok(p) => p,
            Err(_) => return self.c.err("a path cannot be reified"),
        };
        self.ws();
        let o = if self.c.starts(b"<<") && !self.c.starts(b"<<(") { self.reified(out)? } else { self.inner_node()? };
        self.ws();
        let r = if self.c.peek() == b'~' { self.reifier()? } else { self.anon_reifier() };
        self.expect(b">>", "expected >>")?;
        self.reifies(&r, &s, &p, &o, out);
        Ok(r)
    }

    /// A node in object or subject position, possibly generating triples.
    fn graph_node(&mut self, out: &mut Vec<Elt>) -> Res<Tv> {
        self.ws();
        match self.c.peek() {
            b'[' if !self.anon_ahead() => self.property_list_node(out),
            b'(' if !self.nil_ahead() => self.collection(out),
            b'<' if self.c.starts(b"<<") && !self.c.starts(b"<<(") => self.reified(out),
            _ => self.var_or_term(),
        }
    }

    fn property_list_node(&mut self, out: &mut Vec<Elt>) -> Res<Tv> {
        self.c.i += 1;
        let b = Tv::Var(self.fresh_var());
        self.property_list(b.clone(), out)?;
        self.expect(b"]", "expected ]")?;
        Ok(b)
    }

    fn collection(&mut self, out: &mut Vec<Elt>) -> Res<Tv> {
        self.c.i += 1;
        let (first, rest, nil) = (self.rdf(b"first"), self.rdf(b"rest"), self.rdf(b"nil"));
        let mut items = Vec::new();
        while !self.eat(b")") {
            if self.c.eof() {
                return self.c.err("unterminated collection");
            }
            items.push(self.graph_node(out)?);
        }
        let mut next = Tv::Term(nil);
        for item in items.into_iter().rev() {
            let cell = Tv::Var(self.fresh_var());
            out.push(Elt::T(Triple { s: cell.clone(), p: Tv::Term(first), o: item , path: None }));
            out.push(Elt::T(Triple { s: cell.clone(), p: Tv::Term(rest), o: next , path: None }));
            next = cell;
        }
        Ok(next)
    }

    fn var_or_term(&mut self) -> Res<Tv> {
        self.ws();
        if self.at_var() {
            return Ok(Tv::Var(self.var()?));
        }
        self.term()
    }

    fn bnode(&mut self) -> Res<Tv> {
        if self.c.eat(b'[') {
            self.ws();
            self.c.expect(b']', "expected ]")?;
            return Ok(Tv::Var(self.fresh_var()));
        }
        self.c.eat_str(b"_:");
        let mut label = Vec::new();
        self.c.bnode_label(&mut label)?;
        let key = bytes_key(&label);
        if self.in_template {
            if let Some(v) = self.tbnodes.get(&key) {
                return Ok(Tv::Var(v));
            }
            let v = self.fresh_var();
            self.tbnodes.insert(&key, v);
            self.tbnode_labels.push(key);
            return Ok(Tv::Var(v));
        }
        if let Some(i) = self.bnodes.get(&key) {
            let (v, bgp) = self.bnode_vars[i as usize];
            if bgp != self.bgp {
                return self.c.err("a blank node label is used in two basic graph patterns");
            }
            return Ok(Tv::Var(v));
        }
        let v = self.fresh_var();
        self.bnode_vars.push((v, self.bgp));
        self.bnodes.insert(&key, self.bnode_vars.len() as u32 - 1);
        Ok(Tv::Var(v))
    }

    /// A constant term (or blank node, or triple term) in a pattern or VALUES.
    fn term(&mut self) -> Res<Tv> {
        self.ws();
        let c = self.c.peek();
        match c {
            b'<' if self.c.starts(b"<<(") => {
                self.c.i += 3;
                let s = self.inner_node()?;
                let p = match self.verb()? {
                    Ok(p) => p,
                    Err(_) => return self.c.err("a path cannot be in a triple term"),
                };
                let o = self.inner_node()?;
                self.expect(b")>>", "expected )>>")?;
                if let (Tv::Term(s), Tv::Term(p), Tv::Term(o)) = (&s, &p, &o) {
                    return Ok(Tv::Term(self.dict.intern(Key::triple(*s, *p, *o))));
                }
                Ok(Tv::Triple(Box::new([s, p, o])))
            }
            b'_' if self.c.starts(b"_:") => self.bnode(),
            b'[' if self.anon_ahead() => self.bnode(),
            b'(' if self.nil_ahead() => {
                while !self.c.eat(b')') {
                    self.c.i += 1;
                }
                Ok(Tv::Term(self.rdf(b"nil")))
            }
            b'"' | b'\'' => Ok(Tv::Term(self.literal()?)),
            b'0'..=b'9' | b'.' | b'+' | b'-' => Ok(Tv::Term(self.number()?)),
            _ if self.at_kw(b"true") => {
                self.c.i += 4;
                Ok(Tv::Term(self.boolean(b"true")))
            }
            _ if self.at_kw(b"false") => {
                self.c.i += 5;
                Ok(Tv::Term(self.boolean(b"false")))
            }
            _ if self.at_iri() => Ok(Tv::Term(self.iri()?)),
            _ => self.c.err("expected a term"),
        }
    }

    fn boolean(&mut self, v: &[u8]) -> u32 {
        let dt = self.iri_id(&concat(XSD, b"boolean"));
        self.dict.intern(Key::literal(v, dt))
    }

    fn literal(&mut self) -> Res<u32> {
        let q = self.c.peek();
        let mut value = Vec::new();
        if self.c.starts(&[q, q, q]) {
            self.c.i += 3;
            loop {
                if self.c.eof() {
                    return self.c.err("unterminated string");
                }
                let b = self.c.peek();
                if b == q && self.c.starts(&[q, q, q]) {
                    self.c.i += 3;
                    break;
                }
                if b == b'\\' {
                    self.c.escape(&mut value)?;
                } else {
                    if b == b'\n' {
                        self.c.line += 1;
                    }
                    value.push(b);
                    self.c.i += 1;
                }
            }
        } else {
            self.c.string(q, &mut value)?;
        }
        if self.c.eat(b'@') {
            let mut lang = Vec::new();
            let dir = self.c.langdir(&mut lang)?;
            return Ok(self.dict.intern(Key::lang_literal(&value, &lang, dir)));
        }
        let mut dt = NONE;
        if self.c.eat_str(b"^^") {
            dt = self.iri()?;
            if dt == RDF_LANG_STRING || dt == RDF_DIR_LANG_STRING {
                return self.c.err("a language-tagged string needs a language tag");
            }
        }
        Ok(self.dict.intern(Key::literal(&value, dt)))
    }

    fn number(&mut self) -> Res<u32> {
        let start = self.c.i;
        if matches!(self.c.peek(), b'+' | b'-') {
            self.c.i += 1;
        }
        let digits = |c: &mut Cur| {
            let s = c.i;
            while c.peek().is_ascii_digit() {
                c.i += 1;
            }
            c.i - s
        };
        let int = digits(&mut self.c);
        let mut kind: &[u8] = b"integer";
        if self.c.peek() == b'.' && (self.c.at(1).is_ascii_digit() || int > 0 && matches!(self.c.at(1), b'e' | b'E')) {
            self.c.i += 1;
            if digits(&mut self.c) > 0 {
                kind = b"decimal";
            }
        }
        if int == 0 && kind == b"integer" {
            return self.c.err("bad number");
        }
        if matches!(self.c.peek(), b'e' | b'E') {
            self.c.i += 1;
            if matches!(self.c.peek(), b'+' | b'-') {
                self.c.i += 1;
            }
            if digits(&mut self.c) == 0 {
                return self.c.err("bad exponent");
            }
            kind = b"double";
        }
        let text = self.c.s[start..self.c.i].to_vec();
        let dt = self.iri_id(&concat(XSD, kind));
        Ok(self.dict.intern(Key::literal(&text, dt)))
    }

    // --- paths ---------------------------------------------------------------------

    fn path(&mut self) -> Res<Path> {
        let mut p = self.path_seq()?;
        while self.eat(b"|") {
            let q = self.path_seq()?;
            p = Path::Alt(Box::new(p), Box::new(q));
        }
        Ok(p)
    }

    fn path_seq(&mut self) -> Res<Path> {
        let mut p = self.path_elt_or_inverse()?;
        while self.eat(b"/") {
            let q = self.path_elt_or_inverse()?;
            p = Path::Seq(Box::new(p), Box::new(q));
        }
        Ok(p)
    }

    fn path_elt_or_inverse(&mut self) -> Res<Path> {
        if self.eat(b"^") {
            let p = self.path_elt()?;
            return Ok(Path::Inverse(Box::new(p)));
        }
        self.path_elt()
    }

    fn path_elt(&mut self) -> Res<Path> {
        let p = self.path_primary()?;
        // A modifier directly follows (a '?' starting a variable is not one).
        match self.c.peek() {
            b'*' => {
                self.c.i += 1;
                Ok(Path::ZeroOrMore(Box::new(p)))
            }
            b'+' => {
                self.c.i += 1;
                Ok(Path::OneOrMore(Box::new(p)))
            }
            b'?' if !self.at_var() => {
                self.c.i += 1;
                Ok(Path::ZeroOrOne(Box::new(p)))
            }
            _ => Ok(p),
        }
    }

    fn path_primary(&mut self) -> Res<Path> {
        self.ws();
        if self.c.peek() == b'a' && self.a_ahead() {
            self.c.i += 1;
            return Ok(Path::Iri(self.rdf(b"type")));
        }
        if self.eat(b"(") {
            let p = self.path()?;
            self.expect(b")", "expected )")?;
            return Ok(p);
        }
        if self.eat(b"!") {
            let (mut fwd, mut inv) = (Vec::new(), Vec::new());
            let mut one = |me: &mut Self| -> Res<()> {
                me.ws();
                let inverse = me.eat(b"^");
                me.ws();
                let i = if me.c.peek() == b'a' && me.a_ahead() {
                    me.c.i += 1;
                    me.rdf(b"type")
                } else {
                    me.iri()?
                };
                if inverse { inv.push(i) } else { fwd.push(i) }
                Ok(())
            };
            if self.eat(b"(") {
                if !self.eat(b")") {
                    loop {
                        one(self)?;
                        if !self.eat(b"|") {
                            break;
                        }
                    }
                    self.expect(b")", "expected )")?;
                }
            } else {
                one(self)?;
            }
            return Ok(Path::Negated(fwd, inv));
        }
        if !self.at_iri() {
            return self.c.err("expected predicate");
        }
        Ok(Path::Iri(self.iri()?))
    }

    // --- expressions -----------------------------------------------------------------

    fn constraint(&mut self) -> Res<Expr> {
        self.ws();
        if self.c.peek() == b'(' {
            return self.bracketted();
        }
        if self.at_call() {
            return self.primary();
        }
        self.c.err("expected constraint")
    }

    fn bracketted(&mut self) -> Res<Expr> {
        self.expect(b"(", "expected (")?;
        let e = self.expr()?;
        self.expect(b")", "expected )")?;
        Ok(e)
    }

    /// Whether a built-in call or function call starts here.
    fn at_call(&mut self) -> bool {
        self.ws();
        if self.at_iri() {
            return true;
        }
        let s = &self.c.s[self.c.i..];
        let n = s.iter().take_while(|b| b.is_ascii_alphanumeric() || **b == b'_').count();
        n > 0 && builtin(&s[..n]).is_some() || self.at_kw(b"NOT") || self.at_kw(b"EXISTS")
    }

    pub fn expr(&mut self) -> Res<Expr> {
        let mut e = self.and_expr()?;
        while self.eat(b"||") {
            let r = self.and_expr()?;
            e = Expr::Or(Box::new(e), Box::new(r));
        }
        Ok(e)
    }

    fn and_expr(&mut self) -> Res<Expr> {
        let mut e = self.rel_expr()?;
        while self.eat(b"&&") {
            let r = self.rel_expr()?;
            e = Expr::And(Box::new(e), Box::new(r));
        }
        Ok(e)
    }

    fn rel_expr(&mut self) -> Res<Expr> {
        let e = self.add_expr()?;
        self.ws();
        let op = if self.eat(b"=") {
            Cmp::Eq
        } else if self.eat(b"!=") {
            Cmp::Ne
        } else if self.eat(b"<=") {
            Cmp::Le
        } else if self.eat(b">=") {
            Cmp::Ge
        } else if self.c.peek() == b'<' && !self.c.starts(b"<<") && !self.iriref_ahead() {
            self.c.i += 1;
            Cmp::Lt
        } else if self.c.peek() == b'>' && !self.c.starts(b">>") {
            self.c.i += 1;
            Cmp::Gt
        } else if self.kw(b"IN") {
            let list = self.expr_list()?;
            return Ok(Expr::In(Box::new(e), list, false));
        } else if self.at_kw(b"NOT") {
            let save = (self.c.i, self.c.line);
            self.c.i += 3;
            if self.kw(b"IN") {
                let list = self.expr_list()?;
                return Ok(Expr::In(Box::new(e), list, true));
            }
            (self.c.i, self.c.line) = save;
            return Ok(e);
        } else {
            return Ok(e);
        };
        let r = self.add_expr()?;
        Ok(Expr::Cmp(op, Box::new(e), Box::new(r)))
    }

    fn expr_list(&mut self) -> Res<Vec<Expr>> {
        self.expect(b"(", "expected (")?;
        let mut list = Vec::new();
        if self.eat(b")") {
            return Ok(list);
        }
        loop {
            list.push(self.expr()?);
            if self.eat(b")") {
                return Ok(list);
            }
            self.expect(b",", "expected , or )")?;
        }
    }

    fn add_expr(&mut self) -> Res<Expr> {
        let mut e = self.mul_expr()?;
        loop {
            self.ws();
            let op = match self.c.peek() {
                b'+' => Op::Add,
                b'-' => Op::Sub,
                _ => return Ok(e),
            };
            self.c.i += 1;
            let r = self.mul_expr()?;
            e = Expr::Arith(op, Box::new(e), Box::new(r));
        }
    }

    fn mul_expr(&mut self) -> Res<Expr> {
        let mut e = self.unary()?;
        loop {
            self.ws();
            let op = match self.c.peek() {
                b'*' => Op::Mul,
                b'/' => Op::Div,
                _ => return Ok(e),
            };
            self.c.i += 1;
            let r = self.unary()?;
            e = Expr::Arith(op, Box::new(e), Box::new(r));
        }
    }

    fn unary(&mut self) -> Res<Expr> {
        self.ws();
        match self.c.peek() {
            b'!' if !self.c.starts(b"!=") => {
                self.c.i += 1;
                Ok(Expr::Not(Box::new(self.unary()?)))
            }
            b'-' => {
                self.c.i += 1;
                Ok(Expr::Neg(Box::new(self.unary()?)))
            }
            b'+' => {
                self.c.i += 1;
                Ok(Expr::Plus(Box::new(self.unary()?)))
            }
            _ => self.primary(),
        }
    }

    fn primary(&mut self) -> Res<Expr> {
        self.ws();
        let c = self.c.peek();
        if c == b'(' {
            return self.bracketted();
        }
        if self.at_var() {
            return Ok(Expr::Var(self.var()?));
        }
        if c == b'<' && self.c.starts(b"<<(") {
            self.c.i += 3;
            let s = self.primary()?;
            let bad_subject = match &s {
                Expr::Call(Func::Triple, _) => true,
                Expr::Const(c) => self.dict.term(*c).is_some_and(|t| matches!(t.kind, crate::dict::LITERAL | crate::dict::TRIPLE)),
                _ => false,
            };
            if bad_subject {
                return self.c.err("the subject of a triple term must be an IRI or blank node");
            }
            let p = self.primary()?;
            let o = self.primary()?;
            self.expect(b")>>", "expected )>>")?;
            return Ok(Expr::Call(Func::Triple, vec![s, p, o]));
        }
        if matches!(c, b'"' | b'\'') {
            return Ok(Expr::Const(self.literal()?));
        }
        if c.is_ascii_digit() || c == b'.' && self.c.at(1).is_ascii_digit() {
            return Ok(Expr::Const(self.number()?));
        }
        if self.kw(b"true") {
            return Ok(Expr::Const(self.boolean(b"true")));
        }
        if self.kw(b"false") {
            return Ok(Expr::Const(self.boolean(b"false")));
        }
        if self.at_iri() {
            let iri = self.iri_string()?;
            self.ws();
            if self.c.peek() != b'(' {
                return Ok(Expr::Const(self.iri_id(&iri)));
            }
            let args = self.arg_list()?;
            if let Some(local) = iri.strip_prefix(XSD) {
                if CASTS.contains(&local) && args.len() == 1 {
                    let dt = self.iri_id(&iri);
                    return Ok(Expr::Cast(dt, Box::new(args.into_iter().next().unwrap())));
                }
            }
            return Ok(Expr::Unknown);
        }
        self.builtin_call()
    }

    fn arg_list(&mut self) -> Res<Vec<Expr>> {
        self.expect(b"(", "expected (")?;
        let mut args = Vec::new();
        if self.eat(b")") {
            return Ok(args);
        }
        self.kw(b"DISTINCT");
        loop {
            args.push(self.expr()?);
            if self.eat(b")") {
                return Ok(args);
            }
            self.expect(b",", "expected , or )")?;
        }
    }

    fn builtin_call(&mut self) -> Res<Expr> {
        self.ws();
        if self.kw(b"NOT") {
            self.expect_kw(b"EXISTS", "expected EXISTS")?;
            let p = self.group_pattern()?;
            return Ok(Expr::Exists(Box::new(p), true));
        }
        if self.kw(b"EXISTS") {
            let p = self.group_pattern()?;
            return Ok(Expr::Exists(Box::new(p), false));
        }
        let s = &self.c.s[self.c.i..];
        let n = s.iter().take_while(|b| b.is_ascii_alphanumeric() || **b == b'_').count();
        let name: Vec<u8> = s[..n].iter().map(u8::to_ascii_uppercase).collect();
        let Some(kind) = builtin(&name) else {
            return self.c.err("expected expression");
        };
        self.c.i += n;
        match kind {
            Builtin::Agg(k) => self.aggregate(k),
            Builtin::Bound => {
                self.expect(b"(", "expected (")?;
                let v = self.var()?;
                self.expect(b")", "expected )")?;
                Ok(Expr::Bound(v))
            }
            Builtin::If => {
                let a = self.arg_list()?;
                if a.len() != 3 {
                    return self.c.err("IF takes 3 arguments");
                }
                let mut a = a.into_iter();
                Ok(Expr::If(Box::new(a.next().unwrap()), Box::new(a.next().unwrap()), Box::new(a.next().unwrap())))
            }
            Builtin::Coalesce => Ok(Expr::Coalesce(self.arg_list()?)),
            Builtin::F(f, min, max) => {
                let args = if self.at(b"(") {
                    self.arg_list()?
                } else if f == Func::Bnode || f == Func::Rand || f == Func::Now || f == Func::Uuid || f == Func::StrUuid {
                    return self.c.err("expected (");
                } else {
                    return self.c.err("expected (");
                };
                if args.len() < min || args.len() > max {
                    return self.c.err("wrong number of arguments");
                }
                Ok(Expr::Call(f, args))
            }
        }
    }

    fn aggregate(&mut self, kind: AggKind) -> Res<Expr> {
        if !self.agg_ok {
            return self.c.err("aggregate outside SELECT, HAVING or ORDER BY");
        }
        self.expect(b"(", "expected (")?;
        let distinct = self.kw(b"DISTINCT");
        let mut agg = Aggregate { kind, distinct, expr: None, separator: b" ".to_vec() };
        if kind == AggKind::Count && self.eat(b"*") {
        } else {
            // Nested aggregates are not allowed.
            self.agg_ok = false;
            agg.expr = Some(self.expr()?);
            self.agg_ok = true;
        }
        if kind == AggKind::GroupConcat && self.eat(b";") {
            self.expect_kw(b"SEPARATOR", "expected SEPARATOR")?;
            self.expect(b"=", "expected =")?;
            self.ws();
            let q = self.c.peek();
            if !matches!(q, b'"' | b'\'') {
                return self.c.err("expected separator string");
            }
            let mut sep = Vec::new();
            self.c.string(q, &mut sep)?;
            agg.separator = sep;
        }
        self.expect(b")", "expected )")?;
        let v = self.fresh_var();
        self.aggs.push((v, agg));
        Ok(Expr::Var(v))
    }
}

enum Builtin {
    F(Func, usize, usize),
    Agg(AggKind),
    Bound,
    If,
    Coalesce,
}

fn builtin(name: &[u8]) -> Option<Builtin> {
    let upper: Vec<u8> = name.iter().map(u8::to_ascii_uppercase).collect();
    use Func::*;
    let f = |f, a, b| Some(Builtin::F(f, a, b));
    match &upper[..] {
        b"STR" => f(Str, 1, 1),
        b"LANG" => f(Lang, 1, 1),
        b"LANGMATCHES" => f(LangMatches, 2, 2),
        b"DATATYPE" => f(Datatype, 1, 1),
        b"IRI" | b"URI" => f(Iri, 1, 1),
        b"BNODE" => f(Bnode, 0, 1),
        b"RAND" => f(Rand, 0, 0),
        b"ABS" => f(Abs, 1, 1),
        b"CEIL" => f(Ceil, 1, 1),
        b"FLOOR" => f(Floor, 1, 1),
        b"ROUND" => f(Round, 1, 1),
        b"CONCAT" => f(Concat, 0, usize::MAX),
        b"SUBSTR" => f(SubStr, 2, 3),
        b"STRLEN" => f(StrLen, 1, 1),
        b"REPLACE" => f(Replace, 3, 4),
        b"UCASE" => f(UCase, 1, 1),
        b"LCASE" => f(LCase, 1, 1),
        b"ENCODE_FOR_URI" => f(EncodeForUri, 1, 1),
        b"CONTAINS" => f(Contains, 2, 2),
        b"STRSTARTS" => f(StrStarts, 2, 2),
        b"STRENDS" => f(StrEnds, 2, 2),
        b"STRBEFORE" => f(StrBefore, 2, 2),
        b"STRAFTER" => f(StrAfter, 2, 2),
        b"YEAR" => f(Year, 1, 1),
        b"MONTH" => f(Month, 1, 1),
        b"DAY" => f(Day, 1, 1),
        b"HOURS" => f(Hours, 1, 1),
        b"MINUTES" => f(Minutes, 1, 1),
        b"SECONDS" => f(Seconds, 1, 1),
        b"TIMEZONE" => f(Timezone, 1, 1),
        b"TZ" => f(Tz, 1, 1),
        b"NOW" => f(Now, 0, 0),
        b"UUID" => f(Uuid, 0, 0),
        b"STRUUID" => f(StrUuid, 0, 0),
        b"MD5" => f(Md5, 1, 1),
        b"SHA1" => f(Sha1, 1, 1),
        b"SHA256" => f(Sha256, 1, 1),
        b"SHA384" => f(Sha384, 1, 1),
        b"SHA512" => f(Sha512, 1, 1),
        b"STRLANG" => f(StrLang, 2, 2),
        b"STRLANGDIR" => f(StrLangDir, 3, 3),
        b"STRDT" => f(StrDt, 2, 2),
        b"SAMETERM" => f(SameTerm, 2, 2),
        b"ISIRI" | b"ISURI" => f(IsIri, 1, 1),
        b"ISBLANK" => f(IsBlank, 1, 1),
        b"ISLITERAL" => f(IsLiteral, 1, 1),
        b"ISNUMERIC" => f(IsNumeric, 1, 1),
        b"REGEX" => f(Regex, 2, 3),
        b"TRIPLE" => f(Triple, 3, 3),
        b"SUBJECT" => f(Subject, 1, 1),
        b"PREDICATE" => f(Predicate, 1, 1),
        b"OBJECT" => f(Object, 1, 1),
        b"ISTRIPLE" => f(IsTriple, 1, 1),
        b"LANGDIR" => f(LangDir, 1, 1),
        b"HASLANG" => f(HasLang, 1, 1),
        b"HASLANGDIR" => f(HasLangDir, 1, 1),
        b"COUNT" => Some(Builtin::Agg(AggKind::Count)),
        b"SUM" => Some(Builtin::Agg(AggKind::Sum)),
        b"MIN" => Some(Builtin::Agg(AggKind::Min)),
        b"MAX" => Some(Builtin::Agg(AggKind::Max)),
        b"AVG" => Some(Builtin::Agg(AggKind::Avg)),
        b"SAMPLE" => Some(Builtin::Agg(AggKind::Sample)),
        b"GROUP_CONCAT" => Some(Builtin::Agg(AggKind::GroupConcat)),
        b"BOUND" => Some(Builtin::Bound),
        b"IF" => Some(Builtin::If),
        b"COALESCE" => Some(Builtin::Coalesce),
        _ => None,
    }
}

fn empty_select() -> Select {
    Select {
        pattern: Pattern::Unit,
        group: None,
        aggregates: Vec::new(),
        having: Vec::new(),
        extend: Vec::new(),
        order: Vec::new(),
        project: Vec::new(),
        distinct: false,
        reduced: false,
        offset: 0,
        limit: None,
        values: None,
    }
}

fn tv_vars(t: &Tv, out: &mut Vec<Var>) {
    match t {
        Tv::Var(v) => {
            if !out.contains(v) {
                out.push(*v)
            }
        }
        Tv::Triple(b) => b.iter().for_each(|x| tv_vars(x, out)),
        Tv::Term(_) => {}
    }
}

/// Variables a pattern may bind (its in-scope variables), in order of appearance.
pub fn pattern_vars(p: &Pattern, out: &mut Vec<Var>) {
    let add = |v: Var, out: &mut Vec<Var>| {
        if !out.contains(&v) {
            out.push(v)
        }
    };
    match p {
        Pattern::Unit | Pattern::Service(_) => {}
        Pattern::Bgp(ts) => {
            for t in ts {
                tv_vars(&t.s, out);
                tv_vars(&t.p, out);
                tv_vars(&t.o, out);
            }
        }
        Pattern::Join(a, b) | Pattern::LeftJoin(a, b, _) | Pattern::Union(a, b) | Pattern::Lateral(a, b) => {
            pattern_vars(a, out);
            pattern_vars(b, out);
        }
        Pattern::Minus(a, _) | Pattern::Filter(_, a) => pattern_vars(a, out),
        Pattern::Graph(g, a) => {
            tv_vars(g, out);
            pattern_vars(a, out);
        }
        Pattern::Extend(a, v, _) => {
            pattern_vars(a, out);
            add(*v, out);
        }
        Pattern::Values(vs, _) => vs.iter().for_each(|v| add(*v, out)),
        Pattern::Sub(s) => s.project.iter().for_each(|v| add(*v, out)),
    }
}

fn expr_vars_within(e: &Expr, allowed: &[Var]) -> bool {
    match e {
        Expr::Var(v) | Expr::Bound(v) => allowed.contains(v),
        Expr::Const(_) | Expr::Unknown | Expr::Exists(..) => true,
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => expr_vars_within(a, allowed) && expr_vars_within(b, allowed),
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => expr_vars_within(a, allowed),
        Expr::In(a, l, _) => expr_vars_within(a, allowed) && l.iter().all(|x| expr_vars_within(x, allowed)),
        Expr::Call(_, l) | Expr::Coalesce(l) => l.iter().all(|x| expr_vars_within(x, allowed)),
        Expr::If(a, b, c) => expr_vars_within(a, allowed) && expr_vars_within(b, allowed) && expr_vars_within(c, allowed),
    }
}

// --- SPARQL Update ---------------------------------------------------------------

impl P<'_, '_> {
    fn update(&mut self) -> Res<Update> {
        let mut ops = Vec::new();
        // Blank node labels in DATA may not be shared between the operations of a request.
        let mut labels = IdMap::new();
        loop {
            self.prologue()?;
            self.ws();
            if self.c.eof() {
                break;
            }
            let op = self.update_op()?;
            let data = matches!(op, UpdateOp::Data { .. });
            ops.push(op);
            for l in self.tbnode_labels.iter().filter(|_| data) {
                if !labels.add(l) {
                    return self.c.err("a blank node label is used in two operations");
                }
            }
            if !self.eat(b";") {
                break;
            }
        }
        self.ws();
        if !self.c.eof() {
            return self.c.err("unexpected text after the update");
        }
        Ok(Update { ops, base: core::mem::take(&mut self.base) })
    }

    fn update_op(&mut self) -> Res<UpdateOp> {
        self.vars.clear();
        self.bnodes = IdMap::new();
        self.bnode_vars.clear();
        self.tbnodes = IdMap::new();
        self.tbnode_labels.clear();
        self.template_bnodes = IdMap::new();
        self.hidden = 0;
        if self.kw(b"LOAD") {
            let silent = self.kw(b"SILENT");
            self.iri()?;
            if self.kw(b"INTO") {
                self.expect_kw(b"GRAPH", "expected GRAPH")?;
                self.iri()?;
            }
            return Ok(UpdateOp::Load { silent });
        }
        for (word, drop) in [(&b"CLEAR"[..], false), (b"DROP", true)] {
            if self.kw(word) {
                let _ = (self.kw(b"SILENT"), drop);
                let target = self.target(true)?;
                return Ok(UpdateOp::Clear { target });
            }
        }
        if self.kw(b"CREATE") {
            let silent = self.kw(b"SILENT");
            self.expect_kw(b"GRAPH", "expected GRAPH")?;
            let graph = self.iri()?;
            return Ok(UpdateOp::Create { silent, graph });
        }
        for (word, op) in [(&b"ADD"[..], Transfer::Add), (b"MOVE", Transfer::Move), (b"COPY", Transfer::Copy)] {
            if self.kw(word) {
                self.kw(b"SILENT");
                let from = self.graph_or_default()?;
                self.expect_kw(b"TO", "expected TO")?;
                let to = self.graph_or_default()?;
                return Ok(UpdateOp::Transfer { op, from, to });
            }
        }
        if self.kw(b"INSERT") {
            if self.kw(b"DATA") {
                return self.data(true);
            }
            return self.modify(None, Vec::new(), true);
        }
        if self.kw(b"DELETE") {
            if self.kw(b"DATA") {
                return self.data(false);
            }
            if self.kw(b"WHERE") {
                let quads = self.quad_pattern(false)?;
                self.no_bnodes(&quads)?;
                let mut select = empty_select();
                select.pattern = self.quads_to_pattern(&quads);
                select.project = (0..self.vars.len() as Var).collect();
                return Ok(UpdateOp::Modify { delete: quads, insert: Vec::new(), bnodes: 0, with: None, using: None, select, nvars: self.vars.len() });
            }
            let delete = self.quad_pattern(true)?;
            self.no_bnodes(&delete)?;
            let insert = self.kw(b"INSERT");
            return self.modify(None, delete, insert);
        }
        if self.kw(b"WITH") {
            let with = self.iri()?;
            if self.kw(b"DELETE") {
                let delete = self.quad_pattern(true)?;
                self.no_bnodes(&delete)?;
                let insert = self.kw(b"INSERT");
                return self.modify(Some(with), delete, insert);
            }
            self.expect_kw(b"INSERT", "expected DELETE or INSERT")?;
            return self.modify(Some(with), Vec::new(), true);
        }
        self.c.err("expected an update operation")
    }

    fn target(&mut self, all: bool) -> Res<Target> {
        if self.kw(b"GRAPH") {
            return Ok(Target::Graph(self.iri()?));
        }
        if self.kw(b"DEFAULT") {
            return Ok(Target::Default);
        }
        if all && self.kw(b"NAMED") {
            return Ok(Target::Named);
        }
        if all && self.kw(b"ALL") {
            return Ok(Target::All);
        }
        self.c.err("expected GRAPH, DEFAULT, NAMED or ALL")
    }

    /// `DEFAULT` (0) or a graph IRI.
    fn graph_or_default(&mut self) -> Res<u32> {
        if self.kw(b"DEFAULT") {
            return Ok(NONE);
        }
        self.kw(b"GRAPH");
        self.iri()
    }

    fn data(&mut self, insert: bool) -> Res<UpdateOp> {
        let quads = self.quad_pattern(true)?;
        let has_var = |t: &Tt| -> bool {
            fn any(t: &Tt) -> bool {
                match t {
                    Tt::Var(_) => true,
                    Tt::Triple(b) => b.iter().any(any),
                    _ => false,
                }
            }
            any(t)
        };
        if quads.iter().flatten().any(has_var) {
            return self.c.err("variables are not allowed in DATA");
        }
        if !insert {
            self.no_bnodes(&quads)?;
        }
        Ok(UpdateOp::Data { insert, quads, bnodes: self.template_bnodes.len() as u32 })
    }

    fn no_bnodes(&self, quads: &[QuadTemplate]) -> Res<()> {
        fn any(t: &Tt, hidden: &dyn Fn(Var) -> bool) -> bool {
            match t {
                Tt::Bnode(_) => true,
                Tt::Var(v) => hidden(*v),
                Tt::Triple(b) => b.iter().any(|x| any(x, hidden)),
                Tt::Term(_) => false,
            }
        }
        let hidden = |v: Var| self.vars[v as usize].first() == Some(&HIDDEN);
        if quads.iter().flatten().any(|t| any(t, &hidden)) {
            return self.c.err("blank nodes are not allowed when deleting");
        }
        Ok(())
    }

    fn modify(&mut self, with: Option<u32>, delete: Vec<QuadTemplate>, has_insert: bool) -> Res<UpdateOp> {
        let insert = if has_insert { self.quad_pattern(true)? } else { Vec::new() };
        let mut using: Option<(Vec<u32>, Vec<u32>)> = None;
        while self.kw(b"USING") {
            let u = using.get_or_insert_with(|| (Vec::new(), Vec::new()));
            if self.kw(b"NAMED") {
                let i = self.iri()?;
                u.1.push(i);
            } else {
                let i = self.iri()?;
                u.0.push(i);
            }
        }
        self.expect_kw(b"WHERE", "expected WHERE")?;
        let mut select = empty_select();
        select.pattern = self.group_pattern()?;
        select.project = (0..self.vars.len() as Var).collect();
        Ok(UpdateOp::Modify { delete, insert, bnodes: self.template_bnodes.len() as u32, with, using, select, nvars: self.vars.len() })
    }

    /// `{ triples GRAPH g { triples } ... }` as quad templates (graph 0: the default).
    fn quad_pattern(&mut self, template: bool) -> Res<Vec<QuadTemplate>> {
        self.expect(b"{", "expected {")?;
        let mut out = Vec::new();
        loop {
            self.ws();
            if self.eat(b"}") {
                break;
            }
            if self.c.eof() {
                return self.c.err("unterminated quad pattern");
            }
            let (graph, close) = if self.kw(b"GRAPH") {
                let g = self.var_or_iri()?;
                self.expect(b"{", "expected {")?;
                (self.to_template(g), true)
            } else {
                (Tt::Term(NONE), false)
            };
            loop {
                self.ws();
                if close && self.at(b"}") || !close && (self.at(b"}") || self.at_kw(b"GRAPH")) {
                    break;
                }
                self.in_template = template;
                let mut elts = Vec::new();
                let r = self.triples_block(&mut elts);
                self.in_template = false;
                r?;
                for e in elts {
                    match e {
                        Elt::T(t) => out.push([self.to_template(t.s), self.to_template(t.p), self.to_template(t.o), graph.clone()]),
                        Elt::P(..) => return self.c.err("a property path is not allowed here"),
                    }
                }
                if !self.eat(b".") {
                    break;
                }
            }
            if close {
                self.expect(b"}", "expected }")?;
                self.eat(b".");
            }
        }
        Ok(out)
    }

    /// DELETE WHERE: the quads as a pattern.
    fn quads_to_pattern(&self, quads: &[QuadTemplate]) -> Pattern {
        let back = |t: &Tt| -> Tv {
            fn f(t: &Tt) -> Tv {
                match t {
                    Tt::Term(i) => Tv::Term(*i),
                    Tt::Var(v) => Tv::Var(*v),
                    Tt::Bnode(_) => Tv::Term(NONE),
                    Tt::Triple(b) => Tv::Triple(Box::new([f(&b[0]), f(&b[1]), f(&b[2])])),
                }
            }
            f(t)
        };
        let mut default = Vec::new();
        let mut p = Pattern::Unit;
        for q in quads {
            let t = Triple { s: back(&q[0]), p: back(&q[1]), o: back(&q[2]) , path: None };
            match &q[3] {
                Tt::Term(NONE) => default.push(t),
                g => p = join(p, Pattern::Graph(back(g), Box::new(Pattern::Bgp(alloc::vec![t])))),
            }
        }
        join(Pattern::Bgp(default), p)
    }
}
