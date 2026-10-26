import {test} from 'node:test';
import assert from 'node:assert/strict';
import {nodeWindowRoute,openNodeWindow,receiveNodeWindow} from './node-window.mjs';
import {dashboardRouteURL,readDashboardRoute} from './route.mjs';
const host='a'.repeat(32),nonce='b'.repeat(32);
class Channel {
 static instances=[];
 constructor(name){this.name=name;this.closed=false;Channel.instances.push(this);}
 postMessage(data){for(const peer of Channel.instances)if(peer!==this&&!peer.closed&&peer.name===this.name)queueMicrotask(()=>peer.onmessage?.({data}));}
 close(){this.closed=true;}
}
test('public host window routes reject ambiguous or wrong targets and survive canonical history without credentials',()=>{
 assert.equal(nodeWindowRoute('http://localhost/'),null);
 for(const query of ['?host=localhost','?host='+host+'&host='+host,'?window='+nonce,'?host='+host+'&window=bad'])assert.ok(nodeWindowRoute('http://localhost/'+query).error);
 const route=readDashboardRoute('http://localhost/?host='+host+'&view=leases&window='+nonce+'&token=secret');assert.equal(route.host,host);
 assert.equal(dashboardRouteURL({...route,view:'host'},'http://localhost/'),'/?view=host&host='+host);
});
test('one-use channel handoff opens noopener public URL and transfers only a fresh exact-host ticket',async()=>{
 Channel.instances=[];let address,calls=0,exchanges=0;
 const window={location:{href:'http://127.0.0.1:1/?view=mesh#secret'},open(url,name,features){address=url;assert.equal(features,'noopener');assert.equal(name,'_blank');}};
 const close=openNodeWindow({entry:{host},window,Channel,random:()=>nonce,call:async(op,q)=>{calls++;assert.equal(op,'node_open');assert.deepEqual(q,{host,window_nonce:nonce});return {ticket:'c'.repeat(64)};}});
 assert.equal(new URL(address).hash,'');assert.equal(new URL(address).searchParams.has('token'),false);
 const session=await receiveNodeWindow({route:nodeWindowRoute(address),Channel,exchange:async(ticket,target,n)=>{exchanges++;assert.equal(target,host);assert.equal(n,nonce);assert.equal(ticket,'c'.repeat(64));return {token:'d'.repeat(64),context:{host}};}});
 await new Promise(resolve=>setImmediate(resolve));assert.equal(calls,1);assert.equal(exchanges,1);assert.equal(session.context.host,host);assert.equal(Channel.instances.every(c=>c.closed),true);close();
});
test('foreign ready announcements cannot mint a ticket; duplicate ready cannot request a second one',async()=>{
 Channel.instances=[];let calls=0;const close=openNodeWindow({entry:{host},window:{location:{href:'http://localhost/'},open(){}},Channel,random:()=>nonce,call:async()=>{calls++;return {ticket:'c'.repeat(64)};}});
 const attacker=new Channel('arm-node-window:'+nonce);attacker.postMessage({kind:'ready',host:'e'.repeat(32),nonce});await new Promise(resolve=>setImmediate(resolve));assert.equal(calls,0);
 attacker.postMessage({kind:'ready',host,nonce});attacker.postMessage({kind:'ready',host,nonce});await new Promise(resolve=>setImmediate(resolve));assert.equal(calls,1);close();attacker.close();
});
test('Mesh Screen opens the exact node window on its viewer tab',()=>{
 Channel.instances=[];let address;
 const close=openNodeWindow({entry:{host},view:'screen',window:{location:{href:'http://localhost/?view=mesh'},open(url){address=url;}},Channel,random:()=>nonce,call:async()=>({ticket:'c'.repeat(64)})});
 assert.equal(new URL(address).searchParams.get('view'),'screen');
 assert.equal(new URL(address).searchParams.get('host'),host);
 assert.equal(new URL(address).hash,'');close();
});

import {nodeContextClosed} from './node-window.mjs';
test('explicit gateway context rejection stops reconnect while transient failures do not',()=>{const context={host:'a'.repeat(32)};assert.equal(nodeContextClosed(context,{code:1008,reason:'node_context_changed'}),true);for(const event of [{code:1006,reason:''},{code:1008,reason:'temporary failure'},{code:1000,reason:'node_context_changed'}])assert.equal(nodeContextClosed(context,event),false);assert.equal(nodeContextClosed(null,{code:1008,reason:'node_context_changed'}),false);});
