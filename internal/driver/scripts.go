package driver

const ReadyJS = `
(() => {
  const href = location.href || '';
  const h = href.toLowerCase();
  const bodyLen = document.body ? document.body.innerText.length : 0;
  const isHttp = h.startsWith('http://127.0.0.1') || h.startsWith('http://localhost')
              || h.startsWith('http://[::1]');
  return { href: href.slice(0, 120), bodyLen: bodyLen, ready: isHttp && bodyLen > 200 };
})()
`

const DiscoverJS = `
(() => {
  const info = (el) => {
    const attrs = {};
    for (const a of (el.attributes || [])) attrs[a.name] = a.value;
    const r = el.getBoundingClientRect();
    const vis = r.width > 0 && r.height > 0;
    return {
      tag: el.tagName.toLowerCase(),
      attrs: attrs,
      cls: (el.className || '').toString().slice(0, 120),
      placeholder: el.getAttribute('placeholder'),
      ariaLabel: el.getAttribute('aria-label'),
      text: (el.innerText || el.value || '').trim().slice(0, 30),
      visible: vis,
      rect: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)]
    };
  };
  const inputs = [...document.querySelectorAll(
    'textarea,input,[contenteditable="true"],[role="textbox"]'
  )].map(info);
  const btns = [...document.querySelectorAll('button,[role="button"],svg,[class*="send" i]')]
    .slice(0, 40).map(info);
  return {
    url: location.href,
    title: document.title,
    bodyText: document.body ? document.body.innerText.slice(0, 4000) : '',
    inputs, buttons: btns
  };
})()
`

const SendButtonJSTemplate = `
(() => {
  const kws = %s;
  const click = %s;
  const cands = [...document.querySelectorAll('button,[role="button"],div,span,svg')];
  let disabledSeen = null;
  for (const el of cands) {
    const blob = ((el.innerText||'') + ' ' + (el.getAttribute('aria-label')||'')
                  + ' ' + (el.className||'').toString() + ' ' + (el.getAttribute('title')||'')).toLowerCase();
    if (!kws.some(k => blob.includes(k))) continue;
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) continue;
    if (r.width > 260 || r.height > 120) continue;   // 排除容器
    const btn = el.closest('button,[role="button"]') || el;
    const label = ((btn.innerText||'') + '/' + (btn.getAttribute('aria-label')||'')
                   + '/' + (btn.getAttribute('title')||'') + '/'
                   + ((btn.className||'').toString())).replace(/\s+/g, ' ').trim().slice(0, 40)
                  || 'clicked';
    const dis = !!btn.disabled || btn.getAttribute('aria-disabled') === 'true';
    if (dis) { if (!disabledSeen) disabledSeen = 'disabled:' + label; continue; }
    if (click) btn.click();
    return label;
  }
  return disabledSeen;
})()
`

const AutoConfirmJSTemplate = `
(() => {
  const prefer = %s || [];
  const deny   = %s || [];
  // 导航/Tab 类组件永不是选项（实测首页 Tab: home-page__tab / tabs）
  const navRe = /(^|[\s_-])(tab|tabs|nav|menu|toolbar|panel)([\s_-]|$)/i;
  const inChatArea = (el) => {
    for (let n = el; n; n = n.parentElement) {
      const c = (n.className || '').toString();
      if (c.includes('sidebar') || c.includes('chat-toolbar')) return false;
      // 选项卡（agent-question-composer）有专属事实驱动路径，通用路径一律不碰：
      // 否则「提交中」期间仍会点到卡片里的禁用选项，制造假事件。
      if (c.includes('agent-question')) return false;
    }
    return true;
  };
  const isDeny = (t) => deny.some(w => t.includes(w));
  const visibleText = (el) => {
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) return null;
    const t = (el.innerText || '').trim();
    if (!t || t.length > 60) return null;
    return t;
  };
  // 点击前判 disabled：禁用按钮（如「提交中」）点了也没用，
  // 还会吃掉一次尝试次数，误触发人工介入。
  const clickEl = (el) => {
    if (el.disabled || el.getAttribute('aria-disabled') === 'true') return false;
    try { el.click(); return true; } catch (e) { return false; }
  };
  const els = [...document.querySelectorAll('button,[role="button"]')]
    .filter(el => inChatArea(el));

  // 1) 关键词路径（拒绝词永不点；导航类组件永不点）
  for (const el of els) {
    const t = visibleText(el);
    const c = (el.className || '').toString();
    if (t && !isDeny(t) && !navRe.test(t) && !navRe.test(c)
        && prefer.some(k => t.includes(k))) {
      return {text: t.slice(0, 40), cls: (el.className || '').toString().slice(0, 80),
              mode: 'keyword', clicked: clickEl(el)};
    }
  }
  // 2) 结构路径：选项组（同父 ≥2 个带文本按钮）→ 第一个非拒绝项
  const byParent = new Map();
  for (const el of els) {
    const t = visibleText(el);
    if (!t) continue;
    const p = el.parentElement;
    if (!p) continue;
    if (!byParent.has(p)) byParent.set(p, []);
    byParent.get(p).push({el, t, cls: (el.className || '').toString()});
  }
  for (const [p, group] of byParent) {
    const pCls = (p.className || '').toString();
    if (group.length >= 2 && !navRe.test(pCls)
        && !group.some(g => navRe.test(g.cls))) {
      // 拒绝过滤放在选择时：避免把「取消 + 继续」这类二元组误判成单按钮
      const pick = group.find(g => !isDeny(g.t));
      if (!pick) continue;                     // 整组都是拒绝类 → 不点
      const clicked = clickEl(pick.el);
      return {text: pick.t.slice(0, 40),
              cls: (pick.el.className || '').toString().slice(0, 80),
              parentCls: (p.className || '').toString().slice(0, 80),
              texts: group.map(g => g.t.slice(0, 30)),
              mode: 'first-option', clicked};
    }
  }
  return null;
})()
`

