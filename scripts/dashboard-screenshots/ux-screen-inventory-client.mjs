// Approved no-effect dependencies for the actual Screen factory; no noVNC/ARD/network.
const seed=window.__destinationSeed;
const bytes=s=>new TextEncoder().encode(s).length;
const account='synthetic-screen-account';
const text='Synthetic viewer clipboard\n'+'bounded-local-text '.repeat(18)+'\n'+ 'unbroken'.repeat(32)+'\nFinal synthetic line';
const f=window.screenInventoryFixture={account,text,instances:[],factories:[],calls:[],events:[],forbidden:[],loadMode:'ready',loadHolds:[],clipboardMode:'ready',clipHolds:[],nextARD:false,lastRead:null,lastWrite:null,lastSend:null,activeFacade:0,credentialAttempts:0};
const fail=(kind)=>{f.forbidden.push(kind);throw Error('Refused synthetic operation: '+kind);};
const check=(value,kind)=>{if(!value)fail(kind);};
const current=()=>f.instances.at(-1);
const bounded=value=>typeof value==='string'&&bytes(value)<=4096;
function live(kind){check(current()?._rfbConnectionState==='connected',kind+'-not-connected');}
export class SyntheticRFB{
 constructor(surface,url,options){
  check(surface?.tagName==='DIV'&&surface.getAttribute('aria-label')==='Remote Mac screen','frame-surface');
  check(url===`ws://${location.host}/v1/screen`,'constructor-url');
  check(options&&Object.keys(options).sort().join(',')==='credentials,wsProtocols','constructor-keys');
  check(Array.isArray(options.wsProtocols)&&options.wsProtocols.length===2&&options.wsProtocols[0]==='arm.v1'&&options.wsProtocols[1]===`arm.auth.${seed.token}`,'constructor-protocols');
  check(options.credentials===undefined||Object.keys(options.credentials).join(',')==='username'&&options.credentials.username===account,'constructor-account');
  this.clientID=f.instances.length;this.surface=surface;this.events=new Map();this._rfbConnectionState='connecting';this._rfbAppleARD=f.nextARD;this._viewOnly=false;this._mode=1;this._quality=null;this._virtual=null;this._scale=null;this._resize=null;this._cursor=null;this.modeBytes=0;
  this._sock={sQpushBytes:value=>{check(value instanceof Uint8Array&&value.length===4&&value[0]===10&&value[1]===0&&value[2]===0&&[0,1].includes(value[3]),'mode-bytes');this.modeBytes++;f.calls.push({kind:'mode-bytes',mode:value[3],client:this.clientID});},flush:()=>f.calls.push({kind:'mode-flush',client:this.clientID})};
  this._fail=reason=>{check(reason==='Screen Sharing protocol failed.','private-protocol-error');f.calls.push({kind:'protocol-failed',client:this.clientID});};this._initMsg=()=>{};
  this.seededAccount=Boolean(options.credentials);Object.preventExtensions(this);const proxy=new Proxy(this,{get(target,key,receiver){if(typeof key==='symbol'||key in target)return Reflect.get(target,key,receiver);return fail('unknown-client-read:'+String(key));},set(target,key,value,receiver){if(!(key in target))return fail('unknown-client-write:'+String(key));return Reflect.set(target,key,value,receiver);}});f.instances.push(proxy);f.calls.push({kind:'constructor',client:this.clientID,knownURL:true,protocolsExact:true,seededAccount:this.seededAccount,ard:this._rfbAppleARD});return proxy;
 }
 set ardQualityPreset(v){check(v==='millions','quality');this._quality=v;}get ardQualityPreset(){return this._quality;}
 set _ardVirtualLogin(v){check([0,2].includes(v),'virtual-login');this._virtual=v;}get _ardVirtualLogin(){return this._virtual;}
 set ardControlMode(v){check([0,1].includes(v),'control-mode');this._mode=v;this._viewOnly=v===0;f.calls.push({kind:'ard-mode-property',mode:v,client:this.clientID});}get ardControlMode(){return this._mode;}
 set viewOnly(v){check(typeof v==='boolean','view-only');this._viewOnly=v;}get viewOnly(){return this._viewOnly;}
 set scaleViewport(v){check(v===true,'scale');this._scale=v;}get scaleViewport(){return this._scale;}
 set resizeSession(v){check(v===false,'resize');this._resize=v;}get resizeSession(){return this._resize;}
 set showDotCursor(v){check(v===true,'cursor');this._cursor=v;}get showDotCursor(){return this._cursor;}
 addEventListener(type,fn){check(['credentialsrequired','clipboard','connect','securityfailure','disconnect'].includes(type)&&typeof fn==='function'&&!this.events.has(type),'event-listener');this.events.set(type,fn);}
 emit(type,detail={}){check(this.events.has(type),'event-type');if(type==='credentialsrequired')check(Array.isArray(detail.types)&&detail.types.length<=3&&new Set(detail.types).size===detail.types.length&&detail.types.every(x=>['username','password','target'].includes(x)),'credential-types');if(type==='clipboard')check(Object.keys(detail).join(',')==='text'&&bounded(detail.text),'incoming-clipboard');if(type==='disconnect')check(Object.keys(detail).join(',')==='clean'&&typeof detail.clean==='boolean','disconnect-detail');if(type==='connect')this._rfbConnectionState='connected';if(type==='disconnect')this._rfbConnectionState='disconnected';f.events.push({type,client:this.clientID,...(type==='credentialsrequired'?{types:detail.types}:{}),...(type==='clipboard'?{bytes:bytes(detail.text)}:{})});this.events.get(type)({detail});}
 sendCredentials(){f.credentialAttempts++;fail('credential-submission');}
 clipboardPasteFrom(value){live('send');check(!this.viewOnly&&bounded(value),'send-value');f.lastSend=value;f.calls.push({kind:'clipboard-send',bytes:bytes(value),client:this.clientID});}
 requestRemoteClipboard(){live('request');check(this._rfbAppleARD,'request-capability');f.calls.push({kind:'clipboard-request',client:this.clientID});}
 focus(){f.calls.push({kind:'focus',client:this.clientID});}
 disconnect(){this._rfbConnectionState='disconnected';f.calls.push({kind:'disconnect',client:this.clientID});}
}
export async function loadSyntheticRFB(){f.calls.push({kind:'load',mode:f.loadMode});if(f.loadMode==='error')throw Error('Synthetic unavailable loader');if(f.loadMode==='pending')return await new Promise(resolve=>f.loadHolds.push(resolve));return SyntheticRFB;}
async function clipboardCall(kind,value){live(kind);f.activeFacade++;f.calls.push({kind,mode:f.clipboardMode,...(value===undefined?{}:{bytes:bytes(value)})});try{if(value!==undefined)check(bounded(value),'browser-write-value');if(f.clipboardMode==='denied')throw Error('Synthetic denied clipboard');if(f.clipboardMode==='pending')await new Promise(resolve=>f.clipHolds.push(resolve));if(kind==='browser-write'){f.lastWrite=value;return;}f.lastRead=f.text;return f.text;}finally{f.activeFacade--;}}
const clipboard={readText(...args){check(args.length===0,'browser-read-args');return clipboardCall('browser-read');},writeText(...args){check(args.length===1,'browser-write-args');return clipboardCall('browser-write',args[0]);}};
export const syntheticNavigator={get clipboard(){return f.clipboardMode==='missing'?undefined:clipboard;}};
f.release=()=>{f.loadMode='ready';for(const resolve of f.loadHolds.splice(0))resolve(SyntheticRFB);f.clipboardMode='ready';for(const resolve of f.clipHolds.splice(0))resolve();};
f.viewer=()=>f.factories.filter(x=>x.bootstrapComplete).at(-1)?.api;
f.capture=async()=>{f.viewer()?.dispose();f.release();await new Promise(r=>setTimeout(r,50));check(f.activeFacade===0&&!f.loadHolds.length&&!f.clipHolds.length,'capture-pending');check(f.credentialAttempts===0,'capture-auth');return{calls:f.calls,events:f.events,forbidden:f.forbidden,credentialAttempts:f.credentialAttempts,activeFacade:f.activeFacade,pendingLoaders:f.loadHolds.length,pendingClipboard:f.clipHolds.length,viewerConnected:f.viewer()?.connected??false,factories:f.factories.map(x=>({bootstrapComplete:x.bootstrapComplete,referencesExact:x.referencesExact})),realEffects:window.__screenRealEffects};};
