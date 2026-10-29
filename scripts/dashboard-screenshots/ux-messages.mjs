// Synthetic native rendering check. Run through the shared test queue; output
// belongs in a private directory outside the checkout. No ARM backend is used.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {resolve,sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
import {chromium} from 'playwright';

const repo=fileURLToPath(new URL('../../',import.meta.url)),output=resolve(process.argv[2]||'');
assert(process.argv[2]&&output!==resolve(repo)&&!output.startsWith(resolve(repo)+sep),'Supply a private output directory outside the checkout');
await mkdir(output,{recursive:true});
const sourceFiles=['internal/server/web/semblance/messages.mjs','internal/server/web/semblance/app.ttl','internal/server/web/semblance/graph-ui.mjs','internal/server/web/operator-messaging.mjs','scripts/dashboard-screenshots/ux-messages.mjs'];
const sha256=bytes=>createHash('sha256').update(bytes).digest('hex');
const sources=Object.fromEntries(await Promise.all(sourceFiles.map(async file=>[file,sha256(await readFile(resolve(repo,file)))])));
const imports=file=>'/@fs'+resolve(repo,file);
const fixture=`
import {mount} from '@inferal-oss/semblance/web';
import {createGraphUI,ARM} from '${imports('internal/server/web/semblance/graph-ui.mjs')}';
import graph from '${imports('internal/server/web/semblance/app.ttl')}?raw';
import {createOperatorMessaging} from '${imports('internal/server/web/semblance/messages.mjs')}';
const app=await mount(document.getElementById('app'),{root:ARM+'shell',graphs:[graph],store:'wat',prefer:'sparql',onHandled:h=>ui?.handled(h)}),ui=createGraphUI(app);
ui.put(ARM+'shell',ARM+'view',ARM+'messages',true);ui.bind('heading').set({label:'Operator messages'});
const contact={id:'synthetic-worker-'+ 'a'.repeat(64),adapter:'generic',session:'11111111-1111-4111-8111-111111111111',registration:'synthetic-registration-'+ 'b'.repeat(64),delivery:'poll',cwd:'/workspace/'+ 'long-project-name-'.repeat(10),config_dir:'/workspace/configuration',idle:false};
const body='First paragraph.\\n\\nSecond paragraph.\\n'+ 'identifier'.repeat(50);
const message={id:'synthetic-message',sender:contact.id,sender_contact:contact,audience:'operator',created_at:'2026-10-01T12:00:00Z',body,deliveries:{[contact.id]:{state:'unknown',error:'Synthetic transport unavailable',receipt:{text:'First receipt line.\\nSecond receipt line.'}}}};
const unknown=new URL(location.href).searchParams.has('unknown'),request={idempotency_key:'synthetic-request-'+ 'c'.repeat(64),audience:contact.id,body,targets:[contact]};
const calls=[];let online=true,data={contacts:[contact],messages:[message],leases:[]};
const storage={getItem:()=>unknown?JSON.stringify([{request}]):null,setItem(){}};
const panel=createOperatorMessaging({ui,root:ui.bind('messagesContent'),connected:()=>online,storage,key:()=>request.idempotency_key,call:async(...args)=>{calls.push(args);throw Error('Unexpected synthetic transport call');}});panel.update(data);
const dialog=ui.dialog('Synthetic message detail',[ui.descriptions([['Request key',request.idempotency_key],['Recipient',contact.id]])]);
const open=ui.button('Inspect synthetic detail',()=>dialog.showModal(open));ui.bind('messagesContent').append(open);dialog.on('close',()=>{});
window.fixture={body,message,contact,request,calls,setOnline(value){online=value;panel.updateConnection();},refresh(){data={...data,messages:[{...message,body:body+' Updated'}]};panel.update(data);},stale(){data={...data,contacts:[{...contact,registration:'replacement-registration'}]};panel.update(data);},empty(){data={contacts:[],messages:[],leases:[]};panel.update(data);},dispose(){panel.dispose();dialog.dispose();app.unmount();}};window.ready=true;
`;
let browser,context,server,page;const cases=[],started=Date.now();let cleanup=false,failure=null;
try{
 server=await createServer({configFile:false,root:repo,cacheDir:resolve(output,'vite-cache'),resolve:{alias:[{find:/^oxigraph\/web\.js$/,replacement:resolve(repo,'web/arm-wat-only-store.mjs')},{find:'@inferal-oss/semblance/web',replacement:resolve(repo,'web/third_party/semblance/src/web/index.ts')},{find:'@inferal-oss/semblance',replacement:resolve(repo,'web/third_party/semblance/src/index.ts')}]},server:{host:'127.0.0.1',port:0},plugins:[{name:'synthetic-messages',resolveId(id){if(id==='/ux-messages-fixture.mjs')return '\0ux-messages-fixture';},load(id){if(id==='\0ux-messages-fixture')return fixture;},configureServer(server){server.middlewares.use((req,res,next)=>{const path=new URL(req.url,'http://fixture').pathname;if(path==='/ux-messages'){res.setHeader('Content-Type','text/html');res.end('<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Synthetic message layout</title></head><body><div id="app"></div><script type="module" src="/ux-messages-fixture.mjs"></script></body></html>');}else next();});}}]});
 await server.listen();const origin=server.resolvedUrls.local[0];
 browser=await chromium.launch({headless:true});context=await browser.newContext({serviceWorkers:'block'});
 await context.route('**/*',route=>new URL(route.request().url()).origin===new URL(origin).origin?route.continue():route.abort());
 page=await context.newPage();page.setDefaultTimeout(10000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
 async function load(suffix=''){await page.goto(origin+'ux-messages'+suffix);await page.waitForFunction(()=>window.ready).catch(error=>{throw Error(errors.join(' | ')||error.message);});}
 async function geometry(){return page.evaluate(()=>({viewport:innerWidth,document:document.documentElement.scrollWidth,bodyStyles:[...document.querySelectorAll('[data-sem-c="arm-message-body"]')].map(n=>({whiteSpace:getComputedStyle(n).whiteSpace,wrap:getComputedStyle(n).overflowWrap})),facts:[...document.querySelectorAll('[data-sem-c="arm-message-facts"]')].map(n=>({width:n.getBoundingClientRect().width,scroll:n.scrollWidth}))}));}
 const getButton=label=>page.getByRole('button',{name:label,exact:true});
 for(const [width,height,scale]of [[320,640,1],[390,844,1],[768,600,1],[1440,900,1],[1920,1080,1],[768,600,2]]){
  await page.setViewportSize({width,height});await load();await page.evaluate(scale=>document.documentElement.style.fontSize=16*scale+'px',scale);
  const message=page.locator('[data-sem-c="arm-message-body"]').first();assert.equal(await message.textContent(),await page.evaluate(()=>fixture.body));
  const reply=getButton('Reply');await reply.focus();const original=await reply.elementHandle();await page.evaluate(()=>fixture.refresh());assert(await original.evaluate(n=>n===document.activeElement&&n.isConnected));
  await reply.click();await page.getByRole('textbox',{name:'Message',exact:true}).fill('Please inspect the output.\n\nKeep the exact recipient and request.');
  await getButton('Review message').click();await getButton('Back to editing').waitFor();assert(await getButton('Back to editing').evaluate(n=>document.activeElement===n));
  const pairs=await page.locator('[data-sem-c="arm-message-facts"] .sem-pair').evaluateAll(nodes=>nodes.map(n=>[n.querySelector('dt').textContent,n.querySelector('dd').textContent]));
  assert(pairs.some(([key,value])=>key==='Registration'&&value==='synthetic-registration-'+ 'b'.repeat(64)));
  const reviewBody=page.locator('[data-sem-c="arm-message-body"]').filter({visible:true}).first();assert.equal(await reviewBody.textContent(),'Please inspect the output.\n\nKeep the exact recipient and request.');
  const frame=await geometry();assert(frame.document<=frame.viewport+1);assert(frame.bodyStyles.every(n=>n.whiteSpace==='pre-wrap'&&n.wrap==='anywhere'));assert(frame.facts.every(n=>n.scroll<=n.width+1));
  await reviewBody.scrollIntoViewIfNeeded();await page.screenshot({path:resolve(output,`messages-review-${width}-${scale}.png`),animations:'disabled'});
  await page.keyboard.press('Tab');assert(await getButton('Confirm send').evaluate(n=>document.activeElement===n));await page.keyboard.press('Escape');assert.equal(await page.evaluate(()=>fixture.calls.length),0);
  await page.evaluate(()=>fixture.setOnline(false));assert(await getButton('Confirm send').isDisabled());await page.evaluate(()=>fixture.setOnline(true));await page.evaluate(()=>fixture.stale());assert(await getButton('Confirm send').isDisabled());
  await getButton('Back to editing').click();assert(await getButton('Review message').evaluate(n=>document.activeElement===n));assert.equal(await page.getByRole('textbox',{name:'Message',exact:true}).inputValue(),'Please inspect the output.\n\nKeep the exact recipient and request.');
  await getButton('Inspect synthetic detail').click();await page.getByRole('dialog').waitFor();const bounds=await page.getByRole('dialog').boundingBox();assert(bounds.x>=-1&&bounds.x+bounds.width<=width+1);await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'hidden'});assert(await getButton('Inspect synthetic detail').evaluate(n=>n===document.activeElement));
  await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:resolve(output,`messages-${width}-${scale}.png`),animations:'disabled'});
  cases.push({width,height,textScale:scale,paragraphs:true,exactAuthority:true,focusRefresh:true,reviewKeyboard:true,staleOffline:true,dialogContainment:true,escapeReturn:true,geometry:frame});await page.evaluate(()=>fixture.dispose());
 }
 await page.setViewportSize({width:390,height:640});await load('?unknown');assert(await page.getByText('Outcome uncertain',{exact:false}).count());assert(await page.getByText('Restored after reload. Check the stored result before retrying.',{exact:true}).isVisible());
 await page.evaluate(()=>fixture.setOnline(false));assert(await getButton('Retry exact request').isDisabled());assert(await getButton('Check stored result').isDisabled());assert.equal(await page.evaluate(()=>fixture.calls.length),0);await page.getByText('Saved exact request',{exact:true}).click();await page.waitForFunction(()=>[...document.querySelectorAll('[data-sem-c="code"]')].some(n=>n.getClientRects().length&&n.textContent.includes('idempotency_key')&&n.textContent.includes('synthetic-request-'+ 'c'.repeat(64)))); // Full request stays discoverable through the stock disclosure.
 const uncertain=await geometry();assert(uncertain.document<=uncertain.viewport+1);await page.screenshot({path:resolve(output,'messages-uncertain.png'),animations:'disabled'});cases.push({state:'restored-unknown-offline',geometry:uncertain,transportCalls:0});await page.evaluate(()=>fixture.dispose());
 await load();await page.evaluate(()=>fixture.empty());assert(await page.getByText('No messages yet. Agents can write with arm message operator.',{exact:true}).isVisible());await getButton('Review message').click();assert(await page.getByText('Choose a registered agent or Broadcast.',{exact:true}).isVisible());assert.equal(await page.evaluate(()=>fixture.calls.length),0);cases.push({state:'empty-validation-error',transportCalls:0});await page.evaluate(()=>fixture.dispose());
 assert.deepEqual(errors,[]);assert(Date.now()-started<120000,'Fixture exceeded its bounded validation window');
}catch(error){failure=error.message;throw error;}finally{
 try{await context?.close();}finally{try{await browser?.close();}finally{await server?.close();}}cleanup=true;
 await writeFile(resolve(output,'messages-layout.json'),JSON.stringify({sources,cases,failure,elapsedMS:Date.now()-started,cleanup:{browserClosed:cleanup,contextClosed:cleanup,serverJoined:cleanup}},null,2));
}
console.log(JSON.stringify({cases:cases.length,transportCalls:0,browserClosed:cleanup,serverJoined:cleanup}));
