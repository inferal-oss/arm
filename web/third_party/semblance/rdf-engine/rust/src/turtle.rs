//! Turtle 1.2 and TriG 1.2: prefixes, relative IRIs, abbreviations,
//! collections, triple terms, reified triples and annotations.

use crate::dict::{Dict, Key, NONE, RDF_DIR_LANG_STRING, RDF_LANG_STRING};
use crate::iri;
use crate::lex::{Cur, Res, has_scheme, pn_chars, pn_chars_base, pn_chars_u};
use crate::store::Quad;
use crate::idmap::{IdMap, bytes_key};
use alloc::vec::Vec;

const RDF: &[u8] = b"http://www.w3.org/1999/02/22-rdf-syntax-ns#";
const XSD: &[u8] = b"http://www.w3.org/2001/XMLSchema#";

struct Parser<'a, 'd> {
    c: Cur<'a>,
    dict: &'d mut Dict,
    out: &'d mut Vec<Quad>,
    /// (prefix, namespace), in order of definition; a later one wins.
    namespaces: Vec<(Vec<u8>, Vec<u8>)>,
    base: Vec<u8>,
    bnodes: IdMap,
    /// Where triples go: the load's graph, or the graph block being read.
    graph: u32,
    buf: Vec<u8>,
    lang: Vec<u8>,
    iri_buf: Vec<u8>,
}

/// Parse a Turtle (or, with `trig`, TriG) document; triples outside graph blocks go to `graph`.
pub fn parse(text: &[u8], trig: bool, base: &[u8], graph: u32, dict: &mut Dict, out: &mut Vec<Quad>) -> Res<()> {
    let mut p = Parser {
        c: Cur::new(text),
        dict,
        out,
        namespaces: Vec::new(),
        base: base.to_vec(),
        bnodes: IdMap::new(),
        graph,
        buf: Vec::new(),
        lang: Vec::new(),
        iri_buf: Vec::new(),
    };
    loop {
        p.ws();
        if p.c.eof() {
            return Ok(());
        }
        if p.directive()? {
            continue;
        }
        if trig { p.block(graph)? } else { p.statement()? }
    }
}

fn concat(a: &[u8], b: &[u8]) -> Vec<u8> {
    let mut v = Vec::with_capacity(a.len() + b.len());
    v.extend_from_slice(a);
    v.extend_from_slice(b);
    v
}

impl Parser<'_, '_> {
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

    fn expect(&mut self, b: u8, msg: &'static str) -> Res<()> {
        self.ws();
        self.c.expect(b, msg)
    }

    /// A keyword (case-insensitive) not followed by a name character.
    fn keyword(&mut self, word: &[u8]) -> bool {
        let s = &self.c.s[self.c.i..];
        if s.len() < word.len() || !s[..word.len()].eq_ignore_ascii_case(word) {
            return false;
        }
        let next = s.get(word.len()).copied().unwrap_or(b' ');
        if next.is_ascii_alphanumeric() || next == b':' || next == b'_' || next == b'-' || next >= 0x80 {
            return false;
        }
        self.c.i += word.len();
        true
    }

    fn emit(&mut self, s: u32, p: u32, o: u32) {
        self.out.push([s, p, o, self.graph]);
    }

    fn rdf(&mut self, local: &[u8]) -> u32 {
        self.dict.intern(Key::iri(&concat(RDF, local)))
    }

    // --- directives ------------------------------------------------------------

    fn directive(&mut self) -> Res<bool> {
        let (sparql, what) = if self.c.eat_str(b"@prefix") {
            (false, 0)
        } else if self.c.eat_str(b"@base") {
            (false, 1)
        } else if self.c.eat_str(b"@version") {
            (false, 2)
        } else if self.keyword(b"PREFIX") {
            (true, 0)
        } else if self.keyword(b"BASE") {
            (true, 1)
        } else if self.keyword(b"VERSION") {
            (true, 2)
        } else {
            return Ok(false);
        };
        self.ws();
        match what {
            0 => {
                let prefix = self.pname_ns()?;
                self.ws();
                let ns = self.iri_string()?;
                self.namespaces.push((prefix.to_vec(), ns));
            }
            1 => self.base = self.iri_string()?,
            _ => match self.c.peek() {
                q @ (b'"' | b'\'') if !self.c.starts(b"\"\"\"") && !self.c.starts(b"'''") => self.c.string(q, &mut self.buf)?,
                _ => return self.c.err("expected a version string"),
            },
        }
        if !sparql {
            self.expect(b'.', "expected . after directive")?;
        }
        Ok(true)
    }

