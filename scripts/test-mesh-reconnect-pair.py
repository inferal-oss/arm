#!/usr/bin/env python3
"""Pure Pair-lost-ACK oracle controls; no native tools, ARM state or SSH."""
import copy
import hashlib
import importlib.util
from pathlib import Path
import unittest

p=Path(__file__).with_name('test-mesh-reconnect.py')
spec=importlib.util.spec_from_file_location('pair_reconnect_fixture',p)
h=importlib.util.module_from_spec(spec);spec.loader.exec_module(h)

class PairEvidence(unittest.TestCase):
    pair='c'*64
    def row(self,sequence=1,**kw):
        value={'sequence':sequence,'action':'pair','pair':self.pair,'input_sha256':'a'*64,
            'child_started':True,'child_wait_failed':False,'child_exit':0,
            'stdout_sha256':'b'*64,'stderr_sha256':'d'*64}
        value.update(kw);return value
    def test_exact_pair_domain(self):
        source,target='a'*32,'b'*32;key='pair-response-lost'
        expected=hashlib.sha256(('arm-connect:'+key+':'+source+':'+target).encode()).hexdigest()
        self.assertEqual(h.reconnect_pair_id(key,source,target),expected)
        self.assertNotEqual(h.reconnect_pair_id(key,target,source),expected)
        for args in [('',source,target),('x'*129,source,target),('a\x00',source,target),
                (key,source,source),(key,source.upper(),target),(key,source,target+'c')]:
            with self.subTest(args=args),self.assertRaises(ValueError):h.reconnect_pair_id(*args)
    def test_mixed_events_exact_pair_only(self):
        first=self.row(3,fault='after')
        events=[self.row(1,action='readonly_probe',pair='f'*64),
            self.row(2,pair='e'*64),first]
        self.assertEqual(h.committed_pair_lost_ack(events,self.pair),first)
        events += [self.row(4,action='lifecycle_inspect',pair='f'*64),self.row(5),
            self.row(6,action='activate')]
        result=h.recovered_pair_entries(events,self.pair,first)
        self.assertEqual(result,[first,self.row(5)])
        result[0]['fault']='before'
        self.assertEqual(events[2]['fault'],'after')
    def test_missing_or_forged_commit_refused(self):
        first=self.row(fault='after')
        variants=[[],[first,self.row(2)], [self.row(fault='before')], [self.row()],
            [self.row(fault='after',child_started=False)],
            [self.row(fault='after',child_wait_failed=True)],
            [self.row(fault='after',child_exit=1)],
            [self.row(fault='after',action='activate')],
            [self.row(fault='after',pair='e'*64)]]
        for v in variants:
            with self.subTest(v=v),self.assertRaises((ValueError,AssertionError)):
                h.committed_pair_lost_ack(v,self.pair)
    def test_exact_recovery_no_extra_or_changed_entry(self):
        first=self.row(fault='after'); second=self.row(2)
        self.assertEqual(h.recovered_pair_entries([first,second],self.pair,first),[first,second])
        variants=[[first],[first,second,self.row(3)],
            [first,self.row(2,fault='after')],[first,self.row(2,child_exit=1)],
            [first,self.row(2,child_wait_failed=True)],
            [first,self.row(2,child_started=False)],
            [self.row(fault='before'),second],[second,first]]
        for v in variants:
            with self.subTest(v=v),self.assertRaises((ValueError,AssertionError)):
                h.recovered_pair_entries(v,self.pair,first)
    def test_closed_full_event_shape_and_optional_fields(self):
        for field in h.evidence.EVENT_REQUIRED|{'pair','fault'}:
            row=self.row(fault='after');row.pop(field)
            with self.subTest(missing=field),self.assertRaises((ValueError,AssertionError)):
                h.committed_pair_lost_ack([row],self.pair)
        row=self.row(fault='after',unknown='no')
        with self.assertRaises(ValueError):h.committed_pair_lost_ack([row],self.pair)
        # omitempty Key is legitimate, including for actual Pair records.
        self.assertNotIn('key',h.committed_pair_lost_ack([self.row(fault='after')],self.pair))

if __name__=='__main__':unittest.main(verbosity=2)
