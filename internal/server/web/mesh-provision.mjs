import {bytes,stringifyJSON} from './core.mjs';
import {jsonView} from './json-view.mjs';

const hash=(v,n=64)=>typeof v==='string'&&new RegExp(`^[a-f0-9]{${n}}$`).test(v);
const key=v=>typeof v==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(v);
const text=(v,max)=>typeof v==='string'&&v.trim().length>0&&new TextEncoder().encode(v).length<=max&&!/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/u.test(v);
const absolute=v=>text(v,4096)&&v.startsWith('/')&&!v.includes('\\')&&!v.split('/').slice(1).some(p=>p===''||p==='.'||p==='..');
const integer=(v,min=0n,max=9223372036854775807n)=>(typeof v==='bigint'||Number.isSafeInteger(v))&&BigInt(v)>=min&&BigInt(v)<=max;
const same=(a,b)=>stringifyJSON(a)===stringifyJSON(b);
const platforms=['darwin/amd64','darwin/arm64','linux/amd64','linux/arm64'];

export async function provisionKey(connectionKey){
 if(!key(connectionKey))throw Error('An exact connection key is required.');
 const bytes=new Uint8Array(await crypto.subtle.digest('SHA-256',new TextEncoder().encode('arm-node-provision:'+connectionKey)));
 return [...bytes].map(v=>v.toString(16).padStart(2,'0')).join('');
}

// This editor supplies intent only. Native code resolves the platform, account,
// trusted payload, paths and startup policy; browser data never chooses them.
export async function provisionDraft(connectionKey,{destination='',connector='',reason}){
 if(!text(reason,4096))throw Error('Explain why ARM should be installed on this host (up to 4096 bytes).');
 if(Boolean(destination)===Boolean(connector))throw Error('Choose one SSH destination or reviewed connector.');
 if(destination&&(!text(destination,512)||destination.startsWith('-')||/\s/.test(destination)))throw Error('Use a verified SSH destination or configured alias, without command options.');
 if(connector&&!key(connector))throw Error('Choose a reviewed native connector.');
 return{key:await provisionKey(connectionKey),provision:{connection_key:connectionKey,...(connector?{connector}:{ssh_destination:destination}),reason}};
}

// Recovery retains the submitted intent as well as its key until the native
// review arrives. This data describes the request and never grants authority.
export function validProvisionIntent(q){
 const d=q?.provision;
 if(!q||Object.keys(q).sort().join(',')!=='key,provision'||!hash(q.key)||!d||!key(d.connection_key)||!text(d.reason,4096))return false;
 if(Object.keys(d).sort().join(',')==='connection_key,reason,ssh_destination')return text(d.ssh_destination,512)&&!d.ssh_destination.startsWith('-')&&!/\s/.test(d.ssh_destination);
 return Object.keys(d).sort().join(',')==='connection_key,connector,reason'&&key(d.connector);
}

function supportedSeed(seed,t){
 if(seed==null)return true;
 const m=seed.manifest;
 if(seed.schema!==1||!hash(seed.manifest_sha256)||m?.schema!==1||m.expected_arm_version!==t.expected_arm_version||m.expected_lifecycle_protocol!==t.expected_lifecycle_protocol||!text(m.integrity_scope,4096)||!Array.isArray(m.artifacts)||m.artifacts.length!==4)return false;
 let selected=false;
 for(let i=0;i<m.artifacts.length;i++){
  const a=m.artifacts[i];
  if(`${a.os}/${a.arch}`!==platforms[i]||a.name!==`arm-${a.os}-${a.arch}`||!text(a.go_version,128)||!a.go_version.startsWith('go1.')||!hash(a.sha256)||!hash(a.compressed_sha256)||!integer(a.size_bytes,1n,268435456n)||!integer(a.compressed_size_bytes,1n,268435456n))return false;
  if(a.os===t.os&&a.arch===t.arch)selected=a.sha256===t.sha256&&BigInt(a.size_bytes)===BigInt(t.size_bytes);
 }
 return selected;
}

export function supportedProvisionReview(p){
 const q=p?.request,t=q?.transfer,s=q?.startup,r=s?.resources;
 return p?.operation==='install-and-start-arm-node'&&hash(p.key)&&q&&key(q.connection_key)&&t?.key===p.key&&
  text(q.ssh_destination,512)&&!q.ssh_destination.startsWith('-')&&text(q.ssh_identity?.account,128)&&text(q.ssh_identity?.host_key,4096)&&absolute(q.remote_home)&&
  absolute(t.install_root)&&hash(t.sha256)&&integer(t.size_bytes,1n,268435456n)&&typeof t.expected_uid==='string'&&/^(0|[1-9][0-9]*)$/.test(t.expected_uid)&&BigInt(t.expected_uid)<=4294967295n&&
  platforms.includes(`${t.os}/${t.arch}`)&&text(t.expected_arm_version,96)&&t.expected_lifecycle_protocol===1&&text(t.reason,4096)&&
  p.final_binary===`${t.install_root}/artifact-${t.sha256}/arm`&&text(p.artifact_source,4096)&&text(p.effect,16384)&&
  s?.schema===1&&typeof s.listen==='string'&&/^(127\.\d{1,3}\.\d{1,3}\.\d{1,3}|\[::1\]):\d{1,5}$/.test(s.listen)&&Number(s.listen.slice(s.listen.lastIndexOf(':')+1))<=65535&&
  Number.isFinite(r?.cpu)&&r.cpu>0&&integer(r.memory_bytes)&&integer(r.disk_bytes)&&supportedSeed(q.seed_catalog,t);
}

