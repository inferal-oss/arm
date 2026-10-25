import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createMeshConsole} from './mesh-console.mjs';
class Element {
  constructor(tag, doc) {
    this.tagName = tag;
    this.ownerDocument = doc;
    this.children = [];
    this.parentNode = null;
    this.listeners = {};
    this.attributes = {};
    this.dataset = {};
    this._text = '';
    this.value = '';
    if(tag==='textarea')Object.defineProperty(this,'type',{get:()=> 'textarea'});
    this.hidden = false;
    this.disabled = false;
  }
  append(...nodes) {
    for (const node of nodes) {
      node.remove();
      node.parentNode = this;
      this.children.push(node);
    }
  }
  remove() {
    if (this.parentNode) {
      this.parentNode.children = this.parentNode.children.filter(
        (n) => n !== this
      );
      this.parentNode = null;
    }
  }
  replaceChildren(...nodes) {
    for (const old of this.children) old.parentNode = null;
    this.children = [];
    this._text = '';
    this.append(...nodes);
  }
  set textContent(text) {
    this.replaceChildren();
    this._text = String(text);
  }
  get textContent() {
    return this._text + this.children.map((n) => n.textContent).join('');
  }
  setAttribute(k, v) {
    this.attributes[k] = String(v);
  }
  getAttribute(k) { return this.attributes[k] ?? null; }
  showModal() { this.open = true; }
  close() { this.open = false; for(const fn of this.listeners.close??[])fn(); }
  get isConnected() { return this.ownerDocument.body?.contains(this)??false; }
  addEventListener(k, f) {
    (this.listeners[k] ||= []).push(f);
  }
  async fire(k, event = { preventDefault() {} }) {
    for (const f of this.listeners[k] || []) await f(event);
  }
  all() {
    return this.children.flatMap((n) => [n, ...n.all()]);
  }
  contains(n) {
    return this === n || this.all().includes(n);
  }
  querySelectorAll(selector) {
    return this.all().filter((n) =>
      selector.startsWith('[data-')
        ? Object.prototype.hasOwnProperty.call(
            n.dataset,
            selector
              .slice(6, -1)
              .replace(/-([a-z])/g, (_, c) => c.toUpperCase())
          )
        : selector.startsWith('.')
          ? n.className?.split(' ').includes(selector.slice(1))
          : n.tagName === selector
    );
  }
  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }
  focus() {
    this.ownerDocument.activeElement = this;
  }
  scrollIntoView() {}
}


const flush=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(saved=[],savedResumes=[],entries=[],blockers=[]){
 const doc={activeElement:null,createElement:tag=>new Element(tag,doc)};doc.body=doc.createElement('body');const root=doc.createElement('section');doc.body.append(root);let live=true;const calls=[],pending=new Map([['arm-mesh-pending',JSON.stringify(saved)],['arm-mesh-resume-pending',JSON.stringify(savedResumes)]]);let held=false;
 const op={key:'key',kind:'connect',revision:2,phase:'awaiting_review',worker_live:true,review_revision:2,review_sha256:'a'.repeat(64),review_expires_at:'2030-01-01',review:{operation:'connect-direct-peers',local_prepared:{identity:{host:'a'.repeat(32),name:'source'}},remote_prepared:{identity:{host:'b'.repeat(32),name:'target'}},request:{source_permissions_on_target:{permissions:['observe-host']},target_permissions_on_source:{permissions:[]},reason:'test'}}};
 const ui=createMeshConsole({root,document:doc,connected:()=>live,storage:{getItem:k=>pending.get(k),setItem:(k,v)=>pending.set(k,v)},call:async(operation,q)=>{calls.push([operation,q]);if(operation==='mesh_operator_reconnect_context')return{host:q.host,blockers};if(operation==='node_list')return{nodes:entries};if(operation==='node_inspect')return entries.find(e=>e.host===q.host);if(operation==='mesh_operator_list')return[];if(operation==='mesh_operator_connectors')return[];if(operation==='mesh_operator_prepare'){op.key=q.key;if(held)throw Error('connection lost');return op;}if(operation==='mesh_operator_get')return op;if(operation==='mesh_operator_decide')return{...op,phase:'rejected',revision:3,worker_live:false};throw Error(operation);}});
 const find=text=>doc.body.all().find(n=>n.tagName==='button'&&n.textContent===text),dialog=()=>doc.body.all().find(n=>n.tagName==='dialog'&&n.open),form=()=>dialog().all().find(n=>n.tagName==='form');
 return{doc,root,ui,calls,pending,find,dialog,form,op,advance:()=>op.revision++,hold:()=>held=true,offline:()=>{live=false;ui.updateConnection();}};
}
test('connect editor renders real textarea safely; defaults and decline submit only exact reviewed decision',async()=>{
 const f=fixture();await f.find('Connect a node…').fire('click');await flush();assert.equal(f.doc.activeElement.textContent,'Cancel');
 const form=f.form(),inputs=form.all().filter(n=>n.tagName==='input'),reason=form.all().find(n=>n.tagName==='textarea');inputs[0].value='fixture';reason.value='review only';await form.fire('submit');
 const mutation=f.calls.find(([op])=>op==='mesh_operator_prepare')[1];assert.deepEqual(mutation.connect.source_permissions_on_target.permissions,['observe-host']);assert.deepEqual(mutation.connect.target_permissions_on_source.permissions,[]);
 assert.equal(f.doc.activeElement.textContent,'Decline this review');await f.find('Decline this review').fire('click');assert.equal(f.calls.filter(([op])=>op==='mesh_operator_decide').length,1);assert.equal(f.calls.find(([op])=>op==='mesh_operator_decide')[1].approve,false);
});
test('unknown prepare retains exact operation key and cannot resubmit on reconnect or close',async()=>{
 const f=fixture();f.hold();await f.find('Connect a node…').fire('click');await flush();const form=f.form();form.all().find(n=>n.tagName==='input').value='fixture';form.all().find(n=>n.tagName==='textarea').value='retained';await form.fire('submit');
 const key=f.calls.find(([op])=>op==='mesh_operator_prepare')[1].key;assert.deepEqual(JSON.parse(f.pending.get('arm-mesh-pending')),[key]);assert.equal(f.find('Prepare connection review').disabled,true);f.offline();f.dialog().close();await f.ui.refresh();assert.equal(f.calls.filter(([op])=>op==='mesh_operator_prepare').length,1);
});

