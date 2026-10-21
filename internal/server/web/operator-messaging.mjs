import {jsonView} from './json-view.mjs';

const copy = value => JSON.parse(JSON.stringify(value));
const contactFields = ['id','adapter','session','cwd','config_dir','remote','idle','delivery','registration'];
const normalize = contact => Object.fromEntries(contactFields.filter(key => contact[key] !== undefined).map(key => [key,contact[key]]));
export function reviewedTargets(contacts, audience) {
  return contacts.filter(c => audience === 'broadcast' || c.id === audience).map(normalize).sort((a,b) => a.id.localeCompare(b.id));
}
export function prepareMessage({audience,body,lease = '',reply_to = ''}, contacts, key) {
  if (!audience || audience === 'operator') throw Error('Choose a registered agent or Broadcast.');
  if (!body.trim() || new TextEncoder().encode(body).length > 16384) throw Error('Write a message of 1 to 16,384 UTF-8 bytes.');
  const targets = reviewedTargets(contacts,audience);
  if (!targets.length || (audience !== 'broadcast' && targets.length !== 1)) throw Error('The selected recipient is no longer registered.');
  const request = {idempotency_key:key,audience,body,targets};
  if (lease) request.lease = lease;
  if (reply_to) request.reply_to = reply_to;
  if (JSON.stringify(request).length > 262144) throw Error('This audience is too large for the dashboard review. Use the CLI.');
  return request;
}
export function sameTargets(request, contacts) { return JSON.stringify(request.targets) === JSON.stringify(reviewedTargets(contacts,request.audience)); }
const preflightErrors = new Set(['invalid_request','contact_changed','not_found','invalid_contact']);
const storageKey = 'arm-operator-operations-v1';
const requestLimit = 262144, operationLimit = 8;
// Include every request wrapper, separator and array delimiter at the exact
// per-request bound. Writes and reads use the same envelope limit.
const savedLimit = operationLimit * (requestLimit + 32) + 2;

// Uncertain operations survive page reload when session storage is available.
// Restoring, reconnecting and looking up a key never invoke native delivery.
export function createMessageOperations({call,storage,key = () => crypto.randomUUID(),onChange = () => {}}) {
  let operations = [], warning = '';
  try {
    const raw = storage?.getItem(storageKey);
    if (raw) {
      if (raw.length > savedLimit) throw Error('Saved message operations exceed the supported size');
      const saved = JSON.parse(raw);
      if (!Array.isArray(saved) || saved.length > 8) throw Error('Invalid saved operations');
      operations = saved.map(op => {
        if (!op?.request || JSON.stringify(op.request).length > requestLimit || typeof op.request.idempotency_key !== 'string' || op.request.idempotency_key.length > 128 || !op.request.idempotency_key || !Array.isArray(op.request.targets) || typeof op.request.body !== 'string' || new TextEncoder().encode(op.request.body).length > 16384 || typeof op.request.audience !== 'string') throw Error('Invalid saved operation');
        return {request:copy(op.request),state:'unknown',error:'Restored after reload. Check the stored result before retrying.',busy:false};
      });
    }
  } catch { warning = 'Saved message operations could not be read. Inspect Messages before sending a replacement.'; }
  function persist() {
    try { if (!storage) throw Error('Unavailable'); const saved=JSON.stringify(operations.filter(op => ['sending','unknown'].includes(op.state)).map(op=>({request:op.request}))); if(saved.length>savedLimit)throw Error('Saved request limit'); storage.setItem(storageKey,saved); }
    catch { warning = 'This browser cannot save pending requests across reloads. Keep this page open and copy any uncertain request key.'; }
  }
  function changed() { persist(); onChange(); }
  function accept(op,message) {
    const r=op.request;
    if (!message || message.idempotency_key !== r.idempotency_key || message.audience !== r.audience || message.body !== r.body || (message.lease || '') !== (r.lease || '') || (message.reply_to || '') !== (r.reply_to || '')) {
      op.state='unknown'; op.error='The stored result does not match this request. Inspect the key with the CLI; do not send a replacement automatically.'; return;
    }
    op.state='recorded';op.message=message;op.error='';
  }
  async function send(op, retry = false) {
    if (op.busy) return;
    op.busy=true;op.state='sending';op.error='';changed();
    try { accept(op,await call('message_send',copy(op.request))); }
    catch (error) {op.state=!retry&&preflightErrors.has(error.code)?'rejected':'unknown';op.error=(retry?'The prior outcome remains uncertain. ':'')+(error.message || 'The send outcome is unknown.');}
    finally {op.busy=false;changed();}
  }
  return {
    get operations(){return operations;},get warning(){return warning;},newKey:key,
    async submit(request){
      if(JSON.stringify(request).length>requestLimit)throw Error('This request is too large to retain safely in the dashboard. Use the CLI.');
      if (operations.filter(op=>['sending','unknown'].includes(op.state)).length>=8) throw Error('Resolve an existing uncertain operation before sending another (eight pending requests maximum).');
      if (operations.some(op=>op.request.idempotency_key===request.idempotency_key)) throw Error('This request key has already been submitted. Use its existing controls.');
      operations=operations.filter(op=>['sending','unknown'].includes(op.state)).concat(operations.filter(op=>!['sending','unknown'].includes(op.state)).slice(-7));
      const op={request:copy(request),state:'unknown',busy:false};operations.push(op);await send(op);return op;
    },
    async retry(op){if(op.state!=='unknown')return;return send(op,true);},
    async lookup(op){
      if(op.busy)return;op.busy=true;onChange();
      try {const result=await call('message_lookup',{idempotency_key:op.request.idempotency_key});if(result.found)accept(op,result.message);else op.error='No stored result yet. The original request may still commit. Only retry the exact request with this same key.';}
      catch(error){op.error=error.message || 'Could not check the stored result.';}
      finally{op.busy=false;changed();}
    },
    observe(messages){let updated=false;for(const op of operations)if(op.state==='unknown'||op.state==='recorded'){const msg=messages.find(m=>m.idempotency_key===op.request.idempotency_key);if(msg&&JSON.stringify(msg)!==JSON.stringify(op.message)){accept(op,msg);updated=true;}}if(updated)changed();},
  };
}

