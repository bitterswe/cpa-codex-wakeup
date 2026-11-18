'use strict';
const $=id=>document.getElementById(id);
const escapeHTML=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const esc=escapeHTML;
const kinds={daily:'每日',weekly:'每周',interval:'间隔',quota:'配额重置后',startup:'启动后'};
const statuses={pending:'待确认',queued:'排队中',running:'执行中',expired:'确认超时',skipped:'已跳过',interrupted:'运行中断',success:'成功',partial:'部分成功',failed:'失败'};
let key='',state={},accountList=[],draft,saveAction,previewSeq=0,previewTimer,busy=false;
let returnToTask=null;
const modal=$('editor'),fields=$('fields');
const when=(stamp,zone='Asia/Shanghai')=>stamp?new Date(stamp*1000).toLocaleString('zh-CN',{timeZone:zone,hour12:false}):'—';
const options=(pairs,value)=>pairs.map(([v,label])=>`<option value="${esc(v)}" ${v===value?'selected':''}>${esc(label)}</option>`).join('');
const field=(name,control)=>`<label class="field">${name}${control}</label>`;
const button=(label,attrs='')=>`<button type="button" ${attrs}>${esc(label)}</button>`;
const input=(id,type,value,extra='')=>`<input id="${id}" type="${type}" value="${esc(value)}" ${extra}>`;
const statusSwitch=(enabled,label,attrs='')=>`<button type="button" class="task-switch" role="switch" aria-label="${esc(label)}" aria-checked="${!!enabled}" ${attrs}></button>`;
const sessionKey='cpa-codex-wakeup.management-key';
function forgetKey(){key='';try{sessionStorage.removeItem(sessionKey)}catch{}}
function requireLogin(){
 forgetKey();returnToTask=null;if(modal.open)modal.close();$('dashboard').hidden=true;$('toolbar').hidden=true;$('retry').hidden=true;$('login').hidden=false;
}
async function api(action,data={}){
 const sentKey=key;
 const res=await fetch('/v0/management/plugins/cpa-codex-wakeup/tasks',{method:action?'POST':'GET',cache:'no-store',headers:{...(key?{Authorization:'Bearer '+key}:{}),'Content-Type':'application/json'},...(action?{body:JSON.stringify({Action:action,...data})}:{})});
 if(!res.ok){let detail;try{detail=(await res.json()).error}catch{}if((res.status===401||res.status===403)&&sentKey===key)requireLogin();throw Error(detail||`CPA HTTP ${res.status}`)}return res.json();
}
function report(err){(modal.open?$('error'):$('notice')).textContent=err.message}
function taskModels(t){return t.Models?.length?t.Models:[t.Model].filter(Boolean)}
async function systemModels(accounts){
 let catalog={};
 try{catalog=await api('models')}catch{/* Older CPA builds expose the credential catalog over the management API. */}
 const missing=accounts.filter(a=>!a.Disabled&&!Array.isArray(catalog[a.ID]));
 let cursor=0,failed=0;
 async function load(){while(cursor<missing.length){
  const a=missing[cursor++];
  try{const res=await fetch('/v0/management/auth-files/models?'+new URLSearchParams({name:a.File}),{headers:key?{Authorization:'Bearer '+key}:{},cache:'no-store'});
   if(!res.ok)throw Error();const data=await res.json();catalog[a.ID]=data.models.map(m=>m.id).filter(Boolean);
  }catch{failed++}
 }}
 await Promise.all(Array.from({length:Math.min(4,missing.length)},load));
 const all=[...new Set(accounts.filter(a=>!a.Disabled).flatMap(a=>catalog[a.ID]||[]))].sort();
 return {all,catalog,note:failed?'部分账号的模型目录读取失败，可重新打开编辑器重试。':all.length?'':'CPA 尚未返回 Codex 模型。'};
}
function guarded(fn){return async(...args)=>{try{await fn(...args)}catch(err){report(err)}}}
async function reload(){const data=await api();state=data.state;$('notice').textContent=data.storageError||'';render()}
function summary(t){
 const weekdays=(t.Days||[]).map(d=>'一二三四五六日'[d-1]).join('、');
 return {daily:`每日 ${t.Clock}`,weekly:`周${weekdays} ${t.Clock}`,interval:`每 ${t.Hours} 小时`,startup:`启动后 ${t.Delay} 分钟`,quota:`${{either:'任一',primary:'主',secondary:'次'}[t.Window]}窗口重置后 ${t.Delay} 分钟`}[t.Kind];
}
function render(){
 const tasks=state.Tasks||[];$('count').textContent=`${tasks.length} 个任务`;
 $('tasks').innerHTML=tasks.map(t=>`<article><header><h3>${esc(t.Name)} <small>${t.Enabled?'已启用':'已停用'}</small></h3><div class="row">${statusSwitch(t.Enabled,`${t.Name} 启用状态`,`data-action="toggle" data-id="${esc(t.ID)}"`)}${[['run','立即执行'],['edit','编辑'],['delete','删除']].map(([a,n])=>button(n,`data-action="${a}" data-id="${esc(t.ID)}"`)).join('')}</div></header><p>${esc(summary(t))} · ${esc(t.Zone)} · ${t.Pool?'API 服务账号池':`${t.IDs?.length||0} 个账号`}</p><small>${esc(taskModels(t).join('、'))} · ${esc(t.Effort||'默认强度')} · ${t.Mode==='confirm'?'执行前确认':'直接执行'}</small></article>`).join('')||'<p class="muted">尚无任务，点击「新建任务」设置首次唤醒。</p>';
 $('runs').innerHTML=[...(state.Runs||[])].reverse().map(r=>`<details data-run="${esc(r.ID)}"><summary>${esc(r.Name)} · ${esc(statuses[r.Status]||r.Status)} · ${esc(when(r.At,r.Task.Zone))}</summary><small>${esc(kinds[r.Source]||'手动执行')} · ${esc(taskModels(r.Task).join('、'))}</small><h4>任务提示词</h4><pre>${esc(r.Task.Prompt?.trim()?r.Task.Prompt:'hi')}</pre>${(r.Records||[]).map(x=>`<article><strong>${esc(x.Account)} · ${esc(x.Model||r.Task.Model)} · ${esc(statuses[x.Status]||x.Status)}</strong><h4>回答</h4><pre>${esc(x.Reply??'旧版本未保存回答内容。')||'未收到文本回答。'}</pre></article>`).join('')}</details>${r.Status==='pending'?`<div class="row"><small>确认截止 ${esc(when(r.Deadline,r.Task.Zone))}</small>${button('确认执行',`data-action="approve" data-id="${esc(r.ID)}"`)}${button('跳过',`data-action="skip" data-id="${esc(r.ID)}"`)}</div>`:''}`).join('')||'<p class="muted">暂无执行记录。</p>';
}
$('dashboard').onclick=guarded(async ev=>{const b=ev.target.closest('[data-action]');if(!b||b.disabled)return;const {action,id}=b.dataset;
 if(action==='edit')return openTask(state.Tasks.find(t=>t.ID===id));if(action==='delete'&&!confirm('删除这个唤醒任务？'))return;
 b.disabled=true;try{await api(action,{ID:id});await reload()}finally{b.disabled=false}});
