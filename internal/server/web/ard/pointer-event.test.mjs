import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createCipheriv} from 'node:crypto';
import {buildArdPointerBlock} from './pointer-event.js';
import {AES128ECB} from './crypto/aes128.js';

test('Apple pointer block uses encrypted-event marker, reordered buttons and bounded big-endian coordinates',()=>{
    assert.deepEqual([...buildArdPointerBlock(0x05,0x1234,0x5678,3456,2234)],
        [0,0,0,0,0,0,0,0,0,0,0xff,0x03,0x0d,0x7f,0x08,0xb9]);
    assert.deepEqual([...buildArdPointerBlock(0x02,-5,3,100,100).slice(10)],
        [0xff,0x04,0,0,0,3]);
});

test('Apple pointer ciphertext matches an independent AES-128-ECB encoder',()=>{
    const key=Uint8Array.from({length:16},(_,i)=>i);
    const body=buildArdPointerBlock(1,1420,784,3456,2234);
    const actual=new AES128ECB(key).encrypt(body);
    const cipher=createCipheriv('aes-128-ecb',key,null);cipher.setAutoPadding(false);
    const expected=Buffer.concat([cipher.update(body),cipher.final()]);
    assert.deepEqual(Buffer.from(actual),expected);
    assert.equal(actual.length,16);
});
