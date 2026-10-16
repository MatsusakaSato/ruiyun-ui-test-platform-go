/* ================= 测试用例页（预设库 + 队列编辑 + 附件管理） ================= */

import { $, esc, fmtSize, fileToBase64, fmtInvalidAttachments, IS_WIN, MOD_KEY, doOpenFile, copyText,
         MAX_TURNS, toTurns, firstTurn, turnCount, turnsBadge } from './utils.js';
import { state } from './state.js';
import { renderRunCases, doRun } from './page-home.js';
import { syncSelectSkin } from './msel-select.js';

/* ---------- 附件上传与附件库 ---------- */
export async function loadUploads() {
  try {
    state.UPLOADS = ((await (await fetch('/api/uploads')).json()).files) || [];
  } catch (e) {
    state.UPLOADS = [];
  }
}

export function pickAttach(i) {
  const el = $('attFile' + i);
  if (el) el.click();
}

export async function uploadFiles(files, i) {
  const arr = [...files];
  if (!arr.length) return;
  if (!state.CASES[i].attachments) state.CASES[i].attachments = [];
  const fails = [];
  for (const f of arr) {
    try {
      const b64 = await fileToBase64(f);
      const r = await fetch('/api/upload', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: f.name, data_base64: b64 })
      });
      const j = await r.json();
      if (j.ok) state.CASES[i].attachments.push({ name: j.name, path: j.path, size: j.size });
      else fails.push(`${f.name}：${j.message || '上传失败'}`);
    } catch (e) {
      fails.push(`${f.name}：${e.message}`);
    }
  }
  await loadUploads();
  renderCases();
  if (fails.length) alert('部分附件上传失败：\n' + fails.join('\n'));
}

export function openLib(i) {
  state.LIB_FOR = i;
  loadUploads().then(() => {
    renderLib();
    $('libMask')?.classList.add('on');
  });
}

export function closeLib() {
  $('libMask')?.classList.remove('on');
  state.LIB_FOR = -1;
}

// 附件库弹窗当前服务的附件列表：数字 = 队列行序号，'caseadd' = 新增用例对话框。
// 两处的附件都存成 { name, path, size }，对话框那份是待保存的暂存区。
function attsFor(target) {
  if (target === 'caseadd') {
    if (!state.CA_ATTS) state.CA_ATTS = [];
    return state.CA_ATTS;
  }
  const c = state.CASES[target];
  if (!c) return null;
  if (!c.attachments) c.attachments = [];
  return c.attachments;
}

// 预设库里存的是绝对路径数组，队列里要的是 { name, path }，文件名取路径末段
function attsFromPaths(list) {
  return (list || []).map(p => {
    const s = String(p);
    return { name: s.split(/[\\/]/).pop() || s, path: s };
  });
}

export function renderLib() {
  const atts = attsFor(state.LIB_FOR);
  if (!atts) { closeLib(); return; }
  const isCa = state.LIB_FOR === 'caseadd';
  const used = new Set(atts.map(a => a.path));
  const sub = $('libSub');
  if (sub) sub.textContent = (isCa ? '新增用例' : `第 ${state.LIB_FOR + 1} 条用例`)
    + ` · 库中 ${state.UPLOADS.length} 个文件`;

  const list = $('libList');
  if (list) {
    list.innerHTML = state.UPLOADS.length
      ? state.UPLOADS.map((f, k) => {
          const on = used.has(f.path);
          return `<div class="lib-row${on ? ' used' : ''}" data-k="${k}" title="${esc(f.path)}">`
            + `<div class="lib-main"><span class="lib-name">${esc(f.name)}</span>`
            + `<span class="lib-path">${esc(f.path)}</span></div>`
            + `<div class="lib-meta"><span class="lib-size">${fmtSize(f.size)}</span>`
            + `<span class="lib-del" data-path="${esc(f.path)}" title="从附件库删除（不可撤销）">删除</span>`
            + `<span class="lib-add">${on ? '已引用' : '+ 添加'}</span></div></div>`;
        }).join('')
      : '<div class="modal-empty">附件库为空：先点「上传附件」传一个</div>';
  }
  const count = $('libCount');
  if (count) count.textContent = (isCa ? '已选' : '本用例已引用')
    + ` ${used.size} 个附件 · 点击行即加入`;
}

export function addFromLib(i, k) {
  const f = state.UPLOADS[k];
  const atts = attsFor(i);
  if (!f || !atts) return;
  if (atts.some(a => a.path === f.path)) return;
  if (i === 'caseadd' && atts.length >= state.CA_ATT_MAX) {
    caseAddHint(`附件最多 ${state.CA_ATT_MAX} 个`);
    return;
  }
  atts.push({ name: f.name, path: f.path, size: f.size });
  if (i === 'caseadd') renderCaAtts();
  else renderCases();
  renderLib();
}

export async function delUpload(p) {
  if (!p) return;
  if (!confirm('从附件库删除该文件？\n\n文件会从磁盘删除，无法恢复；\n引用了它的用例会同步去掉这个附件。')) return;
  try {
    const r = await fetch('/api/upload/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: p })
    });
    const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
    if (!j.ok) { alert('删除失败：' + (j.message || '未知错误')); return; }
    state.UPLOADS = state.UPLOADS.filter(f => f.path !== p);
    state.CASES.forEach(c => { c.attachments = (c.attachments || []).filter(a => a.path !== p); });
    state.CA_ATTS = (state.CA_ATTS || []).filter(a => a.path !== p);
    renderLib();
    renderCaAtts();
    renderCases();
  } catch (e) {
    alert('请求失败：' + e.message);
  }
}

export function rmAttach(i, j) {
  state.CASES[i].attachments.splice(j, 1);
  renderCases();
}

/* ---------- 队列状态与渲染 ---------- */
export function syncCaseState() {
  const filled = state.CASES.filter(c => turnCount(c.prompt) > 0).length;
  document.querySelectorAll('.btn-run').forEach(b => {
    b.disabled = state.RUNNING || filled === 0;
  });
  const tqc = $('tabQueueCount');
  if (tqc) tqc.textContent = filled ? `（${filled}）` : '';
  const qc = $('queueCount');
  if (qc) qc.textContent = filled ? `（${filled} 条）` : '';

  const hint = $('caseHint');
  if (hint) {
    if (filled === 0) {
      hint.className = 'hint warn';
      hint.textContent = state.CASES.length
        ? '队列里的用例还没填提问。填完一条后按回车，即可在它下面继续新增。'
        : '队列还是空的。到「预设用例库」勾选用例后点「加入本轮」，或点「+ 添加一行」新增。';
    } else {
      hint.className = 'hint';
      hint.textContent = `将按顺序执行 ${filled} 条用例，每条用例单独开一个会话。`;
    }
  }
  renderQueueHint();
  renderRunCases();
}

