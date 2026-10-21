import {allDevices,noDevices,selectedDeviceIDs,toggleDevice} from './io-devices.mjs';

// A stable checkbox popover: telemetry updates labels/readings in place and
// never replace a focused checkbox or silently broaden custom membership.
export function createIOSelector({kind,title,selection=allDevices(),onChange,document=globalThis.document}={}) {
  const el=(tag,cls='',text='')=>{const n=document.createElement(tag);n.className=cls;n.textContent=text;return n;};
  const root=el('div','io-selector'),button=el('button','io-selector-button'),popup=el('div','io-selector-popup');
  const heading=el('p','io-selector-heading','Choose what to combine'),actions=el('div','io-selector-actions'),all=el('button','','Select all'),none=el('button','','Select none'),list=el('div','io-selector-list'),note=el('p','io-selector-note');
  root.append(button,popup);actions.append(all,none);popup.append(heading,actions,list,note);
  button.type=all.type=none.type='button';popup.id=`io-selector-${kind}`;popup.hidden=true;popup.setAttribute('role','dialog');popup.setAttribute('aria-label',`${title} selection`);
  button.setAttribute('aria-haspopup','dialog');button.setAttribute('aria-expanded','false');button.setAttribute('aria-controls',popup.id);
  const window=document.defaultView;
  let devices=[],disposed=false;const rows=new Map(),names=new Map();
  const setText=(n,text)=>{if(n.textContent!==text)n.textContent=text;};
  function position(){
    if(popup.hidden||!window||!root.getBoundingClientRect)return;
    const rect=root.getBoundingClientRect(),width=Math.min(340,window.innerWidth-32),below=window.innerHeight-rect.bottom-16,above=rect.top-16;
    const down=below>=Math.min(300,above),available=Math.max(60,down?below:above);
    popup.style.width=`${width}px`;popup.style.maxHeight=`${Math.min(380,available)}px`;
    popup.style.left=`${Math.max(16,Math.min(rect.left,window.innerWidth-width-16))}px`;
    const height=popup.getBoundingClientRect().height,wantedTop=down?rect.bottom+8:rect.top-height-8;
    popup.style.top=`${Math.max(16,Math.min(wantedTop,window.innerHeight-height-16))}px`;
  }
  function close(focus=false){popup.hidden=true;button.setAttribute('aria-expanded','false');if(focus)button.focus();}
  function render(){
    const selected=selectedDeviceIDs(selection,devices),selectedSet=new Set(selected),current=new Map(devices.map(d=>[d.id,d]));
    const missing=selected.filter(id=>!current.has(id)).length,unit=kind==='network'?'interface':'device';
    setText(button,selection.mode==='all'?(current.size?`All ${current.size} ${unit}${current.size===1?'':'s'} ▾`:`No ${unit}s reported ▾`):selected.length?`${selected.length} selected${missing?` · ${missing} unavailable`:''} ▾`:'None selected ▾');
    button.setAttribute('aria-label',`Choose ${title} ${kind==='network'?'interfaces':'devices'}, ${selection.mode==='all'?'all ':''}${selected.length} selected${missing?`, ${missing} unavailable`:''}`);
    setText(note,selection.mode==='all'?'All includes newly reported devices. Virtual or layered devices can overlap.':'Only these identities are combined. Missing devices remain selected and make totals unavailable.');
    const keep=new Set([...current.keys(),...selected]);
    for(const [id,row] of rows)if(!keep.has(id)){if(row.box.contains(document.activeElement)){setText(row.state,'Unavailable');row.input.setAttribute('aria-label',`Include ${names.get(id)||'Previously selected device'} (unavailable)`);}else{row.box.remove();rows.delete(id);}}
    for(const id of keep){
      const d=current.get(id);if(d?.name)names.set(id,d.name);
      let row=rows.get(id);
      if(!row){const box=el('label','io-selector-option'),input=el('input'),name=el('span'),state=el('small');input.type='checkbox';box.append(input,name,state);list.append(box);row={box,input,name,state};rows.set(id,row);input.addEventListener('change',()=>commit(toggleDevice(selection,devices,id,input.checked)));}
      row.input.checked=selectedSet.has(id);const name=names.get(id)||'Previously selected device';setText(row.name,name);row.box.title=id;row.input.setAttribute('aria-label',`Include ${name}${d?'':' (unavailable)'}`);setText(row.state,d?'':'Unavailable');
    }
    // Collector inventory is bounded, but remembered display names need no
    // lifetime history beyond current/selected/focused rows.
    for(const id of names.keys())if(!keep.has(id)&&!rows.has(id))names.delete(id);
    position();
  }
  function commit(next){selection=next;render();onChange?.(selection);}
  function toggle(){if(popup.hidden){popup.hidden=false;button.setAttribute('aria-expanded','true');position();all.focus();}else close(true);}
  function outside(event){if(!root.contains(event.target))close();}
  function keys(event){if(event.key==='Escape'&&!popup.hidden){event.preventDefault();event.stopPropagation();close(true);}}
  function blur(event){if(!disposed&&event.relatedTarget&&!root.contains(event.relatedTarget))close();}
  button.addEventListener('click',toggle);all.addEventListener('click',()=>commit(allDevices()));none.addEventListener('click',()=>commit(noDevices()));root.addEventListener('keydown',keys);root.addEventListener('focusout',blur);document.addEventListener('pointerdown',outside);window?.addEventListener('resize',position);window?.addEventListener('scroll',position,true);
  return {element:root,get selection(){return selection;},reposition:position,update(next){devices=next??[];render();},dispose(){disposed=true;document.removeEventListener('pointerdown',outside);window?.removeEventListener('resize',position);window?.removeEventListener('scroll',position,true);close();}};
}
