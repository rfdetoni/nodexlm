#!/usr/bin/env python3
"""Validate WinGet schemas and optional immutable download digests."""
import argparse
import hashlib
import json
import pathlib
import re
import urllib.request

import jsonschema
import yaml

# Official Microsoft schema source pinned at a reviewed commit, not mutable master.
SCHEMA_COMMIT = 'afc9a471496c4cd80ac363df269362f84ea5a457'
KINDS = {'installer', 'defaultLocale', 'version'}
DOWNLOAD = re.compile(r'https://github\.com/rfdetoni/nodexlm/releases/download/(?:v[0-9]+\.[0-9]+\.[0-9]+|nodexlm-preview-[a-f0-9]{8,40})/nodexlm_windows_(?:amd64|arm64)\.zip')


def validate(directory, downloads=False):
    files = sorted(directory.glob('*.yaml'))
    kinds = set()
    for file in files:
        data = yaml.safe_load(file.read_text())
        kind = data.get('ManifestType')
        if kind not in KINDS or data.get('ManifestVersion') != '1.6.0' or kind in kinds:
            raise ValueError('Unexpected or duplicate manifest type/version')
        kinds.add(kind)
        url = f'https://raw.githubusercontent.com/microsoft/winget-cli/{SCHEMA_COMMIT}/schemas/JSON/manifests/v1.6.0/manifest.{kind}.1.6.0.json'
        with urllib.request.urlopen(url, timeout=30) as source:
            schema = json.load(source)
        validator = jsonschema.validators.validator_for(schema)(schema, format_checker=jsonschema.FormatChecker())
        validator.validate(data)
        if kind == 'installer':
            for installer in data['Installers']:
                source = installer['InstallerUrl']
                if not DOWNLOAD.fullmatch(source):
                    raise ValueError('Installer must reference an immutable NodexLM release')
                if downloads:
                    with urllib.request.urlopen(source, timeout=60) as response:
                        content = response.read(64 * 1024 * 1024 + 1)
                    if len(content) > 64 * 1024 * 1024 or hashlib.sha256(content).hexdigest().upper() != installer['InstallerSha256'].upper():
                        raise ValueError('Published installer does not match manifest SHA-256')
        print(file.name + ': official Microsoft schema validated')
    if kinds != KINDS:
        raise ValueError('Installer, default locale and version manifests are all required')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=pathlib.Path, required=True)
    parser.add_argument('--check-downloads', action='store_true')
    args = parser.parse_args()
    validate(args.directory, args.check_downloads)