test('heartbeat refresh preserves review controls, expansion and default-no focus; offline confirmation cannot send',async()=>{
 const f=fixture();await f.find('Connect a node…').fire('click');await flush();const form=f.form();form.all().find(n=>n.tagName==='input').value='fixture';form.all().find(n=>n.tagName==='textarea').value='focus retention';await form.fire('submit');
 const decline=f.find('Decline this review'),approve=f.find('Trust and connect');decline.focus();f.advance();await f.ui.refresh();assert.equal(f.find('Decline this review'),decline);assert.equal(f.doc.activeElement,decline);
 f.offline();assert.equal(approve.disabled,true);await approve.fire('click');assert.equal(f.calls.filter(([op])=>op==='mesh_operator_decide').length,0);
});

test('retained uncertain operations remain visible offline and a full cache cannot be overwritten by another prepare',async()=>{
 const keys=Array.from({length:16},(_,i)=>'uncertain-'+i),f=fixture(keys);for(const key of keys)assert.ok(f.find('Look up uncertain operation · '+key));await f.find('Connect a node…').fire('click');await flush();const form=f.form();form.all().find(n=>n.tagName==='input').value='fixture';form.all().find(n=>n.tagName==='textarea').value='do not overwrite';await form.fire('submit');assert.equal(f.calls.filter(([op])=>op==='mesh_operator_prepare').length,0);assert.deepEqual(JSON.parse(f.pending.get('arm-mesh-pending')),keys);
});

test('recovery trust review renders exact binds and directional permissions without relying on raw JSON',async()=>{
 const f=fixture();f.op.review={operation:'verify-existing-connection',local:{identity:{host:'a'.repeat(32),name:'source',address:'10.0.0.1:1'}},remote:{identity:{host:'b'.repeat(32),name:'target',address:'10.0.0.2:2'}},local_listen:'10.0.0.1:1',remote_listen:'10.0.0.2:2',source_permissions_on_target:{permissions:['observe-host','observe-own','lease-own'],limits:{cpu:2,memory_bytes:1073741824,disk_bytes:0},max_ttl_ns:600e9},target_permissions_on_source:{permissions:[]}};
 await f.find('Connect a node…').fire('click');await flush();const form=f.form();form.all().find(n=>n.tagName==='input').value='fixture';form.all().find(n=>n.tagName==='textarea').value='recovery';await form.fire('submit');
 const body=f.dialog().querySelector('.mesh-dialog-body'),facts=body.all().filter(n=>n.className==='mesh-fact');assert.equal(facts.filter(n=>n.textContent.startsWith('Exact listener bind')).length,2);assert.ok(body.all().some(n=>n.tagName==='h4'&&n.textContent==='This node → destination'));assert.match(body.textContent,/2 CPU/);assert.match(body.textContent,/No access/);assert.equal(f.doc.activeElement.textContent,'Decline this review');
});

