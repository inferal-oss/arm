// Host pressure and guardian presentation. Pressure comes from the monitor
// stream; guardian state is polled while the monitor view is visible.
const RDF_TYPE='http://www.w3.org/1999/02/22-rdf-syntax-ns#type',ARM='https://arm.local/ui#',POLL_MS=4000;
export const PRESSURE_RANK={normal:0,elevated:1,high:2,critical:3};
const TONE={normal:'success',elevated:'warning',high:'warning',critical:'danger'};
const LABEL={cpu:'CPU pressure',memory:'Memory pressure',io:'I/O pressure'};

export function pressureRows(pressure,stale){
  return ['cpu','memory','io'].map(key=>{const p=pressure?.[key]??{},level=stale||!p.level?'unknown':p.level;
    return{key,label:LABEL[key],level,value:level.toUpperCase(),tone:TONE[level]??'neutral',reason:level==='normal'||level==='unknown'?'':(p.reason??'')};});
}
// worstRank is the highest known level of a history point, or null.
export function worstRank(point){const known=[point.cpu_pressure_rank,point.memory_pressure_rank,point.io_pressure_rank].filter(v=>typeof v==='number');return known.length?Math.max(...known):null;}

function one(v,digits=1){return v==null?'?':Number(v).toFixed(digits);}
function gib(v){return v==null?'?':(Number(v)/2**30).toFixed(1)+' GiB';}
function mib(v){return v==null?'?':(Number(v)/2**20).toFixed(1)+' MiB/s';}
export function activeReversible(a){return Boolean(a.reversible&&!a.reverted_at&&(a.outcome==='applied'||a.outcome==='partial'));}

// verdictOf mirrors the CLI and terminal headline. Unknown is never clear.
export function verdictOf(status){
  if(!status)return{level:'unknown',tone:'neutral',text:'Guardian status unavailable'};
  if(status.stale)return{level:'unknown',tone:'neutral',text:'Pressure unknown: observations are stale or unavailable'};
  const under=[],unknown=[];let worst='normal';
  for(const[key,name]of[['cpu','CPU'],['memory','memory'],['io','I/O']]){const level=status.pressure?.[key]?.level;
    if(!(level in PRESSURE_RANK)){unknown.push(name);continue;}
    if(PRESSURE_RANK[level]>0)under.push(`${name} ${level}`);
    if(PRESSURE_RANK[level]>PRESSURE_RANK[worst])worst=level;}
  if(!under.length&&unknown.length===3)return{level:'unknown',tone:'neutral',text:'Pressure unknown: no observations yet'};
  if(!under.length&&unknown.length)return{level:'normal',partial:true,tone:'warning',text:`No pressure detected; ${unknown.join(', ')} unknown`};
  let text=under.length?`Under pressure: ${under.join(', ')}`+(unknown.length?`; ${unknown.join(', ')} unknown`:''):'All clear: no resource is under pressure';
  const incidents=(status.incidents??[]).length;if(incidents)text+=` · ${incidents} open incident(s)`;
  return{level:worst,tone:TONE[worst],text};
}
function seconds(ns){const s=Math.round(Number(ns??0)/1e9);return s%60===0&&s>=60?`${s/60}m`:`${s}s`;}
export function ladderRows(ladder){return(ladder??[]).map(r=>[r.step,r.level,seconds(r.after_ns)]);}

