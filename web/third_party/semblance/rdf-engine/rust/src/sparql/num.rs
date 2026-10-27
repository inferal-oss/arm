//! Numeric values: xsd:integer as i128, xsd:decimal as i128 scaled by 10^18,
//! float and double; parsing lexical forms and writing computed values.

use alloc::vec::Vec;
use core::fmt::Write;

pub const SCALE: i128 = 1_000_000_000_000_000_000;

#[derive(Clone, Copy, PartialEq, Debug)]
pub enum Num {
    Int(i128),
    Dec(i128),
    Float(f32),
    Double(f64),
}

/// The numeric type rank, for promotion: integer < decimal < float < double.
fn rank(n: &Num) -> u8 {
    match n {
        Num::Int(_) => 0,
        Num::Dec(_) => 1,
        Num::Float(_) => 2,
        Num::Double(_) => 3,
    }
}

impl Num {
    pub fn as_f64(self) -> f64 {
        match self {
            Num::Int(i) => i as f64,
            Num::Dec(d) => d as f64 / SCALE as f64,
            Num::Float(f) => f as f64,
            Num::Double(d) => d,
        }
    }

    fn as_dec(self) -> Option<i128> {
        match self {
            Num::Int(i) => i.checked_mul(SCALE),
            Num::Dec(d) => Some(d),
            _ => None,
        }
    }

    /// Promote both to their common type.
    pub fn promote(a: Num, b: Num) -> (Num, Num) {
        let r = rank(&a).max(rank(&b));
        (a.to_rank(r), b.to_rank(r))
    }

    fn to_rank(self, r: u8) -> Num {
        match r {
            0 => self,
            1 => self.as_dec().map_or(Num::Double(self.as_f64()), Num::Dec),
            2 => Num::Float(self.as_f64() as f32),
            _ => Num::Double(self.as_f64()),
        }
    }

    pub fn is_zero(self) -> bool {
        match self {
            Num::Int(i) => i == 0,
            Num::Dec(d) => d == 0,
            Num::Float(f) => f == 0.0,
            Num::Double(d) => d == 0.0,
        }
    }

    pub fn is_nan(self) -> bool {
        match self {
            Num::Float(f) => f.is_nan(),
            Num::Double(d) => d.is_nan(),
            _ => false,
        }
    }
}

fn digits(s: &[u8]) -> bool {
    !s.is_empty() && s.iter().all(u8::is_ascii_digit)
}

pub fn parse_int(s: &[u8]) -> Option<i128> {
    let (neg, d) = match s.first() {
        Some(b'-') => (true, &s[1..]),
        Some(b'+') => (false, &s[1..]),
        _ => (false, s),
    };
    if !digits(d) {
        return None;
    }
    let mut n: i128 = 0;
    for &c in d {
        n = n.checked_mul(10)?.checked_add((c - b'0') as i128)?;
    }
    Some(if neg { -n } else { n })
}

pub fn parse_dec(s: &[u8]) -> Option<i128> {
    let (neg, d) = match s.first() {
        Some(b'-') => (true, &s[1..]),
        Some(b'+') => (false, &s[1..]),
        _ => (false, s),
    };
    let (int, frac) = match d.iter().position(|&c| c == b'.') {
        Some(i) => (&d[..i], &d[i + 1..]),
        None => (d, &b""[..]),
    };
    if int.is_empty() && frac.is_empty() || !int.iter().chain(frac).all(u8::is_ascii_digit) {
        return None;
    }
    let mut n: i128 = 0;
    for &c in int {
        n = n.checked_mul(10)?.checked_add((c - b'0') as i128)?;
    }
    n = n.checked_mul(SCALE)?;
    let mut place = SCALE / 10;
    for &c in frac {
        if place == 0 {
            break;
        }
        n += (c - b'0') as i128 * place;
        place /= 10;
    }
    Some(if neg { -n } else { n })
}