test('disconnect confirmation renders real peer owner, pins, SSH identity and exact revocation scope',async()=>{
 const f=fixture();f.op.kind='disconnect';f.op.review={operation:'disconnect-direct-peer',host:{host:'b'.repeat(32),name:'destination',owner:'501',owner_username:'tester',address:'10.0.0.2:1234',fingerprint:'c'.repeat(64),spki_sha256:'d'.repeat(64),expires_at:'2030-01-01T00:00:00Z'},pair:'e'.repeat(64),local_revision:7,remote_revision:9,ssh_account:'tester',ssh_host_key:'SHA256:verified-fixture-key',reason:'Retire this private enrollment',effect:'Existing leases retain expiry.'};
 await f.find('Connect a node…').fire('click');await flush();const form=f.form();form.all().find(n=>n.tagName==='input').value='fixture';form.all().find(n=>n.tagName==='textarea').value='review';await form.fire('submit');
 const body=f.dialog().querySelector('.mesh-dialog-body');assert.match(body.textContent,/tester \(UID 501\)/);assert.doesNotMatch(body.textContent,/undefined/);for(const fact of ['c'.repeat(64),'d'.repeat(64),'e'.repeat(64),'Retire this private enrollment','SHA256:verified-fixture-key','Local enrollment revision7','Destination enrollment revision9'])assert.ok(body.textContent.includes(fact),fact);assert.equal(f.doc.activeElement.textContent,'Decline this review');assert.ok(f.find('Confirm disconnect'));
});


test('uncertain Resume is retained across reload and cannot create another child by blind resend',async()=>{
 const f=fixture();f.op.phase='incomplete';f.op.worker_live=false;
 await f.find('Connect a node…').fire('click');await flush();const form=f.form();form.all().find(n=>n.tagName==='input').value='fixture';form.all().find(n=>n.tagName==='textarea').value='recovery';await form.fire('submit');
 await f.find('Prepare recovery review…').fire('click');const key=f.op.key;
 assert.equal(f.calls.filter(([op])=>op==='mesh_operator_resume').length,1);assert.deepEqual(JSON.parse(f.pending.get('arm-mesh-resume-pending')),[key]);assert.equal(f.find('Prepare recovery review…').disabled,true);
 const restored=fixture([key],[key]);restored.op.key=key;restored.op.phase='incomplete';restored.op.worker_live=false;await restored.find('Look up uncertain operation · '+key).fire('click');assert.equal(restored.find('Prepare recovery review…'),undefined);assert.match(restored.dialog().textContent,/cannot be resent here/);assert.equal(restored.calls.filter(([op])=>op==='mesh_operator_resume').length,0);
});


test('unknown install review is readable but cannot acquire a misleading trust/disconnect approval',async()=>{
 const f=fixture();f.op.kind='provision';f.op.review={operation:'install-arm',effect:'Install a binary'};
 await f.find('Connect a node…').fire('click');await flush();const form=f.form();form.all().find(n=>n.tagName==='input').value='fixture';form.all().find(n=>n.tagName==='textarea').value='unsupported review';await form.fire('submit');
 assert.equal(f.find('Trust and connect'),undefined);assert.equal(f.find('Confirm disconnect'),undefined);assert.match(f.dialog().textContent,/no approval is available here/);
});

test('revoked-node reconnect uses exact host with new defaults and no native path fields',async()=>{
 const entry={host:'b'.repeat(32),name:'target',health:'disconnected',trust:{state:'revoked',pair:'c'.repeat(64),inbound_grant:{permissions:['run']}},outbound_scope:{grant:{permissions:['run']}}};
 const f=fixture([],[],[entry]);await f.ui.refresh();await f.find('Reconnect…').fire('click');await flush();
 assert.equal(f.doc.activeElement.textContent,'Cancel');const form=f.form();const locator=form.all().find(n=>n.tagName==='input');assert.equal(locator.value,'');assert.equal(locator.required,false);
 form.all().find(n=>n.tagName==='textarea').value='fresh least privilege';await form.fire('submit');
 const q=f.calls.find(([name])=>name==='mesh_operator_prepare')[1];assert.equal(q.kind,'reconnect');assert.equal(q.reconnect.host,entry.host);assert.equal(q.connect,undefined);assert.equal(q.reconnect.remote_binary,undefined);assert.equal(q.reconnect.remote_home,undefined);assert.deepEqual(q.reconnect.source_permissions_on_target.permissions,['observe-host']);assert.deepEqual(q.reconnect.target_permissions_on_source.permissions,[]);
 assert.equal(f.calls.filter(([name])=>name==='mesh_operator_prepare').length,1);
});