    fn pname_ns(&mut self) -> Res<Vec<u8>> {
        let start = self.c.i;
        self.prefix_chars()?;
        let prefix = self.c.s[start..self.c.i].to_vec();
        self.c.expect(b':', "expected prefix name")?;
        Ok(prefix)
    }

    /// PN_PREFIX, possibly empty; the cursor stops before the ':'.
    fn prefix_chars(&mut self) -> Res<()> {
        let (c, n) = self.c.ch();
        if self.c.eof() || !pn_chars_base(c) {
            return Ok(());
        }
        self.c.i += n;
        loop {
            let (c, n) = self.c.ch();
            if self.c.eof() || !(pn_chars(c) || c == '.' as u32) {
                break;
            }
            self.c.i += n;
        }
        if self.c.s[self.c.i - 1] == b'.' {
            return self.c.err("prefix ends with .");
        }
        Ok(())
    }

    // --- statements ------------------------------------------------------------

    fn statement(&mut self) -> Res<()> {
        self.triples()?;
        self.expect(b'.', "expected .")
    }

    /// TriG: a graph block, or triples in the load's graph.
    fn block(&mut self, graph: u32) -> Res<()> {
        if self.keyword(b"GRAPH") {
            self.ws();
            let g = self.label()?;
            return self.wrapped_graph(g, graph);
        }
        if self.c.peek() == b'{' {
            return self.wrapped_graph(graph, graph);
        }
        // A label or subject followed by '{' names a graph.
        if matches!(self.c.peek(), b'<' | b'_' | b'[') && !self.c.starts(b"<<") || self.at_pname() {
            let save = (self.c.i, self.c.line, self.out.len());
            let anon_or_label = self.c.peek() != b'[' || self.anon_ahead();
            if anon_or_label {
                let g = self.label()?;
                self.ws();
                if self.c.peek() == b'{' {
                    return self.wrapped_graph(g, graph);
                }
                (self.c.i, self.c.line) = (save.0, save.1);
                self.out.truncate(save.2);
            }
        }
        self.statement()
    }

    fn wrapped_graph(&mut self, g: u32, outer: u32) -> Res<()> {
        self.expect(b'{', "expected {")?;
        self.graph = g;
        loop {
            self.ws();
            if self.c.peek() == b'}' {
                break;
            }
            self.triples()?;
            self.ws();
            if !self.c.eat(b'.') {
                break;
            }
        }
        self.expect(b'}', "expected }")?;
        self.graph = outer;
        Ok(())
    }

    /// A graph label: IRI or blank node.
    fn label(&mut self) -> Res<u32> {
        match self.c.peek() {
            b'_' | b'[' => self.bnode(),
            _ => self.iri(),
        }
    }

    fn triples(&mut self) -> Res<()> {
        let (subject, needs_predicates) = match self.c.peek() {
            b'[' if !self.anon_ahead() => (self.property_list()?, false),
            b'<' if self.c.starts(b"<<") && !self.c.starts(b"<<(") => (self.reified_triple()?, false),
            _ => (self.subject()?, true),
        };
        self.ws();
        if needs_predicates || !matches!(self.c.peek(), b'.' | b'}') {
            self.predicate_object_list(subject)?;
        }
        Ok(())
    }

    fn subject(&mut self) -> Res<u32> {
        match self.c.peek() {
            b'<' if self.c.starts(b"<<") => self.c.err("a triple term cannot be a subject"),
            b'<' => self.iri(),
            b'_' | b'[' => self.bnode(),
            b'(' => self.collection(),
            _ if self.at_pname() => self.iri(),
            _ => self.c.err("expected subject"),
        }
    }

    fn predicate_object_list(&mut self, subject: u32) -> Res<()> {
        loop {
            self.ws();
            let p = self.verb()?;
            self.object_list(subject, p)?;
            self.ws();
            if !self.c.eat(b';') {
                return Ok(());
            }
            loop {
                self.ws();
                if !self.c.eat(b';') {
                    break;
                }
            }
            if matches!(self.c.peek(), b'.' | b']' | b'}' | b'|') || self.c.eof() || self.c.starts(b">>") {
                return Ok(());
            }
        }
    }

