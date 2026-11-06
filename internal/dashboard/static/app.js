'use strict';
const $ = id => document.getElementById(id);
let token = sessionStorage.getItem('nodexlm-admin') || '';
let settingsInitialized = false;
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
async function refresh(){
 const [status,models,nodes,cluster]=await Promise.all([api('/api/status'),api('/api/models'),api('/v1/nodes'),api('/api/cluster')]);
 const c=cluster.resources;const names=Object.keys(cluster.rpc_guests||{}).length;$('capacity').textContent=c.online+' nós online · '+fmt(c.aggregate_budget_bytes)+' orçamento agregado estimado · '+names+' Guests RPC conectados';
 $('nodeCount').textContent=nodes.length;$('onlineCount').textContent=nodes.filter(n=>n.status==='online').length;
 $('modelState').textContent=status.runtime.loaded?'Carregado':'Descarregado';
 const list=$('models');list.replaceChildren();for(const m of models){let p=document.createElement('div');p.className='modelrow';p.textContent=m.name+' · '+fmt(m.size_bytes)+(m.moe?' · MoE ('+m.expert_count+' experts)':'');p.style.padding='8px 0';list.append(p);} if(!models.length){list.textContent='Nenhum GGUF encontrado.';}
 const s=status.settings;
 if (!settingsInitialized) {
  setSelect(models,s.model);
  for(const [id,v] of Object.entries({threads:s.threads,contextTokens:s.context_tokens,gpuLayers:s.gpu_layers,moeCPULayers:s.moe_cpu_layers,moeCache:s.moe_cache_mib})) $(id).value=v;
  $('moeCPU').checked=s.moe_cpu; $('distributed').checked=!!s.distributed; settingsInitialized=true;
 }
 const tbody=$('nodes');tbody.replaceChildren();for(const n of nodes){const tr=document.createElement('tr');for(const v of [n.name,n.platform+' / '+n.architecture,n.status+(n.busy?' · ocupado':''),(n.models||[]).join(', ')||'—']){const td=document.createElement('td');td.textContent=v;tr.append(td);}const td=document.createElement('td');const btn=document.createElement('button');btn.className='secondary';btn.textContent='Revogar';btn.addEventListener('click',async()=>{if(!confirm('Revogar acesso de '+n.name+'?'))return;try{await api('/v1/nodes/'+encodeURIComponent(n.id),'DELETE');notice('Nó revogado');await refresh();}catch(e){notice(e.message)}});td.append(btn);tr.append(td);tbody.append(tr)}
 const d=status.download;$('downloadProgress').textContent=d?(d.status+' · '+d.name+' · '+fmt(d.received)+(d.total>0?' / '+fmt(d.total):'')+(d.error?' · '+d.error:'')):'';
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

$('catalogForm').addEventListener('submit',e=>{e.preventDefault();guard(async()=>{
 const result=$('catalogResults');result.textContent='Consultando catálogo...';
 const models=await api('/api/catalog?q='+encodeURIComponent($('catalogQuery').value));
 result.replaceChildren();for(const m of models){const p=document.createElement('p');p.className='modelrow';p.textContent=m.filename+' · '+fmt(m.size_bytes)+' · '+m.placement+' · '+m.reason;result.append(p)}
 if(!models.length){result.textContent='Nenhum modelo GGUF compatível com a busca.'}
 })});