/* 队列快捷键提示：文案随平台走，Windows 上不出现 ⌘ */
function renderQueueHint() {
  const el = $('queueHint');
  if (!el) return;
  el.textContent = `快捷键：回车 新增用例 · ${MOD_KEY()} + 回车 启动测试`;
}

export function setCaseTab(which) {
  const lib = which !== 'queue';
  const pLib = $('paneLib');
  if (pLib) pLib.hidden = !lib;
  const pQueue = $('paneQueue');
  if (pQueue) pQueue.hidden = lib;
  $('ctabLib')?.classList.toggle('on', lib);
  $('ctabQueue')?.classList.toggle('on', !lib);
}

export function renderCases() {
  const box = $('caseRows');
  if (!box) return;
  box.innerHTML = state.CASES.map((c, i) => {
    const atts = c.attachments || [];
    const L = c.labels || {};
    const turns = toTurns(c.prompt);
    // 预设里带的自定义标签跟着用例一起进队列（只读展示：入库存改一律走库里的「编辑」）
    const chips = [
      L.scene ? `<span class="tag t-scene">${esc(L.scene)}</span>` : '',
      ...((L.tags || []).map(t => `<span class="tag t-tag">${esc(t)}</span>`)),
      ...atts.map((a, j) => `<span class="tag t-att" title="${esc(a.path)}">${esc(a.name)}`
        + `<b class="tag-x" onclick="rmAttach(${i},${j})" title="移除该附件">×</b></span>`),
    ].join('');
    return `
    <div class="case-row">
      <div class="no">${String(i + 1).padStart(2, '0')}</div>
      <div class="fields">
        <input class="prompt" placeholder="输入提示词，例如：请读取你的记忆文件并告诉我记录条数"
               value="${esc(turns[0] || '')}" oninput="setCaseTurn(${i},0,this.value)"
               onchange="setCaseTurn(${i},0,this.value)"/>
        <div class="cl-acts">
          ${turnsBadge(c.prompt)}
          <button class="cl-att" onclick="openTurns(${i})"
                  title="编辑多轮对话：一个用例内依次发送多轮提问">轮次${turns.length > 1 ? '(' + turns.length + ')' : ''}</button>
          <button class="cl-att" onclick="pickAttach(${i})" title="上传附件：运行时会被投递给被测应用">📎 附件</button>
          <button class="cl-att" onclick="openLib(${i})" title="打开附件库，点击即加入本用例">库${state.UPLOADS.length ? '(' + state.UPLOADS.length + ')' : ''}</button>
          <input type="file" multiple style="display:none" id="attFile${i}"
                 onchange="uploadFiles(this.files,${i});this.value=''"/>
        </div>
        ${chips ? `<div class="cl-tags">${chips}</div>` : ''}
      </div>
      <button class="del" title="删除该用例" onclick="delCase(${i})">×</button>
    </div>`;
  }).join('');
  syncCaseState();
}

/* 写回第 idx 轮提问（第 0 轮由队列行上的输入框直接编辑） */
export function setCaseTurn(i, idx, value) {
  const c = state.CASES[i];
  if (!c) return;
  const turns = toTurns(c.prompt);
  while (turns.length <= idx) turns.push('');
  turns[idx] = value;
  c.prompt = turns;
  syncCaseState();
}

/* 队列行的「轮次」按钮：打开多轮编辑弹窗 */
export function openTurns(i) {
  const c = state.CASES[i];
  if (!c) return;
  state.TURNS_ROW = i;
  state.TURNS_DRAFT = toTurns(c.prompt);
  if (state.TURNS_DRAFT.length === 0) state.TURNS_DRAFT = [''];
  renderTurnsModal();
}

/* 队列行的多轮编辑弹窗：与「新增/编辑用例」对话框里的轮次区同构 */
function renderTurnsModal() {
  const mask = $('turnsMask');
  if (!mask) return;
  const box = $('turnsRows');
  if (box) {
    box.innerHTML = state.TURNS_DRAFT.map((t, k) => `
      <div class="turn-row">
        <div class="turn-no">第 ${k + 1} 轮</div>
        <textarea class="turn-inp" rows="2" placeholder="第 ${k + 1} 轮的提问"
          oninput="setDraftTurn(${k},this.value)">${esc(t)}</textarea>
        <button class="turn-del" title="删除该轮" onclick="delDraftTurn(${k})"
          ${state.TURNS_DRAFT.length <= 1 ? 'disabled' : ''}>×</button>
      </div>`).join('');
  }
  const add = $('turnsAdd');
  if (add) add.disabled = state.TURNS_DRAFT.length >= MAX_TURNS;
  const hint = $('turnsHint');
  if (hint) hint.textContent = `最多 ${MAX_TURNS} 轮 · 当前 ${state.TURNS_DRAFT.length} 轮`;
  mask.hidden = false;
}

export function setDraftTurn(k, v) {
  state.TURNS_DRAFT[k] = v;
}

export function delDraftTurn(k) {
  if (state.TURNS_DRAFT.length <= 1) return;
  state.TURNS_DRAFT.splice(k, 1);
  renderTurnsModal();
}

export function addDraftTurn() {
  if (state.TURNS_DRAFT.length >= MAX_TURNS) return;
  state.TURNS_DRAFT.push('');
  renderTurnsModal();
  const rows = document.querySelectorAll('#turnsRows .turn-inp');
  rows[rows.length - 1]?.focus();
}

export function closeTurns() {
  const mask = $('turnsMask');
  if (mask) mask.hidden = true;
  state.TURNS_ROW = -1;
  state.TURNS_DRAFT = [];
}

export function saveTurns() {
  const i = state.TURNS_ROW;
  if (i < 0 || !state.CASES[i]) { closeTurns(); return; }
  const turns = state.TURNS_DRAFT.map(t => String(t || '').trim()).filter(t => t !== '');
  if (!turns.length) {
    const hint = $('turnsHint');
    if (hint) hint.textContent = '至少填写一轮提问';
    return;
  }
  state.CASES[i].prompt = turns;
  closeTurns();
  renderCases();
}

/* at 省略时追加到队尾；传入序号则插到该位置之前（回车新增用 at = 当前行 + 1） */
export function addCase(at) {
  const pos = Number.isInteger(at)
    ? Math.min(Math.max(at, 0), state.CASES.length)
    : state.CASES.length;
  state.CASES.splice(pos, 0, { prompt: [''], attachments: [] });
  renderCases();
  focusCaseRow(pos);
}

/* 聚焦第 i 行的提问输入框（renderCases 重建 DOM 后需要手动聚焦） */
function focusCaseRow(i) {
  const row = document.querySelectorAll('#caseRows .case-row')[i];
  const input = row && row.querySelector('input.prompt');
  if (!input) return;
  input.focus();
  try { row.scrollIntoView({ behavior: 'smooth', block: 'nearest' }); } catch (e) {}
}

