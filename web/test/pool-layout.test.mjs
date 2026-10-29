import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createPoolConsole} from '../../internal/server/web/semblance/pools.mjs';
import {poolRequestCopy} from '../../internal/server/web/pool-model.mjs';
const RDF='http://www.w3.org/1999/02/22-rdf-syntax-ns#type',C='https://semblance.dev/ns/component#';
const tick=()=>new Promise(resolve=>setImmediate(resolve));
const view={pool:{id:'synthetic-pool',name:'Synthetic permits',authority:'synthetic-authority',scope:'global',revision:7n,capacity:9223372036854775807n,state:'active',owner:'1000',unit:'permits'},reserved:9223372036854775800n,available:7n,deficit:0n,users:[],queued:[],review:{authority:'synthetic-authority',consumers:[{id:'synthetic-lease',revision:2n}]}};
function fixture(){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8'));
 const runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><div id="app"></div></body></html>'),renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{onError:e=>{throw e}}),bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 const calls=[],panel=createPoolConsole({ui,root:ui.bind('resourcesContent'),connected:()=>true,key:()=> 'synthetic-request',call:async(op,args)=>{calls.push([op,args]);if(op==='pool_list')return poolRequestCopy([view]);if(op==='pool_inspect')return poolRequestCopy(view);if(op==='pool_history_page')return{pool:view.pool.id,entries:[]};if(op==='pool_consumers')return{pool:view.pool.id,descendants:true,entries:[],cursor:'',more:false};throw Error('No mutation is authorized in this fixture');}});
 ui.put(ARM+'shell',ARM+'view',ARM+'resources',true);panel.update({status:{host:view.pool.authority,owner:'1000',cursor:1},route:{view:'resources',pool:view.pool.id,poolAfter:'0'}});
 const handle=label=>[...ui.handles.values()].find(h=>h.label===label),node=h=>renderer.nodeOf(projection.index.rootOf(h.iri));
 const click=label=>node(handle(label)).dispatchEvent(new window.Event('click',{bubbles:true}));
 return{ui,panel,document,store,calls,handle,node,click,close(){panel.dispose();runtime.dispose();store.dispose?.();}};
}
it('pool dialogs and buttons retain direct native types while editor actions have independent footer ownership',async()=>{
 const x=fixture();try{
 await tick();const manager=x.handle('Custom resource pools');expect(x.store.match(x.ui.nn(manager.iri),x.ui.nn(RDF),x.ui.nn(C+'Dialog'))).toHaveLength(1);
 x.click('Change total capacity…');const editor=x.handle('Change total capacity'),input=x.handle('New total capacity'),inputNode=x.node(input);
 expect(x.store.match(x.ui.nn(editor.iri),x.ui.nn(RDF),x.ui.nn(C+'Dialog'))).toHaveLength(1);
 x.handle('Why are you making this change?').value='Synthetic reviewed change';input.value='9223372036854775806';
 const review=x.handle('Review change'),reviewNode=x.node(review);expect(reviewNode.closest('.sem-actions')).toBeTruthy();x.click('Review change');await tick();
 const confirm=x.handle('Set total capacity to 9,223,372,036,854,775,806');expect(x.node(confirm).closest('.sem-actions')).toBeTruthy();expect(x.store.match(x.ui.nn(confirm.iri),x.ui.nn(RDF),x.ui.nn(C+'Button'))).toHaveLength(1);
 const values=[...x.node(editor).querySelectorAll('[data-sem-c="arm-pool-facts"] dd')].map(n=>n.textContent);expect(values).toContain('9223372036854775807');expect(values).toContain('9223372036854775806');
 x.click('Back to edit');expect(x.node(input)).toBe(inputNode);expect(x.node(review)).toBe(reviewNode);expect(input.value).toBe('9223372036854775806');expect(review.disposed).not.toBe(true);
 x.click('Cancel');expect(editor.open).toBe(false);expect(x.calls.every(([op])=>['pool_list','pool_inspect','pool_history_page','pool_consumers'].includes(op))).toBe(true);
 }finally{x.close();}
});
