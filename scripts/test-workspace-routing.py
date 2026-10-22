#!/usr/bin/env python3
"""Actual three-daemon ORIGINAL-source routed host messages via directory only.

Private reviewed membership; no fabricated proofs/records. Failed fixtures are
retained. Published agent endpoints and general graph alternatives are separate
acceptance gates, not claimed by this initial star-route scenario.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import sqlite3
import sys
import tempfile

sys.dont_write_bytecode=True

spec=importlib.util.spec_from_file_location('workspace_acceptance',Path(__file__).with_name('test-workspaces.py'))
ws=importlib.util.module_from_spec(spec)
spec.loader.exec_module(ws)
mesh=ws.mesh

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--arm',required=True)
    args=parser.parse_args()
    binary=str(Path(args.arm).resolve())
    root=Path(tempfile.mkdtemp(prefix='arm-workspace-routing-'));root.chmod(0o700)
    nodes=[mesh.Node(binary,root,name) for name in ('directory','origin-b','recipient-c')]
    a,b,c=nodes
    relay=ws.ProofReplyRelay(("127.0.0.1",mesh.free_port()))
    def drop_committed_message():
        if not relay.proof_armed:return False
        try:
            with sqlite3.connect((c.home/"state.db").as_uri()+"?mode=ro",uri=True,timeout=.1) as db:
                for row in db.execute("SELECT data FROM routing_inbox"):
                    if json.loads(row[0])["envelope"]["client_key"]=="lost-receipt":
                        relay.proof_armed=False;relay.dropped.set();return True
        except (sqlite3.Error,OSError):pass
        return False
    relay.should_drop_proof=drop_committed_message
    success=False
    try:
        for node in nodes:
            node.start()
            port=mesh.free_port()
            if node is c:relay.upstream=('127.0.0.1',port)
            node.identity=node.cli('mesh','init','--name',node.name,'--listen',f'127.0.0.1:{port}','--address',relay.address if node is c else f'127.0.0.1:{port}','--yes')
        ws.enroll(a,b);ws.enroll(a,c)
        def decide(node,op):
            return node.cli('workspaces','decide','--workspace',op['workspace'],'--key',op['key'],'--revision',str(op['revision']),'--review',op['review_sha256'],'--approve','--yes')
        create=a.cli('workspaces','create','--name','Private routed host inbox','--key','create','--reason','actual closed three-daemon route')
        wid=create['workspace'];decide(a,create)
        invite=root/'invitation.json'
        a.cli('workspaces','invitation','--workspace',wid,'--output',str(invite))
        for node in (b,c):
            join=node.cli('workspaces','join','--invitation',str(invite),'--key','join','--reason','explicit original host membership')
            decide(node,join)
            consent=root/(node.name+'-consent.json')
            node.cli('workspaces','consent','--workspace',wid,'--key','join','--output',str(consent))
            argv=['workspaces','admit','--workspace',wid,'--consent',str(consent),'--key','admit-'+node.name,'--reason','exact current candidate scope']
            review=a.cli(*argv)
            active=a.cli(*argv,'--review',review['review_sha256'],'--yes')
            assert active['phase']=='completed',active
            node.cli('workspaces','refresh','--workspace',wid)
            current=node.cli('workspaces','lookup','--workspace',wid,'--key','join')['operation']
            node.cli('workspaces','finalize','--workspace',wid,'--key','join','--revision',str(current['revision']))
        for node in (b,c):
            node.cli('workspaces','refresh','--workspace',wid)
            assert len(node.cli('mesh','peers'))==1
        body='private B to C through directory; relay is not original sender'
        send=['workspaces','send','--workspace',wid,'--to',c.identity['host'],'--key','b-to-c','--text',body,'--ttl','1h']
        sent=b.cli(*send)
        assert sent['phase']=='completed' and sent['receipt']['state']=='accepted',sent
        assert sent['origin_host']==b.identity['host'] and sent['target_host']==c.identity['host'],sent
        inbox=c.cli('workspaces','inbox','--workspace',wid)
        assert inbox['total']==1 and len(inbox['inbox'])==1,inbox
        item=inbox['inbox'][0]
        assert item['envelope']['body']==body and item['envelope']['origin_host']==b.identity['host'],item
        assert b.cli(*send)==sent,'same key replayed or changed original message'
        assert c.cli('workspaces','inbox','--workspace',wid)['total']==1
        changed=b.result(*send[:-4],'--text','changed original body','--ttl','1h')
        assert changed.returncode!=0 and json.loads(changed.stderr)['error']['code']=='idempotency_conflict',changed.stderr
        reply=c.cli('workspaces','reply','--workspace',wid,'--message',item['envelope']['message_id'],'--key','c-reply','--text','C replies to original B','--ttl','1h')
        assert reply['phase']=='completed' and reply['receipt']['state']=='accepted',reply
        back=b.cli('workspaces','inbox','--workspace',wid)
        assert back['total']==1 and back['inbox'][0]['envelope']['origin_host']==c.identity['host'],back
        assert back['inbox'][0]['envelope']['reply_to']['message_id']==sent['message_id'],back
        exact=b.cli('workspaces','message-lookup','--workspace',wid,'--key','b-to-c','--reconcile')
        assert exact==sent,exact
        # Observe a real destination commit, then drop its encrypted reply. The
        # relay reads SQLite only; it never alters records or invents receipts.
        relay.proof_armed=True
        lost_argv=['workspaces','send','--workspace',wid,'--to',c.identity['host'],'--key','lost-receipt','--text','durable original despite lost encrypted reply','--ttl','1h']
        lost=b.result(*lost_argv)
        assert lost.returncode!=0 and json.loads(lost.stderr)['error']['code']=='routing_outcome_unknown',lost.stderr
        assert relay.dropped.wait(2),'real committed receipt was not dropped'
        unknown=b.cli('workspaces','message-lookup','--workspace',wid,'--key','lost-receipt')
        assert unknown['phase'] in ('unknown','dispatching'),unknown
        assert b.cli(*lost_argv)==unknown,'same-key retry repeated uncertain delivery'
        before_retire=b.cli('workspaces','inspect','--workspace',wid,'--section','members','--limit','1')['workspace']
        refused=b.result('workspaces','retire','--workspace',wid,'--key','refused-unknown-retirement','--revision',str(before_retire['revision']),'--reason','must preserve exact receipt lookup','--yes')
        assert refused.returncode!=0 and json.loads(refused.stderr)['error']['code']=='obligations_unresolved',refused.stderr
        assert b.cli('workspaces','inspect','--workspace',wid,'--section','members','--limit','1')['workspace']==before_retire,'refused retirement changed current membership'
        assert c.cli('workspaces','inbox','--workspace',wid)['total']==2
        assert b.stop();b.stopped=False;b.start()
        durable=b.cli('workspaces','message-lookup','--workspace',wid,'--key','lost-receipt')
        assert durable==unknown,'source restart lost exact unknown intent'
        reconciled=b.cli('workspaces','message-lookup','--workspace',wid,'--key','lost-receipt','--reconcile')
        assert reconciled['phase']=='completed' and reconciled['message_id']==unknown['message_id'],reconciled
        assert c.cli('workspaces','inbox','--workspace',wid)['total']==2,'lookup replayed destination insertion'
        for node in (b,c):
            peers=node.cli('mesh','peers')
            assert len(peers)==1 and peers[0]['identity']['host']==a.identity['host'],peers
        for operation in ('workspace_message_send','workspace_message_reply','workspace_message_lookup','run','agent_message'):
            b.call(a.identity['host'],operation,{},key='foreign-'+operation if operation=='run' else None,codes={'forbidden'})
        # Explicit local deny applies before message insertion. The source may
        # retain an unknown result; no failure can erase an admitted send.
        current=c.cli('workspaces','inspect','--workspace',wid,'--section','members','--limit','1')['workspace']
        c.cli('workspaces','deny','--workspace',wid,'--host',b.identity['host'],'--key','deny-b','--revision',str(current['revision']),'--reason','private current deny gate','--yes')
        denied=b.result('workspaces','send','--workspace',wid,'--to',c.identity['host'],'--key','denied-send','--text','must not enter destination inbox')
        assert denied.returncode!=0 and json.loads(denied.stderr)['error']['code']=='routing_outcome_unknown',denied.stderr
        assert c.cli('workspaces','inbox','--workspace',wid)['total']==2
        retained=b.cli('workspaces','message-lookup','--workspace',wid,'--key','denied-send')
        assert retained['phase'] in ('dispatching','unknown'),retained
        assert b.result('workspaces','message-cancel','--workspace',wid,'--key','denied-send','--revision',str(retained['revision']),'--yes').returncode!=0
        print('PASS actual three-daemon B→A→C host inbox + C→A→B original reply, keyed dedup, lost postcommit reply + source restart/read-only reconciliation, current deny, unknown retention and forwarded native/resource denial; no B-C direct peers')
        success=True
    finally:
        relay.close()
        stopped=all([node.stop() for node in nodes])
        if success and stopped:shutil.rmtree(root)
        else:print('Routed workspace fixture retained for inspection: '+str(root),file=sys.stderr)

if __name__=='__main__':main()