pub fn parse_double(s: &[u8]) -> Option<f64> {
    match s {
        b"INF" | b"+INF" => return Some(f64::INFINITY),
        b"-INF" => return Some(f64::NEG_INFINITY),
        b"NaN" => return Some(f64::NAN),
        _ => {}
    }
    // XSD's lexical space: digits, one optional point, optional exponent.
    let body = match s.first() {
        Some(b'+' | b'-') => &s[1..],
        _ => s,
    };
    let (mant, exp) = match body.iter().position(|&c| c == b'e' || c == b'E') {
        Some(i) => (&body[..i], Some(&body[i + 1..])),
        None => (body, None),
    };
    let points = mant.iter().filter(|&&c| c == b'.').count();
    if mant.is_empty() || points > 1 || !mant.iter().any(u8::is_ascii_digit) || !mant.iter().all(|&c| c.is_ascii_digit() || c == b'.') {
        return None;
    }
    if let Some(e) = exp {
        let e = match e.first() {
            Some(b'+' | b'-') => &e[1..],
            _ => e,
        };
        if !digits(e) {
            return None;
        }
    }
    // Decimal digits to a mantissa (up to 19 significant digits) and a power of ten.
    let neg = s.first() == Some(&b'-');
    let (mut m, mut e10, mut sig, mut point) = (0u64, 0i32, 0u32, false);
    for &c in mant {
        if c == b'.' {
            point = true;
            continue;
        }
        if sig < 19 {
            m = m * 10 + (c - b'0') as u64;
            if m > 0 {
                sig += 1;
            }
            if point {
                e10 -= 1;
            }
        } else if !point {
            e10 += 1;
        }
    }
    if let Some(e) = exp {
        let (en, digits) = match e.first() {
            Some(b'-') => (true, &e[1..]),
            Some(b'+') => (false, &e[1..]),
            _ => (false, e),
        };
        let mut x: i32 = 0;
        for &d in digits {
            x = x.saturating_mul(10).saturating_add((d - b'0') as i32);
        }
        e10 = e10.saturating_add(if en { -x } else { x });
    }
    Some(if neg { -scale10(m, e10) } else { scale10(m, e10) })
}

const POW10: [f64; 23] = [1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22];

/// m * 10^e: exact when m < 2^53 and |e| <= 22 (both factors exact, one
/// rounding); otherwise by repeated scaling, within a few ulps.
fn scale10(m: u64, e: i32) -> f64 {
    let mut v = m as f64;
    if m == 0 {
        return 0.0;
    }
    if e >= 0 {
        let mut e = e;
        while e > 22 {
            v *= 1e22;
            e -= 22;
            if v.is_infinite() {
                return v;
            }
        }
        v * POW10[e as usize]
    } else {
        let mut e = -e;
        while e > 22 {
            v /= 1e22;
            e -= 22;
            if v == 0.0 {
                return v;
            }
        }
        v / POW10[e as usize]
    }
}

pub fn write_int(out: &mut Vec<u8>, n: i128) {
    let mut buf = [0u8; 40];
    let mut i = buf.len();
    let neg = n < 0;
    let mut m = n.unsigned_abs();
    loop {
        i -= 1;
        buf[i] = b'0' + (m % 10) as u8;
        m /= 10;
        if m == 0 {
            break;
        }
    }
    if neg {
        out.push(b'-');
    }
    out.extend_from_slice(&buf[i..]);
}

/// xsd:decimal as XSD 1.1 writes it: no point when integral ("2"), else "2.5".
pub fn write_dec(out: &mut Vec<u8>, d: i128) {
    if d < 0 {
        out.push(b'-');
    }
    let m = d.unsigned_abs();
    write_int(out, (m / SCALE as u128) as i128);
    let mut frac = m % SCALE as u128;
    if frac == 0 {
        return;
    }
    out.push(b'.');
    let mut place = SCALE as u128 / 10;
    while frac > 0 {
        out.push(b'0' + (frac / place) as u8);
        frac %= place;
        place /= 10;
    }
}

struct Buf<'a>(&'a mut Vec<u8>);

impl Write for Buf<'_> {
    fn write_str(&mut self, s: &str) -> core::fmt::Result {
        self.0.extend_from_slice(s.as_bytes());
        Ok(())
    }
}

/// xsd:double / xsd:float as Oxigraph writes them, and as the WAT build does:
/// the shortest digits that read back, in plain notation ("2.5", "0",
/// "10000000", "0.0000001", "-0"), or INF, -INF, NaN. (XPath would write
/// "1.0E7"; Semblance's output follows Oxigraph.)
pub fn write_double(out: &mut Vec<u8>, d: f64, float: bool) {
    if d.is_nan() {
        out.extend_from_slice(b"NaN");
        return;
    }
    if d.is_infinite() {
        out.extend_from_slice(if d > 0.0 { b"INF" } else { b"-INF" });
        return;
    }
    if float {
        let _ = write!(Buf(out), "{}", d as f32);
    } else {
        let _ = write!(Buf(out), "{}", d);
    }
}

pub fn write_num(out: &mut Vec<u8>, n: Num) {
    match n {
        Num::Int(i) => write_int(out, i),
        Num::Dec(d) => write_dec(out, d),
        Num::Float(f) => write_double(out, f as f64, true),
        Num::Double(d) => write_double(out, d, false),
    }
}

