import {namedLabel} from '../resource-names.mjs';
import {count,bytes,quantity,stringifyJSON} from '../core.mjs';
import {cpuBudgetFields,memoryBudgetFields,diskBudgetFields,cpuBudgetKnown,memoryBudgetKnown,diskBudgetKnown,cpuCores} from '../metric-help.mjs';
import {poolBudgetFields} from '../pool-model.mjs';

// Keep the authoritative quantities and freshness rules shared with Monitor.
// Only the presentation is progressive; promises and material warnings stay out.
export function resourcePresentation(r,budgets) {
  const cpu=r.kind==='cpu'?budgets.cpu:null,memory=r.kind==='memory'?budgets.memory:null,disk=r.kind==='disk'?budgets.disk:null;
  let fields=r.kind==='cpu'?cpuBudgetFields(r,cpu):r.kind==='memory'?memoryBudgetFields(r,memory):r.kind==='disk'?diskBudgetFields(r,disk):poolBudgetFields(r);
  const genericUnavailable=!['cpu','memory','disk'].includes(r.kind)&&(r.stale||r.availability_known===false);
  if(genericUnavailable)fields=fields.map(([label,value])=>[label,['AVAILABLE','POOL','TOTAL'].includes(label)?null:value]);
  const format=value=>r.kind==='cpu'?cpuCores(value):quantity(value,r.unit);
  const formatted=fields.map(([label,value])=>[label,format(value)]),warnings=[],notes=[];
  if(r.kind==='cpu'&&!cpuBudgetKnown(cpu))warnings.push('CPU headroom stale or unavailable. New CPU grants wait.');
  if(r.kind==='memory'&&!memoryBudgetKnown(memory))warnings.push('New memory grants paused: waiting for fresh availability.');
  if(r.kind==='disk'&&!diskBudgetKnown(disk))warnings.push('New disk grants paused: waiting for fresh availability.');
  const shortfall=fields.find(([label])=>label==='SHORTFALL');
  if(shortfall?.[1]>0)warnings.push('Shortfall: '+format(shortfall[1])+'. Existing promises remain charged.');
  // Disk and counted partitions expose deficits outside the common fields.
  if(!shortfall&&r.deficit>0)warnings.push('Shortfall: '+format(r.deficit)+'. Existing promises remain charged.');
  if(genericUnavailable)warnings.push('Resource capacity stale or unavailable.');
  if(r.custom?.deficit>0)warnings.push('Shortfall: '+format(r.custom.deficit)+'. Existing promises remain charged.');
  const partition=r.custom?.partition;
  if(partition){
    notes.push(`Direct leases ${count(partition.direct_reserved)} · Child backing ${count(partition.child_backing)} ${r.unit}. Parent and child totals overlap.`);
    if(r.custom.pool?.parent&&partition.held_backing>0)warnings.push(`Held in parent: ${count(partition.held_backing)} ${r.unit}.`);
    if(partition.pending_return>0)warnings.push(`Pending return: ${count(partition.pending_return)} ${r.unit}.`);
  }
  if(cpu){notes.push('Other apps share this CPU · full promises remain charged','Managed use credited: '+cpuCores(cpuBudgetKnown(cpu)?cpu.managed_credit_cores??0:null));if(cpu.ceiling_cores!=null)notes.push('Operator ceiling: '+cpuCores(cpu.ceiling_cores));}
  if(memory){notes.push('Physical memory: '+bytes(memory.observation?.total_bytes));if(memory.ceiling_bytes!=null)notes.push('Operator ceiling: '+bytes(memory.ceiling_bytes));}
  if(disk?.ceiling_bytes!=null)notes.push('Operator ceiling: '+bytes(disk.ceiling_bytes));
  return {label:namedLabel(r,r.custom?.pool?.name||({cpu:'CPU',memory:'Memory',disk:'Storage',port:'Ports'}[r.kind]||r.kind||r.id)),
    available:formatted[0],commitment:formatted.find(([label])=>['PROMISED','COMMITTED','RESERVED'].includes(label)),
    formatted,warnings,notes,context:{accounting:r.accounting,cpu,memory,disk,measurements:[['Physical reading',r.measurement?.used]]}};
}

