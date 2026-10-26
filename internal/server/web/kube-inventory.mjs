// Local Kubernetes inventory only. Selecting a Node does not create a Pod or
// reserve live ARM capacity; those need an exact separate operation review.
export function createKubeInventory({root,call,connected,document=root.ownerDocument}) {
  const el=(tag,cls='',value='')=>{const n=document.createElement(tag);n.className=cls;n.textContent=value;return n;};
  const section=el('section','mesh-operations'),head=el('div','mesh-toolbar');
  const title=el('div');title.append(el('p','eyebrow','KUBERNETES'),el('h2','','Available Nodes'),el('p','mesh-muted','Inspect schedulable resources before preparing a separate reviewed Pod plan.'));
  const open=el('button','','Browse Kubernetes Nodes');open.type='button';
  head.append(title,open);section.append(head);
  const content=el('div','kube-inventory');content.hidden=true;section.append(content);root.append(section);
  const status=el('p','mesh-inline-notice');status.setAttribute('role','status');
  const controls=el('div','mesh-limit-fields'),context=el('select'),namespace=el('input'),query=el('input');
  namespace.placeholder='Context namespace';query.placeholder='Type a Node name, architecture, zone or instance type';query.maxLength=128;
  for(const [label,field] of [['Context',context],['Namespace',namespace],['Find a Node',query]]){const wrap=el('label','mesh-field');wrap.append(el('span','',label),field);controls.append(wrap);}
  const cards=el('div','mesh-nodes'),selected=el('div','kube-selected');
  content.append(controls,status,cards,selected);
  let opened=false,sequence=0,timer=null,selection=null,choices=[];
  const clear=()=>{selection=null;selected.replaceChildren();};
  const detail=(name,value)=>{const line=el('div','kube-fact');line.append(el('strong','',name),el('span','',value||'Not reported'));return line;};
  function render(data,request,version){
    cards.replaceChildren();
    const nodes=Array.isArray(data?.nodes)?data.nodes:[];
    status.textContent=`${nodes.length} matching Nodes in ${data?.namespace?.name??namespace.value}. CPU, memory and storage are Kubernetes allocatable figures, not live ARM admission.`;
    for(const node of nodes){
      const selectable=node.ready&&!node.unschedulable&&node.uid&&node.os==='linux'&&['amd64','arm64'].includes(node.arch)&&!(node.taints?.length);
      const card=el('article','mesh-node-card'),heading=el('h3','',node.name??'Unnamed Node');
      card.append(heading,detail('Architecture',`${node.os??'?'} / ${node.arch??'?'}`),detail('CPU allocatable',node.allocatable_cpu),detail('RAM allocatable',node.allocatable_memory),detail('Disk allocatable',node.allocatable_ephemeral),detail('CPU capacity',node.capacity?.cpu),detail('RAM capacity',node.capacity?.memory),detail('Disk capacity',node.capacity?.['ephemeral-storage']),detail('Zone',node.zone),detail('Instance type',node.instance_type),detail('State',selectable?'Available for ARM Pod review':'Unavailable for ARM Pod review'));
      for(const [name,value] of Object.entries(node.allocatable??{}).filter(([name])=>!['cpu','memory','ephemeral-storage'].includes(name)).slice(0,12))card.append(detail(name,value));
      if(node.taints?.length)card.append(detail('Taints',node.taints.join(', ')));
      const choose=el('button','','Select this Node');choose.type='button';choose.disabled=!selectable;
      choose.addEventListener('click',()=>{if(version!==sequence||!opened||!connected()||context.value!==request.context||namespace.value.trim()!==request.namespace||query.value.trim()!==request.query)return;selection={context:request.context,namespace:data.namespace,node};selected.replaceChildren(el('h3','','Selected Node'),detail('Context',context.value),detail('Namespace',data.namespace?.name),detail('Name',node.name),detail('Exact UID',node.uid),detail('Architecture',node.arch),detail('CPU / RAM / disk',`${node.allocatable_cpu??'?'} / ${node.allocatable_memory??'?'} / ${node.allocatable_ephemeral??'?'}`),el('p','mesh-muted','Use this context, namespace, Node name and UID in a separate current Pod plan. Selection alone creates nothing.'));});
      card.append(choose);cards.append(card);
    }
  }
  async function search(){
    const version=++sequence;clear();cards.replaceChildren();if(!opened)return;if(!context.value||!connected()){status.textContent='Select a context while connected.';return;}
    status.textContent='Reading current Kubernetes Node inventory…';
    const request={context:context.value,namespace:namespace.value.trim(),query:query.value.trim(),limit:50};
    try{const data=await call('kube_nodes',request);if(version===sequence&&opened)render(data,request,version);}
    catch(error){if(version===sequence){cards.replaceChildren();status.textContent=`Node inventory unavailable: ${error.message}`;}}
  }
  function schedule(){++sequence;clear();cards.replaceChildren();if(timer)clearTimeout(timer);if(!opened)return;timer=setTimeout(()=>{timer=null;search();},180);}
  context.addEventListener('change',()=>{namespace.value=choices.find(c=>c.name===context.value)?.namespace??'';search();});
  namespace.addEventListener('input',schedule);namespace.addEventListener('change',search);query.addEventListener('input',schedule);
  open.addEventListener('click',async()=>{
    opened=!opened;const version=++sequence;content.hidden=!opened;open.textContent=opened?'Hide Kubernetes Nodes':'Browse Kubernetes Nodes';
    if(!opened){++sequence;clear();cards.replaceChildren();if(timer)clearTimeout(timer);timer=null;return;}
    if(!connected()){status.textContent='Connect to the local backend to browse Kubernetes Nodes.';return;}
    status.textContent='Loading Kubernetes contexts…';
    try{const loaded=await call('kube_contexts');if(version!==sequence||!opened)return;choices=loaded;context.replaceChildren();for(const choice of choices){const option=el('option','',`${choice.name} · ${choice.cluster_name}`);option.value=choice.name;context.append(option);}const preferred=choices.find(c=>c.current)??choices[0];if(!preferred){status.textContent='No Kubernetes contexts are available.';return;}context.value=preferred.name;namespace.value=preferred.namespace;await search();}
    catch(error){if(version===sequence&&opened)status.textContent=`Kubernetes contexts unavailable: ${error.message}`;}
  });
  return {getSelection:()=>selection,refresh:search};
}
