import test from 'node:test';
import assert from 'node:assert/strict';
import {controlUI} from './control-test-ui.mjs';
import {createScreenViewer} from './screen.mjs';
import {createEphemeralInputs} from './ephemeral-inputs.mjs';

function nativeDocument(){
  const elements=[];
  class Element {
    constructor(tag){this.tag=tag;this.value='';this.children=[];this.attributes={};this.style={};this.ownerDocument=document;elements.push(this);}
    setAttribute(name,value){this.attributes[name]=value;}
    append(...children){this.children.push(...children);}
    replaceChildren(...children){this.children=children;}
    focus(){document.activeElement=this;}
    select(){this.selected=true;}
  }
  const document={activeElement:null,createElement:tag=>new Element(tag),elements};return document;
}
function fixture(t,{loadRFB,token='a'.repeat(64),clipboard={}}={}) {
  const ui=controlUI(),document=nativeDocument(),instances=[],widgets=new Map(),listeners=new Map();
  ui.island=(name,widget,props)=>{const h=ui.stack([],{...props});h.name=name;h.kind='Island';const slot=document.createElement('div'),handle=widget.mount(slot,{},()=>{throw Error('Private island must declare no output events');});widgets.set(name,{handle,slot});h.dispose=()=>{handle.destroy();widgets.delete(name);h.disposed=true;};return h;};
  ui.dialog=(label,children,actions)=>{const h=ui.card(label,[...children,...actions]);h.open=false;h.showModal=()=>{h.open=true;};h.close=()=>{if(h.open){h.open=false;for(const fn of h.events.close??[])fn();}};h.dispose=()=>{h.disposed=true;const walk=x=>{if(x.kind==='Island')x.dispose();else for(const child of x.children??[])walk(child);};for(const child of h.children)walk(child);};return h;};
  class RFB {
    constructor(surface,url,options){this.surface=surface;this.url=url;this.options=options;this.events={};this._rfbConnectionState='connecting';this._rfbAppleARD=false;this._sock={sQpushBytes:()=>{},flush:()=>{}};instances.push(this);}
    addEventListener(type,fn){(this.events[type]??=[]).push(fn);}
    emit(type,detail={}){if(type==='connect')this._rfbConnectionState='connected';for(const fn of this.events[type]??[])fn({detail});}
    _fail(reason){this.reason=reason;}
    _initMsg(){}
    sendCredentials(value){this.credentials=value;}
    clipboardPasteFrom(value){this.sentClipboard=value;}
    disconnect(){this.disconnected=true;this._rfbConnectionState='disconnected';}
    focus(){}
  }
  const window={addEventListener:(name,fn)=>listeners.set(name,fn),removeEventListener:name=>listeners.delete(name)};
  const viewer=createScreenViewer({ui,root:ui.stack(),token,context:{local:false},loadRFB:loadRFB??(async()=>RFB),location:{host:'127.0.0.1:12345'},navigator:{clipboard},window});
  t.after(()=>viewer.dispose());
  return {ui,document,instances,widgets,viewer,listeners,RFB};
}
const graphText=ui=>JSON.stringify(ui.all.map(({kind,label,value,checked,name})=>({kind,label,value,checked,name})));

