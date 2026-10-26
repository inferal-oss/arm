export const MAX_CLIPBOARD_BYTES=1024*1024;

export function clipboardBytes(text){
  return new TextEncoder().encode(text).length;
}

export function sendScreenClipboard(rfb,text){
  if(!rfb||rfb._rfbConnectionState!=='connected')throw new Error('Connect to the Mac first.');
  if(rfb.viewOnly)throw new Error('Turn off View only to send clipboard text.');
  if(typeof text!=='string'||clipboardBytes(text)>MAX_CLIPBOARD_BYTES)throw new Error('Clipboard text exceeds the 1 MiB limit.');
  rfb.clipboardPasteFrom(text);
}

export function requestScreenClipboard(rfb){
  if(!rfb||rfb._rfbConnectionState!=='connected')throw new Error('Connect to the Mac first.');
  if(!rfb._rfbAppleARD||typeof rfb.requestRemoteClipboard!=='function')throw new Error('This Mac did not offer clipboard requests. Copy text on the Mac and reopen Clipboard here.');
  rfb.requestRemoteClipboard();
}