export function guardianView(status){
  if(!status)return{available:false};
  const off=status.mode==='off',budget=status.budget??{};
  return{available:true,off,
    headline:off?'Host guardian is off':`Host guardian · ${status.mode} mode · ${status.role} host`,
    detail:off?'Enable it with: arm config set guardian.enabled true, then restart the backend.':`${budget.used??0} of ${budget.limit??0} process actions used this hour${status.stale?' · pressure stale: all actions held':''}`,
    incidents:(status.incidents??[]).map(i=>`${(LABEL[i.resource]??i.resource)} ${i.level} since ${new Date(i.since).toLocaleTimeString()} · peak ${i.peak}${i.step?' · last step '+i.step:''}`),
    groups:(status.groups??[]).slice(0,10).map(g=>[g.name,one(g.cpu_cores),gib(g.memory_bytes),mib(g.disk_bytes_per_second),String(g.processes),[g.protected?'protected: '+g.protected:'',g.managed?'ARM-managed':'',g.agent?'agent '+g.agent:'',...(g.state??[])].filter(Boolean).join('; ')||'—']),
    actions:(status.actions??[]).slice(0,20).map(a=>({id:a.id,when:new Date(a.at).toLocaleTimeString(),title:`${a.step} ${a.group_name||''}`.trim(),outcome:a.outcome,reason:a.reason,detail:a.detail??'',undo:activeReversible(a)})),
  };
}

