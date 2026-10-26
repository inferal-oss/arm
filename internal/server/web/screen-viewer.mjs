import RFB from './novnc/core/rfb.js';
import {sendMacScreenMode} from './screen-mode.mjs';
import {MAX_CLIPBOARD_BYTES,clipboardBytes,requestScreenClipboard,sendScreenClipboard} from './screen-clipboard.mjs';
import './ard/ard-patch.js';

// Credentials live only in this dialog and noVNC's active RFB instance. They
// are never sent to ARM as JSON, stored, or placed in a URL.
export function createScreenViewer({root, token, context, document=root.ownerDocument}) {
  const make = (tag, text='', className='') => {
    const el=document.createElement(tag);el.className=className;el.textContent=text;return el;
  };
  const intro=make('p','Standard macOS Screen Sharing through the selected ARM node. The Mac authenticates you directly.','mesh-note');
  const controls=make('div','','screen-controls');
  const start=make('button','Connect to screen','primary');start.type='button';
  const stop=make('button','Disconnect');stop.type='button';stop.disabled=true;
  const only=make('label','View only');const onlyInput=make('input');onlyInput.type='checkbox';only.prepend(onlyInput);
  const mode=make('label','Desktop');const modeSelect=make('select');
  for(const [value,label] of [['current','Current console display'],['separate','Different macOS user session']]) {const o=make('option',label);o.value=value;modeSelect.append(o);}
  mode.append(modeSelect);
  const account=make('label','Separate Mac username');const accountInput=make('input');accountInput.type='text';accountInput.autocomplete='off';accountInput.maxLength=256;account.append(accountInput);account.hidden=true;
  const separate=make('p','A different macOS login may get its own virtual desktop when the Mac’s sharing settings permit it. Use that account’s username and password at the prompt; the server decides which desktop it opens.','mesh-note');separate.hidden=true;
  modeSelect.addEventListener('change',()=>{separate.hidden=account.hidden=modeSelect.value!=='separate';});
  const status=make('p','Disconnected','mesh-inline-notice');status.setAttribute('role','status');
  const viewport=make('div','','screen-viewport');
  const surface=make('div','','screen-surface');surface.setAttribute('aria-label','Remote Mac screen');
  const actions=make('div','','screen-viewport-actions');actions.hidden=true;
  const icon=path=>{const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');svg.setAttribute('viewBox','0 0 24 24');svg.setAttribute('aria-hidden','true');const shape=document.createElementNS('http://www.w3.org/2000/svg','path');shape.setAttribute('d',path);svg.append(shape);return svg;};
  const expand=make('button','','screen-expand');expand.type='button';expand.setAttribute('aria-label','Fill window');expand.title='Fill window';
  const end=make('button','','screen-disconnect');end.type='button';end.setAttribute('aria-label','Disconnect screen');end.title='Disconnect screen';
  end.append(icon('M6 6l12 12M18 6L6 18'));
  const clipboardButton=make('button','','screen-clipboard-button');clipboardButton.type='button';clipboardButton.setAttribute('aria-label','Clipboard');clipboardButton.title='Clipboard';
  clipboardButton.append(icon('M9 4h6l1 2h3v15H5V6h3l1-2Zm0 2v2h6V6M8 11h8M8 15h8'));
  const minimize=make('span','','screen-window-minimize');minimize.setAttribute('aria-hidden','true');
  const windowTitle=make('span',context?.local?'This Mac':'Destination Mac','screen-window-title');
  actions.append(end,minimize,expand,windowTitle,clipboardButton);viewport.append(actions,surface);
  controls.append(start,stop,only,mode,account);root.replaceChildren(intro,controls,separate,status,viewport);
  let rfb=null,prompt=null,clipboardDialog=null,remoteClipboard='',connected=false,protocolError='';
  const closePrompt=()=>{if(prompt){prompt.remove();prompt=null;}};
  const closeClipboard=()=>{if(clipboardDialog){clipboardDialog.remove();clipboardDialog=null;}};
  function showClipboard(){
    if(!connected||clipboardDialog)return;
    const dialog=clipboardDialog=make('dialog','','mesh-dialog screen-clipboard-dialog');
    const content=make('div','','screen-clipboard-content');
    const heading=make('div','','screen-clipboard-heading');
    heading.append(make('h2','Clipboard'));
    const close=make('button','Close');close.type='button';close.addEventListener('click',()=>dialog.close());heading.append(close);
    const description=make('p','Text only · up to 1 MiB. Use a button to transfer between this browser and the Mac.','mesh-note');
    const field=make('textarea');field.setAttribute('aria-label','Clipboard text');field.spellcheck=false;field.value=remoteClipboard;
    const note=make('p','Clipboard text stays in this viewer until disconnect.','mesh-inline-notice');note.setAttribute('role','status');
    const buttons=make('div','','screen-clipboard-actions');
    const read=make('button','Read this Mac');read.type='button';
    const send=make('button','Send to Mac','primary');send.type='button';
    const request=make('button','Get from Mac');request.type='button';
    const copy=make('button','Copy to this Mac');copy.type='button';
    read.addEventListener('click',async()=>{
      try{if(!navigator.clipboard?.readText)throw new Error('Clipboard reading is unavailable in this browser. Paste text into the box.');const value=await navigator.clipboard.readText();if(clipboardBytes(value)>MAX_CLIPBOARD_BYTES)throw new Error('Clipboard text exceeds the 1 MiB limit.');field.value=value;note.textContent='Local clipboard loaded. Choose Send to Mac.';}
      catch(error){note.textContent=error.message||'Clipboard reading was denied. Paste text into the box.';field.focus();}
    });
    send.addEventListener('click',()=>{try{sendScreenClipboard(rfb,field.value);note.textContent='Text sent to the Mac clipboard.';}catch(error){note.textContent=error.message;}});
    request.addEventListener('click',()=>{try{requestScreenClipboard(rfb);note.textContent='Waiting for the Mac clipboard…';}catch(error){note.textContent=error.message;}});
    copy.addEventListener('click',async()=>{
      try{if(clipboardBytes(field.value)>MAX_CLIPBOARD_BYTES)throw new Error('Clipboard text exceeds the 1 MiB limit.');if(!navigator.clipboard?.writeText)throw new Error('Clipboard writing is unavailable in this browser. Select the text and press ⌘C.');await navigator.clipboard.writeText(field.value);note.textContent='Text copied to this Mac clipboard.';}
      catch(error){field.select();note.textContent=error.message||'Clipboard writing was denied. Press ⌘C to copy the selected text.';}
    });
    buttons.append(read,send,request,copy);
    content.append(heading,description,field,buttons,note);dialog.append(content);document.body.append(dialog);
    dialog.addEventListener('close',closeClipboard,{once:true});dialog.showModal();field.focus();
  }
  clipboardButton.addEventListener('click',showClipboard);
  function fitViewer(){
    if(!connected||viewport.classList.contains('screen-viewport-full')||!rfb?._fbWidth||!rfb?._fbHeight){viewport.style.width='';viewport.style.height='';return;}
    const ratio=rfb._fbWidth/rfb._fbHeight;
    const barHeight=actions.offsetHeight||42;
    const height=Math.min(window.innerHeight*.72-barHeight,900-barHeight,(root.clientWidth||window.innerWidth)/ratio);
    viewport.style.width=`${Math.round(height*ratio)}px`;
    viewport.style.height=`${Math.round(height+barHeight)}px`;
  }
  function fullView(enabled){viewport.classList.toggle('screen-viewport-full',enabled);fitViewer();const label=enabled?'Exit full view':'Fill window';expand.setAttribute('aria-label',label);expand.title=label;expand.replaceChildren(icon(enabled?'M9 3v6H3m12-6v6h6M3 15h6v6m12-6h-6v6':'M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5'));expand.setAttribute('aria-pressed',String(enabled));}
  fullView(false);
  window.addEventListener('resize',fitViewer);
  function disconnect(){closePrompt();closeClipboard();remoteClipboard='';const old=rfb;rfb=null;connected=false;start.disabled=false;stop.disabled=true;actions.hidden=true;fullView(false);if(old){try{old.disconnect();}catch{}}surface.replaceChildren();status.textContent='Disconnected';}
  function showCredentials(event,active){
    if(active!==rfb)return;
    status.textContent='Mac sign-in required';
    closePrompt();
    const types=event.detail?.types??[];
    const dialog=prompt=make('dialog','','mesh-dialog');
    const form=make('form','','screen-login');form.method='dialog';
    form.append(make('h2','Screen Sharing sign in'));
    const fields={};
    for(const type of ['username','password','target'])if(types.includes(type)){
      const label=make('label',type==='username'?'Mac username':type==='password'?'Mac password':'Login target','mesh-field');
      const input=make('input');input.type=type==='password'?'password':'text';input.autocomplete='off';input.required=true;input.maxLength=256;
      if(type==='username'&&modeSelect.value==='separate')input.value=accountInput.value.trim();
      label.append(input);form.append(label);fields[type]=input;
    }
    if(modeSelect.value==='separate')form.append(make('p','Enter a different macOS account from the one currently at the Mac. A separate desktop depends on the Mac’s sharing settings.','mesh-note'));
    const actions=make('div','','screen-login-actions'),submit=make('button','Sign in','primary'),cancel=make('button','Cancel');submit.type='submit';cancel.type='button';
    cancel.addEventListener('click',()=>{dialog.close();disconnect();});actions.append(cancel,submit);form.append(actions);dialog.append(form);document.body.append(dialog);
    form.addEventListener('submit',e=>{e.preventDefault();if(!form.reportValidity())return;const values={};for(const [type,input]of Object.entries(fields)){values[type]=input.value;input.value='';}dialog.close();closePrompt();if(rfb===active){active.sendCredentials(values);}});
    dialog.addEventListener('close',()=>{if(prompt===dialog)closePrompt();});dialog.showModal();Object.values(fields)[0]?.focus();
  }
  function connect(){
    if(rfb)return;
    if(!token||!/^[a-f0-9]{64}$/.test(token)){status.textContent='Dashboard session unavailable. Reopen this node from Mesh.';return;}
    if(modeSelect.value==='separate'&&!accountInput.value.trim()){status.textContent='Enter the separate Mac account username first.';accountInput.focus();return;}
    start.disabled=true;stop.disabled=false;actions.hidden=false;status.textContent='Connecting to '+(context?.local?'this Mac':'selected Mac')+'…';
    try{
      const initialCredentials=modeSelect.value==='separate'?{username:accountInput.value.trim()}:undefined;
      const current=rfb=new RFB(surface,`ws://${location.host}/v1/screen`,{wsProtocols:['arm.v1',`arm.auth.${token}`],credentials:initialCredentials});
      current.ardQualityPreset='millions';
      current._ardVirtualLogin=modeSelect.value==='separate'?2:0;
      current.ardControlMode=onlyInput.checked?0:1;
      protocolError='';
      const fail=current._fail.bind(current);
      current._fail=(reason)=>{protocolError=String(reason).slice(0,180);return fail(reason);};
      const initMsg=current._initMsg.bind(current);
      current._initMsg=()=>{try{return initMsg();}catch(error){return current._fail(`${current._rfbInitState}: ${String(error?.message||error).slice(0,140)}`);}};
      current.scaleViewport=true;current.resizeSession=false;current.showDotCursor=true;current.viewOnly=onlyInput.checked;
      current.addEventListener('credentialsrequired',e=>showCredentials(e,current));
      current.addEventListener('clipboard',event=>{
        if(rfb!==current||typeof event.detail?.text!=='string')return;
        const value=event.detail.text;
        if(clipboardBytes(value)>MAX_CLIPBOARD_BYTES){status.textContent='Mac clipboard text exceeds the 1 MiB viewer limit.';return;}
        remoteClipboard=value;
        const field=clipboardDialog?.querySelector('textarea');
        if(field){field.value=value;const note=clipboardDialog.querySelector('[role="status"]');if(note)note.textContent='Mac clipboard received. Choose Copy to this Mac.';}
      });
      setTimeout(()=>{if(rfb===current&&!connected&&!prompt)status.textContent='Waiting for Mac sign-in…';},6000);
      current.addEventListener('connect',()=>{if(rfb!==current)return;connected=true;fitViewer();if(!current._rfbAppleARD)sendMacScreenMode(current,current.viewOnly);status.textContent='Connected · Standard Screen Sharing';current.focus();});
      current.addEventListener('securityfailure',()=>{if(rfb===current)status.textContent='Screen Sharing rejected these credentials. Disconnect and reconnect to try again.';});
      current.addEventListener('disconnect',e=>{if(rfb!==current)return;closePrompt();closeClipboard();remoteClipboard='';rfb=null;connected=false;start.disabled=false;stop.disabled=true;actions.hidden=true;fullView(false);status.textContent=e.detail?.clean?'Disconnected':protocolError?`Screen Sharing ended: ${protocolError}`:'Screen Sharing connection ended. Check the Mac’s sharing permissions and sign-in details.';});
    }catch(e){rfb=null;start.disabled=false;stop.disabled=true;actions.hidden=true;fullView(false);status.textContent=e.message;}
  }
  start.addEventListener('click',connect);stop.addEventListener('click',disconnect);end.addEventListener('click',disconnect);
  expand.addEventListener('click',()=>fullView(!viewport.classList.contains('screen-viewport-full')));
  onlyInput.addEventListener('change',()=>{if(rfb){rfb.viewOnly=onlyInput.checked;if(rfb._rfbAppleARD)rfb.ardControlMode=onlyInput.checked?0:1;else sendMacScreenMode(rfb,rfb.viewOnly);}});
  return {disconnect, get connected(){return connected;}};
}
