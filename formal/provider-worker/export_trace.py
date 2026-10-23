#!/usr/bin/env python3
"""Extract one attempt's observed source cuts; never generate model successors.

Native assumptions are explicit in ProviderWorker.tla. Opaque SQL bytes remain
separate hashed evidence; this parser projects only facts actually present in
source events/committed records. TLC must validate every resulting adjacent edge
against the independently authored original Next, without temporal stuttering.
"""
import base64
import copy
import hashlib
import json
import math
from pathlib import Path

BOOLS=('worker','temp','admitted','slot','marker','owner','retained','hello','grant',
       'cancelled','blocked','term','killed','reaped','pumps','absent','closed',
       'cleanup','released','unknown','settled')
FIELDS=set(BOOLS)|{'start','starts','stamps'}
NATIVE_FIELDS={'attempt','start_returned','known_not_started','start_pending','hello_verified',
 'grant_published','cancel_accepted','provider_exit','helper_wait_observed',
 'group_absent_observed','pumps_joined','scoped_native_closed','stdout_eof','stderr_eof',
 'protocol_matched','protocol_success','selected_result','response_sha256','stdout_bytes',
 'stderr_bytes','failure'}
SLOTS=('prepared','helper_starting','helper_ready','dispatching','observed','unknown',
       'compensating','settled')

def strict_object(pairs):
    result={}
    for key,value in pairs:
        if key in result: raise ValueError('duplicate field: '+key)
        result[key]=value
    return result

def decode(raw):
    return json.loads(raw,object_pairs_hook=strict_object,
      parse_constant=lambda x:(_ for _ in ()).throw(ValueError('nonfinite JSON')))

def initial():
    return {**dict.fromkeys(BOOLS,False),'owner':True,'start':'none','starts':0,
            'stamps':dict.fromkeys(map(str,range(8)),False)}

def validate_states(rows):
    if type(rows) is not list or not 2<=len(rows)<=256:raise ValueError('bounded nonempty observation trace required')
    for row in rows:
        if type(row) is not dict or set(row)!=FIELDS:raise ValueError('closed worker state required')
        if any(type(row[k]) is not bool for k in BOOLS):raise ValueError('typed Boolean observations required')
        if row['start'] not in ('none','pending','returned','failed') or type(row['start']) is not str:raise ValueError('closed native start state required')
        if type(row['starts']) is not int or not 0<=row['starts']<=2:raise ValueError('bounded actual launch count required')
        if type(row['stamps']) is not dict or set(row['stamps'])!=set(map(str,range(8))) or any(type(v) is not bool for v in row['stamps'].values()):raise ValueError('exact eight typed milestone slots required')
    return rows

