#!/usr/bin/env python3
"""Meaningful synthetic negative controls built from actual complete Go cuts.

Raw provenance corruption must be rejected by the independent projector. The
complete-state mutations are explicitly synthetic; their exported final rows
must fail the separate TLC safety/final-row correspondence verdict. This script
runs no solver, subprocess or candidate import with process side effects.
"""
import argparse
import copy
import types
import json
from pathlib import Path

SOURCE = Path(__file__).with_name('export_actual.py')
projection = types.ModuleType('node_stop_projection')
projection.__file__ = str(SOURCE)
exec(compile(SOURCE.read_bytes(), str(SOURCE), 'exec'), projection.__dict__)


def load(directory, name):
    raw = (directory / (name + '.json')).read_bytes()
    if len(raw) > 8 << 20:
        raise ValueError('bounded raw trace required')
    return json.loads(raw, object_pairs_hook=projection.unique)


def renumber(document):
    for index, fact in enumerate(document['facts']):
        fact['sequence'] = index + 1
    return document


def rejection(name, document):
    try:
        projection.project(document)
    except (ValueError, KeyError, TypeError) as err:
        return dict(name=name, result='rejected raw provenance', reason=str(err))
    raise ValueError('raw negative control accepted: ' + name)


def append_cut(base, state, name):
    out = copy.deepcopy(base)
    out['schema'] = 'arm.node-stop.synthetic-negative/v1'
    out['scenario'] = name
    out['cuts'].append(dict(sequence=out['cuts'][-1]['sequence'] + 1,
                            kind='synthetic-negative:' + name, state=state))
    return out


