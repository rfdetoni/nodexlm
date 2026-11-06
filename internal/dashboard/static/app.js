'use strict';
const $ = id => document.getElementById(id);
let token = sessionStorage.getItem('nodexlm-admin') || '';
let settingsInitialized = false;
let catalogModels = [];
let localModels = [];
let latestStatus = null;
let pendingApply = null;
let actionBusy = false;
let catalogSearching = false;
let catalogSearched = false;
let downloadPollBusy = false;
const fmt = bytes => bytes > 0 ? (bytes / 1073741824).toFixed(2) + ' GiB' : '—';
function notice(message) { $('toast').textContent=message; $('toast').style.display='block'; clearTimeout(notice.timer); notice.timer=setTimeout(() => $('toast').style.display='none', 4800); }
async function api(path, method='GET', body) {
  const headers={Authorization:'Bearer '+token}; if (body!==undefined) headers['Content-Type']='application/json';
  const response=await fetch(path,{method,headers,body: body===undefined?undefined:JSON.stringify(body),cache:'no-store'});
  const text=await response.text(); let data;try{data=JSON.parse(text)}catch{data={error:text}}
  if (!response.ok) {if(response.status===401) {sessionStorage.removeItem('nodexlm-admin');$('auth').hidden=false;$('app').hidden=true;}throw Error(data.error||'HTTP '+response.status)}
  return data;
}
function setupAuth(){$('auth').hidden=!!token;$('app').hidden=!token;}
function setSelect(items,chosen){let list=$('modelSelect');list.replaceChildren();let empty=document.createElement('option');empty.value='';empty.textContent='Selecione um modelo';list.append(empty);for(const m of items){let el=document.createElement('option');el.value=m.name;el.textContent=m.name+' ('+fmt(m.size_bytes)+')'+(m.moe?' · MoE':'');list.append(el);}list.value=chosen||'';}
function settingsFromForm(model) {
 return {model, threads:+$('threads').value, context_tokens:+$('contextTokens').value,
  gpu_layers:$('gpuLayers').value, moe_cpu:$('moeCPU').checked,
  distributed:$('distributed').checked, moe_cpu_layers:+$('moeCPULayers').value,
  moe_cache_mib:+$('moeCache').value};
}
function modelFile(candidate) {
 // Hub paths may contain directories; the Host only stores a safe GGUF basename.
 const base=String(candidate.filename || '').split('/').pop();
 return base && base.toLowerCase().endsWith('.gguf') && !base.includes('\\') && !base.includes('..') ? base : '';
}
function el(tag, className, content) {
 const node=document.createElement(tag);
 if(className)node.className=className;
 if(content!==undefined)node.textContent=content;
 return node;
}
function action(label, className, handler, disabled=false) {
 const btn=el('button', className, label);
 btn.type='button';btn.disabled=disabled;btn.addEventListener('click',()=>guard(handler));
 return btn;
}
function renderDownload(download) {
 for(const id of ['downloadProgress','catalogDownloadProgress']) {
  const area=$(id); if(!area)continue; area.replaceChildren();
  if(!download)continue;
  const label=download.status==='downloading'?'Baixando':download.status==='completed'?'Concluído':'Falhou';
  area.append(el('div','download-label',label+' · '+download.name));
  if(download.status==='downloading') {
   const bar=el('progress','download-bar');
   bar.max=download.total>0?download.total:1;
   if(download.total>0)bar.value=download.received;
   bar.setAttribute('aria-label','Progresso do download de '+download.name);
   area.append(bar);
   const percent=download.total>0?Math.min(100,Math.floor(download.received*100/download.total))+'%':'Tamanho desconhecido';
   area.append(el('small','muted',fmt(download.received)+(download.total>0?' / '+fmt(download.total):'')+' · '+percent));
  }else if(download.error) area.append(el('small','muted',download.error));
 }
}
async function applyModel(name) {
 if(actionBusy)return;
 actionBusy=true;
 try {
  const status=await api('/api/status');
  const s=status.settings;
  if(status.runtime.loaded && s.model===name){notice('Este modelo já está carregado.');return;}
  if(status.runtime.loaded && !confirm('Descarregar o modelo atual para aplicar '+name+'?'))return;
  notice('Aplicando '+name+'...');
  if(status.runtime.loaded)await api('/api/unload','POST',{});
  const config=settingsInitialized?settingsFromForm(name):{...s,model:name};
  await api('/api/settings','PUT',config);
  $('modelSelect').value=name;
  settingsInitialized=false;
  await refresh();
  notice('Carregando '+name+'...');
  await api('/api/load','POST',{});
  notice(name+' aplicado ao Host');
  await refresh();
 } finally {actionBusy=false; if(latestStatus){renderLocalModels(localModels,latestStatus);renderCatalog();}}
}
async function downloadCandidate(candidate, applyAfter) {
 if(actionBusy)return;
 const name=modelFile(candidate);
 if(!name)throw Error('Arquivo GGUF inválido para download');
 if(localModels.some(m=>m.name===name)) {
  if(applyAfter)await applyModel(name);
  else notice('O modelo já foi baixado: '+name);
  return;
 }
 if(latestStatus?.download?.status==='downloading')throw Error('Aguarde o download atual terminar.');
 actionBusy=true;
 try {
  await api('/api/download','POST',{url:candidate.url,name});
  pendingApply=applyAfter?name:null;
  $('downloadUrl').value=candidate.url;
  $('downloadName').value=name;
  notice(applyAfter?'Download iniciado; modelo será aplicado após concluir.':'Download iniciado: '+name);
  await refresh();
 } finally {actionBusy=false; if(latestStatus){renderLocalModels(localModels,latestStatus);renderCatalog();}}
}
function renderLocalModels(models,status) {
 const list=$('models');list.replaceChildren();
 if(!models.length){list.append(el('div','empty-state','Nenhum GGUF salvo. Pesquise no catálogo abaixo para baixar.'));return;}
 for(const m of models) {
  const row=el('article','model-item');
  const info=el('div','model-info');info.append(el('strong','model-title',m.name));
  info.append(el('div','model-meta',fmt(m.size_bytes)+(m.moe?' · MoE ('+m.expert_count+' experts)':'')));
  const controls=el('div','model-actions');
  const loaded=status.runtime.loaded && status.settings.model===m.name;
  if(loaded)controls.append(el('span','model-tag active','Em uso'));
  else controls.append(action('Aplicar','',()=>applyModel(m.name),actionBusy));
  row.append(info,controls);list.append(row);
 }
}
function placement(model) {
 switch(model.placement) {
  case 'single-node-candidate':return {name:'Candidato local',kind:'ok'};
  case 'distributed-candidate':return {name:'Candidato RPC',kind:'remote'};
  case 'insufficient-memory':return {name:'Memória insuficiente',kind:'warn'};
  default:return {name:'Não estimado',kind:'neutral'};
 }
}
function renderCatalog() {
 const dest=$('catalogResults');dest.replaceChildren();
 const choice=$('catalogFilter').value;
 const filtered=catalogModels.filter(m=>{
  const installed=localModels.some(x=>x.name===modelFile(m));
  return choice==='downloaded'?installed:choice==='available'?['single-node-candidate','distributed-candidate'].includes(m.placement):true;
 });
 $('catalogCount').textContent=catalogModels.length?filtered.length+' de '+catalogModels.length+' arquivos GGUF encontrados':(catalogSearched?'Nenhum arquivo GGUF encontrado':'Pesquise para encontrar modelos GGUF.');
 if(!filtered.length){dest.append(el('div','empty-state',!catalogSearched?'Use a busca acima para explorar modelos GGUF.':catalogModels.length?'Nenhum resultado para este filtro.':'Nenhum resultado. Experimente outra pesquisa.'));return;}
 const busy=latestStatus?.download?.status==='downloading';
 for(const m of filtered) {
  const name=modelFile(m);const installed=localModels.some(x=>x.name===name);
  const row=el('article','catalog-item');
  const info=el('div','model-info');
  info.append(el('strong','model-title',m.filename));
  info.append(el('div','model-meta',m.repository+' · '+fmt(m.size_bytes)));
  const tier=placement(m);
  const tags=el('div','model-tags');tags.append(el('span','model-tag '+tier.kind,tier.name));
  if(installed)tags.append(el('span','model-tag active','Baixado'));
  info.append(tags);
  if(m.reason)info.append(el('p','model-reason',m.reason));
  const transfer=latestStatus?.download;
  if(transfer?.status==='downloading' && transfer.name===name) {
   const bar=el('progress','download-bar');bar.max=transfer.total>0?transfer.total:1;
   if(transfer.total>0)bar.value=transfer.received;
   bar.setAttribute('aria-label','Baixando '+name);
   info.append(bar);
   info.append(el('div','model-meta',transfer.total>0?
    Math.min(100,Math.floor(transfer.received*100/transfer.total))+'% · '+fmt(transfer.received)+' / '+fmt(transfer.total):
    'Baixando · '+fmt(transfer.received)));
  }
  const actions=el('div','model-actions');
  if(installed)actions.append(action('Aplicar','',()=>applyModel(name),actionBusy));
  else if(name && m.url) {
   actions.append(action('Baixar','secondary',()=>downloadCandidate(m,false),busy||actionBusy));
   actions.append(action('Baixar e aplicar','',()=>downloadCandidate(m,true),busy||actionBusy));
  } else actions.append(el('span','muted','Arquivo indisponível'));
  row.append(info,actions);dest.append(row);
 }
}
async function refresh(){
 const [status,models,nodes,cluster]=await Promise.all([api('/api/status'),api('/api/models'),api('/v1/nodes'),api('/api/cluster')]);
 latestStatus=status;localModels=models;
 const c=cluster.resources;const names=Object.keys(cluster.rpc_guests||{}).length;$('capacity').textContent=c.online+' nós online · '+fmt(c.aggregate_budget_bytes)+' orçamento agregado estimado · '+names+' Guests RPC conectados';
 $('nodeCount').textContent=nodes.length;$('onlineCount').textContent=nodes.filter(n=>n.status==='online').length;
 $('modelState').textContent=status.runtime.loaded?'Carregado':'Descarregado';
 const s=status.settings;
 const prior=$('modelSelect').value;
 setSelect(models,settingsInitialized?(models.some(m=>m.name===prior)?prior:s.model):s.model);
 if (!settingsInitialized) {
  for(const [id,v] of Object.entries({threads:s.threads,contextTokens:s.context_tokens,gpuLayers:s.gpu_layers,moeCPULayers:s.moe_cpu_layers,moeCache:s.moe_cache_mib})) $(id).value=v;
  $('moeCPU').checked=s.moe_cpu; $('distributed').checked=!!s.distributed; settingsInitialized=true;
 }
 renderLocalModels(models,status);renderCatalog();
 const tbody=$('nodes');tbody.replaceChildren();for(const n of nodes){
 const gpus=Array.isArray(n.gpus)?n.gpus:[];
 const gpuLine=gpus.map(g=>g.name+': '+(g.free_vram_bytes>0?fmt(g.free_vram_bytes)+' livres / '+fmt(g.vram_bytes):g.vram_bytes>0?fmt(g.vram_bytes)+' total (livre desconhecido)':'VRAM compartilhada / desconhecida')).join(' · ');
 const memoryLine='RAM: '+(n.ram_bytes>0?fmt(n.available_ram_bytes)+' livres / '+fmt(n.ram_bytes):'não informada')+(gpuLine?' · VRAM: '+gpuLine:'');
 const tr=document.createElement('tr');for(const v of [n.name,n.platform+' / '+n.architecture,n.status+(n.busy?' · ocupado':''),memoryLine,(n.models||[]).join(', ')||'—']){const td=document.createElement('td');td.textContent=v;tr.append(td);}const td=document.createElement('td');const btn=document.createElement('button');btn.className='secondary';btn.textContent='Revogar';btn.addEventListener('click',async()=>{if(!confirm('Revogar acesso de '+n.name+'?'))return;try{await api('/v1/nodes/'+encodeURIComponent(n.id),'DELETE');notice('Nó revogado');await refresh();}catch(e){notice(e.message)}});td.append(btn);tr.append(td);tbody.append(tr)}
 const d=status.download;renderDownload(d);
 if(pendingApply && d && d.name===pendingApply && d.status!=='downloading') {
  const name=pendingApply;pendingApply=null;
  if(d.status==='completed')guard(()=>applyModel(name));
  else notice('Falha ao baixar '+name+': '+(d.error||'erro desconhecido'));
 }
}
async function guard(action){try{await action();}catch(e){notice(e.message)}}
$('login').addEventListener('submit',e=>{e.preventDefault();token=$('secret').value.trim();guard(async()=>{await api('/api/status');sessionStorage.setItem('nodexlm-admin',token);$('secret').value='';settingsInitialized=false;setupAuth();await refresh();notice('Conectado')})});
$('logout').addEventListener('click',()=>{sessionStorage.removeItem('nodexlm-admin');token='';setupAuth()});
$('downloadUrl').addEventListener('change',()=>{try{let pathname=new URL($('downloadUrl').value).pathname;const name=decodeURIComponent(pathname.split('/').pop());if(name.toLowerCase().endsWith('.gguf'))$('downloadName').value=name;}catch{}});
$('downloadForm').addEventListener('submit',e=>{e.preventDefault();guard(async()=>{await api('/api/download','POST',{url:$('downloadUrl').value,name:$('downloadName').value});notice('Download iniciado');await refresh()})});
$('settingsForm').addEventListener('submit',e=>{e.preventDefault();guard(async()=>{await api('/api/settings','PUT',{model:$('modelSelect').value,threads:+$('threads').value,context_tokens:+$('contextTokens').value,gpu_layers:$('gpuLayers').value,moe_cpu:$('moeCPU').checked,distributed:$('distributed').checked,moe_cpu_layers:+$('moeCPULayers').value,moe_cache_mib:+$('moeCache').value});notice('Configuração salva');settingsInitialized=false;await refresh()})});
$('load').addEventListener('click',()=>guard(async()=>{notice('Carregando modelo...');await api('/api/load','POST',{});notice('Modelo carregado');await refresh()}));
$('unload').addEventListener('click',()=>guard(async()=>{await api('/api/unload','POST',{});notice('Modelo descarregado');await refresh()}));
$('pair').addEventListener('click',()=>guard(async()=>{const p=await api('/v1/pairing/start','POST',{});$('pairCode').hidden=false;$('pairCode').textContent=p.code+' · válido por '+p.expires_in;notice('Código renovado')}));
$('chatForm').addEventListener('submit',e=>{e.preventDefault();guard(async()=>{const prompt=$('prompt').value;$('chatOutput').textContent='Gerando resposta...';const result=await api('/api/chat','POST',{messages:[{role:'user',content:prompt}],max_tokens:512});$('chatOutput').textContent=result.answer})});
setupAuth();if(token)guard(refresh);
setInterval(()=>{if(token && !$('app').hidden && !document.hidden)guard(refresh)},10000);
// Poll only transfer status during downloads, avoiding full cluster refresh each second.
setInterval(async()=>{
 if(!token || document.hidden || $('app').hidden || downloadPollBusy || latestStatus?.download?.status!=='downloading')return;
 downloadPollBusy=true;
 try {
  const d=await api('/api/download');
  latestStatus.download=d;renderDownload(d);renderCatalog();
  if(d?.status!=='downloading')await refresh();
 } catch(e) {console.warn('Download status unavailable',e);}
 finally {downloadPollBusy=false;}
},1000);

$('catalogFilter').addEventListener('change',renderCatalog);
$('catalogForm').addEventListener('submit',e=>{e.preventDefault();if(catalogSearching)return;guard(async()=>{
 catalogSearching=true;
 const btn=$('catalogSearch');btn.disabled=true;
 const result=$('catalogResults');result.replaceChildren(el('div','empty-state','Consultando catálogo Hugging Face...'));
 $('catalogCount').textContent='Buscando modelos...';
 try {
  catalogModels=await api('/api/catalog?q='+encodeURIComponent($('catalogQuery').value));
  catalogSearched=true;renderCatalog();
 } finally {catalogSearching=false;btn.disabled=false;}
})});
