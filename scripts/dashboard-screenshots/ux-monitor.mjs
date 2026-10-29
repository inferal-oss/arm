// Read-only actual Monitor route inventory; no product source or live transport.
// Run through the normal shared FIFO; output must remain outside the checkout.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {resolve,sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
import {chromium} from 'playwright';

const repo=fileURLToPath(new URL('../../',import.meta.url)),output=resolve(process.argv[2]||''),cohort=process.argv[3]||'help';
assert(['help','states'].includes(cohort));
assert(process.argv[2]&&output!==resolve(repo)&&!output.startsWith(resolve(repo)+sep),'Use a private output directory outside the checkout');
await mkdir(output,{recursive:true});
const files=['internal/server/web/semblance/main.mjs','internal/server/web/semblance/app.ttl','internal/server/web/semblance/graph-ui.mjs','internal/server/web/metric-help.mjs','internal/server/web/monitor-capacity.mjs','internal/server/web/io-devices.mjs','internal/model/monitor.go','internal/model/measurement.go','web/third_party/semblance/ontology/layers/tooltip.ttl','scripts/dashboard-screenshots/ux-monitor.mjs','scripts/dashboard-screenshots/ux-monitor-backend.mjs'];
const sources=Object.fromEntries(await Promise.all(files.map(async file=>[file,createHash('sha256').update(await readFile(resolve(repo,file))).digest('hex')])));
const result={sources,cohort,mode:'read-only-inventory-not-product-acceptance',cases:[],pageErrors:[],networkRequests:[],webSockets:0,cleanup:{},failure:null};
let server,browser,context,page,cdp;
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
const fits=value=>value.fullBoxFits&&value.fullTextFits;
async function nativeTooltip(opener){
  const style=await opener.evaluate(n=>{
    const s=getComputedStyle(n,'::after');return{id:n.id,label:n.getAttribute('data-sem-tooltip'),aria:n.getAttribute('aria-label'),
      content:s.content,left:s.left,right:s.right,transform:s.transform,boxSizing:s.boxSizing,maxWidth:s.maxWidth,
      font:s.font,overflowWrap:s.overflowWrap,textOverflow:s.textOverflow,focusVisible:n.matches(':focus-visible'),hovered:n.matches(':hover')};
  });
  if(style.content==='none'||style.content==='normal')return{style,rendered:false,border:[],textQuads:[],fullBoxFits:true,fullTextFits:true,geometry:await geometry()};
  const {root}=await cdp.send('DOM.getDocument');
  const {nodeId}=await cdp.send('DOM.querySelector',{nodeId:root.nodeId,selector:'[data-sem-c="icon-button"][aria-label='+JSON.stringify(style.aria)+']'});
  assert(nodeId,'Native tooltip opener must resolve in the actual DOM');
  const {node}=await cdp.send('DOM.describeNode',{nodeId,depth:3,pierce:true});
  const pseudo=node.pseudoElements?.find(n=>n.pseudoType==='after');assert(pseudo,'Native tooltip pseudo-element must render');
  const {model}=await cdp.send('DOM.getBoxModel',{backendNodeId:pseudo.backendNodeId});
  const snapshot=await cdp.send('DOMSnapshot.captureSnapshot',{computedStyles:[],includeDOMRects:true});
  const doc=snapshot.documents[0],nodes=doc.nodes,layout=doc.layout;
  const rootIndex=nodes.backendNodeId.indexOf(pseudo.backendNodeId);assert(rootIndex>=0,'Native tooltip must occur in the captured layout tree');const wanted=new Set([rootIndex]);
  for(let i=rootIndex+1;i<nodes.parentIndex.length;i++)if(wanted.has(nodes.parentIndex[i]))wanted.add(i);
  const textIndices=new Set(layout.nodeIndex.map((n,i)=>wanted.has(n)&&snapshot.strings[layout.text[i]]?i:-1).filter(i=>i>=0));
  const text=[...textIndices].map(i=>snapshot.strings[layout.text[i]]).join('');
  result.lastNativeTooltip={style,pseudo,rootIndex,text,textIndices:[...textIndices]};
  assert.equal(text,style.label,'Complete native tooltip text must match the accessible label');
  const quads=[];
  for(let i=0;i<doc.textBoxes.layoutIndex.length;i++)if(textIndices.has(doc.textBoxes.layoutIndex[i])){
    const [x,y,w,h]=doc.textBoxes.bounds[i],left=x-doc.scrollOffsetX,top=y-doc.scrollOffsetY;
    quads.push([left,top,left+w,top,left+w,top+h,left,top+h]);
  }
  assert(quads.length,'Native tooltip text must have painted line bounds');

  const viewport=page.viewportSize(),fitsQuad=q=>q.every((v,i)=>v>=-1&&v<=(i%2?viewport.height:viewport.width)+1);
  return{style,rendered:true,border:model.border,textQuads:quads,fullBoxFits:fitsQuad(model.border),fullTextFits:quads.every(fitsQuad),geometry:await geometry()};
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

const card=title=>page.locator('[data-sem-c="card"]').filter({has:page.getByRole('heading',{name:title,exact:true})}).last();
const stat=label=>page.locator('[data-sem-c="stat"]').filter({has:page.locator('.sem-label',{hasText:label})}).first();
const slug=name=>name.toLowerCase().replace(/[^a-z0-9]+/g,'-');
async function capture(name){await page.screenshot({path:resolve(output,name+'.png'),animations:'disabled'});}
async function plot(name){return page.getByRole('img',{name,exact:true}).evaluate(n=>({
  dots:n.querySelectorAll('.sem-sparkline-dot').length,lines:n.querySelectorAll('.sem-sparkline-line').length,
  values:[...n.querySelectorAll('.sem-sparkline-dot')].map(d=>d.getAttribute('data-sem-tooltip')),
}));}
async function openMonitor(width,height,scale){
  await page.setViewportSize({width,height});await page.goto(server.resolvedUrls.local[0]+'?view=monitor');
  await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Connected',{exact:true}).waitFor();
  await page.getByRole('button',{name:'About CPU activity',exact:true}).waitFor();
  await page.getByText(/^Live host observations/).waitFor();
  await page.evaluate(scale=>{
    const root=document.documentElement,s=getComputedStyle(root);
    for(const size of ['xs','sm','md','lg','xl','2xl','3xl']){
      const n=parseFloat(s.getPropertyValue('--sem-text-'+size));if(Number.isFinite(n))root.style.setProperty('--sem-text-'+size,n*scale+'px');
    }root.style.fontSize=16*scale+'px';
  },scale);await settle();
}
async function connected(){await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Connected',{exact:true}).waitFor();}
async function rates(root){return root.locator('[data-sem-c="description-list"]').first().evaluate(n=>[...n.querySelectorAll('.sem-pair')].map(p=>[p.querySelector('dt').textContent,p.querySelector('dd').textContent]));}
async function selection(root){return root.getByRole('checkbox').evaluateAll(nodes=>nodes.map(n=>({label:n.labels[0]?.textContent,checked:n.checked})));}
async function helpInventory(width,height,scale){
  const item={width,height,textScale:scale,help:[]};
  for(const label of ['All-core activity','RAM remaining','Space remaining'])item[label]=await probe(stat(label).locator('.sem-value'));
  const coreToggle=card('CPU activity').getByRole('button',{name:'Per-core activity',exact:true});
  await coreToggle.focus();await page.keyboard.press('Enter');await settle();
  item.coreCount=await card('CPU activity').getByRole('progressbar').count();
  assert.equal(item.coreCount,32);
  item.coreBounds=await probe(card('CPU activity').getByRole('progressbar').last());
  await capture('monitor-cores-'+width+'-'+height+'-'+scale);
  await coreToggle.click();await settle();
  for(const title of ['Disk I/O','Network']){
    const root=card(title),toggle=root.getByRole('button',{name:'Choose devices',exact:true});
    await toggle.focus();await page.keyboard.press('Enter');await settle();
    item[title]={count:await root.getByRole('checkbox').count(),firstLabel:await probe(root.locator('label').first()),lastLabel:await probe(root.locator('label').last()),allControl:await probe(root.getByRole('button',{name:'All',exact:true})),noneControl:await probe(root.getByRole('button',{name:'None',exact:true}))};
    assert.equal(item[title].count,12);
    await capture('monitor-devices-'+slug(title)+'-'+width+'-'+height+'-'+scale);
    await toggle.click();await settle();
  }
  for(const title of ['CPU activity','Memory & swap','Storage space','Disk activity','Network activity','GPU monitoring']){
    const opener=page.getByRole('button',{name:'About '+title,exact:true});
    await page.mouse.move(1,1);await page.keyboard.press('Tab');await opener.focus();await settle();
    const record={title,tooltip:await nativeTooltip(opener),paragraphs:[],labels:[],values:[]};assert(record.tooltip.rendered,'Keyboard tooltip must actually render for inventory');
    await page.getByRole('checkbox',{name:'Pause monitor display',exact:true}).focus();
    await opener.evaluate(n=>n.scrollIntoView({block:'center',inline:'nearest',behavior:'instant'}));await opener.hover();await settle();record.hoverTooltip=await nativeTooltip(opener);assert(record.hoverTooltip.rendered&&record.hoverTooltip.style.hovered);
    await page.keyboard.press('Tab');await opener.focus();await settle();
    await capture('monitor-tooltip-'+slug(title)+'-'+width+'-'+height+'-'+scale);
    await page.keyboard.press('Enter');await settle();const dialog=page.getByRole('dialog',{name:title,exact:true});await dialog.waitFor();
    for(const text of await dialog.locator('.sem-body [data-sem-c="text"]').all())record.paragraphs.push(await probe(text));
    for(const text of record.paragraphs)assert.equal(text.fontSize,15*scale);
    await capture('monitor-help-prose-'+slug(title)+'-'+width+'-'+height+'-'+scale);
    const technical=dialog.getByRole('button',{name:'Technical details',exact:true});
    if(await technical.count()){
      await technical.focus();await page.keyboard.press('Enter');await settle();
      for(const label of await dialog.locator('.sem-pair dt').all())record.labels.push(await probe(label));
      for(const value of await dialog.locator('.sem-pair dd').all())record.values.push(await probe(value));
    }
    for(const value of record.values)assert.equal(value.fontSize,13*scale);
    record.panel=await dialog.evaluate(n=>{const r=n.getBoundingClientRect(),s=getComputedStyle(n);return{left:r.left,right:r.right,top:r.top,bottom:r.bottom,readingHeight:n.clientHeight-parseFloat(s.paddingTop)-parseFloat(s.paddingBottom)};});
    await capture('monitor-help-details-'+slug(title)+'-'+width+'-'+height+'-'+scale);
    record.close=await probe(dialog.getByRole('button',{name:'Close',exact:true}));
    if(title==='Disk activity')await dialog.getByRole('button',{name:'Close',exact:true}).click();else await page.keyboard.press('Escape');
    await settle();record.originalOpenerRestored=await opener.evaluate(n=>n===document.activeElement);record.disposed=await dialog.count()===0;
    assert(record.originalOpenerRestored&&record.disposed);
    item.help.push(record);
  }
  item.document=await geometry();return item;
}
async function stateInventory(width,height,scale){
  const item={width,height,textScale:scale,initialPlots:{}};
  for(const name of ['CPU activity over time','Available RAM over time','Available filesystem space over time','Disk I/O read','Disk I/O write','Network receive','Network send'])item.initialPlots[name]=await plot(name);
  item.expectedGapCounts={cpuDots11Lines9:item.initialPlots['CPU activity over time'].dots===11&&item.initialPlots['CPU activity over time'].lines===9,diskReadDots10Lines7:item.initialPlots['Disk I/O read'].dots===10&&item.initialPlots['Disk I/O read'].lines===7,networkSendDots11Lines9:item.initialPlots['Network send'].dots===11&&item.initialPlots['Network send'].lines===9};
  const pause=page.getByRole('checkbox',{name:'Pause monitor display',exact:true});
  item.initialCPU=await stat('All-core activity').locator('.sem-value').innerText();await pause.check();const pauseNode=await pause.elementHandle();
  await page.evaluate(()=>monitorFixture.publish({state:'zero'}));await settle();item.pausedCPU=await stat('All-core activity').locator('.sem-value').innerText();item.pauseRetainsReading=item.initialCPU===item.pausedCPU;item.pauseFocusRetained=await pauseNode.evaluate(n=>n===document.activeElement);
  await pause.uncheck();await settle();item.resumedCPU=await stat('All-core activity').locator('.sem-value').innerText();item.zeroMemory=await stat('RAM remaining').locator('.sem-value').innerText();
  await page.evaluate(()=>monitorFixture.publish({state:'fresh'}));await settle();
  for(const [title,key] of [['Disk I/O','disk'],['Network','network']]){
    const root=card(title),toggle=root.getByRole('button',{name:'Choose devices',exact:true});await toggle.focus();await page.keyboard.press('Enter');await settle();
    const record={allInitial:await selection(root)};
    await root.getByRole('button',{name:'None',exact:true}).click();await settle();record.none=await selection(root);record.noneRates=await rates(root);
    const first=root.getByRole('checkbox').first();await first.focus();await page.keyboard.press('Space');await settle();const original=await first.elementHandle();
    record.custom=await selection(root);record.customRates=await rates(root);
    await page.evaluate(()=>monitorFixture.publish({membership:'removed'}));await settle();
    const missing=root.getByRole('checkbox',{name:key+'-synthetic-00 (unavailable)',exact:true});await missing.waitFor();
    record.missing=await selection(root);record.missingRates=await rates(root);record.missingIdentityRetained=await original.evaluate(n=>n.isConnected&&n.checked);record.missingFocusRetained=await original.evaluate(n=>n===document.activeElement);
    if(key==='disk'){
      await page.evaluate(()=>monitorFixture.disconnect());await page.getByText(/^Stale observations/).waitFor();record.disconnectedCPU=await stat('All-core activity').locator('.sem-value').innerText();record.disconnectedRAM=await stat('RAM remaining').locator('.sem-value').innerText();record.disconnectedSpace=await stat('Space remaining').locator('.sem-value').innerText();
      record.disconnectedBounds={};
      for(const label of ['All-core activity','RAM remaining','Space remaining']){record.disconnectedBounds[label]=await probe(stat(label).locator('.sem-value'));await capture('monitor-disconnected-'+slug(label)+'-'+width+'-'+height+'-'+scale);}
      await page.waitForFunction(()=>monitorFixture.connections.length===2);await page.evaluate(()=>monitorFixture.releaseReconnect());await connected();await settle();
      record.reconnectIdentityRetained=await original.evaluate(n=>n.isConnected&&n.checked);record.reconnectFocusRetained=await original.evaluate(n=>n===document.activeElement);
    }
    await page.evaluate(()=>monitorFixture.publish({membership:'returned'}));await settle();
    record.returned=await selection(root);record.returnedRates=await rates(root);record.returnedIdentityRetained=await original.evaluate(n=>n.isConnected&&n.checked);record.returnedFocusRetained=await original.evaluate(n=>n===document.activeElement);
    await root.getByRole('button',{name:'All',exact:true}).click();await settle();record.allIncludingNew=await selection(root);
    await page.evaluate(()=>monitorFixture.publish({state:'reset'}));await settle();record.resetRates=await rates(root);record.resetPlot=await plot(title+(key==='disk'?' read':' receive'));
    await page.evaluate(()=>monitorFixture.publish({state:'zero'}));await settle();record.zeroRates=await rates(root);
    await page.evaluate(()=>monitorFixture.publish({state:'fresh',membership:'initial'}));await settle();await toggle.click();await settle();item[title]=record;
  }
  await page.evaluate(()=>monitorFixture.publish({state:'unknown'}));await settle();item.unknown={cpu:await stat('All-core activity').locator('.sem-value').innerText(),ram:await stat('RAM remaining').locator('.sem-value').innerText(),space:await stat('Space remaining').locator('.sem-value').innerText()};
  item.unknown.bounds={};
  for(const label of ['All-core activity','RAM remaining','Space remaining']){item.unknown.bounds[label]=await probe(stat(label).locator('.sem-value'));await capture('monitor-unknown-'+slug(label)+'-'+width+'-'+height+'-'+scale);}
  await page.evaluate(()=>monitorFixture.publish({state:'stale'}));await page.getByText(/^Stale observations/).waitFor();await settle();item.stale={cpu:await stat('All-core activity').locator('.sem-value').innerText(),ram:await stat('RAM remaining').locator('.sem-value').innerText(),space:await stat('Space remaining').locator('.sem-value').innerText(),warning:await probe(page.getByText(/^Stale observations/))};
  item.stale.bounds={};
  for(const label of ['All-core activity','RAM remaining','Space remaining']){item.stale.bounds[label]=await probe(stat(label).locator('.sem-value'));await capture('monitor-stale-'+slug(label)+'-'+width+'-'+height+'-'+scale);}
  await page.evaluate(()=>monitorFixture.publish({state:'empty'}));await settle();const emptyToggle=card('Disk I/O').getByRole('button',{name:'Choose devices',exact:true});await emptyToggle.click();await settle();item.emptyDisk={checkboxes:await card('Disk I/O').getByRole('checkbox').count(),summary:await card('Disk I/O').getByText(/^Sum of /).innerText()};
  await capture('monitor-empty-'+width+'-'+height+'-'+scale);item.document=await geometry();return item;
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
      name:'synthetic-monitor-route',enforce:'pre',
      resolveId(source,importer){
        if(source==='./backend.mjs'&&importer===resolve(repo,'internal/server/web/semblance/main.mjs'))
          return resolve(repo,'scripts/dashboard-screenshots/ux-monitor-backend.mjs');
      },
      configureServer(s){s.middlewares.use((req,res,next)=>{
        if(req.url?.split('?')[0]==='/'){
          res.setHeader('Content-Type','text/html');
          res.end('<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Synthetic Monitor route</title><div id="app"></div><script type="module" src="/internal/server/web/semblance/main.mjs"></script></html>');
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
  page=await context.newPage();page.setDefaultTimeout(10000);cdp=await context.newCDPSession(page);
  page.on('pageerror',e=>result.pageErrors.push(e.message));
  page.on('websocket',()=>result.webSockets++);

  for(const [width,height,scale] of [[1440,900,1],[390,900,1],[390,320,1],[768,600,2]]){
    await openMonitor(width,height,scale);
    const item=await (cohort==='help'?helpInventory:stateInventory)(width,height,scale);
    item.forbidden=await page.evaluate(()=>monitorFixture.forbidden);assert.deepEqual(item.forbidden,[]);result.cases.push(item);
  }
  assert.deepEqual(result.pageErrors,[]);assert.deepEqual(result.networkRequests,[]);assert.equal(result.webSockets,0);
}catch(error){result.failure=error.message;result.diagnostic=await page?.evaluate(()=>({text:document.body.innerText,state:window.monitorFixture?.state,membership:window.monitorFixture?.membership})).catch(()=>null);await page?.screenshot({path:resolve(output,'failure.png')}).catch(()=>{});process.exitCode=1;}
finally{
  try{try{await cdp?.detach();result.cleanup.nativeProbeDetached=true;}finally{await context?.close();result.cleanup.contextClosed=true;}}
  finally{try{await browser?.close();result.cleanup.browserClosed=true;}finally{await server?.close();result.cleanup.serverJoined=true;}}
  result.elapsedMS=Date.now()-started;await writeFile(resolve(output,'monitor-inventory.json'),JSON.stringify(result,null,2));
}
console.log(JSON.stringify({cohort,cases:result.cases.length,failure:result.failure,elapsedMS:result.elapsedMS,cleanup:result.cleanup}));
