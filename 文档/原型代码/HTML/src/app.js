(() => {
  'use strict';
  // All inventory below is fictitious. No business API is connected in this preview.
  const assets = [
    {id:'QY-02418',title:'月白 · 极光典藏衣橱',art:0,price:4280,unit:'account',mode:'sale',server:'华东一区',level:10,kind:'single',verified:true,rare:true,rating:'4.9',tags:['XVIP 王钻','典藏 ×12','外搭 ×560'],date:8},
    {id:'QY-02426',title:'曜影 · 黑金限定典藏',art:1,price:6880,unit:'account',mode:'sale',server:'华南一区',level:10,kind:'linked',verified:true,rare:true,rating:'5.0',tags:['XVIP 王钻','双号连体','典藏 ×18'],date:6},
    {id:'QY-02431',title:'夜蔷薇 · 暗调高定衣橱',art:2,price:3280,unit:'account',mode:'sale',server:'华北一区',level:8,kind:'single',verified:true,rare:false,rating:'4.9',tags:['VIP 8','服饰 ×320','单号独立'],date:7},
    {id:'QY-02439',title:'鎏光 · 蝶翼幻想典藏',art:3,price:8200,unit:'account',mode:'sale',server:'华东一区',level:10,kind:'linked',verified:true,rare:true,rating:'5.0',tags:['XVIP 王钻','典藏 ×26','稀有翅膀'],date:3},
    {id:'QY-02445',title:'月白 · 流银盛装日租',art:0,price:180,unit:'day',mode:'rent',server:'华东一区',level:9,kind:'single',verified:true,rare:false,rating:'4.8',tags:['VIP 9','典藏 ×8','按日租赁'],date:5},
    {id:'QY-02452',title:'曜影 · 礼服珍藏周租',art:1,price:680,unit:'week',mode:'rent',server:'华南一区',level:9,kind:'single',verified:true,rare:false,rating:'4.9',tags:['VIP 9','外搭 ×280','按周租赁'],date:4},
    {id:'QY-02466',title:'夜蔷薇 · 新入藏风格衣橱',art:2,price:2680,unit:'account',mode:'sale',server:'华北一区',level:7,kind:'single',verified:false,rare:false,rating:'4.8',tags:['VIP 7','服饰 ×210','资料待核验'],date:10},
    {id:'QY-02473',title:'鎏光 · 蝶翼典藏月租',art:3,price:1680,unit:'month',mode:'rent',server:'华东一区',level:10,kind:'single',verified:true,rare:true,rating:'4.9',tags:['XVIP 王钻','典藏 ×16','按月租赁'],date:9}
  ];
  const $ = (q, root=document) => root.querySelector(q);
  const $$ = (q, root=document) => [...root.querySelectorAll(q)];
  const money = n => n.toLocaleString('en-US');
  const esc = s => String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const icon = name => `<svg aria-hidden="true"><use href="#icon-${name}"/></svg>`;
  const unitLabel = {account:'出售总价',day:'日租价格',week:'周租价格',month:'月租价格'};
  const unitSuffix = {account:'',day:'/ 日',week:'/ 周',month:'/ 月'};
  const defaults = {low:0,high:20000,unit:'all',level:'all',verified:false,kind:'all',q:'',category:'all',sort:'featured'};
  let state = {...defaults}, draft = {...state};
  const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)');
  const formFields = f => `
    <fieldset class="filter-field"><legend>价格区间 <small class="mono">CNY</small></legend>
      <select class="unit-filter" name="unit" aria-label="报价单位">
        <option value="all">全部报价单位</option><option value="account">出售 · 账号总价</option><option value="day">租赁 · 每日价格</option><option value="week">租赁 · 每周价格</option><option value="month">租赁 · 每月价格</option>
      </select>
      <div class="range-values mono"><span data-low-value>¥ 0</span><i>—</i><span data-high-value>¥ 20,000</span></div>
      <div class="range-inputs"><div class="range-track"><div class="range-progress"></div></div><input name="low" type="range" min="0" max="20000" step="100" value="0" aria-label="最低价格"><input name="high" type="range" min="0" max="20000" step="100" value="20000" aria-label="最高价格"></div>
      <div class="range-hint"><span>¥ 0</span><span>¥ 20,000+</span></div><p class="budget-hint">不同报价单位分别筛选与排序。</p>
    </fieldset>
    <fieldset class="filter-field"><legend>账号等级</legend><div class="filter-pills">${[['all','不限'],['8','VIP 8+'],['10','XVIP 10']].map(([v,t])=>`<label><input type="radio" name="level" value="${v}"><span>${t}</span></label>`).join('')}</div></fieldset>
    <fieldset class="filter-field"><legend>核验状态</legend><label class="check-label"><input type="checkbox" name="verified"><span>仅看已验号</span>${icon('shield')}</label></fieldset>
    <fieldset class="filter-field"><legend>账号结构</legend><div class="filter-pills">${[['all','不限'],['single','单号'],['linked','连体']].map(([v,t])=>`<label><input type="radio" name="kind" value="${v}"><span>${t}</span></label>`).join('')}</div></fieldset>`;

  function matching(s) {
    const query = s.q.trim().toLowerCase();
    let list = assets.filter(p =>
      (s.category==='all'||(s.category==='rare'?p.rare:p.mode===s.category)) &&
      (s.unit==='all'||p.unit===s.unit) &&
      (p.price>=s.low&&(s.high===20000||p.price<=s.high)) &&
      (s.level==='all'||p.level>=Number(s.level)) &&
      (!s.verified||p.verified) && (s.kind==='all'||p.kind===s.kind) &&
      (!query||`QQ炫舞 ${p.id} ${p.title} ${p.server} ${p.tags.join(' ')} ${p.mode==='sale'?'出售':'租赁'}`.toLowerCase().includes(query))
    );
    if(s.sort==='hot') list.sort(compareHot);
    if(s.sort==='newest') list.sort((a,b)=>b.date-a.date);
    if(s.sort==='price-up') list.sort((a,b)=>a.price-b.price);
    if(s.sort==='price-down') list.sort((a,b)=>b.price-a.price);
    return list;
  }
  function syncForm(form, s) {
    ['unit','low','high'].forEach(k => form.elements[k].value=s[k]);
    ['level','kind'].forEach(k => $$(`[name="${k}"]`,form).forEach(n=>n.checked=n.value===s[k]));
    form.elements.verified.checked=s.verified;
    $('[data-low-value]',form).textContent=`¥ ${money(s.low)}`;
    $('[data-high-value]',form).textContent=`¥ ${money(s.high)}${s.high===20000?'+':''}`;
    const track = $('.range-progress',form);
    track.style.setProperty('--low',`${s.low/200}%`);
    track.style.setProperty('--high',`${s.high/200}%`);
    form.elements.low.setAttribute('aria-valuetext',`${s.low} 元`);
    form.elements.high.setAttribute('aria-valuetext',s.high===20000?'20000 元及以上':`${s.high} 元`);
  }
  $$('.filter-form').forEach(form => {
    form.innerHTML=formFields();
    form.addEventListener('submit',e=>e.preventDefault());
    form.addEventListener('input',e=>{
      const key=e.target.name;
      if(!['unit','low','high','level','verified','kind'].includes(key))return;
      const target=form.dataset.mode==='mobile'?draft:state;
      target[key]=key==='verified'?e.target.checked:['low','high'].includes(key)?Number(e.target.value):e.target.value;
      if(key==='low')target.low=Math.min(target.low,target.high);
      if(key==='high')target.high=Math.max(target.high,target.low);
      if(['low','high'].includes(key)&&target.unit==='all') {
        target.unit=target.category==='rent'?'day':'account';
        toast(`当前按${unitLabel[target.unit]}筛选`);
      }
      if(key==='unit'&&target.unit==='all') {target.low=0;target.high=20000;if(target.sort.startsWith('price'))target.sort='featured';}
      syncForm(form,target);
      if(form.dataset.mode==='mobile')$('#draft-count').textContent=String(matching(draft).length).padStart(2,'0');
      else render();
    });
  });

  function card(p) {
    const rarity=p.rare?'典藏甄选':p.mode==='rent'?'租赁精选':'风格精选';
    return `<article class="asset-card reveal" data-asset="${p.id}"><button class="asset-open" data-report="${p.id}" aria-label="查看${esc(p.title)}，${money(p.price)}元${unitSuffix[p.unit]}的验号档案">
      <span class="asset-image"><span class="asset-art art-${p.art}" role="img" aria-label="${esc(p.title)}概念展示图"></span><span class="verified-badge${p.verified?'':' pending'}">${icon(p.verified?'check':'clock')}${p.verified?'已验号 · '+(p.rare?'极品稀有':'资料齐全'):'资料待核验'}</span><span class="image-bottom"><span>QQ炫舞 · ${p.mode==='sale'?'账号出售':'账号租赁'}</span><span class="mono">CONCEPT / DEMO</span></span></span>
      <span class="asset-body"><span class="asset-id-line"><span class="asset-id">${p.id}</span><span class="rarity">${p.rare?'◇ ':''}${rarity}</span></span><span class="asset-title" role="heading" aria-level="3">${esc(p.title)}</span><span class="asset-meta">${p.server}<i></i>${p.kind==='single'?'单号独立':'连体账号'}<i></i>资料样例</span><span class="pill-tags">${p.tags.map(t=>`<span>${esc(t).replace(/(\d+)/g,'<b>$1</b>')}</span>`).join('')}</span></span>
      <span class="asset-bottom"><span class="seller-info"><span class="seller-rating">${icon('star')}<span class="mono">${p.rating}</span><span>卖家信誉</span></span><span class="seller-cover">${icon('shield')}包赔 · 示例</span></span><span class="asset-price-side"><span class="price-box"><span class="price"><span class="currency">¥</span> ${money(p.price)}</span><span class="price-unit">${p.unit==='account'?'账号总价':unitSuffix[p.unit]}</span></span><span class="asset-arrow">${icon('diagonal')}</span></span></span>
    </button></article>`;
  }
  function activeFilters(s) {
    const list=[];
    if(s.q)list.push(['q',`搜索：${s.q}`]);
    if(s.unit!=='all')list.push(['unit',unitLabel[s.unit]]);
    if(s.low!==0||s.high!==20000)list.push(['budget',`¥ ${money(s.low)} – ${s.high===20000?'不限':money(s.high)}`]);
    if(s.level!=='all')list.push(['level',`VIP ${s.level}+`]);
    if(s.verified)list.push(['verified','已验号']);
    if(s.kind!=='all')list.push(['kind',s.kind==='single'?'单号':'连体']);
    return list;
  }
  function render() {
    const list=matching(state);
    $('#asset-grid').innerHTML=list.map(card).join('');
    $('#result-count').textContent=String(list.length).padStart(2,'0');
    $('#empty-state').hidden=!!list.length;
    $('.collection-end').hidden=!list.length;
    const filters=activeFilters(state);
    $('#applied-filters').hidden=!filters.length;
    $('#applied-filters').innerHTML=filters.map(([key,label])=>`<button class="filter-chip" data-remove="${key}" aria-label="移除${esc(label)}">${esc(label)} ${icon('close')}</button>`).join('');
    $('#mobile-filter-count').hidden=!filters.length;
    $('#mobile-filter-count').textContent=filters.length;
    $('#sort-select').value=state.sort;
    $$('[data-category]').forEach(btn=>{const active=btn.dataset.category===state.category;btn.classList.toggle('active',active);btn.setAttribute('aria-selected',active);btn.tabIndex=active?0:-1;});
    syncForm($('#desktop-filters'),state);
    reveal($('#asset-grid'));
  }

  let observer;
  if('IntersectionObserver' in window&&!reduceMotion.matches)observer=new IntersectionObserver(entries=>entries.forEach(entry=>{
    if(entry.isIntersecting){entry.target.classList.add('in-view');observer.unobserve(entry.target);}
  }),{threshold:.05});
  function reveal(root=document){if(!observer)return;$$('.reveal',root).forEach((el,i)=>{el.style.setProperty('--delay',`${Math.min(i%3*55,110)}ms`);el.classList.add('ready');observer.observe(el);});}

  let toastTimer;
  function toast(message) {
    const el=$('#toast');el.textContent=message;el.classList.add('visible');
    clearTimeout(toastTimer);toastTimer=setTimeout(()=>el.classList.remove('visible'),2800);
  }
  let returnFocus=null;
  function openDialog(dialog) {
    const existing=$('dialog[open]');
    if(!existing)returnFocus=document.activeElement;
    $$('dialog[open]').forEach(d=>{clearTimeout(d.closeTimer);d.classList.remove('closing');d.close();});
    dialog.classList.remove('closing');dialog.showModal();dialog.scrollTop=0;document.body.classList.add('modal-open');
  }
  function closeDialog(dialog) {
    if(!dialog?.open||dialog.classList.contains('closing'))return;
    dialog.classList.add('closing');
    dialog.closeTimer=setTimeout(()=>{dialog.close();dialog.classList.remove('closing');if(!$('dialog[open]')){document.body.classList.remove('modal-open');if(returnFocus?.isConnected)returnFocus.focus({preventScroll:true});}},reduceMotion.matches?0:170);
  }
  $$('dialog').forEach(d=>{
    d.addEventListener('cancel',e=>{e.preventDefault();closeDialog(d);});
    d.addEventListener('close',()=>{if(!$('dialog[open]'))document.body.classList.remove('modal-open');});
    d.addEventListener('click',e=>{if(e.target!==d)return;const r=d.getBoundingClientRect();if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom)closeDialog(d);});
  });
  function searchResults() {
    const q=$('#quick-search').value;
    const list=matching({...defaults,q}).slice(0,6);
    $('#quick-results').innerHTML=list.length?list.map(p=>`<button class="quick-result" data-report="${p.id}"><span class="quick-image"><span class="asset-art art-${p.art}"></span></span><span class="quick-info"><strong>${esc(p.title)}</strong><small>${p.id} · ${p.server}</small></span><span class="quick-price mono">¥ ${money(p.price)}<small>${unitSuffix[p.unit]}</small></span>${icon('chevron')}</button>`).join(''):'<p class="quick-empty">没有匹配的资产，试试“月白”或“华东一区”。</p>';
  }
  function openSearch() {$('#quick-search').value=state.q;searchResults();openDialog($('#search-dialog'));$('#quick-search').focus();}
  $('#quick-search').addEventListener('input',searchResults);
  $('#quick-search').addEventListener('keydown',e=>{if(e.key==='Enter'){e.preventDefault();state={...defaults,q:e.target.value.trim()};render();closeDialog($('#search-dialog'));$('#collection').scrollIntoView({behavior:reduceMotion.matches?'instant':'smooth'});}});
  document.addEventListener('keydown',e=>{if((e.metaKey||e.ctrlKey)&&e.key.toLowerCase()==='k'){e.preventDefault();openSearch();}});

  function openReport(id) {
    const p=assets.find(a=>a.id===id);if(!p)return;
    const checks=['基础账号资料','装扮与典藏数量','账号绑定信息','持有人资料一致性'];
    $('#report-content').innerHTML=`<div class="report-summary"><div class="report-image"><div class="asset-art art-${p.art}" role="img" aria-label="${esc(p.title)}概念展示图"></div></div><span class="asset-id">${p.id} / QQ炫舞</span><h3>${esc(p.title)}</h3><p class="report-meta">${p.server} · ${p.kind==='single'?'单号独立':'连体账号'} · ${p.mode==='sale'?'账号出售':'账号租赁'}</p><p class="price"><span class="currency">¥</span> ${money(p.price)}<small>${p.unit==='account'?'账号总价':unitSuffix[p.unit]}</small></p></div>
      <div class="report-tabs" role="tablist" aria-label="资产档案内容"><button role="tab" class="active" aria-selected="true" data-report-tab="certificate" aria-controls="certificate-panel">验号报告</button><button role="tab" aria-selected="false" tabindex="-1" data-report-tab="overview" aria-controls="overview-panel">资产明细</button></div>
      <div class="report-panel" id="certificate-panel" role="tabpanel" aria-label="验号报告"><section class="certificate"><div class="certificate-top"><div><span class="certificate-kicker mono">CERTIFICATE OF VERIFICATION</span><h4>${p.verified?'账号核验记录':'资料待核验'}</h4><small class="mono">QINGYE DIGITAL ASSET / SAMPLE</small></div><div class="certificate-stamp">${p.verified?'核验':'待审'}<br>样例</div></div><div class="certificate-rule"></div><dl class="certificate-data"><div><dt>档案编号</dt><dd>VR-2026-${p.id.slice(3)}</dd></div><div><dt>对应资产</dt><dd>${p.id}</dd></div><div><dt>展示日期</dt><dd>2026.11.10</dd></div><div><dt>报告状态</dt><dd>${p.verified?'演示 · 核验通过':'演示 · 等待审核'}</dd></div></dl><ul class="check-list">${checks.map(t=>`<li><span>${t}</span><span class="check-result">${icon(p.verified?'check':'clock')}${p.verified?'样例通过':'待核对'}</span></li>`).join('')}</ul><div class="certificate-foot"><span>QINGYE · 仅用于界面效果展示</span>${icon('shield')}</div></section><p class="report-note">此证书为交互样例，并非真实验号证明。图片为概念展示图；核验结果、卖家信誉及包赔标识均为演示，不构成实际保障或赔付承诺。</p></div>
      <div class="report-panel" id="overview-panel" role="tabpanel" aria-label="资产明细" hidden><dl class="report-data-list"><div><dt>游戏与区服</dt><dd>QQ炫舞 · ${p.server}</dd></div><div><dt>账号等级</dt><dd class="mono">VIP ${p.level}</dd></div><div><dt>账号结构</dt><dd>${p.kind==='single'?'单号独立':'连体账号'}</dd></div><div><dt>交易类型</dt><dd>${unitLabel[p.unit]}</dd></div><div><dt>主要装扮</dt><dd>${esc(p.tags[1])}</dd></div><div><dt>核验状态</dt><dd>${p.verified?'已核验（样例）':'待核验（样例）'}</dd></div></dl><p class="report-note">当前展示信息均为样例。正式交易需以实际商品资料、核验记录及客服确认结果为准。</p></div>
      <div class="report-footer"><button class="button-outline" data-copy="${p.id}">${icon('copy')}复制编号</button><button class="button-white" data-action="support">咨询这件资产 ${icon('arrow')}</button></div>`;
    let actions=$('#report-actions');
    if(!actions){actions=document.createElement('div');actions.id='report-actions';actions.className='report-footer';$('#report-dialog').append(actions);}
    const footer=$('#report-content .report-footer');
    actions.replaceChildren(...footer.childNodes);footer.remove();
    openDialog($('#report-dialog'));$('#report-content').scrollTop=0;
  }
  const info = {
    assurance: {title:'每一项资料，都有依据',body:`<div class="info-icon">${icon('shield')}</div><ol class="info-steps"><li><span class="mono">01</span><div><strong>提交完整资料</strong><p>整理区服、装扮清单与账号截图，明确展示信息。</p></div></li><li><span class="mono">02</span><div><strong>核对核心信息</strong><p>核对账号信息与资产明细，记录核验结果。</p></div></li><li><span class="mono">03</span><div><strong>查阅档案，再作决定</strong><p>在商品卡片内查看资料、状态和核验记录。</p></div></li></ol><p class="info-copy">本页为设计预览，所有验号及保障内容均为样例。实际核验标准与服务条款尚未接入。</p><button class="button-white" data-close>了解了 ${icon('check')}</button>`},
    publish: {title:'为你的珍藏，开启下一章',body:`<p class="info-copy">选择适合你的资料提交方式。账号出售与租赁，均可在审核通过后展示。</p><button class="info-choice" data-action="self-publish">${icon('file')}<span><strong>自主提交资料</strong><small>填写账号信息，上传图片，等待审核</small></span>${icon('chevron')}</button><button class="info-choice" data-action="support">${icon('headset')}<span><strong>联系客服协助</strong><small>由客服协助整理资料与上架</small></span>${icon('chevron')}</button><p class="report-note">当前仅预览首页入口，发布功能尚未接入。</p>`},
    'self-publish': {title:'自主发布 · 入口预览',body:`<div class="info-icon">${icon('file')}</div><p class="info-copy">后续可在这里选择「出售 / 租赁」、填写 QQ 炫舞账号资料，并上传展示图片。<br><br>本次只确认首页风格，暂不收集账号、手机号或图片。</p><button class="button-white" data-close>返回浏览 ${icon('arrow')}</button>`},
    support: {title:'客服咨询 · 入口预览',body:`<div class="info-icon">${icon('headset')}</div><p class="info-copy">正式页面将在这里展示客服二维码或联系方式，协助确认商品资料与交付细节。<br><br>当前尚未接入真实客服，请勿在预览页提交个人或账号信息。</p><button class="button-white" data-close>返回浏览 ${icon('arrow')}</button>`}
  };
  function openInfo(type){const content=info[type];if(!content)return;$('#info-title').textContent=content.title;$('#info-content').innerHTML=content.body;openDialog($('#info-dialog'));}
  $('#mobile-filter-button').addEventListener('click',()=>{draft={...state};syncForm($('#mobile-filters'),draft);$('#draft-count').textContent=String(matching(draft).length).padStart(2,'0');openDialog($('#filter-dialog'));});
  $('#reset-draft').addEventListener('click',()=>{draft={...defaults,category:state.category,q:state.q};syncForm($('#mobile-filters'),draft);$('#draft-count').textContent=String(matching(draft).length).padStart(2,'0');});
  $('#apply-filters').addEventListener('click',()=>{state={...draft};render();closeDialog($('#filter-dialog'));});
  $('#sort-select').addEventListener('change',e=>{
    state.sort=e.target.value;
    if(state.sort.startsWith('price')&&state.unit==='all') {state.unit=state.category==='rent'?'day':'account';toast(`当前按${unitLabel[state.unit]}排序，可在筛选中切换`);}
    render();
  });
  document.addEventListener('click',async e=>{
    const button=e.target.closest('button');if(!button)return;
    if(button.hasAttribute('data-close')){closeDialog(button.closest('dialog'));return;}
    if(button.dataset.report){openReport(button.dataset.report);return;}
    if(button.dataset.reportTab){
      $$('[data-report-tab]').forEach(t=>{const active=t===button;t.classList.toggle('active',active);t.setAttribute('aria-selected',active);t.tabIndex=active?0:-1;});
      $('#certificate-panel').hidden=button.dataset.reportTab!=='certificate';$('#overview-panel').hidden=button.dataset.reportTab!=='overview';return;
    }
    if(button.dataset.action){button.dataset.action==='search'?openSearch():openInfo(button.dataset.action);return;}
    if(button.dataset.category){
      state.category=button.dataset.category;
      // Changing the trade type clears its incompatible price unit and budget.
      if((state.category==='sale'&&state.unit!=='account')||(state.category==='rent'&&state.unit==='account')){state.unit='all';state.low=0;state.high=20000;state.sort='featured';}
      render();return;
    }
    if(button.dataset.reset){state={...defaults};render();return;}
    if(button.dataset.remove){
      const key=button.dataset.remove;
      if(key==='budget'){state.low=0;state.high=20000;}
      else state[key]=defaults[key];
      if(key==='unit'){state.low=0;state.high=20000;if(state.sort.startsWith('price'))state.sort='featured';}
      render();return;
    }
    if(button.dataset.copy){
      try {await navigator.clipboard.writeText(button.dataset.copy);button.innerHTML=`${icon('check')}已复制`;setTimeout(()=>{if(button.isConnected)button.innerHTML=`${icon('copy')}复制编号`;},2200);}
      catch {button.textContent=button.dataset.copy;button.setAttribute('aria-label',`请手动复制编号 ${button.dataset.copy}`);}
    }
  });
  $$('[role="tablist"]').forEach(list=>list.addEventListener('keydown',e=>{
    if(!['ArrowRight','ArrowLeft','Home','End'].includes(e.key))return;
    const tabs=$$('[role="tab"]',list),index=tabs.indexOf(document.activeElement);if(index<0)return;
    e.preventDefault();const next=e.key==='Home'?0:e.key==='End'?tabs.length-1:(index+(e.key==='ArrowRight'?1:-1)+tabs.length)%tabs.length;tabs[next].click();tabs[next].focus();
  }));
  // Report tabs are created on demand, so their keyboard handler is delegated.
  $('#report-dialog').addEventListener('keydown',e=>{if(!e.target.matches('[data-report-tab]')||!['ArrowRight','ArrowLeft','Home','End'].includes(e.key))return;e.preventDefault();const tabs=$$('[data-report-tab]');const next=(e.key==='Home'?tabs[0]:e.key==='End'?tabs[1]:tabs.find(t=>t!==e.target));next.click();next.focus();});
  let scrollQueued=false;
  addEventListener('scroll',()=>{if(scrollQueued)return;scrollQueued=true;requestAnimationFrame(()=>{$('#nav-wrap').classList.toggle('scrolled',scrollY>12);scrollQueued=false;});},{passive:true});
  addEventListener('resize',()=>{if(innerWidth>=1024&&$('#filter-dialog').open)closeDialog($('#filter-dialog'));});
  render();reveal(document);

  __V3_SCRIPT__
})();
