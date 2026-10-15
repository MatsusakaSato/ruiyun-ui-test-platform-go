/* ================= 发送通知页（钉钉群消息 + 预设模板） ================= */

import { $, esc } from './utils.js';

// 预设模板存服务端 SQLite（与预设用例同库，表 notify_templates）
const TPL_API = '/api/notify/templates';

// 群机器人配置（服务端工作区 .notify_secrets.json）
const CFG_API = '/api/notify/config';
// 发送前要二次确认的群，与后端 notify.ConfirmKey 保持一致
const CONFIRM_KEY = 'official';

// 是否正在发送（避免连点重复发送）
let sending = false;
let tpls = [];
// 管理列表中处于「就地编辑」的模板 id；null = 都不在编辑态
let editingId = null;

// 服务端生效的群列表（token / secret 已打码），收件人下拉按它渲染
let robots = [];
// 上面这份是不是内置兜底（用户还没自定义过）
let robotsBuiltin = true;
// 配置弹窗里的工作副本：增删行都只动它，点「保存」才落盘
let cfgRows = [];
// 弹窗打开后有没有增删过行（只改名字 / token 不算）
let cfgDirty = false;

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

// 按钮可用性：内容为空 / 没有收件人 / 正在发送 → 禁用。
// 「没有收件人」也要禁：发送接口收到空 group 会回落到正式群，那是真群，不能靠它兜底。
function refreshState() {
  const msg = $('notifyMsg').value.trim();
  $('notifyCount').textContent = msg.length + ' 字';
  $('btnNotifySend').disabled = sending || msg === '' || !$('notifyGroup').value;
}

function openNotifyConfirm() {
  // 群名可以改，标题跟着当前配置走，别写死「正式群」
  const name = groupName(CONFIRM_KEY);
  $('notifyConfirmTitle').textContent = '确认发送到' + name;
  $('notifyConfirmSub').textContent = '消息将直接发到' + name + '，所有成员可见';
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

// 管理弹窗的列表（含空状态提示）。
// 编辑是**在本窗口内就地展开**的：不另开弹窗，避免两层弹窗互相遮挡。
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
    if (editingId === t.id) {
      // 编辑态：名称 + 内容 + 保存/取消，都在这一行内展开
      return `<div class="nf-tpl-row editing">
        <div class="nf-tpl-edit">
          <input class="nf-edit-name" type="text" placeholder="模板名称" autocomplete="off"/>
          <textarea class="nf-edit-content" rows="5" placeholder="模板内容"></textarea>
          <div class="nf-edit-acts">
            <span class="fhint err nf-edit-hint" hidden></span>
            <span class="spacer"></span>
            <button class="btn-ghost nf-edit-cancel">取消</button>
            <button class="btn-primary nf-edit-save">保存</button>
          </div>
        </div>
      </div>`;
    }
    return `<div class="nf-tpl-row">
      <div class="nf-tpl-main">
        <div class="nf-tpl-name">${esc(t.name)}</div>
        <div class="nf-tpl-prev">${esc(prev)}</div>
      </div>
      <div class="nf-tpl-acts">
        <button class="btn-ghost nf-tpl-edit" data-id="${esc(t.id)}">编辑</button>
        <button class="btn-ghost nf-tpl-del" data-id="${esc(t.id)}">删除</button>
      </div>
    </div>`;
  }).join('');

  // 编辑态：回填原值并聚焦名称
  if (editingId) {
    const t = tpls.find(x => x.id === editingId);
    const nameEl = box.querySelector('.nf-edit-name');
    const contentEl = box.querySelector('.nf-edit-content');
    if (t && nameEl && contentEl) {
      nameEl.value = t.name || '';
      contentEl.value = t.content || '';
      nameEl.focus();
      nameEl.select();
    }
    const cancelBtn = box.querySelector('.nf-edit-cancel');
    if (cancelBtn) cancelBtn.onclick = () => { editingId = null; renderTplList(); };

    const saveBtn = box.querySelector('.nf-edit-save');
    if (saveBtn) saveBtn.onclick = () => submitEditTpl(editingId);
    return;
  }

  box.querySelectorAll('.nf-tpl-edit').forEach(b => {
    b.onclick = () => { editingId = b.dataset.id; renderTplList(); };
  });
  box.querySelectorAll('.nf-tpl-del').forEach(b => {
    b.onclick = () => delTpl(b.dataset.id);
  });
}

