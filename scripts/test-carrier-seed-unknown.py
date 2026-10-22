#!/usr/bin/env python3
"""Real verified-SSH seed ACK loss and truncated stream; private roots only.

Wrapper preserves real OpenSSH and its -E authentication transcript. It changes
only one fixed provision-seed stream/response, never SSH trust or mesh grants.
"""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import uuid


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--arm',required=True,help='carrier containing the strict seeded reset proof')
    parser.add_argument('--ssh',required=True)
    parser.add_argument('--scenario',choices=('ack','interrupted','both'),default='both')
    options=parser.parse_args()
    arm=str(Path(options.arm).resolve())
    for scenario in (('ack','interrupted') if options.scenario=='both' else (options.scenario,)):
        root=Path(tempfile.mkdtemp(prefix='arm-seed-unknown-')).resolve();home=root/'source'
        wrappers=root/'wrappers';wrappers.mkdir(mode=0o700)
        remote='/tmp/arm-seed-unknown-'+uuid.uuid4().hex;target=remote+'/state'
        marker=root/'seed-fault';calls=root/'calls.jsonl';owner=uuid.uuid4().hex
        ssh=['/usr/bin/ssh','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UpdateHostKeys=no',options.ssh]
        def shell(command,timeout=60):
            return subprocess.run(ssh+[command],capture_output=True,text=True,timeout=timeout)
        shell('umask 077; mkdir '+shlex.quote(remote)+'; printf %s '+shlex.quote(owner)+' > '+shlex.quote(remote+'/fixture-owner')).check_returncode()
        prefix=[arm,'--home',str(home)]
        args=['mesh','connect',options.ssh,'--binary',remote+'/absent','--remote-home',target,
              '--install-root',remote+'/install','--key','seed-'+scenario+'-'+uuid.uuid4().hex,
              '--reason','Disposable exact seed unknown-outcome acceptance','--yes']
        def cli(arguments,ok=True,environment=None,timeout=180):
            r=subprocess.run(prefix+arguments,env=environment,capture_output=True,text=True,timeout=timeout)
            if ok and r.returncode:raise AssertionError((arguments,r.stdout,r.stderr))
            return r
        plan=json.loads(cli(args+['--dry-run']).stdout);key=plan['key'];binary=plan['final_binary'];seed=plan['request']['seed_catalog']
        # Only fixed known action categories are logged: no host/account/token,
        # remote command, descriptor or application payload is copied to logs.
        wrapper=r'''#!PYTHON
import base64,json,os,subprocess,sys
args=sys.argv[1:];kind=None
if 'provision-seed' in args:kind='seed'
elif 'arm-upload' in args:kind='upload'
elif 'bootstrap' in args:
 child=subprocess.Popen(['/usr/bin/ssh']+args,stdin=subprocess.PIPE)
 raw=sys.stdin.buffer.read((1<<20)+1)
 if len(raw)>(1<<20):child.kill();child.wait();sys.exit(90)
 q=json.loads(raw)
 if q.get('Action',q.get('action'))=='provision_start':
  with open(CALLS,'a') as log:log.write(json.dumps({'kind':'start'})+'\n')
 child.communicate(raw);sys.exit(child.returncode)
if kind:
 with open(CALLS,'a') as log:log.write(json.dumps({'kind':kind})+'\n')
if kind!='seed' or os.path.exists(MARKER):os.execv('/usr/bin/ssh',['/usr/bin/ssh']+args)
if SCENARIO=='ack':
 child=subprocess.Popen(['/usr/bin/ssh']+args,stdout=subprocess.PIPE)
 raw=child.stdout.read((1<<20)+1);code=child.wait()
 if code or len(raw)>(1<<20):sys.exit(code or 91)
 q=json.loads(raw)
 if q.get('phase')!='seeded_verified':sys.exit(92)
 # The successful target response is observed BEFORE dropping it. Parent
 # receives empty stdout only after actual durable publication/receipt commit.
 with open(MARKER,'x') as out:out.write('seeded_verified reply intentionally lost\n')
 sys.exit(0)
else:
 child=subprocess.Popen(['/usr/bin/ssh']+args,stdin=subprocess.PIPE,stdout=subprocess.PIPE)
 # Real main-client authentication still gates these bytes. Close only this
 # test command's stream after the gate plus part of its first gzip frame.
 sent=sys.stdin.buffer.read(256);child.stdin.write(sent);child.stdin.close()
 while sys.stdin.buffer.read(65536):pass
 raw=child.stdout.read((1<<20)+1);code=child.wait()
 if code==0:sys.exit(93)
 with open(MARKER,'x') as out:out.write('seed stream intentionally truncated\n')
 sys.stdout.buffer.write(raw);sys.exit(code)
'''.replace('PYTHON',sys.executable).replace('CALLS',repr(str(calls))).replace('MARKER',repr(str(marker))).replace('SCENARIO',repr(scenario))
        path=wrappers/'ssh';path.write_text(wrapper);path.chmod(0o700)
        env=dict(os.environ,PATH=str(wrappers)+os.pathsep+os.environ['PATH'])
        receipt_path=remote+'/install/receipt-'+key+'.json'
        catalog=target+'/mesh-companions/catalog-'+seed['manifest_sha256']
        def snapshot():
            program=r'''import hashlib,json,os,pathlib
home=pathlib.Path(HOME);catalog=pathlib.Path(CATALOG)
ep=json.loads((home/'endpoint.json').read_text());pid=ep['pid'];stat=pathlib.Path('/proc/'+str(pid)+'/stat').read_text().split(') ',1)[1].split()
def identity(p):
 s=p.stat();return [s.st_dev,s.st_ino,s.st_uid,s.st_mode,s.st_size]
files={}
if catalog.exists():
 for p in sorted(catalog.iterdir()):files[p.name]={'identity':identity(p),'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}
print(json.dumps({'pid':pid,'start_ticks':stat[19],'catalog':identity(catalog) if catalog.exists() else None,'files':files}))
'''.replace('HOME',repr(target)).replace('CATALOG',repr(catalog))
            r=shell('python3 -c '+shlex.quote(program));r.check_returncode();return json.loads(r.stdout)
        def counts():
            result={name:0 for name in ('upload','start','seed')}
            for line in calls.read_text().splitlines():result[json.loads(line)['kind']]+=1
            return result
        def record(which):return json.loads(cli(['mesh','operations',which]).stdout)
        def durable_record(which):
            # Public operation projection deliberately omits the saved Request;
            # audit immutable intent against its actual private journal too.
            records=[json.loads(p.read_text()) for p in (home/'mesh-lifecycle').glob('coordinator-*.json')]
            matches=[q for q in records if q['key']==which];assert len(matches)==1
            public=record(which);assert public['phase']==matches[0]['phase']
            return matches[0]
        success=False
        try:
            first=cli(args,ok=False,environment=env)
            assert first.returncode and 'provision_outcome_unknown' in first.stderr,(first.stdout,first.stderr)
            assert marker.exists(),'requested target seed fault was not injected'
            before=json.loads(shell('cat '+shlex.quote(receipt_path)).stdout);before_snapshot=snapshot()
            original=durable_record(key)
            assert original['phase']=='incomplete' and original['approved'] and not original.get('worker') and not original.get('resolved_by'),original
            assert counts()==dict(upload=1,start=1,seed=1),counts()
            if scenario=='interrupted':
                assert before['phase']=='seed_intent' and before['catalog_seed'].get('identity') is None and before_snapshot['catalog'] is None,before
                for _ in range(2):
                    retry=cli(args,ok=False,environment=env)
                    assert retry.returncode and 'provision_outcome_unknown' in retry.stderr,(retry.stdout,retry.stderr)
                assert durable_record(key)==original,'lookup-only incomplete retry rewrote original evidence'
                assert json.loads(shell('cat '+shlex.quote(receipt_path)).stdout)==before
                assert snapshot()==before_snapshot and counts()==dict(upload=1,start=1,seed=1)
                print('PASS interrupted seed: durable seed_intent, no published catalog, exact retries preserve uncertainty/no frame or start resend',flush=True)
                success=True
                continue
            assert before['phase']=='seeded_verified' and len(before_snapshot['files'])==5,before
            recovered=json.loads(cli(args,environment=env).stdout)
            assert recovered['connected']==before['started_host']
            resolved=durable_record(key);child_key=resolved['resolved_by'];child=durable_record(child_key)
            assert resolved['phase']=='incomplete' and child['phase']=='completed' and child['parent_key']==key and child['approved'] and not child.get('worker')
            assert child['review']['operation']=='verify-existing-provision' and child['result']['reused'],child
            for name in ('request','review','actor','result','request_sha256'):
                assert resolved.get(name)==original.get(name),'original immutable '+name+' changed'
            assert child['request']==original['request'],'read-only child changed reviewed original intent'
            operations=json.loads(cli(['mesh','operations']).stdout)
            for _ in range(2):
                again=json.loads(cli(args,environment=env).stdout);assert again['connected']==before['started_host']
            assert len(json.loads(cli(['mesh','operations']).stdout))==len(operations),'settled retry grew history'
            assert counts()==dict(upload=1,start=1,seed=1),'recovery resent frames/publication/start'
            assert snapshot()==before_snapshot,'recovery changed backend PID/native start or catalog inodes/hash'
            assert json.loads(shell('cat '+shlex.quote(receipt_path)).stdout)==before,'read-only target lookup rewrote seed receipt'
            local_catalog=home/'mesh-artifacts'
            def source_catalog():
                import hashlib
                return {p.name:(p.stat().st_dev,p.stat().st_ino,p.stat().st_mode,hashlib.sha256(p.read_bytes()).hexdigest()) for p in local_catalog.iterdir()}
            source_before=source_catalog()
            cli(['stop','--wait','--timeout','5'])
            preview=json.loads(cli(['database','remove','--dry-run','--retire-mesh','--reason','Review settled seeded unknown-outcome fixture']).stdout)
            assert preview['plan']['settled_proof']['complete'] and preview['plan']['settled_proof']['settled'],preview
            reset=json.loads(cli(['database','remove','--yes','--retire-mesh','--reason','Erase only settled private seed fixture ledger']).stdout)
            assert reset['result']['completed'] and reset['result']['old_authority']!=reset['result']['new_authority'],reset
            assert source_catalog()==source_before and snapshot()==before_snapshot,'local ledger reset changed retained source/remote catalogs'
            # Also reset the destination's LOCAL ledger after it is stopped;
            # its retained onward catalog must survive with exact native IDs.
            stopped=shell(shlex.quote(binary)+' --home '+shlex.quote(target)+' stop --wait --timeout 5');stopped.check_returncode()
            command=[binary,'--home',target,'database','remove','--retire-mesh','--reason','Retain private onward catalog while resetting only local ledger']
            remote_preview=shell(shlex.join(command+['--dry-run']));remote_preview.check_returncode()
            assert json.loads(remote_preview.stdout)['plan']['settled_proof']['settled']
            remote_reset=shell(shlex.join(command+['--yes']));remote_reset.check_returncode();assert json.loads(remote_reset.stdout)['result']['completed']
            verify=r'''import hashlib,json,pathlib
p=pathlib.Path(CATALOG);s=p.stat();files={}
for f in sorted(p.iterdir()):
 x=f.stat();files[f.name]={'identity':[x.st_dev,x.st_ino,x.st_uid,x.st_mode,x.st_size],'sha256':hashlib.sha256(f.read_bytes()).hexdigest()}
print(json.dumps({'catalog':[s.st_dev,s.st_ino,s.st_uid,s.st_mode,s.st_size],'files':files}))
'''.replace('CATALOG',repr(catalog))
            retained=shell('python3 -c '+shlex.quote(verify));retained.check_returncode();retained=json.loads(retained.stdout)
            assert retained=={k:before_snapshot[k] for k in ('catalog','files')},'destination local reset touched onward catalog'
            print('PASS seed ACK loss: immutable incomplete original + approved read-only child, no resend/restart, stable native identities/hashes, no history growth; strict preview and real local resets retain source/onward catalogs',flush=True)
            success=True
        except BaseException:
            print('Retained private seed-fault fixture:',root,remote,flush=True)
            raise
        finally:
            shell(shlex.quote(binary)+' --home '+shlex.quote(target)+' stop --wait --timeout 5')
            cli(['stop','--wait','--timeout','5'],ok=False,timeout=20)
            if success and scenario=='ack':
                cleanup=shell('test ! -e '+shlex.quote(target+'/endpoint.json')+' && test '+shlex.quote(owner)+' = "$(cat '+shlex.quote(remote+'/fixture-owner')+')" && rm -rf -- '+shlex.quote(remote));cleanup.check_returncode()
                assert not (home/'endpoint.json').exists();shutil.rmtree(root)
            elif success:
                print('Retained uncertain private stream evidence (all owned backends stopped):',root,remote,flush=True)


if __name__=='__main__':main()
