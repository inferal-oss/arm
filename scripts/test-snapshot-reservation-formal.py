#!/usr/bin/env python3
"""Private reservation actual-cut gates, using existing Gate/original221 owner."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import stat
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MODEL = ROOT / 'formal/snapshot-reservation'


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


base = load('snapshot_formal_base', ROOT / 'scripts/test-formal.py')
projection = load('snapshot_actual_projection', MODEL / 'export_actual.py')
controls = load('snapshot_actual_controls', MODEL / 'controls_actual.py')


def complete(log, marker):
    found = re.findall(r'(\d+) distinct states found, (\d+) states left on queue', log)
    projection.require(marker in log and found and int(found[-1][0]) > 0 and int(found[-1][1]) == 0, 'nonempty complete finite exploration required')
    return int(found[-1][0])


def observed_module(states, name):
    # Every full actual-derived state and adjacency, including final, is checked
    # by TLC's original Next. Cursor cannot choose a different path or skip rows.
    fields = ','.join(projection.VARIABLES)
    equal = lambda index: ' /\\ '.join(k + '=Observed[' + index + '].' + k for k in projection.VARIABLES)
    return ('---- MODULE ' + name + ' ----\nEXTENDS SnapshotReservation, Sequences\nVARIABLE cursor\nObserved == <<' +
            ',\n'.join(base.tla_value(s) for s in states) + '>>\n'
            'TraceInit == cursor=1 /\\ Init /\\ (' + equal('1') + ')\n'
            'TraceNext == /\\ cursor<Len(Observed) /\\ cursor\'=cursor+1\n'
            '             /\\ [Next]_vars /\\ (' + ' /\\ '.join(k + "'=Observed[cursor+1]." + k for k in projection.VARIABLES) + ')\n'
            'TraceSpec == TraceInit /\\ [][TraceNext]_<<' + fields + ',cursor>>\n'
            'NotFinal == cursor#Len(Observed)\n====\n')


def retained_log(gate, run, name, expected, argv):
    # Reuse an ORIGINAL completed command, never turn an error into a new run.
    # The old failing wrapper/summary remains untouched. No process operations.
    run = Path(run).absolute()
    projection.require(run.is_relative_to(ROOT / '.tla') and not run.is_symlink(), 'retained evidence must be candidate-private')
    receipt = projection.parse((run / (name + '-owner.json')).read_bytes())
    owner = receipt['owner']
    projection.require(receipt['schema'] == 'arm.formal-native-owner/v1' and
                       receipt['source_sha256'] == projection.digest((ROOT / 'scripts/formal_process.py').read_bytes()) and
                       receipt['argv'] == [str(v) for v in argv] and receipt['cwd'] == str(ROOT) and
                       receipt['expected_exit'] == receipt['actual_exit'] == expected and
                       receipt['status'] == 'passed_native_command_only' and
                       owner['native_wait_calls'] == 1 and owner['reaped'] and owner['retired'] and not owner['authority'] and
                       owner['observed_exit'] == expected and owner['post_wait_absence'] == {'pid':'absent','group':'absent'} and
                       not owner['signals'] and owner['unknown'] is None and
                       all(receipt[k] is None for k in ('cleanup_error','cancellation_received','cancellation_pending')), 'retained original owner not clean')
    path = run / (name + '.log'); st = path.lstat(); capture = receipt['capture']
    projection.require(stat.S_ISREG(st.st_mode) and stat.S_IMODE(st.st_mode) == 0o600 and st.st_size <= 16 << 20 and
                       capture['path_matches_open_descriptor'] and
                       (st.st_dev,st.st_ino,st.st_uid,st.st_nlink,st.st_size) ==
                       tuple(capture[k] for k in ('device','inode','uid','links','bytes')), 'retained log descriptor identity differs')
    gate.summary.setdefault('retained_original_commands', []).append({'run':str(run),'name':name,'expected_exit':expected,'log_sha256':projection.digest(path.read_bytes())})
    return path.read_text()


def traces(gate):
    test = 'TestSnapshotReservationActualTransactionTrace'
    expected = {test} | {test + '/' + s for s in projection.SCENARIOS}
    env = dict(os.environ, GOMAXPROCS='2', GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off')
    argv = [gate.args.go, 'test', '-json', '-race', '-tags', 'arm_devtrace', '-count=1', '-timeout=120s', '-run', '^' + test + '$', './internal/manager']
    prior = Path(gate.args.retained_run).absolute() if gate.args.retained_run else None
    if prior:
        projection.require(all((prior / name).read_bytes() == (MODEL / name).read_bytes() for name in
                           ('SnapshotReservation.tla','Safety.cfg','Overstrong.cfg','SnapshotReservationProof.tla')), 'retained model/proof changed')
        log = retained_log(gate, prior, 'actual-go', 0, argv)
        projection.require(projection.parse((prior / 'actual-go-owner.json').read_bytes())['timeout_seconds'] == 150, 'retained Go limit differs')
    else:
        log = gate.command(argv, 'actual-go', env=env, timeout=150)
    ran, passed, output, package_pass = [], [], {}, 0
    for line in log.splitlines():
        e = projection.parse(line)
        projection.require(e.get('Package') == 'github.com/yrashk/arm/internal/manager', 'foreign Go package')
        projection.require(e.get('Action') not in ('fail','skip','build-fail'), 'Go case failed/skipped')
        if e.get('Test') is not None:
            projection.require(e['Test'] in expected, 'foreign Go root/subcase')
        if e['Action'] == 'run': ran.append(e['Test'])
        if e['Action'] == 'pass':
            if 'Test' in e: passed.append(e['Test'])
            else: package_pass += 1
        if 'Output' in e and 'Test' in e:
            output[e['Test']] = output.get(e['Test'], '') + e['Output']
    projection.require(set(ran) == set(passed) == expected and len(ran) == len(passed) == len(expected) and package_pass == 1, 'all nine actual histories must run/pass once')
    histories = {}
    for scenario in projection.SCENARIOS:
        text = output[test + '/' + scenario]
        rows = []
        for line in text.splitlines():
            if 'SNAPSHOT_RESERVATION_RAW ' in line:
                match = re.fullmatch(r'\s+snapshots_trace_dev_test.go:\d+: SNAPSHOT_RESERVATION_RAW (.+)', line)
                projection.require(match is not None, 'raw source prefix provenance')
                rows.append(projection.parse(match[1]))
        projected = projection.project(rows, scenario)
        histories[scenario] = rows
        (gate.run / (scenario + '-raw.json')).write_text(json.dumps(rows, indent=2) + '\n')
        (gate.run / (scenario + '-projection.json')).write_text(json.dumps(projected, indent=2) + '\n')
        name = 'Actual' + str(len(histories))
        (gate.run / (name + '.tla')).write_text(observed_module(projected['states'], name))
        config = gate.run / (name + '.cfg')
        config.write_text('SPECIFICATION TraceSpec\nINVARIANT Invariant\nINVARIANT NotFinal\nCHECK_DEADLOCK FALSE\n')
        witness = gate.run / (name + '-witness.json')
        if prior and (prior / (name + '-owner.json')).exists():
            projection.require((prior / (name + '.tla')).read_bytes() == (gate.run / (name + '.tla')).read_bytes() and
                               (prior / (name + '.cfg')).read_bytes() == config.read_bytes(), 'retained projected path changed')
            old_witness = prior / (name + '-witness.json')
            argv = [gate.args.java, '-Djava.io.tmpdir=' + str(prior / 'java-tmp'), '-XX:+UseParallelGC', '-Xmx512m', '-cp', gate.args.tlc_jar,
                    'tlc2.TLC','-workers','1','-fp','0','-metadir',prior / (name + '-states'),'-noGenerateSpecTE','-config',prior / (name + '.cfg'),
                    '-dumpTrace','json',old_witness,prior / (name + '.tla')]
            result = retained_log(gate, prior, name, 12, argv)
            projection.require(projection.parse((prior / (name + '-owner.json')).read_bytes())['timeout_seconds'] == 180, 'retained TLC limit differs')
            witness = old_witness
        else:
            result = gate.tlc(name + '.tla', config, name, ('-dumpTrace','json',witness), expected=12)
        projection.require('Invariant NotFinal is violated.' in result, 'complete actual path/final witness absent')
        states = base.tlc_states(witness, variables=(*projection.VARIABLES, 'cursor'), validator=lambda x: x)
        projection.require(len(states) == len(projected['states']) and all(
            set(s) == set(projection.VARIABLES) | {'cursor'} and s['cursor'] == i+1 and
            {k:s[k] for k in projection.VARIABLES} == projected['states'][i] for i,s in enumerate(states)), 'full state/edge/final mismatch')
    return {'histories': list(histories), 'alterations_rejected': controls.controls(histories, projection)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--mode', choices=('check','traces','prove','all'), default='all')
    parser.add_argument('--go', default='go')
    parser.add_argument('--retained-run', help='Reuse clean ORIGINAL Go/identical completed TLC paths after a reader-only correction; old failure stays failed')
    parser.add_argument('--java', default='java')
    parser.add_argument('--tlc-jar', default=str(ROOT / '.tla/tools/tlc/tla2tools.jar'))
    parser.add_argument('--tlapm', default=str(ROOT / '.tla/tools/tlapm/tlapm/bin/tlapm'))
    parser.add_argument('--work-dir', default=str(ROOT / '.tla/snapshot-reservation'))
    args = parser.parse_args(); args.targets = []
    os.umask(0o077)
    parent = Path(args.work_dir).absolute()
    projection.require(parent.is_relative_to(ROOT / '.tla'), 'work belongs below candidate .tla')
    current = ROOT
    for part in parent.relative_to(ROOT).parts:
        current /= part
        projection.require(not current.is_symlink(), 'work symlink refused')
        current.mkdir(mode=0o700, exist_ok=True)
    gate = base.Gate.__new__(base.Gate)
    gate.args, gate.run = args, Path(tempfile.mkdtemp(prefix='run-', dir=parent))
    gate.java_tmp = gate.run / 'java-tmp'; gate.java_tmp.mkdir(mode=0o700)
    gate.summary = {'schema':1, 'scope':'private no-work reservation actual commit cuts', 'tools':{}, 'gates':[]}
    gate.capture_runner_sources()
    for path in (Path(__file__).resolve(), MODEL / 'export_actual.py', MODEL / 'controls_actual.py'):
        gate.runner_sources[str(path)] = projection.digest(path.read_bytes())
    gate.summary['runner_sources'] = dict(gate.runner_sources)
    for path in MODEL.iterdir():
        if path.suffix in ('.tla','.cfg'): shutil.copyfile(path, gate.run / path.name)
    try:
        gate.tools()
        if args.mode in ('check','all'):
            result = gate.tlc('SnapshotReservation.tla', gate.run / 'Safety.cfg', 'safety')
            gate.summary['gates'].append({'safety_distinct':complete(result, 'Model checking completed. No error has been found.')})
            result = gate.tlc('SnapshotReservation.tla', gate.run / 'Overstrong.cfg', 'overstrong', expected=13)
            projection.require('PhysicalSourceAtDurability' in result and 'violated' in result, 'overstrong expiry control absent')
            gate.summary['gates'].append({'overstrong_physical_deadline':'rejected'})
        if args.mode in ('traces','all'):
            gate.summary['gates'].append(traces(gate))
        if args.mode in ('prove','all'):
            result = gate.command([args.tlapm, '--strict','--nofp','--threads','2','--stretch','10','--cache-dir',gate.run / 'proof-cache',gate.run / 'SnapshotReservationProof.tla'], 'proof', timeout=300)
            projection.require(re.search(r'All [1-9][0-9]* obligations proved', result) is not None and '[ERROR]' not in result, 'TLAPM complete proof receipt absent')
            gate.summary['gates'].append({'proof':'all obligations proved', 'receipt':'proof.log'})
    except BaseException as e:
        gate.summary['error'] = str(e)
        raise
    finally:
        (gate.run / 'summary.json').write_text(json.dumps(gate.summary, indent=2) + '\n')
        print(gate.run, flush=True)


if __name__ == '__main__':
    main()
