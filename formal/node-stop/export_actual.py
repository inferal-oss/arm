#!/usr/bin/env python3
"""Project actual Go raw facts, then export all 24 original NodeStop variables.

No model successor is evaluated here. Complete raw SQL rows, native marker
facts and retained clock observations remain in the input and each emitted cut
names its exact sequence. TLC separately decides whether every complete pair
is in unchanged NodeStop.Next and reaches the final captured row. This module
starts no process and accepts no legacy/partial/duplicate-key documents.
"""
import argparse
import copy
import hashlib
import json
import re
from pathlib import Path

TTL = 120_000_000_000
VARIABLES = ('generation running desiredRunning desiredRevision grantAllowed '
             'grantRevision prepared approved records ownedWork unknownWork '
             'effects effectGeneration joined observed restartReview '
             'clockHealthy permitIssued permitFresh restartFresh '
             'restartApproved restartID restartHealthy selectedKey').split()
KEYS = ('first', 'second')
SCENARIOS = ('two-generation', 'two-generation-no-source-observation', 'force-owned',
             'wait-owned', 'active-default', 'unknown-work', 'expired-permit',
             'clock-fault', 'revoked-grant', 'changed-grant', 'changed-generation',
             'interrupted-history', 'restart-expired', 'restart-clock-fault')
REFUSALS = {'active-default': 'active_leases', 'unknown-work': 'node_stop_blocked',
            'expired-permit': 'node_stop_review_expired', 'clock-fault': 'node_stop_review_expired',
            'revoked-grant': 'forbidden', 'changed-grant': 'node_context_changed',
            'changed-generation': 'node_generation_changed'}

STAGES = ['http', 'endpoint', 'monitor', 'lifecycle', 'operator', 'workspace', 'mesh', 'manager']
NO_PREPARED = dict(present=False, key='none', gen=0, grant=0, desired=0,
                   policy='default', owned=False, unknown=False, review='none')
NO_RECORD = dict(present=False, gen=0, admittedGeneration=0, phase='none',
                 policy='default', admittedWithGrant=False, review='none')


def unique(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate JSON key: ' + key)
        result[key] = value
    return result


def require(value, message):
    if not value:
        raise ValueError(message)


def policy(plan):
    p = plan['policy']
    require(type(p['force']) is bool and type(p['wait']) is bool and not (p['force'] and p['wait']), 'unsupported policy')
    require(not p.get('timeout_ns', 0), 'timeout policy needs a separate larger model')
    return 'force' if p['force'] else 'wait' if p['wait'] else 'default'


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False)


def go_digest(value):
    raw = json.dumps(value, separators=(',', ':'), ensure_ascii=False)
    for char, escaped in (('<', r'\u003c'), ('>', r'\u003e'), ('&', r'\u0026'), ('\u2028', r'\u2028'), ('\u2029', r'\u2029')):
        raw = raw.replace(char, escaped)
    return hashlib.sha256(raw.encode()).hexdigest()


def sql_json(raw):
    require(type(raw) is str and len(raw.encode()) <= 64 << 10, 'bounded literal SQL text required')
    return json.loads(raw, object_pairs_hook=unique)


def peer_tuple(peer):
    identity = peer['identity']
    return (identity['host'], identity['fingerprint'], identity['spki_sha256'], peer['pair'], peer['revision'])


def actor_tuple(actor):
    return (actor['source_host'], actor['certificate_sha256'], actor['spki_sha256'], actor['pair'], actor['grant_revision'])


