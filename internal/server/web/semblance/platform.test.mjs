import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {markKeyboardPlatform} from './platform.mjs';

function fixture(initial) {
 const attributes=new Map(initial?[['data-sem-platform',initial]]:[]);
 return {documentElement:{setAttribute:(k,v)=>attributes.set(k,v),removeAttribute:k=>attributes.delete(k)},attributes};
}
test('stock Apple keyboard marker covers Mac and mobile Apple platform strings',()=>{
 for(const platform of ['MacIntel','MacPPC','iPhone','iPad']){
  const document=fixture();markKeyboardPlatform(document,platform);
  assert.equal(document.attributes.get('data-sem-platform'),'apple');
 }
});
test('other or unknown platforms clear stale Apple hints',()=>{
 for(const platform of ['Win32','Linux x86_64','',undefined]){
  const document=fixture('apple');markKeyboardPlatform(document,platform);
  assert.equal(document.attributes.has('data-sem-platform'),false);
 }
});
test('the real bootstrap sets the stock marker before the runtime renders',()=>{
 const main=readFileSync(new URL('./main.mjs',import.meta.url),'utf8');
 assert.ok(main.indexOf('markKeyboardPlatform(document,navigator.platform);')>0);
 assert.ok(main.indexOf('markKeyboardPlatform(document,navigator.platform);')<main.indexOf('await mount('));
});
