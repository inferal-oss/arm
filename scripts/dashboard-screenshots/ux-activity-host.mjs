// Read-only actual Activity/Host inventory; negative geometry is a finding.
// Run one bounded cohort through the unchanged shared FIFO; keep raw output private.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {mkdir,readFile,writeFile} from 'node:fs/promises';
import {resolve,sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createServer} from '../../web/node_modules/vite/dist/node/index.js';
import {chromium} from 'playwright';
const repo=fileURLToPath(new URL('../../',import.meta.url)),output=resolve(process.argv[2]||''),cohort=process.argv[3]||'activity';
assert(['activity','host','truncation','lifecycle','header'].includes(cohort));
assert(process.argv[2]&&output!==resolve(repo)&&!output.startsWith(resolve(repo)+sep),'Use an output directory outside the checkout');await mkdir(output,{recursive:true});
const files=['internal/server/web/semblance/main.mjs','internal/server/web/semblance/app.ttl','internal/server/web/semblance/graph-ui.mjs','internal/server/web/core.mjs','internal/server/web/resource-names.mjs','internal/server/web/activity-feed.mjs','web/third_party/semblance/ontology/components/type.ttl','web/third_party/semblance/src/components/type.ts','internal/server/web/semblance/backend.mjs','internal/server/named_views.go','internal/model/model.go','internal/model/ports.go','internal/model/node_metadata.go','internal/local/identity.go','internal/manager/manager.go','web/third_party/semblance/ontology/theme/layout.ttl','web/third_party/semblance/ontology/theme/type.ttl','web/third_party/semblance/ontology/theme/data.ttl','scripts/dashboard-screenshots/ux-activity-host.mjs','scripts/dashboard-screenshots/ux-activity-host-backend.mjs'];
const sources=Object.fromEntries(await Promise.all(files.map(async f=>[f,createHash('sha256').update(await readFile(resolve(repo,f))).digest('hex')])));
const graphVariant=process.argv[4]??'';assert(['','without-menu','without-tooltip'].includes(graphVariant));assert(!graphVariant||cohort==='header');let graphOverride=null;const diskAppSHA=sources['internal/server/web/semblance/app.ttl'];if(graphVariant){const graph=await readFile(resolve(repo,'internal/server/web/semblance/app.ttl'),'utf8'),marker=graphVariant==='without-menu'?'\n# Bound only the opened native action menu.':'\n# Keep the opened native Settings tooltip';assert(graph.includes(marker));const cut=graph.indexOf(marker);if(graphVariant==='without-menu'){const tail=graph.indexOf('\n# Keep the opened native Settings tooltip',cut);assert(tail>cut);graphOverride=graph.slice(0,cut)+graph.slice(tail);assert(graphOverride.includes('# Keep the opened native Settings tooltip'));}else graphOverride=graph.slice(0,cut);sources['internal/server/web/semblance/app.ttl']=createHash('sha256').update(graphOverride).digest('hex');}
const result={cohort,graphVariant,currentDiskAppSHA:diskAppSHA,mode:'activity-host-correction-validation',sources,cases:[],pageErrors:[],networkRequests:[],webSockets:0,clipboardWrites:0,clipboardAttempts:0,sessions:[],cleanup:{},failure:null};
let server,browser,context,page,cdp;const started=Date.now();
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
    return {value:n.textContent.slice(0,512),textLength:n.textContent.length,valuePreviewOnly:n.textContent.length>512,box,text,clip,fontSize:parseFloat(getComputedStyle(n).fontSize),
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


const slug=name=>name.toLowerCase().replace(/[^a-z0-9]+/g,'-');
async function capture(name){await page.screenshot({path:resolve(output,name+'.png'),animations:'disabled'});}
async function saveSession(){const prior=await page?.evaluate(()=>({operations:window.activityHostFixture?.calls.map(x=>x.operation)??[],forbidden:window.activityHostFixture?.forbidden??[],clipboardAttempts:window.__clipboardAttempts??0})).catch(()=>null);if(prior){result.sessions.push(prior);result.clipboardAttempts+=prior.clipboardAttempts;assert.deepEqual(prior.forbidden,[]);assert.equal(prior.clipboardAttempts,0);}}
async function openRoute(route,width,height,scale,fixture='dense'){
 await saveSession();
 await page.setViewportSize({width,height});await page.goto(server.resolvedUrls.local[0]+'?view='+route+'&fixture='+fixture);
 await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Connected',{exact:true}).waitFor();
 await page.getByRole('heading',{name:route==='host'?'Host & capabilities':'Activity',exact:true}).waitFor();
 // Let the real startup-scheduled status refresh finish before opening reading UI.
 await page.waitForFunction(()=>activityHostFixture.calls.filter(x=>x.operation==='status').length>=2);await page.waitForTimeout(50);await settle();
 await page.evaluate(scale=>{const root=document.documentElement,s=getComputedStyle(root);for(const key of ['xs','sm','md','lg','xl','2xl','3xl']){const n=parseFloat(s.getPropertyValue('--sem-text-'+key));if(Number.isFinite(n))root.style.setProperty('--sem-text-'+key,n*scale+'px');}root.style.fontSize=16*scale+'px';},scale);await settle();
}
const external=async locator=>decodeURIComponent((await locator.inputValue()).split('/option/').at(-1));
async function controlLabel(name){const label=page.getByText(name,{exact:true}),count=await label.count();return{count,bounds:count===1?await probe(label):null};}
async function choiceProbe(locator){const p=await probe(locator);p.value=await locator.locator('option:checked').textContent();p.nativeTextQualification='Native select painted glyph bounds unavailable; control box only';p.fullTextFits=null;return p;}
async function pairs(owner){const rows=[];for(const pair of await owner.locator('.sem-pair').all())rows.push({label:await probe(pair.locator('dt')),value:await probe(pair.locator('dd'))});return rows;}
const codeText=async node=>await node.evaluate(n=>(n.querySelector('code')??n).textContent);
async function nativeScrollSettled(locator){return locator.evaluate(n=>new Promise((resolve,reject)=>{let last='',changed=performance.now(),start=changed;function frame(){const value=scrollX+':'+scrollY+':'+n.scrollLeft+':'+n.scrollTop,now=performance.now();if(value!==last){last=value;changed=now;}if(now-changed>=200)return resolve({documentY:scrollY,left:n.scrollLeft,top:n.scrollTop,focus:n===document.activeElement,settledFor:now-changed});if(now-start>2500)return reject(Error('Native scrollers did not settle'));requestAnimationFrame(frame);}frame();}));}
async function readingTail(pre,beforeMarker=false){return pre.evaluate((n,beforeMarker)=>{
 const root=n.querySelector('code')??n,walk=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);let t,last;while(t=walk.nextNode())if(t.textContent)last=t;if(!last)return{empty:true};
 const text=last.textContent,end=beforeMarker&&text.endsWith('\n[display truncated]')?text.length-20:text.length,range=document.createRange();range.setStart(last,Math.max(0,end-16));range.setEnd(last,end);const b=range.getBoundingClientRect(),clip={left:0,right:innerWidth,top:0,bottom:innerHeight};
 for(let p=n;p;p=p.parentElement){const s=getComputedStyle(p),r=p.getBoundingClientRect();if(/hidden|clip|auto|scroll/.test(s.overflowX)){clip.left=Math.max(clip.left,r.left+p.clientLeft);clip.right=Math.min(clip.right,r.left+p.clientLeft+p.clientWidth);}if(/hidden|clip|auto|scroll/.test(s.overflowY)){clip.top=Math.max(clip.top,r.top+p.clientTop);clip.bottom=Math.min(clip.bottom,r.top+p.clientTop+p.clientHeight);}}
 for(const el of document.querySelectorAll('[data-sem-c="app-bar"],[data-sem-c="status-bar"]')){const r=el.getBoundingClientRect(),s=getComputedStyle(el);if(!['fixed','sticky'].includes(s.position))continue;if(r.top<=1)clip.top=Math.max(clip.top,r.bottom);if(r.bottom>=innerHeight-1)clip.bottom=Math.min(clip.bottom,r.top);}
 return{tail:text.slice(Math.max(0,end-16),end),left:b.left,right:b.right,top:b.top,bottom:b.bottom,clip,fullTextFits:b.left>=clip.left-1&&b.right<=clip.right+1&&b.top>=clip.top-1&&b.bottom<=clip.bottom+1,documentY:scrollY};
 },beforeMarker);}
