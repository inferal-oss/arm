import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createKubeInventory} from './kube-inventory.mjs';

class Element {
  constructor(tag,document){this.tagName=tag;this.ownerDocument=document;this.children=[];this.listeners={};this.value='';this.hidden=false;this.disabled=false;this._text='';}
  append(...children){this.children.push(...children);}
  replaceChildren(...children){this.children=[];this._text='';this.append(...children);}
  set textContent(value){this.replaceChildren();this._text=String(value);}
  get textContent(){return this._text+this.children.map(child=>child.textContent).join('');}
  setAttribute(){}
  addEventListener(name,fn){(this.listeners[name]??=[]).push(fn);}
  async fire(name){for(const fn of this.listeners[name]??[])await fn({preventDefault(){}});}
  all(){return this.children.flatMap(child=>[child,...child.all()]);}
}

test('Kubernetes web inventory typeahead shows resources and preserves exact selected UID',async()=>{
  const document={createElement:tag=>new Element(tag,document)},root=document.createElement('section'),calls=[];
  const node={name:'arm-worker',uid:'node-uid',os:'linux',arch:'arm64',ready:true,allocatable_cpu:'1900m',allocatable_memory:'7Gi',allocatable_ephemeral:'20Gi'};
  const tainted={...node,name:'tainted-worker',uid:'tainted-uid',taints:['dedicated=elsewhere:NoSchedule']};
  const inventory=createKubeInventory({root,document,connected:()=>true,call:async(op,params)=>{calls.push([op,params]);if(op==='kube_contexts')return[{name:'local',cluster_name:'disposable',namespace:'default',current:true},{name:'second',cluster_name:'another',namespace:'work'}];if(op==='kube_nodes')return{namespace:{name:params.namespace},nodes:[node,tainted]};throw Error(op);}});
  const find=(tag,text)=>root.all().find(element=>element.tagName===tag&&element.textContent===text);
  await find('button','Browse Kubernetes Nodes').fire('click');
  assert.equal(calls[1][0],'kube_nodes');
  assert.match(root.textContent,/arm64/);assert.match(root.textContent,/1900m/);assert.match(root.textContent,/7Gi/);assert.match(root.textContent,/20Gi/);
  assert.equal(root.all().filter(element=>element.tagName==='button'&&element.textContent==='Select this Node')[1].disabled,true);
  await find('button','Select this Node').fire('click');
  assert.equal(inventory.getSelection().node.uid,'node-uid');
  const query=root.all().find(element=>element.tagName==='input'&&element.placeholder?.startsWith('Type'));
  query.value='arm';await query.fire('input');await new Promise(resolve=>setTimeout(resolve,220));
  assert.equal(calls.at(-1)[1].query,'arm');assert.equal(inventory.getSelection(),null);
  const context=root.all().find(element=>element.tagName==='select');context.value='second';await context.fire('change');
  assert.equal(calls.at(-1)[1].context,'second');assert.equal(calls.at(-1)[1].namespace,'work');
});

test('changing context immediately retires old Node selection and click handlers',async()=>{
  const document={createElement:tag=>new Element(tag,document)},root=document.createElement('section');
  let release;
  const gate=new Promise(resolve=>{release=resolve;});
  const node={name:'original',uid:'original-uid',os:'linux',arch:'amd64',ready:true};
  const inventory=createKubeInventory({root,document,connected:()=>true,call:async(op,params)=>{
    if(op==='kube_contexts')return [{name:'one',namespace:'default',current:true},{name:'two',namespace:'work'}];
    if(params.context==='two')return gate;
    return {namespace:{name:'default'},nodes:[node]};
  }});
  const find=(tag,text)=>root.all().find(element=>element.tagName===tag&&element.textContent===text);
  await find('button','Browse Kubernetes Nodes').fire('click');
  const oldChoose=find('button','Select this Node');
  await oldChoose.fire('click');
  assert.equal(inventory.getSelection().node.uid,'original-uid');
  const context=root.all().find(element=>element.tagName==='select');context.value='two';
  const pending=context.fire('change');
  try {
    assert.equal(inventory.getSelection(),null);
    await oldChoose.fire('click');
    assert.equal(inventory.getSelection(),null,'an old result must not be selected under a different current context');
  } finally {release({namespace:{name:'work'},nodes:[]});await pending;}
});

test('editing retires selection before debounce and hidden inventory cancels queued reads',async()=>{
  const document={createElement:tag=>new Element(tag,document)},root=document.createElement('section');
  let reads=0;
  const inventory=createKubeInventory({root,document,connected:()=>true,call:async(op,params)=>{
    if(op==='kube_contexts')return[{name:'one',namespace:'default',current:true}];
    reads++;return{namespace:{name:params.namespace},nodes:[{name:'node',uid:'uid',os:'linux',arch:'amd64',ready:true}]};
  }});
  const find=(tag,text)=>root.all().find(element=>element.tagName===tag&&element.textContent===text);
  await find('button','Browse Kubernetes Nodes').fire('click');
  await find('button','Select this Node').fire('click');
  const query=root.all().find(element=>element.tagName==='input'&&element.placeholder?.startsWith('Type'));
  query.value='new';await query.fire('input');
  assert.equal(inventory.getSelection(),null,'editing must immediately invalidate the old choice');
  await find('button','Hide Kubernetes Nodes').fire('click');
  await new Promise(resolve=>setTimeout(resolve,220));
  assert.equal(reads,1,'closing must retire a queued search');
  await inventory.refresh();
  assert.equal(reads,1,'hidden inventory must not issue refresh reads');
});
