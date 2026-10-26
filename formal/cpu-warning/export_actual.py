"""Closed reader of actual CPU-warning method boundaries and private facts.

No Go results or owner verdicts are synthesized. The reader independently binds
raw current/saved handles, SQL and frame facts and checks returned arithmetic
against exact representable-input bounds. The finite model normalizes only the
explicit controlled trace partition; this is not native/IEEE completeness.
"""
import calendar
import copy
import hashlib
import json
import math
import re
from fractions import Fraction

SCENARIOS=('bare','unknown','process-credit','process-cap-bare','process-fraction','process-frame','process-cookie','process-pointer','process-joined','process-expired','process-retained','transit-live','transit-context','transit-join','transit-row','inline-live','inline-full','inline-context','inline-volume','inline-cancel','inline-joined','inline-pointer','inline-unstarted','inline-after-snapshot','lost-inline-restart','physical-idle','stale','snapshot','fractional','sum17','unsupported-native')
FIELDS={'epoch','schema','scenario','ordinal','call','cut','manager','host','owner','now','tick','cpu','current_cpu','observed','observed_age','native_age','sample_tick','frame','saved','current','credits','listed','leases','bindings','events','failure','sample','evidence'}
OWNER_FIELDS={'kind','pointer','manager','identity','cookie','exited','context','cancelled','runtime','done','joined','started','settled','id','key','record','volume','binding_key','binding_side','disk'}
SAVED_FIELDS={'source_done','source_joined','source_exited','kind','pointer','identity','cookie','context','runtime','done','record','digest'}
CPU_FIELDS={'known','at','interval_ns','eligible_cpus','capacity_cores','busy_cores','available_cores','scope','method','quality','detail'}
IDENTITY={'pid','pgid','uid','start','namespace'}
EVIDENCE={'state','native_known','at','interval_ns','idle_cores','verified_managed_cores','possible_compliant_supervised_cores','minimum_coverage_cores','possible_compliant_coverage_cores','detail'}
SAMPLE={'Known','ObservedAt','ObservedTick','Epoch','Available'}
FRAME={'Credits','Eligible','Reason','Evidence'}
POINTS=('PreviousHostStarted','PreviousHostFinished','PreviousPostStarted','PreviousPostFinished','CurrentPreStarted','CurrentPreFinished','HostStarted','HostFinished')
def require(ok,message):
    if not ok:raise ValueError(message)
def pairs(values):
    out={}
    for k,v in values:
        require(k not in out,'duplicate JSON field');out[k]=v
    return out
def parse(raw):return json.loads(raw,object_pairs_hook=pairs,parse_constant=lambda x:(_ for _ in ()).throw(ValueError('nonfinite JSON '+x)))
def integer(x):return type(x) is int
def real(x):return type(x) in (int,float) and math.isfinite(x)
def scalar(x):require(real(x),'finite nonboolean real');return float(x)
def typed(x):return json.dumps(x,sort_keys=True,allow_nan=False,separators=(',',':'))
def stamp(x):
    require(type(x) is str,'timestamp string')
    m=re.fullmatch(r'(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)',x)
    require(m is not None,'closed nanosecond timestamp');y,mo,d,h,mi,s=map(int,m.groups()[:6]);zone=m[8]
    require(1<=mo<=12 and 1<=d<=calendar.monthrange(y,mo)[1] and h<24 and mi<60 and s<60,'valid original calendar/time fields')
    require(zone=='Z' or int(zone[1:3])<24 and int(zone[4:])<60,'valid original timezone fields')
    offset=0 if zone=='Z' else (1 if zone[0]=='+' else -1)*(int(zone[1:3])*3600+int(zone[4:])*60)
    return (calendar.timegm((y,mo,d,h,mi,s))-offset)*10**9+int((m[7] or '').ljust(9,'0') or '0')