    fn verb(&mut self) -> Res<u32> {
        if self.c.peek() == b'a' && {
            let next = self.c.at(1);
            !(next.is_ascii_alphanumeric() || matches!(next, b':' | b'_' | b'-' | b'.') || next >= 0x80)
        } {
            self.c.i += 1;
            return Ok(self.rdf(b"type"));
        }
        if self.c.peek() == b'<' && self.c.starts(b"<<") || !(self.c.peek() == b'<' || self.at_pname()) {
            return self.c.err("expected predicate");
        }
        self.iri()
    }

    fn object_list(&mut self, s: u32, p: u32) -> Res<()> {
        loop {
            self.ws();
            let o = self.object()?;
            self.emit(s, p, o);
            self.annotation(s, p, o)?;
            self.ws();
            if !self.c.eat(b',') {
                return Ok(());
            }
        }
    }

    fn object(&mut self) -> Res<u32> {
        match self.c.peek() {
            b'<' if self.c.starts(b"<<(") => self.triple_term(),
            b'<' if self.c.starts(b"<<") => self.reified_triple(),
            b'<' => self.iri(),
            b'_' => self.bnode(),
            b'[' if self.anon_ahead() => self.bnode(),
            b'[' => self.property_list(),
            b'(' => self.collection(),
            b'"' | b'\'' => self.literal(),
            b'0'..=b'9' | b'+' | b'-' | b'.' => self.number(),
            _ if self.keyword(b"true") => self.boolean(b"true"),
            _ if self.keyword(b"false") => self.boolean(b"false"),
            _ if self.at_pname() => self.iri(),
            _ => self.c.err("expected object"),
        }
    }

    // --- RDF 1.2: triple terms, reification, annotations -------------------------

    fn triple_term(&mut self) -> Res<u32> {
        self.c.i += 3;
        self.ws();
        let s = match self.c.peek() {
            b'<' if self.c.starts(b"<<") => return self.c.err("a triple term cannot be a subject"),
            b'<' => self.iri()?,
            b'_' | b'[' => self.anon_or_label()?,
            _ if self.at_pname() => self.iri()?,
            _ => return self.c.err("expected triple term subject"),
        };
        self.ws();
        let p = self.verb()?;
        self.ws();
        let o = match self.c.peek() {
            b'<' if self.c.starts(b"<<(") => self.triple_term()?,
            b'<' if self.c.starts(b"<<") => return self.c.err("a reified triple cannot be in a triple term"),
            b'_' | b'[' => self.anon_or_label()?,
            b'(' => return self.c.err("a collection cannot be in a triple term"),
            _ => self.object()?,
        };
        self.ws();
        if !self.c.eat_str(b")>>") {
            return self.c.err("expected )>>");
        }
        Ok(self.dict.intern(Key::triple(s, p, o)))
    }

    /// A blank node written as a label or `[]` (no property lists).
    fn anon_or_label(&mut self) -> Res<u32> {
        if self.c.peek() == b'[' && !self.anon_ahead() {
            return self.c.err("a blank node property list is not allowed here");
        }
        self.bnode()
    }

    fn reified_triple(&mut self) -> Res<u32> {
        self.c.i += 2;
        self.ws();
        let s = match self.c.peek() {
            b'<' if self.c.starts(b"<<(") => return self.c.err("a triple term cannot be a subject"),
            b'<' if self.c.starts(b"<<") => self.reified_triple()?,
            b'_' | b'[' => self.anon_or_label()?,
            b'(' => return self.c.err("a collection cannot be reified"),
            _ => self.iri()?,
        };
        self.ws();
        let p = self.verb()?;
        self.ws();
        let o = match self.c.peek() {
            b'_' | b'[' => self.anon_or_label()?,
            b'(' => return self.c.err("a collection cannot be reified"),
            _ => self.object()?,
        };
        self.ws();
        let r = if self.c.peek() == b'~' { self.reifier()? } else { self.dict.fresh_bnode() };
        self.ws();
        if !self.c.eat_str(b">>") {
            return self.c.err("expected >>");
        }
        let t = self.dict.intern(Key::triple(s, p, o));
        let reifies = self.rdf(b"reifies");
        self.emit(r, reifies, t);
        Ok(r)
    }

    /// `~` and an optional IRI or blank node; the cursor on `~`.
    fn reifier(&mut self) -> Res<u32> {
        self.c.i += 1;
        self.ws();
        match self.c.peek() {
            b'<' if !self.c.starts(b"<<") => self.iri(),
            b'_' => self.bnode(),
            b'[' if self.anon_ahead() => self.bnode(),
            _ if self.at_pname() => self.iri(),
            _ => Ok(self.dict.fresh_bnode()),
        }
    }

