// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
class Element {
  constructor(tag) {this.tag=tag;this.children=[];this.handlers={};this.style={};this.value='';this.hidden=false;this.disabled=false;this._text='';}
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
const response=data=>({ok:true,status:200,text:async()=>JSON.stringify(data)});
const fetch=async (url,options)=>{
  const method=options.method;
  const body=options.body?JSON.parse(options.body):null;
  calls.push({url,method,body});
  if(url==='/api/status')return response({settings:{...settings},runtime:{loaded},download});
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
const sandbox={document:{getElementById:get,createElement:tag=>new Element(tag),hidden:false},sessionStorage:{getItem:()=>null,removeItem(){},setItem(){}},setInterval(){},setTimeout(){},clearTimeout(){},fetch,confirm:()=>true,URL,console};
vm.createContext(sandbox);
const script=fs.readFileSync(path.join(__dirname,'../internal/dashboard/static/app.js'),'utf8');
vm.runInContext(script,sandbox,{filename:'app.js'});
async function main(){
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