export function delCase(i) {
  state.CASES.splice(i, 1);
  closeLib();
  renderCases();
}

/* ---------- 预设用例库 ---------- */
export const libSelTags = c => {
  const L = c.labels || {};
  let out = '';
  if (L.scene) out += `<span class="tag t-scene">${esc(L.scene)}</span>`;
  if (L.difficulty) out += `<span class="tag t-diff">${esc(L.difficulty)}</span>`;
  // 自定义标签：数量不限，原样按用户填写的顺序展示
  for (const t of (L.tags || [])) out += `<span class="tag t-tag">${esc(t)}</span>`;
  if (L.attachment === true) {
    // 附件绝对路径塞在 data-atts 里（一行一个），hover 时由 showAttTip 渲染气泡：
    // 文件名本身是链接（点开＝用系统默认程序打开该文件），右侧图标点击复制路径。
    // （Excel 导入的老数据可能只标记了「要附件」却没记路径，那时 data-atts 为空）
    const paths = (c.attachments || []).map(p => String(p));
    out += `<span class="tag tag-att" data-atts="${esc(paths.join('\n'))}">附件</span>`;
  }
  return out;
};

/* 附件标签的悬停气泡：毛玻璃质感，一个附件一行 ——
   文件名本身是链接（点击 → POST /api/open 由服务端调系统默认程序打开），
   右侧小图标点击复制该附件的绝对路径。
   预设库表格是 .lib-table（overflow:auto）的滚动容器，纯 CSS 的 ::after 气泡会被容器裁掉，
   所以气泡挂在 body 上用 fixed 定位，跟着标签的 rect 走。
   气泡要能点，就不能再 pointer-events:none，于是鼠标移开后**延迟**收起，
   留出从标签挪到气泡上的时间（气泡自身的 mouseenter 会撤销这次收起）。 */
let attTipTimer = 0;

function removeAttTip() {
  clearTimeout(attTipTimer);
  document.getElementById('attTip')?.remove();
}

function hideAttTip() {
  removeAttTip();
}

function scheduleHideAttTip() {
  clearTimeout(attTipTimer);
  attTipTimer = setTimeout(removeAttTip, 240);
}

function showAttTip(anchor) {
  removeAttTip();
  const paths = (anchor.dataset.atts || '').split('\n').filter(Boolean);
  const el = document.createElement('div');
  el.id = 'attTip';
  el.className = 'att-tip' + (paths.length ? '' : ' muted');
  if (paths.length) {
    el.innerHTML = paths.map(p => {
      const name = p.split(/[\\/]/).pop() || p;
      return '<div class="att-row">'
        + `<a class="att-name" href="#" data-path="${esc(p)}" title="打开：${esc(p)}">${esc(name)}</a>`
        + `<b class="att-copy" data-path="${esc(p)}" title="复制路径">📋</b></div>`;
    }).join('');
  } else {
    el.textContent = '未记录文件名';
  }
  el.addEventListener('mouseenter', () => clearTimeout(attTipTimer));
  el.addEventListener('mouseleave', scheduleHideAttTip);
  el.addEventListener('click', onAttTipClick);
  document.body.appendChild(el);

  const r = anchor.getBoundingClientRect();
  const w = el.offsetWidth;
  const h = el.offsetHeight;
  // 默认贴标签下方（留 4px 缝，鼠标能顺畅挪进气泡）；下方放不下就翻到上方，两侧再夹一下别溢出视口
  const below = r.bottom + 4;
  const top = below + h <= window.innerHeight - 6 ? below : r.top - 4 - h;
  el.style.top = Math.round(Math.max(6, Math.min(top, window.innerHeight - h - 6))) + 'px';
  el.style.left = Math.round(Math.max(6, Math.min(r.left, window.innerWidth - w - 8))) + 'px';
}

// 气泡里的点击：文件名 → 打开文件；图标 → 复制路径（图标短暂变成 ✓）
function onAttTipClick(e) {
  const copyBtn = e.target.closest ? e.target.closest('.att-copy') : null;
  if (copyBtn) {
    e.preventDefault();
    const p = copyBtn.dataset.path;
    if (!p) return;
    copyText(p, () => {
      copyBtn.textContent = '✓';
      copyBtn.classList.add('done');
      setTimeout(() => {
        copyBtn.textContent = '📋';
        copyBtn.classList.remove('done');
      }, 1200);
    });
    return;
  }
  const link = e.target.closest ? e.target.closest('.att-name') : null;
  if (!link) return;
  e.preventDefault();
  if (link.dataset.path) doOpenFile(link.dataset.path);
}

/* ---------- 筛选控件：多选下拉 + 附件复选框 ----------
   三个多选下拉（场景 / 难度 / 标签）共用一份渲染与交互逻辑。渲染时只替换「已选 tag 条」与
   「选项列表」两块内部内容，不重建 .msel 外壳 —— 面板的展开状态（pop 的 hidden）不受列表刷新影响，
   所以勾选一项触发查询后，面板仍是打开的，可以继续勾下一项。
   可选值来自服务端返回的 state.LIB_SCENES / state.LIB_DIFFICULTIES / state.LIB_TAGS（库中已有的去重标签）。
   **多选语义两套**（后端 QueryPresetCases 决定）：场景 / 难度是命中任意一个即可（「或」），
   标签要求全部命中（「且」）—— 多个标签是用来「同时满足」地收窄候选的。 */
const MSEL = {
  scene: { wrap: 'mselScene', box: 'mselSceneBox', tags: 'mselSceneTags', pop: 'mselScenePop', list: 'LIB_SCENES', picked: 'scenes' },
  diff: { wrap: 'mselDiff', box: 'mselDiffBox', tags: 'mselDiffTags', pop: 'mselDiffPop', list: 'LIB_DIFFICULTIES', picked: 'difficulties' },
  tag: { wrap: 'mselTags', box: 'mselTagsBox', tags: 'mselTagsTags', pop: 'mselTagsPop', list: 'LIB_TAGS', picked: 'tags' }
};

// 附件筛选三态循环：不限（''）→ 需要附件（yes）→ 不需要附件（no）
// 底层值与原「需附件 / 不需附件」两个 chip 完全一致，'' 就是原来的「不筛此项」。
// 文案自带「附件」二字（界面上不再有独立的「附件」标签）。
const ATT_NEXT = { '': 'yes', yes: 'no', no: '' };
const ATT_TEXT = { '': '不限附件', yes: '需要附件', no: '不需要附件' };

export function renderLibFilters() {
  for (const key of Object.keys(MSEL)) renderMsel(key);
  renderAttFilter();
}