async function codeInventory(pre,name){
 const text=await codeText(pre),hash=createHash('sha256').update(text).digest('hex');
 const item={length:text.length,sha256:hash,endsWithTruncation:text.endsWith('\n[display truncated]'),bounds:await probe(pre),metrics:await pre.evaluate(n=>{const s=getComputedStyle(n);return{tag:n.tagName,tabIndex:n.tabIndex,clientWidth:n.clientWidth,scrollWidth:n.scrollWidth,clientHeight:n.clientHeight,scrollHeight:n.scrollHeight,fontSize:parseFloat(s.fontSize),whiteSpace:s.whiteSpace,overflowX:s.overflowX,overflowY:s.overflowY,ancestors:(()=>{const a=[];for(let p=n.parentElement;p;p=p.parentElement){const s=getComputedStyle(p),r=p.getBoundingClientRect();a.push({tag:p.tagName,component:p.getAttribute('data-sem-c'),class:p.className,clientWidth:p.clientWidth,clientHeight:p.clientHeight,scrollHeight:p.scrollHeight,left:r.left,right:r.right,top:r.top,bottom:r.bottom,overflowX:s.overflowX,overflowY:s.overflowY,minWidth:s.minWidth,display:s.display,height:s.height,maxHeight:s.maxHeight});}return a;})()};})};
 if(item.metrics.tag==='PRE'){
  assert(item.bounds.box.left>=item.bounds.clip.left-1&&item.bounds.box.right<=item.bounds.clip.right+1,'Whole Host PRE must fit its actual clipping width');
  await pre.focus();const before=await pre.evaluate(n=>n.scrollLeft);await page.keyboard.press('ArrowRight');await nativeScrollSettled(pre);item.keyboardHorizontal=await pre.evaluate((n,before)=>({before,after:n.scrollLeft,max:n.scrollWidth-n.clientWidth,focus:n===document.activeElement}),before);if(item.keyboardHorizontal.max>1)assert(item.keyboardHorizontal.after>before,'Native focused PRE must support horizontal keyboard reading');
  if(item.keyboardHorizontal.max<=100000){
   const steps=Math.ceil((item.keyboardHorizontal.max-item.keyboardHorizontal.after)/40);for(let i=0;i<steps;i++)await page.keyboard.press('ArrowRight');await nativeScrollSettled(pre);item.keyboardRightEnd=await pre.evaluate(n=>({left:n.scrollLeft,max:n.scrollWidth-n.clientWidth,focus:n===document.activeElement}));assert(item.keyboardRightEnd.focus&&item.keyboardRightEnd.left>=item.keyboardRightEnd.max-1,'Native keyboard must reach ordinary metadata right edge');
  }else{
   item.largeKeyboardQualification='Positive native keyboard movement checked; maximum-cap line end uses bounded native wheel input rather than tens of thousands of repeated keys';await pre.hover();await page.mouse.wheel(item.metrics.scrollWidth+100,0);await nativeScrollSettled(pre);
  }
  await page.keyboard.press('End');await nativeScrollSettled(pre);item.rightmostText=await readingTail(pre,item.endsWithTruncation);assert(item.rightmostText.fullTextFits,'Rightmost meaningful metadata text must fit its actual clipping ancestors');
  if(item.endsWithTruncation){await pre.hover();await page.mouse.wheel(-item.metrics.scrollWidth-100,0);await nativeScrollSettled(pre);item.nativeHorizontalStart=await pre.evaluate(n=>n.scrollLeft);}
 }else item.nativeHorizontalQualification='Inline native Code has no dedicated horizontal scrolling surface';
 await page.keyboard.press('End');item.nativeScrollSettled=await nativeScrollSettled(pre);await settle();item.nativeEndTail=await readingTail(pre);await capture(name);return item;
}
async function activityCase(width,height,scale){
 await openRoute('activity',width,height,scale);
 const history=page.getByRole('combobox',{name:'History page',exact:true}),choice=page.getByRole('combobox',{name:'Inspect an event',exact:true});
 const item={width,height,textScale:scale,historyControl:await choiceProbe(history),choiceControl:await choiceProbe(choice),pages:await history.locator('option').allTextContents()};result.active=item;
 item.nativeLabels={history:await controlLabel('History page'),event:await controlLabel('Inspect an event')};
 item.initialCount=await page.locator('[data-sem-c="timeline"][aria-label="Durable operational events"] [data-sem-c="timeline-item"]').count();
 await history.selectOption({index:2});await settle();const options=await choice.locator('option').evaluateAll(xs=>xs.slice(1).map(x=>({value:x.value,label:x.textContent})));item.page2Options=options.length;
 const selectedOption=options[20].value,sequence=decodeURIComponent(selectedOption.split('/option/').at(-1));await choice.selectOption(selectedOption);await settle();item.selected=sequence;
 const exact=page.getByRole('button',{name:'Exact event record',exact:true});await exact.focus();await page.keyboard.press('Enter');await settle();
 item.detailFields=await pairs(page.locator('[data-sem-c="description-list"]').filter({has:page.locator('dt',{hasText:'Sequence'})}).last());
 const pre=page.locator('[data-sem-c="code"]').filter({hasText:'END-REASON'}).last();
 const raw=await codeText(pre),expected=await page.evaluate(seq=>activityHostFixture.exact(activityHostFixture.events.find(e=>String(e.sequence)===seq)),sequence);assert.equal(raw,expected);item.exactBytesEqual=true;assert(raw.includes('9223372036854775807'));assert(raw.includes(sequence));item.exactInt64=true;
 item.code=await codeInventory(pre,'activity-exact-'+width+'-'+height+'-'+scale);item.inspectControl=await probe(page.getByRole('button',{name:'Inspect lease',exact:true}));
 const firstTarget=page.locator('[data-sem-c="timeline"][aria-label="Durable operational events"] .sem-target').first();item.timelineCaption=await probe(page.locator('[data-sem-c="timeline"][aria-label="Durable operational events"] .sem-body strong').first());item.timelineFirst=await probe(firstTarget);item.timelineLast=await probe(page.locator('[data-sem-c="timeline"][aria-label="Durable operational events"] .sem-target').last());item.summaryLength=(await firstTarget.textContent()).length;
 await capture('activity-page-'+width+'-'+height+'-'+scale);const originalChoice=await choice.elementHandle(),originalExact=await exact.elementHandle(),originalHistory=await history.elementHandle();await exact.focus();
 await page.evaluate(seq=>activityHostFixture.repeat(seq),sequence);await page.waitForTimeout(400);await settle();
 item.duplicate={page:await external(history),selected:await external(choice),historyNodeRetained:await originalHistory.evaluate(n=>n.isConnected),choiceNodeRetained:await originalChoice.evaluate(n=>n.isConnected),exactNodeRetained:await originalExact.evaluate(n=>n.isConnected),exactFocusRetained:await originalExact.evaluate(n=>n===document.activeElement),exactOpen:await page.getByRole('button',{name:'Exact event record',exact:true}).getAttribute('aria-expanded')};
 const reopened=page.getByRole('button',{name:'Exact event record',exact:true});await reopened.focus();if(await reopened.getAttribute('aria-expanded')!=='true')await page.keyboard.press('Enter');await settle();const originalReopened=await reopened.elementHandle();await page.evaluate(()=>activityHostFixture.append());await page.waitForTimeout(400);await settle();item.newEventWhileReading={page:await external(history),selected:await external(choice),exactNodeRetained:await originalReopened.evaluate(n=>n.isConnected),exactFocusRetained:await originalReopened.evaluate(n=>n===document.activeElement),exactOpen:await page.getByRole('button',{name:'Exact event record',exact:true}).getAttribute('aria-expanded')};
 for(const reading of [item.duplicate,item.newEventWhileReading]){assert(reading.exactNodeRetained&&reading.exactFocusRetained&&reading.exactOpen==='true','Unchanged selected Activity reader must retain original open control and focus');}
 await choice.focus();await page.evaluate(()=>activityHostFixture.append());await page.waitForTimeout(400);await settle();item.append={page:await external(history),selected:await external(choice),choiceNodeRetained:await originalChoice.evaluate(n=>n.isConnected),choiceFocusRetained:await originalChoice.evaluate(n=>n===document.activeElement)};
 assert(item.append.choiceNodeRetained&&item.append.choiceFocusRetained,'Refresh must preserve deliberate focus outside the reader');
 await history.selectOption({index:3});await settle();item.lastPageOptions=await choice.locator('option').count();await history.selectOption({index:0});await settle();item.pageChangeClearsSelection=await external(choice)==='';
 item.selectionRetired=await originalExact.evaluate(n=>!n.isConnected);assert(item.pageChangeClearsSelection&&item.selectionRetired,'Out-of-page selection must retire the previous reader');
 item.document=await geometry();item.calls=await page.evaluate(()=>activityHostFixture.calls.map(x=>x.operation));
 await openRoute('activity',width,height,scale,'empty');item.empty={timelineItems:await page.locator('[data-sem-c="timeline"][aria-label="Durable operational events"] [data-sem-c="timeline-item"]').count(),historyOptions:await history.locator('option').count(),eventOptions:await choice.locator('option').count(),history:await choiceProbe(history),choice:await choiceProbe(choice),document:await geometry()};await capture('activity-empty-'+width+'-'+height+'-'+scale);return item;
}
async function assertHostText(codes){
 const expected=await page.evaluate(()=>{const s=activityHostFixture.status(),raw=activityHostFixture.exact(s.infrastructure_ports??[]);return{technical:(s.capabilities??[]).join('\n'),infrastructure:raw.length>256000?raw.slice(0,256000)+'\n[display truncated]':raw};});
 const actual={technical:await codeText(codes.first()),infrastructure:await codeText(codes.last())};
 assert.equal(actual.technical,expected.technical,'Full native capability text must match current status');assert.equal(actual.infrastructure,expected.infrastructure,'Full native infrastructure text must match current status/cap');
 for(const value of Object.values(expected))assert.equal(value,value.replace(/^\n+|\s+$/g,''),'Generated native block payload must have no meaningful trimmed boundary');
 return Object.fromEntries(Object.entries(actual).map(([k,v])=>[k,{length:v.length,sha256:createHash('sha256').update(v).digest('hex'),exact:true,boundariesPreserved:true}]));
}
async function hostCase(width,height,scale){
 await openRoute('host',width,height,scale);const authority=page.locator('[data-sem-c="card"]').filter({has:page.getByRole('heading',{name:'Local authority',exact:true})}).last();
 const identity=page.getByRole('button',{name:'Exact identity & cursor',exact:true}),technical=page.getByRole('button',{name:/^Technical identifiers ·/}),infrastructure=page.getByRole('button',{name:'Infrastructure ports',exact:true});
 const item={width,height,textScale:scale,expected:await page.evaluate(()=>({technicalLength:activityHostFixture.status().capabilities?.join('\n').length??0,infrastructureLength:activityHostFixture.exact(activityHostFixture.status().infrastructure_ports??[]).length,portCount:activityHostFixture.status().infrastructure_ports?.length??0})),summary:await pairs(authority.locator('[data-sem-c="description-list"]').first())};result.active=item;
 item.identityControl=await probe(identity);await identity.focus();await page.keyboard.press('Enter');await settle();item.identity=await pairs(authority.locator('[data-sem-c="description-list"]').last());
 item.capabilityLabels=[];for(const label of await page.getByRole('heading',{name:'Available capabilities',exact:true}).locator('..').locator('[data-sem-c="card"] [data-sem-c="text"]').all())item.capabilityLabels.push(await probe(label));
 item.technicalControl=await probe(technical);await technical.focus();await page.keyboard.press('Enter');await settle();const codes=page.locator('[data-sem-c="code"]');item.technical=await codeInventory(codes.first(),'host-technical-'+width+'-'+height+'-'+scale);
 item.infrastructureControl=await probe(infrastructure);await infrastructure.focus();await page.keyboard.press('Enter');await settle();item.infrastructure=await codeInventory(codes.last(),'host-infrastructure-'+width+'-'+height+'-'+scale);
 item.fullNativeText=await assertHostText(codes);item.technicalPaintedLines=await codes.first().evaluate(n=>{const t=n.querySelector('code')??n,text=t.firstChild;if(!text||text.nodeType!==Node.TEXT_NODE)return null;let at=0;return text.textContent.split('\n').map(line=>{const r=document.createRange();r.setStart(text,at);r.setEnd(text,at+Math.min(line.length,1));at+=line.length+1;const b=r.getBoundingClientRect();return{identifier:line,top:b.top,left:b.left};});});
 assert.equal(new Set(item.technicalPaintedLines.map(x=>x.top)).size,26,'Every Host identifier must start on a preserved source line');assert.equal(item.technical.metrics.fontSize,13*scale);assert.equal(item.infrastructure.metrics.fontSize,13*scale);
 const focusedCode=await codes.last().elementHandle();await codes.last().focus();const position=await focusedCode.evaluate(n=>({left:n.scrollLeft,top:n.scrollTop})),previousStatus=await page.evaluate(()=>activityHostFixture.calls.filter(x=>x.operation==='status').length);await page.evaluate(()=>activityHostFixture.refresh('fresh'));await page.waitForFunction(n=>activityHostFixture.calls.filter(x=>x.operation==='status').length>n,previousStatus);await settle();
 item.preRefresh=await focusedCode.evaluate((n,before)=>({originalConnected:n.isConnected,focus:n===document.activeElement,before,left:n.scrollLeft,top:n.scrollTop}),position);assert(item.preRefresh.originalConnected&&item.preRefresh.focus&&item.preRefresh.left===position.left&&item.preRefresh.top===position.top,'Unchanged refresh must retain original PRE focus and reading position');item.refreshedNativeText=await assertHostText(codes);
 const nodes=await Promise.all([authority,identity,technical,infrastructure].map(x=>x.elementHandle()));await technical.focus();
 const snapshot=async()=>({fullNativeText:await assertHostText(codes),nodes:await Promise.all(nodes.map(n=>n.evaluate(n=>n.isConnected))),focus:await nodes[2].evaluate(n=>n===document.activeElement),identityOpen:await identity.getAttribute('aria-expanded'),technicalOpen:await technical.getAttribute('aria-expanded'),infrastructureOpen:await infrastructure.getAttribute('aria-expanded'),summary:await pairs(authority.locator('[data-sem-c="description-list"]').first()),technicalLength:(await codeText(codes.first())).length});
 item.states={};for(const state of ['draining','unknown','empty','fresh']){await technical.focus();const previous=await page.evaluate(()=>activityHostFixture.calls.filter(x=>x.operation==='status').length);await page.evaluate(s=>activityHostFixture.refresh(s),state);await page.waitForFunction(n=>activityHostFixture.calls.filter(x=>x.operation==='status').length>n,previous);await page.waitForTimeout(100);await settle();item.states[state]=await snapshot();await capture('host-'+state+'-'+width+'-'+height+'-'+scale);}
 await technical.focus();await page.evaluate(()=>activityHostFixture.disconnect());await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Disconnected · retrying',{exact:true}).waitFor();item.disconnected=await snapshot();await page.waitForFunction(()=>activityHostFixture.connections.length===2);await page.evaluate(()=>activityHostFixture.releaseReconnect());await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Connected',{exact:true}).waitFor();await page.waitForTimeout(300);await settle();item.reconnected=await snapshot();item.document=await geometry();return item;
}
async function lifecycleCase(width,height,scale){
 await openRoute('activity',width,height,scale);const choice=page.getByRole('combobox',{name:'Inspect an event',exact:true});await choice.selectOption({index:21});await settle();const selected=await external(choice),nativeValue=await choice.inputValue();
 const record=()=>page.getByRole('button',{name:'Exact event record',exact:true});await record().focus();await page.keyboard.press('Enter');await settle();const first=await record().elementHandle();
 await choice.selectOption({index:22});await settle();const item={width,height,textScale:scale,selected,changedSelectionRetired:await first.evaluate(n=>!n.isConnected)};assert(item.changedSelectionRetired);
 await choice.selectOption(nativeValue);await record().focus();await page.keyboard.press('Enter');await settle();const original=await record().elementHandle();
 await page.evaluate(()=>activityHostFixture.disconnect());await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Disconnected · retrying',{exact:true}).waitFor();await page.waitForFunction(()=>activityHostFixture.connections.length===2);await page.evaluate(()=>{activityHostFixture.switchSource();activityHostFixture.releaseReconnect();});await page.getByRole('toolbar',{name:'Backend status',exact:true}).getByText('Connected',{exact:true}).waitFor();await page.waitForTimeout(300);await settle();
 item.context={originalRetired:await original.evaluate(n=>!n.isConnected),selection:await external(choice),readerCount:await record().count()};assert(item.context.originalRetired&&item.context.selection===''&&item.context.readerCount===0,'Changed source must retire and clear the old exact selection');
 await choice.selectOption(nativeValue);await record().focus();await page.keyboard.press('Enter');await settle();const replacement=await record().elementHandle(),pre=page.locator('[data-sem-c="code"]').last();
 const expected=await page.evaluate(seq=>activityHostFixture.exact(activityHostFixture.events.find(e=>String(e.sequence)===seq)),selected);assert.equal(await codeText(pre),expected);assert(expected.includes('Synthetic replacement source'));item.sameSequenceCurrentSourceExact=true;await capture('activity-source-'+width+'-'+height);
 await choice.selectOption({index:0});await settle();item.cancelledSelection={readerCount:await record().count(),previousRetired:await replacement.evaluate(n=>!n.isConnected)};assert(item.cancelledSelection.readerCount===0&&item.cancelledSelection.previousRetired);return item;
}
async function nativeMenuTooltip(control,panel){
 if(!await control.getAttribute('data-sem-tooltip'))return null;
 const expected=await control.getAttribute('aria-label'),style=await control.evaluate(n=>{const s=getComputedStyle(n,'::after');return{content:s.content,left:s.left,transform:s.transform,font:s.font};});if(style.content==='none'||style.content==='normal')return null;
 const {root}=await cdp.send('DOM.getDocument'),{nodeId}=await cdp.send('DOM.querySelector',{nodeId:root.nodeId,selector:'[data-sem-c="app-bar"] .sem-more-body [aria-label='+JSON.stringify(expected)+']'}),{node}=await cdp.send('DOM.describeNode',{nodeId,depth:3}),pseudo=node.pseudoElements?.find(x=>x.pseudoType==='after');assert(pseudo,'Focused native tooltip must render');
 const {model}=await cdp.send('DOM.getBoxModel',{backendNodeId:pseudo.backendNodeId}),snapshot=await cdp.send('DOMSnapshot.captureSnapshot',{computedStyles:[],includeDOMRects:true}),doc=snapshot.documents[0],nodes=doc.nodes,layout=doc.layout,index=nodes.backendNodeId.indexOf(pseudo.backendNodeId),wanted=new Set([index]);for(let i=index+1;i<nodes.parentIndex.length;i++)if(wanted.has(nodes.parentIndex[i]))wanted.add(i);
 const indices=new Set(layout.nodeIndex.map((n,i)=>wanted.has(n)&&snapshot.strings[layout.text[i]]?i:-1).filter(i=>i>=0)),text=[...indices].map(i=>snapshot.strings[layout.text[i]]).join('');assert.equal(text,expected);
 const p=layout.bounds[layout.nodeIndex.indexOf(index)],left=Math.min(...model.border.filter((_,i)=>i%2===0)),top=Math.min(...model.border.filter((_,i)=>i%2===1)),right=Math.max(...model.border.filter((_,i)=>i%2===0)),bottom=Math.max(...model.border.filter((_,i)=>i%2===1));assert(Math.abs(right-left-p[2])<1&&Math.abs(bottom-top-p[3])<1,'Native pseudo snapshot and border sizes must agree');
 const painted=[];for(let i=0;i<doc.textBoxes.layoutIndex.length;i++)if(indices.has(doc.textBoxes.layoutIndex[i])){const[x,y,w,h]=doc.textBoxes.bounds[i];painted.push({left:left+x-p[0],top:top+y-p[1],right:left+x-p[0]+w,bottom:top+y-p[1]+h});}assert(painted.length);
 const clip=await panel.evaluate(n=>{const clip={left:0,right:innerWidth,top:0,bottom:innerHeight};for(let p=n;p;p=p.parentElement){const r=p.getBoundingClientRect(),s=getComputedStyle(p);if(/hidden|auto|scroll|clip/.test(s.overflowX)){clip.left=Math.max(clip.left,r.left+p.clientLeft);clip.right=Math.min(clip.right,r.left+p.clientLeft+p.clientWidth);}if(/hidden|auto|scroll|clip/.test(s.overflowY)){clip.top=Math.max(clip.top,r.top+p.clientTop);clip.bottom=Math.min(clip.bottom,r.top+p.clientTop+p.clientHeight);}}return clip;}),fits=r=>r.left>=clip.left-1&&r.right<=clip.right+1&&r.top>=clip.top-1&&r.bottom<=clip.bottom+1;
 return{expected,text,style,border:{left,right,top,bottom},painted,clip,fullBoxFits:fits({left,right,top,bottom}),fullTextFits:painted.every(fits)};
}
async function focusedMenuControl(control,panel){return control.evaluate(n=>{
 const panel=n.closest('.sem-more-body'),p=panel.getBoundingClientRect(),r=n.getBoundingClientRect(),range=document.createRange();range.selectNodeContents(n);const t=range.getBoundingClientRect(),clip={left:Math.max(0,p.left+panel.clientLeft),right:Math.min(innerWidth,p.left+panel.clientLeft+panel.clientWidth),top:Math.max(0,p.top+panel.clientTop),bottom:Math.min(innerHeight,p.top+panel.clientTop+panel.clientHeight)},fit=b=>b.left>=clip.left-1&&b.right<=clip.right+1&&b.top>=clip.top-1&&b.bottom<=clip.bottom+1;
 return{label:n.getAttribute('aria-label')??n.textContent,focus:n===document.activeElement,originalConnected:n.isConnected,box:{left:r.left,right:r.right,top:r.top,bottom:r.bottom},text:{left:t.left,right:t.right,top:t.top,bottom:t.bottom},clip,boxFits:fit(r),textFits:fit(t),panelTop:p.top,panelBottom:p.bottom,viewportHeight:innerHeight,scrollTop:panel.scrollTop,scrollHeight:panel.scrollHeight,clientHeight:panel.clientHeight,fontSize:parseFloat(getComputedStyle(n).fontSize)};
 });}