pub fn arith(op: super::Op, a: Num, b: Num) -> Option<Num> {
    use super::Op::*;
    let (a, b) = Num::promote(a, b);
    Some(match (a, b) {
        (Num::Int(x), Num::Int(y)) => match op {
            Add => Num::Int(x.checked_add(y)?),
            Sub => Num::Int(x.checked_sub(y)?),
            Mul => Num::Int(x.checked_mul(y)?),
            // integer / integer is a decimal
            Div => return arith(Div, Num::Dec(x.checked_mul(SCALE)?), Num::Dec(y.checked_mul(SCALE)?)),
        },
        (Num::Dec(x), Num::Dec(y)) => match op {
            Add => Num::Dec(x.checked_add(y)?),
            Sub => Num::Dec(x.checked_sub(y)?),
            Mul => Num::Dec(mul_dec(x, y)?),
            Div => {
                if y == 0 {
                    return None;
                }
                Num::Dec(div_dec(x, y)?)
            }
        },
        (Num::Float(x), Num::Float(y)) => Num::Float(match op {
            Add => x + y,
            Sub => x - y,
            Mul => x * y,
            Div => x / y,
        }),
        (x, y) => {
            let (x, y) = (x.as_f64(), y.as_f64());
            Num::Double(match op {
                Add => x + y,
                Sub => x - y,
                Mul => x * y,
                Div => x / y,
            })
        }
    })
}

fn mul_dec(x: i128, y: i128) -> Option<i128> {
    // (x * y) / SCALE without overflowing: split y into whole and fraction.
    let (yi, yf) = (y / SCALE, y % SCALE);
    x.checked_mul(yi)?.checked_add(mul_div(x, yf, SCALE)?)
}

fn div_dec(x: i128, y: i128) -> Option<i128> {
    mul_div(x, SCALE, y)
}

/// x * m / d with a 256-bit intermediate (long multiplication in u128 halves).
fn mul_div(x: i128, m: i128, d: i128) -> Option<i128> {
    let neg = (x < 0) ^ (m < 0) ^ (d < 0);
    let (x, m, d) = (x.unsigned_abs(), m.unsigned_abs(), d.unsigned_abs());
    // 128x128 -> 256 bit product as (hi, lo)
    let (x1, x0) = (x >> 64, x & u64::MAX as u128);
    let (m1, m0) = (m >> 64, m & u64::MAX as u128);
    let p00 = x0 * m0;
    let p01 = x0 * m1;
    let p10 = x1 * m0;
    let p11 = x1 * m1;
    let mid = (p00 >> 64) + (p01 & u64::MAX as u128) + (p10 & u64::MAX as u128);
    let lo = (p00 & u64::MAX as u128) | (mid << 64);
    let hi = p11 + (p01 >> 64) + (p10 >> 64) + (mid >> 64);
    if hi >= d {
        return None;
    }
    // Long division of (hi, lo) by d, bit by bit.
    let (mut r, mut q) = (hi, 0u128);
    for i in (0..128).rev() {
        let carry = r >> 127;
        r = r << 1 | (lo >> i) & 1;
        q <<= 1;
        if carry == 1 || r >= d {
            r = r.wrapping_sub(d);
            q |= 1;
        }
    }
    let q = i128::try_from(q).ok()?;
    Some(if neg { -q } else { q })
}

/// Round half towards positive infinity (fn:round).
pub fn round(n: Num) -> Num {
    match n {
        Num::Int(_) => n,
        Num::Dec(d) => Num::Dec(floor_dec(d + SCALE / 2)),
        Num::Float(f) => Num::Float(libm_floor(f as f64 + 0.5) as f32),
        Num::Double(d) => Num::Double(if d.is_finite() { libm_floor(d + 0.5) } else { d }),
    }
}

fn floor_dec(d: i128) -> i128 {
    d.div_euclid(SCALE) * SCALE
}

pub fn floor(n: Num) -> Num {
    match n {
        Num::Int(_) => n,
        Num::Dec(d) => Num::Dec(floor_dec(d)),
        Num::Float(f) => Num::Float(libm_floor(f as f64) as f32),
        Num::Double(d) => Num::Double(libm_floor(d)),
    }
}

pub fn ceil(n: Num) -> Num {
    match n {
        Num::Int(_) => n,
        Num::Dec(d) => Num::Dec(-floor_dec(-d)),
        Num::Float(f) => Num::Float(-libm_floor(-(f as f64)) as f32),
        Num::Double(d) => Num::Double(-libm_floor(-d)),
    }
}

pub fn abs(n: Num) -> Num {
    match n {
        Num::Int(i) => Num::Int(i.abs()),
        Num::Dec(d) => Num::Dec(d.abs()),
        Num::Float(f) => Num::Float(if f < 0.0 { -f } else { f }),
        Num::Double(d) => Num::Double(if d < 0.0 { -d } else { d }),
    }
}

/// floor without std: via truncation, for finite values.
fn libm_floor(x: f64) -> f64 {
    if !x.is_finite() || x.abs() >= 4_503_599_627_370_496.0 {
        return x;
    }
    let t = x as i64 as f64;
    if t > x { t - 1.0 } else { t }
}

pub fn cmp(a: Num, b: Num) -> Option<core::cmp::Ordering> {
    let (a, b) = Num::promote(a, b);
    match (a, b) {
        (Num::Int(x), Num::Int(y)) | (Num::Dec(x), Num::Dec(y)) => Some(x.cmp(&y)),
        (x, y) => x.as_f64().partial_cmp(&y.as_f64()),
    }
}