const stateDescriptions = {pending:'Delivery pending',available:'Available in agent inbox',queued:'Queued by the agent CLI',responded:'Agent CLI returned a reply',failed:'Delivery failed',unknown:'Native delivery outcome unknown'};
export function deliveryDescription(delivery) {
  const receipt=delivery.receipt;
  let text=stateDescriptions[delivery.state] || delivery.state || 'Delivery state unavailable';
  if(receipt?.acknowledged_at)text+=' · acknowledged by agent';else if(receipt?.received_at)text+=' · fetched by agent';
  if(delivery.error)text+=' · '+delivery.error;
  return text;
}

export function createOperatorMessaging({root,call,connected,onLease,document = globalThis.document,storage,key}) {
  const el=(tag,cls='',text=null)=>{const e=document.createElement(tag);e.className=cls;if(text!==null)e.textContent=text;return e;};
  const button=(text,action,cls='')=>{const e=el('button',cls,text);e.type='button';e.addEventListener('click',action);return e;};
  let contacts=[],messages=[],leases=[],review=null,reply=null,reviewStale=false,reviewWarning=null,reviewConfirm=null,lastContacts='',lastLeases='',lastMessages='';
  const heading=el('div','messaging-heading'),compose=el('section','message-composer'),form=el('form','message-form'),reviewBox=el('section','message-review'),opsBox=el('section','message-operations'),list=el('section','message-list');
  heading.append(el('h2','','Messages'),el('p','measure-note','Send to an exact registered session, or review a broadcast audience. Agent receipt and acknowledgement are shown separately from delivery.'));
  const recipient=el('select'),body=el('textarea'),lease=el('select'),error=el('p','message-error'),replyBox=el('div','message-reply-context'),reviewButton=el('button','message-primary','Review message');
  recipient.id='message-recipient';body.id='message-body';lease.id='message-lease';body.rows=5;body.maxLength=16384;body.placeholder='What should the agent know?';reviewButton.type='submit';error.setAttribute('role','status');
  function field(label,input){const wrap=el('label','message-field',label);wrap.append(input);return wrap;}
  form.append(field('Recipient',recipient),field('Message',body),field('Related lease (optional)',lease),replyBox,error,reviewButton);
  compose.append(el('h3','','Compose'),form,reviewBox);reviewBox.hidden=true;
  opsBox.setAttribute('aria-label','Recent send operations');list.setAttribute('aria-label','Message history');root.append(heading,compose,opsBox,list);
  let ops;
  function showError(text){error.textContent=text;}
  function renderOperations(){
    // Only send/lookup transitions change these nodes; background refreshes do
    // not remove focused action buttons or overwrite a draft.
    const restoreFocus=opsBox.contains(document.activeElement);
    opsBox.replaceChildren();
    if(ops.warning)opsBox.append(el('p','message-error',ops.warning));
    if(!ops.operations.length)return;
    opsBox.append(el('h3','','Send operations'));
    for(const op of [...ops.operations].reverse()){
      const card=el('article','message-operation'),r=op.request;
      card.append(el('strong','',`${r.audience} · ${op.state==='recorded'?'Recorded':op.state==='rejected'?'Not sent':op.state==='sending'?'Sending…':'Outcome uncertain'}`),el('p','message-key',`Request key: ${r.idempotency_key}`));
      card.append(el('p','message-body',r.body));
      const saved=el('details','message-saved-request');saved.append(el('summary','','Saved exact request'),jsonView(r,{label:'Request',document,expansion:op.expansion ||= new Map()}));card.append(saved);
      if(op.error)card.append(el('p','message-error',op.error));
      if(op.message){for(const [id,d]of Object.entries(op.message.deliveries||{}))card.append(el('p','measure-note',`${id}: ${deliveryDescription(d)}`));}
      if(op.state==='unknown'){
        card.append(el('p','measure-note','Checking is read-only. An exact retry uses the saved recipients and key; it cannot create a second recorded operation. Native delivery is never repeated for a recorded key.'));
        const check=button('Check stored result',()=>ops.lookup(op),'message-send-control'),retry=button('Retry exact request',()=>ops.retry(op),'message-send-control');check.disabled=retry.disabled=op.busy||!connected();card.append(check,retry);
      }
      opsBox.append(card);
    }
    if(restoreFocus){opsBox.tabIndex=-1;opsBox.focus();}
  }
  ops=createMessageOperations({call,storage,key,onChange:renderOperations});
  renderOperations();
  function updateOptions(){
    const selected=recipient.value,selectedLease=lease.value;
    recipient.replaceChildren();const placeholder=el('option','','Choose an agent');placeholder.value='';recipient.append(placeholder);
    if(contacts.length){const broadcast=el('option','',`Broadcast · all ${contacts.length} registered agents`);broadcast.value='broadcast';recipient.append(broadcast);}
    for(const c of contacts){const opt=el('option','',`${c.id} · ${c.adapter} · ${c.delivery || 'native'}`);opt.value=c.id;recipient.append(opt);}
    recipient.value=[...recipient.children].some(o=>o.value===selected)?selected:'';
    lease.replaceChildren();const none=el('option','','No related lease');none.value='';lease.append(none);
    for(const l of leases){const opt=el('option','',`${l.id} · ${l.request?.reason || l.state}`);opt.value=l.id;lease.append(opt);}
    lease.value=[...lease.children].some(o=>o.value===selectedLease)?selectedLease:'';
  }
  function returnToDraft(){review=null;reviewWarning=null;reviewConfirm=null;reviewBox.hidden=true;form.hidden=false;reviewButton.focus();}
  function renderReview(){
    reviewBox.replaceChildren();if(!review)return;
    reviewStale=!sameTargets(review,contacts);reviewBox.hidden=false;form.hidden=true;
    reviewBox.append(el('h3','',review.audience==='broadcast'?`Review broadcast · ${review.targets.length} recipients`:'Review exact recipient'),el('p','message-body',review.body));
    if(review.lease)reviewBox.append(el('p','measure-note',`Related lease: ${review.lease}`));
    if(reply)reviewBox.append(el('p','message-reply-context',`Reply to ${reply.id} from ${reply.sender}: ${reply.body}`));
    for(const c of review.targets){const card=el('div','message-target'),dl=el('dl');card.append(el('strong','',c.id));for(const [label,value]of [['Agent',c.adapter],['Session',c.session],['Registration',c.registration||'unavailable'],['Delivery',c.delivery||'native'],['Working directory',c.cwd],['Config directory',c.config_dir||'default'],['Remote',c.remote||'local'],['Declared idle',c.idle?'yes':'no']])dl.append(el('dt','',label),el('dd','',value||'—'));card.append(dl);reviewBox.append(card);}
    reviewBox.append(el('p','measure-note','Only these exact registered sessions and settings are authorized. Changed recipients or broadcast membership require a new review. Poll delivery makes the message available for the agent to fetch. Native delivery is one attempt; receipt is separate.'));
    reviewWarning=el('p','message-error','Recipient settings or broadcast membership changed. Return to editing and review again.');reviewWarning.hidden=!reviewStale;reviewBox.append(reviewWarning);
    const back=button('Back to editing',returnToDraft),confirm=button(review.audience==='broadcast'?'Confirm broadcast':'Confirm send',async()=>{
      if(!review||!connected()||!sameTargets(review,contacts))return;
      const frozen=copy(review);review=null;reviewBox.hidden=true;form.hidden=false;body.value='';reply=null;replyBox.replaceChildren();showError('');reviewButton.disabled=!connected();opsBox.tabIndex=-1;opsBox.focus();
      try{await ops.submit(frozen);}catch(err){showError(err.message);body.value=frozen.body;recipient.value=frozen.audience;lease.value=frozen.lease||'';}
    },'message-primary');reviewConfirm=confirm;confirm.disabled=reviewStale||!connected();reviewBox.append(back,confirm);
  }
  form.addEventListener('submit',event=>{event.preventDefault();showError('');if(!connected()){showError('Reconnect before reviewing a send.');return;}try{review=prepareMessage({audience:recipient.value,body:body.value,lease:lease.value,reply_to:reply?.id||''},contacts,ops.newKey());renderReview();reviewBox.querySelector('button')?.focus();}catch(err){showError(err.message);}});
  function startReply(message){
    if(!contacts.some(c=>c.id===message.sender)||message.audience!=='operator')return;
    review=null;reviewBox.hidden=true;form.hidden=false;reply=message;recipient.value=message.sender;lease.value=message.lease||'';
    replyBox.replaceChildren(el('p','',`Reply to ${message.sender} · ${message.id}`),el('p','message-body',message.body),button('Remove reply context',()=>{reply=null;replyBox.replaceChildren();}));body.focus();
  }
  recipient.addEventListener('change',()=>{if(reply&&recipient.value!==reply.sender){reply=null;replyBox.replaceChildren();}});
  const cards=new Map();
  function renderHistory(){
    const keep=new Set();
    for(const message of [...messages].sort((a,b)=>Date.parse(b.created_at)-Date.parse(a.created_at))){
      keep.add(message.id);const signature=JSON.stringify([message,contacts.some(c=>c.id===message.sender)]);let entry=cards.get(message.id);
      if(!entry){entry={element:el('article','item message-history-item'),signature:''};cards.set(message.id,entry);list.append(entry.element);}
      if(entry.signature===signature||entry.element.contains(document.activeElement))continue;
      entry.signature=signature;const card=entry.element;card.replaceChildren();card.id='message-'+message.id;const head=el('div','item-heading');head.append(el('strong','',`${message.sender} → ${message.audience}`),el('span','',new Date(message.created_at).toLocaleString()));card.append(head,el('p','message-body',message.body));
      if(message.reply_to)card.append(button('View original message '+message.reply_to,()=>focusMessage(message.reply_to),'message-context-link'));
      if(message.lease)card.append(button('Lease '+message.lease,()=>onLease(message.lease),'message-context-link'));
      for(const [id,d]of Object.entries(message.deliveries||{})){card.append(el('p','message-delivery',`${id}: ${deliveryDescription(d)}`));if(d.receipt?.text)card.append(el('p','message-reply-context',d.receipt.text));if(d.reply)card.append(el('p','message-reply-context',d.reply));}
      if(message.audience==='operator'){if(contacts.some(c=>c.id===message.sender))card.append(button('Reply',()=>startReply(message)));else card.append(el('p','measure-note','Reply unavailable: the sender is not a currently registered agent contact.'));}
    }
    for(const [id,entry]of cards)if(!keep.has(id)&&!entry.element.contains(document.activeElement)){entry.element.remove();cards.delete(id);}
    // Existing nodes keep focus; reorder only while focus is outside history.
    if(!list.contains(document.activeElement))for(const m of [...messages].sort((a,b)=>Date.parse(b.created_at)-Date.parse(a.created_at)))list.append(cards.get(m.id).element);
    let noMessages=list.querySelector('.message-empty');if(!messages.length&&!noMessages){noMessages=el('p','empty message-empty','No messages yet. Agents can write with arm message operator.');list.append(noMessages);}if(messages.length)noMessages?.remove();
  }
  function focusMessage(id){const entry=cards.get(id);if(!entry){showError('The original message is outside the latest 100 loaded messages. Its linked ID is retained in this message.');return;}entry.element.tabIndex=-1;entry.element.focus();entry.element.scrollIntoView({block:'center',behavior:'smooth'});}
  return {focusMessage,update(data){
    contacts=data.contacts||[];messages=data.messages||[];leases=data.leases||[];
    const contactSignature=JSON.stringify(contacts),leaseSignature=JSON.stringify(leases.map(l=>[l.id,l.state,l.request?.reason]));
    if(contactSignature!==lastContacts||leaseSignature!==lastLeases){updateOptions();lastLeases=leaseSignature;}
    if(contactSignature!==lastContacts){lastContacts=contactSignature;if(review){reviewStale=!sameTargets(review,contacts);if(reviewWarning)reviewWarning.hidden=!reviewStale;}}
    reviewButton.disabled=!connected();if(reviewConfirm)reviewConfirm.disabled=!connected()||reviewStale;
    for(const b of opsBox.querySelectorAll('.message-send-control'))b.disabled=!connected()||ops.operations.some(op=>op.busy); // busy is bounded and explicit
    ops.observe(messages);
    const messageSignature=JSON.stringify([messages,contacts]);if(messageSignature!==lastMessages){lastMessages=messageSignature;renderHistory();}
  }};
}
