"""Synthetic offline checker tests; no public evidence, Go, network or builds."""
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
from types import SimpleNamespace
from unittest.mock import patch
import zipfile

BASE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('supplement', BASE/'module-zip-supplement.py')
supplement = importlib.util.module_from_spec(spec)
spec.loader.exec_module(supplement)
results = []

def save(path, value):
    path.write_text(json.dumps(value)+'\n')

with tempfile.TemporaryDirectory(prefix='arm-zip-boundary-fixture-') as temporary:
    root = Path(temporary)
    repo, receipts = root/'source', root/'receipts'
    repo.mkdir(); receipts.mkdir()
    git_env = {key:value for key,value in os.environ.items() if not key.startswith('GIT_')}
    git_env.update(GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull)
    def git(*args):
        return subprocess.check_output(['git', '-c', 'core.hooksPath='+os.devnull, '-c', 'commit.gpgSign=false', *args], cwd=repo, env=git_env, stderr=subprocess.DEVNULL).decode().strip()
    git('-c', 'init.templateDir=', 'init', '-q')
    files = {name: ('synthetic '+name+'\n').encode() for name in supplement.LEGACY}
    files['web/example.mjs'] = b'// synthetic offline fixture\n'
    for name, data in files.items():
        path = repo/name; path.parent.mkdir(parents=True, exist_ok=True); path.write_bytes(data)
    git('add', '.')
    git('-c', 'user.name=Offline Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-qm', 'synthetic fixture')
    manifest = supplement.inventory(repo, 'HEAD', False)
    manifest_path = root/'synthetic-manifest.json'; save(manifest_path, manifest)
    commit = manifest['source_commit']
    version = 'v0.0.0-20000101000000-'+commit[:12]
    prefix = supplement.MODULE+'@'+version+'/'
    content = ''.join(hashlib.sha256(files[name]).hexdigest()+'  '+prefix+name+'\n' for name in sorted(files)).encode()
    module_sum = 'h1:'+base64.b64encode(hashlib.sha256(content).digest()).decode()
    query = {'Path':supplement.MODULE, 'Version':version, 'Sum':module_sum, 'GoModSum':'h1:synthetic-fixture-only'}
    save(receipts/'public-query.stdout.txt', query)
    repository_origin = {'Hash':commit, 'URL':supplement.REPOSITORY_URL, 'VCS':'git'}
    origin = dict(query, Origin=repository_origin)
    save(receipts/'public-selected-version-origin.stdout.txt', origin)
    commands = [{'name':'public-query','exit':0,'argv':['go','mod','download','-json',supplement.MODULE+'@latest']},
                {'name':'public-install','exit':0,'argv':['go','install',supplement.MODULE+'/cmd/arm@latest']}]
    commands += [{'name':'synthetic-'+str(i),'exit':0,'argv':['synthetic']} for i in range(10)]
    save(receipts/'commands.json', commands)
    save(receipts/'result.json', {'failures':[], 'command_count':12,'public_query':'latest','scope':'public-install'})
    helper = {'status':'','files':supplement.FROZEN}
    for name in ('harness-identity.json','harness-identity-after.json'): save(receipts/name, helper)
    save(receipts/'source-harness-closure.json', {'harness_unchanged':True,'source_unchanged':True})
    buildinfo = '\t'.join(['mod',supplement.MODULE,version,module_sum])+'\n'
    (receipts/'installed-buildinfo.stdout.txt').write_text(buildinfo)

    def archive(name, mode, creator=3):
        path = root/(name+'.zip')
        with zipfile.ZipFile(path, 'w', compression=zipfile.ZIP_DEFLATED) as output:
            for index, (filename, data) in enumerate(sorted(files.items())):
                info = zipfile.ZipInfo(prefix+filename)
                info.create_system = creator
                info.external_attr = (mode << 16) if creator == 3 else mode
                info.compress_type = zipfile.ZIP_DEFLATED
                output.writestr(info, data)
        return path

    def check(name, path, expected_failure=None):
        output = root/(name+'.json')
        args = SimpleNamespace(manifest=manifest_path, source_root=repo, receipts=receipts, zip=path, output=output)
        code = supplement.verify(args)
        receipt = json.loads(output.read_text())
        if expected_failure is None:
            assert code == 0 and receipt['failures'] == [], receipt
        else:
            assert code == 1 and len(receipt['failures']) == 1 and expected_failure in receipt['failures'][0], receipt
        results.append({'case':name,'passed':True})
        return receipt

    ordinary = archive('regular', stat.S_IFREG|0o644)
    check('regular-file', ordinary)
    check('unspecified-unix-type', archive('unspecified', 0o644))
    check('unspecified-dos-type', archive('dos', 0x20, creator=0))
    for label, kind in [('symlink',stat.S_IFLNK),('directory-mode-without-slash',stat.S_IFDIR),
                        ('fifo',stat.S_IFIFO),('socket',stat.S_IFSOCK),
                        ('block-device',stat.S_IFBLK),('character-device',stat.S_IFCHR)]:
        check(label, archive(label,kind|0o644), 'non-regular ZIP entry')

    oversized = root/'oversized-sparse.zip'
    with oversized.open('wb') as stream: stream.truncate(supplement.MAX_COMPRESSED_ZIP_BYTES+1)
    with patch.object(supplement.zipfile, 'ZipFile', side_effect=AssertionError('ZIP parser must not run')) as parser:
        check('oversized-before-directory-parse', oversized, 'compressed ZIP exceeds 64 MiB limit')
        assert parser.call_count == 0
    exact = root/'exact-limit-sparse.zip'
    with exact.open('wb') as stream: stream.truncate(supplement.MAX_COMPRESSED_ZIP_BYTES)
    with patch.object(supplement.zipfile, 'ZipFile', side_effect=ValueError('parser reached at exact limit')) as parser:
        check('exact-limit-reaches-parser', exact, 'parser reached at exact limit')
        assert parser.call_count == 1

    expected_raw = hashlib.sha256(ordinary.read_bytes()).hexdigest()
    with patch.object(Path, 'read_bytes', side_effect=AssertionError('whole-file reads forbidden')):
        assert supplement.raw_zip_sha(ordinary) == expected_raw
    results.append({'case':'raw-hash-streams-without-read-bytes','passed':True})
    with patch.object(supplement, 'MAX_COMPRESSED_ZIP_BYTES', 8):
        try:
            supplement.raw_zip_sha(ordinary)
        except ValueError as error:
            assert 'compressed ZIP exceeds' in str(error)
        else:
            raise AssertionError('streamed byte ceiling not enforced')
    results.append({'case':'raw-hash-enforces-stream-byte-limit','passed':True})

    save(receipts/'public-selected-version-origin.stdout.txt', dict(query, Origin=dict(repository_origin, Hash='0'*40)))
    check('unchanged-full-origin-refusal', ordinary, 'actual selected source differs')
    save(receipts/'public-selected-version-origin.stdout.txt', origin)
    commands[0]['argv'][-1] = supplement.MODULE+'@'+version
    save(receipts/'commands.json', commands)
    check('unchanged-literal-query-refusal', ordinary, 'original query was not literal latest')
    commands[0]['argv'][-1] = supplement.MODULE+'@latest'
    commands[1]['argv'][-1] = supplement.MODULE+'/cmd/arm@'+version
    save(receipts/'commands.json', commands)
    check('unchanged-literal-install-refusal', ordinary, 'original installation was not literal latest')
    commands[1]['argv'][-1] = supplement.MODULE+'/cmd/arm@latest'
    save(receipts/'commands.json', commands)
    (receipts/'installed-buildinfo.stdout.txt').write_text('synthetic wrong buildinfo\n')
    check('unchanged-buildinfo-refusal', ordinary, 'actual installed buildinfo differs')
    (receipts/'installed-buildinfo.stdout.txt').write_text(buildinfo)
    for label, changed in [('wrong-repository',dict(repository_origin, URL='https://getarm.org')),
                           ('missing-repository',{k:v for k,v in repository_origin.items() if k!='URL'}),
                           ('wrong-vcs',dict(repository_origin, VCS='hg')),
                           ('missing-vcs',{k:v for k,v in repository_origin.items() if k!='VCS'})]:
        save(receipts/'public-selected-version-origin.stdout.txt', dict(query, Origin=changed))
        check(label, ordinary, 'actual selected origin differs from the expected Git repository')
    save(receipts/'public-selected-version-origin.stdout.txt', origin)
    for label, changed in [('query-wrong-repository',{'URL':'https://getarm.org'}),
                           ('query-wrong-vcs',{'VCS':'hg'})]:
        save(receipts/'public-query.stdout.txt', dict(query, Origin=changed))
        check(label, ordinary, 'query-level Origin conflicts')
    save(receipts/'public-query.stdout.txt', query)
    old_module = dict(query, Path='github.com/inferal-oss/arm')
    save(receipts/'public-query.stdout.txt', old_module)
    save(receipts/'public-selected-version-origin.stdout.txt', dict(old_module, Origin=repository_origin))
    check('old-module-cannot-borrow-provenance', ordinary, 'selected version/checksums differ')
    save(receipts/'public-query.stdout.txt', query)
    save(receipts/'public-selected-version-origin.stdout.txt', origin)
    bad_query = dict(query, Sum='h1:wrong-synthetic-checksum')
    save(receipts/'public-query.stdout.txt', bad_query)
    save(receipts/'public-selected-version-origin.stdout.txt', dict(bad_query, Origin=repository_origin))
    (receipts/'installed-buildinfo.stdout.txt').write_text('\t'.join(['mod',supplement.MODULE,version,bad_query['Sum']])+'\n')
    check('unchanged-entire-zip-h1-refusal', ordinary, 'public ZIP content differs')

report = {'synthetic_offline_checks':len(results),'passed':len(results),'cases':results,
          'not_public_gate_evidence':True,'go_network_build_install_ci_publication':False}
print(json.dumps(report,indent=2))
