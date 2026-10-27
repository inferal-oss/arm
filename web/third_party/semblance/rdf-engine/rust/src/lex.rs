//! Terminals shared by the RDF syntaxes (and later SPARQL): a byte cursor with
//! line tracking, IRIs, strings with escapes, blank node labels, language tags.

use alloc::vec::Vec;

pub struct Error {
    pub msg: &'static str,
    pub line: u32,
}

pub type Res<T> = Result<T, Error>;

pub struct Cur<'a> {
    pub s: &'a [u8],
    pub i: usize,
    pub line: u32,
}

impl<'a> Cur<'a> {
    pub fn new(s: &'a [u8]) -> Self {
        Cur { s, i: 0, line: 1 }
    }

    pub fn eof(&self) -> bool {
        self.i >= self.s.len()
    }

    /// The byte at the cursor, or 0 at the end.
    pub fn peek(&self) -> u8 {
        self.at(0)
    }

    pub fn at(&self, k: usize) -> u8 {
        self.s.get(self.i + k).copied().unwrap_or(0)
    }

    pub fn err<T>(&self, msg: &'static str) -> Res<T> {
        Err(Error { msg, line: self.line })
    }

    pub fn eat(&mut self, b: u8) -> bool {
        if !self.eof() && self.s[self.i] == b {
            self.i += 1;
            true
        } else {
            false
        }
    }

    pub fn starts(&self, lit: &[u8]) -> bool {
        self.s[self.i.min(self.s.len())..].starts_with(lit)
    }

    pub fn eat_str(&mut self, lit: &[u8]) -> bool {
        let ok = self.starts(lit);
        if ok {
            self.i += lit.len();
        }
        ok
    }

    pub fn expect(&mut self, b: u8, msg: &'static str) -> Res<()> {
        if self.eat(b) { Ok(()) } else { self.err(msg) }
    }

    /// Decode the UTF-8 character at the cursor: (code point, byte length).
    /// Input comes from a JS string, so it is valid UTF-8.
    pub fn ch(&self) -> (u32, usize) {
        let b = self.peek() as u32;
        let cont = |k: usize| (self.at(k) & 0x3f) as u32;
        if b < 0x80 {
            (b, 1)
        } else if b < 0xe0 {
            ((b & 0x1f) << 6 | cont(1), 2)
        } else if b < 0xf0 {
            ((b & 0x0f) << 12 | cont(1) << 6 | cont(2), 3)
        } else {
            ((b & 0x07) << 18 | cont(1) << 12 | cont(2) << 6 | cont(3), 4)
        }
    }

    /// `\uXXXX` or `\UXXXXXXXX`, the backslash already consumed and `u`/`U` next.
    pub fn uchar(&mut self, out: &mut Vec<u8>) -> Res<u32> {
        let n = match self.peek() {
            b'u' => 4,
            b'U' => 8,
            _ => return self.err("bad escape"),
        };
        self.i += 1;
        let mut cp = 0u32;
        for _ in 0..n {
            let d = match self.peek() {
                c @ b'0'..=b'9' => c - b'0',
                c @ b'a'..=b'f' => c - b'a' + 10,
                c @ b'A'..=b'F' => c - b'A' + 10,
                _ => return self.err("bad hex digit in escape"),
            };
            cp = cp << 4 | d as u32;
            self.i += 1;
        }
        if (0xd800..0xe000).contains(&cp) || cp > 0x10ffff {
            return self.err("escape is not a Unicode scalar value");
        }
        push_utf8(out, cp);
        Ok(cp)
    }

    /// `<...>`, the cursor on `<`. Decodes escapes; the result may be relative.
    pub fn iriref(&mut self, out: &mut Vec<u8>) -> Res<()> {
        out.clear();
        self.expect(b'<', "expected <")?;
        loop {
            let c = self.peek();
            match c {
                b'>' => {
                    self.i += 1;
                    return Ok(());
                }
                b'\\' => {
                    self.i += 1;
                    let cp = self.uchar(out)?;
                    if cp <= 0x20 || b"<>\"{}|^`\\".contains(&(cp as u8)) && cp < 0x80 {
                        return self.err("illegal character in IRI");
                    }
                }
                0..=0x20 | b'<' | b'"' | b'{' | b'}' | b'|' | b'^' | b'`' => return self.err("illegal character in IRI"),
                _ => {
                    out.push(c);
                    self.i += 1;
                }
            }
            if self.eof() {
                return self.err("unterminated IRI");
            }
        }
    }

    /// `\` followed by an ECHAR or UCHAR, the cursor on the backslash.
    pub fn escape(&mut self, out: &mut Vec<u8>) -> Res<()> {
        self.i += 1;
        let b = match self.peek() {
            b't' => b'\t',
            b'b' => 8,
            b'n' => b'\n',
            b'r' => b'\r',
            b'f' => 12,
            b'"' => b'"',
            b'\'' => b'\'',
            b'\\' => b'\\',
            b'u' | b'U' => return self.uchar(out).map(|_| ()),
            _ => return self.err("bad escape"),
        };
        self.i += 1;
        out.push(b);
        Ok(())
    }

