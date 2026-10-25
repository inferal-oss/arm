#!/usr/bin/env python3
"""Check the bounded review model, replay actual Go observations and prove inertness.

This is a one-slot conversation component, not SQL, scheduling, clock or agent
authentication verification. All native commands use the existing formal owner.
"""
import argparse
import copy
import hashlib
import importlib.util
import json
import math
from pathlib import Path
import re
import sys

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[1]


def load_source(name, path):
    if path.is_symlink() or not path.is_file():
        raise ValueError('regular source required: ' + str(path))
    module = importlib.util.module_from_spec(importlib.util.spec_from_loader(name, loader=None))
    module.__file__ = str(path)
    exec(compile(path.read_bytes(), str(path), 'exec'), module.__dict__)
    return module


base = load_source('review_accepted_formal', ROOT / 'scripts/test-formal.py')
exporter = load_source('review_actual_exporter', ROOT / 'scripts/export-lease-review-trace.py')
MODEL = ROOT / 'formal/lease-reviews'
MODEL_FILES = {'LeaseReviews.tla', 'LeaseReviewsProof.tla', 'LeaseReviewsTrace.tla', 'LeaseReviews.cfg'}
THEOREMS = ('ConversationsPreserveEntitlement', 'ConfigureAdvancesWithoutOverflow')
VARS = {'live', 'revision', 'expiry', 'enabled', 'phase', 'captured', 'acknowledged',
        'replied', 'generation', 'policyGeneration', 'capturedPolicyGeneration',
        'lastKind', 'previousLive', 'previousRevision', 'previousExpiry', 'position'}
OBSERVED = {'live': 'live', 'revision': 'revision', 'expiry': 'expiry',
            'enabled': 'enabled', 'phase': 'phase', 'policy_generation': 'policyGeneration'}
ACTIONS = ('initial', 'configure', 'checkpoint', 'acknowledge', 'reply',
           'duplicate_reply', 'ordinary_end', 'duplicate_reply')


def validate_rows(rows):
    if type(rows) is not list or len(rows) != len(ACTIONS):
        raise ValueError('all eight actual observations required')
    for row, action in zip(rows, ACTIONS):
        if type(row) is not dict or set(row) != {'action', *OBSERVED} or row['action'] != action:
            raise ValueError('closed ordered observation required')
        for k in ('live', 'enabled'):
            if type(row[k]) is not bool:
                raise ValueError('Boolean observation required')
        if type(row['revision']) is not int or not 1 <= row['revision'] <= 3:
            raise ValueError('bounded revision required')
        if type(row['policy_generation']) is not int or not 0 <= row['policy_generation'] <= 3:
            raise ValueError('bounded generation required')
        if type(row['expiry']) is not str or len(row['expiry']) > 64 or not row['expiry'].isascii():
            raise ValueError('bounded exact expiry token required')
        if row['phase'] not in ('none', 'pending', 'replied', 'timed_out', 'superseded'):
            raise ValueError('known phase required')
    return rows


def terminal_count(log, expected, violated=None):
    if violated is None:
        if 'Model checking completed. No error has been found.' not in log or 'Invariant ' in log and 'is violated.' in log:
            raise ValueError('complete successful model check required')
    elif log.count('Invariant ' + violated + ' is violated.') != 1:
        raise ValueError('exact requested witness violation required')
    errors = re.findall(r'^Error: (.*)$', log, re.M)
    permitted = [] if violated is None else ['Invariant ' + violated + ' is violated.', 'The behavior up to this point is:']
    if errors != permitted or re.search(r'(?im)(?:^|\n).*\b(?:Warning:|OutOfMemoryError|Exception in thread)', log):
        raise ValueError('solver diagnostic refuses acceptance')
    counts = re.findall(r'(\d+) distinct states found, (\d+) states left on queue', log)
    if len(counts) != 1 or int(counts[0][0]) < 1:
        raise ValueError('one nonempty terminal state statistic required')
    count, queued = map(int, counts[0])
    if violated is None and queued != 0:
        raise ValueError('model exploration is incomplete')
    if expected is not None and count != expected:
        raise ValueError('wrong complete trace length')
    return count