    fn annotation(&mut self, s: u32, p: u32, o: u32) -> Res<()> {
        let mut reifier = None;
        loop {
            self.ws();
            if self.c.peek() == b'~' {
                let r = self.reifier()?;
                let (t, reifies) = (self.dict.intern(Key::triple(s, p, o)), self.rdf(b"reifies"));
                self.emit(r, reifies, t);
                reifier = Some(r);
            } else if self.c.starts(b"{|") {
                self.c.i += 2;
                let r = match reifier.take() {
                    Some(r) => r,
                    None => {
                        let r = self.dict.fresh_bnode();
                        let (t, reifies) = (self.dict.intern(Key::triple(s, p, o)), self.rdf(b"reifies"));
                        self.emit(r, reifies, t);
                        r
                    }
                };
                self.predicate_object_list(r)?;
                self.ws();
                if !self.c.eat_str(b"|}") {
                    return self.c.err("expected |}");
                }
            } else {
                return Ok(());
            }
        }
    }

    // --- abbreviations -----------------------------------------------------------

    /// Whether the cursor is on `[` followed only by white space and `]`.
    fn anon_ahead(&self) -> bool {
        let mut i = self.c.i + 1;
        while let Some(&b) = self.c.s.get(i) {
            match b {
                b' ' | b'\t' | b'\r' | b'\n' => i += 1,
                b'#' => {
                    while self.c.s.get(i).is_some_and(|&b| b != b'\n') {
                        i += 1;
                    }
                }
                b']' => return true,
                _ => return false,
            }
        }
        false
    }

    fn property_list(&mut self) -> Res<u32> {
        self.c.i += 1;
        let b = self.dict.fresh_bnode();
        self.predicate_object_list(b)?;
        self.expect(b']', "expected ]")?;
        Ok(b)
    }

    fn collection(&mut self) -> Res<u32> {
        self.c.i += 1;
        let (first, rest, nil) = (self.rdf(b"first"), self.rdf(b"rest"), self.rdf(b"nil"));
        let mut head = nil;
        let mut last = NONE;
        loop {
            self.ws();
            if self.c.eat(b')') {
                break;
            }
            if self.c.eof() {
                return self.c.err("unterminated collection");
            }
            let item = self.object()?;
            let cell = self.dict.fresh_bnode();
            if last == NONE {
                head = cell;
            } else {
                self.emit(last, rest, cell);
            }
            self.emit(cell, first, item);
            last = cell;
        }
        if last != NONE {
            self.emit(last, rest, nil);
        }
        Ok(head)
    }

    // --- terms -------------------------------------------------------------------

    fn bnode(&mut self) -> Res<u32> {
        if self.c.eat(b'[') {
            self.ws();
            self.c.expect(b']', "expected ]")?;
            return Ok(self.dict.fresh_bnode());
        }
        if !self.c.eat_str(b"_:") {
            return self.c.err("expected blank node");
        }
        self.c.bnode_label(&mut self.buf)?;
        let key = bytes_key(&self.buf);
        if let Some(id) = self.bnodes.get(&key) {
            return Ok(id);
        }
        let id = self.dict.fresh_bnode();
        self.bnodes.insert(&key, id);
        Ok(id)
    }

    fn at_pname(&self) -> bool {
        let (c, _) = self.c.ch();
        !self.c.eof() && (c == ':' as u32 || pn_chars_base(c))
    }

    /// An IRI (written or prefixed), resolved, as a string.
    fn iri_string(&mut self) -> Res<Vec<u8>> {
        let mut out = Vec::new();
        self.iri_into(&mut out)?;
        Ok(out)
    }

    /// An IRI (`<...>` resolved, or a prefixed name) into `out`, replacing it.
    fn iri_into(&mut self, out: &mut Vec<u8>) -> Res<()> {
        out.clear();
        if self.c.peek() == b'<' {
            self.c.iriref(&mut self.buf)?;
            if has_scheme(&self.buf) {
                out.extend_from_slice(&self.buf);
            } else {
                iri::resolve(&self.base, &self.buf, out);
            }
            return Ok(());
        }
        self.prefixed_name(out)
    }

