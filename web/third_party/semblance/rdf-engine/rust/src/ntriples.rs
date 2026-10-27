//! N-Triples and N-Quads 1.2: one statement per line, absolute IRIs, triple
//! terms in object position, directional language tags, `VERSION`.

use crate::dict::{Dict, Key, NONE, RDF_DIR_LANG_STRING, RDF_LANG_STRING};
use crate::lex::{Cur, Res, has_scheme};
use crate::store::Quad;
use crate::idmap::{IdMap, bytes_key};
use alloc::vec::Vec;

struct Parser<'a, 'd> {
    c: Cur<'a>,
    dict: &'d mut Dict,
    bnodes: IdMap,
    buf: Vec<u8>,
    lang: Vec<u8>,
}

/// Parse a document into quads; triples go to `graph`.
pub fn parse(text: &[u8], quads: bool, graph: u32, dict: &mut Dict, out: &mut Vec<Quad>) -> Res<()> {
    let mut p = Parser { c: Cur::new(text), dict, bnodes: IdMap::new(), buf: Vec::new(), lang: Vec::new() };
    loop {
        p.ws();
        if p.c.eof() {
            return Ok(());
        }
        if !p.eol() {
            p.statement(quads, graph, out)?;
            p.ws();
            if !p.c.eof() && !p.eol() {
                return p.c.err("expected end of line");
            }
        }
    }
}

impl Parser<'_, '_> {
    /// Spaces, tabs and a comment up to (not including) the end of line.
    fn ws(&mut self) {
        loop {
            match self.c.peek() {
                b' ' | b'\t' => self.c.i += 1,
                b'#' => {
                    while !self.c.eof() && !matches!(self.c.peek(), b'\n' | b'\r') {
                        self.c.i += 1;
                    }
                }
                _ => return,
            }
        }
    }

    fn eol(&mut self) -> bool {
        let mut any = false;
        while matches!(self.c.peek(), b'\n' | b'\r') && !self.c.eof() {
            if self.c.peek() == b'\n' {
                self.c.line += 1;
            }
            self.c.i += 1;
            any = true;
        }
        any
    }

    fn statement(&mut self, quads: bool, graph: u32, out: &mut Vec<Quad>) -> Res<()> {
        if self.c.eat_str(b"VERSION") {
            self.ws();
            if self.c.peek() != b'"' {
                return self.c.err("expected a version string");
            }
            return self.c.string(b'"', &mut self.buf);
        }
        let s = self.subject()?;
        self.ws();
        let p = self.iri()?;
        self.ws();
        let o = self.object()?;
        self.ws();
        let mut g = graph;
        if quads && self.c.peek() != b'.' {
            g = match self.c.peek() {
                b'<' => self.iri()?,
                b'_' => self.bnode()?,
                _ => return self.c.err("expected graph label or ."),
            };
            self.ws();
        }
        self.c.expect(b'.', "expected .")?;
        out.push([s, p, o, g]);
        Ok(())
    }

    fn iri(&mut self) -> Res<u32> {
        self.c.iriref(&mut self.buf)?;
        if !has_scheme(&self.buf) {
            return self.c.err("relative IRI");
        }
        Ok(self.dict.intern(Key::iri(&self.buf)))
    }

    fn bnode(&mut self) -> Res<u32> {
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

    fn subject(&mut self) -> Res<u32> {
        match self.c.peek() {
            b'<' => self.iri(),
            b'_' => self.bnode(),
            _ => self.c.err("expected subject"),
        }
    }

    fn object(&mut self) -> Res<u32> {
        match self.c.peek() {
            b'<' if self.c.starts(b"<<(") => self.triple_term(),
            b'<' => self.iri(),
            b'_' => self.bnode(),
            b'"' => self.literal(),
            _ => self.c.err("expected object"),
        }
    }

    fn triple_term(&mut self) -> Res<u32> {
        self.c.i += 3;
        self.ws();
        let s = self.subject()?;
        self.ws();
        let p = self.iri()?;
        self.ws();
        let o = self.object()?;
        self.ws();
        if !self.c.eat_str(b")>>") {
            return self.c.err("expected )>>");
        }
        Ok(self.dict.intern(Key::triple(s, p, o)))
    }

    fn literal(&mut self) -> Res<u32> {
        let mut value = Vec::new();
        self.c.string(b'"', &mut value)?;
        // '^^' and the language tag are terminals of their own: white space may precede them.
        let after = self.c.i;
        self.ws();
        if !matches!(self.c.peek(), b'@' | b'^') {
            self.c.i = after;
        }
        if self.c.eat(b'@') {
            let dir = self.c.langdir(&mut self.lang)?;
            return Ok(self.dict.intern(Key::lang_literal(&value, &self.lang, dir)));
        }
        let mut dt = NONE;
        if self.c.eat_str(b"^^") {
            self.ws();
            dt = self.iri()?;
            if dt == RDF_LANG_STRING || dt == RDF_DIR_LANG_STRING {
                return self.c.err("a language-tagged string needs a language tag");
            }
        }
        Ok(self.dict.intern(Key::literal(&value, dt)))
    }
}
