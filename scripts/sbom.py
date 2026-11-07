#!/usr/bin/env python3
"""CycloneDX inventory of shipped CLI archives (native runtimes are separate)."""
import argparse
import datetime
import hashlib
import json
import pathlib
import uuid

parser=argparse.ArgumentParser()
parser.add_argument('--directory',required=True,type=pathlib.Path)
parser.add_argument('--version',required=True)
parser.add_argument('--commit',required=True)
parser.add_argument('--go-version',required=True)
args=parser.parse_args()
archives=sorted([*args.directory.glob('nodexlm_*.tar.gz'),*args.directory.glob('nodexlm_*.zip')])
if not archives:
    parser.error('no release archives found')
components=[{'type':'platform','bom-ref':'go-stdlib','name':'Go standard library','version':args.go_version,
             'licenses':[{'license':{'id':'BSD-3-Clause'}}]}]
for path in archives:
    digest=hashlib.file_digest(path.open('rb'),'sha256').hexdigest()
    components.append({'type':'file','bom-ref':path.name,'name':path.name,'version':args.version,
                       'hashes':[{'alg':'SHA-256','content':digest}]})
bom={'bomFormat':'CycloneDX','specVersion':'1.6','serialNumber':'urn:uuid:'+str(uuid.uuid4()),'version':1,
     'metadata':{'timestamp':datetime.datetime.now(datetime.timezone.utc).isoformat(),
                 'component':{'type':'application','bom-ref':'nodexlm','name':'NodexLM','version':args.version,
                    'licenses':[{'license':{'id':'AGPL-3.0-only'}}],
                    'properties':[{'name':'git:commit','value':args.commit}]}},
     'components':components,
     'dependencies':[{'ref':'nodexlm','dependsOn':[c['bom-ref'] for c in components]}]}
(args.directory/'nodexlm.cdx.json').write_text(json.dumps(bom,indent=2)+'\n')
