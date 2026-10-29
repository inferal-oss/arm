// Security island: native inputs keep secrets outside RDF and the LDES. The
// island declares no outputs; stock Semblance dialogs/buttons own every action.
// The host can read/clear values only for an explicit authenticated effect.
let serial=0;
export function createEphemeralInputs(specification) {
  const schema=specification.map(field=>({...field}));
  let fields=null,slot=null;
  const clear=()=>{if(fields)for(const input of fields.values())input.value='';for(const field of schema)field.initial='';};
  return {
    widget:{mount(element) {
      slot=element;fields=new Map();const doc=element.ownerDocument,container=doc.createElement('div');
      container.className='arm-private-inputs';Object.assign(container.style,{display:'grid',gridTemplateColumns:'minmax(0, 1fr)',gap:'1rem',width:'100%',maxWidth:'100%',minWidth:'0'});element.append(container);
      for(const field of schema) {
        const label=doc.createElement('label'),input=doc.createElement(field.multiline?'textarea':'input');
        Object.assign(label.style,{display:'grid',gridTemplateColumns:'minmax(0, 1fr)',gap:'0.375rem',minWidth:'0',overflowWrap:'anywhere'});
        Object.assign(input.style,{width:'100%',maxWidth:'100%',minWidth:'0',boxSizing:'border-box',margin:'0',padding:'0.625rem 0.75rem',font:'inherit',lineHeight:'1.4',border:'1px solid var(--sem-border, currentColor)',borderRadius:'0.5rem',background:'var(--sem-surface, Canvas)',color:'inherit'});
        if(field.multiline)Object.assign(input.style,{minHeight:'8rem',resize:'vertical'});
        const id=`arm-private-input-${++serial}`;label.htmlFor=id;label.textContent=field.label;input.id=id;
        if(!field.multiline)input.type=field.type||'text';
        input.autocomplete='off';input.spellcheck=false;input.required=Boolean(field.required);input.readOnly=Boolean(field.readOnly);
        if(field.maxLength)input.maxLength=field.maxLength;
        input.setAttribute('aria-label',field.label);input.value=field.initial||'';field.initial='';
        label.append(input);container.append(label);fields.set(field.name,input);
      }
      return {update(){},destroy(){clear();fields=null;slot=null;element.replaceChildren();}};
    }},
    read(name){if(!fields)throw Error('The private input is not ready.');return fields.get(name)?.value??'';},
    write(name,value){if(!fields)throw Error('The private input is not ready.');const field=fields.get(name);if(field)field.value=String(value);},
    take(){if(!fields)throw Error('The private input is not ready.');const values={};for(const [name,input]of fields){if(input.required&&!input.value)throw Error(`Enter ${schema.find(f=>f.name===name).label.toLowerCase()}.`);values[name]=input.value;}clear();return values;},
    clear,
    focus(){fields?.values().next().value?.focus();},
    select(name){const input=fields?.get(name);input?.focus();input?.select();},
    get mounted(){return Boolean(slot);},
  };
}
