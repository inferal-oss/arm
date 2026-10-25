"""Closed sanitized actual-cut relation. No filesystem/clock/process probes.

Initial publication/helper entry are explicit component/real-CLI premises.
Positive Stop/maintenance cuts MUST come from the actual inspector trace, never
RemoveUninstallArtifact's caller-provided Observation. lockHeld is maintenance
permission, not physical unlock or a native-owner/descriptor join theorem.
"""
import copy
import hashlib
import json
import re

SCENARIOS = ('removed-source-cas','lost-finish-ack','copy-unknown-new-key','grant-read-after-revoke','replaced-name-refusal')
FIELDS = 'key phase copyStarted copyActive recovery stopCurrent lockHeld mutationRight readRight identityExact ownershipFresh checked artifactPresent absenceObserved unlinkEffects sourcePhase sourceRevision sourceJoined returnedUnknown consent attempted'.split()
ROW = set('authorization result requested_key allocation ownership absent attempt_source source_error schema ordinal action binding attempt stored stored_error source stop error'.split())
ACTIONS = set('source-initial driver-initial auth-mutation-return auth-read-return receipt-write-return copy-output-created begin-call-return finish-reviewed native-enter ownership-check-return unlink-call-return absence-check-return remove-call-return native-leave finish-call-return lookup-call-return source-owner-returned source-cas-return source-monotone-observed other-key-call-return driver-final'.split())
BINDING = set('key review_sha256 transfer_key root binary_sha256 binary_bytes owner stop_key stop_sha256 target actor root_identity artifact_identity binary_identity recovery'.split())
NATIVE = set('device inode owner born_seconds born_nanoseconds birth_known'.split())
PHASES = {'none','copy-intent','copy-unknown','accepted','stop-admitted','stop-unknown','remove-intent','removed'}


def require(v, msg):
    if not v: raise ValueError(msg)


def unique(pairs):
    out = {}
    for key,value in pairs:
        require(key not in out,'duplicate JSON field')
        out[key]=value
    return out


