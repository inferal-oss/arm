//! Quads in one dense table, with a list per term and position (s, p, o, g)
//! of the quads using it there, and a hash set over the table for add,
//! remove and has. A pattern scans the shortest list among its bound
//! positions and filters. Removal is O(1): the quad leaves its four lists by
//! swapping in their last entry, and the table by moving its last quad in.

use crate::hash;
use alloc::vec::Vec;

pub type Quad = [u32; 4];

#[derive(Default)]
pub struct Store {
    quads: Vec<Quad>,
    /// Each quad's position in its four lists.
    at: Vec<[u32; 4]>,
    /// By term id: the quads using it as subject, predicate, object, graph.
    lists: Vec<[Vec<u32>; 4]>,
    /// Quad index + 1 in the low half, its hash in the high half (0 = empty);
    /// linear probing, at most half full.
    table: Vec<u64>,
    /// The graphs that hold a quad, ascending.
    graphs: Vec<u32>,
}

fn hash_of(q: &Quad) -> u32 {
    hash::top32(q.iter().fold(0, |h, &w| hash::mix(h, w as u64)))
}

fn entry(i: usize, h: u32) -> u64 {
    (h as u64) << 32 | (i as u64 + 1)
}

impl Store {
    /// The table slot holding `q`, or the empty slot where it would go.
    fn slot(&self, q: &Quad) -> usize {
        let mask = self.table.len() - 1;
        let h = hash_of(q);
        let mut i = h as usize & mask;
        loop {
            match self.table[i] {
                0 => return i,
                e if (e >> 32) as u32 == h && self.quads[e as u32 as usize - 1] == *q => return i,
                _ => i = (i + 1) & mask,
            }
        }
    }

    fn list(&self, id: u32, k: usize) -> &[u32] {
        self.lists.get(id as usize).map_or(&[], |l| &l[k])
    }

    pub fn add(&mut self, q: Quad) -> bool {
        if self.table.len() < 2 * (self.quads.len() + 1) {
            let n = (self.table.len() * 2).max(64);
            let old = core::mem::replace(&mut self.table, alloc::vec![0; n]);
            for e in old.into_iter().filter(|&e| e != 0) {
                let mut j = (e >> 32) as usize & (n - 1);
                while self.table[j] != 0 {
                    j = (j + 1) & (n - 1);
                }
                self.table[j] = e;
            }
        }
        let slot = self.slot(&q);
        if self.table[slot] != 0 {
            return false;
        }
        let i = self.quads.len() as u32;
        self.table[slot] = entry(i as usize, hash_of(&q));
        let top = *q.iter().max().unwrap() as usize;
        if self.lists.len() <= top {
            self.lists.resize_with(top + 1, Default::default);
        }
        let mut at = [0; 4];
        for k in 0..4 {
            let l = &mut self.lists[q[k] as usize][k];
            at[k] = l.len() as u32;
            l.push(i);
        }
        if at[3] == 0 {
            let p = self.graphs.partition_point(|&g| g < q[3]);
            self.graphs.insert(p, q[3]);
        }
        self.quads.push(q);
        self.at.push(at);
        true
    }

    pub fn remove(&mut self, q: Quad) -> bool {
        if self.table.is_empty() {
            return false;
        }
        let mut hole = self.slot(&q);
        if self.table[hole] == 0 {
            return false;
        }
        let i = self.table[hole] as u32 as usize - 1;
        // Backward-shift deletion: pull later entries of the run into the hole.
        let mask = self.table.len() - 1;
        let mut j = hole;
        loop {
            j = (j + 1) & mask;
            let e = self.table[j];
            if e == 0 {
                break;
            }
            let home = (e >> 32) as usize & mask;
            if (j.wrapping_sub(home) & mask) >= (j.wrapping_sub(hole) & mask) {
                self.table[hole] = e;
                hole = j;
            }
        }
        self.table[hole] = 0;
        // Out of its four lists.
        let at = self.at[i];
        for k in 0..4 {
            let l = &mut self.lists[q[k] as usize][k];
            let pos = at[k] as usize;
            l.swap_remove(pos);
            if let Some(&moved) = l.get(pos) {
                self.at[moved as usize][k] = pos as u32;
            }
            if k == 3 && l.is_empty() {
                let p = self.graphs.partition_point(|&g| g < q[3]);
                self.graphs.remove(p);
            }
        }
        // The last quad moves into its place.
        let last = self.quads.len() - 1;
        if i != last {
            let m = self.quads[last];
            let s = self.slot(&m);
            self.table[s] = entry(i, (self.table[s] >> 32) as u32);
            for k in 0..4 {
                self.lists[m[k] as usize][k][self.at[last][k] as usize] = i as u32;
            }
        }
        self.quads.swap_remove(i);
        self.at.swap_remove(i);
        true
    }

    pub fn has(&self, q: Quad) -> bool {
        !self.table.is_empty() && self.table[self.slot(&q)] != 0
    }

    pub fn len(&self) -> usize {
        self.quads.len()
    }

    pub fn each(&self, mut f: impl FnMut(&Quad)) {
        self.quads.iter().for_each(|q| f(q));
    }

    /// Every graph that holds a quad.
    pub fn graphs(&self) -> Vec<u32> {
        self.graphs.clone()
    }

    /// Append every quad matching the pattern (0 = any) to `out`, 4 ids each.
    pub fn scan(&self, pat: Quad, out: &mut Vec<u32>) {
        if pat.iter().all(|&x| x != 0) {
            if self.has(pat) {
                out.extend_from_slice(&pat);
            }
            return;
        }
        let mut best: Option<&[u32]> = None;
        for k in 0..4 {
            if pat[k] != 0 {
                let l = self.list(pat[k], k);
                if best.is_none_or(|b| l.len() < b.len()) {
                    best = Some(l);
                }
            }
        }
        let fits = |q: &Quad| pat.iter().zip(q).all(|(&x, &y)| x == 0 || x == y);
        match best {
            Some(l) => {
                for &i in l {
                    let q = &self.quads[i as usize];
                    if fits(q) {
                        out.extend_from_slice(q);
                    }
                }
            }
            None => self.quads.iter().for_each(|q| out.extend_from_slice(q)),
        }
    }
}
