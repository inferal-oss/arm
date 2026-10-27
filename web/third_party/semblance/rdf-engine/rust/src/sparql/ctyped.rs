//! Typed expressions in compiled queries (as the WAT engine's ctyped.wat):
//! strings and numbers in locals.
//!
//! An expression the interpreter would evaluate to a term (interning every
//! string and number it computes) is compiled to code that keeps its values in
//! locals, with a kind known when compiling:
//!   Id   a term id (0: unbound or an error)
//!   Str  a simple literal's bytes: address p, length n (n < 0: an error), and
//!        its id when it is an existing term (else 0). Term bytes are copied
//!        into the engine's buffer (`abi::TX_BUF`; the dictionary's arena moves
//!        when a term is interned), constants live in the module's tables
//!   Num  a number: type t (0 an error, 1 an integer in 64 bits, 4 a double),
//!        i64 i, f64 d (an integer's value too), and its id when it is a term
//!   Bool 0, 1, or 2 for an error
//! A term is made only at the end, when the value is bound or output. What the
//! code does not handle when it runs (a language-tagged string, a decimal, a
//! full buffer, an integer overflow) leaves for the bail block, whose callback
//! (`cq_value` / `cq_test`) evaluates the whole expression in the engine;
//! expressions are pure, so that gives the same value. Subexpressions this
//! compiler does not type are callbacks themselves.

use super::*;

#[derive(Clone, Copy, PartialEq, Eq)]
pub(super) enum K {
    Id,
    Str,
    Num,
    Bool,
}

#[derive(Clone, Copy)]
pub(super) enum Tx {
    Id(u32),
    Str { p: u32, n: u32, id: u32 },
    Num { t: u32, i: u32, d: u32, id: u32 },
    Bool(u32),
}

impl Tx {
    fn kind(&self) -> K {
        match self {
            Tx::Id(_) => K::Id,
            Tx::Str { .. } => K::Str,
            Tx::Num { .. } => K::Num,
            Tx::Bool(_) => K::Bool,
        }
    }
}

// Opcodes beyond wasm.rs's.
const SUB: u8 = 0x6b;
const LT_S32: u8 = 0x48;
const LE_U32: u8 = 0x4d;
const GE_S32: u8 = 0x4e;
const GT_U32: u8 = 0x4b;
const I64_NE: u8 = 0x52;
const I64_LT_S: u8 = 0x53;
const I64_GT_S: u8 = 0x55;
const I64_ADD: u8 = 0x7c;
const I64_SUB: u8 = 0x7d;
const I64_AND: u8 = 0x83;
const I64_XOR: u8 = 0x85;
const I64_EXTEND_U: u8 = 0xad;
const F64_EQ: u8 = 0x61;
const F64_NE: u8 = 0x62;
const F64_LT: u8 = 0x63;
const F64_GT: u8 = 0x64;
const F64_ADD: u8 = 0xa0;
const F64_SUB: u8 = 0xa1;
const F64_CONVERT_I64: u8 = 0xb9;
const I32_LOAD: u8 = 0x28;
const I64_LOAD: u8 = 0x29;
const F64_LOAD: u8 = 0x2b;
const I64_STORE: u8 = 0x37;
const F64_STORE: u8 = 0x39;

const XSD: &[u8] = b"http://www.w3.org/2001/XMLSchema#";

fn boolean_fn(f: Func) -> bool {
    use Func::*;
    matches!(f, LangMatches | Contains | StrStarts | StrEnds | SameTerm | IsIri | IsBlank | IsLiteral | IsNumeric | Regex | IsTriple | HasLang | HasLangDir)
}

/// Whether e gives the same value each time (no RAND, NOW, UUID, BNODE).
fn pure_expr(e: &Expr) -> bool {
    match e {
        Expr::Call(Func::Rand | Func::Now | Func::Uuid | Func::StrUuid | Func::Bnode, _) => false,
        Expr::Call(_, args) | Expr::Coalesce(args) => args.iter().all(pure_expr),
        Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => pure_expr(a) && pure_expr(b),
        Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => pure_expr(a),
        Expr::In(a, list, _) => pure_expr(a) && list.iter().all(pure_expr),
        Expr::If(a, b, c) => pure_expr(a) && pure_expr(b) && pure_expr(c),
        _ => true,
    }
}

impl<'q> Gen<'q> {
    // --- code helpers --------------------------------------------------------------

    /// A block or if (`t`: its result type, or 0x40) that counts towards the bail block's depth.
    fn topen(&mut self, f: usize, op: u8, t: u8) {
        self.f(f).open_t(op, t);
        self.txd += 1;
    }
    fn tend(&mut self, f: usize) {
        self.f(f).op(END);
        self.txd -= 1;
    }
    /// Leave for the bail block when the condition on the stack holds.
    fn bail_if(&mut self, f: usize) {
        let d = self.txd;
        self.f(f).br(BR_IF, d);
    }

