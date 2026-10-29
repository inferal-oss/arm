import {readVisibilityPreference,saveVisibilityPreference} from './dashboard-preferences.mjs';
// Lifecycle controls visibility. An outcome describes how a terminal lease
// ended; it never makes an unresolved or unfamiliar lifecycle safe to hide.
const states = {active: 'Active', queued: 'Queued', provisioning: 'Provisioning', ending: 'Ending', reclaiming: 'Reclaiming', needs_attention: 'Needs attention'};
const outcomes = {released: 'Released', cancelled: 'Cancelled', canceled: 'Cancelled', expired: 'Expired', revoked: 'Revoked', completed: 'Completed', succeeded: 'Completed', success: 'Completed', failed: 'Failed', launch_failed: 'Failed', admission_failed: 'Failed', interrupted: 'Interrupted', reconciled: 'Reconciled', handed_off: 'Handed off', supervision_lost: 'Supervision lost', port_supervision_lost: 'Port supervision lost', descendants_unreconciled: 'Descendants need review', storage_unresolved: 'Storage needs review'};
const known = (labels, key) => Object.hasOwn(labels, key) ? labels[key] : '';
const humanize = value => typeof value === 'string' && value ? value.replaceAll(/[_-]+/g, ' ').replace(/^./, letter => letter.toUpperCase()) : '';

export function isInactiveLease(lease) { return lease?.state === 'terminal'; }
export function leaseStateLabel(lease) {
  if (isInactiveLease(lease)) return known(outcomes, lease.outcome) || humanize(lease.outcome) || 'Inactive';
  return known(states, lease?.state) || humanize(lease?.state) || 'Unknown state';
}
export function leaseVisible(lease, hideInactive = false) { return !hideInactive || !isInactiveLease(lease); }

export const HIDE_INACTIVE_KEY = 'arm.dashboard.hide-inactive-leases.v1';
export const readHideInactive=storage=>readVisibilityPreference(storage,HIDE_INACTIVE_KEY);
export const saveHideInactive=(storage,value)=>saveVisibilityPreference(storage,HIDE_INACTIVE_KEY,value);
