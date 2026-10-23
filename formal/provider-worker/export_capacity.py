#!/usr/bin/env python3
"""Validate direct SQL/receipt snapshots; never apply a model action/reducer."""
import base64,hashlib,json
from pathlib import Path

PHASES={'none','live','unknown','eligible','settled'}
SQL_PHASES={'prepared','helper_starting','helper_ready','dispatching','observed','unknown','compensating','settled'}
ROW_FIELDS={'id','registration','phase','initial','terminal','remaining','native_closed','temp_removed','worker_released'}

def pairs(items):
    result={}
    for k,v in items:
        if k in result:raise ValueError('duplicate capacity evidence field')
        result[k]=v
    return result

def decode(raw):
    return json.loads(raw,object_pairs_hook=pairs,parse_constant=lambda x:(_ for _ in ()).throw(ValueError('nonfinite evidence')))

def raw_bytes(encoded,cap):
    if type(encoded) is not str or len(encoded)>((cap+2)//3)*4:raise ValueError('bounded opaque SQL byte string required')
    result=base64.b64decode(encoded,validate=True)
    if len(result)>cap or base64.b64encode(result).decode()!=encoded:raise ValueError('bounded canonical SQL bytes required')
    return result

def validate_states(rows):
    if type(rows) is not list or not 2<=len(rows)<=64:raise ValueError('bounded actual capacity observations required')
    for s in rows:
        if type(s) is not dict or set(s)!={'phase','terminal'}:raise ValueError('closed capacity state required')
        if any(type(s[k]) is not list or len(s[k])!=6 for k in s):raise ValueError('exact six original keys required')
        if any(type(p) is not str or p not in PHASES for p in s['phase']):raise ValueError('closed capacity phase required')
        if any(type(n) is not int or not 0<=n<=34816 for n in s['terminal']):raise ValueError('bounded actual terminal byte counts required')
    return rows

def extract(path):
    with Path(path).open('rb') as stream:raw=stream.read((4<<20)+1)
    if len(raw)>4<<20:raise ValueError('capacity evidence exceeds4MiB')
    source=decode(raw)
    if type(source) is not dict or set(source)!={'schema','ids','initial_bytes','observations','refused_unchanged'} or source['schema']!='arm.provider-worker-capacity/v1':raise ValueError('closed capacity evidence envelope required')
    ids=source['ids'];sizes=source['initial_bytes'];observed=source['observations']
    if type(ids) is not list or len(ids)!=6 or any(type(k) is not str or not 1<=len(k)<=128 for k in ids) or len(set(ids))!=6:raise ValueError('six bounded unique original keys required')
    if type(sizes) is not list or len(sizes)!=6 or any(type(n) is not int or not 1<=n<=65536 for n in sizes):raise ValueError('actual escaped initial byte counts required')
    if type(observed) is not list or not 2<=len(observed)<=64 or type(source['refused_unchanged']) is not int or source['refused_unchanged']<4:raise ValueError('actual bounded snapshots and unchanged denial checks required')
    states=[];evidence=[];immutable={};registrations={};authority=None;oldslots={}
    for observation in observed:
        if type(observation) is not dict or set(observation)!={'ledger','rows'}:raise ValueError('closed SQL snapshot required')
        entries=observation['rows'];projected={'phase':['none']*6,'terminal':[0]*6};seen=set();facts=[]
        if type(entries) is not list or len(entries)>6:raise ValueError('bounded complete original-key rows required')
        for r in entries:
            if type(r) is not dict or set(r)!=ROW_FIELDS or type(r['id']) is not str or r['id'] not in ids or r['id'] in seen:raise ValueError('closed exact unique source row required')
            key=ids.index(r['id']);seen.add(r['id'])
            if type(r['phase']) is not str or r['phase'] not in SQL_PHASES or type(r['registration']) is not str or not r['registration']:raise ValueError('typed SQL phase/registration required')
            if any(type(r[k]) is not bool for k in ('native_closed','temp_removed','worker_released')):raise ValueError('typed actual positive receipt facts required')
            initial=raw_bytes(r['initial'],65536);terminal=raw_bytes(r['terminal'],34816)
            i=decode(initial);t=decode(terminal)
            if i['attempt']!=r['id'] or i['generation']!=r['registration'] or len(initial)!=sizes[key] or t['phase']!=r['phase']:raise ValueError('SQL bytes do not bind exact row identities/lengths')
            if authority is None:authority=i['authority']
            if i['authority']!=authority:raise ValueError('mixed selected ARM authorities')
            if r['id'] in immutable and immutable[r['id']]!=initial:raise ValueError('immutable original request changed')
            immutable[r['id']]=initial
            if key in registrations and registrations[key]!=r['registration']:raise ValueError('original key retargeted')
            registrations[key]=r['registration']
            if type(r['remaining']) is not int or (r['remaining']!=0 if r['phase']=='settled' else len(terminal)+r['remaining']!=34816):raise ValueError('remaining finalization reserve changed')
            if r['remaining']<0:raise ValueError('negative finalization reserve')
            slots=t['transitions']
            if type(slots) is not list or len(slots)!=8:raise ValueError('exact eight immutable transition slots required')
            for index,slot in enumerate(slots):
                if type(slot) is not dict or set(slot)!={'phase','at'} or type(slot['phase']) is not str or type(slot['at']) is not str:raise ValueError('closed typed transition slot required')
                prior=oldslots.get((r['id'],index))
                if prior and prior['phase'] and prior!=slot:raise ValueError('write-once transition changed')
                oldslots[r['id'],index]=slot
            phase='unknown' if r['phase']=='unknown' else 'live'
            eligible=r['native_closed'] and r['temp_removed'] and r['worker_released']
            if eligible:phase='eligible'
            if r['phase']=='settled':
                if not eligible:raise ValueError('settlement lacks actual native/temp/worker positives')
                phase='settled';projected['terminal'][key]=len(terminal)
            projected['phase'][key]=phase
            facts.append({'id':r['id'],'initial_sha256':hashlib.sha256(initial).hexdigest(),'initial_bytes':len(initial),'terminal_sha256':hashlib.sha256(terminal).hexdigest(),'terminal_bytes':len(terminal),'remaining':r['remaining']})
        if projected!=observation['ledger']:raise ValueError('claimed ledger projection disagrees with raw SQL/receipt observations')
        states.append(projected);evidence.append(facts)
    if set(registrations)>=set((0,1,2,3,5)):
        if registrations[0]!=registrations[5] or len({registrations[k] for k in (0,1,2,3)})!=4:raise ValueError('actual logical registration mapping differs from finite model')
    else:raise ValueError('missing required source registrations')
    validate_states(states)
    if states[0]!={'phase':['none']*6,'terminal':[0]*6}:raise ValueError('actual empty authority snapshot required')
    return {'schema':'arm.provider-worker-capacity-observed/v1','states':states,'initial_bytes':sizes,'source_sha256':hashlib.sha256(raw).hexdigest(),'sql_bytes':evidence}
