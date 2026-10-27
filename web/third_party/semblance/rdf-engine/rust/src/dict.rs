//! The term dictionary: every term is interned once and named by a `u32` id.
//! Terms live in one vector, their strings in one byte arena, and an
//! open-addressing table (linear probing over ids) finds them by content.

use crate::hash;
use alloc::vec::Vec;

pub const NONE: u32 = 0;
pub const DEFAULT_GRAPH_ID: u32 = 1;
pub const XSD_STRING: u32 = 2;
pub const RDF_LANG_STRING: u32 = 3;
pub const RDF_DIR_LANG_STRING: u32 = 4;

pub const FREE: u8 = 0;
pub const IRI: u8 = 1;
pub const BNODE: u8 = 2;
pub const LITERAL: u8 = 3;
pub const TRIPLE: u8 = 4;
pub const DEFAULT: u8 = 5;

/// A term's identity: kind, direction, up to three ids (a literal's datatype;
/// a triple's subject, predicate, object), its value and its language tag.
#[derive(Clone, Copy, PartialEq, Eq)]
pub struct Key<'a> {
    pub kind: u8,
    pub dir: u8,
    pub a: u32,
    pub b: u32,
    pub c: u32,
    pub value: &'a [u8],
    pub lang: &'a [u8],
}

impl<'a> Key<'a> {
    pub fn iri(value: &'a [u8]) -> Self {
        Key { kind: IRI, dir: 0, a: 0, b: 0, c: 0, value, lang: b"" }
    }
    pub fn bnode(value: &'a [u8]) -> Self {
        Key { kind: BNODE, ..Key::iri(value) }
    }
    /// A literal; `datatype` 0 is xsd:string.
    pub fn literal(value: &'a [u8], datatype: u32) -> Self {
        let a = if datatype == NONE { XSD_STRING } else { datatype };
        Key { kind: LITERAL, a, ..Key::iri(value) }
    }
    /// A language-tagged literal; `lang` must already be lowercase.
    pub fn lang_literal(value: &'a [u8], lang: &'a [u8], dir: u8) -> Self {
        let a = if dir == 0 { RDF_LANG_STRING } else { RDF_DIR_LANG_STRING };
        Key { kind: LITERAL, dir, a, b: 0, c: 0, value, lang }
    }
    pub fn triple(s: u32, p: u32, o: u32) -> Self {
        Key { kind: TRIPLE, dir: 0, a: s, b: p, c: o, value: b"", lang: b"" }
    }

    fn hash(&self) -> u32 {
        let h = hash::mix(hash::mix(0, (self.kind as u64) << 8 | self.dir as u64), (self.a as u64) << 32 | self.b as u64);
        let h = hash::mix(h, self.c as u64);
        hash::top32(hash::bytes(hash::bytes(h, self.value), self.lang))
    }
}

#[derive(Clone, Copy)]
pub struct Term {
    pub kind: u8,
    pub dir: u8,
    pub a: u32,
    pub b: u32,
    pub c: u32,
    voff: u32,
    vlen: u32,
    loff: u32,
    llen: u32,
    hash: u32,
}

pub struct Dict {
    terms: Vec<Term>,
    arena: Vec<u8>,
    /// Ids by hash; 0 is an empty slot. The length is a power of two.
    /// Per slot: an id in the low half and its hash in the high half (0 = empty),
    /// so a probe compares hashes without touching `terms`.
    table: Vec<u64>,
    live: u32,
    fresh: u32,
    /// Freed ids, reused before new ones.
    free: Vec<u32>,
}

/// Lowercase ASCII into `buf` if `s` has any uppercase letter.
pub fn lowercase<'a>(s: &'a [u8], buf: &'a mut Vec<u8>) -> &'a [u8] {
    if !s.iter().any(u8::is_ascii_uppercase) {
        return s;
    }
    buf.clear();
    buf.extend(s.iter().map(u8::to_ascii_lowercase));
    buf
}

impl Dict {
    pub fn new() -> Dict {
        let none = Term { kind: FREE, dir: 0, a: 0, b: 0, c: 0, voff: 0, vlen: 0, loff: 0, llen: 0, hash: 0 };
        let mut d = Dict { terms: Vec::new(), arena: Vec::new(), table: alloc::vec![0; 1024], live: 0, fresh: 0, free: Vec::new() };
        d.terms.push(none);
        d.terms.push(Term { kind: DEFAULT, ..none });
        d.intern(Key::iri(b"http://www.w3.org/2001/XMLSchema#string"));
        d.intern(Key::iri(b"http://www.w3.org/1999/02/22-rdf-syntax-ns#langString"));
        d.intern(Key::iri(b"http://www.w3.org/1999/02/22-rdf-syntax-ns#dirLangString"));
        d
    }

    fn matches(&self, t: &Term, k: &Key, h: u32) -> bool {
        t.hash == h
            && t.kind == k.kind
            && t.dir == k.dir
            && t.a == k.a
            && t.b == k.b
            && t.c == k.c
            && self.slice(t.voff, t.vlen) == k.value
            && self.slice(t.loff, t.llen) == k.lang
    }

    fn slice(&self, off: u32, len: u32) -> &[u8] {
        &self.arena[off as usize..(off + len) as usize]
    }

