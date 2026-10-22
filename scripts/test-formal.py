#!/usr/bin/env python3
"""Check the real share lifecycle, replay TLC witnesses in Go, and verify proofs.

Generated artifacts stay beneath .tla/. Missing tools, incomplete proofs, empty
traces and rejected positive replays are failures. Coverage witnesses deliberately
violate NotReached AFTER the separate exhaustive safety check succeeds.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MODEL = ROOT / 'formal' / 'share-preparation'
TARGETS = ('prepared', 'released', 'ending', 'expired', 'early_cancelled',
           'crash_running', 'reconciled', 'admission_unknown', 'worker_unknown',
           'ending_unknown', 'prepared_unknown', 'terminal_unknown', 'duplicate',
           'rollback', 'failed', 'commit_unavailable', 'restart_unknown',
           'restart_rollback_unknown')
FIELDS = {'schema': str, 'action': str, 'phase': str, 'cache_phase': str,
          'cache_revision': int, 'boundary': str, 'worker': str, 'lease': str,
          'charged': bool, 'pin': bool, 'native': str, 'launches': int,
          'join_known': bool, 'absence_proved': bool, 'skipped_proved': bool,
          'revision': int, 'slots': int, 'outcome': str, 'manifest': bool}
PREPARATION_THEOREMS = ('InitEstablishesInvariant', 'EveryStepPreservesInvariant',
    'StutterPreserves', 'StrengthenedSafety', 'Safety', *('Preserve' + action for action in (
        'Admit', 'Start', 'Prepare', 'End', 'Finish', 'NativeBegin', 'NativeJoin',
        'SkipNative', 'Crash', 'Restart', 'ObserveAbsent', 'Lookup', 'LookupUnknown',
        'Retry', 'Reject', 'CommitUnavailable')))


def module_code(text):
    """Mask nested comments and strings, retaining token positions/newlines.

    This is a closed-source scope check, not a TLA+ parser or proof checker.
    TLAPM still parses and proves the actual unchanged source bytes.
    """
    result = list(text)
    index, depth, string = 0, 0, False
    while index < len(text):
        pair = text[index:index+2]
        count = 1
        if depth:
            if pair == '(*':
                depth += 1
                count = 2
            elif pair == '*)':
                depth -= 1
                count = 2
        elif string:
            if text[index] == '\\':
                count = 2
            elif text[index] == '"':
                string = False
            elif text[index] in '\r\n':
                raise ValueError('unclosed TLA string')
        elif pair == '(*':
            depth = 1
            count = 2
        elif pair == '\\*':
            newline = text.find('\n', index)
            count = (len(text) if newline < 0 else newline) - index
        elif text[index] == '"':
            string = True
        else:
            index += 1
            continue
        for offset in range(index, min(index+count, len(text))):
            if result[offset] not in '\r\n':
                result[offset] = ' '
        index += count
    if depth or string:
        raise ValueError('unclosed TLA comment or string')
    return ''.join(result)


def validate_module_source(path, required_theorems=()):
    """Require exactly one complete top-level module and its theorem roster.

    TLAPM can successfully prove a prefix before an accidental module end while
    ignoring subsequent text. A positive obligation count alone cannot detect it.
    Nested modules and trailing declarations are outside this runner's roster.
    """
    path = Path(path)
    raw = path.read_bytes()
    if len(raw) > 1 << 20:
        raise ValueError('formal source exceeds 1MiB')
    code = module_code(raw.decode('utf-8'))
    headers = list(re.finditer(r'-{4,}\s+MODULE\s+([A-Za-z_][A-Za-z0-9_]*)\s+-{4,}', code))
    ends = list(re.finditer(r'={4,}', code))
    if (len(headers) != 1 or headers[0].group(1) != path.stem
            or code[:headers[0].start()].strip() or len(ends) != 1
            or ends[0].start() < headers[0].end() or code[ends[0].end():].strip()):
        raise ValueError('one complete named TLA module with one final terminator required: ' + path.name)
    body = code[headers[0].end():ends[0].start()]
    theorems = re.findall(r'\bTHEOREM\s+([A-Za-z_][A-Za-z0-9_]*)\b', body)
    if len(theorems) != len(set(theorems)) or not set(required_theorems) <= set(theorems):
        raise ValueError('complete unique in-module theorem roster required: ' + path.name)
    return theorems


def strict_object(pairs):
    out = {}
    for key, value in pairs:
        if key in out:
            raise ValueError('duplicate JSON field: ' + key)
        out[key] = value
    return out


def read_json(path):
    with Path(path).open('rb') as stream:
        raw = stream.read((1 << 20) + 1)
    if len(raw) > 1 << 20:
        raise ValueError('formal trace exceeds 1MiB')
    return json.loads(raw, object_pairs_hook=strict_object)


def validate_states(states):
    if not isinstance(states, list) or not 2 <= len(states) <= 128:
        raise ValueError('complete bounded nonempty trace required')
    for state in states:
        if not isinstance(state, dict) or set(state) != set(FIELDS):
            raise ValueError('unknown, missing or noncanonical state fields')
        for key, kind in FIELDS.items():
            if type(state[key]) is not kind:
                raise ValueError('wrong trace scalar type: ' + key)
            if kind is str and (len(state[key]) > 128 or not state[key].isascii()):
                raise ValueError('invalid trace text: ' + key)
            if kind is int and not 0 <= state[key] <= 10000:
                raise ValueError('invalid bounded trace integer: ' + key)
        if state['schema'] != 'arm.share-preparation/v1':
            raise ValueError('unsupported trace schema')
    return states


def tlc_states(path):
    doc = read_json(path)
    if not isinstance(doc, dict) or set(doc) != {'counterexample', 'vars'} or doc['vars'] != ['state']:
        raise ValueError('unexpected pinned TLC JSON envelope')
    ce = doc['counterexample']
    if not isinstance(ce, dict) or set(ce) != {'action', 'state'}:
        raise ValueError('unexpected TLC counterexample fields')
    states = []
    if not isinstance(ce['state'], list) or len(ce['state']) > 128:
        raise ValueError('invalid TLC state roster')
    for index, item in enumerate(ce['state'], 1):
        if not isinstance(item, list) or len(item) != 2 or item[0] != index or not isinstance(item[1], dict) or set(item[1]) != {'state'}:
            raise ValueError('noncontiguous TLC state roster')
        states.append(item[1]['state'])
    if not isinstance(ce['action'], list) or len(ce['action']) != len(states)-1:
        raise ValueError('missing TLC transition edges')
    for index, edge in enumerate(ce['action']):
        if not isinstance(edge, list) or len(edge) != 3 or edge[0] != ce['state'][index] or edge[2] != ce['state'][index+1] or not isinstance(edge[1], dict):
            raise ValueError('TLC action edge does not match exact adjacent states')
    return validate_states(states)


def envelope(states):
    validate_states(states)
    rows = [[i, {'state': state}] for i, state in enumerate(states, 1)]
    return {'vars': ['state'], 'counterexample': {'state': rows, 'action': [
        [rows[i], {'name': states[i+1]['action']}, rows[i+1]] for i in range(len(rows)-1)]}}


def tla_value(value):
    if type(value) is bool:
        return 'TRUE' if value else 'FALSE'
    if type(value) is int:
        return str(value)
    if type(value) is str:
        return json.dumps(value)
    if isinstance(value, dict):
        return '[' + ', '.join(k + ' |-> ' + tla_value(v) for k, v in value.items()) + ']'
    raise ValueError('unsupported TLA literal')


class Gate:
    def __init__(self, args):
        self.args = args
        work = Path(os.path.abspath(args.work_dir))
        allowed = ROOT / '.tla'
        if not work.is_relative_to(allowed):
            raise ValueError('formal work directory must stay beneath repository .tla/')
        current = ROOT
        for part in work.relative_to(ROOT).parts:
            current = current / part
            if current.is_symlink():
                raise ValueError('formal work path cannot contain symlinks')
            current.mkdir(mode=0o700, exist_ok=True)
        self.run = Path(tempfile.mkdtemp(prefix='run-', dir=work))
        self.java_tmp = self.run / 'java-tmp'
        self.java_tmp.mkdir(mode=0o700)
        self.summary = {'schema': 1, 'mode': args.mode, 'artifacts': str(self.run), 'targets': list(args.targets), 'gates': []}
        for source in sorted([*MODEL.glob('*.tla'), *MODEL.glob('*.cfg')]):
            shutil.copyfile(source, self.run / source.name)
        self.summary['sources'] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted(self.run.iterdir()) if p.suffix in ('.tla', '.cfg')}
        self.summary['tools'] = {}
        self.initial_state = None

    def tool(self, name, path, version_args=None):
        resolved = Path(shutil.which(str(path)) or path).resolve(strict=True)
        digest = hashlib.sha256()
        with resolved.open('rb') as stream:
            for chunk in iter(lambda: stream.read(1 << 20), b''):
                digest.update(chunk)
        receipt = {'requested': str(path), 'path': str(resolved), 'sha256': digest.hexdigest()}
        if version_args is not None:
            receipt['version'] = self.command([resolved, *version_args], name + '-version', timeout=30).strip()
        self.summary['tools'][name] = receipt

    def tools(self):
        if self.args.mode in ('check', 'traces', 'all'):
            self.tool('java', self.args.java, ['-version'])
            self.tool('tlc', self.args.tlc_jar)
            help_text = self.command([self.args.java, '-cp', self.args.tlc_jar, 'tlc2.TLC', '-help'], 'tlc-version', expected=1, timeout=30)
            self.summary['tools']['tlc']['version'] = next(line for line in help_text.splitlines() if 'Version' in line or 'TLC -' in line)
        if self.args.mode in ('traces', 'all'):
            self.tool('go', self.args.go, ['version'])
        if self.args.mode in ('prove', 'all'):
            self.tool('tlapm', self.args.tlapm, ['--version'])
            config = self.command([self.args.tlapm, '--config'], 'tlapm-config', timeout=30)
            self.summary['tools']['tlapm']['config'] = config
            # Hash the exact bundled backend reported by this prover, including
            # overrides. Never assume the repository's default binary was used.
            backend_paths = re.findall(r"PATH='([^']+)'", config)
            if not backend_paths:
                raise ValueError('cannot identify TLAPM backend search path')
            for name in ('z3', 'zenon', 'isabelle'):
                path = shutil.which(name, path=backend_paths[0])
                if path:
                    self.tool('tlapm-' + name, path, ['--version'] if name == 'z3' else None)
            if 'tlapm-z3' not in self.summary['tools']:
                raise ValueError('cannot identify actual bundled default SMT backend')

    def command(self, argv, name, expected=0, env=None, timeout=180):
        args = list(map(str, argv))
        log = self.run / (name + '.log')
        process = subprocess.Popen(args, cwd=ROOT, env=env, text=True,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   start_new_session=True)

        group_killed = False

        def signal_group(sig):
            nonlocal group_killed
            if group_killed:
                return
            try:
                os.killpg(process.pid, sig)
            except ProcessLookupError:
                pass
            if sig == signal.SIGKILL:
                group_killed = True

        cleanup_complete = False
        try:
            try:
                output, _ = process.communicate(timeout=timeout)
            except subprocess.TimeoutExpired as expired:
                # A prover or Go test can own further processes. Killing only its
                # parent leaves that work running and can keep the output pipe open.
                signal_group(signal.SIGTERM)
                try:
                    output, _ = process.communicate(timeout=0.5)
                except subprocess.TimeoutExpired:
                    signal_group(signal.SIGKILL)
                    try:
                        output, _ = process.communicate(timeout=5)
                    except subprocess.TimeoutExpired as stuck:
                        output = stuck.output or expired.output or ''
                        if isinstance(output, bytes):
                            output = output.decode(errors='replace')
                        log.write_text(output)
                        process.stdout.close()
                        try:
                            process.wait(timeout=1)
                        except subprocess.TimeoutExpired:
                            pass
                        raise RuntimeError(name + ' timed out and process-group cleanup did not finish; see ' + str(log)) from stuck
                # A descendant may close stdout yet keep running after its parent
                # exits on TERM. Finish cleanup even when communicate returned early.
                signal_group(signal.SIGKILL)
                log.write_text(output)
                cleanup_complete = True
                raise subprocess.TimeoutExpired(args, timeout, output=output) from expired
        except BaseException:
            if cleanup_complete:
                raise
            # The runner, not its isolated tool group, receives Ctrl-C/TERM.
            # Stop owned work before propagating cancellation or another error.
            signal_group(signal.SIGKILL)
            try:
                output, _ = process.communicate(timeout=5)
            except subprocess.TimeoutExpired as stuck:
                output = stuck.output or ''
                if isinstance(output, bytes):
                    output = output.decode(errors='replace')
                process.stdout.close()
                try:
                    process.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    pass
            log.write_text(output)
            raise
        log.write_text(output)
        if process.returncode != expected:
            raise RuntimeError(name + ' exited ' + str(process.returncode) + '; see ' + str(log))
        return output

    def tlc(self, module, config, name, extra=(), expected=0):
        return self.command([self.args.java, '-Djava.io.tmpdir=' + str(self.java_tmp), '-XX:+UseParallelGC', '-Xmx512m', '-cp',
            self.args.tlc_jar, 'tlc2.TLC', '-workers', '1', '-fp', '0', '-metadir',
            self.run / (name + '-states'), '-noGenerateSpecTE', '-config', config,
            *extra, self.run / module], name, expected=expected)

    def check(self):
        config = self.run / 'Safety.cfg'
        log = self.tlc('SharePreparation.tla', config, 'safety')
        explored = re.findall(r'(\d+) distinct states found, (\d+) states left on queue', log)
        if ('Model checking completed. No error has been found.' not in log or not explored
                or int(explored[-1][0]) == 0 or int(explored[-1][1]) != 0):
            raise RuntimeError('TLC did not complete nonempty exhaustive exploration')
        self.summary['gates'].append({'safety': 'passed', 'statistics': re.findall(r'.*(?:states generated|depth of the complete).*', log)})

    def witnesses(self):
        paths = []
        for target in self.args.targets:
            config = self.run / (target + '.cfg')
            config.write_text('SPECIFICATION Spec\nCHECK_DEADLOCK FALSE\nCONSTANT Target = ' + json.dumps(target) + '\nINVARIANT InductiveInvariant\nINVARIANT NotReached\n')
            output = self.run / (target + '-tlc.json')
            log = self.tlc('Witness.tla', config, target + '-witness', ('-dumpTrace', 'json', output), expected=12)
            if 'Invariant NotReached is violated.' not in log:
                raise RuntimeError('unexpected safety failure instead of requested coverage witness')
            states = tlc_states(output)
            normalized = self.run / (target + '-expected.json')
            normalized.write_text(json.dumps(states, indent=2) + '\n')
            paths.append({'target': target, 'path': str(normalized), 'output': str(self.run / (target + '-go.json'))})
        manifest = self.run / 'witnesses.json'
        manifest.write_text(json.dumps(paths, indent=2) + '\n')
        return paths, manifest

    def replay(self, states, name, accepted=True):
        validate_states(states)
        if not accepted and (self.initial_state is None or states[0] != self.initial_state):
            raise ValueError('negative replay must preserve a previously validated exact initial observation')
        module = 'Replay' + re.sub('[^A-Za-z0-9]', '', name)
        source = '---- MODULE ' + module + ' ----\nEXTENDS SharePreparation, Sequences\nVARIABLE cursor\nObserved == <<' + ',\n'.join(tla_value(s) for s in states) + '>>\n'
        source += 'ReplayInit == Init /\\ cursor = 1 /\\ state = Observed[1]\n'
        # Each row is an observed event, not an unobserved passage of time.
        # Temporal stuttering in Spec must not invent a second native entry.
        source += 'ReplayNext == /\\ cursor < Len(Observed) /\\ state\' = Observed[cursor+1] /\\ cursor\' = cursor+1 /\\ Next\n'
        source += 'NotEnd == cursor < Len(Observed)\n====\n'
        (self.run / (module + '.tla')).write_text(source)
        config = self.run / (module + '.cfg')
        config.write_text('INIT ReplayInit\nNEXT ReplayNext\nCHECK_DEADLOCK FALSE\nINVARIANT InductiveInvariant\nINVARIANT NotEnd\n')
        raw = self.run / (name + '-replay.json')
        raw.write_text(json.dumps(envelope(states)) + '\n')
        expected = 12 if accepted else 0
        log = self.tlc(module + '.tla', config, name + '-reverse', ('-loadTrace', 'json', raw), expected=expected)
        if accepted and ('Invariant NotEnd is violated.' not in log or not re.search(r'\b' + str(len(states)) + r' distinct states found', log)):
            raise RuntimeError('TLC did not validate the complete observed trace')
        if accepted:
            if self.initial_state is not None and self.initial_state != states[0]:
                raise ValueError('positive observations disagree on exact initial state')
            self.initial_state = json.loads(json.dumps(states[0]))
        if not accepted and 'Invariant NotEnd is violated.' in log:
            raise RuntimeError('corrupted trace reached its final state')
        explored = re.findall(r'(\d+) distinct states found', log)
        if not accepted and (not explored or int(explored[-1]) < 1):
            raise RuntimeError('negative replay did not explore its valid initial observation')

    def traces(self):
        self.check()
        paths, manifest = self.witnesses()
        negative_output = self.run / 'actual-negatives.json'
        env = dict(os.environ, ARM_FORMAL_WITNESSES=str(manifest), ARM_FORMAL_NEGATIVES=str(negative_output))
        private_tmp = self.run / 'tmp'
        private_tmp.mkdir(mode=0o700)
        env['TMPDIR'] = str(private_tmp)
        env.setdefault('GOPATH', str(ROOT / '.cache' / 'gopath'))
        env.setdefault('GOCACHE', str(ROOT / '.cache' / 'go-build'))
        self.command([self.args.go, 'test', '-race', '-tags', 'arm_devtrace', './internal/manager',
                      '-run', '^TestShareFormal(TLCWitnesses|ObservationNegatives|InputGuards|UnavailableSkipRetainsObligations)$', '-count=1', '-v', '-timeout=180s'],
                     'go-witnesses', env=env, timeout=210)
        observed = []
        for item in paths:
            expected = validate_states(read_json(item['path']))
            actual = validate_states(read_json(item['output']))
            if actual != expected:
                raise RuntimeError('real Go trace differs from TLC witness: ' + item['target'])
            self.replay(actual, item['target'])
            observed.extend(actual)
        negatives = []
        actual_negatives = read_json(negative_output)
        if type(actual_negatives) is not dict or set(actual_negatives) != {'actual-premature-release', 'actual-duplicate-native-entry'}:
            raise ValueError('complete actual mutation inventory required')
        for name, states in actual_negatives.items():
            self.replay(states, name, accepted=False)
            negatives.append(name)
        required = ['charged', 'pin', 'launches', 'revision', 'native-before-admit',
                    'action-reordered', 'remove-join', 'cache-unavailable', 'absence-required', 'duplicate-native-observation']
        samples = {p['target']: read_json(p['output']) for p in paths}
        for name in required:
            source = 'admission_unknown' if name == 'cache-unavailable' else 'reconciled' if name == 'absence-required' else 'prepared'
            if source not in samples:
                continue  # Explicit incomplete coverage below; never count as pass.
            corrupt = json.loads(json.dumps(samples[source]))
            if name in ('charged', 'pin', 'launches', 'revision'):
                position = next(i for i, s in enumerate(corrupt) if s['action'] == 'native_begin')
                corrupt[position][name] = {'charged': False, 'pin': False, 'launches': 2, 'revision': 99}[name]
            elif name == 'native-before-admit':
                corrupt = [corrupt[0], next(s for s in corrupt if s['action'] == 'native_begin')]
            elif name == 'action-reordered':
                corrupt[1], corrupt[2] = corrupt[2], corrupt[1]
            elif name == 'remove-join':
                corrupt = [s for s in corrupt if s['action'] != 'native_join']
            elif name == 'duplicate-native-observation':
                position = next(i for i, s in enumerate(corrupt) if s['action'] == 'native_begin')
                corrupt.insert(position+1, json.loads(json.dumps(corrupt[position])))
            elif name == 'cache-unavailable':
                corrupt[-1]['boundary'] = 'healthy'
                corrupt[-1]['cache_phase'] = corrupt[-1]['phase']
                corrupt[-1]['cache_revision'] = corrupt[-1]['revision']
            elif name == 'absence-required':
                corrupt = [s for s in corrupt if s['action'] != 'observed_absent']
            self.replay(corrupt, 'corrupt-' + name, accepted=False)
            negatives.append(name)
        missing = sorted(set(required) - set(negatives))
        complete = set(self.args.targets) == set(TARGETS)
        if complete and missing:
            raise RuntimeError('required negative coverage missing: ' + ', '.join(missing))
        self.summary['gates'].append({'traces': 'passed', 'witnesses': len(paths), 'observed_rows': len(observed), 'coverage_complete': complete,
            'negative_traces_rejected': negatives, 'negative_coverage_complete': not missing, 'negative_coverage_missing': missing,
            'normalization_guards': ['wrong-key-refused', 'wrong-authority-refused'],
            'conservative_abstraction_guards': ['unavailable-no-start-retains-obligations']})

    def prove(self):
        validate_module_source(self.run / 'SharePreparationProof.tla', PREPARATION_THEOREMS)
        # TLAPM also limits obligation preprocessing (five seconds by default).
        # Stretch all tool budgets without changing the theorem or assumptions.
        log = self.command([self.args.tlapm, '--strict', '--nofp', '--threads', '2', '--stretch', '10', '--cache-dir',
            self.run / 'proofs', '-I', self.run, self.run / 'SharePreparationProof.tla'], 'tlapm', timeout=3600)
        proved = re.search(r'All (\d+) obligations proved\.', log)
        if proved is None or int(proved.group(1)) == 0 or '[ERROR]' in log:
            raise RuntimeError('TLAPM did not discharge every proof obligation')
        self.summary['gates'].append({'proofs': 'passed', 'result': re.findall(r'.*All \d+ obligations proved.*', log)})

    def finish(self):
        (self.run / 'summary.json').write_text(json.dumps(self.summary, indent=2) + '\n')
        print(json.dumps(self.summary, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--mode', choices=('check', 'traces', 'prove', 'all'), default='all')
    parser.add_argument('--java', default='java')
    parser.add_argument('--tlc-jar', default=str(ROOT / '.tla/tools/tlc/tla2tools.jar'))
    parser.add_argument('--tlapm', default=str(ROOT / '.tla/tools/tlapm/tlapm/bin/tlapm'))
    parser.add_argument('--go', default='go')
    parser.add_argument('--work-dir', default=str(ROOT / '.tla/share-preparation'))
    parser.add_argument('--targets', nargs='+', choices=TARGETS, default=list(TARGETS), help='focused development witnesses; default requires all scenario coverage')
    args = parser.parse_args()
    if len(set(args.targets)) != len(args.targets):
        parser.error('duplicate targets')
    if args.mode != 'prove' and not Path(args.tlc_jar).is_file():
        parser.error('TLC missing: provision with make tla-tools')
    if args.mode in ('prove', 'all') and not Path(args.tlapm).is_file():
        parser.error('TLAPM missing: provision with make tla-tools')
    gate = Gate(args)
    gate.tools()
    if args.mode in ('check',):
        gate.check()
    elif args.mode in ('traces',):
        gate.traces()
    elif args.mode == 'prove':
        gate.prove()
    else:
        gate.traces()
        gate.prove()
    gate.finish()


if __name__ == '__main__':
    def terminate(signum, _frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, terminate)
    try:
        main()
    except KeyboardInterrupt:
        print('formal verification interrupted', file=sys.stderr)
        sys.exit(130)
    except (ValueError, RuntimeError, subprocess.TimeoutExpired, OSError) as error:
        print('formal verification failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