async function connect(){await reload();$('login').hidden=true;$('retry').hidden=true;$('dashboard').hidden=false;$('toolbar').hidden=false;$('key').value=''}
$('connect').onsubmit=guarded(async ev=>{ev.preventDefault();key=$('key').value.trim();await connect();if($('remember').checked){try{sessionStorage.setItem(sessionKey,key)}catch{$('notice').textContent='浏览器限制了标签页存储，本次连接仍然有效。'}}});
for(const b of document.querySelectorAll('[data-close]'))b.onclick=()=>modal.close();
function open(title,html,save){$('title').textContent=title;fields.innerHTML=html;$('error').textContent='';saveAction=save;if(!modal.open)modal.showModal();modal.scrollTop=0}
$('save').onclick=guarded(async()=>{if(busy)return;busy=true;$('save').disabled=true;try{await saveAction();await reload();modal.close()}finally{busy=false;$('save').disabled=false}});
modal.onclose=guarded(async()=>{previewSeq++;clearTimeout(previewTimer);saveAction=null;if(returnToTask){const task=returnToTask;returnToTask=null;await openTask(task)}});
function defaults(){const p=state.Presets[0];return {ID:'',Name:'',Enabled:true,Pool:false,IDs:[],Model:p.Model,Effort:p.Default,Kind:'weekly',Days:[1,2,3,4,5],Clock:'06:00',Zone:'Asia/Shanghai',Hours:6,Window:'either',Delay:0,Mode:'auto',Timeout:5,Prompt:''}}
async function openTask(task){
 accountList=await api('accounts');const directory=await systemModels(accountList);draft=structuredClone(task||defaults());draft.IDs??=[];draft.Days??=[];const t=draft;
 t.Models=task?taskModels(t):[];if(!task){t.Model='';t.Effort=''}
 open(task?.ID?'编辑 Codex 唤醒任务':'新建 Codex 唤醒任务',field('任务名称',input('name','text',t.Name,'placeholder="例如：工作日上午唤醒" maxlength="90"'))+
 `<fieldset><legend>状态</legend><div id="enabled" class="row"></div></fieldset><fieldset><legend>选择账号</legend><div class="row" id="source"></div><div id="picker"></div><small id="poolInfo"></small></fieldset>`+
 `<fieldset><legend>选择模型</legend><p>可多选，每个账号将对每个选中模型分别发送一次提示词。</p><div class="grid" id="model"></div><small>${esc(directory.note)}</small>${field('推理强度','<select id="effort"></select>')}<div class="row spread"><small id="actual"></small>${button('管理模型预设','id="editModels"')}</div></fieldset><fieldset><legend>调度方式</legend><div class="row" id="kind"></div><div id="schedule"></div></fieldset>`+
 field('时区',input('zone','text',t.Zone))+field('执行模式',`<select id="mode">${options([['auto','直接执行'],['confirm','执行前确认']],t.Mode)}</select>`)+
 `<div id="timeoutField">${field('等待确认（分钟）',input('timeout','number',t.Timeout,'min="1" max="1440"'))}</div>`+
 field('唤醒提示词',`<textarea id="prompt" rows="3" placeholder="留空使用 hi">${esc(t.Prompt)}</textarea>`)+
 '<h3>下次触发预览</h3><div class="muted" id="preview"></div>',async()=>{await api('save',{ID:t.ID,Task:t})});
 const bind=(id,k,number=false)=>{$(id).oninput=()=>{t[k]=number?Number($(id).value):$(id).value;requestPreview()}};
 bind('name','Name');bind('zone','Zone');bind('timeout','Timeout',true);bind('prompt','Prompt');
 $('editModels').onclick=()=>{returnToTask=t;$('models').onclick()};
 function segments(id,entries,val,fn){const box=$(id);box.innerHTML=entries.map(([v,l])=>button(l,`data-v="${v}" aria-pressed="${v===val}"`)).join('');box.onclick=e=>{const b=e.target.closest('button');if(b)fn(b.dataset.v)}}
 $('enabled').innerHTML=statusSwitch(t.Enabled,'任务启用状态')+'<span></span>';
 const toggle=$('enabled').querySelector('button'),statusText=$('enabled').querySelector('span');
 function showStatus(){toggle.setAttribute('aria-checked',String(t.Enabled));statusText.textContent=t.Enabled?'已启用':'已停用'}
 toggle.onclick=()=>{t.Enabled=!t.Enabled;showStatus()};showStatus();
 function drawModels(){
  const ids=t.Pool?(state.Pool||[]):t.IDs;
  const available=directory.all.filter(m=>ids.every(id=>directory.catalog[id]?.includes(m)));
  const known=ids.filter(id=>directory.catalog[id]?.length);
  const fallback=directory.all.filter(m=>known.every(id=>directory.catalog[id].includes(m)));
  const shown=[...new Set([...available,...(known.length<ids.length?fallback:[]),...state.Presets.map(p=>p.Model),...t.Models])];
  const preset=id=>state.Presets.find(p=>p.Model===id);
  $('model').innerHTML=shown.map(m=>`<label><input type="checkbox" value="${esc(m)}" ${t.Models.includes(m)?'checked':''}> ${esc(preset(m)?.Name||m)}${available.includes(m)?'':'（当前目录未确认）'}</label>`).join('')||'<small>所选账号暂无共同模型。</small>';
  let efforts=['low','medium','high','xhigh'];
  if(t.Models.length){const lists=t.Models.map(m=>preset(m)?.Efforts||efforts);efforts=lists[0].filter(v=>lists.every(list=>list.includes(v)))}
  const choices=[...new Set(['',...efforts,t.Effort])];$('effort').innerHTML=options(choices.map(v=>[v,v||'模型默认']),t.Effort);$('actual').textContent='已选模型：'+(t.Models.join('、')||'无');
 }
 $('model').onchange=e=>{const m=e.target.value;t.Models=e.target.checked?[...t.Models,m]:t.Models.filter(v=>v!==m);t.Model=t.Models[0]||'';t.Effort=t.Models.length===1?(state.Presets.find(p=>p.Model===t.Model)?.Default||''):'';drawModels()};
 $('effort').onchange=()=>{t.Effort=$('effort').value};
 function source(){segments('source',[['manual','手动选账号'],['pool','API 服务账号池']],t.Pool?'pool':'manual',v=>{t.Pool=v==='pool';source();requestPreview()});$('picker').hidden=t.Pool;$('poolInfo').textContent=t.Pool?`运行时使用插件账号池，当前 ${(state.Pool||[]).length} 个账号。`:'';drawModels()}source();
 picker($('picker'),t.IDs,()=>{drawModels();requestPreview()});
 function schedule(){segments('kind',Object.entries(kinds),t.Kind,v=>{t.Kind=v;t.Next=0;t.Resets={};schedule();requestPreview()});
 const timed=['daily','weekly'].includes(t.Kind);
 $('schedule').innerHTML=(t.Kind==='weekly'?'<div class="field">每周执行日<div class="row" id="days"></div></div>':'')+
 (timed?field('执行时间',`<div class="row" id="quick"></div>${input('clock','time',t.Clock)}`):'')+
 (t.Kind==='interval'?field('间隔小时数',input('hours','number',t.Hours,'min="1" max="8760"')):'')+
 (t.Kind==='quota'?field('额度窗口',`<select id="window">${options([['either','任一窗口'],['primary','主窗口'],['secondary','次窗口']],t.Window)}</select>`):'')+
 (['quota','startup'].includes(t.Kind)?field('延迟分钟数',input('delay','number',t.Delay,'min="0"')):'');
 if(t.Kind==='weekly'){const draw=()=>{$('days').innerHTML=[1,2,3,4,5,6,7].map(d=>button('周'+'一二三四五六日'[d-1],`data-d="${d}" aria-pressed="${t.Days.includes(d)}"`)).join('')};draw();$('days').onclick=e=>{if(!e.target.dataset.d)return;const d=Number(e.target.dataset.d);t.Days=t.Days.includes(d)?t.Days.filter(i=>i!==d):[...t.Days,d];draw();requestPreview()}}
 if(timed){bind('clock','Clock');$('quick').innerHTML=['06:00','07:00','08:00','09:00','10:00','14:00','18:00','22:00'].map(x=>button(x)).join('');$('quick').onclick=e=>{if(e.target.tagName==='BUTTON'){t.Clock=$('clock').value=e.target.textContent;requestPreview()}}}
 if($('hours'))bind('hours','Hours',true);if($('delay'))bind('delay','Delay',true);if($('window'))bind('window','Window')}
 $('mode').onchange=()=>{t.Mode=$('mode').value;$('timeoutField').hidden=t.Mode!=='confirm';requestPreview()};$('timeoutField').hidden=t.Mode!=='confirm';schedule();requestPreview();
}
function requestPreview(){const idx=++previewSeq;clearTimeout(previewTimer);previewTimer=setTimeout(async()=>{if(!$('preview'))return;try{const times=await api('preview',{Task:draft});if(idx!==previewSeq||!$('preview'))return;
 $('preview').innerHTML=times.length?times.map(at=>`<div>${esc(when(at,draft.Zone))}</div>`).join(''):draft.Kind==='startup'?`下次插件启动后 ${esc(draft.Delay)} 分钟执行`:'等待所选账号的额度重置时间；可在账号卡片中刷新额度。'}catch(err){if(idx===previewSeq&&$('preview'))$('preview').textContent=err.message}},180)}
