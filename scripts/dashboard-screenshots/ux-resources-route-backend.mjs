// Synthetic read-only transport substitute for the actual Resources route.
// No sockets, credentials, backend authority or live resource actions are used.
const f = window.fixture = {
  calls: [], forbidden: [], connections: [], statusRequests: 0,
  holdStatus: true, statusFailure: false, holdPeers: true,
};
const observation = {
  known: true, available_cores: 4, total_bytes: 17179869184,
  available_bytes: 8589934592, scope: 'synthetic-scope-' + 's'.repeat(64),
  method: 'synthetic-collector-' + 'm'.repeat(64), quality: 'verified',
  at: '2026-10-29T12:00:00Z', interval_ns: 2000000000, eligible_cpus: 8,
  detail: 'First collector paragraph.\n\nSecond collector paragraph. ' + 'detail'.repeat(32),
};
const budget = {
  known: true, age_ns: 0, stale_after_ns: 3600000000000,
  observation, capacity_cores: 8, reserved_cores: 2, available_cores: 6,
  deficit_cores: 0, managed_credit_cores: 1,
};
f.status = {
  host: 'a'.repeat(32), owner: '1000', cursor: 1, capabilities: [], leases: [],
  cpu_budget: budget, memory_budget: budget, disk_budget: budget,
  resources: [
    {id:'cpu',kind:'cpu',unit:'cores'},
    {id:'memory',kind:'memory',unit:'bytes'},
    {id:'disk',kind:'disk',unit:'bytes'},
    {id:'port',kind:'port',unit:'bindings'},
    {id:'synthetic-permits',kind:'custom',unit:'permits',custom:{pool:{name:'Synthetic permits'}}},
  ].map(r => ({...r,reserved:2,available:6,budget_total:8,users:[],
    accounting:'Full promises remain charged. ' + 'accounting'.repeat(24)})),
};
const claim = (i) => ({
  state:i % 2 ? 'queued':'active', queue_position:i + 1,
  resources:{cpu:2,memory_bytes:1073741824,disk_bytes:2147483648},
  expires_at:'2026-11-01T12:00:00Z',
});
const snapshot = {
  held:{cpu:6,memory_bytes:3221225472,disk_bytes:6442450944},
  held_count:3, queued:{cpu:6,memory_bytes:3221225472,disk_bytes:6442450944},
  queued_count:3, truncated:true, items:Array.from({length:6},(_,i)=>claim(i)),
};
f.picture = {
  totals_complete:false, local:snapshot,
  instances:Array.from({length:4},(_,i)=>({
    uid:1000+i,host:(i+11).toString(16).repeat(32),snapshot,
  })),
  advisory:'Peer reports are advisory.\n\nThese synthetic reports do not grant resources.',
};
f.releaseReady = () => f.connections.at(-1).resolveReady();
f.releaseStatus = () => {
  f.holdStatus=false;
  f.statusPending?.resolve(structuredClone(f.status));f.statusPending=null;
};
f.releasePeers = (error) => {
  f.holdPeers=false;
  if(error) f.peersPending?.reject(Error(error));
  else f.peersPending?.resolve(structuredClone(f.picture));
  f.peersPending=null;
};
f.disconnect = () => f.connections.at(-1).socket.close();
f.refresh = () => f.connections.at(-1).options.onEvent({sequence:++f.status.cursor,kind:'synthetic',at:'2026-10-29T12:00:00Z'});
export class Backend {
  constructor(token,options) {
    this.options=options;
    this.socket={readyState:1,close:()=>{
      if(this.socket.readyState===3)return;
      this.socket.readyState=3;
      f.statusPending?.reject(Error('Synthetic backend disconnected'));f.statusPending=null;
      f.peersPending?.reject(Error('Synthetic backend disconnected'));f.peersPending=null;
      options.onClose({code:1006});
    }};
    this.ready=new Promise(resolve=>{this.resolveReady=resolve;});
    f.connections.push(this);
  }
  disconnect(){this.socket.readyState=3;}
  async call(operation,params={}) {
    f.calls.push({operation,params});
    if(operation==='status') {
      f.statusRequests++;
      if(f.statusFailure)throw Error('Synthetic status unavailable');
      if(f.holdStatus)return new Promise((resolve,reject)=>{f.statusPending={resolve,reject};});
      return structuredClone(f.status);
    }
    if(operation==='peers_status') {
      if(f.holdPeers)return new Promise((resolve,reject)=>{f.peersPending={resolve,reject};});
      return structuredClone(f.picture);
    }
    if(['subscribe','mesh_operator_watch'].includes(operation))return {subscription:'synthetic'};
    if(operation==='monitor_subscribe')return {epoch:'synthetic',cursor:0,history:[]};
    if(operation==='node_list')return {nodes:[]};
    if(['events','messages','agents','pool_list'].includes(operation))return [];
    f.forbidden.push(operation);throw Error('Unexpected synthetic operation: '+operation);
  }
}