async function traverseMenu(panel,controls,trace,name){
 for(const [phase,indices,key]of [['forward',controls.map((_,i)=>i),'Tab'],['backward',controls.slice(0,-1).map((_,i)=>controls.length-2-i),'Shift+Tab']])for(const i of indices){await page.keyboard.press(key);await nativeScrollSettled(panel);const state=await focusedMenuControl(controls[i],panel),tooltip=await nativeMenuTooltip(controls[i],panel);trace.push({phase,index:i,...state,tooltip});assert(state.focus&&state.originalConnected&&state.boxFits&&state.textFits,'Each original focused More action must fit its true panel and viewport');if(tooltip){assert(tooltip.fullBoxFits&&tooltip.fullTextFits,'Full native More tooltip must fit its scroll clip');await page.keyboard.press('Shift+Tab');await page.mouse.move(0,0);await settle();assert(!await controls[i].evaluate(n=>n===document.activeElement),'Hover probe must not retain tooltip-owner focus');await controls[i].hover();await settle();const hover=await nativeMenuTooltip(controls[i],panel);trace.at(-1).hoverTooltip=hover;assert(hover?.fullBoxFits&&hover.fullTextFits,'Full hovered native More tooltip must fit its scroll clip');await capture(name+'-'+phase+'-'+i+'-hover');await page.mouse.move(0,0);await page.keyboard.press('Tab');await nativeScrollSettled(panel);assert((await focusedMenuControl(controls[i],panel)).focus,'Restore original tooltip-owner keyboard focus');}await capture(name+'-'+phase+'-'+i);}
 for(const state of trace)assert(state.panelTop>=-1&&state.panelBottom<=state.viewportHeight+1,'Opened More panel must fit viewport');
}
async function headerGeometry(){return await page.locator('[data-sem-c="app-bar"]').evaluate(bar=>{
  const inspect=n=>{const r=n.getBoundingClientRect(),s=getComputedStyle(n),range=document.createRange();range.selectNodeContents(n);const t=range.getBoundingClientRect();return{tag:n.tagName,component:n.getAttribute('data-sem-c'),class:n.className,text:n.textContent.slice(0,150),box:{left:r.left,right:r.right,top:r.top,bottom:r.bottom},textBox:{left:t.left,right:t.right,top:t.top,bottom:t.bottom},style:Object.fromEntries(['display','position','minWidth','width','maxWidth','whiteSpace','overflowWrap','overflowX','overflowY','textOverflow','gridTemplateColumns','flex','paddingInlineStart'].map(k=>[k,s[k]]))};};
  const brand=bar.querySelector('.sem-brand'),byline=brand.querySelector('.sem-byline'),label=byline.querySelector('[data-sem-c="text"]'),actions=bar.querySelector('.sem-actions'),matched=[];
  function rules(list){for(const rule of list){if(rule.media&&!matchMedia(rule.media.mediaText).matches)continue;if(rule.selectorText&&[brand,byline,label].some(n=>n.matches(rule.selectorText))&&['min-width','white-space','grid-template-columns','overflow'].some(p=>rule.style?.getPropertyValue(p)))matched.push({selector:rule.selectorText,style:rule.style.cssText});else if(rule.cssRules)rules(rule.cssRules);}}for(const sheet of [...document.styleSheets,...document.adoptedStyleSheets])rules(sheet.cssRules);
  const footer=document.querySelector('[data-sem-c="status-bar"]')?.getBoundingClientRect();return{readingHeight:Math.min(innerHeight,footer?.top??innerHeight)-Math.max(0,bar.getBoundingClientRect().bottom),bar:inspect(bar),brand:inspect(brand),byline:inspect(byline),label:inspect(label),actions:inspect(actions),matched,documentWidth:document.documentElement.scrollWidth,scrollX,viewport:innerWidth};
 });}
