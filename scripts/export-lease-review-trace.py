#!/usr/bin/env python3
"""Export one actual dev-test trace to the existing TLC model/config format.

No candidate import, process, solver or prover is started. This is not a replay
verdict. Completion requires separate strict safety and final-row witness runs.
"""
import argparse
import json
import re
import datetime
from pathlib import Path

TEST = 'TestLeaseReviewDevTraceConversationIsInert'
PACKAGE = 'github.com/inferal-oss/arm/internal/manager'
ACTIONS = ('initial', 'configure', 'checkpoint', 'acknowledge', 'reply',
           'duplicate_reply', 'ordinary_end', 'duplicate_reply')
PREFIX = 'arm-lease-review-trace '


def unique(pairs):
    out = {}
    for key, value in pairs:
        if key in out:
            raise ValueError('duplicate JSON key')
        out[key] = value
    return out


def read_trace(path):
    path = Path(path)
    if path.stat().st_size > 1 << 20:
        raise ValueError('trace transcript exceeds 1MiB')
    records = [json.loads(line, object_pairs_hook=unique) for line in path.read_text().splitlines()]
    run, passed, package, traces = [], [], [], []
    output = []
    for record in records:
        if record.get('Package') != PACKAGE:
            raise ValueError('foreign package')
        if record.get('Action') in ('fail', 'skip'):
            raise ValueError('unsuccessful actual trace test')
        if 'Test' in record and record['Test'] != TEST:
            raise ValueError('foreign test')
        if record.get('Action') == 'run':
            run.append(record.get('Test'))
        if record.get('Action') == 'pass':
            (passed if 'Test' in record else package).append(record)
        text = record.get('Output', '')
        if record.get('Action') == 'output' and record.get('Test') == TEST:
            output.append(text)
    # Go test JSON can split a long Log record into multiple output chunks.
    # Preserve every chunk byte, then admit complete bounded lines only.
    combined = ''.join(output)
    if len(combined.encode()) > 64 << 10 or (combined and not combined.endswith('\n')):
        raise ValueError('bounded complete output required')
    for text in combined.splitlines():
        if PREFIX in text:
            before, _, value = text.partition(PREFIX)
            if not before.strip().startswith('lease_reviews_trace_dev_test.go:'):
                raise ValueError('unexpected trace source')
            traces.append(json.loads(value, object_pairs_hook=unique))
    if run != [TEST] or len(passed) != 1 or len(package) != 1 or len(traces) != 1:
        raise ValueError('require sole actual run/pass/package completion and one trace')
    trace = traces[0]
    if set(trace) != {'schema', 'rows'} or trace['schema'] != 'arm.lease-review-conversation/v2':
        raise ValueError('closed trace schema required')
    rows = trace['rows']
    if type(rows) is not list or len(rows) != len(ACTIONS):
        raise ValueError('eight complete actual rows required')
    for row, action in zip(rows, ACTIONS):
        if set(row) != {'action', 'live', 'revision', 'expiry', 'phase', 'enabled', 'policy_generation'} or row['action'] != action:
            raise ValueError('unknown or reordered trace row')
        if type(row['live']) is not bool or type(row['enabled']) is not bool:
            raise ValueError('exact Boolean flags required')
        if type(row['policy_generation']) is not int or not 0 <= row['policy_generation'] <= 3:
            raise ValueError('selected trace policy generation outside bounded model')
        if type(row['revision']) is not int or not 1 <= row['revision'] <= 3:
            raise ValueError('bounded actual lease revision required')
        if type(row['expiry']) is not str or not re.fullmatch(r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z',row['expiry']):
            raise ValueError('exact captured RFC3339Nano UTC token required')
        datetime.datetime.strptime(row['expiry'][:19], '%Y-%m-%dT%H:%M:%S')
        if row['phase'] not in ('none', 'pending', 'replied', 'timed_out', 'superseded'):
            raise ValueError('unknown actual checkpoint phase')
    return rows


def export(path, destination):
    rows = read_trace(path)
    destination = Path(destination)
    destination.mkdir(mode=0o700)
    texts = []
    for row in rows:
        fields = []
        for key, value in row.items():
            text = ('TRUE' if value else 'FALSE') if type(value) is bool else (
                str(value) if type(value) is int else json.dumps(value))
            fields.append(key + ' |-> ' + text)
        texts.append('[' + ','.join(fields) + ']')
    module = ('---- MODULE ActualLeaseReview ----\nEXTENDS LeaseReviewsTrace\n'
              'ActualRows == <<' + ',\n'.join(texts) + '>>\n'
              'CapturedExpiry == ' + json.dumps(rows[0]['expiry']) + '\n'
              'UnusedRenewedExpiry == \"unused-renewal-deadline\"\n====\n')
    (destination / 'ActualLeaseReview.tla').write_text(module)
    common = ('CONSTANTS MaxPolicyGeneration=3\nTraceRows <- ActualRows\nInitialExpiry <- CapturedExpiry\n'
              'RenewedExpiry <- UnusedRenewedExpiry\nSPECIFICATION TraceSpec\n'
              'CHECK_DEADLOCK FALSE\n')
    (destination / 'safety.cfg').write_text(common + 'INVARIANT TraceSafety\n')
    (destination / 'witness.cfg').write_text(common + 'INVARIANT NotComplete\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--input', required=True)
    parser.add_argument('--out', required=True)
    args = parser.parse_args()
    export(args.input, args.out)