    fn tx_new(&mut self, f: usize, k: K) -> Tx {
        let b = self.f(f);
        match k {
            K::Id => Tx::Id(b.local()),
            K::Str => Tx::Str { p: b.local(), n: b.local(), id: b.local() },
            K::Num => Tx::Num { t: b.local(), i: b.local_of(I64), d: b.local_of(F64), id: b.local() },
            K::Bool => Tx::Bool(b.local()),
        }
    }

    /// Push whether v is an error (or unbound).
    fn tx_err(&mut self, f: usize, v: Tx) {
        let b = self.f(f);
        match v {
            Tx::Id(l) => b.get(l).op(EQZ),
            Tx::Str { n, .. } => b.get(n).i32(0).op(LT_S32),
            Tx::Num { t, .. } => b.get(t).op(EQZ),
            Tx::Bool(l) => b.get(l).i32(2).op(EQ),
        };
    }

    fn tx_set_err(&mut self, f: usize, v: Tx) {
        let b = self.f(f);
        match v {
            Tx::Id(l) => b.i32(0).set(l),
            Tx::Str { n, id, .. } => b.i32(-1).set(n).i32(0).set(id),
            Tx::Num { t, id, .. } => b.i32(0).set(t).i32(0).set(id),
            Tx::Bool(l) => b.i32(2).set(l),
        };
    }

    /// dst = src, of one kind.
    fn tx_copy(&mut self, f: usize, dst: Tx, src: Tx) {
        let b = self.f(f);
        match (dst, src) {
            (Tx::Id(x), Tx::Id(y)) | (Tx::Bool(x), Tx::Bool(y)) => {
                b.get(y).set(x);
            }
            (Tx::Str { p, n, id }, Tx::Str { p: p2, n: n2, id: id2 }) => {
                b.get(p2).set(p).get(n2).set(n).get(id2).set(id);
            }
            (Tx::Num { t, i, d, id }, Tx::Num { t: t2, i: i2, d: d2, id: id2 }) => {
                b.get(t2).set(t).get(i2).set(i).get(d2).set(d).get(id2).set(id);
            }
            _ => unreachable!(),
        }
    }

    /// The module global holding the string buffer's use (bytes from its start).
    fn used(&mut self) -> u32 {
        match self.tx_used {
            Some(g) => g,
            None => {
                let g = self.global();
                self.tx_used = Some(g);
                g
            }
        }
    }

    // --- conversions ---------------------------------------------------------------

    /// Push v as a term id (0: an error), making the term when it is a new value.
    fn to_id(&mut self, f: usize, v: Tx) {
        let num = crate::abi::tx_num();
        let b = self.f(f);
        match v {
            Tx::Id(l) => {
                b.get(l);
            }
            Tx::Str { p, n, id } => {
                b.get(id).open_t(IF, I32).get(id).op(ELSE);
                b.get(n).i32(0).op(LT_S32).open_t(IF, I32).i32(0).op(ELSE).get(p).get(n).call(STRTERM).op(END);
                b.op(END);
            }
            Tx::Num { t, i, d, id } => {
                b.get(id).open_t(IF, I32).get(id).op(ELSE);
                b.get(t).op(EQZ).open_t(IF, I32).i32(0).op(ELSE);
                b.i32(0).get(i).mem(I64_STORE, 3, num).i32(0).get(d).mem(F64_STORE, 3, num + 8).get(t).call(NUMTERM);
                b.op(END).op(END);
            }
            Tx::Bool(l) => {
                b.get(l).i32(2).op(EQ).open_t(IF, I32).i32(0).op(ELSE);
                b.i32(0).get(l).op(I64_EXTEND_U).mem(I64_STORE, 3, num).i32(5).call(NUMTERM).op(END);
            }
        }
    }

    fn as_id(&mut self, f: usize, v: Tx) -> u32 {
        if let Tx::Id(l) = v {
            return l;
        }
        let l = self.f(f).local();
        self.to_id(f, v);
        self.f(f).set(l);
        l
    }

    /// v as kind k: itself, or (k Id) its term.
    fn as_kind(&mut self, f: usize, v: Tx, k: K) -> Tx {
        if v.kind() == k {
            return v;
        }
        debug_assert!(k == K::Id);
        Tx::Id(self.as_id(f, v))
    }

    /// Push v's effective boolean value: 0, 1, or 2 for an error.
    fn to_bool(&mut self, f: usize, v: Tx) {
        let b = self.f(f);
        match v {
            Tx::Bool(l) => {
                b.get(l);
            }
            Tx::Id(l) => {
                b.get(l).op(EQZ).open_t(IF, I32).i32(2).op(ELSE).get(l).call(EBV).op(END);
            }
            Tx::Str { n, .. } => {
                b.get(n).i32(0).op(LT_S32).open_t(IF, I32).i32(2).op(ELSE).get(n).i32(0).op(NE).op(END);
            }
            Tx::Num { t, i, d, .. } => {
                b.get(t).op(EQZ).open_t(IF, I32).i32(2).op(ELSE);
                b.get(t).i32(1).op(EQ).open_t(IF, I32).get(i).i64(0).op(I64_NE).op(ELSE);
                b.get(d).f64(0.0).op(F64_NE).get(d).get(d).op(F64_EQ).op(AND).op(END);
                b.op(END);
            }
        }
    }

