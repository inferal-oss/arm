#!/usr/bin/env python3
"""Pure parser/coverage guards. This test starts no native command or fixture.

Synthetic envelope units do not replace the actual raw-TLC controls performed
later by test-node-stop-formal.py on a real full24 witness.
"""
import copy
import json
from pathlib import Path
import tempfile
import types
import unittest

ROOT = Path(__file__).resolve().parents[1]
source = ROOT / 'scripts/test-node-stop-formal.py'
runner = types.ModuleType('node_stop_runner')
runner.__file__ = str(source)
exec(compile(source.read_bytes(), str(source), 'exec'), runner.__dict__)
projection_source = ROOT / 'formal/node-stop/export_actual.py'
projection = types.ModuleType('node_stop_projection')
projection.__file__ = str(projection_source)
exec(compile(projection_source.read_bytes(), str(projection_source), 'exec'), projection.__dict__)


class NodeStopRunnerGuards(unittest.TestCase):
    def test_consumed_restart_review_survives_a_later_stop(self):
        # Closed synthetic SQL/review frames exercise projection only. They are
        # not native observations or evidence that the Go lifecycle completed.
        p = projection.Projector()
        actor = dict(source_host='a' * 32, certificate_sha256='b' * 64,
                     spki_sha256='c' * 64, pair='d' * 64, grant_revision=1)
        peer = dict(identity=dict(host=actor['source_host'], fingerprint=actor['certificate_sha256'],
                                  spki_sha256=actor['spki_sha256']), pair=actor['pair'], revision=1,
                    state='active', proven=True, grant=dict(permissions=['node-stop']))
        p.accept_peer(peer)
        p.authorities = ['1' * 32]
        stamp = '2026-01-01T00:00:00Z'

        def record(key, generation, revision):
            target = dict(host='e' * 32, certificate_sha256='f' * 64,
                          spki_sha256='0' * 64, owner='1000', home='/tmp/arm-trace-fixture',
                          directory=dict(device=1, inode=1, owner=1000, born_seconds=1,
                                         born_nanoseconds=0, birth_known=True),
                          backend_generation=generation, startup_contract='arm-desired-state-gated-v1')
            plan = dict(schema='arm.node-stop.plan/v1', key=key, target=target, actor=actor,
                        desired_state_revision=revision - 1, policy=dict(force=False, wait=False),
                        reason='synthetic projection control',
                        impact=dict(complete=True, scope_sha256='9' * 64, outstanding=0,
                                    obligations=[], blockers=[], affected_peers=[actor['source_host']]),
                        observed_at=stamp, expires_at='2026-01-01T00:02:00Z', supervisor_scope='fixture')
            digest = projection.go_digest(plan)
            p.permits[digest] = plan
            return dict(schema='arm.node-stop.record/v1', plan=plan, review_sha256=digest,
                        shutdown_id=key + '-shutdown', desired_state_revision=revision,
                        phase='drain-ready' if key == 'first' else 'accepted',
                        created_at=stamp, updated_at=stamp)

        def ledger(records, revision, running):
            selected = records[-1]
            state = dict(schema='arm.node-stop.state/v1', revision=revision, desired_running=running,
                         target=selected['plan']['target'], stop_key=selected['plan']['key'],
                         stop_sha256=selected['review_sha256'])
            rows = {'record:' + r['plan']['key']: json.dumps(r) for r in records}
            rows['state'] = json.dumps(state)
            p.accept_ledger(dict(state=state, records=records, rows=rows, error=''))
            return state

        def review(state):
            q = dict(schema='arm.node-stop.restart/v1', stopped_intent=copy.deepcopy(state),
                     stopped_intent_sha256=projection.go_digest(state), reason='independent synthetic review',
                     observed_at=stamp, expires_at='2026-01-01T00:02:00Z')
            p.restart = dict(expected=copy.deepcopy(state), review=q,
                             elapsed=dict(first=10, last=10, failure=''), attempted=False,
                             closed=False, error='', failure_kind='', failure_matches_return=False)
            identity = projection.canonical(q)
            p.restart_objects.append(identity)
            return identity

        first = record('first', p.authorities[0], 2)
        stopped = ledger([first], 2, False)
        consent = review(stopped)
        p.restart_consent.add(consent)
        pending = p.snapshot()
        self.assertEqual((pending['restartReview'], pending['restartFresh'], pending['restartApproved']),
                         (2, True, True))
        ledger([first], 3, True)
        consumed = p.snapshot()
        self.assertEqual((consumed['restartReview'], consumed['restartFresh'], consumed['restartApproved']),
                         (0, False, False))
        p.authorities.append('2' * 32)
        second = record('second', p.authorities[-1], 4)
        later_stop = ledger([first, second], 4, False)
        later = p.snapshot()
        self.assertEqual((later['restartReview'], later['restartFresh'], later['restartApproved']),
                         (0, False, False))
        self.assertEqual(later['restartID'], consumed['restartID'])
        self.assertEqual(later['records']['second']['phase'], 'admitted')

        fresh_consent = review(later_stop)
        fresh = p.snapshot()
        self.assertEqual((fresh['restartID'], fresh['restartReview'], fresh['restartFresh'], fresh['restartApproved']),
                         (2, 4, True, False))
        p.restart_consent.add(fresh_consent)
        self.assertTrue(p.snapshot()['restartApproved'])
        for name, change, healthy in (
                ('expired', dict(elapsed=dict(first=10, last=10 + projection.TTL,
                                             failure='review expired in elapsed time')), True),
                ('clock-fault', dict(elapsed=dict(first=10, last=10, failure='native clock failed')), False),
                ('failed-attempt', dict(attempted=True, error='operation refused'), True),
                ('closed', dict(closed=True), True)):
            with self.subTest(name=name):
                original = copy.deepcopy(p.restart)
                p.restart.update(change)
                refused = p.snapshot()
                self.assertEqual((refused['restartReview'], refused['restartFresh'],
                                  refused['restartApproved'], refused['restartHealthy']),
                                 (4, False, True, healthy))
                p.restart = original

    def test_exact_case_roster(self):
        expected = {runner.ACTUAL_TEST, runner.OBSERVER_TEST} | {runner.ACTUAL_TEST + '/' + name for name in runner.SCENARIOS}
        events = []
        for name in sorted(expected):
            events += [dict(Action='run', Package=runner.PACKAGE, Test=name), dict(Action='pass', Package=runner.PACKAGE, Test=name)]
        events.append(dict(Action='pass', Package=runner.PACKAGE))
        encode = lambda rows: '\n'.join(json.dumps(x) for x in rows)
        self.assertEqual(runner.go_receipt(encode(events))['cases'], list(runner.SCENARIOS))
        for name, mutate in (
            ('missing-case', lambda rows: rows.pop(0)),
            ('duplicate-pass', lambda rows: rows.append(copy.deepcopy(rows[1]))),
            ('skipped', lambda rows: rows[1].update(Action='skip')),
            ('foreign-package', lambda rows: rows[0].update(Package='elsewhere')),
            ('unknown-test', lambda rows: rows[0].update(Test='Unknown'))):
            with self.subTest(name=name):
                altered = copy.deepcopy(events)
                mutate(altered)
                with self.assertRaises(ValueError):
                    runner.go_receipt(encode(altered))

    def test_integer_state_ids_with_consistent_edges(self):
        states = [[1, {'v': 0}], [2, {'v': 1}]]
        original = dict(vars=['v'], counterexample=dict(state=states, action=[[copy.deepcopy(states[0]), {'name': 'edge'}, copy.deepcopy(states[1])]]))
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'trace.json'
            path.write_text(json.dumps(original))
            self.assertEqual(runner.base.tlc_states(path, variables=['v'], validator=lambda rows: rows), [{'v': 0}, {'v': 1}])
            for value in (True, 1.0):
                altered = copy.deepcopy(original)
                altered['counterexample']['state'][0][0] = value
                altered['counterexample']['action'][0][0] = copy.deepcopy(altered['counterexample']['state'][0])
                path.write_text(json.dumps(altered))
                with self.assertRaisesRegex(ValueError, 'noncontiguous'):
                    runner.base.tlc_states(path, variables=['v'], validator=lambda rows: rows)

    def test_original_command_owner_pin(self):
        self.assertEqual(runner.digest(ROOT / 'scripts/formal_process.py'), runner.PROCESS_SHA)

    def test_model_and_proof_source_pins(self):
        folder = ROOT / 'formal/node-stop'
        for name, sha in {**runner.MODEL_SHA, **runner.PROOF_SHA}.items():
            with self.subTest(name=name):
                self.assertEqual(runner.digest(folder / name), sha)

    def test_no_partial_actual_roster_flag(self):
        text = (ROOT / 'scripts/test-node-stop-formal.py').read_text()
        self.assertNotIn("add_argument('--targets'", text)
        self.assertEqual(len(runner.SCENARIOS), 14)


if __name__ == '__main__':
    unittest.main()
