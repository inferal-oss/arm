import {jsonView} from './json-view.mjs';
import {bytes,stringifyJSON} from './core.mjs';
import {validHostID,meshPreparationNotice,phaseLabel,operationAdvice,grantFromDraft,grantDescription,reviewIdentities,canReview,supportedReview,validateMeshReason} from './mesh-model.mjs';

const when = value => value ? new Date(value).toLocaleString() : 'Not reported';
export function createMeshConsole({root,call,connected,storage,openNode,document=root.ownerDocument,onActivity=()=>{}}) {
  const el=(tag,cls='',text=null)=>{const n=document.createElement(tag);n.className=cls;if(text!==null)n.textContent=text;return n;};
  const button=(text,fn,cls='')=>{const n=el('button',cls,text);n.type='button';n.addEventListener('click',fn);return n;};
  const message=el('p','mesh-inline-notice');message.setAttribute('role','status');
  const toolbar=el('div','mesh-toolbar'),intro=el('div'),actions=el('div','mesh-toolbar-actions');
  intro.append(el('p','eyebrow','DIRECT CONNECTIONS'),el('h2','','Your resource network'),el('p','mesh-muted','Each host owns its resources. Access is explicit, directional and revocable.'));
  const refreshButton=button('Refresh nodes',()=>refresh(true)),connectButton=button('Connect a node…',()=>editConnect(),'primary');
  actions.append(refreshButton,connectButton);toolbar.append(intro,actions);
  const nodes=el('div','mesh-nodes'),operations=el('section','mesh-operations');
  root.replaceChildren(toolbar,message,nodes,operations);
  let rosterKey='',operationsKey='',roster=null,allOps=[],selectedHost='',detail=null,dialog=null,opKey='',opVersion=0,renderedRevision=null,renderedSemantic='',currentOperation=null,refreshBusy=false,refreshQueued=false,disposed=false;
  const announce=text=>{message.textContent=text;};
  let pendingKeys=[],pendingStoreInvalid=false;
  try{const saved=storage?.getItem('arm-mesh-pending');if(saved){let parsed;try{parsed=JSON.parse(saved);}catch{if(/^[A-Za-z0-9_-]{1,128}$/.test(saved))parsed=[saved];else throw Error('Invalid pending key store');}if(!Array.isArray(parsed)||parsed.length>16||parsed.some(k=>typeof k!=='string'||!k.length||k.length>128))throw Error('Invalid pending operation store');pendingKeys=[...new Set(parsed)];}}catch{pendingStoreInvalid=true;announce('Saved pending keys could not be read. Inspect durable Connection operations before starting another change.');}
  let uncertainResumes=new Set(),resumeStoreInvalid=false;
  try{const saved=JSON.parse(storage?.getItem('arm-mesh-resume-pending')??'[]');if(!Array.isArray(saved)||saved.length>16||saved.some(key=>typeof key!=='string'||!key.length||key.length>128))throw Error('Invalid recovery intent store');uncertainResumes=new Set(saved);}catch{resumeStoreInvalid=true;}
  function retainResume(key){if(!storage?.setItem)throw Error('Durable recovery intent storage is unavailable; no request was sent.');if(resumeStoreInvalid||(!uncertainResumes.has(key)&&uncertainResumes.size>=16))throw Error('Recovery intent storage needs inspection before another recovery request.');uncertainResumes.add(key);storage?.setItem('arm-mesh-resume-pending',JSON.stringify([...uncertainResumes]));}
  function settleResume(key){uncertainResumes.delete(key);try{storage?.setItem('arm-mesh-resume-pending',JSON.stringify([...uncertainResumes]));}catch{resumeStoreInvalid=true;}}
  function savePending(){try{storage?.setItem('arm-mesh-pending',JSON.stringify(pendingKeys));}catch{announce('Pending key storage is unavailable. Keep this tab open and retain exact operation keys before leaving.');}}
  const remember=key=>{if(pendingStoreInvalid)throw Error('Saved pending-key data is unreadable; inspect durable operations before explicitly clearing this browser cache.');if(!pendingKeys.includes(key)){if(pendingKeys.length>=16)throw Error('16 uncertain operations are retained. Inspect them before preparing another change.');pendingKeys.push(key);savePending();}};
  const forget=key=>{if(pendingKeys.includes(key)){pendingKeys=pendingKeys.filter(k=>k!==key);savePending();}};
  function modal(title,eyebrow='MESH / LOCAL OPERATOR') {
    if(dialog?.open)dialog.close();
    const opener=document.activeElement,d=el('dialog','mesh-dialog'),head=el('header','mesh-dialog-head'),heading=el('h2','',title),body=el('div','mesh-dialog-body'),foot=el('footer','mesh-dialog-foot');
    heading.id='mesh-dialog-title';d.setAttribute('aria-labelledby',heading.id);heading.tabIndex=-1;
    const close=button('Close ×',()=>d.close(),'mesh-close');close.setAttribute('aria-label','Close mesh dialog');
    const text=el('div');text.append(el('p','eyebrow',eyebrow),heading);head.append(text,close);d.append(head,body,foot);document.body.append(d);dialog=d;
    d.addEventListener('close',()=>{const current=dialog===d;if(current){dialog=null;opKey='';opVersion++;}d.remove();if(current){if(opener?.isConnected)opener.focus();else connectButton.focus();}});
    d.showModal();return {d,body,foot,heading};
  }
  function note(parent,text,kind='') {parent.append(el('p','mesh-note '+kind,text));}
  function row(parent,label,value,mono=false){const r=el('div','mesh-fact');r.append(el('dt','',label),el('dd',mono?'mesh-mono':'',value??'Not reported'));parent.append(r);}
  function grantCard(title,grant) {
    const box=el('section','mesh-grant-card');box.append(el('h4','',title),el('p','',grantDescription(grant)));
    if(grant?.permissions?.includes('lease-own')){const facts=el('dl','mesh-facts');row(facts,'Aggregate ceilings',`${grant.limits?.cpu??0} CPU · ${bytes(grant.limits?.memory_bytes??0)} memory · ${bytes(grant.limits?.disk_bytes??0)} disk`);row(facts,'Maximum lease',`${Number(grant.max_ttl_ns??0)/60e9} minutes`);box.append(facts);}
    if(grant?.permissions?.includes('message'))note(box,`Messages: ${grant.operator_inbox?'operator inbox':''}${grant.operator_inbox&&grant.contact_ids?.length?' + ':''}${grant.contact_ids?.length?grant.contact_ids.length+' exact contacts':''}`);
    return box;
  }
  function renderRoster() {
    const key=stringifyJSON((roster?.nodes??[]).map(n=>[n.host,n.name,n.local,n.health,n.trust?.state,n.trust?.execution_account,n.metadata?.platform,n.metadata?.build,n.metadata?.inventory?.logical_cpus,n.metadata?.inventory?.host_memory?.total_bytes,n.metadata_freshness?.stale,n.error].map(value=>value??null)));
    if(key===rosterKey)return;rosterKey=key;
    const count=roster?.nodes?.length??0;nodes.replaceChildren();
    if(!count){nodes.append(el('p','empty','No node inventory yet. Refresh reads the registry without creating mesh state.'));return;}
    for(const entry of roster.nodes){
      const card=el('article','mesh-node'+(entry.local?' is-local':'')),head=el('div','mesh-node-head'),icon=el('span','mesh-node-symbol',entry.local?'⌂':'◇'),identity=el('div'),name=button(entry.name||entry.host,()=>openNode?openNode(entry):inspect(entry.host),'mesh-node-title');name.setAttribute('aria-label',openNode?`Open dashboard for ${entry.name||entry.host} in a new window`:`View ${entry.name||entry.host}`);
      identity.append(name,el('p','mesh-mono mesh-node-id',entry.host));head.append(icon,identity,el('span','badge',entry.local?'This node':entry.trust?.state??'Unknown'));
      const metadata=entry.metadata,platform=metadata?.platform;card.append(head,el('p','mesh-node-platform',platform?`${platform.os} / ${platform.process_architecture} · ARM ${metadata.build?.arm_version??'unknown'}${entry.metadata_freshness?.stale?' · cached / stale':''}`:'Metadata not yet read'));
      const facts=el('dl','mesh-node-stats');row(facts,'Status',entry.health??'Unknown');row(facts,'Run as',entry.trust?.execution_account?.username||entry.trust?.execution_account?.uid||'Not reported');
      if(metadata)row(facts,'Inventory',`${metadata.inventory?.logical_cpus?.value??metadata.inventory?.logical_cpus??'—'} CPU · ${bytes(metadata.inventory?.host_memory?.total_bytes)}`);
      card.append(facts);
      const controls=el('div','mesh-node-actions');controls.append(button('View node',()=>inspect(entry.host)));
      if(openNode)controls.append(button('Open dashboard ↗',()=>openNode(entry)));
      card.append(controls);nodes.append(card);
    }
    if(roster.error)announce(roster.error.message);
  }
  async function inspect(host){
    if(!validHostID(host))return;selectedHost=host;
    const ui=modal('Node details','MESH / NODE'),status=el('p','mesh-muted','Reading this exact host…');ui.body.append(status);detail=ui;
    try{const entry=await call('node_inspect',{host});if(dialog!==ui.d||selectedHost!==host)return;renderNode(ui,entry);}catch(error){if(dialog===ui.d)status.textContent=error.message;}
  }
  function renderNode(ui,entry){
    ui.heading.textContent=entry.name||entry.host;ui.body.replaceChildren();
    const summary=el('div','mesh-node-summary'),facts=el('dl','mesh-facts');
    row(facts,'Stable host ID',entry.host,true);row(facts,'Authority',entry.local?'This local backend':'Direct peer through this local gateway');row(facts,'Connection',entry.health);row(facts,'Run as',entry.trust?.execution_account?.username?`${entry.trust.execution_account.username} (UID ${entry.trust.execution_account.uid})`:entry.trust?.execution_account?.uid);
    summary.append(facts);ui.body.append(summary);
    if(!entry.local){
      note(ui.body,entry.scope_freshness?.stale?'Permissions are stale or unavailable. A fresh destination check is required for actions.':'Permissions below are this source’s current observed rights, enforced by the destination.',entry.scope_freshness?.stale?'warning':'');
      const grants=el('div','mesh-direction-grid');grants.append(grantCard('We may do on this node',entry.outbound_scope?.grant),grantCard('This node may do here',entry.trust?.inbound_grant));ui.body.append(grants);
      const trust=el('dl','mesh-facts mesh-trust');row(trust,'Endpoint',entry.trust?.address,true);row(trust,'Enrollment',entry.trust?.pair,true);row(trust,'Certificate SHA-256',entry.trust?.certificate_fingerprint,true);row(trust,'Public key SHA-256',entry.trust?.spki_sha256,true);row(trust,'Certificate expires',when(entry.trust?.expires_at));ui.body.append(trust);
    }
    if(entry.metadata){const label=el('h3','','Reported node metadata');ui.body.append(label);note(ui.body,'Reported capabilities describe installed software, not permission to use it. Inventory timestamps describe measurement freshness.');ui.body.append(jsonView(entry.metadata,{label:'Reported metadata',document}));}
    else note(ui.body,'This peer has not provided metadata. No local data is substituted.');
    if(entry.error)note(ui.body,entry.error.message,'warning');
    ui.foot.replaceChildren(button('Close',()=>ui.d.close()));if(openNode)ui.foot.append(button('Open dashboard ↗',()=>openNode(entry),'primary'));
    if(!entry.local&&entry.trust?.state==='active')ui.foot.append(button('Disconnect this node…',()=>editDisconnect(entry),'danger'));
  }
  function field(parent,label,{type='text',value='',placeholder='',required=false}={}){const wrap=el('label','mesh-field'),input=el(type==='textarea'?'textarea':'input');if(type!=='textarea')input.type=type;input.value=value;input.placeholder=placeholder;input.required=required;if(type==='textarea')input.maxLength=4096;wrap.append(el('span','',label),input);parent.append(wrap);return input;}
  function grantEditor(parent,title,initial){
    const box=el('fieldset','mesh-grant-editor');box.append(el('legend','',title));const mode=el('select');for(const [value,text]of [['none','No access'],['summary','Host summary only'],['leases','Our leases + resource reservations'],['run','Our leases + run programs']]){const o=el('option','',text);o.value=value;mode.append(o);}mode.value=initial;
    const label=el('label','mesh-field');label.append(el('span','','Permission level'),mode);box.append(label);
    const limits=el('div','mesh-limit-fields'),cpu=field(limits,'CPU ceiling (cores)',{type:'number',value:'1'}),memory=field(limits,'Memory ceiling (GiB)',{type:'number',value:'1'}),disk=field(limits,'Disk ceiling (GiB)',{type:'number',value:'2'}),ttl=field(limits,'Maximum lease (minutes)',{type:'number',value:'10'});for(const input of[cpu,memory,disk,ttl]){input.min='0';input.step='any';}box.append(limits);
    const messaging=el('details','mesh-messaging-options');messaging.append(el('summary','','Message access (optional)'));const allow=field(messaging,'Allow messages',{type:'checkbox'}),inbox=field(messaging,'Allow operator inbox',{type:'checkbox'}),contacts=field(messaging,'Exact allowed contact IDs',{placeholder:'Contact IDs separated by commas'});box.append(messaging);
    const warning=el('p','mesh-note warning','Run executes as the destination OS account, with its file and credential access. It is not a sandbox.');box.append(warning);
    const update=()=>{limits.hidden=!['leases','run'].includes(mode.value);for(const input of[cpu,memory,disk,ttl])input.disabled=limits.hidden;warning.hidden=mode.value!=='run';messaging.hidden=mode.value==='none';};mode.addEventListener('change',update);update();parent.append(box);
    return()=>grantFromDraft({mode:mode.value,cpu:cpu.value,memory:memory.value,disk:disk.value,ttl:ttl.value,messages:allow.checked,operatorInbox:inbox.checked,contacts:contacts.value});
  }
  async function editConnect(){
    const ui=modal('Connect a node','MESH / NEW CONNECTION'),form=el('form','mesh-form'),error=el('p','mesh-form-error');error.setAttribute('role','alert');
    note(form,meshPreparationNotice);const target=el('section','mesh-form-section');target.append(el('h3','','1. Choose a destination'));
    const source=el('select'),direct=el('option','','SSH destination');direct.value='';source.append(direct);const sourceLabel=el('label','mesh-field');sourceLabel.append(el('span','','Connection method'),source);target.append(sourceLabel);
    const destination=field(target,'Verified SSH destination',{placeholder:'user@host or a configured SSH alias',required:true});note(target,'Uses your existing known_hosts trust. ARM must already be installed there; no credentials or private keys are copied.');
    const reason=field(target,'Reason for connecting',{type:'textarea',required:true,placeholder:'What will this connection be used for?'});form.append(target);
    call('mesh_operator_connectors').then(profiles=>{if(dialog!==ui.d)return;for(const profile of profiles??[]){const option=el('option','',`${profile.id} · ${profile.ssh_destination}`);option.value=profile.id;source.append(option);}}).catch(e=>{if(dialog===ui.d)note(target,`Reviewed connectors unavailable: ${e.message}`,'warning');});
    source.addEventListener('change',()=>{destination.disabled=Boolean(source.value);destination.required=!source.value;});
    const directions=el('section','mesh-form-section');directions.append(el('h3','','2. Set directional permissions'));const grid=el('div','mesh-direction-grid'),forward=grantEditor(grid,'This node → destination','summary'),reverse=grantEditor(grid,'Destination → this node','none');directions.append(grid);form.append(directions);
    const replace=field(form,'Explicitly replace a previously revoked enrollment, if present',{type:'checkbox'});form.append(error);ui.body.append(form);
    const cancel=button('Cancel',()=>ui.d.close()),prepare=el('button','primary','Prepare connection review');prepare.type='submit';prepare.setAttribute('form','mesh-connect-form');form.id='mesh-connect-form';ui.foot.append(cancel,prepare);cancel.autofocus=true;cancel.focus();
    form.addEventListener('submit',async event=>{event.preventDefault();error.textContent='';if(!connected()){error.textContent='Reconnect and inspect before preparing a change.';return;}if(!reason.value.trim()||(!source.value&&!destination.value.trim())){error.textContent='A destination and a meaningful reason are required.';return;}
      let q;try{validateMeshReason(reason.value);q={key:crypto.randomUUID(),kind:'connect',connect:{...(source.value?{connector:source.value}:{ssh_destination:destination.value.trim()}),reason:reason.value,source_permissions_on_target:forward(),target_permissions_on_source:reverse(),replace_revoked:Boolean(replace.checked)}};}catch(e){error.textContent=e.message;return;}
      try{remember(q.key);}catch(e){error.textContent=e.message;return;}prepare.disabled=true;try{const op=await call('mesh_operator_prepare',q);if(dialog===ui.d){ui.d.close();showOperation(op);}else{announce(`Operation ${op.key} recorded. View its receipt under Connection operations.`);refresh();}}catch(e){error.textContent=`${e.message}. The exact operation key is ${q.key}. Inspect its receipt before preparing anything again.`;announce(`Connection outcome requires lookup: ${q.key}`);renderOperations();}
    });
  }
  function editDisconnect(entry){
    const ui=modal('Disconnect this node','MESH / EXACT ENROLLMENT'),facts=el('dl','mesh-facts');row(facts,'Node',entry.name);row(facts,'Stable host',entry.host,true);row(facts,'Enrollment',entry.trust?.pair,true);ui.body.append(facts);
    note(ui.body,'Disconnect revokes this enrollment locally first and closes its sessions. An offline destination may need explicit recovery. Existing leases, expiry and cleanup obligations remain. Other nodes are unchanged.','warning');
    const form=el('form'),reason=field(form,'Reason for disconnecting',{type:'textarea',required:true}),error=el('p','mesh-form-error');error.setAttribute('role','alert');form.id='mesh-disconnect-form';form.append(error);ui.body.append(form);const keep=button('Keep connection',()=>ui.d.close()),prepare=el('button','danger','Prepare disconnect review');prepare.type='submit';prepare.setAttribute('form',form.id);ui.foot.append(keep,prepare);keep.focus();
    form.addEventListener('submit',async event=>{event.preventDefault();if(!connected()){error.textContent='Reconnect before preparing a disconnect.';return;}if(!reason.value.trim()){error.textContent='Explain why this enrollment should be disconnected.';return;}const key=crypto.randomUUID();try{validateMeshReason(reason.value);remember(key);}catch(e){error.textContent=e.message;return;}prepare.disabled=true;
      try{const op=await call('mesh_operator_prepare',{key,kind:'disconnect',disconnect:{host:entry.host,pair:entry.trust.pair,reason:reason.value}});if(dialog===ui.d){ui.d.close();showOperation(op);}else{announce(`Operation ${op.key} recorded. View its receipt under Connection operations.`);refresh();}}catch(e){error.textContent=`${e.message}. Inspect operation ${key} before any retry.`;renderOperations();}
    });
  }
  function identityCard(label,identity,listen){const box=el('section','mesh-identity');box.append(el('h4','',label));const facts=el('dl','mesh-facts');row(facts,'Name',identity.name);row(facts,'Stable host',identity.host,true);row(facts,'Run as',identity.owner_username?(identity.owner_uid?`${identity.owner_username} (UID ${identity.owner_uid})`:identity.owner_username):identity.owner_uid);row(facts,'Advertised endpoint',identity.address,true);if(listen)row(facts,'Exact listener bind',listen,true);row(facts,'Certificate SHA-256',identity.certificate_sha256,true);row(facts,'Public key SHA-256',identity.public_key_sha256,true);row(facts,'Expires',when(identity.certificate_expires));box.append(facts);return box;}
  function semantic(op){return stringifyJSON([op.key,op.phase,op.worker_live,op.cancel_requested,op.review_sha256,op.review_revision,op.approved,op.result,op.error,op.resolved_by,op.recovery_sha256,canReview(op)].map(value=>value??null));}
  function renderOperation(ui,op){
    if(dialog!==ui.d||!ui.d.open)return;
    forget(op.key);currentOperation=op;renderedSemantic=semantic(op);renderedRevision=String(op.revision);
    ui.heading.textContent=phaseLabel(op);ui.body.replaceChildren();ui.foot.replaceChildren();
    const banner=el('div','mesh-operation-banner');banner.setAttribute('role','status');banner.dataset.phase=op.phase;banner.append(el('span','mesh-step',op.kind==='connect'?'CONNECT':op.kind==='disconnect'?'DISCONNECT':'OTHER OPERATION'),el('p','',operationAdvice(op)));ui.body.append(banner);
    const facts=el('dl','mesh-facts mesh-receipt-key');row(facts,'Exact operation',op.key,true);row(facts,'Updated',when(op.updated_at));if(op.parent_key)row(facts,'Original intent',op.parent_key,true);ui.body.append(facts);
    if(op.error)note(ui.body,`${op.error.code}: ${op.error.message}`,'warning');
    const review=op.review;
    if(review){
      if(!supportedReview(op))note(ui.body,'This review type is not supported by this dashboard. Inspect it using the CLI; no approval is available here.','warning');
      ui.body.append(el('h3','',supportedReview(op)?'Concrete trust review':'Recorded review'));const identities=el('div','mesh-direction-grid');for(const item of reviewIdentities(review))identities.append(identityCard(item.label,item.identity,item.listen));ui.body.append(identities);
      if(review.ssh_identity||review.ssh_account){const ssh=el('dl','mesh-facts');for(const [key,value]of Object.entries(review.ssh_identity??{account:review.ssh_account,host_key:review.ssh_host_key}))row(ssh,`SSH ${key.replaceAll('_',' ')}`,String(value),true);ui.body.append(ssh);}
      if(review.request||review.source_permissions_on_target){const request=review.request??review;const grants=el('div','mesh-direction-grid');grants.append(grantCard('This node → destination',request.source_permissions_on_target),grantCard('Destination → this node',request.target_permissions_on_source));ui.body.append(grants);}
      if(review.reason??review.request?.reason)row(facts,'Reason',review.reason??review.request.reason);
      if(review.operation==='disconnect-direct-peer'){row(facts,'Exact enrollment',review.pair,true);row(facts,'Local enrollment revision',review.local_revision);row(facts,'Destination enrollment revision',review.remote_revision);}
      note(ui.body,review.warning||review.effect||op.notice||meshPreparationNotice,'warning');
      if(canReview(op)){note(ui.body,`This review expires ${when(op.review_expires_at)}. Confirmation binds these exact identities, endpoints, permissions and revisions.`);}
      ui.body.append(jsonView(review,{label:'Full immutable review',document}));
    }else note(ui.body,op.notice||meshPreparationNotice);
    if(op.result)ui.body.append(jsonView(op.result,{label:'Recorded result',document}));
    const close=button('Close',()=>ui.d.close());ui.foot.append(close);
    if(canReview(op)){
      close.textContent='Keep review open later';const decline=button('Decline this review',()=>decide(false),'mesh-decline'),approve=button(op.kind==='disconnect'?'Confirm disconnect':'Trust and connect',()=>decide(true),op.kind==='disconnect'?'danger':'primary');ui.foot.append(decline,approve);decline.autofocus=true;
      async function decide(approved){if(!connected()){note(ui.body,'Reconnect and refresh this review before deciding.','warning');return;}if(!canReview(op)){note(ui.body,'This review expired. Inspect the receipt and prepare a fresh review.','warning');return;}decline.disabled=approve.disabled=true;try{const next=await call('mesh_operator_decide',{key:op.key,review_revision:op.review_revision,review_sha256:op.review_sha256,approve:approved});renderOperation(ui,next);onActivity();}catch(e){note(ui.body,`${e.message}. The decision may have been recorded. Refresh this exact receipt; no decision will be resent.`, 'warning');ui.foot.append(button('Look up recorded outcome',()=>lookup(op.key)));}}
    }else if(op.worker_live&&!op.cancel_requested){ui.foot.append(button('Request cancellation',async()=>{if(!connected())return;try{renderOperation(ui,await call('mesh_operator_cancel',{key:op.key,revision:(currentOperation?.key===op.key?currentOperation.revision:op.revision)}));}catch(e){note(ui.body,`${e.message}. Inspect this exact operation before another action.`,'warning');}}));}
    if(op.phase==='incomplete'&&!op.worker_live&&!op.resolved_by){
      if(resumeStoreInvalid||uncertainResumes.has(op.key)){note(ui.body,'A recovery request may already have created a child attempt. Inspect Connection operations and this exact parent; this request cannot be resent here. Separately keyed recovery lookup is still required for in-app resolution.','warning');}
      else {const resume=button('Prepare recovery review…',async()=>{if(!connected())return;resume.disabled=true;try{retainResume(op.key);}catch(e){note(ui.body,e.message,'warning');return;}try{const next=await call('mesh_operator_resume',{key:op.key,revision:op.revision});settleResume(op.key);forget(next.key);opKey=next.key;if(dialog===ui.d&&ui.d.open)renderOperation(ui,next);else refresh();}catch(e){note(ui.body,`${e.message}. Recovery may already have created a child attempt. Inspect this parent and Connection operations; no request will be resent.`,'warning');}});ui.foot.append(resume);}
    }
    if(op.recovery_sha256&&!op.worker_live&&!op.resolved_by)ui.foot.append(button('Hand off unresolved operation…',()=>editHandoff(op),'danger'));
    if(!ui.d.contains(document.activeElement))(ui.foot.querySelector('.mesh-decline')??ui.foot.querySelector('button'))?.focus();
  }
  function editHandoff(op){
    const ui=modal('Hand off unresolved operation','MESH / WORKFLOW EXCLUSION'),facts=el('dl','mesh-facts');row(facts,'Exact operation',op.key,true);row(facts,'Recorded phase',phaseLabel(op));row(facts,'Operation revision',String(op.revision));ui.body.append(facts);
    const fingerprint=el('details','mesh-trust metric-technical'),full=el('code','mesh-mono',op.recovery_sha256);full.setAttribute('aria-label','Full handoff review SHA-256 fingerprint');fingerprint.append(el('summary','','Handoff review fingerprint · SHA-256'),el('p','mesh-muted','This fingerprint binds the exact operation snapshot being handed off. A changed operation requires a fresh review.'),full);ui.body.append(fingerprint);
    note(ui.body,op.recovery_effect||'Release only this workflow exclusion. This does not establish remote cleanup, revoke trust, close listeners or release any lease.','warning');
    note(ui.body,'Inspect the recorded review and result before proceeding. Other operators may be able to prepare a new change after handoff; unresolved resource and trust obligations remain.');
    const form=el('form'),reason=field(form,'Reason and operator recovery plan',{type:'textarea',required:true}),error=el('p','mesh-form-error');form.id='mesh-handoff-form';form.append(error);ui.body.append(form);
    const keep=button('Keep operation protected',()=>ui.d.close()),confirm=el('button','danger','Confirm workflow handoff');confirm.type='submit';confirm.setAttribute('form',form.id);ui.foot.append(keep,confirm);keep.focus();
    form.addEventListener('submit',async event=>{event.preventDefault();if(!connected()||!reason.value.trim()){error.textContent='A live connection and meaningful recovery reason are required.';return;}try{validateMeshReason(reason.value);}catch(e){error.textContent=e.message;return;}confirm.disabled=true;try{const result=await call('mesh_operator_handoff',{key:op.key,revision:op.revision,review_sha256:op.recovery_sha256,reason:reason.value});if(dialog===ui.d){ui.d.close();showOperation(result);}else refresh();}catch(e){error.textContent=`${e.message}. Inspect the exact operation receipt before another attempt.`;}});
  }
  function showOperation(op){const ui=modal(phaseLabel(op),'MESH / DURABLE OPERATION');opKey=op.key;forget(op.key);renderOperation(ui,op);ui.foot.querySelector('.mesh-decline')?.focus();if(!canReview(op))ui.foot.querySelector('button')?.focus();}
  async function lookup(key){try{const op=await call('mesh_operator_get',{key});showOperation(op);}catch(e){announce(`${e.message}. Operation ${key} was not retried.`);}}
  function renderOperations(){const key=stringifyJSON([pendingKeys,allOps.map(op=>[op.key,op.phase,op.cancel_requested,op.resolved_by,op.error].map(v=>v??null))]);if(key===operationsKey)return;operationsKey=key;operations.replaceChildren();const head=el('div','mesh-section-heading');head.append(el('div','',''),el('h3','','Connection operations'));head.replaceChildren(el('h3','','Connection operations'),el('span','mesh-muted','Durable receipts · no replay'));operations.append(head);
    const entries=[...allOps].sort((a,b)=>Date.parse(b.updated_at)-Date.parse(a.updated_at));for(const key of [...pendingKeys]){if(entries.some(op=>op.key===key))forget(key);else operations.append(button(`Look up uncertain operation · ${key}`,()=>lookup(key),'mesh-pending-lookup'));}
    if(!entries.length){operations.append(el('p','mesh-muted','Connection changes will appear here, including interrupted or incomplete work.'));return;}
    const list=el('div','mesh-operation-list');for(const op of entries){const item=button('',()=>lookup(op.key),'mesh-operation-row'),label=el('div');label.append(el('strong','',phaseLabel(op)),el('span','mesh-mono',op.key));item.append(el('span','mesh-operation-kind',op.kind==='connect'?'↗':op.kind==='disconnect'?'⊘':'?'),label,el('span','mesh-muted',when(op.updated_at)),el('span','','View receipt →'));list.append(item);}operations.append(list);
  }
  async function refresh(inspectSelected=false){if(disposed||!connected())return;if(refreshBusy){refreshQueued=true;return;}refreshBusy=true;refreshButton.disabled=true;try{const next=await call('node_list');roster=next;renderRoster();allOps=await call('mesh_operator_list')??[];renderOperations();if(opKey&&dialog?.open){const key=opKey,version=++opVersion,op=await call('mesh_operator_get',{key});if(opKey===key&&version===opVersion)currentOperation=op;if(opKey===key&&version===opVersion&&dialog?.open&&semantic(op)!==renderedSemantic){const active=document.activeElement;renderOperation({d:dialog,heading:dialog.querySelector('h2'),body:dialog.querySelector('.mesh-dialog-body'),foot:dialog.querySelector('.mesh-dialog-foot')},op);if(active?.isConnected)active.focus();}}if(inspectSelected&&selectedHost&&detail?.d===dialog){const entry=await call('node_inspect',{host:selectedHost});if(detail?.d===dialog)renderNode(detail,entry);}announce('Registry refreshed. Peer metadata is fetched only when you inspect a node.');}catch(e){announce(`${e.message}. Existing data may be stale; no operation was replayed.`);}finally{refreshBusy=false;refreshButton.disabled=!connected();if(refreshQueued){refreshQueued=false;queueMicrotask(()=>refresh());}}}
  renderOperations();
  return {refresh,invalidate:()=>refresh(),updateConnection(){connectButton.disabled=refreshButton.disabled=!connected();if(!connected()){announce('Disconnected. Cached nodes and receipts may be stale. Reconnect performs reads only.');if(dialog?.open)for(const control of dialog.querySelectorAll('button'))if(!['Close','Close ×','Keep review open later','Keep connection','Cancel'].includes(control.textContent))control.disabled=true;}else{renderedSemantic='';}},dispose(){disposed=true;dialog?.close();}};
}
