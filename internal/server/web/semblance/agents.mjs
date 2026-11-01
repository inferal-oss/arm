import {agentResources,participationLabel,contributionFreshness} from './agent-status-model.mjs';
import {createAgentReportWidget} from './agent-report-widget.mjs';

// Stock components own navigation, controls and detachable window lifecycle.
// The one foreign island contains only the sandboxed static report iframe.
export function createAgentsConsole({ui,root,call,connected,context,onLease,onRun,onNavigate,location=globalThis.location,timers=globalThis}) {
 let rows=[],offset=0,total=0,active=false,disposed=false,busy=false,epoch=0,timer=null,config=null,scope='',standalone=null;
 let resources={leases:[],runs:[]},localHost='',review=null,reviewDialog=null,sequence=0;
 const views=new Map(),selected=new Set(),cards=new Map(),ownedJobs=new Set();
 const warning=ui.text('',{role:'status'}),count=ui.text(''),inventory=ui.stack(),windows=ui.stack(),page=ui.stack();
 const filter=ui.field('Filter agents',{type:'search'},render),mode=ui.select('Registration state',[{value:'all',label:'All retained agents'},{value:'current',label:'Current registrations'},{value:'dormant',label:'Idle or historical registrations'}],{},render);
 const previous=ui.button('Previous agents',()=>{offset=Math.max(0,offset-16);selected.clear();refresh();}),next=ui.button('Next agents',()=>{offset+=16;selected.clear();refresh();});
 const refreshButton=ui.button('Refresh observations',()=>refresh()),requestButton=ui.button('Review contribution requests',reviewRequests);
 const setupText=ui.text(''),setupDetails=ui.disclosure('Project status setup preferences',[setupText],{open:false});setupDetails.hidden=true;
 const list=ui.stack([ui.text('Agent accounts and observed resources have separate provenance. Idle, expired or missing replies never prove process exit.'),setupDetails,ui.toolbar('Agent inventory',[refreshButton,requestButton]),filter,mode,count,warning,inventory,ui.toolbar('Agent pages',[previous,next])]);
 root.replace(list,page,windows);
 function current(cut,source){return !disposed&&cut===epoch&&source===context()&&connected();}
 const observing=()=>active||[...views.values()].some(view=>view.window?.detached);
 function retire(){epoch++;busy=false;if(timer!==null)timers.clearTimeout(timer);timer=null;}
 function schedule(){if(!disposed&&observing()&&connected()&&timer===null)timer=timers.setTimeout(()=>{timer=null;refresh();},5000);}
 function followingChanged(){if(!observing()&&timer!==null){timers.clearTimeout(timer);timer=null;}else schedule();}
 function workspace(){ui.batch(()=>{page.replace(...(standalone&&views.has(standalone)?[views.get(standalone).root]:[]));windows.replace(...[...views.values()].filter(v=>v.id!==standalone).map(v=>v.root));});}
 function render(){if(disposed)return;ui.batch(()=>{
  workspace();
  list.hidden=Boolean(standalone);page.hidden=!standalone;
  const text=String(filter.value||'').toLowerCase(),shown=rows.filter(row=>{
   const contact=row.identity.contact;return (mode.value!=='current'||row.current)&&(mode.value!=='dormant'||!row.current||contact.idle)&&`${contact.id} ${contact.adapter} ${contact.cwd} ${row.identity.host}`.toLowerCase().includes(text);
  });
  count.set({label:`${offset+1>total?0:offset+1}–${Math.min(offset+16,total)} of ${total} retained identities · ${scope||'Local authority'} · ${config?.enabled?'Summarizer enabled: '+config.model:'Summarizer disabled; participation and observations remain available'}`});
  previous.disabled=offset===0||!connected();next.disabled=offset+16>=total||!connected();refreshButton.disabled=!connected();requestButton.disabled=!connected()||selected.size===0;
  const keep=new Set();for(const row of shown){keep.add(row.id);let card=cards.get(row.id);const c=row.identity.contact,identity={...c,host:row.identity.host};
   if(!card){const check=ui.check('Select '+c.id,{checked:selected.has(row.id)},()=>{if(check.checked)selected.add(row.id);else selected.delete(row.id);requestButton.disabled=!connected()||!selected.size;});const title=ui.heading(c.id,3),facts=ui.text(''),status=ui.text(''),counts=ui.text(''),open=ui.button('Open status window',()=>openStatus(row.id)),body=ui.stack([check,title,facts,status,counts,open]);card={body,facts,status,counts,check};cards.set(row.id,card);}
   const observed=agentResources(identity,resources);
   card.facts.set({label:`${c.adapter} · ${row.identity.host===localHost?'Local host':row.identity.host} · ${c.cwd?.split('/').filter(Boolean).at(-1)||'Project not recorded'} · ${row.current?(c.idle?'Declared idle · activity unknown':'Current registration · activity unknown'):'Historical registration · activity unknown'}`});
   card.status.set({label:participationLabel(row.latest)+(row.latest?.reply_observed_at?' · Contribution observed '+new Date(row.latest.reply_observed_at).toLocaleString():' · No contribution observed')});card.counts.set({label:`${observed.leases.length} label-associated leases · ${observed.runs.length} label-associated runs · legacy attribution; exact historical ownership unavailable`});
   card.check.disabled=!connected()||!row.current;
  }
  for(const [id,card]of cards)if(!keep.has(id)){cards.delete(id);card.body.dispose();}
  inventory.replace(...shown.map(row=>cards.get(row.id).body));
 });}
 async function refresh(){
  if(disposed||busy||!connected()||!observing())return;schedule();busy=true;const cut=epoch,source=context();
  try{const result=await call('agent_status_list',{offset});if(!current(cut,source)||!observing())return;rows=result.rows??[];total=result.total??0;config=result.config;scope=result.scope;const setup=result.project_setup??{},projects=setup.projects??[];setupDetails.hidden=!projects.length&&!setup.diagnostic;setupText.set({label:[...projects.map(p=>`${p.project}: setup preference ${p.setup?'on':'off'} · saved revision ${p.policy_revision} · ${p.provenance}`),setup.diagnostic,'Setup preferences do not enable providers or enroll agents.'].filter(Boolean).join('\n')});warning.set({label:result.capacity_reached?'Identity capacity reached; inventory is incomplete. No retained evidence was discarded.':''});render();
   if(observing()){try{const runs=await call('runs');if(!current(cut,source)||!observing())return;resources.runs=runs??[];render();}catch(error){if(current(cut,source))warning.set({label:'Run observations unavailable: '+error.message});}}
   for(const view of views.values()){if(!current(cut,source)||!observing())return;if(active||view.window.detached)await load(view,cut,source);}
  }catch(error){if(current(cut,source))warning.set({label:error.message});}finally{if(cut===epoch)busy=false;schedule();}
 }
 function closeView(id){const v=views.get(id);if(!v)return;v.report.clear();v.window?.dispose();v.root.dispose();views.delete(id);if(standalone===id){standalone=null;onNavigate({view:'agents',lease:null});}followingChanged();render();}
 function renderResources(view){
  const children=[],size=12;
  for(const kind of ['leases','runs']){
   const records=view.observed[kind],total=records.length,maxPage=Math.max(0,Math.ceil(total/size)-1);
   const page=view.resourcePages[kind]=Math.min(view.resourcePages[kind],maxPage),start=page*size,end=Math.min(start+size,total);
   const previous=ui.button('Previous '+kind,()=>{view.resourcePages[kind]--;renderResources(view);}),next=ui.button('Next '+kind,()=>{view.resourcePages[kind]++;renderResources(view);});
   previous.disabled=page===0;next.disabled=page===maxPage;
   children.push(ui.heading(kind==='leases'?'Leases':'Runs',4),ui.text(`${total?start+1:0}–${end} of ${total} label-associated ${kind} · legacy attribution; exact historical ownership unavailable`));
   for(const {record}of records.slice(start,end))children.push(ui.button(`${record.request?.reason||record.reason||record.id} · ${record.state}`,()=>kind==='leases'?onLease(record.id):onRun(record)));
   children.push(ui.toolbar(kind==='leases'?'Lease pages':'Run pages',[previous,next]));
  }
  const old=[...view.resourceBox.children];view.resourceBox.replace(...children);for(const h of old)h.dispose();
 }
 async function openStatus(id){
  let view=views.get(id);if(view){view.window?.reveal();view.root.focus();return view;}
  if(views.size>=8){warning.set({label:'Eight status windows are open. Close one before opening another.'});return;}
  const report=createAgentReportWidget(''),island=ui.island('agent-report-'+(++sequence),report.widget),title=ui.heading('Agent status',3),state=ui.text('Loading…'),facts=ui.descriptions([]),resourceBox=ui.stack(),account=ui.code(''),accountFold=ui.disclosure('Owning agent contribution',[account],{open:false}),error=ui.text('',{role:'status'});
  const body=ui.stack(),container=ui.stack().retain();view={id,report,island,title,state,facts,resourceBox,account,error,root:container,body,latest:null,reportID:null,observed:{leases:[],runs:[]},resourcePages:{leases:0,runs:0}};
  const collect=ui.button('Check agent reply',()=>act(view,'collect')),generate=ui.button('Review summary generation',()=>reviewGeneration(view)),cancel=ui.button('Cancel current attempt',()=>act(view,'cancel')),full=ui.button('Open full page',()=>{standalone=id;view.window.dock();onNavigate({view:'agents',agent:id,lease:null});render();});
  const pageLink=ui.component('Link',{label:'Standalone page link'}),address=new URL(location.href);address.search='';address.hash='';address.searchParams.set('view','agents');address.searchParams.set('agent',id);ui.put(pageLink.iri,'https://semblance.dev/ns/component#href',address.pathname+address.search);
  const details=ui.disclosure('Contribution and report details',[facts,accountFold],{open:false});
  view.controls=[collect,generate,cancel,full];view.pageLink=pageLink;body.replace(ui.toolbar('Status navigation',[ui.button('Close status',()=>closeView(id)),full,pageLink]),title,state,error,island,ui.toolbar('Agent report actions',[collect,generate,cancel]),details,ui.heading('Observed leases and runs',3),resourceBox);
  view.window=ui.detachable('Agent status',body);view.window.on('detach',()=>{if(views.get(id)===view){title.hidden=view.window.detached;followingChanged();}});container.replace(view.window);views.set(id,view);workspace();if(standalone!==id)view.window.popOut();
  await load(view,epoch,context());schedule();return view;
 }
 async function load(view,cut,source){
  try{const result=await call('agent_status_read',{id:view.id});if(!current(cut,source)||views.get(view.id)!==view)return;
   view.latest=result.latest;const c=result.entry.identity.contact,identity={...c,host:result.entry.identity.host},fresh=contributionFreshness(result.report),a=result.latest;
   view.title.set({label:c.id});view.title.hidden=view.window.detached;view.window.tab?.set({label:c.id+' · Status'});view.state.set({label:`${participationLabel(a)} · ${fresh.stale?'Stale or absent report':'Recent report'} · ${fresh.at}`});
   view.facts.setPairs([['Host',identity.host===localHost?'Local host':identity.host],['Project',c.cwd||'Not recorded'],['Contribution observed',a?.reply_observed_at??'No contribution'],['Report model',result.report?.model??'No generated report'],['Report request',result.report?.id??'None'],['Latest attempt',a?.id??'None'],['Billing',a?.usage?.billing??'Not reported']]);
   view.error.set({label:a?.error??''});view.account.set({value:a?.account?JSON.stringify(a.account,null,2):'No structured contribution received from this exact agent registration.'});
   if(view.reportID!==result.report?.id){view.reportID=result.report?.id;view.report.update(result.report?.html??'');}view.island.hidden=!result.report?.html;
   view.observed=agentResources(identity,resources);renderResources(view);
   view.controls[0].disabled=!connected()||!a;view.controls[1].disabled=!connected()||!config?.enabled||!['contributed','late_reply'].includes(a?.state);view.controls[2].disabled=!connected()||!a||['ready','cancelled','superseded'].includes(a.state);
  }catch(error){if(current(cut,source)&&views.get(view.id)===view)view.error.set({label:error.message});}
 }
 async function act(view,op){const a=view.latest;if(!a||!connected())return;const cut=epoch,source=context();try{const result=await call('agent_status_'+op,{id:a.id});if(current(cut,source)){await load(view,cut,source);if(result.note)view.error.set({label:result.note});}}catch(error){if(current(cut,source))view.error.set({label:error.message});}}
 function dismissReview(){review=null;const dialog=reviewDialog;reviewDialog=null;dialog?.dispose();}
 function reviewGeneration(view){
  if(!view.latest||!connected())return;dismissReview();const attempt=structuredClone(view.latest),cut=epoch,source=context();
  const confirm=ui.button('Generate this summary',async()=>{confirm.disabled=true;try{ownedJobs.add(attempt.id);await call('agent_status_generate',{id:attempt.id,confirm:true});if(current(cut,source)){dismissReview();await load(view,cut,source);}}catch(error){if(current(cut,source))view.error.set({label:error.message});dismissReview();}});
  reviewDialog=ui.dialog('Review one summary call',[ui.text(`${attempt.provider} · ${attempt.model}. Only the structured agent account is sent. Up to 12 KiB input, 2048 generated tokens, 60 seconds; one call with no fallback or retry. Estimated attempt budget: $${config?.estimated_attempt_usd}. Provider billing can remain unknown after cancellation.`),ui.code(JSON.stringify(attempt.account,null,2))],[confirm,ui.button('Back',dismissReview)]);reviewDialog.showModal();
 }
 function reviewRequests(){
  dismissReview();const targets=rows.filter(r=>selected.has(r.id)&&r.current).map(r=>({id:r.id,contact:structuredClone(r.identity.contact),key:crypto.randomUUID()}));if(!targets.length)return;
  review=targets;const cut=epoch,source=context(),reviewTargets=targets;
  const confirm=ui.button('Request these contributions',async()=>{confirm.disabled=true;let success=false;try{for(const target of reviewTargets){if(!current(cut,source))return;const attempt=await call('agent_status_request',{...target,confirm:true});ownedJobs.add(attempt.id);}success=true;}catch(error){if(current(cut,source)){warning.set({label:'Request outcome may be unknown. The original recipients and keys remain in this review: '+error.message});confirm.set({label:'Retry exact reviewed requests'});confirm.disabled=false;}}finally{if(current(cut,source)&&success){dismissReview();selected.clear();refresh();}}});
  reviewDialog=ui.dialog('Review exact contribution recipients',[ui.text('Requests use existing operator messaging. Native delivery may wake the original agent and incur its normal session costs. Polling agents answer when they next poll. Summary generation is a separate explicit action.'),...targets.map(t=>ui.json(t,'Exact recipient and retained request key'))],[confirm,ui.button('Back',dismissReview)]);reviewDialog.showModal();
 }
 return {
  setActive(value){active=Boolean(value);followingChanged();if(active)refresh();},
  update(data){resources.leases=data.leases??[];localHost=data.host??'';render();},
  updateConnection(){retire();for(const v of views.values()){v.error.set({label:connected()?'':'Disconnected · cached report is stale'});for(const c of v.controls)c.disabled=!connected();}render();if(connected())refresh();},
  async applyRoute(route){if(route.agent){standalone=route.agent;const v=await openStatus(route.agent);if(v&&standalone===route.agent){v.window.dock();workspace();}render();}else if(standalone){const v=views.get(standalone);standalone=null;workspace();render();}},
  reset(){retire();dismissReview();if(connected())call('agent_status_cancel',{cancel_owned:true,confirm:true}).catch(()=>{});ownedJobs.clear();for(const v of views.values()){v.report.clear();v.window.dispose();v.root.dispose();}views.clear();for(const c of cards.values())c.body.dispose();cards.clear();selected.clear();rows=[];total=0;offset=0;standalone=null;resources={leases:[],runs:[]};localHost='';setupDetails.hidden=true;setupText.set({label:''});render();},
  dispose(){this.reset();disposed=true;},
 };
}
