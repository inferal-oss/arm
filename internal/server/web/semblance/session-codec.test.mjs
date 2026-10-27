import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {parseJSON,stringifyJSON} from '../core.mjs';
import {createDashboardAuthentication} from '../connection-links.mjs';
const source=readFileSync(new URL('./main.mjs',import.meta.url),'utf8');
const host='1'.repeat(32),origin='2'.repeat(32),nonce='3'.repeat(32),ticket='4'.repeat(64),token='5'.repeat(64);
const local=9007199254740993n,remote=9223372036854775807n;
const expiration=new Date(Date.now()+60000).toISOString();
const raw=(extra={})=>stringifyJSON({token,expires_at:expiration,context:{host,source_host:origin,local:false,pair:'6'.repeat(64),certificate_fingerprint:'7'.repeat(64),local_revision:local,remote_revision:remote},...extra});
const between=(start,end)=>{const a=source.indexOf(start),b=source.indexOf(end,a);assert.ok(a>=0&&b>a);return source.slice(a,b);};
const decode=source.includes('function decodeSessionJSON(')?between('function decodeSessionJSON(','\nlet ui'):'';
const authBlock=between('const authentication=','\nconst connectionLinks=');
const nodeBlock=source.slice(source.indexOf('if(targetWindow?.error)'));
function fixture({response=raw(),ok=true,target={host,nonce}}={}){
 const saved=new Map(),calls=[],errors=[],connected=[];let bodyReads=0;
 const storage={getItem:k=>saved.get(k)??null,setItem:(k,v)=>saved.set(k,v),removeItem:k=>saved.delete(k)};
 const globals={parseJSON,stringifyJSON,storage,sessionKey:'session',targetWindow:target,session:null,nodeContextInvalid:false,connectionState(){},notice:m=>errors.push(m),connect:()=>{connected.push(true);return Promise.resolve();},
  fetch:async(url,options)=>{calls.push({url,options});return{ok,text:async()=>{bodyReads++;return response;},json:async()=>{bodyReads++;return JSON.parse(response);}};},createDashboardAuthentication:options=>options};
 const options=runInNewContext(decode+'\n'+authBlock+'\nauthentication;',globals);
 return{saved,storage,calls,errors,connected,options,get bodyReads(){return bodyReads;},
  async handoff(){let exchange;const auth=createDashboardAuthentication({...options,onStart(){},onError:e=>errors.push(e.message)});
   await runInNewContext(decode+'\n'+nodeBlock,{...globals,navigation:{sanitize(){}},authentication:auth,receiveNodeWindow:async args=>{exchange=args.exchange;return args.exchange(ticket,host,nonce);},connectionLinks:{start(){throw Error('Unexpected local route');}}});
   return exchange;
  }};
}

test('actual operator exchange and stored node context retain 2^53+1 and maximum Go int revisions exactly',async()=>{
 const f=fixture(),controller=new AbortController(),value=await f.options.exchange(ticket,controller.signal);
 assert.equal(value.context.local_revision,local);assert.equal(value.context.remote_revision,remote);
 const {url,options}=f.calls[0];assert.equal(url,'/v1/browser/session');assert.equal(options.signal,controller.signal);assert.equal(options.method,'POST');assert.equal(options.credentials,'omit');assert.equal(options.cache,'no-store');assert.equal(options.redirect,'error');assert.deepEqual(JSON.parse(options.body),{ticket});
 f.options.writeSession(value);assert.match(f.saved.get('session'),/"local_revision":9007199254740993/);assert.equal(f.options.readSession().context.remote_revision,remote);
 await f.options.onSession(value);assert.equal(f.connected.length,1);
});

test('actual node HTTP handoff and subsequent authentication preserve exact stored context',async()=>{
 const f=fixture();await f.handoff();assert.equal(f.errors.length,0);assert.equal(f.connected.length,1);
 const stored=f.options.readSession();assert.equal(stored.context.local_revision,local);assert.equal(stored.context.remote_revision,remote);
 const {options}=f.calls[0];assert.deepEqual(JSON.parse(options.body),{ticket,host,window_nonce:nonce});assert.equal(options.credentials,'omit');assert.equal(options.redirect,'error');assert.equal(options.cache,'no-store');
});

test('both actual HTTP paths refuse failed exchanges before reading or storing their bodies',async()=>{
 const f=fixture({ok:false,response:raw()});await assert.rejects(f.options.exchange(ticket,new AbortController().signal),/expired or already used/);assert.equal(f.bodyReads,0);assert.equal(f.saved.size,0);
 const node=fixture({ok:false});await node.handoff();assert.equal(node.bodyReads,0);assert.equal(node.saved.size,0);assert.equal(node.connected.length,0);assert.ok(node.errors.some(e=>/expired, changed, or already used/.test(e)));
});

test('malformed and invalid base sessions never reflect response credentials through errors',async()=>{
 for(const response of ['{"token":"'+token+'","context":broken}',raw({token:5555555555555555555555555555555555555555555555555555555555555555n}),raw({expires_at:1})]){
  const f=fixture({response});await assert.rejects(f.options.exchange(ticket,new AbortController().signal),e=>/Invalid dashboard session response/.test(e.message)&&!e.message.includes(token));assert.equal(f.saved.size,0);
  const node=fixture({response});await node.handoff();assert.equal(node.connected.length,0);assert.equal(node.saved.size,0);assert.ok(node.errors.every(e=>!e.includes(token)));
 }
});

test('stored invalid, expired and wrong-host contexts cannot connect or lift node scope',async()=>{
 const f=fixture();for(const value of ['broken',raw({context:{host:'8'.repeat(32)}})]){f.saved.set('session',value);assert.equal(f.options.readSession(),null);}
 assert.throws(()=>f.options.onSession(parseJSON(raw({context:{host:'8'.repeat(32)}}))),/exact node/);assert.equal(f.connected.length,0);
 const expired=fixture({response:raw({expires_at:'2000-01-01T00:00:00Z'})});await expired.handoff();assert.equal(expired.connected.length,0);assert.ok(expired.errors.some(e=>/authenticated local console/.test(e)));
});