function renderMsel(key) {
  const m = MSEL[key];
  const picked = state.LIB[m.picked];
  const tagsEl = $(m.tags);
  if (tagsEl) {
    // 未选时不占位：字段名（场景 / 难度）在 .msel-box 里常驻，本身即提示
    tagsEl.innerHTML = picked.length
      ? picked.map(v => `<span class="msel-tag">${esc(v)}`
          + `<b class="msel-x" data-v="${esc(v)}" title="移除">×</b></span>`).join('')
      : '';
  }
  const popEl = $(m.pop);
  if (popEl) {
    const opts = state[m.list] || [];
    popEl.innerHTML = opts.length
      ? opts.map(v => `<div class="msel-opt${picked.includes(v) ? ' on' : ''}" data-v="${esc(v)}" tabindex="0">`
          + `<input type="checkbox" tabindex="-1" ${picked.includes(v) ? 'checked' : ''}/>`
          + `<span>${esc(v)}</span></div>`).join('')
      : '<div class="msel-none">无可选项</div>';
  }
}

function renderAttFilter() {
  const cur = ATT_TEXT[state.LIB.attachment] ? state.LIB.attachment : '';
  const txt = $('libAttText');
  if (txt) txt.textContent = ATT_TEXT[cur];
  const box = $('libAtt');
  if (box) {
    box.checked = cur === 'yes';
    box.indeterminate = cur === '';
  }
}

function closeMsels() {
  for (const key of Object.keys(MSEL)) {
    const m = MSEL[key];
    const pop = $(m.pop);
    if (pop) pop.hidden = true;
    $(m.wrap)?.classList.remove('open');
    $(m.box)?.setAttribute('aria-expanded', 'false');
  }
}

function toggleMsel(key) {
  const m = MSEL[key];
  const pop = $(m.pop);
  if (!pop) return;
  const open = pop.hidden;
  closeMsels();
  if (open) {
    pop.hidden = false;
    $(m.wrap)?.classList.add('open');
    $(m.box)?.setAttribute('aria-expanded', 'true');
  }
}

// 勾选 / 取消勾选一个可选值：改状态 → 只重绘这一个控件 → 重新查询（页码归零、清空所选）
function pickMsel(key, v) {
  const m = MSEL[key];
  const cur = state.LIB[m.picked];
  state.LIB[m.picked] = cur.includes(v) ? cur.filter(x => x !== v) : cur.concat(v);
  renderMsel(key);
  libQuery(true);
}

export function renderLibTable() {
  hideAttTip();   // 整块重渲染会把标签换掉，气泡留着就成孤儿了
  const rows = state.LIB_ROWS;
  const tbl = $('libTable');
  if (tbl) {
    tbl.innerHTML = '<table><thead><tr>'
      + '<th class="th-chk" style="width:38px" title="全选 / 取消全选"><input type="checkbox" id="libAllBox" tabindex="-1"/></th>'
      + '<th style="width:104px">编号</th><th>提问</th><th style="width:210px">标签</th>'
      + '<th style="width:104px"></th></tr></thead><tbody>'
      + (rows.length ? rows.map(c => {
        const t1 = firstTurn(c.prompt);
        const n = turnCount(c.prompt);
        const titleText = toTurns(c.prompt).join('  ⏎  ');
        return `<tr data-id="${esc(c.id || '')}">
          <td class="td-chk"><input type="checkbox" ${state.PSEL.has(c.id) ? 'checked' : ''} tabindex="-1"/></td>
          <td class="mono">${esc(c.id || '')}</td>
          <td class="lp" title="${esc(titleText)}">${turnsBadge(c.prompt)}${esc(t1)}${n > 1 ? `<span class="lp-more">等 ${n} 轮</span>` : ''}</td>
          <td>${libSelTags(c)}</td>
          <td class="td-acts"><button class="home-link lib-edit" data-id="${esc(c.id || '')}">编辑</button>
            <button class="home-link lib-del" data-id="${esc(c.id || '')}">删除</button></td>
        </tr>`;
      }).join('')
        : '<tr><td colspan="5" class="tbl-empty">没有符合条件的用例</td></tr>')
      + '</tbody></table>';
  }

  const from = state.LIB_TOTAL ? state.LIB.offset + 1 : 0;
  const to = Math.min(state.LIB.offset + rows.length, state.LIB_TOTAL);
  const pageEl = $('libPage');
  if (pageEl) pageEl.textContent = `第 ${from}–${to} 条 / 共 ${state.LIB_TOTAL} 条`;

  const prev = $('libPrev');
  if (prev) prev.disabled = state.LIB.offset <= 0;
  const next = $('libNext');
  if (next) next.disabled = state.LIB.offset + state.LIB_LIMIT >= state.LIB_TOTAL;

  syncLibSel();
}

export function syncLibSel() {
  const countEl = $('libSelCount');
  if (countEl) countEl.innerHTML = `已选 <b>${state.PSEL.size}</b> 条`;
  const selEl = $('libSel');
  if (selEl && !countEl && selEl.firstChild) selEl.firstChild.textContent = `已选 ${state.PSEL.size} 条`;

  const toQueue = $('libToQueue');
  if (toQueue) toQueue.disabled = state.PSEL.size === 0;
  const del = $('libDel');
  if (del) del.disabled = state.PSEL.size === 0;
  const btnNone = $('libNone');
  if (btnNone) btnNone.disabled = state.PSEL.size === 0;

  const box = $('libAllBox');
  if (box) {
    const total = state.LIB_ROWS.length;
    const selected = total ? state.LIB_ROWS.filter(c => state.PSEL.has(c.id)).length : 0;
    box.checked = total > 0 && selected === total;
    box.indeterminate = selected > 0 && selected < total;
  }
}

export async function loadPresetLib() {
  // 筛选条件与后端 GET /api/preset-cases 的参数一一对应：
  //   scene / difficulties / tags 多个值用英文逗号拼接（单个值时与改造前的格式完全一致），空串表示不限
  //   attachment 取 '' / 'yes' / 'no'
  const q = new URLSearchParams({
    limit: state.LIB_LIMIT,
    offset: state.LIB.offset,
    keyword: state.LIB.keyword,
    scene: state.LIB.scenes.join(','),
    difficulties: state.LIB.difficulties.join(','),
    tags: state.LIB.tags.join(','),
    attachment: state.LIB.attachment
  });
  try {
    const j = await (await fetch('/api/preset-cases?' + q)).json();
    state.LIB_ROWS = j.cases || [];
    state.LIB_TOTAL = j.total || 0;
    state.LIB_SCENES = j.scenes || [];
    state.LIB_DIFFICULTIES = j.difficulties || [];
    state.LIB_TAGS = j.tags || [];
    state.PSEL = new Set([...state.PSEL].filter(id => state.LIB_ROWS.some(c => c.id === id)));

    const caScenesEl = $('caScenes');
    if (caScenesEl) {
      caScenesEl.innerHTML = state.LIB_SCENES.map(s => `<option value="${esc(s)}"/>`).join('');
    }
    // 对话框标签输入的联想项：库里已用过的标签（不限制自由输入，只是方便复用）
    const caTagOpts = $('caTagOptions');
    if (caTagOpts) {
      caTagOpts.innerHTML = state.LIB_TAGS.map(t => `<option value="${esc(t)}"/>`).join('');
    }
    renderLibFilters();
    renderLibTable();
  } catch (e) {
    const tbl = $('libTable');
    if (tbl) tbl.innerHTML = `<div class="tbl-empty">读取失败：${esc(e.message)}</div>`;
  }
}