    /// The lexical form of the term in local l into the buffer: a Str (an
    /// error when l is 0 or the term does not qualify). Mode 0: STR's; 1: a
    /// string argument's (an xsd:string; a language-tagged one leaves).
    fn lex(&mut self, f: usize, l: u32, mode: i32) -> Tx {
        let buf = crate::abi::tx_buf() as i32;
        let g = self.used();
        let r = self.tx_new(f, K::Str);
        let Tx::Str { p, n, id } = r else { unreachable!() };
        self.f(f).global_get(g).i32(buf).op(ADD).set(p);
        self.f(f).get(l).op(EQZ).open_t(IF, I32).i32(-1).op(ELSE).get(l).get(p).i32(mode).call(LEX).op(END).tee(n);
        self.f(f).i32(-2).op(EQ);
        self.bail_if(f);
        self.f(f).get(n).i32(0).op(GE_S32).open(IF).global_get(g).get(n).op(ADD).global_set(g).op(END);
        // the term itself when it is a simple literal
        self.f(f).get(l).i32(0).get(l).call(DATATYPE).i32(dict::XSD_STRING as i32).op(EQ).op(SELECT).set(id);
        r
    }

    /// v as a string argument: a simple literal's bytes (else an error).
    fn to_str(&mut self, f: usize, v: Tx) -> Tx {
        match v {
            Tx::Str { .. } => v,
            Tx::Id(l) => self.lex(f, l, 1),
            _ => {
                let r = self.tx_new(f, K::Str);
                self.tx_set_err(f, r);
                r
            }
        }
    }

    /// v as a number (an error when it is not one); a term that is a number
    /// compiled code does not keep (a decimal, a float) leaves. `strict`: a
    /// bound term that is not a number leaves too (dates and durations have
    /// arithmetic of their own in the engine).
    fn to_num(&mut self, f: usize, v: Tx, strict: bool) -> Tx {
        let num = crate::abi::tx_num();
        let l = match v {
            Tx::Num { .. } => return v,
            Tx::Id(l) => l,
            _ if strict => self.as_id(f, v),
            _ => {
                let r = self.tx_new(f, K::Num);
                self.tx_set_err(f, r);
                return r;
            }
        };
        let r = self.tx_new(f, K::Num);
        let Tx::Num { t, i, d, id } = r else { unreachable!() };
        self.f(f).get(l).op(EQZ).open_t(IF, I32).i32(0).op(ELSE).get(l).call(NUM).op(END).tee(t).i32(6).op(EQ);
        self.bail_if(f);
        if strict {
            self.f(f).get(t).op(EQZ).get(l).i32(0).op(NE).op(AND);
            self.bail_if(f);
        }
        self.f(f).i32(0).mem(I64_LOAD, 3, num).set(i).i32(0).mem(F64_LOAD, 3, num + 8).set(d).get(l).set(id);
        r
    }

    // --- kinds ---------------------------------------------------------------------

    /// xsd:integer (1) or xsd:double (4) as a cast's target, else 0.
    fn tx_cast(&self, dt: u32) -> u32 {
        match self.dict.value(dt).strip_prefix(XSD) {
            Some(b"integer") => 1,
            Some(b"double") => 4,
            _ => 0,
        }
    }

    /// A constant number this code keeps: its type and value.
    fn const_num(&self, c: u32) -> Option<(u32, i64, f64)> {
        let t = self.dict.term(c)?;
        if t.kind != dict::LITERAL {
            return None;
        }
        let s = self.dict.value(c);
        match self.tx_cast(t.a) {
            1 => {
                let i = i64::try_from(num::parse_int(s)?).ok()?;
                Some((1, i, i as f64))
            }
            4 => Some((4, 0, num::parse_double(s)?)),
            _ => None,
        }
    }

    fn const_str(&self, c: u32) -> bool {
        self.dict.term(c).is_some_and(|t| t.kind == dict::LITERAL && t.a == dict::XSD_STRING)
    }

    /// The kind of value `tx` gives for e.
    fn tk(&self, e: &Expr) -> K {
        match e {
            Expr::Const(c) if self.const_str(*c) => K::Str,
            Expr::Const(c) if self.const_num(*c).is_some() => K::Num,
            Expr::Or(..) | Expr::And(..) | Expr::Not(_) | Expr::Cmp(..) | Expr::In(..) | Expr::Bound(_) | Expr::Exists(..) => K::Bool,
            Expr::Call(f, _) if boolean_fn(*f) => K::Bool,
            Expr::Call(Func::Str | Func::Concat, args) if !args.is_empty() => K::Str,
            Expr::Call(Func::SubStr, args) if args.len() >= 2 => K::Str,
            Expr::Call(Func::StrLen, _) => K::Num,
            Expr::Arith(Op::Add | Op::Sub, ..) => K::Num,
            Expr::Cast(dt, _) if self.tx_cast(*dt) != 0 => K::Num,
            Expr::If(_, a, b) => {
                let (x, y) = (self.tk(a), self.tk(b));
                if x == y { x } else { K::Id }
            }
            Expr::Coalesce(list) if !list.is_empty() => {
                let k = self.tk(&list[0]);
                if list.iter().all(|x| self.tk(x) == k) { k } else { K::Id }
            }
            _ => K::Id,
        }
    }

