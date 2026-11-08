#!/usr/bin/env python3
"""Acceptance against a real local GGUF via the official OpenAI Python SDK.
Requires NODEXLM_RPC_DIR, NODEXLM_TEST_MODEL, a built nodexlm CLI,
openai and openai-agents.
Does not call the OpenAI cloud or execute any function requested by a model.
"""
import json
import os
import pathlib
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

from openai import OpenAI, NotFoundError, AuthenticationError, DefaultHttpxClient

ROOT = pathlib.Path(__file__).resolve().parents[1]


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    runtime = pathlib.Path(os.environ['NODEXLM_RPC_DIR']).resolve()
    model = pathlib.Path(os.environ['NODEXLM_TEST_MODEL']).resolve()
    cli = ROOT / ('nodexlm.exe' if os.name == 'nt' else 'nodexlm')
    with tempfile.TemporaryDirectory(prefix='nodexlm-openai-') as temp:
        host_dir = pathlib.Path(temp)
        models = host_dir / 'models'
        models.mkdir()
        shutil.copyfile(model, models / 'acceptance.gguf')
        if embedding := os.environ.get('NODEXLM_TEST_EMBEDDING'):
            shutil.copyfile(embedding, models / 'embedding.gguf')
        port = free_port()
        env = {**os.environ, 'NODEXLM_API_KEY': 'sdk-' + 'c' * 48,
               'LD_LIBRARY_PATH': str(runtime / 'bin')}
        env.pop('NODEXLM_ADMIN_TOKEN', None)
        env.pop('NODEXLM_JOIN_TOKEN', None)
        log = (host_dir / 'host.log').open('w+')
        process = subprocess.Popen([str(cli), 'host', '--dir', temp,
            '--addr', f'127.0.0.1:{free_port()}', '--web-addr', f'127.0.0.1:{port}',
            '--discovery-port', str(free_port()), '--llama-bin', str(runtime / 'bin' / ('llama-server.exe' if os.name == 'nt' else 'llama-server'))], env=env, stdout=log, stderr=log)
        origin = f'http://127.0.0.1:{port}'
        try:
            for _ in range(200):
                if (host_dir / 'secrets.json').exists():
                    token = json.loads((host_dir / 'secrets.json').read_text())['admin']
                    try:
                        urllib.request.urlopen(urllib.request.Request(origin + '/api/status', headers={'Authorization': 'Bearer ' + token}), timeout=1).close()
                        break
                    except OSError:
                        pass
                if process.poll() is not None:
                    raise RuntimeError('Host exited')
                time.sleep(.1)
            else:
                raise TimeoutError('Host did not start')

            def api(path, body=None, method='GET'):
                data = None if body is None else json.dumps(body).encode()
                request = urllib.request.Request(origin + path, data=data, method=method,
                    headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
                with urllib.request.urlopen(request, timeout=120) as response:
                    return json.load(response)

            api('/api/settings', {'model': 'acceptance.gguf', 'threads': 2, 'context_tokens': 4096, 'gpu_layers': '0'}, 'PUT')
            api('/api/load', {}, 'POST')
            for _ in range(1200):
                state = api('/api/status')['runtime']
                if state.get('loaded'):
                    break
                if state.get('phase') == 'error':
                    raise RuntimeError(state.get('error'))
                time.sleep(.1)
            else:
                raise TimeoutError('Native model did not load')
            client = OpenAI(base_url=origin + '/v1', api_key=env['NODEXLM_API_KEY'], timeout=90, max_retries=0, http_client=DefaultHttpxClient(trust_env=False))
            name = 'acceptance.gguf'
            assert client.models.list().data[0].id == name
            assert client.models.retrieve(name).id == name
            try:
                OpenAI(base_url=origin + '/v1', api_key='invalid', max_retries=0, http_client=DefaultHttpxClient(trust_env=False)).models.list()
                raise AssertionError('Bad API key accepted')
            except AuthenticationError:
                pass
            messages = [{'role': 'developer', 'content': 'Respond briefly.'}, {'role': 'user', 'content': [{'type': 'text', 'text': 'Once upon a time'}]}]
            completion = client.chat.completions.create(model=name, messages=messages, max_completion_tokens=8, temperature=0)
            assert completion.choices and completion.usage and completion.usage.completion_tokens > 0
            chunks = list(client.chat.completions.create(model=name, messages=messages, max_completion_tokens=8, stream=True, stream_options={'include_usage': True}))
            assert any(c.choices and c.choices[0].delta.content for c in chunks)
            assert any(c.usage for c in chunks)
            print('PASS: official SDK model discovery, authentication, content parts, developer role, token alias, SSE and usage', flush=True)
            assert client.completions.create(model=name, prompt='Once upon a time', max_tokens=8).choices
            assert list(client.completions.create(model=name, prompt='Once upon a time', max_tokens=8, stream=True))
            assert api('/v1/responses/input_tokens', {'model': name, 'input': 'Hello'}, 'POST')['input_tokens'] > 0
            assert api('/v1/chat/completions/input_tokens', {'model': name, 'messages': [{'role': 'user', 'content': 'Hello'}]}, 'POST')['input_tokens'] > 0


            schema = {'type': 'object', 'properties': {'city': {'type': 'string', 'enum': ['Joinville']}}, 'required': ['city'], 'additionalProperties': False}
            function = {'name': 'weather', 'description': 'Read the weather.', 'parameters': schema, 'strict': True}
            tools = [{'type': 'function', 'function': function}]
            tool_messages = [{'role': 'user', 'content': 'Use weather for Joinville.'}]
            called = client.chat.completions.create(model=name, messages=tool_messages, tools=tools, tool_choice='required', max_completion_tokens=128, temperature=0)
            call = called.choices[0].message.tool_calls[0]
            assert call.function.name == 'weather' and json.loads(call.function.arguments) == {'city': 'Joinville'}
            assert called.choices[0].finish_reason == 'tool_calls'
            completed_messages = tool_messages + [called.choices[0].message.model_dump(exclude_none=True), {'role': 'tool', 'tool_call_id': call.id, 'content': '{"temperature":20}'}]
            final = client.chat.completions.create(model=name, messages=completed_messages, tools=tools, tool_choice='none', max_completion_tokens=8)
            assert final.choices[0].message.content is not None
            streaming = list(client.chat.completions.create(model=name, messages=tool_messages, tools=tools, tool_choice='required', stream=True, max_completion_tokens=128, temperature=0))
            args = ''.join(c.choices[0].delta.tool_calls[0].function.arguments or '' for c in streaming if c.choices and c.choices[0].delta.tool_calls and c.choices[0].delta.tool_calls[0].function)
            assert json.loads(args) == {'city': 'Joinville'}
            structured = client.chat.completions.create(model=name, messages=tool_messages, max_completion_tokens=64,
                response_format={'type': 'json_schema', 'json_schema': {'name': 'weather_result', 'strict': True, 'schema': schema}})
            assert json.loads(structured.choices[0].message.content) == {'city': 'Joinville'}
            print('PASS: real tool call, client tool result, next turn, incremental tool arguments and strict JSON schema', flush=True)
            legacy = client.chat.completions.create(model=name, messages=tool_messages, functions=[function], function_call={'name': 'weather'}, max_completion_tokens=128, temperature=0)
            assert legacy.choices[0].message.function_call.name == 'weather' and legacy.choices[0].finish_reason == 'function_call'
            legacy_messages = tool_messages + [legacy.choices[0].message.model_dump(exclude_none=True), {'role': 'function', 'name': 'weather', 'content': '20C'}]
            assert client.chat.completions.create(model=name, messages=legacy_messages, functions=[function], function_call='none', max_completion_tokens=8).choices
            print('PASS: legacy functions/function_call request, response and function result loop', flush=True)
            legacy_stream = list(client.chat.completions.create(model=name, messages=tool_messages, functions=[function], function_call={'name': 'weather'}, max_completion_tokens=128, temperature=0, stream=True))
            assert any(c.choices and c.choices[0].delta.function_call for c in legacy_stream)



            response = client.responses.create(model=name, input='Once upon a time', max_output_tokens=8)
            assert response.id and response.output_text
            assert client.responses.retrieve(response.id).id == response.id
            assert client.responses.input_items.list(response.id, limit=1).data
            continued = client.responses.create(model=name, previous_response_id=response.id, input='Continue.', max_output_tokens=8)
            assert continued.output
            events = list(client.responses.create(model=name, input='Once upon a time', max_output_tokens=8, stream=True))
            assert any(e.type == 'response.output_text.delta' for e in events)
            assert any(e.type in ('response.completed', 'response.incomplete') for e in events)
            response_tool = client.responses.create(model=name, input='Use weather for Joinville.', tools=[{'type': 'function', **function}], tool_choice='required', max_output_tokens=128, temperature=0)
            call = next(item for item in response_tool.output if item.type == 'function_call')
            assert call.name == 'weather' and json.loads(call.arguments) == {'city': 'Joinville'}
            response_final = client.responses.create(model=name, previous_response_id=response_tool.id,
                input=[{'type': 'function_call_output', 'call_id': call.call_id, 'output': '{"temperature":20}'}], max_output_tokens=8)
            assert response_final.output
            with client.responses.stream(model=name, input='Use weather for Joinville.', tools=[{'type': 'function', **function}], tool_choice='required', max_output_tokens=128, temperature=0) as stream:
                list(stream)
                streamed_tool = stream.get_final_response()
            assert any(item.type == 'function_call' and item.name == 'weather' for item in streamed_tool.output)
            private = client.responses.create(model=name, input='Once upon a time', max_output_tokens=8, store=False)
            try:
                client.responses.retrieve(private.id)
                raise AssertionError('store:false retained the response')
            except NotFoundError:
                pass
            client.responses.delete(response.id)
            try:
                client.responses.retrieve(response.id)
                raise AssertionError('Deleted response is still retrievable')
            except NotFoundError:
                pass
            print('PASS: Responses SDK, named SSE, storage, previous_response_id, function results, deletion and store:false', flush=True)
            # The official agent runner must complete a tool -> result -> final
            # answer loop on both adapters, without tracing to an external service.
            import asyncio
            from openai import AsyncOpenAI, DefaultAsyncHttpxClient
            from agents import Agent, Runner, ModelSettings, function_tool, OpenAIResponsesModel, OpenAIChatCompletionsModel
            from agents.tracing import set_trace_provider
            from agents.tracing.provider import DefaultTraceProvider
            tracing = DefaultTraceProvider()
            tracing.set_disabled(True)
            set_trace_provider(tracing)

            async def agent_acceptance():
                executed = []

                @function_tool
                def weather(city: str) -> str:
                    """Return the current weather for the requested city."""
                    executed.append(city)
                    return 'The temperature is 20 degrees Celsius.'

                async_client = AsyncOpenAI(base_url=origin + '/v1', api_key=env['NODEXLM_API_KEY'],
                    max_retries=0, http_client=DefaultAsyncHttpxClient(trust_env=False))
                try:
                    for adapter in (OpenAIChatCompletionsModel, OpenAIResponsesModel):
                        executed.clear()
                        agent = Agent(name='Compatibility acceptance',
                            instructions='Use the weather tool once for Joinville, then answer briefly with the temperature. Do not call other tools.',
                            model=adapter(model=name, openai_client=async_client), tools=[weather],
                            model_settings=ModelSettings(temperature=0, max_tokens=128, tool_choice='required'))
                        result = await Runner.run(agent, 'What is the temperature in Joinville?', max_turns=3)
                        assert executed and isinstance(result.final_output, str) and result.final_output, (executed, result.final_output)
                    executed.clear()
                    result = Runner.run_streamed(agent, 'What is the temperature in Joinville?', max_turns=3)
                    async for _ in result.stream_events():
                        pass
                    assert executed and result.final_output
                finally:
                    await async_client.close()

            asyncio.run(agent_acceptance())
            print('PASS: official Agents SDK tool execution and final answer over Chat Completions, Responses and streamed Responses', flush=True)
            if os.environ.get('NODEXLM_TEST_EMBEDDING'):
                api('/api/resident-models', {'model': 'embedding.gguf'}, 'POST')
                embeddings = client.embeddings.create(model='embedding.gguf', input=['Joinville', 'Santa Catarina'], encoding_format='float')
                assert len(embeddings.data) == 2 and len(embeddings.data[0].embedding) > 0 and embeddings.usage.prompt_tokens > 0
                encoded = client.embeddings.create(model='embedding.gguf', input='Joinville', encoding_format='base64')
                assert isinstance(encoded.data[0].embedding, str)
                api('/api/resident-models/embedding.gguf', method='DELETE')
                print('PASS: real dedicated embedding GGUF, batch vectors, usage and base64 encoding', flush=True)


        except Exception:
            log.flush()
            print((host_dir / 'host.log').read_text()[-12000:])
            raise
        finally:
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
            log.close()


if __name__ == '__main__':
    main()
