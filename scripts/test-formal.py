#!/usr/bin/env python3
"""Common command ownership, strict decoding and source guards for formal suites.

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
import types

ROOT = Path(__file__).resolve().parents[1]


MODEL = None  # Each current suite supplies its own model directory.


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
    raise ValueError('suite-specific closed trace validator required')


def tlc_states(path, *, variables=None, validator=None):
    doc = read_json(path)
    expected = ['state'] if variables is None else list(variables)
    roster = doc.get('vars') if isinstance(doc, dict) else None
    valid_roster = (roster == expected) if variables is None else (
        type(roster) is list and all(type(v) is str for v in roster) and
        len(roster) == len(expected) and len(set(roster)) == len(roster) and set(roster) == set(expected))
    if not isinstance(doc, dict) or set(doc) != {'counterexample', 'vars'} or not valid_roster:
        raise ValueError('unexpected pinned TLC JSON envelope')
    ce = doc['counterexample']
    if not isinstance(ce, dict) or set(ce) != {'action', 'state'}:
        raise ValueError('unexpected TLC counterexample fields')
    states = []
    if not isinstance(ce['state'], list) or len(ce['state']) > 128:
        raise ValueError('invalid TLC state roster')
    for index, item in enumerate(ce['state'], 1):
        if not isinstance(item, list) or len(item) != 2 or type(item[0]) is not int or item[0] != index or not isinstance(item[1], dict) or set(item[1]) != set(expected):
            raise ValueError('noncontiguous TLC state roster')
        states.append(item[1]['state'] if variables is None else item[1])
    if not isinstance(ce['action'], list) or len(ce['action']) != len(states)-1:
        raise ValueError('missing TLC transition edges')
    for index, edge in enumerate(ce['action']):
        if not isinstance(edge, list) or len(edge) != 3 or json.dumps(edge[0], sort_keys=True, allow_nan=False) != json.dumps(ce['state'][index], sort_keys=True, allow_nan=False) or json.dumps(edge[2], sort_keys=True, allow_nan=False) != json.dumps(ce['state'][index+1], sort_keys=True, allow_nan=False) or not isinstance(edge[1], dict):
            raise ValueError('TLC action edge does not match exact adjacent states')
    return validate_states(states) if validator is None else validator(states)


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


def process_runtime():
    requested = Path(__file__).with_name('formal_process.py')
    if requested.is_symlink() or not requested.is_file():
        raise RuntimeError('formal process helper must be a regular source file')
    path = requested.resolve(strict=True)
    name_key = '_arm_formal_process_' + hashlib.sha256(str(path).encode()).hexdigest()
    raw = path.read_bytes()
    digest = hashlib.sha256(raw).hexdigest()
    runtime = sys.modules.get(name_key)
    if runtime is None:
        # Execute these exact captured source bytes, never an mtime/size-valid
        # __pycache__ file whose executable contents were not hash-captured.
        runtime = types.ModuleType(name_key)
        runtime.__file__ = str(path)
        sys.modules[name_key] = runtime
        try:
            exec(compile(raw, str(path), 'exec'), runtime.__dict__)
        except BaseException:
            sys.modules.pop(name_key, None)
            raise
    if Path(runtime.__file__).resolve() != path or runtime.SOURCE_SHA256 != digest:
        raise RuntimeError('formal process helper source changed')
    return runtime



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
        self.capture_runner_sources()

    def capture_runner_sources(self):
        self.runner_sources = {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in
            (Path(__file__).resolve(), Path(__file__).with_name('formal_process.py').resolve())}
        self.summary['runner_sources'] = dict(self.runner_sources)

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
        # Lazy construction also supports imported Gate.__new__/run callers.
        # The helper installs one process-wide main-thread cancellation owner;
        # importing this runner alone never changes signal/native state.
        captured = getattr(self, 'runner_sources', None)
        if captured is not None and captured != {p: hashlib.sha256(Path(p).read_bytes()).hexdigest() for p in captured}:
            raise RuntimeError('captured formal runner/helper source changed')
        runtime = process_runtime()
        if captured is not None:
            helper = str(Path(__file__).with_name('formal_process.py').resolve())
            if captured.get(helper) != runtime.SOURCE_SHA256:
                raise RuntimeError('loaded formal helper differs from initial capture')
        command = runtime.Command(self.run, ROOT)
        return command.command(argv, name, expected=expected, env=env, timeout=timeout)

    def tlc(self, module, config, name, extra=(), expected=0):
        return self.command([self.args.java, '-Djava.io.tmpdir=' + str(self.java_tmp), '-XX:+UseParallelGC', '-Xmx512m', '-cp',
            self.args.tlc_jar, 'tlc2.TLC', '-workers', '1', '-fp', '0', '-metadir',
            self.run / (name + '-states'), '-noGenerateSpecTE', '-config', config,
            *extra, self.run / module], name, expected=expected)

    def check_captured_sources(self):
        sources = self.summary.get('sources')
        if not sources:
            raise RuntimeError('captured source inventory required')
        for name, digest in sources.items():
            path = self.run / name
            if name != Path(name).name or path.is_symlink():
                raise RuntimeError('captured source identity changed: ' + name)
            with path.open('rb') as stream:
                raw = stream.read((1 << 20) + 1)
            if len(raw) > 1 << 20 or hashlib.sha256(raw).hexdigest() != digest:
                raise RuntimeError('captured source changed: ' + name)

    def finish(self):
        (self.run / 'summary.json').write_text(json.dumps(self.summary, indent=2) + '\n')
        print(json.dumps(self.summary, indent=2))


if __name__ == '__main__':
    raise SystemExit('Use a focused formal suite or make test-formal; this file is a shared library.')
