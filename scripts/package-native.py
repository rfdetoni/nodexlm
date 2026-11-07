#!/usr/bin/env python3
"""Build native Linux packages from an already compiled, reviewed CLI binary.
No services are enabled or started by these packages.
"""
import argparse,pathlib,re,shutil,subprocess,tempfile
p=argparse.ArgumentParser();p.add_argument('--binary',type=pathlib.Path,required=True);p.add_argument('--version',required=True);p.add_argument('--arch',choices=['amd64','arm64','armhf'],required=True);p.add_argument('--format',choices=['deb','rpm'],required=True);p.add_argument('--output',type=pathlib.Path,required=True);a=p.parse_args()
if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:[-+.][A-Za-z0-9.]+)?',a.version):p.error('version must be a semantic release version')
root=pathlib.Path(__file__).resolve().parents[1];a.output.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='nodexlm-package-') as temp:
 stage=pathlib.Path(temp)/'root';(stage/'usr/bin').mkdir(parents=True);shutil.copyfile(a.binary,stage/'usr/bin/nodexlm');(stage/'usr/bin/nodexlm').chmod(0o755)
 units=stage/'usr/lib/systemd/system';units.mkdir(parents=True)
 for source in (root/'deploy').glob('*.service'):units.joinpath(source.name).write_text(source.read_text().replace('/usr/local/bin/nodexlm','/usr/bin/nodexlm'))
 doc=stage/'usr/share/doc/nodexlm';doc.mkdir(parents=True);shutil.copyfile(root/'LICENSE',doc/'copyright');shutil.copyfile(root/'docs/OPERATIONS.md',doc/'OPERATIONS.md')
 users='''for account in nodexlm nodexlm-guest; do
    getent passwd "$account" >/dev/null || useradd --system --user-group --home-dir "/var/lib/$account" --shell /usr/sbin/nologin "$account"
done
'''
 if a.format=='deb':
  control=stage/'DEBIAN';control.mkdir()
  (control/'control').write_text(f'Package: nodexlm\nVersion: {a.version}\nArchitecture: {a.arch}\nMaintainer: NodexLM maintainers\nDepends: ca-certificates, libstdc++6, libgomp1, passwd\nSection: utils\nPriority: optional\nDescription: Local distributed GGUF inference over authenticated TLS\n')
  (control/'postinst').write_text('#!/bin/sh\nset -eu\nif [ "$1" = configure ]; then\n'+users+'fi\n');(control/'postinst').chmod(0o755)
  subprocess.run(['dpkg-deb','--build','--root-owner-group',str(stage),str(a.output/f'nodexlm_{a.version}_{a.arch}.deb')],check=True)
 else:
  top=pathlib.Path(temp)/'rpm';top.mkdir();arch={'amd64':'x86_64','arm64':'aarch64','armhf':'armv7hl'}[a.arch];version=a.version.replace('-','_').replace('+','_')
  spec=top/'nodexlm.spec';spec.write_text(f'''Name: nodexlm
Version: {version}
Release: 1
Summary: Local distributed GGUF inference
License: AGPL-3.0-only
Requires: ca-certificates, libstdc++, libgomp, shadow-utils
AutoReqProv: no
%description
Local inference Host and Guest over authenticated TLS.
%install
mkdir -p %{{buildroot}}
cp -a {stage}/. %{{buildroot}}/
%pre
{users}
%files
/usr/bin/nodexlm
/usr/lib/systemd/system/*.service
/usr/share/doc/nodexlm/*
''')
  subprocess.run(['rpmbuild','-bb','--target',arch,'--define',f'_topdir {top}',str(spec)],check=True)
  for package in top.rglob('*.rpm'):shutil.copyfile(package,a.output/package.name)
