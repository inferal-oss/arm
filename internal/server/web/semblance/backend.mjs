import {parseJSON,stringifyJSON} from '../core.mjs';
import {fixedNamingDiagnostic} from '../resource-names.mjs';
const namingSnapshots=new Set(['status','node_list','name_list']);

// Same bounded authenticated protocol as the original dashboard. Requests are
// rejected on disconnect and never replayed by this transport.
export class Backend {
 constructor(token,{isCurrent=()=>true,onEvent=()=>{},onMonitor=()=>{},onWatch=()=>{},onClose=()=>{},onError=()=>{},onNamingDiagnostic=()=>{},localNaming=()=>false,location=globalThis.location,Socket=globalThis.WebSocket}={}) {
  this.pending=new Map();this.next=0;this.namingObserved=0;this.Socket=Socket;
  this.socket=new Socket(`ws://${location.host}/v1/ws`,['arm.v1',`arm.auth.${token}`]);
  this.ready=new Promise((resolve,reject)=>{
   const timer=setTimeout(()=>{reject(Error('Connection timed out'));this.socket.close();},10000);
   this.socket.addEventListener('open',()=>{clearTimeout(timer);resolve();},{once:true});
   this.socket.addEventListener('close',()=>{clearTimeout(timer);reject(Error('Connection refused'));},{once:true});
  });
  this.socket.addEventListener('message',event=>{if(!isCurrent())return;try{
   const response=parseJSON(event.data);if(response.version!==1)throw Error('Unsupported protocol version');
   if(response.event)onEvent(response.event);
   else if(response.telemetry&&response.subscription===this.monitorID)onMonitor(response.telemetry);
   else if(this.meshWatchID&&response.subscription===this.meshWatchID)onWatch();
   else if(response.id&&this.pending.has(response.id)){
    const p=this.pending.get(response.id);this.pending.delete(response.id);clearTimeout(p.timer);
    if(localNaming()&&p.sequence>=this.namingObserved){const diagnostic=response.naming_diagnostic??(namingSnapshots.has(p.operation)?response.result?.naming_diagnostic:null);if(diagnostic){this.namingObserved=p.sequence;onNamingDiagnostic(fixedNamingDiagnostic(diagnostic));}else if(!response.error&&namingSnapshots.has(p.operation)){this.namingObserved=p.sequence;onNamingDiagnostic(null);}}
    if(p.operation==='name_mutate'&&!response.error&&(response.naming_diagnostic||response.result?.naming_diagnostic))p.reject(Error('Naming mutation response is unhealthy; outcome may be unknown. Retain the exact request and read current names.'));
    else if(response.error)p.reject(Object.assign(Error(`${response.error.code}: ${response.error.message}`),{code:response.error.code,definitive:true}));
    else{if(p.operation==='mesh_operator_watch')this.meshWatchID=response.result?.subscription;p.resolve(response.result);}
   }
  }catch(error){onError(error);this.socket.close();}});
  this.socket.addEventListener('close',event=>{for(const p of this.pending.values()){clearTimeout(p.timer);p.reject(Error('Backend disconnected'));}this.pending.clear();if(isCurrent())onClose(event);});
 }
 disconnect(){for(const p of this.pending.values()){clearTimeout(p.timer);p.reject(Error('Dashboard session changed; the prior request outcome may be unknown'));}this.pending.clear();this.socket.close();}
 call(operation,params={}){
  if(this.socket.readyState!==this.Socket.OPEN)return Promise.reject(Error('Backend disconnected'));
  if(this.pending.size>=4)return Promise.reject(Error('Dashboard request limit reached'));
  const id=String(++this.next);if(operation==='monitor_subscribe')this.monitorID=id;
  return new Promise((resolve,reject)=>{const timer=setTimeout(()=>{this.pending.delete(id);reject(Error(`${operation} timed out`));},15000);this.pending.set(id,{resolve,reject,timer,operation,sequence:this.next});
   try{this.socket.send(stringifyJSON({version:1,id,operation,params}));}catch(error){clearTimeout(timer);this.pending.delete(id);reject(error);}
  });
 }
}
