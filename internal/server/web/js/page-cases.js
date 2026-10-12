/* ================= 测试用例页（预设库 + 队列编辑 + 附件管理） ================= */

import { $, esc, fmtSize, fileToBase64, IS_WIN, MOD_KEY } from './utils.js';
import { state } from './state.js';
import { renderRunCases, doRun } from './page-home.js';

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
  const filled = state.CASES.filter(c => c.prompt.trim()).length;
  document.querySelectorAll('.btn-run').forEach(b => {
    b.disabled = state.RUNNING || filled === 0;
  });
  const tqc = $('tabQueueCount');
  if (tqc) tqc.textContent = filled ? `（${filled}）` : '';
  const qc = $('queueCount');
  if (qc) qc.textContent = filled ? `（${filled} 条）` : '';

  state.CASES.forEach((c, i) => {
    const b = $('saveCase' + i);
    if (b) b.disabled = !c.prompt.trim();
  });

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

export async function savePreset(i) {
  const c = state.CASES[i];
  if (!c) return;
  const prompt = (c.prompt || '').trim();
  if (!prompt) { alert('这条用例还没有填提问，先填了再存。'); return; }
  const btn = $('saveCase' + i);
  if (btn) btn.disabled = true;
  try {
    const labels = c.labels || {};
    // 队列里已经挂好的附件要一并入库，否则存成预设后附件就丢了
    const attachments = (c.attachments || []).map(a => a.path);
    const body = {
      action: 'add', prompt,
      labels: {
        scene: labels.scene || '',
        targets: labels.targets || [],
        attachment: labels.attachment === true || attachments.length > 0
      },
      attachments
    };
    const r = await fetch('/api/preset-cases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
    if (!j.ok) { alert('存入预设失败：' + (j.message || '未知错误')); return; }
    const id = (j.case && j.case.id) || '';
    if (btn) {
      btn.textContent = id ? `✓ ${id}` : '✓ 已存入';
      btn.classList.add('saved');
      setTimeout(() => { btn.textContent = '☆ 存为预设'; btn.classList.remove('saved'); }, 2200);
    }
    state.LIB.keyword = '';
    state.LIB.scenes = [];
    state.LIB.targets = [];
    state.LIB.attachment = '';
    state.LIB.offset = 0;
    const kwEl = $('libKw');
    if (kwEl) kwEl.value = '';
    await loadPresetLib();
  } catch (e) {
    alert('请求失败：' + e.message);
  } finally {
    const b = $('saveCase' + i);
    if (b) b.disabled = !state.CASES[i] || !(state.CASES[i].prompt || '').trim();
  }
}

export function renderCases() {
  const box = $('caseRows');
  if (!box) return;
  box.innerHTML = state.CASES.map((c, i) => {
    const atts = c.attachments || [];
    const tags = [
      c.labels && c.labels.scene ? `<span class="tag t-scene">${esc(c.labels.scene)}</span>` : '',
      ...((c.labels && c.labels.targets) || []).map(t => `<span class="tag">${esc(t)}</span>`),
      ...atts.map((a, j) => `<span class="tag t-att" title="${esc(a.path)}">${esc(a.name)}`
        + `<b class="tag-x" onclick="rmAttach(${i},${j})" title="移除该附件">×</b></span>`),
    ].join('');
    return `
    <div class="case-row">
      <div class="no">${String(i + 1).padStart(2, '0')}</div>
      <div class="fields">
        <input class="prompt" placeholder="输入提示词，例如：请读取你的记忆文件并告诉我记录条数"
               value="${esc(c.prompt)}" oninput="CASES[${i}].prompt=this.value; syncCaseState()"
               onchange="CASES[${i}].prompt=this.value; syncCaseState()"/>
        <div class="cl-acts">
          <button class="cl-att" onclick="pickAttach(${i})" title="上传附件：运行时会被投递给被测应用">📎 附件</button>
          <button class="cl-att" onclick="openLib(${i})" title="打开附件库，点击即加入本用例">库${state.UPLOADS.length ? '(' + state.UPLOADS.length + ')' : ''}</button>
          <button class="cl-att cl-save" id="saveCase${i}" onclick="savePreset(${i})"
                  title="把这条提问存进预设用例库">☆ 存为预设</button>
          <input type="file" multiple style="display:none" id="attFile${i}"
                 onchange="uploadFiles(this.files,${i});this.value=''"/>
        </div>
        ${tags ? `<div class="cl-tags">${tags}</div>` : ''}
      </div>
      <button class="del" title="删除该用例" onclick="delCase(${i})">×</button>
    </div>`;
  }).join('');
  syncCaseState();
}

/* at 省略时追加到队尾；传入序号则插到该位置之前（回车新增用 at = 当前行 + 1） */
export function addCase(at) {
  const pos = Number.isInteger(at)
    ? Math.min(Math.max(at, 0), state.CASES.length)
    : state.CASES.length;
  state.CASES.splice(pos, 0, { prompt: '', attachments: [] });
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
  for (const t of (L.targets || [])) out += `<span class="tag">${esc(t)}</span>`;
  if (L.attachment === true) {
    // 附件名塞在 data-atts 里，hover 时由 showAttTip 读出来浮个气泡
    // （Excel 导入的老数据可能只标记了「要附件」却没记路径，那时 data-atts 为空）
    const names = attsFromPaths(c.attachments).map(a => a.name);
    out += `<span class="tag tag-att" data-atts="${esc(names.join('\n'))}">附件</span>`;
  }
  return out;
};

/* 附件标签的悬停气泡。
   预设库表格是 .lib-table（overflow:auto）的滚动容器，纯 CSS 的 ::after 气泡会被容器裁掉，
   所以气泡挂在 body 上用 fixed 定位，跟着标签的 rect 走。 */
function hideAttTip() {
  document.getElementById('attTip')?.remove();
}

function showAttTip(anchor) {
  hideAttTip();
  const names = anchor.dataset.atts || '';
  const el = document.createElement('div');
  el.id = 'attTip';
  el.className = 'att-tip' + (names ? '' : ' muted');
  el.textContent = names || '未记录文件名';
  document.body.appendChild(el);

  const r = anchor.getBoundingClientRect();
  const w = el.offsetWidth;
  const h = el.offsetHeight;
  // 默认贴标签下方；下方放不下就翻到上方，两侧再夹一下别溢出视口
  const below = r.bottom + 7;
  const top = below + h <= window.innerHeight - 6 ? below : r.top - 7 - h;
  el.style.top = Math.round(Math.max(6, Math.min(top, window.innerHeight - h - 6))) + 'px';
  el.style.left = Math.round(Math.max(6, Math.min(r.left, window.innerWidth - w - 8))) + 'px';
}

/* ---------- 筛选控件：多选下拉 + 附件复选框 ----------
   两个多选下拉共用一份渲染与交互逻辑。渲染时只替换「已选 tag 条」与「选项列表」两块
   内部内容，不重建 .msel 外壳 —— 面板的展开状态（pop 的 hidden）不受列表刷新影响，
   所以勾选一项触发查询后，面板仍是打开的，可以继续勾下一项。
   可选值来自服务端返回的 state.LIB_SCENES / state.LIB_TARGETS（库中已有的去重标签）。 */
const MSEL = {
  scene: { wrap: 'mselScene', box: 'mselSceneBox', tags: 'mselSceneTags', pop: 'mselScenePop', list: 'LIB_SCENES', picked: 'scenes' },
  target: { wrap: 'mselTarget', box: 'mselTargetBox', tags: 'mselTargetTags', pop: 'mselTargetPop', list: 'LIB_TARGETS', picked: 'targets' }
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
    // 未选时不占位：字段名（场景 / 测试目标）在 .msel-box 里常驻，本身即提示
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
      + '<th style="width:64px"></th></tr></thead><tbody>'
      + (rows.length ? rows.map(c => `<tr data-id="${esc(c.id || '')}">
          <td class="td-chk"><input type="checkbox" ${state.PSEL.has(c.id) ? 'checked' : ''} tabindex="-1"/></td>
          <td class="mono">${esc(c.id || '')}</td>
          <td class="lp" title="${esc(c.prompt || '')}">${esc(c.prompt || '')}</td>
          <td>${libSelTags(c)}</td>
          <td><button class="home-link lib-del" data-id="${esc(c.id || '')}">删除</button></td>
        </tr>`).join('')
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
  //   scene / targets 多个值用英文逗号拼接（单个值时与改造前的格式完全一致），空串表示不限
  //   attachment 取 '' / 'yes' / 'no'
  const q = new URLSearchParams({
    limit: state.LIB_LIMIT,
    offset: state.LIB.offset,
    keyword: state.LIB.keyword,
    scene: state.LIB.scenes.join(','),
    targets: state.LIB.targets.join(','),
    attachment: state.LIB.attachment
  });
  try {
    const j = await (await fetch('/api/preset-cases?' + q)).json();
    state.LIB_ROWS = j.cases || [];
    state.LIB_TOTAL = j.total || 0;
    state.LIB_SCENES = j.scenes || [];
    state.LIB_TARGETS = j.targets || [];
    state.PSEL = new Set([...state.PSEL].filter(id => state.LIB_ROWS.some(c => c.id === id)));

    const caScenesEl = $('caScenes');
    if (caScenesEl) {
      caScenesEl.innerHTML = state.LIB_SCENES.map(s => `<option value="${esc(s)}"/>`).join('');
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

export function openCaseAdd() {
  const mask = $('caseAddMask');
  if (!mask) return;
  if ($('caName')) $('caName').value = '';
  if ($('caScene')) $('caScene').value = '';
  if ($('caPrompt')) $('caPrompt').value = '';
  state.CA_ATTS = [];
  renderCaAtts();
  const caFile = $('caAttFile');
  if (caFile) caFile.value = '';
  $('caTargets')?.querySelectorAll('input').forEach(i => { i.checked = false; });
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
    caseAddHint('提问不能为空');
    promptEl?.closest('.fld')?.classList.add('err');
    promptEl?.focus();
    return;
  }
  promptEl.closest('.fld')?.classList.remove('err');

  const name = ($('caName')?.value || '').trim();
  const scene = ($('caScene')?.value || '').trim();
  const targets = [...($('caTargets')?.querySelectorAll('input:checked') || [])].map(i => i.value);
  // 附件以本机绝对路径数组入库；服务端还会逐个 os.Stat，不存在的剔除后回传 skipped_attachments
  const attachments = (state.CA_ATTS || []).map(a => a.path);
  // 「需要附件」不再手填：挂了附件就是需要，没挂就是不需要
  const attachment = attachments.length > 0;

  const okBtn = $('caseAddOk');
  if (okBtn) okBtn.disabled = true;
  caseAddHint('保存中…');
  try {
    const r = await fetch('/api/preset-cases', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        action: 'add', name, prompt,
        labels: { scene, targets, attachment },
        attachments
      })
    });
    const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
    if (!j.ok) {
      // 服务端校验（如重复用例）留在对话框里提示，不关窗、不清空输入
      caseAddHint(j.message || '保存失败');
      return;
    }
    const skipped = (j.skipped_attachments || []).length;
    closeCaseAdd();

    // 重置筛选条件与搜索关键字，确保新增用例直接在第一行立即可见，不被既有过滤条件隐藏
    state.LIB.keyword = '';
    state.LIB.scenes = [];
    state.LIB.targets = [];
    state.LIB.attachment = '';
    state.LIB.offset = 0;
    const kwEl = $('libKw');
    if (kwEl) kwEl.value = '';

    await loadPresetLib();
    flashAddedCase(j.case && j.case.id);
    if (skipped) {
      const m = $('libMsg');
      if (m) m.textContent = `已跳过 ${skipped} 个不存在的附件`;
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
      if (i === state.CASES.length - 1 && !((cur && cur.prompt) || '').trim()) return;
      addCase(i + 1);
    });
  }

  const btnClear = $('btnClearCases');
  if (btnClear) {
    btnClear.onclick = () => {
      if (state.CASES.filter(c => c.prompt.trim()).length
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

  /* ---- 筛选：场景 / 测试目标（多选下拉）----
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
    libTable.addEventListener('mouseout', e => {
      if (e.target.closest('.tag-att')) hideAttTip();
    });
    // 表格自己滚动、或页面滚动时标签会移走，气泡不能留在原地
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
      state.CASES = state.CASES.filter(c => c.prompt.trim());
      const have = new Set(state.CASES.map(c => (c.prompt || '').trim()));
      let dup = 0;
      for (const it of picked) {
        const p = (it.prompt || '').trim();
        if (!p) continue;
        if (have.has(p)) { dup++; continue; }
        // 预设里存的附件路径要一起带回队列，否则运行时不会投递
        state.CASES.push({ prompt: p, labels: it.labels || {}, attachments: attsFromPaths(it.attachments) });
        have.add(p);
      }
      renderCases();
      setCaseTab('queue');
      const msg = $('caseHint');
      if (msg) {
        msg.className = 'hint';
        msg.textContent = `已加入本轮：共 ${state.CASES.filter(c => c.prompt.trim()).length} 条`
          + (dup ? `（跳过 ${dup} 条重复提问）` : '') + '。点右下角「运行测试」即可开始。';
      }
    };
  }
}
