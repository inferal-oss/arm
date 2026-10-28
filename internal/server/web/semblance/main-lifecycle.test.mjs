import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {namedLabel} from '../resource-names.mjs';

// Execute the production lifecycle functions with IO/component boundaries
// doubled. These tests cover connection/route ownership, not RDF rendering.
const source=readFileSync(new URL('./main.mjs',import.meta.url),'utf8');
function between(start,end){const a=source.indexOf(start),b=source.indexOf(end,a);assert.ok(a>=0&&b>a,start);return source.slice(a,b);}
const declarations=[
  between('function disposeDetail(){','\nnavigation=createDashboardHistory'),
  between('function applyRoute(route){','\nfunction showLease'),
  between('async function refresh(){','\nasync function connect(){'),
  between('async function connect(){','\nconst authentication='),
  between('async function loadLease(id){','\nfunction renderLeaseDetail'),
  between('function renderLeaseDetail(lease,leases,request,resultNote=', '\nconst activityTimeline='),
].join('\n');
const reset=between('onStart:()=>{',',onSession:value=>').slice('onStart:'.length);
const onDetailClose=between("detail.on('close',()=>{",');\nconst when=').slice("detail.on('close',".length);
const id='a'.repeat(32),tick=()=>new Promise(resolve=>setImmediate(resolve));

function fixture({open=true,view='leases',ready=()=>Promise.resolve(),failStatus=false,displayName}={}){
  const log=[],calls=[],instances=[],actions=[],reviews=[];
  const oldPanel={disposed:false,dispose(){this.disposed=true;}},oldReviews={disposed:false,dispose(){this.disposed=true;}};
  const lease=Object.freeze({id,display_name:'Build cache',host:'c'.repeat(32),revision:9007199254740993n,state:'active',owner:'operator',request:{reason:'Build'},expires_at:new Date(Date.now()+60000).toISOString()});
  let api;
  const scope={
    ARM:'https://arm.local/ui#',C:'https://semblance.dev/ns/component#',REVIEW_CAPABILITY:'reviews',
    Date,performance,WebSocket:{OPEN:1},setTimeout:()=>1,clearTimeout:()=>{},
    session:{token:'b'.repeat(64),expires_at:lease.expires_at},connection:{disconnect(){log.push('disconnect:old');}},
    state:{connected:false,status:null,epoch:0,cursor:0},targetWindow:null,view,retry:0,
    retryTimer:null,refreshTimer:null,refreshBusy:false,refreshAgain:false,nodeContextInvalid:false,remoteDashboard:null,remoteSlot:null,remoteView:null,screenViewer:null,peerPicture:null,
    poolConsole:null,meshConsole:null,detailRequest:0,detailLeaseID:open?id:'',detailPanel:oldPanel,detailReviews:oldReviews,resettingSession:false,
    copyLoginGeneration:0,copyingLogin:false,paletteNodes:[],palettePools:[],paletteKubernetes:null,
    pendingLeaseActions:new Set(),pendingReviewConfigurations:new Set(),
    roots:{screen:{replace(){}}},detailBody:{},
    clearLogin(){},peerPoll:{reset(){}},notice:text=>log.push('notice:'+text),
    connectionState(label,live=false){scope.state.connected=live;log.push('connection:'+label);},
    nodeContextClosed:()=>false,addEvent(){},acceptMonitor(){},updateFreshness(){},updateActivityFeed(){},updateMessages(){},scheduleRefresh(){},refreshPalette(){},
    renderCurrent(){log.push('render:view');},owner:()=> 'operator',when:value=>value,leaseStateLabel:value=>value.state,leaseActionResult:()=>'',
    owned(root,children){root.children=children;},
    ui:{batch:fn=>fn(),put(){},heading:(label,level)=>({kind:'Heading',label,level}),text:label=>({label}),badge:label=>({label}),json:()=>({}),stack:()=>({}),
      bind:()=>({set(){},focus(){log.push('focus:heading');}})},
    hostLabel:{set(props){Object.assign(this,props);}},namedLabel,
    localOperator:()=>true,namingNotice:{set(){}},namingConsole:{updateConnection(){}},
    detail:{open,set(props){Object.assign(this,props);},showModal(){this.open=true;log.push('open:dialog');this.focus();},focus(){log.push('focus:dialog');},close(){this.open=false;api.onDetailClose();}},
    navigation:{current:{view:'leases',lease:id},navigate(next){this.current=next;log.push('navigate');api.applyRoute(next);}},
    createLeaseActions(options){actions.push(options);return {dispose(){},updateConnection(){}};},
    createLeaseReviews(options){reviews.push(options);return {dispose(){},updateConnection(){}};},
    Backend:class {
      constructor(token,options){this.options=options;this.index=instances.length;instances.push(this);this.ready=ready(this.index);this.socket={readyState:1,close:()=>{this.socket.readyState=3;log.push('socket:close');}};}
      async call(operation,args){calls.push({connection:this.index,operation,args});
        if(operation==='status'){if(failStatus)throw Error('Read unavailable');return {host:'c'.repeat(32),display_name:displayName,cursor:0,capabilities:['reviews'],leases:[lease]};}
        if(operation==='inspect')return lease;
        if(operation==='monitor_subscribe')return {epoch:0,cursor:0};
        if(['events','messages','agents'].includes(operation))return [];
        if(['subscribe','mesh_operator_watch'].includes(operation))return {};
        throw Error('Unexpected operation '+operation);
      }
    },
  };
  api=runInNewContext(declarations+'\n({connect,refresh,applyRoute,disposeDetail,loadLease,reset:'+reset+',onDetailClose:'+onDetailClose+'})',scope);
  return {scope,api,log,calls,instances,actions,reviews,oldPanel,oldReviews,lease};
}
function onlyReads(f){const allowed=new Set(['status','inspect','monitor_subscribe','events','messages','agents','subscribe','mesh_operator_watch']);assert.ok(f.calls.every(c=>allowed.has(c.operation)));}