def consuming(l):return l['state'] in ('active','provisioning','ending','needs_attention')
def identity(v):require(type(v) is dict and set(v)==IDENTITY and integer(v['pid']) and integer(v['pgid']) and all(type(v[k]) is str for k in ('uid','start','namespace')),'native identity shape')
LEASE_FIELDS={'cargo','pool_binding','pool_error','budget_binding','budget_policy','budget_error','queue_info','id','host','owner','owner_username','request','state','outcome','failure','created_at','started_at','expires_at','ended_at','revision','pid','managed_dir','exit_code','capabilities','artifact','ports','process_identity','usage','workload'}
REQUEST_FIELDS={'budget','start_deadline_at','origin_certificate','origin_grant_revision','origin_key','origin_peer','host','reason','project','agent','resources','ttl_ns','idempotency_key','queue','profile','kind','storage_lease','service_lease','ports','port_lease','pools','usage_policy'}
def raw_record(raw):require(type(raw) is str and len(raw)<=1<<20,'bounded exact row bytes');v=parse(raw);require(type(v) is dict,'row object');return v

def native_terms(value):
    # Typed captured metadata only: this does not prove native counter
    # compatibility or derive credit from the terms in this scoped model.
    require(value is None or type(value) is list,'native term collection type')
    for term in value or []:
        require(type(term) is dict and set(term)=={'Owner','RawDelta','Ticks','MatchedProcesses','Members'},'native term fields')
        require(all(integer(term[k]) and term[k]>=0 for k in ('RawDelta','Ticks','MatchedProcesses')),'native term integer fields')
        owner=term['Owner'];require(type(owner) is dict and set(owner)=={'Token','Root','Cookie'} and type(owner['Token']) is str and type(owner['Cookie']) is str,'native term owner fields');identity(owner['Root'])
        members=term['Members'];require(members is None or type(members) is list,'native term member collection')
        for member in members or []:
            require(type(member) is dict and set(member)=={'BeforeUser','BeforeSystem','AfterUser','AfterSystem','Identity','Cookie','ParentCookie','ParentPID','User','System'},'native term member fields')
            require(all(integer(member[k]) and member[k]>=0 for k in ('BeforeUser','BeforeSystem','AfterUser','AfterSystem','User','System')) and integer(member['ParentPID']),'native term member integers')
            require(type(member['Cookie']) is str and type(member['ParentCookie']) is str,'native term member cookies');identity(member['Identity'])

def frame_matches(frame,cpu):
    require(type(frame) is dict and set(frame)==FRAME and type(frame['Eligible']) is bool and type(frame['Reason']) is str,'frame shape')
    e=frame['Evidence'];require(type(e) is dict and set(e)==set(POINTS)|{'MaxTotalDelta','BusyTicks','Quantum','CorrectionTicks','Terms'},'frame evidence roster')
    for k in ('MaxTotalDelta','BusyTicks','Quantum','CorrectionTicks'):require(integer(e[k]) and e[k]>=0,'frame integer type')
    native_terms(e['Terms'])
    p=[stamp(e[k]) for k in POINTS]
    return frame['Eligible'] and all(x>stamp('0001-01-01T00:00:00Z') for x in p) and p==sorted(p) and p[6]<=stamp(cpu['at'])<=p[7] and p[6]-p[1]<=cpu['interval_ns']<=p[7]-p[0]

def add_up(a,b):
    exact=Fraction.from_float(a)+Fraction.from_float(b);rounded=float(exact)
    return math.nextafter(rounded,math.inf) if Fraction.from_float(rounded)<exact else rounded