// createGuardianPanel builds the Monitor pressure card and summary, and the
// Guardian page. Guardian state is polled while either view is visible.
export function createGuardianPanel({ui,call,connected,onError=()=>{},openGuardian=()=>{}}){
  let visible=false,timer=null,busy=false,status=null,disposed=false,readError='';
  const pressureParts=()=>{const stats=pressureRows(null,true).map(r=>ui.stat(r.label,r.value)),reasons=ui.text(''),chart=ui.line('Worst pressure level over time',[],{min:0,max:3,unit:'level'});return{stats,reasons,chart};};
  const monitorPressure=pressureParts(),pagePressure=pressureParts();
  const legend=()=>ui.text('0 normal · 1 elevated · 2 high · 3 critical. Gaps are unknown, never normal.');
  const pressureCard=ui.card('Host pressure',[ui.grid(monitorPressure.stats,{columns:3,minWidth:'160px',gap:2}),monitorPressure.reasons,monitorPressure.chart,legend()]);
  const summaryVerdict=ui.text(''),summaryDetail=ui.text('');
  const summaryCard=ui.card('Host guardian',[summaryVerdict,summaryDetail],{actions:[ui.button('Open Guardian',()=>openGuardian())]});
  ui.put?.(pressureCard.iri,RDF_TYPE,ARM+'PressureCard',true);ui.put?.(summaryCard.iri,RDF_TYPE,ARM+'GuardianCard',true);
  // Guardian page.
  const verdictBadge=ui.badge('Unknown'),verdictText=ui.text(''),headline=ui.text(''),detail=ui.text(''),updated=ui.text('');
  const statusCard=ui.card('Guardian status',[ui.toolbar('Verdict',[verdictBadge,verdictText]),headline,detail,updated],{actions:[ui.button('Refresh',()=>poll(true))]});
  const pagePressureCard=ui.card('Pressure',[ui.grid(pagePressure.stats,{columns:3,minWidth:'160px',gap:2}),pagePressure.reasons,pagePressure.chart,legend()]);
  const ladder=ui.stack(),incidents=ui.stack(),groups=ui.stack(),actions=ui.stack();
  const ladderCard=ui.card('Escalation ladder',[ladder,ui.text('Change it with arm guardian ladder edit, or inspect it with arm guardian ladder.')]);
  const incidentsCard=ui.card('Open incidents',[incidents]),groupsCard=ui.card('Heaviest programs',[groups]),actionsCard=ui.card('Recent actions',[actions]);
  ui.put?.(statusCard.iri,RDF_TYPE,ARM+'GuardianStatusCard',true);
  const page=[ui.grid([statusCard,pagePressureCard],{columns:2,minWidth:'320px',gap:2}),ui.grid([incidentsCard,ladderCard],{columns:2,minWidth:'320px',gap:2}),groupsCard,actionsCard];
  let actionRows=[];
  function replace(root,children){const old=[...root.children];root.replace(...children);for(const h of old)if(!root.children.includes(h))h.dispose();}
  function setPressure(parts,sample,stale,series){const rows=pressureRows(sample?.pressure,stale);rows.forEach((r,i)=>parts.stats[i].set({value:r.value,tone:r.tone}));
    parts.reasons.set({label:rows.filter(r=>r.reason).map(r=>`${r.label}: ${r.reason}`).join(' · ')||(stale?'Pressure unavailable while observations are stale':'No resource is under pressure')});
    parts.chart.setSeries(series(worstRank),{min:0,max:3,unit:'level'});}
  function updatePressure(sample,history,stale,series){ui.batch(()=>{setPressure(monitorPressure,sample,stale,series);setPressure(pagePressure,sample,stale,series);});}
  let undoing=false,again=false;
  // A reverted action loses its Undo button at once, even while a poll is in flight.
  async function undo(id){if(undoing)return;undoing=true;try{await call('guardian_undo',{action:id});const a=status?.actions?.find(x=>x.id===id);if(a){a.reverted_at=new Date().toISOString();render();}await poll(true);}catch(error){onError(error);}finally{undoing=false;}}
  function render(){ui.batch(()=>{const v=guardianView(status),verdict=verdictOf(status);
    summaryVerdict.set({label:verdict.text});
    verdictBadge.set({label:verdict.partial?'PARTLY UNKNOWN':verdict.level.toUpperCase(),tone:verdict.tone});verdictText.set({label:verdict.text});
    if(!v.available){const why=readError?`Host guardian status unavailable: ${readError}`:'Host guardian status unavailable from this backend';
      summaryDetail.set({label:why});headline.set({label:why});detail.set({label:''});updated.set({label:''});replace(ladder,[]);replace(incidents,[]);replace(groups,[]);replace(actions,[]);actionRows=[];return;}
    summaryDetail.set({label:v.off?v.detail:`${v.headline} · ${v.incidents.length} open incident(s)`});
    headline.set({label:v.headline});detail.set({label:v.detail});
    updated.set({label:readError?`Stale: ${readError}`:status.at?`Updated ${new Date(status.at).toLocaleTimeString()}`:'No reading yet'});
    const steps=ladderRows(status.ladder);replace(ladder,v.off?[ui.text('The ladder runs only while the guardian is enabled.')]:steps.length?[ui.table(['Step','Minimum level','After'],steps)]:[ui.text('No steps')]);
    replace(incidents,v.incidents.length?v.incidents.map(t=>ui.text(t,{tone:'warning'})):[ui.text('No open incidents')]);
    replace(groups,v.groups.length?[ui.table(['Program','CPU cores','Memory','Disk','Processes','Notes'],v.groups)]:[ui.text('No program activity sampled yet')]);
    const stamp=JSON.stringify(v.actions);if(stamp!==actionRows.stamp){actionRows=v.actions.map(a=>ui.card(a.title,[ui.badge(a.outcome,{tone:a.outcome==='failed'?'danger':a.outcome==='planned'?'info':'neutral'}),ui.text(`${a.when} · ${a.reason}`),...(a.detail?[ui.text(a.detail)]:[])],{actions:a.undo?[ui.button('Undo',()=>undo(a.id))]:[]}));actionRows.stamp=stamp;replace(actions,actionRows.length?actionRows:[ui.text('No guardian actions recorded')]);}
  });}
  // A failed read keeps the last reading, marked stale.
  async function poll(force=false){if(disposed||(!visible&&!force)||!connected())return;if(busy){if(force)again=true;return;}busy=true;try{status=await call('guardian_status');readError='';render();}catch(error){readError=error?.message??String(error);render();}finally{busy=false;if(again){again=false;poll(true);}}}
  function setVisible(v){visible=v;clearInterval(timer);timer=null;if(v){poll();timer=setInterval(poll,POLL_MS);}}
  function dispose(){disposed=true;clearInterval(timer);}
  return{cards:[pressureCard,summaryCard],page,updatePressure,setVisible,poll,dispose};
}
