#!/usr/bin/env python3
"""Compare every full actual-Go cut with pinned TLC state and action edges.

The existing formal runner owns JSON envelope/contiguous edge validation. This
consumer supplies its optional exact 25-variable roster (the original 24 plus
trace position) and a type-sensitive complete-state validator. This reads only;
solver exit/log acceptance belongs to the command-owning formal runner.
"""
import argparse
import types
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
base = types.ModuleType('accepted_formal_runner')
source = ROOT / 'scripts/test-formal.py'
base.__file__ = str(source)
exec(compile(source.read_bytes(), str(source), 'exec'), base.__dict__)
projection = types.ModuleType('actual_node_stop_export')
source = Path(__file__).with_name('export_actual.py')
projection.__file__ = str(source)
exec(compile(source.read_bytes(), str(source), 'exec'), projection.__dict__)


def exact(left, right):
    if type(left) is not type(right):
        return False
    if type(left) is dict:
        return set(left) == set(right) and all(exact(left[k], right[k]) for k in left)
    if type(left) is list:
        return len(left) == len(right) and all(exact(a, b) for a, b in zip(left, right))
    return left == right


def normalized(state):
    result = dict(state)
    for field in ('joined', 'observed', 'permitIssued', 'permitFresh'):
        value = result[field]
        projection.require(type(value) is list and len(set(value)) == len(value), 'exact set values required')
        result[field] = sorted(value)
    return result


def compare(path, expected):
    projection.require(expected['variables'] == projection.VARIABLES and 2 <= len(expected['cuts']) <= 128, 'complete bounded expected cuts required')
    def validate(rows):
        projection.require(len(rows) == len(expected['cuts']), 'TLC did not reach every actual changed cut')
        for index, (row, cut) in enumerate(zip(rows, expected['cuts']), 1):
            projection.require(set(row) == set(projection.VARIABLES) | {'position'} and type(row['position']) is int and row['position'] == index, 'missing/reordered TLC trace position')
            actual = {k: row[k] for k in projection.VARIABLES}
            projection.require(exact(normalized(actual), normalized(cut['state'])), 'complete 24-variable TLC state mismatch at actual cut ' + str(index))
        return rows
    return base.tlc_states(path, variables=projection.VARIABLES + ['position'], validator=validate)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('tlc_trace', type=Path)
    parser.add_argument('projection', type=Path)
    args = parser.parse_args()
    projection.require(args.projection.stat().st_size <= 8 << 20, 'bounded expected projection required')
    expected = json.loads(args.projection.read_text(), object_pairs_hook=projection.unique)
    rows = compare(args.tlc_trace, expected)
    print(json.dumps(dict(states=len(rows), variables=24, adjacent_edges=len(rows) - 1, final_row=True, verdict='full raw TLC row/edge comparison passed; separate command status/log acceptance still required')))
