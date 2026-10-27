//! Expression values and SPARQL's operators and functions.

use super::eval::Ctx;
use super::num::{self, Num};
use super::regex;
use super::*;
use crate::dict::{self, Key, NONE, RDF_DIR_LANG_STRING, RDF_LANG_STRING, XSD_STRING};
use crate::iri;
use core::cmp::Ordering;

pub type R<T> = Result<T, ()>;

/// A value computed by an expression; dictionary terms stay ids.
#[derive(Clone)]
pub enum Val {
    Id(u32),
    Bool(bool),
    Num(Num),
    /// A string, with its language tag and direction if any.
    Str(Vec<u8>, Option<(Vec<u8>, u8)>),
    Iri(Vec<u8>),
    /// A literal of another datatype.
    Typed(Vec<u8>, u32),
}

/// What a datatype is, for operators.
#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Dt {
    Integer,
    Decimal,
    Float,
    Double,
    Boolean,
    String,
    LangString,
    DateTime,
    Date,
    Other,
}

const XSD: &[u8] = b"http://www.w3.org/2001/XMLSchema#";

pub fn classify(local: &[u8]) -> Dt {
    match local {
        b"integer" | b"int" | b"long" | b"short" | b"byte" | b"nonNegativeInteger" | b"positiveInteger" | b"nonPositiveInteger"
        | b"negativeInteger" | b"unsignedInt" | b"unsignedLong" | b"unsignedShort" | b"unsignedByte" => Dt::Integer,
        b"decimal" => Dt::Decimal,
        b"float" => Dt::Float,
        b"double" => Dt::Double,
        b"boolean" => Dt::Boolean,
        b"string" => Dt::String,
        b"dateTime" | b"dateTimeStamp" => Dt::DateTime,
        b"date" => Dt::Date,
        _ => Dt::Other,
    }
}

/// A literal seen through its parts.
pub struct Lit {
    pub value: Vec<u8>,
    pub dt: u32,
    pub lang: Option<(Vec<u8>, u8)>,
}

