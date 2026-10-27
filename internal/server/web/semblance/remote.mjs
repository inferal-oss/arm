import {namedLabel} from '../resource-names.mjs';
import {bytes,quantity} from '../core.mjs';
import {grantDescription} from '../mesh-model.mjs';
import {leaseStateLabel} from '../lease-state.mjs';
import {budgetForView,cpuBudgetKnown,remoteCPUBudget,cpuCores,memoryBudgetKnown,diskBudgetKnown} from '../metric-help.mjs';

export function remoteResourceLabel(resource) {
  if(typeof resource.display_name==='string'&&resource.display_name.trim())return resource.display_name;
  if(resource.kind==='port') {
    const match=String(resource.id??'').match(/\/(tcp[46]|udp[46])\/([^/]+)$/);
    return match?`${match[1].toUpperCase()} port · ${match[2]}`:'Port reservation';
  }
  return ({cpu:'Live CPU headroom',memory:'Memory budget',disk:'Disk budget',custom:'Custom resource'})[resource.kind]??'Resource budget';
}
const nativeKinds=new Set(['cpu','memory','disk']);
const format=(resource,value)=>resource.unit==='bytes'?bytes(value):quantity(value,resource.unit);
const memoryBudget=budget=>budget&&({...budget,observation:{at:budget.observation_at,total_bytes:budget.total_bytes,available_bytes:budget.estimated_available_bytes,quality:budget.quality}});