    /// Whether e is worth compiling typed: it computes strings or numbers (or
    /// compares terms) somewhere, and gives the same value each time.
    pub(super) fn tx_worth(&self, e: &Expr) -> bool {
        !off(32) && self.here.is_some() && pure_expr(e) && self.worth(e)
    }

    fn worth(&self, e: &Expr) -> bool {
        match e {
            Expr::Call(Func::Concat | Func::Iri | Func::SubStr | Func::StrLen | Func::Contains | Func::StrStarts | Func::StrEnds, _) => true,
            Expr::Cmp(Cmp::Lt | Cmp::Gt | Cmp::Le | Cmp::Ge, ..) => true,
            Expr::Cmp(_, a, b) if !self.iri_compare(a, b) && self.lang_empty(a, b).is_none() => true,
            Expr::Cast(dt, _) if self.tx_cast(*dt) != 0 => true,
            Expr::Arith(Op::Add | Op::Sub, ..) => true,
            Expr::Exists(..) => false,
            Expr::Call(_, args) | Expr::Coalesce(args) => args.iter().any(|x| self.worth(x)),
            Expr::Or(a, b) | Expr::And(a, b) | Expr::Cmp(_, a, b) | Expr::Arith(_, a, b) => self.worth(a) || self.worth(b),
            Expr::Not(a) | Expr::Neg(a) | Expr::Plus(a) | Expr::Cast(_, a) => self.worth(a),
            Expr::In(a, list, _) => self.worth(a) || list.iter().any(|x| self.worth(x)),
            Expr::If(a, b, c) => self.worth(a) || self.worth(b) || self.worth(c),
            _ => false,
        }
    }

    // --- expressions ---------------------------------------------------------------

    /// A callback for e: a Bool (cq_test) or an Id (cq_value).
    fn tx_cb(&mut self, f: usize, e: &Expr, k: K) -> Tx {
        let x = self.expr_index(e);
        let l = self.f(f).local();
        self.f(f).i32(x as i32).call(if k == K::Bool { TEST } else { VALUE }).set(l);
        if k == K::Bool { Tx::Bool(l) } else { Tx::Id(l) }
    }

    /// e as the id-level code of `bool3` (nothing typed inside): a Bool.
    fn tx_cond(&mut self, f: usize, e: &Expr) -> Tx {
        let l = self.f(f).local();
        self.bool3(f, e);
        self.f(f).set(l);
        Tx::Bool(l)
    }

    /// Emit e's value into locals; its kind is `tk(e)`.
    fn tx(&mut self, f: usize, e: &Expr) -> Tx {
        let k = self.tk(e);
        let v = self.tx_any(f, e, k);
        self.as_kind(f, v, k)
    }

