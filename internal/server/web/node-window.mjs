import {validHostID} from './mesh-model.mjs';
// Only public rendezvous data enters history. Credentials travel once over an
// origin-scoped channel, then are exchanged for an immutable host-bound session.
export function nodeWindowRoute(address) {
 const p=new URL(address).searchParams,host=p.get('host'),nonce=p.get('window');
 if(!p.has('host')&&!p.has('window'))return null;
 if(p.getAll('host').length!==1||!validHostID(host)||p.getAll('window').length>1||(nonce!==null&&!validHostID(nonce)))return {error:'Invalid node window. Open the exact node from an authenticated local Mesh page.'};
 return {host,nonce};
}
export function openNodeWindow({entry,call,view='resources',window=globalThis.window,Channel=globalThis.BroadcastChannel,random=()=>crypto.randomUUID().replaceAll('-',''),onError=()=>{},timeout=60000}) {
 const host=entry.host;if(!validHostID(host)||!Channel)throw Error('Secure node-window handoff is unavailable in this browser.');
 const nonce=random(),url=new URL(window.location.href);url.search='';url.hash='';url.searchParams.set('host',host);url.searchParams.set('window',nonce);url.searchParams.set('view',view==='screen'?'screen':'resources');
 const channel=new Channel('arm-node-window:'+nonce);let used=false,closed=false;
 const close=()=>{if(closed)return;closed=true;clearTimeout(timer);channel.close();};
 const timer=setTimeout(()=>{close();onError('Node window authentication timed out. Open it again from Mesh.');},timeout);
 channel.onmessage=async event=>{const data=event.data;if(data?.kind==='complete'&&data.host===host&&data.nonce===nonce){close();return;}if(closed||used||data?.kind!=='ready'||data.host!==host||data.nonce!==nonce)return;used=true;
  try{const result=await call('node_open',{host,window_nonce:nonce});if(!closed)channel.postMessage({kind:'ticket',host,nonce,ticket:result.ticket});}catch(e){if(!closed)channel.postMessage({kind:'error',host,nonce,message:e.message});close();onError(e.message);}
 };
 // noopener deliberately prevents any long-lived opener credential path.
 window.open(url.href,'_blank','noopener');return close;
}
export async function receiveNodeWindow({route,exchange,Channel=globalThis.BroadcastChannel,timeout=65000}){
 if(route.error)throw Error(route.error);if(!route.nonce||!Channel)throw Error('Open this node from an authenticated local Mesh page.');
 const channel=new Channel('arm-node-window:'+route.nonce);
 try{return await new Promise((resolve,reject)=>{
  let consumed=false;const timer=setTimeout(()=>reject(Error('Node window handoff expired. Open it again from Mesh.')),timeout);
  channel.onmessage=event=>{const data=event.data;if(consumed||data?.host!==route.host||data.nonce!==route.nonce)return;
   if(data.kind==='error'){consumed=true;clearTimeout(timer);reject(Error(data.message||'Node window authentication failed.'));}
   if(data.kind==='ticket'&&/^[a-f0-9]{64}$/.test(data.ticket??'')){consumed=true;clearTimeout(timer);Promise.resolve(exchange(data.ticket,route.host,route.nonce)).then(value=>{channel.postMessage({kind:'complete',host:route.host,nonce:route.nonce});resolve(value);},reject);}
  };channel.postMessage({kind:'ready',host:route.host,nonce:route.nonce});
 });}finally{channel.close();}
}

// Only an explicit gateway policy rejection invalidates a stored host context.
export function nodeContextClosed(context,event){return Boolean(context)&&event?.code===1008&&event?.reason==='node_context_changed';}
