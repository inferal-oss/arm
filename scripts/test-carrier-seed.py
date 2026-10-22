#!/usr/bin/env python3
"""Real carrier-only missing-node install+catalog seeding. Private roots only.

Optional onward fixture supplies its own loopback SSH client config on Linux;
no global SSH files/keys/services are modified by this test.
"""
import argparse
import json
from pathlib import Path
import shlex
import subprocess
import tempfile
import uuid


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--arm',required=True)
    parser.add_argument('--ssh',required=True)
    parser.add_argument('--onward-config',help='reviewed private Linux loopback SSH config')
    options=parser.parse_args()
    arm=str(Path(options.arm).resolve())
    root=Path(tempfile.mkdtemp(prefix='arm-carrier-seed-')).resolve()
    home=root/'source'
    remote='/tmp/arm-carrier-seed-'+uuid.uuid4().hex
    target_home=remote+'/node-a';install=remote+'/install-a'
    prefix=[arm,'--home',str(home)]
    ssh=['ssh','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UpdateHostKeys=no',options.ssh]
    def shell(command,timeout=60):return subprocess.run(ssh+[command],capture_output=True,text=True,timeout=timeout)
    def call(args,ok=True,timeout=180):
        result=subprocess.run(prefix+args,capture_output=True,text=True,timeout=timeout)
        if ok and result.returncode:raise AssertionError((args,result.stdout,result.stderr))
        return result
    def data(args):return json.loads(call(args).stdout)
    shell('umask 077; mkdir '+shlex.quote(remote)).check_returncode()
    args=['mesh','connect',options.ssh,'--binary',remote+'/absent-a','--remote-home',target_home,'--install-root',install,
          '--key','carrier-first-'+uuid.uuid4().hex,'--reason','Disposable carrier embedded catalog acceptance',
          '--grant','observe-host,observe-own,lease-own,message','--cpu','1','--memory','64MiB','--disk','1MiB','--max-ttl','1m']
    target_binary=None
    try:
        plan=json.loads(call(args+['--dry-run']).stdout)
        seed=plan['request']['seed_catalog'];assert len(seed['manifest']['artifacts'])==4
        assert not home.exists(),'embedded lookup wrote source state'
        refused=call(args,ok=False)
        assert refused.returncode and 'confirmation_required' in refused.stderr and not home.exists()
        assert shell('test ! -e '+shlex.quote(target_home)+' && test ! -e '+shlex.quote(install)).returncode==0
        connected=json.loads(call(args+['--yes']).stdout);host=connected['connected']
        operations=data(['mesh','operations']);provision=next(op for op in operations if op['kind']=='provision')
        provision=data(['mesh','operations',provision['key']])
        receipt=provision['result']['receipt'];assert receipt['phase']=='seeded_verified' and receipt['catalog_seed']['identity']
        assert receipt['catalog_seed']['request']['catalog']==seed
        assert receipt['catalog_seed']['request']['started_host']==host
        target_binary=receipt['binary']
        summary=json.loads(shell('python3 -c '+shlex.quote("import json,os;from pathlib import Path;p=Path("+repr(target_home+"/mesh-companions/catalog-"+seed['manifest_sha256'])+");print(json.dumps({'files':sorted(x.name for x in p.iterdir()),'size':os.stat("+repr(target_binary)+").st_size,'siblings':sorted(x.name for x in Path("+repr(str(Path(target_binary).parent))+").iterdir())}))")).stdout)
        assert len(summary['files'])==5 and summary['siblings']==['arm']
        assert summary['size']<Path(arm).stat().st_size/2,'destination unexpectedly contains recursive carrier payloads'
        reused=json.loads(call(args+['--yes']).stdout);assert reused['connected']==host
        assert len(data(['mesh','operations']))==len(operations),'healthy exact retry created seed/trust attempts'
        print('PASS carrier-only macOS -> Linux: zero-write lookup/consent, selected thin install, four exact seeded compressed companions, current runtime identity, exact retry',flush=True)
        print('PRIVATE_FIXTURE',json.dumps({'local_root':str(root),'remote_root':remote,'target_home':target_home,'target_binary':target_binary,'host':host}),flush=True)
        if options.onward_config:
            onward = dict(binary=target_binary, source_home=target_home, root=remote,
                          config=options.onward_config, catalog_digest=seed['manifest_sha256'], source_host=host)
            program = r'''import json,os,pathlib,shlex,subprocess,uuid
q=INPUT
root=pathlib.Path(q['root']);shims=root/'client-bin';shims.mkdir(mode=0o700)
wrapper=shims/'ssh';wrapper.write_text('#!/bin/sh\nexec /usr/bin/ssh -F '+shlex.quote(q['config'])+' "$@"\n');wrapper.chmod(0o700)
env=dict(os.environ);env['PATH']=str(shims)+':'+env['PATH'];env.pop('SSH_AUTH_SOCK',None)
prefix=[q['binary'],'--home',q['source_home']];target=str(root/'node-b');install=str(root/'install-b')
def call(args,ok=True):
 r=subprocess.run(prefix+args,capture_output=True,text=True,timeout=180,env=env)
 if ok and r.returncode:raise AssertionError((args,r.stdout,r.stderr))
 return r
args=['mesh','connect','seed-onward','--binary',str(root/'absent-b'),'--remote-home',target,'--install-root',install,'--key','carrier-onward-'+uuid.uuid4().hex,'--reason','Disposable thin-node onward catalog acceptance']
binary=None
try:
 plan=json.loads(call(args+['--dry-run']).stdout);assert plan['request']['seed_catalog']['manifest_sha256']==q['catalog_digest'];assert not pathlib.Path(target).exists() and not pathlib.Path(install).exists()
 result=json.loads(call(args+['--yes']).stdout);assert result['connected']!=q['source_host']
 operations=json.loads(call(['mesh','operations']).stdout);op=next(x for x in operations if x['kind']=='provision')
 record=json.loads(call(['mesh','operations',op['key']]).stdout);receipt=record['result']['receipt'];binary=receipt['binary'];assert receipt['phase']=='seeded_verified';assert receipt['catalog_seed']['request']['catalog']['manifest_sha256']==q['catalog_digest'];assert receipt['started_host']==result['connected']
 catalog=pathlib.Path(target)/'mesh-companions'/('catalog-'+q['catalog_digest']);assert len(list(catalog.iterdir()))==5
 assert sorted(x.name for x in pathlib.Path(binary).parent.iterdir())==['arm']
 again=json.loads(call(args+['--yes']).stdout);assert again['connected']==result['connected'];assert len(json.loads(call(['mesh','operations']).stdout))==len(operations)
 print(json.dumps({'onward':'passed','transport':'private unprivileged system OpenSSH loopback','source_host':q['source_host'],'target_host':result['connected'],'catalog_digest':q['catalog_digest']}))
finally:
 if binary:subprocess.run([binary,'--home',target,'stop','--wait','--timeout','5'],capture_output=True,timeout=20)
'''.replace('INPUT',repr(onward))
            forwarded=shell('python3 -c '+shlex.quote(program),timeout=240)
            assert forwarded.returncode==0,(forwarded.stdout,forwarded.stderr)
            print('PASS thin Linux -> distinct Linux authority via private real OpenSSH: retained catalog selected/seeded without carrier or companions; exact retry stable',flush=True)
            print(forwarded.stdout.strip(),flush=True)
    except BaseException:
        print('Retained private seed fixture:',root,remote,flush=True)
        raise
    finally:
        if target_binary:shell(shlex.quote(target_binary)+' --home '+shlex.quote(target_home)+' stop --wait --timeout 5')
        call(['stop','--wait','--timeout','5'],ok=False,timeout=15)


if __name__=='__main__':main()
