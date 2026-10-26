const hex = bytes => [...bytes].map(value => value.toString(16).padStart(2, '0')).join('');
const id = value => /^[a-f0-9]{32}$/.test(value);
const serviceName = value => /^[a-z0-9][a-z0-9._-]{0,63}$/.test(value);

export function forwardSourceDraft({workspace,target,service,revision,bind,minutes,reason}) {
  const grantRevision=Number(revision), lifetime=Number(minutes), port=Number(bind?.split(':').at(-1));
  if(!id(workspace)||!id(target)||!serviceName(service)||!Number.isSafeInteger(grantRevision)||grantRevision<1||
     !/^(127\.0\.0\.1|\[::1\]):(0|[1-9][0-9]{0,4})$/.test(bind)||!Number.isInteger(port)||port>65535||
     !Number.isInteger(lifetime)||lifetime<1||lifetime>15||!reason?.trim()||reason.length>4096) {
    throw Error('Enter exact workspace and target IDs, named service, grant revision, literal loopback bind, 1–15 minute lifetime and reason.');
  }
  return {workspace,target,service,grant_revision:grantRevision,bind,ttl_ns:lifetime*60e9,reason};
}

// The raw bearer is returned only to this browser dialog. The backend receives
// a digest, and neither the key nor token is placed in a URL or saved storage.
export async function prepareForwardSource(fields, cryptoAPI=globalThis.crypto) {
  const draft=forwardSourceDraft(fields);
  if(!cryptoAPI?.getRandomValues||!cryptoAPI?.subtle?.digest)throw Error('A secure browser random source is required.');
  const key=hex(cryptoAPI.getRandomValues(new Uint8Array(16)));
  const token=hex(cryptoAPI.getRandomValues(new Uint8Array(32)));
  const digest=hex(new Uint8Array(await cryptoAPI.subtle.digest('SHA-256',new TextEncoder().encode(token))));
  return {token,request:{key,...draft,token_sha256:digest}};
}