/** Scoped, read-only destination projection; its host performs only bound reads. */
export function createRemoteNodeDashboard({ui,root,feed,call,context,onError=()=>{},clock={wall:()=>Date.now(),mono:()=>performance.now()}}) {
  let currentView='resources',snapshot=null,metadata=null,scope=null,stale=true,disposed=false;
  let generation=0,pending=null,statusReceived=0,metadataRequestedAt=null,lastMetadata=null,lastView='',held=false,detail=null;
  let resourceCards=[],leaseRows=[],freshnessMessage='';
  const banner=ui.stack(),feedback=ui.text('',{role:'status'}),content=ui.stack(),detailSlot=ui.stack();
  ui.batch(()=>{root.replace(banner,feedback,content,detailSlot);feed?.replace();});
  content.on('focusout',()=>queueMicrotask(()=>{
    if(!disposed&&held&&!content.contains(ui.activeComponent)){render();}
  }));
  function announce(label,remember=true) {if(remember)freshnessMessage=label;feedback.set({label});}
  function validateContext(info) {
    const reported=info?.context,access=info?.access_scope;
    if(!reported||reported.local!==false||reported.host!==context.host||reported.source_host!==context.source_host||reported.pair!==context.pair||
      reported.local_revision!==context.local_revision||reported.remote_revision!==context.remote_revision||reported.certificate_fingerprint!==context.certificate_fingerprint||info.read_only!==true||
      !Array.isArray(access?.grant?.permissions)||!access||access.target_host!==context.host||access.source_host!==context.source_host||access.pair!==context.pair||access.revision!==context.remote_revision)
      throw Error('Destination context differs from this exact selected node. Reopen it from Mesh.');
    return access;
  }
  function mayInspect(lease) {
    const permissions=scope?.grant?.permissions??[];
    return !disposed&&!stale&&(permissions.includes('observe-all')||permissions.includes('observe-own')&&lease.request?.origin_peer===context.source_host);
  }
  function updateFreshness() {
    if(disposed)return;
    ui.batch(()=>{
      for(const card of resourceCards) {
        const {resource,budget,received,amount,summary,note}=card;
        const aged=budgetForView(budget,{connected:!stale,elapsedMS:clock.mono()-received});
        if(resource.kind==='cpu') {
          const known=cpuBudgetKnown(aged)&&resource.availability_known!==false&&!resource.stale;
          amount.set({label:cpuCores(known?aged.available_cores:null)});
          summary.set({label:`Live headroom ${cpuCores(known?aged.capacity_cores:null)} · promised ${cpuCores(aged?.reserved_cores??resource.reserved)} · shortfall ${cpuCores(known?aged.deficit_cores:null)}`});
          note.set({label:known?'Other apps share this CPU. Full promises remain charged.':'CPU headroom is stale or unavailable. New CPU grants wait; promises stay charged.'});
        }else if(resource.kind==='memory'||resource.kind==='disk') {
          const known=(resource.kind==='memory'?memoryBudgetKnown(aged):diskBudgetKnown(aged))&&resource.availability_known!==false&&!resource.stale;
          amount.set({label:format(resource,known?resource.available:null)});
          summary.set({label:`Live pool ${format(resource,known?resource.budget_total:null)} · promised ${format(resource,resource.reserved)} · shortfall ${format(resource,known?(budget?.deficit_bytes??resource.deficit):null)}`});
          note.set({label:known?(resource.kind==='memory'?'Full memory promises remain charged. This is reservable headroom, separate from physical RAM.':'Full disk promises remain charged. Shared blocks and snapshots are not guaranteed reclaimable space.'):`${resource.kind==='memory'?'Memory':'Disk'} availability is stale or unknown. New grants wait; promises stay charged.`});
        }else note.set({label:stale||resource.stale||resource.availability_known===false?'Availability is cached, stale or unknown.':''});
      }
      for(const row of leaseRows)row.button.disabled=!mayInspect(row.lease);
    });
  }
  function render() {
    if(disposed)return;
    if(content.contains(ui.activeComponent)) {
      held=true;announce('View held while reading a control. Newer destination observations are available after focus leaves this panel.',false);return;
    }
    if(held){held=false;announce(freshnessMessage);}
    const sameMetadata=currentView==='host'&&lastView==='host'&&lastMetadata===metadata;
    lastView=currentView;lastMetadata=metadata;
    ui.batch(()=>{
      banner.replace(ui.badge('Remote node'),ui.heading(namedLabel(snapshot,metadata?.hostname||context.host),2),ui.text(`Host ID: ${context.host}`),
        ui.text(`${metadata?.platform?.os??'Unknown OS'} / ${metadata?.platform?.process_architecture??'unknown architecture'}`),
        ui.text(`Destination permissions: ${scope?grantDescription(scope.grant):'Not yet read'}. Screen Sharing opens separately and requires macOS sign-in.`));
      if(sameMetadata)return;
      resourceCards=[];leaseRows=[];
      if(currentView==='host') {
        const report=metadata?ui.json(metadata,'Reported node metadata'):ui.text('Metadata unavailable.');
        if(metadata)report.open=true;
        content.replace(report);return;
      }
      if(currentView==='leases') {
        const full=scope?.grant?.permissions?.includes('observe-all');
        const entries=(snapshot?.leases??[]).slice(0,200).map(lease=>{
          const button=ui.button(lease.request?.reason||'Lease',()=>inspect(lease,button),{disabled:!mayInspect(lease)});
          leaseRows.push({lease,button});
          return ui.card(namedLabel(lease,lease.id),[ui.text('ID: '+lease.id),ui.badge(leaseStateLabel(lease)),button,ui.text(new Date(lease.expires_at).toLocaleString()),
            ...(!full&&lease.request?.origin_peer!==context.source_host?[ui.text('Detail is unavailable under this source’s own-lease grant.')]:[])]);
        });
        content.replace(ui.text(full?'Lease inventory includes the destination’s currently authorized full view. Detail reads follow that observation grant.':'Only leases attributed to this source node are visible when observe-own is granted; detail reads require the same source ownership.'),...entries,
          ...((snapshot?.leases?.length??0)>200?[ui.text('Showing the first 200 authorized leases. Use the CLI to inspect the complete destination inventory.')]:[]),
          ...(!snapshot?.leases?.length?[ui.text('No leases are visible under this destination grant.')]:[]));return;
      }
      const cards=(snapshot?.resources??[]).map(resource=>{
        const amount=ui.heading(format(resource,resource.available),3),summary=ui.text(`Available · total ${format(resource,resource.budget_total)} · reserved ${format(resource,resource.reserved)}`),note=ui.text('');
        const budget=resource.kind==='cpu'?remoteCPUBudget(snapshot.cpu):resource.kind==='memory'?memoryBudget(snapshot.memory):resource.kind==='disk'?snapshot.disk:null;
        resourceCards.push({resource,budget,received:statusReceived,amount,summary,note});
        return ui.card(remoteResourceLabel(resource),[ui.badge(resource.kind??'Resource'),amount,summary,note,
          ui.disclosure('Scope and accounting details',[ui.json(nativeKinds.has(resource.kind)?{resource,budget:resource.kind==='cpu'?snapshot.cpu:resource.kind==='memory'?snapshot.memory:snapshot.disk}:resource,'Resource details')])]);
      });
      content.replace(ui.stack(cards),
        ...(snapshot?.memory?[ui.json(snapshot.memory,'Memory budget and observation freshness')]:[]),
        ...(snapshot?.disk?[ui.json(snapshot.disk,'Disk budget and observation freshness')]:[]));
      updateFreshness();
    });
  }
  async function inspect(lease,opener) {
    if(!mayInspect(lease))return;
    detail?.dialog.close();
    const requestGeneration=generation,body=ui.stack([ui.text('Reading this exact destination lease…')]),close=ui.button('Close',()=>dialog.close()),dialog=ui.dialog('Remote lease',[body],[close]);
    const record=detail={dialog,body};
    dialog.on('close',()=>{
      if(detail===record){detail=null;detailSlot.replace();if(opener?.isConnected)opener.focus();else banner.focus();}
    });
    detailSlot.replace(dialog);dialog.showModal();close.focus();
    try {
      const result=await call('inspect',{id:lease.id});
      if(disposed||generation!==requestGeneration||detail!==record||!dialog.open)return;
      if(result?.id!==lease.id)throw Error('Destination lease differs from the selected exact lease.');
      if(!scope?.grant?.permissions?.includes('observe-all')&&result.request?.origin_peer!==context.source_host)
        throw Error('Destination lease differs from this source’s own-lease scope.');
      ui.batch(()=>{const report=ui.json(result,'Destination lease');report.open=true;body.replace(ui.heading(namedLabel(result,result.id),3),ui.text('ID: '+result.id),ui.text('Remote lease mutations are not implemented in this window. Use an explicitly keyed CLI request if the destination grants it.'),report);});
    }catch(error){if(!disposed&&generation===requestGeneration&&detail===record&&dialog.open)body.replace(ui.text(error.message,{role:'alert'}));}
  }
  async function refresh() {
    if(disposed||pending)return;
    const token={generation};pending=token;
    try {
      const info=await call('node_context');
      if(disposed||token.generation!==generation)return;
      const nextScope=validateContext(info),nextSnapshot=await call('status');
      if(disposed||token.generation!==generation)return;
      if(nextSnapshot?.host!==context.host)throw Error('Destination status differs from this exact selected node. This window never falls back to localhost.');
      const received=clock.mono();
      let nextMetadata=metadata;
      if(metadataRequestedAt===null||clock.mono()-metadataRequestedAt>=30000) {
        metadataRequestedAt=clock.mono();
        try {
          const result=await call('node_metadata');
          if(disposed||token.generation!==generation)return;
          if(result?.host!==context.host)throw Object.assign(Error('Destination metadata differs from this exact selected node.'),{wrongHost:true});
          nextMetadata=result;
        }catch(error){if(error.wrongHost)throw error;}
      }
      if(disposed||token.generation!==generation)return;
      scope=nextScope;snapshot=nextSnapshot;statusReceived=received;metadata=nextMetadata;stale=false;
      updateFreshness();announce(`Destination snapshot received ${new Date(clock.wall()).toLocaleTimeString()}. Resource observation timestamps remain independent.`);render();
    }catch(error) {
      if(disposed||token.generation!==generation)return;
      stale=true;updateFreshness();announce(`STALE · ${error.message}. This window never falls back to localhost.`);onError(error);
    }finally {if(pending===token)pending=null;}
  }
  return {
    refresh,updateFreshness,
    show(view) {if(disposed)return;currentView=['resources','leases','host'].includes(view)?view:'resources';render();},
    disconnect() {if(disposed)return;generation++;pending=null;stale=true;if(detail)detail.body.replace(ui.text('Disconnected · this exact destination lease cannot be refreshed. No operations will be replayed.',{role:'status'}));updateFreshness();announce('Disconnected · cached destination data is stale. No operations will be replayed.');},
    dispose() {if(disposed)return;disposed=true;generation++;pending=null;detail?.dialog.close();detail=null;root.replace();},
    get stale(){return stale;}
  };
}
