import {test} from 'node:test';
import assert from 'node:assert/strict';
import {clipboardBytes,requestScreenClipboard,sendScreenClipboard} from './screen-clipboard.mjs';

test('clipboard send requires a connected controlling viewer and bounds UTF-8 text',()=>{
  const sent=[];
  const rfb={_rfbConnectionState:'connected',viewOnly:false,clipboardPasteFrom:text=>sent.push(text)};
  sendScreenClipboard(rfb,'hello');
  assert.deepEqual(sent,['hello']);
  rfb.viewOnly=true;
  assert.throws(()=>sendScreenClipboard(rfb,'private'),/View only/);
  rfb.viewOnly=false;
  assert.equal(clipboardBytes('é'),2);
  assert.throws(()=>sendScreenClipboard(rfb,'é'.repeat(524289)),/1 MiB/);
  rfb._rfbConnectionState='disconnected';
  assert.throws(()=>sendScreenClipboard(rfb,'private'),/Connect/);
  assert.deepEqual(sent,['hello']);
});

test('remote clipboard request is explicit and only available for Apple ARD',()=>{
  let requests=0;
  const rfb={_rfbConnectionState:'connected',_rfbAppleARD:true,requestRemoteClipboard(){requests++;}};
  requestScreenClipboard(rfb);
  assert.equal(requests,1);
  rfb._rfbAppleARD=false;
  assert.throws(()=>requestScreenClipboard(rfb),/did not offer/);
  assert.equal(requests,1);
});
