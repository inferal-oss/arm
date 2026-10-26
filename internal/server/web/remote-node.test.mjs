import {test} from 'node:test';
import assert from 'node:assert/strict';
import {remoteResourceLabel} from './remote-node.mjs';
test('remote resource headings are human labels; namespaces and collector IDs stay in optional details',()=>{
 assert.equal(remoteResourceLabel({kind:'cpu',id:'private-host/cpu'}),'Live CPU headroom');
 assert.equal(remoteResourceLabel({kind:'memory'}),'Memory budget');assert.equal(remoteResourceLabel({kind:'disk'}),'Disk budget');
 assert.equal(remoteResourceLabel({kind:'port',id:'private-host/ports/linux:boot-private:net:[123]/tcp4/127.0.0.1:1234'}),'TCP4 port · 127.0.0.1:1234');
 assert.equal(remoteResourceLabel({kind:'custom',id:'private-catalog-id'}),'Custom resource');
});
