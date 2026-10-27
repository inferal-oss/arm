//! A fast non-cryptographic hash (FxHash's multiply-rotate) over words.

const K: u64 = 0x517c_c1b7_2722_0a95;

#[inline]
pub fn mix(h: u64, w: u64) -> u64 {
    (h.rotate_left(5) ^ w).wrapping_mul(K)
}

pub fn bytes(mut h: u64, b: &[u8]) -> u64 {
    let mut chunks = b.chunks_exact(8);
    for c in &mut chunks {
        let mut w = [0u8; 8];
        w.copy_from_slice(c);
        h = mix(h, u64::from_le_bytes(w));
    }
    let r = chunks.remainder();
    if !r.is_empty() {
        let mut w = [0u8; 8];
        w[..r.len()].copy_from_slice(r);
        h = mix(h, u64::from_le_bytes(w));
    }
    mix(h, b.len() as u64)
}

/// The high bits, which are the well-mixed ones.
#[inline]
pub fn top32(h: u64) -> u32 {
    (h >> 32) as u32
}