def valid_ledger(ledger):
    require(set(ledger) == {'state', 'records', 'rows', 'error'} and not ledger['error'], 'incomplete ledger cut')
    records, rows, state = ledger['records'], ledger['rows'], ledger['state']
    require(type(records) is list and len(records) <= 2 and type(rows) is dict, 'bounded exact ledger required')
    if state is None:
        require(not records and not rows, 'absent schema cannot contain records')
        return
    require('state' in rows and sql_json(rows['state']) == state, 'decoded state differs from raw SQL row')
    require(set(rows) == {'state'} | {'record:' + r['plan']['key'] for r in records}, 'raw SQL rows incomplete or foreign')
    require(len({r['plan']['key'] for r in records}) == len(records), 'duplicate durable key')
    for r in records:
        require(set(r) == {'schema', 'plan', 'review_sha256', 'shutdown_id', 'desired_state_revision', 'phase', 'created_at', 'updated_at'}, 'closed durable operation required')
        key = r['plan']['key']
        require(key in KEYS and sql_json(rows['record:' + key]) == r, 'raw operation row mismatch')
        require(go_digest(r['plan']) == r['review_sha256'], 'durable review digest differs from literal actual plan')
        require(r['schema'] == 'arm.node-stop.record/v1' and r['phase'] in ('accepted', 'drain-ready', 'interrupted'), 'unknown literal durable phase')
    require(set(state) == {'schema', 'revision', 'desired_running', 'target', 'stop_key', 'stop_sha256'}, 'closed durable state required')
    selected = [r for r in records if r['plan']['key'] == state['stop_key']]
    require(len(selected) == 1 and selected[0]['review_sha256'] == state['stop_sha256'] and selected[0]['plan']['target'] == state['target'], 'state does not bind exact selected operation')


