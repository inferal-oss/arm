// Behavioral fixture for backend/review controls. Real RDF rendering is tested
// by the composed runtime suite; this fixture never substitutes for that gate.
export function controlUI() {
  let next=0;const all=[];
  class Component {
    constructor(kind,label='',props={}){this.kind=kind;this.id=++next;this.label=label;this.children=[];this.events={};this.hidden=false;this.disabled=false;this.value='';this.checked=false;this.set(props);all.push(this);}
    set(props){Object.assign(this,props);return this;}
    replace(...children){this.children=children.flat();return this;}
    append(...children){this.children.push(...children.flat());return this;}
    on(type,fn){(this.events[type]??=[]).push(fn);return this;}
    async emit(type){if(this.disabled)return;for(const fn of this.events[type]??[])await fn({target:this,currentTarget:this});}
    focus(){ui.activeComponent=this;}
    dispose(){this.disposed=true;}
  }
  const make=(kind,label,props)=>new Component(kind,label,props);
  const ui={all,activeComponent:null,batch:fn=>fn(),stack:(children=[],props={})=>make('Stack','',props).replace(children),heading:(label,level)=>make('Heading',label,{level}),text:(label,props)=>make('Text',label,props),badge:(label,props)=>make('Badge',label,props),button:(label,fn,props)=>make('Button',label,props).on('activate',fn),field:(label,props,fn)=>{const h=make('TextField',label,props);if(fn)h.on('input',fn);return h;},area:(label,props,fn)=>{const h=make('TextArea',label,props);if(fn)h.on('input',fn);return h;},check:(label,props,fn)=>{const h=make('Checkbox',label,props);if(fn)h.on('change',fn);return h;},select:(label,options=[],props={},fn)=>{const h=make('Choice',label,{options,value:options[0]?.value??'',...props});if(fn)h.on('change',fn);return h;},card:(label,children=[],props={})=>make('Card',label,props).replace(children),json:(value,label)=>make('JSON',label,{value}),get:(label)=>all.filter(h=>h.label===label&&!h.disposed).at(-1),labels:()=>all.map(h=>h.label).join('\n')};
  ui.descriptions=pairs=>{const h=make('DescriptionList','',{pairs});h.setPairs=rows=>{h.pairs=rows;return h;};return h;};
  ui.grid=(children=[],props={})=>make('Grid','',props).replace(children);
  ui.messageList=label=>{const h=make('MessageList',label);h.setMessages=entries=>{h.entries=entries;h.messages=entries;h.replace(entries.map(e=>e.body));return h;};return h;};
  return ui;
}