function assertHeaderReading(data){
 const within=(a,b)=>a.left>=b.left-1&&a.right<=b.right+1&&a.top>=b.top-1&&a.bottom<=b.bottom+1;
 assert(within(data.label.textBox,data.byline.box)&&within(data.label.textBox,data.brand.box)&&within(data.label.textBox,data.bar.box),'Full host label must fit byline, brand and header');
 const overlap=Math.min(data.label.textBox.right,data.actions.box.right)-Math.max(data.label.textBox.left,data.actions.box.left)>1&&Math.min(data.label.textBox.bottom,data.actions.box.bottom)-Math.max(data.label.textBox.top,data.actions.box.top)>1;assert(!overlap,'Header text must not overlap native actions');assert.equal(data.documentWidth,data.viewport,'Header must not grow document width');
}
async function headerCase(width,height,scale){const item={width,height,textScale:scale,routes:{}};result.activeHeader=item;
 for(const route of ['activity','host']){
  await openRoute(route,width,height,scale);const bar=page.locator('[data-sem-c="app-bar"]'),label=bar.locator('.sem-byline [data-sem-c="text"]'),originalBar=await bar.elementHandle(),originalLabel=await label.elementHandle();
  const data=await headerGeometry();assertHeaderReading(data);if(height<=640)assert(data.readingHeight>=120,'Short header must leave useful reading space');item.routes[route]={geometry:data};await capture('header-'+route+'-'+width+'-'+height+'-'+scale);
  const picker=bar.getByRole('combobox',{name:'Views',exact:true}),more=bar.getByRole('button',{name:'Actions and display',exact:true});
  if(await picker.isVisible()){
   const originalPicker=await picker.elementHandle(),before=await picker.inputValue(),url=page.url();await picker.focus();await page.keyboard.press('Space');await page.keyboard.press('Escape');await settle();
   item.routes[route].picker={cancelledValue:await picker.inputValue(),originalConnected:await originalPicker.evaluate(n=>n.isConnected),openerFocus:await originalPicker.evaluate(n=>n===document.activeElement),routeUnchanged:page.url()===url};assert(item.routes[route].picker.cancelledValue===before&&item.routes[route].picker.originalConnected&&item.routes[route].picker.openerFocus&&item.routes[route].picker.routeUnchanged,'Native picker Escape must preserve selection/route/original focus');
   const target=route==='host'?'activity':'host';await picker.focus();await page.keyboard.press('Space');await page.keyboard.type(target==='host'?'h':'a');await page.keyboard.press('Enter');await page.waitForURL(u=>u.searchParams.get('view')===target,{timeout:2000});await settle();item.routes[route].picker.keyboardTarget=target;
  }else item.routes[route].desktopPickerHidden=true;
  if(await more.isVisible()){
   const originalMore=await more.elementHandle();await more.focus();await page.keyboard.press('Enter');await page.waitForFunction(()=>document.querySelector('[data-sem-c="app-bar"] .sem-more')?.getAttribute('data-open')==='true');await settle();item.routes[route].moreOpen=await bar.locator('.sem-more-body').evaluate(n=>{const b=n.getBoundingClientRect(),s=getComputedStyle(n);return{box:{left:b.left,right:b.right,top:b.top,bottom:b.bottom},viewport:{width:innerWidth,height:innerHeight},overflowY:s.overflowY,controls:[...n.querySelectorAll('button,select,input')].map(x=>{const r=x.getBoundingClientRect(),range=document.createRange();range.selectNodeContents(x);const t=range.getBoundingClientRect();return{label:x.getAttribute('aria-label')??x.textContent,box:{left:r.left,right:r.right,top:r.top,bottom:r.bottom},text:{left:t.left,right:t.right,top:t.top,bottom:t.bottom},viewportFits:r.left>=-1&&r.right<=innerWidth+1&&r.top>=-1&&r.bottom<=innerHeight+1};})};});await capture('header-more-'+route+'-'+width+'-'+height+'-'+scale);const panel=bar.locator('.sem-more-body'),originalPanel=await panel.elementHandle(),controls=await panel.locator('button,select,input').elementHandles();item.routes[route].menuTraversal=[];await traverseMenu(panel,controls,item.routes[route].menuTraversal,'header-menu-'+route+'-'+width+'-'+height);
   if(width===390&&height===900){item.routes[route].resize={};await page.setViewportSize({width,height:320});await settle();await page.keyboard.press('Shift+Tab');item.routes[route].resize.short=[];await traverseMenu(panel,controls,item.routes[route].resize.short,'header-menu-resize-'+route);await page.setViewportSize({width,height});await settle();item.routes[route].resize.back={panelRetained:await originalPanel.evaluate(n=>n.isConnected),controlsRetained:await Promise.all(controls.map(n=>n.evaluate(n=>n.isConnected))),open:await bar.locator('.sem-more').getAttribute('data-open')==='true'};assert(item.routes[route].resize.back.panelRetained&&item.routes[route].resize.back.controlsRetained.every(Boolean)&&item.routes[route].resize.back.open);}
   await page.keyboard.press('Escape');await settle();
   item.routes[route].more={closed:await bar.locator('.sem-more').getAttribute('data-open')==='false',originalConnected:await originalMore.evaluate(n=>n.isConnected),openerFocus:await originalMore.evaluate(n=>n===document.activeElement)};assert(item.routes[route].more.closed&&item.routes[route].more.originalConnected&&item.routes[route].more.openerFocus,'Native More Escape must restore the original opener');
  }else item.routes[route].desktopMoreHidden=true;
  const focusBeforeName=await page.evaluateHandle(()=>document.activeElement);const nextName='updated-host-'+ 'node'.repeat(24),previous=await page.evaluate(()=>activityHostFixture.calls.filter(x=>x.operation==='status').length);await page.evaluate(name=>{activityHostFixture.displayName=name;activityHostFixture.refresh('fresh');},nextName);await page.waitForFunction(n=>activityHostFixture.calls.filter(x=>x.operation==='status').length>n,previous);await settle();
  item.routes[route].updated={focusRetained:await focusBeforeName.evaluate(n=>n===document.activeElement),barRetained:await originalBar.evaluate(n=>n.isConnected),labelRetained:await originalLabel.evaluate(n=>n.isConnected),fullLabel:await label.textContent(),geometry:await headerGeometry()};assert(item.routes[route].updated.barRetained&&item.routes[route].updated.labelRetained&&item.routes[route].updated.focusRetained);assert.equal(item.routes[route].updated.fullLabel,nextName);assertHeaderReading(item.routes[route].updated.geometry);
 }return item;
}
async function truncationCase(width,height,scale){const item={width,height,textScale:scale};result.active=item;
 for(const route of ['activity','host']){await openRoute(route,width,height,scale,'truncated');let pre;
  if(route==='activity'){const choice=page.getByRole('combobox',{name:'Inspect an event',exact:true});await choice.selectOption({index:1});await page.getByRole('button',{name:'Exact event record',exact:true}).click();pre=page.locator('[data-sem-c="code"]').last();}
  else{await page.getByRole('button',{name:'Infrastructure ports',exact:true}).click();pre=page.locator('[data-sem-c="code"]').last();}
  await settle();item[route]=await codeInventory(pre,route+'-truncated-'+width+'-'+height+'-'+scale);item[route].expectedDisplayedLength=256000+'\n[display truncated]'.length;item[route].truncationRendered=item[route].endsWithTruncation&&item[route].length===item[route].expectedDisplayedLength;if(route==='host'){item[route].fullNativeText=await assertHostText(page.locator('[data-sem-c="code"]'));assert(item[route].truncationRendered,'Host truncation marker must render after the binding correction');}item[route].document=await geometry();
 }return item;
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
      name:'synthetic-activity-host-route',enforce:'pre',
      resolveId(source,importer){
        if(graphOverride!==null&&source==='./app.ttl?raw'&&importer===resolve(repo,'internal/server/web/semblance/main.mjs'))return '\0activity-host-app-graph';
        if(source==='./backend.mjs'&&importer===resolve(repo,'internal/server/web/semblance/main.mjs'))
          return resolve(repo,'scripts/dashboard-screenshots/ux-activity-host-backend.mjs');
      },
      load(id){if(id==='\0activity-host-app-graph')return 'export default '+JSON.stringify(graphOverride);},
      configureServer(s){s.middlewares.use((req,res,next)=>{
        if(req.url?.split('?')[0]==='/'){
          res.setHeader('Content-Type','text/html');
          res.end('<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Synthetic Activity and Host routes</title><div id="app"></div><script type="module" src="/internal/server/web/semblance/main.mjs"></script></html>');
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
    window.WebSocket=RefusedWebSocket;window.__clipboardAttempts=0;Object.defineProperty(navigator,'clipboard',{value:{writeText(){window.__clipboardAttempts++;throw Error('Real clipboard write refused');},write(){window.__clipboardAttempts++;throw Error('Real clipboard write refused');}}});
  });
  page=await context.newPage();page.setDefaultTimeout(10000);if(cohort==='header')cdp=await context.newCDPSession(page);
  page.on('pageerror',e=>result.pageErrors.push(e.message));
  page.on('websocket',()=>result.webSockets++);


 for(const [width,height,scale] of (['truncation','lifecycle'].includes(cohort)?[[1440,900,1],[390,320,1]]:[[1440,900,1],[390,900,1],[390,320,1],[768,600,2]])){
  const item=await(cohort==='activity'?activityCase:cohort==='host'?hostCase:cohort==='lifecycle'?lifecycleCase:cohort==='header'?headerCase:truncationCase)(width,height,scale);
  item.forbidden=await page.evaluate(()=>activityHostFixture.forbidden);assert.deepEqual(item.forbidden,[]);result.cases.push(item);
 }
 assert.deepEqual(result.pageErrors,[]);assert.deepEqual(result.networkRequests,[]);assert.equal(result.webSockets,0);assert.equal(result.clipboardWrites,0);
}catch(error){result.failure=error.message;result.failedGeometry=await geometry().catch(()=>null);result.diagnostic=await page?.evaluate(()=>({text:document.body.innerText.slice(0,6000),calls:window.activityHostFixture?.calls.map(x=>x.operation),codeShapes:[...document.querySelectorAll('[data-sem-c="code"]')].map(n=>({tag:n.tagName,textLength:n.textContent.length,children:n.children.length}))})).catch(()=>null);await page?.screenshot({path:resolve(output,'failure.png')}).catch(()=>{});process.exitCode=1;}
finally{await saveSession().catch(e=>{result.failure??=e.message;process.exitCode=1;});try{if(cdp){await cdp.detach();result.cleanup.nativeProbeDetached=true;}}finally{try{await context?.close();result.cleanup.contextClosed=true;}finally{try{await browser?.close();result.cleanup.browserClosed=true;}finally{await server?.close();result.cleanup.serverJoined=true;}}}result.elapsedMS=Date.now()-started;await writeFile(resolve(output,'activity-host-inventory.json'),JSON.stringify(result,null,2));}
console.log(JSON.stringify({cohort,cases:result.cases.length,failure:result.failure,elapsedMS:result.elapsedMS,cleanup:result.cleanup}));
