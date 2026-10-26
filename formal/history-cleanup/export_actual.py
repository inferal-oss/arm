"""Closed projection of native/SQL observations; never executes cleanup decisions."""
import copy
import hashlib
import json
import re

SCENARIOS=('progress64','lost-before','lost-after','lost-unlink','same-revision','false-completed','reappear','cancel','worker','cas-replacement','cold-ref')
FIELDS={'stage','row','expectedRow','absent','pendingAbsent','worker','workerOwner','workerFD','coldCurrent','workerCurrent','context','contextOwner','deadline','remainingNS','bounded','work'}
STAGES={'enter','bound','guard','unlink','sync','checkpoint','ack','cancel','worker','cold','reappear','replace','stop','retry','reconcile'}
def require(ok,message):
    if not ok: raise ValueError(message)
def pairs(items):
    out={}
    for k,v in items:
        require(k not in out,'duplicate field');out[k]=v
    return out
def parse(raw):return json.loads(raw,object_pairs_hook=pairs)
def strict_int(v):return type(v) is int

def project(rows,scenario):
    require(scenario in SCENARIOS and type(rows) is list and len(rows)>4,'scenario/rows')
    roster={};tokens={};states=[];initial_revision=None;request=None;owner=None;receipt=None;worker=None;window=None;windows=[]
    def token(h):
        require(type(h) is str and re.fullmatch('[0-9a-f]{64}',h) and h in roster,'exact known row hash')
        if h not in tokens:tokens[h]=len(tokens)
        return tokens[h]
    for index,entry in enumerate(rows):
        require(type(entry) is dict and set(entry)=={'observation','rows'},'raw envelope roster')
        v=entry['observation'];require(type(v) is dict and set(v)==FIELDS,'observation exact fields')
        require(type(entry['rows']) is dict,'row dictionary')
        for h,raw in entry['rows'].items():
            require(type(raw) is str and hashlib.sha256(raw.encode()).hexdigest()==h and h not in roster,'row bytes/hash/uniqueness')
            op=parse(raw);require(type(op) is dict and set(op)=={'schema','request','request_sha256','actor','created_at','updated_at','revision','phase','pack_id','prepared','receipt','cleaned'},'exact operation row roster');roster[h]=op
        for k in ('coldCurrent','workerCurrent','context','bounded'):require(type(v[k]) is bool,'exact boolean '+k)
        require(type(v['contextOwner']) is str and type(v['deadline']) is str and strict_int(v['remainingNS']),'actual context ownership/deadline types')
        require(type(v['worker']) is str and type(v['workerOwner']) is str and strict_int(v['workerFD']) and strict_int(v['work']),'worker/work type')
        require(type(v['stage']) is str and v['stage'] in STAGES,'stage roster')
        row=token(v['row']);expected=token(v['expectedRow']);op=roster[v['row']];pred=roster[v['expectedRow']]
        if initial_revision is None:
            initial_revision=op['revision'];request=op['request'];owner=op['actor'];receipt=op['receipt']
            require(v['stage']=='enter' and row==expected==0,'actual committed initial cut')
        for x in (op,pred):
            require(x['request']==request and x['actor']==owner and x['receipt']==receipt,'original tuple/owner/receipt binding')
            require(strict_int(x['revision']) and x['revision']>=initial_revision,'revision type/bound')
        members=request['plan']['members'];n=len(members)
        require(n==(64 if scenario=='progress64' else 5),'actual multi-batch member bound')
        names=[m['file'] for m in members];require(len(set(names))==n,'exact member roster')
        for x in (op,pred):
            require(type(x['cleaned']) is list and x['cleaned']==names[:len(x['cleaned'])] and len(x['cleaned'])<=n,'exact cleaned prefix')
        for k in ('absent','pendingAbsent'):
            require(type(v[k]) is list and len(v[k])==n and all(type(b) is bool for b in v[k]),'exact native roster/types')
        require(all(v['pendingAbsent']),'fixture pending absence')
        absent=sum(v['absent']);s=copy.deepcopy(states[-1]) if states else dict(removed=0,work=0,clean=0,rev=0,row=0,expected=0,ackRow=0,running=True,window=False,worker=True,context=True,cold=True,reappeared=False,completed=False,certified=False,reset=False)
        stage=v['stage']
        if stage=='enter':
            require(v['bounded'] and v['deadline'] and v['contextOwner'] and 0<v['remainingNS']<=5_000_000_000,'unchanged five-second mutation window')
            window=(v['contextOwner'],v['deadline']);require(window not in windows,'fresh bounded context per batch');windows.append(window)
        if stage in ('bound','guard','unlink','sync','checkpoint'):
            require(window==(v['contextOwner'],v['deadline']) and 0<v['remainingNS']<=5_000_000_000,'original context owns this batch cut')
            require(v['bounded'] and v['context'] and v['workerCurrent'],'actual bounded/native owner cut')
        if v['worker']:
            if worker is None:worker=(v['worker'],v['workerOwner'],v['workerFD'])
            require((v['worker'],v['workerOwner'],v['workerFD'])==worker,'original retained worker/descriptor identity')
        s.update(row=row,expected=expected,clean=len(op['cleaned']),rev=op['revision']-initial_revision,completed=op['phase']=='completed')
        require(op['phase'] in ('committed','completed'),'cleanup phase roster')
        if stage=='bound':s.update(window=True,work=v['work'])
        if stage=='unlink':
            require(rows[index-1]['observation']['stage']=='guard' and v['coldCurrent'],'actual immediately preceding exact SQL/native guard')
            require(absent==s['removed']+1,'one actual native unlink');s['removed']=absent
        if stage=='sync':s['work']=v['work']
        if stage=='checkpoint':s.update(window=False,ackRow=row,certified=s['completed'])
        if stage=='cancel':s['context']=False
        if stage=='worker':s['worker']=False
        if stage=='cold':s['cold']=False
        if stage=='reappear':s['reappeared']=True
        if stage=='replace':s['certified']=False
        if stage=='stop':s.update(running=False,window=False)
        if stage=='reconcile':
            require(v['context'],'fresh explicit reconciliation context')
            s.update(running=True,context=True,work=s['clean'],expected=row);worker=None
        if stage!='reappear':require(absent==s['removed']-(1 if s['reappeared'] else 0),'native absence/progress correspondence')
        if states and stage=='retry':require(s==states[-1],'ordinary exact-key retry is read-only')
        states.append(s)
    require(states[-1]['running'] is False and rows[-1]['observation']['stage'] in ('stop','retry'),'terminal observation')
    if scenario in ('progress64','lost-before','lost-after','lost-unlink','cancel'):require(states[-1]['certified'] and states[-1]['removed']==n,'complete positive native outcome')
    else:require(states[-1]['removed']==4 and not states[-1]['certified'],'refusal preserves later member')
    require(any(s['clean']==4 for s in states) or scenario in ('lost-unlink','cas-replacement','cold-ref'),'nontrivial multi-batch progress')
    require(len(windows)==16 if scenario=='progress64' else len(windows)>=1,'actual batch window roster')
    return {'states':states,'members':n,'rows':len(rows),'windows':len(windows)}

