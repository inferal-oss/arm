#!/usr/bin/env python3
"""Pure exporter controls over one actual retained Go dev-test capture."""
import argparse
import importlib.util
import json
from pathlib import Path
import sys
sys.dont_write_bytecode = True
import tempfile
import unittest

parser = argparse.ArgumentParser()
parser.add_argument('--input', required=True)
args, remaining = parser.parse_known_args()
source = Path(args.input)
exporter = Path(__file__).with_name('export-lease-review-trace.py')
spec = importlib.util.spec_from_file_location('lease_review_exporter', exporter)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class ActualTraceExporter(unittest.TestCase):
    def setUp(self):
        self.records = [json.loads(line) for line in source.read_text().splitlines()]

    def reject(self, records):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'altered.log'
            path.write_text(''.join(json.dumps(row) + '\n' for row in records))
            with self.assertRaises((ValueError, TypeError)):
                module.read_trace(path)

    def test_complete_actual_capture_preserves_all_eight_rows(self):
        rows = module.read_trace(source)
        self.assertEqual(len(rows), 8)
        self.assertEqual([r['action'] for r in rows], list(module.ACTIONS))
        self.assertTrue(all(r['expiry'] == rows[0]['expiry'] for r in rows))

    def test_requires_test_and_package_completion(self):
        for tagged in (True, False):
            self.reject([r for r in self.records if not (
                r.get('Action') == 'pass' and ('Test' in r) == tagged)])

    def test_missing_chunk_and_duplicate_actual_trace_refuse(self):
        self.reject([r for r in self.records if not (
            r.get('Action') == 'output' and 'policy_generation' in r.get('Output', ''))])
        self.reject(self.records + [r for r in self.records if
            r.get('Action') == 'output' and r.get('Test') == module.TEST])

    def test_exact_test_and_package_provenance(self):
        self.reject([{**r, 'Test': 'Other'} if r.get('Test') == module.TEST else r
                     for r in self.records])
        self.reject([{**r, 'Package': 'Other'} for r in self.records])

    def test_failure_or_skip_is_not_a_trace_pass(self):
        for action in ('fail', 'skip'):
            self.reject(self.records + [{'Action': action, 'Package': module.PACKAGE,
                                        'Test': module.TEST}])


if __name__ == '__main__':
    unittest.main(argv=[str(exporter), *remaining], verbosity=2)
