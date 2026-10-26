"""Project real CPU budget cuts and physical lease/event SQL into model states.

No model-generated expected raw states, native probes or fixture verdict flags.
Controlled pressure observations are a declared collector premise; production
computations and real committed outcomes are checked independently below.
"""
import copy
import json
import math
import re

SCENARIOS = ('shrink-renew', 'fifo-recovery', 'unknown-stale', 'managed-credit', 'credit-invalidation')
OPERATIONS = {
 'shrink-renew': ['initial','observe','acquire','observe','renew','observe','renew','end'],
 'fifo-recovery': ['initial','observe','acquire','acquire','tick','observe','tick','end','end'],
 'unknown-stale': ['initial','observe','acquire','observe','acquire','zero','observe','acquire','end','end'],
 'managed-credit':['initial','observe','run','observe','observe','observe','renew','acquire','renew','observe','observe','renew','observe','end','end'],
 'credit-invalidation':['initial','observe','run','observe','observe','renew','invalidate','renew','observe','observe','renew','observe','acquire','renew','end','tick','end'],
}
COMMON = set('schema scenario ordinal cut host owner budget requested credit_inputs'.split())
BUDGET = set('observation ceiling_cores managed_credit_cores capacity_cores reserved_cores available_cores deficit_cores known stale age_ns stale_after_ns accounting'.split())
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

IDENTITY = set('pid pgid uid start namespace'.split())
CREDIT = set('id sample_generation sample_identity sample_cookie cores current_generation current_identity current_cookie current_done current_exited current_lease lease_read_error'.split())

def native_identity(v,owner):
    require(type(v) is dict and set(v)==IDENTITY,'closed native CPU identity')
    require(integer(v['pid']) and 0<v['pid']<2**31 and integer(v['pgid']) and v['pgid']==v['pid'],'original native process/group')
    require(v['uid']==owner and all(type(v[k]) is str and 0<len(v[k])<=512 for k in ('uid','start','namespace')),'native original owner/start scope')
    return tuple(v[k] for k in sorted(IDENTITY))

def derive_credit(inputs,reserved,busy,host,owner):
    require(type(inputs) is list and len(inputs)<=32,'bounded actual managed candidates')
    seen=set();credit=0.;eligible={}
    for c in inputs:
        require(type(c) is dict and set(c)==CREDIT,'closed actual candidate fields')
        require(type(c['id']) is str and re.fullmatch('[a-f0-9]{32}',c['id']),'sample lease identity')
        require(type(c['sample_generation']) is str and re.fullmatch('0x[0-9a-f]+',c['sample_generation']) and c['sample_generation']!='0x0','retained original job generation')
        require(type(c['current_generation']) is str and (c['current_generation']=='' or re.fullmatch('0x[0-9a-f]+',c['current_generation'])),'current job generation')
        identity=native_identity(c['sample_identity'],owner)
        if c['current_identity'] is not None:native_identity(c['current_identity'],owner)
        require(type(c['current_done']) is type(c['current_exited']) is bool and all(type(c[k]) is str and len(c[k])<=1024 for k in ('sample_cookie','current_cookie','lease_read_error')),'current native lifetime/cookie fields')
        require(number(c['cores']) and 0<c['cores']<=8,'controlled finite positive candidate')
        item=c['current_lease']
        require(c['lease_read_error']=='' and type(item) is dict and set(item)=={'row','id','key','body'} and integer(item['row']) and item['row']>0,'actual current physical lease read')
        alias,cpu=lease(item['body'],host,owner,True)
        require(item['id']==item['body']['id']==c['id'] and item['key']==alias,'sample/current SQL ID and request key binding')
        valid=(c['sample_generation']==c['current_generation'] and c['current_identity']==c['sample_identity'] and c['sample_cookie']!='' and c['sample_cookie']==c['current_cookie'] and not c['current_done'] and not c['current_exited'] and item['body']['state'] in CONSUMING and item['body'].get('process_identity')==c['sample_identity'] and cpu>0)
        if not valid or identity in seen:continue
        seen.add(identity);amount=min(c['cores'],cpu)
        credit=math.nextafter(credit+amount,0.)
        eligible[alias]=eligible.get(alias,0)+amount
    return min(credit,reserved,busy),eligible