impl Ctx<'_> {
    pub fn xsd(&mut self, local: &[u8]) -> u32 {
        // The datatypes computed values take, interned once per query.
        let slot = [&b"boolean"[..], b"integer", b"decimal", b"float", b"double", b"dateTime"].iter().position(|x| *x == local);
        if let Some(i) = slot {
            if self.xsd_ids[i] != NONE {
                return self.xsd_ids[i];
            }
        }
        let mut v = XSD.to_vec();
        v.extend_from_slice(local);
        let id = self.dict.intern(Key::iri(&v));
        if let Some(i) = slot {
            self.xsd_ids[i] = id;
        }
        id
    }

    pub fn dt_kind(&mut self, dt: u32) -> Dt {
        if dt == XSD_STRING {
            return Dt::String;
        }
        if dt == RDF_LANG_STRING || dt == RDF_DIR_LANG_STRING {
            return Dt::LangString;
        }
        if let Some(k) = self.dt_cache.get(&[dt]) {
            return [Dt::Integer, Dt::Decimal, Dt::Float, Dt::Double, Dt::Boolean, Dt::String, Dt::LangString, Dt::DateTime, Dt::Date, Dt::Other][k as usize];
        }
        let v = self.dict.value(dt);
        let k = match v.strip_prefix(XSD) {
            Some(local) => classify(local),
            None => Dt::Other,
        };
        self.dt_cache.insert(&[dt], k as u32);
        k
    }

    /// Intern a value as a term.
    pub fn to_id(&mut self, v: &Val) -> u32 {
        match v {
            Val::Id(i) => *i,
            Val::Bool(b) => {
                let dt = self.xsd(b"boolean");
                self.dict.intern(Key::literal(if *b { b"true" } else { b"false" }, dt))
            }
            Val::Num(n) => {
                let dt = self.xsd(match n {
                    Num::Int(_) => b"integer",
                    Num::Dec(_) => b"decimal",
                    Num::Float(_) => b"float",
                    Num::Double(_) => b"double",
                });
                let mut s = Vec::new();
                num::write_num(&mut s, *n);
                self.dict.intern(Key::literal(&s, dt))
            }
            Val::Str(s, None) => self.dict.intern(Key::literal(s, NONE)),
            Val::Str(s, Some((l, d))) => self.dict.intern(Key::lang_literal(s, l, *d)),
            Val::Iri(s) => self.dict.intern(Key::iri(s)),
            Val::Typed(s, dt) => self.dict.intern(Key::literal(s, *dt)),
        }
    }

    fn kind(&self, v: &Val) -> u8 {
        match v {
            Val::Id(i) => self.dict.term(*i).map_or(dict::FREE, |t| t.kind),
            Val::Iri(_) => dict::IRI,
            _ => dict::LITERAL,
        }
    }

    pub fn lit(&mut self, v: &Val) -> Option<Lit> {
        Some(match v {
            Val::Id(i) => {
                let t = *self.dict.term(*i)?;
                if t.kind != dict::LITERAL {
                    return None;
                }
                let lang = self.dict.lang(*i);
                Lit { value: self.dict.value(*i).to_vec(), dt: t.a, lang: if lang.is_empty() { None } else { Some((lang.to_vec(), t.dir)) } }
            }
            Val::Bool(_) | Val::Num(_) => {
                let id = self.to_id(v);
                return self.lit(&Val::Id(id));
            }
            Val::Str(s, l) => Lit { value: s.clone(), dt: if l.is_some() { RDF_LANG_STRING } else { XSD_STRING }, lang: l.clone() },
            Val::Typed(s, dt) => Lit { value: s.clone(), dt: *dt, lang: None },
            Val::Iri(_) => return None,
        })
    }

    pub fn num(&mut self, v: &Val) -> Option<Num> {
        match v {
            Val::Num(n) => Some(*n),
            Val::Id(i) => {
                let t = *self.dict.term(*i)?;
                if t.kind != dict::LITERAL {
                    return None;
                }
                let k = self.dt_kind(t.a);
                let s = self.dict.value(*i);
                match k {
                    Dt::Integer => num::parse_int(s).map(Num::Int),
                    Dt::Decimal => num::parse_dec(s).map(Num::Dec),
                    Dt::Float => num::parse_double(s).map(|d| Num::Float(d as f32)),
                    Dt::Double => num::parse_double(s).map(Num::Double),
                    _ => None,
                }
            }
            _ => None,
        }
    }

    fn boolean(&mut self, v: &Val) -> Option<bool> {
        match v {
            Val::Bool(b) => Some(*b),
            Val::Id(i) => {
                let t = *self.dict.term(*i)?;
                if t.kind != dict::LITERAL || self.dt_kind(t.a) != Dt::Boolean {
                    return None;
                }
                match self.dict.value(*i) {
                    b"true" | b"1" => Some(true),
                    b"false" | b"0" => Some(false),
                    _ => None,
                }
            }
            _ => None,
        }
    }

    /// Two simple literals (xsd:string), by their values.
    fn simple_pair(&self, x: u32, y: u32) -> Option<(&[u8], &[u8])> {
        let simple = |id: u32| self.dict.term(id).is_some_and(|t| t.kind == dict::LITERAL && t.a == XSD_STRING);
        if simple(x) && simple(y) { Some((self.dict.value(x), self.dict.value(y))) } else { None }
    }

    /// Check that a value is a string literal, for `str_view`.
    fn str_check(&mut self, v: &Val) -> R<()> {
        match v {
            Val::Str(..) => Ok(()),
            Val::Id(i) => {
                let t = *self.dict.term(*i).ok_or(())?;
                if t.kind != dict::LITERAL {
                    return Err(());
                }
                match self.dt_kind(t.a) {
                    Dt::String | Dt::LangString => Ok(()),
                    _ => Err(()),
                }
            }
            _ => Err(()),
        }
    }

    /// A string literal's value and language tag, borrowed (after `str_check`).
    fn str_view<'v>(&'v self, v: &'v Val) -> (&'v [u8], Option<(&'v [u8], u8)>) {
        match v {
            Val::Str(s, l) => (s, l.as_ref().map(|(t, d)| (&t[..], *d))),
            Val::Id(i) => {
                let lang = self.dict.lang(*i);
                let dir = self.dict.term(*i).map_or(0, |t| t.dir);
                (self.dict.value(*i), if lang.is_empty() { None } else { Some((lang, dir)) })
            }
            _ => (&[], None),
        }
    }

    /// A string literal (simple, xsd:string or language-tagged): value and tag.
    pub fn string(&mut self, v: &Val) -> R<(Vec<u8>, Option<(Vec<u8>, u8)>)> {
        match v {
            Val::Str(s, l) => Ok((s.clone(), l.clone())),
            _ => {
                let l = self.lit(v).ok_or(())?;
                let k = self.dt_kind(l.dt);
                if k == Dt::String || k == Dt::LangString { Ok((l.value, l.lang)) } else { Err(()) }
            }
        }
    }

    /// Effective boolean value.
    pub fn ebv(&mut self, v: &Val) -> R<bool> {
        match v {
            Val::Bool(b) => return Ok(*b),
            Val::Num(n) => return Ok(!n.is_zero() && !n.is_nan()),
            Val::Str(s, _) => return Ok(!s.is_empty()),
            _ => {}
        }
        let l = self.lit(v).ok_or(())?;
        match self.dt_kind(l.dt) {
            Dt::Boolean => self.boolean(v).ok_or(()),
            Dt::String => Ok(!l.value.is_empty()),
            Dt::Integer | Dt::Decimal | Dt::Float | Dt::Double => Ok(self.num(v).is_some_and(|n| !n.is_zero() && !n.is_nan())),
            _ => Err(()),
        }
    }

    fn same_term(&mut self, a: &Val, b: &Val) -> bool {
        match (a, b) {
            (Val::Id(x), Val::Id(y)) => x == y,
            _ => {
                let (x, y) = (self.to_id(a), self.to_id(b));
                x == y
            }
        }
    }

    /// `=`: value equality where defined, else term equality (or an error for
    /// different literals of unknown types).
    pub fn equal(&mut self, a: &Val, b: &Val) -> R<bool> {
        if let (Val::Id(x), Val::Id(y)) = (a, b) {
            let (kx, ky) = (self.kind(a), self.kind(b));
            // An IRI or blank node equals only itself.
            if matches!(kx, dict::IRI | dict::BNODE) || matches!(ky, dict::IRI | dict::BNODE) {
                return Ok(x == y);
            }
            if let Some((p, q)) = self.simple_pair(*x, *y) {
                return Ok(p == q);
            }
        }
        if let (Some(x), Some(y)) = (self.num(a), self.num(b)) {
            return Ok(num::cmp(x, y) == Some(Ordering::Equal));
        }
        if self.kind(a) == dict::TRIPLE && self.kind(b) == dict::TRIPLE {
            let (x, y) = (self.to_id(a), self.to_id(b));
            let (tx, ty) = (*self.dict.term(x).ok_or(())?, *self.dict.term(y).ok_or(())?);
            for (p, q) in [(tx.a, ty.a), (tx.b, ty.b), (tx.c, ty.c)] {
                if !self.equal(&Val::Id(p), &Val::Id(q))? {
                    return Ok(false);
                }
            }
            return Ok(true);
        }
        if self.same_term(a, b) {
            return Ok(true);
        }
        let (la, lb) = (self.lit(a), self.lit(b));
        match (la, lb) {
            (Some(x), Some(y)) => {
                // An ill-formed literal of a known datatype compares like one of an unknown type.
                let (kx, ky) = (self.dt_kind(x.dt), self.dt_kind(y.dt));
                let (kx, ky) = (self.well_formed(a, &x, kx), self.well_formed(b, &y, ky));
                if (kx, ky) == (Dt::Other, Dt::Other) {
                    if let Some(o) = self.temporal_cmp(&x, &y, true) {
                        return o.map(|o| o == Ordering::Equal);
                    }
                }
                match (kx, ky) {
                    (Dt::String, Dt::String) => Ok(x.value == y.value),
                    (Dt::LangString, Dt::LangString) => Ok(x.value == y.value && x.lang == y.lang),
                    (Dt::Boolean, Dt::Boolean) => Ok(self.boolean(a).ok_or(())? == self.boolean(b).ok_or(())?),
                    (Dt::DateTime, Dt::DateTime) => Ok(datetime_cmp(&x.value, &y.value).ok_or(())? == Ordering::Equal),
                    (Dt::Date, Dt::Date) => Ok(datetime_cmp(&date_as_datetime(&x.value).ok_or(())?, &date_as_datetime(&y.value).ok_or(())?).ok_or(())? == Ordering::Equal),
                    // A language-tagged string differs from every typed literal.
                    (Dt::LangString, _) | (_, Dt::LangString) => Ok(false),
                    // Unknown datatypes (and ill-formed literals) may share values with any typed literal.
                    (Dt::Other, _) | (_, Dt::Other) => Err(()),
                    (Dt::Integer | Dt::Decimal | Dt::Float | Dt::Double, Dt::Integer | Dt::Decimal | Dt::Float | Dt::Double) => Err(()),
                    _ => Ok(false),
                }
            }
            _ => Ok(false),
        }
    }

    fn well_formed(&mut self, v: &Val, l: &Lit, k: Dt) -> Dt {
        let ok = match k {
            Dt::Integer | Dt::Decimal | Dt::Float | Dt::Double => self.num(v).is_some(),
            Dt::Boolean => self.boolean(v).is_some(),
            Dt::DateTime => datetime(&l.value).is_some(),
            Dt::Date => date_as_datetime(&l.value).is_some(),
            _ => true,
        };
        if ok { k } else { Dt::Other }
    }

    /// `<`, `>`, ...: an ordering for comparable values, else an error.
    pub fn order(&mut self, a: &Val, b: &Val) -> R<Ordering> {
        if let (Val::Id(x), Val::Id(y)) = (a, b) {
            if let Some((p, q)) = self.simple_pair(*x, *y) {
                return Ok(p.cmp(q));
            }
        }
        if let (Some(x), Some(y)) = (self.num(a), self.num(b)) {
            return num::cmp(x, y).ok_or(());
        }
        let (x, y) = (self.lit(a).ok_or(())?, self.lit(b).ok_or(())?);
        match (self.dt_kind(x.dt), self.dt_kind(y.dt)) {
            (Dt::String, Dt::String) => Ok(x.value.cmp(&y.value)),
            (Dt::LangString, Dt::LangString) if x.lang == y.lang => Ok(x.value.cmp(&y.value)),
            (Dt::Boolean, Dt::Boolean) => Ok(self.boolean(a).ok_or(())?.cmp(&self.boolean(b).ok_or(())?)),
            (Dt::DateTime, Dt::DateTime) => datetime_cmp(&x.value, &y.value).ok_or(()),
            (Dt::Date, Dt::Date) => datetime_cmp(&date_as_datetime(&x.value).ok_or(())?, &date_as_datetime(&y.value).ok_or(())?).ok_or(()),
            (Dt::Other, Dt::Other) => self.temporal_cmp(&x, &y, false).unwrap_or(Err(())),
            _ => Err(()),
        }
    }

    /// The total order of ORDER BY: unbound, blank nodes, IRIs, literals, triple terms.
    pub fn sort_cmp(&mut self, a: Option<&Val>, b: Option<&Val>) -> Ordering {
        let rank = |me: &Self, v: Option<&Val>| match v {
            None => 0,
            Some(v) => match me.kind(v) {
                dict::BNODE => 1,
                dict::IRI => 2,
                dict::TRIPLE => 4,
                _ => 3,
            },
        };
        let (ra, rb) = (rank(self, a), rank(self, b));
        if ra != rb {
            return ra.cmp(&rb);
        }
        let (Some(a), Some(b)) = (a, b) else { return Ordering::Equal };
        match ra {
            1 | 2 => {
                let (x, y) = (self.to_id(a), self.to_id(b));
                self.dict.value(x).cmp(self.dict.value(y))
            }
            3 => {
                if let Ok(o) = self.order(a, b) {
                    return o;
                }
                let (x, y) = (self.lit(a).unwrap(), self.lit(b).unwrap());
                // Numbers first, then by lexical form, datatype and language.
                let (nx, ny) = (self.num(a).is_some(), self.num(b).is_some());
                ny.cmp(&nx)
                    .then_with(|| x.value.cmp(&y.value))
                    .then_with(|| self.dict.value(x.dt).cmp(self.dict.value(y.dt)))
                    .then_with(|| x.lang.cmp(&y.lang))
            }
            _ => {
                let (x, y) = (self.to_id(a), self.to_id(b));
                let (tx, ty) = (*self.dict.term(x).unwrap(), *self.dict.term(y).unwrap());
                for (p, q) in [(tx.a, ty.a), (tx.b, ty.b), (tx.c, ty.c)] {
                    let o = self.sort_cmp(Some(&Val::Id(p)), Some(&Val::Id(q)));
                    if o != Ordering::Equal {
                        return o;
                    }
                }
                Ordering::Equal
            }
        }
    }

    pub fn expr(&mut self, e: &Expr, row: &[u32]) -> R<Val> {
        match e {
            Expr::Var(v) => match row[*v as usize] {
                NONE => Err(()),
                id => Ok(Val::Id(id)),
            },
            Expr::Const(c) => Ok(Val::Id(*c)),
            Expr::Bound(v) => Ok(Val::Bool(row[*v as usize] != NONE)),
            Expr::Or(a, b) => {
                let x = self.expr(a, row).and_then(|v| self.ebv(&v));
                if x == Ok(true) {
                    return Ok(Val::Bool(true));
                }
                let y = self.expr(b, row).and_then(|v| self.ebv(&v));
                match (x, y) {
                    (_, Ok(true)) => Ok(Val::Bool(true)),
                    (Ok(false), Ok(false)) => Ok(Val::Bool(false)),
                    _ => Err(()),
                }
            }
            Expr::And(a, b) => {
                let x = self.expr(a, row).and_then(|v| self.ebv(&v));
                if x == Ok(false) {
                    return Ok(Val::Bool(false));
                }
                let y = self.expr(b, row).and_then(|v| self.ebv(&v));
                match (x, y) {
                    (_, Ok(false)) => Ok(Val::Bool(false)),
                    (Ok(true), Ok(true)) => Ok(Val::Bool(true)),
                    _ => Err(()),
                }
            }
            Expr::Not(a) => {
                let v = self.expr(a, row)?;
                Ok(Val::Bool(!self.ebv(&v)?))
            }
            Expr::Cmp(op, a, b) => {
                let (x, y) = (self.expr(a, row)?, self.expr(b, row)?);
                Ok(Val::Bool(match op {
                    Cmp::Eq => self.equal(&x, &y)?,
                    Cmp::Ne => !self.equal(&x, &y)?,
                    Cmp::Lt => self.order(&x, &y)? == Ordering::Less,
                    Cmp::Gt => self.order(&x, &y)? == Ordering::Greater,
                    Cmp::Le => self.order(&x, &y)? != Ordering::Greater,
                    Cmp::Ge => self.order(&x, &y)? != Ordering::Less,
                }))
            }
            Expr::In(a, list, negated) => {
                let x = self.expr(a, row)?;
                let mut error = false;
                for item in list {
                    match self.expr(item, row).and_then(|y| self.equal(&x, &y)) {
                        Ok(true) => return Ok(Val::Bool(!negated)),
                        Ok(false) => {}
                        Err(()) => error = true,
                    }
                }
                if error { Err(()) } else { Ok(Val::Bool(*negated)) }
            }
            Expr::Arith(op, a, b) => {
                let (x, y) = (self.expr(a, row)?, self.expr(b, row)?);
                let (Some(x), Some(y)) = (self.num(&x), self.num(&y)) else { return self.temporal_arith(*op, &x, &y) };
                if *op == Op::Div && matches!(Num::promote(x, y), (Num::Int(_) | Num::Dec(_), _)) && y.is_zero() {
                    return Err(());
                }
                num::arith(*op, x, y).map(Val::Num).ok_or(())
            }
            Expr::Neg(a) => {
                let v = self.expr(a, row)?;
                let Some(n) = self.num(&v) else { return self.temporal_neg(&v) };
                Ok(Val::Num(match n {
                    Num::Int(i) => Num::Int(-i),
                    Num::Dec(d) => Num::Dec(-d),
                    Num::Float(f) => Num::Float(-f),
                    Num::Double(d) => Num::Double(-d),
                }))
            }
            Expr::Plus(a) => {
                let v = self.expr(a, row)?;
                Ok(Val::Num(self.num(&v).ok_or(())?))
            }
            Expr::If(c, a, b) => {
                let v = self.expr(c, row)?;
                if self.ebv(&v)? { self.expr(a, row) } else { self.expr(b, row) }
            }
            Expr::Coalesce(list) => {
                for x in list {
                    if let Ok(v) = self.expr(x, row) {
                        return Ok(v);
                    }
                }
                Err(())
            }
            Expr::Exists(p, negated) => {
                let found = self.exists(p, row);
                Ok(Val::Bool(found != *negated))
            }
            Expr::Cast(dt, a) => {
                let v = self.expr(a, row)?;
                self.cast(*dt, &v)
            }
            Expr::Unknown => Err(()),
            Expr::Call(f, args) => self.call(*f, args, row),
        }
    }

    pub(crate) fn cast(&mut self, dt: u32, v: &Val) -> R<Val> {
        let local = self.dict.value(dt).strip_prefix(XSD).ok_or(())?.to_vec();
        if self.kind(v) == dict::IRI {
            if local == b"string" {
                let id = self.to_id(v);
                return Ok(Val::Str(self.dict.value(id).to_vec(), None));
            }
            return Err(());
        }
        let l = self.lit(v).ok_or(())?;
        let src = self.dt_kind(l.dt);
        if l.lang.is_some() && local != b"string" {
            return Err(());
        }
        let s = trim(&l.value);
        let n = self.num(v);
        let numeric_source = matches!(src, Dt::Integer | Dt::Decimal | Dt::Float | Dt::Double);
        if numeric_source && n.is_none() {
            return Err(());
        }
        if let Some(r) = self.temporal_cast(&local, &l, src == Dt::String) {
            return r;
        }
        match classify(&local) {
            // A number, boolean, date, time or duration is written in its canonical form.
            Dt::String => {
                let mut out = Vec::new();
                match (src, n) {
                    (Dt::Integer | Dt::Decimal | Dt::Float | Dt::Double, Some(n)) => num::write_num(&mut out, n),
                    (Dt::Boolean, _) => out.extend_from_slice(if self.boolean(v).ok_or(())? { b"true" } else { b"false" }),
                    _ => {
                        let own = self.dict.value(l.dt).strip_prefix(XSD).map(<[u8]>::to_vec);
                        match own.and_then(|own| self.temporal_cast(&own, &l, false)) {
                            Some(Ok(t)) => out = self.lit(&t).ok_or(())?.value,
                            Some(Err(())) => return Err(()),
                            None => out = l.value,
                        }
                    }
                }
                Ok(Val::Str(out, None))
            }
            Dt::Boolean => match (src, n) {
                (_, Some(n)) => Ok(Val::Bool(!n.is_zero() && !n.is_nan())),
                (Dt::Boolean | Dt::String, _) => match s {
                    b"true" | b"1" => Ok(Val::Bool(true)),
                    b"false" | b"0" => Ok(Val::Bool(false)),
                    _ => Err(()),
                },
                _ => Err(()),
            },
            Dt::Integer => {
                let i = match (src, n) {
                    (_, Some(Num::Int(i))) => i,
                    (_, Some(Num::Dec(d))) => d / num::SCALE,
                    (_, Some(x @ (Num::Float(_) | Num::Double(_)))) => {
                        let f = x.as_f64();
                        if !f.is_finite() {
                            return Err(());
                        }
                        f as i128
                    }
                    (Dt::Boolean, _) => (self.boolean(v).ok_or(())?) as i128,
                    (Dt::String, _) => num::parse_int(s).ok_or(())?,
                    _ => return Err(()),
                };
                if local == b"integer" {
                    Ok(Val::Num(Num::Int(i)))
                } else {
                    let mut out = Vec::new();
                    num::write_int(&mut out, i);
                    Ok(Val::Typed(out, dt))
                }
            }
            Dt::Decimal => Ok(Val::Num(Num::Dec(match (src, n) {
                (_, Some(Num::Int(i))) => i.checked_mul(num::SCALE).ok_or(())?,
                (_, Some(Num::Dec(d))) => d,
                (_, Some(x)) => {
                    let f = x.as_f64();
                    if !f.is_finite() {
                        return Err(());
                    }
                    (f * num::SCALE as f64) as i128
                }
                (Dt::Boolean, _) => (self.boolean(v).ok_or(())?) as i128 * num::SCALE,
                (Dt::String, _) => {
                    if s.contains(&b'e') || s.contains(&b'E') {
                        return Err(());
                    }
                    num::parse_dec(s).ok_or(())?
                }
                _ => return Err(()),
            }))),
            k @ (Dt::Float | Dt::Double) => {
                let f = match (src, n) {
                    (_, Some(n)) => n.as_f64(),
                    (Dt::Boolean, _) => (self.boolean(v).ok_or(())?) as u8 as f64,
                    (Dt::String, _) => num::parse_double(s).ok_or(())?,
                    _ => return Err(()),
                };
                Ok(Val::Num(if k == Dt::Float { Num::Float(f as f32) } else { Num::Double(f) }))
            }
            Dt::DateTime => match src {
                Dt::DateTime | Dt::String => {
                    datetime(s).ok_or(())?;
                    Ok(Val::Typed(s.to_vec(), dt))
                }
                _ => Err(()),
            },
            _ => match src {
                Dt::String => Ok(Val::Typed(s.to_vec(), dt)),
                _ if l.dt == dt => Ok(Val::Typed(l.value, dt)),
                _ => Err(()),
            },
        }
    }

    fn call(&mut self, f: Func, args: &[Expr], row: &[u32]) -> R<Val> {
        use Func::*;
        // Functions with their own argument handling.
        match f {
            Bnode => {
                if args.is_empty() {
                    return Ok(Val::Id(self.dict.fresh_bnode()));
                }
                let v = self.expr(&args[0], row)?;
                let (s, l) = self.string(&v)?;
                if l.is_some() {
                    return Err(());
                }
                return Ok(Val::Id(self.bnode_for(row, &s)));
            }
            Rand => return Ok(Val::Num(Num::Double(self.random()))),
            Now => return Ok(Val::Typed(self.now_lexical(), self.xsd(b"dateTime"))),
            Uuid => {
                let mut s = b"urn:uuid:".to_vec();
                s.extend_from_slice(&self.uuid());
                return Ok(Val::Iri(s));
            }
            StrUuid => return Ok(Val::Str(self.uuid(), None)),
            _ => {}
        }
        let mut v = Vec::with_capacity(args.len());
        for a in args {
            v.push(self.expr(a, row)?);
        }
        match f {
            Str => match self.kind(&v[0]) {
                dict::IRI => {
                    let id = self.to_id(&v[0]);
                    Ok(Val::Str(self.dict.value(id).to_vec(), None))
                }
                dict::LITERAL => {
                    if let Val::Id(i) = v[0] {
                        return Ok(Val::Str(self.dict.value(i).to_vec(), None));
                    }
                    if let Val::Num(n) = v[0] {
                        let mut s = Vec::new();
                        num::write_num(&mut s, n);
                        return Ok(Val::Str(s, None));
                    }
                    Ok(Val::Str(self.lit(&v[0]).ok_or(())?.value, None))
                }
                _ => Err(()),
            },
            Lang => {
                if let Val::Id(i) = v[0] {
                    if self.kind(&v[0]) != dict::LITERAL {
                        return Err(());
                    }
                    return Ok(Val::Str(self.dict.lang(i).to_vec(), None));
                }
                let l = self.lit(&v[0]).ok_or(())?;
                Ok(Val::Str(l.lang.map(|x| x.0).unwrap_or_default(), None))
            }
            LangDir => {
                let l = self.lit(&v[0]).ok_or(())?;
                let d = l.lang.map_or(0, |x| x.1);
                Ok(Val::Str(match d {
                    1 => b"ltr".to_vec(),
                    2 => b"rtl".to_vec(),
                    _ => Vec::new(),
                }, None))
            }
            HasLang => Ok(Val::Bool(self.lit(&v[0]).is_some_and(|l| l.lang.is_some()))),
            HasLangDir => Ok(Val::Bool(self.lit(&v[0]).is_some_and(|l| l.lang.is_some_and(|x| x.1 != 0)))),
            LangMatches => {
                let (tag, _) = self.string(&v[0])?;
                let (range, _) = self.string(&v[1])?;
                let n = range.len();
                Ok(Val::Bool(if range == b"*" {
                    !tag.is_empty()
                } else {
                    tag.len() >= n && tag[..n].eq_ignore_ascii_case(&range) && (tag.len() == n || tag[n] == b'-')
                }))
            }
            Datatype => {
                let l = self.lit(&v[0]).ok_or(())?;
                Ok(Val::Id(l.dt))
            }
            Iri => match self.kind(&v[0]) {
                dict::IRI => Ok(v[0].clone()),
                _ => {
                    self.str_check(&v[0])?;
                    let (s, l) = self.str_view(&v[0]);
                    if l.is_some() {
                        return Err(());
                    }
                    let mut out = Vec::new();
                    iri::resolve(self.base, s, &mut out);
                    Ok(Val::Iri(out))
                }
            },
            Abs | Ceil | Floor | Round => {
                let n = self.num(&v[0]).ok_or(())?;
                Ok(Val::Num(match f {
                    Abs => num::abs(n),
                    Ceil => num::ceil(n),
                    Floor => num::floor(n),
                    _ => num::round(n),
                }))
            }
            Concat => {
                let mut out = Vec::new();
                let mut lang: Option<Option<(Vec<u8>, u8)>> = None;
                for x in &v {
                    self.str_check(x)?;
                    let (s, l) = self.str_view(x);
                    out.extend_from_slice(s);
                    lang = Some(match lang {
                        None => l.map(|(t, d)| (t.to_vec(), d)),
                        Some(prev) if prev.as_ref().map(|(t, d)| (&t[..], *d)) == l => prev,
                        Some(_) => None,
                    });
                }
                Ok(Val::Str(out, lang.flatten()))
            }
            SubStr => {
                self.str_check(&v[0])?;
                let start = self.num(&v[1]).ok_or(())?.as_f64();
                let length = if v.len() > 2 { Some(self.num(&v[2]).ok_or(())?.as_f64()) } else { None };
                let (s, l) = self.str_view(&v[0]);
                let (from, to) = substr_range(s, start, length);
                Ok(Val::Str(s[from..to].to_vec(), l.map(|(t, d)| (t.to_vec(), d))))
            }
            StrLen => {
                self.str_check(&v[0])?;
                let (s, _) = self.str_view(&v[0]);
                Ok(Val::Num(Num::Int(s.iter().filter(|&&b| b & 0xc0 != 0x80).count() as i128)))
            }
            UCase | LCase => {
                let (s, l) = self.string(&v[0])?;
                let mut out = Vec::with_capacity(s.len());
                for c in regex::chars(&s) {
                    let ch = char::from_u32(c).ok_or(())?;
                    if f == UCase {
                        ch.to_uppercase().for_each(|u| crate::lex::push_utf8(&mut out, u as u32));
                    } else {
                        ch.to_lowercase().for_each(|u| crate::lex::push_utf8(&mut out, u as u32));
                    }
                }
                Ok(Val::Str(out, l))
            }
            EncodeForUri => {
                let (s, _) = self.string(&v[0])?;
                let mut out = Vec::with_capacity(s.len());
                for b in s {
                    if b.is_ascii_alphanumeric() || b"-_.~".contains(&b) {
                        out.push(b);
                    } else {
                        out.push(b'%');
                        out.push(b"0123456789ABCDEF"[(b >> 4) as usize]);
                        out.push(b"0123456789ABCDEF"[(b & 15) as usize]);
                    }
                }
                Ok(Val::Str(out, None))
            }
            Contains | StrStarts | StrEnds | StrBefore | StrAfter => {
                self.str_check(&v[0])?;
                self.str_check(&v[1])?;
                let ((a, la), (b, lb)) = (self.str_view(&v[0]), self.str_view(&v[1]));
                if lb.is_some() && la.map(|x| x.0) != lb.map(|x| x.0) {
                    return Err(());
                }
                let la = la.map(|(t, d)| (t.to_vec(), d));
                let at = find(a, b);
                Ok(match f {
                    Contains => Val::Bool(at.is_some()),
                    StrStarts => Val::Bool(a.starts_with(b)),
                    StrEnds => Val::Bool(a.ends_with(b)),
                    StrBefore => match at {
                        Some(i) => Val::Str(a[..i].to_vec(), if i == 0 && !b.is_empty() { None } else { la }),
                        None => Val::Str(Vec::new(), None),
                    },
                    _ => match at {
                        Some(i) => Val::Str(a[i + b.len()..].to_vec(), if i + b.len() == a.len() && !b.is_empty() { None } else { la }),
                        None => Val::Str(Vec::new(), None),
                    },
                })
            }
            Regex => {
                let (s, _) = self.string(&v[0])?;
                let (p, pl) = self.string(&v[1])?;
                let flags = if v.len() > 2 { self.string(&v[2])?.0 } else { Vec::new() };
                if pl.is_some() {
                    return Err(());
                }
                let re = self.regex(&p, &flags)?;
                Ok(Val::Bool(re.find(&regex::chars(&s), 0).is_some()))
            }
            Replace => {
                let (s, l) = self.string(&v[0])?;
                let (p, _) = self.string(&v[1])?;
                let (r, _) = self.string(&v[2])?;
                let flags = if v.len() > 3 { self.string(&v[3])?.0 } else { Vec::new() };
                // XPath makes a pattern that matches the empty string an error;
                // Oxigraph (and Semblance's text projection) replace anyway.
                let re = self.regex(&p, &flags)?;
                let text = regex::chars(&s);
                let repl = regex::chars(&r);
                let literal = flags.contains(&b'q');
                let mut out = Vec::new();
                let mut at = 0;
                while at <= text.len() {
                    let Some(m) = re.find(&text, at) else { break };
                    let (ms, me) = (m[0], m[1]);
                    text[at..ms].iter().for_each(|&c| crate::lex::push_utf8(&mut out, c));
                    let mut i = 0;
                    while i < repl.len() {
                        let c = repl[i];
                        if !literal && c == '\\' as u32 && i + 1 < repl.len() {
                            crate::lex::push_utf8(&mut out, repl[i + 1]);
                            i += 2;
                        } else if !literal && c == '$' as u32 && i + 1 < repl.len() && (0x30..=0x39).contains(&repl[i + 1]) {
                            let k = (repl[i + 1] - 0x30) as usize;
                            if let (Some(&a), Some(&b)) = (m.get(2 * k), m.get(2 * k + 1)) {
                                if a != usize::MAX && b != usize::MAX {
                                    text[a..b].iter().for_each(|&c| crate::lex::push_utf8(&mut out, c));
                                }
                            }
                            i += 2;
                        } else {
                            crate::lex::push_utf8(&mut out, c);
                            i += 1;
                        }
                    }
                    if me == ms {
                        if ms < text.len() {
                            crate::lex::push_utf8(&mut out, text[ms]);
                        }
                        at = ms + 1;
                    } else {
                        at = me;
                    }
                }
                if at < text.len() {
                    text[at..].iter().for_each(|&c| crate::lex::push_utf8(&mut out, c));
                }
                Ok(Val::Str(out, l))
            }
            Year | Month | Day | Hours | Minutes | Seconds | Timezone | Tz => {
                let l = self.lit(&v[0]).ok_or(())?;
                if self.dt_kind(l.dt) != Dt::DateTime {
                    return Err(());
                }
                let d = datetime(&l.value).ok_or(())?;
                Ok(match f {
                    Year => Val::Num(Num::Int(d.year as i128)),
                    Month => Val::Num(Num::Int(d.month as i128)),
                    Day => Val::Num(Num::Int(d.day as i128)),
                    Hours => Val::Num(Num::Int(d.hour as i128)),
                    Minutes => Val::Num(Num::Int(d.minute as i128)),
                    Seconds => Val::Num(Num::Dec(num::parse_dec(d.seconds).ok_or(())?)),
                    Tz => Val::Str(d.tz.to_vec(), None),
                    _ => {
                        let off = d.offset_minutes.ok_or(())?;
                        let mut s = Vec::new();
                        if off < 0 {
                            s.push(b'-');
                        }
                        s.extend_from_slice(b"PT");
                        let (h, m) = (off.abs() / 60, off.abs() % 60);
                        if h > 0 || m == 0 {
                            num::write_int(&mut s, h as i128);
                            s.push(b'H');
                        }
                        if m > 0 {
                            num::write_int(&mut s, m as i128);
                            s.push(b'M');
                        }
                        if off == 0 {
                            s = b"PT0S".to_vec();
                        }
                        let dt = self.xsd(b"dayTimeDuration");
                        Val::Typed(s, dt)
                    }
                })
            }
            Md5 => {
                let (s, l) = self.string(&v[0])?;
                if l.is_some() {
                    return Err(());
                }
                let h = super::md5::md5(&s);
                let mut out = Vec::with_capacity(32);
                for b in h {
                    out.push(b"0123456789abcdef"[(b >> 4) as usize]);
                    out.push(b"0123456789abcdef"[(b & 15) as usize]);
                }
                Ok(Val::Str(out, None))
            }
            Sha1 | Sha256 | Sha384 | Sha512 => {
                let (s, l) = self.string(&v[0])?;
                if l.is_some() {
                    return Err(());
                }
                let h = match f {
                    Sha1 => super::sha::sha1(&s),
                    Sha256 => super::sha::sha256(&s),
                    Sha384 => super::sha::sha384(&s),
                    _ => super::sha::sha512(&s),
                };
                let mut out = Vec::with_capacity(h.len() * 2);
                for b in h {
                    out.push(b"0123456789abcdef"[(b >> 4) as usize]);
                    out.push(b"0123456789abcdef"[(b & 15) as usize]);
                }
                Ok(Val::Str(out, None))
            }
            StrLang | StrLangDir => {
                let (s, l) = self.string(&v[0])?;
                if l.is_some() {
                    return Err(());
                }
                let (tag, _) = self.string(&v[1])?;
                let mut c = crate::lex::Cur::new(&tag);
                let mut lang = Vec::new();
                let dir0 = c.langdir(&mut lang).map_err(|_| ())?;
                if !c.eof() || dir0 != 0 {
                    return Err(());
                }
                let dir = if f == StrLangDir {
                    match &self.string(&v[2])?.0[..] {
                        b"ltr" => 1,
                        b"rtl" => 2,
                        _ => return Err(()),
                    }
                } else {
                    0
                };
                Ok(Val::Str(s, Some((lang, dir))))
            }
            StrDt => {
                let (s, l) = self.string(&v[0])?;
                if l.is_some() || self.kind(&v[1]) != dict::IRI {
                    return Err(());
                }
                let dt = self.to_id(&v[1]);
                Ok(Val::Typed(s, dt))
            }
            SameTerm => Ok(Val::Bool(self.same_term(&v[0], &v[1]))),
            IsIri => Ok(Val::Bool(self.kind(&v[0]) == dict::IRI)),
            IsBlank => Ok(Val::Bool(self.kind(&v[0]) == dict::BNODE)),
            IsLiteral => Ok(Val::Bool(self.kind(&v[0]) == dict::LITERAL)),
            IsTriple => Ok(Val::Bool(self.kind(&v[0]) == dict::TRIPLE)),
            IsNumeric => Ok(Val::Bool(self.num(&v[0]).is_some())),
            Triple => {
                let (s, p, o) = (self.to_id(&v[0]), self.to_id(&v[1]), self.to_id(&v[2]));
                let ks = self.dict.term(s).map(|t| t.kind);
                if !matches!(ks, Some(dict::IRI | dict::BNODE)) || self.dict.term(p).map(|t| t.kind) != Some(dict::IRI) {
                    return Err(());
                }
                Ok(Val::Id(self.dict.intern(Key::triple(s, p, o))))
            }
            Subject | Predicate | Object => {
                let id = self.to_id(&v[0]);
                let t = *self.dict.term(id).ok_or(())?;
                if t.kind != dict::TRIPLE {
                    return Err(());
                }
                Ok(Val::Id(match f {
                    Subject => t.a,
                    Predicate => t.b,
                    _ => t.c,
                }))
            }
            Bnode | Rand | Now | Uuid | StrUuid => unreachable!(),
        }
    }
}

