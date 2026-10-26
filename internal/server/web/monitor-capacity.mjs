// The monitor shows physical availability. ARM's separate reservable budgets
// already account for live promises and must never be inferred from these data.
export function monitorStale(page, connected, elapsedMS) {
  if (!page || !connected || page.stale) return true;
  const age = Number(page.age_ns) / 1e6 + elapsedMS;
  const after = Number(page.stale_after_ns) / 1e6;
  return !Number.isFinite(age) || age < 0 || !Number.isFinite(after) || after <= 0 || age > after;
}

export function remainingValue(measurement, stale = false) {
  const value = measurement?.value;
  return !stale && typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}