const QCardJS = `
(() => {
  const vis = (el) => {
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  };
  const clsList = (el) => ((el && el.className) || '').toString().split(/\s+/);
  const txt = (el) => ((el && el.innerText) || '').trim();
  const roots = [...document.querySelectorAll('[class*="agent-question-composer"]')]
    .filter(el => clsList(el).includes('agent-question-composer') && vis(el));
  if (!roots.length) return {found: false};
  const composer = roots[roots.length - 1];

  const titleEl = composer.querySelector('#agent-question-title')
              || composer.querySelector('.agent-question-composer__heading h2');
  const counter = txt(composer.querySelector('.agent-question-composer__counter'));
  const m = /^(\d+)\s*\/\s*(\d+)$/.exec(counter);
  const questionIndex = m ? Number(m[1]) - 1 : null;
  const questionCount = m ? Number(m[2]) : null;

  const options = [...composer.querySelectorAll('button.agent-question-option')].map(el => ({
    label: (txt(el.querySelector('.agent-question-option__label')) || txt(el)).slice(0, 80),
    selected: clsList(el).includes('is-selected'),
    disabled: !!el.disabled,
    visible: vis(el),
  }));

  const otherInput = composer.querySelector('.agent-question-other input');
  const customInput = otherInput ? (otherInput.value || '') : null;
  const inputDisabled = otherInput ? !!otherInput.disabled : true;

  const buttons = [
    ...composer.querySelectorAll('.agent-question-composer__footer button'),
    ...composer.querySelectorAll('.agent-question-composer__nav button'),
  ].map(el => {
    const c = (el.className || '').toString();
    const kind = c.includes('composer__primary') ? 'primary'
               : c.includes('composer__skip') ? 'skip'
               : c.includes('composer__close') ? 'close' : 'nav';
    return {kind, text: txt(el).slice(0, 20), disabled: !!el.disabled, visible: vis(el)};
  });

  const primary = buttons.find(b => b.kind === 'primary' && b.visible) || null;
  const skip = buttons.find(b => b.kind === 'skip' && b.visible) || null;
  const submitting = !!(primary && (
    primary.text.includes('提交中')
    || (primary.disabled && options.length > 0 && options.every(o => o.disabled))
  ));

  return {
    found: true, question: txt(titleEl).slice(0, 150), counter,
    questionIndex, questionCount,
    isLast: (questionIndex !== null && questionCount !== null)
              ? questionIndex === questionCount - 1 : null,
    options, customInput, inputDisabled, submitting, primary, skip,
  };
})()
`

const QCardClickJSTemplate = `
(() => {
  const kind = %s;
  const idx  = %s;
  const text = %s;
  const vis = (el) => {
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  };
  const clsList = (el) => ((el && el.className) || '').toString().split(/\s+/);
  const roots = [...document.querySelectorAll('[class*="agent-question-composer"]')]
    .filter(el => clsList(el).includes('agent-question-composer') && vis(el));
  if (!roots.length) return {ok: false, reason: 'no-card'};
  const composer = roots[roots.length - 1];

  let el = null;
  if (kind === 'primary') {
    el = composer.querySelector('button.agent-question-composer__primary');
  } else if (kind === 'option') {
    el = [...composer.querySelectorAll('button.agent-question-option')][idx] || null;
  } else if (kind === 'fill-other') {
    el = composer.querySelector('.agent-question-other input');
  }
  if (!el) return {ok: false, reason: 'no-target'};
  if (!vis(el)) return {ok: false, reason: 'invisible'};
  if (el.disabled || el.getAttribute('aria-disabled') === 'true') {
    return {ok: false, reason: 'disabled', text: (el.innerText || '').trim().slice(0, 30)};
  }
  if (kind === 'fill-other') {
    const proto = window.HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
    setter.call(el, text);
    el.dispatchEvent(new Event('input', {bubbles: true}));
    el.dispatchEvent(new Event('change', {bubbles: true}));
    return {ok: true, kind: kind, text: text.slice(0, 30)};
  }
  el.click();
  return {ok: true, kind: kind, text: ((el.innerText || '') + '').trim().slice(0, 40)};
})()
`

const PageStateJS = `
(() => {
  const vis = (el) => {
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  };
  const body = (document.body ? document.body.innerText : '') || '';
  const loginRe = /(登录|扫码|验证码|账号|密码|sign\s*in|log\s*in)/i;
  const pwd = [...document.querySelectorAll('input[type="password"]')].some(vis);
  const composer = [...document.querySelectorAll(
    'textarea,[contenteditable="true"],[role="textbox"]')].some(vis);
  const newTask = [...document.querySelectorAll('button')].some(b =>
    ((b.innerText || '').includes('新建任务')
     || ((b.className || '').toString().includes('sidebar-primary-action'))));
  return {
    href: (location.href || '').slice(0, 160),
    title: (document.title || '').slice(0, 80),
    has_password_input: pwd,
    has_composer: composer,
    has_new_task: newTask,
    login_like: pwd || loginRe.test(body),
    body_head: body.replace(/\s+/g, ' ').slice(0, 120),
  };
})()
`