def compare_witness(document, rows):
    validate_rows(rows)
    if type(document) is not dict or set(document) != {'vars', 'counterexample'}:
        raise ValueError('closed TLC envelope required')
    variables = document['vars']
    if type(variables) is not list or len(variables) != len(VARS) or set(variables) != VARS:
        raise ValueError('complete TLC variable roster required')
    ce = document['counterexample']
    if type(ce) is not dict or set(ce) != {'state', 'action'} or type(ce['state']) is not list or len(ce['state']) != len(rows):
        raise ValueError('complete counterexample required')
    for position, (item, observed) in enumerate(zip(ce['state'], rows), 1):
        if type(item) is not list or len(item) != 2 or type(item[0]) is not int or item[0] != position or type(item[1]) is not dict or set(item[1]) != VARS:
            raise ValueError('closed contiguous state required')
        state = item[1]
        if type(state['position']) is not int or state['position'] != position:
            raise ValueError('state cursor differs from actual observation')
        for actual, formal in OBSERVED.items():
            if type(state[formal]) is not type(observed[actual]) or state[formal] != observed[actual]:
                raise ValueError('TLC state differs from actual Go observation: ' + actual)
        for key in VARS:
            value = state[key]
            if type(value) not in (bool, int, str) or type(value) is str and (len(value) > 64 or not value.isascii()):
                raise ValueError('bounded scalar state required')
    if type(ce['action']) is not list or len(ce['action']) != len(rows) - 1:
        raise ValueError('complete edge roster required')
    for n, edge in enumerate(ce['action']):
        if type(edge) is not list or len(edge) != 3 or edge[0] != ce['state'][n] or edge[2] != ce['state'][n + 1] or type(edge[1]) is not dict:
            raise ValueError('edge differs from adjacent actual states')
    return len(rows)


def negatives(rows):
    validate_rows(rows)
    choices = {}
    for name, index, field, value in (
        ('ack-releases-lease', 3, 'live', False),
        ('ack-revises-lease', 3, 'revision', 2),
        ('reply-without-recorded-reply', 4, 'phase', 'pending'),
        ('configure-generation-alias', 1, 'policy_generation', 2),
    ):
        changed = copy.deepcopy(rows)
        changed[index][field] = value
        if changed == rows or changed[0] != rows[0]:
            raise ValueError('negative must alter actual transition and retain original initial state')
        choices[name] = validate_rows(changed)
    return choices


def source_inventory(root):
    excluded = {'.git', '.cache', '.tla', '.tools', 'bin', 'dist', '__pycache__'}
    out = {}
    def visit(directory):
        for p in sorted(directory.iterdir()):
            if p.name in excluded:
                continue
            if p.is_symlink():
                raise ValueError('project source symlink refused: ' + str(p))
            if p.is_dir():
                visit(p)
            elif p.is_file():
                out[str(p.relative_to(root))] = hashlib.sha256(p.read_bytes()).hexdigest()
            else:
                raise ValueError('nonregular project source refused')
    visit(root)
    return out


PRODUCER_TEST = 'TestLeaseReviewDevTraceSnapshotControls'
PRODUCER_CASES = (PRODUCER_TEST, *(PRODUCER_TEST + '/' + name for name in (
    'early-actual-scheduler-commit', 'additional-real-checkpoint-history',
    'crossed-state-column', 'crossed-context-lease', 'pending-terminal-evidence',
    'unknown-state', 'unknown-record-field', 'malformed-record')))


