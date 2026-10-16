/* ================= Excel / CSV 导入用例 =================
   平台已提供标准模板，导入不再让用户手动挑选列：
   上传后先按模板校验并预览，不符合模板直接提示；确认导入后在用例库给出成功/失败结果。 */

import { $, esc, fileToBase64, fmtInvalidAttachments, toTurns, firstTurn, turnsBadge } from './utils.js';
import { state } from './state.js';
import { loadPresetLib } from './page-cases.js';

// 导入模式：radio 分段控件，取值统一从这里读
export function impxMode() {
  const el = document.querySelector('#impxBar input[name="impxMode"]:checked');
  return el ? el.value : 'append';
}

// 预览里带无效附件的行号集合（服务端静态检查的结果，行号即 items 下标 + 1）
function impxBadRows() {
  const set = new Set();
  ((state.IMPX && state.IMPX.suspicious_attachments) || []).forEach(x => set.add(x.row));
  return set;
}

function baseName(s) {
  const t = String(s || '');
  const i = Math.max(t.lastIndexOf('/'), t.lastIndexOf('\\'));
  return i >= 0 ? t.slice(i + 1) : t;
}

export function updateImpxNote() {
  if (!state.IMPX) return;
  const total = state.IMPX.items_total != null ? state.IMPX.items_total : (state.IMPX.items || []).length;
  const isReplace = impxMode() === 'replace';
  const sus = (state.IMPX.suspicious_attachments || []).length;

  const cntEl = $('impxCount');
  if (cntEl) {
    cntEl.innerHTML = `识别到 <b>${total}</b> 条用例`;
  }
  const noteEl = $('impxNote');
  if (noteEl) {
    noteEl.innerHTML = `已跳过 <b>${state.IMPX.skipped || 0}</b> 行空提问，即将导入 <b>${total}</b> 条。`
      + (isReplace
        ? '<span class="danger"> ⚠️ 覆盖导入会清空当前预设用例库并重新写入！</span>'
        : '确认后以 <b>追加</b> 方式写入')
      + (sus
        ? `<br><span class="warn">⚠️ 有 ${sus} 个附件看起来无法解析，导入时会被忽略。</span>`
        : '')
      + ((state.IMPX.items || []).length < total ? `<br><span class="warn">表格只预览前 ${(state.IMPX.items || []).length} 条，实际导入全部 ${total} 条。</span>` : '');
  }
  const okBtn = $('impxOk');
  if (okBtn) {
    okBtn.disabled = !total;
    // 覆盖导入是不可逆操作：按钮文案与配色跟着模式变，避免误点
    okBtn.textContent = isReplace ? '覆盖并导入' : '加入用例库';
    okBtn.classList.toggle('is-danger', isReplace);
  }
}

export function impxRender(d) {
  state.IMPX = d;
  const sub = $('impxSub');
  if (sub) sub.textContent = `${d.file}${d.sheet ? ' · 工作表 ' + d.sheet : ''}`;

  const bar = $('impxBar');
  if (bar) {
    bar.innerHTML =
       `<span class="impx-count" id="impxCount"></span>`
      + `<span class="spacer"></span>`
      + `<span class="impx-seg" role="radiogroup" aria-label="导入模式">`
      + `<label class="impx-seg-opt"><input type="radio" name="impxMode" value="append" checked/><span>追加导入</span></label>`
      + `<label class="impx-seg-opt danger"><input type="radio" name="impxMode" value="replace"/><span>覆盖导入</span></label>`
      + `</span>`;
  }

  const tbl = $('impxTable');
  if (tbl) {
    const badRows = impxBadRows();
    tbl.innerHTML = '<thead><tr><th class="imp-c-no">#</th>'
      + '<th>提问</th>'
      + '<th class="imp-c-scene">场景</th>'
      + '<th class="imp-c-diff">难度</th>'
      + '<th class="imp-c-att">附件</th></tr></thead><tbody>'
      + ((d.items || []).map((it, i) => {
          const no = i + 1;
          const atts = it.attachments || [];
          const att = atts.length
            ? `<span class="imp-att${badRows.has(no) ? ' bad' : ''}" title="${esc(atts.join('\n'))}">`
              + `${badRows.has(no) ? '⚠️' : '📎'} ${esc(baseName(atts[0]))}${atts.length > 1 ? ` +${atts.length - 1}` : ''}</span>`
            : '<span class="imp-none">—</span>';
          return `<tr>`
            + `<td class="imp-no">${no}</td>`
            + `<td class="imp-prompt" title="${esc(toTurns(it.prompt).join('  ⏎  '))}">`
              + `${turnsBadge(it.prompt)}${esc(firstTurn(it.prompt))}</td>`
            + `<td class="imp-scene">${esc(it.scene || '')}</td>`
            + `<td class="imp-diff">${esc(it.difficulty || '')}</td>`
            + `<td>${att}</td>`
            + `</tr>`;
        }).join('')
        || '<tr><td colspan="5" class="tbl-empty">没有解析到任何用例</td></tr>')
      + '</tbody>';
  }
  updateImpxNote();
}

export async function impxSend(parseOnly, replace) {
  const body = {
    name: state.IMPX.file,
    data_base64: state.IMPX.data_base64,
    parse_only: !!parseOnly,
    replace: !!replace
  };
  const r = await fetch('/api/preset-import', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  return await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
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
        // 不符合模板直接提示，不再进入手动改列的流程
        if (d.valid_template === false) {
          alert('文件不符合导入模板，无法导入：\n\n'
            + (d.template_error || '未识别到模板要求的结构')
            + '\n\n请点击「下载模板」，按模板的表头和列填写后再上传。');
          return;
        }
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

  // 切换导入模式会影响提示文案
  const bar = $('impxBar');
  if (bar) {
    bar.addEventListener('change', e => {
      if (e.target && e.target.name === 'impxMode') updateImpxNote();
    });
  }

  const impxOk = $('impxOk');
  if (impxOk) {
    impxOk.onclick = async () => {
      if (!state.IMPX) return;
      const isReplace = impxMode() === 'replace';
      if (isReplace) {
        if (!confirm('确定要覆盖导入吗？这将清空当前预设用例库中的全部用例并替换为当前文件内容！')) {
          return;
        }
      }
      impxOk.disabled = true;
      try {
        const d = await impxSend(false, isReplace);
        if (!d.ok) { alert('导入失败：' + (d.message || '未知错误')); return; }
        closeImpx();
        state.LIB.offset = 0;
        state.PSEL = new Set();
        await loadPresetLib();

        const pMsg = $('libMsg');
        const invalid = d.invalid_attachments || [];
        if (d.added > 0) {
          if (pMsg) pMsg.textContent = d.message || `已导入 ${d.added} 条用例`;
          if (invalid.length) {
            alert(`导入完成，但有 ${invalid.length} 个附件无效已被忽略：\n\n`
              + fmtInvalidAttachments(invalid)
              + '\n\n请修正表格里的附件后重新导入相关用例。');
          }
        } else {
          // 一条都没进：多半是全部重复
          if (pMsg) pMsg.textContent = d.message || '未导入任何用例';
          alert('未导入任何用例。\n\n' + (d.message || '')
            + (invalid.length ? `\n\n另有 ${invalid.length} 个附件无效：\n` + fmtInvalidAttachments(invalid) : ''));
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
