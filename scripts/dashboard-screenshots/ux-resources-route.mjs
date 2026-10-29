// Inventory the actual Resources route using a synthetic, read-only Backend.
// Run through the normal shared FIFO; output must remain outside the checkout.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {resolve,sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
import {chromium} from 'playwright';

const repo=fileURLToPath(new URL('../../',import.meta.url));
const output=resolve(process.argv[2]||'');
assert(process.argv[2]&&output!==resolve(repo)&&!output.startsWith(resolve(repo)+sep),
  'Supply a private output directory outside the checkout');
await mkdir(output,{recursive:true});
const files=[
  'internal/server/web/semblance/main.mjs','internal/server/web/semblance/app.ttl',
  'internal/server/web/semblance/graph-ui.mjs','internal/server/web/semblance/resources.mjs',
  'internal/server/web/metric-help.mjs','internal/server/web/route.mjs',
  'internal/server/web/peer-poll.mjs','internal/server/web/connection-links.mjs',
  'scripts/dashboard-screenshots/ux-resources-route.mjs',
  'scripts/dashboard-screenshots/ux-resources-route-backend.mjs',
];
const sources=Object.fromEntries(await Promise.all(files.map(async file=>[
  file,createHash('sha256').update(await readFile(resolve(repo,file))).digest('hex'),
])));
const result={sources,cases:[],pageErrors:[],forbiddenOperations:[],networkRequests:[],
  webSockets:0,cleanup:{},failure:null};