    fn iri(&mut self) -> Res<u32> {
        let mut s = core::mem::take(&mut self.iri_buf);
        let r = self.iri_into(&mut s).map(|()| self.dict.intern(Key::iri(&s)));
        self.iri_buf = s;
        r
    }

    fn prefixed_name(&mut self, out: &mut Vec<u8>) -> Res<()> {
        let start = self.c.i;
        self.prefix_chars()?;
        let prefix = &self.c.s[start..self.c.i];
        if !self.c.eat(b':') {
            return self.c.err("expected prefixed name");
        }
        let Some(ns) = self.namespaces.iter().rposition(|(p, _)| p == prefix) else {
            return self.c.err("undefined prefix");
        };
        out.extend_from_slice(&self.namespaces[ns].1);
        // PN_LOCAL; a trailing unescaped '.' ends the statement instead.
        let (mut keep_len, mut keep_i) = (out.len(), self.c.i);
        let mut first = true;
        loop {
            let (c, n) = self.c.ch();
            if self.c.eof() {
                break;
            }
            if c == '\\' as u32 {
                let e = self.c.at(1);
                if !b"_~.-!$&'()*+,;=/?#@%".contains(&e) {
                    return self.c.err("bad escape in local name");
                }
                out.push(e);
                self.c.i += 2;
            } else if c == '%' as u32 {
                if !(self.c.at(1).is_ascii_hexdigit() && self.c.at(2).is_ascii_hexdigit()) {
                    return self.c.err("bad % escape in local name");
                }
                out.extend_from_slice(&self.c.s[self.c.i..self.c.i + 3]);
                self.c.i += 3;
            } else if c == '.' as u32 && !first {
                out.push(b'.');
                self.c.i += 1;
                continue;
            } else if if first { pn_chars_u(c) || c == ':' as u32 || (0x30..=0x39).contains(&c) } else { pn_chars(c) || c == ':' as u32 } {
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
        Ok(())
    }

    fn literal(&mut self) -> Res<u32> {
        let mut value = Vec::new();
        let q = self.c.peek();
        if self.c.starts(&[q, q, q]) {
            self.long_string(q, &mut value)?;
        } else {
            self.c.string(q, &mut value)?;
        }
        let after = (self.c.i, self.c.line);
        self.ws();
        if self.c.eat(b'@') {
            let dir = self.c.langdir(&mut self.lang)?;
            return Ok(self.dict.intern(Key::lang_literal(&value, &self.lang, dir)));
        }
        let mut dt = NONE;
        if self.c.eat_str(b"^^") {
            self.ws();
            if !(self.c.peek() == b'<' && !self.c.starts(b"<<") || self.at_pname()) {
                return self.c.err("expected datatype IRI");
            }
            dt = self.iri()?;
            if dt == RDF_LANG_STRING || dt == RDF_DIR_LANG_STRING {
                return self.c.err("a language-tagged string needs a language tag");
            }
        } else {
            (self.c.i, self.c.line) = after;
        }
        Ok(self.dict.intern(Key::literal(&value, dt)))
    }

    fn long_string(&mut self, q: u8, out: &mut Vec<u8>) -> Res<()> {
        self.c.i += 3;
        loop {
            if self.c.eof() {
                return self.c.err("unterminated string");
            }
            match self.c.peek() {
                c if c == q && self.c.starts(&[q, q, q]) => {
                    self.c.i += 3;
                    return Ok(());
                }
                b'\\' => self.c.escape(out)?,
                c => {
                    if c == b'\n' {
                        self.c.line += 1;
                    }
                    out.push(c);
                    self.c.i += 1;
                }
            }
        }
    }

    fn number(&mut self) -> Res<u32> {
        let start = self.c.i;
        let digits = |c: &mut Cur| {
            let s = c.i;
            while c.peek().is_ascii_digit() {
                c.i += 1;
            }
            c.i - s
        };
        if matches!(self.c.peek(), b'+' | b'-') {
            self.c.i += 1;
        }
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
        } else if self.c.s[self.c.i - 1] == b'.' {
            return self.c.err("bad number");
        }
        let dt = self.dict.intern(Key::iri(&concat(XSD, kind)));
        let text = self.c.s[start..self.c.i].to_vec();
        Ok(self.dict.intern(Key::literal(&text, dt)))
    }

    fn boolean(&mut self, value: &[u8]) -> Res<u32> {
        let dt = self.dict.intern(Key::iri(&concat(XSD, b"boolean")));
        Ok(self.dict.intern(Key::literal(value, dt)))
    }
}
