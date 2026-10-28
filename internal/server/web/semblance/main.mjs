import {mount} from '@inferal-oss/semblance/web';
import {createGraphUI,ARM} from './graph-ui.mjs';
import graph from './app.ttl?raw';
import {Backend} from './backend.mjs';
import {createNamingConsole} from './resource-names.mjs';
import {loadedNameTargets,namedLabel} from '../resource-names.mjs';
import {createPoolConsole} from './pools.mjs';
import {createResourceOverview} from './resources.mjs';
import {createMeshConsole} from './mesh.mjs';
import {createOperatorMessaging} from './messages.mjs';
import {createLeaseActions,createLeaseReviews,REVIEW_CAPABILITY} from './lease-controls.mjs';
import {createRemoteNodeDashboard} from './remote.mjs';
import {createScreenViewer} from './screen.mjs';
import {createConnectionLinkHandler,createDashboardAuthentication} from '../connection-links.mjs';
import {nodeWindowRoute,openNodeWindow,receiveNodeWindow,nodeContextClosed} from '../node-window.mjs';
import {createDashboardHistory} from '../route.mjs';
import {installDashboardShortcuts} from '../dashboard-shortcuts.mjs';
import {createPeerPollGate} from '../peer-poll.mjs';
import {parseJSON,stringifyJSON,numeric,count,bytes,quantity,percent,mergeHistory,ownerDisplay,hostResourceSummary} from '../core.mjs';
import {leaseStateLabel,leaseVisible,readHideInactive,saveHideInactive} from '../lease-state.mjs';
import {leaseActionResult} from '../lease-actions.mjs';
import {metricHelp,capacityBudgetsForView,capacityFreshnessKey,cpuBudgetKnown,memoryBudgetKnown,diskBudgetKnown,cpuCores} from '../metric-help.mjs';
import {monitorStale,remainingValue} from '../monitor-capacity.mjs';
import {allDevices,noDevices,toggleDevice,selectedDeviceIDs,aggregateIO,aggregateIOWindow} from '../io-devices.mjs';
import {activityItems} from '../activity-feed.mjs';
import {createEphemeralInputs} from './ephemeral-inputs.mjs';
import {createCommandPalette} from './command-palette.mjs';
import {markKeyboardPlatform} from './platform.mjs';

markKeyboardPlatform(document,navigator.platform);