test('reconnect blocker links exact pending recovery and guards even a synthetic submit',async()=>{
 const entry={host:'b'.repeat(32),name:'target',health:'disconnected',trust:{state:'revoked'}};
 const pending={key:'old-disconnect',kind:'disconnect',phase:'incomplete',worker_live:false};
 const f=fixture([],[],[entry],[pending]);await f.ui.refresh();await f.find('Reconnect…').fire('click');await flush();
 assert.equal(f.find('Prepare reconnect review').disabled,true);
 f.form().all().find(n=>n.tagName==='textarea').value='not yet';await f.form().fire('submit');
 assert.equal(f.calls.filter(([name])=>name==='mesh_operator_prepare').length,0);
 assert.match(f.dialog().textContent,/old obligations remain/i);
 await f.find('Inspect pending operation old-disconnect').fire('click');
 assert.deepEqual(f.calls.find(([name])=>name==='mesh_operator_get')[1],{key:'old-disconnect'});
});

async function exactHandoffFixture(){
 const f=fixture(['key']);Object.assign(f.op,{kind:'disconnect',phase:'incomplete',revision:8,worker_live:false,recovery_sha256:'b'.repeat(64),recovery_effect:'Workflow exclusion only; old obligations remain.'});
 await f.find('Look up uncertain operation · key').fire('click');
 await f.find('Hand off unresolved operation…').fire('click');return f;
}
test('handoff review exposes exact operation revision and full recovery fingerprint while keeping default no',async()=>{
 const f=await exactHandoffFixture(),body=f.dialog().querySelector('.mesh-dialog-body');
 assert.ok(body.all().some(n=>n.className==='mesh-fact'&&n.textContent==='Operation revision8'));
 const disclosure=body.all().find(n=>n.tagName==='details'&&n.all().some(c=>c.tagName==='summary'&&c.textContent==='Handoff review fingerprint · SHA-256'));
 assert.ok(disclosure,'human-labelled recovery fingerprint disclosure required');
 assert.notEqual(disclosure.open,true,'technical digest stays behind optional disclosure');
 const full=disclosure.all().find(n=>n.tagName==='code');assert.equal(full.textContent,'b'.repeat(64));
 assert.notEqual(full.textContent,f.op.review_sha256,'handoff uses recovery snapshot fingerprint, not original trust review');
 assert.equal(full.getAttribute('aria-label'),'Full handoff review SHA-256 fingerprint');
 assert.equal(f.doc.activeElement.textContent,'Keep operation protected');
 const before=JSON.stringify(f.op);await f.find('Keep operation protected').fire('click');
 assert.equal(f.dialog(),undefined);assert.equal(JSON.stringify(f.op),before);
 assert.equal(f.calls.filter(([name])=>name==='mesh_operator_handoff').length,0);
});
test('handoff review sends unchanged exact key revision recovery digest and reason only after explicit submit',async()=>{
 const f=await exactHandoffFixture();await f.form().fire('submit');
 assert.equal(f.calls.filter(([name])=>name==='mesh_operator_handoff').length,0,'blank reason sends nothing');
 f.form().all().find(n=>n.tagName==='textarea').value='Preserve exact old obligations for manual recovery';
 await f.form().fire('submit');
 assert.deepEqual(f.calls.filter(([name])=>name==='mesh_operator_handoff'),[['mesh_operator_handoff',{key:'key',revision:8,review_sha256:'b'.repeat(64),reason:'Preserve exact old obligations for manual recovery'}]]);
});