    fn tx_any(&mut self, f: usize, e: &Expr, k: K) -> Tx {
        match e {
            Expr::Var(v) => {
                let l = self.f(f).local();
                self.load_var(f, *v);
                self.f(f).set(l);
                Tx::Id(l)
            }
            Expr::Const(c) => {
                let r = self.tx_new(f, k);
                match r {
                    Tx::Str { p, n, id } => {
                        let bytes: alloc::boxed::Box<[u8]> = self.dict.value(*c).into();
                        let (at, len) = (bytes.as_ptr() as i32, bytes.len() as i32);
                        self.t.consts.push(bytes);
                        self.f(f).i32(at).set(p).i32(len).set(n).i32(*c as i32).set(id);
                    }
                    Tx::Num { t, i, d, id } => {
                        let (ty, iv, dv) = self.const_num(*c).unwrap();
                        self.f(f).i32(ty as i32).set(t).i64(iv).set(i).f64(dv).set(d).i32(*c as i32).set(id);
                    }
                    Tx::Id(l) => {
                        self.f(f).i32(*c as i32).set(l);
                    }
                    Tx::Bool(_) => unreachable!(),
                }
                r
            }
            // What bool3 does at the level of ids, with nothing typed inside.
            Expr::Bound(_) | Expr::Exists(..) => self.tx_cond(f, e),
            Expr::Cmp(Cmp::Eq | Cmp::Ne, a, b) if self.iri_compare(a, b) || self.lang_empty(a, b).is_some() => self.tx_cond(f, e),
            Expr::Call(Func::SameTerm | Func::IsIri | Func::IsBlank | Func::IsLiteral, args) if args.iter().all(simple) => self.tx_cond(f, e),
            Expr::Not(a) => {
                let r = self.f(f).local();
                let x = self.tx(f, a);
                self.to_bool(f, x);
                // An error stays one: select(t ^ 1, t, t != 2).
                self.f(f).tee(r).i32(1).op(XOR).get(r).get(r).i32(2).op(NE).op(SELECT).set(r);
                Tx::Bool(r)
            }
            Expr::And(a, b) | Expr::Or(a, b) => {
                // AND: false decides; OR: true decides; else an error if either is one.
                let decisive = if matches!(e, Expr::And(..)) { 0 } else { 1 };
                let (t, u, r) = (self.f(f).local(), self.f(f).local(), self.f(f).local());
                let x = self.tx(f, a);
                self.to_bool(f, x);
                self.f(f).tee(t).i32(decisive).op(EQ);
                self.topen(f, IF, I32);
                self.f(f).i32(decisive).op(ELSE);
                let y = self.tx(f, b);
                self.to_bool(f, y);
                self.f(f).tee(u).i32(decisive).op(EQ);
                self.topen(f, IF, I32);
                self.f(f).i32(decisive).op(ELSE);
                self.f(f).i32(2).i32(1 - decisive).get(t).get(u).op(OR).i32(2).op(AND).op(SELECT);
                self.tend(f);
                self.tend(f);
                self.f(f).set(r);
                Tx::Bool(r)
            }
            Expr::Cmp(op, a, b) => {
                let (x, y) = (self.tx(f, a), self.tx(f, b));
                self.tx_compare(f, *op, x, y)
            }
            Expr::Arith(op @ (Op::Add | Op::Sub), a, b) => {
                let (x, y) = (self.tx(f, a), self.tx(f, b));
                let (x, y) = (self.to_num(f, x, true), self.to_num(f, y, true));
                self.tx_arith(f, *op, x, y)
            }
            Expr::Cast(dt, a) if self.tx_cast(*dt) != 0 => {
                let tc = self.tx_cast(*dt);
                let x = self.tx(f, a);
                self.tx_tonum(f, x, tc)
            }
            Expr::Call(Func::Str, args) => {
                let x = self.tx(f, &args[0]);
                match x {
                    Tx::Str { .. } => x,
                    _ => {
                        let l = self.as_id(f, x);
                        self.lex(f, l, 0)
                    }
                }
            }
            Expr::Call(Func::Concat, args) if !args.is_empty() => self.tx_concat(f, args),
            Expr::Call(Func::Iri, args) => {
                let x = self.tx(f, &args[0]);
                self.tx_iri(f, x)
            }
            Expr::Call(func @ (Func::StrStarts | Func::StrEnds | Func::Contains), args) => {
                let (x, y) = (self.tx(f, &args[0]), self.tx(f, &args[1]));
                let (x, y) = (self.to_str(f, x), self.to_str(f, y));
                let (Tx::Str { p: pa, n: na, .. }, Tx::Str { p: pb, n: nb, .. }) = (x, y) else { unreachable!() };
                let mode = match func {
                    Func::StrStarts => 0,
                    Func::StrEnds => 1,
                    _ => 2,
                };
                let r = self.f(f).local();
                self.f(f).get(na).get(nb).op(OR).i32(0).op(LT_S32).open_t(IF, I32).i32(2).op(ELSE);
                self.f(f).i32(mode).get(pa).get(na).get(pb).get(nb).call(STRFN).op(END).set(r);
                Tx::Bool(r)
            }
            Expr::Call(Func::StrLen, args) => {
                let x = self.tx(f, &args[0]);
                let Tx::Str { p, n, .. } = self.to_str(f, x) else { unreachable!() };
                let r = self.tx_new(f, K::Num);
                let Tx::Num { t, i, d, id } = r else { unreachable!() };
                self.f(f).i32(0).set(id).get(n).i32(0).op(LT_S32).open(IF).i32(0).set(t).op(ELSE);
                self.f(f).i32(1).set(t).get(p).get(n).call(CPLEN).op(I64_EXTEND_U).tee(i).op(F64_CONVERT_I64).set(d).op(END);
                r
            }
            Expr::Call(Func::SubStr, args) if args.len() >= 2 => self.tx_substr(f, args),
            Expr::If(c, a, b) => {
                let x = self.tx(f, c);
                let cond = self.f(f).local();
                self.to_bool(f, x);
                self.f(f).set(cond);
                let r = self.tx_new(f, k);
                self.f(f).get(cond).i32(1).op(EQ);
                self.topen(f, IF, 0x40);
                let va = self.tx(f, a);
                let va = self.as_kind(f, va, k);
                self.tx_copy(f, r, va);
                self.f(f).op(ELSE).get(cond).op(EQZ);
                self.topen(f, IF, 0x40);
                let vb = self.tx(f, b);
                let vb = self.as_kind(f, vb, k);
                self.tx_copy(f, r, vb);
                self.f(f).op(ELSE);
                self.tx_set_err(f, r);
                self.tend(f);
                self.tend(f);
                r
            }
            Expr::Coalesce(list) if !list.is_empty() => {
                let r = self.tx_new(f, k);
                self.topen(f, BLOCK, 0x40);
                let at = self.txd;
                for x in list {
                    let v = self.tx(f, x);
                    let v = self.as_kind(f, v, k);
                    self.tx_err(f, v);
                    self.f(f).op(EQZ);
                    self.topen(f, IF, 0x40);
                    self.tx_copy(f, r, v);
                    let d = self.txd - at;
                    self.f(f).br(BR, d);
                    self.tend(f);
                }
                self.tx_set_err(f, r);
                self.tend(f);
                r
            }
            _ => self.tx_cb(f, e, k),
        }
    }

