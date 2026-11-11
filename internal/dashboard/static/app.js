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
let residentModelNames=[];
let loadingPlan=null;
let loadingPlanModel='';
let modelFolderPath='';
const fmt = bytes => bytes > 0 ? (bytes / 1073741824).toFixed(2) + ' GiB' : '—';
function notice(message) { $('toast').textContent=message; $('toast').style.display='block'; clearTimeout(notice.timer); notice.timer=setTimeout(() => $('toast').style.display='none', 4800); }
async function api(path, method='GET', body) {
  const headers={Authorization:'Bearer '+token}; if (body!==undefined) headers['Content-Type']='application/json';
  const response=await fetch(path,{method,headers,body: body===undefined?undefined:JSON.stringify(body),cache:'no-store'});
  const text=await response.text(); let data;try{data=JSON.parse(text)}catch{data={error:text}}
  if (!response.ok) {if(response.status===401) {sessionStorage.removeItem('nodexlm-admin');$('auth').hidden=false;$('app').hidden=true;}throw Error(data.error||'HTTP '+response.status)}
  return data;
}
const consoleViews=[
 {id:'overview',name:'Visão geral',description:'Estado do cluster e modelos disponíveis.'},
 {id:'models',name:'Modelos',description:'Biblioteca GGUF, downloads e catálogo.'},
 {id:'inference',name:'Inferência',description:'Configuração, camadas e carregamento.'},
 {id:'cluster',name:'Dispositivos',description:'Guests, recursos e pareamento seguro.'},
 {id:'observability',name:'Diagnóstico',description:'Medições reais de rede e inferência.'},
 {id:'chat',name:'Chat',description:'Converse com os modelos residentes.'}
];
function activateConsoleView(view){
 const chosen=consoleViews.find(item=>item.id===view)||consoleViews[0];
 for(const item of consoleViews){
  const active=item.id===chosen.id;
  $('view-'+item.id).hidden=!active;
  $('nav-'+item.id).setAttribute('aria-current',active?'page':'false');
 }
 $('workspaceTitle').textContent=chosen.name;
 $('workspaceDescription').textContent=chosen.description;
 $('consoleMain').scrollTop=0;
 try{sessionStorage.setItem('nodexlm-console-view',chosen.id)}catch{}
 if(chosen.id==='observability' && token)guard(refreshObservations);
}
function setupConsoleNavigation(){
 for(let i=0;i<consoleViews.length;i++){
  const item=consoleViews[i],button=$('nav-'+item.id);
  button.addEventListener('click',()=>activateConsoleView(item.id));
  button.addEventListener('keydown',event=>{
   let next=i;
   if(event.key==='ArrowDown'||event.key==='ArrowRight')next=(i+1)%consoleViews.length;
   else if(event.key==='ArrowUp'||event.key==='ArrowLeft')next=(i+consoleViews.length-1)%consoleViews.length;
   else if(event.key==='Home')next=0;
   else if(event.key==='End')next=consoleViews.length-1;
   else return;
   event.preventDefault();
   activateConsoleView(consoleViews[next].id);
   $('nav-'+consoleViews[next].id).focus();
  });
 }
 for(const [id,view] of [['quickModels','models'],['quickCluster','cluster'],['quickInference','inference'],['quickChat','chat']]){
  $(id).addEventListener('click',()=>activateConsoleView(view));
 }
 $('overviewOpenChat').addEventListener('click',()=>{
  const draft=$('overviewPrompt').value;
  if(draft.trim())$('prompt').value=draft;
  activateConsoleView('chat');
  $('prompt').focus();
 });
 let stored='overview';
 try{stored=sessionStorage.getItem('nodexlm-console-view')||'overview'}catch{}
 activateConsoleView(stored);
}
function setupAuth(){$('auth').hidden=!!token;$('app').hidden=!token;}
function setSelect(items,chosen){let list=$('modelSelect');list.replaceChildren();let empty=document.createElement('option');empty.value='';empty.textContent='Selecione um modelo';list.append(empty);for(const m of items){let el=document.createElement('option');el.value=m.name;el.textContent=m.name+' ('+fmt(m.size_bytes)+')'+(m.moe?' · MoE':'');list.append(el);}list.value=chosen||'';}
function setDraftModels(items, chosen){
 const select=$('draftModelSelect');select.replaceChildren();const empty=document.createElement('option');empty.value='';empty.textContent='Sem draft';select.append(empty);
 for(const item of items){const opt=document.createElement('option');opt.value=item.name;opt.textContent=item.name;select.append(opt);}
 select.value=items.some(m=>m.name===chosen)?chosen:'';
}
function settingsFromForm(model) {
 return {model, threads:+$('threads').value, context_tokens:+$('contextTokens').value,
  gpu_layers:$('gpuLayers').value, moe_cpu:$('moeCPU').checked,
  distributed:$('distributed').checked, placement_strategy:$('placementStrategy').value, moe_cpu_layers:+$('moeCPULayers').value,
  moe_cache_mib:+$('moeCache').value, flash_attention:$('flashAttention').value,
   kv_cache_type:$('kvCacheType').value, batch_size:+$('batchSize').value,
   ubatch_size:+$('ubatchSize').value, speculative_mode:$('speculativeMode').value, draft_model:$('draftModelSelect').value};
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
  const label=download.status==='downloading'?'Baixando':download.status==='completed'?'Concluído':download.status==='cancelled'?'Interrompido':'Falhou';
  area.append(el('div','download-label',label+' · '+download.name));
  if(download.status==='downloading') {
   const bar=el('progress','download-bar');
   bar.max=download.total>0?download.total:1;
   if(download.total>0)bar.value=download.received;
   bar.setAttribute('aria-label','Progresso do download de '+download.name);
   area.append(bar);
   const percent=download.total>0?Math.min(100,Math.floor(download.received*100/download.total))+'%':'Tamanho desconhecido';
   area.append(action('Interromper download','secondary',()=>api('/api/download','DELETE')));
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
  if(status.runtime.loaded && !await confirmAction('Descarregar o modelo atual para aplicar '+name+'?','Descarregar'))return;
  notice('Aplicando '+name+'...');
  if(status.runtime.loaded)await api('/api/unload','POST',{});
  const config=settingsInitialized?settingsFromForm(name):{...s,model:name};
  await api('/api/settings','PUT',config);
  $('modelSelect').value=name;
  settingsInitialized=false;
  await refresh();
  notice('Carregando '+name+'...');
  await api('/api/load','POST',{});
  notice('Carregamento iniciado: '+name);
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
async function loadModelFolder() {
 try {
  const folder=await api('/api/models/folder');
  modelFolderPath=folder.path||'';
  $('modelFolder').textContent=modelFolderPath||'Não configurada';
 } catch(e) {$('modelFolder').textContent='Indisponível';}
}
function renderLoadStatus(status){
 const box=$('loadingStatus'), runtime=status.runtime||{};
 if(!runtime.loading){box.hidden=true;box.replaceChildren();loadingPlan=null;loadingPlanModel='';return;}
 box.hidden=false;box.replaceChildren(el('strong','', 'Carregando '+(status.settings?.model||'GGUF')+' · '+String(runtime.phase||'preparando').replace(/_/g,' ')));
 const activity=el('progress','download-bar');
 activity.setAttribute('aria-label','Carregamento em andamento: progresso real indisponível');
 box.append(activity,el('p','muted','Tempo decorrido: '+Math.floor((runtime.elapsed_seconds||0)/60)+'min '+(runtime.elapsed_seconds||0)%60+'s · progresso real não informado pelo llama.cpp'));
 if(!loadingPlan || loadingPlanModel!==status.settings.model){
  box.append(el('p','muted','Calculando distribuição estimada das camadas…'));return;
 }
 box.append(el('p','muted','Distribuição planejada (não é porcentagem carregada):'));
 for(const node of loadingPlan.nodes){
  const name=node.name||node.endpoint, line=el('div','layer-allocation');
  line.append(el('strong','',name+' · ~'+node.layers+' de '+loadingPlan.blocks+' camadas'));
  line.append(el('small','muted',fmt(node.estimated_weight_bytes)+' de pesos estimados / '+fmt(node.budget_bytes)+' disponíveis'));
  const bar=el('progress','download-bar');
  bar.max=Math.max(1,loadingPlan.blocks);bar.value=node.layers;
  bar.setAttribute('aria-label',name+': participação estimada de '+node.layers+' camadas em '+loadingPlan.blocks);
  line.append(bar);box.append(line);
 }
}
async function updateLoadingPlan(status){
 if(!status.runtime?.loading || !status.settings?.model || loadingPlanModel===status.settings.model)return;
 loadingPlanModel=status.settings.model;loadingPlan=null;
 try{loadingPlan=await api('/api/plan')}catch(e){console.warn('Placement estimate unavailable during load',e.message)}
 if(latestStatus?.runtime?.loading)renderLoadStatus(latestStatus);
}
function renderLocalModels(models,status) {
 const list=$('models');list.replaceChildren();
 if(!models.length){list.append(el('div','empty-state','Nenhum GGUF salvo. Pesquise no catálogo abaixo para baixar.'));return;}
 for(const m of models) {
  const row=el('article','model-item');
  const info=el('div','model-info');info.append(el('strong','model-title',m.name));
  info.append(el('div','model-meta',fmt(m.size_bytes)+(m.moe?' · MoE ('+m.expert_count+' experts)':'')));
  const controls=el('div','model-actions');
  const loaded=residentModelNames.includes(m.name) || (status.runtime.loaded && status.runtime.model===m.name);
  const blocked=loaded || status.runtime.loading || actionBusy;
  if(loaded)controls.append(el('span','model-tag active','Em uso'));
  else controls.append(action('Aplicar','',()=>applyModel(m.name),blocked));
  controls.append(action('Excluir','secondary',async()=>{
   if(!await confirmAction('Excluir definitivamente '+m.name+' e todas as suas partes GGUF?','Excluir'))return;
   await api('/api/models/'+encodeURIComponent(m.name),'DELETE');
   notice('Modelo excluído: '+m.name);settingsInitialized=false;await refresh();
  },blocked));
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
 const [status,models,nodes,cluster,resident]=await Promise.all([api('/api/status'),api('/api/models'),api('/v1/nodes'),api('/api/cluster'),api('/api/resident-models')]);
 renderResidentModels(resident);
 $('residentCount').textContent=resident.length;
 latestStatus=status;localModels=models;
 renderLoadStatus(status);
 await updateLoadingPlan(status);
 const c=cluster.resources;const names=Object.keys(cluster.rpc_guests||{}).length;$('capacity').textContent=c.online+' nós online · '+fmt(c.aggregate_budget_bytes)+' orçamento agregado estimado · '+names+' Guests RPC conectados';
 $('nodeCount').textContent=nodes.length;$('onlineCount').textContent=nodes.filter(n=>n.status==='online').length;
 $('sideOnline').textContent=nodes.filter(n=>n.status==='online').length;
 $('overviewCapacity').textContent=fmt(c.aggregate_budget_bytes);
 $('sideCapacity').textContent=fmt(c.aggregate_budget_bytes);
 $('modelState').textContent=status.runtime.loading
  ? 'Carregando · '+String(status.runtime.phase||'preparando').replace(/_/g,' ')
  : status.runtime.error ? 'Falha · '+status.runtime.error
  : status.runtime.loaded?'Carregado':'Descarregado';
 const s=status.settings;
 const prior=$('modelSelect').value;
 setSelect(models,settingsInitialized?(models.some(m=>m.name===prior)?prior:s.model):s.model);
 setDraftModels(models,settingsInitialized?$('draftModelSelect').value:s.draft_model);
 if (!settingsInitialized) {
  for(const [id,v] of Object.entries({threads:s.threads,contextTokens:s.context_tokens,gpuLayers:s.gpu_layers,moeCPULayers:s.moe_cpu_layers,moeCache:s.moe_cache_mib})) $(id).value=v;
  $('moeCPU').checked=s.moe_cpu; $('distributed').checked=!!s.distributed; $('placementStrategy').value=s.placement_strategy||'capacity'; $('flashAttention').value=s.flash_attention||'auto'; $('kvCacheType').value=s.kv_cache_type||'f16'; $('batchSize').value=s.batch_size||0; $('ubatchSize').value=s.ubatch_size||0; $('speculativeMode').value=s.speculative_mode||'off'; settingsInitialized=true;
 }
 renderLocalModels(models,status);renderCatalog();
 const tbody=$('nodes');tbody.replaceChildren();for(const n of nodes){
 const gpus=Array.isArray(n.gpus)?n.gpus:[];
 const gpuLine=gpus.map(g=>g.name+': '+(g.free_vram_bytes>0?fmt(g.free_vram_bytes)+' livres / '+fmt(g.vram_bytes):g.vram_bytes>0?fmt(g.vram_bytes)+' total (livre desconhecido)':'VRAM compartilhada / desconhecida')).join(' · ');
 const memoryLine='RAM: '+(n.ram_bytes>0?fmt(n.available_ram_bytes)+' livres / '+fmt(n.ram_bytes):'não informada')+(gpuLine?' · VRAM: '+gpuLine:'');
 const tr=document.createElement('tr');for(const v of [n.name,n.platform+' / '+n.architecture,n.status+(n.busy?' · ocupado':'')+(n.status==='online'?(n.runtime?.rpc_ready?' · RPC conectado':' · RPC desconectado'):''),memoryLine,(n.models||[]).join(', ')||'—']){const td=document.createElement('td');td.textContent=v;tr.append(td);}const td=document.createElement('td');const btn=document.createElement('button');btn.className='secondary';btn.textContent='Revogar';btn.addEventListener('click',async()=>{if(!await confirmAction('Revogar acesso de '+n.name+'?','Revogar'))return;try{await api('/v1/nodes/'+encodeURIComponent(n.id),'DELETE');notice('Nó revogado');await refresh();}catch(e){notice(e.message)}});td.append(btn);if(n.runtime?.benchmark && n.status==='online')td.append(action('Medir rede','secondary',async()=>{await api('/v1/bench/'+encodeURIComponent(n.id),'POST',{});await refreshObservations()}));tr.append(td);tbody.append(tr)}
 const d=status.download;renderDownload(d);
 if(pendingApply && d && d.name===pendingApply && d.status!=='downloading') {
  const name=pendingApply;pendingApply=null;
  if(d.status==='completed')guard(()=>applyModel(name));
  else notice('Falha ao baixar '+name+': '+(d.error||'erro desconhecido'));
 }
}
async function guard(action){try{await action();}catch(e){notice(e.message)}}

let chatController=null;
let chatHistory=[];
try {if(typeof localStorage!=='undefined'){
 const saved=JSON.parse(localStorage.getItem('nodexlm-chat')||'[]');
 if(Array.isArray(saved))chatHistory=saved.filter(m=>m&&['user','assistant'].includes(m.role)&&typeof m.content==='string'&&m.content.length<=65536).slice(-62);
}}catch { /* Unavailable or damaged browser storage must not break chat. */ }
function saveChat(){try{localStorage.setItem('nodexlm-chat',JSON.stringify(chatHistory))}catch{notice('Não foi possível salvar o histórico neste navegador.')}}
function renderChat(){const output=$('chatOutput');output.replaceChildren();for(const message of chatHistory){output.append(el('strong','',message.role==='user'?'Você':'NodexLM'),el('p','',message.content))}}
function confirmAction(text,label){
 const dialog=$('confirmation');$('confirmationText').textContent=text;$('confirmAction').textContent=label;
 return new Promise(resolve=>{dialog.addEventListener('close',()=>resolve(dialog.returnValue==='confirm'),{once:true});dialog.returnValue='cancel';dialog.showModal()});
}
async function sendChat(){
 if(chatController)return;
 const prompt=$('prompt').value.trim();if(!prompt)return;
 const model=$('chatModel').value||latestStatus?.runtime?.model;if(!model)throw Error('Carregue um modelo antes de conversar.');
 const controller=new AbortController();chatController=controller;$('sendChat').disabled=true;$('stopChat').disabled=false;
 const start=performance.now();let first=null;let answer='';let usage=null;let done=false;let reader;
 renderChat();const output=$('chatOutput');output.append(el('strong','','Você'),el('p','',prompt),el('strong','','NodexLM'));const reply=el('p','','Gerando resposta…');output.append(reply);
 try{
  const response=await fetch('/api/chat',{method:'POST',headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},signal:controller.signal,body:JSON.stringify({model,messages:[...chatHistory.slice(-62),{role:'user',content:prompt}],temperature:+$('chatTemperature').value,max_tokens:+$('chatTokens').value,cache_prompt:true,stream_options:{include_usage:true},stream:true})});
  if(!response.ok){const text=await response.text();let message=text;try{message=JSON.parse(text).error||text}catch{}throw Error(message)}
  reader=response.body.getReader();const decoder=new TextDecoder();let pending='';
  while(!done){
   const part=await reader.read();pending+=decoder.decode(part.value||new Uint8Array(),{stream:!part.done}).replace(/\r\n/g,'\n');
   if(pending.length>1048576)throw Error('Resposta do modelo excede o limite.');
   let boundary;
   while((boundary=pending.indexOf('\n\n'))>=0){
    const frame=pending.slice(0,boundary);pending=pending.slice(boundary+2);
    const data=frame.split('\n').filter(line=>line.startsWith('data:')).map(line=>line.slice(5).trimStart()).join('\n');
    if(!data)continue;if(data==='[DONE]'){done=true;break}
    const event=JSON.parse(data);if(event.error)throw Error(event.error.message||event.error);
    if(event.usage)usage=event.usage;
    const text=event.choices?.[0]?.delta?.content||'';
    if(text){if(first===null)first=performance.now();answer+=text;if(answer.length>65536)throw Error('Resposta longa demais para o histórico.');reply.textContent=answer}
   }
   if(part.done&&!done)throw Error('A conexão terminou antes de concluir a resposta.');
  }
  if(controller.signal.aborted)return;
  chatHistory.push({role:'user',content:prompt},{role:'assistant',content:answer});chatHistory=chatHistory.slice(-62);saveChat();$('prompt').value='';
  if(!answer)reply.textContent='Resposta vazia.';
  const elapsed=(performance.now()-start)/1000;
  $('chatMetrics').textContent='Tempo: '+elapsed.toFixed(2)+' s'+(first!==null?' · Primeiro token: '+((first-start)/1000).toFixed(2)+' s':'')+(usage?.completion_tokens?' · '+(usage.completion_tokens/elapsed).toFixed(1)+' tokens/s':'');
 }catch(error){
  const message=controller.signal.aborted?'Geração interrompida.':error.message;$('chatMetrics').textContent=message;
  if(!answer)reply.textContent=message;if(!controller.signal.aborted)notice(message);
 }finally{
  if(reader)await reader.cancel().catch(()=>{});
  if(chatController===controller)chatController=null;$('sendChat').disabled=false;$('stopChat').disabled=true;
 }
}
$('login').addEventListener('submit',e=>{e.preventDefault();token=$('secret').value.trim();guard(async()=>{await api('/api/status');sessionStorage.setItem('nodexlm-admin',token);$('secret').value='';settingsInitialized=false;setupAuth();renderChat();await Promise.all([refresh(),loadModelFolder()]);notice('Conectado')})});
$('logout').addEventListener('click',()=>{sessionStorage.removeItem('nodexlm-admin');token='';chatController?.abort();chatHistory=[];$('chatOutput').replaceChildren();setupAuth()});
$('downloadUrl').addEventListener('change',()=>{try{let pathname=new URL($('downloadUrl').value).pathname;const name=decodeURIComponent(pathname.split('/').pop());if(name.toLowerCase().endsWith('.gguf'))$('downloadName').value=name;}catch{}});
$('downloadForm').addEventListener('submit',e=>{e.preventDefault();guard(async()=>{await api('/api/download','POST',{url:$('downloadUrl').value,name:$('downloadName').value});notice('Download iniciado');await refresh()})});
$('settingsForm').addEventListener('submit',e=>{e.preventDefault();guard(async()=>{await api('/api/settings','PUT',settingsFromForm($('modelSelect').value));notice('Configuração salva');settingsInitialized=false;await refresh()})});
$('load').addEventListener('click',()=>guard(async()=>{loadingPlan=null;loadingPlanModel='';await api('/api/load','POST',{});notice('Carregamento iniciado; acompanhe o estado no painel.');await refresh()}));
$('unload').addEventListener('click',()=>guard(async()=>{await api('/api/unload','POST',{});notice('Modelo descarregado');await refresh()}));
$('pair').addEventListener('click',()=>guard(async()=>{const p=await api('/v1/pairing/start','POST',{});$('pairCode').hidden=false;$('pairCode').textContent=p.code+' · válido por '+p.expires_in;notice('Código renovado')}));
$('previewPlan').addEventListener('click',()=>guard(async()=>{
 const plan=await api('/api/plan');const area=$('planOutput');area.hidden=false;area.replaceChildren(el('strong','',plan.model+' · '+plan.blocks+' camadas · '+(plan.strategy||'capacity'))); if(plan.selection_reason)area.append(el('p','muted',plan.selection_reason)); if(plan.excluded_guests?.length)area.append(el('p','muted','Guests não selecionados: '+plan.excluded_guests.map(x=>x.slice(0,12)).join(', ')));
 for(const node of plan.nodes)area.append(el('p','',(node.name||node.endpoint)+': ~'+node.layers+' camadas · '+fmt(node.estimated_weight_bytes)+' de pesos / '+fmt(node.budget_bytes)+' disponíveis'));
 area.append(el('p','', 'KV '+(plan.estimated_kv_type||'f16')+' estimado: '+fmt(plan.estimated_kv_bytes)+' · '+(plan.tokens_per_second?plan.tokens_per_second.toFixed(1)+' tokens/s medidos':'Tokens/s ainda não medido')+' · '+({'unmeasured':'Gargalo ainda não medido','network-advisory':'Atenção à velocidade da rede','host-memory':'Limite de memória do Host'}[plan.bottleneck]||plan.bottleneck)));
 for(const node of plan.nodes){if(node.network)area.append(el('p','muted',(node.name||node.endpoint)+': '+node.network.round_trip_ms.toFixed(2)+' ms RTT · '+node.network.mib_per_second.toFixed(1)+' MiB/s de eco'));}
 for(const node of plan.nodes){if(node.tokens_per_second)area.append(el('p','muted',(node.name||node.endpoint)+': '+node.tokens_per_second.toFixed(1)+' tokens/s observados'));}
 area.append(el('p','muted','Camadas e KV F16 são estimativas; a alocação final depende do runtime. Aviso de rede: RTT acima de 10 ms ou eco abaixo de 50 MiB/s. Tokens/s são medidos, sem previsão pela banda; mudar a configuração ou os nós pode invalidar a comparação.'));
}));
$('chatForm').addEventListener('submit',e=>{e.preventDefault();guard(sendChat)});
$('stopChat').addEventListener('click',()=>chatController?.abort());
$('clearChat').addEventListener('click',()=>guard(async()=>{
 if(!await confirmAction('Apagar o histórico de conversas salvo neste navegador?','Apagar histórico'))return;
 chatController?.abort();chatHistory=[];saveChat();renderChat();$('chatMetrics').textContent='Histórico apagado.';
}));


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

function renderResidentModels(models){
 residentModelNames=models;
 const list=$('residentModels');list.replaceChildren();const selector=$('chatModel');const prior=selector.value;selector.replaceChildren();
 for(const model of models){const option=el('option','',model);option.value=model;selector.append(option);const row=el('div','model-item');row.append(el('strong','',model),action('Descarregar','secondary',async()=>{await api('/api/resident-models/'+encodeURIComponent(model),'DELETE');await refresh()}));list.append(row)}
 selector.value=models.includes(prior)?prior:(models[0]||'');
}
$('loadAdditional').addEventListener('click',()=>guard(async()=>{
 const button=$('loadAdditional');button.disabled=true;
 try{await api('/api/resident-models','POST',{model:$('modelSelect').value});await refresh();notice('Modelo adicional carregado')}finally{button.disabled=false}
}));
$('inferenceBench').addEventListener('click',()=>guard(async()=>{
 const button=$('inferenceBench');button.disabled=true;
 try{const sample=await api('/api/bench/inference','POST',{model:$('modelSelect').value});notice(sample.tokens_per_second.toFixed(1)+' tokens/s medidos');await refreshObservations()}finally{button.disabled=false}
}));
$('refreshObservations').addEventListener('click',()=>guard(refreshObservations));
async function refreshObservations(){
 const data=await api('/api/observability');const area=$('observations');area.replaceChildren();
 for(const sample of data.inference)area.append(el('p','',sample.model+': '+sample.tokens_per_second.toFixed(1)+' tokens/s · '+sample.cached_tokens+' tokens de prompt reutilizados'));
 for(const node of data.nodes){
  const row=el('article','catalog-item');const info=el('div','model-info');info.append(el('strong','',node.node_id.slice(0,12)+'…'),el('p','muted',node.tunnels.connected+' túneis · '+node.reconnect_failures+' falhas de reconexão'));
  const history=node.latency||[];
  if(history.length){
   const svg=document.createElementNS('http://www.w3.org/2000/svg','svg');svg.setAttribute('viewBox','0 0 600 140');svg.setAttribute('role','img');svg.setAttribute('aria-label','Histórico de latência RTT do Guest em milissegundos');svg.setAttribute('class','latency-chart');
   const peak=Math.max(1,...history.map(p=>p.round_trip_ms));const line=document.createElementNS('http://www.w3.org/2000/svg','polyline');line.setAttribute('points',history.map((p,i)=>(20+i*560/Math.max(1,history.length-1))+','+(120-100*p.round_trip_ms/peak)).join(' '));line.setAttribute('fill','none');line.setAttribute('stroke','currentColor');line.setAttribute('stroke-width','3');svg.append(line);for(let i=0;i<history.length;i++){const dot=document.createElementNS('http://www.w3.org/2000/svg','circle');dot.setAttribute('cx',String(20+i*560/Math.max(1,history.length-1)));dot.setAttribute('cy',String(120-100*history[i].round_trip_ms/peak));dot.setAttribute('r','3');dot.setAttribute('fill','currentColor');svg.append(dot)}info.append(svg);
   const last=history[history.length-1];info.append(el('p','',last.round_trip_ms.toFixed(2)+' ms RTT · '+last.mib_per_second.toFixed(1)+' MiB/s de eco'));
  }
  row.append(info);area.append(row);
 }
 if(!data.inference.length&&!data.nodes.length)area.append(el('p','muted','Nenhuma medição disponível.'));
}

$('copyModelFolder').addEventListener('click',()=>guard(async()=>{
 if(!modelFolderPath)throw Error('Caminho da pasta indisponível');
 if(!navigator.clipboard?.writeText)throw Error('Cópia automática indisponível neste navegador');
 await navigator.clipboard.writeText(modelFolderPath);notice('Caminho copiado');
}));
$('openModelFolder').addEventListener('click',()=>guard(async()=>{
 await api('/api/models/folder/open','POST',{});
 notice('Solicitada abertura da pasta no computador Host.');
}));
setupConsoleNavigation();
setupAuth();if(token){renderChat();guard(refresh);guard(loadModelFolder);}
