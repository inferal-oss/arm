//! The query budget (ABI.md, "Query budget"): a deadline on `env.now` and a
//! number of evaluation steps. A step is one row from a scan, one node of a
//! path walk or one pair a join tries. Once either runs out, `stop()` holds,
//! the evaluator's loops return early, and `run` discards what they produced.

use core::cell::Cell;

struct Budget {
    deadline: Cell<f64>,
    fuel: Cell<u32>,
    /// Steps left in this run.
    left: Cell<u64>,
    /// Steps taken; the clock is read every CLOCK_EVERY.
    ticks: Cell<u32>,
    stop: Cell<bool>,
    /// Between start() and finish(): only queries are budgeted, not updates.
    active: Cell<bool>,
    /// Steps to the next event (the fuel running out, or a look at the clock),
    /// counted down by step() and in line by compiled modules (cd_addr), which
    /// call event() when it reaches 0; as in WAT.
    cd: Cell<u32>,
    /// Steps from the last event to the next.
    chunk: Cell<u32>,
}

// Wasm without threads: there is one caller at a time.
unsafe impl Sync for Budget {}

static B: Budget = Budget {
    deadline: Cell::new(0.0),
    fuel: Cell::new(0),
    left: Cell::new(u64::MAX),
    ticks: Cell::new(0),
    stop: Cell::new(false),
    active: Cell::new(false),
    cd: Cell::new(0),
    chunk: Cell::new(0),
};

/// How many steps go between two looks at the clock.
const CLOCK_EVERY: u32 = 4096;

pub fn set_deadline(at_ms: f64) {
    B.deadline.set(at_ms);
}

pub fn set_fuel(steps: u32) {
    B.fuel.set(steps);
}

/// At the start of a run: the whole budget again.
pub fn start() {
    let f = B.fuel.get();
    B.left.set(if f == 0 { u64::MAX } else { f as u64 });
    B.ticks.set(0);
    B.stop.set(false);
    B.active.set(true);
    next();
}

fn next() {
    let mut n = CLOCK_EVERY - (B.ticks.get() & (CLOCK_EVERY - 1));
    if B.left.get() < n as u64 {
        n = B.left.get() as u32;
    }
    B.chunk.set(n);
    B.cd.set(n);
}

/// At the end of a run: whether it was stopped; later evaluation is not budgeted.
pub fn finish() -> bool {
    B.active.set(false);
    B.stop.get()
}

/// One step; true once the budget is spent.
#[inline]
pub fn step() -> bool {
    if !B.active.get() {
        return false;
    }
    let c = B.cd.get() - 1;
    B.cd.set(c);
    c == 0 && event()
}

/// The step that ends a chunk: the fuel used, the clock read on every
/// CLOCK_EVERY-th step. Once spent, every later step ends here too.
#[inline(never)]
pub fn event() -> bool {
    if !B.stop.get() {
        B.ticks.set(B.ticks.get().wrapping_add(B.chunk.get()));
        B.left.set(B.left.get() - B.chunk.get() as u64);
        if B.left.get() == 0 {
            B.stop.set(true);
        } else if B.ticks.get() % CLOCK_EVERY == 0 && B.deadline.get() > 0.0 && crate::host::now() >= B.deadline.get() {
            B.stop.set(true);
        }
        if !B.stop.get() {
            next();
            return false;
        }
    }
    B.cd.set(1);
    true
}

/// Where the countdown is, for compiled modules to step in line.
pub fn cd_addr() -> u32 {
    B.cd.as_ptr() as u32
}

pub fn stopped() -> bool {
    B.stop.get()
}
