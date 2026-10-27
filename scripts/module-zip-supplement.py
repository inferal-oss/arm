#!/usr/bin/env python3
"""PROPOSAL: offline source-bound ZIP supplement; performs no network/build/install."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import re
import stat
import subprocess
import urllib.parse
import zipfile

MODULE = 'getarm.org'
REPOSITORY_URL = 'https://github.com/inferal-oss/arm'
MAX_COMPRESSED_ZIP_BYTES = 64*1024*1024
ROOTS = ('internal/server/web/', 'web/')
LEGACY = ('go.mod', 'go.sum', 'third_party/rsyncgo/LICENSE',
          'third_party/rsyncgo/ARM-CHANGES.md', 'third_party/rsyncgo/UPSTREAM-go.mod.txt',
          'third_party/rsyncgo/UPSTREAM-go.sum.txt', 'third_party/rsyncgo/rsyncstream/stream.go',
          'third_party/rsyncgo/internal/sender/sender.go',
          'third_party/rsyncgo/internal/receiver/receiver.go')
FROZEN = {'scripts/validate-module.py': 'dc2d30a670588451c221563169e8ed6f2f5f1ddab5a4fe73976ae841cfb967c7',
          'scripts/test-validate-module.py': '1c8b3f05669ba2ab6257ce61640274469a47c6fcab319874d3629238add2ce18'}

def git(repo, *args):
    return subprocess.check_output(['git', *args], cwd=repo)

def read(path):
    return json.loads(Path(path).read_text())

def sha(data):
    return hashlib.sha256(data).hexdigest()

def raw_zip_sha(path):
    digest = hashlib.sha256()
    size = 0
    with path.open('rb') as stream:
        for chunk in iter(lambda: stream.read(1024*1024), b''):
            size += len(chunk)
            if size > MAX_COMPRESSED_ZIP_BYTES:
                raise ValueError('compressed ZIP exceeds 64 MiB limit')
            digest.update(chunk)
    return digest.hexdigest()

def write_new(path, value):
    with path.open('x') as output:
        output.write(json.dumps(value, indent=2)+'\n')

def inventory(repo, ref, draft):
    commit = git(repo, 'rev-parse', ref+'^{commit}').decode().strip()
    tree = git(repo, 'rev-parse', commit+'^{tree}').decode().strip()
    entries = []
    for item in git(repo, 'ls-tree', '-rz', '--full-tree', commit).split(b'\0'):
        if not item:
            continue
        metadata, name = item.split(b'\t', 1)
        mode, kind, oid = metadata.split()
        name = name.decode()
        if name not in LEGACY and not name.startswith(ROOTS):
            continue
        if kind != b'blob' or mode not in (b'100644', b'100755'):
            raise ValueError('non-regular required input: '+name)
        if name not in ('go.mod', 'go.sum') and name.endswith('/go.mod'):
            raise ValueError('nested module in required input scope: '+name)
        entries.append((name, oid))
    entries.sort()
    raw = subprocess.check_output(['git', 'cat-file', '--batch'], cwd=repo,
                                  input=b''.join(oid+b'\n' for _, oid in entries))
    files, offset = [], 0
    for name, oid in entries:
        end = raw.index(b'\n', offset)
        actual, kind, size = raw[offset:end].split()
        size = int(size)
        if actual != oid or kind != b'blob':
            raise ValueError('unexpected Git object')
        offset = end+1
        data = raw[offset:offset+size]
        offset += size+1
        files.append({'path': name, 'bytes': size, 'sha256': sha(data)})
    if offset != len(raw) or not set(LEGACY).issubset({f['path'] for f in files}):
        raise ValueError('incomplete Git inventory')
    return {'schema_version': 1, 'draft': draft, 'source_commit': commit, 'source_tree': tree,
            'policy': {'required_roots': list(ROOTS), 'additional_required_files': list(LEGACY),
                       'excluded_required_paths': [], 'filter_vendor_paths': False}, 'files': files}

def selection(receipts):
    query = read(receipts/'public-query.stdout.txt')
    origin = read(receipts/'public-selected-version-origin.stdout.txt')
    fields = ('Path', 'Version', 'Sum', 'GoModSum')
    if query.get('Path') != MODULE or not all(query.get(k) and query[k] == origin.get(k) for k in fields):
        raise ValueError('selected version/checksums differ from original query')
    if not re.fullmatch(r'v[0-9A-Za-z.+-]+', query['Version']):
        raise ValueError('invalid selected version')
    return query, origin

def identity(repo):
    return {'commit': git(repo, 'rev-parse', 'HEAD').decode().strip(),
            'tree': git(repo, 'rev-parse', 'HEAD^{tree}').decode().strip(),
            'status': git(repo, 'status', '--porcelain', '--untracked-files=all').decode().strip()}

def verify(args):
    report = {'failures': [], 'scope': 'public ZIP supplemental source closure',
              'descendant_cleanup': 'unproven on interruption'}
    try:
        manifest = read(args.manifest)
        if manifest['draft']:
            raise ValueError('draft manifest cannot authorize final gate')
        before = identity(args.source_root)
        report['source_before'] = before
        if before != {'commit': manifest['source_commit'], 'tree': manifest['source_tree'], 'status': ''}:
            raise ValueError('source checkout differs from clean immutable manifest')
        if inventory(args.source_root, before['commit'], False) != manifest:
            raise ValueError('manifest differs from independently regenerated Git objects')
        query, origin = selection(args.receipts)
        report['actual_selection'] = {k: query[k] for k in ('Path', 'Version', 'Sum', 'GoModSum')}
        report['full_origin'] = origin.get('Origin')
        if (origin.get('Origin') or {}).get('Hash') != before['commit']:
            raise ValueError('actual selected source differs from immutable expected source')
        selected_origin = origin.get('Origin') or {}
        if selected_origin.get('URL') != REPOSITORY_URL or selected_origin.get('VCS') != 'git':
            raise ValueError('actual selected origin differs from the expected Git repository')
        expected_origin = {'Hash': before['commit'], 'URL': REPOSITORY_URL, 'VCS': 'git'}
        if any((query.get('Origin') or {}).get(key) not in (None, value) for key, value in expected_origin.items()):
            raise ValueError('query-level Origin conflicts')
        commands = read(args.receipts/'commands.json')
        by_name = {c['name']: c for c in commands}
        if len(commands) != 12 or any(c['exit'] != 0 for c in commands):
            raise ValueError('original helper commands did not all succeed')
        if by_name['public-query']['argv'][1:] != ['mod', 'download', '-json', MODULE+'@latest']:
            raise ValueError('original query was not literal latest')
        if by_name['public-install']['argv'][1:] != ['install', MODULE+'/cmd/arm@latest']:
            raise ValueError('original installation was not literal latest')
        if read(args.receipts/'result.json') != {'failures': [], 'command_count': 12, 'public_query': 'latest', 'scope': 'public-install'}:
            raise ValueError('original helper did not accept its actual run')
        helper_before = read(args.receipts/'harness-identity.json')
        if helper_before != read(args.receipts/'harness-identity-after.json') or helper_before['status'] or helper_before['files'] != FROZEN:
            raise ValueError('original frozen helper closure differs')
        if read(args.receipts/'source-harness-closure.json') != {'harness_unchanged': True, 'source_unchanged': True}:
            raise ValueError('original source/helper closure failed')
        buildinfo = (args.receipts/'installed-buildinfo.stdout.txt').read_text()
        if '\t'.join(['mod', MODULE, query['Version'], query['Sum']]) not in buildinfo:
            raise ValueError('actual installed buildinfo differs from selected version/sum')
        prefix = MODULE+'@'+query['Version']+'/'
        hashes, sizes = {}, {}
        # Bound central-directory/trailing-data parsing before ZipFile opens it.
        if args.zip.stat().st_size > MAX_COMPRESSED_ZIP_BYTES:
            raise ValueError('compressed ZIP exceeds 64 MiB limit')
        with zipfile.ZipFile(args.zip) as archive:
            infos = archive.infolist()
            if len(infos) > 100000 or sum(i.file_size for i in infos) > 500*1024*1024:
                raise ValueError('ZIP exceeds bounded inventory limits')
            for info in infos:
                name = info.filename
                relative = name[len(prefix):] if name.startswith(prefix) else ''
                if (not relative or info.is_dir() or name in hashes or '\\' in name or '\n' in name
                    or relative.startswith('/') or any(p in ('', '.', '..') for p in relative.split('/'))
                    or stat.S_IFMT(info.external_attr >> 16) not in (0, stat.S_IFREG)):
                    raise ValueError('duplicate, unsafe or non-regular ZIP entry: '+name)
                digest = hashlib.sha256()
                with archive.open(info) as stream:
                    for chunk in iter(lambda: stream.read(1024*1024), b''):
                        digest.update(chunk)
                hashes[name], sizes[name] = digest.hexdigest(), info.file_size
        # Equivalent to Go sumdb/dirhash.HashZip(..., Hash1): names retain the full module@version prefix.
        content = ''.join(hashes[name]+'  '+name+'\n' for name in sorted(hashes)).encode()
        zip_sum = 'h1:'+base64.b64encode(hashlib.sha256(content).digest()).decode()
        report['computed_go_h1'] = zip_sum
        if zip_sum != query['Sum']:
            raise ValueError('public ZIP content differs from actual selected Go Sum')
        matches = []
        for item in manifest['files']:
            name = prefix+item['path']
            if hashes.get(name) != item['sha256'] or sizes.get(name) != item['bytes']:
                raise ValueError('missing or changed required ZIP input: '+item['path'])
            matches.append(item)
        report.update(required_files=len(matches), matched_files=matches, public_zip_files=len(hashes),
                      raw_zip_sha256=raw_zip_sha(args.zip), manifest_sha256=sha(args.manifest.read_bytes()))
        after = identity(args.source_root)
        report['source_after'] = after
        if before != after:
            raise ValueError('supplement changed source identity')
    except Exception as error:
        report['failures'].append(str(error))
    write_new(args.output, report)
    return bool(report['failures'])

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    generate = sub.add_parser('manifest')
    generate.add_argument('--repo', type=Path, required=True)
    generate.add_argument('--ref', required=True)
    generate.add_argument('--final', action='store_true')
    generate.add_argument('--output', type=Path, required=True)
    check = sub.add_parser('verify')
    for name in ('manifest', 'source-root', 'receipts', 'zip', 'output'):
        check.add_argument('--'+name, type=Path, required=True)
    url = sub.add_parser('url')
    url.add_argument('--receipts', type=Path, required=True)
    args = parser.parse_args()
    if args.command == 'manifest':
        write_new(args.output, inventory(args.repo, args.ref, not args.final))
        return 0
    if args.command == 'url':
        query, _ = selection(args.receipts)
        print('https://proxy.golang.org/'+MODULE+'/@v/'+urllib.parse.quote(query['Version'], safe='')+'.zip')
        return 0
    return verify(args)

if __name__ == '__main__':
    raise SystemExit(main())
