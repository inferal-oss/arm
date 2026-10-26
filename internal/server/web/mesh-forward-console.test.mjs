import {test} from 'node:test';
import assert from 'node:assert/strict';
import {webcrypto} from 'node:crypto';
import {createForwardConsole,forwardSourceDraft,prepareForwardSource} from './mesh-forward-console.mjs';

class Element {
  constructor(tag,document){this.tagName=tag;this.ownerDocument=document;this.children=[];this.listeners={};this.value='';this.checked=false;this.disabled=false;this._text='';}
  append(...nodes){this.children.push(...nodes)}
  replaceChildren(...nodes){this.children=[];this._text='';this.append(...nodes)}
  set textContent(value){this.replaceChildren();this._text=String(value)}
  get textContent(){return this._text+this.children.map(child=>child.textContent).join('')}
  setAttribute(){}
  addEventListener(name,fn){(this.listeners[name]??=[]).push(fn)}
  async fire(name){for(const fn of this.listeners[name]??[])await fn({preventDefault(){}})}
  all(){return this.children.flatMap(child=>[child,...child.all()])}
}
const document={createElement(tag){return new Element(tag,document)}};
const draft={workspace:'a'.repeat(32),target:'b'.repeat(32),service:'web',revision:'7',bind:'127.0.0.1:0',minutes:'10',reason:'local service access'};

test('browser source preparation sends a digest and keeps the bearer only in memory',async()=>{
  const made=await prepareForwardSource(draft,webcrypto);
  assert.match(made.token,/^[a-f0-9]{64}$/);
  assert.match(made.request.key,/^[a-f0-9]{32}$/);
  assert.match(made.request.token_sha256,/^[a-f0-9]{64}$/);
  assert.equal(JSON.stringify(made.request).includes(made.token),false);
  assert.throws(()=>forwardSourceDraft({...draft,bind:'0.0.0.0:80'}));
  assert.throws(()=>forwardSourceDraft({...draft,service:'127.0.0.1:22'}));
});

test('browser source start needs saved-token confirmation and does not replay after an uncertain reply',async()=>{
  const root=new Element('section',document),sent=[];
  const ui=createForwardConsole({root,document,cryptoAPI:webcrypto,connected:()=>true,call:async(op,q)=>{
    sent.push([op,q]);if(op==='mesh_forward_list')return[];if(op==='mesh_forward_start')throw Error('acknowledgement lost');throw Error(op);
  }});
  await ui.refresh();
  for(const [name,value] of Object.entries(draft))root.all().find(node=>node.name===name).value=value;
  await root.all().find(node=>node.tagName==='form').fire('submit');
  const token=root.all().find(node=>node.tagName==='code').textContent;
  const start=root.all().find(node=>node.textContent==='Start exact listener');
  await start.fire('click');
  assert.equal(sent.filter(([op])=>op==='mesh_forward_start').length,0);
  root.all().find(node=>node.type==='checkbox').checked=true;
  await start.fire('click');
  const calls=sent.filter(([op])=>op==='mesh_forward_start');
  assert.equal(calls.length,1);
  assert.equal(JSON.stringify(calls[0][1]).includes(token),false);
  assert.equal(start.disabled,true);
  assert.match(root.textContent,/uncertain/);
  await start.fire('click');
  await root.all().find(node=>node.tagName==='form').fire('submit');
  assert.equal(sent.filter(([op])=>op==='mesh_forward_start').length,1);
  assert.match(root.textContent,/Inspect listener/);
  ui.dispose();
  assert.equal(root.textContent,'');
});

test('a lost Stop reply leaves the exact source mutation disabled',async()=>{
  const root=new Element('section',document),sent=[];
  const ui=createForwardConsole({root,document,cryptoAPI:webcrypto,connected:()=>true,call:async(op,q)=>{
    sent.push([op,q]);
    if(op==='mesh_forward_list')return [{key:'c'.repeat(32),state:'active',bind:'127.0.0.1:43123',target:draft.target,service:'web',grant_revision:7}];
    if(op==='mesh_forward_stop')throw Error('acknowledgement lost');
    throw Error(op);
  }});
  await ui.refresh();
  await root.all().find(node=>node.textContent==='Review Stop').fire('click');
  const confirm=root.all().find(node=>node.textContent==='Stop this listener');
  await confirm.fire('click');
  await confirm.fire('click');
  assert.equal(confirm.disabled,true);
  assert.equal(sent.filter(([op])=>op==='mesh_forward_stop').length,1);
  assert.match(root.textContent,/uncertain/);
  ui.dispose();
});