    /// CONCAT of simple strings, into the buffer.
    fn tx_concat(&mut self, f: usize, args: &[Expr]) -> Tx {
        let mut parts = Vec::new();
        for a in args {
            let x = self.tx(f, a);
            parts.push(self.to_str(f, x));
        }
        if parts.len() == 1 {
            return parts[0];
        }
        let buf = crate::abi::tx_buf() as i32;
        let g = self.used();
        let r = self.tx_new(f, K::Str);
        let Tx::Str { p, n, id } = r else { unreachable!() };
        let q = self.f(f).local();
        self.f(f).i32(0).set(id).i32(0);
        for x in &parts {
            let Tx::Str { n: m, .. } = *x else { unreachable!() };
            self.f(f).get(m).op(OR);
        }
        self.f(f).i32(0).op(LT_S32);
        self.topen(f, IF, 0x40);
        self.f(f).i32(-1).set(n).op(ELSE);
        // room for all of it, else the bail block
        self.f(f).global_get(g);
        for x in &parts {
            let Tx::Str { n: m, .. } = *x else { unreachable!() };
            self.f(f).get(m).op(ADD);
        }
        self.f(f).i32(crate::abi::TX_BUF_LEN as i32).op(GT_U32);
        self.bail_if(f);
        self.f(f).global_get(g).i32(buf).op(ADD).tee(p).set(q);
        for x in &parts {
            let Tx::Str { p: s, n: m, .. } = *x else { unreachable!() };
            // memory.copy q, s, m
            self.f(f).get(q).get(s).get(m).code.extend_from_slice(&[0xfc, 0x0a, 0x00, 0x00]);
            self.f(f).get(q).get(m).op(ADD).set(q);
        }
        self.f(f).get(q).get(p).op(SUB).set(n).global_get(g).get(n).op(ADD).global_set(g);
        self.tend(f);
        r
    }

    /// IRI(v): an IRI is itself; a simple literal is resolved (cq_iri); else an error.
    fn tx_iri(&mut self, f: usize, v: Tx) -> Tx {
        let r = self.f(f).local();
        match v {
            Tx::Id(l) => {
                self.f(f).get(l).call(KIND).i32(dict::IRI as i32).op(EQ);
                self.topen(f, IF, 0x40);
                self.f(f).get(l).set(r).op(ELSE);
                let Tx::Str { p, n, .. } = self.lex(f, l, 1) else { unreachable!() };
                self.f(f).get(n).i32(0).op(LT_S32).open_t(IF, I32).i32(0).op(ELSE).get(p).get(n).call(IRI_OF).op(END).set(r);
                self.tend(f);
            }
            Tx::Str { p, n, .. } => {
                self.f(f).get(n).i32(0).op(LT_S32).open_t(IF, I32).i32(0).op(ELSE).get(p).get(n).call(IRI_OF).op(END).set(r);
            }
            _ => {
                self.f(f).i32(0).set(r);
            }
        }
        Tx::Id(r)
    }

    /// SUBSTR(s, start[, length]) as the engine computes it (characters, rounded
    /// positions): a slice of s's bytes, nothing copied.
    fn tx_substr(&mut self, f: usize, args: &[Expr]) -> Tx {
        let num = crate::abi::tx_num();
        let x = self.tx(f, &args[0]);
        let s = self.to_str(f, x);
        let y = self.tx(f, &args[1]);
        let start = self.to_num(f, y, false);
        let len = match args.get(2) {
            Some(a) => {
                let z = self.tx(f, a);
                Some(self.to_num(f, z, false))
            }
            None => None,
        };
        let (Tx::Str { p: sp, n: sn, .. }, Tx::Num { t: st, d: sd, .. }) = (s, start) else { unreachable!() };
        let r = self.tx_new(f, K::Str);
        let Tx::Str { p, n, id } = r else { unreachable!() };
        self.f(f).i32(0).set(id).get(sn).i32(0).op(LT_S32).get(st).op(EQZ).op(OR);
        if let Some(Tx::Num { t: lt, .. }) = len {
            self.f(f).get(lt).op(EQZ).op(OR);
        }
        self.f(f).open(IF).i32(-1).set(n).op(ELSE);
        self.f(f).i32(0).get(sd).mem(F64_STORE, 3, num + 8);
        if let Some(Tx::Num { d: ld, .. }) = len {
            self.f(f).i32(0).get(ld).mem(F64_STORE, 3, num + 24);
        }
        self.f(f).get(sp).get(sp).get(sn).i32(if len.is_some() { 3 } else { 2 }).call(SUBSTR).op(ADD).set(p);
        self.f(f).i32(0).mem(I32_LOAD, 2, num + 16).set(n).op(END);
        r
    }