export function createResourceOverview({ui,root,owner,onLease,help}) {
  const cards=new Map(),empty=ui.text('No matching resources').retain();
  function create(r,model){
    const available=ui.stat(...model.available),commitment=ui.text(''),warnings=ui.stack(),facts=ui.descriptions([]),notes=ui.stack(),consumers=ui.stack();
    const details=ui.disclosure('Accounting & consumers',[facts,notes,consumers]);
    const context={current:model.context};
    const explanation=help(r.custom?.pool?'resource-custom':'resource-'+r.kind,()=>context.current);
    // Stock Stat has an 11rem flex basis for horizontal statistic rows. Keep
    // that basis on the inline axis rather than stretching a vertical card.
    const card=ui.card(model.label,[ui.stack([available],{row:true}),commitment,warnings,details],{actions:[explanation]}).retain();
    return {card,available,commitment,warnings,facts,notes,consumers,details,fingerprint:null,explanation,context,consumerRows:new Map()};
  }
  return {update(resources,budgets,filter='') {ui.batch(()=>{
    const wanted=new Set(),visible=[];
    for(const r of resources){
      const key=r.id;wanted.add(key);const model=resourcePresentation(r,budgets);let h=cards.get(key);
      if(!h){h=create(r,model);cards.set(key,h);}
      h.context.current=model.context;h.card.set({label:model.label});h.available.set({label:model.available[0],value:model.available[1]});
      h.commitment.set({label:model.commitment?`${model.commitment[0]} · ${model.commitment[1]}`:''});
      const fingerprint=stringifyJSON({formatted:model.formatted,warnings:model.warnings,notes:model.notes});
      if(fingerprint!==h.fingerprint){
        h.fingerprint=fingerprint;
        h.warnings.replace(...model.warnings.map(label=>ui.text(label,{tone:'warning'})));
        h.facts.setPairs([...model.formatted.slice(1),['Resource ID',r.id]]);
        h.notes.replace(...model.notes.map(label=>ui.text(label)));
      }
      const rows=[],wantedConsumers=new Set();
      for(const [index,u] of (r.users??[]).entries()){
        const key=u.lease?stringifyJSON([u.lease,u.relationship??null]):'unidentified:'+index;
        wantedConsumers.add(key);let consumer=h.consumerRows.get(key);
        const caption=(u.lease_display_name??u.reason)||u.lease||'Consumer';
        if(!consumer){
          const action=u.lease?ui.button(caption,()=>onLease(u.lease)):ui.text(caption);
          const identity=ui.text(''),reason=ui.text(''),metadata=ui.text('');
          consumer={row:ui.stack([action,identity,reason,metadata]),action,identity,reason,metadata};h.consumerRows.set(key,consumer);
        }
        consumer.action.set({label:caption});
        consumer.identity.set({label:u.lease?'Lease ID '+u.lease:'',hidden:!u.lease});
        consumer.reason.set({label:u.reason??'',hidden:!(u.lease_display_name&&u.reason)});
        consumer.metadata.set({label:[u.agent,owner(u),quantity(u.quantity,r.unit),u.project,u.state,u.expires_at?'Expires '+new Date(u.expires_at).toLocaleString():''].filter(Boolean).join(' · ')});
        rows.push(consumer.row);
      }
      h.consumers.replace(...(rows.length?rows:[ui.text('No current consumers')]));
      for(const key of h.consumerRows.keys())if(!wantedConsumers.has(key))h.consumerRows.delete(key);
      h.details.set({label:`Accounting & consumers · ${r.users?.length??0}`});
      if(stringifyJSON(r).toLowerCase().includes(filter.toLowerCase()))visible.push(h.card);
    }
    root.replace(...(visible.length?visible:[empty]));
    for(const [key,h] of cards)if(!wanted.has(key)){h.card.release();cards.delete(key);}
  });},dispose(){root.replace();for(const h of cards.values())h.card.release();cards.clear();empty.release();}};
}
