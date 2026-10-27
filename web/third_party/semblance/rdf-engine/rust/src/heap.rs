//! A power-of-two size-class allocator over linear memory: one free list per
//! class, a bump pointer for fresh blocks, `memory.grow` when it runs out.
//! Freed blocks are reused for the same class only; memory is never returned.
//! The block at the top of the bump region grows and shrinks in place, and
//! memory grows by at least a quarter at a time, so a growing vector neither
//! copies nor calls `memory.grow` on every doubling.

use core::alloc::{GlobalAlloc, Layout};
use core::arch::wasm32;
use core::cell::UnsafeCell;
use core::ptr::{copy_nonoverlapping, null_mut};

const PAGE: usize = 65536;

struct State {
    free: [usize; 32],
    next: usize,
    end: usize,
}

pub struct Heap(UnsafeCell<State>);

// Wasm without threads: there is one caller at a time.
unsafe impl Sync for Heap {}

unsafe extern "C" {
    static __heap_base: u8;
}

/// The class of a block: the smallest `c` with `1 << c >= size` (at least 8 bytes).
fn class(size: usize, align: usize) -> usize {
    let size = size.max(align).max(8);
    (usize::BITS - (size - 1).leading_zeros()) as usize
}

impl Heap {
    pub const fn new() -> Heap {
        Heap(UnsafeCell::new(State { free: [0; 32], next: 0, end: 0 }))
    }
}

impl State {
    /// Move the bump pointer to `stop`, growing memory if needed.
    fn bump(&mut self, stop: usize) -> bool {
        if stop > self.end {
            let pages = (stop - self.end).div_ceil(PAGE).max(self.end / PAGE / 4).max(16);
            if wasm32::memory_grow(0, pages) == usize::MAX {
                let pages = (stop - self.end).div_ceil(PAGE);
                if wasm32::memory_grow(0, pages) == usize::MAX {
                    return false;
                }
                self.end += pages * PAGE;
            } else {
                self.end += pages * PAGE;
            }
        }
        self.next = stop;
        true
    }
}

unsafe impl GlobalAlloc for Heap {
    unsafe fn alloc(&self, l: Layout) -> *mut u8 {
        let h = unsafe { &mut *self.0.get() };
        let c = class(l.size(), l.align());
        if c >= 32 {
            return null_mut();
        }
        let head = h.free[c];
        // Free blocks are only known to be 8-aligned.
        if head != 0 && l.align() <= 8 {
            h.free[c] = unsafe { *(head as *const usize) };
            return head as *mut u8;
        }
        if h.end == 0 {
            h.next = &raw const __heap_base as usize;
            h.end = wasm32::memory_size(0) * PAGE;
        }
        let align = l.align().max(8);
        let start = (h.next + align - 1) & !(align - 1);
        if !h.bump(start + (1 << c)) {
            return null_mut();
        }
        start as *mut u8
    }

    unsafe fn dealloc(&self, p: *mut u8, l: Layout) {
        let h = unsafe { &mut *self.0.get() };
        let c = class(l.size(), l.align());
        if p as usize + (1 << c) == h.next {
            h.next = p as usize;
            return;
        }
        unsafe { *(p as *mut usize) = h.free[c] };
        h.free[c] = p as usize;
    }

    unsafe fn realloc(&self, p: *mut u8, l: Layout, size: usize) -> *mut u8 {
        let h = unsafe { &mut *self.0.get() };
        let (from, to) = (class(l.size(), l.align()), class(size, l.align()));
        if from == to {
            return p;
        }
        if to < 32 && p as usize + (1 << from) == h.next {
            return if h.bump(p as usize + (1 << to)) { p } else { null_mut() };
        }
        let to = unsafe { Layout::from_size_align_unchecked(size, l.align()) };
        let n = unsafe { self.alloc(to) };
        if !n.is_null() {
            unsafe {
                copy_nonoverlapping(p, n, l.size().min(size));
                self.dealloc(p, l);
            }
        }
        n
    }
}