let server,browser,context,page;
const started=Date.now();
async function settle(){
  await page.evaluate(async()=>{
    await Promise.race([
      Promise.allSettled(document.getAnimations().filter(a=>a.playState==='running').map(a=>a.finished)),
      new Promise((_,reject)=>setTimeout(()=>reject(Error('Expansion did not settle')),2000)),
    ]);
    await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
  });
}
async function probe(locator){
  await locator.evaluate(n=>n.scrollIntoView({block:'center',inline:'nearest',behavior:'instant'}));
  await settle();
  return locator.evaluate(n=>{
    const b=n.getBoundingClientRect(),clip={left:0,right:innerWidth,top:0,bottom:innerHeight};
    for(let p=n.parentElement;p;p=p.parentElement){
      const s=getComputedStyle(p),r=p.getBoundingClientRect();
      if(/hidden|clip|auto|scroll/.test(s.overflowX)){
        clip.left=Math.max(clip.left,r.left+p.clientLeft);
        clip.right=Math.min(clip.right,r.left+p.clientLeft+p.clientWidth);
      }
      if(/hidden|clip|auto|scroll/.test(s.overflowY)){
        clip.top=Math.max(clip.top,r.top+p.clientTop);
        clip.bottom=Math.min(clip.bottom,r.top+p.clientTop+p.clientHeight);
      }
    }
    if(!n.closest('[data-sem-c="dialog"]')){
      for(const el of document.querySelectorAll('[data-sem-c="app-bar"],[data-sem-c="status-bar"]')){
        const s=getComputedStyle(el),r=el.getBoundingClientRect();
        if(!['fixed','sticky'].includes(s.position)||!r.height)continue;
        if(r.top<=1)clip.top=Math.max(clip.top,r.bottom);
        if(r.bottom>=innerHeight-1)clip.bottom=Math.min(clip.bottom,r.top);
      }
    }
    const range=document.createRange();range.selectNodeContents(n);
    const rs=[...range.getClientRects()].filter(r=>r.width&&r.height);
    const box={left:b.left,right:b.right,top:b.top,bottom:b.bottom};
    const text=rs.length?{left:Math.min(...rs.map(r=>r.left)),right:Math.max(...rs.map(r=>r.right)),
      top:Math.min(...rs.map(r=>r.top)),bottom:Math.max(...rs.map(r=>r.bottom))}:box;
    const fit=r=>r.left>=clip.left-1&&r.right<=clip.right+1&&r.top>=clip.top-1&&r.bottom<=clip.bottom+1;
    return {value:n.textContent,box,text,clip,fontSize:parseFloat(getComputedStyle(n).fontSize),
      fullBoxFits:fit(box),fullTextFits:fit(text)};
  });
}
async function geometry(){return page.evaluate(()=>({
  scrollWidth:document.documentElement.scrollWidth,scrollLeft:scrollX,
  viewport:innerWidth,bodyWidth:document.body.getBoundingClientRect().width,
  elements:[...document.querySelectorAll('*')].filter(n=>n.getClientRects().length&&
    (n.getBoundingClientRect().right+scrollX>innerWidth+1||n.getBoundingClientRect().left< -1)).map(n=>({
      component:n.getAttribute('data-sem-c'),tag:n.tagName,
      left:n.getBoundingClientRect().left,right:n.getBoundingClientRect().right,
      text:n.textContent?.slice(0,100),
    })).slice(0,20),
}));}
async function screenshot(name){await page.screenshot({path:resolve(output,name+'.png'),animations:'disabled'});}
async function openRoute(width,height,scale){
  await page.setViewportSize({width,height});
  await page.goto(server.resolvedUrls.local[0]+'?view=resources');
  await page.waitForFunction(()=>window.fixture?.connections.length===1);
  await page.getByRole('heading',{name:'Resource budgets',exact:true}).waitFor();
  await page.evaluate(scale=>{
    const root=document.documentElement,s=getComputedStyle(root);
    for(const size of ['xs','sm','md','lg','xl','2xl','3xl']){
      const n=parseFloat(s.getPropertyValue('--sem-text-'+size));
      if(Number.isFinite(n))root.style.setProperty('--sem-text-'+size,n*scale+'px');
    }
    root.style.fontSize=16*scale+'px';
  },scale);
}
async function connected(){
  await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Connected',{exact:true}).waitFor();
  await page.getByRole('button',{name:'About Live CPU headroom',exact:true}).waitFor();
}
try{
  server=await createServer({
    configFile:false,root:repo,cacheDir:resolve(output,'vite-cache'),
    resolve:{alias:[
      {find:/^\/novnc\//,replacement:resolve(repo,'internal/server/web/novnc')+'/'},
      {find:/^\/ard\//,replacement:resolve(repo,'internal/server/web/ard')+'/'},
      {find:/^oxigraph\/web\.js$/,replacement:resolve(repo,'web/arm-wat-only-store.mjs')},
      {find:'@inferal-oss/semblance/web',replacement:resolve(repo,'web/third_party/semblance/src/web/index.ts')},
      {find:'@inferal-oss/semblance',replacement:resolve(repo,'web/third_party/semblance/src/index.ts')},
    ]},
    server:{host:'127.0.0.1',port:0},
    plugins:[{
      name:'synthetic-resources-route',enforce:'pre',
      resolveId(source,importer){
        if(source==='./backend.mjs'&&importer===resolve(repo,'internal/server/web/semblance/main.mjs'))
          return resolve(repo,'scripts/dashboard-screenshots/ux-resources-route-backend.mjs');
      },
      configureServer(s){s.middlewares.use((req,res,next)=>{
        if(req.url?.split('?')[0]==='/'){
          res.setHeader('Content-Type','text/html');
          res.end('<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Synthetic Resources route</title><div id="app"></div><script type="module" src="/internal/server/web/semblance/main.mjs"></script></html>');
        }else next();
      });},
    }],
  });
  await server.listen();
  browser=await chromium.launch({headless:true});
  context=await browser.newContext({serviceWorkers:'block',locale:'en-US'});
  const origin=new URL(server.resolvedUrls.local[0]).origin;
  await context.route('**/*',route=>{
    const u=new URL(route.request().url());
    if(u.origin!==origin||u.pathname.startsWith('/v1/')){
      result.networkRequests.push(u.pathname);return route.abort();
    }
    return route.continue();
  });
  await context.addInitScript(()=>{
    sessionStorage.setItem('arm-session',JSON.stringify({token:'0'.repeat(64),expires_at:'2999-01-01T00:00:00Z'}));
    class RefusedWebSocket {static OPEN=1;constructor(){throw Error('Real WebSocket forbidden in synthetic fixture');}}
    window.WebSocket=RefusedWebSocket;
  });
  page=await context.newPage();page.setDefaultTimeout(10000);
  page.on('pageerror',e=>result.pageErrors.push(e.message));
  page.on('websocket',()=>result.webSockets++);
  for(const [width,height,scale] of [[1440,900,1],[390,900,1],[768,600,2],[390,320,1]]){
    const id=width+'-'+height+'-'+scale;
    await openRoute(width,height,scale);
    const entry={width,height,textScale:scale,help:[]};
    const initialText=await page.locator('body').innerText();
    entry.initial={authenticating:initialText.includes('Authenticating'),
      peerWaiting:initialText.includes('Waiting for peer reports'),
      resourceLoadingText:/Loading.*resource/i.test(initialText)};
    await screenshot('route-pending-'+id);
    await page.evaluate(()=>fixture.releaseReady());
    await page.waitForFunction(()=>fixture.statusPending);
    await page.evaluate(()=>fixture.releaseStatus());
    await connected();await page.waitForFunction(()=>fixture.peersPending);
    entry.pendingPeersText=await page.getByText('Partial view · some peers unavailable',{exact:true}).innerText();
    await page.evaluate(()=>fixture.releasePeers('Synthetic discovery unavailable. ' + 'discovery'.repeat(20)));
    await page.getByText(/Synthetic discovery unavailable/).waitFor();
    entry.discoveryError=await probe(page.getByText(/Synthetic discovery unavailable/));
    await screenshot('route-discovery-error-'+id);
    const filter=page.getByRole('searchbox',{name:'Filter resources',exact:true});
    await filter.focus();const original=await filter.elementHandle();
    const before=await page.evaluate(()=>fixture.statusRequests);
    await page.evaluate(()=>{fixture.statusFailure=true;fixture.refresh();});
    await page.getByText('Synthetic status unavailable',{exact:true}).waitFor();
    entry.statusError=await probe(page.getByText('Synthetic status unavailable',{exact:true}));
    entry.statusErrorRetainsFilterFocus=await original.evaluate(n=>n.isConnected&&n===document.activeElement);
    assert(entry.statusErrorRetainsFilterFocus);
    assert(await page.evaluate(()=>fixture.statusRequests)>before);
    await page.evaluate(()=>{fixture.statusFailure=false;fixture.disconnect();});
    await page.getByText('Stale view · backend disconnected',{exact:true}).waitFor();
    entry.disconnected=await probe(page.getByText('Stale view · backend disconnected',{exact:true}));
    await page.getByText('CPU headroom stale or unavailable. New CPU grants wait.',{exact:true}).waitFor();
    await screenshot('route-disconnected-'+id);
    const staleOpener=page.getByRole('button',{name:'About Live CPU headroom',exact:true});
    await staleOpener.focus();await page.keyboard.press('Enter');await settle();
    const staleDialog=page.getByRole('dialog',{name:'Live CPU headroom',exact:true});
    entry.disconnectedHelp=await probe(staleDialog.getByText('CPU availability is missing or stale. New CPU grants wait; existing promises stay charged. Unknown does not mean the host is idle.',{exact:true}));
    assert.equal(entry.disconnectedHelp.fontSize,15*scale);
    await screenshot('route-help-stale-'+id);
    await page.keyboard.press('Escape');await settle();
    entry.staleHelpRestoresOpener=await staleOpener.evaluate(n=>n===document.activeElement);
    assert(entry.staleHelpRestoresOpener);
    await filter.focus();
    await page.waitForFunction(()=>fixture.connections.length===2);
    await page.evaluate(()=>fixture.releaseReady());
    await connected();await page.getByText('Peer reports are advisory.',{exact:false}).first().waitFor();
    entry.reconnectRetainsFilterNode=await original.evaluate(n=>n.isConnected);
    entry.reconnectRetainsFilterFocus=await original.evaluate(n=>n===document.activeElement);
    const claims=page.getByRole('button',{name:'Reported claims',exact:true});
    await claims.focus();await page.keyboard.press('Enter');await settle();
    entry.claimsExpanded=await claims.getAttribute('aria-expanded')==='true';
    entry.peerSummary=await probe(page.getByText(/^UID 1000 · b{32} · Held /).first());
    entry.peerAdvisory=await probe(page.getByText(/^Peer reports are advisory\./).first());
    entry.peerDetailTitle=await probe(page.getByRole('heading',{name:'UID 1000 · '+ 'b'.repeat(32),exact:true}));
    entry.peerClaim=await probe(page.getByText(/^active · 2\.00 cores/).first());
    entry.peerQueue=await probe(page.getByText(/^queued · 2\.00 cores/).first());
    entry.peerGeometry=await geometry();
    await screenshot('route-peers-'+id);
    await claims.focus();await page.keyboard.press('Enter');await settle();
    for(const title of ['Live CPU headroom','Memory reservations','Storage reservations','Port reservations','Coordination pool']){
      const opener=page.getByRole('button',{name:'About '+title,exact:true});
      await opener.focus();await page.keyboard.press('Enter');await settle();
      const dialog=page.getByRole('dialog',{name:title,exact:true});
      await dialog.waitFor();
      const item={title,paragraphs:[],values:[]};
      for(const paragraph of await dialog.locator('.sem-body [data-sem-c="text"]').all())
        item.paragraphs.push(await probe(paragraph));
      for(const p of item.paragraphs)assert.equal(p.fontSize,15*scale);
      await screenshot('route-help-prose-'+title.toLowerCase().replaceAll(' ','-')+'-'+id);
      const technical=dialog.getByRole('button',{name:'Technical details',exact:true});
      if(await technical.count()){
        await technical.focus();await page.keyboard.press('Enter');await settle();
        for(const value of await dialog.locator('.sem-pair dd').all())item.values.push(await probe(value));
      }
      for(const v of item.values)assert.equal(v.fontSize,13*scale);
      item.panel=await dialog.evaluate(n=>{
        const r=n.getBoundingClientRect();const body=n.querySelector(':scope > .sem-body');return{width:r.width,height:r.height,top:r.top,bottom:r.bottom,viewportHeight:innerHeight,bodyClientHeight:body?.clientHeight,bodyScrollHeight:body?.scrollHeight};
      });
      await screenshot('route-help-'+title.toLowerCase().replaceAll(' ','-')+'-'+id);
      item.closeBounds=await probe(dialog.getByRole('button',{name:'Close',exact:true}));
      await page.keyboard.press('Escape');await settle();
      item.escapeRestoresOpener=await opener.evaluate(n=>n===document.activeElement);
      assert(item.escapeRestoresOpener,'Native help Escape must restore opener');
      assert.equal(await dialog.count(),0,'Closed help must dispose its original native dialog');
      item.closedGeometry=await geometry();
      entry.help.push(item);
    }
    entry.documentWidth=await page.evaluate(()=>document.documentElement.scrollWidth);
    entry.overflowing=await page.evaluate(()=>[...document.querySelectorAll('*')].filter(n=>n.getClientRects().length&&n.getBoundingClientRect().right>innerWidth+1).map(n=>({component:n.getAttribute('data-sem-c'),tag:n.tagName,right:n.getBoundingClientRect().right,text:n.textContent?.slice(0,100)})).slice(0,20));
    entry.forbidden=await page.evaluate(()=>fixture.forbidden);
    result.forbiddenOperations.push(...entry.forbidden);
    result.cases.push(entry);
  }
  await openRoute(390,900,1);
  await page.evaluate(()=>{fixture.statusFailure=true;fixture.releaseReady();});
  await page.getByText(/Synthetic status unavailable\. If the backend restarted/).waitFor();
  const initialError=await probe(page.getByText(/Synthetic status unavailable\. If the backend restarted/));
  await screenshot('route-initial-status-error-390');
  result.initialStatusError={initialError,retainedResources:await page.locator('[data-sem-c="arm-resource-surface"] > [data-sem-c="card"]').count()};
  assert.deepEqual(result.pageErrors,[]);assert.deepEqual(result.forbiddenOperations,[]);
  assert.deepEqual(result.networkRequests,[]);assert.equal(result.webSockets,0);
}catch(error){result.failure=error.message;result.diagnostic=await page?.evaluate(()=>({text:document.body.innerText,fixture:window.fixture?{connections:fixture.connections.length,calls:fixture.calls}:null})).catch(()=>null);await page?.screenshot({path:resolve(output,'failure.png')}).catch(()=>{});process.exitCode=1;}
finally{
  try{await context?.close();result.cleanup.contextClosed=true;}
  finally{try{await browser?.close();result.cleanup.browserClosed=true;}
    finally{await server?.close();result.cleanup.serverJoined=true;}}
  result.elapsedMS=Date.now()-started;
  await writeFile(resolve(output,'resources-route.json'),JSON.stringify(result,null,2));
}
console.log(JSON.stringify({cases:result.cases.length,failure:result.failure,cleanup:result.cleanup}));
