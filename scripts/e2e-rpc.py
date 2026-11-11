#!/usr/bin/env python3
"""Real TLS pairing -> native Guest -> distributed tiny GGUF -> OpenAI SSE.
Requires NODEXLM_RPC_DIR (matching native runtime), NODEXLM_TEST_MODEL and built CLI.
No model or runtime download is performed implicitly by this script.
"""
import json
import os
import pathlib
import re
import shutil
import socket
import ssl
import hashlib
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
CLI = ROOT / ('nodexlm.exe' if os.name=='nt' else 'nodexlm')


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def wait_for(check, seconds=30):
    deadline = time.monotonic() + seconds
    last = None
    while time.monotonic() < deadline:
        try:
            result = check()
            if result:
                return result
        except (OSError, ValueError, KeyError) as exc:
            last = exc
        time.sleep(.1)
    raise AssertionError(f'Timed out: {last}')


def main():
    runtime = pathlib.Path(os.environ['NODEXLM_RPC_DIR']).resolve()
    model = pathlib.Path(os.environ['NODEXLM_TEST_MODEL']).resolve()
    assert CLI.is_file() and model.is_file()
    processes = []
    guest_count = int(os.environ.get('NODEXLM_TEST_GUEST_COUNT','1'))
    assert guest_count in (1,2)
    with tempfile.TemporaryDirectory(prefix='nodexlm-e2e-') as tmp:
        root = pathlib.Path(tmp)
        host_dir, guest_dir = root/'host', root/'guest'
        host_dir.mkdir(mode=0o700)
        models = host_dir/'models'
        models.mkdir()
        shutil.copyfile(model, models/model.name)
        shutil.copyfile(model, models/"second.gguf")
        env = {**os.environ, 'LD_LIBRARY_PATH':str(runtime/'bin'),
               'NODEXLM_API_KEY':'inference-'+'c'*48}
        # Prevent shell-local credentials overriding the generated Host identity.
        env.pop('NODEXLM_ADMIN_TOKEN', None)
        env.pop('NODEXLM_JOIN_TOKEN', None)
        lan_port, web_port, discovery_port = port(), port(), port()
        base = f'http://127.0.0.1:{web_port}'
        log_path = root/'host.log'
        log = log_path.open('w+')
        guest_log = (root/'guest.log').open('w+')
        guest_logs = [guest_log]
        try:
            host = subprocess.Popen([str(CLI),'host','--dir',str(host_dir),
                '--addr',f'127.0.0.1:{lan_port}','--web-addr',f'127.0.0.1:{web_port}',
                '--discovery-port',str(discovery_port),'--rpc-transport',os.environ.get('NODEXLM_TEST_RPC_TRANSPORT','auto'),'--llama-bin',str(runtime/'bin'/('llama-server.exe' if os.name=='nt' else 'llama-server'))],
                env=env,stdout=log,stderr=log)
            processes.append(host)
            code = wait_for(lambda: re.search(r'PAIRING CODE: ([A-Z0-9]{8})',log_path.read_text()))[1]
            # A wrong full pin must fail before enrollment writes credentials.
            rejected=subprocess.run([str(CLI),'join','--dir',str(guest_dir),'--name','e2e-guest',
                '--server',f'https://127.0.0.1:{lan_port}','--code',code,'--fingerprint','0'*64],env=env,capture_output=True,timeout=20)
            assert rejected.returncode!=0 and not (guest_dir/'agent.json').exists(),'Wrong Host identity was enrolled'
            fingerprint=hashlib.sha256(ssl.PEM_cert_to_DER_cert((host_dir/'server.crt').read_text())).hexdigest()
            subprocess.run([str(CLI),'join','--dir',str(guest_dir),'--name','e2e-guest',
                '--server',f'https://127.0.0.1:{lan_port}','--code',code,'--fingerprint',fingerprint],env=env,check=True,capture_output=True,timeout=20)
            token = json.loads((host_dir/'secrets.json').read_text())['admin']

            def api(path, method='GET', body=None, key=token):
                data = None if body is None else json.dumps(body).encode()
                request = urllib.request.Request(base+path,method=method,data=data,
                    headers={'Authorization':'Bearer '+key,'Content-Type':'application/json'})
                try:
                    return urllib.request.urlopen(request,timeout=120)
                except urllib.error.HTTPError as exc:
                    if exc.code==401:raise
                    raise AssertionError(f"{method} {path}: HTTP {exc.code}: {exc.read().decode()}") from exc

            rpc = next(p for p in [runtime/'bin'/name for name in ('ggml-rpc-server','rpc-server','ggml-rpc-server.exe','rpc-server.exe')] if p.exists())
            guest = subprocess.Popen([str(CLI),'rpc-node','--rpc-device',os.environ.get('NODEXLM_TEST_RPC_DEVICE',''),'--dir',str(guest_dir),'--rpc-bin',str(rpc),'--rpc-addr',('127.0.0.1:0' if os.environ.get('NODEXLM_TEST_RPC_TRANSPORT')=='tcp' else 'auto')],
                env=env,stdout=guest_log,stderr=guest_log)
            processes.append(guest)
            if guest_count == 2:
                # Second independently enrolled Linux worker exercises the
                # parallel per-device loading path. No shared Guest identity.
                second_dir = root/'guest2'
                second_dir.mkdir(mode=0o700)
                with api('/v1/pairing/start','POST',{}) as resp:
                    code2 = json.load(resp)['code']
                subprocess.run([str(CLI),'join','--dir',str(second_dir),'--name','e2e-guest-2',
                    '--server',f'https://127.0.0.1:{lan_port}','--code',code2,'--fingerprint',fingerprint],
                    env=env,check=True,capture_output=True,timeout=20)
                second_log = (root/'guest2.log').open('w+')
                guest_logs.append(second_log)
                guest2 = subprocess.Popen([str(CLI),'rpc-node','--rpc-device',os.environ.get('NODEXLM_TEST_RPC_DEVICE',''),
                    '--dir',str(second_dir),'--rpc-bin',str(rpc),
                    '--rpc-addr',('127.0.0.1:0' if os.environ.get('NODEXLM_TEST_RPC_TRANSPORT')=='tcp' else 'auto')],
                    env=env,stdout=second_log,stderr=second_log)
                processes.append(guest2)
            nodes = wait_for(lambda: (
                ready if len(ready)>=guest_count else None
            ) if (ready:=[n for n in json.load(api('/v1/nodes')) if n['runtime']['rpc_ready']]) else None,120)
            # Exercise the benchmark marker through a genuine enrolled TLS tunnel.
            measurement = json.load(api('/v1/bench/'+nodes[0]['id'],'POST',{}))
            assert measurement['mib_per_second'] > 0
            settings = json.load(api('/api/status'))['settings']
            settings.update(model=model.name,distributed=True,context_tokens=256,threads=2,gpu_layers=os.environ.get('NODEXLM_TEST_GPU_LAYERS','auto'),placement_strategy='capacity')
            api('/api/settings','PUT',settings).close()
            api('/api/load','POST',{}).close()
            def loaded():
                state = json.load(api('/api/status'))['runtime']
                if state.get('error'):
                    raise AssertionError(state['error'])
                return state['loaded']
            wait_for(loaded,120)
            listed = json.load(api('/v1/models',key=env['NODEXLM_API_KEY']))
            assert listed['data'][0]['id'] == model.name
            chunks = []
            with api('/v1/chat/completions','POST',{'model':model.name,'messages':[
                    {'role':'user','content':'Once upon a time'}],'max_tokens':8,'stream':True},key=env['NODEXLM_API_KEY']) as stream:
                assert stream.headers['Content-Type'].startswith('text/event-stream')
                done = False
                for line in stream:
                    if line.startswith(b'data: '):
                        payload = line[6:].strip()
                        if payload == b'[DONE]':
                            done = True
                            break
                        data = json.loads(payload)
                        assert 'error' not in data, data
                        chunks.append(data)
            assert done and chunks, 'Missing token stream or DONE'
            # Separate native processes keep model KV state isolated.
            residents=json.load(api('/api/resident-models','POST',{'model':'second.gguf'}))
            assert set(residents)=={model.name,'second.gguf'},residents
            reply=json.load(api('/v1/chat/completions','POST',{'model':'second.gguf','messages':[{'role':'user','content':'Once upon a time'}],'max_tokens':4,'cache_prompt':True},key=env['NODEXLM_API_KEY']))
            assert reply['model']=='second.gguf' and reply['choices']
            api('/api/resident-models/second.gguf','DELETE').close()
            # Benchmark a genuine Guest inference workload after unloading.
            api('/api/unload','POST',{}).close()
            measured=json.load(api('/api/bench/inference','POST',{'model':model.name,'node_id':nodes[0]['id']}))
            assert measured['tokens_per_second']>0 and measured['tokens']>0,measured
            # Exercise the same collection tool used for physical-device sessions,
            # explicitly labelled CI so it cannot masquerade as hardware acceptance.
            import importlib.util
            spec=importlib.util.spec_from_file_location('acceptance',ROOT/'scripts/hardware-acceptance.py')
            acceptance=importlib.util.module_from_spec(spec);spec.loader.exec_module(acceptance)
            report=acceptance.collect(base,token,model.name,nodes[0]['id'],2,'ci')
            assert report['physical_acceptance']=='not-established' and len(report['measurements'])==2
            assert all(p['inference']['tokens_per_second']>0 for p in report['measurements'])
            # An inference key must not access administrative resources.
            try:
                api('/api/settings','PUT',settings,key=env['NODEXLM_API_KEY'])
                raise AssertionError('inference key allowed administration')
            except urllib.error.HTTPError as exc:
                assert exc.code == 401
            if os.environ.get('NODEXLM_UI_CHECK'):
                api('/api/load','POST',{}).close()
                wait_for(loaded,120)
                result=subprocess.run(['node',os.environ['NODEXLM_UI_CHECK'],base,token],timeout=90)
                assert result.returncode==0,'Browser console verification failed'
            print(f'PASS: {guest_count} Guests, pairing, TLS benchmark, native RPC, resident model pool, inference benchmark, SSE and restricted API key')
        finally:
            for process in reversed(processes):
                if os.name=='nt':
                    # Windows terminate() skips the Go signal handler. Tear down
                    # the test's native child tree before closing captured logs.
                    subprocess.run(['taskkill','/PID',str(process.pid),'/T','/F'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
                else:
                    process.terminate()
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            log.close()
            for guest_log in guest_logs:
                guest_log.close()
            if any(p.returncode not in (0,-15) for p in processes):
                print(log_path.read_text()[-12000:])
                print((root/'guest.log').read_text()[-6000:])


if __name__ == '__main__':
    main()
