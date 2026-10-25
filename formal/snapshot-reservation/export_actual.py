"""Strict, bounded actual-cut relation for the PRIVATE one-key no-work fixture.

Never reads a clock or database. All time comparisons use captured native Go
samples with integer nanoseconds. A SQL transaction view is staged evidence.
Only the actual Commit result plus a complete committed read advances durable
state. This module supplies data to the existing original-owner formal gate.
"""
import base64
import calendar
import copy
import datetime
import hashlib
import json
import re

SCENARIOS = ('reserve-cancel', 'pre-cancel', 'lost-admit-ack', 'lost-cancel-ack',
             'close-reopen', 'admission-rollback', 'deadline-before-check',
             'deadline-after-check', 'commit-context-cancel')
VARIABLES = ('phase', 'fence', 'request', 'staged', 'pending', 'checked',
             'cachePending', 'work', 'destination', 'pinned', 'owner', 'unknown',
             'deadlineOK', 'nativeStarted')
ROW_KEYS = set('observed ordinal home host owner runtime sql_view cut physical schema action id transaction copies parts journals leases fences controls commit_pending boundary_unknown error'.split())
CUT_KEYS = set('operation deadline api_error report_error request key revision reason record present'.split())
ACTIONS = set('publication-read-refused initial preflight-registered preflight-owner-return admission-writes-staged pre-cancel-fence-staged cancellation-writes-staged before-admit-commit before-cancel-commit before-pre-cancel-commit admission-check-accepted admission-check-refused commit-context-refused commit-call-entry commit-call-return admit-commit-return admit-commit-unknown cancel-commit-return cancel-commit-unknown pre-cancel-commit-return pre-cancel-commit-unknown admission-published cancellation-published rollback-call-return sql-owner-loaded sql-close-return reserve-call-return cancel-call-return lookup-call-return original-key-lookup-observed exact-key-retry-observed key-conflict-observed before-actual-manager-close actual-manager-reopened cancel-return-observed cancelled-key-retry-observed actual-admission-rollback-return cancelled-request-return expired-check-return delayed-commit-settled cancelled-commit-return'.split())
RECORD_KEYS = set('schema id authority owner runtime key request_sha256 request_bytes request_parts source_storage source_revision source_identity provider_identity work_lease destination_lease created updated work_expires destination_expires revision phase planning_attempted allocation_attempted final_slots decision'.split())


def require(value, message):
    if not value:
        raise ValueError(message)


def unique(pairs):
    out = {}
    for key, value in pairs:
        require(key not in out, 'duplicate JSON key')
        out[key] = value
    return out