// 提交就地编辑（名称 + 内容）
async function submitEditTpl(id) {
  const box = $('nfTplList');
  const nameEl = box.querySelector('.nf-edit-name');
  const contentEl = box.querySelector('.nf-edit-content');
  const hintEl = box.querySelector('.nf-edit-hint');
  const name = (nameEl.value || '').trim();
  const content = (contentEl.value || '').trim();

  if (!name) {
    hintEl.textContent = '模板名称不能为空';
    hintEl.hidden = false;
    nameEl.focus();
    return;
  }
  if (!content) {
    hintEl.textContent = '模板内容不能为空';
    hintEl.hidden = false;
    contentEl.focus();
    return;
  }
  const dup = tpls.some(t => t.name === name && t.id !== id);
  if (dup) {
    hintEl.textContent = '已存在同名模板，请换一个名称';
    hintEl.hidden = false;
    nameEl.focus();
    return;
  }
  hintEl.hidden = true;

  const btn = box.querySelector('.nf-edit-save');
  btn.disabled = true;
  try {
    const j = await apiTpl({ action: 'update', id, name, content });
    if (j.ok) {
      editingId = null;
      await loadTpls();
      renderTplOptions();
      renderTplList();
      showResult('ok', '✅ ' + (j.message || '已保存'));
    } else {
      hintEl.textContent = j.message || '保存失败';
      hintEl.hidden = false;
    }
  } catch (e) {
    hintEl.textContent = '保存出错：' + e;
    hintEl.hidden = false;
  } finally {
    btn.disabled = false;
  }
}

/* ---------------- 模板：新增 / 就地编辑 / 删除 ---------------- */

