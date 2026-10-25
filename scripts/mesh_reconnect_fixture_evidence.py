"""Closed private fixture evidence; these facts confer no backend authority.

Only an actual trusted CLI stop can create a shutdown record. Endpoint absence
or a saved PID never grants permission to signal/kill anything. Callback PID
observations are read-only; all process ownership remains in the existing runner.
"""
from pathlib import Path
import copy
import hashlib
import json
import os
import re
import stat

EVENT_REQUIRED = frozenset(('sequence','action','input_sha256','child_started',
    'child_wait_failed','child_exit','stdout_sha256','stderr_sha256'))
EVENT_OPTIONAL = frozenset(('key','pair','fault'))
EVENT_FILE_LIMIT = 64 * (16384 + 1)  # Go's bounded JSON record plus its newline.


def strict_object(pairs):
    value={}
    for key,item in pairs:
        if key in value:raise ValueError('duplicate fixture evidence field: '+key)
        value[key]=item
    return value


def strict_json(raw):
    def invalid_constant(value):raise ValueError('non-JSON constant: '+value)
    return json.loads(raw.decode('utf-8') if isinstance(raw,bytes) else raw,
        object_pairs_hook=strict_object,parse_constant=invalid_constant)


def validate_event_row(row,previous):
    if type(row) is not dict or not EVENT_REQUIRED<=set(row) or set(row)-EVENT_REQUIRED-EVENT_OPTIONAL:
        raise ValueError('closed fixture event schema required')
    if type(row['sequence']) is not int or not previous<row['sequence']<=64:
        raise ValueError('strictly ordered fixture request sequence required')
    if type(row['action']) is not str or not 1<=len(row['action'].encode('utf-8'))<=128 or any(ord(c)<32 or ord(c)==127 for c in row['action']):
        raise ValueError('bounded fixture action required')
    for key in ('input_sha256','stdout_sha256','stderr_sha256'):
        if type(row[key]) is not str or re.fullmatch('[0-9a-f]{64}',row[key]) is None:
            raise ValueError('exact fixture evidence digest required')
    if any(type(row[key]) is not bool for key in ('child_started','child_wait_failed')):
        raise ValueError('exact native fixture boolean facts required')
    if type(row['child_exit']) is not int or not -1<=row['child_exit']<=255:
        raise ValueError('native fixture exit required')
    if 'key' in row and (type(row['key']) is not str or not 1<=len(row['key'].encode('utf-8'))<=128 or any(ord(c)<32 or ord(c)==127 for c in row['key'])):
        raise ValueError('bounded optional fixture key required')
    if 'pair' in row and (type(row['pair']) is not str or re.fullmatch('[0-9a-f]{64}',row['pair']) is None):
        raise ValueError('exact optional fixture pair required')
    if 'fault' in row and row['fault'] not in ('before','after'):
        raise ValueError('exact optional fixture fault required')
    return row['sequence']


def decode_fixture_events(raw):
    if type(raw) is not bytes or len(raw)>EVENT_FILE_LIMIT:
        raise ValueError('bounded raw fixture events required')
    if not raw:return []
    if not raw.endswith(b'\n'):raise ValueError('complete newline-terminated fixture events required')
    lines=raw[:-1].split(b'\n')
    if len(lines)>64:raise ValueError('fixture event roster exceeds64')
    rows=[];previous=0
    for line in lines:
        if not 0<len(line)<=16384:raise ValueError('bounded nonempty fixture event required')
        row=strict_json(line)
        previous=validate_event_row(row,previous)
        rows.append(row)
    return rows


def read_fixture_events(path):
    with Path(path).open('rb') as stream:raw=stream.read(EVENT_FILE_LIMIT+1)
    return decode_fixture_events(raw)


def committed_revoke_fault(events,key):
    if type(key) is not str or not 1<=len(key.encode('utf-8'))<=128:raise ValueError('exact expected fixture key required')
    if type(events) is not list or len(events)>64:raise ValueError('bounded decoded fixture event roster required')
    previous=0
    for row in events:previous=validate_event_row(row,previous)
    # Select by optional key first so a duplicate/wrong-action record under the
    # expected key cannot be ignored in favour of another apparently good row.
    matches=[row for row in events if row.get('key')==key]
    if len(matches)!=1:raise AssertionError('one exact keyed fixture fault event required')
    row=matches[0]
    if row.get('action')!='lifecycle_revoke' or row.get('fault')!='after' or row.get('child_started') is not True or row.get('child_wait_failed') is not False or row.get('child_exit')!=0:
        raise AssertionError('exact committed revoke/after/native-success evidence required')
    return row