fn trim(s: &[u8]) -> &[u8] {
    let is_ws = |c: &u8| matches!(c, b' ' | b'\t' | b'\n' | b'\r');
    let start = s.iter().position(|c| !is_ws(c)).unwrap_or(s.len());
    let end = s.iter().rposition(|c| !is_ws(c)).map_or(start, |i| i + 1);
    &s[start..end]
}

/// SUBSTR's byte range of s: characters p (1-based) with first <= p < end,
/// first and end the rounded start and start + length.
pub fn substr_range(s: &[u8], start: f64, length: Option<f64>) -> (usize, usize) {
    let first = libm_round(start);
    let end = match length {
        Some(n) => first + libm_round(n),
        None => f64::INFINITY,
    };
    let (mut from, mut to, mut p) = (s.len(), s.len(), 0.0);
    for (i, &b) in s.iter().enumerate() {
        if b & 0xc0 != 0x80 {
            p += 1.0;
            if p >= first && from == s.len() && p < end {
                from = i;
            }
            if p >= end && from != s.len() {
                to = i;
                break;
            }
        }
    }
    (from.min(to), to)
}

pub fn find(a: &[u8], b: &[u8]) -> Option<usize> {
    if b.is_empty() {
        return Some(0);
    }
    a.windows(b.len()).position(|w| w == b)
}