class Projector:
    def __init__(self):
        # Retained actual observations, not a shadow copy of model successors.
        self.ledger = dict(state=None, records=[], rows={}, error='')
        self.leases = []
        self.authorities = []
        self.active = False
        self.peer = None
        self.local = None
        self.grants = []
        self.target = None
        self.pending = None
        self.reviewed = set()
        self.permits = {}
        self.clock = dict(last=0, have=False, failure='', issued={})
        self.permit_first = {}
        self.closures = {}
        self.teardown = set()
        self.markers = {}
        self.inspections = set()
        self.restart = None
        self.restart_objects = []
        self.restart_consent = set()
        self.begun = False
        self.cuts = []
        self.history = {}
        self.facts = []
        self.current_sequence = 0

    def generation(self, raw):
        require(raw in self.authorities, 'unpublished target generation')
        return self.authorities.index(raw) + 1

    def review_id(self, digest):
        require(digest in self.permits, 'record has no actual issued exact review')
        return 'review' + str(list(self.permits).index(digest) + 1)

    def accept_ledger(self, ledger):
        valid_ledger(ledger)
        for record in ledger['records']:
            key = record['plan']['key']
            immutable = {k: v for k, v in record.items() if k not in ('phase', 'updated_at')}
            if key in self.history:
                require(self.history[key] == immutable, 'durable original operation was replaced')
            self.history[key] = immutable
            self.generation(record['plan']['target']['backend_generation'])
        self.ledger = copy.deepcopy(ledger)

    def accept_clock(self, clock):
        require(set(clock) == {'last', 'have', 'failure', 'issued'}, 'complete retained clock required')
        require(type(clock['last']) is int and type(clock['have']) is bool and type(clock['failure']) is str and type(clock['issued']) is dict, 'clock field types')
        require(all(type(v) is int and 0 <= v <= clock['last'] for v in clock['issued'].values()), 'permit first tick after retained last tick')
        # A failed original owner cannot recover through a later observation.
        require(not self.clock['failure'] or clock['failure'], 'sticky retained clock failure revived')
        require(not self.clock['have'] or not clock['have'] or clock['last'] >= self.clock['last'], 'retained clock observation went backwards')
        for digest, first in clock['issued'].items():
            token = (self.authorities[-1], digest)
            require(token not in self.permit_first or self.permit_first[token] == first, 'exact permit original issue tick was refreshed')
            self.permit_first[token] = first
        self.clock = copy.deepcopy(clock)

    def accept_peer(self, peer):
        require(peer['state'] in ('pending', 'active', 'revoked'), 'unknown grant state')
        allowed = peer['state'] == 'active' and peer['proven'] is True and 'node-stop' in peer['grant']['permissions']
        if allowed and peer_tuple(peer) not in self.grants:
            self.grants.append(peer_tuple(peer))
            require(len(self.grants) <= 2, 'third enrollment outside bounded model')
        self.peer = copy.deepcopy(peer)

    def work(self):
        outstanding = [l for l in self.leases if l['state'] != 'terminal']
        require(len(outstanding) <= 1, 'more than one distinct workload outside bounded abstraction')
        require(all(l['state'] in ('queued', 'active', 'provisioning', 'ending', 'needs_attention') for l in outstanding), 'unknown lease state')
        unknown = any(l['state'] == 'needs_attention' for l in outstanding)
        return bool(outstanding) and not unknown, unknown

    def snapshot(self):
        require(self.authorities and self.peer is not None and self.grants, 'missing actual baseline authority/grant')
        state = self.ledger['state']
        gen = len(self.authorities)
        require(gen <= 2, 'third Serve generation outside bounded model')
        raw_current = self.authorities[-1]
        records = {k: copy.deepcopy(NO_RECORD) for k in KEYS}
        for record in self.ledger['records']:
            plan = record['plan']
            key = plan['key']
            require(record['review_sha256'] in self.permits, 'unissued durable review')
            require(actor_tuple(plan['actor']) in self.grants, 'admission not bound to an actually observed granted enrollment')
            admitted = self.generation(plan['target']['backend_generation'])
            records[key] = dict(present=True, gen=admitted, admittedGeneration=admitted,
                                phase={'accepted': 'admitted', 'drain-ready': 'ready', 'interrupted': 'interrupted'}[record['phase']],
                                policy=policy(plan), admittedWithGrant=True,
                                review=self.review_id(record['review_sha256']))
        pending = self.pending
        # Durable presence consumes the prepared operation; closed/native facts
        # do not rewrite the durable row or pretend a prepared operation applied.
        if pending and any(r['plan']['key'] == pending['key'] for r in self.ledger['records']):
            pending = None
        prepared = copy.deepcopy(NO_PREPARED)
        if pending:
            digest = next((d for d, p in self.permits.items() if p == pending), None)
            require(digest is not None, 'pending review lacks raw successful Prepare')
            impact = pending['impact']
            unknown = any(not b['id'].startswith(('stop:', 'join:')) for b in impact['blockers'])
            owned = any(o['state'] != 'needs_attention' for o in impact['obligations'])
            actor = actor_tuple(pending['actor'])
            require(actor in self.grants, 'prepared actor was never actually granted')
            prepared = dict(present=True, key=pending['key'], gen=self.generation(pending['target']['backend_generation']),
                            grant=self.grants.index(actor) + 1, desired=pending['desired_state_revision'],
                            policy=policy(pending), owned=owned, unknown=unknown,
                            review=self.review_id(digest))
        issued = [self.review_id(d) for d in self.permits]
        fresh = [self.review_id(d) for d, first in self.clock['issued'].items()
                 if d in self.permits and self.permits[d]['target']['backend_generation'] == raw_current
                 and not self.clock['failure'] and self.clock['last'] - first < TTL]
        effects = {k: int(k in self.closures) for k in KEYS}
        effect_gen = {k: self.generation(self.closures[k]['record']['plan']['target']['backend_generation']) if k in self.closures else 0 for k in KEYS}
        owned, unknown = self.work()
        allowed = self.peer['state'] == 'active' and self.peer['proven'] is True and 'node-stop' in self.peer['grant']['permissions']
        # Revocation is a state change, not a newly active grant epoch. Actual
        # numeric revisions and pair tokens remain unmodified in raw provenance.
        grant_rev = self.grants.index(peer_tuple(self.peer)) + 1 if allowed else len(self.grants)
        restart_review, restart_fresh, restart_approved, restart_healthy = 0, False, False, True
        if self.restart:
            r = self.restart
            consumed = state is not None and state['revision'] > r['expected']['revision']
            failure = r['elapsed']['failure']
            restart_healthy = not failure or 'expired in elapsed time' in failure
            if not consumed:
                restart_review = r['expected']['revision']
                restart_fresh = not r['attempted'] and not r['closed'] and not failure and r['elapsed']['last'] - r['elapsed']['first'] < TTL
                restart_approved = canonical(r['review']) in self.restart_consent
        result = dict(generation=gen, running=self.active,
                      desiredRunning=state['desired_running'] if state else True,
                      desiredRevision=state['revision'] if state else 1,
                      grantAllowed=allowed, grantRevision=grant_rev,
                      prepared=prepared, approved=prepared['present'] and prepared['review'] in [self.review_id(d) for d in self.reviewed],
                      records=records, ownedWork=owned, unknownWork=unknown,
                      effects=effects, effectGeneration=effect_gen,
                      joined=sorted(set(effect_gen.values()) - {0}), observed=sorted(self.inspections),
                      restartReview=restart_review, clockHealthy=not bool(self.clock['failure']),
                      permitIssued=issued, permitFresh=fresh,
                      restartFresh=restart_fresh, restartApproved=restart_approved,
                      restartID=len(self.restart_objects), restartHealthy=restart_healthy,
                      selectedKey=state['stop_key'] if state else 'none')
        require(set(result) == set(VARIABLES), 'projection omitted model fields')
        return result

    def emit(self, fact):
        if not self.begun:
            return
        state = self.snapshot()
        if not self.cuts or self.cuts[-1]['state'] != state:
            self.cuts.append(dict(sequence=fact['sequence'], kind=fact['kind'], state=state))

    def consume(self, fact):
        require(set(fact) == {'sequence', 'kind', 'home', 'data'}, 'closed raw fact schema required')
        require(type(fact['sequence']) is int and fact['sequence'] == self.current_sequence + 1, 'missing/reordered raw fact')
        self.current_sequence = fact['sequence']
        self.facts.append(copy.deepcopy(fact))
        kind, data = fact['kind'], fact['data']
        if kind == 'serve.published':
            generation = data['generation']
            require(re.fullmatch('[0-9a-f]{32}', generation) and generation not in self.authorities, 'reused/malformed actual Serve generation')
            self.authorities.append(generation)
            self.active = True
            # NewBoundary has an empty permit owner; retained audit issuance is
            # separate from this newly published owner's native-clock lifetime.
            self.clock = dict(last=0, have=False, failure='', issued={})
        elif kind.startswith('manager.'):
            require(not data['error'], 'manager snapshot failed')
            self.accept_ledger(data['ledger'])
            self.leases = data['leases'] or []
            self.accept_clock(data['clock'])
        elif kind == 'grant.guard-refused':
            require(data['api'] == 'Store.WithPeerSnapshot' and data['code'] == 'forbidden' and data['peer'] is not None and data['source'] == data['peer']['identity']['host'], 'actual refusing peer snapshot required')
            self.accept_peer(data['peer'])
        elif kind in ('grant.observed', 'grant.authenticated'):
            self.accept_peer(data['peer'] if kind == 'grant.authenticated' else data)
            if kind == 'grant.authenticated':
                self.local = copy.deepcopy(data['local'])
        elif kind.startswith('boundary.'):
            require(data['target']['backend_generation'] == self.authorities[-1] and data['target']['home'] == fact['home'], 'altered current boundary generation/home')
            if self.target is None:
                self.target = copy.deepcopy(data['target'])
            else:
                require({k: v for k, v in self.target.items() if k != 'backend_generation'} == {k: v for k, v in data['target'].items() if k != 'backend_generation'}, 'original target identity changed')
            require(self.local is not None and (data['target']['host'], data['target']['owner'], data['target']['certificate_sha256'], data['target']['spki_sha256']) == (self.local['host'], self.local['owner'], self.local['fingerprint'], self.local['spki_sha256']), 'boundary differs from authenticated destination identity')
            self.accept_clock(data['clock'])
            if kind == 'boundary.prepare' and not data['error']:
                plan = data['plan']
                require(plan['target'] == data['target'] and plan['key'] in KEYS and plan['impact']['complete'], 'successful Prepare missing exact actual target/census')
                issued = [d for d in data['clock']['issued'] if d not in self.permits]
                require(len(issued) == 1 and len(self.permits) < 2 and go_digest(plan) == issued[0], 'successful Prepare must retain one new exact issued digest')
                self.permits[issued[0]] = copy.deepcopy(plan)
                self.pending = copy.deepcopy(plan)
        elif kind == 'operator.reviewed':
            require(data['digest'] in self.permits and self.permits[data['digest']] == data['plan'], 'consent not bound to actual issued review')
            self.reviewed.add(data['digest'])
        elif kind == 'serve.teardown':
            require(data['generation'] == self.authorities[-1] and not data['error'], 'failed/unmatched real Serve teardown')
            self.teardown.add(data['generation'])
        elif kind == 'closure.write':
            if data['error']:
                require(data.get('closed') is None, 'failed closure has success payload')
            else:
                closed, record = data['closed'], data['record']
                generation = record['plan']['target']['backend_generation']
                require(generation in self.teardown and data['stage_count'] == 8 and closed['joined_stages'] == STAGES and closed['record'] == record, 'missing actual Serve component closure')
                key = record['plan']['key']
                current = [r for r in self.ledger['records'] if r['plan']['key'] == key]
                require(current == [record] and record['phase'] == 'drain-ready' and key not in self.closures, 'closed effect differs from exact durable ready record')
                self.closures[key] = copy.deepcopy(closed)
                self.active = False
        elif kind == 'serve.return':
            require(data['generation'] == self.authorities[-1], 'foreign Serve owner completion')
            self.active = False
        elif kind == 'marker.matched':
            record = data['record']
            key = record['plan']['key']
            require(data['closed']['record'] == record and data['closed']['daemon_lock'] == data['lock'] and re.fullmatch('[0-9a-f]{64}', data['sha256']), 'native marker does not bind original lock/row')
            require(key in self.closures and self.closures[key] == data['closed'], 'matched native marker has no actual joined closure')
            self.markers[key] = copy.deepcopy(data)
        elif kind == 'native.observed':
            self.accept_ledger(data['ledger'])
            observation = data['observation']
            record = observation['record']
            key = record['plan']['key']
            require(observation['server_joined'] is True and observation['original_generation_absent'] is True and observation['desired_state'] == self.ledger['state'], 'incomplete native inspection')
            require(key in self.markers and self.markers[key]['record'] == record and key in self.closures, 'positive observation requires real matched original marker and closure')
            self.inspections.add(self.generation(record['plan']['target']['backend_generation']))
        elif kind.startswith('restart.') and kind != 'restart.cas':
            require(set(data) == {'expected', 'review', 'elapsed', 'attempted', 'closed', 'error', 'failure_kind', 'failure_matches_return'}, 'complete restart object snapshot required')
            require(data['review']['stopped_intent'] == data['expected'] and data['review']['stopped_intent_sha256'] == go_digest(data['expected']), 'restart owner does not bind actual stopped state digest')
            identity = canonical(data['review'])
            if kind == 'restart.prepared':
                require(identity not in self.restart_objects and len(self.restart_objects) < 2, 'reused/extra restart owner')
                self.restart_objects.append(identity)
            else:
                require(identity in self.restart_objects, 'restart Plan/Apply lacks retained object preparation')
            self.restart = copy.deepcopy(data)
        elif kind == 'operator.restart-reviewed':
            require(canonical(data) in self.restart_objects, 'restart consent has no actual retained review')
            self.restart_consent.add(canonical(data))
        elif kind == 'restart.cas':
            require(self.restart is not None, 'restart CAS lacks retained owner')
            self.restart['elapsed'] = copy.deepcopy(data['elapsed'])
            self.accept_ledger(data['ledger'])
            require(data['state'] == self.ledger['state'] and data['state']['desired_running'], 'CAS result not actual running-intent SQL state')
        elif kind == 'driver.begin':
            require(not self.begun and len(self.authorities) == 1 and self.peer is not None, 'baseline missing/duplicated')
            self.begun = True
        elif kind == 'startup.stopped-refused':
            require(data == self.ledger['state'] and data['desired_running'] is False, 'startup refusal differs from actual stopped ledger')
        elif kind in ('driver.outcome', 'driver.end', 'input.clock-control', 'worker.paused'):
            pass
        else:
            raise ValueError('unknown raw cut: ' + kind)
        self.emit(fact)


