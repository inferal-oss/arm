import {nameBindingKey,sameNameBinding,validCustomName,validNameSnapshot} from '../resource-names.mjs';
import {stringifyJSON,parseJSON} from '../core.mjs';

const sameRevision=(a,b)=>{try{return BigInt(a)===BigInt(b);}catch{return false;}};
const requestBinding=request=>({kind:request.kind,authority_host:request.authority_host,id:request.id,binding_lease:request.binding_lease});
function resultMatches(request,result) {
 if(BigInt(result.revision)<BigInt(request.expected_revision))return false;
 if(request.operation==='clear')return !result.entries.some(entry=>sameNameBinding(entry,requestBinding(request))&&entry.name===request.name);
 return result.entries.some(entry=>entry.active&&entry.kind===request.kind&&entry.authority_host===request.authority_host&&entry.id===request.id&&entry.name===request.name&&(!request.binding_lease||entry.binding_lease===request.binding_lease));
}
const sameRequest=(a,b)=>a.operation===b.operation&&sameNameBinding(requestBinding(a),requestBinding(b))&&a.name===b.name&&sameRevision(a.expected_revision,b.expected_revision);

// Cosmetic requests stay local, exact and CAS-bound. Unknown intents retain
// their local source/owner; no open, read, reconnect or refresh replays them.
export function createNamingConsole({ui,call,connected=()=>false,local=()=>false,source=()=>'',owner=()=>'',context=()=>null,targets=()=>[],onChanged=()=>{},storage}) {
 const root=ui.stack(),feedback=ui.text(''),selection=ui.select('Resource to name',[],{},()=>select()),name=ui.field('Custom name',{maxLength:128}),reserved=ui.select('Exact custom reservation',[],{},()=>updateButtons());
 const manual=ui.check('Use an exact reference from a registered node',{checked:false},()=>updateButtons());
 const manualHost=ui.select('Registered target host',[],{},()=>updateButtons()),manualKind=ui.select('Reference kind',[{value:'lease',label:'Lease'},{value:'pool',label:'Pool'},{value:'resource',label:'Resource'}],{},()=>updateButtons());
 const manualID=ui.field('Exact resource ID',{maxLength:256},()=>updateButtons()),manualBinding=ui.field('Reservation lease ID (optional assertion)',{maxLength:32},()=>updateButtons()),referenceNote=ui.text('');
 const save=ui.button('Set custom name',()=>mutate('set')),clear=ui.button('Clear exact reservation',()=>mutate('clear')),read=ui.button('Read current names',()=>load()),close=ui.button('Close',()=>dialog.close());
 root.replace(ui.text('Names change local presentation. Resource IDs, permissions and existing actions remain unchanged.'),selection,manual,manualHost,manualKind,manualID,manualBinding,referenceNote,name,reserved,feedback,read,save,clear);
 const dialog=ui.dialog('Resource names',[root],[close]);
 for(const body of [root,dialog.body])if(body)ui.put?.(body.iri,'http://www.w3.org/1999/02/22-rdf-syntax-ns#type','https://arm.local/ui#NamingBody',true);
 let snapshot=null,current=null,inventory=new Map(),generation=0,openedSource='',openedOwner='',openedContext=null,busy=false,disposed=false,unknowns=[],pendingLoaded=false,pendingError=false,storageUnavailable=!storage;
 const storageKey=()=>`arm-custom-name-unknown:${openedSource}:${openedOwner}`;
 function allowed(){return !disposed&&local()&&connected()&&source()===openedSource&&context()===openedContext&&(!owner()||owner()===openedOwner);}
 function validSnapshot(value){return validNameSnapshot(value,openedSource)&&(!openedOwner||value.owner===openedOwner);}
 function persist(key=storageKey()){try{if(unknowns.length)storage?.setItem(key,stringifyJSON(unknowns));else storage?.removeItem(key);}catch{storageUnavailable=true;}}
 function restore(){
  if(pendingLoaded||!openedOwner)return;pendingLoaded=true;
  try{const raw=storage?.getItem(storageKey());if(!raw)return;if(raw.length>32768)throw Error();const values=parseJSON(raw);if(!Array.isArray(values)||values.length>16)throw Error();
   for(const record of values){const q=record?.request;if(record.source!==openedSource||record.owner!==openedOwner||!q||!['set','clear'].includes(q.operation)||!validCustomName(q.name)||typeof q.id!=='string'||q.id.length>256||typeof q.binding_lease!=='string'||!['host','lease','pool','resource'].includes(q.kind)||!Number.isInteger(Number(q.expected_revision))||BigInt(q.expected_revision)<0n||BigInt(q.expected_revision)>9223372036854775807n)throw Error();}
   unknowns=values;
  }catch{pendingError=true;}
 }
 function pruneObserved(value){
  // A strictly newer monotonic map revision makes an older CAS permanently
  // inapplicable. This observes current state, never invents an audit receipt.
  unknowns=unknowns.filter(record=>record.source!==value.host||record.owner!==value.owner||BigInt(value.revision)<=BigInt(record.request.expected_revision));persist();
 }
 function pendingText(){
  if(pendingError)return 'Retained naming intent data could not be decoded. It was preserved; inspect the private session record before another change.';
  return unknowns.length?'Prior responses may be unknown. Exact local-writer intents are retained; no retry was sent. '+(storageUnavailable?'Private session storage is unavailable; copy these exact intents before closing. ':'')+stringifyJSON(unknowns):'';
 }
 function knownHosts(){const hosts=new Map(targets().filter(ref=>ref.kind==='host').map(ref=>[ref.id,ref.label||ref.id]));if(openedSource)hosts.set(openedSource,hosts.get(openedSource)||'Local writer');return hosts;}
 function chooseRef(){return inventory.get(selection.value)||null;}
 function manualRef(){
  const authority=manualHost.value,id=manualID.value,binding=manualBinding.value,kind=manualKind.value;
  if(!knownHosts().has(authority)||typeof id!=='string'||id.length>256||!['lease','pool','resource'].includes(kind))return null;
  const ref={kind,authority_host:authority,id,binding_lease:binding};
  if(kind==='lease')return /^[a-f0-9]{32}$/.test(id)&&binding===''?ref:null;
  if(kind==='pool'||kind==='resource'&&/^pool_[a-f0-9]{32}$/.test(id))return /^pool_[a-f0-9]{32}$/.test(id)&&binding===''?{...ref,kind:'pool'}:null;
  if(['cpu','memory','disk'].some(type=>id===authority+'/'+type))return binding===''?ref:null;
  const printable=[...id].every(c=>c.charCodeAt(0)>=33&&c.charCodeAt(0)<=126),parts=id.slice((authority+'/ports/').length).split('/');
  return id.startsWith(authority+'/ports/')&&printable&&parts.length===3&&parts[0]&&parts[2]&&['tcp4','tcp6','udp4','udp6'].includes(parts[1])&&(binding===''||/^[a-f0-9]{32}$/.test(binding))?ref:null;
 }
 function reservations(){return (snapshot?.entries??[]).filter(entry=>sameNameBinding(entry,current));}
 function select(){current=chooseRef();const entries=reservations();reserved.set({options:entries.map((entry,index)=>({value:String(index),label:`${entry.name}${entry.active?' · active':' · retained'}`})),value:entries.length?'0':''});name.value=entries.find(entry=>entry.active)?.name??'';updateButtons();}
 function updateButtons(){
  const can=allowed()&&!busy&&snapshot&&validSnapshot(snapshot)&&!pendingError&&unknowns.length<16,target=manual.checked?manualRef():current;
  save.disabled=!can||!target||!manual.checked&&!targets().some(ref=>sameNameBinding(ref,current));clear.disabled=!can||manual.checked||!reservations()[Number(reserved.value)];manual.disabled=!allowed()||busy;
  for(const field of [manualHost,manualKind,manualID,manualBinding]){field.disabled=!allowed()||busy||!manual.checked;field.hidden=!manual.checked;}
  referenceNote.set({label:manual.checked?'The local writer stores this label only after its own fresh authorized node read. This form does not grant access or change destination metadata.':''});
  read.disabled=!allowed()||busy;close.disabled=busy;selection.disabled=reserved.disabled=name.disabled=busy||!allowed();selection.hidden=reserved.hidden=manual.checked;
 }
 async function load(){
  if(!allowed()||busy)return;const request=++generation,host=openedSource,connection=openedContext;busy=true;updateButtons();
  try{const result=await call('name_list',{all:true});if(disposed||request!==generation||source()!==host||context()!==connection||!local()||!allowed())return;
   if(!validSnapshot(result))throw Error('A healthy naming snapshot for this exact local source and owner is required.');
   if(!openedOwner)openedOwner=result.owner;restore();snapshot=result;if(!pendingError)pruneObserved(result);inventory=new Map(targets().map(ref=>[nameBindingKey(ref),ref]));
   for(const entry of result.entries){const key=nameBindingKey(entry);if(!inventory.has(key))inventory.set(key,{...entry,label:entry.id+' · retained reference'});}
   const previous=selection.value;selection.set({options:[...inventory].map(([key,ref])=>({value:key,label:`${ref.kind}: ${ref.label} · ${ref.id}`})),value:inventory.has(previous)?previous:[...inventory.keys()][0]??''});
   const hosts=knownHosts();manualHost.set({options:[...hosts].map(([id,label])=>({value:id,label:`${label} · ${id}`})),value:hosts.has(manualHost.value)?manualHost.value:openedSource});select();feedback.set({label:pendingText()||`Naming map revision ${result.revision}. Generated names are also reserved by the backend.`});
  }catch(error){if(!disposed&&request===generation){snapshot=null;feedback.set({label:error.message});}}
  finally{if(!disposed&&request===generation){busy=false;updateButtons();}}
 }
 async function mutate(operation){
  if(!allowed()||busy||!snapshot||!validSnapshot(snapshot)||pendingError||unknowns.length>=16)return;
  const ref=manual.checked?manualRef():chooseRef();if(!ref){feedback.set({label:'Choose a loaded registered host and provide an exact canonical ID and optional reservation lease ID.'});return;}
  let value=name.value;
  if(operation==='clear'){if(manual.checked)return;const exact=reservations()[Number(reserved.value)];if(!exact)return;value=exact.name;}
  else if(!manual.checked&&!targets().some(entry=>sameNameBinding(entry,ref))){feedback.set({label:'The selected binding is no longer in the loaded authorized view. Read it again.'});return;}
  else if(!validCustomName(value)){feedback.set({label:'Use 1–128 ASCII letters, digits, dot, underscore or hyphen, starting with a letter or digit. IDs and localhost are reserved.'});return;}
  const request={operation,kind:ref.kind,authority_host:ref.authority_host,id:ref.id,binding_lease:ref.binding_lease,name:value,expected_revision:snapshot.revision};
  const record={source:openedSource,owner:openedOwner,request},requestGeneration=++generation,host=openedSource,connection=openedContext,key=storageKey();
  if(!unknowns.some(old=>sameRequest(old.request,request)))unknowns.push(record);persist(key);busy=true;feedback.set({label:'Saving exact custom-name binding…'});updateButtons();
  try{const result=await call('name_mutate',request);if(!validSnapshot(result)||result.owner!==record.owner||!resultMatches(request,result))throw Error('The naming response did not establish this exact source, owner and requested map state.');
   unknowns=unknowns.filter(old=>old!==record);persist(key);if(disposed||requestGeneration!==generation||source()!==host||context()!==connection||!local()||!allowed())return;
   snapshot=result;pruneObserved(result);select();feedback.set({label:pendingText()||`Name ${operation==='clear'?'cleared':'saved'} at revision ${result.revision}.`});onChanged();
  }catch(error){if(disposed||requestGeneration!==generation)return;snapshot=null;
   if(error.definitive){unknowns=unknowns.filter(old=>old!==record);persist(key);feedback.set({label:`${error.message}. Read current names before another change. ${pendingText()}`});}
   else feedback.set({label:pendingText()});
  }finally{if(!disposed&&requestGeneration===generation){busy=false;updateButtons();}}
 }
 return {dialog,async open(){if(disposed||busy||!local()||!connected())return false;openedSource=source();openedOwner=owner();openedContext=context();manual.checked=false;if(!openedSource)return false;generation++;snapshot=null;busy=false;unknowns=[];pendingLoaded=false;pendingError=false;restore();dialog.showModal();await load();return true;},updateConnection(){updateButtons();},dispose(){disposed=true;generation++;dialog.dispose();},get pending(){return unknowns.at(-1)?.request??null;},get pendingRecords(){return unknowns.map(record=>({...record,request:{...record.request}}));}};
}
