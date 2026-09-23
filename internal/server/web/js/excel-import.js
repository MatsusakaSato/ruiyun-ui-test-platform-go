/* ================= Excel / CSV 导入用例 ================= */

import { $, esc, fileToBase64 } from './utils.js';
import { state } from './state.js';
import { loadPresetLib } from './page-cases.js';

export function colName(i) {
  let s = ''; i = Number(i) + 1;
  while (i > 0) {
    const r = (i - 1) % 26;
    s = String.fromCharCode(65 + r) + s;
    i = Math.floor((i - 1) / 26);
  }
  return '第 ' + s + ' 列';
}

export function impxOptions(selected, allowNone) {
  if (!state.IMPX) return '';
  const n = Math.max(state.IMPX.width || 0, (state.IMPX.header || []).length, selected != null ? Number(selected) + 1 : 0);
  let out = allowNone ? `<option value="-1"${selected == null ? ' selected' : ''}>不使用</option>` : '';
  for (let i = 0; i < n; i++) {
    const t = (state.IMPX.header || [])[i];
    const label = (t && String(t).trim()) ? `${colName(i)} · ${String(t).trim()}` : colName(i);
    out += `<option value="${i}"${Number(selected) === i ? ' selected' : ''}>${esc(label)}</option>`;
  }
  return out;
}

export function updateImpxNote() {
  if (!state.IMPX) return;
  const total = state.IMPX.items_total != null ? state.IMPX.items_total : (state.IMPX.items || []).length;
  const isReplace = $('impxMode') && $('impxMode').value === 'replace';
  const cntEl = $('impxCount');
  if (cntEl) {
    cntEl.textContent = `识别到 ${total} 条用例`
      + (state.IMPX.skipped ? `，跳过 ${state.IMPX.skipped} 行（提问为空）` : '');
  }
  const noteEl = $('impxNote');
  if (noteEl) {
    noteEl.innerHTML = `已跳过 <b>${state.IMPX.skipped || 0}</b> 行空提问，即将导入 <b>${total}</b> 条。`
      + (isReplace
        ? '<span style="color:#ef4444;font-weight:600"> ⚠️ 警告：当前选择【覆盖导入】，确认后将清空当前预设用例库并重新写入！</span>'
        : '确认后以 <b>追加</b> 方式写入预设用例库，不影响已有用例。')
      + ((state.IMPX.items || []).length < total ? `<br><span class="warn">表格只预览前 ${(state.IMPX.items || []).length} 条，实际导入全部 ${total} 条。</span>` : '');
  }
  const okBtn = $('impxOk');
  if (okBtn) okBtn.disabled = !total;
}

export function impxRender(d) {
  state.IMPX = d;
  const sub = $('impxSub');
  if (sub) sub.textContent = `${d.file}${d.sheet ? ' · 工作表 ' + d.sheet : ''}`;

  const cols = $('impxCols');
  if (cols) {
    cols.innerHTML =
      `<div class="fld"><label>提问列（必选）</label><select id="impxPrompt">${impxOptions(d.prompt_col, false)}</select></div>`
      + `<div class="fld"><label>场景列（选填）</label><select id="impxScene">${impxOptions(d.scene_col, true)}</select></div>`
      + `<div class="fld"><label>序号/名称列（选填）</label><select id="impxName">${impxOptions(d.name_col, true)}</select></div>`
      + `<div class="fld"><label>测试目标列（选填）</label><select id="impxTarget">${impxOptions(d.target_col, true)}</select></div>`
      + `<div class="fld"><label>附件列（选填）</label><select id="impxAttachment">${impxOptions(d.attachment_col, true)}</select></div>`
      + `<div class="fld"><label>导入模式</label><select id="impxMode">`
      + `<option value="append" selected>追加导入（保留现有）</option>`
      + `<option value="replace">覆盖导入（清空后重建）</option></select></div>`;
  }

  const tbl = $('impxTable');
  if (tbl) {
    tbl.innerHTML = '<thead><tr><th style="width:40px">行</th>'
      + '<th style="width:130px">序号/名称</th>'
      + '<th>提问</th>'
      + '<th style="width:100px">场景</th>'
      + '<th style="width:110px">测试目标</th>'
      + '<th style="width:110px">附件</th></tr></thead><tbody>'
      + ((d.items || []).map((it, i) => `<tr><td class="mono">${i + 1}</td>`
          + `<td class="mono" style="font-size:11.5px" title="${esc(it.name || '')}">${esc(it.name || '')}</td>`
          + `<td class="imp-prompt" title="${esc(it.prompt)}">${esc(it.prompt)}</td>`
          + `<td>${esc(it.scene || '')}</td>`
          + `<td>${esc((it.targets || []).join(', '))}</td>`
          + `<td style="font-size:11.5px;color:var(--tx3)" title="${esc((it.attachments || []).join('; '))}">${esc((it.attachments || []).join('; '))}</td></tr>`).join('')
        || '<tr><td colspan="6" class="tbl-empty">没有解析到任何用例</td></tr>')
      + '</tbody>';
  }
  updateImpxNote();
}

