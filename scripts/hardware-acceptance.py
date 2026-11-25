#!/usr/bin/env python3
"""Collect real Host/Guest measurements without claiming physical-device acceptance."""
import argparse
import datetime
import json
import os
import pathlib
import ssl
import subprocess
import time
import urllib.parse
import urllib.error
import urllib.request


def collect(base, token, model, node, repeat, environment, ca=None):
    parsed = urllib.parse.urlsplit(base)
    if parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in ('', '/'):
        raise ValueError('Use a management origin without credentials, path or query')
    if not parsed.hostname or not (parsed.scheme == 'https' or parsed.scheme == 'http' and parsed.hostname in ('127.0.0.1', 'localhost', '::1')):
        raise ValueError('Management requires HTTPS outside loopback')
    context = ssl.create_default_context(cafile=ca)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))

    def api(path, data=None):
        request = urllib.request.Request(base.rstrip('/') + path,
            data=None if data is None else json.dumps(data).encode(),
            headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
        try:
            with opener.open(request, timeout=240) as response:
                raw = response.read(2 * 1024 * 1024 + 1)
        except urllib.error.HTTPError as error:
            detail = error.read(4096).decode('utf-8', errors='replace')
            raise RuntimeError(f'{path}: HTTP {error.code}: {detail}') from error
        if len(raw) > 2 * 1024 * 1024:
            raise ValueError('Measurement response exceeded limit')
        return json.loads(raw)

    if api('/api/resident-models'):
        raise ValueError('Unload resident models deliberately before benchmarking; this tool never evicts them')
    before = api('/v1/nodes')
    settings = api('/api/status')['settings']
    plan = None
    if node == 'cluster':
        if settings.get('model') != model or not settings.get('distributed'):
            raise ValueError('Select the target GGUF and enable distributed placement before cluster benchmarking')
        plan = api('/api/plan')
        targets = [p['node_id'] for p in plan['nodes'] if p.get('node_id') and p.get('layers', 0) > 0]
        if not targets:
            raise ValueError('Cluster plan has no remote layers; choose capacity mode if appropriate')
    elif node:
        if not any(item['id'] == node and item.get('status') == 'online' for item in before):
            raise ValueError('Requested Guest is not online')
        targets = [node]
    else:
        targets = []
    def wait_for_idle_guests():
        if not targets:
            return
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            profiles = api('/v1/nodes')
            observations = api('/api/observability')['nodes']
            ready = {p['id'] for p in profiles if p.get('status') == 'online' and not p.get('busy') and p.get('runtime', {}).get('rpc_ready')}
            idle = {p['node_id'] for p in observations if p.get('tunnels', {}).get('idle', 0) > 0}
            if set(targets) <= ready & idle:
                return
            time.sleep(.2)
        raise RuntimeError('Guest did not return to idle RPC readiness within 15 seconds')

    measurements = []
    started = time.monotonic()
    for index in range(repeat):
        sample = {}
        wait_for_idle_guests()
        if targets:
            networks = {target: api('/v1/bench/' + urllib.parse.quote(target, safe=''), {}) for target in targets}
            if node == 'cluster':
                sample['networks'] = networks
            else:
                sample['network'] = networks[node]
            wait_for_idle_guests()
        if api('/api/status')['settings'] != settings:
            raise RuntimeError('Settings changed during measurement; discard the session')
        sample['rpc_stats_before'] = api('/api/cluster')['rpc_stats']
        sample['inference'] = api('/api/bench/inference', {'model': model, 'node_id': node})
        sample['rpc_stats_after'] = api('/api/cluster')['rpc_stats']
        if api('/api/status')['settings'] != settings:
            raise RuntimeError('Settings changed during measurement; discard the session')
        measurements.append(sample)
    return {'schema': 1, 'kind': 'measured-ci' if environment == 'ci' else 'measured-device-session',
            'physical_acceptance': 'not-established',
            'notice': 'Real measurements only. Operator must verify physical hardware, GPU backend, thermal/battery and screen-off behavior.',
            'recorded_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
            'duration_seconds': time.monotonic() - started, 'model': model, 'node_id': node,
            'settings': settings, 'plan': plan,
            'nodes_before': before, 'nodes_after': api('/v1/nodes'), 'measurements': measurements,
            'observations': api('/api/observability')}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--host', default='http://127.0.0.1:8787')
    parser.add_argument('--model', required=True)
    parser.add_argument('--node', default='', help='empty=Host, Guest ID, or cluster')
    parser.add_argument('--repeat', type=int, default=10)
    parser.add_argument('--environment', choices=('device', 'ci'), default='device')
    parser.add_argument('--ca')
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--cli', default='nodexlm')
    args = parser.parse_args()
    if not 1 <= args.repeat <= 100:
        parser.error('repeat must be between 1 and 100')
    if args.output.exists():
        parser.error('output already exists; preserve prior evidence')
    token = os.environ.get('NODEXLM_ADMIN_TOKEN') or subprocess.check_output([args.cli, 'web-token'], text=True).strip()
    report = collect(args.host, token, args.model, args.node, args.repeat, args.environment, args.ca)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open('x') as output:
        os.chmod(args.output, 0o600)
        json.dump(report, output, indent=2)
        output.write('\n')
    print('Real measurements saved. Physical acceptance remains subject to device/operator checks.')


if __name__ == '__main__':
    main()
