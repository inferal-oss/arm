#!/usr/bin/env python3
"""Offline checks for local formal-tool provisioning and extraction safeguards."""
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("provision_tla", Path(__file__).with_name("provision-tla.py"))
tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tool)


class ProvisionTests(unittest.TestCase):
    def archive(self, root, entries):
        archive = root / "input.tar.gz"
        with tarfile.open(archive, "w:gz") as output:
            for name, kind, value in entries:
                item = tarfile.TarInfo(name)
                item.type = kind
                if kind == tarfile.REGTYPE:
                    item.size = len(value)
                    output.addfile(item, io.BytesIO(value))
                else:
                    item.linkname = value
                    output.addfile(item)
        return archive

    def test_bad_paths_links_and_duplicates_refuse_before_extraction(self):
        for entries in ([('tlapm/../../escape', tarfile.REGTYPE, b'x')],
                        [('tlapm/link', tarfile.SYMTYPE, '/tmp')],
                        [('tlapm/link', tarfile.LNKTYPE, 'tlapm/file')],
                        [('tlapm/x', tarfile.REGTYPE, b'a'), ('tlapm/x', tarfile.REGTYPE, b'b')]):
            with self.subTest(entries=entries), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                destination = root / 'out'
                destination.mkdir()
                archive = self.archive(root, entries)
                with self.assertRaises(ValueError):
                    tool.extract(archive, destination)
                self.assertEqual(list(destination.iterdir()), [])

    def test_expansion_is_bounded(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = self.archive(root, [('tlapm/file', tarfile.REGTYPE, b'large')])
            destination = root / 'out'
            destination.mkdir()
            with patch.object(tool, 'MAX_EXPANDED', 3), self.assertRaises(ValueError):
                tool.extract(archive, destination)
            self.assertEqual(list(destination.iterdir()), [])

    def test_repeated_install_validates_content_and_preserves_damage(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / 'tools.jar'
            source.write_bytes(b'known')
            artifact = {'sha256': hashlib.sha256(b'known').hexdigest()}
            destination = root / 'installed'
            tool.install(artifact, source, destination, 'test', False)
            tool.install(artifact, source, destination, 'test', False)
            payload = destination / 'tla2tools.jar'
            payload.write_bytes(b'changed')
            with self.assertRaises(ValueError):
                tool.install(artifact, source, destination, 'test', False)
            self.assertEqual(payload.read_bytes(), b'changed')

    def test_unrecognized_directory_and_symlink_are_preserved(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / 'tools.jar'
            source.write_bytes(b'known')
            artifact = {'sha256': hashlib.sha256(b'known').hexdigest()}
            destination = root / 'unknown'
            destination.mkdir()
            (destination / 'keep').write_bytes(b'important')
            with self.assertRaises(ValueError):
                tool.install(artifact, source, destination, 'test', False)
            self.assertEqual((destination / 'keep').read_bytes(), b'important')
            link = root / 'link'
            link.symlink_to(destination, target_is_directory=True)
            with self.assertRaises(ValueError):
                tool.install(artifact, source, link, 'test', False)

    def test_download_mismatch_never_overwrites_cache(self):
        with tempfile.TemporaryDirectory() as temporary:
            target = Path(temporary) / 'cached'
            target.write_bytes(b'unknown')
            artifact = {'url': 'https://example.invalid/never', 'bytes': 5, 'sha256': hashlib.sha256(b'known').hexdigest()}
            with self.assertRaises(ValueError):
                tool.download(artifact, target)
            self.assertEqual(target.read_bytes(), b'unknown')

    def test_download_limits_and_digest_before_publication(self):
        for body in (b'too many bytes', b'wrong'):
            with self.subTest(body=body), tempfile.TemporaryDirectory() as temporary:
                target = Path(temporary) / 'cached'
                artifact = {'url': 'https://example.invalid/never', 'bytes': 5, 'sha256': hashlib.sha256(b'known').hexdigest()}
                with patch.object(tool.urllib.request, 'urlopen', return_value=io.BytesIO(body)), self.assertRaises(ValueError):
                    tool.download(artifact, target)
                self.assertFalse(target.exists())
                self.assertEqual(list(target.parent.iterdir()), [])


if __name__ == '__main__':
    unittest.main()
