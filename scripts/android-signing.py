#!/usr/bin/env python3
"""Create or install a persistent Android signing identity without logging secrets."""
import argparse
import base64
import hashlib
import json
import os
import pathlib
import secrets
import shutil
import ssl
import subprocess
import sys

ALIAS = 'nodexlm-android-release'
NAMES = ('ANDROID_KEYSTORE_BASE64', 'ANDROID_KEYSTORE_PASSWORD', 'ANDROID_KEY_ALIAS', 'ANDROID_KEY_PASSWORD')


def private_write(path, data):
    with path.open('x', encoding='utf-8') as output:
        os.chmod(path, 0o600)
        output.write(data)


def generate(directory):
    if any((parent / '.git').exists() for parent in (directory, *directory.parents)):
        raise ValueError('Generate signing identities outside Git repositories')
    directory.mkdir(mode=0o700, parents=True, exist_ok=False)
    password = secrets.token_urlsafe(36)
    environment = {**os.environ, 'NODEXLM_NEW_STORE_PASS': password}
    key = directory / 'android-release.p12'
    try:
        subprocess.run(['keytool', '-genkeypair', '-noprompt', '-storetype', 'PKCS12', '-keystore', str(key),
                        '-storepass:env', 'NODEXLM_NEW_STORE_PASS', '-keypass:env', 'NODEXLM_NEW_STORE_PASS',
                        '-alias', ALIAS, '-keyalg', 'RSA', '-keysize', '4096', '-sigalg', 'SHA256withRSA',
                        '-validity', '10000', '-dname', 'CN=NodexLM Android Release,O=NodexLM'],
                       env=environment, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        os.chmod(key, 0o600)
        subprocess.run(['keytool', '-exportcert', '-rfc', '-keystore', str(key), '-storepass:env', 'NODEXLM_NEW_STORE_PASS',
                        '-alias', ALIAS, '-file', str(directory / 'certificate.pem')], env=environment,
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        certificate = ssl.PEM_cert_to_DER_cert((directory / 'certificate.pem').read_text())
        private_write(directory / 'password.txt', password + '\n')
        private_write(directory / 'identity.json', json.dumps({'alias': ALIAS,
                      'certificate_sha256': hashlib.sha256(certificate).hexdigest()}, indent=2) + '\n')
    except Exception:
        # Never leave a key whose only password copy was lost during generation.
        shutil.rmtree(directory)
        raise
    print('Persistent Android signing identity created. Keep password.txt separate from the encrypted keystore backup.')


def configure(directory, repository):
    metadata = json.loads((directory / 'identity.json').read_text())
    password = (directory / 'password.txt').read_text().strip()
    if len(password) < 16:
        raise ValueError('Signing password is missing or too short')
    names = json.loads(subprocess.check_output(['gh', 'secret', 'list', '--repo', repository, '--json', 'name'], text=True))
    if set(NAMES).intersection(item['name'] for item in names):
        raise ValueError('Android signing secrets already exist; refusing to rotate or partially replace an identity')
    environment = {**os.environ, 'NODEXLM_NEW_STORE_PASS': password}
    certificate = subprocess.check_output(['keytool', '-exportcert', '-rfc', '-keystore', str(directory / 'android-release.p12'),
                    '-storepass:env', 'NODEXLM_NEW_STORE_PASS', '-alias', metadata['alias']], env=environment, stderr=subprocess.PIPE, text=True)
    digest = hashlib.sha256(ssl.PEM_cert_to_DER_cert(certificate)).hexdigest()
    if digest != metadata['certificate_sha256']:
        raise ValueError('Backup certificate identity does not match metadata')
    values = [base64.b64encode((directory / 'android-release.p12').read_bytes()).decode(), password, metadata['alias'], password]
    installed = []
    try:
        for name, value in zip(NAMES, values):
            subprocess.run(['gh', 'secret', 'set', name, '--repo', repository], input=value, text=True,
                           check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            installed.append(name)
    except Exception as error:
        # Do not silently overwrite or remove credentials after partial remote success.
        raise RuntimeError('Configuration stopped; installed secret names: ' + ', '.join(installed) + '. Retain this same backup to complete setup.') from error
    print('Four Android signing secrets configured. Public certificate SHA-256: ' + digest)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('generate', 'configure'))
    parser.add_argument('--directory', required=True, type=pathlib.Path)
    parser.add_argument('--repository', default='rfdetoni/nodexlm')
    args = parser.parse_args()
    if args.action == 'generate':
        generate(args.directory.resolve())
    else:
        configure(args.directory.resolve(), args.repository)


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        # Command exceptions may contain stdout/stderr. Never echo secret values.
        print('Signing setup failed: ' + (str(error) if isinstance(error, (ValueError, RuntimeError)) else type(error).__name__), file=sys.stderr)
        sys.exit(1)
