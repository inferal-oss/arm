import {jsonView} from './json-view.mjs';
import {bytes,quantity} from './core.mjs';
import {grantDescription} from './mesh-model.mjs';
import {leaseStateLabel} from './lease-state.mjs';
import {cpuBudgetKnown,cpuBudgetForView,remoteCPUBudget,cpuCores} from './metric-help.mjs';
export function remoteResourceLabel(resource){
 if(resource.kind==='port'){const match=String(resource.id??'').match(/\/(tcp[46]|udp[46])\/([^/]+)$/);return match?`${match[1].toUpperCase()} port · ${match[2]}`:'Port reservation';}
 return ({cpu:'Live CPU headroom',memory:'Memory budget',disk:'Disk budget',custom:'Custom resource'})[resource.kind]??'Resource budget';
}
export function createRemoteNodeDashboard({root,feed,call,context,document=root.ownerDocument,onError=()=>{}}){
 const el=(tag,cls='',text=null)=>{const n=document.createElement(tag);n.className=cls;if(text!==null)n.textContent=text;return n;};
 let currentView='resources',snapshot=null,metadata=null,scope=null,busy=false,stale=true,detail=null,metadataAt=0,lastMetadata=null,lastView='',held=false;
 let statusReceived=0,cpuCards=[];
 const metadataExpansion=new Map();
 function updateCPU(){for(const {resource,budget,received,amount,summary,note} of cpuCards){const cpu=cpuBudgetForView(budget,{connected:!stale,elapsedMS:performance.now()-received}),known=cpuBudgetKnown(cpu);amount.textContent=cpuCores(known?cpu.available_cores:null);summary.textContent=`Live headroom ${cpuCores(known?cpu.capacity_cores:null)} · promised ${cpuCores(cpu?.reserved_cores??resource.reserved)} · shortfall ${cpuCores(known?cpu.deficit_cores:null)}`;note.textContent=known?'Other apps share this CPU. Full promises remain charged.':'CPU headroom is stale or unavailable. New CPU grants wait; promises stay charged.';}}
 const banner=el('div','remote-authority'),content=el('div'),feedback=el('p','mesh-inline-notice');feedback.setAttribute('role','status');root.replaceChildren(banner,feedback,content);
 feed.replaceChildren(el('header','feed-heading','DESTINATION CONTEXT'),el('p','mesh-note','Live event and monitor streams are unavailable over this peer grant. No local activity is shown in this window.'),el('p','mesh-mono',context.host));
 content.addEventListener('focusout',()=>queueMicrotask(()=>{if(held&&!content.contains(document.activeElement)){held=false;render();}}));
 function render(){
  if(content.contains(document.activeElement)){held=true;feedback.textContent='View held while reading a control. Newer destination observations are available after focus leaves this panel.';return;}
  if(currentView==='host'&&lastView==='host'&&lastMetadata===metadata)return;
  lastView=currentView;lastMetadata=metadata;
  banner.replaceChildren(el('p','eyebrow','REMOTE NODE · READ-ONLY GATEWAY'),el('h2','',metadata?.hostname||context.host),el('p','mesh-mono',context.host),el('p','mesh-muted',`${metadata?.platform?.os??'Unknown OS'} / ${metadata?.platform?.process_architecture??'unknown architecture'} · through source ${context.source_host}`),el('p','mesh-note',`Current destination permissions: ${scope?grantDescription(scope.grant):'Not yet read'}. This window currently supports inspection only.`));
  content.replaceChildren();
  if(currentView==='host') {if(metadata)content.append(jsonView(metadata,{label:'Reported node metadata',open:true,document,expansion:metadataExpansion}));else content.append(el('p','empty','Metadata unavailable.'));return;}
  if(currentView==='leases'){
   content.append(el('p','mesh-note',scope?.grant?.permissions?.includes('observe-all')?'Lease inventory includes the destination’s currently authorized full view. Detail lookup remains limited to this source’s own claims.':'Only leases attributed to this source node are visible when observe-own is granted.'));
   const list=el('div','mesh-operation-list');for(const lease of (snapshot?.leases??[]).slice(0,200)){const row=el('button','mesh-operation-row');row.type='button';const text=el('div');text.append(el('strong','',lease.request?.reason||'Lease'),el('span','mesh-mono',lease.id));row.append(el('span','badge',leaseStateLabel(lease)),text,el('span','mesh-muted',new Date(lease.expires_at).toLocaleString()));row.addEventListener('click',()=>inspect(lease.id,row));list.append(row);}content.append(list);if((snapshot?.leases?.length??0)>200)content.append(el('p','mesh-note','Showing the first 200 authorized leases. Use the CLI to inspect the complete destination inventory.'));if(!snapshot?.leases?.length)content.append(el('p','empty','No leases are visible under this destination grant.'));return;
  }
  const grid=el('div','mesh-nodes');cpuCards=[];
  for(const resource of snapshot?.resources??[]){
   const cardCPU=snapshot?.cpu,card=el('article','mesh-node'),amount=el('p','metric accent',resource.unit==='bytes'?bytes(resource.available):quantity(resource.available,resource.unit)),summary=el('p','mesh-muted',`Available · total ${resource.unit==='bytes'?bytes(resource.budget_total):quantity(resource.budget_total,resource.unit)} · reserved ${resource.unit==='bytes'?bytes(resource.reserved):quantity(resource.reserved,resource.unit)}`);
   card.append(el('p','eyebrow',resource.kind??'RESOURCE'),el('h3','',remoteResourceLabel(resource)),amount,summary);
   if(resource.kind==='cpu'){const note=el('p','mesh-note');card.append(note);cpuCards.push({resource,budget:remoteCPUBudget(cardCPU),received:statusReceived,amount,summary,note});}
   else if(resource.stale||resource.availability_known===false)card.append(el('p','mesh-note warning','Availability is stale or unknown.'));
   const technical=el('details','remote-resource-detail');technical.append(el('summary','','Scope and accounting details'));let loaded=false;technical.addEventListener('toggle',()=>{if(technical.open&&!loaded){loaded=true;technical.append(jsonView(resource.kind==='cpu'?{resource,cpu:cardCPU}:resource,{label:'Resource details',document}));}});card.append(technical);grid.append(card);
  }
  content.append(grid);updateCPU();
  if(snapshot?.memory)content.append(jsonView(snapshot.memory,{label:'Memory budget and observation freshness',document}));if(snapshot?.disk)content.append(jsonView(snapshot.disk,{label:'Disk budget and observation freshness',document}));
 }
 async function inspect(id,opener){
  if(detail?.open)detail.close();const d=detail=el('dialog','mesh-dialog');const head=el('header','mesh-dialog-head'),close=el('button','','Close');close.type='button';close.addEventListener('click',()=>d.close());head.append(el('h2','','Remote lease'),close);const body=el('div','mesh-dialog-body','Reading this exact destination lease…');d.append(head,body);document.body.append(d);d.addEventListener('close',()=>{d.remove();if(detail===d){detail=null;if(opener?.isConnected)opener.focus();else document.querySelector('nav button[aria-current]')?.focus();}});d.showModal();close.focus();
  try{const lease=await call('inspect',{id});if(!d.open)return;body.replaceChildren(el('p','mesh-note','Remote lease mutations are not implemented in this window. Use an explicitly keyed CLI request if the destination grants it.'),jsonView(lease,{label:'Destination lease',open:true,document}));}catch(e){if(d.open)body.textContent=e.message;}
 }
 async function refresh(){if(busy)return;busy=true;try{const info=await call('node_context');scope=info.access_scope;snapshot=await call('status');statusReceived=performance.now();if(!metadata||Date.now()-metadataAt>30000){try{metadata=await call('node_metadata');metadataAt=Date.now();}catch{}}stale=false;updateCPU();feedback.textContent=`Destination snapshot received ${new Date().toLocaleTimeString()}. Resource observation timestamps remain independent.`;render();}catch(e){stale=true;updateCPU();feedback.textContent=`STALE · ${e.message}. This window never falls back to localhost.`;onError(e);}finally{busy=false;}}
 return {refresh,updateFreshness:updateCPU,show(view){currentView=['resources','leases','host'].includes(view)?view:'resources';render();},disconnect(){stale=true;updateCPU();feedback.textContent='Disconnected · cached destination data is stale. No operations will be replayed.';},get stale(){return stale;}};
}
