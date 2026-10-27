//! Dates, times and durations: XPath's arithmetic and comparisons on them
//! (op:add-dayTimeDuration-to-dateTime, op:subtract-dateTimes,
//! op:multiply-dayTimeDuration, ...). A value without a timezone is taken to be
//! in UTC, the implicit timezone. As in Oxigraph, an xsd:duration may also be
//! added to a dateTime, and durations of different kinds add to an xsd:duration.

use super::Op;
use super::eval::Ctx;
use super::expr::{civil_from_days, date_as_datetime, datetime, days_from_civil, Lit, Val, R};
use super::num::{self, Num, SCALE};
use alloc::vec::Vec;
use core::cmp::Ordering;

const XSD: &[u8] = b"http://www.w3.org/2001/XMLSchema#";
const DAY: i128 = 86400 * SCALE;
/// Seconds are kept to the microsecond, as in the WAT build.
const MICRO: i128 = SCALE / 1_000_000;

/// Scaled seconds truncated to the microsecond.
fn micro(secs: i128) -> i128 {
    secs - secs % MICRO
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum K {
    DateTime,
    Date,
    Time,
    Duration,
    DayTime,
    YearMonth,
}

/// A dateTime, date or time on its own clock: days since the epoch, and scaled
/// seconds into the day (a time is on 1972-12-31).
#[derive(Clone, Copy)]
struct Moment {
    k: K,
    days: i64,
    secs: i128,
    tz: Option<i32>,
}

/// A duration: months and scaled seconds, never of opposite signs.
#[derive(Clone, Copy)]
struct Dur {
    k: K,
    months: i64,
    secs: i128,
}

enum T {
    M(Moment),
    D(Dur),
}

fn kind_of(local: &[u8]) -> Option<K> {
    Some(match local {
        b"dateTime" | b"dateTimeStamp" => K::DateTime,
        b"date" => K::Date,
        b"time" => K::Time,
        b"duration" => K::Duration,
        b"dayTimeDuration" => K::DayTime,
        b"yearMonthDuration" => K::YearMonth,
        _ => return None,
    })
}

fn local(k: K) -> &'static [u8] {
    match k {
        K::DateTime => b"dateTime",
        K::Date => b"date",
        K::Time => b"time",
        K::Duration => b"duration",
        K::DayTime => b"dayTimeDuration",
        K::YearMonth => b"yearMonthDuration",
    }
}

