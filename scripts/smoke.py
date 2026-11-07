#!/usr/bin/env python3
# Copyright (C) 2026 Rodrigo Felipe de Toni Grave
# SPDX-License-Identifier: AGPL-3.0-only
"""Exercise TLS enrollment, node long-poll, model forwarding and chat response."""
import http.server
import os
import pathlib
import socket
import subprocess
import tempfile
import threading
import time
import json

ROOT = pathlib.Path(__file__).resolve().parents[1]
CLI = ROOT / 'nodexlm'


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


class MockInference(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        content = self.rfile.read(int(self.headers['Content-Length']))
        req = json.loads(content)
        assert req['messages'][0]['content'] == 'Hello Nodex'
        payload = json.dumps({'choices': [{'message': {'role': 'assistant', 'content': 'READY FROM WORKER'}}]}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):
        pass


def execute(*args, env, timeout=20):
    p = subprocess.run([str(CLI), *args], env=env, text=True, capture_output=True, timeout=timeout)
    if p.returncode:
        raise AssertionError(f'{args}: {p.stderr}')
    return p.stdout


def main():
    if not CLI.exists():
        raise SystemExit('Compile CLI first: go build -o nodexlm ./cmd/nodexlm')
    with tempfile.TemporaryDirectory() as home:
        d = pathlib.Path(home)
        admin = 'admin-' + 'a' * 40
        join = 'join-' + 'b' * 40
        env = {**os.environ, 'NODEXLM_HOME':home, 'NODEXLM_ADMIN_TOKEN':admin, 'NODEXLM_JOIN_TOKEN':join}
        backend_port = port()
        backend = http.server.ThreadingHTTPServer(('127.0.0.1', backend_port), MockInference)
        thread = threading.Thread(target=backend.serve_forever, daemon=True)
        thread.start()
        execute('cert', '--hosts', '127.0.0.1,localhost', env=env)
        coord_port = port()
        server = f'https://127.0.0.1:{coord_port}'
        web_port = port()
        admin_server = f'http://127.0.0.1:{web_port}'
        service = subprocess.Popen([str(CLI), 'coordinator', '--addr', f'127.0.0.1:{coord_port}', '--web-addr', f'127.0.0.1:{web_port}'], env=env,
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        node = None
        try:
            for _ in range(40):
                if service.poll() is not None:
                    raise AssertionError('coordinator quit on startup')
                try:
                    execute('nodes','--server',admin_server,env=env,timeout=2)
                    break
                except Exception:
                    time.sleep(.15)
            else:
                raise AssertionError('coordinator not ready')
            execute('join','--server',server,'--ca',str(d/'server.crt'),'--name','sample-linux', '--model','test.gguf',env=env)
            node = subprocess.Popen([str(CLI),'node','--model','test.gguf','--backend',f'http://127.0.0.1:{backend_port}',
                                     '--ca',str(d/'server.crt')],env=env, stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            node_id = json.loads((d/'agent.json').read_text())['node']['node_id']
            out = execute('chat','--server',admin_server,'--node',node_id,'--model','test.gguf','--prompt','Hello Nodex',env=env,timeout=20)
            assert out.strip()=='READY FROM WORKER',out
            print('PASS: HTTPS -> enroll -> lease -> local backend -> job result -> chat')
        finally:
            if node: node.terminate();node.wait(timeout=10)
            service.terminate();service.wait(timeout=10)
            backend.shutdown()

if __name__=='__main__':
    main()