def read_producer_controls(text):
    """Require the separate actual control invocation; never export it as a trace."""
    if type(text) is not str or len(text.encode()) > 1 << 20 or not text.endswith('\n'):
        raise ValueError('bounded complete producer-control transcript required')
    if exporter.PREFIX in text:
        raise ValueError('producer controls must not emit an actual trace')
    lines = text.splitlines()
    if not 1 <= len(lines) <= 512:
        raise ValueError('bounded producer-control record roster required')
    records = [json.loads(line, object_pairs_hook=exporter.unique) for line in lines]
    runs, passes, started, finished = [], [], set(), set()
    package_started, package_finished = False, False
    expected_passes = (*PRODUCER_CASES[1:], PRODUCER_CASES[0])
    for record in records:
        if type(record) is not dict or package_finished:
            raise ValueError('closed producer-control record before sole package completion required')
        action = record.get('Action')
        common = {'Time', 'Action', 'Package'}
        allowed = {'start': common, 'run': common | {'Test'},
                   'output': common | {'Test', 'Output', 'OutputType'},
                   'pass': common | {'Test', 'Elapsed'}}
        if type(action) is not str or action not in allowed or not common <= set(record) <= allowed[action]:
            raise ValueError('unknown producer-control event or field')
        if record['Package'] != exporter.PACKAGE or type(record['Time']) is not str or not 1 <= len(record['Time']) <= 64:
            raise ValueError('exact producer-control package and bounded timestamp required')
        name = record.get('Test')
        if 'Test' in record and (type(name) is not str or name not in PRODUCER_CASES):
            raise ValueError('foreign producer-control test')
        if action == 'start':
            if package_started:
                raise ValueError('duplicate producer-control package start')
            package_started = True
            continue
        if not package_started:
            raise ValueError('producer-control event precedes package start')
        if action == 'run':
            if name is None or name in started or len(runs) >= len(PRODUCER_CASES) or name != PRODUCER_CASES[len(runs)]:
                raise ValueError('complete ordered unique producer-control run roster required')
            if name != PRODUCER_TEST and (PRODUCER_TEST not in started or PRODUCER_TEST in finished):
                raise ValueError('producer-control child outside its actual parent')
            started.add(name)
            runs.append(name)
        elif action == 'output':
            if type(record.get('Output')) is not str or record.get('OutputType', 'frame') != 'frame':
                raise ValueError('closed producer-control output required')
            if name is not None and (name not in started or name in finished):
                raise ValueError('producer-control output outside its actual case')
        else:
            elapsed = record.get('Elapsed')
            if type(elapsed) not in (int, float) or not math.isfinite(elapsed) or elapsed < 0:
                raise ValueError('finite producer-control elapsed value required')
            if name is None:
                if tuple(runs) != PRODUCER_CASES or tuple(passes) != expected_passes:
                    raise ValueError('all nine actual controls must pass before package completion')
                package_finished = True
            else:
                if name not in started or name in finished or len(passes) >= len(expected_passes) or name != expected_passes[len(passes)]:
                    raise ValueError('complete ordered unique producer-control pass roster required')
                finished.add(name)
                passes.append(name)
    if not package_started or not package_finished or tuple(runs) != PRODUCER_CASES or tuple(passes) != expected_passes:
        raise ValueError('sole actual nine-control invocation did not complete')
    return len(runs)


def verify_proof_configuration(config, expected):
    if expected is not None and (type(expected) is not str or re.fullmatch('[0-9a-f]{64}', expected) is None):
        raise ValueError('exact proof configuration SHA256 required')
    actual = hashlib.sha256(config.encode()).hexdigest()
    if expected is not None and actual != expected:
        raise ValueError('observed proof backend configuration differs from reviewed selection')
    return actual


base.MODEL = MODEL