def parse(raw):
    return json.loads(raw,object_pairs_hook=unique,parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite JSON')))


def digest(value): return hashlib.sha256(value).hexdigest()

def integer(v): return type(v) is int

def hexid(value,n): return type(value) is str and re.fullmatch('[0-9a-f]{'+str(n)+'}',value) is not None

def native_scalars(value):
    require(type(value) is dict and set(value)==NATIVE,'native identity schema')
    require(type(value['birth_known']) is bool and all(integer(value[k]) for k in NATIVE-{'birth_known'}),'native identity scalar alias')
    require(0<=value['device']<2**64 and 0<=value['inode']<2**64 and 0<=value['owner']<2**32 and
            -(2**63)<=value['born_seconds']<2**63 and -(2**63)<=value['born_nanoseconds']<2**63,'native scalar outside Go domain')


def native(value,owner):
    native_scalars(value)
    require(value['birth_known'] and value['inode']>0 and value['owner']==int(owner) and value['born_seconds']>0 and 0<=value['born_nanoseconds']<10**9,'native identity/owner differs')


def known_identity(value,owner):
    native_scalars(value)  # Validate types BEFORE Python's bool==0 equality.
    if value==dict(device=0,inode=0,owner=0,born_seconds=0,born_nanoseconds=0,birth_known=False):return False
    native(value,owner);return True


def binding(b):
    require(set(b)==BINDING and hexid(b['review_sha256'],64) and hexid(b['binary_sha256'],64) and hexid(b['transfer_key'],64) and integer(b['binary_bytes']) and 0<b['binary_bytes']<=256<<20,'review/transfer binding schema')
    require(type(b['key']) is str and 0<len(b['key'])<=128 and b['owner'].isdigit() and str(int(b['owner']))==b['owner'] and 0<=int(b['owner'])<2**32,'key/account binding')
    token=digest(b['key'].encode())
    require(b['stop_key']=='uninstall-'+token and b['recovery']==b['root']+'/recovery-'+token+'/arm' and hexid(b['stop_sha256'],64),'key/recovery scope differs')
    t,a=b['target'],b['actor']
    require(set(t)==set('host certificate_sha256 spki_sha256 owner home directory backend_generation startup_contract'.split()) and set(a)==set('source_host certificate_sha256 spki_sha256 pair grant_revision'.split()), 'closed target/actor schema')
    require(t['owner']==b['owner'] and hexid(t['host'],32) and hexid(t['backend_generation'],32) and hexid(t['certificate_sha256'],64) and hexid(t['spki_sha256'],64),'target scope differs')
    require(hexid(a['source_host'],32) and hexid(a['certificate_sha256'],64) and hexid(a['spki_sha256'],64) and hexid(a['pair'],64) and integer(a['grant_revision']) and a['grant_revision']>0,'actor scope differs')
    for name in ('root_identity','artifact_identity','binary_identity'):native(b[name],b['owner'])
    native(t['directory'],b['owner'])


def receipt(r,b):
    require(set(r)=={'key','review_sha256','phase','recovery_identity'} and r['key']==b['key'] and r['review_sha256']==b['review_sha256'] and r['phase'] in PHASES-{'none'},'receipt key/review/phase differs')
    known=known_identity(r['recovery_identity'],b['owner'])
    require((r['phase']!='copy-intent' or not known) and (r['phase'] in ('copy-intent','copy-unknown') or known),'receipt native recovery identity missing')
    return known


def authorization(a,b):
    require(set(a)==set('read_only host owner certificate spki listener_enabled peers'.split()) and type(a['read_only']) is type(a['listener_enabled']) is bool and type(a['peers']) is list and len(a['peers'])<=512,'public authorization schema')
    t=b['target'];actor=b['actor'];seen=set();match=None
    target=(a['host'],a['owner'],a['certificate'],a['spki'])==(t['host'],t['owner'],t['certificate_sha256'],t['spki_sha256'])
    for p in a['peers']:
        require(set(p)==set('host certificate spki pair revision state proven permissions'.split()) and p['host'] not in seen and p['host']!=a['host'],'duplicate/self/unknown public peer')
        require(hexid(p['host'],32) and hexid(p['certificate'],64) and hexid(p['spki'],64) and hexid(p['pair'],64) and integer(p['revision']) and type(p['proven']) is bool and type(p['permissions']) is list and len(set(p['permissions']))==len(p['permissions']),'public peer scalar/permission alias')
        seen.add(p['host'])
        if (p['host'],p['certificate'],p['spki'],p['pair'])==(actor['source_host'],actor['certificate_sha256'],actor['spki_sha256'],actor['pair']):match=p
    identity=target and match is not None and match['state']=='active' and match['proven']
    read=identity and bool(set(match['permissions'])&{'node-uninstall','observe-own','observe-all'})
    mutate=identity and a['listener_enabled'] and match['revision']==actor['grant_revision'] and {'node-uninstall','node-stop'}<=set(match['permissions'])
    return bool(mutate),bool(read),bool(identity)


class Projector:
    def __init__(self):
        self.s=dict(key='none',phase='none',copyStarted=False,copyActive=False,recovery=False,stopCurrent=False,lockHeld=False,mutationRight=False,readRight=False,identityExact=True,ownershipFresh=True,checked=False,artifactPresent=True,absenceObserved=False,unlinkEffects=0,sourcePhase='incomplete',sourceRevision=1,sourceJoined=False,returnedUnknown=False,consent=False,attempted=False)
        self.states=[];self.actions=[];self.bind=None;self.last_source=None;self.source_base=None;self.allocation=None;self.last_auth=0;self.last_owned=0;self.removal_intent=0;self.current_removing='';self.seen=[];self.source_hash=None;self.native_error=False;self.lost_error=False;self.lookup_after_error=False
    def emit(self,action,**changes):
        self.s.update(changes);self.states.append(copy.deepcopy(self.s));self.actions.append(action)
    def inputs(self,m=None,r=None,i=None,o=None):
        self.emit('ObserveInputs',mutationRight=self.s['mutationRight'] if m is None else m,readRight=self.s['readRight'] if r is None else r,identityExact=self.s['identityExact'] if i is None else i,ownershipFresh=self.s['ownershipFresh'] if o is None else o,checked=False)
    def stop(self,o):
        require(set(o)==set('record_schema record_desired_revision record_phase record_key record_digest record_target record_actor desired server_joined original_generation_absent boundary'.split()),'Stop boundary schema')
        b=self.bind;d=o['desired']
        require(set(d)==set('schema revision desired_running target stop_key stop_sha256'.split()) and d['schema']=='arm.node-stop.state/v1' and integer(d['revision']) and integer(o['record_desired_revision']), 'desired stopped schema/revision differs')
        require(o['boundary']=='accepted-original-WithInspectedNodeStop' and o['record_schema']=='arm.node-stop.record/v1' and o['record_phase']=='drain-ready' and o['record_key']==b['stop_key'] and o['record_digest']==b['stop_sha256'] and o['record_target']==b['target'] and o['record_actor']==b['actor'],'original Stop record changed')
        require(o['server_joined'] is True and o['original_generation_absent'] is True and d['desired_running'] is False and d['target']==b['target'] and d['stop_key']==b['stop_key'] and d['stop_sha256']==b['stop_sha256'] and d['revision']==o['record_desired_revision'],'current stopped intent/join differs')
    def consume(self,f):
        require(type(f) is dict and set(f)==ROW and f['schema']=='arm.uninstall.actual/v1' and integer(f['ordinal']) and f['ordinal']==len(self.seen)+1 and f['action'] in ACTIONS,'unknown/reordered raw schema')
        require(all(type(f[k]) is str for k in ('error','stored_error','source_error','requested_key')),'raw string alias')
        binding(f['binding'])
        if self.bind is None:
            require(f['action']=='source-initial' and f['source'] is not None,'complete source initial state absent')
            self.bind=copy.deepcopy(f['binding']);self.emit('Init')
        require(f['binding']==self.bind,'immutable original binding changed')
        b=self.bind;a=f['action'];self.seen.append(a)
        if f['stored'] is not None:
            require(not f['stored_error'],'ambiguous stored receipt')
            recovery=receipt(f['stored'],b)
        else:
            require(self.s['phase']=='none' and f['stored_error'],'missing retained original duty')
            recovery=False
        if a=='receipt-write-return':
            require(f['attempt'] is not None and f['stored']==f['attempt'] and not f['error'],'unknown/partial write not a successful receipt transition')
            r=f['stored'];phase=r['phase']
            if phase=='copy-intent':
                require(self.s['phase']=='none' and self.s['mutationRight'],'replayed/ungranted copy intent')
                self.emit('Admit',key='A',phase=phase)
            elif phase in ('accepted','copy-unknown'):
                require(self.s['copyActive'] and self.s['phase']=='copy-intent','copy outcome without original creation')
                if recovery:require(r['recovery_identity']==self.allocation,'copied helper original native identity differs')
                self.emit('FinishCopy',phase=phase,copyActive=False,recovery=recovery,returnedUnknown=phase=='copy-unknown')
            elif phase in ('stop-admitted','stop-unknown'):
                require(self.s['phase']=='accepted','Stop repeated or lacks known copy')
                self.emit('MarkStop',phase=phase,returnedUnknown=phase=='stop-unknown',consent=phase=='stop-admitted')
            elif phase=='remove-intent':
                require(self.s['checked'] and self.s['lockHeld'],'removal intent without real native check')
                self.removal_intent=f['ordinal'];self.emit('RemovalIntent',phase=phase)
            elif phase=='removed':
                require(self.s['phase']=='remove-intent' and self.s['absenceObserved'] and self.s['lockHeld'],'removed receipt lacks observed original absence')
                self.emit('FinishRemoval',phase=phase,returnedUnknown=False)
            else:raise ValueError('unmapped receipt write')
        elif a=='copy-output-created':
            require(self.s['phase']=='copy-intent' and not self.s['copyStarted'] and f['allocation'] is not None,'copy before durable intent/repeated')
            native(f['allocation'],b['owner']);self.allocation=copy.deepcopy(f['allocation'])
            self.emit('BeginCopy',copyStarted=True,copyActive=True)
        elif a in ('auth-mutation-return','auth-read-return'):
            require(f['authorization'] is not None and f['authorization']['read_only']==(a=='auth-read-return'),'authorization role mismatch')
            m,r,i=authorization(f['authorization'],b)
            require(bool(f['error']) != (r if a=='auth-read-return' else m),'authorization result contradicts public original grant snapshot')
            self.inputs(m,r,i)
            if a=='auth-mutation-return' and not f['error']:self.last_auth=f['ordinal']
        elif a=='finish-reviewed':
            require(self.s['recovery'] and not self.s['lockHeld'],'finish review before retained helper or during native action')
            self.emit('ReviewFinish',consent=True)
        elif a=='native-enter':
            require(f['stop'] is not None and self.s['consent'] and self.s['recovery'] and not self.s['lockHeld'],'native action without explicit original review/recovery')
            self.stop(f['stop']);self.emit('EnterNative',stopCurrent=True,lockHeld=True,consent=False,attempted=False)
        elif a=='native-leave':
            require(self.s['lockHeld'] and f['stop'] is not None,'unowned maintenance end')
            self.stop(f['stop']);self.native_error=bool(f['error']);self.emit('LeaveNative',lockHeld=False,checked=False)
        elif a=='ownership-check-return':
            require(self.s['lockHeld'] and f['ownership'] is not None,'ownership check outside original maintenance scope')
            o=f['ownership']
            require(set(o)=={'root','artifact','binary','homes','removing'},'ownership schema')
            if f['error']:
                self.inputs(o=False)
            else:
                require(o['root']==b['root_identity'] and o['artifact']==b['artifact_identity'] and o['binary']==b['binary_identity'] and o['homes']==[b['target']['home']] and o['removing'] in ('',b['key']),'fresh current ownership differs')
                require(self.s['mutationRight'] and self.s['identityExact'] and self.last_auth>0,'ownership alone acquired mutation right')
                self.inputs(o=True);self.current_removing=o['removing'];self.last_owned=f['ordinal']
                self.emit('CheckRemove',checked=True)
        elif a=='unlink-call-return':
            require(self.s['checked'] and self.s['lockHeld'] and self.s['phase']=='remove-intent' and not self.s['attempted'] and self.s['artifactPresent'] and self.current_removing==b['key'] and self.last_owned>self.last_auth>self.removal_intent,'unlink lacks fresh exact original mutation/ownership/name fence')
            ok=not f['error'];self.emit('Unlink',checked=False,attempted=True,artifactPresent=not ok,unlinkEffects=1 if ok else 0,returnedUnknown=not ok)
        elif a=='absence-check-return':
            require(type(f['absent']) is bool and not f['error'] and f['absent'] and self.s['lockHeld'] and self.s['unlinkEffects']==1 and not self.s['artifactPresent'],'absence is not a joined/known removal verdict')
            self.emit('ObserveAbsent',absenceObserved=True)
        elif a=='other-key-call-return':
            require(f['requested_key'] and f['requested_key']!=b['key'] and f['error'] and self.s['phase']=='copy-unknown','new key did not retain original unknown copy duty')
        elif a in ('finish-call-return','lookup-call-return'):
            require(f['result'] is not None,'method result missing')
            result=f['result'];require(set(result)=={'receipt','stop_observed','artifact_absent'},'result schema')
            require(not self.s['lockHeld'], 'outer method return before maintenance scope ended')
            if a=='finish-call-return' and f['error']:
                self.emit('FinishReplyFailure',returnedUnknown=True)
                if self.s['phase']=='removed':self.lost_error=True
            if not f['error']:
                require(a!='finish-call-return' or not self.native_error, 'native maintenance error lost at outer return')
                receipt(result['receipt'],b)
                require(result['receipt']==f['stored'] and result['stop_observed'] is True and result['artifact_absent'] is True and self.s['phase']=='removed' and self.s['absenceObserved'],'positive return without original current receipt/absence')
                if a=='lookup-call-return':
                    require(self.s['readRight'] and self.s['identityExact'], 'lookup acquired knowledge without original read permission')
                    if self.s['returnedUnknown']:self.lookup_after_error=True
                    self.emit('OriginalLookupKnown',returnedUnknown=False)
        if f['source'] is not None:
            src=f['source'];require(not f['source_error'] and f['attempt_source']==src,'source CAS/readback mismatch')
            require(set(src)==set('key kind phase revision approved review_hash request_hash worker result_phase stop_observed artifact_absent'.split()) and src['key']==b['key'] and src['kind']=='uninstall' and src['approved'] is True and src['review_hash']==b['review_sha256'] and hexid(src['request_hash'],64) and type(src['worker']) is str,'source original review/request differs')
            require(integer(src['revision']) and src['revision']>0 and type(src['stop_observed']) is type(src['artifact_absent']) is bool, 'source scalar alias')
            if self.source_hash is None:self.source_hash=src['request_hash']
            require(src['request_hash']==self.source_hash, 'immutable original source request digest changed')
            if self.last_source is None:
                require(a=='source-initial' and src['phase']=='incomplete' and src['worker'] and src['result_phase']=='unknown','initial original source owner missing')
                self.source_base=src['revision']
            elif a=='source-owner-returned':
                require(self.last_source['worker'] and not src['worker'] and src['revision']==self.last_source['revision']+1 and src['phase']=='incomplete','creator join/settlement invented or stale')
                self.emit('SourceJoinObserved',sourceJoined=True,sourceRevision=self.s['sourceRevision']+1)
            elif a=='source-cas-return':
                require(src['revision']==self.last_source['revision']+1 and self.s['sourceJoined'] and self.s['phase']=='removed' and not self.s['returnedUnknown'] and src['phase']=='completed' and src['result_phase']=='removed' and src['stop_observed'] is True and src['artifact_absent'] is True,'source completion lacks original joined positive native result')
                self.emit('SourceCAS',sourcePhase='completed',sourceRevision=self.s['sourceRevision']+1)
            else:require(src==self.last_source,'unexplained source mutation/completion regression')
            self.last_source=copy.deepcopy(src)
            require(src['revision']-self.source_base+1==self.s['sourceRevision'],'source revision alias')
        if f['stored'] is not None:
            require(f['stored']['phase']==self.s['phase'] and recovery==self.s['recovery'],'unexplained durable receipt/identity transition')
        self.emit('LookupOrRefusal')
    def finish(self,scenario):
        require(scenario in SCENARIOS and self.seen[-1]=='driver-final' and not self.s['copyActive'] and not self.s['lockHeld'] and len(self.states)<=128,'incomplete final/authority suffix')
        if scenario in ('removed-source-cas','lost-finish-ack'):
            require(self.s['phase']=='removed' and self.s['sourcePhase']=='completed' and self.s['unlinkEffects']==1 and self.s['sourceJoined'] and 'source-monotone-observed' in self.seen,'complete source/native suffix absent')
        elif scenario=='copy-unknown-new-key':require(self.s['phase']=='copy-unknown' and self.s['copyStarted'] and not self.s['recovery'] and self.s['unlinkEffects']==0 and 'other-key-call-return' in self.seen,'unknown duty/new-key control absent')
        else:require(self.s['unlinkEffects']==0 and self.s['artifactPresent'] and self.s['phase'] in ('stop-admitted','stop-unknown'),'refusal changed original artifact')
        if scenario=='lost-finish-ack':require(self.lost_error and self.lookup_after_error and not self.s['returnedUnknown'], 'actual unknown-to-known original lookup suffix absent')
        if scenario=='grant-read-after-revoke':require('auth-read-return' in self.seen and not self.s['mutationRight'] and self.s['readRight'],'read grant regained deletion authority')
        if scenario=='replaced-name-refusal':require(not self.s['ownershipFresh'],'changed name was not refused')
        return {'scenario':scenario,'states':self.states,'actions':self.actions,'raw_rows':len(self.seen)}


def project(rows,scenario):
    require(type(rows) is list and 0<len(rows)<=128 and len(json.dumps(rows).encode())<=4<<20,'bounded raw history required')
    p=Projector()
    for row in rows:p.consume(row)
    return p.finish(scenario)
