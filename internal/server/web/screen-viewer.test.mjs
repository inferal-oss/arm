import {test} from 'node:test';
import assert from 'node:assert/strict';
import {sendMacScreenMode} from './screen-mode.mjs';

test('Mac Screen Sharing mode requests control only for an active controlling viewer',()=>{
  const sent=[];
  const rfb={_rfbConnectionState:'connecting',_sock:{sQpushBytes(bytes){sent.push([...bytes]);},flush(){sent.push('flush');}}};
  assert.equal(sendMacScreenMode(rfb,false),false);
  assert.deepEqual(sent,[]);
  rfb._rfbConnectionState='connected';
  assert.equal(sendMacScreenMode(rfb,false),true);
  assert.equal(sendMacScreenMode(rfb,true),true);
  assert.deepEqual(sent,[[0x0a,0,0,1],'flush',[0x0a,0,0,0],'flush']);
});
