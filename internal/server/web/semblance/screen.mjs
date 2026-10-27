import {sendMacScreenMode} from '../screen-mode.mjs';
import {MAX_CLIPBOARD_BYTES,clipboardBytes,requestScreenClipboard,sendScreenClipboard} from '../screen-clipboard.mjs';
import {createEphemeralInputs} from './ephemeral-inputs.mjs';

async function loadPatchedRFB() {
  const [{default:RFB}]=await Promise.all([import('/novnc/core/rfb.js'),import('/ard/ard-patch.js')]);
  return RFB;
}

// External clipboard/client errors may quote private input. Only our closed
// validation messages may enter graph-backed diagnostics; never reflect an
// arbitrary peer or native-API exception after handling a secret.
const safeClipboardMessages=new Set([
  'Connect to the Mac first.', 'Turn off View only to send clipboard text.',
  'Clipboard text exceeds the 1 MiB limit.',
  'This Mac did not offer clipboard requests. Copy text on the Mac and reopen Clipboard here.',
  'Clipboard reading is unavailable in this browser. Paste text into the box.',
  'Clipboard writing is unavailable in this browser. Select the text and press ⌘C.',
]);
const clipboardNotice=(error,fallback)=>safeClipboardMessages.has(error?.message)?error.message:fallback;

