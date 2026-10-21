import test from 'node:test';
import assert from 'node:assert/strict';
import {createIOSelector} from './io-selector.mjs';

class Element {
 constructor(tag,doc){this.tagName=tag;this.ownerDocument=doc;this.children=[];this.parentNode=null;this.listeners={};this.attributes={};this._text='';this.hidden=false;this.checked=false;}
 append(...nodes){for(const node of nodes){node.remove();node.parentNode=this;this.children.push(node);}}
 remove(){if(this.parentNode){this.parentNode.children=this.parentNode.children.filter(n=>n!==this);this.parentNode=null;}}
 set textContent(value){this._text=String(value);for(const child of this.children)child.parentNode=null;this.children=[];}
 get textContent(){return this._text+this.children.map(child=>child.textContent).join('');}
 setAttribute(key,value){this.attributes[key]=String(value);}
 addEventListener(key,fn){(this.listeners[key]??=[]).push(fn);}
 fire(key,event={}){for(const fn of this.listeners[key]??[])fn({preventDefault(){},stopPropagation(){},...event});}
 all(){return this.children.flatMap(child=>[child,...child.all()]);}
 contains(node){return node===this||this.all().includes(node);}
 focus(){this.ownerDocument.activeElement=this;}
}
function fixture(window){
 const doc={defaultView:window,activeElement:null,listeners:{},createElement:tag=>new Element(tag,doc),addEventListener(key,fn){(this.listeners[key]??=[]).push(fn);},removeEventListener(key,fn){this.listeners[key]=(this.listeners[key]??[]).filter(f=>f!==fn);}},changes=[];
 const selector=createIOSelector({kind:'disk_io',title:'Disk I/O',document:doc,onChange:value=>changes.push(value)});
 const root=selector.element,button=root.children[0],popup=root.children[1],find=text=>root.all().find(el=>el.tagName==='button'&&el.textContent===text),checkbox=name=>root.all().find(el=>el.tagName==='input'&&el.attributes['aria-label']===`Include ${name}`);
 return {doc,selector,root,button,popup,find,checkbox,changes};
}

test('multi-select refresh preserves checkbox identity/focus and fixed custom membership',()=>{
 const f=fixture();f.selector.update([{id:'a',name:'Alpha'},{id:'b',name:'Beta'}]);f.button.fire('click');
 const alpha=f.checkbox('Alpha');alpha.focus();alpha.checked=false;alpha.fire('change');
 assert.equal(f.changes.length,1);assert.deepEqual(f.changes[0],{mode:'custom',ids:['b']});assert.equal(f.popup.hidden,false);
 f.selector.update([{id:'a',name:'Alpha'},{id:'b',name:'Beta'},{id:'c',name:'<script>Literal</script>'}]);
 assert.equal(f.checkbox('Alpha'),alpha);assert.equal(f.doc.activeElement,alpha);assert.equal(f.checkbox('<script>Literal</script>').checked,false);assert.equal(f.button.attributes['aria-expanded'],'true');
 assert.equal(f.root.all().some(el=>el.tagName==='script'),false);
 f.selector.update([{id:'a',name:'Alpha'}]);assert.match(f.button.textContent,/1 unavailable/);assert.deepEqual(f.selector.selection.ids,['b']);
 f.selector.update([]);assert.equal(f.doc.activeElement,alpha);assert.equal(alpha.attributes['aria-label'],'Include Alpha (unavailable)');
 f.find('Select all').fire('click');assert.equal(f.selector.selection.mode,'all');
 f.selector.update([{id:'a',name:'Alpha'},{id:'d',name:'Delta'}]);assert.equal(f.checkbox('Delta').checked,true);
 f.find('Select none').fire('click');assert.deepEqual(f.selector.selection,{mode:'custom',ids:[]});assert.match(f.button.textContent,/None selected/);
 f.selector.dispose();assert.equal(f.doc.listeners.pointerdown.length,0);
});

test('Escape restores the opener, outside clicks preserve their target, and blur closes the popup',async()=>{
 const f=fixture();f.selector.update([{id:'a',name:'Alpha'}]);f.button.fire('click');assert.equal(f.popup.hidden,false);
 f.checkbox('Alpha').focus();f.root.fire('keydown',{key:'Escape'});assert.equal(f.popup.hidden,true);assert.equal(f.doc.activeElement,f.button);assert.equal(f.button.attributes['aria-expanded'],'false');
 f.button.fire('click');const outside=f.doc.createElement('button');outside.focus();for(const fn of f.doc.listeners.pointerdown)fn({target:outside});assert.equal(f.popup.hidden,true);assert.equal(f.doc.activeElement,outside);
 f.button.fire('click');outside.focus();f.root.fire('focusout',{relatedTarget:outside});await Promise.resolve();assert.equal(f.popup.hidden,true);
 f.selector.dispose();
});

test('moving focus within an open popover cannot close it before an action click',async()=>{
 const f=fixture();f.selector.update([{id:'a',name:'Alpha'}]);f.button.fire('click');f.checkbox('Alpha').focus();
 const action=f.find('Select none');f.doc.activeElement=null;f.root.fire('focusout',{relatedTarget:action});await Promise.resolve();
 assert.equal(f.popup.hidden,false);action.focus();action.fire('click');assert.deepEqual(f.selector.selection,{mode:'custom',ids:[]});assert.equal(f.popup.hidden,false);
 f.selector.dispose();
});

test('a scrolled-offscreen anchor cannot place the open popup outside the viewport',()=>{
 const window={innerWidth:390,innerHeight:844,addEventListener(){},removeEventListener(){}},f=fixture(window);
 f.root.getBoundingClientRect=()=>({left:350,top:1500,bottom:1540});f.popup.style={};f.popup.getBoundingClientRect=()=>({height:300});
 f.selector.update([{id:'a',name:'Alpha'}]);f.button.fire('click');
 assert.equal(f.popup.style.left,'34px');assert.equal(f.popup.style.top,'528px');
 f.root.getBoundingClientRect=()=>({left:-100,top:-400,bottom:-360});f.selector.reposition();assert.equal(f.popup.style.left,'16px');assert.equal(f.popup.style.top,'16px');
 f.selector.dispose();
});
