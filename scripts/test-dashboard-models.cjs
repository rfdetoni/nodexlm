// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
class Element {
  constructor(tag) {this.tag=tag;this.children=[];this.handlers={};this.style={};this.dataset={};this.value='';this.checked=false;this.hidden=false;this.disabled=false;this._text='';}
  set textContent(value) {this._text=String(value);this.children=[];}
  get textContent() {return this._text+this.children.map(c=>c.textContent).join('');}
  append(...elements) {this.children.push(...elements);}
  replaceChildren(...elements) {this._text='';this.children=[...elements];}
  addEventListener(name,callback) {this.handlers[name]=callback;}
  focus() {this.focused=true;}
  setAttribute(name,value) {this[name]=String(value);}
}
const elements=new Map();
const get=id=>{if(!elements.has(id))elements.set(id,new Element(id));return elements.get(id)};
const settings={model:'',threads:2,context_tokens:2048,gpu_layers:'auto',moe_cpu:false,distributed:false,moe_cpu_layers:0,moe_cache_mib:0};
let loaded=false;
let download=null;
const locals=[];
const calls=[];
let pairingCalls = 0;
let pairingRequestFails = false;
const documentEvents = {};
const browserStorage = new Map();
const localStorage = {
  getItem(key) {return browserStorage.get(key)||null},
  setItem(key,value) {browserStorage.set(key,String(value))},
  removeItem(key) {browserStorage.delete(key)}
};
const response=data=>({ok:true,status:200,text:async()=>JSON.stringify(data)});
const fetch=async (url,options)=>{
  const method=options.method;
  const body=options.body?JSON.parse(options.body):null;
  calls.push({url,method,body});
  if(url==='/api/status')return response({settings:{...settings},runtime:{loaded},download});
  if(url==='/v1/pairing/start' && method==='POST') {
    pairingCalls++;
    if(pairingRequestFails)return {ok:false,status:503,text:async()=>JSON.stringify({error:'Host offline'})};
    const codes=['ABCD2345','EFGH6789','JKMN2345','PQRS2345'];
    return response({code:codes[(pairingCalls-1)%codes.length],expires_in:'10m',expires_in_ms:600000});
  }
  if(url==='/api/inference/topology')return response({loaded:false,nodes:[]});
  if(url==='/api/models')return response(locals);
  if(url==='/api/resident-models')return response(loaded?[settings.model]:[]);
  if(url==='/v1/nodes')return response([]);
  if(url==='/api/cluster')return response({resources:{online:1,aggregate_budget_bytes:8*1073741824},rpc_guests:{}});
  if(url==='/api/download' && method==='POST') {download={name:body.name,status:'downloading',received:0,total:2*1073741824};return response({status:'downloading'});}
  if(url==='/api/settings' && method==='PUT'){Object.assign(settings,body);return response(settings);}
  if(url==='/api/load' && method==='POST'){loaded=true;return response({loaded});}
  if(url==='/api/unload' && method==='POST'){loaded=false;return response({loaded});}
  throw Error(`Unexpected request ${method} ${url}`);
};
const document={getElementById:get,createElement:tag=>new Element(tag),hidden:false,
 addEventListener(name,handler){documentEvents[name]=handler;}};