export function libQuery(resetPage) {
  if (resetPage) { state.LIB.offset = 0; state.PSEL = new Set(); }
  loadPresetLib();
}

export async function delPresetOne(id) {
  if (!id) return;
  if (!confirm(`删除预设用例 ${id}？\n\n该操作无法撤销，确定删除？`)) return;
  try {
    const r = await fetch('/api/preset-cases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'delete', ids: [id] })
    });
    const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
    if (!j.ok) { alert('删除失败：' + (j.message || '未知错误')); return; }
    state.PSEL.delete(id);
    if (state.LIB_ROWS.length === 1 && state.LIB.offset > 0) state.LIB.offset -= state.LIB_LIMIT;
    await loadPresetLib();
  } catch (e) {
    alert('请求失败：' + e.message);
  }
}

/* ---------- 新增用例对话框 ---------- */

/* ---- 附件：上传到本机附件库，入库存的是落盘后的绝对路径 ---- */

export function pickCaAttach() {
  const el = $('caAttFile');
  if (el) el.click();
}

export function renderCaAtts() {
  const box = $('caAtts');
  if (!box) return;
  box.innerHTML = (state.CA_ATTS || []).map((a, j) =>
    `<span class="tag t-att" title="${esc(a.path)}">${esc(a.name)}`
    + `<b class="tag-x" onclick="rmCaAttach(${j})" title="移除该附件">×</b></span>`).join('');
}

export function rmCaAttach(j) {
  (state.CA_ATTS || []).splice(j, 1);
  renderCaAtts();
}

/* ---- 自定义标签：数量与内容都不限（逗号除外：逗号既是输入确认键，也是筛选参数的传输分隔符） ---- */

export function renderCaTags() {
  const box = $('caTagChips');
  if (!box) return;
  box.innerHTML = (state.CA_TAGS || []).map((t, j) =>
    `<span class="tag t-tag">${esc(t)}`
    + `<b class="tag-x" data-j="${j}" title="移除该标签">×</b></span>`).join('');
}

// 加一个标签：去空白、空串丢弃、重复的直接跳过（与后端 cleanTags 同一套口径）
export function addCaTag(raw) {
  const t = String(raw || '').trim();
  if (!t) return;
  if (!state.CA_TAGS) state.CA_TAGS = [];
  if (state.CA_TAGS.includes(t)) return;
  state.CA_TAGS.push(t);
  renderCaTags();
}

export function rmCaTag(j) {
  (state.CA_TAGS || []).splice(j, 1);
  renderCaTags();
}

// 把输入框里还没确认的内容变成一个标签：回车、逗号、失焦都会调它。
// 粘进来一串「a,b,c」时按分隔符拆成多个，免得整串变成一个标签。
export function commitCaTagInput() {
  const el = $('caTagInput');
  if (!el) return;
  for (const p of String(el.value || '').split(/[,，、]/)) addCaTag(p);
  el.value = '';
}

// 逐个上传到本机附件库；失败的文件只提示、不回滚已成功的，超出上限的直接忽略
export async function uploadCaFiles(files) {
  const arr = [...files];
  if (!arr.length) return;
  const atts = attsFor('caseadd');
  const take = arr.slice(0, Math.max(state.CA_ATT_MAX - atts.length, 0));
  const fails = [];
  for (const f of take) {
    try {
      const b64 = await fileToBase64(f);
      const r = await fetch('/api/upload', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: f.name, data_base64: b64 })
      });
      const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
      if (j.ok) atts.push({ name: j.name, path: j.path, size: j.size });
      else fails.push(`${f.name}：${j.message || '上传失败'}`);
    } catch (e) {
      fails.push(`${f.name}：${e.message}`);
    }
  }
  const overflow = arr.length - take.length;
  await loadUploads();
  renderCaAtts();
  renderCases();
  const msgs = [];
  if (fails.length) msgs.push(`上传失败 —— ${fails.join('；')}`);
  if (overflow > 0) msgs.push(`最多 ${state.CA_ATT_MAX} 个附件，已忽略 ${overflow} 个`);
  caseAddHint(msgs.join(' · '));
}

/* ---------- 对话框里的「多轮提问」编辑区 ----------
   第 1 轮就是原有的 #caPrompt 输入框（保持不变），这里只负责第 2 轮及以后。
   轮次上限 MAX_TURNS 与后端、Excel 的 prompt_1..prompt_8 一致。 */

export function renderCaTurns() {
  const box = $('caTurns');
  if (!box) return;
  const extra = (state.CA_TURNS || []).slice(1);
  box.innerHTML = extra.map((t, k) => {
    const n = k + 2;   // 第 1 轮在 #caPrompt
    return `
    <div class="turn-row">
      <div class="turn-no">第 ${n} 轮</div>
      <textarea class="turn-inp" rows="2" placeholder="第 ${n} 轮的提问"
        oninput="setCaTurn(${k + 1},this.value)">${esc(t)}</textarea>
      <button class="turn-del" title="删除该轮" onclick="delCaTurn(${k + 1})">×</button>
    </div>`;
  }).join('');

  const total = 1 + extra.length;
  const add = $('caTurnAdd');
  if (add) add.disabled = total >= MAX_TURNS;
  const hint = $('caTurnHint');
  if (hint) hint.textContent = total > 1 ? `多轮用例 · 共 ${total} 轮` : '';
}

export function setCaTurn(idx, v) {
  if (!state.CA_TURNS) state.CA_TURNS = [''];
  while (state.CA_TURNS.length <= idx) state.CA_TURNS.push('');
  state.CA_TURNS[idx] = v;
}

export function addCaTurn() {
  if (!state.CA_TURNS) state.CA_TURNS = [''];
  if (state.CA_TURNS.length >= MAX_TURNS) return;
  state.CA_TURNS.push('');
  renderCaTurns();
  const rows = document.querySelectorAll('#caTurns .turn-inp');
  rows[rows.length - 1]?.focus();
}