// The only foreign UI is noVNC's framebuffer plus native ephemeral text fields.
// Credentials, selected Mac account and clipboard text never enter RDF/events.
export function createScreenViewer({ui,root,token,context,loadRFB=loadPatchedRFB,
  location=globalThis.location,navigator=globalThis.navigator,window=globalThis.window}) {
  let rfb=null,connected=false,connecting=false,disposed=false,generation=0,prompt=null,clipboardDialog=null;
  let remoteClipboard='',protocolError='',waitingTimer=null,full=false,frameSurface=null;
  const status=ui.text('Disconnected');
  const start=ui.button('Connect to screen',connect),stop=ui.button('Disconnect',disconnect,{disabled:true}),only=ui.check('View only',{checked:false},()=>{
    if(rfb)try{rfb.viewOnly=only.checked;if(rfb._rfbAppleARD)rfb.ardControlMode=only.checked?0:1;else sendMacScreenMode(rfb,rfb.viewOnly);}catch{disconnect();setStatus('Screen mode change outcome is unknown. Reconnect explicitly before resuming.');}
  });
  const mode=ui.select('Desktop',[{value:'current',label:'Current console display'},{value:'separate',label:'Different macOS user session'}],{},()=>{
    separate.hidden=account.hidden=mode.value!=='separate';if(mode.value!=='separate')accountInputs.clear();
  });
  const accountInputs=createEphemeralInputs([{name:'username',label:'Separate Mac username',maxLength:256}]);
  const account=ui.island('screen-account',accountInputs.widget,{label:'Separate Mac username',hidden:true});
  const separate=ui.text('A different macOS login may get its own virtual desktop when the Mac’s sharing settings permit it. Use that account’s username and password at the prompt; the server decides which desktop it opens.',{hidden:true});
  const frameWidget={mount(slot) {
    const doc=slot.ownerDocument;frameSurface=doc.createElement('div');frameSurface.setAttribute('aria-label','Remote Mac screen');
    // Geometry belongs to the framebuffer host; colors and controls use the
    // stock surrounding components. noVNC itself owns the canvas subtree.
    frameSurface.style.width='100%';fitFrame();slot.append(frameSurface);
    return {update(){fitFrame();},destroy(){disconnect();frameSurface=null;slot.replaceChildren();}};
  }};
  const frame=ui.island('screen-framebuffer',frameWidget,{label:'Remote Mac framebuffer'});
  const expand=ui.button('Fill window',()=>fullView(!full)),end=ui.button('Disconnect screen',disconnect),clipboardButton=ui.button('Clipboard',showClipboard);
  const actions=ui.stack([end,expand,ui.text(context?.local?'This Mac':'Destination Mac'),clipboardButton],{row:true,hidden:true});
  const viewport=ui.stack([actions,frame]);
  root.replace(ui.text('Standard macOS Screen Sharing through the selected ARM node. The Mac authenticates you directly.'),ui.stack([start,stop,only,mode,account],{row:true}),separate,status,viewport);
  function setStatus(label){if(!disposed)status.set({label});}
  function closePrompt(){if(prompt){const old=prompt;prompt=null;old.inputs.clear();old.dialog.close();old.dialog.dispose();}}
  function closeClipboard(){if(clipboardDialog){const old=clipboardDialog;clipboardDialog=null;old.inputs.clear();old.dialog.close();old.dialog.dispose();}}
  function fitFrame(){if(frameSurface)frameSurface.style.height=full?'calc(100dvh - 8rem)':'min(72dvh, 900px)';}
  function fullView(enabled){full=Boolean(enabled);viewport.set({fullViewport:full});expand.set({label:full?'Exit full view':'Fill window'});fitFrame();}
  function disconnect(){
    generation++;connecting=false;clearTimeout(waitingTimer);waitingTimer=null;closePrompt();closeClipboard();remoteClipboard='';accountInputs.clear();
    const old=rfb;rfb=null;connected=false;start.disabled=false;stop.disabled=true;actions.hidden=true;fullView(false);
    if(old)try{old.disconnect();}catch{}
    frameSurface?.replaceChildren();setStatus('Disconnected');
  }
  function showCredentials(event,active) {
    if(disposed||active!==rfb)return;setStatus('Mac sign-in required');closePrompt();
    const types=event.detail?.types??[];
    const schema=['username','password','target'].filter(type=>types.includes(type)).map(type=>({name:type,label:type==='username'?'Mac username':type==='password'?'Mac password':'Login target',type:type==='password'?'password':'text',required:true,maxLength:256,initial:type==='username'&&mode.value==='separate'?accountInputs.read('username').trim():''}));
    const inputs=createEphemeralInputs(schema),island=ui.island('screen-credentials-'+generation,inputs.widget,{label:'Private Mac credentials'}),note=ui.text('');
    const dialog=ui.dialog('Screen Sharing sign in',[island,...(mode.value==='separate'?[ui.text('Enter a different macOS account from the one currently at the Mac. A separate desktop depends on the Mac’s sharing settings.')]:[]),note],[]);
    const cancel=ui.button('Cancel',()=>{dialog.close();disconnect();}),submit=ui.button('Sign in',()=>{
      if(disposed||rfb!==active)return;let values;
      try{values=inputs.take();}catch(error){note.set({label:error.message});inputs.focus();return;}
      closePrompt();if(rfb===active)try{active.sendCredentials(values);}catch{setStatus('Screen sign-in outcome is unknown. Disconnect and reconnect before trying again.');}
      // noVNC owns the active authentication object. Its asynchronous ARD
      // handshake must finish reading it; the form and RDF never retain it.
    });
    dialog.append(ui.stack([cancel,submit],{row:true}));prompt={dialog,inputs};
    dialog.on('close',()=>{if(prompt?.dialog===dialog){inputs.clear();prompt=null;dialog.dispose();}});dialog.showModal();inputs.focus();
  }
  function showClipboard() {
    if(disposed||!connected||clipboardDialog)return;
    const inputs=createEphemeralInputs([{name:'text',label:'Clipboard text',multiline:true,initial:remoteClipboard}]);
    const island=ui.island('screen-clipboard-'+generation,inputs.widget,{label:'Private clipboard text'}),note=ui.text('Clipboard text stays in this viewer until disconnect.');
    const dialog=ui.dialog('Clipboard',[ui.text('Text only · up to 1 MiB. Use a button to transfer between this browser and the Mac.'),island,note],[]);
    const read=ui.button('Read this Mac',async()=>{
      try{if(!navigator.clipboard?.readText)throw Error('Clipboard reading is unavailable in this browser. Paste text into the box.');const value=await navigator.clipboard.readText();if(!connected||clipboardDialog?.dialog!==dialog)return;if(clipboardBytes(value)>MAX_CLIPBOARD_BYTES)throw Error('Clipboard text exceeds the 1 MiB limit.');inputs.write('text',value);note.set({label:'Local clipboard loaded. Choose Send to Mac.'});}
      catch(error){if(clipboardDialog?.dialog===dialog){note.set({label:clipboardNotice(error,'Clipboard reading was denied. Paste text into the box.')});inputs.focus();}}
    });
    const send=ui.button('Send to Mac',()=>{try{sendScreenClipboard(rfb,inputs.read('text'));note.set({label:'Text sent to the Mac clipboard.'});}catch(error){note.set({label:clipboardNotice(error,'Clipboard transfer outcome is unknown. Inspect the Mac clipboard before trying again.')});}});
    const request=ui.button('Get from Mac',()=>{try{requestScreenClipboard(rfb);note.set({label:'Waiting for the Mac clipboard…'});}catch(error){note.set({label:clipboardNotice(error,'Clipboard request failed. Reconnect before trying again.')});}});
    const copy=ui.button('Copy to this Mac',async()=>{
      try{const text=inputs.read('text');if(clipboardBytes(text)>MAX_CLIPBOARD_BYTES)throw Error('Clipboard text exceeds the 1 MiB limit.');if(!navigator.clipboard?.writeText)throw Error('Clipboard writing is unavailable in this browser. Select the text and press ⌘C.');await navigator.clipboard.writeText(text);if(clipboardDialog?.dialog===dialog)note.set({label:'Text copied to this Mac clipboard.'});}
      catch(error){if(clipboardDialog?.dialog===dialog){inputs.select('text');note.set({label:clipboardNotice(error,'Clipboard writing was denied. Press ⌘C to copy the selected text.')});}}
    });
    const close=ui.button('Close clipboard',()=>dialog.close());dialog.append(ui.stack([read,send,request,copy,close],{row:true}));
    clipboardDialog={dialog,inputs,note};dialog.on('close',()=>{if(clipboardDialog?.dialog===dialog){inputs.clear();clipboardDialog=null;dialog.dispose();}});dialog.showModal();inputs.focus();
  }
  async function connect() {
    if(disposed||rfb||connecting)return;
    if(!token||!/^[a-f0-9]{64}$/.test(token)){setStatus('Dashboard session unavailable. Reopen this node from Mesh.');return;}
    let username;
    try{username=mode.value==='separate'?accountInputs.read('username').trim():undefined;}catch(error){setStatus(error.message);return;}
    if(mode.value==='separate'&&!username){setStatus('Enter the separate Mac account username first.');accountInputs.focus();return;}
    connecting=true;const run=++generation;start.disabled=true;stop.disabled=false;actions.hidden=false;setStatus('Connecting to '+(context?.local?'this Mac':'selected Mac')+'…');
    try {
      const RFB=await loadRFB();if(disposed||run!==generation)return;
      if(!frameSurface)throw Error('The screen framebuffer is not ready.');
      const current=rfb=new RFB(frameSurface,`ws://${location.host}/v1/screen`,{wsProtocols:['arm.v1',`arm.auth.${token}`],credentials:username?{username}:undefined});
      current.ardQualityPreset='millions';current._ardVirtualLogin=mode.value==='separate'?2:0;current.ardControlMode=only.checked?0:1;protocolError='';
      const fail=current._fail.bind(current);current._fail=()=>{protocolError='Screen Sharing protocol failed.';return fail(protocolError);};
      const initMsg=current._initMsg.bind(current);current._initMsg=()=>{try{return initMsg();}catch{return current._fail();}};
      current.scaleViewport=true;current.resizeSession=false;current.showDotCursor=true;current.viewOnly=only.checked;
      current.addEventListener('credentialsrequired',event=>showCredentials(event,current));
      current.addEventListener('clipboard',event=>{
        if(rfb!==current||typeof event.detail?.text!=='string')return;const value=event.detail.text;
        if(clipboardBytes(value)>MAX_CLIPBOARD_BYTES){setStatus('Mac clipboard text exceeds the 1 MiB viewer limit.');return;}
        remoteClipboard=value;if(clipboardDialog){clipboardDialog.inputs.write('text',value);clipboardDialog.note.set({label:'Mac clipboard received. Choose Copy to this Mac.'});}
      });
      clearTimeout(waitingTimer);waitingTimer=setTimeout(()=>{if(rfb===current&&!connected&&!prompt)setStatus('Waiting for Mac sign-in…');},6000);
      current.addEventListener('connect',()=>{if(rfb!==current)return;try{connected=true;connecting=false;clearTimeout(waitingTimer);fitFrame();if(!current._rfbAppleARD)sendMacScreenMode(current,current.viewOnly);current.focus();setStatus('Connected · Standard Screen Sharing');}catch{disconnect();setStatus('Screen connection setup failed. Reconnect explicitly before resuming.');}});
      current.addEventListener('securityfailure',()=>{if(rfb===current)setStatus('Screen Sharing rejected these credentials. Disconnect and reconnect to try again.');});
      current.addEventListener('disconnect',event=>{
        if(rfb!==current)return;closePrompt();closeClipboard();remoteClipboard='';accountInputs.clear();clearTimeout(waitingTimer);rfb=null;connected=false;connecting=false;start.disabled=false;stop.disabled=true;actions.hidden=true;fullView(false);frameSurface?.replaceChildren();
        setStatus(event.detail?.clean?'Disconnected':protocolError?`Screen Sharing ended: ${protocolError}`:'Screen Sharing connection ended. Check the Mac’s sharing permissions and sign-in details.');
      });
    } catch(error){if(run===generation){rfb=null;connecting=false;start.disabled=false;stop.disabled=true;actions.hidden=true;fullView(false);setStatus('Screen Sharing could not connect. Check sharing permissions and reconnect explicitly.');}}
  }
  // Stock Fullscreen can leave through Escape. Follow its graph fact rather
  // than retaining a host-only mode after the framework closes the viewport.
  const fsFull='https://semblance.dev/ns/fullscreen#fullViewport';
  const unwatch=ui.app?.runtime?.onInput((graph,delta)=>{
    if(graph!==undefined||![...delta.add,...delta.remove].some(q=>q.subject.value===viewport.iri&&q.predicate.value===fsFull))return;
    queueMicrotask(()=>{if(disposed)return;const store=ui.app.store,f=store.factory;full=store.match(f.namedNode(viewport.iri),f.namedNode(fsFull),null,f.defaultGraph()).some(q=>q.object.value==='true');expand.set({label:full?'Exit full view':'Fill window'});fitFrame();});
  });
  window?.addEventListener('resize',fitFrame);
  return {disconnect,get connected(){return connected;},dispose(){disconnect();disposed=true;unwatch?.();window?.removeEventListener('resize',fitFrame);}};
}
