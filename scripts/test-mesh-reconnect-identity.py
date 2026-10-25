#!/usr/bin/env python3
"""Pure fixture identity-oracle controls; no subprocess, listener or ARM launch."""
from pathlib import Path
import copy
import importlib.util
import unittest

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('reconnect_identity_oracle',ROOT/'scripts/test-mesh-reconnect.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)


def identity(address='127.0.0.1:12345'):
    return {'owner':'501','owner_username':'fixture-user','host':'a'*32,
        'name':'fixture-node','address':address,'certificate':'exact public certificate',
        'spki_sha256':'b'*64,'fingerprint':'c'*64,'expires_at':'2027-01-01T00:00:00Z'}


class FixtureIdentityOracle(unittest.TestCase):
    def test_only_port_rebind_or_same_endpoint_is_allowed(self):
        for endpoint in ('127.0.0.1:12345','127.0.0.1:23456','127.0.0.1:1','127.0.0.1:65535'):
            with self.subTest(endpoint=endpoint):
                before,after=identity(),identity(endpoint);saved=copy.deepcopy((before,after))
                receipt=module.stable_reconnect_identity(before,after,endpoint)
                self.assertEqual(receipt['unchanged_fields'],sorted(set(before)-{'address'}))
                self.assertEqual(receipt['before_endpoint'],before['address'])
                self.assertEqual(receipt['after_endpoint'],endpoint)
                self.assertEqual((before,after),saved,'oracle mutated its evidence')

    def test_every_nonendpoint_identity_mutation_is_refused(self):
        for field in set(identity())-{'address'}:
            with self.subTest(field=field):
                before,after=identity(),identity('127.0.0.1:23456');after[field]+='-changed'
                with self.assertRaises(AssertionError):module.stable_reconnect_identity(before,after,after['address'])

    def test_missing_added_or_wrong_typed_fields_are_refused(self):
        for side in (0,1):
            for field in identity():
                for change in ('missing','null','integer'):
                    with self.subTest(side=side,field=field,change=change):
                        identities=[identity(),identity('127.0.0.1:23456')]
                        if change=='missing':identities[side].pop(field)
                        else:identities[side][field]=None if change=='null' else 501
                        with self.assertRaises(AssertionError):module.stable_reconnect_identity(*identities,'127.0.0.1:23456')
            with self.subTest(side=side,change='unknown'):
                identities=[identity(),identity('127.0.0.1:23456')];identities[side]['unreviewed']='x'
                with self.assertRaises(AssertionError):module.stable_reconnect_identity(*identities,'127.0.0.1:23456')
        for wrong in (None,[],{'address':'127.0.0.1:23456'}):
            with self.subTest(wrong=wrong):
                with self.assertRaises(AssertionError):module.stable_reconnect_identity(identity(),wrong,'127.0.0.1:23456')

    def test_invalid_or_nonfixture_listener_is_refused_in_every_position(self):
        for address in ('',None,501,'localhost:12345','0.0.0.0:12345','127.0.0.2:12345',
            '[::1]:12345','127.0.0.1:0','127.0.0.1:01','127.0.0.1:65536',
            '127.0.0.1:-1','127.0.0.1:+1','127.0.0.1:12345 ','127.0.0.1:12345\n',
            'http://127.0.0.1:12345','127.0.0.1:12345/path','x'*64):
            for position in ('before','after','listen'):
                with self.subTest(address=address,position=position):
                    before,after=identity(),identity('127.0.0.1:23456');listen=after['address']
                    if position=='before':before['address']=address
                    elif position=='after':after['address']=address
                    else:listen=address
                    with self.assertRaises(AssertionError):module.stable_reconnect_identity(before,after,listen)

    def test_new_identity_endpoint_must_equal_actual_config_listen(self):
        with self.assertRaises(AssertionError):
            module.stable_reconnect_identity(identity(),identity('127.0.0.1:23456'),'127.0.0.1:23457')


if __name__=='__main__':unittest.main(verbosity=2)
