#!/usr/bin/env python3
"""Actual three-daemon ORIGINAL-source routed host messages via directory only.

Private reviewed membership and explicit poll publication; no fabricated proof or
message records. Native CLI turn delivery is deliberately unsupported in Phase A.
Failed fixtures are retained for inspection.
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
    root=Path(tempfile.mkdtemp(prefix='arm-published-agents-')).resolve();root.chmod(0o700)
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

        contacts=[];pubs=[];publish_commands=[]
        for node in (b,c):
            contact=node.cli('agent','register','--id','shared-label','--adapter','generic','--delivery','poll','--cwd',str(root))
            contacts.append(contact)
            argv=['agent','publish','plan','--workspace',wid,'--id',contact['id'],'--session',contact['session'],'--registration',contact['registration'],'--label','Public agent','--key','publish-1','--reason','explicit poll publication']
            plan=node.cli(*argv)
            apply=['agent','publish','apply',*argv[3:],'--expect',plan['review_sha256']]
            declined=node.result(*apply)
            assert declined.returncode!=0 and json.loads(declined.stderr)['error']['code']=='confirmation_required',declined.stderr
            node.cli(*apply,'--dry-run')
            native=node.result(*argv,'--delivery','native')
            assert native.returncode!=0 and json.loads(native.stderr)['error']['code']=='unsupported_delivery',native.stderr
            pub=node.cli(*apply,'--yes')
            publish_commands.append(apply)
            with sqlite3.connect(node.home/'state.db') as db:before=db.execute('SELECT COUNT(*) FROM published_agent_changes').fetchone()[0]
            assert node.cli(*apply,'--yes')==pub
            with sqlite3.connect(node.home/'state.db') as db:assert db.execute('SELECT COUNT(*) FROM published_agent_changes').fetchone()[0]==before
            pubs.append(pub)
        bc,cc=contacts;bp,cp=pubs
        page=b.cli('agent','endpoints','--workspace',wid,'--host',c.identity['host'])
        assert len(page['cards'])==1,page
        assert page['cards'][0]['endpoint']==cp['endpoint'],page
        assert all(private not in json.dumps(page) for private in (cc['session'],cc['registration'],str(root))),page
        sender=['--workspace',wid,'--id',bc['id'],'--session',bc['session'],'--registration',bc['registration']]
        recipient=['--workspace',wid,'--id',cc['id'],'--session',cc['session'],'--registration',cc['registration']]
        send=['agent','send',*sender,'--host',c.identity['host'],'--endpoint',cp['endpoint']['id'],'--generation',cp['endpoint']['generation'],'--key','agent-b-c','--text','exact B to C poll']
        sent=b.cli(*send);assert sent['receipt']['state']=='accepted',sent
        inbox=c.cli('agent','inbox','--id',cc['id'],'--session',cc['session'],'--registration',cc['registration'])
        assert len(inbox['items'])==1,inbox
        msg=inbox['items'][0]['message'];assert msg['routed_origin']['origin_host']==b.identity['host'],msg
        assert msg['deliveries'][cc['id']]['mode']=='poll',msg
        assert b.cli(*send)==sent
        reply_command=['agent','reply',*recipient,'--message',msg['id'],'--key','agent-c-b','--text','exact reply to B']
        reply=c.cli(*reply_command)
        assert reply['receipt']['state']=='accepted',reply
        back=b.cli('agent','inbox','--id',bc['id'],'--session',bc['session'],'--registration',bc['registration'])
        assert len(back['items'])==1,back
        ack=c.cli('agent','ack','--id',cc['id'],'--session',cc['session'],'--registration',cc['registration'],'--message',msg['id'])
        updated=b.cli('agent','message-lookup',*sender,'--key','agent-b-c','--reconcile')
        assert updated['receipt']['state']=='acknowledged',updated
        # Observe a genuine destination commit before dropping its encrypted
        # acknowledgement. Restart preserves the source key; lookup never
        # dispatches a second local poll message.
        lost_send=['agent','send',*sender,'--host',c.identity['host'],'--endpoint',cp['endpoint']['id'],'--generation',cp['endpoint']['generation'],'--key','lost-receipt','--text','durable original despite lost ACK']
        relay.proof_armed=True
        lost=b.result(*lost_send)
        assert lost.returncode!=0 and json.loads(lost.stderr)['error']['code']=='routing_outcome_unknown',lost.stderr
        assert relay.dropped.wait(2),'actual committed encrypted acknowledgement was not dropped'
        unknown=b.cli('agent','message-lookup',*sender,'--key','lost-receipt')
        assert unknown['phase'] in ('dispatching','unknown'),unknown
        assert b.cli(*lost_send)==unknown,'unknown exact key redispatched'
        assert b.stop();b.stopped=False;b.start()
        assert b.cli('agent','message-lookup',*sender,'--key','lost-receipt')==unknown
        reconciled=b.cli('agent','message-lookup',*sender,'--key','lost-receipt','--reconcile')
        assert reconciled['phase']=='completed' and reconciled['message_id']==unknown['message_id'],reconciled
        assert b.cli(*lost_send)==reconciled
        inbox_after=c.cli('agent','inbox','--id',cc['id'],'--session',cc['session'],'--registration',cc['registration'])
        assert len(inbox_after['items'])==2,'lost ACK/restart/lookup replayed original delivery'
        changed=c.cli('agent','register','--id',cc['id'],'--adapter','generic','--delivery','poll','--cwd',str(root),'--session',cc['session'],'--idle')
        assert changed['registration']==cc['registration'],changed
        after=b.cli('agent','endpoints','--workspace',wid,'--host',c.identity['host'])
        assert after['cards']==[],after
        assert c.cli(*reply_command)==reply,'old reply key was rebound or retransmitted after withdrawal'
        stale=b.cli(*[('--key' if x=='--key' else x) for x in send[:-4]],'--key','stale-generation','--text','do not retarget')
        assert stale['receipt']['state']=='superseded',stale
        b.cli('agent','publish','withdraw','--workspace',wid,'--endpoint',bp['endpoint']['id'],'--generation',bp['endpoint']['generation'],'--revision','1','--key','withdraw-b','--reason','explicit end of acceptance','--yes')
        for node in (b,c,a):
            current=node.cli('workspaces','inspect','--workspace',wid,'--section','members','--limit','1')['workspace']
            node.cli('workspaces','retire','--workspace',wid,'--key','retire-membership','--revision',str(current['revision']),'--reason','explicit local fixture retirement','--yes')
            routed=node.cli('workspaces','inbox','--workspace',wid)
            node.cli('workspaces','messages-retire','--workspace',wid,'--key','retire-routing','--revision',str(routed['revision']),'--yes')
        for node in nodes:
            assert node.stop()
            proof=node.cli('database','remove','--reason','retire only private fixture','--retire-mesh','--dry-run',timeout=120)['plan']['settled_proof']
            assert proof['complete'] and proof['settled'],proof
        print('PASS actual B-A-C published poll send, immutable reply, scoped endpoint page, dedup, acknowledgement, same-registration Idle withdrawal and stale-generation refusal, lost committed ACK/restart/lookup without replay and strict retired proof')
        success=True
    finally:
        for node in nodes:node.stop()
        relay.close()
        if success:shutil.rmtree(root)
        else:print('Published agent fixture retained: '+str(root),file=sys.stderr)

if __name__=='__main__':main()