def project(document):
    require(set(document) == {'schema', 'scenario', 'facts'} and document['schema'] == 'arm.node-stop.actual-go/v1', 'closed actual-Go trace schema required')
    facts = document['facts']
    require(type(facts) is list and 3 <= len(facts) <= 2048 and facts[-1]['kind'] == 'driver.end', 'complete bounded raw prefix ending required')
    require(len({f['home'] for f in facts}) == 1, 'mixed authority homes')
    require(document['scenario'] in SCENARIOS, 'unknown named scenario')
    p = Projector()
    for fact in facts:
        p.consume(fact)
    require(p.begun and len(p.cuts) >= 3, 'missing actual complete initial/changed/final cuts')
    coverage = scenario_coverage(document, p.cuts)
    return dict(schema='arm.node-stop.projection/v1', scenario=document['scenario'], coverage=coverage,
                variables=VARIABLES, cuts=p.cuts,
                provenance=dict(raw_sha256=hashlib.sha256(canonical(document).encode()).hexdigest(),
                                raw_sequences=len(facts), raw_final_sequence=facts[-1]['sequence']))



def scenario_coverage(document, cuts):
    name, facts = document['scenario'], document['facts']
    def events(kind):
        return [f for f in facts if f['kind'] == kind]
    def boundary(kind, code=None):
        rows = events('boundary.' + kind)
        return [f for f in rows if f['data']['code'] == code] if code is not None else rows
    def outcome(operation, code=None):
        rows = [f for f in events('driver.outcome') if f['data']['operation'] == operation]
        require(len(rows) == 1, 'exact named API outcome required: ' + operation)
        value = rows[0]['data']
        require(set(value) == {'operation', 'error', 'code', 'error_type'}, 'complete typed actual API outcome required')
        if code is not None:
            require(value['code'] == code and value['error'] and value['error_type'] == '*model.Error', 'wrong intended typed refusal: ' + operation)
        return rows[0]
    prepared = [f for f in boundary('prepare', '') if f['data']['plan']['key'] == 'first']
    require(len(prepared) == 1, 'one actual successful first Prepare required')
    first_plan = prepared[0]['data']['plan']
    outcome('prepare')
    last = cuts[-1]['state']
    if name in REFUSALS:
        wanted = REFUSALS[name]
        result = outcome('apply', wanted)
        require(all(not r['present'] for r in last['records'].values()), 'refusal admitted an operation')
        if name == 'revoked-grant':
            outcome('revoked-wire-attempt', 'remote_outcome_unknown')
            guard = events('grant.guard-refused')
            require(len(guard) == 1 and guard[0]['data']['peer']['state'] == 'revoked' and guard[0]['data']['source'] == first_plan['actor']['source_host'] and guard[0]['sequence'] < result['sequence'], 'missing actual destination-local revoked grant guard')
            require(not boundary('admit'), 'revoked grant must refuse before Boundary admission')
        else:
            refused = boundary('admit', wanted)
            require(refused and refused[0]['sequence'] < result['sequence'], 'missing actual intended Boundary refusal')
            proof = refused[0]['data']
            if name == 'active-default':
                require(first_plan['impact']['outstanding'] == 1 and policy(first_plan) == 'default' and last['ownedWork'], 'active default refusal lacks real owned census')
            elif name == 'unknown-work':
                lost = [f for f in events('manager.end-return') if any(l['state'] == 'needs_attention' and any(p['state'] == 'unresolved' for p in l.get('ports', [])) for l in f['data']['leases'])]
                require(lost and last['unknownWork'] and not last['ownedWork'], 'unknown-work refusal lacks actual unresolved cooperative port')
            elif name == 'expired-permit':
                clock = proof['clock']
                require(not clock['failure'] and any(clock['last'] - tick >= TTL for tick in clock['issued'].values()) and not last['permitFresh'], 'expiry refusal lacks actual retained elapsed expiry')
                repeat = boundary('prepare', wanted)
                require(len(repeat) == 1 and repeat[0]['data']['plan'] == first_plan and repeat[0]['sequence'] > result['sequence'], 'same exact expired Prepare was not attempted/refused')
                outcome('repeat-exact-expired-prepare', wanted)
            elif name == 'clock-fault':
                require(proof['clock']['failure'] and not last['clockHealthy'] and not last['permitFresh'], 'clock refusal lacks actual sticky owner failure')
                retry = outcome('revive-clock', wanted)
                require(len(refused) == 2 and refused[-1]['sequence'] < retry['sequence'] and refused[-1]['data']['clock']['failure'] and refused[-1]['data']['clock']['issued'] == proof['clock']['issued'], 'failed original owner revival path missing')
            elif name == 'changed-grant':
                auth = [f for f in events('grant.authenticated') if f['sequence'] < refused[0]['sequence']]
                require(auth and actor_tuple(proof['plan']['actor']) != peer_tuple(auth[-1]['data']['peer']) and last['grantRevision'] == 2, 'context refusal lacks actual replacement granted enrollment')
            elif name == 'changed-generation':
                require(proof['plan']['target']['backend_generation'] != proof['target']['backend_generation'], 'generation refusal lacks actual mismatching requested target')
    else:
        require(not outcome('apply')['data']['error'] and last['records']['first']['present'], 'positive path lacks successful actual admission')
        if name == 'interrupted-history':
            require(events('worker.paused') and events('restart.cas') and last['records']['first']['phase'] == 'interrupted' and not last['effects']['first'] and last['generation'] == 2, 'interrupted history lacks real accepted closure gap/CAS/startup')
            prepare_later = outcome('prepare-after-interrupted')
            require(not prepare_later['data']['error'], 'later Prepare did not reach actual history census')
            outcome('apply-after-interrupted', 'node_stop_blocked')
            refused = [f for f in boundary('admit', 'node_stop_blocked') if f['data']['plan']['key'] == 'second']
            require(len(refused) == 1 and any(b['id'] == 'stop:first' for b in refused[0]['data']['plan']['impact']['blockers']), 'prior-duty refusal lacks actual retained predecessor blocker')
        else:
            require(events('closure.write') and events('startup.stopped-refused') and outcome('ordinary-start-stopped-refused')['data']['error'], 'positive stop lacks joined closure and exact startup gate refusal')
            if name.startswith('two-generation'):
                require(last['generation'] == 2 and last['records']['second']['present'] and last['effects'] == {'first': 1, 'second': 1} and events('restart.cas'), 'two real closed generations and separate reviewed CAS required')
                first_generation = first_plan['target']['backend_generation']
                observed_first = [f for f in events('native.observed') if f['data']['observation']['record']['plan']['target']['backend_generation'] == first_generation]
                require(bool(observed_first) == (name == 'two-generation'), 'source observation/no-observation coverage differs')
            elif name in ('force-owned', 'wait-owned'):
                require(policy(first_plan) == ('force' if name == 'force-owned' else 'wait'), 'wrong owned-work policy')
                acquired = [c for c in cuts if c['kind'] == 'manager.acquire-return' and c['state']['ownedWork']]
                finished = [c for c in cuts if c['kind'] == 'manager.end-return' and not c['state']['ownedWork']]
                ready = [c for c in cuts if c['kind'] == 'manager.ready-commit']
                require(acquired and finished and ready and acquired[0]['sequence'] < finished[0]['sequence'] < ready[0]['sequence'], 'real acquired/released/ready microcuts missing')
            elif name.startswith('restart-'):
                failures = [f for f in facts if f['kind'].startswith('restart.') and f['kind'] != 'restart.cas' and f['data']['error']]
                wanted = 'retained-review-expired' if name == 'restart-expired' else 'retained-clock-fault'
                require(len(failures) == 1 and failures[0]['kind'] == 'restart.apply', 'first real failure must be observed/persisted by Apply')
                value = failures[0]['data']
                require(value['failure_matches_return'] is True and value['failure_kind'] == wanted and not value['attempted'] and not events('restart.cas'), 'restart refusal does not match exact original retained owner/error identity')
                ticks = value['elapsed']
                require(ticks['failure'] and ((ticks['last'] - ticks['first'] >= TTL) == (name == 'restart-expired')), 'retained first/last ticks do not establish intended refusal')
                require(outcome('restart-refused')['data']['error'] and not last['desiredRunning'], 'restart failure unexpectedly changed intent')
    return dict(name=name, actual_path_facts=True, refusal_code=REFUSALS.get(name, ''), full24=True)

