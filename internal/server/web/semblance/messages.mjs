import {createMessageOperations, operatorReplyTarget, prepareMessage, sameTargets, deliveryDescription} from '../operator-messaging.mjs';
const copy=value=>JSON.parse(JSON.stringify(value));
const RDF_TYPE='http://www.w3.org/1999/02/22-rdf-syntax-ns#type',ARM='https://arm.local/ui#';
const has=(root,component)=>root===component||Boolean(root?.children?.some(child=>has(child,component)));

// Transport/recovery is shared with the existing protocol implementation. The
// view is entirely stock Semblance RDF components and graph event callbacks.
export function createOperatorMessaging({ui,root,call,connected,onLease=()=>{},storage,key}) {
  let contacts=[],messages=[],leases=[],review=null,reply=null,reviewStale=false,reviewWarning=null,reviewConfirm=null;
  let lastContacts='',lastLeases='',lastMessages='',historyHeld=false,disposed=false,ops;
  const typed=(component,name)=>{ui.put?.(component.iri,RDF_TYPE,ARM+name,true);return component;};
  const messageText=value=>typed(ui.text(value),'MessageBody');
  const facts=pairs=>typed(ui.descriptions(pairs),'MessageFacts');
  typed(root,'MessageSurface').set({measure:'ReadingMeasure'});
  const form=ui.stack(),reviewBox=ui.stack([],{hidden:true}),opsBox=ui.stack(),list=ui.messageList('Operator conversation'),emptyHistory=ui.text('No messages yet. Agents can write with arm message operator.'),replyBox=ui.stack(),error=ui.text('');
  const recipient=ui.select('Recipient',[{value:'',label:'Choose an agent'}]),body=ui.area('Message',{value:'',placeholder:'What should the agent know?'}),lease=ui.select('Related lease (optional)',[{value:'',label:'No related lease'}]);
  const reviewButton=ui.button('Review message',()=>{
    showError('');if(disposed||!connected()){showError('Reconnect before reviewing a send.');return;}
    try {
      if(reply?.sender_contact&&!operatorReplyTarget(reply,contacts))throw Error('The original sender changed. This reply cannot target a replacement registration.');
      review=prepareMessage({audience:recipient.value,body:body.value,lease:lease.value,reply_to:reply?.id||''},contacts,ops.newKey());renderReview();
    } catch(err){showError(err.message);}
  });
  form.append(recipient,body,lease,replyBox,error,reviewButton);
  root.append(ui.text('Direct messages, agent replies, and reviewed broadcasts. Delivery and acknowledgement are separate.'),ui.card('Write a message',[form,reviewBox]),opsBox,ui.heading('Conversation',3),emptyHistory,list);
  const operationControls=[];
  function showError(text){if(!disposed)error.set({label:text});}
  function updateConnection() {
    if(disposed)return;reviewButton.disabled=!connected();if(reviewConfirm)reviewConfirm.disabled=!connected()||reviewStale;
    for(const {control,operation}of operationControls)control.disabled=!connected()||operation.busy||ops.operations.some(op=>op.busy);
  }
  function renderOperations() {
    if(disposed)return;
    const focused=has(opsBox,ui.activeComponent);operationControls.length=0;
    const children=[];
    if(ops.warning)children.push(ui.text(ops.warning,{tone:'warning'}));
    if(ops.operations.length)children.push(ui.heading('Send operations',3));
    for(const op of [...ops.operations].reverse()) {
      const r=op.request,detail=[facts([['Request key',r.idempotency_key]]),typed(ui.text('Message'),'MessageLabel'),messageText(r.body),ui.json(r,'Saved exact request')];
      if(op.error)detail.push(ui.text(op.error,{tone:'warning'}));
      if(op.message)for(const [id,d]of Object.entries(op.message.deliveries||{}))detail.push(facts([['Recipient',id],['Delivery',deliveryDescription(d)]]));
      if(op.state==='unknown') {
        detail.push(ui.text('Checking is read-only. An exact retry uses the saved recipients and key; it cannot create a second recorded operation. Native delivery is never repeated for a recorded key.'));
        const check=ui.button('Check stored result',()=>{if(connected()&&!op.busy)return ops.lookup(op);}),retry=ui.button('Retry exact request',()=>{if(connected()&&!op.busy)return ops.retry(op);});
        operationControls.push({control:check,operation:op},{control:retry,operation:op});detail.push(ui.stack([check,retry],{row:true}));
      }
      children.push(ui.card(`${r.audience} · ${op.state==='recorded'?'Recorded':op.state==='rejected'?'Not sent':op.state==='sending'?'Sending…':'Outcome uncertain'}`,detail));
    }
    opsBox.replace(...children);updateConnection();if(focused)opsBox.focus();
  }
  ops=createMessageOperations({call,storage,key,onChange:renderOperations});renderOperations();
  function updateOptions() {
    const selected=recipient.value,selectedLease=lease.value;
    const targets=[{value:'',label:'Choose an agent'},...(contacts.length?[{value:'broadcast',label:`Broadcast · all ${contacts.length} registered agents`}]:[]),...contacts.map(c=>({value:c.id,label:`${c.id} · ${c.adapter} · ${c.delivery||'native'}`}))];
    const related=[{value:'',label:'No related lease'},...leases.map(l=>({value:l.id,label:`${l.id} · ${l.request?.reason||l.state}`}))];
    recipient.set({options:targets,value:targets.some(o=>o.value===selected)?selected:''});lease.set({options:related,value:related.some(o=>o.value===selectedLease)?selectedLease:''});
  }
  function returnToDraft(){review=null;reviewWarning=null;reviewConfirm=null;reviewBox.hidden=true;form.hidden=false;reviewButton.focus();}
  function renderReview() {
    if(!review)return;reviewStale=!sameTargets(review,contacts);reviewBox.hidden=false;form.hidden=true;
    const children=[ui.heading(review.audience==='broadcast'?`Review broadcast · ${review.targets.length} recipients`:'Review exact recipient',3),typed(ui.text('Message'),'MessageLabel'),messageText(review.body)];
    if(review.lease)children.push(facts([['Related lease',review.lease]]));
    if(reply)children.push(ui.card('Reply context',[facts([['Message ID',reply.id],['From',reply.sender]]),messageText(reply.body)]));
    for(const c of review.targets)children.push(ui.card(c.id,[facts([['Agent',c.adapter],['Session',c.session],['Registration',c.registration||'unavailable'],['Delivery',c.delivery||'native'],['Working directory',c.cwd],['Config directory',c.config_dir||'default'],['Remote',c.remote||'local'],['Declared idle',c.idle?'yes':'no']].map(([label,value])=>[label,value||'—']))]));
    children.push(ui.text('Only these exact registered sessions and settings are authorized. Changed recipients or broadcast membership require a new review. Poll delivery makes the message available for the agent to fetch. Native delivery is one attempt; receipt is separate.'));
    reviewWarning=ui.text('Recipient settings or broadcast membership changed. Return to editing and review again.',{hidden:!reviewStale,tone:'warning'});children.push(reviewWarning);
    const back=ui.button('Back to editing',returnToDraft);
    reviewConfirm=ui.button(review.audience==='broadcast'?'Confirm broadcast':'Confirm send',async()=>{
      if(disposed||!review||!connected()||!sameTargets(review,contacts))return;
      const frozen=copy(review);review=null;reviewBox.hidden=true;form.hidden=false;body.value='';reply=null;replyBox.replace();showError('');reviewButton.disabled=!connected();opsBox.focus();
      try{await ops.submit(frozen);}catch(err){if(!disposed){showError(err.message);body.value=frozen.body;recipient.value=frozen.audience;lease.value=frozen.lease||'';}}
    });
    reviewConfirm.disabled=reviewStale||!connected();children.push(ui.stack([back,reviewConfirm],{row:true}));reviewBox.replace(...children);back.focus();
  }
  function startReply(message) {
    if(disposed)return;const target=operatorReplyTarget(message,contacts);if(!target)return;
    review=null;reviewBox.hidden=true;form.hidden=false;reply=message;recipient.value=target.id;lease.value=message.lease||'';
    replyBox.replace(ui.text(`Reply to ${message.sender} · ${message.id}`),messageText(message.body),ui.button('Remove reply context',()=>{reply=null;replyBox.replace();}));body.focus();
  }
  recipient.on('change',()=>{if(reply&&recipient.value!==(reply.sender_contact?.id||reply.sender)){reply=null;replyBox.replace();}});
  const cards=new Map();
  function renderHistory() {
    const focused=has(list,ui.activeComponent),sorted=[...messages].sort((a,b)=>Date.parse(a.created_at)-Date.parse(b.created_at)),keep=new Set();
    for(const message of sorted) {
      keep.add(message.id);const signature=JSON.stringify([message,operatorReplyTarget(message,contacts)]);let entry=cards.get(message.id);
      if(!entry){entry={element:ui.stack(),signature:''};cards.set(message.id,entry);}
      if(entry.signature===signature||has(entry.element,ui.activeComponent))continue;
      entry.signature=signature;
      const children=[messageText(message.body)];
      if(message.reply_to)children.push(ui.button('View original message '+message.reply_to,()=>focusMessage(message.reply_to)));
      if(message.lease)children.push(ui.button('Lease '+message.lease,()=>onLease(message.lease)));
      for(const [id,d]of Object.entries(message.deliveries||{})){children.push(facts([['Recipient',id],['Delivery',deliveryDescription(d)]]));if(d.receipt?.text)children.push(messageText(d.receipt.text));if(d.reply)children.push(messageText(d.reply));}
      if(message.audience==='operator')children.push(operatorReplyTarget(message,contacts)?ui.button('Reply',()=>startReply(message)):ui.text('Reply unavailable: the original sender is no longer registered with these settings.'));
      entry.element.replace(...children);
    }
    for(const [id,entry]of cards)if(!keep.has(id)&&!has(entry.element,ui.activeComponent)){cards.delete(id);entry.element.dispose();}
    emptyHistory.hidden=sorted.length>0;
    historyHeld=focused;
    if(!focused)list.setMessages(sorted.map(m=>({id:m.id,author:m.sender,at:m.created_at,summary:m.body,body:cards.get(m.id).element})));
  }
  // A live update never replaces the control the operator is using. Once
  // focus leaves the stock log, render its latest held bodies and membership
  // even when the following backend snapshot is byte-identical.
  list.on('focusout',()=>{
    if(disposed||!historyHeld||has(list,ui.activeComponent))return;
    renderHistory();
  });
  function focusMessage(id){if(disposed)return;const entry=cards.get(id);if(!entry){showError('The original message is outside the loaded message window. Its linked ID is retained in this message.');return;}entry.element.focus();}
  return {element:root,focusMessage,updateConnection,update(data) {
    if(disposed)return;contacts=data.contacts||[];messages=data.messages||[];leases=data.leases||[];
    const contactSignature=JSON.stringify(contacts),leaseSignature=JSON.stringify(leases.map(l=>[l.id,l.state,l.request?.reason]));
    if(contactSignature!==lastContacts||leaseSignature!==lastLeases){updateOptions();lastLeases=leaseSignature;}
    if(contactSignature!==lastContacts){lastContacts=contactSignature;if(review){reviewStale=!sameTargets(review,contacts);if(reviewWarning)reviewWarning.hidden=!reviewStale;}}
    updateConnection();ops.observe(messages);
    const messageSignature=JSON.stringify([messages,contacts]);if(messageSignature!==lastMessages||(historyHeld&&!has(list,ui.activeComponent))){lastMessages=messageSignature;renderHistory();}
  },dispose(){disposed=true;}};
}