def controls(histories):
    tests=[]
    def reject(name,scenario,change):
        value=copy.deepcopy(histories[scenario]);change(value)
        try:project(value,scenario)
        except (ValueError,KeyError,TypeError):tests.append(name);return
        raise ValueError('negative control accepted: '+name)
    reject('bool-as-work','progress64',lambda r:r[0]['observation'].__setitem__('work',True))
    reject('absent-roster','progress64',lambda r:r[0]['observation']['absent'].pop())
    reject('boolean-as-native-count','progress64',lambda r:r[0]['observation']['absent'].__setitem__(0,1))
    reject('crossed-row','same-revision',lambda r:r[-1]['observation'].__setitem__('row',r[0]['observation']['row']))
    reject('false-complete-substitution','false-completed',lambda r:r[-1]['observation'].__setitem__('absent',[True]*5))
    reject('worker-substitution','progress64',lambda r:r[1]['observation'].__setitem__('worker','replacement'))
    reject('native-effects-hidden','reappear',lambda r:r[-1]['observation']['absent'].__setitem__(4,True))
    reject('unbounded-effect','progress64',lambda r:next(x for x in r if x['observation']['stage']=='unlink')['observation'].__setitem__('bounded',False))
    reject('boolean-deadline-budget','progress64',lambda r:r[0]['observation'].__setitem__('remainingNS',True))
    reject('detached-window-context','progress64',lambda r:next(x for x in r if x['observation']['stage']=='guard')['observation'].__setitem__('contextOwner','detached'))
    def reuse_window(rows):
        enters=[x['observation'] for x in rows if x['observation']['stage']=='enter']
        enters[1].update(contextOwner=enters[0]['contextOwner'],deadline=enters[0]['deadline'])
    reject('reused-window-context','progress64',reuse_window)
    return tests