def extract(path):
    with Path(path).open('rb') as source:
        raw=source.read((4<<20)+1)
    if len(raw)>4<<20:raise ValueError('trace file bound')
    events=decode(raw)
    if type(events) is not list or not 1<=len(events)<=4096:raise ValueError('event count bound')
    rows=[initial()];cuts=[];facts=[];worker=temp=None;attempt=None;terminal=None
    def emit(e,s):
        rows.append(copy.deepcopy(s));cuts.append({'ticket':e['ticket'],'kind':e['kind']})
    for ticket,e in enumerate(events,1):
        if type(e) is not dict or set(e)!={'ticket','kind','attempt','word','raw'}:raise ValueError('closed source event required')
        if type(e['ticket']) is not int or e['ticket']!=ticket or type(e['word']) is not int or not 0<=e['word']<=34816:raise ValueError('contiguous bounded event scalars required')
        if type(e['attempt']) is not str or not e['attempt'] or len(e['attempt'])>128:raise ValueError('bounded attempt required')
        if attempt is None:attempt=e['attempt']
        if e['attempt']!=attempt:raise ValueError('one original attempt per ownership trace')
        s=copy.deepcopy(rows[-1]);kind=e['kind'];body=e['raw'];changed=True
        if kind in ('ordinary_worker_granted','managed_temp_granted'):
            if type(body) is not dict or body.get('state')!='active' or type(body.get('owner')) is not str:raise ValueError('actual ordinary admission required')
            resources=body.get('request',{}).get('resources',{})
            if kind=='ordinary_worker_granted':
                cpu=resources.get('cpu');memory=resources.get('memory_bytes')
                if worker is not None or type(cpu) not in (int,float) or not math.isfinite(cpu) or cpu<=0 or type(memory) is not int or memory<=0:raise ValueError('positive actual CPU/memory held admission required')
                worker=body;s.update(worker=True,admitted=True)
            else:
                artifact=body.get('artifact',{});disk=resources.get('disk_bytes')
                if temp is not None or body['request'].get('kind')!='storage' or type(disk) is not int or disk<=0 or artifact.get('phase')!='owned' or not artifact.get('identity',{}).get('birth_known'):raise ValueError('positive exact owned managed storage admission required')
                temp=body;s['temp']=True
        elif kind.startswith('durable_'):
            if type(body) is not dict or set(body)!={'initial','terminal','remaining'} or type(body['remaining']) is not int or body['remaining']!=e['word']:raise ValueError('closed exact SQL byte envelope required')
            def sql_bytes(value):
                if type(value) is not str:raise ValueError('opaque SQL bytes must use canonical base64')
                data=base64.b64decode(value,validate=True)
                if base64.b64encode(data).decode()!=value:raise ValueError('noncanonical SQL byte envelope')
                return data
            saved=sql_bytes(body['terminal']);current=decode(saved)
            if len(saved)>34816:raise ValueError('actual escaped terminal byte cap exceeded')
            if current['phase']=='settled':
                if body['remaining']!=0:raise ValueError('settled reserve mismatch')
            elif len(saved)+body['remaining']!=34816:raise ValueError('unknown/live finalization bytes were dropped')
            if kind=='durable_reserve_ack':
                immutable=sql_bytes(body['initial']);record=decode(immutable)
                if len(immutable)>65536 or worker is None or temp is None or record['attempt']!=attempt or record['worker_lease']!=worker['id'] or record['temp_reservation']!=temp['id'] or record['authority']!=worker['host'] or temp['host']!=worker['host'] or temp['owner']!=worker['owner']:raise ValueError('SQL reservation detached from actual admissions')
                s['slot']=True
                facts.append({'ticket':ticket,'initial_sha256':hashlib.sha256(immutable).hexdigest(),'initial_bytes':len(immutable)})
            elif body['initial'] is not None:raise ValueError('immutable initial bytes reissued')
            slots=current.get('transitions')
            if type(slots) is not list or len(slots)!=8:raise ValueError('exact write-once slot roster required')
            for i,v in enumerate(slots):
                if type(v) is not dict or set(v)!={'phase','at'} or type(v['phase']) is not str or type(v['at']) is not str:raise ValueError('typed milestone record required')
                if terminal and terminal['transitions'][i]['phase'] and v!=terminal['transitions'][i]:raise ValueError('acknowledged write-once milestone rewritten')
                if v['phase'] and v['phase']!=SLOTS[i]:raise ValueError('milestone retargeted')
                s['stamps'][str(i)]=bool(v['phase'])
            s['marker']=s['stamps']['1'];s['unknown']=s['stamps']['5'];s['settled']=s['stamps']['7']
            if s['settled']:s['slot']=False
            facts.append({'ticket':ticket,'terminal_sha256':hashlib.sha256(saved).hexdigest(),'terminal_bytes':len(saved),'remaining':body['remaining']})
            # A second acknowledged compensation refinement (e.g. release_pending)
            # changes fixed record details but no modeled lifecycle fact. It is
            # retained as byte evidence, not fabricated as another Next edge.
            changed=s!=rows[-1]
            if not changed and kind not in ("durable_compensating_ack","durable_unknown_ack"):raise ValueError("duplicate lifecycle SQL ACK has no observed transition")
            terminal=current
        elif kind=='handle_retained':s['retained']=True
        elif kind=='native_start_begun':s['start']='pending';s['starts']+=1
        elif kind=='native_start_returned':
            if e['word'] not in (0,1):raise ValueError('actual native Start result word required')
            if e['word']==1:s['start']='returned'
            else:changed=False
        elif kind=='known_not_started':s.update(start='failed',closed=True,pumps=True)
        elif kind=='hello_verified':s['hello']=True
        elif kind=='grant_published':s['grant']=True
        elif kind=='cancel_accepted':s.update(cancelled=True,blocked=s['blocked'] or not s['grant'])
        elif kind=='group_signal_before_reap':
            if e['word']==15:s['term']=True
            elif e['word']==9:s['killed']=True
            else:raise ValueError('unknown nonzero native signal')
        elif kind=='helper_reaped':s['reaped']=True
        elif kind=='actual_native_observation':
            if type(body) is not dict or set(body)!=NATIVE_FIELDS or body['attempt']!=attempt:raise ValueError('closed actual native observation required')
            bool_fields=('start_returned','known_not_started','start_pending','hello_verified','grant_published','cancel_accepted','helper_wait_observed','group_absent_observed','pumps_joined','scoped_native_closed','stdout_eof','stderr_eof','protocol_matched','protocol_success')
            if any(type(body[k]) is not bool for k in bool_fields):raise ValueError('native Boolean fields must not be coerced')
            if any(type(body[k]) is not int or not 0<=body[k]<=((2<<20)+65536) for k in ('stdout_bytes','stderr_bytes')):raise ValueError('bounded native output byte count required')
            if body['provider_exit'] is not None and (type(body['provider_exit']) is not dict or set(body['provider_exit'])!={'code','signal'} or any(type(v) is not int or not 0<=v<=255 for v in body['provider_exit'].values())):raise ValueError('closed bounded actual provider exit required')
            for source,field in [('helper_wait_observed','reaped'),('pumps_joined','pumps'),('group_absent_observed','absent'),('scoped_native_closed','closed')]:
                if type(body[source]) is not bool:raise ValueError('native Boolean observation required')
                s[field]=body[source]
            if body['known_not_started']:s['start']='failed'
            if body['grant_published']!=s['grant'] or body['cancel_accepted']!=s['cancelled'] or body['hello_verified']!=s['hello']:raise ValueError('native observation contradicts exact earlier source cuts')
            if body['protocol_success'] and (not body['protocol_matched'] or body['provider_exit']!={'code':0,'signal':0} or not body['stdout_eof'] or not body['scoped_native_closed']):raise ValueError('response success lacks actual exit/EOF/native closure')
            changed=s!=rows[-1]
        elif kind=='positive_temp_cleanup':
            artifact=body.get('artifact',{}) if type(body) is dict else {}
            if temp is None or body.get('id')!=temp['id'] or body.get('state')!='terminal' or body.get('outcome')!='released' or artifact.get('phase')!='removed' or not artifact.get('namespace_removed'):raise ValueError('positive exact temp cleanup missing')
            s.update(cleanup=True,temp=False)
        elif kind=='positive_worker_release':
            if worker is None or type(body) is not dict or body.get('id')!=worker['id'] or body.get('state')!='terminal' or body.get('outcome')!='released':raise ValueError('positive exact worker release missing')
            s.update(released=True,worker=False)
        elif kind=='fully_settled':s['retained']=False
        elif kind in ('provider_exit_observed','native_observation_complete','wait_pending'):
            if body is not None:raise ValueError('unexpected observation-only payload')
            changed=False
        else:raise ValueError('unsupported source cut: '+str(kind))
        if changed:emit(e,s)
    validate_states(rows)
    return {'schema':'arm.provider-worker-observed/v1','rows':rows,'cuts':cuts,'sql_bytes':facts,'source_sha256':hashlib.sha256(raw).hexdigest()}
