import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';

// Execute the production lifecycle functions with IO/component boundaries
// doubled. These tests cover connection/route ownership, not RDF rendering.
const source=readFileSync(new URL('./main.mjs',import.meta.url),'utf8');
function between(start,end){const a=source.indexOf(start),b=source.indexOf(end,a);assert.ok(a>=0&&b>a,start);return source.slice(a,b);}
const declarations=[
  between('function disposeDetail(){','\nnavigation=createDashboardHistory'),
  between('function applyRoute(route){','\nfunction showLease'),
  between('async function connect(){','\nconst authentication='),
  between('async function loadLease(id){','\nfunction renderLeaseDetail'),
  between('function renderLeaseDetail(lease,leases,request,resultNote=', '\nconst activityTimeline='),
].join('\n');
const reset=between('onStart:()=>{',',onSession:value=>').slice('onStart:'.length);
const onDetailClose=between("detail.on('close',()=>{",');\nconst when=').slice("detail.on('close',".length);
const id='a'.repeat(32),tick=()=>new Promise(resolve=>setImmediate(resolve));

function fixture({open=true,view='leases',ready=()=>Promise.resolve(),failStatus=false}={}){
  const log=[],calls=[],instances=[],actions=[],reviews=[];
  const oldPanel={disposed:false,dispose(){this.disposed=true;}},oldReviews={disposed:false,dispose(){this.disposed=true;}};
  const lease={id,state:'active',owner:'operator',request:{reason:'Build'},expires_at:new Date(Date.now()+60000).toISOString()};
  let api;
  const scope={
    ARM:'https://arm.local/ui#',C:'https://semblance.dev/ns/component#',REVIEW_CAPABILITY:'reviews',
    Date,performance,WebSocket:{OPEN:1},setTimeout:()=>1,clearTimeout:()=>{},
    session:{token:'b'.repeat(64),expires_at:lease.expires_at},connection:{disconnect(){log.push('disconnect:old');}},
    state:{connected:false,status:null,epoch:0,cursor:0},targetWindow:null,view,retry:0,
    retryTimer:null,refreshTimer:null,nodeContextInvalid:false,remoteDashboard:null,remoteSlot:null,remoteView:null,screenViewer:null,peerPicture:null,
    poolConsole:null,meshConsole:null,detailRequest:0,detailLeaseID:open?id:'',detailPanel:oldPanel,detailReviews:oldReviews,resettingSession:false,
    copyLoginGeneration:0,copyingLogin:false,paletteNodes:[],palettePools:[],paletteKubernetes:null,
    pendingLeaseActions:new Set(),pendingReviewConfigurations:new Set(),
    roots:{screen:{replace(){}}},detailBody:{},
    clearLogin(){},peerPoll:{reset(){}},notice:text=>log.push('notice:'+text),
    connectionState(label,live=false){scope.state.connected=live;log.push('connection:'+label);},
    nodeContextClosed:()=>false,addEvent(){},acceptMonitor(){},updateFreshness(){},updateMessages(){},scheduleRefresh(){},refreshPalette(){},
    renderCurrent(){log.push('render:view');},owner:()=> 'operator',when:value=>value,leaseStateLabel:value=>value.state,leaseActionResult:()=>'',
    owned(root,children){root.children=children;},
    ui:{batch:fn=>fn(),put(){},text:label=>({label}),badge:label=>({label}),json:()=>({}),stack:()=>({}),
      bind:()=>({set(){},focus(){log.push('focus:heading');}})},
    hostLabel:{set(){}},
    detail:{open,showModal(){this.open=true;log.push('open:dialog');this.focus();},focus(){log.push('focus:dialog');},close(){this.open=false;api.onDetailClose();}},
    navigation:{current:{view:'leases',lease:id},navigate(next){this.current=next;log.push('navigate');api.applyRoute(next);}},
    createLeaseActions(options){actions.push(options);return {dispose(){},updateConnection(){}};},
    createLeaseReviews(options){reviews.push(options);return {dispose(){},updateConnection(){}};},
    Backend:class {
      constructor(token,options){this.options=options;this.index=instances.length;instances.push(this);this.ready=ready(this.index);this.socket={readyState:1,close:()=>{this.socket.readyState=3;log.push('socket:close');}};}
      async call(operation,args){calls.push({connection:this.index,operation,args});
        if(operation==='status'){if(failStatus)throw Error('Read unavailable');return {host:'c'.repeat(32),cursor:0,capabilities:['reviews'],leases:[lease]};}
        if(operation==='inspect')return lease;
        if(operation==='monitor_subscribe')return {epoch:0,cursor:0};
        if(['events','messages','agents'].includes(operation))return [];
        if(['subscribe','mesh_operator_watch'].includes(operation))return {};
        throw Error('Unexpected operation '+operation);
      }
    },
  };
  api=runInNewContext(declarations+'\n({connect,applyRoute,disposeDetail,loadLease,reset:'+reset+',onDetailClose:'+onDetailClose+'})',scope);
  return {scope,api,log,calls,instances,actions,reviews,oldPanel,oldReviews};
}
function onlyReads(f){const allowed=new Set(['status','inspect','monitor_subscribe','events','messages','agents','subscribe','mesh_operator_watch']);assert.ok(f.calls.every(c=>allowed.has(c.operation)));}