export function createForwardConsole({root,call,connected,document=root.ownerDocument,cryptoAPI=globalThis.crypto}) {
  const el=(tag,cls='',content='')=>{const n=document.createElement(tag);n.className=cls;n.textContent=content;return n;};
  const button=(label,action,cls='')=>{const n=el('button',cls,label);n.type='button';n.addEventListener('click',action);return n;};
  const form=el('form','mesh-forward-form'),review=el('section','mesh-grant-card'),records=el('section','mesh-operations'),message=el('p','mesh-inline-notice');
  message.setAttribute('role','status');
  let prepared=null,disposed=false,reading=false;
  const fields={};
  const field=(name,label,value,type='text')=>{
    const wrapper=el('label','mesh-field'),caption=el('span','',label),input=el('input');
    input.name=name;input.type=type;input.value=value;wrapper.append(caption,input);form.append(wrapper);fields[name]=input;
  };
  form.append(el('h3','','Start a local source listener'));
  field('workspace','Workspace ID','');field('target','Destination host ID','');field('service','Destination service name','');
  field('revision','Destination grant revision','1','number');field('bind','Local loopback bind','127.0.0.1:0');
  field('minutes','Listener lifetime in minutes','10','number');field('reason','Resource reservation reason','');
  const prepare=el('button','primary','Prepare private token');prepare.type='submit';form.append(prepare);
  root.replaceChildren(el('p','mesh-muted','The destination grants a named service. Relays carry bounded transport only. A local token protects each source listener.'),message,form,review,records);
  const warn=text=>{message.textContent=text};
  form.addEventListener('submit',async event=>{
    event.preventDefault();if(disposed||!connected()){warn('Reconnect before starting a listener.');return}
    if(prepared?.uncertain){warn(`Inspect listener ${prepared.request.key} before preparing another source.`);return}
    if(prepare.disabled)return;
    prepare.disabled=true;
    try{
      prepared=await prepareForwardSource(Object.fromEntries(Object.entries(fields).map(([name,input])=>[name,input.value])),cryptoAPI);
      showPrepared();warn('Save the token privately before starting. It is shown only in this dialog.');
    }catch(error){warn(error.message)}finally{prepare.disabled=!connected()||!!prepared?.uncertain}
  });
  function showPrepared(){
    review.replaceChildren();if(!prepared)return;
    const {token,request}=prepared;
    review.append(el('h3','','Exact source review'),el('p','mesh-mono',`Key ${request.key} · ${request.workspace} → ${request.target} / ${request.service} · grant ${request.grant_revision} · bind ${request.bind}`),
      el('p','mesh-muted','This token is a local bearer credential. Save the key and token privately. The token cannot be recovered from ARM status.'),el('code','mesh-mono',token));
    const saved=el('label','mesh-field'),check=el('input');check.type='checkbox';saved.append(check,el('span','','I saved the key and token privately'));review.append(saved);
    const start=button('Start exact listener',async()=>{
      if(start.disabled)return;
      if(!check.checked){warn('Save the token before starting.');return}
      if(!connected()){warn('Reconnect and inspect this key before any new start.');return}
      start.disabled=true;prepared.sent=true;
      try{const status=await call('mesh_forward_start',request);warn(`Listener ${request.key} is ${status.state} at ${status.bind}. The destination grant is checked on each connection.`);await refresh()}
      catch(error){prepared.uncertain=true;prepare.disabled=true;warn(`Start outcome for ${request.key} is uncertain: ${error.message}. Keep this token; inspect the exact key before starting again.`)}
    },'primary');review.append(start);
  }
  function statusLine(row){return `${row.state} · ${row.key} · ${row.bind} → ${row.target}/${row.service} · grant ${row.grant_revision}${row.failure?' · '+row.failure:''}`}
  function render(rows){
    records.replaceChildren(el('h3','','Source listeners'));
    const exact=el('div','mesh-toolbar-actions'),lookup=el('input');lookup.placeholder='Exact 32-hex listener key';lookup.setAttribute('aria-label','Exact TCP listener key');
    exact.append(lookup,button('Inspect exact key',async()=>{
      if(!connected()||!id(lookup.value)){warn('Enter an exact 32-hex listener key.');return}
      try{const row=await call('mesh_forward_status',{key:lookup.value});warn(statusLine(row));}catch(error){warn(error.message)}
    }));records.append(exact);
    if(!rows.length){records.append(el('p','mesh-muted','No recorded local source listeners. Destination grant review remains in the native CLI.'));return}
    for(const row of rows){
      const item=el('article','mesh-grant-card');item.append(el('p','mesh-mono',statusLine(row)));
      if(row.expires_at)item.append(el('p','mesh-muted',`Expires ${new Date(row.expires_at).toLocaleString()}`));
      if(row.state==='active'||row.state==='ending'){
        const stop=button('Review Stop',()=>{
          stop.disabled=true;
          const confirm=button('Stop this listener',async()=>{
            if(confirm.disabled)return;
            if(!connected()){warn('Reconnect before Stop.');return}
            confirm.disabled=true;
            try{const result=await call('mesh_forward_stop',{key:row.key});warn(statusLine(result));await refresh()}catch(error){warn(`Stop outcome for ${row.key} is uncertain: ${error.message}. Inspect its exact key.`)}
          },'danger');
          item.append(el('p','mesh-note warning',`Stop ${row.key} at ${row.bind}; active streams close, and the port/resource charge remains until join and exact probe.`),confirm);
        });item.append(stop);
      }
      if(row.state==='needs_attention'){
        const recovery=button('Review local recovery',()=>{
          recovery.disabled=true;
          const reason=el('input');reason.placeholder='Exact cleanup reason';reason.setAttribute('aria-label','TCP listener recovery reason');
          const confirm=button('Recover exact charge',async()=>{
              if(confirm.disabled)return;
              if(!connected()||!reason.value.trim()){warn('Reconnect and enter a meaningful recovery reason.');return}
              confirm.disabled=true;
              try{const result=await call('mesh_forward_recover',{key:row.key,reason:reason.value});warn(statusLine(result));await refresh()}
              catch(error){warn(`Recovery for ${row.key} remains uncertain: ${error.message}. Inspect the exact key.`)}
            },'danger');
          item.append(el('p','mesh-note warning','Only the old local port/resource charge can be reconciled after the original backend process has ended. Application delivery remains unknown.'),reason,confirm);
        });item.append(recovery);
      }
      records.append(item);
    }
  }
  async function refresh(){
    if(disposed||!connected()||reading)return;reading=true;
    try{const rows=await call('mesh_forward_list',{});if(!disposed)render(Array.isArray(rows)?rows:[])}
    catch(error){if(!disposed)warn(`Source listener list unavailable: ${error.message}`)}finally{reading=false}
  }
  return {refresh,updateConnection(){prepare.disabled=!connected()||!!prepared?.uncertain;if(!connected())warn('Disconnected. Inspect exact listener keys after reconnect; no mutation is replayed.')},dispose(){disposed=true;prepared=null;root.replaceChildren()}};
}
