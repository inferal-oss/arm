#!/usr/bin/env python3
"""Private file-only binding for one reviewed reconnect terminal fixture.

Never opens a socket, redeems a ticket, sends an RPC or starts/signals a process.
This is cooperative owned-fixture evidence, not a production database validator
or an atomic defence against a hostile same-account writer. Private output may
contain a one-shot browser ticket; console output deliberately does not.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import urllib.parse

_endpoint_spec=importlib.util.spec_from_file_location('shared_fixture_endpoint_schema',Path(__file__).with_name('mesh_reconnect_fixture_evidence.py'))
_endpoint_evidence=importlib.util.module_from_spec(_endpoint_spec);_endpoint_spec.loader.exec_module(_endpoint_evidence)

MAX_JSON=1<<20
READY_KEYS=frozenset(('mode','dashboard_url','source_home','arm','host','pending_key','outcome_file','required'))
COORD_KEYS=frozenset(('resume_from','parent_key','resolved_by','schema','key','kind','request_sha256','request','actor','targets','revision','phase','updated_at','worker','worker_expires_at','review','review_revision','review_sha256','review_expires_at','cancel_requested','approved','result','handoff','error'))
REQUIRED_TEXT='inspect blocker; explicit reviewed handoff; decline one exact reconnect review; approve a fresh exact reconnect'
IDENTITY_FIELDS=('host','owner','owner_username','name','certificate','fingerprint','spki_sha256','expires_at')


def digest(raw):return hashlib.sha256(raw).hexdigest()

def pairs(items):
    value={}
    for key,item in items:
        if key in value:raise ValueError('duplicate JSON field')
        value[key]=item
    return value

def decode(raw):
    def bad(_):raise ValueError('non-JSON constant')
    return json.loads(raw.decode('utf-8'),object_pairs_hook=pairs,parse_constant=bad)

def native(info):return {'device':info.st_dev,'inode':info.st_ino,'uid':info.st_uid,'mode':stat.S_IMODE(info.st_mode)}

def directory(path):
    before=path.lstat()
    if not stat.S_ISDIR(before.st_mode) or before.st_uid!=os.getuid() or stat.S_IMODE(before.st_mode)!=0o700:
        raise ValueError('exact private owned directory required')
    fd=os.open(path,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:
        info=os.fstat(fd)
        if native(info)!=native(before):raise ValueError('directory changed')
        return native(info)
    finally:os.close(fd)

def read(path,cap,executable=False):
    before=path.lstat()
    expected=0o700 if executable else 0o600
    if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or stat.S_IMODE(before.st_mode)!=expected:
        raise ValueError('private owned regular input required')
    fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    try:
        info=os.fstat(fd)
        if native(info)!=native(before) or info.st_size>cap:raise ValueError('input identity/size changed')
        with os.fdopen(fd,'rb',closefd=False) as stream:raw=stream.read(cap+1)
        if len(raw)>cap or len(raw)!=info.st_size:raise ValueError('bounded complete input required')
        after=os.fstat(fd)
        if native(after)!=native(info) or (after.st_size,after.st_mtime_ns,after.st_ctime_ns)!=(info.st_size,info.st_mtime_ns,info.st_ctime_ns):
            raise ValueError('input changed while reading')
        return raw,{'native':native(info),'sha256':digest(raw),'size':len(raw)}
    finally:os.close(fd)

def read_json(path,cap=MAX_JSON):
    raw,proof=read(path,cap);return decode(raw),proof

def key(value):
    if type(value) is not str or not 1<=len(value.encode('utf-8'))<=128 or any(ord(c)<32 or ord(c)>126 for c in value):
        raise ValueError('exact bounded printable fixture key required')
    return value

def host(value):
    if type(value) is not str or re.fullmatch('[0-9a-f]{32}',value) is None:raise ValueError('stable host ID required')
    return value

def origin(text,endpoint=False):
    if type(text) is not str or len(text)>2048:raise ValueError('bounded local URL required')
    value=urllib.parse.urlsplit(text)
    if (value.scheme!=('ws' if endpoint else 'http') or value.hostname!='127.0.0.1' or value.username is not None or value.password is not None or
            value.path not in (('/v1/ws',) if endpoint else ('','/')) or value.query or value.port is None or not 1<=value.port<=65535):
        raise ValueError('dedicated literal loopback HTTP authority required')
    return 'http://127.0.0.1:'+str(value.port),value

def principal(identity):
    if type(identity) is not dict or set(identity)!=set(IDENTITY_FIELDS)|{'address'}:
        raise ValueError('exact current identity shape required')
    if any(type(identity[x]) is not str or not identity[x] for x in IDENTITY_FIELDS):raise ValueError('complete principal required')
    host(identity['host']);return {k:identity[k] for k in IDENTITY_FIELDS}

def operation(home,selected):
    selected=key(selected)
    path=home/'mesh-lifecycle'/('coordinator-operation-'+digest(selected.encode())+'.json')
    value,proof=read_json(path)
    if type(value) is not dict or set(value)-COORD_KEYS or value.get('schema')!=1 or value.get('key')!=selected or type(value.get('revision')) is not int or value['revision']<1:
        raise ValueError('exact coordinator receipt required')
    return value,proof

def context(root,arm_sha):
    if not root.is_absolute() or '..' in root.parts:raise ValueError('exact absolute fixture root required')
    if re.fullmatch('[0-9a-f]{64}',arm_sha or '') is None:raise ValueError('selected ARM digest required')
    dirs={str(q.relative_to(root)):directory(q) for q in (root,root/'source',root/'source/mesh',root/'source/mesh-lifecycle',root/'destination',root/'destination/mesh')}
    ready,ready_proof=read_json(root/'ui-ready.json',16384)
    if type(ready) is not dict or set(ready)!=READY_KEYS or ready['mode']!='terminal' or ready['required']!=REQUIRED_TEXT:
        raise ValueError('closed dedicated terminal marker required')
    expected={'source_home':str(root/'source'),'arm':str(root/'arm'),'outcome_file':str(root/'ui-outcome.json')}
    if any(ready[k]!=v for k,v in expected.items()):raise ValueError('marker path escaped selected fixture')
    target=host(ready['host']);key(ready['pending_key'])
    _,arm_proof=read(root/'arm',64<<20,True)
    if arm_proof['sha256']!=arm_sha:raise ValueError('selected ARM image changed')
    ep,ep_proof=read_json(root/'source/endpoint.json',16384)
    _endpoint_evidence.validate_fixture_endpoint(ep)
    endpoint_origin,endpoint_url=origin(ep['url'],endpoint=True)
    dashboard_origin,url=origin(ready['dashboard_url'])
    if endpoint_url.fragment or dashboard_origin!=endpoint_origin or re.fullmatch('connect=[0-9a-f]{64}',url.fragment) is None:
        raise ValueError('exact selected endpoint and one dashboard ticket required')
    source,_=read_json(root/'source/mesh/config.json');target_config,_=read_json(root/'destination/mesh/config.json')
    source_principal=principal(source.get('identity'));target_principal=principal(target_config.get('identity'))
    if source_principal['host']==target or target_principal['host']!=target:raise ValueError('fixture host binding changed')
    peers=source.get('peers')
    if type(peers) is not list or len(peers)!=1 or principal(peers[0].get('identity'))!=target_principal:
        raise ValueError('exact single selected peer required')
    for q in (root,root/'source',root/'source/mesh',root/'source/mesh-lifecycle',root/'destination',root/'destination/mesh'):
        if directory(q)!=dirs[str(q.relative_to(root))]:raise ValueError('fixture directory changed')
    return {'schema':'arm.mesh-reconnect-private-terminal-binding/v1','root':str(root),'directories':dirs,'ready':ready,'ready_proof':ready_proof,
        'arm_proof':arm_proof,'endpoint_proof':ep_proof,'endpoint_pid':ep['pid'],'endpoint_generation':ep['backend_generation'],'origin':endpoint_origin,
        'source_principal':source_principal,'target_principal':target_principal}

def _base_begin(root,arm_sha):
    value=context(root,arm_sha);op,proof=operation(root/'source',value['ready']['pending_key'])
    if (op.get('kind')!='disconnect' or op.get('phase')!='incomplete' or op.get('worker') or op.get('resolved_by') or
            op.get('approved') is not True or op.get('targets')!=['host:'+value['ready']['host']] or
            op.get('request',{}).get('key')!=op['key'] or op.get('request',{}).get('kind')!='disconnect' or
            op.get('request',{}).get('disconnect',{}).get('host')!=value['ready']['host']):
        raise ValueError('exact inactive incomplete disconnect required')
    value['pending_receipt']={'key':op['key'],'revision':op['revision'],'proof':proof,'request':op['request'],'review_sha256':op.get('review_sha256')}
    return value

def _base_finish(binding,binding_sha,declined_key,completed_key):
    saved,proof=read_json(binding)
    if proof['sha256']!=binding_sha or type(saved) is not dict or saved.get('schema')!='arm.mesh-reconnect-private-terminal-binding/v1':
        raise ValueError('exact saved terminal binding required')
    root=Path(saved['root']);now=context(root,saved['arm_proof']['sha256'])
    for name in ('root','directories','ready','ready_proof','arm_proof','endpoint_proof','endpoint_pid','endpoint_generation','origin','source_principal','target_principal'):
        if now[name]!=saved[name]:raise ValueError('terminal fixture generation changed: '+name)
    selected=saved['pending_receipt']['key'];keys=(key(selected),key(declined_key),key(completed_key))
    if len(set(keys))!=3:raise ValueError('handoff, declined and approved keys must be distinct')
    pending,pending_proof=operation(root/'source',selected)
    declined,declined_proof=operation(root/'source',declined_key)
    completed,completed_proof=operation(root/'source',completed_key)
    if (pending.get('phase')!='handed_off' or pending.get('kind')!='disconnect' or
            pending['revision']<=saved['pending_receipt']['revision'] or not pending.get('handoff') or
            pending.get('request')!=saved['pending_receipt']['request'] or pending.get('review_sha256')!=saved['pending_receipt']['review_sha256']):
        raise ValueError('exact reviewed handoff receipt required')
    if (declined.get('kind')!='reconnect' or declined.get('phase')!='rejected' or declined.get('approved') is not False or
            declined.get('error',{}).get('code')!='confirmation_declined' or not declined.get('review')):
        raise ValueError('exact unapproved declined reconnect required')
    if (completed.get('kind')!='reconnect' or completed.get('phase')!='completed' or completed.get('approved') is not True or
            not completed.get('review') or not completed.get('review_sha256') or completed.get('result',{}).get('connected')!=saved['ready']['host']):
        raise ValueError('exact approved reviewed reconnect result required')
    for op in (declined,completed):
        if (op.get('request',{}).get('key')!=op['key'] or op.get('request',{}).get('kind')!='reconnect' or
                op.get('request',{}).get('reconnect',{}).get('draft',{}).get('host')!=saved['ready']['host']):
            raise ValueError('fresh review request target differs from selected node')
    outcome={'handoff_key':selected,'declined_key':declined_key,'reconnect_key':completed_key}
    return root/'ui-outcome.json',outcome,{'schema':'arm.mesh-reconnect-private-terminal-completion/v1','binding_sha256':binding_sha,'origin':saved['origin'],
        'endpoint_pid':saved['endpoint_pid'],'endpoint_generation':saved['endpoint_generation'],'receipts':{'handoff':pending_proof,'declined':declined_proof,'completed':completed_proof},'outcome':outcome}

def exclusive(path,value):
    directory(path.parent)
    raw=(json.dumps(value,indent=2,ensure_ascii=False)+'\n').encode('utf-8')
    if len(raw)>MAX_JSON:raise ValueError('bounded private output required')
    fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'wb') as stream:stream.write(raw);stream.flush();os.fsync(stream.fileno())
    return digest(raw)

# Derived from the immutable web binder above. Additional terminal requirements
# cannot be satisfied by a web binding or by process absence alone.
_spec=importlib.util.spec_from_file_location('terminal_binding_state',Path(__file__).with_name('mesh_reconnect_terminal_state.py'))
state=importlib.util.module_from_spec(_spec);_spec.loader.exec_module(state)
STAGES={'dashboard','blocked-reconnect','handoff-default','handoff-confirm','fresh-review-decline','fresh-review-approve','completed','child-exit'}

def begin(root,arm_sha,rendered_dir):
 value=_base_begin(root,arm_sha);chosen,_=read_json(root/'terminal-reservation.json',16384);current=state.observe(chosen)
 if current['state']!='open' or current['closing']:raise ValueError('unused open terminal reservation required')
 expected={'root':str(root),'root_native':directory(root),'endpoint_sha256':value['endpoint_proof']['sha256'],'arm_sha256':arm_sha,'pending_key':value['pending_receipt']['key'],'pending_revision':value['pending_receipt']['revision'],'recovery_sha256':current['binding']['recovery_sha256']}
 if current['binding']!=expected:raise ValueError('terminal reservation differs from exact fixture binding')
 rendered_dir=Path(rendered_dir)
 if not rendered_dir.is_absolute() or '..' in rendered_dir.parts or rendered_dir==root or root in rendered_dir.parents:raise ValueError('separate private rendered evidence directory required')
 summary,summary_proof=read_json(root/'terminal-reviewed-operation.json')
 if summary.get('key')!=value['pending_receipt']['key'] or summary.get('revision')!=value['pending_receipt']['revision'] or summary.get('recovery_sha256')!=current['binding']['recovery_sha256'] or summary.get('phase')!='incomplete' or summary.get('kind')!='disconnect':raise ValueError('exact original native operation summary required')
 value['native_operation_summary']=summary_proof
 value['terminal_selection']=chosen;value['pending_receipt']['recovery_sha256']=current['binding']['recovery_sha256'];value['rendered_directory']={'path':str(rendered_dir),'native':directory(rendered_dir)}
 return value

def selected_binding(path,want):
 saved,proof=read_json(path)
 if proof['sha256']!=want or saved.get('schema')!='arm.mesh-reconnect-private-terminal-binding/v1':raise ValueError('exact saved terminal binding required')
 now=context(Path(saved['root']),saved['arm_proof']['sha256'])
 for name in ('root','directories','ready','ready_proof','arm_proof','endpoint_proof','endpoint_pid','endpoint_generation','origin','source_principal','target_principal'):
  if now[name]!=saved[name]:raise ValueError('terminal fixture generation changed: '+name)
 d=saved['rendered_directory'];root=Path(d['path'])
 if directory(root)!=d['native']:raise ValueError('rendered evidence directory replaced')
 return saved,root

def check_default(path,want):
 saved,rendered=selected_binding(path,want);op,proof=operation(Path(saved['root'])/'source',saved['pending_receipt']['key'])
 if proof!=saved['pending_receipt']['proof'] or op['revision']!=saved['pending_receipt']['revision']:raise ValueError('default keep changed pending receipt')
 evidence={'schema':1,'binding_sha256':want,'key':op['key'],'revision':op['revision'],'proof':proof}
 destination=rendered/'default-decline.json';return destination,evidence

def artifact(path,want):
 before=path.lstat()
 if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or stat.S_IMODE(before.st_mode) not in (0o600,0o644) or before.st_nlink!=1 or not 0<before.st_size<=8<<20:raise ValueError('bounded owned screenshot file required')
 fd=os.open(path,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
 try:
  version=lambda s:(native(s),s.st_size,s.st_mtime_ns,s.st_ctime_ns,s.st_nlink)
  if version(os.fstat(fd))!=version(before):raise ValueError('screenshot changed at open')
  with os.fdopen(fd,'rb',closefd=False) as stream:raw=stream.read((8<<20)+1)
  if len(raw)!=before.st_size or version(os.fstat(fd))!=version(before) or version(path.lstat())!=version(before) or digest(raw)!=want:raise ValueError('screenshot changed during read')
 finally:os.close(fd)
 if not (raw.startswith(b'\x89PNG\r\n\x1a\n') or raw.startswith(b'\xff\xd8')):raise ValueError('PNG/JPEG screenshot bytes required')
 return {'native':native(before),'size':len(raw),'sha256':want}

def finish(binding,binding_sha,declined_key,completed_key,rendered_manifest,rendered_sha):
 saved,rendered=selected_binding(binding,binding_sha);root=Path(saved['root']);ledger=state.observe(saved['terminal_selection'])
 if ledger['state']!='closed_positive' or ledger['receipt']['status']!='passed' or ledger['owner']['binding_sha256']!=binding_sha:raise ValueError('positive exact TUI child completion required')
 child,child_proof=read_json(root/'terminal-child.json',16384)
 if child!=ledger['receipt']:raise ValueError('terminal child receipt changed')
 transcript_proofs={}
 for label,filename,cap in [('input','terminal-input.bin',1<<20),('output','terminal-output.ansi',4<<20)]:
  _,proof=read(root/filename,cap)
  if proof['sha256']!=child['transcripts'][label]['sha256'] or proof['size']!=child['transcripts'][label]['bytes']:raise ValueError('terminal transcript differs')
  transcript_proofs[label]=proof
 default,default_proof=read_json(rendered/'default-decline.json',16384)
 if type(default) is not dict or set(default)!={'schema','binding_sha256','key','revision','proof'} or default['schema']!=1 or default['binding_sha256']!=binding_sha or default['key']!=saved['pending_receipt']['key'] or default['revision']!=saved['pending_receipt']['revision'] or default['proof']!=saved['pending_receipt']['proof']:raise ValueError('exact unchanged default-decline observation required')
 if rendered_manifest!=rendered/'actions.json':raise ValueError('exact selected rendered action manifest required')
 actions,actions_proof=read_json(rendered_manifest)
 if actions_proof['sha256']!=rendered_sha or type(actions) is not dict or set(actions)!={'schema','source','window_id','new_window','actions'} or actions['schema']!=1 or actions['source']!='root-cua-terminal-window' or actions['new_window'] is not True or type(actions['window_id']) is not str or not 1<=len(actions['window_id'])<=256:raise ValueError('closed actual Terminal action evidence required')
 if type(actions['actions']) is not list or not 8<=len(actions['actions'])<=16:raise ValueError('bounded actual Terminal screenshot roster required')
 seen=set();filenames=set();images=[];total=0
 for item in actions['actions']:
  if type(item) is not dict or set(item)!={'stage','file','sha256'} or item['stage'] not in STAGES or item['file'] in filenames or type(item['file']) is not str or re.fullmatch('[A-Za-z0-9_-]{1,96}\.(png|jpg|jpeg)',item['file']) is None:raise ValueError('closed unique terminal screenshot selection required')
  state.digest(item['sha256']);proof=artifact(rendered/item['file'],item['sha256']);total+=proof['size'];images.append({'stage':item['stage'],'file':item['file'],'proof':proof});seen.add(item['stage']);filenames.add(item['file'])
 if seen!=STAGES or total>64<<20:raise ValueError('complete bounded actual Terminal stages required')
 path,outcome,receipt=_base_finish(binding,binding_sha,declined_key,completed_key)
 receipt.update(terminal_child=child_proof,terminal_transcripts=transcript_proofs,default_decline=default_proof,rendered_actions=actions_proof,rendered_images=images,window_id=actions['window_id'],rendering_scope='actual root CUA screenshots; transcript/header checks alone are not visual attestation')
 return path,outcome,receipt

def main():
 p=argparse.ArgumentParser(description=__doc__);sub=p.add_subparsers(dest='verb',required=True)
 b=sub.add_parser('begin');b.add_argument('--root',type=Path,required=True);b.add_argument('--arm-sha256',required=True);b.add_argument('--rendered-dir',type=Path,required=True);b.add_argument('--out',type=Path,required=True)
 for verb in ('check-default','finish'):
  f=sub.add_parser(verb);f.add_argument('--binding',type=Path,required=True);f.add_argument('--binding-sha256',required=True)
  if verb=='finish':
   f.add_argument('--declined-key',required=True);f.add_argument('--completed-key',required=True);f.add_argument('--rendered-manifest',type=Path,required=True);f.add_argument('--rendered-sha256',required=True);f.add_argument('--evidence-out',type=Path,required=True)
 a=p.parse_args()
 if a.verb=='begin':
  value=begin(a.root,a.arm_sha256,a.rendered_dir);h=exclusive(a.out,value);print(json.dumps({'binding':str(a.out),'sha256':h,'pending_key':value['pending_receipt']['key'],'revision':value['pending_receipt']['revision'],'recovery_sha256':value['pending_receipt']['recovery_sha256']}))
 elif a.verb=='check-default':
  path,value=check_default(a.binding,a.binding_sha256);h=exclusive(path,value);print(json.dumps({'default_evidence':str(path),'sha256':h}))
 else:
  path,value,receipt=finish(a.binding,a.binding_sha256,a.declined_key,a.completed_key,a.rendered_manifest,a.rendered_sha256);h=exclusive(a.evidence_out,receipt);outcome=exclusive(path,value);print(json.dumps({'evidence':str(a.evidence_out),'sha256':h,'outcome':str(path),'outcome_sha256':outcome}))
if __name__=='__main__':main()
