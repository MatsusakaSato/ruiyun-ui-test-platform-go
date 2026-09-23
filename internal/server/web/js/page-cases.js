/* ================= 测试用例页（预设库 + 队列编辑 + 附件管理） ================= */

import { $, esc, fmtSize, fileToBase64 } from './utils.js';
import { state } from './state.js';
import { renderRunCases } from './page-home.js';

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

export function renderLib() {
  const c = state.CASES[state.LIB_FOR];
  if (!c) { closeLib(); return; }
  const used = new Set((c.attachments || []).map(a => a.path));
  const sub = $('libSub');
  if (sub) sub.textContent = `第 ${state.LIB_FOR + 1} 条用例 · 库中 ${state.UPLOADS.length} 个文件`;

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
      : '<div class="modal-empty">附件库为空：关闭后点用例行的「📎 附件」上传</div>';
  }
  const count = $('libCount');
  if (count) count.textContent = `本用例已引用 ${used.size} 个附件 · 点击行即加入`;
}

export function addFromLib(i, k) {
  const f = state.UPLOADS[k];
  if (!f || !state.CASES[i]) return;
  if (!state.CASES[i].attachments) state.CASES[i].attachments = [];
  if (state.CASES[i].attachments.some(a => a.path === f.path)) return;
  state.CASES[i].attachments.push({ name: f.name, path: f.path, size: f.size });
  renderCases();
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
    renderLib();
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
      hint.textContent = '队列还是空的。到「预设用例库」勾选用例后点「加入本轮」，或直接「+ 添加一行」。';
    } else {
      hint.className = 'hint';
      hint.textContent = `将按顺序执行 ${filled} 条用例，每条用例单独开一个会话。`;
    }
  }
  renderRunCases();
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
    const body = {
      action: 'add', prompt,
      labels: {
        scene: labels.scene || '',
        targets: labels.targets || [],
        attachment: labels.attachment === true
      }
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
               value="${esc(c.prompt)}" oninput="CASES[${i}].prompt=this.value; syncCaseState()"/>
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

export function addCase() {
  state.CASES.push({ prompt: '', attachments: [] });
  renderCases();
  const els = document.querySelectorAll('#caseRows .prompt');
  els[els.length - 1]?.focus();
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
  if (L.attachment === true) out += '<span class="tag t-att">需附件</span>';
  return out;
};

export function chipHtml(group, listItems, val, text) {
  const on = Array.isArray(val) ? val.includes(listItems) : (val === listItems);
  return `<span class="chip-c${on ? ' active' : ''}" data-g="${group}" data-v="${esc(listItems)}">${esc(text)}</span>`;
}

export function renderLibFilters() {
  const scenesEl = $('libScenes');
  if (scenesEl) {
    scenesEl.innerHTML = state.LIB_SCENES.length
      ? state.LIB_SCENES.map(s => chipHtml('scene', s, state.LIB.scene, s)).join('')
      : '<span class="fhint">库里还没有场景标签</span>';
  }
  const targetsEl = $('libTargets');
  if (targetsEl) {
    targetsEl.innerHTML = state.LIB_TARGETS.length
      ? state.LIB_TARGETS.map(t => chipHtml('target', t, state.LIB.targets, t)).join('')
      : '<span class="fhint">还没有测试目标标签</span>';
  }
  const attEl = $('libAtt');
  if (attEl) {
    attEl.innerHTML = chipHtml('att', 'yes', state.LIB.attachment, '需附件')
      + chipHtml('att', 'no', state.LIB.attachment, '不需附件');
  }
}

export function renderLibTable() {
  const rows = state.LIB_ROWS;
  const tbl = $('libTable');
  if (tbl) {
    tbl.innerHTML = '<table><thead><tr>'
      + '<th style="width:38px"><input type="checkbox" id="libAllBox" tabindex="-1"/></th>'
      + '<th style="width:104px">编号</th><th>提问</th><th style="width:210px">标签</th>'
      + '<th style="width:64px"></th></tr></thead><tbody>'
      + (rows.length ? rows.map(c => `<tr data-id="${esc(c.id || '')}">
          <td><input type="checkbox" ${state.PSEL.has(c.id) ? 'checked' : ''} tabindex="-1"/></td>
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
  const selEl = $('libSel');
  if (selEl && selEl.firstChild) selEl.firstChild.textContent = `已选 ${state.PSEL.size} 条`;
  const toQueue = $('libToQueue');
  if (toQueue) toQueue.disabled = state.PSEL.size === 0;
  const del = $('libDel');
  if (del) del.disabled = state.PSEL.size === 0;

  const box = $('libAllBox');
  if (box) box.checked = !!state.LIB_ROWS.length && state.LIB_ROWS.every(c => state.PSEL.has(c.id));
}

export async function loadPresetLib() {
  const q = new URLSearchParams({
    limit: state.LIB_LIMIT,
    offset: state.LIB.offset,
    keyword: state.LIB.keyword,
    scene: state.LIB.scene,
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

    const paddScenes = $('paddScenes');
    if (paddScenes) {
      paddScenes.innerHTML = state.LIB_SCENES.map(s => `<option value="${esc(s)}"/>`).join('');
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
  if (btnAdd) btnAdd.onclick = addCase;

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

  const libScenes = $('libScenes');
  if (libScenes) {
    libScenes.onclick = e => {
      const el = e.target.closest('.chip-c');
      if (!el) return;
      state.LIB.scene = (state.LIB.scene === el.dataset.v) ? '' : el.dataset.v;
      libQuery(true);
    };
  }

  const libTargets = $('libTargets');
  if (libTargets) {
    libTargets.onclick = e => {
      const el = e.target.closest('.chip-c');
      if (!el) return;
      const v = el.dataset.v;
      state.LIB.targets = state.LIB.targets.includes(v)
        ? state.LIB.targets.filter(x => x !== v)
        : state.LIB.targets.concat(v);
      libQuery(true);
    };
  }

  const libAtt = $('libAtt');
  if (libAtt) {
    libAtt.onclick = e => {
      const el = e.target.closest('.chip-c');
      if (!el) return;
      state.LIB.attachment = (state.LIB.attachment === el.dataset.v) ? '' : el.dataset.v;
      libQuery(true);
    };
  }

  const libPrev = $('libPrev');
  if (libPrev) libPrev.onclick = () => { state.LIB.offset = Math.max(0, state.LIB.offset - state.LIB_LIMIT); loadPresetLib(); };
  const libNext = $('libNext');
  if (libNext) libNext.onclick = () => { state.LIB.offset += state.LIB_LIMIT; loadPresetLib(); };
  const btnReload = $('btnLibReload');
  if (btnReload) btnReload.onclick = () => loadPresetLib();

  const libTable = $('libTable');
  if (libTable) {
    libTable.onclick = e => {
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
  }

  const libAll = $('libAll');
  if (libAll) libAll.onclick = () => { state.LIB_ROWS.forEach(c => state.PSEL.add(c.id)); renderLibTable(); };
  const libNone = $('libNone');
  if (libNone) libNone.onclick = () => { state.PSEL.clear(); renderLibTable(); };

  const btnLibAdd = $('btnLibAdd');
  if (btnLibAdd) {
    btnLibAdd.onclick = () => {
      const b = $('paddBody');
      if (b) {
        b.classList.add('show');
        $('paddPrompt')?.focus();
      }
    };
  }

  const paddBody = $('paddBody');
  if (paddBody) paddBody.onclick = e => e.stopPropagation();

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
        const pMsg = $('paddMsg');
        if (pMsg) { pMsg.textContent = '✓ ' + j.message; pMsg.style.color = ''; }
      } catch (e) {
        alert('请求失败：' + e.message);
      }
    };
  }

  const paddOk = $('paddOk');
  if (paddOk) {
    paddOk.onclick = async () => {
      const prompt = $('paddPrompt')?.value.trim();
      const msg = $('paddMsg');
      if (!prompt) {
        if (msg) { msg.textContent = '提问不能为空'; msg.style.color = '#b91c1c'; }
        return;
      }
      const targets = [...($('paddTargets')?.querySelectorAll('input:checked') || [])].map(i => i.value);
      const scene = $('paddScene')?.value.trim() || '';
      paddOk.disabled = true;
      if (msg) { msg.textContent = '保存中…'; msg.style.color = ''; }
      try {
        const r = await fetch('/api/preset-cases', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: 'add', prompt, labels: { scene, targets } })
        });
        const j = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
        if (!j.ok) {
          if (msg) { msg.textContent = j.message || '保存失败'; msg.style.color = '#b91c1c'; }
          return;
        }
        if ($('paddPrompt')) $('paddPrompt').value = '';
        if ($('paddScene')) $('paddScene').value = '';
        $('paddTargets')?.querySelectorAll('input').forEach(i => { i.checked = false; });
        if (msg) { msg.textContent = '✓ ' + (j.message || '已保存'); msg.style.color = ''; }
        state.LIB.offset = 0;
        await loadPresetLib();
      } catch (e) {
        if (msg) { msg.textContent = '请求失败：' + e.message; msg.style.color = '#b91c1c'; }
      }
      paddOk.disabled = false;
    };
  }

  const paddPrompt = $('paddPrompt');
  if (paddPrompt) {
    paddPrompt.addEventListener('keydown', e => {
      if (e.key !== 'Enter' || e.isComposing || !(e.ctrlKey || e.metaKey)) return;
      e.preventDefault();
      $('paddOk')?.click();
    });
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
        state.CASES.push({ prompt: p, labels: it.labels || {}, attachments: [] });
        have.add(p);
      }
      renderCases();
      setCaseTab('queue');
      const msg = $('caseHint');
      if (msg) {
        msg.className = 'hint';
        msg.textContent = `已加入本轮：共 ${state.CASES.filter(c => c.prompt.trim()).length} 条`
          + (dup ? `（跳过 ${dup} 条重复提问）` : '') + '。到「首页」点「▶ 运行测试」即可开始。';
      }
    };
  }
}