def controls(raw_directory, destination):
    raw_directory, destination = Path(raw_directory), Path(destination)
    destination.mkdir(mode=0o700)
    positive = load(raw_directory, 'two-generation')
    expired = load(raw_directory, 'expired-permit')
    interrupted = load(raw_directory, 'interrupted-history')
    faulted = load(raw_directory, 'clock-fault')
    # Ensure actual positives are projectable before accepting any negative.
    observed = {name: projection.project(raw) for name, raw in (
        ('positive', positive), ('expired', expired), ('interrupted', interrupted), ('faulted', faulted))}
    receipts = []
    altered = copy.deepcopy(positive)
    fact = next(f for f in altered['facts'] if f['kind'] == 'boundary.prepare')
    fact['data']['target']['backend_generation'] = '0' * 32
    receipts.append(rejection('altered-boundary-generation', altered))
    missing = copy.deepcopy(positive)
    missing['facts'] = [f for f in missing['facts'] if f['kind'] != 'closure.write']
    receipts.append(rejection('missing-real-component-closure', renumber(missing)))
    missing_native = copy.deepcopy(positive)
    missing_native['facts'] = [f for f in missing_native['facts'] if f['kind'] != 'marker.matched']
    receipts.append(rejection('missing-original-native-marker-match', renumber(missing_native)))
    refreshed = copy.deepcopy(expired)
    before = False
    changed = False
    for f in refreshed['facts']:
        if f['kind'] == 'boundary.prepare' and not f['data']['error']:
            before = True
            continue
        if before and f['kind'].startswith('boundary.'):
            clock = f['data']['clock']
            if clock['last'] >= projection.TTL:
                digest = next(iter(clock['issued']))
                clock['issued'][digest] = clock['last']
                changed = True
                break
    projection.require(changed, 'actual expired permit cut missing')
    receipts.append(rejection('refreshed-original-issue-tick', refreshed))
    revived = copy.deepcopy(faulted)
    failed = False
    changed = False
    for f in revived['facts']:
        data = f['data']
        if not f['kind'].startswith('boundary.'):
            continue
        if failed:
            data['clock']['failure'] = ''
            changed = True
            break
        failed = bool(data['clock']['failure'])
    projection.require(changed, 'actual failed-clock retry missing')
    receipts.append(rejection('revived-failed-permit-owner', revived))
    wrong_code = copy.deepcopy(expired)
    for fact in wrong_code['facts']:
        if fact['kind'] == 'driver.outcome' and fact['data']['operation'] == 'apply':
            fact['data']['code'] = 'host_unavailable'
        if fact['kind'] == 'boundary.admit':
            fact['data']['code'] = 'host_unavailable'
    receipts.append(rejection('unrelated-refusal-code', wrong_code))
    unmatched_restart = load(raw_directory, 'restart-expired')
    failed = [f for f in unmatched_restart['facts'] if f['kind'] == 'restart.apply' and f['data']['error']]
    projection.require(len(failed) == 1, 'actual first restart Apply failure required')
    failed[0]['data']['failure_matches_return'] = False
    receipts.append(rejection('unmatched-original-restart-failure', unmatched_restart))
    (destination / 'raw-rejections.json').write_text(json.dumps(receipts, indent=2) + '\n')
    mutants = {}
    # Domain-valid retargeting still cannot match the admitted generation of
    # the previous actual prepared review. This is not merely a type failure.
    retargeted = copy.deepcopy(observed['positive'])
    admitted_cut = next(c for c in retargeted['cuts'] if c['kind'] == 'manager.accepted-commit')
    admitted_cut['state']['records']['first']['gen'] = 2
    admitted_cut['state']['records']['first']['admittedGeneration'] = 2
    mutants['retargeted-admission'] = retargeted
    # Native closure cannot be replaced with a raw inactive flag. Keep the
    # original durable ready row and later positive observation unchanged.
    unclosed = copy.deepcopy(observed['positive'])
    closed_cut = next(c for c in unclosed['cuts'] if c['kind'] == 'closure.write')
    closed_cut['state']['effects']['first'] = 0
    closed_cut['state']['effectGeneration']['first'] = 0
    closed_cut['state']['joined'] = []
    mutants['missing-joined-effect'] = unclosed
    expired_state = copy.deepcopy(observed['expired']['cuts'][-1]['state'])
    projection.require(expired_state['prepared']['present'] and not expired_state['permitFresh'], 'actual expired final prefix missing')
    stale = copy.deepcopy(expired_state)
    p = stale['prepared']
    stale['records'][p['key']] = dict(present=True, gen=p['gen'], admittedGeneration=p['gen'],
                                     phase='admitted', policy=p['policy'], admittedWithGrant=True, review=p['review'])
    stale['desiredRunning'] = False
    stale['desiredRevision'] += 1
    stale['selectedKey'] = p['key']
    stale['prepared'] = copy.deepcopy(projection.NO_PREPARED)
    stale['approved'] = False
    mutants['stale-review-admission'] = append_cut(observed['expired'], stale, 'stale-review-admission')
    revived_state = copy.deepcopy(expired_state)
    revived_state['permitFresh'] = [revived_state['prepared']['review']]
    mutants['revived-exact-review'] = append_cut(observed['expired'], revived_state, 'revived-exact-review')
    later = copy.deepcopy(observed['interrupted']['cuts'][-1]['state'])
    projection.require(later['prepared']['present'] and later['prepared']['key'] == 'second' and later['records']['first']['phase'] == 'interrupted' and later['effects']['first'] == 0, 'actual interrupted prior-duty refusal prefix missing')
    p = later['prepared']
    later['records']['second'] = dict(present=True, gen=p['gen'], admittedGeneration=p['gen'], phase='admitted', policy=p['policy'], admittedWithGrant=True, review=p['review'])
    later['desiredRunning'] = False
    later['desiredRevision'] += 1
    later['selectedKey'] = 'second'
    later['prepared'] = copy.deepcopy(projection.NO_PREPARED)
    later['approved'] = False
    mutants['overtaken-prior-stop-duty'] = append_cut(observed['interrupted'], later, 'overtaken-prior-stop-duty')
    index = []
    for name, mutant in mutants.items():
        mutant['schema'] = 'arm.node-stop.synthetic-negative/v1'
        mutant['scenario'] = name
        projection.export_projection(mutant, destination / name)
        index.append(dict(name=name, verdict='must fail strict TLC safety or fail exact final-row witness', directory=name))
    (destination / 'controls.json').write_text(json.dumps(dict(schema='arm.node-stop.correspondence-controls/v1', raw_rejections=receipts, state_controls=index), indent=2) + '\n')
    return len(receipts), len(index)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('raw_directory', type=Path)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    raw_count, state_count = controls(args.raw_directory, args.destination)
    print(json.dumps(dict(raw_rejections=raw_count, synthetic_state_controls=state_count, verdict='controls prepared; separate actual TLC verdict required')))
