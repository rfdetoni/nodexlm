// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
class Element {
  constructor(tag) {this.tag=tag;this.children=[];this.handlers={};this.style={};this.value='';this.checked=false;this.hidden=false;this.disabled=false;this._text='';}
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
const sandbox={document,performance:{now:()=>Date.now()},sessionStorage:{getItem:()=>null,removeItem(){},setItem(){}},setInterval(){},setTimeout(){},clearTimeout(){},fetch,confirm:()=>true,URL,console};
vm.createContext(sandbox);
const script=fs.readFileSync(path.join(__dirname,'../internal/dashboard/static/app.js'),'utf8');
vm.runInContext(script,sandbox,{filename:'app.js'});
async function main(){
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
  assert.ok(!resourceCard.textContent.includes('0.0%'),'offline CPU gauge is stale');
  const stalledLoad={settings:{model:'test.gguf'},runtime:{loading:true,phase:'loading_model',
    elapsed_seconds:190,phase_elapsed_seconds:180,rpc_transfer:{'guest-123':{
      to_guest_bytes:0,from_guest_bytes:0,connected:1,idle:0,last_activity_ms:0}}}};
  vm.runInContext('renderLoadStatus('+JSON.stringify(stalledLoad)+')',sandbox);
  assert.ok(get('loadingStatus').textContent.includes('Sem transferência RPC recente'),
    'inactivity must not appear as progress while no tensors are transferred');
  const transferring=JSON.parse(JSON.stringify(stalledLoad));
  transferring.runtime.rpc_transfer['guest-123'].to_guest_bytes=1048576;
  transferring.runtime.rpc_transfer['guest-123'].last_activity_ms=Date.now();
  vm.runInContext('renderLoadStatus('+JSON.stringify(transferring)+')',sandbox);
  assert.ok(get('loadingStatus').textContent.includes('RPC real'),
    'measured bytes must appear independently of planned layer count');
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
  assert.equal(get('catalogDownloadProgress').children[1].tag,'progress');
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
