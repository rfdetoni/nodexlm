#!/usr/bin/env python3
"""Refresh integrity metadata after native signing changes archive bytes."""
import argparse
import hashlib
import json
import pathlib
import subprocess
import sys
import tarfile
import tempfile


def finalize(directory, version):
    paths = sorted(set(directory.glob('nodexlm_*.tar.gz')) | set(directory.glob('nodexlm_*.zip')) |
                   set(directory.glob('nodexlm*.deb')) | set(directory.glob('nodexlm*.rpm')))
    if not paths:
        raise ValueError('No CLI release archives or packages found')
    checks = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in paths}
    checksum_path = directory / 'SHA256SUMS.txt'
    checksum_path.write_text(''.join(digest + '  ' + name + '\n' for name, digest in checks.items()))
    sbom = directory / 'nodexlm.cdx.json'
    inventory = json.loads(sbom.read_text())
    represented = set()
    for component in inventory['components']:
        if component['name'] in checks:
            component['hashes'] = [{'alg': 'SHA-256', 'content': checks[component['name']]}]
            represented.add(component['name'])
    if represented != checks.keys():
        raise ValueError('Release inventory does not cover every archive/package')
    sbom.write_text(json.dumps(inventory, indent=2) + '\n')
    if version:
        with tempfile.TemporaryDirectory(prefix='nodexlm-manifests-') as temp:
            subprocess.run([sys.executable, str(pathlib.Path(__file__).with_name('package-manifests.py')),
                            '--version', version, '--checksums', str(checksum_path), '--output', temp], check=True)
            archive = directory / 'nodexlm-registry-manifests.tar.gz'
            with tarfile.open(archive, 'w:gz') as target:
                for path in sorted(pathlib.Path(temp).iterdir()):
                    target.add(path, arcname=path.name)
            with checksum_path.open('a') as output:
                output.write(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + archive.name + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=pathlib.Path, required=True)
    parser.add_argument('--stable-version')
    args = parser.parse_args()
    finalize(args.directory, args.stable_version)
