#!/usr/bin/env python3
"""Pure regressions; synthetic envelopes here are not actual TLC evidence."""
import copy
import json
import importlib.util
from pathlib import Path
import tempfile
import types
import unittest
from unittest.mock import patch

path = Path(__file__).with_name('test-lease-reviews-formal.py')
spec = importlib.util.spec_from_file_location('review_formal_tests', path)
formal = importlib.util.module_from_spec(spec)
exec(compile(path.read_bytes(), str(path), 'exec'), formal.__dict__)


def fixture():
    rows = []
    for n, action in enumerate(formal.ACTIONS):
        rows.append({'action': action, 'live': n < 6, 'revision': 1 if n < 6 else 2,
                     'expiry': '2026-10-25T01:00:00Z', 'enabled': n > 0,
                     'phase': 'none' if n < 2 else 'pending' if n < 4 else 'replied',
                     'policy_generation': int(n > 0)})
    states = []
    for n, row in enumerate(rows, 1):
        values = {key: 0 for key in formal.VARS}
        values.update({key: row[actual] for actual, key in formal.OBSERVED.items()})
        values.update(position=n, acknowledged=n >= 4, replied=n >= 5,
                      previousLive=True, previousExpiry=row['expiry'], lastKind='review')
        states.append([n, values])
    document = {'vars': sorted(formal.VARS), 'counterexample': {
        'state': states, 'action': [[states[n], {'name': 'TraceNext'}, states[n + 1]] for n in range(7)]}}
    return rows, document


def producer_fixture():
    common = {'Time': '2026-10-25T02:00:00Z', 'Package': formal.exporter.PACKAGE}
    records = [{**common, 'Action': 'start'},
               {**common, 'Action': 'run', 'Test': formal.PRODUCER_TEST}]
    for name in formal.PRODUCER_CASES[1:]:
        records.extend([{**common, 'Action': 'run', 'Test': name},
                        {**common, 'Action': 'pass', 'Test': name, 'Elapsed': 0.01}])
    records.extend([{**common, 'Action': 'pass', 'Test': formal.PRODUCER_TEST, 'Elapsed': 0.1},
                    {**common, 'Action': 'pass', 'Elapsed': 0.2}])
    return records


def producer_text(records):
    return ''.join(json.dumps(record) + '\n' for record in records)