export function assertProvisionOperation(op,expectedKey,review=null){
 if(op?.kind!=='provision'||op.key!==expectedKey||!hash(expectedKey)||!integer(op.revision,1n)||!Array.isArray(op.targets)||op.targets.length!==1||typeof op.targets[0]!=='string'||!op.targets[0].startsWith('ssh:'))throw Error('The installation receipt does not match the exact original operation. Keep its key and inspect it.');
 if(op.review&&(op.review.key!==op.key||op.review.request?.transfer?.key!==op.key||op.targets[0]!==`ssh:${op.review.request?.ssh_destination}`||!hash(op.review_sha256)||!integer(op.review_revision,1n)||BigInt(op.review_revision)>BigInt(op.revision)))throw Error('The installation review changed its original key, SSH destination or decision revision.');
 if(review?.target&&op.targets[0]!==review.target)throw Error('The installation receipt changed its original SSH destination.');
 if(review?.plan){
  const provisional=review.provisional===true&&integer(review.revision,1n)&&integer(op.review_revision,1n)&&BigInt(op.review_revision)>BigInt(review.revision)&&BigInt(op.review_revision)<=BigInt(op.revision);
  if(!same(review.plan,op.review)||review.hash!==op.review_sha256||review.revision!==op.review_revision&&!provisional)throw Error('The immutable installation review changed. No decision or upload was repeated.');
 }
 return op;
}

export function assertProvisionPreparation(op,q){
 assertProvisionOperation(op,q.key);
 const draft=q.provision,p=op.review?.request;
 if(draft.ssh_destination&&op.targets[0]!==`ssh:${draft.ssh_destination}`||p&&(p.connection_key!==draft.connection_key||p.transfer?.reason!==draft.reason))throw Error('The installation preparation changed the requested destination, linked key or purpose. Retain its exact key and inspect it.');
 return op;
}

// Native projection supplies the full settled-receipt/catalog check. Public
// field bindings here additionally prevent UI success or connection links from
// borrowing another source operation, artifact or started runtime.
export function provisionConfirmed(op){
 const p=op?.review,q=p?.request,t=q?.transfer,r=op?.result,c=r?.receipt,n=c?.runtime;
 return op?.kind==='provision'&&hash(op.key)&&integer(op.revision,1n)&&integer(op.review_revision,1n)&&BigInt(op.review_revision)<=BigInt(op.revision)&&op.approved===true&&op.phase==='completed'&&op.worker_live===false&&!op.error&&op.provision_connection_ready===true&&
  supportedProvisionReview(p)&&p.key===op.key&&hash(op.review_sha256)&&Array.isArray(op.targets)&&op.targets.length===1&&op.targets[0]===`ssh:${q.ssh_destination}`&&
  r?.key===op.key&&r.connection_key===q.connection_key&&r.provisioned===p.final_binary&&c?.schema===1&&same(c.request,t)&&c.binary===p.final_binary&&
  c.phase===(q.seed_catalog?'seeded_verified':'started_verified')&&hash(c.started_host,32)&&c.start_home===q.remote_home&&same(c.startup,q.startup)&&
  n?.host===c.started_host&&n.uid===t.expected_uid&&n.sha256===t.sha256&&integer(n.size_bytes,1n)&&BigInt(n.size_bytes)===BigInt(t.size_bytes)&&n.arm_version===t.expected_arm_version&&n.lifecycle_protocol===t.expected_lifecycle_protocol&&
  n.method===(t.os==='linux'?'kernel-self-executable':'startup-open-file-snapshot')&&Number.isFinite(Date.parse(n.captured_at));
}