function stopConsoleFixture(options={}){
 const doc={activeElement:null,createElement:tag=>new Element(tag,doc)};doc.body=doc.createElement('body');const root=doc.createElement('section');doc.body.append(root);
 const now=Date.now(),host='b'.repeat(32),calls=[],saved=options.saved??new Map();
 const node={host,name:'Build node',local:false,health:'connected',trust:{state:'active'},scope_freshness:{known:true,stale:false},outbound_scope:{target_host:host,grant:{permissions:['observe-host','node-stop']},node_stop:{supported:true,reason:'Usable fixed completion check'}}};
 const plan={schema:'arm.node-stop.plan/v1',key:'stop-ui',target:{host,owner:'501',home:'/home/operator/arm',backend_generation:'c'.repeat(32),certificate_sha256:'d'.repeat(64),spki_sha256:'e'.repeat(64),directory:{owner:501,inode:42,device:7},startup_contract:'arm-desired-state-gated-v1'},actor:{source_host:'a'.repeat(32),pair:'f'.repeat(64),grant_revision:7,certificate_sha256:'1'.repeat(64),spki_sha256:'2'.repeat(64)},desired_state_revision:9,policy:{force:false,wait:false},reason:'Finish maintenance',impact:{complete:true,scope_sha256:'3'.repeat(64),outstanding:1,obligations:[{id:'compile-lease',kind:'lease',state:'active',revision:4}],blockers:[],affected_peers:['a'.repeat(32)]},observed_at:new Date(now).toISOString(),expires_at:new Date(now+120000).toISOString(),supervisor_scope:'The host stays running. External service-manager ownership is unverified.'};
 const op=options.op??{key:'stop-ui',kind:'stop',targets:['host:'+host],revision:3,phase:'awaiting_review',worker_live:true,approved:false,lookup_only:false,can_prepare_fresh:false,review_revision:2,review_sha256:'a'.repeat(64),review_expires_at:plan.expires_at,review:{operation:'stop-arm-node',key:'stop-ui',review_sha256:'a'.repeat(64),owner_username:'operator',history_used:4,history_capacity:128,completion_check:{available:true,detail:'Checks only the original ARM backend.'},plan}};
 let known=Boolean(options.op),live=true,hook=null;
 const ui=createMeshConsole({root,document:doc,connected:()=>live,storage:{getItem:k=>saved.get(k),setItem:(k,v)=>{if(options.storageFailure)throw Error('storage unavailable');saved.set(k,v);}},call:async(name,q)=>{
  calls.push([name,q]);if(hook){const answer=hook(name,q);if(answer!==undefined)return await answer;}
  if(name==='node_list')return{nodes:[node]};if(name==='node_inspect')return node;if(name==='mesh_operator_list')return known?[op]:[];
  if(name==='mesh_operator_prepare'){known=true;op.key=q.key;op.review.key=q.key;op.review.plan.key=q.key;op.review.plan.policy=q.stop.policy;op.review.plan.reason=q.stop.reason;if(options.losePrepare)throw Error('prepare reply lost');return op;}
  if(name==='mesh_operator_get')return op;
  if(name==='mesh_operator_decide'){if(options.loseDecision)throw Error('decision reply lost');Object.assign(op,{phase:'rejected',worker_live:false,approved:false,can_prepare_fresh:true});return op;}
  if(name==='mesh_operator_stop_lookup')return op;
  throw Error(name);
 }});
 const find=text=>doc.body.all().find(n=>n.tagName==='button'&&n.textContent===text),dialog=()=>doc.body.all().find(n=>n.tagName==='dialog'&&n.open),form=()=>dialog().all().find(n=>n.tagName==='form');
 const open=async()=>{await ui.refresh();await find('View node').fire('click');};
 const prepare=async()=>{await open();await find('Stop ARM…').fire('click');const mode=form().all().find(n=>n.tagName==='select');mode.value='wait';await mode.fire('change');form().all().find(n=>n.tagName==='textarea').value='Finish maintenance';await form().fire('submit');};
 return{doc,root,ui,node,op,calls,saved,find,dialog,form,open,prepare,route:fn=>{hook=fn;},offline:()=>{live=false;ui.updateConnection();}};
}

