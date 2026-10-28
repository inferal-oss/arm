import { C as stockC, BEH as stockBEH } from '@inferal-oss/semblance';
import { stringifyJSON } from '../core.mjs';
import {replaceOwnership,releaseOwnership} from './graph-ownership.mjs';

export const ARM = 'https://arm.local/ui#';
const RDF = 'http://www.w3.org/1999/02/22-rdf-syntax-ns#';
const XSD = 'http://www.w3.org/2001/XMLSchema#';
const RDFS = 'http://www.w3.org/2000/01/rdf-schema#';
const POS = 'https://semblance.dev/ns/position#';
const TOK = 'https://semblance.dev/ns/token#';
const STY = 'https://semblance.dev/ns/style#';
const SEM = 'https://semblance.dev/ns/core#';
const C = new Proxy(stockC, {get: (target,key) => target[key] ?? 'https://semblance.dev/ns/component#'+String(key)});
// The ontology defines these DOM event classes even where the convenience
// namespace export omits them. Never key an effect map by undefined.
const BEH={...stockBEH,Input:'https://semblance.dev/ns/behaviour#Input',Change:'https://semblance.dev/ns/behaviour#Change',Focus:'https://semblance.dev/ns/behaviour#Focus'};
// Message displays the clock written in as:published. Preserve the instant
// while expressing that clock with the viewer's offset (including historic DST).
function localDateTime(ms){const d=new Date(ms),offset=-d.getTimezoneOffset(),clock=new Date(ms+offset*60000).toISOString().slice(0,-1),n=Math.abs(offset);return clock+(offset<0?'-':'+')+String(Math.floor(n/60)).padStart(2,'0')+':'+String(n%60).padStart(2,'0');}