    /// The slot holding `k`, or the empty slot where it would go.
    fn slot(&self, k: &Key, h: u32) -> usize {
        let mask = self.table.len() - 1;
        let mut i = h as usize & mask;
        loop {
            let e = self.table[i];
            if e == 0 || (e >> 32) as u32 == h && self.matches(&self.terms[e as u32 as usize], k, h) {
                return i;
            }
            i = (i + 1) & mask;
        }
    }

    pub fn get(&self, k: Key) -> u32 {
        self.table[self.slot(&k, k.hash())] as u32
    }

    pub fn intern(&mut self, k: Key) -> u32 {
        let h = k.hash();
        let i = self.slot(&k, h);
        if self.table[i] != 0 {
            return self.table[i] as u32;
        }
        let voff = self.arena.len() as u32;
        self.arena.extend_from_slice(k.value);
        let loff = self.arena.len() as u32;
        self.arena.extend_from_slice(k.lang);
        let term = Term {
            kind: k.kind,
            dir: k.dir,
            a: k.a,
            b: k.b,
            c: k.c,
            voff,
            vlen: k.value.len() as u32,
            loff,
            llen: k.lang.len() as u32,
            hash: h,
        };
        let id = match self.free.pop() {
            Some(id) => {
                self.terms[id as usize] = term;
                id
            }
            None => {
                self.terms.push(term);
                self.terms.len() as u32 - 1
            }
        };
        self.table[i] = (h as u64) << 32 | id as u64;
        self.live += 1;
        if self.live as usize * 2 > self.table.len() {
            self.grow();
        }
        id
    }

    fn grow(&mut self) {
        let mut table = alloc::vec![0u64; self.table.len() * 2];
        let mask = table.len() - 1;
        for &e in &self.table {
            if e == 0 {
                continue;
            }
            let mut i = (e >> 32) as usize & mask;
            while table[i] != 0 {
                i = (i + 1) & mask;
            }
            table[i] = e;
        }
        self.table = table;
    }

    /// A blank node no label in this dictionary names yet.
    pub fn fresh_bnode(&mut self) -> u32 {
        let mut label = [0u8; 10];
        loop {
            self.fresh += 1;
            let mut n = self.fresh;
            let mut i = label.len();
            while n > 0 {
                i -= 1;
                label[i] = b"0123456789abcdefghijklmnopqrstuv"[(n & 31) as usize];
                n >>= 5;
            }
            i -= 1;
            label[i] = b'f';
            let k = Key::bnode(&label[i..]);
            if self.get(k) == NONE {
                return self.intern(k);
            }
        }
    }

    pub fn term(&self, id: u32) -> Option<&Term> {
        self.terms.get(id as usize).filter(|t| t.kind != FREE)
    }

    pub fn value(&self, id: u32) -> &[u8] {
        self.term(id).map_or(&[][..], |t| self.slice(t.voff, t.vlen))
    }

    pub fn lang(&self, id: u32) -> &[u8] {
        self.term(id).map_or(&[][..], |t| self.slice(t.loff, t.llen))
    }

    pub fn len(&self) -> u32 {
        self.live + 1
    }

    /// Ids ever handed out (live or free), for mark arrays.
    pub fn capacity(&self) -> usize {
        self.terms.len()
    }

    /// Mark a term live, with what it is made of (a literal's datatype, a triple's parts).
    pub fn mark(&self, id: u32, alive: &mut [bool]) {
        let i = id as usize;
        if i >= alive.len() || alive[i] {
            return;
        }
        alive[i] = true;
        let Some(t) = self.term(id) else { return };
        match t.kind {
            LITERAL => self.mark(t.a, alive),
            TRIPLE => {
                let (a, b, c) = (t.a, t.b, t.c);
                self.mark(a, alive);
                self.mark(b, alive);
                self.mark(c, alive);
            }
            _ => {}
        }
    }

    /// Free every term not marked alive; compacts the strings and rebuilds the
    /// table. Returns the freed ids.
    pub fn sweep(&mut self, alive: &[bool]) -> Vec<u32> {
        let mut freed = Vec::new();
        let mut arena = Vec::with_capacity(self.arena.len());
        for id in 0..self.terms.len() {
            let t = self.terms[id];
            if t.kind == FREE || t.kind == DEFAULT {
                continue;
            }
            if !alive.get(id).copied().unwrap_or(true) && id > RDF_DIR_LANG_STRING as usize {
                self.terms[id].kind = FREE;
                self.free.push(id as u32);
                self.live -= 1;
                freed.push(id as u32);
                continue;
            }
            let voff = arena.len() as u32;
            arena.extend_from_slice(&self.arena[t.voff as usize..(t.voff + t.vlen) as usize]);
            let loff = arena.len() as u32;
            arena.extend_from_slice(&self.arena[t.loff as usize..(t.loff + t.llen) as usize]);
            self.terms[id].voff = voff;
            self.terms[id].loff = loff;
        }
        self.arena = arena;
        // Lower ids are reused first.
        self.free.sort_unstable_by(|a, b| b.cmp(a));
        let mut size = 1024;
        while self.live as usize * 2 > size {
            size *= 2;
        }
        self.table = alloc::vec![0u64; size];
        let mask = size - 1;
        for (id, t) in self.terms.iter().enumerate() {
            if t.kind == FREE || t.kind == DEFAULT {
                continue;
            }
            let mut i = t.hash as usize & mask;
            while self.table[i] != 0 {
                i = (i + 1) & mask;
            }
            self.table[i] = (t.hash as u64) << 32 | id as u64;
        }
        freed
    }
}