test('Stop entry explains unsupported backend without offering a working action',async()=>{
 const f=stopConsoleFixture();f.node.outbound_scope.node_stop={supported:false,reason:'Current Serve cannot confirm a backend stop.'};await f.open();const action=f.find('Stop ARM…');assert.equal(action.disabled,true);assert.match(f.dialog().textContent,/cannot confirm a backend stop/);await action.fire('click');assert.equal(f.form(),undefined);assert.equal(f.calls.filter(([name])=>name==='mesh_operator_prepare').length,0);
});
test('Stop editor defaults to refusal and only sends exact selected host policy reason after explicit preparation',async()=>{
 const f=stopConsoleFixture();await f.open();await f.find('Stop ARM…').fire('click');assert.equal(f.doc.activeElement.textContent,'Keep running');const form=f.form(),mode=form.all().find(n=>n.tagName==='select');assert.equal(mode.value,'refuse');await form.fire('submit');assert.equal(f.calls.filter(([name])=>name==='mesh_operator_prepare').length,0);form.all().find(n=>n.tagName==='textarea').value='Finish maintenance';mode.value='wait';await mode.fire('change');await form.fire('submit');const q=f.calls.find(([name])=>name==='mesh_operator_prepare')[1];assert.deepEqual(q.stop,{host:f.node.host,reason:'Finish maintenance',policy:{force:false,wait:true}});assert.equal(q.kind,'stop');assert.equal(f.doc.activeElement.textContent,'Keep running');assert.ok(f.find('Stop this ARM backend'));assert.match(f.dialog().textContent,/operator \(UID 501\)/);assert.match(f.dialog().textContent,/compile-lease/);assert.ok(f.dialog().all().some(n=>n.tagName==='table'));assert.ok(f.dialog().all().some(n=>n.className==='json-view'));assert.equal(f.dialog().all().some(n=>n.tagName==='pre'),false);
 await f.find('Keep running').fire('click');assert.deepEqual(f.calls.filter(([name])=>name==='mesh_operator_decide').map(([,q])=>q.approve),[false]);assert.equal(f.calls.filter(([name])=>name==='mesh_operator_stop_lookup').length,0);
});
test('Stop decision uncertainty survives reload and a transient awaiting review cannot re-enable confirmation',async()=>{
 const f=stopConsoleFixture({loseDecision:true});await f.prepare();await f.find('Stop this ARM backend').fire('click');assert.equal(f.calls.filter(([name])=>name==='mesh_operator_decide').length,1);assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-stop-decisions')),[f.op.key]);
 const restored=stopConsoleFixture({saved:f.saved,op:f.op});await restored.ui.refresh();const operation=restored.root.all().find(n=>n.tagName==='button'&&n.className==='mesh-operation-row');await operation.fire('click');assert.equal(restored.find('Stop this ARM backend'),undefined);assert.match(restored.dialog().textContent,/decision may already be recorded/);assert.equal(restored.find('Prepare recovery review…'),undefined);assert.equal(restored.find('Hand off unresolved operation…'),undefined);await restored.ui.refresh();assert.equal(restored.calls.filter(([name])=>name==='mesh_operator_decide'||name==='mesh_operator_stop_lookup').length,0);
 Object.assign(restored.op,{phase:'rejected',worker_live:false,can_prepare_fresh:true});await restored.find('Refresh recorded receipt').fire('click');assert.ok(restored.find('Prepare a fresh stop review…'));assert.deepEqual(JSON.parse(restored.saved.get('arm-mesh-stop-decisions')),[]);
});
test('Lost Stop preparation exposes exact key lookup without resending or approving on reload',async()=>{
 const f=stopConsoleFixture({losePrepare:true});await f.prepare();assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-pending')),[f.op.key]);assert.equal(f.find('Prepare stop review').disabled,true);f.dialog().close();const restored=stopConsoleFixture({saved:f.saved,op:f.op});await restored.find('Look up uncertain operation · '+f.op.key).fire('click');assert.equal(restored.calls.filter(([name])=>name==='mesh_operator_prepare'||name==='mesh_operator_decide').length,0);assert.ok(restored.find('Stop this ARM backend'));assert.equal(restored.doc.activeElement.textContent,'Keep running');
});
test('Checking original Stop is explicit, revision-bound and late lookup cannot replace another dialog',async()=>{
 const f=stopConsoleFixture();await f.prepare();Object.assign(f.op,{phase:'incomplete',approved:true,lookup_only:true,worker_live:false});await f.ui.refresh();assert.ok(f.find('Check original stop'));assert.equal(f.calls.filter(([name])=>name==='mesh_operator_stop_lookup').length,0);let resolve;f.route((name)=>name==='mesh_operator_stop_lookup'?new Promise(done=>{resolve=done;}):undefined);const pending=f.find('Check original stop').fire('click');assert.deepEqual(f.calls.find(([name])=>name==='mesh_operator_stop_lookup')[1],{key:f.op.key,revision:f.op.revision});f.dialog().close();await f.find('View node').fire('click');const next=f.dialog();resolve(f.op);await pending;assert.equal(f.dialog(),next);assert.equal(f.calls.filter(([name])=>name==='mesh_operator_decide').length,0);
});
test('Stop preparation and decision refuse missing durable browser storage',async()=>{
 const f=stopConsoleFixture({storageFailure:true});await f.prepare();assert.equal(f.calls.filter(([name])=>name==='mesh_operator_prepare').length,0);assert.match(f.dialog().textContent,/storage unavailable/);
});

test('Outstanding work blocks default Stop approval while explicit Keep running remains available',async()=>{
 const f=stopConsoleFixture();await f.open();await f.find('Stop ARM…').fire('click');f.form().all().find(n=>n.tagName==='textarea').value='Default safe policy';await f.form().fire('submit');assert.equal(f.find('Stop this ARM backend'),undefined);assert.ok(f.find('Keep running'));assert.equal(f.doc.activeElement.textContent,'Keep running');assert.match(f.dialog().textContent,/Existing work prevents the default stop/);await f.find('Keep running').fire('click');assert.deepEqual(f.calls.filter(([name])=>name==='mesh_operator_decide').map(([,q])=>q.approve),[false]);
});