    /// A cast of v to xsd:integer (tc 1) or xsd:double (4).
    fn tx_tonum(&mut self, f: usize, v: Tx, tc: u32) -> Tx {
        let num = crate::abi::tx_num();
        let r = self.tx_new(f, K::Num);
        let Tx::Num { t, i, d, id } = r else { unreachable!() };
        match v {
            Tx::Str { p, n, .. } => {
                self.f(f).get(n).i32(0).op(LT_S32).open_t(IF, I32).i32(0).op(ELSE).i32(1).get(p).get(n).i32(tc as i32).call(TONUM).op(END).set(t);
            }
            Tx::Num { t: t0, i: i0, d: d0, .. } => {
                self.f(f).get(t0).op(EQZ).open_t(IF, I32).i32(0).op(ELSE);
                self.f(f).i32(0).get(i0).mem(I64_STORE, 3, num).i32(0).get(d0).mem(F64_STORE, 3, num + 8);
                self.f(f).i32(2).get(t0).i32(0).i32(tc as i32).call(TONUM).op(END).set(t);
            }
            _ => {
                let l = self.as_id(f, v);
                self.f(f).get(l).op(EQZ).open_t(IF, I32).i32(0).op(ELSE).i32(0).get(l).i32(0).i32(tc as i32).call(TONUM).op(END).set(t);
            }
        }
        self.f(f).get(t).i32(6).op(EQ);
        self.bail_if(f);
        self.f(f).i32(0).mem(I64_LOAD, 3, num).set(i).i32(0).mem(F64_LOAD, 3, num + 8).set(d).i32(0).set(id);
        r
    }

    /// a + b or a - b: integers (an overflow leaves), or with a double a double.
    fn tx_arith(&mut self, f: usize, op: Op, a: Tx, b: Tx) -> Tx {
        let (Tx::Num { t: ta, i: ia, d: da, .. }, Tx::Num { t: tb, i: ib, d: db, .. }) = (a, b) else { unreachable!() };
        let r = self.tx_new(f, K::Num);
        let Tx::Num { t, i, d, id } = r else { unreachable!() };
        let add = op == Op::Add;
        self.f(f).i32(0).set(t).i32(0).set(id).get(ta).op(EQZ).get(tb).op(EQZ).op(OR).op(EQZ);
        self.topen(f, IF, 0x40);
        self.f(f).get(ta).i32(1).op(EQ).get(tb).i32(1).op(EQ).op(AND);
        self.topen(f, IF, 0x40);
        self.f(f).get(ia).get(ib).op(if add { I64_ADD } else { I64_SUB }).set(i);
        // overflow: add ((a ^ r) & (b ^ r)) < 0; sub ((a ^ b) & (a ^ r)) < 0
        if add {
            self.f(f).get(ia).get(i).op(I64_XOR).get(ib).get(i).op(I64_XOR);
        } else {
            self.f(f).get(ia).get(ib).op(I64_XOR).get(ia).get(i).op(I64_XOR);
        }
        self.f(f).op(I64_AND).i64(0).op(I64_LT_S);
        self.bail_if(f);
        self.f(f).get(i).op(F64_CONVERT_I64).set(d).i32(1).set(t).op(ELSE);
        self.f(f).get(da).get(db).op(if add { F64_ADD } else { F64_SUB }).set(d).i32(4).set(t);
        self.tend(f);
        self.tend(f);
        r
    }

    /// A comparison's operand: its class local (0 an error, 1 a number, 2 a
    /// simple string, 3 an IRI or blank node), its Str and Num parts, its id.
    fn tx_operand(&mut self, f: usize, v: Tx) -> (u32, Tx, Tx, u32) {
        let c = self.f(f).local();
        let v = if let Tx::Bool(_) = v { Tx::Id(self.as_id(f, v)) } else { v };
        match v {
            Tx::Str { n, id, .. } => {
                self.f(f).get(n).i32(0).op(LT_S32).open_t(IF, I32).i32(0).op(ELSE).i32(2).op(END).set(c);
                let z = self.tx_new(f, K::Num);
                (c, v, z, id)
            }
            Tx::Num { t, id, .. } => {
                self.f(f).get(t).op(EQZ).open_t(IF, I32).i32(0).op(ELSE).i32(1).op(END).set(c);
                let z = self.tx_new(f, K::Str);
                (c, z, v, id)
            }
            Tx::Id(l) => {
                let k = self.f(f).local();
                let s = self.tx_new(f, K::Str);
                let n = self.tx_new(f, K::Num);
                self.f(f).i32(0).set(c).get(l);
                self.topen(f, IF, 0x40);
                self.f(f).get(l).call(KIND).tee(k).i32(dict::LITERAL as i32).op(EQ);
                self.topen(f, IF, 0x40);
                self.f(f).get(l).call(DATATYPE).i32(dict::XSD_STRING as i32).op(EQ);
                self.topen(f, IF, 0x40);
                let x = self.lex(f, l, 1);
                self.tx_copy(f, s, x);
                self.f(f).i32(2).set(c).op(ELSE);
                let y = self.to_num(f, v, false);
                self.tx_copy(f, n, y);
                // another literal (a boolean, a date, a language-tagged string): the engine decides
                let Tx::Num { t, .. } = n else { unreachable!() };
                self.f(f).get(t).op(EQZ);
                self.bail_if(f);
                self.f(f).i32(1).set(c);
                self.tend(f);
                self.f(f).op(ELSE);
                // a triple term leaves; an IRI or blank node is itself
                self.f(f).get(k).i32(dict::TRIPLE as i32).op(EQ);
                self.bail_if(f);
                self.f(f).i32(3).set(c);
                self.tend(f);
                self.tend(f);
                (c, s, n, l)
            }
            Tx::Bool(_) => unreachable!(),
        }
    }

