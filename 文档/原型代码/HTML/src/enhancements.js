  // Homepage composition: editorial selection is separate from rule-based recommendations.
  // This is fixture data, not real traffic. Go + Redis will supply the live ranks later.
  const editorial = [
    {id:'QY-02418',kicker:'THE IVORY EDITION',name:'月白极光',collection:'典藏衣橱',description:'银白丝缎与羽饰，在细节中收藏你的审美。'},
    {id:'QY-02426',kicker:'THE NOIR EDITION',name:'曜影黑金',collection:'双号典藏',description:'利落剪裁，克制金饰。一份值得慢慢欣赏的珍藏。'},
    {id:'QY-02473',kicker:'THE BUTTERFLY EDITION',name:'鎏光蝶翼',collection:'月租甄选',description:'为下一段旅程，换一种出场方式。'}
  ];
  const rankingFixture = {
    'QY-02418':{views7:155,favorites7:20,ageHours:100},
    'QY-02426':{views7:130,favorites7:19,ageHours:120},
    'QY-02431':{views7:160,favorites7:14,ageHours:110},
    'QY-02439':{views7:120,favorites7:18,ageHours:160},
    'QY-02445':{views7:70,favorites7:9,ageHours:140},
    'QY-02452':{views7:98,favorites7:12,ageHours:150},
    'QY-02466':{views7:22,favorites7:2,ageHours:12},
    'QY-02473':{views7:65,favorites7:8,ageHours:36}
  };
  function hotScore(p){const m=rankingFixture[p.id];return m.views7+5*m.favorites7+(m.ageHours<72?10:0);}
  function compareHot(a,b){return hotScore(b)-hotScore(a)||b.date-a.date||a.id.localeCompare(b.id);}
  let recommendationMode='hot';
  function renderRecommendations(mode='hot'){
    recommendationMode=mode;
    const featuredIds=new Set(editorial.map(p=>p.id));
    const candidates=assets.filter(p=>!featuredIds.has(p.id));
    candidates.sort(mode==='hot'?compareHot:(a,b)=>b.date-a.date||a.id.localeCompare(b.id));
    const grid=$('#recommendation-grid');
    grid.innerHTML=candidates.slice(0,3).map(card).join('');
    grid.setAttribute('aria-label',mode==='hot'?'热门推荐':'最新上架');
    grid.scrollLeft=0;
    $$('[data-recommendation]').forEach(b=>{const selected=b.dataset.recommendation===mode;b.classList.toggle('active',selected);b.setAttribute('aria-selected',selected);b.tabIndex=selected?0:-1;});
    $('#recommendation-caption').textContent=mode==='hot'?'近 7 日关注 · 排序样例':'按首次公开时间 · 样例';
    reveal(grid);
  }
  function browse(mode){
    state={...defaults};
    if(mode==='sale'||mode==='rent')state.category=mode;
    if(mode==='newest'||mode==='hot')state.sort=mode;
    render();
    $('#collection').scrollIntoView({behavior:reduceMotion.matches?'instant':'smooth',block:'start'});
  }
  info.recommendation={title:'推荐，有清晰的依据',body:`<div class="info-icon">${icon('star')}</div><ol class="info-steps"><li><span class="mono">01</span><div><strong>本期甄选</strong><p>运营挑选的账号，独立展示，不算作自然热度。</p></div></li><li><span class="mono">02</span><div><strong>热门推荐</strong><p>依据近期有效浏览、收藏及首次公开时间排序。正式权重仍待确认。</p></div></li><li><span class="mono">03</span><div><strong>最新上架</strong><p>按首次公开时间展示，修改资料不会变成新上架。</p></div></li></ol><p class="info-copy">当前是首页交互样稿，使用固定样例数据演示排序。没有采集真实行为，也未连接推荐服务；这不是个性化推荐。</p><button class="button-white" data-close>继续浏览 ${icon('arrow')}</button>`};
  info.account={title:'个人中心 · 入口预览',body:`<div class="info-icon">${icon('user')}</div><p class="info-copy">后续登录后可管理收藏、提交账号资料、查看审核结果。浏览和联系客服仍可直接使用。</p><button class="info-choice" data-action="login-preview">${icon('lock')}<span><strong>登录 / 注册</strong><small>短信或手机号密码登录</small></span>${icon('chevron')}</button><button class="info-choice" data-action="self-publish">${icon('file')}<span><strong>我的发布</strong><small>草稿、审核进度与已发布账号</small></span>${icon('chevron')}</button><p class="report-note">本轮只制作首页，此入口尚未连接会员服务。</p>`};
  info['login-preview']={title:'登录与注册 · 待开发页面',body:`<div class="info-icon">${icon('lock')}</div><p class="info-copy">后续页面沿用当前字体、配色、输入框与动效，支持短信注册、短信登录和密码登录。<br><br>此预览不发送验证码，也不创建真实账号。</p><button class="button-white" data-close>返回浏览 ${icon('arrow')}</button>`};
  document.addEventListener('click',e=>{
    const target=e.target.closest('button');if(!target)return;
    if(target.dataset.browse)browse(target.dataset.browse);
    if(target.dataset.recommendation)renderRecommendations(target.dataset.recommendation);
  });
  $('#recommendation-more').addEventListener('click',()=>browse(recommendationMode));
  renderRecommendations();

  // Accessible carousel: controls, touch swipe, keyboard navigation and explicit pause.
  const carousel=$('#editorial-carousel');
  $('#carousel-slides').innerHTML=editorial.map((slide,i)=>{
    const p=assets.find(a=>a.id===slide.id);
    return `<article class="editorial-slide${i===0?' active':''}" role="group" aria-roledescription="幻灯片" aria-label="${i+1} / ${editorial.length}：${esc(p.title)}" aria-hidden="${i!==0}" ${i===0?'':'inert'}><div class="slide-visual" role="img" aria-label="${esc(p.title)}概念展示图"><div class="asset-art art-${p.art}"></div></div><div class="slide-copy"><span class="slide-kicker mono">${slide.kicker}</span><h3>${slide.name}<span>${slide.collection}</span></h3><p class="slide-description">${slide.description}</p><p class="slide-meta"><span>QQ炫舞</span><i>/</i><span>${p.server}</span><i>/</i><span>${p.mode==='sale'?'账号出售':'按月租赁'}</span></p><div class="slide-action-row"><span class="slide-price mono">¥ ${money(p.price)}<small>${p.unit==='account'?'账号总价':unitSuffix[p.unit]}</small></span><button class="button-white" data-report="${p.id}">查看这件资产 ${icon('arrow')}</button></div></div></article>`;
  }).join('');
  $('#carousel-dots').innerHTML=editorial.map((p,i)=>`<button data-slide="${i}" aria-label="第 ${i+1} 张：${p.name}" aria-current="${i===0}"></button>`).join('');
  const slides=$$('.editorial-slide',carousel), dots=$$('[data-slide]',carousel);
  const slideDuration=7500;
  let slideIndex=0,elapsed=0,lastFrame=0,carouselFrame=0;
  let userPaused=reduceMotion.matches,hoverPaused=false,carouselVisible=true,modalPaused=false;
  const canPlay=()=>!userPaused&&!hoverPaused&&carouselVisible&&!document.hidden&&!modalPaused;
  function syncPlayButton(){
    const paused=userPaused;
    $('#carousel-toggle').setAttribute('aria-label',paused?'播放自动轮播':'暂停自动轮播');
    $('#carousel-toggle').innerHTML=icon(paused?'play':'pause');
  }
  function slideTo(next,manual=true){
    slideIndex=(next+slides.length)%slides.length;
    slides.forEach((slide,i)=>{slide.classList.toggle('active',i===slideIndex);slide.setAttribute('aria-hidden',i!==slideIndex);slide.inert=i!==slideIndex;});
    dots.forEach((dot,i)=>dot.setAttribute('aria-current',i===slideIndex));
    $('#slide-current').textContent=String(slideIndex+1).padStart(2,'0');
    elapsed=0;carousel.style.setProperty('--slide-progress','0');
    if(manual){userPaused=true;$('#carousel-announcement').textContent=`第 ${slideIndex+1} 张，共 ${slides.length} 张：${editorial[slideIndex].name}，已暂停自动轮播。`;}
    syncPlayButton();syncCarousel();
  }
  function carouselTick(now){
    carouselFrame=0;
    if(!canPlay()){lastFrame=0;return;}
    if(lastFrame)elapsed+=Math.min(now-lastFrame,100);
    lastFrame=now;
    carousel.style.setProperty('--slide-progress',String(Math.min(elapsed/slideDuration,1)));
    if(elapsed>=slideDuration)slideTo(slideIndex+1,false);
    if(!carouselFrame)carouselFrame=requestAnimationFrame(carouselTick);
  }
  function syncCarousel(){
    if(canPlay()){if(!carouselFrame){lastFrame=0;carouselFrame=requestAnimationFrame(carouselTick);}}
    else{cancelAnimationFrame(carouselFrame);carouselFrame=0;lastFrame=0;}
  }
  $('#carousel-prev').addEventListener('click',()=>slideTo(slideIndex-1));
  $('#carousel-next').addEventListener('click',()=>slideTo(slideIndex+1));
  dots.forEach((b,i)=>b.addEventListener('click',()=>slideTo(i)));
  $('#carousel-toggle').addEventListener('click',()=>{userPaused=!userPaused;syncPlayButton();syncCarousel();});
  carousel.addEventListener('keydown',e=>{if(!['ArrowLeft','ArrowRight'].includes(e.key))return;e.preventDefault();slideTo(slideIndex+(e.key==='ArrowRight'?1:-1));});
  carousel.addEventListener('pointerenter',e=>{if(e.pointerType==='mouse'){hoverPaused=true;syncCarousel();}});
  carousel.addEventListener('pointerleave',()=>{hoverPaused=false;syncCarousel();});
  carousel.addEventListener('focusin',e=>{if(e.target.closest('#carousel-toggle'))return;userPaused=true;syncPlayButton();syncCarousel();});
  let touchOrigin=null;
  carousel.addEventListener('pointerdown',e=>{if(e.pointerType==='touch')touchOrigin={x:e.clientX,y:e.clientY};});
  carousel.addEventListener('pointerup',e=>{if(!touchOrigin)return;const dx=e.clientX-touchOrigin.x,dy=e.clientY-touchOrigin.y;touchOrigin=null;if(Math.abs(dx)>45&&Math.abs(dx)>Math.abs(dy)*1.4)slideTo(slideIndex+(dx<0?1:-1));});
  carousel.addEventListener('pointercancel',()=>{touchOrigin=null;});
  if('IntersectionObserver' in window)new IntersectionObserver(entries=>{carouselVisible=entries[0].isIntersecting;syncCarousel();},{threshold:.15}).observe(carousel);
  const dialogObserver=new MutationObserver(()=>{modalPaused=!!$('dialog[open]');syncCarousel();});
  $$('dialog').forEach(d=>dialogObserver.observe(d,{attributes:true,attributeFilter:['open']}));
  document.addEventListener('visibilitychange',syncCarousel);
  syncPlayButton();syncCarousel();

  // Pointer light only on precise pointers. No cursor replacement or scroll interception.
  const finePointer=matchMedia('(hover: hover) and (pointer: fine)');
  const aura=$('#cursor-aura');
  let px=innerWidth/2,py=220,tx=px,ty=py,pointerFrame=0,spotTarget=null,spotX=0,spotY=0;
  const pointerAllowed=()=>finePointer.matches&&!reduceMotion.matches&&!document.hidden;
  function lightTick(){
    pointerFrame=0;if(!pointerAllowed())return;
    px+=(tx-px)*.11;py+=(ty-py)*.11;
    aura.style.transform=`translate3d(${(px-350).toFixed(1)}px,${(py-350).toFixed(1)}px,0)`;
    if(spotTarget?.isConnected){spotTarget.style.setProperty('--spot-x',`${spotX}px`);spotTarget.style.setProperty('--spot-y',`${spotY}px`);spotTarget=null;}
    if(Math.abs(tx-px)+Math.abs(ty-py)>.6)pointerFrame=requestAnimationFrame(lightTick);
  }
  document.addEventListener('pointermove',e=>{
    if(!pointerAllowed())return;
    tx=e.clientX;ty=e.clientY;aura.classList.add('visible');
    const target=e.target.closest('.asset-card,.editorial-carousel');
    if(target){const rect=target.getBoundingClientRect();spotTarget=target;spotX=e.clientX-rect.left;spotY=e.clientY-rect.top;}
    if(!pointerFrame)pointerFrame=requestAnimationFrame(lightTick);
  },{passive:true});
  document.documentElement.addEventListener('pointerleave',()=>aura.classList.remove('visible'));
  let progressFrame=0;
  function updateProgress(){
    progressFrame=0;
    const height=document.documentElement.scrollHeight-innerHeight;
    document.documentElement.style.setProperty('--reading-progress',height>0?String(Math.min(scrollY/height,1)):'0');
    document.documentElement.style.setProperty('--ribbon-offset',reduceMotion.matches?'0px':`${Math.min(scrollY*.09,85).toFixed(1)}px`);
  }
  function scheduleProgress(){if(!progressFrame)progressFrame=requestAnimationFrame(updateProgress);}
  addEventListener('scroll',scheduleProgress,{passive:true});addEventListener('resize',scheduleProgress);updateProgress();
  function respectMotion(){
    if(reduceMotion.matches){userPaused=true;syncPlayButton();syncCarousel();cancelAnimationFrame(pointerFrame);pointerFrame=0;aura.classList.remove('visible');$$('.reveal.ready').forEach(el=>el.classList.add('in-view'));}
    updateProgress();
  }
  reduceMotion.addEventListener('change',respectMotion);
  document.addEventListener('visibilitychange',()=>{if(document.hidden){cancelAnimationFrame(pointerFrame);pointerFrame=0;aura.classList.remove('visible');}});
  // Header and bottom banner share the same entrance language, without hiding content if JS fails.
  $$('.collection-header').forEach(el=>el.classList.add('reveal'));reveal(document);
