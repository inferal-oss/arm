#!/usr/bin/env python3
"""Lose real verified-SSH publication/start acknowledgments, never replay work."""
import argparse
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import uuid


def main():
    p=argparse.ArgumentParser()
    p.add_argument('--arm',required=True)
    p.add_argument('--artifact',required=True)
    p.add_argument('--ssh',required=True)
    o=p.parse_args()
    arm=str(Path(o.arm).resolve()); artifact=str(Path(o.artifact).resolve())
    real_ssh='/usr/bin/ssh'
    for loss in ('publication','start'):
        root=Path(tempfile.mkdtemp(prefix='arm-provision-unknown-')).resolve()
        remote='/tmp/arm-provision-unknown-'+uuid.uuid4().hex
        target_home=remote+'/state'; home=root/'source'; wrappers=root/'wrappers';wrappers.mkdir(mode=0o700)
        ssh=[real_ssh,'-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UpdateHostKeys=no',o.ssh]
        def shell(cmd):return subprocess.run(ssh+[cmd],capture_output=True,text=True,timeout=30)
        shell('umask 077; mkdir '+shlex.quote(remote)).check_returncode()
        prefix=[arm,'--home',str(home)]
        args=['mesh','connect',o.ssh,'--binary',remote+'/absent-arm','--remote-home',target_home,
              '--install-root',remote+'/install','--artifact',artifact,'--key','lost-'+uuid.uuid4().hex,
              '--reason','Disposable exact unknown-outcome acceptance','--yes']
        def run(extra=(),env=None):return subprocess.run(prefix+args+list(extra),env=env,capture_output=True,text=True,timeout=120)
        plan=json.loads(run(['--dry-run']).stdout);binary=plan['final_binary'];key=plan['key']
        succeeded=False
        token=uuid.uuid4().hex
        (root/'fixture-owner').write_text(token)
        shell('printf %s '+shlex.quote(token)+' > '+shlex.quote(remote+'/fixture-owner')).check_returncode()
        marker=wrappers/'dropped'
        # The wrapper changes neither trust nor encrypted SSH bytes. It runs
        # real OpenSSH and discards ONE complete response only after target
        # work committed. Main-client -E identity capture remains real OpenSSH.
        wrapper='#!/bin/sh\nset -eu\nmatched=false\nfor arg in "$@"; do\n'
        if loss=='publication':wrapper+=' test "$arg" != arm-upload || matched=true\n'
        else:wrapper+=' case "$arg" in '+shlex.quote(remote)+'/install/artifact-*/arm) matched=true;; esac\n'
        wrapper+='done\nif test "$matched" = true && ! test -e '+shlex.quote(str(marker))+'; then\n : > '+shlex.quote(str(marker))+'\n exec '+real_ssh+' "$@" > /dev/null\nfi\nexec '+real_ssh+' "$@"\n'
        (wrappers/'ssh').write_text(wrapper);(wrappers/'ssh').chmod(0o700)
        env=dict(os.environ,PATH=str(wrappers)+os.pathsep+os.environ['PATH'])
        try:
            first=run(env=env)
            assert first.returncode and 'provision_outcome_unknown' in first.stderr,(first.stdout,first.stderr)
            assert marker.exists(),'fixture never dropped requested target ACK'
            receipt_path=remote+'/install/receipt-'+key+'.json'
            before=json.loads(shell('cat '+shlex.quote(receipt_path)).stdout)
            assert before['phase']==('published_verified' if loss=='publication' else 'started_verified'),before
            endpoint=target_home+'/endpoint.json'
            pid_before=shell("python3 -c "+shlex.quote("import json;print(json.load(open("+repr(endpoint)+"))['pid'])")).stdout.strip() if loss=='start' else None
            retry=run() # Clean transport: exact key/request, lookup before work.
            after=json.loads(shell('cat '+shlex.quote(receipt_path)).stdout)
            if loss=='publication':
                assert retry.returncode and 'provision_outcome_unknown' in retry.stderr
                assert after==before,'unknown publication triggered a new target mutation/start'
                assert shell('test ! -e '+shlex.quote(endpoint)).returncode==0,'retry automatically started a node'
            else:
                assert retry.returncode==0,(retry.stdout,retry.stderr)
                assert json.loads(retry.stdout)['connected']==before['started_host']
                pid_after=shell("python3 -c "+shlex.quote("import json;print(json.load(open("+repr(endpoint)+"))['pid'])")).stdout.strip()
                assert pid_before==pid_after,'known start ACK recovery restarted backend'
                ops=json.loads(subprocess.check_output(prefix+['mesh','operations'],text=True))
                original=next(op for op in ops if op['key']==key)
                assert original['phase']=='incomplete' and original['resolved_by'],'original receipt not immutably resolved by reviewed child'
                third=run()
                assert third.returncode==0,(third.stdout,third.stderr)
                assert json.loads(third.stdout)['connected']==before['started_host']
                again=json.loads(subprocess.check_output(prefix+['mesh','operations'],text=True))
                assert len(again)==len(ops),'resolved exact retry created more workers/trust history'
                subprocess.run(prefix+['stop','--wait','--timeout','5'],check=True,capture_output=True,text=True,timeout=15)
                reset=json.loads(subprocess.check_output(prefix+['database','remove','--dry-run','--retire-mesh','--reason','Review resolved private provisioning history'],text=True))
                assert reset['plan']['settled_proof']['complete'] and reset['plan']['settled_proof']['settled'],reset
            succeeded=True
            print('PASS lost '+loss+' ACK: exact lookup-first retry, no publication/backend execution replay',flush=True)
        except BaseException:
            print('Retained private unknown-outcome fixture:',root,remote,flush=True)
            raise
        finally:
            shell(shlex.quote(binary)+' --home '+shlex.quote(target_home)+' stop --wait --timeout 5')
            subprocess.run(prefix+['stop','--wait','--timeout','5'],capture_output=True,text=True,timeout=15)
            if succeeded:
                cleanup=shell('test ! -e '+shlex.quote(target_home+'/endpoint.json')+' && test '+shlex.quote(token)+' = "$(cat '+shlex.quote(remote+'/fixture-owner')+')" && rm -rf '+shlex.quote(remote))
                cleanup.check_returncode()
                assert not (home/'endpoint.json').exists() and (root/'fixture-owner').read_text()==token
                shutil.rmtree(root)


if __name__=='__main__':main()