export function delCaTurn(idx) {
  if (!state.CA_TURNS) return;
  // 只允许删第 2 轮及以后（第 1 轮是必填，走 #caPrompt）
  if (idx < 1 || idx >= state.CA_TURNS.length) return;
  state.CA_TURNS.splice(idx, 1);
  renderCaTurns();
}

export function openCaseAdd() {
  const mask = $('caseAddMask');
  if (!mask) return;
  if ($('caseAddTitle')) $('caseAddTitle').textContent = '新增用例';
  state.CA_EDIT_ID = '';
  if ($('caScene')) $('caScene').value = '';
  // 每次打开都回到「不设置」：原生 select 赋 value 不触发 change，皮肤要靠 syncSelectSkin 跟上
  if ($('caDifficulty')) $('caDifficulty').value = '';
  syncSelectSkin('caDifficulty');
  if ($('caPrompt')) $('caPrompt').value = '';
  state.CA_ATTS = [];
  state.CA_TAGS = [];
  state.CA_TURNS = [''];
  renderCaTurns();
  renderCaAtts();
  renderCaTags();
  const caFile = $('caAttFile');
  if (caFile) caFile.value = '';
  const caTagInput = $('caTagInput');
  if (caTagInput) caTagInput.value = '';
  clearCaseAddHint();
  mask.classList.add('on');
  $('caPrompt')?.focus();
}

/* 打开「编辑用例」对话框：用库里的那一行回填全部字段。
   数据取自当前列表行（点编辑的按钮就在这一行里），取不到就提示并返回。 */
export function openCaseEdit(id) {
  const mask = $('caseAddMask');
  if (!mask) return;
  const c = (state.LIB_ROWS || []).find(x => x.id === id);
  if (!c) {
    const m = $('libMsg');
    if (m) m.textContent = `找不到用例 ${id}，刷新后再试`;
    return;
  }
  const L = c.labels || {};
  state.CA_EDIT_ID = id;
  if ($('caseAddTitle')) $('caseAddTitle').textContent = '编辑用例';
  if ($('caScene')) $('caScene').value = L.scene || '';
  if ($('caDifficulty')) $('caDifficulty').value = L.difficulty || '';
  syncSelectSkin('caDifficulty');
  if ($('caPrompt')) $('caPrompt').value = firstTurn(c.prompt);
  state.CA_TURNS = toTurns(c.prompt);
  renderCaTurns();
  state.CA_ATTS = attsFromPaths(c.attachments);
  state.CA_TAGS = (L.tags || []).map(String);
  renderCaAtts();
  renderCaTags();
  const caFile = $('caAttFile');
  if (caFile) caFile.value = '';
  const caTagInput = $('caTagInput');
  if (caTagInput) caTagInput.value = '';
  clearCaseAddHint();
  mask.classList.add('on');
  $('caPrompt')?.focus();
}

export function closeCaseAdd() {
  $('caseAddMask')?.classList.remove('on');
  clearCaseAddHint();
}

// 对话框底部的提示位：一条短句，空串即隐藏
function caseAddHint(text) {
  const el = $('caHint');
  if (!el) return;
  el.textContent = text || '';
  el.hidden = !text;
}

function clearCaseAddHint() {
  caseAddHint('');
  $('caPrompt')?.closest('.fld')?.classList.remove('err');
}

async function submitCaseAdd() {
  const promptEl = $('caPrompt');
  const prompt = (promptEl?.value || '').trim();
  if (!prompt) {
    caseAddHint('第 1 轮提问不能为空');
    promptEl?.closest('.fld')?.classList.add('err');
    promptEl?.focus();
    return;
  }
  promptEl.closest('.fld')?.classList.remove('err');

  // 组装轮次：第 1 轮取输入框，其余轮取「轮次编辑区」的草稿。
  // 空行直接丢掉（加了行又没填是常见路径）。
  const extra = (state.CA_TURNS || []).slice(1)
    .map(t => String(t || '').trim())
    .filter(t => t !== '');
  const turns = [prompt, ...extra];
  if (turns.length > MAX_TURNS) {
    caseAddHint(`最多 ${MAX_TURNS} 轮，当前填了 ${turns.length} 轮`);
    return;
  }

  const scene = ($('caScene')?.value || '').trim();
  const difficulty = ($('caDifficulty')?.value || '').trim();
  // 输入框里还没确认的那段也当作标签（用户直接点保存、没敲回车的常见路径）
  commitCaTagInput();
  const tags = (state.CA_TAGS || []).slice();
  // 附件以本机绝对路径数组入库；服务端还会逐个 os.Stat，无效的剔除后回传 invalid_attachments
  const attachments = (state.CA_ATTS || []).map(a => a.path);
  // 「需要附件」不再手填：挂了附件就是需要，没挂就是不需要
  const attachment = attachments.length > 0;
  const editID = state.CA_EDIT_ID || '';

  const okBtn = $('caseAddOk');
  if (okBtn) okBtn.disabled = true;
  caseAddHint('保存中…');
  try {
    const r = await fetch('/api/preset-cases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(editID
        ? { action: 'update', id: editID, prompt: turns, labels: { scene, difficulty, tags, attachment }, attachments }
        : { action: 'add', prompt: turns, labels: { scene, difficulty, tags, attachment }, attachments })
    });
    const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
    if (!j.ok) {
      // 服务端校验（如重复用例）留在对话框里提示，不关窗、不清空输入
      caseAddHint(j.message || '保存失败');
      return;
    }
    const invalid = j.invalid_attachments || [];
    const savedID = (j.case && j.case.id) || editID;
    const wasEdit = !!editID;
    closeCaseAdd();

    if (!wasEdit) {
      // 新增：重置筛选条件与搜索关键字，确保新用例直接在第一行立即可见，不被既有过滤条件隐藏
      state.LIB.keyword = '';
      state.LIB.scenes = [];
      state.LIB.difficulties = [];
      state.LIB.tags = [];
      state.LIB.attachment = '';
      state.LIB.offset = 0;
      const kwEl = $('libKw');
      if (kwEl) kwEl.value = '';
    }
    // 编辑不动用户的筛选条件：改完可能就落在当前筛选之外，那就只在提示位说明已保存

    await loadPresetLib();
    const visible = !!document.querySelector(`tr[data-id="${savedID}"]`);
    flashAddedCase(savedID);
    const m = $('libMsg');
    if (invalid.length) {
      // 用例本身已保存成功，只是个别附件被服务端剔除；务必说清，别让用户误以为整条失败
      if (m) m.textContent = `用例已保存，已忽略 ${invalid.length} 个无效附件`;
      alert(`用例已保存，但有 ${invalid.length} 个附件无效已被忽略：\n\n`
        + fmtInvalidAttachments(invalid)
        + '\n\n请修正后重新添加相关附件。');
    } else if (wasEdit || !visible) {
      if (m) m.textContent = j.message || (savedID ? `已保存 ${savedID}` : '已保存');
    }
  } catch (e) {
    caseAddHint('请求失败：' + e.message);
  } finally {
    if (okBtn) okBtn.disabled = false;
  }
}

