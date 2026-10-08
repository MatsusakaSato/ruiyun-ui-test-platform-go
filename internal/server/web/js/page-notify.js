/* ================= 发送通知页（钉钉群消息 + 预设模板） ================= */

import { $, esc } from './utils.js';

// 预设模板存服务端 SQLite（与预设用例同库，表 notify_templates）
const TPL_API = '/api/notify/templates';

// 是否正在发送（避免连点重复发送）
let sending = false;
let tpls = [];
// 保存弹窗处于「重命名」模式时，记录正在改的模板 id；null = 新建
let editingId = null;

/* ---------------- 模板读写（服务端 SQLite） ---------------- */

async function apiTpl(payload) {
  const r = await fetch(TPL_API, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  });
  return await r.json();
}

async function loadTpls() {
  try {
    const j = await (await fetch(TPL_API)).json();
    tpls = Array.isArray(j.items) ? j.items : [];
  } catch (e) {
    tpls = [];
  }
}

/* ---------------- 发送相关 ---------------- */

function showResult(cls, text) {
  const box = $('notifyResult');
  box.className = 'probe-box ' + cls;
  box.style.display = 'block';
  box.textContent = text;
}

function showFieldError(msg) {
  const h = $('notifyMsgHint');
  h.textContent = msg;
  h.hidden = false;
}

function clearFieldError() {
  $('notifyMsgHint').hidden = true;
}

// 按钮可用性：内容为空 或 正在发送 → 禁用
function refreshState() {
  const msg = $('notifyMsg').value.trim();
  $('notifyCount').textContent = msg.length + ' 字';
  $('btnNotifySend').disabled = sending || msg === '';
}

function openNotifyConfirm() {
  $('notifyConfirmText').textContent = $('notifyMsg').value.trim();
  $('notifyMask').classList.add('on');
}

function closeNotifyConfirm() {
  $('notifyMask').classList.remove('on');
}

async function doSend(group, msg) {
  const btn = $('btnNotifySend');
  sending = true;
  btn.disabled = true;
  btn.classList.add('is-busy');
  btn.textContent = '发送中…';
  showResult('busy', '正在发送…');
  try {
    const r = await fetch('/api/notify/send', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message: msg, group })
    });
    const j = await r.json();
    if (j.ok) {
      showResult('ok', '✅ ' + (j.message || '发送成功'));
    } else {
      showResult('err', '❌ ' + (j.message || '发送失败'));
    }
  } catch (e) {
    showResult('err', '❌ 发送出错：' + e);
  } finally {
    sending = false;
    btn.classList.remove('is-busy');
    btn.textContent = '发送';
    refreshState();
  }
}

/* ---------------- 模板渲染 ---------------- */

// 底部操作条的「使用模板」下拉
function renderTplOptions() {
  const sel = $('notifyTpl');
  sel.innerHTML = '';
  const ph = document.createElement('option');
  ph.value = '';
  ph.textContent = tpls.length ? '使用预设模板…' : '暂无预设模板';
  sel.appendChild(ph);
  tpls.forEach(t => {
    const op = document.createElement('option');
    op.value = t.id;
    op.textContent = t.name;
    sel.appendChild(op);
  });
}

// 管理弹窗的列表（含空状态提示）
function renderTplList() {
  const box = $('nfTplList');
  $('nfManageCount').textContent = tpls.length ? `共 ${tpls.length} 个` : '';
  if (!tpls.length) {
    box.innerHTML = '<div class="nf-tpl-empty"><b>还没有预设模板</b>' +
      '在通知内容里写好内容后，点右上角的「＋ 存入预设」保存一个，' +
      '下次从下方「使用预设模板」一键填入。</div>';
    return;
  }
  box.innerHTML = tpls.map(t => {
    const prev = String(t.content || '').replace(/\s+/g, ' ').slice(0, 48) || '（空内容）';
    return `<div class="nf-tpl-row">
      <div class="nf-tpl-main">
        <div class="nf-tpl-name">${esc(t.name)}</div>
        <div class="nf-tpl-prev">${esc(prev)}</div>
      </div>
      <div class="nf-tpl-acts">
        <button class="btn-ghost nf-tpl-rename" data-id="${esc(t.id)}">重命名</button>
        <button class="btn-ghost nf-tpl-del" data-id="${esc(t.id)}">删除</button>
      </div>
    </div>`;
  }).join('');

  box.querySelectorAll('.nf-tpl-rename').forEach(b => {
    b.onclick = () => openSaveModal('rename', b.dataset.id);
  });
  box.querySelectorAll('.nf-tpl-del').forEach(b => {
    b.onclick = () => delTpl(b.dataset.id);
  });
}

/* ---------------- 模板：保存 / 重命名 / 删除 ---------------- */

