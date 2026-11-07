import hashlib
import importlib.util
import json
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('finalize_release', pathlib.Path(__file__).with_name('finalize-release.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SigningMetadataTests(unittest.TestCase):
    def test_changed_archive_updates_both_checksum_and_inventory(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            archive = root / 'nodexlm_windows_amd64.zip'
            archive.write_bytes(b'PE archive after Authenticode signing')
            (root / 'nodexlm.cdx.json').write_text(json.dumps({'components': [{'name': archive.name, 'hashes': [{'alg': 'SHA-256', 'content': 'old-unsigned-digest'}]}]}))
            module.finalize(root, None)
            expected = hashlib.sha256(archive.read_bytes()).hexdigest()
            self.assertEqual((root / 'SHA256SUMS.txt').read_text(), expected + '  ' + archive.name + '\n')
            component = json.loads((root / 'nodexlm.cdx.json').read_text())['components'][0]
            self.assertEqual(component['hashes'], [{'alg': 'SHA-256', 'content': expected}])

    def test_missing_inventory_entry_blocks_publication(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            (root / 'nodexlm_windows_arm64.zip').write_bytes(b'signed archive')
            (root / 'nodexlm.cdx.json').write_text('{"components":[]}')
            with self.assertRaisesRegex(ValueError, 'every archive'):
                module.finalize(root, None)


if __name__ == '__main__':
    unittest.main()