test('ephemeral input island retains no values after take, clear or destroy',()=>{
  const document=nativeDocument(),slot=document.createElement('div'),inputs=createEphemeralInputs([{name:'password',label:'Mac password',type:'password',required:true,maxLength:256}]);
  const handle=inputs.widget.mount(slot);assert.throws(()=>inputs.take(),/Enter mac password/);document.elements.find(e=>e.type==='password').value='secret-password';assert.deepEqual(inputs.take(),{password:'secret-password'});assert.equal(inputs.read('password'),'');inputs.write('password','another-secret');inputs.clear();assert.equal(inputs.read('password'),'');handle.destroy();assert.equal(inputs.mounted,false);assert.throws(()=>inputs.read('password'),/not ready/);assert.equal(slot.children.length,0);
});
test('screen connects only explicitly using token protocol, never graph values or URL',async t=>{
  const f=fixture(t);assert.equal(f.instances.length,0);await f.ui.get('Connect to screen').emit('activate');assert.equal(f.instances.length,1);const rfb=f.instances[0];assert.equal(rfb.url,'ws://127.0.0.1:12345/v1/screen');assert.deepEqual(rfb.options.wsProtocols,['arm.v1','arm.auth.'+'a'.repeat(64)]);assert.doesNotMatch(graphText(f.ui),/a{64}/);assert.equal(rfb.resizeSession,false);assert.equal(rfb.scaleViewport,true);rfb.emit('connect');assert.equal(f.viewer.connected,true);
});
test('invalid dashboard credentials never instantiate a screen transport',async t=>{const f=fixture(t,{token:'invalid'});await f.ui.get('Connect to screen').emit('activate');assert.equal(f.instances.length,0);assert.match(f.ui.labels(),/Dashboard session unavailable/);});
test('disconnect during lazy noVNC loading cancels the pending connection',async t=>{let done;const f=fixture(t,{loadRFB:()=>new Promise(resolve=>{done=resolve;})});const pending=f.ui.get('Connect to screen').emit('activate');f.viewer.disconnect();done(f.RFB);await pending;assert.equal(f.instances.length,0);assert.equal(f.viewer.connected,false);});
test('Mac credentials stay in native island and active noVNC object, with no RDF/event payload',async t=>{
  const f=fixture(t);await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.emit('credentialsrequired',{types:['username','password']});
  const password=f.document.elements.find(e=>e.type==='password'),username=f.document.elements.find(e=>e.attributes['aria-label']==='Mac username');password.value='private-password';username.value='private-user';
  assert.doesNotMatch(graphText(f.ui),/private-password|private-user/);await f.ui.get('Sign in').emit('activate');assert.deepEqual(rfb.credentials,{username:'private-user',password:'private-password'});assert.equal(password.value,'');assert.equal(username.value,'');assert.doesNotMatch(graphText(f.ui),/private-password|private-user/);
});
test('credential cancellation clears the native fields and disconnects without sending',async t=>{
  const f=fixture(t);await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.emit('credentialsrequired',{types:['password']});const password=f.document.elements.find(e=>e.type==='password');password.value='cancelled-secret';await f.ui.get('Cancel').emit('activate');assert.equal(password.value,'');assert.equal(rfb.credentials,undefined);assert.equal(rfb.disconnected,true);assert.doesNotMatch(graphText(f.ui),/cancelled-secret/);
});
test('clipboard text is transferred only explicitly and never enters RDF controls',async t=>{
  let copied;const f=fixture(t,{clipboard:{readText:async()=> 'local-private-text',writeText:async text=>{copied=text;}}});await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.emit('connect');rfb.emit('clipboard',{text:'remote-private-text'});assert.equal(rfb.sentClipboard,undefined);await f.ui.get('Clipboard').emit('activate');
  const field=f.document.elements.find(e=>e.tag==='textarea');assert.equal(field.value,'remote-private-text');assert.doesNotMatch(graphText(f.ui),/remote-private-text/);await f.ui.get('Read this Mac').emit('activate');assert.equal(field.value,'local-private-text');assert.equal(rfb.sentClipboard,undefined);await f.ui.get('Send to Mac').emit('activate');assert.equal(rfb.sentClipboard,'local-private-text');await f.ui.get('Copy to this Mac').emit('activate');assert.equal(copied,'local-private-text');assert.doesNotMatch(graphText(f.ui),/local-private-text|remote-private-text/);f.viewer.disconnect();assert.equal(field.value,'');
});
test('view-only clipboard send and UTF-8 overflow remain blocked',async t=>{
  const f=fixture(t);await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.emit('connect');await f.ui.get('Clipboard').emit('activate');const field=f.document.elements.find(e=>e.tag==='textarea');field.value='private';f.ui.get('View only').checked=true;await f.ui.get('View only').emit('change');await f.ui.get('Send to Mac').emit('activate');assert.equal(rfb.sentClipboard,undefined);assert.match(f.ui.labels(),/Turn off View only/);f.ui.get('View only').checked=false;await f.ui.get('View only').emit('change');field.value='é'.repeat(524289);await f.ui.get('Send to Mac').emit('activate');assert.equal(rfb.sentClipboard,undefined);assert.match(f.ui.labels(),/1 MiB/);
});