def parse(raw):
    return json.loads(raw, object_pairs_hook=unique,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite JSON')))


def go_json(value):
    raw = json.dumps(value, ensure_ascii=False, separators=(',', ':'), allow_nan=False)
    return raw.replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e').replace('\u2028', '\\u2028').replace('\u2029', '\\u2029').encode()


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def stamp(value):
    match = re.fullmatch(r'(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?Z', value or '')
    require(match is not None, 'exact UTC native timestamp required')
    whole = datetime.datetime.strptime(match[1], '%Y-%m-%dT%H:%M:%S')
    return calendar.timegm(whole.timetuple()) * 10**9 + int((match[2] or '').ljust(9, '0'))


def role(host, key, name):
    return digest(('arm.snapshot-reservation/v1\0' + host + '\0' + name + '\0' + key).encode())


def integer(value):
    return type(value) is int


def cells(row, types):
    require(len(row) == len(types), 'physical SQL column roster')
    out = []
    for cell, typ in zip(row, types):
        require(set(cell) == {'type', 'value'} and cell['type'] in typ.split('|'), 'physical SQL type alias')
        value = cell['value']
        require((cell['type'] == 'null' and value is None) or
                (cell['type'] in ('text', 'blob') and type(value) is str) or
                (cell['type'] == 'integer' and integer(value)), 'physical SQL scalar differs')
        if cell['type'] == 'blob':
            value = base64.b64decode(value, validate=True)
        out.append(value)
    return out


def sql_view(row):
    """Validate physical/body/source/promise/key/budget/journal relationships."""
    tables = {}
    for table in row['physical']:
        require(set(table) == {'name', 'rows'} and table['name'] not in tables, 'physical table roster')
        tables[table['name']] = table['rows']
    base = {'metadata', 'leases', 'lease_budget_bindings', 'cancelled_requests'}
    optional = {'snapshot_copies', 'snapshot_copy_parts', 'snapshot_copy_journal'}
    require(set(tables) in (base, base | optional), 'missing/foreign SQL evidence table')
    leases, bindings, metadata = {}, {}, {}
    for raw in tables['metadata']:
        key, value = cells(raw, ('text', 'text'))
        require(key not in metadata, 'duplicate metadata')
        metadata[key] = value
    require(('snapshot_copy_schema' in metadata) == (set(tables) == base | optional), 'schema presence differs')
    if 'snapshot_copy_schema' in metadata:
        require(metadata['snapshot_copy_schema'] == '1', 'schema version differs')
    for raw in tables['leases']:
        lid, key, body, run = cells(raw, ('text', 'text|null', 'text', 'null'))
        value = parse(body)
        require(lid not in leases and lid == value['id'] and key == value['request'].get('idempotency_key'), 'SQL lease id/key/run binding')
        require(value['host'] == row['host'] and value['owner'] == row['owner'], 'foreign lease authority')
        leases[lid] = value
    require(list(leases.values()) == row['leases'], 'body/physical lease evidence differs')
    for raw in tables['lease_budget_bindings']:
        lid, body = cells(raw, ('text', 'text'))
        require(lid not in bindings, 'duplicate budget binding')
        bindings[lid] = parse(body)
    fences = [cells(raw, ('text',))[0] for raw in tables['cancelled_requests']]
    require(fences == row['fences'] and len(fences) == len(set(fences)), 'fence evidence differs')
    records, requests, record_bytes = {}, {}, {}
    for raw in tables.get('snapshot_copies', []):
        rid, key, wid, did, body = cells(raw, ('text',) * 5)
        r = parse(body)
        require(set(r) == RECORD_KEYS and rid == r['id'] and key == r['key'] and
                wid == r['work_lease'] and did == r['destination_lease'], 'copy physical identity')
        require(rid not in records and r['schema'] == 1 and not r['planning_attempted'] and not r['allocation_attempted'], 'duplicate/attempted record')
        require(r['authority'] == row['host'] and r['owner'] == row['owner'], 'record authority differs')
        require(len({rid, r['source_storage'], wid, did}) == 4, 'aliased record roles')
        require((r['phase'], r['revision'], r['final_slots']) in
                (('reserved-no-work', 1, 1), ('cancelled-no-work', 2, 0)), 'invalid phase/revision')
        require(parse(metadata['storage_root']) == r['provider_identity'], 'provider identity differs')
        source = leases[r['source_storage']]
        require(source['request']['kind'] == 'storage' and source.get('pid', 0) == 0 and
                not source.get('cargo') and not source.get('workload') and
                source['artifact']['identity'] == r['source_identity'] and
                source['artifact']['name'] == source['id'] and
                source['artifact']['path'] == source['managed_dir'] == row['home'] + '/storage/' + source['id'], 'source binding differs')
        records[rid], record_bytes[rid] = r, body.encode()
    require(list(records.values()) == row['copies'] and len(records) <= 1, 'one-key copy roster differs')
    parts = {rid: [] for rid in records}
    observed_parts = []
    for raw in tables.get('snapshot_copy_parts', []):
        rid, index, data = cells(raw, ('text', 'integer', 'blob'))
        require(rid in parts and index == len(parts[rid]) and 0 < len(data) <= 8192, 'part identity/order/bound')
        parts[rid].append(data)
        observed_parts.append({'copy': rid, 'part': index, 'data': base64.b64encode(data).decode()})
    require(observed_parts == row['parts'], 'part evidence differs')
    for rid, r in records.items():
        raw = b''.join(parts[rid])
        require(len(parts[rid]) == r['request_parts'] and len(raw) == r['request_bytes'] and digest(raw) == r['request_sha256'], 'canonical request hash/count differs')
        q = parse(raw)
        require(q['key'] == r['key'] and q['source_storage'] == r['source_storage'] and
                q['source_revision'] == r['source_revision'] and q['policy'] == 'idle-native-v1' and
                stamp(r['work_expires']) == stamp(r['created']) + q['work_ttl_ns'] and
                stamp(r['destination_expires']) == stamp(r['created']) + q['retention_ns'], 'request scope/deadline differs')
        requests[rid] = (q, raw)
        source = leases[r['source_storage']]
        if r['phase'] == 'reserved-no-work':
            require(source['state'] == 'active' and source['revision'] == r['source_revision'] and
                    source['artifact']['phase'] == 'owned' and stamp(source['expires_at']) > stamp(r['work_expires']), 'charged source pin differs')
        for name, lid, kind, expiry, state in (
                ('work', r['work_lease'], 'share-snapshot-work', r['work_expires'], 'active'),
                ('destination', r['destination_lease'], 'storage', r['destination_expires'], 'provisioning')):
            lease = leases[lid]
            require(lease['request']['idempotency_key'] == role(row['host'], r['key'], name) and lease['request']['kind'] == kind and
                    lease['request']['reason'] == q['reason'] and lease['expires_at'] == expiry and
                    lease['created_at'] == r['created'] and lease['started_at'] == r['created'] and
                    lease.get('capabilities') == ['snapshot-reservation-no-work'] and lease.get('pid', 0) == 0 and
                    not lease.get('cargo') and not lease.get('workload') and
                    lease['budget_binding'] == bindings[lid] and bindings[lid]['authority'] == row['host'] and
                    bindings[lid]['owner'] == row['owner'] and bindings[lid]['selected'] == q['budget'], 'ordinary promise/budget scope differs')
            expected_request = dict(budget=q['budget'], host='', reason=q['reason'],
                                    resources=q['work_resources'] if name == 'work' else
                                    dict(cpu=0, memory_bytes=0, disk_bytes=q['content_ceiling'] + (64 << 20)),
                                    ttl_ns=q['work_ttl_ns'] if name == 'work' else q['retention_ns'],
                                    idempotency_key=role(row['host'], r['key'], name), queue=False, kind=kind)
            if not expected_request['budget']:
                del expected_request['budget']
            if name == 'work': expected_request['storage_lease'] = r['source_storage']
            require(lease['request'] == expected_request, 'complete ordinary request differs')
            if name == 'work':
                require(lease['request']['storage_lease'] == r['source_storage'] and
                        lease['request']['resources'] == q['work_resources'] and not lease.get('artifact') and not lease.get('managed_dir'), 'work role differs')
            else:
                artifact = lease['artifact']
                require(lease['request']['resources'].get('disk_bytes') == q['content_ceiling'] + (64 << 20) and
                        artifact['name'] == lid and artifact['phase'] == 'creating' and not artifact.get('identity') and
                        artifact['policy'] == 'snapshot-reservation-no-work/v1; no allocation attempted' and
                        artifact['path'] == lease['managed_dir'] == row['home'] + '/storage/' + lid, 'destination attempted/role differs')
            if r['phase'] == 'reserved-no-work':
                require(lease['state'] == state and lease['revision'] == 1 and not lease.get('ended_at') and not lease.get('outcome'), 'promise not charged')
            else:
                require(lease['state'] == 'terminal' and lease['revision'] == 2 and lease['outcome'] == 'cancelled' and lease['ended_at'] == r['updated'], 'no-work settlement differs')
    journals, chain, seq = [], {}, 0
    for raw in tables.get('snapshot_copy_journal', []):
        current, body = cells(raw, ('integer', 'text'))
        j = parse(body)
        require(current > seq and j['copy'] in records, 'journal sequence/copy differs')
        seq = current
        prev_revision, prev_hash = chain.get(j['copy'], (0, ''))
        require(j['schema'] == 1 and j['revision'] == prev_revision + 1 and j['before'] == prev_hash and
                j['transition'] == ('reserved-no-work' if j['revision'] == 1 else 'cancelled-no-work'), 'journal chain differs')
        chain[j['copy']] = (j['revision'], j['after'])
        journals.append(j)
    require(journals == row['journals'], 'journal body differs')
    for rid, r in records.items():
        require(chain[rid] == (r['revision'], digest(record_bytes[rid])), 'journal final hash differs')
        initial = copy.deepcopy(r)
        initial.update(phase='reserved-no-work', revision=1, final_slots=1, decision='', updated=r['created'])
        require(next(j['after'] for j in journals if j['copy'] == rid and j['revision'] == 1) == digest(go_json(initial)), 'journal initial record hash differs')
    return records, requests, fences, leases


class Projector:
    def __init__(self):
        self.s = dict(phase='none', fence=False, request='none', staged='none', pending='none', checked=False,
                      cachePending=False, work=False, destination=False, pinned=False, owner=True,
                      unknown=False, deadlineOK=True, nativeStarted=False)
        self.states, self.edges, self.scopes = [], [], None
        self.runtimes, self.record, self.staged_view, self.last_view, self.commit_return = set(), None, None, None, None
        self.ordinal, self.runtime = 0, None
        self.receipts = []
        self.key = None
        self.calls = []
        self.transaction_id = None
        self.seen = []

    def emit(self, action, **changes):
        self.s.update(changes)
        self.states.append(copy.deepcopy(self.s))
        self.edges.append(action)

    def durable(self, view):
        records, _, fences, _ = view
        if records:
            r = next(iter(records.values()))
            return dict(phase='reserved' if r['phase'] == 'reserved-no-work' else 'cancelled', request='A',
                        fence=False, work=r['phase'] == 'reserved-no-work', destination=r['phase'] == 'reserved-no-work', pinned=r['phase'] == 'reserved-no-work')
        return dict(phase='none', request='none', fence=bool(fences), work=False, destination=False, pinned=False)

    def consume(self, row):
        require(set(row) == ROW_KEYS and row['schema'] == 'arm.snapshot-reservation.raw/v2' and not row['error'], 'raw schema/error')
        require(integer(row['ordinal']) and row['ordinal'] == self.ordinal + 1, 'missing/reordered raw row')
        self.ordinal += 1
        for key in ('sql_view', 'transaction', 'commit_pending', 'boundary_unknown'):
            require(type(row[key]) is bool, 'raw boolean alias')
        scope = (row['home'], row['host'], row['owner'])
        require(all(type(v) is str and v for v in scope), 'missing home authority')
        if self.scopes is None:
            require(row['action'] == 'initial' and row['sql_view'] and not row['transaction'], 'actual initial SQL row required')
            self.scopes, self.runtime = scope, row['runtime']
            self.runtimes.add(self.runtime)
        require(scope == self.scopes, 'foreign home/authority')
        require(re.fullmatch('[0-9a-f]{32}', row['runtime']) is not None, 'runtime identity differs')
        stamp(row['observed'])
        action, cut = row['action'], row['cut']
        require(action in ACTIONS, 'unknown raw action family')
        self.seen.append(action)
        if cut is not None:
            require(set(cut) <= CUT_KEYS and {'operation', 'api_error', 'report_error'} <= set(cut) and type(cut['api_error']) is str and type(cut['report_error']) is str, 'cut schema')
        view = sql_view(row) if row['sql_view'] else None
        if not row['sql_view']:
            require(not row['transaction'] and not any(row[k] for k in ('copies','parts','journals','leases','fences','physical')), 'fabricated light SQL view')
        if self.runtime != row['runtime']:
            require(action == 'sql-owner-loaded' and not self.s['owner'] and row['runtime'] not in self.runtimes and cut['api_error'] == '', 'runtime splice/reload without complete reader')
            self.runtime = row['runtime']; self.runtimes.add(self.runtime)
            self.emit('ReloadSQL', owner=True, unknown=False, cachePending=False)
        if action == 'pre-cancel-fence-staged':
            require(self.key in (None, row['id']), 'pre-cancel operation key changed')
            self.key = row['id']
        if view and view[0]:
            r = next(iter(view[0].values()))
            require(self.key in (None, r['key']), 'second operation key')
            self.key = r['key']
            if self.record is None:
                self.record = copy.deepcopy(r)
            else:
                for key in RECORD_KEYS - {'phase','revision','updated','decision','final_slots'}:
                    require(r[key] == self.record[key], 'immutable record changed')
        if view and view[2]:
            require(self.key is not None and view[2] == [role(row['host'], self.key, 'request')] and not view[0], 'foreign or revived pre-cancel fence')
        # Observation at the diagnostic sample is independent of the earlier
        # admission check. It can go either way after wall-clock adjustment.
        diagnostic_after_check = None
        if self.record:
            deadlines = (stamp(self.record['work_expires']),)
            source = (view or self.staged_view or self.last_view)[3][self.record['source_storage']]
            if source.get('expires_at'):
                deadlines += (stamp(source['expires_at']),)
            valid = all(stamp(row['observed']) < expiry for expiry in deadlines)
            if action in ('admission-check-accepted','admission-check-refused'):
                # Admission reads preceded the diagnostic envelope sample.
                diagnostic_after_check = (valid, valid != self.s['deadlineOK'])
            elif valid != self.s['deadlineOK']:
                self.emit('ObserveValidity', deadlineOK=valid)
        if action in ('admission-writes-staged','pre-cancel-fence-staged','cancellation-writes-staged'):
            require(row['transaction'] and self.s['staged'] == 'none' and not self.s['unknown'] and not self.s['cachePending'], 'stage cut/fence')
            stage = {'admission-writes-staged':'admit-A','pre-cancel-fence-staged':'fence','cancellation-writes-staged':'cancel'}[action]
            self.transaction_id = self.key if stage == 'fence' else next(iter(view[0]))
            require(row['id'] == self.transaction_id, 'staged cut physical copy/key identity differs')
            self.staged_view = view
            self.emit({'admit-A':'StageAdmit','fence':'StageFence','cancel':'StageCancel'}[stage], staged=stage)
        elif action in ('before-admit-commit','before-cancel-commit','before-pre-cancel-commit'):
            require(row['id'] == self.transaction_id and row['transaction'] and view is not None and view == self.staged_view and self.s['staged'] != 'none', 'before-Commit tuple differs from original staged writes')
        elif action in ('admission-check-accepted','admission-check-refused'):
            require(row['id'] == self.transaction_id and self.s['staged'] == 'admit-A' and cut['operation'] == 'admit' and cut.get('deadline'), 'deadline without staged admission')
            d, r = cut['deadline'], self.record
            require(set(d) == set('source source_revision request_sha256 work_expires source_expires work_observed source_observed'.split()), 'deadline roster')
            source = self.staged_view[3][r['source_storage']]
            require(d['source'] == r['source_storage'] and d['source_revision'] == r['source_revision'] and
                    d['request_sha256'] == r['request_sha256'] and d['work_expires'] == r['work_expires'] and d['source_expires'] == source.get('expires_at'), 'deadline/source binding')
            valid = stamp(d['work_observed']) < stamp(d['work_expires']) and d['source_expires'] is not None
            require((d['source_observed'] is not None) == valid, 'short-circuit clock read fabricated/missing')
            if valid:
                valid = stamp(d['source_observed']) < stamp(d['source_expires'])
            self.emit('ObserveValidity', deadlineOK=valid)
            require(valid == (action == 'admission-check-accepted'), 'actual native check verdict differs')
            if valid:
                self.emit('CheckAdmit', checked=True)
        elif action == 'commit-call-entry':
            require(row['id'] == self.transaction_id and self.s['staged'] != 'none' and self.s['pending'] == 'none' and row['commit_pending'] and
                    (self.s['staged'] != 'admit-A' or self.s['checked']), 'unchecked/repeated Commit')
            require(cut['operation'] == {'admit-A':'admit','fence':'pre-cancel','cancel':'cancel'}[self.s['staged']], 'wrong Commit role')
            self.emit('BeginCommit', pending=self.s['staged'], cachePending=True)
        elif action == 'commit-call-return':
            require(row['id'] == self.transaction_id and self.s['pending'] != 'none' and self.commit_return is None, 'unowned/duplicate Commit return')
            require(cut['operation'] == {'admit-A':'admit','fence':'pre-cancel','cancel':'cancel'}[self.s['pending']], 'returned Commit role differs')
            self.commit_return = copy.deepcopy(cut)
        elif action in ('admit-commit-return','admit-commit-unknown','cancel-commit-return','cancel-commit-unknown','pre-cancel-commit-return','pre-cancel-commit-unknown'):
            require(row['id'] == self.transaction_id and view is not None and not row['transaction'] and self.commit_return is not None, 'outcome without actual Commit/result/read')
            result = self.commit_return
            ack = not result['api_error'] and not result['report_error']
            require(self.s['pending'] in ('admit-A','fence','cancel'), 'outcome pending role absent')
            expected_role = {'admit-A':'admit','fence':'pre-cancel','cancel':'cancel'}[self.s['pending']]
            require(action == expected_role + '-commit-' + ('return' if ack else 'unknown'), 'outcome role/API/report result differs from retained attempt')
            durable = self.durable(view)
            if durable == {k:self.s[k] for k in durable}:
                require(not ack and result['api_error'], 'acknowledged SQL write missing')
                self.emit('UnknownWithoutWrite', unknown=True, staged='none', pending='none', checked=False)
            else:
                expected = self.durable(self.staged_view)
                require(durable == expected, 'actual SQL outcome differs from complete staged tuple')
                op = self.s['pending']
                require(op in ('admit-A','fence','cancel'), 'outcome role')
                self.emit({'admit-A':'FinishAdmit','fence':'FinishFence','cancel':'FinishCancel'}[op],
                          **durable, staged='none', pending='none', checked=False, unknown=not ack,
                          cachePending=not ack if op == 'fence' else True)
            self.commit_return = None
            self.last_view, self.staged_view = view, None
            self.receipts.extend(copy.deepcopy(list(view[0].values())))
        elif action == 'publication-read-refused':
            require(cut['api_error'] and self.s['cachePending'] and not self.s['unknown'] and self.s['pending'] == self.s['staged'] == 'none', 'unowned publication failure')
            self.emit('PublicationUnknown', unknown=True)
        elif action in ('admission-published','cancellation-published'):
            require(view is not None and not row['transaction'] and not self.s['unknown'] and self.s['cachePending'] and
                    self.durable(view) == {k:self.s[k] for k in self.durable(view)}, 'publication without exact complete committed read')
            self.emit('PublishKnown', cachePending=False)
        elif action == 'rollback-call-return':
            if not cut['api_error']:
                require(self.s['pending'] == 'none' and self.s['staged'] != 'none' and not self.s['unknown'], 'rollback cut differs')
                self.emit('Rollback', staged='none', checked=False)
            else:
                require(self.s['pending'] == 'none' and self.s['staged'] == 'none', 'unresolved failed rollback')
        elif action == 'sql-close-return':
            require(not cut['api_error'] and self.s['staged'] == 'none' and self.s['pending'] == 'none', 'unknown SQL authority retirement')
            self.emit('CloseSQL', owner=False, unknown=True)
        elif action == 'reserve-call-return':
            if not cut['api_error']:
                require(self.last_view is not None and cut['record'] in self.receipts, 'successful reserve receipt differs')
                q = cut.get('request')
                require(q and q['key'] == cut['key'] == cut['record']['key'] and digest(go_json(q)) == cut['record']['request_sha256'], 'method complete request/key binding differs')
            elif cut.get('request') and self.record:
                require(cut['key'] == cut['request']['key'] == self.key, 'failed request key differs')
                if digest(go_json(cut['request'])) != self.record['request_sha256']:
                    require(cut['api_error'].startswith('idempotency_conflict:'), 'conflicting complete request did not return known immutable-key conflict')
        elif action == 'cancel-call-return':
            require(cut['key'] == self.key, 'cancel method original key differs')
            if not cut['api_error']:
                require(self.s['phase'] == 'cancelled' or self.s['fence'], 'positive cancel without durable settlement/fence')
        elif action == 'lookup-call-return':
            require(cut['key'] == self.key and type(cut.get('present')) is bool, 'lookup original key/presence differs')
            if not cut['api_error'] and cut['present']:
                require(self.last_view is not None and cut['record'] in self.last_view[0].values(), 'lookup returned foreign/malformed record')
        if diagnostic_after_check is not None:
            valid, retained_sample = diagnostic_after_check
            if retained_sample or valid != self.s['deadlineOK']:
                self.emit('ObserveValidity', deadlineOK=valid)
        if action in ('reserve-call-return','cancel-call-return','lookup-call-return'):
            self.calls.append((action, copy.deepcopy(cut)))
        if view and not row['transaction']:
            require(self.durable(view) == {k:self.s[k] for k in self.durable(view)}, 'unexplained durable SQL mutation')
            self.last_view = view
            self.receipts.extend(copy.deepcopy(list(view[0].values())))
        require(row['commit_pending'] == self.s['cachePending'], 'cache fence missing/cleared')
        # Closed owner's uncertainty is modeled separately from live boundary.
        if self.s['owner']:
            require(row['boundary_unknown'] == self.s['unknown'], 'boundary unknown mismatch')
        if not self.states:
            self.states.append(copy.deepcopy(self.s)); self.edges.append('Init')
        else:
            self.emit('LookupOrRefusal')

    def finish(self, scenario):
        require(scenario in SCENARIOS and self.seen[-1] == 'sql-close-return' and not self.s['owner'] and
                self.commit_return is None and self.s['staged'] == self.s['pending'] == 'none', 'missing actual final close/outcome suffix')
        require(len(self.states) <= 128, 'projected complete state bound')
        reserve = [c for action,c in self.calls if action == 'reserve-call-return']
        cancel = [c for action,c in self.calls if action == 'cancel-call-return']
        lookup = [c for action,c in self.calls if action == 'lookup-call-return']
        if scenario == 'reserve-cancel':
            require(self.s['phase'] == 'cancelled' and len(reserve) == 4 and sum(not c['api_error'] for c in reserve) == 3 and
                    sum(c['api_error'].startswith('idempotency_conflict:') for c in reserve) == 1 and
                    len(cancel) == 1 and not cancel[0]['api_error'], 'full exact-key/conflict/cancel/retry suffix absent')
        elif scenario == 'pre-cancel':
            require(self.s['fence'] and self.s['phase'] == 'none' and len(cancel) == len(reserve) == 1 and
                    not cancel[0]['api_error'] and reserve[0]['api_error'].startswith('request_cancelled:'), 'actual durable pre-cancel/refusal absent')
        elif scenario == 'lost-admit-ack':
            require(self.s['phase'] == 'reserved' and self.s['cachePending'] and len(reserve) == len(lookup) == 1 and
                    reserve[0]['api_error'].startswith('snapshot_outcome_unknown:') and not lookup[0]['api_error'] and lookup[0]['present'], 'unknown admission/original-key lookup absent')
        elif scenario == 'lost-cancel-ack':
            require(self.s['phase'] == 'cancelled' and self.s['cachePending'] and len(cancel) == 1 and
                    cancel[0]['api_error'].startswith('snapshot_outcome_unknown:'), 'actual lost-cancel acknowledgment absent')
        elif scenario == 'close-reopen':
            require(len(self.runtimes) == 2 and self.seen.count('sql-close-return') == 2 and
                    self.s['phase'] == 'cancelled' and 'sql-owner-loaded' in self.seen, 'fresh complete SQL runtime/recovery suffix absent')
        elif scenario == 'admission-rollback':
            require(self.s['phase'] == 'none' and 'admission-writes-staged' in self.seen and
                    'commit-call-entry' not in self.seen and len(reserve) == 1 and reserve[0]['api_error'], 'actual final-write rollback absent')
        if scenario == 'deadline-after-check':
            require('admission-check-accepted' in self.seen and 'admit-commit-return' in self.seen and any(
                a['phase'] == 'reserved' and not a['deadlineOK'] for a in self.states), 'delayed Commit witness absent')
        if scenario == 'deadline-before-check':
            require('admission-check-refused' in self.seen and 'commit-call-entry' not in self.seen, 'expired check failed to refuse before Commit')
        if scenario == 'commit-context-cancel':
            require('admit-commit-unknown' in self.seen and self.s['phase'] == 'none', 'real cancelled Commit no-write control absent')
        return {'scenario':scenario, 'states':self.states, 'edges':self.edges, 'raw_rows':self.ordinal}


def project(rows, scenario):
    require(type(rows) is list and 0 < len(rows) <= 128 and len(json.dumps(rows).encode()) <= 4 << 20, 'bounded complete raw history')
    p = Projector()
    for row in rows:
        p.consume(row)
    return p.finish(scenario)
