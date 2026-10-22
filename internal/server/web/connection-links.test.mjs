import {test} from 'node:test';
import assert from 'node:assert/strict';
import {connectionLink,createConnectionLinkHandler,createDashboardAuthentication} from './connection-links.mjs';
import {createDashboardHistory} from './route.mjs';
import {createMessageOperations,prepareMessage} from './operator-messaging.mjs';
const ticket='a'.repeat(64),second='b'.repeat(64),token='c'.repeat(64),now=Date.parse('2026-10-21T12:00:00Z');
const credential=value=>({token:value,expires_at:'2026-10-21T20:00:00Z'});
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return{promise,resolve,reject};};
function fixture(address){
 let url=new URL(address);const entries=[address],calls=[],routes=[];
 const location={get href(){return url.href;},get pathname(){return url.pathname;},get search(){return url.search;},get hash(){return url.hash;}};
 const history={replaceState(state,title,target){assert.equal(state,null);url=new URL(target,url);entries[entries.length-1]=url.href;},pushState(state,title,target){assert.equal(state,null);url=new URL(target,url);entries.push(url.href);}};
 const navigation=createDashboardHistory({location,history,onRoute:r=>routes.push(r)});
 const handler=createConnectionLinkHandler({location,navigation,onConnect:link=>{assert.equal(location.hash,'');calls.push(link);},onStored:()=>calls.push('stored')});
 return{location,handler,calls,entries,routes,set(urlText){url=new URL(urlText,url);entries.push(url.href);}};
}

test('connection fragments are exact, bounded single tickets and ordinary anchors are not credentials',()=>{
 assert.deepEqual(connectionLink('#connect='+ticket),{ticket});assert.equal(connectionLink('#main'),null);
 for(const hash of ['#connect=','#connect=short','#connect='+ticket+'&connect='+second,'#connect='+ticket.toUpperCase()])assert.ok(connectionLink(hash).error);
});

test('initial and same-document fresh links are captured before route cleanup and consumed once across both events',()=>{
 const f=fixture('http://127.0.0.1:1/?view=messages&private=drop#connect='+ticket);f.handler.start();assert.deepEqual(f.calls,[{ticket}]);assert.deepEqual(f.routes.at(-1),{view:'messages',lease:null,pool:null,poolAfter:null,poolBefore:null});
 f.set('/?view=resources#connect='+second);f.handler.change();f.handler.change();assert.deepEqual(f.calls,[{ticket},{ticket:second}]);assert.ok(f.entries.every(e=>!e.includes('connect')&&!e.includes(ticket)&&!e.includes(second)&&!e.includes('private')));
 f.set('/?view=host');f.handler.change();assert.equal(f.calls.length,2);assert.equal(f.routes.at(-1).view,'host');
});

test('no-ticket initial load restores stored credentials once; invalid new ticket is sanitized without exchange',()=>{
 const f=fixture('http://127.0.0.1:1/?view=leases');f.handler.start();f.handler.change();assert.deepEqual(f.calls,['stored']);
 f.set('/?view=messages#connect=invalid');f.handler.change();assert.ok(f.calls[1].error);assert.equal(f.location.hash,'');
});

test('new link cancels older exchange and only latest credentials can win or reach storage/connect',async()=>{
 const first=deferred(),next=deferred(),signals=[],saved=[],connected=[],errors=[];let starts=0,clears=0;
 const auth=createDashboardAuthentication({now:()=>now,exchange:(value,signal)=>{signals.push(signal);return value===ticket?first.promise:next.promise;},readSession:()=>assert.fail('fresh link restored old token'),writeSession:s=>saved.push(s),clearSession:()=>clears++,onStart:()=>starts++,onSession:s=>connected.push(s),onError:e=>errors.push(e)});
 const old=auth.start({ticket}),latest=auth.start({ticket:second});assert.equal(signals[0].aborted,true);next.resolve(credential(token));await latest;first.resolve(credential('d'.repeat(64)));await old;
 assert.equal(starts,2);assert.equal(clears,2);assert.deepEqual(saved,[credential(token)]);assert.deepEqual(connected,saved);assert.deepEqual(errors,[]);
});

test('failed obsolete exchange cannot overwrite new connection state; invalid sessions fail without leaking secrets',async()=>{
 const old=deferred(),errors=[],sessions=[];const auth=createDashboardAuthentication({now:()=>now,exchange:value=>value===ticket?old.promise:Promise.resolve(credential(token)),readSession:()=>null,writeSession(){},clearSession(){},onStart(){},onSession:s=>sessions.push(s),onError:e=>errors.push(e.message)});
 const first=auth.start({ticket});await auth.start({ticket:second});old.reject(Error('obsolete exchange'));await first;assert.equal(sessions.length,1);assert.equal(errors.length,0);
 await auth.start({error:'Invalid connection link'});assert.deepEqual(errors,['Invalid connection link']);
 const invalid=createDashboardAuthentication({now:()=>now,exchange:async()=>({token:ticket,expires_at:'invalid'}),readSession:()=>null,writeSession:()=>assert.fail('bad credential saved'),clearSession(){},onStart(){},onSession:()=>assert.fail('bad credential connected'),onError:e=>errors.push(e.message)});await invalid.start({ticket});assert.doesNotMatch(errors.at(-1),new RegExp(ticket));
});

test('fresh authentication interrupts an in-flight send, preserves its exact pending key, and never retries it',async()=>{
 let stored=null;const persistence={getItem:()=>stored,setItem:(_,value)=>stored=value},wire=deferred(),calls=[];
 const ops=createMessageOperations({storage:persistence,call:(operation,params)=>{calls.push([operation,params]);return wire.promise;}});
 const request=prepareMessage({audience:'builder',body:'Pending operator request'},[{id:'builder',adapter:'codex',session:'specific',cwd:'/work',delivery:'poll',registration:'fixed'}],'retained-key');
 const submitted=ops.submit(request);let sessionStored=credential('d'.repeat(64));
 const auth=createDashboardAuthentication({now:()=>now,exchange:async()=>credential(token),readSession:()=>sessionStored,writeSession:s=>sessionStored=s,clearSession:()=>{sessionStored=null;},onStart:()=>wire.reject(Error('Backend disconnected')),onSession(){},onError:()=>assert.fail('fresh authentication failed')});
 await auth.start({ticket});await submitted;assert.equal(ops.operations[0].state,'unknown');assert.equal(calls.length,1);assert.equal(sessionStored.token,token);
 const restored=createMessageOperations({storage:persistence,call:()=>assert.fail('authentication replayed pending send')});assert.deepEqual(restored.operations[0].request,request);
});
