"""Alter retained actual evidence; every complete reader must refuse it."""
import copy

def controls(histories, projection):
    rejected=[]
    def check(name, scenario, alter):
        rows=copy.deepcopy(histories[scenario]);alter(rows)
        # Row-drop controls retain a well-formed envelope sequence, so refusal
        # must also check actual decision/coverage rather than just old ordinals.
        for i,row in enumerate(rows,1): row['ordinal']=i
        try: projection.project(rows,scenario)
        except ValueError: rejected.append(name);return
        raise RuntimeError('accepted actual corruption: '+name)
    def pick(rows,cut): return next(r for r in rows if r['cut']==cut)
    def sql(rows,action): return next(r for r in rows if r['cut']=='sql' and r['operation']['action']==action and r.get('leases'))
    def permission_budget(rows,predicate,alter):
        index=next(i for i,r in enumerate(rows) if predicate(r))
        # Preserve the producer's exact computation→permission coupling; these
        # controls must fail the substantive admission/renewal relation.
        alter(rows[index]['budget'])
        rows[index-1]['budget']=copy.deepcopy(rows[index]['budget'])
    check('boolean-numeric-alias','shrink-renew',lambda r:pick(r,'admission').update(requested=True))
    check('computed-capacity','shrink-renew',lambda r:pick(r,'admission')['budget'].update(capacity_cores=3))
    check('native-headroom','shrink-renew',lambda r:pick(r,'admission')['budget']['observation'].update(available_cores=3))
    def unknown(rows):
        def alter(b):
            b['observation']['known']=False
            b.update(known=False,capacity_cores=0,available_cores=0,deficit_cores=b['reserved_cores'])
        permission_budget(rows,lambda r:r['cut']=='admission',alter)
    check('grant-from-unknown','shrink-renew',unknown)
    check('missing-permission-cut','shrink-renew',lambda r:r.__setitem__(slice(None),[v for v in r if v['cut']!='admission']))
    check('foreign-owner','shrink-renew',lambda r:sql(r,'acquire')['leases'][0]['body'].update(owner='4294967295'))
    check('physical-request-key','shrink-renew',lambda r:sql(r,'acquire')['leases'][0].update(key='B'))
    def lost(rows):
        row=next(r for r in rows if r['cut']=='sql' and r['operation']['action']=='observe' and r.get('leases'))
        row['leases'][0]['body'].update(state='terminal',outcome='released')
    check('shrink-erases-promise','shrink-renew',lost)
    def twice(rows):
        permission_budget(rows,lambda r:r['cut']=='renewal' and r['budget']['known'] and r['budget']['deficit_cores']==0,
            lambda b:b.update(reserved_cores=4,available_cores=0,deficit_cores=2))
    check('renewal-counts-self-twice','shrink-renew',twice)
    def tail(rows):
        row=next(r for r in rows if r['cut']=='sql' and len([e for e in r.get('events',[]) if e['type']=='lease.granted'])==2)
        indices=[i for i,e in enumerate(row['events']) if e['type']=='lease.granted']
        a,b=indices;sa,sb=row['events'][a]['sequence'],row['events'][b]['sequence']
        row['events'][a],row['events'][b]=row['events'][b],row['events'][a]
        row['events'][a]['sequence'],row['events'][b]['sequence']=sa,sb
    check('fitting-tail-bypasses-head','fifo-recovery',tail)
    check('missing-final-sql','shrink-renew',lambda r:r.pop())
    check('changed-native-scope','unknown-stale',lambda r:pick(r,'budget')['budget']['observation'].update(scope='another-host'))
    def zero(rows):
        row=sql(rows,'zero');item=next(v for v in row['leases'] if v['key']=='Z')
        item['body']['request']['resources']['cpu']=1
    check('zero-cpu-borrows-positive-permit','unknown-stale',zero)
    def credit_input(rows,change):
        index=next(i for i,r in enumerate(rows) if r['cut']=='renewal' and r['budget']['managed_credit_cores']>0)
        # The positive deliberately retains duplicate candidates. Invalidate all
        # copies: invalidating just one correctly leaves the other usable.
        for candidate in rows[index]['credit_inputs']:
            change({'credit_inputs':[candidate],'budget':rows[index]['budget']})
        rows[index-1]['credit_inputs']=copy.deepcopy(rows[index]['credit_inputs'])
        rows[index-1]['budget']=copy.deepcopy(rows[index]['budget'])
    def current_generation(row):row['credit_inputs'][0]['current_generation']='0x1234'
    check('credit-replaced-current-job','managed-credit',lambda r:credit_input(r,current_generation))
    check('credit-current-cookie-changed','managed-credit',lambda r:credit_input(r,lambda x:x['credit_inputs'][0].update(current_cookie='another-native-lifetime')))
    check('credit-exited-before-cleanup','managed-credit',lambda r:credit_input(r,lambda x:x['credit_inputs'][0].update(current_exited=True)))
    check('credit-joined-worker','managed-credit',lambda r:credit_input(r,lambda x:x['credit_inputs'][0].update(current_done=True)))
    check('credit-foreign-native-start','managed-credit',lambda r:credit_input(r,lambda x:x['credit_inputs'][0]['sample_identity'].update(start='another-lifetime')))
    check('credit-foreign-current-lease','managed-credit',lambda r:credit_input(r,lambda x:x['credit_inputs'][0]['current_lease']['body'].update(id='0'*32)))
    check('credit-current-lease-not-consuming','managed-credit',lambda r:credit_input(r,lambda x:x['credit_inputs'][0]['current_lease']['body'].update(state='terminal',outcome='released')))
    def duplicate_spend(row):
        b=row['budget'];b['managed_credit_cores']=2
        b['capacity_cores']=4;b['available_cores']=4-b['reserved_cores'];b['deficit_cores']=0
    check('credit-duplicate-identity-spent-twice','managed-credit',lambda r:credit_input(r,duplicate_spend))
    def borrowed(rows):
        i=next(i for i,r in enumerate(rows) if r['cut']=='renewal' and r['budget']['observation']['available_cores']==0 and r['budget']['reserved_cores']==3)
        b=rows[i]['budget'];b.update(managed_credit_cores=3,capacity_cores=3,available_cores=0,deficit_cores=0)
        rows[i-1]['budget']=copy.deepcopy(b)
    check('credit-borrows-other-idle-promise','managed-credit',borrowed)
    check('credit-false-original-owner-join','managed-credit',lambda r:next(x for x in r if x.get('operation',{}).get('joined')).get('operation').update(joined=False))
    check('credit-unannounced-withdrawal','credit-invalidation',lambda r:r.__setitem__(slice(None),[x for x in r if x.get('operation',{}).get('action')!='invalidate']))
    return rejected