test('Stop operator text stays literal and complete records remain structured and collapsible',async()=>{
 const f=stopConsoleFixture();await f.prepare();f.op.review.owner_username='<img src=x onerror=alert(1)>';f.op.review.plan.reason='<script>unsafe</script>';f.op.review_sha256='b'.repeat(64);f.op.review_revision++;f.op.revision++;await f.ui.refresh();const body=f.dialog().querySelector('.mesh-dialog-body');assert.ok(body.textContent.includes('<img src=x onerror=alert(1)> (UID 501)'));assert.ok(body.textContent.includes('<script>unsafe</script>'));assert.equal(body.all().some(n=>n.tagName==='img'||n.tagName==='script'||n.tagName==='pre'),false);assert.ok(body.all().some(n=>n.className==='json-view'));assert.ok(body.all().some(n=>n.tagName==='details'));
});

test('Stop preparation cannot replace the selected host and leaves its uncertain exact key retained',async()=>{
 const f=stopConsoleFixture();f.route((name,q)=>{if(name!=='mesh_operator_prepare')return;const op=structuredClone(f.op);op.key=q.key;op.targets=['host:'+'e'.repeat(32)];op.review.key=q.key;op.review.plan.key=q.key;op.review.plan.target.host='e'.repeat(32);return op;});await f.prepare();const sent=f.calls.find(([name])=>name==='mesh_operator_prepare')[1];assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-pending')),[sent.key]);assert.equal(f.find('Stop this ARM backend'),undefined);assert.match(f.dialog().textContent,/does not match the exact requested key and host/);
});
test('Stop decision response with changed reviewed hash never clears uncertainty or rearms approval',async()=>{
 const f=stopConsoleFixture();await f.prepare();f.route(name=>name==='mesh_operator_decide'?{...f.op,review_sha256:'f'.repeat(64)}:undefined);await f.find('Stop this ARM backend').fire('click');assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-stop-decisions')),[f.op.key]);assert.match(f.dialog().textContent,/changed the exact reviewed revision or hash/);assert.equal(f.calls.filter(([name])=>name==='mesh_operator_decide').length,1);
});

test('A stale successful Stop response cannot rearm a decision that may already be in flight',async()=>{
 const f=stopConsoleFixture();await f.prepare();f.route(name=>name==='mesh_operator_decide'?{...f.op}:undefined);await f.find('Stop this ARM backend').fire('click');assert.deepEqual(JSON.parse(f.saved.get('arm-mesh-stop-decisions')),[f.op.key]);assert.match(f.dialog().textContent,/has not confirmed the decision transition/);await f.ui.refresh();assert.equal(f.find('Stop this ARM backend'),undefined);assert.equal(f.calls.filter(([name])=>name==='mesh_operator_decide').length,1);
});


test('A delayed older Stop receipt preserves the newer dialog content and focus before any modal replacement',async()=>{
 const f=stopConsoleFixture();await f.prepare();const old=structuredClone(f.op);let release;
 f.route(name=>{
  if(name==='mesh_operator_get')return new Promise(resolve=>{release=resolve;});
  if(name==='mesh_operator_decide')return {...f.op,revision:f.op.revision+1,phase:'incomplete',approved:true,lookup_only:true,can_prepare_fresh:false,worker_live:false,result:{operation:'stop-arm-node',key:f.op.key,status:'unknown',detail:'Newer recorded outcome; original authority stop still unconfirmed.'}};
 });
 const pending=f.find('Refresh recorded receipt').fire('click'),dialog=f.dialog();await f.find('Stop this ARM backend').fire('click');
 assert.equal(f.dialog(),dialog);assert.ok(dialog.textContent.includes('Newer recorded outcome'));const focused=f.doc.activeElement;
 release(old);await pending;
 assert.equal(f.dialog(),dialog);assert.equal(f.doc.activeElement,focused);assert.ok(dialog.textContent.includes('Newer recorded outcome'));assert.equal(f.calls.filter(([name])=>name==='mesh_operator_decide').length,1);assert.equal(f.calls.filter(([name])=>name==='mesh_operator_stop_lookup').length,0);
});