// 存入预设弹窗（只负责新增；改模板走管理列表里的就地编辑）
function openSaveModal() {
  editingId = null;
  $('nfSaveTitle').textContent = '存入预设';

  const content = $('notifyMsg').value.trim();
  if (!content) {
    showResult('err', '通知内容为空，先写点内容再存入预设');
    $('notifyMsg').focus();
    return;
  }
  $('nfTplName').value = '';
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

  const btn = $('nfSaveOk');
  btn.disabled = true;
  try {
    const j = await apiTpl({ action: 'add', name, content: $('nfSavePreview').textContent });
    if (j.ok) {
      await loadTpls();
      renderTplOptions();
      renderTplList();
      closeSaveModal();
      showResult('ok', '✅ ' + (j.message || '已存入预设'));
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

/* ---------------- 群机器人配置（齿轮弹窗） ----------------
   落盘在服务端工作区 .notify_secrets.json（0600），与 LLM Key 一个路子：
   token 只回显打码提示，要明文得单独点「显示」。 */

async function apiCfg(payload) {
  const r = await fetch(CFG_API, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  });
  return await r.json();
}

async function loadRobots() {
  try {
    const j = await (await fetch(CFG_API)).json();
    robots = Array.isArray(j.robots) ? j.robots : [];
    robotsBuiltin = !!j.builtin;
  } catch (e) {
    robots = [];
    robotsBuiltin = true;
  }
}

// 接口返回值就是保存后的最新列表，直接拿来用，不必再请求一次
function applyCfgResponse(j) {
  robots = Array.isArray(j.robots) ? j.robots : [];
  robotsBuiltin = !!j.builtin;
  renderGroupOptions();
}

function groupName(key) {
  const r = robots.find(x => x.key === key);
  return (r && r.name) || key || '';
}

// 收件人下拉：按配置的群渲染。保不住原来的选中项（群被删了）就落到第一个，
// 别让下拉停在空值上 —— 发送接口收到空 group 会回落到「正式群」，等于发错群。
function renderGroupOptions() {
  const sel = $('notifyGroup');
  const keep = sel.value;
  sel.innerHTML = '';
  robots.forEach(r => {
    const op = document.createElement('option');
    op.value = r.key;
    op.textContent = r.name || r.key;
    sel.appendChild(op);
  });
  if (robots.some(r => r.key === keep)) sel.value = keep;
  updateGroupHint();
  refreshState();
}

function updateGroupHint() {
  $('notifyGroupHint').textContent =
    $('notifyGroup').value === CONFIRM_KEY ? '发送前会弹确认窗口' : '可直接发送';
}

function showCfgHint(msg) {
  const h = $('nfCfgHint');
  h.textContent = msg;
  h.hidden = false;
}

async function openNotifyCfg() {
  await loadRobots();
  cfgRows = robots.map(r => ({
    key: r.key,
    name: r.name || '',
    token: '',
    secret: '',
    tokenHint: r.token_hint || '',
    secretHint: r.secret_hint || '',
    isNew: false
  }));
  $('nfCfgHint').hidden = true;
  cfgDirty = false;
  renderCfgList();
  $('notifyCfgMask').classList.add('on');
}

function closeNotifyCfg() {
  $('notifyCfgMask').classList.remove('on');
  $('nfCfgHint').hidden = true;
  cfgRows = [];
  cfgDirty = false;
}

// 把列表里已敲进去的值收回工作副本。
// 增删行都会整块重渲染 innerHTML，不收一遍的话别的行刚填的 token 就没了。
function syncCfgRows() {
  $('nfCfgList').querySelectorAll('.nf-cfg-row').forEach(el => {
    const r = cfgRows[+el.dataset.i];
    if (!r) return;
    const name = el.querySelector('.nf-cfg-name');
    const token = el.querySelector('.nf-cfg-token');
    const secret = el.querySelector('.nf-cfg-secret');
    if (name) r.name = name.value;
    if (token) r.token = token.value;
    if (secret) r.secret = secret.value;
  });
}

// 新行的 key 必须在新增时就定死且不撞现有 key：
// 留空交给后端按序号生成的话，撞上同名老 key 会被当成「老群」而悄悄沿用它的 token。
function nextCfgKey() {
  const used = new Set(cfgRows.map(r => r.key));
  for (let n = 1; ; n++) {
    const k = 'custom-' + n;
    if (!used.has(k)) return k;
  }
}

function renderCfgList() {
  const box = $('nfCfgList');
  // 副标题：没增删过才敢说「内置默认」，加了行就是自定义了
  $('nfCfgSub').textContent = robotsBuiltin && !cfgDirty ? '内置默认' : `${cfgRows.length} 个群`;
  if (!cfgRows.length) {
    box.innerHTML = '<div class="nf-cfg-empty">还没有群，点下面的「＋ 添加群」建一个</div>';
    return;
  }
  box.innerHTML = cfgRows.map((r, i) => `
    <div class="nf-cfg-row" data-i="${i}">
      <div class="nf-cfg-hd">
        <input class="nf-cfg-name" type="text" value="${esc(r.name)}" placeholder="群名称"
               autocomplete="off" spellcheck="false"/>
        <button class="btn-ghost nf-cfg-del" type="button">删除</button>
      </div>
      <div class="fld">
        <label>Token</label>
        <div class="pw-row">
          <input class="nf-cfg-token" type="password" value="${esc(r.token)}"
                 placeholder="${r.tokenHint ? '已保存 ' + esc(r.tokenHint) + '，'
                                           : '机器人 Webhook 里的 access_token'}"
                 autocomplete="off" spellcheck="false"/>
          ${r.isNew ? '' : '<button class="btn-ghost nf-cfg-reveal" type="button">显示</button>'}
        </div>
      </div>
      <div class="fld">
        <label>加签密钥（选填）</label>
        <div class="pw-row">
          <input class="nf-cfg-secret" type="password" value="${esc(r.secret)}"
                 placeholder="${r.secretHint ? '已保存 ' + esc(r.secretHint) + '，'
                                            : 'SEC 开头；留空则按自定义关键词发送'}"
                 autocomplete="off" spellcheck="false"/>
        </div>
      </div>
    </div>`).join('');

  box.querySelectorAll('.nf-cfg-del').forEach(b => {
    b.onclick = () => {
      syncCfgRows();
      cfgRows.splice(+b.closest('.nf-cfg-row').dataset.i, 1);
      cfgDirty = true;
      $('nfCfgHint').hidden = true;
      renderCfgList();
    };
  });
  box.querySelectorAll('.nf-cfg-reveal').forEach(b => {
    b.onclick = () => toggleReveal(b.closest('.nf-cfg-row'));
  });
}

function addCfgRow() {
  syncCfgRows();
  cfgRows.push({
    key: nextCfgKey(), name: '', token: '', secret: '',
    tokenHint: '', secretHint: '', isNew: true
  });
  cfgDirty = true;
  $('nfCfgHint').hidden = true;
  renderCfgList();
  const rows = $('nfCfgList').querySelectorAll('.nf-cfg-row');
  const last = rows[rows.length - 1];
  if (last) last.querySelector('.nf-cfg-name').focus();
}

// 「显示 / 隐藏」已保存的明文。只补空位：用户已敲进新值就别拿旧值盖掉。
async function toggleReveal(rowEl) {
  const r = cfgRows[+rowEl.dataset.i];
  if (!r) return;
  const btn = rowEl.querySelector('.nf-cfg-reveal');
  const tokenEl = rowEl.querySelector('.nf-cfg-token');
  const secretEl = rowEl.querySelector('.nf-cfg-secret');

  if (btn.dataset.on === '1') {
    btn.dataset.on = '';
    btn.textContent = '显示';
    tokenEl.type = 'password';
    secretEl.type = 'password';
    tokenEl.value = '';
    secretEl.value = '';
    return;
  }
  btn.disabled = true;
  try {
    const j = await apiCfg({ action: 'reveal', key: r.key });
    if (!j.ok) {
      showCfgHint(j.message || '取不到明文');
      return;
    }
    if (!tokenEl.value) tokenEl.value = j.token || '';
    if (!secretEl.value) secretEl.value = j.secret || '';
    tokenEl.type = 'text';
    secretEl.type = 'text';
    btn.dataset.on = '1';
    btn.textContent = '隐藏';
  } catch (e) {
    showCfgHint('取明文出错：' + e);
  } finally {
    btn.disabled = false;
  }
}

async function saveNotifyCfg() {
  syncCfgRows();
  // 新群没有原值可沿用，当场就得给 token（老的留空表示不改动）
  const blank = cfgRows.find(r => !r.token && !r.tokenHint);
  if (blank) {
    showCfgHint(blank.name ? `「${blank.name}」还没填 Token` : '有群还没填 Token');
    return;
  }
  const btn = $('nfCfgSave');
  btn.disabled = true;
  try {
    const j = await apiCfg({
      action: 'save',
      robots: cfgRows.map(r => ({ key: r.key, name: r.name, token: r.token, secret: r.secret }))
    });
    if (!j.ok) {
      showCfgHint(j.message || '保存失败');
      return;
    }
    // 保存成功不回绿色提示条：关掉弹窗 + 收件人下拉跟着变，本身就是结果
    applyCfgResponse(j);
    closeNotifyCfg();
  } catch (e) {
    showCfgHint('保存出错：' + e);
  } finally {
    btn.disabled = false;
  }
}

async function resetNotifyCfg() {
  if (!confirm('恢复成内置的正式群 / 测试群？自定义的群和 Token 会被清掉。')) return;
  const btn = $('nfCfgReset');
  btn.disabled = true;
  try {
    const j = await apiCfg({ action: 'clear' });
    if (!j.ok) {
      showCfgHint(j.message || '恢复失败');
      return;
    }
    applyCfgResponse(j);
    closeNotifyCfg();
  } catch (e) {
    showCfgHint('恢复出错：' + e);
  } finally {
    btn.disabled = false;
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

  await loadRobots();
  renderGroupOptions();
  $('notifyGroup').onchange = updateGroupHint;

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

  $('btnTplSave').onclick = () => openSaveModal();
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

  // 齿轮：群机器人配置
  $('btnNotifyCfg').onclick = openNotifyCfg;
  $('nfCfgAdd').onclick = addCfgRow;
  $('nfCfgSave').onclick = saveNotifyCfg;
  $('nfCfgReset').onclick = resetNotifyCfg;
  $('nfCfgCancel').onclick = closeNotifyCfg;
  $('nfCfgClose').onclick = closeNotifyCfg;

  const cfgMask = $('notifyCfgMask');
  cfgMask.onclick = e => { if (e.target === e.currentTarget) closeNotifyCfg(); };
  cfgMask.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      closeNotifyCfg();
      return;
    }
    // Ctrl/⌘ + 回车 = 保存，与新增用例对话框一致
    if (e.key !== 'Enter' || e.isComposing || !(e.ctrlKey || e.metaKey)) return;
    e.preventDefault();
    saveNotifyCfg();
  });

  btn.onclick = () => {
    const msg = msgEl.value.trim();
    if (!msg) {
      showFieldError('通知内容不能为空');
      showResult('err', '通知内容不能为空，请先填写再发送');
      msgEl.focus();
      return;
    }
    clearFieldError();
    const group = $('notifyGroup').value;
    if (group === CONFIRM_KEY) {
      openNotifyConfirm();
    } else {
      doSend(group, msg);
    }
  };

  $('notifyConfirmOk').onclick = () => {
    closeNotifyConfirm();
    doSend(CONFIRM_KEY, msgEl.value.trim());
  };
  $('notifyCancel').onclick = closeNotifyConfirm;
  $('notifyClose').onclick = closeNotifyConfirm;

  refreshState();
}
