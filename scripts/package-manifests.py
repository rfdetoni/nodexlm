#!/usr/bin/env python3
"""Generate Homebrew/winget manifests using actual stable release checksums.
Publishing to their upstream repositories remains a separate review process.
"""
import argparse,json,pathlib,re
p=argparse.ArgumentParser();p.add_argument('--version',required=True);p.add_argument('--checksums',type=pathlib.Path,required=True);p.add_argument('--output',type=pathlib.Path,required=True);a=p.parse_args()
if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+',a.version):p.error('only stable X.Y.Z releases can generate registry manifests')
checks={}
for line in a.checksums.read_text().splitlines():
 fields=line.split()
 if len(fields)==2 and re.fullmatch('[a-fA-F0-9]{64}',fields[0]):checks[fields[1].lstrip('*')]=fields[0].lower()
base=f'https://github.com/rfdetoni/nodexlm/releases/download/v{a.version}/';a.output.mkdir(parents=True,exist_ok=True)
formula=['class Nodexlm < Formula','  desc "Local distributed GGUF inference"','  homepage "https://github.com/rfdetoni/nodexlm"',f'  version "{a.version}"','  license "AGPL-3.0-only"']
for os,ruby in [('darwin','macos'),('linux','linux')]:
 formula.append(f'  on_{ruby} do')
 for arch,condition in [('arm64','Hardware::CPU.arm?'),('amd64','Hardware::CPU.intel?')]:
  filename=f'nodexlm_{os}_{arch}.tar.gz';digest=checks[filename]
  formula+= [f'    if {condition}',f'      url "{base+filename}"',f'      sha256 "{digest}"','    end']
 formula.append('  end')
formula+=['  def install','    bin.install "nodexlm"','  end','  test do','    assert_match version.to_s, shell_output("#{bin}/nodexlm version")','  end','end']
(a.output/'nodexlm.rb').write_text('\n'.join(formula)+'\n')
identity=f'PackageIdentifier: rfdetoni.NodexLM\nPackageVersion: {a.version}\n'
(a.output/'rfdetoni.NodexLM.installer.yaml').write_text(identity+'''InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
  - RelativeFilePath: nodexlm.exe
    PortableCommandAlias: nodexlm
Installers:
'''+''.join(f'  - Architecture: {arch}\n    InstallerUrl: {base}nodexlm_windows_{goarch}.zip\n    InstallerSha256: {checks[f"nodexlm_windows_{goarch}.zip"].upper()}\n' for arch,goarch in [('x64','amd64'),('arm64','arm64')])+'ManifestType: installer\nManifestVersion: 1.6.0\n')
(a.output/'rfdetoni.NodexLM.locale.en-US.yaml').write_text(identity+'''PackageLocale: en-US
Publisher: Rodrigo de Toni
PackageName: NodexLM
License: AGPL-3.0-only
ShortDescription: Local distributed GGUF inference
ManifestType: defaultLocale
ManifestVersion: 1.6.0
''')
(a.output/'rfdetoni.NodexLM.yaml').write_text(identity+'DefaultLocale: en-US\nManifestType: version\nManifestVersion: 1.6.0\n')