function decodeSessionJSON(raw){
 try{const value=parseJSON(raw);if(!value||Array.isArray(value)||typeof value.token!=='string'||typeof value.expires_at!=='string')throw Error();return value;}
 catch{throw Error('Invalid dashboard session response. Open a fresh connection link.');}
}
let ui,navigation;const widgets={};
const app=await mount(document.getElementById('app'),{root:ARM+'shell',graphs:[graph],store:'wat',files:false,widgets,onHandled:h=>{
 ui?.handled(h);
 if(navigation){const selected=app.store.match(app.store.factory.namedNode(ARM+'shell'),app.store.factory.namedNode(ARM+'view'))[0]?.object.value;const name=({'activityView':'activity','hostView':'host'})[selected?.slice(ARM.length)]??selected?.slice(ARM.length);if(name&&name!==navigation.current.view)navigation.navigate({view:name,lease:null});}
 if(h.target.value===ARM+'theme')try{localStorage.setItem('arm-semblance-theme',app.store.match(app.store.factory.namedNode(ARM+'shell'),app.store.factory.namedNode('https://semblance.dev/ns/style#theme'))[0]?.object.value);}catch{}
 if(h.target.value===ARM+'scheme')try{localStorage.setItem('arm-semblance-color-scheme',app.store.match(app.store.factory.namedNode(ARM+'shell'),app.store.factory.namedNode('https://semblance.dev/ns/style#colorScheme'))[0]?.object.value);}catch{}
}});
ui=createGraphUI(app,{widgets,onError:error=>notice(error.message)});
const f=app.store.factory,C='https://semblance.dev/ns/component#';
const state={status:null,statusReceived:0,messages:[],contacts:[],events:[],history:[],epoch:'',cursor:0,monitor:null,received:0,connected:false};
const targetWindow=nodeWindowRoute(location.href),sessionKey=targetWindow?.host?'arm-node-session:'+targetWindow.host:'arm-session';
let session=null,connection=null,view='monitor',retry=0,retryTimer,refreshTimer,refreshBusy=false,refreshAgain=false,nodeContextInvalid=false;
let poolConsole=null,meshConsole=null,remoteDashboard=null,remoteSlot=null,remoteView=null,screenViewer=null,peerPicture=null,detailRequest=0,detailLeaseID='',detailPanel=null,detailReviews=null,resettingSession=false;
const pendingLeaseActions=new Set(),pendingReviewConfigurations=new Set(),peerPoll=createPeerPollGate();
let storage,preferences;try{storage=sessionStorage;}catch{}try{preferences=localStorage;}catch{}
let hideInactive=readHideInactive(preferences),capacityFreshness=null;
try{const remembered=preferences?.getItem('arm-semblance-theme');if(remembered&&app.store.match(f.namedNode(remembered),f.namedNode('http://www.w3.org/1999/02/22-rdf-syntax-ns#type'),f.namedNode('https://semblance.dev/ns/style#Theme')).length)ui.put(ARM+'shell','https://semblance.dev/ns/style#theme',remembered,true);}catch{}
try{const scheme=preferences?.getItem('arm-semblance-color-scheme');if(['Light','Dark','System'].some(x=>scheme==='https://semblance.dev/ns/style#'+x))ui.put(ARM+'shell','https://semblance.dev/ns/style#colorScheme',scheme,true);}catch{}
const roots=Object.fromEntries(['monitor','resources','leases','messages','activity','host','mesh','screen'].map(name=>[name,ui.bind(name+'Content')]));
const connectionBadge=ui.bind('connection','Badge'),hostLabel=ui.bind('host','Text'),noticeBox=ui.bind('notice','Alert'),freshness=ui.text('Waiting for first sample');
ui.bind('statusline').append(freshness);
let copyLoginGeneration=0,copyingLogin=false,loginDialog=null,loginInputs=null,paletteNodes=[],palettePools=[],paletteKubernetes=null,paletteRefreshBusy=false;
const localOperator=()=>!targetWindow?.host&&!session?.context;
const copyLogin=ui.button('Copy login link',async()=>{
 if(copyingLogin||!state.connected||!localOperator())return;copyingLogin=true;copyLogin.disabled=true;const current=connection,generation=++copyLoginGeneration;
 try{const result=await current.call('dashboard_login_link');if(connection!==current||!state.connected)return;
  const link=new URL(result.url);if(link.origin!==location.origin||link.pathname!=='/'||!/^#connect=[a-f0-9]{64}$/.test(link.hash)||!Number.isFinite(Date.parse(result.expires_at))||Date.parse(result.expires_at)<=Date.now())throw Error('The backend returned an invalid login link.');
  try{await navigator.clipboard.writeText(link.href);if(connection!==current||!state.connected)return;copyLogin.set({label:'Login link copied'});setTimeout(()=>{if(generation===copyLoginGeneration)copyLogin.set({label:'Copy login link'});},2500);}
  catch{if(connection!==current||!state.connected)return;clearLogin();const input=createEphemeralInputs([{name:'link',label:'One-use login link',readOnly:true,initial:link.href}]),island=ui.island('login-link-'+Date.now(),input.widget),d=ui.dialog('Copy login link',[ui.text('Use once within two minutes. This opens the same local operator dashboard.'),island]);loginDialog=d;loginInputs=input;d.on('close',clearLogin);d.showModal(copyLogin);input.select('link');}
 }catch(error){notice(error.message);}finally{if(generation===copyLoginGeneration){copyingLogin=false;copyLogin.disabled=!state.connected||!localOperator();}}
},{emphasis:'Quiet'});
function clearLogin(){loginInputs?.clear();loginInputs=null;const d=loginDialog;loginDialog=null;d?.dispose();}
const liveTimeline=ui.timeline('Live operational activity',12),liveStatus=ui.badge('Connecting'),holdActivity=ui.check('Pause updates while reading',{checked:false},()=>updateActivityFeed());
const liveActivity=ui.bind('liveActivityContent');liveActivity.replace(liveStatus,holdActivity,liveTimeline);
const activityDrawer=ui.drawer('Live activity',[liveActivity]);
activityDrawer.replace(ui.toolbar('Activity navigation',[ui.button('All activity',()=>{activityDrawer.close();navigation.navigate({view:'activity',lease:null});}),ui.button('Messages',()=>{activityDrawer.close();navigation.navigate({view:'messages',lease:null});})]),liveActivity);
const activityButton=ui.button('Live activity',()=>{updateActivityFeed();activityDrawer.showModal();},{emphasis:'Quiet'});
const palette=createCommandPalette({ui,navigate:route=>navigation.navigate(route),onLease:(id,record)=>{if(record.host&&record.host!==state.status?.host){notice('Open the matching node to inspect this lease.');return;}showLease(id);},onNode:entry=>openNodeWindow({entry,call:(...args)=>connection.call(...args),onError:notice}),onMessage:id=>messages.focusMessage(id),onBrowseKubernetes:()=>{navigation.navigate({view:'mesh',lease:null});meshConsole?.openKubernetesInventory?.();},onKubernetesNode:record=>{navigation.navigate({view:'mesh',lease:null});if(!meshConsole?.focusKubernetesNode?.(record))notice('This node inventory changed. Browse its exact context again.');},onResource:(id,record)=>{resourceFilter.value=id;navigation.navigate({view:'resources',lease:null});renderResourceCards();},onPool:id=>navigation.navigate({view:'resources',pool:id,poolAfter:'0',lease:null})});
const paletteButton=ui.button('Search…',()=>{updatePalette();palette.open();refreshPalette();},{emphasis:'Quiet'}),paletteStatus=ui.text('',{hidden:true,role:'status'});ui.bind('statusline').append(paletteStatus);
palette.element.on('open',refreshPalette);ui.put(paletteButton.iri,'https://semblance.dev/ns/keyboard#hint','mod+k');
const namingNotice=ui.text('',{hidden:true,role:'status'});ui.bind('statusline').append(namingNotice);
const namingConsole=createNamingConsole({ui,call:(...args)=>connection.call(...args),connected:()=>state.connected,local:localOperator,source:()=>state.status?.host??'',owner:()=>state.status?.owner??'',context:()=>connection,targets:()=>loadedNameTargets({status:state.status,nodes:paletteNodes,pools:palettePools}),onChanged:()=>{scheduleRefresh();refreshPalette();if(detail.open&&detailLeaseID)loadLease(detailLeaseID);},storage});
const namesButton=ui.button('Resource names',()=>namingConsole.open(),{emphasis:'Quiet'});
const displayDialog=ui.dialog('Display settings',[ui.bind('theme'),ui.bind('scheme')]),displayButton=ui.iconButton('Display settings','⚙',()=>displayDialog.showModal(),{emphasis:'Ghost'});
ui.list(ARM+'header',C+'moreActions',[paletteButton.iri,namesButton.iri,copyLogin.iri,activityButton.iri,displayButton.iri]);
function updatePalette(){const local=state.connected&&localOperator();paletteButton.disabled=!local;palette.update({context:local?'local':'remote',status:state.status,messages:state.messages,nodes:paletteNodes,pools:palettePools,kubernetes:paletteKubernetes});const omitted=Object.values(palette.omitted).reduce((n,x)=>n+x,0);paletteStatus.set({hidden:!omitted,label:omitted?`Search shows up to 256 items per category · ${omitted} more items remain in their views`:''});}
async function refreshPalette(){if(paletteRefreshBusy||!state.connected||!localOperator())return;paletteRefreshBusy=true;const current=connection;try{const results=await Promise.allSettled([current.call('node_list'),current.call('pool_list',{all:true})]);if(connection!==current||!state.connected||!localOperator())return;if(results[0].status==='fulfilled')paletteNodes=results[0].value?.nodes??[];if(results[1].status==='fulfilled')palettePools=results[1].value??[];updatePalette();}finally{paletteRefreshBusy=false;}}
const resourceFilter=ui.field('Filter resources',{type:'search'},()=>renderResourceCards()),resourceCards=ui.grid([],{columns:4,minWidth:'280px'}),peerCards=ui.stack(),poolRoot=ui.stack();
roots.resources.replace(resourceFilter,resourceCards,poolRoot,peerCards);
const peerStatus=ui.text('Waiting for peer reports'),peerSummary=ui.stack(),peerNotes=ui.stack(),peerDetails=ui.disclosure('Reported claims');
peerCards.replace(ui.card('Nearby ARM instances',[peerStatus,peerSummary,peerNotes,peerDetails]));
const resourceOverview=createResourceOverview({ui,root:resourceCards,owner:record=>owner(record),onLease:id=>showLease(id),help:(...args)=>help(...args)});
const leaseFilter=ui.field('Filter leases, owners or reasons',{type:'search'},()=>renderLeases()),leaseCount=ui.text(''),leaseCards=ui.grid();
const inactiveToggle=ui.check('Hide inactive leases',{checked:hideInactive},()=>{hideInactive=inactiveToggle.checked;saveHideInactive(preferences,hideInactive);renderLeases();});roots.leases.replace(leaseFilter,inactiveToggle,leaseCount,leaseCards);
const messages=createOperatorMessaging({ui,root:roots.messages,call:(...args)=>connection.call(...args),connected:()=>state.connected&&!remoteDashboard,onLease:id=>showLease(id),storage});
const detailBody=ui.stack(),detailClose=ui.button('Close lease detail',()=>detail.close()),detail=ui.dialog('Lease detail',[detailBody],[detailClose]);
detail.on('close',()=>{if(detail.open||resettingSession)return;if(navigation.current.lease){navigation.navigate({...navigation.current,lease:null});return;}disposeDetail();});
const when=value=>value?new Date(value).toLocaleString():'—';
const owner=record=>ownerDisplay(record,state.status,record?.host);
const rate=value=>value==null?'unavailable':bytes(value)+'/s';
function notice(text=''){noticeBox.set({label:text,hidden:!text});}
function owned(root,children){const old=[...root.children];root.replace(...children);for(const h of old)if(!root.children.includes(h))h.dispose();}
function connectionState(text,live=false){ui.batch(()=>{connectionBadge.set({label:text,tone:live?'success':'warning'});state.connected=live;updatePeerStatus();ui.bind('sessionScope','Text').set({label:session?(localOperator()?'Local operator session':'Node session'):(targetWindow?.host?'Node dashboard':'Local dashboard')});copyLogin.disabled=!live||!localOperator();namesButton.disabled=!live||!localOperator();namesButton.hidden=!localOperator();namingConsole.updateConnection();activityButton.hidden=!localOperator();updatePalette();detailPanel?.updateConnection();detailReviews?.updateConnection();poolConsole?.updateConnection();meshConsole?.updateConnection();messages.updateConnection?.();if(!live){clearLogin();remoteDashboard?.disconnect();screenViewer?.disconnect();}updateActivityFeed();});}
function disposeDetail(){detailRequest++;detailLeaseID='';detailPanel?.dispose();detailReviews?.dispose();detailPanel=detailReviews=null;}
navigation=createDashboardHistory({location,history,onRoute:applyRoute});
function applyRoute(route){
 const name=remoteDashboard&&!['resources','leases','host','screen'].includes(route.view)?'resources':route.view;
 const changingView=view!==name;view=name;ui.put(ARM+'shell',ARM+'view',ARM+({activity:'activityView',host:'hostView'}[name]??name),true);
 ui.bind('heading','Heading').set({label:({monitor:'Host monitor',resources:'Resource budgets',leases:'Leases',messages:'Operator messages',activity:'Activity',host:'Host & capabilities',mesh:'Mesh',screen:'Screen Sharing'})[name]});
 if(name!=='screen')screenViewer?.disconnect();else if(!screenViewer)screenViewer=createScreenViewer({ui,root:roots.screen,token:session?.token,context:session?.context??{local:true}});
 if(remoteDashboard){if(name!=='screen'){if(remoteView&&remoteView!==name)roots[remoteView].replace();roots[name].replace(remoteSlot);remoteView=name;remoteDashboard.show(name);}if(changingView)ui.bind('heading').focus();return;}
 renderCurrent();poolConsole?.applyRoute(route);
 if(changingView)ui.bind('heading').focus();
 if(route.lease){if(detailLeaseID!==route.lease||!detail.open)loadLease(route.lease);}else if(detail.open){detail.close();}else disposeDetail();
}
function showLease(id){navigation.navigate({...navigation.current,view,lease:id});}
installDashboardShortcuts({document,selectView:name=>navigation.navigate({view:name,lease:null})});
function renderCurrent(){ui.batch(()=>{({monitor:renderMonitor,resources:renderResources,leases:renderLeases,messages:updateMessages,activity:renderActivity,host:renderHost,mesh:renderMesh,screen:()=>{}})[view]?.();});}
function updateMessages(){messages.update({contacts:state.contacts,messages:state.messages,leases:state.status?.leases??[]});updatePalette();}
function addEvent(event){const events=new Map(state.events.map(e=>[String(e.sequence),e]));events.set(String(event.sequence),event);state.events=[...events.values()].sort((a,b)=>a.sequence<b.sequence?-1:a.sequence>b.sequence?1:0).slice(-200);if(view==='activity')renderActivity();updateActivityFeed();}
function acceptMonitor(page){state.history=mergeHistory(state.history,page,state.epoch);state.epoch=page.epoch;state.cursor=page.cursor;state.monitor=page;state.received=performance.now();if(view==='monitor')renderMonitor();scheduleRefresh();updateFreshness();}
function scheduleRefresh(){if(!refreshTimer)refreshTimer=setTimeout(()=>{refreshTimer=null;refresh();},200);}
async function refresh(){if(refreshBusy){refreshAgain=true;return;}if(!state.connected)return;if(remoteDashboard){await remoteDashboard.refresh();return;}refreshBusy=true;const current=connection;try{
 const status=await current.call('status'),received=performance.now(),newMessages=await current.call('messages'),contacts=await current.call('agents');if(connection!==current)return;
 Object.assign(state,{status,statusReceived:received,messages:(newMessages??[]).slice(-200),contacts:contacts??[]});hostLabel.set({label:namedLabel(status,'Host '+String(status.host).slice(0,8))});namingConsole.updateConnection();updateMessages();updateActivityFeed();if(view!=='monitor')renderCurrent();
}catch(error){if(connection===current)notice(error.message);}finally{refreshBusy=false;if(refreshAgain){refreshAgain=false;scheduleRefresh();}}}
async function connect(){
 if(!session||Date.now()>=Date.parse(session.expires_at)){connectionState('Session expired');return;}
 const current=new Backend(session.token,{isCurrent:()=>connection===current,localNaming:()=>localOperator()&&!remoteDashboard,onNamingDiagnostic:text=>namingNotice.set({label:text??'',hidden:!text}),onEvent:event=>{addEvent(event);scheduleRefresh();},onMonitor:acceptMonitor,onWatch:()=>meshConsole?.invalidate(),onError:error=>notice(error.message),onClose:event=>{
  if(nodeContextClosed(session?.context,event))nodeContextInvalid=true;
  connectionState(nodeContextInvalid?'Node context changed':'Disconnected · retrying');updateFreshness();
  if(nodeContextInvalid){notice('Trust or permissions changed. Reopen this exact node from the local Mesh page.');return;}
  if(Date.now()<Date.parse(session?.expires_at))retryTimer=setTimeout(connect,Math.min(15000,1000*2**Math.min(retry++,4)));else notice('Dashboard session expired. Run arm dashboard for a new connection link.');
 }});connection=current;peerPoll.reset(current);
 try{await current.ready;if(connection!==current)return;
  if(session.context&&!session.context.local){
   if(session.context.host!==targetWindow?.host)throw Error('Node session does not match this window.');
   if(!remoteDashboard){remoteSlot=ui.stack().retain();remoteDashboard=createRemoteNodeDashboard({ui,root:remoteSlot,feed:ui.bind('liveActivityContent'),call:(...args)=>connection.call(...args),context:session.context,onLabel:label=>hostLabel.set({label}),onError:error=>{if(error.code==='node_context_changed'){nodeContextInvalid=true;current.socket.close();}}});
    ui.list(ARM+'navigation',C+'options',['resources','leases','hostView','screen'].map(x=>ARM+x));}
   hostLabel.set({label:'Node '+String(session.context.host).slice(0,8)});retry=0;connectionState('Connected to destination',true);notice();applyRoute(navigation.current);await remoteDashboard.refresh();return;
  }
  const status=await current.call('status');if(connection!==current)return;state.status=status;state.statusReceived=performance.now();hostLabel.set({label:namedLabel(status,'Host '+String(status.host).slice(0,8))});
  await current.call('subscribe',{after:status.cursor});await current.call('mesh_operator_watch');acceptMonitor(await current.call('monitor_subscribe',{epoch:state.epoch,after:state.cursor}));
  for(const event of await current.call('events',{after:BigInt(status.cursor)>100n?BigInt(status.cursor)-100n:0})??[])addEvent(event);
  state.messages=(await current.call('messages')??[]).slice(-200);state.contacts=await current.call('agents')??[];
  if(connection!==current||current.socket.readyState!==WebSocket.OPEN)return;retry=0;if(detail.open&&navigation.current.lease)disposeDetail();connectionState('Connected',true);notice();applyRoute(navigation.current);updateMessages();scheduleRefresh();refreshPalette();
 }catch(error){if(connection!==current)return;notice(`${error.message}. If the backend restarted, run arm dashboard for a fresh link.`);current.socket.close();}
}
const authentication=createDashboardAuthentication({exchange:async(ticket,signal)=>{const response=await fetch('/v1/browser/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ticket}),credentials:'omit',cache:'no-store',redirect:'error',signal});if(!response.ok)throw Error('Connection link expired or already used. Run arm dashboard again.');return decodeSessionJSON(await response.text());},
 readSession:()=>{try{const value=decodeSessionJSON(storage?.getItem(sessionKey)??'null');return targetWindow?.host&&value?.context?.host!==targetWindow.host?null:value;}catch{return null;}},writeSession:value=>{try{storage?.setItem(sessionKey,stringifyJSON(value));}catch{}},clearSession:()=>{try{storage?.removeItem(sessionKey);}catch{}},
 onStart:()=>{hostLabel.set({label:targetWindow?.host?'Node '+String(targetWindow.host).slice(0,8):'Local dashboard'});copyLoginGeneration++;copyingLogin=false;clearLogin();paletteNodes=[];palettePools=[];paletteKubernetes=null;screenViewer?.dispose();screenViewer=null;roots.screen.replace();remoteDashboard?.dispose();remoteDashboard=null;remoteSlot?.dispose();remoteSlot=null;remoteView=null;resettingSession=true;try{disposeDetail();if(detail.open)detail.close();}finally{resettingSession=false;}clearTimeout(retryTimer);clearTimeout(refreshTimer);refreshTimer=null;retry=0;const previous=connection;connection=null;session=null;previous?.disconnect();peerPicture=null;peerPoll.reset(null);connectionState('Authenticating');},onSession:value=>{if(targetWindow?.host&&value.context?.host!==targetWindow.host)throw Error('Open this exact node from the local Mesh page.');session=value;nodeContextInvalid=false;return connect();},onError:error=>{connectionState('Not connected');notice(error.message);}});
const connectionLinks=createConnectionLinkHandler({location,navigation,onConnect:link=>authentication.start(targetWindow?.host?{error:'Node windows require a host-bound handoff. Open this node again from the local Mesh page.'}:link),onStored:()=>authentication.start()});
window.addEventListener('popstate',connectionLinks.change);window.addEventListener('hashchange',connectionLinks.change);

// All monitor/history data is bounded; missing samples break chart series.
let paused=false,lastMonitorStale=null;
const pause=ui.check('Pause monitor display',{checked:false},()=>{paused=pause.checked;if(!paused)renderMonitor();}),monitorFresh=ui.text(''),monitorCards=ui.stack();roots.monitor.replace(ui.toolbar('Monitor display',[pause,monitorFresh]),monitorCards);
const ioSelections={disk_io:allDevices(),network:allDevices()};
function help(kind,options={}){const get=()=>typeof options==='function'?options():options,title=metricHelp(kind,get()).title;return ui.iconButton('About '+title,'?',()=>{const info=metricHelp(kind,get()),body=info.paragraphs.map(x=>ui.text(x));if(info.technical.length)body.push(ui.disclosure('Technical details',[ui.descriptions(info.technical)]));const d=ui.dialog(info.title,body);d.on('close',()=>d.dispose());d.showModal();});}
const GiB=1024**3;
function series(get,{scale=1}={}){const cadence=Number(state.monitor?.cadence_ns??2e9)/1e6,rows=state.history.map(p=>{const v=numeric(get(p));return{id:String(p.sequence),x:Date.parse(p.at)/1000,value:v===null||!Number.isFinite(v)?null:v/scale,label:new Date(p.at).toLocaleTimeString()};});for(let i=0;i<rows.length-1;i++)rows[i].connectNext=Number.isFinite(rows[i].x)&&Number.isFinite(rows[i+1].x)&&rows[i+1].x>rows[i].x&&(rows[i+1].x-rows[i].x)*1000<=cadence*1.6;return rows;}
let monitorModel=null;
function initMonitor(){
 const cpuValue=ui.stat('All-core activity','unavailable'),cores=ui.grid([],{columns:6,minWidth:'120px',gap:2}),cpuChart=ui.line('CPU activity over time',[],{min:0,max:100,unit:'%'}),cpuNote=ui.text('');
 const ramValue=ui.stat('RAM remaining','unavailable'),ramChart=ui.line('Available RAM over time',[],{min:0,max:1,unit:'GiB'}),ramFacts=ui.descriptions([]),ramNote=ui.text('');
 const diskValue=ui.stat('Space remaining','unavailable'),diskChart=ui.line('Available filesystem space over time',[],{min:0,max:1,unit:'GiB'}),diskFacts=ui.descriptions([]),diskNote=ui.text('');
 const cpuCard=ui.card('CPU activity',[cpuNote,cpuChart,ui.disclosure('Per-core activity',[cores])],{actions:[help('cpu',()=>({measurements:[['CPU',state.monitor?.latest?.cpu_busy_percent]]}))]}),
 ramCard=ui.card('Memory & swap',[ramNote,ramChart,ui.disclosure('Memory & swap details',[ramFacts])],{actions:[help('memory',()=>({measurements:[['Available RAM',state.monitor?.latest?.memory.available],['RAM used',state.monitor?.latest?.memory.used],['Available swap',state.monitor?.latest?.swap.available]]}))]}),
 diskCard=ui.card('Storage space',[diskNote,diskChart,ui.disclosure('Space details',[diskFacts])],{actions:[help('filesystem',()=>({filesystem:state.monitor?.latest?.filesystem.filesystem,measurements:[['Available space',state.monitor?.latest?.filesystem.available]]}))]});
 const io={};for(const[key,title]of [['disk_io','Disk I/O'],['network','Network']]){
  const summary=ui.text(''),rates=ui.descriptions([]),readChart=ui.line(title+' '+(key==='network'?'receive':'read'),[],{min:0,max:1,unit:'MiB/s'}),writeChart=ui.line(title+' '+(key==='network'?'send':'write'),[],{min:0,max:1,unit:'MiB/s'}),selectors=ui.grid([],{columns:4,minWidth:'128px',gap:2}),quality=ui.badge(''),deviceMap=new Map();
  const all=ui.button('All',()=>{ioSelections[key]=allDevices();renderMonitor();}),none=ui.button('None',()=>{ioSelections[key]=noDevices();renderMonitor();});
  const selector=ui.disclosure('Choose devices',[ui.toolbar('Device selection',[all,none]),selectors]);
  const card=ui.card(title,[quality,summary,rates,ui.grid([readChart,writeChart],{columns:2,minWidth:'140px',gap:2}),selector,ui.text('Reported counters may overlap. Unknown or reset counters leave gaps.')],{actions:[help(key)]});io[key]={summary,rates,readChart,writeChart,selectors,quality,deviceMap,card};
 }
 const gpu=ui.card('GPU monitoring',[ui.text('Unavailable in this build')],{actions:[help('gpu')]});
 owned(monitorCards,[ui.grid([cpuValue,ramValue,diskValue],{columns:3,minWidth:'180px',gap:2}),ui.grid([cpuCard,ramCard,diskCard],{columns:3,minWidth:'220px',gap:2}),ui.grid([io.disk_io.card,io.network.card],{columns:2,minWidth:'320px',gap:2}),gpu]);
 monitorModel={cpuValue,cpuChart,cpuNote,cores,coreMap:new Map(),ramValue,ramChart,ramFacts,ramNote,diskValue,diskChart,diskFacts,diskNote,io};
}
function updateDescriptions(handle,pairs){const stamp=JSON.stringify(pairs);if(handle.stamp===stamp)return;handle.stamp=stamp;handle.setPairs(pairs);}
function renderMonitor(){if(paused)return;const sample=state.monitor?.latest;if(!sample)return;
 const stale=monitorStale(state.monitor,state.connected,performance.now()-state.received);lastMonitorStale=stale;
 ui.batch(()=>{if(!monitorModel)initMonitor();const m=monitorModel;
  monitorFresh.set({label:(stale?'Stale observations':'Live host observations')+' · '+when(sample.at)});
  m.cpuValue.set({value:percent(sample.cpu_busy_percent.value)});m.cpuNote.set({label:`${sample.cores?.length??0} logical cores · includes work outside ARM`});m.cpuChart.setSeries(series(p=>p.cpu_busy_percent),{min:0,max:100,unit:'%'});
  const ids=new Set();for(const core of sample.cores??[]){ids.add(core.id);let bar=m.coreMap.get(core.id);if(!bar){bar=ui.progress(core.id+' unavailable',null);m.coreMap.set(core.id,bar);}bar.set({label:core.id+' '+percent(core.busy_percent.value),value:core.busy_percent.value??0,active:core.busy_percent.value!==null});}
  for(const[id,bar]of m.coreMap)if(!ids.has(id)){m.coreMap.delete(id);bar.dispose();}m.cores.replace(...[...ids].map(id=>m.coreMap.get(id)));
  m.ramValue.set({value:bytes(remainingValue(sample.memory.available,stale))});m.ramChart.setSeries(series(p=>p.memory_available_bytes,{scale:GiB}),{min:0,max:Math.max(1,numeric(sample.memory.total.value)/GiB),unit:'GiB'});
  m.ramNote.set({label:'Swap remaining '+bytes(remainingValue(sample.swap.available,stale))});
  updateDescriptions(m.ramFacts,[['RAM total',bytes(sample.memory.total.value)],['RAM used',bytes(sample.memory.used.value)],['Swap remaining',bytes(remainingValue(sample.swap.available,stale))],['Swap total',bytes(sample.swap.total.value)],['Swap used',bytes(sample.swap.used.value)]]);
  m.diskNote.set({label:stale||sample.filesystem_stale?'Space usage unavailable (stale observation)':sample.filesystem.used.value==null||sample.filesystem.total.value==null?'Space usage unavailable':bytes(sample.filesystem.used.value)+' of '+bytes(sample.filesystem.total.value)+' used'});
  m.diskValue.set({value:bytes(remainingValue(sample.filesystem.available,stale||sample.filesystem_stale))});m.diskChart.setSeries(series(p=>p.filesystem_available_bytes,{scale:GiB}),{min:0,max:Math.max(1,numeric(sample.filesystem.total.value)/GiB),unit:'GiB'});updateDescriptions(m.diskFacts,[['Space total',bytes(sample.filesystem.total.value)],['Space used',bytes(sample.filesystem.used.value)]]);
  for(const[key,title]of [['disk_io','Disk I/O'],['network','Network']]){
   const model=m.io[key],devices=sample[key]??[],selection=ioSelections[key],selected=selectedDeviceIDs(selection,devices),totals=aggregateIO(devices,selected),points=aggregateIOWindow(state.history,key,selection,devices),bySequence=new Map(points.map(p=>[String(p.sequence),p]));
   const quality=(key==='network'?sample.network_quality:sample.disk_quality)??'unavailable';model.quality.set({label:quality,hidden:quality==='reported'});model.summary.set({label:`Sum of ${selected.length} selected ${key==='network'?'interface':'device'} reports`});updateDescriptions(model.rates,[[key==='network'?'Receive':'Read',rate(totals.read)],[key==='network'?'Send':'Write',rate(totals.write)]]);
   const read=series(p=>bySequence.get(String(p.sequence))?.read,{scale:1024**2}),write=series(p=>bySequence.get(String(p.sequence))?.write,{scale:1024**2}),ceiling=Math.max(.001,...read.map(x=>x.value??0),...write.map(x=>x.value??0));model.readChart.setSeries(read,{min:0,max:ceiling,unit:'MiB/s'});model.writeChart.setSeries(write,{min:0,max:ceiling,unit:'MiB/s'});
   const inventory=new Map(devices.map(d=>[d.id,d.name||d.id]));for(const id of selected)if(!inventory.has(id))inventory.set(id,id+' (unavailable)');
   for(const[id,label]of inventory){let check=model.deviceMap.get(id);if(!check){check=ui.check(label,{},()=>{ioSelections[key]=toggleDevice(ioSelections[key],state.monitor?.latest?.[key]??[],id,check.checked);renderMonitor();});model.deviceMap.set(id,check);}check.set({label,checked:selected.includes(id)});}
   for(const[id,check]of model.deviceMap)if(!inventory.has(id)){model.deviceMap.delete(id);check.dispose();}model.selectors.replace(...[...inventory.keys()].map(id=>model.deviceMap.get(id)));
  }
 });}

function renderResources(){if(!state.status)return;renderResourceCards();renderPeers();refreshPeers();if(!poolConsole)poolConsole=createPoolConsole({ui,root:poolRoot,call:(...args)=>connection.call(...args),connected:()=>state.connected,onLease:showLease,onNavigate:route=>navigation.navigate({view:'resources',lease:null,...route}),onChanged:scheduleRefresh,storage});poolConsole.update({status:state.status,route:navigation.current});}
function renderResourceCards(){if(!state.status)return;const budgets=capacityBudgetsForView(state.status,{connected:state.connected,elapsedMS:performance.now()-state.statusReceived});capacityFreshness=capacityFreshnessKey(budgets);resourceOverview.update(state.status.resources??[],budgets,resourceFilter.value);}
async function refreshPeers(){if(!state.connected)return;const current=connection;if(!peerPoll.begin(current,performance.now()))return;try{const picture=await current.call('peers_status');if(connection!==current)return;peerPicture=picture;if(view==='resources')renderPeers();}catch(error){if(connection===current){peerPicture={discovery_unavailable:error.message,instances:[],totals_complete:false};renderPeers();}}finally{peerPoll.finish(current,performance.now());}}
function updatePeerStatus(){peerStatus.set({label:!state.connected?'Stale view · backend disconnected':peerPicture?.totals_complete?'Recent discovered peer reports':'Partial view · some peers unavailable'});}
function renderPeers(){ui.batch(()=>{const r=peerPicture,summary=[],details=[],notes=[];updatePeerStatus();if(r){for(const [label,snapshot]of [['This ARM instance',r.local],...(r.instances??[]).map(p=>['UID '+p.uid+' · '+p.host,p.snapshot])])if(snapshot){summary.push(ui.text(`${label} · Held ${hostResourceSummary(snapshot.held)} (${snapshot.held_count}); queued ${hostResourceSummary(snapshot.queued)} (${snapshot.queued_count})`));details.push(ui.card(label,(snapshot.items??[]).map(item=>ui.text(`${item.state} · ${hostResourceSummary(item.resources)} · ${item.state==='queued'?'queue #'+(item.queue_position??'?'):'expires '+when(item.expires_at)}`))));if(snapshot.truncated)notes.push(ui.text(label+': itemized list truncated; totals include all reported claims.',{tone:'warning'}));}if(r.discovery_unavailable)notes.push(ui.text(r.discovery_unavailable,{tone:'warning'}));notes.push(ui.text(r.advisory||'Peer reports are advisory.'));}peerSummary.replace(...summary);peerNotes.replace(...notes);peerDetails.replace(...details);});}
function renderLeases(){ui.batch(()=>{const all=state.status?.leases??[],matches=[...all].reverse().filter(l=>leaseVisible(l,hideInactive)&&(leaseStateLabel(l)+' '+stringifyJSON(l)).toLowerCase().includes(leaseFilter.value.toLowerCase())),rows=matches.slice(0,200);leaseCount.set({label:`${matches.length} matching of ${all.length} leases${matches.length>200?' · showing 200':''}`});owned(leaseCards,rows.length?rows.map(l=>{const claims=l.request?.resources??{},summary=[];if(claims.cpu)summary.push(count(claims.cpu)+' cores');if(claims.memory_bytes)summary.push(bytes(claims.memory_bytes)+' memory');if(claims.disk_bytes)summary.push(bytes(claims.disk_bytes)+' disk');if(l.ports?.length)summary.push(l.ports.length+' ports');if(l.request?.pools?.length)summary.push(l.request.pools.length+' pool claims');return ui.card(l.request?.reason||'Lease',[ui.button(namedLabel(l,l.id),()=>showLease(l.id)),ui.text('ID: '+l.id),ui.badge(leaseStateLabel(l)),ui.descriptions([['Owner',owner(l)],['Agent',l.request?.agent??'—'],['Claims',summary.join(' · ')||'—'],['Expires',when(l.expires_at)],...(l.exit_code!=null?[['Exit code',String(l.exit_code)]]:[])])]);}):[ui.text('No matching leases')]);});}
async function loadLease(id){detail.set({label:'Lease detail'});detailLeaseID=id;const request=++detailRequest;detailPanel?.dispose();detailReviews?.dispose();owned(detailBody,[ui.text('Loading lease…')]);if(!detail.open)detail.showModal();if(!state.connected){owned(detailBody,[ui.text('Waiting for a connection to inspect this lease…')]);return;}
 const current=connection;try{const lease=await current.call('inspect',{id}),status=await current.call('status');if(request!==detailRequest||!detail.open||connection!==current)return;renderLeaseDetail(lease,status.leases??[],request);}catch(error){if(request===detailRequest&&detail.open)owned(detailBody,[ui.text(error.message)]);}}
function renderLeaseDetail(lease,leases,request,resultNote=''){ui.batch(()=>{detailPanel?.dispose();detailReviews?.dispose();const current=connection,actions=ui.stack(),reviews=ui.stack();detail.set({label:namedLabel(lease,'Lease detail')});owned(detailBody,[ui.heading(namedLabel(lease,lease.id),3),ui.text('ID '+lease.id),ui.badge(leaseStateLabel(lease)),ui.text(lease.request?.reason??''),ui.text('Owner '+owner(lease)),ui.text('Agent '+(lease.request?.agent??'—')),ui.text('Project '+(lease.request?.project??'—')),ui.text('Expires '+when(lease.expires_at)),ui.text(resultNote),actions,reviews,ui.json(lease,'Full lease record')]);
 detailPanel=createLeaseActions({ui,root:actions,lease,leases,call:(...args)=>current.call(...args),connected:()=>state.connected&&connection===current,busy:()=>pendingLeaseActions.has(lease.id),onRefresh:()=>loadLease(lease.id),onPending:pending=>{if(pending)pendingLeaseActions.add(lease.id);else pendingLeaseActions.delete(lease.id);detailPanel?.updateConnection();detailReviews?.updateConnection();if(!pending&&request!==detailRequest&&detailLeaseID===lease.id&&detail.open)loadLease(lease.id);},onResult:result=>{scheduleRefresh();if(request===detailRequest&&detail.open)renderLeaseDetail(result,leases,request,leaseActionResult(result));}});
 detailReviews=createLeaseReviews({ui,root:reviews,lease,call:(...args)=>current.call(...args),connected:()=>state.connected&&connection===current,supported:!session?.context&&(state.status?.capabilities??[]).includes(REVIEW_CAPABILITY),busy:()=>pendingReviewConfigurations.has(lease.id),onPending:pending=>{if(pending)pendingReviewConfigurations.add(lease.id);else pendingReviewConfigurations.delete(lease.id);detailReviews?.updateConnection();if(!pending&&request!==detailRequest&&detailLeaseID===lease.id&&detail.open)loadLease(lease.id);},onRefresh:()=>loadLease(lease.id)});detail.focus();});}
const activityTimeline=ui.timeline('Durable operational events',50),activityDetails=ui.stack(),activityPage=ui.select('History page',[{value:'0',label:'Latest events'}],{},()=>renderActivity()),eventChoice=ui.select('Inspect an event',[],{},()=>renderEventDetail());
roots.activity.replace(ui.text('Latest 200 durable events · newest first'),activityPage,activityTimeline,eventChoice,activityDetails);
let activityRows=[];
function eventCaption(e){return String(e.kind??e.type??'Event').replaceAll(/[._-]/g,' ').replace(/^./,c=>c.toUpperCase());}
function eventSummary(e){return [e.data?.request?.reason??e.data?.reason??e.data?.detail??e.message??e.reason,e.entity].filter(Boolean).join(' · ').slice(0,320);}
function renderEventDetail(){const e=activityRows.find(e=>String(e.sequence)===eventChoice.value);activityDetails.replace(...(e?[ui.descriptions([['Type',e.type??e.kind],['At',when(e.at)],['Entity',e.entity],['Sequence',String(e.sequence)]]),...(String(e.type).startsWith('lease.')&&e.entity?[ui.button('Inspect lease',()=>showLease(e.entity))]:[]),ui.json(e,'Exact event record')]:[]));}
function renderActivity(){ui.batch(()=>{const all=state.events.slice().reverse(),pages=Math.max(1,Math.ceil(all.length/50)),page=Math.max(0,Math.min(pages-1,Number(activityPage.value)||0));activityPage.set({options:Array.from({length:pages},(_,i)=>({value:String(i),label:i===0?'Latest 50 events':`Events ${i*50+1}–${Math.min(all.length,(i+1)*50)}`})),value:String(page)});
 activityRows=all.slice(page*50,(page+1)*50);activityTimeline.setEvents(activityRows.slice().reverse().map(e=>({id:String(e.sequence),kind:eventCaption(e),at:e.at,summary:eventSummary(e)})));
 const selected=eventChoice.value;eventChoice.set({options:[{value:'',label:'Choose an event for its exact record'},...activityRows.map(e=>({value:String(e.sequence),label:`${eventCaption(e)} · ${when(e.at)}`}))],value:activityRows.some(e=>String(e.sequence)===selected)?selected:''});renderEventDetail();});}
function updateActivityFeed(){ui.batch(()=>{liveStatus.set({label:remoteDashboard?'Destination activity unavailable':holdActivity.checked?'Updates paused':state.connected?'Live':'Disconnected · observations retained',tone:state.connected&&!holdActivity.checked?'success':'warning'});if(remoteDashboard||holdActivity.checked)return;
 const entries=activityItems({events:state.events,messages:state.messages,leases:state.status?.leases??[],ownerContext:state.status},12);liveTimeline.setEvents(entries.slice().reverse().map(e=>({id:e.key,kind:e.title,at:e.at,summary:[e.body.length>120?e.body.slice(0,117)+'…':e.body,e.actor,e.status].filter(Boolean).join(' · ')})));
});}
function renderMesh(){if(!meshConsole)meshConsole=createMeshConsole({ui,root:roots.mesh,call:(...args)=>connection.call(...args),connected:()=>state.connected,onChanged:scheduleRefresh,onKubeInventory:snapshot=>{paletteKubernetes=snapshot;updatePalette();},storage,openNode:(entry,nodeView='resources')=>openNodeWindow({entry,call:(...args)=>connection.call(...args),view:nodeView,onError:notice})});meshConsole.refresh?.();}
function renderHost(){if(!state.status)return;ui.batch(()=>{const status=state.status,caps=status.capabilities??[],groups=[
 ['Resource management',[
  ['Live CPU headroom','live-cpu-admission'],['Live memory headroom','live-memory-admission'],['Live disk headroom','live-disk-admission'],['Counted resource pools','custom-resource-pools'],['Pool hierarchy','counted-resource-subpools'],['Owner budgets','local-owner-policy-budgets']]],
 ['Work & supervision',[
  ['Cooperative reservations','cooperative-reservation'],['Process supervision','process-group-supervision'],['Lease check-ins','local-lease-reviews-v1'],['Observed usage policy','observed-usage-policy'],['Graceful shutdown','draining-shutdown'],['Owned Gradle services','owned-gradle-services']]],
 ['Nodes & agents',[
  ['Mesh connections','automatic-mesh-lifecycle'],['Registered agent messaging','sender-aware-agent-messaging'],['Workspace agents','workspace-published-agents-poll-v1'],['Port reservations','port-reservation'],['Native port inventory','native-port-inventory'],['Socket activation','socket-activation']]],
 ['Observation & controls',[
  ['Durable event history','durable-events'],['Host monitoring','host-monitor-stream'],['Reported node metadata','reported-node-metadata'],['Database inspection & backups','database-inspection-backup'],['Reviewed browser controls','browser-reviewed-lease-controls']]]
 ];
 const cards=groups.map(([title,features])=>ui.card(title,features.filter(([,cap])=>caps.includes(cap)).map(([label])=>ui.text(label))));
 owned(roots.host,[ui.card('Local authority',[ui.descriptions([['Host',namedLabel(status,status.host?.slice(0,12)+'…')],['Owner',owner(status)],['Mode',status.draining?'Draining':'Accepting reviewed requests']]),ui.disclosure('Exact identity & cursor',[ui.descriptions([['Host ID',status.host],['Event cursor',count(status.cursor)]])])]),ui.heading('Available capabilities',3),ui.grid(cards,{columns:2}),ui.disclosure(`Technical identifiers · ${caps.length} capabilities`,[ui.code(caps.join('\n'))]),ui.json(status.infrastructure_ports??[],'Infrastructure ports')]);});}
function updateFreshness(){remoteDashboard?.updateFreshness();if(remoteDashboard)return;const elapsed=performance.now()-state.statusReceived;if(view==='resources'&&state.status){if(capacityFreshness!==capacityFreshnessKey(capacityBudgetsForView(state.status,{connected:state.connected,elapsedMS:elapsed})))renderResourceCards();refreshPeers();}
 if(view==='monitor'&&state.monitor){const stale=monitorStale(state.monitor,state.connected,performance.now()-state.received);if(stale!==lastMonitorStale)renderMonitor();freshness.set({label:`${stale?'STALE · ':''}${paused?'display paused · ':''}sample ${Math.max(0,Math.floor((performance.now()-state.received)/1000))}s ago`});}else if(state.status)freshness.set({label:`${state.connected?'':'STALE · '}state updated ${Math.max(0,Math.floor(elapsed/1000))}s ago`});}
setInterval(updateFreshness,1000);setInterval(()=>{if(remoteDashboard&&state.connected)remoteDashboard.refresh();},5000);
if(targetWindow?.error){navigation.sanitize();connectionState('Invalid node window');notice(targetWindow.error);}
else if(targetWindow?.nonce){navigation.sanitize();connectionState('Waiting for node authorization');receiveNodeWindow({route:targetWindow,exchange:async(ticket,host,nonce)=>{const response=await fetch('/v1/browser/session',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({ticket,host,window_nonce:nonce}),credentials:'omit',cache:'no-store',redirect:'error'});if(!response.ok)throw Error('Node handoff expired, changed, or already used. Reopen it from Mesh.');return decodeSessionJSON(await response.text());}}).then(value=>{try{storage?.setItem(sessionKey,stringifyJSON(value));}catch{}return authentication.start();}).catch(error=>{connectionState('Node not connected');notice(error.message);});}
else connectionLinks.start();
