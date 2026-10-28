// Prepared chosen-node panel. Shared Mesh integration supplies exact host-bound
// read/set functions; this module never falls back to localhost or polls a list.
function validReport(report,host) {
  const d=report?.owner_declared;
  const date=value=>typeof value==='string' && value.length<=64 && Number.isFinite(Date.parse(value));
  return report?.schema===1 && report.host===host && date(report.observed_at) && Number.isSafeInteger(d?.revision) && d.revision>=0 && ['unspecified','stable','intermittent'].includes(d?.profile) && (d.revision===0 ? d.profile==='unspecified' && d.updated_at===undefined : date(d.updated_at));
}
export function availabilityFacts({host, report, profileState, connection, resourceState}) {
  const lookup=(values,key,fallback)=>Object.hasOwn(values,key)?values[key]:fallback;
  const profiles = {unspecified: 'Unspecified (owner declared)', stable: 'Stable (owner declared)', intermittent: 'Mobile / intermittent (owner declared)'};
  const valid = validReport(report,host);
  let declaration = profileState === 'stale' ? 'Stale owner declaration; refresh this exact node' : 'Unknown / unsupported';
  if (profileState === 'reported' && valid) declaration = profiles[report.owner_declared.profile];
  if (profileState === 'stale' && valid) declaration = `Last reported: ${profiles[report.owner_declared.profile]}; stale declaration`;
  return [
    ['Owner availability', declaration],
    ['Connection observation', lookup({reachable:'Reachable',rejoining:'Rejoining',offline:'Offline',stale:'Stale',denied:'Observation denied',unsupported:'Observation unsupported',identity_unavailable:'Identity unavailable'},connection,'Unknown')],
    ['Resource availability', lookup({fresh:'Fresh report; destination admission still required',stale:'Stale; unavailable for placement',unknown:'Unknown; unavailable for placement'},resourceState,'Unknown; unavailable for placement')],
    ['Stable host ID', /^[a-f0-9]{32}$/.test(host)?host:'Unavailable']
  ];
}

export function availabilityObservation(host, observation) {
  if (observation?.schema!==1 || observation.host!==host) return {connection:'unobserved',resourceState:'unknown',initialReport:null,declarationState:'unsupported'};
  const states=observation.resource_freshness;
  const values=[states?.cpu,states?.memory,states?.disk];
  const resourceState=values.every(v=>v==='fresh')?'fresh':values.some(v=>v==='stale')?'stale':'unknown';
  const initialReport={schema:1,host,owner_declared:observation.owner_declared,observed_at:observation.last_success_at};
  return {connection:observation.connection_state,resourceState,initialReport:validReport(initialReport,host)?initialReport:null,declarationState:observation.declaration_state==='reported'?'reported':'stale'};
}

