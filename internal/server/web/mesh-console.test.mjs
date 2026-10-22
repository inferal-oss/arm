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
function fixture(saved=[],savedResumes=[]){
 const doc={activeElement:null,createElement:tag=>new Element(tag,doc)};doc.body=doc.createElement('body');const root=doc.createElement('section');doc.body.append(root);let live=true;const calls=[],pending=new Map([['arm-mesh-pending',JSON.stringify(saved)],['arm-mesh-resume-pending',JSON.stringify(savedResumes)]]);let held=false;
 const op={key:'key',kind:'connect',revision:2,phase:'awaiting_review',worker_live:true,review_revision:2,review_sha256:'a'.repeat(64),review_expires_at:'2030-01-01',review:{operation:'connect-direct-peers',local_prepared:{identity:{host:'a'.repeat(32),name:'source'}},remote_prepared:{identity:{host:'b'.repeat(32),name:'target'}},request:{source_permissions_on_target:{permissions:['observe-host']},target_permissions_on_source:{permissions:[]},reason:'test'}}};
 const ui=createMeshConsole({root,document:doc,connected:()=>live,storage:{getItem:k=>pending.get(k),setItem:(k,v)=>pending.set(k,v)},call:async(operation,q)=>{calls.push([operation,q]);if(operation==='node_list')return{nodes:[]};if(operation==='mesh_operator_list')return[];if(operation==='mesh_operator_connectors')return[];if(operation==='mesh_operator_prepare'){op.key=q.key;if(held)throw Error('connection lost');return op;}if(operation==='mesh_operator_get')return op;if(operation==='mesh_operator_decide')return{...op,phase:'rejected',revision:3,worker_live:false};throw Error(operation);}});
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