    /// A single-line string quoted with `q`, the cursor on the opening quote.
    pub fn string(&mut self, q: u8, out: &mut Vec<u8>) -> Res<()> {
        out.clear();
        self.i += 1;
        loop {
            match self.peek() {
                c if c == q => {
                    self.i += 1;
                    return Ok(());
                }
                b'\\' => self.escape(out)?,
                b'\n' | b'\r' => return self.err("line break in string"),
                c => {
                    if self.eof() {
                        return self.err("unterminated string");
                    }
                    out.push(c);
                    self.i += 1;
                }
            }
        }
    }

    /// A blank node label after `_:`.
    pub fn bnode_label(&mut self, out: &mut Vec<u8>) -> Res<()> {
        out.clear();
        let (c, n) = self.ch();
        if !(pn_chars_u(c) || (0x30..=0x39).contains(&c)) {
            return self.err("bad blank node label");
        }
        out.extend_from_slice(&self.s[self.i..self.i + n]);
        self.i += n;
        loop {
            let (c, n) = self.ch();
            if self.eof() || !(pn_chars(c) || c == '.' as u32) {
                break;
            }
            out.extend_from_slice(&self.s[self.i..self.i + n]);
            self.i += n;
        }
        // A label cannot end with '.': give trailing dots back.
        while out.last() == Some(&b'.') {
            out.pop();
            self.i -= 1;
        }
        Ok(())
    }

    /// A language tag with optional direction after `@`: lowercased into `out`;
    /// returns the direction (0 none, 1 ltr, 2 rtl).
    pub fn langdir(&mut self, out: &mut Vec<u8>) -> Res<u8> {
        out.clear();
        // BCP 47 shape: subtags of 1 to 8 characters, the first alphabetic.
        let subtag = |c: &mut Self, out: &mut Vec<u8>, ok: fn(&u8) -> bool| -> Res<()> {
            let start = out.len();
            while ok(&c.peek()) {
                out.push(c.peek().to_ascii_lowercase());
                c.i += 1;
            }
            if out.len() == start || out.len() - start > 8 { c.err("bad language tag") } else { Ok(()) }
        };
        subtag(self, out, u8::is_ascii_alphabetic)?;
        while self.peek() == b'-' && self.at(1).is_ascii_alphanumeric() {
            out.push(b'-');
            self.i += 1;
            subtag(self, out, u8::is_ascii_alphanumeric)?;
        }
        if self.starts(b"--") {
            self.i += 2;
            return if self.eat_str(b"ltr") {
                Ok(1)
            } else if self.eat_str(b"rtl") {
                Ok(2)
            } else {
                self.err("bad base direction")
            };
        }
        Ok(0)
    }
}

pub fn push_utf8(out: &mut Vec<u8>, cp: u32) {
    if cp < 0x80 {
        out.push(cp as u8);
    } else if cp < 0x800 {
        out.extend_from_slice(&[0xc0 | (cp >> 6) as u8, 0x80 | (cp & 0x3f) as u8]);
    } else if cp < 0x10000 {
        out.extend_from_slice(&[0xe0 | (cp >> 12) as u8, 0x80 | (cp >> 6 & 0x3f) as u8, 0x80 | (cp & 0x3f) as u8]);
    } else {
        out.extend_from_slice(&[0xf0 | (cp >> 18) as u8, 0x80 | (cp >> 12 & 0x3f) as u8, 0x80 | (cp >> 6 & 0x3f) as u8, 0x80 | (cp & 0x3f) as u8]);
    }
}

pub fn pn_chars_base(c: u32) -> bool {
    if c < 0x80 {
        return (c as u8).is_ascii_alphabetic();
    }
    matches!(c,
        0x41..=0x5a | 0x61..=0x7a | 0xc0..=0xd6 | 0xd8..=0xf6 | 0xf8..=0x2ff | 0x370..=0x37d
        | 0x37f..=0x1fff | 0x200c..=0x200d | 0x2070..=0x218f | 0x2c00..=0x2fef | 0x3001..=0xd7ff
        | 0xf900..=0xfdcf | 0xfdf0..=0xfffd | 0x10000..=0xeffff)
}

pub fn pn_chars_u(c: u32) -> bool {
    pn_chars_base(c) || c == '_' as u32
}

pub fn pn_chars(c: u32) -> bool {
    if c < 0x80 {
        return (c as u8).is_ascii_alphanumeric() || c == '_' as u32 || c == '-' as u32;
    }
    pn_chars_u(c) || matches!(c, 0x2d | 0x30..=0x39 | 0xb7 | 0x300..=0x36f | 0x203f..=0x2040)
}

/// Whether an IRI has a scheme (is absolute).
pub fn has_scheme(iri: &[u8]) -> bool {
    match iri.first() {
        Some(c) if c.is_ascii_alphabetic() => {}
        _ => return false,
    }
    for &c in &iri[1..] {
        match c {
            b':' => return true,
            c if c.is_ascii_alphanumeric() || c == b'+' || c == b'-' || c == b'.' => {}
            _ => return false,
        }
    }
    false
}