def derive(row):
    require(type(row) is dict and set(row)==FIELDS,'exact boundary roster')
    require(row['failure']=='','native observation/SQL readback failed')
    for k in ('ordinal','call','tick','observed_age','native_age','sample_tick','events'):require(integer(row[k]),'exact integer '+k)
    for k in ('manager','host','owner','epoch'):require(type(row[k]) is str and row[k],'original scope identity '+k)
    cpu=row['cpu'];require(type(cpu) is dict and set(cpu)==CPU_FIELDS and type(cpu['known']) is bool,'CPU shape')
    require(cpu['scope']=='controlled-test-host' and cpu['method']=='controlled-counter-interval','controlled native-input scope')
    background=row['current_cpu']
    require(type(background) is dict and set(background)==CPU_FIELDS and type(background['known']) is bool,'background observation shape/types')
    require(integer(background['interval_ns']) and integer(background['eligible_cpus']),'background native integer types')
    for k in ('capacity_cores','available_cores','busy_cores'):scalar(background[k])
    stamp(background['at'])
    require(all(type(background[k]) is str for k in ('scope','method','quality','detail')),'background native metadata types')
    require(integer(cpu['interval_ns']) and integer(cpu['eligible_cpus']),'native integer types')
    cap,idle,busy=[scalar(cpu[k]) for k in ('capacity_cores','available_cores','busy_cores')]
    now=stamp(row['now']);at=stamp(cpu['at']);observed=stamp(row['observed'])
    require(row['observed_age']==now-observed and row['native_age']==now-at,'original freshness ages bind exact times')
    good=cpu['known'] and cpu['eligible_cpus']>0 and .002<=cap<=65536 and cap<=cpu['eligible_cpus'] and 0<=idle<=cap and 0<=busy<=cpu['eligible_cpus'] and 100_000_000<=cpu['interval_ns']<=6_000_000_000 and 0<=row['observed_age']<=4_000_000_000 and 0<=row['native_age']<=4_000_000_000 and 0<=row['sample_tick']<=row['tick'] and row['tick']-row['sample_tick']<=4_000_000_000
    same_frame=frame_matches(row['frame'],cpu)
    require(type(row['leases']) is list and len(row['leases'])<=32 and type(row['listed']) is list,'closed physical/listed rosters')
    physical={};last=0
    for item in row['leases']:
        require(type(item) is dict and set(item)=={'row','id','key','data','run'} and integer(item['row']) and item['row']>last,'physical SQL row binding/order');last=item['row']
        l=raw_record(item['data']);require(set(l)<=LEASE_FIELDS and {'id','host','owner','request','state','created_at','revision','capabilities'}<=set(l) and set(l['request'])<=REQUEST_FIELDS and set(l['request']['resources'])=={'cpu','memory_bytes','disk_bytes'},'exact lease/request/resource schema');require(l['id']==item['id'] and l['request']['idempotency_key']==item['key'] and l['id'] not in physical,'physical ID/key bytes binding')
        require(item['run'] is None or type(item['run']) is str,'physical run descriptor type');physical[l['id']]=(l,item)
    listed={l['id']:l for l in row['listed']};require(len(listed)==len(row['listed']) and set(listed)==set(physical),'complete exact listed/SQL ID roster')
    for id,l in listed.items():
        require(typed(l)==typed(physical[id][0]),'complete actual SQL/listed row equality')
        require(real(l['request']['resources']['cpu']) and integer(l['revision']) and type(l['owner']) is str and type(l['host']) is str,'lease resource/revision types')
    require(type(row['bindings']) is dict and len(row['bindings'])<=32,'binding roster')
    for id,b in row['bindings'].items():require(id in physical and type(b) is dict and set(b)=={'key','side','volume','disk'} and integer(b['disk']) and b['disk']>0 and all(type(b[k]) is str and b[k] for k in ('key','side','volume')),'physical sync binding')
    saved,current=row['saved'],row['current'];require(type(saved) is dict and type(current) is dict and len(saved)<=32 and len(current)<=32,'owner roster bounds')
    kinds={};intrinsic={};exact={};closed={};unknown=set();valid=set();candidate_ids=set()
    for id,s in saved.items():
        require(type(s) is dict and set(s)==SAVED_FIELDS and s['kind'] in ('process','transit','inline') and type(s['source_joined']) is bool and type(s['source_exited']) is bool,'saved owner exact fields')
        identity(s['identity']);require(all(type(s[k]) is str for k in ('source_done','pointer','cookie','context','runtime','done','record','digest')) and s['pointer'],'saved immutable identity types')
        require(re.fullmatch('[0-9a-f]{64}',s['digest']) is not None,'saved digest type');kinds[id]=s['kind']
    for id,owners in current.items():
        require(type(owners) is list and 1<=len(owners)<=3,'current owner kind roster')
        for c in owners:
            require(type(c) is dict and set(c)==OWNER_FIELDS and c['kind'] in ('process','transit','inline'),'current owner exact fields')
            require(all(type(c[k]) is bool for k in ('exited','cancelled','joined','started','settled')) and integer(c['disk']),'current lifecycle type')
            require(all(type(c[k]) is str for k in OWNER_FIELDS-{'identity','exited','cancelled','joined','started','settled','disk'}),'current token types')
            if c['identity'] is not None:identity(c['identity'])
        require(id in physical,'current owner retained physical lease');c=owners[0];kinds.setdefault(id,c['kind'])
        l=listed[id];local=l['host']==row['host'] and l['owner']==row['owner'];active=consuming(l) and l.get('expires_at') is not None and stamp(l['expires_at'])>now and scalar(l['request']['resources']['cpu'])>0 and local
        own=False;match=False;s=saved.get(id)
        if len(owners)==1 and c['kind']=='process':
            own=active and c['identity'] is not None and typed(l.get('process_identity'))==typed(c['identity']) and not c['exited'] and not c['joined']
            match=own and s is not None and s['kind']=='process' and c['pointer']==s['pointer'] and typed(c['identity'])==typed(s['identity']) and c['cookie']==s['cookie']
        elif len(owners)==1 and c['kind']=='transit':
            record=raw_record(c['record']);own=active and c['manager']==row['manager'] and not c['cancelled'] and not c['joined'] and not c['settled'] and c['id']==id and c['key']==l['request']['idempotency_key'] and typed(record)==typed(l)
            match=own and s is not None and s['kind']=='transit' and c['pointer']==s['pointer'] and c['context']==s['context'] and c['done']==s['done'] and c['record']==s['record']
        elif len(owners)==1 and c['kind']=='inline':
            record=raw_record(c['record']);b=row['bindings'].get(id)
            own=active and c['manager']==row['manager'] and c['started'] and not c['cancelled'] and not c['joined'] and c['id']==id and c['runtime'] and c['context'] and c['done'] and c['record']==physical[id][1]['data'] and b==dict(key=c['binding_key'],side=c['binding_side'],volume=c['volume'],disk=c['disk'])
            match=own and s is not None and s['kind']=='inline' and c['pointer']==s['pointer'] and c['runtime']==s['runtime'] and c['context']==s['context'] and c['done']==s['done'] and hashlib.sha256(c['record'].encode()).hexdigest()==s['digest'] and typed(l)==typed(record)
        intrinsic[id]=bool(own);exact[id]=bool(match)
        if match:valid.add(id)
    for id,l in listed.items():
        if not consuming(l) or scalar(l['request']['resources']['cpu'])<=0 or id in current:continue
        had_native=l.get('process_identity') is not None or l.get('pid',0)>0
        retained=had_native or l['request'].get('kind') in ('relay-transit','sync-preparation') or ((l['state']=='needs_attention' or l['request'].get('origin_key','')) and (physical[id][1]['run'] is not None or id in row['bindings']))
        if retained:
            kinds.setdefault(id,'process' if had_native else 'inline' if id in row['bindings'] else 'transit');unknown.add(id)
    ambiguous=bool((set(saved)|set(current)|unknown)-valid)
    possible_exact=sum((Fraction.from_float(scalar(listed[id]['request']['resources']['cpu'])) for id in valid),Fraction())
    # Native credit follows independently bound candidate identity, never the
    # producer's classifier verdict. Each owner is capped by its own promise.
    credit=0.;seen=set();require(type(row['credits']) is list and len(row['credits'])<=32,'candidate roster')
    raw_candidate=0.
    for c in row['credits']:
        require(type(c) is dict and set(c)=={'id','job','identity','cookie','cores'} and type(c['id']) is str and type(c['job']) is str and type(c['cookie']) is str and real(c['cores']),'raw candidate type');identity(c['identity'])
        id=c['id'];owners=current.get(id,[]);l=listed.get(id);r=owners[0] if len(owners)==1 else None
        if l is not None and kinds.get(id)=='process':raw_candidate=max(raw_candidate,min(scalar(c['cores']),scalar(l['request']['resources']['cpu']),busy))
        if not good or not same_frame or id not in valid or saved[id]['kind']!='process' or r is None or c['job']!=r['pointer'] or typed(c['identity'])!=typed(r['identity']) or not c['cookie'] or c['cookie']!=r['cookie'] or scalar(c['cores'])<=0:continue
        key=typed(c['identity'])
        if key in seen:continue
        seen.add(key);credit=math.nextafter(credit+min(scalar(c['cores']),scalar(l['request']['resources']['cpu'])),0)
    reserved=sum(scalar(l['request']['resources']['cpu']) for l in listed.values() if consuming(l) and l.get('expires_at') and stamp(l['expires_at'])>now)
    credit=min(credit,reserved,busy) if good and same_frame else 0.
    if row['cut']=='return':
        e,s=row['evidence'],row['sample'];require(type(e) is dict and set(e)==EVIDENCE and type(s) is dict and set(s)==SAMPLE,'actual result exact shape')
        require(type(e['native_known']) is bool and type(s['Known']) is bool and integer(e['interval_ns']) and integer(s['ObservedTick']) and integer(s['Available']),'returned scalar types')
        require(e['at']==cpu['at'] and e['interval_ns']==cpu['interval_ns'] and s['ObservedAt']==cpu['at'] and s['ObservedTick']==row['sample_tick'] and s['Epoch']==row['epoch'],'result binds original frozen observation/clock/epoch')
        for k in ('idle_cores','verified_managed_cores','possible_compliant_supervised_cores','minimum_coverage_cores','possible_compliant_coverage_cores'):scalar(e[k])
        require(e['native_known']==good,'native freshness/shape verdict')
        if not good:
            require(e['state']=='unavailable' and not s['Known'] and s['Available']==0 and all(e[k]==0 for k in ('idle_cores','verified_managed_cores','possible_compliant_supervised_cores','minimum_coverage_cores','possible_compliant_coverage_cores')),'unavailable cannot invent coverage')
        else:
            require(e['idle_cores']==idle and e['verified_managed_cores']==credit,'native lower bound exact ownership/frame/cap binding')
            rounded_possible=0.
            for l in row['listed']:
                if l['id'] in valid:rounded_possible=add_up(rounded_possible,scalar(l['request']['resources']['cpu']))
            require(e['possible_compliant_supervised_cores']==rounded_possible,'exact per-owner outward sum')
            expected_min=idle+credit
            if credit>0:expected_min=math.nextafter(expected_min,0)
            expected_min=min(cap,expected_min)
            expected_max=min(cap,add_up(idle,max(rounded_possible,credit)))
            require(e['minimum_coverage_cores']==expected_min and e['possible_compliant_coverage_cores']==expected_max,'exact directed coverage values')
            possible=Fraction.from_float(scalar(e['possible_compliant_supervised_cores']));require(possible>=possible_exact and possible-possible_exact<=Fraction(1,10**10),'possible sum exact-rational outward bound')
            lo=Fraction.from_float(idle)+Fraction.from_float(credit);lo=min(Fraction.from_float(cap),lo)
            hi=min(Fraction.from_float(cap),Fraction.from_float(idle)+max(possible,Fraction.from_float(credit)))
            minimum=Fraction.from_float(scalar(e['minimum_coverage_cores']));maximum=Fraction.from_float(scalar(e['possible_compliant_coverage_cores']))
            require(0<=minimum<=lo and lo-minimum<=Fraction(1,10**10) and maximum>=hi and maximum-hi<=Fraction(1,10**10),'directed representable coverage bounds')
            enter=max(1,math.floor(Fraction.from_float(cap)*750));exit=max(enter+1,math.ceil(Fraction.from_float(cap)*900))
            low,high=math.floor(minimum*1000),math.ceil(maximum*1000)
            label='recovery' if low>=exit else 'uncertain' if ambiguous else 'shortfall' if high<=enter else 'neutral' if low>enter and high<exit else 'uncertain'
            require(e['state']==label and s['Known']==(label in ('recovery','shortfall','neutral')) and s['Available']==(low if label in ('recovery','neutral') else high if label=='shortfall' else 0),'actual classifier uses bound directions and ambiguity, never possible as health')
    else:require(row['sample'] is None and row['evidence'] is None,'entry has no synthesized expected output')
    def group(kind):
        ids={id for id,k in kinds.items() if k==kind};curr={id for id in ids if id in current};lost=ids&unknown
        status=1 if curr and all(intrinsic.get(id,False) for id in curr) and not lost else 2 if curr else 3 if lost else 0
        sampled=any(id in saved for id in ids)
        taint=sampled and any(not exact.get(id,False) for id in ids if id in saved)
        require(not curr or len(curr)==1 or kind=='transit' and len(curr)==17 and all(exact.get(id,False) for id in curr),'finite actual group partition')
        return status,sampled,taint
    facts={}
    for key,kind in [('p','process'),('t','transit'),('i','inline')]:facts[key],facts['s'+key],facts['x'+key]=group(kind)
    if cap==.0026 and idle==.002:obs=5
    elif cap==.25 and idle==0:obs=6
    else:
        require(cap==1 and idle in (0,.75,1,.5),'finite actual native partition');obs=0 if not cpu['known'] else {0:1,.75:2,1:3,.5:4}[idle]
    bgcpu=row['current_cpu'];bg=5 if bgcpu['capacity_cores']==.0026 else 6 if bgcpu['capacity_cores']==.25 else 0 if not bgcpu['known'] else {0:1,.75:2,1:3,.5:4}.get(bgcpu['available_cores'])
    require(bg is not None,'finite background partition')
    candidate=math.floor(Fraction.from_float(math.nextafter(raw_candidate,0))*1000) if raw_candidate>0 else 0
    # A zero-busy sample has no usable native candidate regardless of an old
    # original process row. This is an input normalization, not measured usage.
    require(candidate in (0,249,999),'finite candidate partition')
    t_ids=[id for id,k in kinds.items() if k=='transit' and id in listed and consuming(listed[id])]
    tp=751 if len(t_ids)==17 else 250
    bare=any(consuming(l) and scalar(l['request']['resources']['cpu'])>0 and id not in kinds and id not in unknown for id,l in listed.items())
    facts.update(obs=obs,bg=bg,good=good,frame=same_frame,tp=tp,candidate=candidate,bare=bare)
    return facts