export async function impxSend(parseOnly, cols, replace) {
  const body = {
    name: state.IMPX.file,
    data_base64: state.IMPX.data_base64,
    parse_only: !!parseOnly,
    replace: !!replace
  };
  if (cols) {
    body.prompt_col = cols.prompt;
    body.scene_col = cols.scene;
    body.target_col = cols.target;
    body.name_col = cols.name;
    body.attachment_col = cols.attachment;
  }
  const r = await fetch('/api/preset-import', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  return await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
}

export async function impxRemap() {
  if (!state.IMPX) return;
  const cols = {
    prompt: +$('impxPrompt').value,
    scene: +$('impxScene').value,
    target: +$('impxTarget').value,
    name: +$('impxName').value,
    attachment: +$('impxAttachment').value
  };
  const modeVal = $('impxMode') ? $('impxMode').value : 'append';
  const d = await impxSend(true, cols, false).catch(e => ({ ok: false, message: e.message }));
  if (!d.ok) { alert('重新解析失败：' + (d.message || '未知错误')); return; }
  d.data_base64 = state.IMPX.data_base64;
  impxRender(d);
  if ($('impxMode')) $('impxMode').value = modeVal;
  updateImpxNote();
}

export function closeImpx() {
  $('impxMask')?.classList.remove('on');
  state.IMPX = null;
}

export function initExcelImport() {
  const btnImport = $('btnLibImport');
  if (btnImport) btnImport.onclick = () => $('libFile')?.click();

  const fileInp = $('libFile');
  if (fileInp) {
    fileInp.onchange = async () => {
      const f = fileInp.files && fileInp.files[0];
      fileInp.value = '';
      if (!f) return;
      if (f.size > 100 * 1024 * 1024) { alert('文件超过 100MB，请拆分后再导入'); return; }
      let b64 = '';
      try { b64 = await fileToBase64(f); } catch (e) { alert('读取文件失败：' + e.message); return; }

      btnImport.disabled = true;
      btnImport.textContent = '解析中…';
      try {
        const r = await fetch('/api/preset-import', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: f.name, data_base64: b64, parse_only: true })
        });
        const d = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
        if (!d.ok) { alert('解析失败：' + (d.message || '未知错误')); return; }
        d.data_base64 = b64;
        impxRender(d);
        $('impxMask')?.classList.add('on');
      } catch (e) {
        alert('请求失败：' + e.message);
      } finally {
        btnImport.disabled = false;
        btnImport.textContent = '📄 从 Excel 导入';
      }
    };
  }

  const impxCols = $('impxCols');
  if (impxCols) {
    impxCols.addEventListener('change', e => {
      const id = e.target && e.target.id;
      if (id === 'impxPrompt' || id === 'impxScene' || id === 'impxTarget' || id === 'impxName' || id === 'impxAttachment') {
        impxRemap();
      } else if (id === 'impxMode') {
        updateImpxNote();
      }
    });
  }

  const impxOk = $('impxOk');
  if (impxOk) {
    impxOk.onclick = async () => {
      if (!state.IMPX) return;
      const isReplace = $('impxMode') && $('impxMode').value === 'replace';
      if (isReplace) {
        if (!confirm('确定要覆盖导入吗？这将清空当前预设用例库中的全部用例并替换为当前文件内容！')) {
          return;
        }
      }
      impxOk.disabled = true;
      try {
        const cols = {
          prompt: +$('impxPrompt').value,
          scene: +$('impxScene').value,
          target: +$('impxTarget').value,
          name: +$('impxName').value,
          attachment: +$('impxAttachment').value
        };
        const d = await impxSend(false, cols, isReplace);
        if (!d.ok) { alert('导入失败：' + (d.message || '未知错误')); return; }
        closeImpx();
        state.LIB.offset = 0;
        state.PSEL = new Set();
        await loadPresetLib();
        const pMsg = $('paddMsg');
        if (pMsg) {
          pMsg.textContent = '✓ ' + (d.message || '导入完成');
          pMsg.style.color = '';
        }
      } catch (e) {
        alert('请求失败：' + e.message);
      } finally {
        impxOk.disabled = false;
      }
    };
  }

  const impxClose = $('impxClose');
  if (impxClose) impxClose.onclick = closeImpx;
  const impxCancel = $('impxCancel');
  if (impxCancel) impxCancel.onclick = closeImpx;
  const impxMask = $('impxMask');
  if (impxMask) impxMask.onclick = e => { if (e.target === impxMask) closeImpx(); };
}