const sandbox={document,performance:{now:()=>Date.now()},localStorage,sessionStorage:{getItem:()=>null,removeItem(){},setItem(){}},setInterval(){},setTimeout(){},clearTimeout(){},fetch,confirm:()=>true,URL,console};
vm.createContext(sandbox);
const script=fs.readFileSync(path.join(__dirname,'../internal/dashboard/static/app.js'),'utf8');
vm.runInContext(script,sandbox,{filename:'app.js'});
function checkResponsiveLayout(html,css){
  const {spawnSync}=require('node:child_process');
  const os=require('node:os');
  const browsers=[process.env.CHROME_BIN,'google-chrome','google-chrome-stable','chromium','chromium-browser'].filter(Boolean);
  const browser=browsers.find(bin=>spawnSync(bin,['--version'],{stdio:'ignore'}).status===0);
  if(!browser){
    if(process.env.GITHUB_ACTIONS==='true')throw Error('Headless Chromium is required for dashboard layout regression');
    console.log('Skipping real-browser layout checks: Chrome/Chromium unavailable');
    return;
  }
  // Inline the real stylesheet and remove the network-dependent application
  // script. Behavioral tests above/below exercise the actual JS separately.
  const fixture=html.replace('<link rel="stylesheet" href="/styles.css">','<style>'+css+'</style>').replace('<script src="/app.js" defer></script>','');
  const escape=value=>value.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
  const widths=[320,360,480,768,1024,1440];
  const frames=widths.map(w=>'<iframe title="'+w+'" width="'+w+'" height="950" style="display:block;border:0" srcdoc="'+escape(fixture)+'"></iframe>').join('');
  const probe=`<script>
  window.addEventListener('load',()=>{
    const errors=[];
    for(const frame of document.querySelectorAll('iframe')){
      const d=frame.contentDocument,w=frame.contentWindow;
      const width=Number(frame.title),id=x=>d.getElementById(x);
      const check=(ok,message)=>{if(!ok)errors.push(width+'px: '+message)};
      id('auth').hidden=true;id('app').hidden=false;
      const views=[...d.querySelectorAll('.console-view')];
      for(const view of views){
        views.forEach(v=>v.hidden=v!==view);
        if(view.id==='view-chat'){
          d.querySelector('.chat-settings').open=true;
          d.querySelector('.chat-attachments').open=true;
          id('chatOutput').textContent='A'.repeat(200);
          const fallback=id('allowExternalFallback'),checkbox=fallback.getBoundingClientRect();
          const send=id('sendChat').getBoundingClientRect(),stop=id('stopChat').getBoundingClientRect(),clear=id('clearChat').getBoundingClientRect();
          check(checkbox.width>=16&&checkbox.width<=22&&checkbox.height>=16&&checkbox.height<=22,'checkbox must remain compact');
          check(send.width>=95&&send.height<=75,'send button compressed or vertically wrapped');
          check(stop.width>=55&&stop.height<=75&&clear.height<=75,'chat actions must remain readable');
          check(send.top>=fallback.closest('label').getBoundingClientRect().bottom-1,'fallback must not share the action row');
          check(send.right<=view.getBoundingClientRect().right+2,'send action outside workspace');
          d.querySelector('.chat-settings').open=false;d.querySelector('.chat-attachments').open=false;
          check(id('consoleMain').scrollHeight<=id('consoleMain').clientHeight+2,'chat workspace must not scroll');
          check(id('chatOutput').clientHeight>0,'history must occupy remaining space');
          check(w.getComputedStyle(id('sendChat')).overflowWrap!=='anywhere','button words may not break letter by letter');
        }
        if(view.id==='view-models'){
          for(const item of view.querySelectorAll('details'))item.open=true;
        }
        check(d.documentElement.scrollWidth<=d.documentElement.clientWidth+2,view.id+' document horizontal overflow');
        const main=id('consoleMain');
        check(main.scrollWidth<=main.clientWidth+2,view.id+' workspace horizontal overflow');
      }
    }
    document.getElementById('layout-result').textContent=errors.length?'LAYOUT_FAIL: '+errors.join(' | '):'LAYOUT_PASS_6_WIDTHS';
  });
  </script>`;
  const tmp=fs.mkdtempSync(path.join(os.tmpdir(),'nodexlm-layout-'));
  try{
    const file=path.join(tmp,'layout.html');
    fs.writeFileSync(file,'<!doctype html><meta charset="utf-8"><pre id="layout-result">LAYOUT_PENDING</pre>'+frames+probe);
    const result=spawnSync(browser,['--headless','--no-sandbox','--disable-gpu','--disable-dev-shm-usage','--no-first-run','--disable-background-networking','--user-data-dir='+path.join(tmp,'profile'),'--window-size=1600,1100','--virtual-time-budget=7000','--dump-dom','file://'+file],{encoding:'utf8',timeout:45000,maxBuffer:8*1024*1024});
    if(result.error)throw result.error;
    assert.equal(result.status,0,'headless browser failed: '+result.stderr?.slice(-2000));
    const match=result.stdout.match(/<pre id="layout-result">([^<]*)<\/pre>/);
    assert.equal(match?.[1],'LAYOUT_PASS_6_WIDTHS',match?.[1]||'No browser layout result');
    console.log('Responsive dashboard layout passed at 320, 360, 480, 768, 1024 and 1440 px');
  }finally{fs.rmSync(tmp,{recursive:true,force:true})}
}
async function main(){
  const html=fs.readFileSync(path.join(__dirname,'../internal/dashboard/static/index.html'),'utf8');
  const css=fs.readFileSync(path.join(__dirname,'../internal/dashboard/static/styles.css'),'utf8');
  checkResponsiveLayout(html,css);
  const metrics=vm.runInContext("chatRunMetrics({usage:{completion_tokens:100,completion_tokens_details:{reasoning_tokens:60}},timings:{predicted_n:100,predicted_ms:2000,prompt_ms:500},elapsed:3,firstOutput:0.6,firstText:2,firstReasoning:0.6,lastReasoning:1.8,hasReasoning:true})",sandbox);
  assert.ok(metrics.text.includes('Decode nativo (inclui reasoning): 50.0 t/s'));
  assert.ok(metrics.text.includes('Reasoning: 60 tokens'));
  assert.ok(metrics.text.includes('Saída sem reasoning: 40 tokens'));
  assert.ok(metrics.text.includes('Média total (inclui prompt): 33.3 t/s'));
  const missing=vm.runInContext("chatRunMetrics({usage:{completion_tokens:100},elapsed:3,hasReasoning:true})",sandbox);
  assert.ok(missing.text.includes('contagem separada indisponível'));
  assert.ok(!missing.text.includes('Reasoning: 0 tokens'));
  const invalid=vm.runInContext("chatRunMetrics({usage:{completion_tokens:10,completion_tokens_details:{reasoning_tokens:11}},elapsed:1,hasReasoning:true})",sandbox);
  assert.ok(!invalid.text.includes('Saída sem reasoning: -1'));
  assert.ok(!invalid.text.includes('Reasoning: 11 tokens'));
  const noUsage=vm.runInContext("chatRunMetrics({timings:{predicted_n:20,predicted_ms:1000},elapsed:2,hasReasoning:true})",sandbox);
  assert.ok(noUsage.text.includes('Tokens decodificados (runtime): 20'));
  assert.ok(noUsage.text.includes('Decode nativo (inclui reasoning): 20.0 t/s'));
  const chat=html.slice(html.indexOf('id="view-chat"'),html.indexOf('id="view-images"'));
  assert.match(html,/id="nodePageStatus"/);
  assert.match(html,/id="toggleSecret"/);
  vm.runInContext("hydrateSettings({threads:6,context_tokens:4096,distributed:true,backend:'llama.cpp',vision_projector:'vision.gguf'})",sandbox);
  const hydrated=vm.runInContext("settingsFromForm('test.gguf')",sandbox);
  assert.equal(hydrated.threads,6);
  assert.equal(hydrated.context_tokens,4096);
  assert.equal(hydrated.distributed,true);
  assert.equal(hydrated.vision_projector,'vision.gguf');
  assert.equal(hydrated.parallel_slots,1);

  vm.runInContext("renderNodeTable(Array.from({length:25},(_,i)=>({id:'guest-'+i,name:'Guest '+i,platform:'linux',architecture:'amd64',status:'online',runtime:{benchmark:true}})))",sandbox);
  assert.equal(get('nodes').children.length,20);
  const originalNodeAction=get('nodes').children[0].children[5].children[0];
  vm.runInContext("nodeTableData[0].ram_bytes=1024;renderNodeTable(nodeTableData)",sandbox);
  assert.equal(get('nodes').children[0].children[5].children[0],originalNodeAction,'telemetry should not replace focused row actions');
  get('nodeNext').handlers.click();
  assert.equal(get('nodes').children.length,5);
  assert.equal(get('nodePageStatus').textContent,'21–25 de 25 Guests');
  vm.runInContext("renderNodeTable(nodeTableData.slice(0,2))",sandbox);
  assert.equal(get('nodes').children.length,2,'page clamped after revocation');
  get('secret').type='password';get('toggleSecret').handlers.click();
  assert.equal(get('secret').type,'text');
  get('toggleSecret').handlers.click();assert.equal(get('secret').type,'password');
  await vm.runInContext("(async()=>{let calls=0,resolve;const b=$('inferenceBench');const first=withBusy(b,()=>{calls++;return new Promise(r=>resolve=r)});await withBusy(b,()=>{calls++});resolve();await first;if(calls!==1||b.disabled)throw Error('busy guard regression')})()",sandbox);

  assert.match(chat,/<label class="check-field"><input id="allowExternalFallback" type="checkbox"><span>/);
  assert.match(chat,/<div class="chat-actions">[\s\S]*?<div class="buttons"><button id="stopChat"/);
  assert.doesNotMatch(chat,/<div class="buttons"><label/);
  assert.match(css,/\.buttons button\{flex:0 0 auto\}/);
  assert.match(html,/id="nav-images"[^>]*aria-controls="view-images"/);
  assert.equal(new Set([...html.matchAll(/\bid="([^"]+)"/g)].map(m=>m[1])).size,[...html.matchAll(/\bid="([^"]+)"/g)].length,'IDs must be unique');
  vm.runInContext("chatHistory=[{role:'user',content:'Olá'},{role:'assistant',content:'Resposta'}];renderChat()",sandbox);
  assert.equal(get('chatOutput').children.length,2,'each message must have its own layout container');
  assert.equal(get('chatOutput').children[0].textContent,'VocêOlá');
  vm.runInContext("chatHistory=[];renderChat()",sandbox);
  assert.ok(get('chatOutput').textContent.includes('começar'),'empty chat retains useful guidance');
  vm.runInContext("setOptions('chatModel',[['a','A'],['b','B']],'b')",sandbox);
  const originalOption=get('chatModel').children[0];
  vm.runInContext("setOptions('chatModel',[['a','A'],['b','B']],'a')",sandbox);
  assert.equal(get('chatModel').children[0],originalOption,'unchanged options retain DOM identity');
  assert.equal(get('chatModel').value,'a');

  // Telemetry must never turn an unknown CPU/GPU counter into a claimed idle reading.
  const device = {
    id:'guest-1',name:'Guest Linux',platform:'linux',status:'online',
    last_seen:new Date().toISOString(),
    cpu_usage_percent:0,ram_bytes:1000,ram_used_bytes:500,
    gpus:[{name:'AMD GPU',vram_bytes:1000,vram_used_bytes:250}]
  };
  vm.runInContext('renderNodeResources('+JSON.stringify([device])+')',sandbox);
  let resourceCard=get('nodeResourceCards').children[0];
  assert.ok(resourceCard.textContent.includes('0.0%'),'measured idle CPU should be rendered as 0%');
  assert.ok(resourceCard.textContent.includes('Indisponível'),'missing GPU busy must not be invented');
  const offline={...device,status:'offline'};
  vm.runInContext('renderNodeResources('+JSON.stringify([offline])+')',sandbox);
  resourceCard=get('nodeResourceCards').children[0];
  assert.ok(resourceCard.textContent.includes('offline'),'offline nodes must not display old load as current');
  assert.equal(get('nodeResourceCards').children.length,1,'one card per device');
  assert.ok(resourceCard.textContent.includes('Revogar acesso'),'tools belong to the device card');
  assert.equal([...html.matchAll(/id="nodes"/g)].length,1,'Guest management table exists exactly once');
  assert.ok(!resourceCard.textContent.includes('0.0%'),'offline CPU gauge is stale');
  // Alternate runtimes must never silently enable incompatible GGML RPC.
  get('runtimeBackend').value='mistralrs';
  get('distributed').checked=true;
  get('runtimeBackend').handlers.change();
  assert.equal(get('distributed').checked,false,'mistral.rs should clear RPC distribution');
  assert.equal(get('distributed').disabled,true,'mistral.rs cannot enable the RPC Guest checkbox');
  assert.equal(vm.runInContext("settingsFromForm('model.gguf').backend",sandbox),'mistralrs');
  get('runtimeBackend').value='llama.cpp';
  get('runtimeBackend').handlers.change();
  assert.equal(get('distributed').disabled,false,'llama.cpp must retain distributed RPC support');
  const stalledLoad={settings:{model:'test.gguf'},runtime:{loading:true,phase:'loading_model',
    elapsed_seconds:190,phase_elapsed_seconds:180,rpc_transfer:{'guest-123':{
      to_guest_bytes:0,from_guest_bytes:0,connected:1,idle:0,last_activity_ms:0}}}};
  vm.runInContext('renderLoadStatus('+JSON.stringify(stalledLoad)+')',sandbox);
  assert.ok(get('loadingStatus').textContent.includes('Sem transferência RPC recente'),
    'inactivity must not appear as progress while no tensors are transferred');
  const transferring=JSON.parse(JSON.stringify(stalledLoad));
  transferring.runtime.rpc_transfer['guest-123'].to_guest_bytes=1048576;
  transferring.runtime.rpc_transfer['guest-123'].last_activity_ms=Date.now();
  transferring.runtime.upload_plan={nodes:[{node_id:'guest-123',name:'Android Lab',layers:12,weight_bytes:2097152}]};
  vm.runInContext('renderLoadStatus('+JSON.stringify(transferring)+')',sandbox);
  assert.ok(get('loadingStatus').textContent.includes('Android Lab'),'use device name');
  assert.ok(!get('loadingStatus').textContent.includes('guest-123'),'never display raw guest ID');
  assert.ok(get('loadingStatus').textContent.includes('RPC real'),
    'measured bytes must appear independently of planned layer count');
  assert.ok(get('loadingStatus').textContent.includes('MiB/s'),
    'real transfer rate must be exposed separately from planned layers');
  get('chatEffort').value='none';
  get('chatThinkingBudget').value='500';
  get('chatEffort').handlers.change();
  assert.equal(get('chatThinkingBudget').disabled,true,
    'thinking budget must be disabled when reasoning is off');
  assert.equal(get('chatThinkingBudget').value,'0',
    'disabled thinking must clear any old budget');
  get('chatEffort').value='high';
  get('chatEffort').handlers.change();
  assert.equal(get('chatThinkingBudget').disabled,false,
    'reasoning budget must be editable for thinking-capable models');
  get('chatEffort').value='max';
  get('chatEffort').handlers.change();
  get('chatThinkingBudget').value='128';
  get('chatThinkingBudget').handlers.change();
  assert.deepEqual(JSON.parse(localStorage.getItem('nodexlm-chat-reasoning')),
    {effort:'max',budget:128}, 'chat tuning must persist without saving model responses');
  assert.ok(!get('loadingStatus').textContent.includes('Sem transferência RPC recente'),
    'active transfer should not warn about a stall');
  // The pairing code is not an admin session token. Its TTL comes from the Host.
  assert.equal(vm.runInContext("pairingTTL({expires_in:'10m'})",sandbox),600000);
  vm.runInContext("token='admin-token'; setupAuth()",sandbox);
  assert.equal(get('pairAutoRenew').checked,false,'auto-renew must be off by default');
  await get('pair').handlers.click();
  assert.equal(pairingCalls,1);
  assert.equal(get('pairCode').textContent,'ABCD2345');
  assert.equal(get('pairingSession').hidden,false);
  assert.match(get('pairCountdown').textContent,/^09:59|10:00$/);
  vm.runInContext('pairingSession.expiresAt=pairingNow()-1000',sandbox);
  await vm.runInContext('pairingTick()',sandbox);
  assert.equal(get('pairCode').textContent,'Código expirado');
  assert.equal(pairingCalls,1,'expiration alone must not rotate');
  get('pairAutoRenew').checked=true;
  await get('pairAutoRenew').handlers.change();
  assert.equal(pairingCalls,2,'opted-in expiry must rotate');
  assert.equal(get('pairCode').textContent,'EFGH6789');
  vm.runInContext('pairingSession.expiresAt=pairingNow()-1000',sandbox);
  document.hidden=true;
  await vm.runInContext('pairingTick()',sandbox);
  assert.equal(pairingCalls,2,'hidden tab must not generate credentials');
  document.hidden=false;
  await documentEvents.visibilitychange();
  assert.equal(pairingCalls,3,'return to visible tab must renew an expired code');
  // Errors must mask the previous code and throttle retries.
  pairingRequestFails=true;
  vm.runInContext('pairingSession.expiresAt=pairingNow()-1000',sandbox);
  await vm.runInContext('pairingTick()',sandbox);
  assert.equal(pairingCalls,4);
  assert.equal(get('pairCode').textContent,'Código expirado');
  await vm.runInContext('pairingTick()',sandbox);
  assert.equal(pairingCalls,4,'automatic retry must be rate-limited');
  pairingRequestFails=false;
  await vm.runInContext('pairingRetryAt=pairingNow()-1; pairingTick()',sandbox);
  assert.equal(pairingCalls,5);
  assert.notEqual(get('pairCode').textContent,'Código expirado');
  get('pairAutoRenew').checked=false;
  await get('pairAutoRenew').handlers.change();
  vm.runInContext('pairingSession.expiresAt=pairingNow()-1000',sandbox);
  await vm.runInContext('pairingTick()',sandbox);
  assert.equal(pairingCalls,5,'turning off renewal must prevent rotation');
  get('logout').handlers.click();
  assert.equal(get('pairingSession').hidden,true,'logout must hide pairing code');
  assert.equal(get('pairAutoRenew').checked,false);
  assert.equal(vm.runInContext('pairingSession',sandbox),null);
  vm.runInContext("token='admin-token'; setupAuth()",sandbox);
  assert.equal(get('view-overview').hidden,false,'overview should be visible at startup');
  assert.equal(get('view-models').hidden,true,'secondary workspaces should be hidden');
  get('overviewPrompt').value='Explain Q4 quantization';
  get('overviewOpenChat').handlers.click();
  assert.equal(get('view-chat').hidden,false,'overview prompt should open Chat');
  assert.equal(get('prompt').value,'Explain Q4 quantization','draft must reach Chat without sending');
  get('nav-overview').handlers.click();
  get('quickModels').handlers.click();
  assert.equal(get('view-models').hidden,false,'model shortcut did not open workspace');
  assert.equal(get('view-overview').hidden,true,'previous workspace still visible');
  assert.equal(get('nav-models')['aria-current'],'page','active menu state not announced');
  get('nav-models').handlers.keydown({key:'ArrowRight',preventDefault(){}});
  assert.equal(get('view-inference').hidden,false,'keyboard navigation to inference failed');
  assert.equal(get('nav-inference').focused,true,'keyboard focus not moved');
  get('nav-overview').handlers.click();
  assert.equal(get('view-overview').hidden,false,'return to overview failed');
  const model={repository:'owner/quantizations',filename:'qwen/Qwen3-Q4_K_M.gguf',size_bytes:2*1073741824,
    placement:'single-node-candidate',reason:'Estimativa de memória',url:'https://huggingface.co/owner/quantizations/resolve/main/qwen/Qwen3-Q4_K_M.gguf'};
  vm.runInContext(`catalogModels=[${JSON.stringify(model)}];catalogSearched=true;renderCatalog()`,sandbox);
  const row=get('catalogResults').children[0];
  assert.equal(row.tag,'article');
  assert.equal(row.children[0].children[0].textContent,model.filename);
  assert.equal(row.children[1].children[0].textContent,'Baixar');
  assert.equal(row.children[1].children[1].textContent,'Baixar e aplicar');
  await row.children[1].children[1].handlers.click();
  assert.equal(download.status,'downloading');
  assert.equal(download.name,'Qwen3-Q4_K_M.gguf');
  assert.equal(get('downloadProgress').children[1].tag,'progress');
  const stableMeter=get('downloadProgress').children[1];
  const stableCatalogRow=get('catalogResults').children[0];
  vm.runInContext("latestStatus.download.received=1048576;renderDownload(latestStatus.download);renderCatalog()",sandbox);
  assert.equal(get('downloadProgress').children[1],stableMeter,'global transfer meter must update in place');
  assert.equal(get('catalogResults').children[0],stableCatalogRow,'progress must not replace catalog row');
  assert.equal(stableMeter.value,1048576);
  assert.ok(!html.includes('id="catalogDownloadProgress"'),'only one global transfer status');
  assert.ok(get('catalogResults').children[0].children[0].children.some(n=>n.tag==='progress'));
  locals.push({name:download.name,size_bytes:model.size_bytes});
  download.status='completed';download.received=download.total;
  await vm.runInContext('refresh()',sandbox);
  for(let i=0;i<60 && !loaded;i++)await new Promise(r=>setTimeout(r,5));
  assert.equal(loaded,true,'download completion applies the selected model');
  assert.equal(settings.model,download.name);
  assert.ok(calls.some(c=>c.url==='/api/settings' && c.body.model===download.name));
  assert.ok(calls.some(c=>c.url==='/api/load' && c.method==='POST'));
  assert.ok(get('models').children.some(r=>r.textContent.includes('Em uso')));
  get('catalogFilter').value='downloaded';
  vm.runInContext('renderCatalog()',sandbox);
  assert.equal(get('catalogResults').children.length,1);
  assert.equal(get('catalogResults').children[0].children[1].children[0].textContent,'Aplicar');
  console.log('Console navigation and model catalog: keyboard, workspaces, download, progress, auto-apply, filters PASS');
}
main().catch(e=>{console.error(e);process.exitCode=1;});
