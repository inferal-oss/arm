#!/usr/bin/env python3
"""Private synthetic file controls only; no backend, HTTP, SSH or native tools."""
import copy
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
s=importlib.util.spec_from_file_location('web_fixture_binding',Path(__file__).with_name('mesh_reconnect_ui_binding.py'));b=importlib.util.module_from_spec(s);s.loader.exec_module(b)
class Binding(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory(prefix='arm-ui-binding-files-');self.addCleanup(self.tmp.cleanup)
        self.root=Path(self.tmp.name);self.root.chmod(0o700)
        for p in ('source','source/mesh','source/mesh-lifecycle','destination','destination/mesh'):(self.root/p).mkdir(mode=0o700)
        self.source='a'*32;self.target='b'*32;self.arm=b'owned synthetic non-executable bytes';self.sha=hashlib.sha256(self.arm).hexdigest()
        (self.root/'arm').write_bytes(self.arm);(self.root/'arm').chmod(0o700)
        self.ready={'mode':'web','dashboard_url':'http://127.0.0.1:12345/#connect='+'c'*64,'source_home':str(self.root/'source'),'arm':str(self.root/'arm'),'host':self.target,'pending_key':'lost-disconnect','outcome_file':str(self.root/'ui-outcome.json'),'required':b.REQUIRED_TEXT}
        self.endpoint={'url':'ws://127.0.0.1:12345/v1/ws','pid':123,'token':'d'*64}
        def identity(host):return {'host':host,'owner':str(os.getuid()),'owner_username':'fixture-user','name':'fixture','address':'127.0.0.1:23456','certificate':'synthetic bounded certificate','spki_sha256':'e'*64,'fingerprint':'f'*64,'expires_at':'2027-01-01T00:00:00Z'}
        self.left=identity(self.source);self.right=identity(self.target)
        self.put('ui-ready.json',self.ready);self.put('source/endpoint.json',self.endpoint)
        self.put('source/mesh/config.json',{'identity':self.left,'peers':[{'identity':self.right,'state':'revoked'}]})
        self.put('destination/mesh/config.json',{'identity':self.right})
        self.pending={'schema':1,'key':'lost-disconnect','kind':'disconnect','revision':1,'phase':'incomplete','approved':True,'targets':['host:'+self.target],'request':{'key':'lost-disconnect','kind':'disconnect','disconnect':{'host':self.target}},'review':{'exact':'review'},'review_sha256':'a'*64}
        self.save_op(self.pending)
    def put(self,path,value):
        p=self.root/path;p.write_text(json.dumps(value));p.chmod(0o600);return p
    def save_op(self,value):return self.put('source/mesh-lifecycle/coordinator-operation-'+hashlib.sha256(value['key'].encode()).hexdigest()+'.json',value)
    def begun(self):
        value=b.begin(self.root,self.sha);p=self.root/'binding.json';sha=b.exclusive(p,value);return p,sha,value
    def complete(self):
        pending=copy.deepcopy(self.pending);pending.update(phase='handed_off',revision=2,handoff={'reason':'explicit preservation'})
        self.save_op(pending)
        common={'schema':1,'kind':'reconnect','revision':3,'review':{'exact':'fresh review'},'review_sha256':'f'*64}
        declined=dict(common,key='declined',phase='rejected',approved=False,error={'code':'confirmation_declined'},request={'key':'declined','kind':'reconnect','reconnect':{'draft':{'host':self.target}}})
        done=dict(common,key='approved',phase='completed',approved=True,result={'connected':self.target},request={'key':'approved','kind':'reconnect','reconnect':{'draft':{'host':self.target}}})
        self.save_op(declined);self.save_op(done);return pending,declined,done
    def test_begin_and_complete_are_file_only_and_exclusive(self):
        p,sha,value=self.begun();self.assertEqual(value['origin'],'http://127.0.0.1:12345');self.assertNotIn('token',value)
        self.complete();path,outcome,proof=b.finish(p,sha,'declined','approved')
        self.assertEqual(path,self.root/'ui-outcome.json');self.assertEqual(outcome,{'handoff_key':'lost-disconnect','declined_key':'declined','reconnect_key':'approved'})
        self.assertFalse(path.exists());self.assertEqual(set(proof['receipts']),{'handoff','declined','completed'})
        b.exclusive(path,outcome)
        with self.assertRaises(FileExistsError):b.exclusive(path,{'replacement':True})
    def test_ready_origin_ticket_paths_and_closedness(self):
        variants=[dict(self.ready,mode='terminal'),dict(self.ready,unknown=True),dict(self.ready,host=self.source),dict(self.ready,source_home='/tmp/elsewhere'),dict(self.ready,outcome_file='/tmp/elsewhere')]
        for url in ['http://localhost:12345/#connect='+'c'*64,'http://127.0.0.1:12346/#connect='+'c'*64,'http://127.0.0.1:12345/#connect=short','http://127.0.0.1:12345/#connect='+'c'*64+'&connect='+'d'*64,'http://127.0.0.1:12345/?private=1#connect='+'c'*64]:variants.append(dict(self.ready,dashboard_url=url))
        for v in variants:
            self.put('ui-ready.json',v)
            with self.subTest(v=v),self.assertRaises(ValueError):b.begin(self.root,self.sha)
        self.put('ui-ready.json',self.ready)
    def test_native_endpoint_scheme_path_distinct_from_dashboard(self):
        for address in ['http://127.0.0.1:12345','ws://127.0.0.1:12345','ws://127.0.0.1:12345/other','ws://localhost:12345/v1/ws']:
            self.put('source/endpoint.json',dict(self.endpoint,url=address))
            with self.subTest(address=address),self.assertRaises(ValueError):b.begin(self.root,self.sha)
        self.put('source/endpoint.json',self.endpoint)
        self.assertEqual(b.begin(self.root,self.sha)['origin'],'http://127.0.0.1:12345')

    def test_raw_malformed_duplicate_size_symlink_mode(self):
        p=self.root/'ui-ready.json'
        for raw in [b'',b'{} trailing',b'{"mode":"web","mode":"web"}',b'{"mode":"\xff"}',b'x'*16385]:
            p.write_bytes(raw)
            with self.subTest(raw=raw[:20]),self.assertRaises((ValueError,UnicodeError)):b.begin(self.root,self.sha)
        self.put('ui-ready.json',self.ready);p.chmod(0o644)
        with self.assertRaises(ValueError):b.begin(self.root,self.sha)
        p.unlink();p.symlink_to(self.root/'source/endpoint.json')
        with self.assertRaises(ValueError):b.begin(self.root,self.sha)
    def test_endpoint_generation_changes_refuse_finish(self):
        p,sha,_=self.begun();self.complete()
        variants=[dict(self.endpoint,pid=124),dict(self.endpoint,token='a'*64),dict(self.endpoint,url='ws://127.0.0.1:12346/v1/ws')]
        for v in variants:
            self.put('source/endpoint.json',v)
            with self.subTest(v=v),self.assertRaises(ValueError):b.finish(p,sha,'declined','approved')
        self.put('source/endpoint.json',self.endpoint)
        # Restoring identical bytes in a replacement inode still fails exact generation.
        old=self.root/'source/endpoint.json';raw=old.read_bytes();old.rename(self.root/'old-endpoint.json');old.write_bytes(raw);old.chmod(0o600)
        with self.assertRaises(ValueError):b.finish(p,sha,'declined','approved')
    def assert_selected_change_refused(self,p,sha,saved,message):
        # Each control starts in its own setUp tree; a previous endpoint mutation
        # cannot cause an unrelated image/directory/principal assertion to pass.
        endpoint,proof=b.read_json(self.root/'source/endpoint.json',16384)
        self.assertEqual(endpoint,self.endpoint)
        self.assertEqual(proof,saved['endpoint_proof'])
        self.assertFalse((self.root/'ui-outcome.json').exists())
        with self.assertRaisesRegex(ValueError,message):b.finish(p,sha,'declined','approved')
        self.assertFalse((self.root/'ui-outcome.json').exists())

    def test_changed_selected_image_refuses_without_endpoint_mismatch(self):
        p,sha,saved=self.begun();self.complete()
        image=self.root/'arm';image.write_bytes(b'changed selected synthetic image')
        self.assertEqual(image.stat().st_mode&0o777,0o700)
        self.assert_selected_change_refused(p,sha,saved,'selected ARM image changed')

    def test_replaced_source_directory_refuses_without_endpoint_mismatch(self):
        p,sha,saved=self.begun();self.complete()
        source=self.root/'source';held=self.root/'retained-source-directory'
        source.rename(held);source.mkdir(mode=0o700)
        for child in held.iterdir():child.rename(source/child.name)
        self.assertNotEqual(b.directory(source),saved['directories']['source'])
        # The old directory remains owned and empty until TemporaryDirectory
        # cleanup; its retained inode prevents accidental reuse in this oracle.
        self.assert_selected_change_refused(p,sha,saved,'generation changed: directories')

    def test_replaced_root_directory_refuses_without_endpoint_mismatch(self):
        p,sha,saved=self.begun();self.complete()
        held=self.root.with_name(self.root.name+'-retained-root')
        self.root.rename(held);self.root.mkdir(mode=0o700)
        for child in held.iterdir():child.rename(self.root/child.name)
        self.addCleanup(held.rmdir)
        self.assertNotEqual(b.directory(self.root),saved['directories']['.'])
        self.assert_selected_change_refused(p,sha,saved,'generation changed: directories')

    def test_changed_source_principal_refuses_without_endpoint_mismatch(self):
        p,sha,saved=self.begun();self.complete()
        config=b.read_json(self.root/'source/mesh/config.json')[0]
        config['identity']['fingerprint']='1'*64
        self.put('source/mesh/config.json',config)
        current=b.context(self.root,self.sha)
        self.assertEqual(current['target_principal'],saved['target_principal'])
        self.assertNotEqual(current['source_principal'],saved['source_principal'])
        self.assert_selected_change_refused(p,sha,saved,'generation changed: source_principal')

    def test_changed_target_principal_refuses_without_endpoint_mismatch(self):
        p,sha,saved=self.begun();self.complete()
        # Change both source's selected observation and destination's report so
        # context() remains internally consistent, reaching the saved-principal
        # fence rather than passing due to an unrelated mismatched peer report.
        left=b.read_json(self.root/'source/mesh/config.json')[0]
        right=b.read_json(self.root/'destination/mesh/config.json')[0]
        left['peers'][0]['identity']['fingerprint']='2'*64
        right['identity']['fingerprint']='2'*64
        self.put('source/mesh/config.json',left);self.put('destination/mesh/config.json',right)
        current=b.context(self.root,self.sha)
        self.assertEqual(current['source_principal'],saved['source_principal'])
        self.assertNotEqual(current['target_principal'],saved['target_principal'])
        self.assert_selected_change_refused(p,sha,saved,'generation changed: target_principal')

    def test_inactive_exact_pending_binding_and_receipt_corruption(self):
        for changed in [dict(self.pending,worker='live'),dict(self.pending,approved=False),dict(self.pending,phase='completed'),dict(self.pending,targets=['host:'+self.source]),dict(self.pending,unknown=True)]:
            self.save_op(changed)
            with self.subTest(changed=changed),self.assertRaises(ValueError):b.begin(self.root,self.sha)
        self.save_op(self.pending);p,sha,_=self.begun();pending,declined,done=self.complete()
        for wrong in [dict(declined,approved=True),dict(declined,error={'code':'unrelated'}),dict(declined,review=None),dict(done,result={'connected':self.source})]:
            original=declined if wrong['key']=='declined' else done;self.save_op(wrong)
            with self.subTest(wrong=wrong),self.assertRaises(ValueError):b.finish(p,sha,'declined','approved')
            self.save_op(original)
        with self.assertRaises(ValueError):b.finish(p,'0'*64,'declined','approved')
        with self.assertRaises(ValueError):b.finish(p,sha,'approved','approved')
    def test_write_sentinel_and_symlink_never_replaced(self):
        target=self.put('sentinel.json',{'keep':'exact'});raw=target.read_bytes()
        with self.assertRaises(FileExistsError):b.exclusive(target,{'changed':True})
        link=self.root/'link';link.symlink_to(target)
        with self.assertRaises(FileExistsError):b.exclusive(link,{'changed':True})
        self.assertEqual(target.read_bytes(),raw);self.assertTrue(link.is_symlink())
if __name__=='__main__':unittest.main(verbosity=2)
