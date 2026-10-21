// These are sums of reported counters, not exclusive physical throughput:
// layered disks and virtual interfaces can report overlapping traffic.
const idsOf = devices => [...new Set((devices ?? []).map(device => device?.id).filter(id => typeof id === 'string' && id.length > 0))].sort();
export const allDevices = () => ({mode:'all', ids:[]});
export const noDevices = () => ({mode:'custom', ids:[]});
export function selectedDeviceIDs(selection, devices) {
  return selection?.mode === 'custom' ? [...new Set((selection.ids ?? []).filter(id => typeof id === 'string' && id.length > 0))].sort() : idsOf(devices);
}
export function toggleDevice(selection, devices, id, checked) {
  const ids = new Set(selectedDeviceIDs(selection, devices));
  if (typeof id === 'string' && id) { if (checked) ids.add(id); else ids.delete(id); }
  // Even selecting every current ID is deliberate custom membership. Only an
  // explicit All action allows future devices to join the aggregate.
  return {mode:'custom', ids:[...ids].sort()};
}

function rate(device, direction) {
  const metric = device?.[`${direction}_bytes_per_second`];
  const value = Object.hasOwn(device ?? {}, `${direction}_bytes_per_second`) ? metric?.value : device?.[direction];
  if (value === null || value === undefined || typeof value !== 'number' || !Number.isFinite(value) || value < 0 || metric?.quality === 'unavailable') return null;
  return value;
}

// Fix the selected identity set for an entire rendered window. All mode also
// requires historical membership to match today's membership, so adding or
// removing a device produces a gap instead of an invented throughput change.
export function aggregateIO(devices, selectedIDs, {strictMembership=false}={}) {
  const selected = [...new Set(selectedIDs ?? [])], entries = new Map(), duplicates = new Set();
  for (const device of devices ?? []) {
    if (typeof device?.id !== 'string' || !device.id) continue;
    if (entries.has(device.id)) duplicates.add(device.id);
    entries.set(device.id, device);
  }
  const present = selected.filter(id => entries.has(id)).length;
  const matches = !strictMembership || entries.size === selected.length && selected.every(id => entries.has(id));
  const complete = selected.length > 0 && present === selected.length && selected.every(id => !duplicates.has(id)) && matches;
  function total(direction) {
    if (!complete) return null;
    let sum=0;
    for (const id of selected) { const value=rate(entries.get(id),direction); if (value===null) return null; sum+=value; }
    return Number.isFinite(sum) ? sum : null;
  }
  return {read:total('read'),write:total('write'),selected:selected.length,present,membershipMatches:matches};
}

export function aggregateIOWindow(history, key, selection, currentDevices) {
  const ids=selectedDeviceIDs(selection,currentDevices), strictMembership=selection?.mode!=='custom';
  return (history ?? []).map(point=>({at:point.at,sequence:point.sequence,...aggregateIO(point[key],ids,{strictMembership})}));
}