// This builds RDF instances, never HTML. Stock ontology rules are responsible
// for rendering, keyboard behavior and field state. Host callbacks perform IO
// only after the Semblance event/reaction has committed.
export function createGraphUI(app, {onError = error => console.error(error),widgets={}} = {}) {
  const {store, runtime, renderer} = app, f = store.factory, g = f.defaultGraph();
  const nodeOf = iri => renderer.nodeOf(app.projection.index.rootOf(iri)??iri);
  const focusEligible = n => n&&!n.closest?.('[hidden],[inert]')&&!n.matches?.(':disabled')&&n.getAttribute?.('aria-disabled')!=='true'&&(!n.getClientRects||n.getClientRects().length>0);
  const handles = new Map(); let next = 0, listRevision = 0, batching = 0, dirty = false, pendingFocus = null, activating = null;
  const nn = value => f.namedNode(value);
  const match = (s,p) => store.match(s?.termType?s:nn(s),nn(p),null,g);
  const object = (s,p) => match(s,p)[0]?.object;
  const flush = () => {if(batching)return;if(dirty){dirty=false;app.flush();}if(pendingFocus){const h=pendingFocus;pendingFocus=null;h.focus();}};
  function trackSubject(subject) {
    let value=subject;
    while(value.startsWith(ARM)) {
      const owner=handles.get(value);if(owner){owner.subjects.add(subject);return;}
      const slash=value.lastIndexOf('/');if(slash<ARM.length)return;value=value.slice(0,slash);
    }
  }
  const apply = delta => {for(const q of delta.add)trackSubject(q.subject.value);runtime.apply(delta);
    for(const q of delta.remove)if(!store.match(q.subject,null,null,g).length){let value=q.subject.value;while(value.startsWith(ARM)){const owner=handles.get(value);if(owner){if(value!==q.subject.value)owner.subjects.delete(q.subject.value);break;}const slash=value.lastIndexOf('/');if(slash<ARM.length)break;value=value.slice(0,slash);}}
    dirty=true;flush();};
  const term = value => value?.termType ? value : typeof value === 'boolean'
    ? f.literal(String(value),nn(XSD+'boolean')) : typeof value === 'number'
      ? f.literal(String(value),nn(XSD+(Number.isInteger(value)?'integer':'double'))) : f.literal(String(value));
  function put(s,p,value,iri=false) {
    const remove=match(s,p), add=value===null||value===undefined?[]:[f.quad(nn(s),nn(p),iri?nn(value):term(value),g)];
    if(remove.length===add.length && remove.every((q,i)=>q.object.equals(add[i].object)))return;
    apply({remove,add});
  }
  function list(s,p,values) {
    const remove=match(s,p),old=object(s,p),seen=new Set();let cursor=old;
    while(cursor&&cursor.value!==RDF+'nil'&&!seen.has(cursor.termType+':'+cursor.value)) {
      seen.add(cursor.termType+':'+cursor.value);
      const first=match(cursor,RDF+'first'),rest=match(cursor,RDF+'rest');remove.push(...first,...rest);cursor=rest[0]?.object;
    }
    // The component follows its list pointer, not each mutable list cell.
    // Change that pointer so coalescing the tick cannot hide new membership.
    const prefix=s+'/list/'+encodeURIComponent(p)+'/'+(++listRevision)+'/',head=values.length?prefix+'0':RDF+'nil',add=[];
    add.push(f.quad(nn(s),nn(p),nn(head),g));
    values.forEach((v,i)=>{const cell=prefix+i;
      add.push(f.quad(nn(cell),nn(RDF+'first'),typeof v==='string'?nn(v):v,g),f.quad(nn(cell),nn(RDF+'rest'),nn(i+1<values.length?prefix+(i+1):RDF+'nil'),g));});
    apply({remove,add});
  }
  function batch(fn) {batching++;try{return runtime.transaction(fn);}finally{batching--;flush();}}
  class Component {
    constructor(iri,kind) {this.iri=iri;this.kind=kind;this.children=[];this.parents=new Set();this.ownedChildren=new Set();this.retainCount=0;this.subjects=new Set([iri]);this.events=new Map();this.options=[];handles.set(iri,this);}
    set(props={}) {if(this.disposed)return this;batch(()=>{for(const [key,value] of Object.entries(props)) {
      if(key==='options'){const selected=this.value,previous=this.optionIRIs??new Set();this.options=value;const opts=value.map(item=>{const iri=this.iri+'/option/'+encodeURIComponent(String(item.value));put(iri,RDFS+'label',String(item.label));put(iri,ARM+'externalValue',String(item.value));return iri;});this.optionIRIs=new Set(opts);list(this.iri,C.options,opts);for(const iri of previous)if(!this.optionIRIs.has(iri))apply({remove:store.match(nn(iri),null,null,g),add:[]});this.set({value:selected});continue;}
      if(key==='value' && this.kind==='Choice'){const option=this.options.find(x=>String(x.value)===String(value));put(this.iri,ARM+'selection',option?this.iri+'/option/'+encodeURIComponent(String(option.value)):null,true);continue;}
      if(key==='row'){put(this.iri,POS+'direction',POS+(value?'Row':'Column'),true);continue;}
      if(key==='tone'){put(this.iri,STY+'tone',value==null?null:STY+({danger:'Danger',error:'Danger',success:'Success',warning:'Warning',info:'Info',neutral:'Neutral'}[value]||value),true);continue;}
      if(key==='theme'){put(this.iri,STY+'theme',STY+value,true);continue;}
      if(key==='role'){put(this.iri,ARM+'role',value);continue;}
      if(key==='emphasis'){put(this.iri,STY+'emphasis',value==null?null:STY+({Strong:'Solid',Quiet:'Ghost'}[value]??value),true);continue;}
      if(['measure','align','justify'].includes(key)){put(this.iri,POS+key,value==null?null:POS+value,true);continue;}
      if(key==='gap'){put(this.iri,POS+'gap',value==null?null:TOK+'space-'+value,true);continue;}
      if(key==='fullViewport'){put(this.iri,'https://semblance.dev/ns/fullscreen#fullViewport',value);continue;}
      if(key==='key'){put(this.iri,'https://semblance.dev/ns/keyboard#shortcut',value);continue;}
      if(key==='open' && this.kind==='AccordionItem'){put(this.iri,C.expanded,value);continue;}
      const mapping={label:C.label,value:C.value,checked:C.checked,disabled:C.disabled,hidden:C.hidden,open:C.open,help:C.help,placeholder:C.placeholder,type:C.inputType,level:C.level,text:C.text,code:C.code,language:C.language,max:C.max,active:C.active,readOnly:C.disabled,columns:C.columns,minWidth:C.minWidth};
      if(mapping[key])put(this.iri,mapping[key],value);
    }});return this;}
    replace(...children) {if(this.disposed)return this;batch(()=>{
      const desired=children.flat().filter(x=>x!==null&&x!==undefined).map(x=>typeof x==='string'?ui.text(x):x);
      const target=this.body??this,orphaned=replaceOwnership(target,desired);target.children=desired;this.children=desired;
      list(target.iri,C.children,desired.map(x=>x.iri));for(const child of orphaned)child.dispose();
    });return this;}
    append(...children) {return this.replace(...this.children,...children);}
    on(event,fn) {if(this.disposed)return this;if(!this.events.has(event))this.events.set(event,new Set());this.events.get(event).add(fn);
      const q=f.quad(nn(this.iri),nn(RDF+'type'),nn(ARM+'Effect'),g);if(!store.has(q))apply({remove:[],add:[q]});return this;}
    emit(event,detail={}) {if(this.disposed)return;for(const fn of this.events.get(event)||[])try{Promise.resolve(fn({target:this,currentTarget:this,...detail})).catch(onError);}catch(error){onError(error);}}
    get value(){const v=object(this.iri,this.kind==='Choice'?ARM+'selection':C.value);return this.kind==='Choice'?(v?object(v.value,ARM+'externalValue')?.value??'':''):v?.value??'';}
    get label(){return object(this.iri,C.label)?.value??'';} set label(v){this.set({label:v});}
    set value(v){this.set({value:v});}
    get checked(){return object(this.iri,C.checked)?.value==='true';} set checked(v){this.set({checked:v});}
    get hidden(){return object(this.iri,C.hidden)?.value==='true';} set hidden(v){this.set({hidden:v});}
    get disabled(){return object(this.iri,C.disabled)?.value==='true';} set disabled(v){this.set({disabled:v});}
    get open(){return object(this.iri,this.kind==='AccordionItem'?C.expanded:C.open)?.value==='true';} set open(v){this.set({open:v});if(v)this.wasOpen=true;}
    get isConnected(){return Boolean(nodeOf(this.iri)?.isConnected);}
    focus(){if(this.disposed||this.disabled)return;if(batching){pendingFocus=this;return;}app.flush();let n=nodeOf(this.iri);if(!n)return;const selector='input,textarea,select,button,a[href],[tabindex]';if(!focusEligible(n))return;const controls=[...n.querySelectorAll?.(selector)??[]],target=n.matches?.(selector)?n:controls.find(focusEligible);if(!target&&controls.length)return;
      // Only otherwise unfocusable reading destinations need a graph flag.
      // Native buttons and fields retain their stock place in the Tab order.
      const reading=!target||(target===n&&object(this.iri,ARM+'focusable')?.value==='true');
      if(!target){put(this.iri,ARM+'focusable',true);app.flush();n=nodeOf(this.iri);}n?.scrollIntoView?.({block:reading?'center':'nearest'});(target??n)?.focus?.({preventScroll:true});}
    scrollEnd(){if(this.disposed)return;app.flush();const n=nodeOf(this.iri);if(n)n.scrollTop=n.scrollHeight;}
    showModal(opener){this.returnTo=ui.activeComponent;this.opener=opener??activating??this.returnTo;
      // Stock overlays share a stacking level, and keyboard focus follows the
      // last visible modal. Raise the existing graph handle when it opens;
      // creation order must not put an older lease dialog behind management.
      if(ui.desktopHosts.has(this.iri)&&[...ui.desktopHosts].at(-1)!==this.iri){ui.desktopHosts.delete(this.iri);ui.desktopHosts.add(this.iri);ui.syncDesktop();}
      this.open=true;this.focus();}
    restoreFocus(){
      if(pendingFocus)return;
      const eligible=h=>h&&!h.disposed&&!h.disabled&&h.isConnected&&focusEligible(nodeOf(h.iri));
      const modal=[...ui.desktopHosts].reverse().map(iri=>handles.get(iri)).find(h=>['Dialog','Drawer'].includes(h?.kind)&&h.open&&eligible(h));
      for(const h of [this.opener,this.returnTo])if(eligible(h)&&(!modal||modal.contains(h))){h.focus();return;}
      if(modal){modal.focus();return;}
      const heading=handles.get(ARM+'heading');if(eligible(heading))heading.focus();
    }
    close(){if(this.open){this.open=false;this.wasOpen=false;this.emit('close');this.restoreFocus();}}
    contains(handle){if(!handle)return false;const n=nodeOf(this.iri),child=nodeOf(handle.iri);return !!n&&!!child&&(n===child||n.contains?.(child));}
    retain(){if(!this.disposed)this.retainCount++;return this;}
    release(){if(this.retainCount)this.retainCount--;if(!this.retainCount&&!this.parents.size)this.dispose();return this;}
    dispose(){if(this.disposed)return;this.disposed=true;batch(()=>{
      const orphaned=releaseOwnership(this);for(const child of orphaned)child.dispose();
      if(this.widgetName&&widgets[this.widgetName]===this.widget)delete widgets[this.widgetName];
      if(ui.desktopHosts.delete(this.iri))ui.syncDesktop();
      const owned=[...this.subjects].flatMap(subject=>store.match(nn(subject),null,null,g));apply({remove:owned,add:[]});handles.delete(this.iri);this.events.clear();this.children=[];this.subjects.clear();
    });}

  }
  function component(kind,props={}) {const iri=ARM+'c'+(++next);const h=new Component(iri,kind);put(iri,RDF+'type',C[kind],true);h.set(props);return h;}
  const ui={app,batch,handles,nn,put,list,component,
    bind(name,kind='Stack'){const iri=ARM+name;if(handles.has(iri))return handles.get(iri);const type=object(iri,RDF+'type');const h=new Component(iri,type?.value.startsWith('https://semblance.dev/ns/component#')?type.value.split('#')[1]:kind);
      let cell=object(iri,C.children);while(cell&&cell.value!==RDF+'nil'){const child=object(cell,RDF+'first');if(child?.value.startsWith(ARM))h.children.push(ui.bind(child.value.slice(ARM.length)));cell=object(cell,RDF+'rest');}replaceOwnership(h,h.children);return h;},
    stack(children=[],props={}) {return batch(()=>component('Stack',{row:false,gap:2,...props}).replace(...children));},
    grid(children=[],props={}){return batch(()=>component('Grid',{columns:Math.min(children.length||3,3),minWidth:'240px',...props}).replace(...children));},
    toolbar(label,children=[]){return batch(()=>component('Toolbar',{label}).replace(...children));},
    stat(label,value,help=''){return component('Stat',{label,value,help});},
    timeline(label='Activity',limit=50){return batch(()=>{const h=component('Timeline',{label}),stream=h.iri+'/stream',TREE='https://w3id.org/tree#',LDES='https://w3id.org/ldes#';h.eventItems=new Set();
      put(h.iri,C.of,stream,true);put(h.iri,C.limit,Math.max(1,Math.min(50,limit)));put(stream,RDF+'type',LDES+'EventStream',true);
      h.setEvents=entries=>batch(()=>{if(h.disposed)return;const rows=entries.slice(-50),wanted=new Set(rows.map(e=>h.iri+'/event/'+encodeURIComponent(e.id))),add=[],remove=[];
        for(const [i,e]of rows.entries()){const item=h.iri+'/event/'+encodeURIComponent(e.id),kind=item+'/kind',target=item+'/target';
          if(!h.eventItems.has(item))add.push(f.quad(nn(stream),nn(TREE+'member'),nn(item),g));
          put(item,RDF+'type',kind,true);put(kind,RDFS+'label',e.kind??'Event');put(item,BEH.sequence,i+1);put(target,RDFS+'label',e.summary??'');put(item,BEH.target,target,true);
          const at=Date.parse(e.at);put(item,BEH.at,Number.isFinite(at)?f.literal(new Date(at).toISOString(),nn(XSD+'dateTime')):null);
          put(item,BEH.previous,i?h.iri+'/event/'+encodeURIComponent(rows[i-1].id):null,true);
        }
        put(stream,BEH.latest,rows.length?h.iri+'/event/'+encodeURIComponent(rows.at(-1).id):null,true);
        for(const item of h.eventItems)if(!wanted.has(item)){for(const s of [item,item+'/kind',item+'/target'])remove.push(...store.match(nn(s),null,null,g));remove.push(...store.match(nn(stream),nn(TREE+'member'),nn(item),g));}
        apply({remove,add});h.eventItems=wanted;return h;
      });return h;});},
    descriptions(pairs){return batch(()=>{const h=component('DescriptionList'),subject=h.iri+'/data';h.descriptionFields=new Set();
      // These are caller-formatted labels, not quantities to infer from their
      // spelling. Use the stock of/fields + labelled-resource contract so IDs,
      // leading zeroes and int64 values remain exact in the description.
      h.setPairs=rows=>{if(h.disposed)return h;return batch(()=>{const fields=rows.map(([label,value],i)=>{const field=h.iri+'/field/'+i,display=field+'/value';put(field,RDFS+'label',String(label));put(display,RDFS+'label',String(value??'—'));put(subject,field,display,true);return field;});
        put(h.iri,C.of,subject,true);list(h.iri,C.fields,fields);const next=new Set(fields),remove=[];
        for(const field of h.descriptionFields)if(!next.has(field))remove.push(...match(subject,field),...store.match(nn(field),null,null,g),...store.match(nn(field+'/value'),null,null,g));if(remove.length)apply({remove,add:[]});h.descriptionFields=next;return h;
      });};h.setPairs(pairs);return h;});},
    messageList(label='Conversation'){return batch(()=>{const h=component('MessageList',{label}),AS='https://www.w3.org/ns/activitystreams#';h.messageItems=new Set();h.messageAuthors=new Set();h.children=[];
      put(h.iri,C.viewer,h.iri+'/author/operator',true);
      h.setMessages=entries=>batch(()=>{if(h.disposed)return;const bodies=entries.map(e=>e.body).filter(Boolean),orphaned=replaceOwnership(h,bodies);h.children=bodies;
        const wanted=new Set(entries.map(e=>h.iri+'/item/'+encodeURIComponent(e.id))),authors=new Set(),remove=match(h.iri,C.item).filter(q=>!wanted.has(q.object.value)),add=[];
        for(const e of entries){const item=h.iri+'/item/'+encodeURIComponent(e.id),author=h.iri+'/author/'+encodeURIComponent(e.author??'Unknown');
          if(!h.messageItems.has(item))add.push(f.quad(nn(h.iri),nn(C.item),nn(item),g));
          authors.add(author);
          put(author,RDFS+'label',e.author??'Unknown');put(item,AS+'attributedTo',author,true);put(item,AS+'content',e.summary??'');put(item,C.content,e.body?.iri??null,true);
          const at=Date.parse(e.at);put(item,AS+'published',Number.isFinite(at)?f.literal(localDateTime(at),nn(XSD+'dateTime')):null);
        }
        for(const item of h.messageItems)if(!wanted.has(item))remove.push(...store.match(nn(item),null,null,g));
        for(const author of h.messageAuthors)if(!authors.has(author))remove.push(...store.match(nn(author),null,null,g));
        apply({remove,add});h.messageItems=wanted;h.messageAuthors=authors;for(const child of orphaned)child.dispose();return h;
      });return h;});},
    heading(label,level=2){return component('Heading',{label,level});}, text(label,props={}){return component('Text',{label,...props});},
    badge(label,props={}){return component('Badge',{label,...props});},
    button(label,callback,props={}){const h=component('Button',{label,...props});if(callback)h.on('activate',callback);return h;},
    iconButton(label,icon,callback,props={}){const h=component('IconButton',{label,...props});put(h.iri,C.icon,icon);if(callback)h.on('activate',callback);return h;},
    field(label,props={},onInput){const h=component('TextField',{label,value:'',...props});if(onInput)h.on('input',onInput);return h;},
    area(label,props={},onInput){const h=component('TextArea',{label,value:'',...props});if(onInput)h.on('input',onInput);return h;},
    check(label,props={},onChange){const h=component('Checkbox',{label,checked:false,...props});if(onChange)h.on('change',onChange);return h;},
    select(label,options=[],props={},onChange){return batch(()=>{const h=component('Choice',{label});put(h.iri,C.controls,h.iri,true);put(h.iri,C.property,ARM+'selection',true);h.set({options,value:options[0]?.value??'',...props});if(onChange)h.on('change',onChange);return h;});},
    card(label,children=[],props={}){return batch(()=>{const {actions=[],...rest}=props,h=component('Card',{label,...rest});h.body=ui.stack(children);put(h.iri,C.content,h.body.iri,true);h.children=h.body.children;replaceOwnership(h,[h.body,...actions]);if(actions.length)list(h.iri,C.actions,actions.map(x=>x.iri));return h;});},
    code(text){return component('Code',{value:String(text),language:'json'});},
    island(name,widget,props={}){return batch(()=>{const kind=ARM+'Island',ext='https://semblance.dev/ns/foreign#';
      if(!match(kind,RDFS+'subClassOf').some(q=>q.object.value===ext+'Island')||object(kind,ext+'adapter')?.value!==ext+'Widget')throw Error('Declare the ARM Island class and Widget adapter before constructing the runtime.');
      widgets[name]=widget;
      const h=component('Stack',props);put(h.iri,RDF+'type',kind,true);put(h.iri,ext+'widget',name);h.kind='Island';h.widgetName=name;h.widget=widget;return h;});},
    progress(label,value,max=100){return component('Progress',{label,value:value??0,max,active:value!==null&&value!==undefined});},
    line(label,rows=[],options={}){return batch(()=>{const h=component('Sparkline',{label});h.points=new Set();
      h.setSeries=(values,settings={})=>batch(()=>{if(h.disposed)return;const limited=values.slice(-60),xs=limited.map(x=>x.x).filter(Number.isFinite),ys=limited.map(x=>x.value).filter(x=>typeof x==='number'&&Number.isFinite(x)),lo=settings.start??Math.min(...xs),hi=settings.end??Math.max(...xs),bottom=settings.min??Math.min(0,...ys),top=settings.max??Math.max(1,...ys),membership=new Set();
        put(h.iri,C.start,Number.isFinite(lo)?lo:0);put(h.iri,C.end,Number.isFinite(hi)&&hi>lo?hi:(Number.isFinite(lo)?lo:0)+1);put(h.iri,C.min,bottom);put(h.iri,C.max,top>bottom?top:bottom+1);put(h.iri,C.unit,settings.unit??'');
        const add=[],remove=[];limited.forEach((row,i)=>{const point=h.iri+'/point/'+encodeURIComponent(String(row.id??row.x));membership.add(point);if(!h.points.has(point))add.push(f.quad(nn(h.iri),nn(C.point),nn(point),g));
          put(point,C.start,Number.isFinite(row.x)?row.x:null);put(point,C.value,typeof row.value==='number'&&Number.isFinite(row.value)?row.value:null);put(point,C.label,row.label??String(row.x));
          const next=limited[i+1];put(point,'https://ontology.inferal.com/modules/ordering/immediatelyPrecedes',next&&row.connectNext!==false?h.iri+'/point/'+encodeURIComponent(String(next.id??next.x)):null,true);
        });
        for(const point of h.points)if(!membership.has(point)){remove.push(...store.match(nn(point),null,null,g));remove.push(...store.match(nn(h.iri),nn(C.point),nn(point),g));}
        apply({remove,add});h.points=membership;return h;
      });h.setSeries(rows,options);return h;});},
    dialog(label,children=[],actions=[]){return batch(()=>{const h=component('Dialog',{label,open:false});h.body=ui.stack(children);h.children=h.body.children;replaceOwnership(h,[h.body,...actions]);put(h.iri,C.content,h.body.iri,true);list(h.iri,C.actions,actions.map(x=>x.iri));ui.desktopHosts.add(h.iri);ui.syncDesktop();return h;});},
    drawer(label,children=[],actions=[]){return batch(()=>{const h=component('Drawer',{label,open:false});h.body=ui.stack(children);h.children=h.body.children;replaceOwnership(h,[h.body,...actions]);put(h.iri,C.content,h.body.iri,true);put(h.iri,C.side,'end');list(h.iri,C.actions,actions.map(x=>x.iri));ui.desktopHosts.add(h.iri);ui.syncDesktop();return h;});},
    // Stock Tabs owns tear-off, keyboard pop-out and return-by-provenance.
    // Its content handle stays owned here while the renderer moves the same node.
    detachable(label,content){return batch(()=>{
      if(!ui.windowDesktop){
        const desktop=component('Desktop'),taskbar=component('Taskbar',{label:'Log windows'});
        put(taskbar.iri,C.of,desktop.iri,true);put(desktop.iri,C.hosts,taskbar.iri,true);
        replaceOwnership(desktop,[taskbar]);desktop.children=[taskbar];
        ui.windowDesktop=desktop;ui.desktopHosts.add(desktop.iri);ui.syncDesktop();
      }
      const h=component('Tabs'),tab=component('Tab',{label}),desktop=ui.windowDesktop;
      put(tab.iri,C.content,content.iri,true);replaceOwnership(tab,[content]);tab.children=[content];
      replaceOwnership(h,[tab]);h.children=[tab];h.tab=tab;
      list(h.iri,C.tabs,[tab.iri]);put(h.iri,C.selected,tab.iri,true);put(h.iri,C.detachable,true);put(h.iri,C.windowsOn,desktop.iri,true);
      Object.defineProperty(h,'detached',{get:()=>object(tab.iri,C.detached)?.value==='true'});
      h.popOut=()=>put(tab.iri,C.detached,true);h.dock=()=>put(tab.iri,C.detached,null);
      let detached=false;
      const dispose=h.dispose.bind(h),window=h.iri+'/~window-'+encodeURIComponent(tab.iri),viewport=globalThis.window;
      function keepVisible(){
        if(h.disposed||!h.detached||!viewport)return;
        const state=object(window,C.windowState)?.value,edge=object(window,C.attachedTo)?.value;
        if(state&&state!==C.Normal||edge&&edge!==C.NoEdge)return;
        const node=nodeOf(window);let box=node?.getBoundingClientRect?.();
        if(!box?.width||!box.height)return;
        // Stock frame dimensions are content-box values; leave room for its
        // border and a viewport margin before clamping its position.
        batch(()=>{if(box.width>viewport.innerWidth-32)put(window,C.width,Math.max(1,viewport.innerWidth-34));if(box.height>viewport.innerHeight-32)put(window,C.height,Math.max(1,viewport.innerHeight-34));});
        box=node.getBoundingClientRect();
        const x=Math.max(0,Math.min(box.left,viewport.innerWidth-box.width)),y=Math.max(0,Math.min(box.top,viewport.innerHeight-box.height));
        if(Math.abs(x-box.left)>1||Math.abs(y-box.top)>1)batch(()=>{put(window,C.x,Math.round(x));put(window,C.y,Math.round(y));});
      }
      viewport?.addEventListener('resize',keepVisible);
      const unwatch=runtime.onInput((graph,delta)=>{
        if(graph!==undefined||![...delta.add,...delta.remove].some(q=>q.subject.value===tab.iri&&q.predicate.value===C.detached||q.subject.value===window&&q.predicate.value===C.windowState))return;
        queueMicrotask(()=>{
          if(h.disposed)return;
          if(detached!==h.detached){detached=h.detached;
            // A frame with saved geometry can remain projected after its tab
            // docks. Park it explicitly, retaining geometry for the next detach.
            put(window,C.open,detached);h.emit('detach',{detached});
          }
          keepVisible();
        });
      });
      h.dispose=()=>{if(h.disposed)return;unwatch();viewport?.removeEventListener('resize',keepVisible);batch(()=>{
        // Native gestures write geometry directly to the deterministic window.
        // Retire it with the tab so closed sessions leave no private state behind.
        apply({remove:store.match(nn(window),null,null,g),add:[]});
        if(object(desktop.iri,C.topmost)?.value===window)put(desktop.iri,C.topmost,null);
        dispose();
      });};
      return h;
    });},
    disclosure(label,children=[],props={}){return batch(()=>{const h=component('AccordionItem',{label,open:false,...props});h.body=ui.stack(children);put(h.iri,C.content,h.body.iri,true);h.children=h.body.children;replaceOwnership(h,[h.body]);return h;});},
    json(value,label='JSON'){const raw=stringifyJSON(value);return ui.disclosure(label,[ui.code(raw.length>256000?raw.slice(0,256000)+'\n[display truncated]':raw)]);},
    // Review cells are already formatted; stock code format preserves their
    // lexical values instead of inferring an IEEE-754 number from the text.
    table(headers,rows){return batch(()=>{const h=component('Table',{label:headers.join(' / ')}),columns=headers.map((label,i)=>{const iri=h.iri+'/column/'+i;put(iri,RDF+'type',C.Column,true);put(iri,C.label,label);put(iri,C.variable,'col'+i);put(iri,C.format,'code');return iri;});
      list(h.iri,C.tableColumns,columns);put(h.iri,C.query,h.iri+'/query',true);put(h.iri,C.dataset,h.iri,true);
      put(h.iri+'/query',SEM+'select',`SELECT ${headers.map((_,i)=>'?col'+i).join(' ')} WHERE { <${h.iri}> <${ARM}row> ?row . ?row <${ARM}position> ?position . ${headers.map((_,i)=>`?row <${ARM}col${i}> ?col${i} .`).join(' ')} } ORDER BY ?position`);
      const add=[];rows.forEach((row,i)=>{const iri=h.iri+'/row/'+i;add.push(f.quad(nn(h.iri),nn(ARM+'row'),nn(iri),g),f.quad(nn(iri),nn(ARM+'position'),term(i),g));row.forEach((value,j)=>{if(typeof value!=='string'&&typeof value!=='number'&&typeof value!=='bigint')throw Error('Use selectable record cards for interactive table cells');add.push(f.quad(nn(iri),nn(ARM+'col'+j),term(String(value)),g));});});apply({remove:[],add});return h;});},
    desktopHosts:new Set(),syncDesktop(){const desktop=ui.bind('desktop','Stack');desktop.replace(...[...ui.desktopHosts].map(x=>handles.get(x)).filter(Boolean));},
    handled(h){batch(()=>{const target=handles.get(h.target.value),map={[BEH.Click]:'activate',[BEH.Input]:'input',[BEH.Change]:'change',[BEH.Focus]:'focus'},effects=[];
    if(target&&map[h.type])effects.push([target,map[h.type],h]);
    // Snapshot and consume EVERY committed request before any callback can
    // focus a dialog and reenter through a native Focus event. This also
    // delivers stock palette commands' nested Clicks exactly once.
    const requested=store.match(null,nn(ARM+'requested'),null,g),seen=new Set(target?[target.iri]:[]);if(requested.length)apply({remove:requested,add:[]});
    for(const q of requested){const effect=handles.get(q.subject.value);if(!effect||seen.has(effect.iri))continue;seen.add(effect.iri);effects.push([effect,'activate',{...h,target:nn(effect.iri),type:BEH.Click,event:q.object}]);}
    if(target)put(target.iri,ARM+'focused',null);
    for(const[effect,event,detail]of effects)if(!effect.disabled){const previous=activating;if(event==='activate')activating=effect;try{effect.emit(event,{event:detail});}finally{activating=previous;}}
    for(const iri of ui.desktopHosts){const x=handles.get(iri);if(!x)continue;const open=x.open,closed=x.wasOpen&&!open,opened=!x.wasOpen&&open;x.wasOpen=open;if(opened)x.emit('open');if(closed){x.emit('close');if(!pendingFocus)x.restoreFocus();}}
    if(target){put(target.iri,ARM+'requested',null);put(target.iri,ARM+'focused',null);}
    });},
    get activeComponent(){const active=globalThis.document?.activeElement;if(!active)return null;let best=null,bestNode=null;for(const h of handles.values()){const n=nodeOf(h.iri);if((n===active||n?.contains?.(active))&&(!bestNode||bestNode.contains(n))){best=h;bestNode=n;}}return best;},
  };
  globalThis.document?.addEventListener('focusout',event=>{const affected=[...handles.values()].filter(h=>h.events.has('focusout')&&nodeOf(h.iri)?.contains?.(event.target));queueMicrotask(()=>{for(const h of affected)h.emit('focusout');});});
  return ui;
}