SINGLE={'unknown','process-credit','process-cap-bare','process-fraction','transit-live','inline-live','inline-full','inline-unstarted','fractional','sum17','unsupported-native'}
def snapshot_identity(r):
    saved=copy.deepcopy(r['saved'])
    for s in saved.values():
        for k in ('source_done','source_joined','source_exited'):s.pop(k)
    return typed([r['cpu'],r['observed'],r['sample_tick'],r['frame'],saved,r['credits']])

def project(rows,scenario):
    require(scenario in SCENARIOS and type(rows) is list,'closed scenario')
    expected=1 if scenario in SINGLE else 3 if scenario in ('physical-idle','lost-inline-restart') else 2
    require(len(rows)==2*expected,'complete exact actual call count')
    states=[];last=None;prior_raw=None
    for n in range(expected):
        before,after=rows[2*n:2*n+2]
        for j,r in enumerate((before,after),2*n+1):
            require(r['schema']=='arm.cpu-warning-actual/v1' and r['scenario']==scenario and r['ordinal']==j and r['call']==n+1 and r['cut']==('enter' if j%2 else 'return'),'exact original boundary order/identity')
            derive(r)
        a=copy.deepcopy(before);b=copy.deepcopy(after)
        for x in (a,b):
            for k in ('cut','ordinal','sample','evidence'):x.pop(k)
        require(typed(a)==typed(b),'actual warning method changed frozen input, owner, SQL, event or context facts')
        facts=derive(before)
        state=dict(facts,epoch=1,changes=0,bgUsed=False,phase='ready',result='none',known=False,native=False,credit=0,possible=0,minimum=0,maximum=0,admission=False,signal=False,cleanup=False)
        if last is not None:
            state.update(epoch=last['epoch'],changes=last['changes'],bgUsed=last['bgUsed'])
            if before['manager']!=prior_raw['manager']:
                require(scenario=='lost-inline-restart' and set(before['saved'])==set(before['current'])==set(),'only original restart can lose the Manager owner')
                require(set(x['id'] for x in before['leases'])==set(x['id'] for x in prior_raw['leases']),'restart preserves exact ledger roster')
                state.update(epoch=last['epoch']+1,changes=0,bgUsed=False)
            elif snapshot_identity(before)!=snapshot_identity(prior_raw):state.update(epoch=last['epoch']+1,changes=0,bgUsed=False)
            elif facts['good']!=last['good']:
                require(last['good'] and not facts['good'],'age only invalidates frozen confidence')
            elif facts['bg']!=last['bg']:state['bgUsed']=True
            else:
                state['changes']=1
        states.append(state)
        s=copy.deepcopy(state);e=after['evidence'];sample=after['sample'];s.update(phase='done',result=e['state'],known=sample['Known'],native=e['native_known'],credit=math.floor(Fraction.from_float(float(e['verified_managed_cores']))*1000),possible=math.ceil(Fraction.from_float(float(e['possible_compliant_supervised_cores']))*1000),minimum=math.floor(Fraction.from_float(float(e['minimum_coverage_cores']))*1000),maximum=math.ceil(Fraction.from_float(float(e['possible_compliant_coverage_cores']))*1000))
        states.append(s);last=s;prior_raw=after
    if scenario=='process-joined':
        require(any(s['source_joined'] and s['source_exited'] for s in rows[-2]['saved'].values()),'original native Wait actually joined')
    if scenario in ('inline-live','inline-full'):
        require(rows[0]['credits'] and frame_matches(rows[0]['frame'],rows[0]['cpu']),'actual inline forged-candidate refusal covered')
    if scenario=='sum17':require(len(rows[0]['current'])==17 and len(rows[0]['saved'])==17,'actual17-owner rounding roster')
    if scenario=='lost-inline-restart':require(rows[0]['manager']!=rows[2]['manager'] and rows[2]['bindings'] and not rows[2]['current'],'actual Manager restart retains original operation binding')
    return dict(states=states,calls=expected,raw_rows=len(rows))