    /// a op b, as the engine's `equal` and `order` decide it.
    fn tx_compare(&mut self, f: usize, op: Cmp, a: Tx, b: Tx) -> Tx {
        let (ca, sa, na, ida) = self.tx_operand(f, a);
        let (cb, sb, nb, idb) = self.tx_operand(f, b);
        let (Tx::Str { p: pa, n: la, .. }, Tx::Str { p: pb, n: lb, .. }) = (sa, sb) else { unreachable!() };
        let (Tx::Num { t: ta, i: ia, d: da, .. }, Tx::Num { t: tb, i: ib, d: db, .. }) = (na, nb) else { unreachable!() };
        // x: -1, 0, 1 ordered; 3 unordered numbers (NaN); 4 incomparable;
        // 5 the same IRI or blank node; 2 an error
        let bd = self.f(f);
        let (x, r) = (bd.local(), bd.local());
        bd.i32(4).set(x);
        bd.get(ca).op(EQZ).get(cb).op(EQZ).op(OR).open(IF).i32(2).set(x).op(ELSE);
        // numbers: integers exactly, else as doubles
        bd.get(ca).i32(1).op(EQ).get(cb).i32(1).op(EQ).op(AND).open(IF);
        bd.get(ta).i32(1).op(EQ).get(tb).i32(1).op(EQ).op(AND).open(IF);
        bd.i32(-1).i32(1).i32(0).get(ia).get(ib).op(I64_GT_S).op(SELECT).get(ia).get(ib).op(I64_LT_S).op(SELECT).set(x);
        bd.op(ELSE);
        bd.i32(-1).i32(1).i32(0).i32(3).get(da).get(db).op(F64_EQ).op(SELECT).get(da).get(db).op(F64_GT).op(SELECT).get(da).get(db).op(F64_LT).op(SELECT).set(x);
        bd.op(END).op(ELSE);
        // simple strings, by their bytes
        bd.get(ca).i32(2).op(EQ).get(cb).i32(2).op(EQ).op(AND).open(IF);
        bd.i32(3).get(pa).get(la).get(pb).get(lb).call(STRFN).set(x);
        bd.op(ELSE);
        // IRIs and blank nodes: the same term or not
        bd.get(ca).i32(3).op(EQ).get(cb).i32(3).op(EQ).op(AND).open(IF);
        bd.i32(5).i32(4).get(ida).get(idb).op(EQ).op(SELECT).set(x);
        bd.op(END).op(END).op(END).op(END);
        match op {
            Cmp::Eq | Cmp::Ne => {
                let (yes, no) = if op == Cmp::Eq { (1, 0) } else { (0, 1) };
                bd.i32(yes).i32(no).get(x).op(EQZ).get(x).i32(5).op(EQ).op(OR).op(SELECT);
                bd.i32(2).get(x).i32(2).op(NE).op(SELECT);
            }
            _ => {
                // ordered: x + 1 <= 2 (unsigned)
                bd.get(x).i32(1).op(ADD).i32(2).op(LE_U32).open_t(IF, I32);
                match op {
                    Cmp::Lt => bd.get(x).i32(-1).op(EQ),
                    Cmp::Gt => bd.get(x).i32(1).op(EQ),
                    Cmp::Le => bd.get(x).i32(1).op(NE),
                    _ => bd.get(x).i32(-1).op(NE),
                };
                bd.op(ELSE).i32(2).op(END);
            }
        }
        bd.set(r);
        Tx::Bool(r)
    }

    // --- top level -----------------------------------------------------------------

    /// Push e's value (cond: its condition, 0, 1 or 2; else its term id), typed,
    /// with the engine's evaluation as the bail block.
    pub(super) fn tx_top(&mut self, f: usize, e: &Expr, cond: bool) {
        let (saved, was) = (self.txd, self.in_tx);
        self.txd = 0;
        self.in_tx = true;
        let g = self.used();
        let u = self.f(f).local();
        self.f(f).global_get(g).set(u).open_t(BLOCK, I32).open(BLOCK);
        let v = self.tx(f, e);
        if cond {
            self.to_bool(f, v);
        } else {
            self.to_id(f, v);
        }
        self.f(f).get(u).global_set(g).br(BR, 1).op(END);
        let k = self.expr_index(e);
        self.f(f).get(u).global_set(g).i32(k as i32).call(if cond { TEST } else { VALUE }).op(END);
        self.txd = saved;
        self.in_tx = was;
    }
}