def budget(b,inputs,host,owner):
    require(type(b) is dict and set(b)==BUDGET, 'closed budget fields')
    o=b['observation']
    require(type(o) is dict and set(o)==OBSERVATION, 'closed observation fields')
    require(type(b['known']) is type(b['stale']) is type(o['known']) is bool, 'boolean budget fields')
    require(all(number(b[k]) and 0<=b[k]<=8 for k in ('ceiling_cores','managed_credit_cores','capacity_cores','reserved_cores','available_cores','deficit_cores')), 'bounded exact numeric budget')
    require(b['ceiling_cores'] in (2,4), 'finite configured ceiling')
    require(o['eligible_cpus']==4 and integer(o['eligible_cpus']) and o['capacity_cores']==4 and number(o['capacity_cores']), 'controlled effective scope')
    require(all(number(o[k]) and 0<=o[k]<=4 for k in ('busy_cores','available_cores')) and o['busy_cores']+o['available_cores']==4, 'bounded coherent controlled native headroom')
    require(o['scope']=='controlled-test-host' and o['method']=='controlled-counter-interval', 'explicit controlled observation premise')
    require(integer(o['interval_ns']) and o['interval_ns']==10**9 and type(o['at']) is str and o['at'].endswith('Z'), 'actual interval/timestamp fields')
    require(integer(b['age_ns']) and integer(b['stale_after_ns']) and b['stale_after_ns']==6*10**9 and b['age_ns']>=0, 'actual freshness fields')
    require(b['stale']==(b['age_ns']>b['stale_after_ns']), 'stale age correspondence')
    require(b['known']==(o['known'] and not b['stale']), 'known/fresh correspondence')
    r=b['reserved_cores']
    credit,eligible=derive_credit(inputs,r,o['busy_cores'],host,owner)
    if not b['known']:credit=0.;eligible={}
    require(b['managed_credit_cores']==credit,'actual owned credit derivation')
    cap=min(b['ceiling_cores'],o['capacity_cores'],o['available_cores']+credit) if b['known'] else 0
    require(b['capacity_cores']==cap and b['available_cores']==max(0,cap-r) and b['deficit_cores']==max(0,r-cap), 'actual computed CPU accounting')
    require(integer(o['available_cores']) or o['available_cores'].is_integer(),'integer controlled native decision domain')
    # Project only whole-core capacity effective at integer request boundaries.
    # Raw float arithmetic is verified above, and each actual permission and
    # refusal below must agree at that boundary; flooring cannot invent grants.
    units=max(0,math.floor(cap)-min(b['ceiling_cores'],int(o['available_cores'])))
    ca=min(units,2) if 'A' in eligible else 0
    cb=min(units-ca,1) if 'B' in eligible else 0
    require(ca+cb==units,'credit abstraction requires real eligible owned promises')
    return int(o['available_cores']),b['ceiling_cores'],o['known'],not b['stale'],ca,cb,1 if ca else 0,2 if cb else 0

def initial():
    return dict(native=0,ceiling=4,known=False,fresh=False,a=0,b=0,queue=[],last='init',admitted=False,
        decisionKnown=False,decisionFresh=False,decisionCapacity=0,decisionReserved=0,ca=0,cb=0,oa=0,ob=0)