class ReviewGate(base.Gate):
    def __init__(self, args):
        if {p.name for p in MODEL.iterdir() if p.suffix in ('.tla', '.cfg')} != MODEL_FILES:
            raise ValueError('exact review model/proof roster required')
        self.project_sources = source_inventory(ROOT)
        super().__init__(args)
        self.runner_sources.update({str(Path(__file__).resolve()): hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                                    str(ROOT / 'scripts/export-lease-review-trace.py'): hashlib.sha256((ROOT / 'scripts/export-lease-review-trace.py').read_bytes()).hexdigest()})
        self.summary['runner_sources'] = dict(self.runner_sources)
        self.summary.update(project_sources=self.project_sources, scope='one-slot conversation inertness and exact policy-generation action; not SQL/fairness/clock/authentication proof')

    def tools(self):
        expected = self.args.expect_proof_config_sha256
        if expected is not None and re.fullmatch('[0-9a-f]{64}', expected) is None:
            raise ValueError('exact proof configuration SHA256 required before tool health checks')
        # Tool health includes --config and its own version probes. A reviewed
        # configuration must match before any theorem/SMT proof is attempted.
        super().tools()
        if self.args.mode in ('prove', 'all'):
            actual = verify_proof_configuration(self.summary['tools']['tlapm']['config'], expected)
            self.summary['proof_configuration'] = {'sha256': actual, 'reviewed_selection_required': expected is not None, 'health_probes_are_not_proofs': True}

    def verify_sources(self):
        if source_inventory(ROOT) != self.project_sources:
            raise ValueError('project input source changed')
        for name, digest in self.summary['sources'].items():
            if hashlib.sha256((self.run / name).read_bytes()).hexdigest() != digest:
                raise ValueError('captured model source changed')

    def check(self):
        log = self.tlc('LeaseReviews.tla', self.run / 'LeaseReviews.cfg', 'review-safety')
        count = terminal_count(log, None)
        self.summary['gates'].append({'finite_safety': 'passed', 'distinct_states': count})

    def replay(self, rows, name, accepted):
        validate_rows(rows)
        module = 'ReviewReplay' + re.sub('[^A-Za-z0-9]', '', name)
        text = '---- MODULE ' + module + ' ----\nEXTENDS LeaseReviewsTrace\n'
        text += 'ActualRows == <<' + ',\n'.join(base.tla_value(row) for row in rows) + '>>\n'
        text += 'CapturedExpiry == ' + json.dumps(rows[0]['expiry']) + '\nUnusedRenewedExpiry == "unused-renewal-deadline"\n====\n'
        (self.run / (module + '.tla')).write_text(text)
        common = 'CONSTANTS MaxPolicyGeneration=3\nTraceRows <- ActualRows\nInitialExpiry <- CapturedExpiry\nRenewedExpiry <- UnusedRenewedExpiry\nSPECIFICATION TraceSpec\nCHECK_DEADLOCK FALSE\n'
        if accepted:
            safety = self.run / (name + '-safety.cfg')
            safety.write_text(common + 'INVARIANT TraceSafety\n')
            log = self.tlc(module + '.tla', safety, name + '-safety')
            terminal_count(log, len(rows))
        config = self.run / (name + '-witness.cfg')
        config.write_text(common + 'INVARIANTS TraceSafety NotComplete\n')
        trace = self.run / (name + '-tlc.json')
        log = self.tlc(module + '.tla', config, name, ('-dumpTrace', 'json', trace) if accepted else (), expected=12 if accepted else 0)
        count = terminal_count(log, len(rows) if accepted else None, 'NotComplete' if accepted else None)
        if accepted:
            compare_witness(base.read_json(trace), rows)
        elif count >= len(rows):
            raise ValueError('altered actual trace reached its terminal observation')
        return count

    def traces(self):
        self.verify_sources()
        controls = self.command([self.args.go, 'test', '-race', '-tags', 'arm_devtrace', './internal/manager', '-run', '^TestLeaseReviewDevTraceSnapshotControls$', '-count=1', '-timeout=120s', '-json'], 'actual-review-producer-controls', timeout=150)
        control_count = read_producer_controls(controls)
        self.summary['gates'].append({'actual_producer_controls': 'passed', 'named_cases': control_count, 'trace_export': False})
        self.command([self.args.go, 'test', '-race', '-tags', 'arm_devtrace', './internal/manager', '-run', '^TestLeaseReviewDevTraceConversationIsInert$', '-count=1', '-timeout=120s', '-json'], 'actual-review-go', timeout=150)
        rows = validate_rows(exporter.read_trace(self.run / 'actual-review-go.log'))
        (self.run / 'actual-rows.json').write_text(json.dumps(rows, indent=2) + '\n')
        self.replay(rows, 'actual-conversation', True)
        rejected = {name: self.replay(changed, name, False) for name, changed in negatives(rows).items()}
        self.summary['gates'].append({'actual_trace': 'passed', 'observed_rows': len(rows), 'altered_histories_rejected': rejected})

    def prove(self):
        self.verify_sources()
        path = self.run / 'LeaseReviewsProof.tla'
        covered = base.validate_module_source(path, THEOREMS)
        log = self.command([self.args.tlapm, '--summary', '--cache-dir', self.run / 'parse', '-I', self.run, path], 'review-proof-scope', timeout=120)
        counts = re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$', log, re.M)
        modules = re.findall(r'^---- summary of module "([^"]+)" ----$', log, re.M)
        if modules != ['LeaseReviewsProof'] or len(counts) != 1 or int(counts[0]) < 1:
            raise ValueError('exact positive proof scope required')
        log = self.command([self.args.tlapm, '--strict', '--nofp', '--threads', '2', '--stretch', '10', '--cache-dir', self.run / 'proofs', '-I', self.run, path], 'review-proof', timeout=180)
        results = re.findall(r'All (\d+) obligations proved\.', log)
        if len(results) != 1 or results[0] != counts[0] or '[ERROR]' in log:
            raise ValueError('every parsed obligation must be proved')
        self.summary['gates'].append({'proofs': 'passed', 'theorems': covered, 'obligations': int(results[0])})


