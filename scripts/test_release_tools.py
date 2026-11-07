import hashlib
import importlib.util
import json
import pathlib
import tempfile
import subprocess
import sys
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


class PreviewManifestTests(unittest.TestCase):
    def test_prerelease_cannot_be_presented_as_stable(self):
        with tempfile.TemporaryDirectory() as temp:
            result = subprocess.run([sys.executable, str(pathlib.Path(__file__).with_name('package-manifests.py')), '--version', '0.1.0-edge.1', '--checksums', temp+'/missing', '--output', temp+'/out'], capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b'stable manifests require', result.stderr)

    def test_ambiguous_duplicate_checksum_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            path=pathlib.Path(temp)/'checksums'
            path.write_text(('a'*64+'  nodexlm_windows_amd64.zip\n')*2)
            result=subprocess.run([sys.executable, str(pathlib.Path(__file__).with_name('package-manifests.py')), '--version', '1.0.0', '--checksums', str(path), '--output', temp+'/out'],capture_output=True)
            self.assertNotEqual(result.returncode,0)
            self.assertIn(b'duplicate checksum',result.stderr)


if __name__ == '__main__':
    unittest.main()