function picker(box,selected,onChange){
 let page=0,size=50,query='',plan='',tag='';
 const attempted=new Set();let loading=false;
 box.innerHTML='<p class="muted">可多选，任务将依次执行。</p><div class="row"><input aria-label="搜索账号" placeholder="搜索邮箱或名称"><select aria-label="套餐"></select><select aria-label="标签"></select><button type="button" data-all>全选筛选结果</button></div><p data-count></p><div class="grid" data-list></div><div class="row"><button data-prev>上一页</button><small data-page></small><button data-next>下一页</button><select aria-label="每页条数" data-size><option>20</option><option selected>50</option><option>100</option></select></div>';
 const get=q=>box.querySelector(q);const selects=box.querySelectorAll('select');
 const quotas=()=>state.Quotas||{};const tags=id=>(state.Tags||{})[id]||[];
 const fillFilters=()=>{selects[0].innerHTML=options([['','全部套餐'],...[...new Set(accountList.map(a=>quotas()[a.ID]?.Plan).filter(Boolean))].map(v=>[v,v])],plan);selects[1].innerHTML=options([['','全部标签'],...[...new Set(accountList.flatMap(a=>tags(a.ID)))].map(v=>[v,v])],tag)};
 function filtered(){return accountList.filter(a=>(a.Email+' '+a.Label).toLowerCase().includes(query.toLowerCase())&&(!plan||quotas()[a.ID]?.Plan===plan)&&(!tag||tags(a.ID).includes(tag)))}
 function draw(){const list=filtered();const pages=Math.max(1,Math.ceil(list.length/size));page=Math.max(0,Math.min(page,pages-1));get('[data-count]').textContent=`筛选 ${list.length} 个账号 · 已选择 ${selected.length} 个`;get('[data-page]').textContent=`${page+1} / ${pages}`;get('[data-prev]').disabled=page===0;get('[data-next]').disabled=page===pages-1;
 get('[data-list]').innerHTML=list.slice(page*size,(page+1)*size).map(a=>{const q=quotas()[a.ID];return `<article class="account ${selected.includes(a.ID)?'chosen':''}"><label><input type="checkbox" data-id="${esc(a.ID)}" ${selected.includes(a.ID)?'checked':''} ${a.Disabled&&!selected.includes(a.ID)?'disabled':''}> <strong>${esc(a.Email)}</strong></label><small>${esc([a.Label,q?.Plan,...tags(a.ID),a.Disabled?'已停用':a.Unavailable?'暂不可用，可加入任务':''].filter(Boolean).join(' · '))}</small><small>主窗口 ${q?.Primary?esc(q.Primary.Remaining)+'%':'—'} · 次窗口 ${q?.Secondary?esc(q.Secondary.Remaining)+'%':'—'}</small>${button('刷新额度',`data-quota="${esc(a.ID)}" ${a.Disabled?'disabled':''}`)}</article>`}).join('')||'<small>没有匹配的账号。</small>'}
 get('input').oninput=e=>{query=e.target.value;page=0;draw()};selects[0].onchange=e=>{plan=e.target.value;page=0;draw()};selects[1].onchange=e=>{tag=e.target.value;page=0;draw()};
 get('[data-all]').onclick=()=>{const ids=filtered().filter(a=>!a.Disabled).map(a=>a.ID);const all=ids.every(i=>selected.includes(i));for(const id of ids){if(all)selected.splice(selected.indexOf(id),1);else if(!selected.includes(id))selected.push(id)}draw();onChange()};
 get('[data-list]').onchange=e=>{const id=e.target.dataset.id;if(!id)return;if(e.target.checked&&!selected.includes(id))selected.push(id);else if(!e.target.checked)selected.splice(selected.indexOf(id),1);draw();onChange()};
 get('[data-list]').onclick=guarded(async e=>{const id=e.target.dataset.quota;if(!id)return;e.target.disabled=true;try{await api('quota',{ID:id});await reload();fillFilters();onChange()}finally{draw()}});
 async function loadVisible(){
  if(loading)return;loading=true;
  try{while(box.isConnected&&modal.open&&!box.hidden){
   const account=filtered().slice(page*size,(page+1)*size).find(a=>!a.Disabled&&!attempted.has(a.ID)&&(!quotas()[a.ID]||quotas()[a.ID].Checked<Date.now()/1000-300));
   if(!account)break;attempted.add(account.ID);
   try{const q=await api('quota',{ID:account.ID});(state.Quotas??={})[account.ID]=q;if(box.isConnected){fillFilters();draw();onChange()}}catch{/* Manual refresh exposes the provider error and allows retry. */}
  }}finally{loading=false}
 }
 box.addEventListener('change',()=>void loadVisible());box.addEventListener('click',()=>void loadVisible());box.addEventListener('input',()=>void loadVisible());
 get('[data-prev]').onclick=()=>{page--;draw()};get('[data-next]').onclick=()=>{page++;draw()};get('[data-size]').onchange=e=>{size=Number(e.target.value);page=0;draw()};fillFilters();draw();void loadVisible();
}
$('create').onclick=guarded(()=>openTask());
$('pool').onclick=guarded(async()=>{accountList=await api('accounts');const ids=[...(state.Pool||[])];open('API 服务账号池与标签','<p class="muted">使用账号池的任务在触发时读取此处成员。</p><div id="members"></div><hr><h3>账号标签</h3><select id="tagAccount"></select>'+field('标签（逗号分隔）',input('tagText','text',''))+button('保存标签','id="applyTag"'),()=>api('pool',{IDs:ids}));picker($('members'),ids,()=>{});
 $('tagAccount').innerHTML=options(accountList.map(a=>[a.ID,a.Email]),'');const show=()=>{$('tagText').value=(state.Tags?.[$('tagAccount').value]||[]).join(', ')};show();$('tagAccount').onchange=show;$('applyTag').onclick=guarded(async()=>{const id=$('tagAccount').value;if(!id)return;await api('tags',{ID:id,Tags:$('tagText').value.split(/[,，]/)});await reload();picker($('members'),ids,()=>{});$('error').textContent='标签已保存'})});