fn days_in(y: i64, m: u32) -> u32 {
    match m {
        2 if y % 4 == 0 && (y % 100 != 0 || y % 400 == 0) => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

fn moment(k: K, s: &[u8]) -> Option<Moment> {
    let text = match k {
        K::DateTime => s.to_vec(),
        K::Date => date_as_datetime(s)?,
        _ => {
            let mut v = b"1972-12-31T".to_vec();
            v.extend_from_slice(s);
            v
        }
    };
    let d = datetime(&text)?;
    let sec = micro(num::parse_dec(d.seconds)?);
    if d.day > days_in(d.year, d.month) || !(0..60 * SCALE).contains(&sec) {
        return None;
    }
    let mut m = Moment { k, days: days_from_civil(d.year, d.month, d.day), secs: (d.hour as i128 * 3600 + d.minute as i128 * 60) * SCALE + sec, tz: d.offset_minutes };
    // 24:00:00 is the start of the next day.
    m.days += m.secs.div_euclid(DAY) as i64;
    m.secs = m.secs.rem_euclid(DAY);
    Some(m)
}

fn duration(k: K, s: &[u8]) -> Option<Dur> {
    let (neg, s) = match s.strip_prefix(b"-") {
        Some(r) => (true, r),
        None => (false, s),
    };
    let mut s = s.strip_prefix(b"P")?;
    let (mut months, mut secs, mut time, mut last, mut any) = (0i128, 0i128, false, 0u8, false);
    while !s.is_empty() {
        if s[0] == b'T' {
            if time || s.len() == 1 {
                return None;
            }
            time = true;
            s = &s[1..];
            continue;
        }
        let n = s.iter().position(|c| !(c.is_ascii_digit() || *c == b'.'))?;
        let (digits, rest) = s.split_at(n);
        s = &rest[1..];
        let rank = match (time, rest[0]) {
            (false, b'Y') => 1,
            (false, b'M') => 2,
            (false, b'D') => 3,
            (true, b'H') => 4,
            (true, b'M') => 5,
            (true, b'S') => 6,
            _ => return None,
        };
        if n == 0 || rank <= last || (rank != 6 && digits.contains(&b'.')) {
            return None;
        }
        last = rank;
        any = true;
        let whole = || num::parse_int(digits).filter(|&v| v < 1 << 60);
        match rank {
            1 => months += whole()? * 12,
            2 => months += whole()?,
            3 => secs += whole()? * DAY,
            4 => secs += whole()? * 3600 * SCALE,
            5 => secs += whole()? * 60 * SCALE,
            _ => secs += micro(num::parse_dec(digits)?),
        }
    }
    if !any || (k == K::YearMonth && secs != 0) || (k == K::DayTime && months != 0) {
        return None;
    }
    let months = i64::try_from(months).ok()?;
    Some(if neg { Dur { k, months: -months, secs: -secs } } else { Dur { k, months, secs } })
}

fn push2(out: &mut Vec<u8>, n: i128) {
    out.push(b'0' + (n / 10) as u8);
    out.push(b'0' + (n % 10) as u8);
}

fn write_moment(m: &Moment) -> Vec<u8> {
    let mut out = Vec::new();
    if m.k != K::Time {
        let (y, mo, d) = civil_from_days(m.days);
        if y < 0 {
            out.push(b'-');
        }
        let mut year = Vec::new();
        num::write_int(&mut year, y.unsigned_abs() as i128);
        for _ in year.len()..4 {
            out.push(b'0');
        }
        out.extend_from_slice(&year);
        out.push(b'-');
        push2(&mut out, mo as i128);
        out.push(b'-');
        push2(&mut out, d as i128);
    }
    if m.k == K::DateTime {
        out.push(b'T');
    }
    if m.k != K::Date {
        let whole = m.secs / SCALE;
        push2(&mut out, whole / 3600);
        out.push(b':');
        push2(&mut out, whole / 60 % 60);
        out.push(b':');
        push2(&mut out, whole % 60);
        let mut frac = Vec::new();
        num::write_dec(&mut frac, m.secs % SCALE);
        if let Some(dot) = frac.iter().position(|&c| c == b'.') {
            out.extend_from_slice(&frac[dot..]);
        }
    }
    match m.tz {
        None => {}
        Some(0) => out.push(b'Z'),
        Some(t) => {
            out.push(if t < 0 { b'-' } else { b'+' });
            push2(&mut out, (t.abs() / 60) as i128);
            out.push(b':');
            push2(&mut out, (t.abs() % 60) as i128);
        }
    }
    out
}

fn write_dur(d: &Dur) -> Vec<u8> {
    let mut out = Vec::new();
    if d.months < 0 || d.secs < 0 {
        out.push(b'-');
    }
    out.push(b'P');
    let (months, secs) = (d.months.unsigned_abs() as i128, d.secs.abs());
    let part = |out: &mut Vec<u8>, n: i128, c: u8| {
        if n > 0 {
            num::write_int(out, n);
            out.push(c);
        }
    };
    part(&mut out, months / 12, b'Y');
    part(&mut out, months % 12, b'M');
    part(&mut out, secs / DAY, b'D');
    let rest = secs % DAY;
    if rest > 0 {
        out.push(b'T');
        part(&mut out, rest / (3600 * SCALE), b'H');
        part(&mut out, rest / (60 * SCALE) % 60, b'M');
        let s = rest % (60 * SCALE);
        if s > 0 {
            num::write_dec(&mut out, s);
            out.push(b'S');
        }
    }
    if out.ends_with(b"P") {
        out.extend_from_slice(if d.k == K::YearMonth { b"0M" } else { b"T0S" });
    }
    out
}

/// The instant, as scaled seconds since the epoch in UTC.
fn utc(m: &Moment) -> i128 {
    m.days as i128 * DAY + m.secs - m.tz.unwrap_or(0) as i128 * 60 * SCALE
}

/// `m` moved by `sign` times `d`: months first (the day clamped to the month's
/// last), then seconds. A date keeps only its date; a time wraps around midnight.
fn shift(mut m: Moment, d: Dur, sign: i64) -> Option<Moment> {
    let (months, secs) = (d.months.checked_mul(sign)?, d.secs * sign as i128);
    if months != 0 {
        if m.k == K::Time {
            return None;
        }
        let (y, mo, day) = civil_from_days(m.days);
        let total = y.checked_mul(12)?.checked_add(mo as i64 - 1)?.checked_add(months)?;
        let (ny, nm) = (total.div_euclid(12), (total.rem_euclid(12) + 1) as u32);
        m.days = days_from_civil(ny, nm, day.min(days_in(ny, nm)));
    }
    let t = m.secs + secs;
    if m.k != K::Time {
        m.days = m.days.checked_add(i64::try_from(t.div_euclid(DAY)).ok()?)?;
    }
    m.secs = if m.k == K::Date { 0 } else { t.rem_euclid(DAY) };
    Some(m)
}

fn combine(x: Dur, y: Dur, sub: bool) -> Option<Dur> {
    let sign = if sub { -1 } else { 1 };
    let (months, secs) = (x.months.checked_add(y.months.checked_mul(sign)?)?, x.secs + y.secs * sign as i128);
    if (months > 0 && secs < 0) || (months < 0 && secs > 0) {
        return None;
    }
    let k = if x.k == y.k { x.k } else { K::Duration };
    Some(Dur { k, months, secs })
}

/// Round half to even, for finite values.
fn round_even(x: f64) -> f64 {
    let t = x as i64 as f64;
    let f = if t > x { t - 1.0 } else { t };
    let d = x - f;
    if d > 0.5 || (d == 0.5 && f % 2.0 != 0.0) { f + 1.0 } else { f }
}

/// A duration multiplied or divided by a number, in doubles on microseconds as
/// the WAT build does: a dayTimeDuration rounds half to even, a
/// yearMonthDuration half up (as fn:round).
fn scale(d: Dur, n: Num, div: bool) -> Option<Dur> {
    let x = n.as_f64();
    if !x.is_finite() || (div && x == 0.0) || d.k == K::Duration {
        return None;
    }
    let base = if d.k == K::DayTime { (d.secs / MICRO) as f64 } else { d.months as f64 };
    let r = if div { base / x } else { base * x };
    let r = if d.k == K::DayTime { round_even(r) } else { let h = r + 0.5; let t = h as i64 as f64; if t > h { t - 1.0 } else { t } };
    if !(r.abs() < 9e18) {
        return None;
    }
    Some(if d.k == K::DayTime { Dur { k: d.k, months: 0, secs: r as i64 as i128 * MICRO } } else { Dur { k: d.k, months: r as i64, secs: 0 } })
}

/// Order or equality of two durations, or of two times.
fn cmp(x: &T, y: &T, eq: bool) -> Option<Ordering> {
    match (x, y) {
        (T::M(a), T::M(b)) if a.k == K::Time && b.k == K::Time => Some(utc(a).cmp(&utc(b))),
        (T::D(a), T::D(b)) if eq => Some(if (a.months, a.secs) == (b.months, b.secs) { Ordering::Equal } else { Ordering::Less }),
        (T::D(a), T::D(b)) if a.k == b.k && a.k == K::DayTime => Some(a.secs.cmp(&b.secs)),
        (T::D(a), T::D(b)) if a.k == b.k && a.k == K::YearMonth => Some(a.months.cmp(&b.months)),
        _ => None,
    }
}

impl Ctx<'_> {
    fn temporal_lit(&mut self, l: &Lit) -> Option<T> {
        let k = kind_of(self.dict.value(l.dt).strip_prefix(XSD)?)?;
        match k {
            K::DateTime | K::Date | K::Time => moment(k, &l.value).map(T::M),
            _ => duration(k, &l.value).map(T::D),
        }
    }

    fn temporal(&mut self, v: &Val) -> Option<T> {
        let l = self.lit(v)?;
        self.temporal_lit(&l)
    }

    fn temporal_val(&mut self, t: T) -> Val {
        let (s, k) = match t {
            T::M(m) => (write_moment(&m), m.k),
            T::D(d) => (write_dur(&d), d.k),
        };
        Val::Typed(s, self.xsd(local(k)))
    }

    /// `+`, `-`, `*`, `/` where an operand is a date, time or duration.
    pub fn temporal_arith(&mut self, op: Op, a: &Val, b: &Val) -> R<Val> {
        let (ta, tb) = (self.temporal(a), self.temporal(b));
        let out = match (op, ta, tb) {
            (Op::Add, Some(T::M(m)), Some(T::D(d))) | (Op::Add, Some(T::D(d)), Some(T::M(m))) => shift(m, d, 1).map(T::M),
            (Op::Sub, Some(T::M(m)), Some(T::D(d))) => shift(m, d, -1).map(T::M),
            (Op::Sub, Some(T::M(x)), Some(T::M(y))) if x.k == y.k => Some(T::D(Dur { k: K::DayTime, months: 0, secs: utc(&x) - utc(&y) })),
            (Op::Add | Op::Sub, Some(T::D(x)), Some(T::D(y))) => combine(x, y, op == Op::Sub).map(T::D),
            (Op::Mul | Op::Div, Some(T::D(d)), None) => self.num(b).and_then(|n| scale(d, n, op == Op::Div)).map(T::D),
            (Op::Mul, None, Some(T::D(d))) => self.num(a).and_then(|n| scale(d, n, false)).map(T::D),
            (Op::Div, Some(T::D(x)), Some(T::D(y))) if x.k == y.k && x.k != K::Duration => {
                let (p, q) = if x.k == K::DayTime { (Num::Dec(x.secs), Num::Dec(y.secs)) } else { (Num::Int(x.months as i128), Num::Int(y.months as i128)) };
                if q.is_zero() {
                    return Err(());
                }
                return num::arith(Op::Div, p, q).map(Val::Num).ok_or(());
            }
            _ => None,
        };
        let out = out.ok_or(())?;
        Ok(self.temporal_val(out))
    }

    /// A cast to xsd:dateTime, date, time or a duration (None for other
    /// targets), by XPath's casting rules: from a string by its lexical form,
    /// a dateTime to its date or time, a date to its midnight, a duration to
    /// another kind by dropping the part that kind lacks. The result is in
    /// canonical form ("…00.000Z" is "…00Z").
    pub fn temporal_cast(&mut self, target: &[u8], l: &Lit, from_string: bool) -> Option<R<Val>> {
        let k = kind_of(target)?;
        let moment_target = matches!(k, K::DateTime | K::Date | K::Time);
        let t = if from_string {
            let s = l.value.trim_ascii();
            if moment_target { moment(k, s).map(T::M) } else { duration(k, s).map(T::D) }
        } else {
            match self.temporal_lit(l) {
                Some(T::M(m)) if moment_target => match (m.k, k) {
                    (a, b) if a == b => Some(T::M(m)),
                    (K::DateTime, K::Date) => Some(T::M(Moment { k, secs: 0, ..m })),
                    (K::DateTime, K::Time) => Some(T::M(Moment { k, ..m })),
                    (K::Date, K::DateTime) => Some(T::M(Moment { k, ..m })),
                    _ => None,
                },
                Some(T::D(d)) if !moment_target => match k {
                    K::DayTime => Some(T::D(Dur { k, months: 0, secs: d.secs })),
                    K::YearMonth => Some(T::D(Dur { k, months: d.months, secs: 0 })),
                    _ => Some(T::D(Dur { k, ..d })),
                },
                _ => None,
            }
        };
        Some(match t {
            Some(t) => Ok(self.temporal_val(t)),
            None => Err(()),
        })
    }

    /// Unary minus on a duration.
    pub fn temporal_neg(&mut self, v: &Val) -> R<Val> {
        match self.temporal(v) {
            Some(T::D(d)) => Ok(self.temporal_val(T::D(Dur { k: d.k, months: -d.months, secs: -d.secs }))),
            _ => Err(()),
        }
    }

    /// Comparison of two times or two durations (literals of no other kind the
    /// operators know); None when these are not such a pair.
    pub fn temporal_cmp(&mut self, x: &Lit, y: &Lit, eq: bool) -> Option<R<Ordering>> {
        let (a, b) = (self.temporal_lit(x)?, self.temporal_lit(y)?);
        Some(cmp(&a, &b, eq).ok_or(()))
    }
}
