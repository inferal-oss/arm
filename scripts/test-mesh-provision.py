#!/usr/bin/env python3
"""Actual verified-SSH missing-node provisioning using explicit disposable roots.

The remote user's installed ARM, shell configuration and system services are
never changed. Supply a trusted locally built target-platform companion.
"""
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
    options=p.parse_args()
    arm=str(Path(options.arm).resolve()); artifact=str(Path(options.artifact).resolve())
    root=Path(tempfile.mkdtemp(prefix='arm-mesh-provision-')).resolve()
    home=root/'source'
    remote='/tmp/arm-mesh-provision-'+uuid.uuid4().hex
    target_home=remote+'/state'; install_root=remote+'/install'
    prefix=[arm,'--home',str(home)]
    ssh=['ssh','-o','BatchMode=yes','-o','StrictHostKeyChecking=yes','-o','UpdateHostKeys=no',options.ssh]
    def shell(command):
        return subprocess.run(ssh+[command],capture_output=True,text=True,timeout=30)
    def invoke(*args,ok=True,timeout=120):
        result=subprocess.run(prefix+list(args),capture_output=True,text=True,timeout=timeout)
        if ok and result.returncode:
            raise AssertionError((args,result.returncode,result.stdout,result.stderr))
        return result
    def data(*args): return json.loads(invoke(*args).stdout)
    shell('umask 077; mkdir '+shlex.quote(remote)).check_returncode()
    key='missing-node-'+uuid.uuid4().hex
    args=['mesh','connect',options.ssh,'--binary',remote+'/not-installed',
          '--remote-home',target_home,'--install-root',install_root,
          '--artifact',artifact,'--key',key,'--reason','Disposable missing-node provisioning acceptance',
          '--grant','observe-host,observe-own,lease-own,message','--reverse-grant','',
          '--cpu','1','--memory','64MiB','--disk','1MiB','--max-ttl','1m']
    target_binary=None
    succeeded=False
    token=uuid.uuid4().hex
    (root/"fixture-owner").write_text(token)
    shell("printf %s "+shlex.quote(token)+" > "+shlex.quote(remote+"/fixture-owner")).check_returncode()
    try:
        plan=json.loads(invoke(*(args+['--dry-run'])).stdout)
        assert plan['operation']=='install-and-start-arm-node',plan
        assert plan['request']['transfer']['sha256'] and plan['request']['startup']['resources']['disk_bytes']==0
        assert not home.exists(),'dry-run created ARM source state'
        assert shell('test ! -e '+shlex.quote(install_root)+' && test ! -e '+shlex.quote(target_home)).returncode==0,'dry-run wrote remote state'
        unapproved=invoke(*args,ok=False)
        assert unapproved.returncode and 'confirmation_required' in unapproved.stderr,(unapproved.stdout,unapproved.stderr)
        assert json.loads(unapproved.stdout)['operation']=='install-and-start-arm-node'
        assert not home.exists(),'missing machine consent created source ARM/catalog state'
        assert shell('test ! -e '+shlex.quote(install_root)+' && test ! -e '+shlex.quote(target_home)).returncode==0,'missing machine consent wrote target state'
        target_binary=plan['final_binary']
        # Explicit machine approval covers this install/start review and the
        # distinct subsequent concrete TLS identities/grants review.
        connected=json.loads(invoke(*(args+['--yes'])).stdout)
        host=connected['connected']; assert len(host)==32,connected
        operations=data('mesh','operations')
        provision=[op for op in operations if op['kind']=='provision']
        assert len(provision)==1 and provision[0]['phase']=='completed',provision
        install_receipt=data('mesh','operations',provision[0]['key'])
        assert install_receipt['review']['operation']=='install-and-start-arm-node'
        receipt=install_receipt['result']['receipt']
        assert receipt['phase']=='started_verified' and receipt['started_host']==host
        assert receipt['request']['sha256']==plan['request']['transfer']['sha256']
        assert target_binary.startswith(install_root+'/artifact-')
        lease=data('acquire','--host',host,'--cpu','.25','--ttl','20s','--reason','Provisioned-node real claim','--key','provision-claim')
        assert lease['host']==host and lease['request']['origin_peer'],lease
        data('release',lease['id'],'--host',host,'--key','provision-release')
        message=data('mesh','send',host,'--body','Disposable provisioned-node operator inbox check','--key','provision-message')
        assert message, 'empty message result'
        assert receipt['runtime']['sha256']==receipt['request']['sha256'] and receipt['runtime']['method']=='kernel-self-executable'
        runtime_payload=json.dumps({'Action':'provision_runtime','Home':target_home})
        def runtime_proof():
            return subprocess.run(ssh+[shlex.quote(target_binary)+' mesh bootstrap'],input=runtime_payload,capture_output=True,text=True,timeout=30)
        runtime_before=json.loads(runtime_proof().stdout)
        old_image=target_binary+'.original-running-image'
        replacement=shell('mv '+shlex.quote(target_binary)+' '+shlex.quote(old_image)+' && cp '+shlex.quote(old_image)+' '+shlex.quote(target_binary)+' && chmod 700 '+shlex.quote(target_binary))
        replacement.check_returncode()
        try:
            assert runtime_proof().returncode!=0,'replaced path inherited old daemon runtime proof'
            assert data('status','--host',host)['host']==host,'replacement test disturbed existing authority'
        finally:
            shell('mv -f '+shlex.quote(old_image)+' '+shlex.quote(target_binary)).check_returncode()
        runtime_after=json.loads(runtime_proof().stdout)
        assert runtime_after==runtime_before,'restored exact running inode changed original startup evidence'
        before=len(operations)
        reused=json.loads(invoke(*(args+['--yes'])).stdout)
        assert reused['connected']==host and len(data('mesh','operations'))==before,'exact retry created new trust/install history'
        invoke('stop','--wait','--timeout','5')
        reset=data('database','remove','--dry-run','--retire-mesh','--reason','Review disposable completed provisioning history')
        assert reset['plan']['settled_proof']['complete'] and reset['plan']['settled_proof']['settled'],reset
        assert 'mesh-artifacts' in ' '.join(reset['plan']['preserved']),reset
        succeeded=True
        print('PASS missing node: read-only probe/dry-run, imported companion, separate durable install+trust reviews, private no-replace publication/start receipt, real claim/message and exact retry',flush=True)
    except BaseException:
        print('Retained private provisioning fixture:',root,remote,flush=True)
        raise
    finally:
        # Exact private homes only; no global ARM executable or backend stop.
        if target_binary:
            shell(shlex.quote(target_binary)+' --home '+shlex.quote(target_home)+' stop --wait --timeout 5')
        invoke('stop','--wait','--timeout','5',ok=False,timeout=15)
        if succeeded:
            cleanup=shell('test ! -e '+shlex.quote(target_home+'/endpoint.json')+' && test '+shlex.quote(token)+' = "$(cat '+shlex.quote(remote+'/fixture-owner')+')" && rm -rf '+shlex.quote(remote))
            cleanup.check_returncode()
            assert not (home/'endpoint.json').exists() and (root/'fixture-owner').read_text()==token
            shutil.rmtree(root)


if __name__=='__main__': main()
