// Run output is data. Stock Code renders escaped text; no terminal or HTML interpretation.
const WINDOW = 256 * 1024;
const FS='https://semblance.dev/ns/fullscreen#fullViewport',RDF_TYPE='http://www.w3.org/1999/02/22-rdf-syntax-ns#type',ARM='https://arm.local/ui#';
export function cleanRunText(value) {
  return value.replace(/\r\n/g, '\n').replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f\u2028\u2029\u202a-\u202e\u2066-\u2069]/g, c => `\\u${c.charCodeAt(0).toString(16).padStart(4,'0')}`);
}
export function createRunsConsole({ui,root,call,connected,context=()=>null,onLease=()=>{}}) {
  let active=false,disposed=false,generation=0,busy=false,logBusy=false,timer=null,logTimer=null,selected=null,offset=0,text='',done=false,following=true,decoder=new TextDecoder(),displayCut=false;
  const filter=ui.field('Filter runs, commands or agents',{type:'search'},renderList),status=ui.text('Open a run to follow its output'),list=ui.stack(),detail=ui.stack();
  root.replace(filter,status,list,detail);let runs=[],detailID=null,display=null,full=false,foldBeforeFull=true;const listRows=new Map();
  function replace(parent,children){const old=[...parent.children];parent.replace(...children);for(const child of old)if(!parent.children.includes(child))child.dispose();}
  const label=r=>r.name||r.id;
  function renderList(){ui.batch(()=>{
    const query=filter.value.toLowerCase(),matches=runs.filter(r=>[r.id,r.name,r.agent,r.reason,r.state,...(r.command??[])].join(' ').toLowerCase().includes(query)),visible=matches.slice(0,200),keep=new Set(visible.map(r=>r.id));
    for(const [id,row] of listRows)if(!keep.has(id)){row.card.dispose();listRows.delete(id);}
    const cards=visible.map(run=>{
      let row=listRows.get(run.id);
      if(!row){row={run,command:ui.text(''),summary:ui.text(''),times:ui.text('')};row.button=ui.button('Open '+label(run),()=>open(row.run));row.card=ui.card(label(run),[row.button,row.command,row.summary,row.times]);listRows.set(run.id,row);}
      row.run=run;row.command.set({label:(run.command??[]).join(' ')});row.summary.set({label:`${run.state}${run.outcome?' · '+run.outcome:''} · ${run.agent||'unattributed'} · ${run.reason||'no reason'}`});row.times.set({label:`Started ${run.started_at||run.created_at||'unknown'} · ${run.lease_id?'lease '+run.lease_id:'no lease'}`});return row.card;
    });
    list.replace(...cards);status.set({label:`${matches.length} matching runs${matches.length>200?' · showing 200':''}${!connected()?' · disconnected; cached data':''}`});
  });}
  function renderDetail(){if(!selected){replace(detail,[]);detailID=null;display=null;return;}ui.batch(()=>{
    const run=selected;
    if(detailID!==run.id){
      full=false;detailID=run.id;
      display={heading:ui.heading(label(run),3),command:ui.text(''),metadata:ui.text(''),state:ui.text(''),times:ui.text(''),cursor:ui.text(''),output:ui.code(''),follow:ui.button('Pause following',toggleFollow),next:ui.button('Next output page',()=>readLog(true)),lost:ui.text('Reporter lost; process completion is unknown',{tone:'warning',hidden:true})};
      display.output.set({language:''});
      display.expand=ui.button('Fullscreen log',()=>setFull(!full));
      display.exitHint=ui.text('Escape returns to the run details',{hidden:true});
      display.fold=ui.disclosure('Saved log output',[display.cursor,display.lost,display.output],{open:true});
      display.transcript=ui.stack([ui.heading(label(run)+' · Log output',3),ui.toolbar('Run log controls',[display.follow,display.expand,ui.button('Read from start',readStart),display.next]),display.exitHint,ui.text('Folding hides the transcript; capture and following continue.'),display.fold]);
      ui.put?.(display.transcript.iri,RDF_TYPE,ARM+'RunTranscript',true);
      ui.put?.(display.output.iri,'http://www.w3.org/1999/02/22-rdf-syntax-ns#type','https://arm.local/ui#RunOutput',true);
      ui.put?.(display.output.iri,'https://semblance.dev/ns/component#block',true);
      replace(detail,[display.heading,ui.text('Run ID: '+run.id),display.command,display.metadata,display.state,display.times,...(run.lease_id?[ui.button('Inspect associated lease',()=>onLease(run.lease_id))]:[ui.text('Observation only · no resource lease')]),ui.button('Close output',close),display.transcript]);
    }
    display.command.set({label:(run.command??[]).join(' ')});
    display.metadata.set({label:`Agent: ${run.agent||'—'} · Reason: ${run.reason||'—'} · Host: ${run.host||'—'} · Directory: ${run.cwd||'—'}`});
    display.state.set({label:`State: ${run.state}${run.outcome?' · '+run.outcome:''}${run.exit_code!=null?' · exit '+run.exit_code:''}`});
    display.times.set({label:`Created: ${run.created_at||'—'} · Started: ${run.started_at||'—'} · Ended: ${run.ended_at||'—'} · Last report: ${run.last_reported_at||'—'}`});
    display.follow.set({label:following?'Pause following':'Follow output'});
    display.next.disabled=following||done||!connected();
    display.cursor.set({label:`${!connected()?'Disconnected · cached output · ':''}${following?'Following':'Paused'} · read through byte ${offset}${done?' · end of saved log':''}${displayCut?' · display shows latest 256 KiB; read from start to inspect earlier output':''}${run.truncated?' · saved log reached its retention limit':''}`});
    display.lost.set({hidden:run.outcome!=='reporter_lost'});
    display.output.set({value:text||'(No output yet)'});
  });}
  function setFull(enabled,restoreFocus=true){
    if(!display||full===Boolean(enabled))return;
    if(enabled){foldBeforeFull=display.fold.open;display.fold.open=true;}
    full=Boolean(enabled);
    display.transcript.set({fullViewport:full});
    ui.put?.(display.output.iri,RDF_TYPE,ARM+(full?'RunOutputFull':'RunOutput'),true);
    display.expand.set({label:full?'Exit fullscreen':'Fullscreen log'});display.exitHint.hidden=!full;
    if(!full)display.fold.open=foldBeforeFull;
    if(restoreFocus)display.expand.focus();
    if(full&&following)display.output.scrollEnd?.();
  }
  // Native Escape retracts the stock Fullscreen graph fact. Reflect that same
  // transition in controls and focus, without creating another log reader.
  const unwatch=ui.app?.runtime?.onInput((graph,delta)=>{
    const current=display;
    if(graph!==undefined||!current||![...delta.add,...delta.remove].some(q=>q.subject.value===current.transcript.iri&&q.predicate.value===FS))return;
    queueMicrotask(()=>{
      if(disposed||display!==current||!full)return;
      const store=ui.app.store,f=store.factory;
      const enabled=store.match(f.namedNode(current.transcript.iri),f.namedNode(FS),null,f.defaultGraph()).some(q=>q.object.value==='true');
      if(!enabled)setFull(false);
    });
  });
  function retire(){generation++;busy=false;logBusy=false;if(timer!==null)clearTimeout(timer);timer=null;if(logTimer!==null)clearTimeout(logTimer);logTimer=null;}
  function close(){
    const id=selected?.id;setFull(false,false);retire();selected=null;renderDetail();
    const opener=listRows.get(id)?.button;
    (opener&&!opener.disposed&&opener.isConnected!==false?opener:filter).focus();schedule();
  }
  function open(run){setFull(false,false);retire();selected=run;offset=0;text='';done=false;following=true;displayCut=false;decoder=new TextDecoder();renderDetail();display.heading.focus();return refresh();}
  function toggleFollow(){retire();following=!following;renderDetail();schedule();if(following)return refresh();}
  function readStart(){retire();offset=0;text='';done=false;following=false;displayCut=false;decoder=new TextDecoder();const pending=readLog(true);schedule();return pending;}
  function schedule(){if(active&&!disposed&&timer===null)timer=setTimeout(()=>{timer=null;refresh();},1000);}
  function scheduleLog(){
    if(!active||disposed||!connected()||!following||done||logTimer!==null)return;
    const cut=generation,source=context();
    logTimer=setTimeout(()=>{logTimer=null;if(cut===generation&&source===context())readLog();},0);
  }
  async function readLog(manual=false){
    if(disposed||!active||!selected||!connected()||(!manual&&(!following||done))||logBusy)return;
    logBusy=true;let catchup=false;
    const id=selected.id,cut=generation,source=context(),start=offset;
    try{const page=await call('run_output',{id,offset:start});if(disposed||cut!==generation||source!==context()||!active||selected?.id!==id)return;
      if(page.run?.id!==id||Number(page.offset)!==start)throw Error('Run output identity or cursor changed');
      const bytes=Uint8Array.from(atob(page.data??''),c=>c.charCodeAt(0)),next=Number(page.next_offset);
      if(!Number.isSafeInteger(next)||next!==start+bytes.length)throw Error('Invalid run output cursor');
      const drained=Boolean(page.done)&&next>=Number(page.run.log_bytes);
      text+=cleanRunText(decoder.decode(bytes,{stream:!drained}));if(text.length>WINDOW){text=text.slice(-WINDOW);if(/[\udc00-\udfff]/.test(text[0]))text=text.slice(1);displayCut=true;}
      offset=next;selected=page.run;done=drained;renderDetail();if(following&&bytes.length&&display.fold.open)display.output.scrollEnd?.();
      // One bounded page per task keeps rendering and pause controls responsive.
      catchup=bytes.length>0&&following&&!done&&offset<Number(page.run.log_bytes);
    }catch(error){if(cut===generation&&source===context())status.set({label:'Output unavailable: '+error.message});}finally{if(cut===generation){logBusy=false;if(catchup)scheduleLog();}}
  }
  async function refresh(){if(disposed||!active||busy){schedule();return;}if(!connected()){status.set({label:'Disconnected · cached runs and output are stale'});schedule();return;}busy=true;const cut=generation,source=context();try{
    const inventory=await call('runs');if(disposed||cut!==generation||source!==context()||!active)return;runs=inventory??[];renderList();if(selected){const current=runs.find(r=>r.id===selected.id);if(current){selected=current;renderDetail();}}await readLog();
  }catch(error){if(cut===generation&&source===context())status.set({label:'Runs unavailable: '+error.message});}finally{if(cut===generation)busy=false;schedule();}}
  return {refresh,open,get selected(){return selected;},setActive(value){if(active===value)return;if(!value)setFull(false,false);active=value;retire();if(active)refresh();},updateConnection(){retire();renderList();renderDetail();if(active)refresh();},reset(){setFull(false,false);retire();runs=[];selected=null;renderList();renderDetail();},dispose(){setFull(false,false);disposed=true;active=false;retire();unwatch?.();}};
}
