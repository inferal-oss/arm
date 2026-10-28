import {currentRun,runStatus,compactRunText,runCommand,runPurpose,orderRuns,runWhen} from './run-list.mjs';
// Run output is data. Stock Code renders escaped text; no terminal or HTML interpretation.
const WINDOW = 256 * 1024;
const FS='https://semblance.dev/ns/fullscreen#fullViewport',RDF_TYPE='http://www.w3.org/1999/02/22-rdf-syntax-ns#type',ARM='https://arm.local/ui#';
export function cleanRunText(value) {
  return value.replace(/\r\n/g, '\n').replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f\u2028\u2029\u202a-\u202e\u2066-\u2069]/g, c => `\\u${c.charCodeAt(0).toString(16).padStart(4,'0')}`);
}
export function createRunsConsole({ui,root,call,connected,context=()=>null,onLease=()=>{}}) {
  let active=false,disposed=false,generation=0,busy=false,logBusy=false,timer=null,logTimer=null,selected=null,offset=0,text='',done=false,following=true,decoder=new TextDecoder(),displayCut=false,listError='',logError='';
  const filter=ui.field('Search runs',{type:'search',placeholder:'Purpose, command or agent…'},renderList),status=ui.text(''),historyError=ui.text('',{role:'status',tone:'warning',hidden:true}),list=ui.stack(),detail=ui.stack(),history=ui.stack([filter,status,historyError,list]);
  ui.put?.(root.iri,RDF_TYPE,ARM+'RunSurface',true);root.replace(history,detail);let runs=[],detailID=null,display=null,full=false,foldBeforeFull=true;const listRows=new Map();
  function replace(parent,children){const old=[...parent.children];parent.replace(...children);for(const child of old)if(!parent.children.includes(child))child.dispose();}
  const label=r=>r.name||r.id;
  list.set({role:'list'});
  const activeHeading=ui.heading('Current',3),pastHeading=ui.heading('History',3);
  activeHeading.hidden=pastHeading.hidden=true;list.replace(activeHeading,pastHeading);
  const typed=(component,name)=>{ui.put?.(component.iri,RDF_TYPE,ARM+name,true);return component;};
  function renderList(){
    const focused=ui.activeComponent,focusRow=focused?[...listRows].find(([,row])=>row.button===focused||row.element.contains?.(focused))?.[0]:null;
    ui.batch(()=>{
    const query=filter.value.toLowerCase(),matches=orderRuns(runs.filter(r=>[r.id,r.name,r.agent,r.reason,r.state,r.outcome,runStatus(r).label,...(r.command??[])].join(' ').toLowerCase().includes(query))),visible=matches.slice(0,200),keep=new Set(visible.map(r=>r.id));
    for(const [id,row]of listRows)if(!keep.has(id)){row.element.dispose();listRows.delete(id);}
    const rows=visible.map(run=>{
      let row=listRows.get(run.id);
      if(!row){
        row={run,command:typed(ui.code(''),'RunListCommand'),status:ui.badge(''),secondary:typed(ui.text(''),'RunSecondary')};row.command.set({language:''});
        // Keep the stock Button type: its renderer requires the direct type.
        row.button=ui.button(runPurpose(run),()=>open(row.run),{emphasis:'Quiet'});
        row.element=typed(ui.stack([typed(ui.stack([row.button,row.command]),'RunPrimary'),typed(ui.stack([row.status,row.secondary],{row:true}),'RunRowMeta')]),'RunListRow');row.element.set({role:'listitem'});listRows.set(run.id,row);
      }
      const state=runStatus(run);row.run=run;row.button.set({label:runPurpose(run)});row.command.set({value:runCommand(run)});row.status.set({label:state.label,tone:state.tone});row.secondary.set({label:`${compactRunText(run.agent||'Unattributed',28)} · ${runWhen(run)}`});return {run,row:row.element};
    });
    const active=rows.filter(x=>currentRun(x.run)),past=rows.filter(x=>!currentRun(x.run));activeHeading.set({label:'Current · '+active.length});pastHeading.set({label:'History · '+past.length});
    activeHeading.hidden=!active.length;pastHeading.hidden=!past.length;
    list.replace(activeHeading,...active.map(x=>x.row),pastHeading,...past.map(x=>x.row));
    status.set({label:`${matches.length} run${matches.length===1?'':'s'}${matches.length>200?' · showing 200':''}${!connected()?' · disconnected; cached data':''}`});
    });
    // Native moves within the same list can blur a focused button. Restore only
    // a still-visible control that owned focus before this synchronous update.
    renderErrors();
    if(focusRow&&listRows.has(focusRow)&&!ui.activeComponent&&!focused.disposed&&focused.isConnected!==false)focused.focus();
  }
  function renderErrors(){
    historyError.set({label:listError?'Run list unavailable: '+listError:'',hidden:!listError});
    if(display){const errors=[!connected()?'Disconnected; cached output is stale':'',listError?'Run list unavailable: '+listError:'',logError?'Output unavailable: '+logError:''].filter(Boolean);display.error.set({label:errors.join(' · '),hidden:!errors.length});}
  }
  function renderDetail(){history.hidden=Boolean(selected);detail.hidden=!selected;if(!selected){replace(detail,[]);detailID=null;display=null;renderErrors();return;}ui.batch(()=>{
    const run=selected;
    if(detailID!==run.id){
      full=false;detailID=run.id;
      display={heading:ui.heading(runPurpose(run),3),command:typed(ui.code(''),'RunDetailCommand'),fullCommand:ui.code(''),metadata:typed(ui.text(''),'RunTechnical'),state:ui.badge(''),times:ui.text(''),cursor:ui.text(''),error:ui.text('',{role:'status',tone:'warning',hidden:true}),output:ui.code(''),follow:ui.button('Pause following',toggleFollow),next:ui.button('Next output page',()=>readLog(true)),lost:ui.text('Reporter lost; process completion is unknown',{tone:'warning',hidden:true})};
      display.output.set({language:''});display.command.set({language:''});display.fullCommand.set({language:''});
      display.expand=ui.button('Fullscreen log',()=>setFull(!full));
      display.exitHint=ui.text('Escape returns to the run details',{hidden:true});
      display.fold=ui.disclosure('Saved log output',[display.cursor,display.lost,display.output],{open:true});
      display.transcript=ui.stack([ui.heading(runPurpose(run)+' · Log output',3),ui.toolbar('Run log controls',[display.follow,display.expand,ui.button('Read from start',readStart),display.next]),display.exitHint,display.error,ui.text('Folding hides the transcript; capture and following continue.'),display.fold]);
      ui.put?.(display.transcript.iri,RDF_TYPE,ARM+'RunTranscript',true);
      ui.put?.(display.output.iri,'http://www.w3.org/1999/02/22-rdf-syntax-ns#type','https://arm.local/ui#RunOutput',true);
      ui.put?.(display.output.iri,'https://semblance.dev/ns/component#block',true);
      display.technical=ui.disclosure('Details',[ui.text('Run: '+label(run)+' · ID: '+run.id),display.fullCommand,display.metadata,display.times,...(run.lease_id?[ui.text('Lease ID: '+run.lease_id),ui.button('Inspect associated lease',()=>onLease(run.lease_id))]:[ui.text('Observation only · no resource lease')])],{open:false});
      replace(detail,[ui.button('Back to runs',close,{emphasis:'Quiet'}),display.heading,display.command,display.state,display.technical,display.transcript]);
    }
    display.command.set({value:runCommand(run,150)});display.fullCommand.set({value:JSON.stringify(run.command??[])});
    display.metadata.set({label:`Agent: ${run.agent||'—'} · Reason: ${run.reason||'—'} · Host: ${run.host||'—'} · Directory: ${run.cwd||'—'} · State: ${run.state||'—'} · Outcome: ${run.outcome||'—'} · Exit code: ${run.exit_code??'—'}`});
    display.state.set({label:runStatus(run).label,tone:runStatus(run).tone});
    display.times.set({label:`Created: ${run.created_at||'—'} · Started: ${run.started_at||'—'} · Ended: ${run.ended_at||'—'} · Last report: ${run.last_reported_at||'—'}`});
    display.follow.set({label:following?'Pause following':'Follow output'});
    display.next.disabled=following||done||!connected();
    display.cursor.set({label:`${!connected()?'Disconnected · cached output · ':listError||logError?'Stale output · ':''}${following?'Following':'Paused'} · read through byte ${offset}${done?' · end of saved log':''}${displayCut?' · display shows latest 256 KiB; read from start to inspect earlier output':''}${run.truncated?' · saved log reached its retention limit':''}`});
    display.lost.set({hidden:run.outcome!=='reporter_lost'});
    display.output.set({value:text||'(No output yet)'});renderErrors();
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
    const id=selected?.id;setFull(false,false);retire();selected=null;logError='';renderDetail();
    const opener=listRows.get(id)?.button;
    (opener&&!opener.disposed&&opener.isConnected!==false?opener:filter).focus();schedule();
  }
  function open(run){setFull(false,false);retire();selected=run;logError='';offset=0;text='';done=false;following=true;displayCut=false;decoder=new TextDecoder();renderDetail();display.heading.focus();return refresh();}
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
      logError='';offset=next;selected=page.run;done=drained;renderDetail();if(following&&bytes.length&&display.fold.open)display.output.scrollEnd?.();
      // One bounded page per task keeps rendering and pause controls responsive.
      catchup=bytes.length>0&&following&&!done&&offset<Number(page.run.log_bytes);
    }catch(error){if(cut===generation&&source===context()){logError=error.message;renderDetail();}}finally{if(cut===generation){logBusy=false;if(catchup)scheduleLog();}}
  }
  async function refresh(){if(disposed||!active||busy){schedule();return;}if(!connected()){status.set({label:'Disconnected · cached runs and output are stale'});renderDetail();schedule();return;}busy=true;const cut=generation,source=context();try{
    const inventory=await call('runs');if(disposed||cut!==generation||source!==context()||!active)return;listError='';runs=inventory??[];renderList();if(selected){const current=runs.find(r=>r.id===selected.id);if(current){selected=current;renderDetail();}}await readLog();
  }catch(error){if(cut===generation&&source===context()){listError=error.message;renderDetail();}}finally{if(cut===generation)busy=false;schedule();}}
  return {refresh,open,get selected(){return selected;},setActive(value){if(active===value)return;if(!value)setFull(false,false);active=value;retire();if(active)refresh();},updateConnection(){retire();renderList();renderDetail();if(active)refresh();},reset(){setFull(false,false);retire();listError=logError='';runs=[];selected=null;renderList();renderDetail();},dispose(){setFull(false,false);disposed=true;active=false;retire();unwatch?.();}};
}
