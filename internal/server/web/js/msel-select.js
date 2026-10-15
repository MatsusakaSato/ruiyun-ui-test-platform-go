/* ================= 单选下拉皮肤 =================
   把原生 <select> 换成与「场景 / 难度」多选下拉（page-cases.js 的 .msel）同款外观：
   白底胶囊 + 标签 + 当前值 + ▾，点开是自定义面板而不是系统下拉。

   原生 select 不删除，只是隐藏后留在包装层里 —— 既有逻辑照旧读写它的
   .value / .disabled / .onchange，本模块只负责外观、开合与回写：
   点击选项时写回 select.value 并派发冒泡的 change 事件，触发原有 onchange。

   面板与选项复用 cases.css 里的 .msel-* 样式，保证与「场景」完全一致。 */

import { $, esc } from './utils.js';

const singles = [];

function closeAll(except) {
  for (const s of singles) if (s !== except) s.close();
}

// enhanceSelect(elOrId, opts) → { el, wrap, render, open, close }
//   label       显示在胶囊里的字段名（省略则不显示，值直接顶到左边）
//   wrapId      包装层的 id（nav.js 要用 #wrapEnv 挂 title / dimmed）
export function enhanceSelect(elOrId, opts = {}) {
  const sel = typeof elOrId === 'string' ? $(elOrId) : elOrId;
  if (!sel || sel.dataset.mselEnhanced) return null;
  sel.dataset.mselEnhanced = '1';

  const wrap = document.createElement('div');
  wrap.className = 'msel msel-single';
  if (opts.wrapId) wrap.id = opts.wrapId;

  const box = document.createElement('div');
  box.className = 'msel-box';
  box.setAttribute('role', 'button');
  box.setAttribute('tabindex', '0');
  box.setAttribute('aria-haspopup', 'listbox');
  box.setAttribute('aria-expanded', 'false');
  box.innerHTML = (opts.label ? `<span class="msel-lb">${esc(opts.label)}</span>` : '')
    + '<span class="msel-val"></span><span class="msel-caret">▾</span>';

  const pop = document.createElement('div');
  pop.className = 'msel-pop';
  pop.setAttribute('role', 'listbox');
  pop.hidden = true;

  sel.parentNode.insertBefore(wrap, sel);
  wrap.appendChild(box);
  wrap.appendChild(pop);
  wrap.appendChild(sel);
  sel.classList.add('msel-native');

  const valEl = box.querySelector('.msel-val');

  function render() {
    const cur = sel.value;
    valEl.textContent = sel.selectedIndex >= 0 ? sel.options[sel.selectedIndex].text : '';
    let html = '';
    for (const op of sel.options) {
      const on = op.value === cur;
      html += `<div class="msel-opt${on ? ' on' : ''}" data-v="${esc(op.value)}" tabindex="0">`
        + `<input type="radio" tabindex="-1" ${on ? 'checked' : ''}/>`
        + `<span>${esc(op.text)}</span></div>`;
    }
    pop.innerHTML = sel.options.length ? html : '<div class="msel-none">无可选项</div>';
    // 禁用态跟随原生控件；dimmed 由 nav.js 单独维护（环境加载失败时用），两者互不干涉
    wrap.classList.toggle('msel-disabled', !!sel.disabled);
    box.tabIndex = sel.disabled ? -1 : 0;
    if (sel.disabled && !pop.hidden) close();
  }

  function close() {
    pop.hidden = true;
    pop.classList.remove('up');
    wrap.classList.remove('open');
    box.setAttribute('aria-expanded', 'false');
  }

  function open() {
    if (sel.disabled) return;
    closeAll(api);
    render();
    pop.hidden = false;
    wrap.classList.add('open');
    box.setAttribute('aria-expanded', 'true');
    // 底部空间不够就向上弹（通知页的「预设模板」贴着卡片底部）
    const r = box.getBoundingClientRect();
    const need = Math.min(pop.scrollHeight || 240, 238) + 8;
    if (r.bottom + need > window.innerHeight && r.top - need > 0) pop.classList.add('up');
  }

  function toggle() { pop.hidden ? open() : close(); }

  function pick(v) {
    if (sel.value !== v) {
      sel.value = v;
      sel.dispatchEvent(new Event('change', { bubbles: true }));
    }
    render();
    close();
  }

  box.addEventListener('click', e => { e.stopPropagation(); toggle(); });
  box.addEventListener('keydown', e => {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(); }
    else if (e.key === 'Escape') close();
  });
  // 面板内的点击一律 stopPropagation：面板会就地重绘，被点的节点随即脱离 DOM，
  // 放任冒泡到 document 会让「点外收起」的判定认不出它来自面板，从而误收起。
  pop.addEventListener('click', e => {
    e.stopPropagation();
    const row = e.target.closest('.msel-opt');
    if (row) pick(row.dataset.v);
  });
  pop.addEventListener('keydown', e => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    const row = e.target.closest('.msel-opt');
    if (row) { e.preventDefault(); pick(row.dataset.v); }
  });

  // 原生控件被外部改写（innerHTML 换选项、value 变、disabled 变）时自动跟随
  sel.addEventListener('change', render);
  new MutationObserver(render).observe(sel, {
    childList: true, subtree: true, attributes: true, attributeFilter: ['disabled']
  });

  const api = { el: sel, wrap, box, pop, render, open, close };
  singles.push(api);
  render();
  return api;
}

// 全站初始化：把需要「点开弹选项」的单选下拉统一换成 msel 外观
export function initSelectSkins() {
  enhanceSelect('optEnv', { label: '环境', wrapId: 'wrapEnv' });
  enhanceSelect('notifyGroup', {});
  enhanceSelect('notifyTpl', {});
  enhanceSelect('caDifficulty', {});

  document.addEventListener('click', e => {
    if (!e.target.closest || !e.target.closest('.msel')) closeAll(null);
  });
  document.addEventListener('keydown', e => { if (e.key === 'Escape') closeAll(null); });
}

// 代码里直接给原生 select 赋 .value 不会触发 change，皮肤不会自己刷新；
// 赋完值后调一次本函数，让胶囊上的当前值与选中项跟上。找不到对应实例就静默返回。
export function syncSelectSkin(elOrId) {
  const el = typeof elOrId === 'string' ? $(elOrId) : elOrId;
  const api = el && singles.find(s => s.el === el);
  if (api) api.render();
}
