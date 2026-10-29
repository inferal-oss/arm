// Presentation helpers never infer process completion from lease outcomes.
export const activeRun = run => ['active','queued','provisioning','ending','reclaiming','needs_attention'].includes(run.state);
export const currentRun = run => run.state!=='terminal'||run.outcome==='reporter_lost';
export function runStatus(run) {
  const live={active:['Running','success'],queued:['Queued','info'],provisioning:['Starting','info'],ending:['Stopping','warning'],reclaiming:['Cleaning up','warning'],needs_attention:['Needs attention','warning']};
  if(Object.hasOwn(live,run.state))return {label:live[run.state][0],tone:live[run.state][1]};
  if(run.outcome==='log_failed')return {label:'Log failed',tone:'warning'};
  if(run.outcome==='reporter_lost')return {label:'Reporter lost',tone:'warning'};
  if(run.state==='interrupted')return {label:'Interrupted',tone:'warning'};
  if(run.state!=='terminal')return {label:'Unknown',tone:'warning'};
  if(run.outcome==='rejected')return {label:'Rejected',tone:'warning'};
  if(run.outcome==='expired')return {label:'Expired',tone:'neutral'};
  if(['cancelled','revoked','released'].includes(run.outcome))return {label:run.outcome==='cancelled'?'Cancelled':'Stopped',tone:'neutral'};
  if(run.exit_code!=null&&run.exit_code!==0||['failed','launch_failed','admission_failed'].includes(run.outcome))return {label:'Failed',tone:'danger'};
  return {label:run.state==='terminal'?(run.outcome==='completed'||run.exit_code===0&&!run.outcome?'Completed':'Finished'):'Unknown',tone:run.state==='terminal'?'neutral':'warning'};
}
const plain=value=>String(value??'').replace(/[\x00-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]/g,' ').replace(/\s+/g,' ').trim();
export function compactRunText(value,limit=80){const chars=Array.from(plain(value));return chars.length>limit?chars.slice(0,Math.max(0,limit-1)).join('')+'…':chars.join('');}
const basename=value=>String(value??'').split(/[\\/]/).filter(Boolean).at(-1)||String(value??'');
export function runCommand(run,limit=100){
  const command=(run.command??[]).map((value,i)=>{if(i===0)return basename(value);const text=plain(value);return text.length>52&&/[\\/]/.test(text)?'…/'+basename(text):text;}).join(' ');
  return compactRunText(command||'Command unavailable',limit);
}
export const runPurpose = run => compactRunText(run.reason||runCommand(run),84);
const instant=run=>Date.parse(run.started_at||run.created_at)||0;
export function orderRuns(runs){return [...runs].sort((a,b)=>(Number(activeRun(b))*2+Number(currentRun(b)))-(Number(activeRun(a))*2+Number(currentRun(a)))||instant(b)-instant(a)||String(a.id).localeCompare(String(b.id)));}
export function runWhen(run,now=Date.now(),locale=undefined){
  const start=instant(run),at=activeRun(run)?start:Date.parse(run.ended_at||run.last_reported_at||run.started_at||run.created_at)||0;
  if(!at)return 'Time unavailable';
  const seconds=Math.max(0,Math.floor((now-at)/1000));
  const short=seconds<60?`${seconds}s`:seconds<3600?`${Math.floor(seconds/60)}m`:`${Math.floor(seconds/3600)}h`;
  if(activeRun(run))return short+' elapsed';
  return seconds<10?'just now':seconds<86400?short+' ago':new Intl.DateTimeFormat(locale,{month:'short',day:'numeric',hour:'numeric',minute:'2-digit'}).format(new Date(at));
}