export function provisionLabel(op){
 if(provisionConfirmed(op))return 'ARM installation completed';
 if(op?.phase==='completed'&&op.approved===true&&op.worker_live===false&&!op.error&&op.has_result===true&&!op.review&&!op.result)return 'Installation result recorded';
 if(op?.phase==='rejected'&&op.error?.code==='confirmation_declined')return 'Installation declined · unchanged';
 if(op?.phase==='awaiting_review'&&!op.approved)return 'Review ARM installation';
 if(op?.phase==='preparing'&&!op.approved)return 'Preparing installation review';
 if(op?.phase==='review_declined'&&!op.approved&&op.worker_live&&!op.error)return 'Keeping host unchanged';
 if(['review_approved','applying'].includes(op?.phase)&&op.approved&&op.worker_live&&!op.error)return 'Installing reviewed ARM node';
 return 'Installation outcome needs inspection';
}

export function provisionAdvice(op){
 if(op?.review&&!supportedProvisionReview(op.review))return 'This dashboard does not manage this installation review. Inspect the exact original receipt using the CLI; no approval is available here.';
 if(provisionConfirmed(op))return 'This receipt records the completed ARM installation. Installation grants no peer trust; mesh access has a separate connection review and receipt.';
 if(['review_approved','applying'].includes(op?.phase)&&op.approved&&op.worker_live&&!op.error)return 'The reviewed installation workflow is running. Keep its exact key to inspect the outcome; closing this view does not repeat the installation.';
 if(op?.approved||op?.lookup_only)return 'Installation may already have effects. Inspect this exact key; upload, startup and catalog transfer are never automatically repeated.';
 return 'Read-only discovery selects trusted local bytes and exact user-level paths. Review installation before any remote write; keeping the host unchanged is the default.';
}

export function provisionReviewView(p,{document=globalThis.document}={}){
 const el=(tag,cls='',value=null)=>{const n=document.createElement(tag);n.className=cls;if(value!==null)n.textContent=String(value);return n;};
 const root=el('section','mesh-provision-review'),q=p?.request,t=q?.transfer;
 if(!supportedProvisionReview(p)){root.append(el('p','mesh-note warning','Exact supported installation review unavailable. Keep this host unchanged and inspect the original receipt.'),jsonView(p,{label:'Unsupported installation review',document}));return root;}
 const head=el('div','mesh-stop-heading');head.append(el('span','mesh-stop-symbol','↗'),el('div'));head.children[1].append(el('h3','','Install ARM for this user'),el('p','','Installation and mesh trust are separate decisions.'));root.append(head);
 const grid=el('div','mesh-stop-grid');
 const sections=[['DESTINATION',[['SSH destination',q.ssh_destination],['Run as',`${q.ssh_identity?.account??'Unknown'} (UID ${t.expected_uid??'Unknown'})`],['ARM home',q.remote_home],['Private executable',p.final_binary]]],['VERIFIED SOFTWARE',[['Platform',`${t.os} / ${t.arch}`],['ARM version',t.expected_arm_version],['Image size',bytes(t.size_bytes)],['Local source','Trusted ARM companion']]]];
 for(const[title,rows]of sections){const card=el('section','mesh-stop-card'),facts=el('dl','mesh-facts');card.append(el('h4','',title));for(const[label,value]of rows){const row=el('div','mesh-fact');row.append(el('dt','',label),el('dd','mesh-mono',value??'Not reported'));facts.append(row);}card.append(facts);grid.append(card);}root.append(grid);
 const boundary=el('section','mesh-uninstall-preserved');boundary.append(el('h4','','TWO SEPARATE DECISIONS'),el('p','','1. Install and start the reviewed user-level ARM node.'),el('p','','2. Review the node identity and choose mesh permissions. Installation grants no peer access.'));root.append(boundary);
 if(q.seed_catalog){const seed=el('section','mesh-stop-card');seed.append(el('h4','','COMPANIONS FOR FUTURE NODES'),el('p','','The four reviewed compressed companions are retained for onward provisioning. They grant no mesh access.'));for(const a of q.seed_catalog.manifest?.artifacts??[])seed.append(el('p','mesh-mono',`${a.os} / ${a.arch} · ${bytes(a.compressed_size_bytes)} compressed · ${bytes(a.size_bytes)} executable`));root.append(seed);}
 const resources=q.startup.resources,details=el('details','mesh-stop-completion');details.append(el('summary','','SSH pin, image digest and startup settings'),el('code','mesh-stop-completion-detail',`SSH host key: ${q.ssh_identity.host_key}\nExecutable SHA-256: ${t.sha256}\nLocal source: ${p.artifact_source}\nLocal listener: ${q.startup.listen}\nCPU budget: ${resources.cpu}\nMemory ceiling: ${BigInt(resources.memory_bytes)>0n?bytes(resources.memory_bytes):'Current host availability'}\nDisk ceiling: ${BigInt(resources.disk_bytes)>0n?bytes(resources.disk_bytes):'Current host availability'}`));root.append(details);
 root.append(el('h4','','WHY'),el('p','mesh-stop-reason',t.reason),el('p','mesh-note',p.effect));
 root.append(jsonView(p,{label:'Full immutable installation and startup review',document}));return root;
}