class ReviewFormalTests(unittest.TestCase):
    def test_separate_producer_controls_complete_actual_roster(self):
        self.assertEqual(formal.read_producer_controls(producer_text(producer_fixture())), 9)

    def test_producer_control_missing_duplicate_foreign_failed_and_partial_refuse(self):
        for mutation in ('missing', 'duplicate', 'order', 'foreign-package', 'foreign-case',
                         'fail', 'skip', 'trace-prefix', 'partial', 'extra-field', 'no-package-pass'):
            with self.subTest(mutation=mutation):
                records = producer_fixture()
                if mutation == 'missing':
                    records.pop(2)
                elif mutation == 'duplicate':
                    records.insert(2, copy.deepcopy(records[1]))
                elif mutation == 'order':
                    records[2], records[4] = records[4], records[2]
                elif mutation == 'foreign-package':
                    records[2]['Package'] = 'other-package'
                elif mutation == 'foreign-case':
                    records[2]['Test'] = formal.exporter.TEST
                elif mutation in ('fail', 'skip'):
                    records[3]['Action'] = mutation
                elif mutation == 'trace-prefix':
                    records.insert(2, {'Time': 'now', 'Action': 'output', 'Package': formal.exporter.PACKAGE,
                                       'Test': formal.PRODUCER_TEST, 'Output': formal.exporter.PREFIX + '{}\n'})
                elif mutation == 'extra-field':
                    records[2]['Invented'] = True
                elif mutation == 'no-package-pass':
                    records.pop()
                text = producer_text(records)
                if mutation == 'partial':
                    text = text[:-1]
                with self.assertRaises(ValueError):
                    formal.read_producer_controls(text)

    def test_producer_control_json_types_duplicates_and_completion_are_closed(self):
        for mutation in ('bool-elapsed', 'nan-elapsed', 'null-test', 'string-output', 'late-event', 'duplicate-json-key'):
            with self.subTest(mutation=mutation):
                records = producer_fixture()
                if mutation == 'bool-elapsed':
                    records[3]['Elapsed'] = True
                elif mutation == 'nan-elapsed':
                    records[3]['Elapsed'] = float('nan')
                elif mutation == 'null-test':
                    records[-1]['Test'] = None
                elif mutation == 'string-output':
                    records.insert(2, {'Time': 'now', 'Action': 'output', 'Package': formal.exporter.PACKAGE,
                                       'Test': formal.PRODUCER_TEST, 'Output': 12})
                elif mutation == 'late-event':
                    records.append(copy.deepcopy(records[-1]))
                text = producer_text(records)
                if mutation == 'duplicate-json-key':
                    text = text.replace('"Action": "start"', '"Action": "start", "Action": "start"', 1)
                with self.assertRaises(ValueError):
                    formal.read_producer_controls(text)

    def test_producer_controls_run_separately_before_original_sole_trace(self):
        rows, _ = fixture()
        with tempfile.TemporaryDirectory() as directory:
            gate = formal.ReviewGate.__new__(formal.ReviewGate)
            gate.run = Path(directory)
            gate.args = types.SimpleNamespace(go='fixed-go')
            gate.summary = {'gates': []}
            gate.verify_sources = lambda: None
            calls = []
            def command(argv, name, **kwargs):
                calls.append((argv, name, kwargs))
                return producer_text(producer_fixture()) if name == 'actual-review-producer-controls' else ''
            gate.command = command
            gate.replay = lambda actual, name, accepted: 8 if accepted else 7
            with patch.object(formal.exporter, 'read_trace', return_value=rows):
                gate.traces()
            self.assertEqual([call[1] for call in calls], ['actual-review-producer-controls', 'actual-review-go'])
            self.assertEqual([call[0][call[0].index('-run') + 1] for call in calls],
                             ['^TestLeaseReviewDevTraceSnapshotControls$', '^TestLeaseReviewDevTraceConversationIsInert$'])
            self.assertTrue(all(call[2] == {'timeout': 150} for call in calls))
            self.assertEqual(gate.summary['gates'][0]['named_cases'], 9)
            self.assertFalse(gate.summary['gates'][0]['trace_export'])

    def test_bad_producer_controls_stop_before_original_trace(self):
        gate = formal.ReviewGate.__new__(formal.ReviewGate)
        gate.args = types.SimpleNamespace(go='fixed-go')
        gate.summary = {'gates': []}
        gate.verify_sources = lambda: None
        calls = []
        records = producer_fixture()[:-1]
        gate.command = lambda argv, name, **kwargs: (calls.append(name), producer_text(records))[1]
        with self.assertRaises(ValueError):
            gate.traces()
        self.assertEqual(calls, ['actual-review-producer-controls'])
        self.assertEqual(gate.summary['gates'], [])

    def test_changed_configuration_refuses_before_theorem_execution(self):
        actual = 'observed configuration'
        expected = formal.hashlib.sha256(b'reviewed configuration').hexdigest()
        gate = formal.ReviewGate.__new__(formal.ReviewGate)
        gate.args = types.SimpleNamespace(mode='prove', expect_proof_config_sha256=expected)
        gate.summary = {'tools': {'tlapm': {'config': actual}}}
        calls = []
        with patch.object(formal.base.Gate, 'tools', side_effect=lambda: calls.append('health')):
            with self.assertRaisesRegex(ValueError, 'differs from reviewed selection'):
                gate.tools()
                calls.append('theorem execution')
        self.assertEqual(calls, ['health'])
        digest = formal.hashlib.sha256(actual.encode()).hexdigest()
        self.assertEqual(formal.verify_proof_configuration(actual, digest), digest)

    def test_malformed_configuration_pin_refuses_before_health(self):
        gate = formal.ReviewGate.__new__(formal.ReviewGate)
        gate.args = types.SimpleNamespace(mode='prove', expect_proof_config_sha256='not-a-digest')
        with patch.object(formal.base.Gate, 'tools', side_effect=AssertionError('no native health probe')):
            with self.assertRaisesRegex(ValueError, 'before tool health'):
                gate.tools()

    def test_failure_still_records_source_closure_and_keeps_original(self):
        primary = RuntimeError('native failed')
        later = ValueError('source changed after failed command')
        calls = []
        def verify():
            calls.append('verify')
            raise later
        gate = types.SimpleNamespace(summary={}, verify_sources=verify, finish=lambda: calls.append('finish'))
        result = formal.finish_gate(gate, primary)
        self.assertIs(result, primary)
        self.assertIs(result.review_source_closure_failure, later)
        self.assertEqual(calls, ['verify', 'finish'])
        self.assertEqual(gate.summary['status'], 'failed')
        self.assertEqual(gate.summary['source_closure']['status'], 'failed')

    def test_final_source_failure_cannot_be_reported_as_success(self):
        error = ValueError('source changed')
        def verify():
            raise error
        gate = types.SimpleNamespace(summary={}, verify_sources=verify, finish=lambda: None)
        self.assertIs(formal.finish_gate(gate, None), error)
        self.assertEqual(gate.summary['status'], 'failed')

    def test_summary_failure_preserves_original_cancellation(self):
        original = KeyboardInterrupt()
        error = OSError('summary unavailable')
        def finish():
            raise error
        gate = types.SimpleNamespace(summary={}, verify_sources=lambda: None, finish=finish)
        self.assertIs(formal.finish_gate(gate, original), original)
        self.assertIs(original.review_summary_failure, error)
        self.assertEqual(gate.summary['source_closure']['status'], 'passed')

    def test_positive_safety_completes_before_witness(self):
        rows, doc = fixture()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            gate = formal.ReviewGate.__new__(formal.ReviewGate)
            gate.run = root
            calls = []
            def tlc(module, config, name, extra=(), expected=0):
                source = config.read_text()
                calls.append((name, source, expected, extra))
                if name.endswith('-safety'):
                    self.assertEqual(expected, 0)
                    self.assertNotIn('NotComplete', source)
                    return 'Model checking completed. No error has been found.\n8 distinct states found, 0 states left on queue\n'
                self.assertEqual(len(calls), 2)
                (root / 'actual-tlc.json').write_text(__import__('json').dumps(doc))
                return 'Error: Invariant NotComplete is violated.\nError: The behavior up to this point is:\n8 distinct states found, 0 states left on queue\n'
            gate.tlc = tlc
            self.assertEqual(gate.replay(rows, 'actual', True), 8)
            self.assertEqual([item[2] for item in calls], [0, 12])
            self.assertIn('NotComplete', calls[1][1])

    def test_complete_envelope(self):
        rows, document = fixture()
        self.assertEqual(formal.compare_witness(document, rows), 8)

    def test_closed_complete_roster(self):
        for mutation in ('missing', 'duplicate', 'unknown', 'extra-envelope'):
            with self.subTest(mutation=mutation):
                rows, doc = fixture()
                if mutation == 'missing':
                    doc['vars'].pop()
                elif mutation == 'duplicate':
                    doc['vars'][0] = doc['vars'][1]
                elif mutation == 'unknown':
                    doc['vars'][0] = 'unobserved'
                else:
                    doc['trusted'] = True
                with self.assertRaises(ValueError):
                    formal.compare_witness(doc, rows)

    def test_original_go_fields_types_and_order(self):
        for mutation in ('value', 'bool-ordinal', 'cursor', 'field', 'crossed-edge', 'missing-state'):
            with self.subTest(mutation=mutation):
                rows, doc = fixture()
                if mutation == 'value':
                    doc['counterexample']['state'][3][1]['revision'] = 2
                elif mutation == 'bool-ordinal':
                    doc['counterexample']['state'][0][0] = True
                elif mutation == 'cursor':
                    doc['counterexample']['state'][0][1]['position'] = True
                elif mutation == 'field':
                    doc['counterexample']['state'][0][1]['extra'] = 1
                elif mutation == 'crossed-edge':
                    doc['counterexample']['action'][0][2] = doc['counterexample']['state'][2]
                else:
                    doc['counterexample']['state'].pop()
                with self.assertRaises(ValueError):
                    formal.compare_witness(doc, rows)

    def test_alterations_preserve_original_initial_and_closed_shape(self):
        rows, _ = fixture()
        altered = formal.negatives(rows)
        self.assertEqual(len(altered), 4)
        for changed in altered.values():
            self.assertEqual(changed[0], rows[0])
            self.assertNotEqual(changed, rows)
            self.assertEqual(formal.validate_rows(changed), changed)

    def test_terminal_statistic_is_required_and_exhaustive(self):
        text = 'Model checking completed. No error has been found.\n8 distinct states found, 0 states left on queue\n'
        self.assertEqual(formal.terminal_count(text, 8), 8)
        for broken in (text.replace('0 states left', '1 states left'), text.replace('8 distinct states found', '0 distinct states found'), text + text, text + '\nError: unexpected\n', text + '\nWarning: incomplete\n', 'Model checking completed. No error has been found.'):
            with self.subTest(broken=broken), self.assertRaises(ValueError):
                formal.terminal_count(broken, 8)

    def test_only_exact_witness_diagnostics(self):
        text = 'Error: Invariant NotComplete is violated.\nError: The behavior up to this point is:\n8 distinct states found, 0 states left on queue\n'
        self.assertEqual(formal.terminal_count(text, 8, 'NotComplete'), 8)
        for broken in (text.replace('NotComplete', 'TraceSafety'), text.replace('Error: The behavior up to this point is:\n', ''), text + 'Error: semantic error\n', text.replace('8 distinct', '7 distinct')):
            with self.subTest(broken=broken), self.assertRaises(ValueError):
                formal.terminal_count(broken, 8, 'NotComplete')

    def test_non_go_inputs_are_captured(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'embedded.json').write_text('old')
            before = formal.source_inventory(root)
            (root / 'embedded.json').write_text('new')
            self.assertNotEqual(formal.source_inventory(root), before)
            (root / '.tla').mkdir()
            (root / '.tla' / 'generated.log').write_text('retained output')
            self.assertNotIn('.tla/generated.log', formal.source_inventory(root))

    def test_unknown_and_unordered_rows_refuse(self):
        rows, _ = fixture()
        rows[1], rows[2] = rows[2], rows[1]
        with self.assertRaises(ValueError):
            formal.validate_rows(rows)


if __name__ == '__main__':
    unittest.main()
