"""Project real CPU budget cuts and physical lease/event SQL into model states.

No model-generated expected raw states, native probes or fixture verdict flags.
Controlled pressure observations are a declared collector premise; production
computations and real committed outcomes are checked independently below.
"""
import copy
import json
import math
import re

SCENARIOS = ('shrink-renew', 'fifo-recovery', 'unknown-stale')
OPERATIONS = {
 'shrink-renew': ['initial','observe','acquire','observe','renew','observe','renew','end'],
 'fifo-recovery': ['initial','observe','acquire','acquire','tick','observe','tick','end','end'],
 'unknown-stale': ['initial','observe','acquire','observe','acquire','zero','observe','acquire','end','end'],
}
COMMON = set('schema scenario ordinal cut host owner budget requested'.split())
BUDGET = set('observation ceiling_cores capacity_cores reserved_cores available_cores deficit_cores known stale age_ns stale_after_ns accounting'.split())
OBSERVATION = set('known at interval_ns eligible_cpus capacity_cores busy_cores available_cores scope method quality detail'.split())
CONSUMING = {'active','provisioning','ending','needs_attention'}

def require(ok, message):
    if not ok: raise ValueError(message)

def unique(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, 'duplicate JSON field')
        result[key] = value
    return result

def parse(raw):
    return json.loads(raw, object_pairs_hook=unique,
        parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite JSON')))

def integer(v): return type(v) is int
def number(v): return type(v) in (int,float) and math.isfinite(v)

def budget(b):
    require(type(b) is dict and set(b)==BUDGET, 'closed budget fields')
    o=b['observation']
    require(type(o) is dict and set(o)==OBSERVATION, 'closed observation fields')
    require(type(b['known']) is type(b['stale']) is type(o['known']) is bool, 'boolean budget fields')
    require(all(number(b[k]) and 0<=b[k]<=8 for k in ('ceiling_cores','capacity_cores','reserved_cores','available_cores','deficit_cores')), 'bounded exact numeric budget')
    require(b['ceiling_cores'] in (2,4), 'finite configured ceiling')
    require(o['eligible_cpus']==4 and integer(o['eligible_cpus']) and o['capacity_cores']==4 and number(o['capacity_cores']), 'controlled effective scope')
    require(all(number(o[k]) and 0<=o[k]<=4 for k in ('busy_cores','available_cores')), 'bounded native headroom')
    require(o['scope']=='controlled-test-host' and o['method']=='controlled-counter-interval', 'explicit controlled observation premise')
    require(integer(o['interval_ns']) and o['interval_ns']==10**9 and type(o['at']) is str and o['at'].endswith('Z'), 'actual interval/timestamp fields')
    require(integer(b['age_ns']) and integer(b['stale_after_ns']) and b['stale_after_ns']==6*10**9 and b['age_ns']>=0, 'actual freshness fields')
    require(b['stale']==(b['age_ns']>b['stale_after_ns']), 'stale age correspondence')
    require(b['known']==(o['known'] and not b['stale']), 'known/fresh correspondence')
    cap=min(b['ceiling_cores'],o['available_cores']) if b['known'] else 0
    r=b['reserved_cores']
    require(b['capacity_cores']==cap and b['available_cores']==max(0,cap-r) and b['deficit_cores']==max(0,r-cap), 'actual computed CPU accounting')
    return o['available_cores'],b['ceiling_cores'],o['known'],not b['stale']

def initial():
    return dict(native=0,ceiling=4,known=False,fresh=False,a=0,b=0,queue=[],last='init',admitted=False,
        decisionKnown=False,decisionFresh=False,decisionCapacity=0,decisionReserved=0)

def lease(body, host, owner):
    require(type(body) is dict and type(body.get('request')) is dict, 'typed SQL lease body')
    q=body['request'];key=q.get('idempotency_key')
    require(key in ('A','B','Z') and type(body.get('id')) is str and re.fullmatch('[a-f0-9]{32}',body['id']), 'exact lease key/ID')
    require(body.get('host')==host and body.get('owner')==owner and type(owner) is str and owner.isdecimal(), 'same authority/owner')
    require(integer(body.get('revision')) and body['revision']>0 and body.get('state') in CONSUMING|{'queued','terminal'}, 'typed lease state/revision')
    cpu=q.get('resources',{}).get('cpu')
    require(number(cpu) and cpu=={'A':2,'B':1,'Z':0}[key], 'exact original requested CPU')
    require(q.get('ttl_ns')==60*10**9 and q.get('kind','')=='' and body.get('process_identity') is None and not body.get('pid') and not body.get('artifact'), 'cooperative CPU-only fixture scope')
    if key!='Z': require(q['resources'].get('memory_bytes')==q['resources'].get('disk_bytes')==0,'no hidden resource constraint')
    else: require(q['resources'].get('memory_bytes')==1 and q['resources'].get('disk_bytes')==0,'unrelated zero-CPU resource')
    return key,cpu

def project(rows, scenario):
    require(scenario in SCENARIOS and type(rows) is list and 1<len(rows)<=256,'complete actual scenario')
    host=rows[0]['host'];owner=rows[0]['owner']
    require(type(host) is str and re.fullmatch('[a-f0-9]{32}',host),'actual host identity')
    state=initial();states=[];physical={};bindings={};events_seen=0;permissions=[];actions=[];grants=[];renewals=0
    previous_row=None
    for ordinal,row in enumerate(rows,1):
        require(type(row) is dict and set(row)>=COMMON and set(row)<=COMMON|{'operation','leases','events'},'closed actual envelope')
        require(row['schema']=='arm.cpu-admission-actual/v1' and row['scenario']==scenario and integer(row['ordinal']) and row['ordinal']==ordinal,'actual case/ordinal')
        require(row['host']==host and row['owner']==owner and row['cut'] in ('budget','admission','renewal','sql'),'original owner/cut')
        env=budget(row['budget'])
        require(number(row['requested']) and 0<=row['requested']<=2,'request scalar')
        if row['cut']!='sql':
            require(set(row)==COMMON,'budget hook cannot carry synthetic SQL')
            if row['cut'] in ('admission','renewal'):
                require(previous_row is not None and previous_row['cut']=='budget' and previous_row['budget']==row['budget'],'permission cut must follow exact computed budget')
                require((row['cut']=='admission' and row['requested']>0) or (row['cut']=='renewal' and row['requested']==0),'positive CPU permission cut scope')
                permissions.append(row)
            previous_row=row
            continue
        require(previous_row is not None and previous_row['cut']=='budget' and previous_row['budget']==row['budget'],'SQL observation must retain exact computed budget')
        previous_row=row
        require(set(row)>=COMMON|{'operation'} and type(row['operation']) is dict and set(row['operation'])==set('action key id cpu error'.split()),'actual completion operation')
        op=row['operation'];actions.append(op['action'])
        require(type(op['error']) is str and type(op['id']) is str and type(op['key']) is str and number(op['cpu']),'actual API outcome fields')
        current_env=(state['native'],state['ceiling'],state['known'],state['fresh'])
        if op['action']=='initial':
            require(not states and env==current_env and not row.get('leases') and not row.get('events'),'actual empty unknown initial state')
            states.append(copy.deepcopy(state));permissions=[];continue
        if op['action']=='observe':
            state.update(native=env[0],ceiling=env[1],known=env[2],fresh=env[3],last='observe',admitted=False)
            states.append(copy.deepcopy(state))
        else: require(env==current_env,'unannounced collector change during actual operation')
        changed=False
        for event in row.get('events',[]):
            require(type(event) is dict and set(event)=={'sequence','type','id','body'} and integer(event['sequence']) and event['sequence']>events_seen,'durable event order/schema')
            events_seen=event['sequence'];body=event['body'];alias,cpu=lease(body,host,owner)
            require(event['id']==body['id'] and event['type'] in ('lease.created','lease.granted','lease.renewed','lease.released','queue.changed'),'event SQL/body identity')
            previous=physical.get(alias)
            require(alias not in bindings or bindings[alias]==body['id'],'immutable alias/native row binding')
            bindings[alias]=body['id']
            if previous is None: require(event['type']=='lease.created' and body['revision']==1,'first actual creation record')
            else: require(previous['id']==body['id'] and body['revision']>=previous['revision'],'monotone durable row')
            old_claim=state['a'] if alias=='A' else state['b'] if alias=='B' else 0
            new_claim=cpu if body['state'] in CONSUMING else 0
            if alias in ('A','B') and new_claim>old_claim:
                require(body['state']=='active' and old_claim==0,'new actual positive CPU grant')
                reserved=state['a']+state['b']
                matches=[p for p in permissions if p['cut']=='admission' and p['requested']==cpu and p['budget']['reserved_cores']==reserved and p['budget']['known'] and p['budget']['available_cores']>=cpu]
                require(matches,'actual grant lacks exact permission budget')
                permission=matches[0];permissions.remove(permission)
                require(budget(permission['budget'])==env,'grant/sample correspondence')
                require(not state['queue'] or state['queue'][0]==alias,'FIFO head grant')
                if state['queue']: state['queue'].pop(0)
                state[alias.lower()]=new_claim
                state.update(last='grant',admitted=True,decisionKnown=permission['budget']['known'],decisionFresh=not permission['budget']['stale'],decisionCapacity=permission['budget']['capacity_cores'],decisionReserved=reserved+cpu)
                grants.append(alias)
            elif alias in ('A','B') and old_claim>new_claim:
                require(body['state']=='terminal' and body.get('outcome')=='released','only settled ordinary release returns CPU')
                state[alias.lower()]=new_claim;state.update(last='release',admitted=False)
            elif alias in ('A','B') and body['state']=='queued' and alias not in state['queue']:
                state['queue'].append(alias);state.update(last='queue',admitted=False)
            elif event['type']=='lease.renewed' and alias in ('A','B'):
                reserved=state['a']+state['b']
                matches=[p for p in permissions if p['cut']=='renewal' and p['budget']['reserved_cores']==reserved]
                require(matches and not state['queue'],'renewal uses full promises/self once and respects queue')
                permission=matches[0];permissions.remove(permission);b=permission['budget']
                require(budget(b)==env and b['known'] and b['deficit_cores']==0,'actual renewal fence')
                require(previous is not None and body.get('expires_at')>previous.get('expires_at'),'real deadline extension')
                state.update(last='renew',admitted=True,decisionKnown=b['known'],decisionFresh=not b['stale'],decisionCapacity=b['capacity_cores'],decisionReserved=reserved);renewals+=1
            else: state.update(last='refuse',admitted=False)
            physical[alias]=copy.deepcopy(body);states.append(copy.deepcopy(state));changed=True
        if not changed and op['action']!='observe':
            state.update(last='refuse',admitted=False);states.append(copy.deepcopy(state))
        actual={};queue=[];last_row=0
        for item in row.get('leases',[]):
            require(type(item) is dict and set(item)=={'row','id','key','body'} and integer(item['row']) and item['row']>last_row,'physical lease roster/order')
            last_row=item['row'];body=item['body'];alias,cpu=lease(body,host,owner)
            require(item['id']==body['id'] and item['key']==alias and alias not in actual,'physical SQL/body/key identity')
            require(alias in physical and physical[alias]==body,'all durable state matches complete event suffix')
            actual[alias]=cpu if body['state'] in CONSUMING else 0
            if body['state']=='queued': queue.append(alias)
        require(set(actual)==set(physical) and actual.get('A',0)==state['a'] and actual.get('B',0)==state['b'] and queue==state['queue'],'complete SQL final state/queue correspondence')
        require(row['budget']['reserved_cores']==sum(actual.values()),'real consuming SQL total counted once')
        if op['action']=='renew':
            require(any(p['cut']=='renewal' for p in permissions) or (changed and not op['error']),'real positive-CPU renewal budget cut')
            renewed=[e for e in row.get('events',[]) if e['type']=='lease.renewed' and e['id']==op['id']]
            require(len(renewed)==(0 if op['error'] else 1),'actual renewal API/committed outcome correspondence')
        if op['action'] in ('acquire','zero'):
            if op['error']:
                require(not op['id'] and not any(e['type']=='lease.created' and e['body']['request']['idempotency_key']==op['key'] for e in row.get('events',[])),'failed acquisition cannot hide durable grant')
            else:
                require(op['key'] in physical and physical[op['key']]['id']==op['id'] and physical[op['key']]['state'] in ('active','queued'),'successful acquisition exact durable receipt')
        if op['action']=='end':
            require(not op['error'] and op['key'] in physical and physical[op['key']]['id']==op['id'] and physical[op['key']]['state']=='terminal' and physical[op['key']].get('outcome')=='released' and physical[op['key']].get('ended_at'),'ordinary settled original release')
        permissions=[]
    require(actions==OPERATIONS[scenario] and rows[-1]['cut']=='sql','complete scenario and final SQL witness')
    require(grants==({'fifo-recovery':['A','B']}.get(scenario,['A'])),'nonvacuous real grants/FIFO coverage')
    require(renewals==(1 if scenario=='shrink-renew' else 0),'nonvacuous renewal coverage')
    require(state['a']==state['b']==0 and not state['queue'],'all original cooperative promises ordinarily released')
    return {'states':states,'raw_rows':len(rows),'operations':actions,'grants':grants,'renewals':renewals}
