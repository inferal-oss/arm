// The Guardian page. Pressure comes from the monitor stream; guardian state
// is polled while the page is visible.
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
  const off=status.mode==='off';
  return{available:true,off,
    incidents:(status.incidents??[]).map(i=>`${(LABEL[i.resource]??i.resource)} ${i.level} since ${new Date(i.since).toLocaleTimeString()} · peak ${i.peak}${i.step?' · last step '+i.step:''}`),
    groups:(status.groups??[]).slice(0,10).map(g=>[g.name,one(g.cpu_cores),gib(g.memory_bytes),mib(g.disk_bytes_per_second),String(g.processes),[g.protected?'protected: '+g.protected:'',g.managed?'ARM-managed':'',g.agent?'agent '+g.agent:'',...(g.state??[])].filter(Boolean).join('; ')||'—']),
    actions:(status.actions??[]).slice(0,20).map(a=>({id:a.id,when:new Date(a.at).toLocaleTimeString(),title:`${a.step} ${a.group_name||''}`.trim(),outcome:a.outcome,reason:a.reason,detail:a.detail??'',undo:activeReversible(a)})),
  };
}

const MODE_HINT={observe:'Records steps as planned and runs none',advise:'Notifies and asks agents only',act:'Runs every ladder step',off:'Observes nothing and acts on nothing'};
const MODE_TONE={act:'success',advise:'info',observe:'info',off:'neutral'};
const ROLE_HINT={manned:'An operator is present',unmanned:'Unattended host'};
// verdictParts names the verdict for its badge; the rest of its sentence
// becomes the heading.
export function verdictParts(verdict){const label=verdict.level==='unknown'?'Unknown':verdict.partial?'Partly unknown':verdict.level==='normal'?'All clear':'Under pressure';
  const cut=verdict.text.indexOf(': '),rest=cut<0?verdict.text:verdict.text.slice(cut+2);return{label,text:rest.charAt(0).toUpperCase()+rest.slice(1)};}
export function statusFacts(status){const budget=status?.budget??{},mode=status?.mode??'unknown',role=status?.role??'unknown',incidents=(status?.incidents??[]).length;
  return[{label:'Mode',value:mode,help:MODE_HINT[mode]??'',tone:MODE_TONE[mode]??'neutral'},{label:'Host',value:role,help:ROLE_HINT[role]??'',tone:'neutral'},
    {label:'Actions this hour',value:`${budget.used??0} / ${budget.limit??0}`,help:'Process steps the hourly budget allows',tone:(budget.limit&&budget.used>=budget.limit)?'warning':'neutral'},
    {label:'Open incidents',value:String(incidents),help:incidents?'Resources held at high or above':'No resource held at high',tone:incidents?'warning':'success'}];}

// copyCommand is a command button that copies its command when clicked.
function copyCommand(ui,command){let timer=null;const h=ui.button(command,async()=>{clearTimeout(timer);
    try{await navigator.clipboard.writeText(command);h.set({label:`${command}  ✓ copied`});}catch{h.set({label:`${command}  · copy unavailable`});}
    timer=setTimeout(()=>h.set({label:command}),2000);},{help:'Copy to clipboard'});
  return h;}
// commandRow shows copy commands in monospace (styled through its class).
function commandRow(ui,commands){const row=ui.stack(commands.map(c=>copyCommand(ui,c)),{row:true,gap:2});ui.put?.(row.iri,RDF_TYPE,ARM+'CommandRow',true);return row;}

