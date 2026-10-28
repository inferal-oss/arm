import {currentRun,runStatus,compactRunText,runCommand,runPurpose,orderRuns,runWhen} from './run-list.mjs';
import {createRunLogView} from './run-log.mjs';
export {cleanRunText} from './run-log.mjs';
const RDF_TYPE='http://www.w3.org/1999/02/22-rdf-syntax-ns#type',ARM='https://arm.local/ui#',MAX_OPEN_LOGS=8;
export function createRunsConsole({ui,root,call,connected,context=()=>null,onLease=()=>{}}){
  let visible=false,disposed=false,epoch=0,timer=null,inventoryBusy=false,selected=null,runs=[],listError='',openError='',tiled=false,inFlight=0;
  const views=new Map(),waiting=[],listRows=new Map();
  const filter=ui.field('Search runs',{type:'search',placeholder:'Purpose, command or agent…'},renderList),status=ui.text(''),historyError=ui.text('',{role:'status',tone:'warning',hidden:true}),list=ui.stack(),detail=ui.stack();
  const watchCount=ui.text(''),layout=ui.button('Tile log windows',toggleLayout),controls=ui.toolbar('Open run logs',[watchCount,layout]),history=ui.stack([controls,filter,status,historyError,list]);
  ui.put?.(root.iri,RDF_TYPE,ARM+'RunSurface',true);root.replace(history,detail);list.set({role:'list'});
  const activeHeading=ui.heading('Current',3),pastHeading=ui.heading('History',3);activeHeading.hidden=pastHeading.hidden=true;list.replace(activeHeading,pastHeading);
  const typed=(component,name)=>{ui.put?.(component.iri,RDF_TYPE,ARM+name,true);return component;};
  const active=()=>visible||[...views.values()].some(view=>view.detached);
  // Share two real in-flight requests across inventory and every log. Retired
  // queued work is removed, while outstanding calls keep their slots until settled.
  function request(op,args,valid){return new Promise((resolve,reject)=>{waiting.push({op,args,valid,resolve,reject});drain();});}
  function drain(){
    for(let i=waiting.length-1;i>=0;i--)if(!waiting[i].valid()){waiting.splice(i,1)[0].resolve(undefined);}
    while(inFlight<2&&waiting.length){const task=waiting.shift();inFlight++;
      Promise.resolve().then(()=>task.valid()?call(task.op,task.args):undefined).then(task.resolve,task.reject).finally(()=>{inFlight--;drain();});
    }
  }
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
    historyError.set({label:openError|| (listError?'Run list unavailable: '+listError:''),hidden:!openError&&!listError});
    if(focusRow&&listRows.has(focusRow)&&!ui.activeComponent&&!focused.disposed&&focused.isConnected!==false)focused.focus();
  }
  function workspace(){
    ui.batch(()=>{history.hidden=Boolean(selected);detail.hidden=!views.size;detail.replace(...[...views.values()].map(view=>view.root));
      for(const view of views.values()){view.root.hidden=view!==selected;view.setActive(view.detached||visible&&view===selected);view.setLayout(tiled);}
      controls.hidden=!views.size;watchCount.set({label:`${views.size} log${views.size===1?'':'s'} open`});layout.hidden=[...views.values()].filter(v=>v.detached).length<2;layout.set({label:tiled?'Float log windows':'Tile log windows'});
    });
    drain();if(active())schedule();else if(timer!==null){clearTimeout(timer);timer=null;}
  }
  function remove(view,restoreFocus=false){
    if(!view||!views.has(view.run.id))return;const id=view.run.id;views.delete(id);if(selected===view)selected=null;view.dispose();openError='';
    if(!views.size){tiled=false;ui.tileWindows?.(false);}workspace();renderList();if(restoreFocus){if(selected)selected.focusDetails();else(listRows.get(id)?.button??filter).focus();}
  }
  function detached(view){
    if(!views.has(view.run.id))return;
    if(view.detached){if(selected===view)selected=null;}
    else {if(tiled){tiled=false;ui.tileWindows(false);}if(selected&&selected!==view)remove(selected);selected=view;}
    workspace();if(view.detached)view.focus();else if(visible)view.focusDetails();
  }
  function toggleLayout(){for(const view of views.values())view.exitFullscreen();tiled=!tiled;ui.tileWindows(tiled);workspace();}
  function read(view,manual=false){drain();return view.read(manual);}
  function open(run){
    if(disposed)return Promise.resolve();let view=views.get(run.id);
    if(view){if(view.detached)view.focus();else{selected=view;workspace();view.focusDetails();}return Promise.resolve();}
    if(selected)remove(selected);
    if(views.size>=MAX_OPEN_LOGS){openError='Eight logs are open. Close a log before opening another.';renderList();historyError.focus();return Promise.resolve();}
    view=createRunLogView({ui,record:run,request,connected,context,onRead:read,onDetach:detached,onClose:v=>remove(v,true),onFullscreen:v=>{for(const other of views.values())if(other!==v)other.exitFullscreen();},onLayout:toggleLayout,onLease});
    views.set(run.id,view);selected=view;openError='';workspace();view.focusDetails();return refresh();
  }
  function schedule(){if(active()&&!disposed&&timer===null)timer=setTimeout(()=>{timer=null;refresh();},1000);}
  async function refresh(){
    if(disposed||!active())return;
    if(!connected()){for(const view of views.values())view.update(null,listError);renderList();schedule();return;}
    const cut=epoch,source=context();
    if(!inventoryBusy){inventoryBusy=true;try{
      const inventory=await request('runs',undefined,()=>!disposed&&cut===epoch&&source===context()&&active());
      if(disposed||cut!==epoch||source!==context()||!active())return;
      runs=inventory??[];listError='';renderList();
      for(const view of views.values())view.update(runs.find(run=>run.id===view.run.id),listError);
    }catch(error){if(cut===epoch&&source===context()){listError=error.message;renderList();for(const view of views.values())view.update(null,listError);}}
    finally{inventoryBusy=false;}}
    try{await Promise.all([...views.values()].filter(view=>view.active).map(view=>read(view)));}finally{schedule();}
  }
  function reset(){epoch++;if(timer!==null)clearTimeout(timer);timer=null;selected=null;for(const view of views.values())view.dispose();views.clear();runs=[];listError=openError='';tiled=false;ui.tileWindows?.(false);workspace();renderList();}
  return {refresh,open,closeLog:id=>remove(views.get(id)),get selected(){return selected?.run??null;},get openRuns(){return [...views.values()].map(view=>view.run);},
    setActive(value){const next=Boolean(value);if(visible===next)return;visible=next;if(!visible)for(const view of views.values())view.exitFullscreen();workspace();if(visible)refresh();},
    updateConnection(){epoch++;for(const view of views.values())view.invalidate();drain();renderList();for(const view of views.values())view.update(null,listError);if(active())refresh();},
    reset,dispose(){if(disposed)return;disposed=true;reset();}
  };
}
