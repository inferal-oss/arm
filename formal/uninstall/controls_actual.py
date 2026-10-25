"""Mutate retained REAL histories; no fabricated successful trace fixtures."""
import copy


def controls(histories, projection):
    def row(rows, action, last=False):
        found=[r for r in rows if r['action']==action]
        return found[-1] if last else found[0]
    def wrong_owner(rows):
        row(rows,'ownership-check-return')['ownership']['binary']['inode']+=1
    def wrong_source_revision(rows):
        r=row(rows,'source-cas-return')
        r['source']['revision']+=1;r['attempt_source']['revision']+=1
    def changed_generation(rows):
        row(rows,'native-enter')['stop']['record_target']['backend_generation']='f'*32
    def wrong_pair(rows):
        row(rows,'auth-mutation-return')['authorization']['peers'][0]['pair']='e'*64
    def missing_lookup(rows):
        index=next(i for i,r in enumerate(rows)if r['action']=='lookup-call-return')
        rows.pop(index)
        for i,r in enumerate(rows,1):r['ordinal']=i
    cases=(
        ('lost-finish-error-erased','lost-finish-ack',lambda r:row(r,'finish-call-return').update(error='')),
        ('lost-finish-lookup-missing','lost-finish-ack',missing_lookup),
        ('copy-before-intent','removed-source-cas',lambda r: row(r,'receipt-write-return').update(action='driver-initial')),
        ('wrong-owned-inode','removed-source-cas',wrong_owner),
        ('wrong-stop-generation','removed-source-cas',changed_generation),
        ('running-desired-intent','removed-source-cas',lambda r: row(r,'native-enter')['stop']['desired'].update(desired_running=True)),
        ('missing-native-scope','removed-source-cas',lambda r: row(r,'native-enter').update(action='driver-initial')),
        ('foreign-pair','removed-source-cas',wrong_pair),
        ('wrong-original-key','removed-source-cas',lambda r: row(r,'receipt-write-return')['stored'].update(key='other')),
        ('missing-final-suffix','removed-source-cas',lambda r:r.pop()),
        ('source-revision-gap','removed-source-cas',wrong_source_revision),
        ('completion-regression','removed-source-cas',lambda r:row(r,'source-monotone-observed')['source'].update(phase='incomplete')),
        ('read-grant-became-mutation','grant-read-after-revoke',lambda r:row(r,'auth-mutation-return',True).update(error='')),
        ('unknown-new-key-accepted','copy-unknown-new-key',lambda r:row(r,'other-key-call-return').update(error='')),
        ('native-uint64-overflow','removed-source-cas',lambda r:row(r,'copy-output-created')['allocation'].update(device=2**64)),
        ('zero-identity-bool-alias','removed-source-cas',lambda r:row(r,'receipt-write-return')['stored']['recovery_identity'].update(birth_known=0)),
        ('wrong-allocation-inode','removed-source-cas',lambda r:row(r,'copy-output-created')['allocation'].update(inode=1)),
    )
    rejected=[]
    for name,scenario,mutate in cases:
        rows=copy.deepcopy(histories[scenario]);mutate(rows)
        try:projection.project(rows,scenario)
        except (ValueError,KeyError,TypeError,StopIteration):rejected.append(name)
        else:raise ValueError('altered actual history accepted: '+name)
    return rejected