def read_fixture_endpoint(home):
    path=Path(home)/'endpoint.json'
    try:before=path.lstat()
    except FileNotFoundError:return None
    if not stat.S_ISREG(before.st_mode) or before.st_uid!=os.getuid() or stat.S_IMODE(before.st_mode)&0o077:
        raise ValueError('private owned endpoint evidence required')
    flags=os.O_RDONLY|getattr(os,'O_NOFOLLOW',0)
    with os.fdopen(os.open(path,flags),'rb') as stream:
        current=os.fstat(stream.fileno())
        if not os.path.samestat(before,current):raise ValueError('endpoint identity changed during read')
        raw=stream.read(16385)
    if len(raw)>16384:raise ValueError('bounded fixture endpoint required')
    return raw


class BackendShutdownEvidence:
    """In-memory lifetime evidence of this one dedicated disposable harness."""
    def __init__(self):
        self.authorities={};self.stops={}

    def observe(self,home,raw):
        if type(raw) is not bytes or len(raw)>16384:raise ValueError('bounded raw endpoint required')
        value=strict_json(raw)
        if type(value) is not dict or set(value)!={'url','pid','token'} or type(value['pid']) is not int or value['pid']<=1 or value['pid']>2**31-1 or type(value['url']) is not str or not 1<=len(value['url'])<=256 or type(value['token']) is not str or not 1<=len(value['token'])<=256:
            raise ValueError('exact bounded endpoint authority required')
        authority={'home':str(home),'pid':value['pid'],
            'endpoint_sha256':hashlib.sha256(raw).hexdigest()}
        # Even reappearance of the same bytes after a positive stop is not
        # permission to reuse that stopped lifetime's receipt.
        self.stops.pop(str(home),None)
        self.authorities[str(home)]=authority
        return dict(authority)

    def selected(self,home):
        value=self.authorities.get(str(home))
        return dict(value) if value is not None else None

    def record_stop(self,home,before,returncode,stdout,endpoint_after):
        home=str(home)
        self.stops.pop(home,None)
        if before is None or self.authorities.get(home)!=before:
            raise ValueError('stop must bind the exact observed endpoint generation')
        if endpoint_after is not None:
            self.observe(home,endpoint_after)
            raise ValueError('endpoint remains/replaced after stop; old receipt ineligible')
        if type(returncode) is not int or returncode!=0 or type(stdout) is not str or len(stdout.encode('utf-8'))>1<<20:
            raise ValueError('successful bounded trusted CLI stop result required')
        result=strict_json(stdout)
        if type(result) is not dict or result.get('stopped') is not True or result.get('phase')!='stopped' or type(result.get('id')) is not str or re.fullmatch('[0-9a-f]{32}',result['id']) is None:
            raise ValueError('positive stopped/phase/report-ID result required')
        record={'home':home,'pid':before['pid'],'endpoint_sha256':before['endpoint_sha256'],
            'returncode':returncode,'report_id':result['id'],
            'stdout_sha256':hashlib.sha256(stdout.encode('utf-8')).hexdigest(),
            'result':result}
        self.stops[home]=record
        return copy.deepcopy(record)

    def pending_reuse(self,home,current_endpoint):
        """Review exact stopped-lifetime evidence without claiming PID closure."""
        home=str(home)
        if current_endpoint is not None:
            self.observe(home,current_endpoint)
            return None  # A current endpoint needs its own authorized stop.
        record=self.stops.get(home);current=self.authorities.get(home)
        if record is None or current is None or any(record[key]!=current[key] for key in ('home','pid','endpoint_sha256')):
            raise ValueError('endpoint absence without positive exact-generation shutdown is unknown')
        return copy.deepcopy(record)

    def reuse(self,home,current_endpoint,pid_absent):
        record=self.pending_reuse(home,current_endpoint)
        if record is None:return None
        if pid_absent(record['pid']) is not True:
            raise ValueError('positive stop receipt has no independent saved-PID absence observation')
        return copy.deepcopy(dict(record,independent_pid_absence_observed=True))