def lease(body, host, owner, managed=False):
    require(type(body) is dict and type(body.get('request')) is dict, 'typed SQL lease body')
    q=body['request'];key=q.get('idempotency_key')
    require(key in ('A','B','Z') and type(body.get('id')) is str and re.fullmatch('[a-f0-9]{32}',body['id']), 'exact lease key/ID')
    require(body.get('host')==host and body.get('owner')==owner and type(owner) is str and owner.isdecimal(), 'same authority/owner')
    require(integer(body.get('revision')) and body['revision']>0 and body.get('state') in CONSUMING|{'queued','terminal'}, 'typed lease state/revision')
    cpu=q.get('resources',{}).get('cpu')
    require(number(cpu) and cpu=={'A':2,'B':1,'Z':0}[key], 'exact original requested CPU')
    require(q.get('ttl_ns')==60*10**9 and q.get('kind','')=='' and not body.get('artifact'), 'CPU-only fixture request scope')
    if body.get('process_identity') is not None or body.get('pid'):
        require(managed and key=='A' and body.get('pid')==body.get('process_identity',{}).get('pid'),'actual supervised A process only')
        native_identity(body['process_identity'],owner)
    else:require(not body.get('pid'),'cooperative or not-yet-launched request')
    if key!='Z': require(q['resources'].get('memory_bytes')==q['resources'].get('disk_bytes')==0,'no hidden resource constraint')
    else: require(q['resources'].get('memory_bytes')==1 and q['resources'].get('disk_bytes')==0,'unrelated zero-CPU resource')
    return key,cpu