def controls(histories):
    rejected=[]
    def reject(label,scenario,mutate):
        rows=copy.deepcopy(histories[scenario]);mutate(rows)
        try:project(rows,scenario)
        except (ValueError,KeyError,TypeError):rejected.append(label);return
        raise ValueError('accepted malformed/altered actual evidence: '+label)
    reject('boolean-ordinal','bare',lambda r:r[0].update(ordinal=True))
    reject('integer-background-known','bare',lambda r:[x['current_cpu'].update(known=1) for x in r])
    reject('boolean-background-idle','bare',lambda r:[x['current_cpu'].update(available_cores=False) for x in r])
    reject('duplicate-physical-row','process-credit',lambda r:r[0]['leases'].append(copy.deepcopy(r[0]['leases'][0])))
    reject('missing-return','bare',lambda r:r.pop())
    reject('foreign-test-scope','bare',lambda r:r[0].update(scenario='other'))
    reject('unknown-healthy','unknown',lambda r:r[-1]['evidence'].update(state='recovery',native_known=True))
    reject('possible-as-measured','inline-live',lambda r:r[-1]['evidence'].update(verified_managed_cores=.25,minimum_coverage_cores=1,state='recovery'))
    reject('bare-health','bare',lambda r:r[-1]['evidence'].update(minimum_coverage_cores=1,state='recovery'))
    def coupled(rows,alter):
        for row in rows[:2]:alter(row)
    reject('wrong-original-native-cookie','process-credit',lambda r:coupled(r,lambda x:x['credits'][0].update(cookie='foreign-cookie')))
    reject('changed-saved-native-generation','process-credit',lambda r:coupled(r,lambda x:next(iter(x['saved'].values())).update(pointer='0x1234')))
    reject('same-frame-bypass','process-frame',lambda r:r[-1]['evidence'].update(verified_managed_cores=1))
    reject('joined-native-use','process-joined',lambda r:r[-1]['evidence'].update(verified_managed_cores=1,possible_compliant_supervised_cores=1))
    reject('lost-owner-shortfall','process-retained',lambda r:r[-1]['evidence'].update(state='shortfall'))
    reject('lost-owner-poisons-idle','physical-idle',lambda r:r[-1]['evidence'].update(state='uncertain'))
    reject('expired-owner-lends-use','process-expired',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=1))
    reject('changed-transit-context','transit-context',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('changed-transit-witness','transit-join',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('same-revision-transit-row','transit-row',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('changed-inline-context','inline-context',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('changed-inline-volume','inline-volume',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('cancelled-inline-use','inline-cancel',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('joined-inline-use','inline-joined',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('unstarted-inline-use','inline-unstarted',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('new-owner-old-snapshot','inline-after-snapshot',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('reopen-reconstructed-owner','lost-inline-restart',lambda r:r[3]['evidence'].update(possible_compliant_supervised_cores=.25))
    reject('round-sum-inward','sum17',lambda r:r[-1]['evidence'].update(possible_compliant_supervised_cores=.75))
    reject('fractional-recovery','fractional',lambda r:r[-1]['evidence'].update(state='recovery'))
    reject('stale-healthy','stale',lambda r:r[-1]['evidence'].update(state='recovery',native_known=True))
    reject('new-background-overwrites-frozen-sample','snapshot',lambda r:r[-1]['evidence'].update(idle_cores=0,minimum_coverage_cores=0,possible_compliant_coverage_cores=0,state='shortfall'))
    reject('original-inline-volume-binding','inline-live',lambda r:coupled(r,lambda x:next(iter(x['bindings'].values())).update(volume='another-volume')))
    reject('original-inline-runtime-binding','inline-live',lambda r:coupled(r,lambda x:next(iter(x['saved'].values())).update(runtime='0x1234')))
    reject('original-transit-row-binding','transit-live',lambda r:coupled(r,lambda x:next(iter(x['saved'].values())).update(record='{}')))
    reject('boolean-sample-available','bare',lambda r:r[-1]['sample'].update(Available=False))
    reject('method-mutates-ledger','bare',lambda r:r[1].update(events=r[1]['events']+1))
    reject('boolean-native-frame-terms','process-credit',lambda r:coupled(r,lambda x:x['frame']['Evidence'].update(Terms=True)))
    def invalid_calendar(rows):
        original=rows[0]['now'][:10]
        def change(v):
            if type(v) is str:return v.replace(original,'2026-02-30')
            if type(v) is list:return [change(x) for x in v]
            if type(v) is dict:return {k:change(x) for k,x in v.items()}
            return v
        rows[:]=change(rows)
    reject('nonexistent-calendar-day','bare',invalid_calendar)
    return rejected
