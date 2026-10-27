const C='https://semblance.dev/ns/component#',KEY='https://semblance.dev/ns/keyboard#';
const RDF='http://www.w3.org/1999/02/22-rdf-syntax-ns#',RDFS='http://www.w3.org/2000/01/rdf-schema#',SEM='https://semblance.dev/ns/core#';
const ARM='https://arm.local/ui#';
const sections=['Commands','Resources','Leases','Nodes','Kubernetes Nodes','Pools','Messages'];
const views=['monitor','resources','leases','messages','activity','host','mesh','screen'];
const records=value=>Array.isArray(value)?value:[];
const text=value=>typeof value==='string'||typeof value==='number'?String(value):'';
const identity=value=>{const id=text(value);return id&&id.length<=1024?id:'';};
const compact=(value,max=100)=>text(value).replace(/\s+/g,' ').trim().slice(0,max);
const label=(...parts)=>parts.map(v=>compact(v,512)).filter(Boolean).join(' · ').slice(0,512);
const short=value=>{const s=text(value);return s.length>24?s.slice(0,12)+'…'+s.slice(-8):s;};
const qualified=(id,host)=>short(id)+' @ '+short(host);
const keywords=(...parts)=>parts.map(v=>text(v).slice(0,16384)).filter(Boolean).join('\n').slice(0,32768).replace(/[\uD800-\uDBFF]$/,'');
const key=(...parts)=>JSON.stringify(parts);

// Each loaded category has a separate record cap (reported by omitted). Labels
// stay concise and help contains short context; full IDs and bodies are keywords. Bounded whitelisted
// metadata enters stock c:keywords (requires the upstream keywords contract).
// Query folding, matching, navigation and highlighting
// belong to the stock CommandPalette/Typeahead rules, never to a host filter.
export function paletteEntries(data={},limit=256) {
  limit=Number.isInteger(limit)?Math.max(1,Math.min(256,limit)):256;
  const entries=[],seen=new Set(),omitted={};
  const host=identity(data.localHost)||identity(data.status?.host)||'local';
  const add=entry=>{if(!seen.has(entry.key)){seen.add(entry.key);entries.push(entry);}};
  for(const view of views)add({key:key('command',view),section:'Commands',label:'Go to '+view,help:'Open the '+view+' view',action:'navigate',route:{view,lease:null}});
  add({key:key('command','kubernetes'),section:'Commands',label:'Browse Kubernetes nodes',help:'Read inventory for an explicitly selected context',action:'browse-kubernetes'});
  function each(section,values,make){let accepted=0;for(const value of records(values)){const entry=make(value);if(!entry||seen.has(entry.key))continue;if(accepted++>=limit){omitted[section]=(omitted[section]||0)+1;continue;}add(entry);}}
  each('Resources',data.resources??data.status?.resources,r=>{const id=identity(r?.id);if(!id)return;const owner=identity(r.host)||host;return {key:key('resource',owner,id),section:'Resources',label:label('Resource',compact(r.title||r.name||r.kind||id),qualified(id,owner)),help:label(r.kind,r.unit,'Open resource budget'),keywords:keywords(id,owner,r.owner,r.kind,r.title,r.name,r.unit,r.pool),action:'resource',id,host:owner,record:r};});
  each('Leases',data.leases??data.status?.leases,l=>{const id=identity(l?.id);if(!id)return;const owner=identity(l.host)||host;return {key:key('lease',owner,id),section:'Leases',label:label('Lease',compact(l.title||l.request?.reason||l.state||id),qualified(id,owner)),help:label(l.state,'Owner '+(l.owner_username||l.owner||'unknown'),l.request?.agent),keywords:keywords(id,owner,l.owner,l.owner_username,l.title,l.request?.reason,l.request?.agent,l.state),action:'lease',id,host:owner,record:l};});
  each('Nodes',data.nodes,n=>{const id=identity(n?.host);if(!id)return;return {key:key('node',id),section:'Nodes',label:label('Node',compact(n.name||n.title||id),short(id)),help:label(n.health||n.state,n.trust?.execution_account?.username,'Open node dashboard'),keywords:keywords(id,n.owner,n.name,n.title,n.state),action:'node',id,host:id,record:n};});
  const kube=data.kubernetes,context=identity(kube?.context),server=identity(kube?.cluster?.server),ca=identity(kube?.cluster?.ca_sha256),namespace=identity(kube?.namespace?.uid);
  if(context&&server&&ca&&namespace)each('Kubernetes Nodes',kube.nodes,n=>{const id=identity(n?.uid);if(!id)return;return{key:key('kubernetes-node',context,server,ca,namespace,id),section:'Kubernetes Nodes',label:label('Kubernetes',compact(n.name||id),context),help:label(n.os,n.arch,n.ready?'Ready':'Not ready',kube.namespace.name),keywords:keywords(id,n.name,context,server,ca,namespace,kube.namespace.name,n.os,n.arch,n.zone,n.instance_type),action:'kubernetes-node',id,record:{uid:id,context,server,ca_sha256:ca,namespace:kube.namespace,node:n}};});
  const pools=data.pools??records(data.resources??data.status?.resources).filter(r=>r?.custom?.pool).map(r=>r.custom);
  each('Pools',pools,v=>{const p=v?.pool??v,id=identity(p?.id);if(!id)return;const owner=identity(p.host)||host;return {key:key('pool',owner,id),section:'Pools',label:label('Pool',compact(p.name||p.title||id),qualified(id,owner)),help:label(p.scope,p.unit,compact(p.description)),keywords:keywords(id,owner,p.owner,p.name,p.title,p.description,p.scope,p.unit),action:'pool',id,host:owner,record:v};});
  each('Messages',data.messages,m=>{const id=identity(m?.id);if(!id)return;const owner=identity(m.host)||host;return {key:key('message',owner,id),section:'Messages',label:label('Message',compact(m.sender,32),compact(m.title||m.subject||m.body||id),qualified(id,owner)),help:label('To '+(m.audience||'operator'),m.lease?'Lease '+short(m.lease):'',m.created_at),keywords:keywords(id,owner,m.title,m.subject,m.sender,m.audience,m.lease,m.body),action:'message',id,host:owner,record:m};});
  return {entries,omitted};
}