def project(rows, scenario):
    require(scenario in SCENARIOS and type(rows) is list and 1<len(rows)<=256,'complete actual scenario')
    host=rows[0]['host'];owner=rows[0]['owner']
    require(type(host) is str and re.fullmatch('[a-f0-9]{32}',host),'actual host identity')
    state=initial();states=[];physical={};bindings={};events_seen=0;permissions=[];actions=[];grants=[];renewals=0
    previous_row=None
    managed=scenario in ('managed-credit','credit-invalidation')
    positive_credits=0;invalidations=0
    def environment(row):return budget(row['budget'],row['credit_inputs'],host,owner)
    for ordinal,row in enumerate(rows,1):
        require(type(row) is dict and set(row)>=COMMON and set(row)<=COMMON|{'operation','leases','events'},'closed actual envelope')
        require(row['schema']=='arm.cpu-admission-actual/v1' and row['scenario']==scenario and integer(row['ordinal']) and row['ordinal']==ordinal,'actual case/ordinal')
        require(row['host']==host and row['owner']==owner and row['cut'] in ('budget','admission','renewal','sql'),'original owner/cut')
        env=environment(row)
        positive_credits+=row['budget']['managed_credit_cores']>0
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
        require(set(row)>=COMMON|{'operation'} and type(row['operation']) is dict and set(row['operation'])==set('action key id cpu error job_generation joined'.split()),'actual completion operation')
        op=row['operation'];actions.append(op['action'])
        require(type(op['joined']) is bool and type(op['job_generation']) is str,'actual original worker join fields')
        if op['action']=='end' and managed and op['key']=='A':
            require(op['joined'] and re.fullmatch('0x[0-9a-f]+',op['job_generation']) and any(c['id']==op['id'] and c['sample_generation']==op['job_generation'] for c in row['credit_inputs']),'original admitted native worker must actually join before final End snapshot')
        else:require(not op['joined'] and op['job_generation']=='','no invented native owner join on cooperative/read cuts')
        require(type(op['error']) is str and type(op['id']) is str and type(op['key']) is str and number(op['cpu']),'actual API outcome fields')
        current_env=tuple(state[k] for k in ('native','ceiling','known','fresh','ca','cb','oa','ob'))
        if op['action']=='initial':
            require(not states and env==current_env and not row.get('leases') and not row.get('events'),'actual empty unknown initial state')
            states.append(copy.deepcopy(state));permissions=[];continue
        if op['action']=='observe':
            state.update(native=env[0],ceiling=env[1],known=env[2],fresh=env[3],ca=env[4],cb=env[5],oa=env[6],ob=env[7],last='observe',admitted=False)
            states.append(copy.deepcopy(state))
        elif op['action']=='invalidate':
            require(op['key']=='A' and op['id']==bindings.get('A') and env[:4]==current_env[:4] and env[4:]==(0,0,0,0) and any(c['sample_generation']!=c['current_generation'] for c in row['credit_inputs']),'actual replacement invalidation')
            state.update(ca=0,cb=0,oa=0,ob=0,last='invalidate',admitted=False);states.append(copy.deepcopy(state));invalidations+=1
        else:require(env[:4]==current_env[:4],'unannounced collector change during actual operation')
        changed=False
        for event in row.get('events',[]):
            require(type(event) is dict and set(event)=={'sequence','type','id','body'} and integer(event['sequence']) and event['sequence']>events_seen,'durable event order/schema')
            events_seen=event['sequence'];body=event['body'];alias,cpu=lease(body,host,owner,managed)
            require(event['id']==body['id'] and event['type'] in ('lease.created','lease.granted','lease.renewed','lease.ending','lease.started','lease.released','queue.changed'),'event SQL/body identity')
            previous=physical.get(alias)
            require(alias not in bindings or bindings[alias]==body['id'],'immutable alias/native row binding')
            bindings[alias]=body['id']
            if previous is None: require(event['type']=='lease.created' and body['revision']==1,'first actual creation record')
            else: require(previous['id']==body['id'] and body['revision']>=previous['revision'],'monotone durable row')
            old_claim=state['a'] if alias=='A' else state['b'] if alias=='B' else 0
            new_claim=cpu if body['state'] in CONSUMING else 0
            if alias in ('A','B') and new_claim>old_claim:
                require(body['state'] in ('active','provisioning') and (body['state']!='provisioning' or managed and op['action']=='run') and old_claim==0,'new actual positive CPU grant')
                reserved=state['a']+state['b']
                matches=[p for p in permissions if p['cut']=='admission' and p['requested']==cpu and p['budget']['reserved_cores']==reserved and p['budget']['known'] and p['budget']['available_cores']>=cpu]
                require(matches,'actual grant lacks exact permission budget')
                permission=matches[0];permissions.remove(permission)
                require(environment(permission)==current_env,'grant/sample correspondence')
                require(not state['queue'] or state['queue'][0]==alias,'FIFO head grant')
                if state['queue']: state['queue'].pop(0)
                state[alias.lower()]=new_claim
                state.update(last='grant',admitted=True,decisionKnown=permission['budget']['known'],decisionFresh=not permission['budget']['stale'],decisionCapacity=math.floor(permission['budget']['capacity_cores']),decisionReserved=reserved+cpu)
                grants.append(alias)
            elif alias in ('A','B') and old_claim>new_claim:
                require(body['state']=='terminal' and body.get('outcome')=='released','only settled ordinary release returns CPU')
                state[alias.lower()]=new_claim;state['c'+alias.lower()]=0;state['o'+alias.lower()]=0;state.update(last='release',admitted=False)
            elif alias in ('A','B') and body['state']=='queued' and alias not in state['queue']:
                state['queue'].append(alias);state.update(last='queue',admitted=False)
            elif event['type']=='lease.renewed' and alias in ('A','B'):
                reserved=state['a']+state['b']
                matches=[p for p in permissions if p['cut']=='renewal' and p['budget']['reserved_cores']==reserved]
                require(matches and not state['queue'],'renewal uses full promises/self once and respects queue')
                permission=matches[0];permissions.remove(permission);b=permission['budget']
                require(environment(permission)==current_env and b['known'] and b['deficit_cores']==0,'actual renewal fence')
                require(previous is not None and body.get('expires_at')>previous.get('expires_at'),'real deadline extension')
                state.update(last='renew',admitted=True,decisionKnown=b['known'],decisionFresh=not b['stale'],decisionCapacity=math.floor(b['capacity_cores']),decisionReserved=reserved);renewals+=1
            else: state.update(last='refuse',admitted=False)
            physical[alias]=copy.deepcopy(body);states.append(copy.deepcopy(state));changed=True
        if not changed and op['action'] not in ('observe','invalidate'):
            state.update(last='refuse',admitted=False);states.append(copy.deepcopy(state))
        actual={};queue=[];last_row=0
        for item in row.get('leases',[]):
            require(type(item) is dict and set(item)=={'row','id','key','body'} and integer(item['row']) and item['row']>last_row,'physical lease roster/order')
            last_row=item['row'];body=item['body'];alias,cpu=lease(body,host,owner,managed)
            require(item['id']==body['id'] and item['key']==alias and alias not in actual,'physical SQL/body/key identity')
            require(alias in physical and physical[alias]==body,'all durable state matches complete event suffix')
            actual[alias]=cpu if body['state'] in CONSUMING else 0
            if body['state']=='queued': queue.append(alias)
        require(set(actual)==set(physical) and actual.get('A',0)==state['a'] and actual.get('B',0)==state['b'] and queue==state['queue'],'complete SQL final state/queue correspondence')
        require(row['budget']['reserved_cores']==sum(actual.values()),'real consuming SQL total counted once')
        require(tuple(state[k] for k in ('ca','cb','oa','ob'))==env[4:],'current ownership/release credit transition')
        for c in row['credit_inputs']:
            require(c['current_lease']['body']==physical.get(c['current_lease']['key']),'credit current physical row must equal complete SQL/event state')
        if op['action']=='renew':
            require(any(p['cut']=='renewal' for p in permissions) or (changed and not op['error']),'real positive-CPU renewal budget cut')
            renewed=[e for e in row.get('events',[]) if e['type']=='lease.renewed' and e['id']==op['id']]
            require(len(renewed)==(0 if op['error'] else 1),'actual renewal API/committed outcome correspondence')
            if op['error']:
                require(op['error']=='renewal_denied' and (not row['budget']['known'] or row['budget']['reserved_cores']>row['budget']['capacity_cores'] or bool(state['queue'])),'actual refusal must agree with CPU/FIFO decision boundary')
        if op['action'] in ('acquire','zero','run'):
            if op['error']:
                require(not op['id'] and not any(e['type']=='lease.created' and e['body']['request']['idempotency_key']==op['key'] for e in row.get('events',[])),'failed acquisition cannot hide durable grant')
            else:
                require(op['key'] in physical and physical[op['key']]['id']==op['id'] and physical[op['key']]['state'] in ('active','queued'),'successful acquisition exact durable receipt')
        if op['action']=='end':
            require(not op['error'] and op['key'] in physical and physical[op['key']]['id']==op['id'] and physical[op['key']]['state']=='terminal' and physical[op['key']].get('outcome')=='released' and physical[op['key']].get('ended_at'),'ordinary settled original release')
        permissions=[]
    require(actions==OPERATIONS[scenario] and rows[-1]['cut']=='sql','complete scenario and final SQL witness')
    require(grants==({'fifo-recovery':['A','B'],'managed-credit':['A','B'],'credit-invalidation':['A','B']}.get(scenario,['A'])),'nonvacuous real grants/FIFO coverage')
    require(renewals=={'shrink-renew':1,'managed-credit':2,'credit-invalidation':1}.get(scenario,0),'nonvacuous renewal coverage')
    require(positive_credits>0 if managed else positive_credits==0,'nonvacuous controlled managed credit scope')
    require(invalidations==(1 if scenario=='credit-invalidation' else 0),'original generation invalidation coverage')
    require(state['a']==state['b']==0 and not state['queue'],'all original cooperative promises ordinarily released')
    return {'states':states,'raw_rows':len(rows),'operations':actions,'grants':grants,'renewals':renewals}
