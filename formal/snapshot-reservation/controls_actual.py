"""Alter complete REAL histories; no expected-success synthetic transcript."""
import copy


def controls(histories, projection):
    def row(rows, action):
        return next(x for x in rows if x['action'] == action)
    def corrupt_deadline(rows):
        r = row(rows, 'admission-check-accepted')
        r['cut']['deadline']['work_observed'] = r['cut']['deadline']['work_expires']
    def corrupt_key_type(rows):
        r = row(rows, 'admission-writes-staged')
        table = next(t for t in r['physical'] if t['name'] == 'snapshot_copies')
        table['rows'][0][1]['type'] = 'blob'
    def corrupt_id(rows):
        r = row(rows, 'admission-writes-staged')
        next(t for t in r['physical'] if t['name'] == 'snapshot_copies')['rows'][0][0]['value'] = 'f' * 32
    def duplicate(rows):
        rows.insert(2, copy.deepcopy(rows[1]))
    cases = (
        ('altered-native-check', 'reserve-cancel', corrupt_deadline),
        ('missing-deadline', 'reserve-cancel', lambda r: row(r, 'admission-check-accepted')['cut'].pop('deadline')),
        ('blob-key-alias', 'reserve-cancel', corrupt_key_type),
        ('physical-id', 'reserve-cancel', corrupt_id),
        ('missing-final-close', 'reserve-cancel', lambda r: r.pop()),
        ('foreign-runtime', 'reserve-cancel', lambda r: row(r, 'commit-call-entry').update(runtime='f' * 32)),
        ('duplicate-row', 'reserve-cancel', duplicate),
        ('stage-marked-durable', 'reserve-cancel', lambda r: row(r, 'admission-writes-staged').update(transaction=False)),
        ('unknown-cleared', 'lost-admit-ack', lambda r: row(r, 'admit-commit-unknown').update(boundary_unknown=False)),
        ('cache-fence-cleared', 'lost-admit-ack', lambda r: row(r, 'admit-commit-unknown').update(commit_pending=False)),
        ('wrong-outcome-role', 'reserve-cancel', lambda r: row(r, 'admit-commit-return').update(action='cancel-commit-return')),
        ('wrong-commit-copy-id', 'reserve-cancel', lambda r: row(r, 'commit-call-return').update(id='f' * 32)),
        ('lost-api-error', 'commit-context-cancel', lambda r: row(r, 'commit-call-return')['cut'].update(api_error='')),
    )
    accepted = []
    for name, scenario, mutate in cases:
        rows = copy.deepcopy(histories[scenario]); mutate(rows)
        try:
            projection.project(rows, scenario)
        except (ValueError, KeyError, TypeError, StopIteration):
            accepted.append(name)
        else:
            raise ValueError('altered actual history accepted: ' + name)
    return accepted