test('private screen errors never become graph labels or escape into general effect errors',async t=>{
  const secret='screen-private-error-sentinel';
  await t.test('remote protocol reason',async t=>{
    const f=fixture(t);await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];
    rfb._fail(secret);rfb.emit('disconnect',{clean:false});assert.doesNotMatch(graphText(f.ui),new RegExp(secret));
    assert.notEqual(rfb.reason,secret,'foreign client diagnostics retained the peer-supplied private reason');
  });
  await t.test('credential submission exception',async t=>{
    const f=fixture(t);await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.emit('credentialsrequired',{types:['password']});
    f.document.elements.find(e=>e.type==='password').value=secret;rfb.sendCredentials=()=>{throw Error(secret);};
    await assert.doesNotReject(f.ui.get('Sign in').emit('activate'));assert.doesNotMatch(graphText(f.ui),new RegExp(secret));
  });
  await t.test('screen mode exception',async t=>{
    const f=fixture(t);await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.emit('connect');rfb._sock.flush=()=>{throw Error(secret);};f.ui.get('View only').checked=true;
    await assert.doesNotReject(f.ui.get('View only').emit('change'));assert.doesNotMatch(graphText(f.ui),new RegExp(secret));assert.equal(f.viewer.connected,false);
  });
  await t.test('connected client setup exception',async t=>{
    const f=fixture(t);await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.focus=()=>{throw Error(secret);};
    assert.doesNotThrow(()=>rfb.emit('connect'));assert.doesNotMatch(graphText(f.ui),new RegExp(secret));assert.equal(f.viewer.connected,false);
  });
  for(const operation of ['read','write','send','request'])await t.test('clipboard '+operation+' exception',async t=>{
    const clipboard={readText:async()=>{throw Error(secret);},writeText:async()=>{throw Error(secret);}};
    const f=fixture(t,{clipboard});await f.ui.get('Connect to screen').emit('activate');const rfb=f.instances[0];rfb.emit('connect');
    rfb._rfbAppleARD=true;rfb.clipboardPasteFrom=()=>{throw Error(secret);};rfb.requestRemoteClipboard=()=>{throw Error(secret);};
    await f.ui.get('Clipboard').emit('activate');f.document.elements.find(e=>e.tag==='textarea').value=secret;
    await assert.doesNotReject(f.ui.get({read:'Read this Mac',write:'Copy to this Mac',send:'Send to Mac',request:'Get from Mac'}[operation]).emit('activate'));
    assert.doesNotMatch(graphText(f.ui),new RegExp(secret));
  });
});

test('private multi-field and multiline layout preserves labels, sizing and private cleanup',()=>{
 const document=nativeDocument(),slot=document.createElement('div'),inputs=createEphemeralInputs([{name:'username',label:'Mac username',required:true},{name:'password',label:'Mac password',type:'password',required:true},{name:'link',label:'Connection link',readOnly:true,initial:'https://example.test/sample'},{name:'clipboard',label:'Clipboard text',multiline:true,initial:'Synthetic text\nSecond line'}]);
 const handle=inputs.widget.mount(slot),container=slot.children[0];assert.equal(container.style.display,'grid');assert.equal(container.style.gridTemplateColumns,'minmax(0, 1fr)');assert.equal(container.children.length,4);
 for(const label of container.children){const input=label.children[0];assert.equal(label.style.display,'grid');assert.equal(label.htmlFor,input.id);assert.equal(input.style.width,'100%');assert.equal(input.style.minWidth,'0');assert.equal(input.style.boxSizing,'border-box');}
 const username=document.elements.find(e=>e.attributes['aria-label']==='Mac username'),password=document.elements.find(e=>e.type==='password'),link=document.elements.find(e=>e.attributes['aria-label']==='Connection link'),text=document.elements.find(e=>e.tag==='textarea');
 assert.equal(link.readOnly,true);assert.equal(text.style.resize,'vertical');assert.equal(text.style.minHeight,'8rem');inputs.focus();assert.equal(document.activeElement,username);inputs.select('link');assert.equal(document.activeElement,link);assert.equal(link.selected,true);
 username.value='synthetic-user';password.value='synthetic-password';assert.deepEqual(inputs.take(),{username:'synthetic-user',password:'synthetic-password',link:'https://example.test/sample',clipboard:'Synthetic text\nSecond line'});assert.equal(password.value,'');assert.equal(username.value,'');inputs.write('clipboard','Synthetic retry');inputs.clear();assert.equal(text.value,'');inputs.write('password','synthetic-cleanup');handle.destroy();assert.equal(password.value,'');assert.equal(slot.children.length,0);assert.equal(inputs.mounted,false);
});