// 新增成功后高亮并滚动到这一行
function flashAddedCase(id) {
  if (!id) return;
  const row = document.querySelector(`tr[data-id="${id}"]`);
  if (!row) return;
  row.classList.add('just-added');
  row.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  setTimeout(() => row.classList.remove('just-added'), 3000);
}

/* ---------- 注册事件 ---------- */
export function initCases() {
  const libList = $('libList');
  if (libList) {
    libList.onclick = e => {
      const del = e.target.closest('.lib-del');
      if (del) { delUpload(del.dataset.path); return; }
      const row = e.target.closest('.lib-row');
      if (!row || row.classList.contains('used')) return;
      addFromLib(state.LIB_FOR, +row.dataset.k);
    };
  }

  const libClose = $('libClose');
  if (libClose) libClose.onclick = closeLib;
  const libDone = $('libDone');
  if (libDone) libDone.onclick = closeLib;
  const libMask = $('libMask');
  if (libMask) libMask.onclick = e => { if (e.target === e.currentTarget) closeLib(); };

  const btnAdd = $('btnAddQueueRow');
  if (btnAdd) btnAdd.onclick = () => addCase();

  /* ---- 「本轮要跑的」队列快捷键 ----
     回车：在当前用例下方新增一条空用例（末行且还是空的时候不再追加，免得连出一串空行）
     Ctrl + 回车：启动本轮测试（macOS 上 ⌘ + 回车 同效；Windows 的 Win 键不算） */
  const paneQueue = $('paneQueue');
  if (paneQueue) {
    paneQueue.addEventListener('keydown', e => {
      if (e.key !== 'Enter' || e.isComposing) return;

      if (IS_WIN ? e.ctrlKey : (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        doRun();
        return;
      }
      // ⌘（macOS）/ Win（Windows）单独按下时不当普通回车用
      if (e.shiftKey || e.altKey || e.metaKey) return;

      // 只认用例行里的提问输入框；按钮上的回车交给按钮自己处理
      const input = e.target.closest ? e.target.closest('input.prompt') : null;
      if (!input) return;
      const rows = [...document.querySelectorAll('#caseRows .case-row')];
      const i = rows.indexOf(input.closest('.case-row'));
      if (i < 0) return;

      e.preventDefault();
      const cur = state.CASES[i];
      if (i === state.CASES.length - 1 && turnCount(cur && cur.prompt) === 0) return;
      addCase(i + 1);
    });
  }

  const btnClear = $('btnClearCases');
  if (btnClear) {
    btnClear.onclick = () => {
      if (state.CASES.filter(c => turnCount(c.prompt) > 0).length
        && !confirm('清空本轮队列？（只清队列，预设用例库不受影响）')) return;
      state.CASES = [];
      closeLib();
      renderCases();
    };
  }

  const libKw = $('libKw');
  if (libKw) {
    libKw.addEventListener('input', () => {
      clearTimeout(state.libKwTimer);
      state.libKwTimer = setTimeout(() => {
        state.LIB.keyword = libKw.value.trim();
        libQuery(true);
      }, 300);
    });
    libKw.addEventListener('keydown', e => {
      if (e.key === 'Enter') {
        e.preventDefault();
        clearTimeout(state.libKwTimer);
        state.LIB.keyword = libKw.value.trim();
        libQuery(true);
      }
    });
  }

  /* ---- 筛选：场景 / 难度（多选下拉）----
     点触发器开合面板；点选项勾选/取消；点已选项上的 × 删除该值；Esc 或点面板外收起。
     面板内的点击一律 stopPropagation：勾选会就地重绘选项/标签，被点的节点随即脱离 DOM，
     放任事件冒泡到 document 会让「点外收起」的判定认不出它来自面板，从而误收起面板。 */
  for (const key of Object.keys(MSEL)) {
    const m = MSEL[key];
    const box = $(m.box);
    if (box) {
      box.onclick = e => {
        e.stopPropagation();
        const x = e.target.closest('.msel-x');
        if (x) { pickMsel(key, x.dataset.v); return; }
        toggleMsel(key);
      };
      box.onkeydown = e => {
        if (e.key !== 'Enter' && e.key !== ' ') return;
        e.preventDefault();
        toggleMsel(key);
      };
    }
    const pop = $(m.pop);
    if (pop) {
      pop.onclick = e => {
        e.stopPropagation();
        const row = e.target.closest('.msel-opt');
        if (!row) return;
        pickMsel(key, row.dataset.v);
      };
      pop.onkeydown = e => {
        if (e.key !== 'Enter' && e.key !== ' ') return;
        const row = e.target.closest('.msel-opt');
        if (!row) return;
        e.preventDefault();
        pickMsel(key, row.dataset.v);
      };
    }
  }

  /* ---- 筛选：附件（单个复选框，三态循环）---- */
  const libAtt = $('libAtt');
  if (libAtt) {
    libAtt.onchange = () => {
      state.LIB.attachment = ATT_NEXT[state.LIB.attachment] || '';
      renderAttFilter();
      libQuery(true);
    };
  }

  document.addEventListener('click', e => {
    if (!e.target.closest || !e.target.closest('.msel')) closeMsels();
  });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape') closeMsels();
  });

  const libPrev = $('libPrev');
  if (libPrev) libPrev.onclick = () => { state.LIB.offset = Math.max(0, state.LIB.offset - state.LIB_LIMIT); loadPresetLib(); };
  const libNext = $('libNext');
  if (libNext) libNext.onclick = () => { state.LIB.offset += state.LIB_LIMIT; loadPresetLib(); };
  const btnReload = $('btnLibReload');
  if (btnReload) btnReload.onclick = () => loadPresetLib();

  const libTable = $('libTable');
  if (libTable) {
    libTable.onclick = e => {
      // 1. 点击表头的复选框或表头单元格，切换全选/取消全选
      if (e.target.id === 'libAllBox' || e.target.closest('.th-chk')) {
        if (!state.LIB_ROWS.length) return;
        const allSelected = state.LIB_ROWS.every(c => state.PSEL.has(c.id));
        if (allSelected) {
          state.LIB_ROWS.forEach(c => state.PSEL.delete(c.id));
        } else {
          state.LIB_ROWS.forEach(c => state.PSEL.add(c.id));
        }
        renderLibTable();
        return;
      }

      const del = e.target.closest('.lib-del');
      if (del) { delPresetOne(del.dataset.id); return; }
      // 编辑：必须在下面的「整行点击切换勾选」之前 return，否则点编辑会顺带勾选/取消勾选
      const edit = e.target.closest('.lib-edit');
      if (edit) { openCaseEdit(edit.dataset.id); return; }
      const row = e.target.closest('tr[data-id]');
      if (!row) return;
      const id = row.dataset.id;
      if (!id) return;
      state.PSEL.has(id) ? state.PSEL.delete(id) : state.PSEL.add(id);
      const chk = row.querySelector('input');
      if (chk) chk.checked = state.PSEL.has(id);
      syncLibSel();
    };

    // 「附件」标签的悬停气泡：只绑在容器上（表格会整块重渲染，绑行上会随重建丢掉）
    libTable.addEventListener('mouseover', e => {
      const t = e.target.closest('.tag-att');
      if (t) showAttTip(t);
    });
    // 移开标签不立刻销毁：鼠标得挪到气泡上才点得到文件名/复制图标，
    // 气泡自己的 mouseenter 会撤销这次延迟收起
    libTable.addEventListener('mouseout', e => {
      if (e.target.closest('.tag-att')) scheduleHideAttTip();
    });
    // 表格自己滚动、或页面滚动时标签会移走，气泡不能留在原地（这两种情况立即收）
    libTable.addEventListener('scroll', hideAttTip, true);
    window.addEventListener('scroll', hideAttTip, true);
  }

  const libAll = $('libAll');
  if (libAll) libAll.onclick = () => { state.LIB_ROWS.forEach(c => state.PSEL.add(c.id)); renderLibTable(); };
  const libNone = $('libNone');
  if (libNone) libNone.onclick = () => { state.PSEL.clear(); renderLibTable(); };

  /* ---- 新增用例对话框：入口、关闭、提交、快捷键 ---- */
  const btnLibAdd = $('btnLibAdd');
  if (btnLibAdd) btnLibAdd.onclick = openCaseAdd;

  const caseAddClose = $('caseAddClose');
  if (caseAddClose) caseAddClose.onclick = closeCaseAdd;
  const caseAddCancel = $('caseAddCancel');
  if (caseAddCancel) caseAddCancel.onclick = closeCaseAdd;

  const caAttPick = $('caAttPick');
  if (caAttPick) caAttPick.onclick = pickCaAttach;
  const caAttLib = $('caAttLib');
  if (caAttLib) caAttLib.onclick = () => openLib('caseadd');
  const caAttFile = $('caAttFile');
  if (caAttFile) caAttFile.onchange = () => { uploadCaFiles(caAttFile.files); caAttFile.value = ''; };

  /* ---- 自定义标签输入：回车 / 逗号确认，空输入时退格删掉最后一个，失焦也当确认 ---- */
  const caTagChips = $('caTagChips');
  if (caTagChips) {
    // 芯片上的 × 用委托绑定（芯片是动态重渲染的，绑在具体节点上会随重建丢掉）
    caTagChips.addEventListener('click', e => {
      const x = e.target.closest ? e.target.closest('.tag-x') : null;
      if (!x) return;
      e.stopPropagation();
      rmCaTag(Number(x.dataset.j));
    });
  }
  const caTagInput = $('caTagInput');
  if (caTagInput) {
    caTagInput.addEventListener('keydown', e => {
      if (e.isComposing) return;   // 中文输入法组字中的回车不算确认
      if (e.key === 'Enter' || e.key === ',' || e.key === '，' || e.key === '、') {
        e.preventDefault();
        commitCaTagInput();
        return;
      }
      // 输入框空着按退格 = 删掉最后一个标签（常见的手滑补救）
      if (e.key === 'Backspace' && !caTagInput.value) {
        const list = state.CA_TAGS || [];
        if (list.length) {
          e.preventDefault();
          rmCaTag(list.length - 1);
        }
      }
    });
    caTagInput.addEventListener('blur', commitCaTagInput);
  }

  const caseAddMask = $('caseAddMask');
  if (caseAddMask) {
    // 点遮罩空白处关闭（点对话框内部不关）
    caseAddMask.onclick = e => { if (e.target === e.currentTarget) closeCaseAdd(); };
    caseAddMask.addEventListener('keydown', e => {
      if (e.key === 'Escape') {
        // 附件库叠在对话框之上时，Esc 只关库（放行给全局处理）；
        // 拦下来会连对话框一起关掉，用户就白填了
        if ($('libMask')?.classList.contains('on')) return;
        e.stopPropagation();   // 别让全局 Esc（关附件库）也插一脚
        closeCaseAdd();
        return;
      }
      // Ctrl/⌘ + 回车 = 保存；单独回车留给 textarea 换行
      if (e.key !== 'Enter' || e.isComposing || !(e.ctrlKey || e.metaKey)) return;
      e.preventDefault();
      submitCaseAdd();
    });
  }

  const caseAddOk = $('caseAddOk');
  if (caseAddOk) caseAddOk.onclick = submitCaseAdd;

  const libDel = $('libDel');
  if (libDel) {
    libDel.onclick = async () => {
      const ids = [...state.PSEL];
      if (!ids.length) return;
      if (!confirm(`删除选中的 ${ids.length} 条预设用例？\n\n该操作无法撤销，确定删除？`)) return;
      try {
        const r = await fetch('/api/preset-cases', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: 'delete', ids })
        });
        const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
        if (!j.ok) { alert('删除失败：' + (j.message || '未知错误')); return; }
        state.PSEL = new Set();
        await loadPresetLib();
        const pMsg = $('libMsg');
        if (pMsg) pMsg.textContent = j.message;
      } catch (e) {
        alert('请求失败：' + e.message);
      }
    };
  }

  const libToQueue = $('libToQueue');
  if (libToQueue) {
    libToQueue.onclick = () => {
      const picked = state.LIB_ROWS.filter(c => state.PSEL.has(c.id));
      if (!picked.length) { alert('请先勾选要加入本轮的用例'); return; }
      state.CASES = state.CASES.filter(c => turnCount(c.prompt) > 0);
      // 勾选几条就加几条，不去重：队列里已有的相同提问也照加
      for (const it of picked) {
        // 多轮用例：整组轮次一起带回队列
        const turns = toTurns(it.prompt).map(t => String(t).trim()).filter(t => t !== '');
        if (!turns.length) continue;
        // 预设里存的附件路径要一起带回队列，否则运行时不会投递
        state.CASES.push({ prompt: turns, labels: it.labels || {}, attachments: attsFromPaths(it.attachments) });
      }
      renderCases();
      setCaseTab('queue');
      const msg = $('caseHint');
      if (msg) {
        msg.className = 'hint';
        msg.textContent = `已加入本轮：共 ${state.CASES.filter(c => turnCount(c.prompt) > 0).length} 条。点右下角「运行测试」即可开始。`;
      }
    };
  }
}