// createGuardianPanel builds the Guardian page. Guardian state is polled
// while the page is visible.
export function createGuardianPanel({ui,call,connected,onError=()=>{}}){
  let visible=false,timer=null,busy=false,status=null,disposed=false,readError='';
  const pressureStats=pressureRows(null,true).map(r=>ui.stat(r.label,r.value)),pressureReasons=ui.text(''),pressureChart=ui.line('Worst pressure level over time',[],{min:0,max:3,unit:'level'});
  // Status: the verdict, the guardian's facts, open incidents and a note.
  const verdictBadge=ui.badge('Unknown'),verdictHeading=ui.heading('',3),facts=statusFacts(null).map(f=>ui.stat(f.label,f.value,f.help)),incidents=ui.stack(),note=ui.stack(),updated=ui.text('');
  const statusCard=ui.card('Guardian status',[ui.toolbar('Verdict',[verdictBadge,verdictHeading]),ui.grid(facts,{columns:4,minWidth:'150px',gap:2}),incidents,note,updated],{actions:[ui.button('Refresh',()=>poll(true))]});
  ui.put?.(statusCard.iri,RDF_TYPE,ARM+'GuardianStatusCard',true);
  // Kept across renders so a copy's confirmation is not reset by a poll.
  const enableText=ui.text('Enable the host guardian, then restart the backend:').retain(),enableRow=commandRow(ui,['arm config set guardian.enabled true']).retain();
  const pressureCard=ui.card('Pressure',[ui.grid(pressureStats,{columns:3,minWidth:'160px',gap:2}),pressureReasons,pressureChart,ui.text('0 normal · 1 elevated · 2 high · 3 critical. Gaps are unknown, never normal.')]);
  const ladder=ui.stack(),groups=ui.stack(),actions=ui.stack();
  const ladderCard=ui.card('Escalation ladder',[ladder,commandRow(ui,['arm guardian ladder edit','arm guardian ladder']),ui.text('Click a command to copy it, then run it in a terminal.')]);
  const groupsCard=ui.card('Heaviest programs',[groups]),actionsCard=ui.card('Recent actions',[actions]);
  const page=[statusCard,ui.grid([pressureCard,ladderCard],{columns:2,minWidth:'320px',gap:2}),groupsCard,actionsCard];
  let actionRows=[];
  function replace(root,children){const old=[...root.children];root.replace(...children);for(const h of old)if(!root.children.includes(h)&&h!==enableText&&h!==enableRow)h.dispose();}
  // Each level carries its evidence, with the values and the boundary reached.
  function updatePressure(sample,history,stale,series){ui.batch(()=>{const rows=pressureRows(sample?.pressure,stale);rows.forEach((r,i)=>pressureStats[i].set({value:r.value,tone:r.tone,help:r.reason}));
    pressureReasons.set({label:rows.some(r=>r.reason)?'':(stale?'Pressure unavailable while observations are stale':'No resource is under pressure')});
    pressureChart.setSeries(series(worstRank),{min:0,max:3,unit:'level'});});}
  let undoing=false,again=false;
  // A reverted action loses its Undo button at once, even while a poll is in flight.
  async function undo(id){if(undoing)return;undoing=true;try{await call('guardian_undo',{action:id});const a=status?.actions?.find(x=>x.id===id);if(a){a.reverted_at=new Date().toISOString();render();}await poll(true);}catch(error){onError(error);}finally{undoing=false;}}
  function render(){ui.batch(()=>{const v=guardianView(status),verdict=verdictOf(status),parts=verdictParts(verdict);
    verdictBadge.set({label:parts.label.toUpperCase(),tone:verdict.tone});verdictHeading.set({label:parts.text});
    statusFacts(status).forEach((f,i)=>facts[i].set({value:v.available?f.value:'—',help:v.available?f.help:'',tone:v.available?f.tone:'neutral'}));
    if(!v.available){const why=readError?`Host guardian status unavailable: ${readError}`:'Host guardian status unavailable from this backend';
      replace(incidents,[]);replace(note,[ui.text(why,{tone:'warning'})]);updated.set({label:''});replace(ladder,[]);replace(groups,[]);replace(actions,[]);actionRows=[];return;}
    replace(incidents,v.incidents.map(t=>ui.text(t,{tone:'warning'})));
    const notes=[];
    if(v.off)notes.push(enableText,enableRow);
    if(status.stale)notes.push(ui.text('Pressure is stale or unavailable; the guardian holds all actions.',{tone:'warning'}));
    if(status.inventory)notes.push(ui.text(`Inventory: ${status.inventory}`,{tone:'warning'}));
    replace(note,notes);
    updated.set({label:readError?`Stale: ${readError}`:status.at?`Updated ${new Date(status.at).toLocaleTimeString()} · refreshes every ${POLL_MS/1000}s`:'No reading yet'});
    const steps=ladderRows(status.ladder);replace(ladder,v.off?[ui.text('The ladder runs only while the guardian is enabled.')]:steps.length?[ui.table(['Step','Minimum level','After'],steps)]:[ui.text('No steps')]);
    replace(groups,v.groups.length?[ui.table(['Program','CPU cores','Memory','Disk','Processes','Notes'],v.groups)]:[ui.text('No program activity sampled yet')]);
    const stamp=JSON.stringify(v.actions);if(stamp!==actionRows.stamp){actionRows=v.actions.map(a=>ui.card(a.title,[ui.badge(a.outcome,{tone:a.outcome==='failed'?'danger':a.outcome==='planned'?'info':'neutral'}),ui.text(`${a.when} · ${a.reason}`),...(a.detail?[ui.text(a.detail)]:[])],{actions:a.undo?[ui.button('Undo',()=>undo(a.id))]:[]}));actionRows.stamp=stamp;replace(actions,actionRows.length?actionRows:[ui.text('No guardian actions recorded')]);}
  });}
  // A failed read keeps the last reading, marked stale.
  async function poll(force=false){if(disposed||(!visible&&!force)||!connected())return;if(busy){if(force)again=true;return;}busy=true;try{status=await call('guardian_status');readError='';render();}catch(error){readError=error?.message??String(error);render();}finally{busy=false;if(again){again=false;poll(true);}}}
  function setVisible(v){visible=v;clearInterval(timer);timer=null;if(v){poll();timer=setInterval(poll,POLL_MS);}}
  function dispose(){disposed=true;clearInterval(timer);enableText.release();enableRow.release();}
  return{page,updatePressure,setVisible,poll,dispose};
}