function openSaveModal(mode, id) {
  editingId = mode === 'rename' ? id : null;
  const t = tpls.find(x => x.id === id);
  $('nfSaveTitle').textContent = mode === 'rename' ? '重命名模板' : '存入预设';

  const content = mode === 'rename' && t ? t.content : $('notifyMsg').value.trim();
  if (!content) {
    showResult('err', '通知内容为空，先写点内容再存入预设');
    $('notifyMsg').focus();
    return;
  }
  $('nfTplName').value = mode === 'rename' && t ? t.name : '';
  $('nfSavePreview').textContent = content;
  $('nfTplNameHint').hidden = true;
  $('nfSaveMask').classList.add('on');
  $('nfTplName').focus();
}

function closeSaveModal() {
  $('nfSaveMask').classList.remove('on');
  editingId = null;
}

async function submitSaveTpl() {
  const name = $('nfTplName').value.trim();
  const hint = $('nfTplNameHint');
  if (!name) {
    hint.textContent = '模板名称不能为空';
    hint.hidden = false;
    $('nfTplName').focus();
    return;
  }
  // 重名校验：重命名时排除自己
  const dup = tpls.some(t => t.name === name && t.id !== editingId);
  if (dup) {
    hint.textContent = '已存在同名模板，请换一个名称';
    hint.hidden = false;
    $('nfTplName').focus();
    return;
  }
  hint.hidden = true;

  const wasRename = editingId !== null;
  const btn = $('nfSaveOk');
  btn.disabled = true;
  try {
    const j = wasRename
      ? await apiTpl({ action: 'rename', id: editingId, name })
      : await apiTpl({ action: 'add', name, content: $('nfSavePreview').textContent });
    if (j.ok) {
      await loadTpls();
      renderTplOptions();
      renderTplList();
      closeSaveModal();
      showResult('ok', '✅ ' + (j.message || (wasRename ? '已重命名' : '已存入预设')));
    } else {
      // 服务端兜底校验（如并发下重名）→ 提示留在弹窗里
      hint.textContent = j.message || '保存失败';
      hint.hidden = false;
    }
  } catch (e) {
    hint.textContent = '保存出错：' + e;
    hint.hidden = false;
  } finally {
    btn.disabled = false;
  }
}

async function delTpl(id) {
  const t = tpls.find(x => x.id === id);
  if (!t) return;
  if (!confirm(`确定删除预设模板「${t.name}」？删除后不可恢复。`)) return;
  try {
    const j = await apiTpl({ action: 'delete', id });
    if (j.ok) {
      await loadTpls();
      renderTplOptions();
      renderTplList();
      showResult('ok', '✅ ' + (j.message || '已删除'));
    } else {
      showResult('err', '❌ ' + (j.message || '删除失败'));
    }
  } catch (e) {
    showResult('err', '❌ 删除出错：' + e);
  }
}

/* ---------------- 初始化 ---------------- */

export async function initNotify() {
  const btn = $('btnNotifySend');
  if (!btn) return;

  const msgEl = $('notifyMsg');

  await loadTpls();
  renderTplOptions();
  renderTplList();

  // 输入即联动：清除旧的错误提示并刷新字数 / 按钮状态
  msgEl.addEventListener('input', () => {
    if (msgEl.value.trim() !== '') clearFieldError();
    refreshState();
  });

  // 选中模板 → 填入输入框（可二次编辑），随后复位下拉
  $('notifyTpl').onchange = e => {
    const id = e.target.value;
    if (!id) return;
    const t = tpls.find(x => x.id === id);
    if (t) {
      msgEl.value = t.content || '';
      clearFieldError();
      refreshState();
    }
    e.target.value = '';
  };

  $('btnTplSave').onclick = () => openSaveModal('save', null);
  $('btnTplManage').onclick = async () => {
    await loadTpls();
    renderTplList();
    $('nfManageMask').classList.add('on');
  };

  $('nfSaveOk').onclick = submitSaveTpl;
  $('nfSaveCancel').onclick = closeSaveModal;
  $('nfSaveClose').onclick = closeSaveModal;
  $('nfTplName').addEventListener('input', () => { $('nfTplNameHint').hidden = true; });
  $('nfManageDone').onclick = () => $('nfManageMask').classList.remove('on');
  $('nfManageClose').onclick = () => $('nfManageMask').classList.remove('on');

  btn.onclick = () => {
    const msg = msgEl.value.trim();
    if (!msg) {
      showFieldError('通知内容不能为空');
      showResult('err', '通知内容不能为空，请先填写再发送');
      msgEl.focus();
      return;
    }
    clearFieldError();
    if ($('notifyGroup').value === 'official') {
      openNotifyConfirm();
    } else {
      doSend('test', msg);
    }
  };

  $('notifyConfirmOk').onclick = () => {
    closeNotifyConfirm();
    doSend('official', msgEl.value.trim());
  };
  $('notifyCancel').onclick = closeNotifyConfirm;
  $('notifyClose').onclick = closeNotifyConfirm;

  refreshState();
}
