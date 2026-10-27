import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync,readdirSync,mkdirSync,mkdtempSync,writeFileSync,rmSync} from 'node:fs';
import {join,resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {createHash} from 'node:crypto';
import {runtimeNotices,embeddedNotices} from '../license-notices.mjs';
const root=resolve(import.meta.dirname,'..'), hash=b=>createHash('sha256').update(b).digest('hex');

test('all shipped Semblance source paths have exact frozen combined provenance; excluded agent data stays absent',()=>{
 const p=JSON.parse(readFileSync(join(root,'semblance-provenance.json'))), source=join(root,p.source_directory);
 assert.equal(Object.keys(p.base_files).length,369);assert.equal(Object.keys(p.files).length,373);assert.equal(Object.keys(p.shipped_files).length,368);assert.equal(Object.keys(p.excluded_upstream_paths).length,5);
 for(const [path,sha] of Object.entries(p.shipped_files)){assert.equal(sha,p.files[path]);assert.equal(hash(readFileSync(join(source,path))),sha,path);}
 const walk=(dir,prefix='')=>readdirSync(dir,{withFileTypes:true}).flatMap(e=>e.isDirectory()?walk(join(dir,e.name),prefix+e.name+'/'):[prefix+e.name]);
 const generated=p.generated_engine.file.replace(p.source_directory+'/','');
 assert.deepEqual(walk(source).sort(),[...Object.keys(p.shipped_files),generated].sort());
 for(const [path,entry] of Object.entries(p.excluded_upstream_paths)){assert.equal(entry.sha256,p.files[path]);assert.ok(path.startsWith('.agents/'));assert.ok(!walk(source).includes(path));}
 assert.equal(hash(readFileSync(join(root,p.generated_engine.file))),p.generated_engine.sha256);
});

test('all locked runtime notices are complete and embedded; altered/missing/additional packages fail closed',()=>{
 const text=embeddedNotices(root), manifest=JSON.parse(readFileSync(join(root,'licenses/runtime-manifest.json')));assert.equal(manifest.packages.length,12);
 for(const p of [...manifest.packages,...manifest.build_helpers]) for(const path of Object.keys(p.files))assert.ok(text.includes(readFileSync(join(root,path),'utf8')));
 const fixture=mkdtempSync(join(tmpdir(),'arm-ui-license-'));
 try{
  mkdirSync(join(fixture,'licenses/runtime'),{recursive:true});
  const lock=JSON.parse(readFileSync(join(root,'package-lock.json')));writeFileSync(join(fixture,'package-lock.json'),JSON.stringify(lock));
  writeFileSync(join(fixture,'licenses/runtime-manifest.json'),JSON.stringify(manifest));
  assert.throws(()=>runtimeNotices(fixture),/ENOENT/);
  for(const p of [...manifest.packages,...manifest.build_helpers])for(const path of Object.keys(p.files)){mkdirSync(resolve(fixture,path,'..'),{recursive:true});writeFileSync(join(fixture,path),readFileSync(join(root,path)));}
  assert.equal(runtimeNotices(fixture),runtimeNotices(root));
  const firstPath=Object.keys(manifest.packages[0].files)[0];writeFileSync(join(fixture,firstPath),'altered notice');assert.throws(()=>runtimeNotices(fixture),/hash mismatch/);writeFileSync(join(fixture,firstPath),readFileSync(join(root,firstPath)));

  lock.packages['node_modules/unknown-runtime']={version:'1',integrity:'bad'};writeFileSync(join(fixture,'package-lock.json'),JSON.stringify(lock));assert.throws(()=>runtimeNotices(fixture),/inventory/);
  delete lock.packages['node_modules/unknown-runtime'];lock.packages['node_modules/n3'].integrity='changed';writeFileSync(join(fixture,'package-lock.json'),JSON.stringify(lock));
  const n3=manifest.packages.find(p=>p.package==='n3');manifest.packages=[n3,...manifest.packages.filter(p=>p!==n3)];writeFileSync(join(fixture,'licenses/runtime-manifest.json'),JSON.stringify(manifest));assert.throws(()=>runtimeNotices(fixture),/pin mismatch/);
 }finally{rmSync(fixture,{recursive:true,force:true});}
});

test('the ARM-only build preserves upstream hashes, selects WAT and refuses the optional store at its initialization boundary',async()=>{
 const p=JSON.parse(readFileSync(join(root,'semblance-provenance.json'))),overlay=p.build_overlays[0];
 assert.equal(p.build_overlays.length,1);assert.equal(overlay.import,'oxigraph/web.js');assert.equal(overlay.selected_product_store,'wat');
 for(const [file,sha]of [[overlay.replacement,overlay.replacement_sha256],[overlay.configuration,overlay.configuration_sha256]])assert.equal(hash(readFileSync(join(root,file))),sha);
 const source=readFileSync(join(root,'../internal/server/web/semblance/main.mjs'),'utf8');assert.equal((source.match(/\bmount\(/g)||[]).length,1);assert.match(source,/store:'wat',files:false/);
 const loader=readFileSync(join(root,'third_party/semblance/src/web/load.ts'),'utf8');assert.match(loader,/await import\('oxigraph\/web.js'\)/);assert.match(loader,/await web\.default\(\)/);
 const stub=await import('../arm-wat-only-store.mjs');assert.deepEqual(Object.keys(stub),['default']);await assert.rejects(stub.default(),/pinned WAT store only/);
});
