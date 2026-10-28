import {runStatus,runCommand,runPurpose,compactRunText} from './run-list.mjs';
const WINDOW=256*1024;
const FS='https://semblance.dev/ns/fullscreen#fullViewport',RDF_TYPE='http://www.w3.org/1999/02/22-rdf-syntax-ns#type',ARM='https://arm.local/ui#';
export function cleanRunText(value){
  return value.replace(/\r\n/g,'\n').replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f\u2028\u2029\u202a-\u202e\u2066-\u2069]/g,c=>`\\u${c.charCodeAt(0).toString(16).padStart(4,'0')}`);
}
// One owned transcript and one byte cursor per run. The console supplies shared IO.
export function createRunLogView({ui,record,request,connected,context,onRead,onDetach,onClose,onFullscreen,onLayout,onLease}){
  let run=record,active=false,disposed=false,generation=0,logBusy=false,logTimer=null,offset=0,text='',done=false,following=true,decoder=new TextDecoder(),displayCut=false,listError='',logError='',full=false,foldBeforeFull=true,display,api;
  const root=ui.stack(),label=r=>r.name||r.id;
  const title=()=>`${run.agent?compactRunText(run.agent,32)+' · ':''}${runPurpose(run)} · Log output`;
  const typed=(component,name)=>{ui.put?.(component.iri,RDF_TYPE,ARM+name,true);return component;};
  display={heading:ui.heading(runPurpose(run),3),command:typed(ui.code(''),'RunDetailCommand'),fullCommand:ui.code(''),metadata:typed(ui.text(''),'RunTechnical'),state:ui.badge(''),times:ui.text(''),cursor:ui.text(''),error:ui.text('',{role:'status',tone:'warning',hidden:true}),output:ui.code(''),follow:ui.button('Pause following',toggleFollow),next:ui.button('Next output page',()=>onRead(api,true)),lost:ui.text('Reporter lost; process completion is unknown',{tone:'warning',hidden:true})};
  display.output.set({language:''});display.command.set({language:''});display.fullCommand.set({language:''});
  display.expand=ui.button('Fullscreen log',()=>setFull(!full));
  display.exitHint=ui.text('Escape returns to the run details',{hidden:true});
  display.fold=ui.disclosure('Saved log output',[display.cursor,display.lost,display.output],{open:true});
  display.layout=ui.iconButton('Tile log windows','▦',onLayout);display.close=ui.iconButton('Close log','×',()=>onClose(api));
  display.transcriptHeading=ui.heading(title(),3);display.foldHint=ui.text('Folding hides the transcript; capture and following continue.');
  display.transcript=ui.stack([display.transcriptHeading,ui.toolbar('Run log controls',[display.follow,display.expand,ui.button('Read from start',readStart),display.next,display.layout,display.close]),display.exitHint,display.error,display.foldHint,display.fold]);
  ui.put?.(display.transcript.iri,RDF_TYPE,ARM+'RunTranscript',true);
  ui.put?.(display.output.iri,'http://www.w3.org/1999/02/22-rdf-syntax-ns#type','https://arm.local/ui#RunOutput',true);
  ui.put?.(display.output.iri,'https://semblance.dev/ns/component#block',true);
  display.technical=ui.disclosure('Details',[ui.text('Run: '+label(run)+' · ID: '+run.id),display.fullCommand,display.metadata,display.times,...(run.lease_id?[ui.text('Lease ID: '+run.lease_id),ui.button('Inspect associated lease',()=>onLease(run.lease_id))]:[ui.text('Observation only · no resource lease')])],{open:false});
  display.window=ui.detachable(title(),display.transcript);
  display.window.on('detach',()=>{setFull(false,false);renderPresentation();onDetach(api);if(following&&display.fold.open)display.output.scrollEnd?.();});
  display.window.on('layout',()=>{if(following&&display.fold.open)display.output.scrollEnd?.();});
  root.replace(ui.button('Back to runs',()=>onClose(api),{emphasis:'Quiet'}),display.heading,display.command,display.state,display.technical,ui.text('Drag the log tab out, use its pop-out control, or press Shift+Enter. Closing its window returns the log here.'),display.window);
  function renderErrors(){
    if(display){const errors=[!connected()?'Disconnected; cached output is stale':'',listError?'Run list unavailable: '+listError:'',logError?'Output unavailable: '+logError:''].filter(Boolean);display.error.set({label:errors.join(' · '),hidden:!errors.length});}
  }
  function render(){ui.batch(()=>{
    display.window.tab?.set({label:title()});display.transcriptHeading.set({label:title()});
    display.command.set({value:runCommand(run,150)});display.fullCommand.set({value:JSON.stringify(run.command??[])});
    display.metadata.set({label:`Agent: ${run.agent||'—'} · Reason: ${run.reason||'—'} · Host: ${run.host||'—'} · Directory: ${run.cwd||'—'} · State: ${run.state||'—'} · Outcome: ${run.outcome||'—'} · Exit code: ${run.exit_code??'—'}`});
    display.state.set({label:runStatus(run).label,tone:runStatus(run).tone});
    display.times.set({label:`Created: ${run.created_at||'—'} · Started: ${run.started_at||'—'} · Ended: ${run.ended_at||'—'} · Last report: ${run.last_reported_at||'—'}`});
    display.follow.set({label:following?'Pause following':'Follow output'});
    display.next.disabled=following||done||!connected();
    display.cursor.set({label:`${!connected()?'Disconnected · cached output · ':listError||logError?'Stale output · ':''}${following?'Following':'Paused'} · ${runStatus(run).label} · read through byte ${offset}${done?' · end of saved log':''}${displayCut?' · display shows latest 256 KiB; read from start to inspect earlier output':''}${run.truncated?' · saved log reached its retention limit':''}`});
    display.lost.set({hidden:run.outcome!=='reporter_lost'});
    display.output.set({value:text||'(No output yet)'});renderErrors();
  });}
  function renderPresentation(){
    if(!display)return;const detached=Boolean(display.window?.detached);
    ui.batch(()=>{ui.put?.(display.transcript.iri,RDF_TYPE,ARM+(detached?'RunTranscriptWindow':'RunTranscript'),true);display.transcriptHeading.hidden=display.foldHint.hidden=detached&&!full;});
  }
  function setFull(enabled,restoreFocus=true){
    if(!display||full===Boolean(enabled))return;
    if(enabled){onFullscreen(api);foldBeforeFull=display.fold.open;display.fold.open=true;}
    full=Boolean(enabled);
    display.transcript.set({fullViewport:full});
    ui.put?.(display.output.iri,RDF_TYPE,ARM+(full?'RunOutputFull':'RunOutput'),true);
    display.expand.set({label:full?'Exit fullscreen':'Fullscreen log'});display.exitHint.hidden=!full;renderPresentation();
    if(!full)display.fold.open=foldBeforeFull;
    if(restoreFocus)display.expand.focus();
    if(following&&display.fold.open)display.output.scrollEnd?.();
  }
  // Native Escape retracts the stock Fullscreen graph fact. Reflect that same
  // transition in controls and focus, without creating another log reader.
  const unwatch=ui.app?.runtime?.onInput((graph,delta)=>{
    const current=display;
    if(graph!==undefined||!current||![...delta.add,...delta.remove].some(q=>q.subject.value===current.transcript.iri&&q.predicate.value===FS))return;
    queueMicrotask(()=>{
      if(disposed||!full)return;
      const store=ui.app.store,f=store.factory;
      const enabled=store.match(f.namedNode(current.transcript.iri),f.namedNode(FS),null,f.defaultGraph()).some(q=>q.object.value==='true');
      if(!enabled)setFull(false);
    });
  });
  function retire(){generation++;logBusy=false;if(logTimer!==null)clearTimeout(logTimer);logTimer=null;}
  function toggleFollow(){retire();following=!following;render();return onRead(api,false);}
  function readStart(){retire();offset=0;text='';done=false;following=false;displayCut=false;decoder=new TextDecoder();return onRead(api,true);}
  function scheduleLog(){
    if(!active||disposed||!connected()||!following||done||logTimer!==null)return;
    const cut=generation,source=context();
    logTimer=setTimeout(()=>{logTimer=null;if(cut===generation&&source===context())onRead(api,false);},0);
  }
  async function readLog(manual=false){
    if(disposed||!active||!run||!connected()||(!manual&&(!following||done))||logBusy)return;
    logBusy=true;let catchup=false;
    const id=run.id,cut=generation,source=context(),start=offset;
    try{const page=await request('run_output',{id,offset:start},()=>!disposed&&active&&cut===generation&&source===context());if(disposed||cut!==generation||source!==context()||!active||run.id!==id)return;
      if(page.run?.id!==id||Number(page.offset)!==start)throw Error('Run output identity or cursor changed');
      const bytes=Uint8Array.from(atob(page.data??''),c=>c.charCodeAt(0)),next=Number(page.next_offset);
      if(!Number.isSafeInteger(next)||next!==start+bytes.length)throw Error('Invalid run output cursor');
      const drained=Boolean(page.done)&&next>=Number(page.run.log_bytes);
      text+=cleanRunText(decoder.decode(bytes,{stream:!drained}));if(text.length>WINDOW){text=text.slice(-WINDOW);if(/[\udc00-\udfff]/.test(text[0]))text=text.slice(1);displayCut=true;}
      logError='';offset=next;run=page.run;done=drained;render();if(following&&bytes.length&&display.fold.open)display.output.scrollEnd?.();
      // One bounded page per task keeps rendering and pause controls responsive.
      catchup=bytes.length>0&&following&&!done&&offset<Number(page.run.log_bytes);
    }catch(error){if(cut===generation&&source===context()){logError=error.message;render();}}finally{if(cut===generation){logBusy=false;if(catchup)scheduleLog();}}
  }
  api={root,window:display.window,get run(){return run;},get detached(){return display.window.detached;},get active(){return active;},
    read:readLog,update(next,error=''){if(disposed)return;if(next?.id===run.id)run=next;listError=error;render();},
    setActive(value){if(active===value)return;active=value;if(!active){setFull(false,false);retire();}},
    invalidate(){retire();render();},exitFullscreen(){setFull(false,false);},
    focus(){display.window.reveal?.();display.follow.focus();if(following&&display.fold.open)display.output.scrollEnd?.();},focusDetails(){display.heading.focus();},
    setLayout(tiled){display.layout.set({label:tiled?'Float log windows':'Tile log windows'});},
    dispose(){if(disposed)return;setFull(false,false);disposed=true;active=false;retire();unwatch?.();display.window.dispose();root.dispose();}
  };
  render();return api;
}
