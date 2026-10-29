import {it,expect} from 'vitest';
import {readFileSync} from 'node:fs';
import {parseHTML} from 'linkedom';
import {loadStore} from '../third_party/semblance/src/node/load.ts';
import {Runtime,EventBus,MOD} from '../third_party/semblance/src/index.ts';
import {DEFAULT_LAYERS} from '../third_party/semblance/src/ns.ts';
import {DomRenderer} from '../third_party/semblance/src/render/dom.ts';
import {createGraphUI,ARM} from '../../internal/server/web/semblance/graph-ui.mjs';
import {createResourceOverview} from '../../internal/server/web/semblance/resources.mjs';
const C='https://semblance.dev/ns/component#',RDF='http://www.w3.org/1999/02/22-rdf-syntax-ns#type';
function fixture(){
 const store=loadStore('wat',readFileSync(new URL('../../internal/server/web/semblance/app.ttl',import.meta.url),'utf8')),runtime=new Runtime(store),projection=runtime.project({modality:MOD.Screen,layers:DEFAULT_LAYERS,prefer:'sparql',roots:[ARM+'shell']},e=>{throw e});
 const {document,window}=parseHTML('<!doctype html><html><body><div id="app"></div></body></html>'),renderer=new DomRenderer(projection.index,document.getElementById('app'),ARM+'shell');renderer.flush();
 const app={store,runtime,projection,renderer,flush:()=>renderer.flush()},ui=createGraphUI(app,{onError:e=>{throw e}}),bus=new EventBus(runtime,projection);bus.attach(renderer,h=>{app.flush();ui.handled(h)});
 const root=ui.grid(),calls=[],overview=createResourceOverview({ui,root,owner:u=>'Snapshot owner '+u.owner,onLease:id=>calls.push(id),help:()=>ui.button('Explain',()=>{})});ui.bind('monitorContent').replace(root);
 return{ui,store,document,window,calls,overview,node:h=>renderer.nodeOf(projection.index.rootOf(h.iri)),close(){overview.dispose();runtime.dispose();store.dispose?.();}};
}
it('resource metadata preserves exact quantities/IDs, paragraphs, native buttons and expanded detail identity across updates',()=>{
 const x=fixture();try{
 const user={lease:'a'.repeat(32),lease_display_name:'Synthetic build',reason:'Stage one.\n\nStage two.',agent:'synthetic-worker',owner:'1000',quantity:9223372036854775807n,project:'synthetic-project',state:'active',expires_at:'2026-11-01T12:00:00Z'};
 const resource={id:'synthetic-permits',kind:'custom',unit:'permits',available:0n,budget_total:7n,reserved:9223372036854775807n,users:[user]};x.overview.update([resource],{});
 const handle=label=>[...x.ui.handles.values()].find(h=>h.label===label),details=handle('Agent, project & expiry'),outer=handle('Accounting & consumers · 1'),action=handle('Synthetic build');outer.open=details.open=true;
 const node=x.node(action);expect(x.store.match(x.ui.nn(action.iri),x.ui.nn(RDF),x.ui.nn(C+'Button'))).toHaveLength(1);
 const next={...user,agent:'renewed-worker',expires_at:'2026-11-02T12:00:00Z'};x.overview.update([{...resource,users:[next,{lease:'b'.repeat(32),quantity:1n}]}],{});
 const metadata=[...x.ui.handles.values()].find(h=>h.kind==='DescriptionList'&&x.node(h)?.textContent.startsWith('Quantity'));const fields=x.store.match(x.ui.nn(metadata.iri),x.ui.nn(C+'fields'))[0].object.value;x.overview.update([{...resource,users:[next,{lease:'b'.repeat(32),quantity:1n}]}],{});expect(x.store.match(x.ui.nn(metadata.iri),x.ui.nn(C+'fields'))[0].object.value).toBe(fields);
 expect(x.node(action)).toBe(node);expect(details.open).toBe(true);expect(outer.open).toBe(true);expect(details.disposed).not.toBe(true);
 const pairs=[...x.document.querySelectorAll('[data-sem-c="arm-resource-facts"] .sem-pair')].map(n=>[n.querySelector('dt').textContent,n.querySelector('dd').textContent]);
 for(const pair of [['Quantity','9,223,372,036,854,775,807 permits'],['Owner','Snapshot owner 1000'],['State','active'],['Agent','renewed-worker'],['Project','synthetic-project'],['Exact expiry',next.expires_at]])expect(pairs).toContainEqual(pair);
 expect(x.document.querySelector('[data-sem-c="arm-resource-reason"]').textContent).toBe(user.reason);expect(x.document.body.textContent).toContain('Lease ID '+user.lease);
 expect(x.calls).toEqual([]);node.dispatchEvent(new x.window.Event('click',{bubbles:true}));expect(x.calls).toEqual([user.lease]);
 x.overview.update([resource],{},'no match');x.overview.update([resource],{});expect(details.open).toBe(true);expect(action.disposed).not.toBe(true);
 }finally{x.close();}
});