fn libm_round(x: f64) -> f64 {
    if !x.is_finite() {
        return x;
    }
    let t = (x + 0.5) as i64 as f64;
    if t > x + 0.5 { t - 1.0 } else { t }
}

pub struct DateTime<'a> {
    pub year: i64,
    pub month: u32,
    pub day: u32,
    pub hour: u32,
    pub minute: u32,
    pub seconds: &'a [u8],
    pub tz: &'a [u8],
    pub offset_minutes: Option<i32>,
}

/// Parse an xsd:dateTime lexical form.
pub fn datetime(s: &[u8]) -> Option<DateTime<'_>> {
    let num = |b: &[u8]| -> Option<u32> {
        if b.is_empty() || !b.iter().all(u8::is_ascii_digit) {
            return None;
        }
        Some(b.iter().fold(0u32, |n, d| n * 10 + (d - b'0') as u32))
    };
    let neg = s.first() == Some(&b'-');
    let body = if neg { &s[1..] } else { s };
    let dash = body.iter().position(|&c| c == b'-')?;
    if dash < 4 {
        return None;
    }
    let year = num(&body[..dash])? as i64;
    let rest = &body[dash + 1..];
    if rest.len() < 14 || rest[2] != b'-' || rest[5] != b'T' || rest[8] != b':' || rest[11] != b':' {
        return None;
    }
    let (month, day, hour, minute) = (num(&rest[0..2])?, num(&rest[3..5])?, num(&rest[6..8])?, num(&rest[9..11])?);
    let tail = &rest[12..];
    let sec_end = tail.iter().position(|&c| c == b'Z' || c == b'+' || c == b'-').unwrap_or(tail.len());
    let seconds = &tail[..sec_end];
    if seconds.len() < 2 || num(&seconds[..2]).is_none() {
        return None;
    }
    let tz = &tail[sec_end..];
    let offset_minutes = match tz {
        b"" => None,
        b"Z" => Some(0),
        _ if tz.len() == 6 && tz[3] == b':' => {
            let m = (num(&tz[1..3])? * 60 + num(&tz[4..6])?) as i32;
            Some(if tz[0] == b'-' { -m } else { m })
        }
        _ => return None,
    };
    if !(1..=12).contains(&month) || !(1..=31).contains(&day) || hour > 24 || minute > 59 {
        return None;
    }
    Some(DateTime { year: if neg { -year } else { year }, month, day, hour, minute, seconds, tz, offset_minutes })
}