test('Stop obligation details show who and why without changing the review or Keep running default',async()=>{
 const f=stopConsoleFixture(),item=f.op.review.plan.impact.obligations[0];Object.assign(item,{id:'lease:compile',lease_details:'permitted',lease:{owner:'501',owner_username:'recorded-user',reason:'Compile useful binary <script>',truncated:true}});
 let sent;
 f.route((name,q)=>{if(name==='mesh_operator_stop_lease'){sent=q;return{...q,id:'compile',host:f.node.host,owner:'501',owner_username:'recorded-user',reason:'Full purpose <script> stays text',state:'active',revision:5,reviewed_revision:4,changed_since_review:true,resources:{cpu:2,memory_bytes:1024,disk_bytes:2048},created_at:new Date().toISOString()};}});
 await f.prepare();const dialog=f.dialog(),before=JSON.stringify(f.op.review),active=f.doc.activeElement;
 assert.match(dialog.textContent,/recorded-user \(UID 501\)/);assert.match(dialog.textContent,/Compile useful binary <script>/);assert.match(dialog.textContent,/Shortened preview/);
 await f.find('View lease details').fire('click');
 assert.deepEqual(sent,{key:f.op.key,review_revision:f.op.review_revision,review_sha256:f.op.review_sha256,obligation:'lease:compile'});
 assert.equal(f.dialog(),dialog);assert.equal(JSON.stringify(f.op.review),before);assert.equal(f.doc.activeElement,active);assert.equal(active.textContent,'Keep running');
 assert.match(dialog.textContent,/Full purpose <script> stays text/);assert.match(dialog.textContent,/changed since the Stop review/);assert.equal(dialog.all().some(n=>n.tagName==='script'),false);
 assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide'||n==='mesh_operator_stop_lookup').length,0);
 await f.find('Keep running').fire('click');assert.deepEqual(f.calls.filter(([n])=>n==='mesh_operator_decide').map(([,q])=>q.approve),[false]);
});
test('Stop lease detail redaction and stale responses never retarget or approve',async()=>{
 const redacted=stopConsoleFixture();await redacted.prepare();assert.equal(redacted.find('View lease details'),undefined);assert.match(redacted.dialog().textContent,/Details not shared by this destination/);assert.equal(redacted.calls.filter(([n])=>n==='mesh_operator_stop_lease').length,0);
 const f=stopConsoleFixture(),item=f.op.review.plan.impact.obligations[0];Object.assign(item,{id:'lease:compile',lease_details:'permitted',lease:{owner:'501',reason:'Build',truncated:false}});
 let resolve;f.route((n)=>n==='mesh_operator_stop_lease'?new Promise(done=>{resolve=done;}):undefined);await f.prepare();const old=f.dialog(),pending=f.find('View lease details').fire('click');old.close();await f.find('View node').fire('click');const next=f.dialog();resolve({key:f.op.key,review_revision:2,review_sha256:f.op.review_sha256,obligation:'lease:compile',id:'compile',host:f.node.host,reason:'late-private-detail'});await pending;
 assert.equal(f.dialog(),next);assert.equal(next.textContent.includes('late-private-detail'),false);assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide').length,0);
});
test('Stop lease permission failure remains an explanation inside the retained review',async()=>{
 const f=stopConsoleFixture(),item=f.op.review.plan.impact.obligations[0];Object.assign(item,{id:'lease:compile',lease_details:'permitted',lease:{owner:'501',reason:'Build',truncated:false}});
 f.route(n=>{if(n==='mesh_operator_stop_lease')throw Error('Destination revoked lease-read permission.');});await f.prepare();const before=JSON.stringify(f.op.review);await f.find('View lease details').fire('click');assert.match(f.dialog().textContent,/LEASE DETAILS UNAVAILABLE/);assert.match(f.dialog().textContent,/revoked lease-read permission/);assert.equal(JSON.stringify(f.op.review),before);assert.ok(f.find('Keep running'));assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide'||n==='mesh_operator_prepare').length,1);
});


test('Stop completion diagnostics are complete in an initially closed disclosure while Keep running retains focus',async()=>{
 const f=stopConsoleFixture(),detail='Native helper SHA-256 '+ 'a'.repeat(64)+'\nOriginal generation only; no process authority from a saved PID.';
 f.op.review.completion_check.detail=detail;
 await f.prepare();const original=JSON.stringify(f.op.review);
 const check=f.dialog().all().find(n=>n.tagName==='details'&&n.className==='mesh-stop-completion');
 assert.ok(check);assert.notEqual(check.open,true);assert.equal(check.children[0].tagName,'summary');assert.equal(check.children[0].textContent,'How ARM checks completion');
 const full=check.all().find(n=>n.className==='mesh-stop-completion-detail');assert.equal(full.tagName,'code');assert.equal(full.textContent,detail);
 assert.equal(f.doc.activeElement.textContent,'Keep running');assert.equal(JSON.stringify(f.op.review),original);assert.equal(f.calls.filter(([n])=>n==='mesh_operator_decide').length,0);
 const unavailable=stopConsoleFixture();unavailable.op.review.completion_check={available:false,detail:'Cannot inspect the original helper: '+ 'f'.repeat(64)};await unavailable.prepare();
 assert.match(unavailable.dialog().textContent,/Cannot inspect the original helper/);assert.equal(unavailable.dialog().all().some(n=>n.className==='mesh-stop-completion'),false);assert.equal(unavailable.find('Stop this ARM backend'),undefined);assert.ok(unavailable.find('Keep running'));
});