def tla(value, function=False):
    if type(value) is bool:
        return 'TRUE' if value else 'FALSE'
    if type(value) is int:
        return str(value)
    if type(value) is str:
        return json.dumps(value)
    if type(value) is list:
        return '{' + ','.join(tla(v) for v in value) + '}'
    require(type(value) is dict, 'unsupported TLA value')
    if function:
        return '[k \\in Keys |-> CASE ' + ' [] '.join('k=' + json.dumps(k) + ' -> ' + tla(v) for k, v in value.items()) + ']'
    return '[' + ','.join(k + ' |-> ' + tla(v) for k, v in value.items()) + ']'


def export(document, directory):
    return export_projection(project(document), directory)


def export_projection(projected, directory):
    require(projected['variables'] == VARIABLES and all(set(c['state']) == set(VARIABLES) for c in projected['cuts']), 'complete 24-variable projection required')
    directory = Path(directory)
    directory.mkdir(mode=0o700)
    tuples = []
    for cut in projected['cuts']:
        row = cut['state']
        tuples.append('<<' + ','.join(tla(row[k], k in ('records', 'effects', 'effectGeneration')) for k in VARIABLES) + '>>')
    module = r'''---- MODULE ActualNodeStop ----
EXTENDS NodeStop, Sequences
VARIABLE position
ActualRows == <<''' + ',\n'.join(tuples) + r'''>>
ActualInit == Init /\ position=1 /\ vars=ActualRows[1]
ActualNext == /\ position < Len(ActualRows)
              /\ Next /\ position'=position+1
              /\ vars'=ActualRows[position']
ActualSpec == ActualInit /\ [][ActualNext]_<<vars,position>>
FinalSeen == position=Len(ActualRows)
NeverFinal == ~FinalSeen
====
'''
    (directory / 'ActualNodeStop.tla').write_text(module)
    # Two separately run configurations: safety/conformance must succeed;
    # final witness must fail only NeverFinal at the exact last captured row.
    cfg = 'SPECIFICATION ActualSpec\nCHECK_DEADLOCK FALSE\nINVARIANT TypeOK\nINVARIANT StoppedNeedsPositiveObservation\nINVARIANT ClosedEffectIsGenerationBound\nPROPERTY RestartLatchSafety\nPROPERTY ContinuousReviewSafety\nPROPERTY ExactReviewExpirySafety\nPROPERTY HistoryFenceSafety\n'
    (directory / 'Safety.cfg').write_text(cfg)
    (directory / 'Witness.cfg').write_text(cfg + 'INVARIANT NeverFinal\n')
    (directory / 'projection.json').write_text(json.dumps(projected, indent=2) + '\n')
    return projected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('raw', type=Path)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    require(args.raw.stat().st_size <= 8 << 20, 'raw capture exceeds bound')
    document = json.loads(args.raw.read_text(), object_pairs_hook=unique)
    result = export(document, args.destination)
    print(json.dumps(dict(scenario=result['scenario'], cuts=len(result['cuts']), variables=len(VARIABLES), verdict='exported; separate TLC safety and final-row witness required')))


if __name__ == '__main__':
    main()