test('a new connection reloads an open lease and replaces callbacks bound to the old connection',async()=>{
  const f=fixture();await f.api.connect();await tick();
  assert.equal(f.oldPanel.disposed,true);assert.equal(f.oldReviews.disposed,true);assert.equal(f.scope.detail.open,true);
  assert.deepEqual(f.calls.filter(c=>c.operation==='inspect').map(c=>c.args.id),[id]);
  assert.equal(f.actions.length,1);assert.equal(f.actions[0].connected(),true);assert.equal(f.reviews[0].connected(),true);
  f.scope.connection={};assert.equal(f.actions[0].connected(),false);assert.equal(f.reviews[0].connected(),false);onlyReads(f);
});
test('authentication reset preserves a direct lease route and reconnect reopens it',async()=>{
  const f=fixture();f.api.reset();assert.equal(f.scope.navigation.current.lease,id);assert.equal(f.scope.detail.open,false);assert.equal(f.scope.connection,null);assert.equal(f.scope.resettingSession,false);assert.equal(f.log.includes('navigate'),false);
  f.scope.session={token:'b'.repeat(64),expires_at:new Date(Date.now()+60000).toISOString()};await f.api.connect();await tick();
  assert.equal(f.scope.detail.open,true);assert.equal(f.actions.length,1);assert.equal(f.log.at(-1),'focus:dialog');onlyReads(f);
});
test('an explicit detail close clears the lease route',async()=>{
  const f=fixture();f.scope.detail.close();assert.equal(f.scope.navigation.current.lease,null);assert.ok(f.log.includes('navigate'));onlyReads(f);
});
test('view-heading focus precedes opening a directly linked lease dialog',async()=>{
  const f=fixture({open:false,view:'monitor'});await f.api.connect();await tick();
  const heading=f.log.indexOf('focus:heading'),dialog=f.log.indexOf('open:dialog');assert.ok(heading>=0&&dialog>heading);assert.equal(f.log.at(-1),'focus:dialog');onlyReads(f);
});
test('a superseded connection cannot rebind the current lease detail',async()=>{
  let release;const f=fixture({ready:index=>index===0?new Promise(resolve=>{release=resolve;}):Promise.resolve()});
  const first=f.api.connect();await f.api.connect();await tick();release();await first;await tick();
  assert.equal(f.instances.length,2);assert.equal(f.actions.length,1);assert.deepEqual(f.calls.filter(c=>c.operation==='inspect').map(c=>c.connection),[1]);onlyReads(f);
});
test('a failed connection read retains the existing detail and route without a mutation',async()=>{
  const f=fixture({failStatus:true});await f.api.connect();await tick();
  assert.equal(f.scope.navigation.current.lease,id);assert.equal(f.scope.detail.open,true);assert.equal(f.oldPanel.disposed,false);assert.equal(f.actions.length,0);assert.ok(f.log.includes('socket:close'));onlyReads(f);
});
