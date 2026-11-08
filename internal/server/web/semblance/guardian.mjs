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

export function createGuardianPanel({ui,call,connected,onError=()=>{}}){
  let visible=false,timer=null,busy=false,status=null,disposed=false;
  const stats=pressureRows(null,true).map(r=>ui.stat(r.label,r.value)),reasons=ui.text(''),chart=ui.line('Worst pressure level over time',[],{min:0,max:3,unit:'level'});
  const pressureCard=ui.card('Host pressure',[ui.grid(stats,{columns:3,minWidth:'160px',gap:2}),reasons,chart,ui.text('0 normal · 1 elevated · 2 high · 3 critical. Gaps are unknown, never normal.')]);
  const headline=ui.text(''),detail=ui.text(''),incidents=ui.stack(),groups=ui.stack(),actions=ui.stack();
  const guardianCard=ui.card('Host guardian',[headline,detail,incidents,ui.disclosure('Heaviest programs',[groups]),ui.disclosure('Recent actions',[actions],{open:true})]);
  ui.put?.(pressureCard.iri,RDF_TYPE,ARM+'PressureCard',true);ui.put?.(guardianCard.iri,RDF_TYPE,ARM+'GuardianCard',true);
  let actionRows=[];
  function replace(root,children){const old=[...root.children];root.replace(...children);for(const h of old)if(!root.children.includes(h))h.dispose();}
  function updatePressure(sample,history,stale,series){ui.batch(()=>{const rows=pressureRows(sample?.pressure,stale);rows.forEach((r,i)=>stats[i].set({value:r.value,tone:r.tone}));
    reasons.set({label:rows.filter(r=>r.reason).map(r=>`${r.label}: ${r.reason}`).join(' · ')||(stale?'Pressure unavailable while observations are stale':'No resource is under pressure')});
    chart.setSeries(series(worstRank),{min:0,max:3,unit:'level'});});}
  let undoing=false;
  async function undo(id){if(undoing)return;undoing=true;try{await call('guardian_undo',{action:id});await poll(true);}catch(error){onError(error);}finally{undoing=false;}}
  function render(){ui.batch(()=>{const v=guardianView(status);if(!v.available){headline.set({label:'Host guardian status unavailable from this backend'});detail.set({label:''});replace(incidents,[]);replace(groups,[]);replace(actions,[]);actionRows=[];return;}
    headline.set({label:v.headline});detail.set({label:v.detail});
    replace(incidents,v.incidents.map(t=>ui.text(t,{tone:'warning'})));
    replace(groups,v.groups.length?[ui.table(['Program','CPU cores','Memory','Disk','Processes','Notes'],v.groups)]:[ui.text('No program activity sampled yet')]);
    const stamp=JSON.stringify(v.actions);if(stamp!==actionRows.stamp){actionRows=v.actions.map(a=>ui.card(a.title,[ui.badge(a.outcome,{tone:a.outcome==='failed'?'danger':a.outcome==='planned'?'info':'neutral'}),ui.text(`${a.when} · ${a.reason}`),...(a.detail?[ui.text(a.detail)]:[])],{actions:a.undo?[ui.button('Undo',()=>undo(a.id))]:[]}));actionRows.stamp=stamp;replace(actions,actionRows.length?actionRows:[ui.text('No guardian actions recorded')]);}
  });}
  async function poll(force=false){if(disposed||busy||(!visible&&!force)||!connected())return;busy=true;try{status=await call('guardian_status');render();}catch{status=null;render();}finally{busy=false;}}
  function setVisible(v){visible=v;clearInterval(timer);timer=null;if(v){poll();timer=setInterval(poll,POLL_MS);}}
  function dispose(){disposed=true;clearInterval(timer);}
  return{cards:[pressureCard,guardianCard],updatePressure,setVisible,poll,dispose};
}
