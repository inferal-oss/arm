// Public-contract-shaped Monitor observations; read-only in-memory transport.
// No sockets, host discovery, configuration, resource operations or OS collectors.
const f=window.monitorFixture={calls:[],forbidden:[],connections:[],cursor:12,state:'fresh',membership:'initial',holdReconnect:false};
const base=Date.parse('2026-10-29T12:00:00Z'),GiB=1024**3,MiB=1024**2;
const at=sequence=>new Date(base+sequence*2000).toISOString();
function measured(value,unit,scope,sequence){
  return{value,unit,scope,method:'synthetic-native-observation',quality:value===null?'unavailable':'reported',
    at:at(sequence),complete:value!==null,detail:'Synthetic observation first paragraph.\n\nSecond paragraph with bounded collector context. '+ 'detail'.repeat(24)};
}
function devices(key,sequence){
  const ids=Array.from({length:12},(_,i)=>key+'-synthetic-'+String(i).padStart(2,'0'));
  if(f.membership==='removed'){ids.shift();ids.push(key+'-synthetic-12');}
  if(f.membership==='returned')ids.push(key+'-synthetic-12');
  if(f.state==='empty')return[];
  return ids.map(id=>{
    const i=Number(id.slice(-2)),unknown=f.state==='unknown'||f.state==='reset'&&i===0;
    const amount=f.state==='zero'?0:(i+1)*MiB;
    return{id,name:'Synthetic '+(key==='disk'?'storage':'interface')+' '+i+' · '+ 'longlabel'.repeat(6),
      read_bytes_per_second:measured(unknown?null:amount,'bytes/second',key+':'+id,sequence),
      write_bytes_per_second:measured(unknown?null:amount*2,'bytes/second',key+':'+id,sequence),interval_ns:2000000000};
  });
}
function sample(sequence){
  const missing=['unknown','empty'].includes(f.state),zero=f.state==='zero';
  const resource=(total,available,scope)=>({
    total:measured(missing?null:total,'bytes',scope+':total',sequence),
    available:measured(missing?null:zero?0:available,'bytes',scope+':available',sequence),
    used:measured(missing?null:zero?total:total-available,'bytes',scope+':used',sequence),
  });
  return{at:at(sequence),collection_ns:1000000,cpu_interval_ns:2000000000,
    cpu_busy_percent:measured(missing?null:zero?0:46.5,'percent','host-cpu',sequence),
    cores:f.state==='empty'?[]:Array.from({length:32},(_,i)=>({id:'cpu'+i,busy_percent:measured(missing?null:zero?0:(i*3)%100,'percent','cpu:'+i,sequence)})),
    memory:resource(32*GiB,16*GiB,'host-memory'),swap:resource(4*GiB,3*GiB,'host-swap'),
    filesystem:{...resource(512*GiB,256*GiB,'state-filesystem'),filesystem:{path:'/synthetic/state',type:'apfs',volume:'synthetic-volume',pool:'synthetic-shared-pool',identity_quality:'reported',mount:'/synthetic',shared:true,
      pool_total:measured(1024*GiB,'bytes','synthetic-pool',sequence),pool_free:measured(512*GiB,'bytes','synthetic-pool',sequence),
      detail:'Synthetic shared pool; volumes overlap and must not be added. '+ 'filesystem'.repeat(20)}},
    filesystem_stale:f.state==='stale',
    disk_io:devices('disk',sequence),network:devices('network',sequence),
    disk_quality:missing?'unavailable':'reported',
    network_quality:missing?'unavailable':'reported',
    disk_detail:'Synthetic reported device counters',network_detail:'Synthetic reported interface counters',
    gpu_busy_percent:measured(null,'percent','gpu-unavailable',sequence)};
}
function point(sequence,s){
  return{sequence,at:s.at,cpu_busy_percent:s.cpu_busy_percent.value,
    memory_used_bytes:s.memory.used.value,memory_available_bytes:s.memory.available.value,
    swap_used_bytes:s.swap.used.value,filesystem_available_bytes:s.filesystem.available.value,filesystem_at:s.at,
    disk_io:s.disk_io.map(d=>({id:d.id,read:d.read_bytes_per_second.value,write:d.write_bytes_per_second.value})),
    network:s.network.map(d=>({id:d.id,read:d.read_bytes_per_second.value,write:d.write_bytes_per_second.value}))};
}
f.history=Array.from({length:12},(_,i)=>{
  const s=sample(i+1),p=point(i+1,s);
  if(i===3){p.cpu_busy_percent=null;p.disk_io[0].read=null;p.network[0].write=null;}
  if(i===6){p.memory_available_bytes=null;p.disk_io=p.disk_io.slice(1);}
  if(i===8)p.filesystem_available_bytes=null;
  return p;
});
f.latest=sample(f.cursor);
f.page=()=>({host:'a'.repeat(32),epoch:'b'.repeat(32),cursor:f.cursor,oldest:f.history[0].sequence,
  gap:false,cadence_ns:2000000000,filesystem_cadence_ns:5000000000,
  stale_after_ns:3600000000000,age_ns:f.state==='stale'?3600000000001:0,stale:f.state==='stale',
  latest:structuredClone(f.latest),history:structuredClone(f.history)});
f.publish=({state=f.state,membership=f.membership}={})=>{
  f.state=state;f.membership=membership;f.cursor++;f.latest=sample(f.cursor);
  f.history.push(point(f.cursor,f.latest));f.history=f.history.slice(-24);
  f.connections.at(-1).options.onMonitor(f.page());
};
f.disconnect=()=>{f.holdReconnect=true;f.connections.at(-1).socket.close();};
f.releaseReconnect=()=>{f.holdReconnect=false;f.resolveReconnect?.();f.resolveReconnect=null;};
export class Backend{
  constructor(token,options){
    this.options=options;this.socket={readyState:1,close:()=>{
      if(this.socket.readyState===3)return;this.socket.readyState=3;options.onClose({code:1006});
    }};
    this.ready=f.holdReconnect?new Promise(resolve=>{f.resolveReconnect=resolve;}):Promise.resolve();
    f.connections.push(this);
  }
  disconnect(){this.socket.readyState=3;}
  async call(operation,params={}){
    f.calls.push({operation,params});
    if(operation==='status')return{host:'a'.repeat(32),owner:'1000',cursor:f.cursor,capabilities:[],resources:[],leases:[]};
    if(operation==='monitor_subscribe')return f.page();
    if(['subscribe','mesh_operator_watch'].includes(operation))return{subscription:'synthetic-read-only'};
    if(operation==='node_list')return{nodes:[]};
    if(['events','messages','agents','pool_list'].includes(operation))return[];
    f.forbidden.push(operation);throw Error('Unexpected Monitor inventory operation: '+operation);
  }
}