/// An xsd:date as the dateTime at its start ("2001-01-01Z" -> "2001-01-01T00:00:00Z").
pub fn date_as_datetime(s: &[u8]) -> Option<Vec<u8>> {
    let neg = s.first() == Some(&b'-');
    let body = if neg { &s[1..] } else { s };
    let dash = body.iter().position(|&c| c == b'-')?;
    let end = dash + 6;
    if body.len() < end || body[dash + 3] != b'-' {
        return None;
    }
    let cut = s.len() - body.len() + end;
    let mut out = s[..cut].to_vec();
    out.extend_from_slice(b"T00:00:00");
    out.extend_from_slice(&s[cut..]);
    datetime(&out)?;
    Some(out)
}

/// Compare two dateTimes (timezone-adjusted when both have one).
pub fn datetime_cmp(a: &[u8], b: &[u8]) -> Option<Ordering> {
    let (x, y) = (datetime(a)?, datetime(b)?);
    // Seconds since the epoch (scaled decimal), as if in UTC when there is no timezone.
    let key = |d: &DateTime| -> i128 {
        let days = days_from_civil(d.year, d.month, d.day) as i128;
        let mins = days * 1440 + d.hour as i128 * 60 + d.minute as i128 - d.offset_minutes.unwrap_or(0) as i128;
        mins * 60 * num::SCALE + num::parse_dec(d.seconds).unwrap_or(0)
    };
    let (kx, ky) = (key(&x), key(&y));
    if x.offset_minutes.is_some() == y.offset_minutes.is_some() {
        return Some(kx.cmp(&ky));
    }
    // One without a timezone could be anywhere from -14:00 to +14:00.
    let spread = 14 * 3600 * num::SCALE;
    let (t, n, flip) = if x.offset_minutes.is_some() { (kx, ky, false) } else { (ky, kx, true) };
    let o = if t < n - spread {
        Ordering::Less
    } else if t > n + spread {
        Ordering::Greater
    } else {
        return None;
    };
    Some(if flip { o.reverse() } else { o })
}

pub fn days_from_civil(y: i64, m: u32, d: u32) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = (m as i64 + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d as i64 - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146097 + doe - 719468
}

pub fn civil_from_days(z: i64) -> (i64, u32, u32) {
    let z = z + 719468;
    let era = z.div_euclid(146097);
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (yoe + era * 400 + if m <= 2 { 1 } else { 0 }, m, d)
}
