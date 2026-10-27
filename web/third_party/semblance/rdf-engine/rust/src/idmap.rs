//! One hash map for every lookup table the engine needs: keys are `u32`
//! slices (term ids, rows, or bytes packed four to a word), values are `u32`.
//! A single non-generic type, so it is compiled once.

use crate::hash;
use alloc::vec::Vec;

#[derive(Default)]
pub struct IdMap {
    /// Keys back to back.
    keys: Vec<u32>,
    /// (key offset, key length, hash, value) per entry, in insertion order.
    entries: Vec<[u32; 4]>,
    /// Entry index + 1 by hash; 0 is empty. Length is a power of two (or 0).
    table: Vec<u32>,
}

fn hash_of(k: &[u32]) -> u32 {
    let mut h = hash::mix(0, k.len() as u64);
    for &w in k {
        h = hash::mix(h, w as u64);
    }
    hash::top32(h)
}

/// Bytes as a key: the length, then the bytes packed four to a word.
pub fn bytes_key(b: &[u8]) -> Vec<u32> {
    let mut k = Vec::with_capacity(1 + b.len().div_ceil(4));
    k.push(b.len() as u32);
    for c in b.chunks(4) {
        let mut w = [0u8; 4];
        w[..c.len()].copy_from_slice(c);
        k.push(u32::from_le_bytes(w));
    }
    k
}

impl IdMap {
    pub const EMPTY: IdMap = IdMap { keys: Vec::new(), entries: Vec::new(), table: Vec::new() };

    pub fn new() -> IdMap {
        IdMap::default()
    }

    pub fn len(&self) -> usize {
        self.entries.len()
    }

    fn key(&self, e: &[u32; 4]) -> &[u32] {
        &self.keys[e[0] as usize..(e[0] + e[1]) as usize]
    }

    /// The table slot for `k`: its entry index + 1, or the empty slot.
    fn slot(&self, k: &[u32], h: u32) -> usize {
        let mask = self.table.len() - 1;
        let mut i = h as usize & mask;
        loop {
            let e = self.table[i];
            if e == 0 {
                return i;
            }
            let entry = &self.entries[e as usize - 1];
            if entry[2] == h && self.key(entry) == k {
                return i;
            }
            i = (i + 1) & mask;
        }
    }

    pub fn get(&self, k: &[u32]) -> Option<u32> {
        if self.table.is_empty() {
            return None;
        }
        match self.table[self.slot(k, hash_of(k))] {
            0 => None,
            e => Some(self.entries[e as usize - 1][3]),
        }
    }

    /// Insert unless present; returns the value now stored and whether it was new.
    pub fn insert(&mut self, k: &[u32], v: u32) -> (u32, bool) {
        if (self.entries.len() + 1) * 4 > self.table.len() * 3 {
            self.grow();
        }
        let h = hash_of(k);
        let i = self.slot(k, h);
        if self.table[i] != 0 {
            return (self.entries[self.table[i] as usize - 1][3], false);
        }
        self.entries.push([self.keys.len() as u32, k.len() as u32, h, v]);
        self.keys.extend_from_slice(k);
        self.table[i] = self.entries.len() as u32;
        (v, true)
    }

    /// Insert as a set member; true if it was new.
    pub fn add(&mut self, k: &[u32]) -> bool {
        self.insert(k, 0).1
    }

    pub fn set(&mut self, k: &[u32], v: u32) {
        let (_, new) = self.insert(k, v);
        if !new {
            let i = self.slot(k, hash_of(k));
            let e = self.table[i] as usize - 1;
            self.entries[e][3] = v;
        }
    }

    fn grow(&mut self) {
        let size = (self.table.len() * 2).max(16);
        self.table = alloc::vec![0; size];
        let mask = size - 1;
        for (n, e) in self.entries.iter().enumerate() {
            let mut i = e[2] as usize & mask;
            while self.table[i] != 0 {
                i = (i + 1) & mask;
            }
            self.table[i] = n as u32 + 1;
        }
    }
}
