// Only an explicit opt-out reveals history. Missing, malformed or unavailable
// client storage uses the current-activity default without mutating storage.
export function readVisibilityPreference(storage,key){
  try{return storage?.getItem(key)!=='false';}catch{return true;}
}
export function saveVisibilityPreference(storage,key,value){
  try{if(!storage)return false;storage.setItem(key,value?'true':'false');return true;}catch{return false;}
}
export const HIDE_INACTIVE_RUNS_KEY='arm.dashboard.hide-inactive-runs.v1';
export const readHideInactiveRuns=storage=>readVisibilityPreference(storage,HIDE_INACTIVE_RUNS_KEY);
export const saveHideInactiveRuns=(storage,value)=>saveVisibilityPreference(storage,HIDE_INACTIVE_RUNS_KEY,value);