$('models').onclick=guarded(async()=>{const directory=await systemModels(await api('accounts'));const list=structuredClone(state.Presets);open('模型预设','<h3>从系统模型添加预设</h3><div class="grid" id="catalog"></div><small>'+esc(directory.note)+'</small>'+button('添加勾选的模型','id="add"')+'<div id="presets"></div>',()=>api('presets',{Presets:list}));
 function draw(){
 $('catalog').innerHTML=directory.all.filter(m=>!list.some(p=>p.Model===m)).map(m=>`<label><input type="checkbox" value="${esc(m)}"> ${esc(m)}</label>`).join('')||'<small>暂无可添加的模型。</small>';
 $('presets').innerHTML=list.map((p,i)=>`<article data-i="${i}"><div class="grid">${field('显示名称',input('pn'+i,'text',p.Name,'data-k="Name"'))}${field('实际模型 ID',`<select data-k="Model">${options([...new Set([p.Model,...directory.all])].map(m=>[m,m]),p.Model)}</select>`)}</div>${field('可用推理强度（逗号分隔）',input('pe'+i,'text',(p.Efforts||[]).join(', '),'data-k="Efforts"'))}${field('默认推理强度',input('pd'+i,'text',p.Default,'data-k="Default"'))}${button('删除预设','data-del')}</article>`).join('')}
 $('presets').oninput=e=>{const k=e.target.dataset.k;if(k)list[Number(e.target.closest('article').dataset.i)][k]=k==='Efforts'?e.target.value.split(/[,，]/).map(s=>s.trim()):e.target.value};$('presets').onclick=e=>{if(e.target.hasAttribute('data-del')){list.splice(Number(e.target.closest('article').dataset.i),1);draw()}};
 $('add').onclick=()=>{for(const box of $('catalog').querySelectorAll(':checked')){if(!list.some(p=>p.Model===box.value))list.push({Name:box.value,Model:box.value,Efforts:['','low','medium','high','xhigh'],Default:''})}draw()};draw()});
setInterval(guarded(async()=>{if(!document.hidden&&!modal.open&&!$('dashboard').hidden){const expanded=new Set([...document.querySelectorAll('#runs details[open]')].map(d=>d.dataset.run));await reload();for(const d of document.querySelectorAll('#runs details'))d.open=expanded.has(d.dataset.run)}}),5000);
async function reconnect(){
 $('retry').hidden=true;$('notice').textContent='正在连接 CPA…';
 try{await connect()}catch(err){
  if($('login').hidden){$('retry').hidden=false;report(err)}
  else{$('notice').textContent='CPA 需要管理密钥。'}
 }
}
try{key=sessionStorage.getItem(sessionKey)||''}catch{}
$('retry').onclick=reconnect;reconnect();
