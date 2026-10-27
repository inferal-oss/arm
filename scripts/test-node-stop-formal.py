#!/usr/bin/env python3
"""Bounded NodeStop finite/proof and actual-Go correspondence gate.

All native commands use the original formal_process.py owner through the
existing Gate. No new supervisor, saved-PID signals or retry-on-unknown exists.
Exact Source8/Proof3 bytes, all14 actual cases, all7 safety checks, full24 states,
raw action edges, final-row witnesses and named negative controls are required.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import sys
import types

ROOT = Path(__file__).resolve().parents[1]
PROCESS_SHA = '221acd10f39f979c0ece89055361fcd860ab1cdd7e4ddeb816c36abbfc758b76'
MODEL_SHA = {'NodeStop.tla': 'c79ddc6481f9e7530480288ea25c8d0e5892ec449eb51f44fc5975ac4db51971',
             'Safety.cfg': 'aedaf061213d3c622bdb7530dbe2ad0ad3c0d25ec122f3f3bb8cc095316d09e4'}
PROOF_SHA = {'NodeStopInduction.tla': 'b2e75e5623baff4bac2898e1ecf5b955cb7e4a604370c834b43dd6ff00d08471',
             'NodeStopProof.tla': '92d3e3d36537b3ce64c3a04976e382902e0606116f067d6acbe80d9364e2d521',
             'Induction.cfg': '7412587f4138b64bce1ce09a54efac5756b0f67c278ea1d302a4f245d7b47f60'}
MODEL_STATES = 406992
PROOF_OBLIGATIONS = 102
PACKAGE = 'getarm.org/internal/server'
ACTUAL_TEST = 'TestNodeStopActualDevTrace'
OBSERVER_TEST = 'TestNodeStopTraceUnrelatedHomeInert'


def load_source(name, path):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('source symlink refused: ' + str(path))
    raw = path.read_bytes()
    if len(raw) > 1 << 20:
        raise ValueError('bounded runner source required')
    module = types.ModuleType(name)
    module.__file__ = str(path)
    # Execute the exact hashed source; never trust an mtime-valid pycache file.
    exec(compile(raw, str(path), 'exec'), module.__dict__)
    return module


base = load_source('node_stop_base', ROOT / 'scripts/test-formal.py')
projection = load_source('node_stop_projection', ROOT / 'formal/node-stop/export_actual.py')
comparison = load_source('node_stop_comparison', ROOT / 'formal/node-stop/compare_actual.py')
controls = load_source('node_stop_controls', ROOT / 'formal/node-stop/controls_actual.py')
SCENARIOS = projection.SCENARIOS


def bounded_json(path, cap=8 << 20):
    path = Path(path)
    if path.is_symlink():
        raise ValueError('evidence symlink refused')
    with path.open('rb') as stream:
        raw = stream.read(cap + 1)
    projection.require(len(raw) <= cap, 'bounded evidence required')
    return json.loads(raw, object_pairs_hook=projection.unique)


def digest(path):
    path = Path(path)
    projection.require(path.is_file() and not path.is_symlink(), 'regular captured source required')
    return hashlib.sha256(path.read_bytes()).hexdigest()


def go_sources():
    paths = [ROOT / 'go.mod', ROOT / 'go.sum', ROOT / 'Makefile']
    for folder in ('internal', 'cmd', 'scripts'):
        paths.extend((ROOT / folder).rglob('*.go'))
    return {str(p.relative_to(ROOT)): digest(p) for p in sorted(set(paths))}


def go_receipt(log):
    expected = {ACTUAL_TEST, OBSERVER_TEST} | {ACTUAL_TEST + '/' + name for name in SCENARIOS}
    ran, passed, packages = [], [], 0
    for line in log.splitlines():
        event = json.loads(line, object_pairs_hook=projection.unique)
        projection.require(event.get('Package') == PACKAGE, 'foreign/missing actual Go package')
        action, test = event.get('Action'), event.get('Test')
        projection.require(action not in ('fail', 'skip', 'build-fail'), 'actual Go test failed/skipped')
        if test is not None:
            projection.require(test in expected, 'foreign actual Go test')
        if action == 'run':
            ran.append(test)
        if action == 'pass':
            if test is None:
                packages += 1
            else:
                passed.append(test)
    projection.require(len(ran) == len(expected) and set(ran) == expected and len(passed) == len(expected) and set(passed) == expected and packages == 1, 'all14 cases and observer guard must actually run/pass once')
    return dict(package=PACKAGE, tests=sorted(expected), cases=list(SCENARIOS), no_skips=True)


def raw_tlc_controls(raw_path, expected, directory):
    """Retain meaningful corruptions; every call must fail the existing parser.

    Boolean/float state IDs include consistently altered duplicated edge
    endpoints. Thus rejection cannot be attributed to an unrelated edge mismatch.
    """
    directory.mkdir(mode=0o700)
    original = bounded_json(raw_path, 1 << 20)
    rows = original['counterexample']['state']
    projection.require(len(rows) >= 4, 'meaningful middle/final row controls require at least four states')
    receipts = []
    def reject(name, doc=None, text=None):
        path = directory / (name + '.json')
        path.write_text(text if text is not None else json.dumps(doc, separators=(',', ':')))
        try:
            comparison.compare(path, expected)
        except ValueError as error:
            receipts.append(dict(name=name, result='rejected', reason=str(error), sha256=digest(path)))
        else:
            raise ValueError('corrupted TLC raw trace accepted: ' + name)
    def adjacent(doc):
        states = doc['counterexample']['state']
        for index, edge in enumerate(doc['counterexample']['action']):
            edge[0] = copy.deepcopy(states[index])
            edge[2] = copy.deepcopy(states[index + 1])
        return doc
    for name, value in (('boolean-state-id', True), ('float-state-id', 1.0)):
        doc = copy.deepcopy(original)
        doc['counterexample']['state'][0][0] = value
        reject(name, adjacent(doc))
    doc = copy.deepcopy(original)
    doc['counterexample']['state'][1][1]['desiredRevision'] += 1
    reject('middle-complete-state', adjacent(doc))
    doc = copy.deepcopy(original)
    doc['counterexample']['state'][-1][1]['running'] = not doc['counterexample']['state'][-1][1]['running']
    reject('final-complete-state', adjacent(doc))
    doc = copy.deepcopy(original)
    doc['counterexample']['action'][0][2][1]['desiredRevision'] += 1
    reject('adjacent-edge-endpoint', doc)
    doc = copy.deepcopy(original)
    doc['counterexample']['state'][1][1]['position'] += 1
    reject('skipped-position', adjacent(doc))
    doc = copy.deepcopy(original)
    doc['counterexample']['state'].pop()
    doc['counterexample']['action'].pop()
    reject('premature-final-row', doc)
    doc = copy.deepcopy(original)
    del doc['counterexample']['state'][0][1]['ownedWork']
    reject('missing-original-variable', adjacent(doc))
    doc = copy.deepcopy(original)
    doc['counterexample']['state'][0][1]['desiredRevision'] = True
    reject('boolean-for-integer-state', adjacent(doc))
    text = json.dumps(original, separators=(',', ':'))
    token = '"running":true'
    projection.require(token in text, 'actual true running field required for duplicate-key control')
    reject('duplicate-json-key', text=text.replace(token, token + ',' + token, 1))
    (directory / 'receipt.json').write_text(json.dumps(receipts, indent=2) + '\n')
    return receipts


class NodeStopGate(base.Gate):
    def capture_runner_sources(self):
        super().capture_runner_sources()
        for path in (Path(__file__), ROOT / 'formal/node-stop/export_actual.py',
                     ROOT / 'formal/node-stop/compare_actual.py', ROOT / 'formal/node-stop/controls_actual.py'):
            self.runner_sources[str(path.resolve())] = digest(path)
        self.summary['runner_sources'] = dict(self.runner_sources)

    def __init__(self, args):
        projection.require(digest(ROOT / 'scripts/formal_process.py') == PROCESS_SHA, 'original221 native command owner required')
        model = Path(args.model_dir).resolve(strict=True)
        proof = Path(args.proof_dir).resolve(strict=True)
        projection.require({n: digest(model / n) for n in MODEL_SHA} == MODEL_SHA, 'unchanged authoritative Source8 model/checks required')
        projection.require({n: digest(proof / n) for n in PROOF_SHA} == PROOF_SHA, 'unchanged reviewed Proof3 full scoped proof required')
        projection.require(digest(proof / 'NodeStop.tla') == MODEL_SHA['NodeStop.tla'], 'proof and actual correspondence use different models')
        base.MODEL = model
        super().__init__(args)
        for name, sha in PROOF_SHA.items():
            shutil.copyfile(proof / name, self.run / name)
            projection.require(digest(self.run / name) == sha, 'copied proof bytes changed')
            self.summary['sources'][name] = sha
        self.summary.update(scope='Source8 finite model + Proof3 induction + all14 actual-Go cases; trusted consent/clock/scheduling inputs; CLI/network acceptance separate',
                            actual_cases=list(SCENARIOS), original_safety_checks=7,
                            source_head_checkpoint='execution caller must attach its independently captured current HEAD/worktree binding',
                            original_native_owner=PROCESS_SHA, race_requested=args.race)
        self.go_snapshot = go_sources()
        self.summary['go_source_sha256'] = self.go_snapshot

    def tlc(self, module, config, name, extra=(), expected=0):
        # Same existing Gate TLC invocation/owner, with an explicit two-CPU JVM
        # bound as well as512MiB, one worker and a finite command deadline.
        return self.command([self.args.java, '-Djava.io.tmpdir=' + str(self.java_tmp),
            '-XX:+UseParallelGC', '-XX:ActiveProcessorCount=2', '-Xmx512m', '-cp',
            self.args.tlc_jar, 'tlc2.TLC', '-workers', '1', '-fp', '0', '-metadir',
            self.run / (name + '-states'), '-noGenerateSpecTE', '-config', config,
            *extra, self.run / module], name, expected=expected,
            timeout=900 if name in ('safety', 'induction') else 180)

    def completed(self, log, count=None):
        found = re.findall(r'(\d+) distinct states found, (\d+) states left on queue', log)
        projection.require('Model checking completed. No error has been found.' in log and found and int(found[-1][1]) == 0 and int(found[-1][0]) > 0, 'complete nonempty TLC exploration required')
        if count is not None:
            projection.require(int(found[-1][0]) == count, 'TLC did not visit every exact captured position')
        return int(found[-1][0])

    def check(self):
        self.check_captured_sources()
        for module, cfg, name in (('NodeStop.tla', 'Safety.cfg', 'safety'),
                                  ('NodeStopInduction.tla', 'Induction.cfg', 'induction')):
            log = self.tlc(module, self.run / cfg, name)
            self.completed(log, MODEL_STATES)
        self.check_captured_sources()
        self.summary['gates'].append(dict(finite_safety='passed', distinct_states=MODEL_STATES, original_checks=7, strengthened_invariant='checked, not assumed'))

    def case_tlc(self, folder, name, expected, accepted):
        shutil.copyfile(self.run / 'NodeStop.tla', folder / 'NodeStop.tla')
        projection.require(digest(folder / 'NodeStop.tla') == MODEL_SHA['NodeStop.tla'], 'case model changed')
        source = folder / 'ActualNodeStop.tla'
        for cfg, witness in (('Safety.cfg', False), ('Witness.cfg', True)):
            lines = (folder / cfg).read_text().splitlines()
            invariants = [line.split()[1] for line in lines if line.startswith('INVARIANT ')]
            properties = [line.split()[1] for line in lines if line.startswith('PROPERTY ')]
            expected_inv = ['TypeOK', 'StoppedNeedsPositiveObservation', 'ClosedEffectIsGenerationBound'] + (['NeverFinal'] if witness else [])
            projection.require(invariants == expected_inv and properties == ['RestartLatchSafety', 'ContinuousReviewSafety', 'ExactReviewExpirySafety', 'HistoryFenceSafety'] and lines[:2] == ['SPECIFICATION ActualSpec', 'CHECK_DEADLOCK FALSE'] and len(lines) == 2 + len(invariants) + len(properties), 'all7 exact safety checks and sole final witness required')
        sources = {name: digest(folder / name) for name in ('ActualNodeStop.tla', 'NodeStop.tla', 'Safety.cfg', 'Witness.cfg')}
        log = self.tlc(source, folder / 'Safety.cfg', name + '-safety')
        count = self.completed(log)
        wanted = len(expected['cuts'])
        raw = folder / 'tlc-witness.json'
        witness = self.tlc(source, folder / 'Witness.cfg', name + '-witness', ('-dumpTrace', 'json', raw), expected=12 if accepted else 0)
        if accepted:
            projection.require(count == wanted and 'Invariant NeverFinal is violated.' in witness and not re.search(r'Invariant (?!NeverFinal\b)\w+ is violated\.', witness), 'only exact final-row witness may fail')
            comparison.compare(raw, expected)
        else:
            projection.require(0 < count < wanted and 'Invariant NeverFinal is violated.' not in witness, 'synthetic invalid trace reached its final captured row')
            self.completed(witness, count)
        projection.require(sources == {name: digest(folder / name) for name in sources}, 'actual case model/config changed during TLC')
        return dict(name=name, accepted=accepted, states=count, requested_states=wanted, sources=sources,
                    complete24=accepted, raw_trace_sha256=digest(raw) if raw.exists() else None)

    def traces(self):
        self.check()
        self.check_captured_sources()
        projection.require(go_sources() == self.go_snapshot, 'Go source changed before actual test')
        raw = self.run / 'actual-raw'
        tmp = self.run / 'go-tmp'
        raw.mkdir(mode=0o700)
        tmp.mkdir(mode=0o700)
        env = dict(os.environ, ARM_NODE_STOP_TRACE_DIR=str(raw), TMPDIR=str(tmp), PYTHONDONTWRITEBYTECODE='1')
        env.setdefault('GOPATH', str(ROOT / '.cache/gopath'))
        env.setdefault('GOCACHE', str(ROOT / '.cache/go-build'))
        env.setdefault('GOMAXPROCS', '2')
        self.command([self.args.go, 'test', '-count=1', './internal/nodestop', './internal/manager', './internal/mesh', './internal/server', '-run', '^$', '-timeout=300s'], 'normal-hook-build', env=env, timeout=900)
        argv = [self.args.go, 'test', '-json', '-count=1', '-tags', 'arm_devtrace']
        if self.args.race:
            argv.append('-race')
        argv += ['./internal/server', '-run', '^(' + ACTUAL_TEST + '|' + OBSERVER_TEST + ')$', '-timeout=300s']
        log = self.command(argv, 'actual-go', env=env, timeout=900)
        self.summary['actual_go_receipt'] = go_receipt(log)
        projection.require(go_sources() == self.go_snapshot, 'Go source changed during actual test')
        projection.require({p.name for p in raw.iterdir()} == {name + '.json' for name in SCENARIOS}, 'exact14 successful raw cases required; failure prefixes retained but cannot pass')
        result = []
        case_root = self.run / 'actual-cases'
        case_root.mkdir(mode=0o700)
        first_expected = first_raw = None
        for name in SCENARIOS:
            document = bounded_json(raw / (name + '.json'))
            projection.require(document['scenario'] == name, 'raw case filename/scenario binding changed')
            expected = projection.export(document, case_root / name)
            result.append(self.case_tlc(case_root / name, name, expected, True))
            if name == 'two-generation':
                first_expected, first_raw = expected, case_root / name / 'tlc-witness.json'
        control_root = self.run / 'correspondence-controls'
        raw_count, state_count = controls.controls(raw, control_root)
        projection.require((raw_count, state_count) == (7, 5), 'complete raw/state control roster required')
        control_document = bounded_json(control_root / 'controls.json')
        projection.require({r['name'] for r in control_document['raw_rejections']} == {'altered-boundary-generation', 'missing-real-component-closure', 'missing-original-native-marker-match', 'refreshed-original-issue-tick', 'revived-failed-permit-owner', 'unrelated-refusal-code', 'unmatched-original-restart-failure'}, 'exact7 raw provenance/path controls required')
        state_controls = control_document['state_controls']
        projection.require({x['name'] for x in state_controls} == {'retargeted-admission', 'missing-joined-effect', 'stale-review-admission', 'revived-exact-review', 'overtaken-prior-stop-duty'}, 'exact meaningful state control roster required')
        for control in state_controls:
            name = control['name']
            folder = control_root / name
            expected = bounded_json(folder / 'projection.json')
            result.append(self.case_tlc(folder, 'negative-' + name, expected, False))
        corruptions = raw_tlc_controls(first_raw, first_expected, self.run / 'raw-tlc-controls')
        self.check_captured_sources()
        projection.require(go_sources() == self.go_snapshot, 'actual Go sources changed after correspondence')
        self.summary['gates'].append(dict(actual_correspondence='passed', cases=result,
            all14=True, all7_safety=True, full24=True, raw_action_edges=True, exact_final_rows=True,
            observer_noninterference='selected/unrelated home real read controls passed',
            raw_provenance_controls=raw_count, state_controls=state_count,
            raw_tlc_controls=[r['name'] for r in corruptions]))

    def prove(self):
        self.check_captured_sources()
        path = self.run / 'NodeStopProof.tla'
        names = base.validate_module_source(path)
        projection.require(len(names) == 26 and names[-1] == 'FullScopedSafety', 'full scoped theorem roster required')
        summary = self.command([self.args.tlapm, '--summary', '--cache-dir', self.run / 'proof-scope', '-I', self.run, path], 'tlapm-scope', timeout=120)
        modules = re.findall(r'^---- summary of module "([^"]+)" ----$', summary, re.M)
        counts = re.findall(r'^\s*obligations_count\s*=\s*(\d+)\s*$', summary, re.M)
        projection.require(modules == ['NodeStopProof'] and counts == [str(PROOF_OBLIGATIONS)], 'complete exact102 parser obligations required')
        log = self.command([self.args.tlapm, '--strict', '--nofp', '--threads', '2', '--stretch', '20', '--cache-dir', self.run / 'proofs', '-I', self.run, path], 'tlapm', timeout=3600)
        self.check_captured_sources()
        projection.require(re.findall(r'All (\d+) obligations proved\.', log) == [str(PROOF_OBLIGATIONS)] and '[ERROR]' not in log, 'all102 exact scoped obligations must be proved')
        self.summary['gates'].append(dict(full_scoped_proof='passed', obligations=PROOF_OBLIGATIONS,
                                         safety_checks=7, backend_invocation_closure='separate independent evidence required'))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=('check', 'traces', 'prove', 'all'), default='all')
    parser.add_argument('--java', default='java')
    parser.add_argument('--tlc-jar', default=str(ROOT / '.tla/tools/tlc/tla2tools.jar'))
    parser.add_argument('--tlapm', default=str(ROOT / '.tla/tools/tlapm/tlapm/bin/tlapm'))
    parser.add_argument('--go', default='go')
    parser.add_argument('--work-dir', default=str(ROOT / '.tla/node-stop'))
    parser.add_argument('--model-dir', default=str(ROOT / 'formal/node-stop'))
    parser.add_argument('--proof-dir', default=str(ROOT / 'formal/node-stop'))
    parser.add_argument('--race', action=argparse.BooleanOptionalAction, default=True)
    args = parser.parse_args()
    args.targets = SCENARIOS
    gate = None
    def interrupted(number, _frame):
        raise SystemExit(128 + number)
    signal.signal(signal.SIGTERM, interrupted)
    try:
        gate = NodeStopGate(args)
        gate.tools()
        if args.mode == 'check':
            gate.check()
        elif args.mode == 'traces':
            gate.traces()
        elif args.mode == 'prove':
            gate.prove()
        else:
            gate.traces()
            gate.prove()
        gate.summary['status'] = 'passed'
    except BaseException as error:
        if gate is not None:
            gate.summary.update(status='failed', error=repr(error))
        raise
    finally:
        if gate is not None:
            (gate.run / 'summary.json').write_text(json.dumps(gate.summary, indent=2) + '\n')
            print('NodeStop gate receipt:', gate.run / 'summary.json', flush=True)
    return 0


if __name__ == '__main__':
    sys.exit(main())
