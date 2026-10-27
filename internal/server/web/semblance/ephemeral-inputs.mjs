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
      slot=element;fields=new Map();const doc=element.ownerDocument;
      for(const field of schema) {
        const label=doc.createElement('label'),input=doc.createElement(field.multiline?'textarea':'input');
        const id=`arm-private-input-${++serial}`;label.htmlFor=id;label.textContent=field.label;input.id=id;
        if(!field.multiline)input.type=field.type||'text';
        input.autocomplete='off';input.spellcheck=false;input.required=Boolean(field.required);input.readOnly=Boolean(field.readOnly);
        if(field.maxLength)input.maxLength=field.maxLength;
        input.setAttribute('aria-label',field.label);input.value=field.initial||'';field.initial='';
        label.append(input);element.append(label);fields.set(field.name,input);
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