test('a new connection reloads an open lease and replaces callbacks bound to the old connection',async()=>{
  const f=fixture();await f.api.connect();await tick();
  assert.equal(f.oldPanel.disposed,true);assert.equal(f.oldReviews.disposed,true);assert.equal(f.scope.detail.open,true);
  assert.deepEqual(f.calls.filter(c=>c.operation==='inspect').map(c=>c.args.id),[id]);
  assert.equal(f.actions.length,1);assert.equal(f.actions[0].connected(),true);assert.equal(f.reviews[0].connected(),true);
  const heading=f.scope.detailBody.children.find(child=>child.kind==='Heading');
  assert.equal(heading.label,'Build cache');assert.notEqual(heading.label,id);
  assert.ok(f.scope.detailBody.children.some(child=>child.label==='ID '+id));
  assert.equal(f.actions[0].lease,f.lease);assert.equal(f.reviews[0].lease,f.lease);
  assert.equal(f.actions[0].lease.id,id);assert.equal(f.actions[0].lease.host,'c'.repeat(32));
  assert.equal(f.actions[0].lease.revision,9007199254740993n);assert.equal(f.scope.detailLeaseID,id);
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
test('a delayed or rejected next lease read clears the previous display name immediately',async()=>{
 const f=fixture();await f.api.connect();await tick();
 const next='d'.repeat(32);let rejectRead;
 f.scope.connection.call=async(operation)=>{if(operation==='inspect')return new Promise((_,reject)=>{rejectRead=reject;});throw Error('Unexpected read');};
 f.scope.detail.set({label:'prior-readable-name'});
 const read=f.api.loadLease(next);
 assert.equal(f.scope.detail.label,'Lease detail');
 assert.equal(f.scope.detailLeaseID,next);
 rejectRead(Error('Second lease unavailable'));await read;
 assert.equal(f.scope.detail.label,'Lease detail');
 assert.equal(f.scope.detailBody.children[0].label,'Second lease unavailable');
 f.scope.detail.set({label:'prior-readable-name'});f.scope.state.connected=false;
 await f.api.loadLease(next);assert.equal(f.scope.detail.label,'Lease detail');
 assert.match(f.scope.detailBody.children[0].label,/Waiting for a connection/);
});


test('empty optional host labels use the exact-ID fallback at connect and refresh',async()=>{
 const f=fixture({displayName:''});await f.api.connect();await tick();assert.equal(f.scope.hostLabel.label,'Host cccccccc');
 f.scope.hostLabel.set({label:'prior-name'});await f.api.refresh();assert.equal(f.scope.hostLabel.label,'Host cccccccc');onlyReads(f);
});


test('backend close refreshes peer status without replacing reported claims or moving focus',async()=>{
 const f=fixture({open:false,view:'resources'});
 f.scope.navigation.current={view:'resources',lease:null};
 const slot=()=>({children:[],replacements:0,replace(...children){this.children=children;this.replacements++;}});
 Object.assign(f.scope,{
  connectionBadge:{set(){}},copyLogin:{},namesButton:{},activityButton:{},messages:{updateConnection(){}},updatePalette(){},
  detailPanel:null,detailReviews:null,
  peerStatus:{set(props){Object.assign(this,props);}},peerSummary:slot(),peerNotes:slot(),peerDetails:slot(),
  hostResourceSummary:value=>JSON.stringify(value),
 });
 Object.assign(f.scope.ui,{text:label=>({label}),card:(label,children)=>({label,children})});
 const production=runInNewContext([
  between('function connectionState(', '\nfunction disposeDetail'),
  between('function updatePeerStatus(){', '\nfunction renderLeases'),
  '\n({connectionState,renderPeers})',
 ].join('\n'),f.scope);
 f.scope.connectionState=production.connectionState;
 await f.api.connect();await tick();
 for(const complete of [false,true]){
  const picture={totals_complete:complete,local:{held:{cpu:1},held_count:1,queued:{cpu:2},queued_count:1,items:[{state:'held',resources:{cpu:1},expires_at:f.lease.expires_at}]},instances:[]};
  f.scope.peerPicture=picture;production.renderPeers();
  const liveLabel=complete?'Recent discovered peer reports':'Partial view · some peers unavailable';
  assert.equal(f.scope.peerStatus.label,liveLabel);
  const slots=[f.scope.peerSummary,f.scope.peerNotes,f.scope.peerDetails];
  const before=slots.map(s=>({children:s.children,replacements:s.replacements}));
  const calls=f.calls.length,focus=f.log.filter(x=>x.startsWith('focus:')).length;
  f.instances.at(-1).options.onClose({code:1006});
  assert.equal(f.scope.state.connected,false);
  assert.equal(f.scope.peerStatus.label,'Stale view · backend disconnected');
  assert.equal(f.scope.peerPicture,picture);
  for(let i=0;i<slots.length;i++){
   assert.equal(slots[i].children,before[i].children);
   assert.equal(slots[i].replacements,before[i].replacements);
  }
  assert.equal(f.calls.length,calls);
  assert.equal(f.log.filter(x=>x.startsWith('focus:')).length,focus);
  await f.api.connect();await tick();
  assert.equal(f.scope.state.connected,true);assert.equal(f.scope.peerStatus.label,liveLabel);
 }
 onlyReads(f);
});