export function mountNodeAvailability({ui, root, host, sourceHost, local, read, inspect, setSelf, connected=()=>true, mutationState={epoch:0,pending:false,uncertain:false}, initialReport=null, declarationState='unsupported', connection='unobserved', resourceState='unknown', newKey=()=>globalThis.crypto.randomUUID()}) {
  const facts = ui.stack(), note = ui.text('Owner availability is advisory. Observed freshness is separate. Prefer stable hosts for long or deadline-sensitive work. Automatic cross-host placement is not implemented.'), status = ui.text('', {role:'status'});
  const box = ui.card('Availability', [facts, note, status]);
  root.append(box);
  let disposed=false, generation=0, report=initialReport, busy=false, currentReport=false;
  let profile, reason, save;
  const owner = local === true && host === sourceHost && typeof setSelf === 'function';
  const render = (state) => facts.replace(ui.descriptions(availabilityFacts({host,report,profileState:state,connection,resourceState})));
  const showFence = () => {
    render(report?'stale':'unsupported');
    if(save)save.disabled=true;
    const original=mutationState.original;
    status.set({label:`The update ${mutationState.pending?'is still pending':'may have applied or the revision may have changed'}. Original key: ${original?.key??'unavailable'}; expected revision: ${original?.expected_revision??'unavailable'}. Read current declaration after the original response settles before another explicit change. No update was replayed.`});
  };
  const refresh = async () => {
    if (disposed || !connected() || typeof read !== 'function') return;
    const current=++generation, boundary=mutationState.epoch;
    try {
      const [readResult, observationResult]=await Promise.allSettled([read(), typeof inspect==='function'?inspect():Promise.resolve(null)]);
      if (disposed || current!==generation || boundary!==mutationState.epoch || mutationState.pending) return;
      const observation=observationResult.status==='fulfilled'?observationResult.value:null;
      const state=availabilityObservation(host,observation);
      if (typeof inspect==='function') {connection=state.connection;resourceState=state.resourceState;}
      if (readResult.status!=='fulfilled') throw Error('Current report unavailable');
      const received=readResult.value;
      if (!validReport(received,host)) throw Error('Invalid report');
      report=received;currentReport=true;mutationState.uncertain=false;mutationState.original=null;render('reported');
      if (owner) {profile.value=report.owner_declared.profile;save.disabled=busy || !connected();}
      status.set({label:'Current owner declaration received. Resource admission remains destination-owned.'});
    } catch {
      if (disposed || current!==generation || boundary!==mutationState.epoch || mutationState.pending) return;
      currentReport=false;
      if (mutationState.uncertain) {showFence();return;}
      render(report?'stale':'unsupported');
      if (save) save.disabled=true;
      status.set({label:'No current declaration: this operation is unsupported, denied or unavailable. No stable profile is inferred.'});
    }
  };
  const refreshButton=ui.button('Read current declaration',refresh);
  box.append(refreshButton);
  if (owner) {
    profile=ui.select('Availability profile',[{value:'unspecified',label:'Unspecified'},{value:'stable',label:'Stable'},{value:'intermittent',label:'Mobile / intermittent'}],{value:'unspecified'});
    reason=ui.area('Private reason for changing availability',{value:'',required:true,maxLength:1024});
    save=ui.button('Save owner availability',async()=>{
      if (disposed || !connected() || busy || mutationState.pending || mutationState.uncertain || !report || !currentReport) return;
      const text=String(reason.value??'');
      if (!['unspecified','stable','intermittent'].includes(profile.value) || !text || text.trim()!==text || new TextEncoder().encode(text).length>1024) {status.set({label:'Choose a profile and enter a trimmed reason of1–1024bytes.'});return;}
      const request=Object.freeze({key:newKey(),expected_revision:report.owner_declared.revision,profile:profile.value,reason:text});
      generation++; // A read begun before this write cannot clear its outcome fence.
      mutationState.epoch++;mutationState.pending=true;mutationState.original={key:request.key,expected_revision:request.expected_revision};
      currentReport=false;busy=true;save.disabled=true;
      try {await setSelf(request);mutationState.pending=false;mutationState.uncertain=false;mutationState.epoch++;if(disposed)return;report=null;await refresh();if(!disposed && report)status.set({label:'Owner availability updated; current declaration received.'});}
      catch {mutationState.pending=false;mutationState.uncertain=true;mutationState.epoch++;if(disposed)return;generation++;showFence();}
      finally {busy=false;if(!disposed && currentReport && report && !mutationState.uncertain && !mutationState.pending)save.disabled=!connected();}
    });
    save.disabled=true;
    box.append(ui.stack([profile,reason,save]));
  }
  render(declarationState);
  if (owner && (mutationState.pending || mutationState.uncertain)) showFence();
  else void refresh();
  return {refresh,updateConnection(){if(!connected()){generation++;currentReport=false;connection='offline';resourceState='unknown';render(report?'stale':'unsupported');}refreshButton.disabled=!connected();if(save)save.disabled=!connected()||busy||mutationState.pending||mutationState.uncertain||!report||!currentReport;},dispose(){disposed=true;generation++;box.dispose();}};
}