// Requires the shared ui.handled effect boundary to consume arm:requested on
// nested synthetic Clicks (stock PaletteRun raises the selected Button's Click).
export function createCommandPalette({ui,navigate=()=>{},onLease=()=>{},onNode=()=>{},onMessage=()=>{},onKubernetesNode=()=>{},onBrowseKubernetes=()=>{},onResource,onPool,limit=256}) {
  const palette=ui.component('CommandPalette',{label:'Commands and resources',placeholder:'Search commands, resources, leases, nodes, pools and messages',open:false});
  const handles=new Map();let current=new Map(),disposed=false,remote=false,signature='',omitted={};
  const sectionIRIs=new Map(sections.map((name,i)=>[name,palette.iri+'/section/'+i]));
  const command=palette.iri+'/keyboard',source=palette.iri+'/explicit';
  ui.batch(()=>{
    ui.put(source,RDF+'type',C+'PaletteSource',true);ui.put(palette.iri,C+'lists',source,true);ui.put(palette.iri,C+'recentLimit',0);
    for(const [i,name]of sections.entries()){const iri=sectionIRIs.get(name);ui.put(iri,RDF+'type',C+'PaletteSection',true);ui.put(iri,RDFS+'label',name);ui.put(iri,SEM+'order',i+1);}
    ui.put(command,RDF+'type',KEY+'Command',true);ui.put(command,C+'opens',palette.iri,true);ui.put(command,KEY+'shortcut','mod+k');
    ui.desktopHosts.add(palette.iri);ui.syncDesktop();
  });
  function activate(id){
    const entry=current.get(id);if(disposed||remote||!entry)return;
    if(entry.action==='navigate')return navigate(entry.route);
    if(entry.action==='lease')return onLease(entry.id,entry.record);
    if(entry.action==='node')return onNode(entry.record);
    if(entry.action==='browse-kubernetes')return onBrowseKubernetes();
    if(entry.action==='kubernetes-node')return onKubernetesNode(entry.record);
    if(entry.action==='message'){navigate({view:'messages',lease:null});return onMessage(entry.id,entry.record);}
    if(entry.action==='pool')return onPool?onPool(entry.id,entry.record):navigate({view:'resources',pool:entry.id,lease:null});
    if(entry.action==='resource')return onResource?onResource(entry.id,entry.record):navigate({view:'resources',pool:entry.record.custom?.pool?.id||null,lease:null});
  }
  function update(data={}) {
    if(disposed)return;
    // Context is sticky across partial inventory updates. Only explicit local
    // context can re-enable a palette disabled by a remote dashboard switch.
    if(Object.hasOwn(data,'remote'))remote=Boolean(data.remote);
    if(Object.hasOwn(data,'context'))remote=data.context!=='local'&&data.context?.remote!==false;
    const inventory=remote?{entries:[],omitted:{}}:paletteEntries(data,limit);
    const next=new Map(inventory.entries.map(e=>[e.key,e]));current=next;omitted=inventory.omitted;
    const nextSignature=JSON.stringify([remote,inventory.entries.map(({key,section,label,help,keywords})=>[key,section,label,help,keywords])]);
    if(signature===nextSignature)return;signature=nextSignature;
    ui.batch(()=>{
      if(remote){palette.close();palette.value='';}
      ui.put(command,KEY+'shortcut',remote?null:'mod+k');
      for(const entry of inventory.entries){let handle=handles.get(entry.key);if(!handle){const iri=palette.iri+'/entry/'+encodeURIComponent(entry.key);handle=ui.bind(iri.slice(ARM.length),'Button');ui.put(iri,RDF+'type',C+'Button',true);const entryKey=entry.key;handle.on('activate',()=>activate(entryKey));handles.set(entry.key,handle);}handle.set({label:entry.label,help:entry.help});ui.put(handle.iri,C+'keywords',entry.keywords||null);ui.put(handle.iri,C+'paletteSection',sectionIRIs.get(entry.section),true);}
      ui.list(palette.iri,C+'entries',inventory.entries.map(e=>handles.get(e.key).iri));
      for(const [id,handle]of handles)if(!next.has(id)){handles.delete(id);handle.dispose();}
    });
  }
  update();
  return {element:palette,update,get omitted(){return {...omitted};},open(){if(disposed||remote)return false;palette.showModal();return true;},dispose(){if(disposed)return;disposed=true;current.clear();ui.batch(()=>{palette.close();for(const h of handles.values())h.dispose();handles.clear();palette.dispose();});}};
}