def finish_gate(gate, failure):
    # Source closure is evidence on failures too. Do not replace the original
    # native/parser/cancellation failure with a later diagnostic failure.
    try:
        gate.verify_sources()
        gate.summary['source_closure'] = {'status': 'passed'}
    except BaseException as error:
        gate.summary['source_closure'] = {'status': 'failed', 'error_type': type(error).__name__, 'message': str(error)[:1024]}
        if failure is None:
            failure = error
        else:
            failure.review_source_closure_failure = error
    gate.summary['status'] = 'failed' if failure is not None else 'passed'
    try:
        gate.finish()
    except BaseException as error:
        if failure is None:
            failure = error
        else:
            failure.review_summary_failure = error
    return failure


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=('check', 'traces', 'prove', 'all'), default='all')
    parser.add_argument('--java', default='java')
    parser.add_argument('--tlc-jar', default=str(ROOT / '.tla/tools/tlc/tla2tools.jar'))
    parser.add_argument('--tlapm', default=str(ROOT / '.tla/tools/tlapm/tlapm/bin/tlapm'))
    parser.add_argument('--go', default='go')
    parser.add_argument('--work-dir', default=str(ROOT / '.tla/lease-reviews'))
    parser.add_argument('--expect-proof-config-sha256', help='Optional reviewed exact --config output digest; mismatch refuses theorem execution. Tool health/version probes still run.')
    args = parser.parse_args()
    args.targets = []
    gate = ReviewGate(args)
    failure = None
    try:
        gate.tools()
        if args.mode in ('check', 'all'):
            gate.check()
        if args.mode in ('traces', 'all'):
            gate.traces()
        if args.mode in ('prove', 'all'):
            gate.prove()
    except BaseException as error:
        failure = error
    finally:
        failure = finish_gate(gate, failure)
    if failure is not None:
        raise failure.with_traceback(failure.__traceback__)


if __name__ == '__main__':
    main()
