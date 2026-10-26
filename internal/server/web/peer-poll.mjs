// Monotonic request ownership: a disconnected session cannot release a new
// session's pending slot, and the first view is immediately eligible to load.
export function createPeerPollGate(interval = 10000) {
  let owner = null, completedAt = null, pending = false;
  return {
    reset(session) { owner = session; completedAt = null; pending = false; },
    begin(session, now) {
      if (session !== owner || pending || (completedAt !== null && now - completedAt < interval)) return false;
      pending = true; return true;
    },
    finish(session, now) {
      if (session !== owner) return false;
      completedAt = now; pending = false; return true;
    },
  };
}
