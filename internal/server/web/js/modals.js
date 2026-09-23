/* ================= 模型设置与应用设置弹窗 ================= */

import { $, esc, FM_NAME, doReveal, setPlatformWin } from './utils.js';
import { state } from './state.js';

/* ---------- 模型设置（大模型接入） ---------- */
export async function loadLLM() {
  try {
    state.LLM_SRV = await (await fetch('/api/llm/config')).json();
  } catch (e) {
    state.LLM_SRV = { configured: false, base_url: '', model: '' };
  }
  try {
    const r = await fetch('/api/llm/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reveal: true })
    });
    const j = await r.json();
    if (j.ok) state.LLM_SRV.api_key = j.api_key || '';
  } catch (e) {}
  syncLLMBadge();
}

export function syncLLMBadge() {
  const dot = $('llmDot');
  if (dot) dot.classList.toggle('on', !!state.LLM_SRV.configured);
  const item = $('smModel');
  if (item) {
    item.title = state.LLM_SRV.configured ? ('已配置：' + state.LLM_SRV.base_url) : '未配置模型';
  }
}

export async function migrateLocalLLM() {
  let old = null;
  try {
    old = JSON.parse(localStorage.getItem(state.LLM_STORE) || 'null');
  } catch (e) {
    old = null;
  }
  if (!old || !old.base_url || !old.api_key) return false;
  try {
    const r = await fetch('/api/llm/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ base_url: old.base_url, api_key: old.api_key, model: old.model || '' })
    });
    const j = await r.json();
    if (j.ok) {
      localStorage.removeItem(state.LLM_STORE);
      state.LLM_SRV = j;
      syncLLMBadge();
      return true;
    }
  } catch (e) {}
  return false;
}

export function llmForm() {
  return {
    base_url: $('llmBase')?.value.trim() || '',
    api_key: $('llmKey')?.value.trim() || '',
    model: $('llmModel')?.value.trim() || '',
    probe_path: $('llmPath')?.value.trim() || '',
  };
}

export function llmProbeBox(kind, msg, detail) {
  const box = $('llmProbe');
  if (!box) return;
  box.className = 'probe-box' + (kind ? (' ' + kind) : '');
  box.innerHTML = esc(msg) + (detail ? `<div class="pb-detail">${esc(detail)}</div>` : '');
}

export function llmInvalidate() {
  state.LLM_TESTED = false;
  const keyVal = $('llmKey')?.value.trim() || '';
  const srvKey = state.LLM_SRV.api_key || '';
  const saveBtn = $('llmSave');
  if (saveBtn) {
    saveBtn.disabled = !!(keyVal && keyVal !== srvKey);
    const stateEl = $('llmState');
    if (stateEl) stateEl.textContent = saveBtn.disabled ? '需重新测试' : 'Key 未改动，可直接保存';
  }
}

export async function openLlm() {
  const migrated = await migrateLocalLLM();
  if (!migrated) await loadLLM();
  if ($('llmBase')) $('llmBase').value = state.LLM_SRV.base_url || '';
  if ($('llmKey')) $('llmKey').value = state.LLM_SRV.api_key || '';
  if ($('llmModel')) $('llmModel').value = state.LLM_SRV.model || '';
  if ($('llmPath')) $('llmPath').value = '';
  if ($('llmKey')) {
    $('llmKey').placeholder = state.LLM_SRV.configured ? '已配置；留空保存则沿用服务端 Key' : 'sk-…';
    $('llmKey').type = 'password';
  }
  if ($('llmKeyToggle')) $('llmKeyToggle').textContent = '显示';

  state.LLM_TESTED = false;
  const saveBtn = $('llmSave');
  if (saveBtn) saveBtn.disabled = !state.LLM_SRV.configured;
  const stateEl = $('llmState');
  if (stateEl) {
    stateEl.textContent = state.LLM_SRV.configured ? '已自动回显服务端 Key（未改动可直接保存）' : '未配置';
  }
  llmProbeBox('', migrated ? '已将浏览器中的旧配置迁移到服务端' : '填写后点击「测试连接」进行校验');
  $('llmMask')?.classList.add('on');
}

export function closeLlm() {
  $('llmMask')?.classList.remove('on');
}

export async function testLLM() {
  const f = llmForm();
  if (!f.base_url) { llmProbeBox('err', '请填写模型供应商地址'); return; }
  if (!f.api_key) { llmProbeBox('err', '请填写 API Key'); return; }
  if (!f.model) { llmProbeBox('err', '请填写模型名（或端点 ID）：留空会让判分请求带上占位模型名而失败'); return; }

  const btn = $('llmTest');
  if (btn) btn.disabled = true;
  llmProbeBox('busy', '正在测试连接…');
  const stateEl = $('llmState');
  if (stateEl) stateEl.textContent = '测试中';

  try {
    const r = await fetch('/api/llm/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(f)
    });
    const raw = await r.text();
    let j;
    try { j = JSON.parse(raw); }
    catch (e) { j = { ok: false, message: `本地服务返回异常（HTTP ${r.status}）：${(raw || '').slice(0, 120)}` }; }

    if (j.ok) {
      state.LLM_TESTED = true;
      if ($('llmSave')) $('llmSave').disabled = false;
      if (stateEl) stateEl.textContent = '验证通过';
      llmProbeBox('ok', `✓ ${j.message}（${j.latency_ms}ms · 阶段 ${j.stage}）`, j.detail || '');
    } else {
      llmInvalidate();
      if (stateEl) stateEl.textContent = '验证失败';
      llmProbeBox('err', `✗ ${j.message || '测试失败'}${j.status ? `（HTTP ${j.status}）` : ''}`, j.detail || '');
    }
  } catch (e) {
    llmInvalidate();
    if (stateEl) stateEl.textContent = '请求失败';
    llmProbeBox('err', '请求本地服务失败：' + e.message);
  }
  if (btn) btn.disabled = false;
}

/* ---------- 应用设置（被测应用路径 / 会话日志目录） ---------- */
export function appCfgMsg(kind, text) {
  const box = $('appCfgState');
  if (!box) return;
  box.style.display = '';
  box.className = 'probe-box' + (kind ? (' ' + kind) : '');
  box.textContent = text;
}

export function appCfgHideMsg() {
  const box = $('appCfgState');
  if (box) box.style.display = 'none';
}

export async function openAppCfg() {
  $('appMask')?.classList.add('on');
  appCfgHideMsg();
  try {
    const d = await (await fetch('/api/app-settings')).json();
    if (d.error) { appCfgMsg('err', '读取失败：' + d.error); return; }
    fillAppCfg(d);
  } catch (e) {
    appCfgMsg('err', '请求失败：' + e.message);
  }
}

export function fillAppCfg(d) {
  setPlatformWin(!!d.is_windows);
  state.APP_CFG_DESC = d;
  if ($('appBinInp')) {
    $('appBinInp').value = d.binary.source === 'local' ? d.binary.value : '';
    $('appBinInp').placeholder = d.binary.value || '(空)';
  }
  if ($('appLogInp')) {
    $('appLogInp').value = d.session_root.source === 'local' ? d.session_root.value : '';
    $('appLogInp').placeholder = d.session_root.value || '(空)';
  }
  if ($('wsInp')) {
    $('wsInp').value = d.workspace.source === 'local' ? d.workspace.value : '';
    $('wsInp').placeholder = d.workspace.value || '(空)';
  }
  const def = d.defaults || {};
  if ($('appBinHint')) {
    $('appBinHint').textContent = '留空使用内置默认：' + (def.binary || d.binary.value || '(空)');
  }
  if ($('appLogHint')) {
    $('appLogHint').textContent = '被测应用产生的日志地址（sess_* 会话目录的父目录）。'
      + '留空使用内置默认：' + (def.session_root || d.session_root.value || '(空)');
  }
  ['appBinReveal', 'appLogReveal', 'wsReveal'].forEach(id => {
    const b = $(id);
    if (b) b.title = `在${FM_NAME()}中打开当前生效路径`;
  });
}

export function closeAppCfg() {
  $('appMask')?.classList.remove('on');
}

export function revealCfgPath(kind) {
  const it = state.APP_CFG_DESC && state.APP_CFG_DESC[kind];
  if (!it || !it.value) { alert('当前无生效路径'); return; }
  doReveal(it.value);
}

/* ---------- 注册弹窗事件 ---------- */
export function initModals() {
  const llmClose = $('llmClose');
  if (llmClose) llmClose.onclick = closeLlm;
  const llmCancel = $('llmCancel');
  if (llmCancel) llmCancel.onclick = closeLlm;
  const llmMask = $('llmMask');
  if (llmMask) llmMask.onclick = e => { if (e.target === llmMask) closeLlm(); };

  const llmClear = $('llmClear');
  if (llmClear) {
    llmClear.onclick = async () => {
      if (!confirm('清空服务端已保存的模型配置？')) return;
      try {
        await fetch('/api/llm/config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ clear: true })
        });
      } catch (e) {}
      try { localStorage.removeItem(state.LLM_STORE); } catch (e) {}
      state.LLM_SRV = { configured: false, base_url: '', model: '' };
      ['llmBase', 'llmKey', 'llmModel', 'llmPath'].forEach(id => { if ($(id)) $(id).value = ''; });
      state.LLM_TESTED = false;
      if ($('llmSave')) $('llmSave').disabled = true;
      if ($('llmState')) $('llmState').textContent = '已清空';
      syncLLMBadge();
      llmProbeBox('', '已清空服务端配置');
    };
  }

  const llmTest = $('llmTest');
  if (llmTest) llmTest.onclick = testLLM;

  const keyToggle = $('llmKeyToggle');
  if (keyToggle) {
    keyToggle.onclick = () => {
      const el = $('llmKey');
      if (!el) return;
      const show = el.type === 'password';
      el.type = show ? 'text' : 'password';
      keyToggle.textContent = show ? '隐藏' : '显示';
    };
  }

  const advToggle = $('llmAdvToggle');
  if (advToggle) {
    advToggle.onclick = () => {
      const w = $('llmAdvWrap');
      if (!w) return;
      w.classList.toggle('show');
      advToggle.textContent = (w.classList.contains('show') ? '▾ ' : '▸ ') + '高级：自定义探活路径';
    };
  }

  const llmSave = $('llmSave');
  if (llmSave) {
    llmSave.onclick = async () => {
      const f0 = llmForm();
      if (!state.LLM_TESTED && f0.api_key && f0.api_key !== (state.LLM_SRV.api_key || '')) {
        alert('API Key 已修改，请先完成「测试连接」再保存'); return;
      }
      const f = llmForm();
      llmSave.disabled = true;
      try {
        const body = { base_url: f.base_url, model: f.model, keep_key: true };
        if (f.api_key) body.api_key = f.api_key;
        const r = await fetch('/api/llm/config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body)
        });
        const j = await r.json();
        if (!j.ok) { alert('保存失败：' + (j.message || '未知错误')); llmSave.disabled = false; return; }
        state.LLM_SRV = j;
        syncLLMBadge();
        if ($('llmState')) $('llmState').textContent = '已保存';
        llmProbeBox('ok', '✓ 已保存到服务端（0600 权限，不进版本库）');
        setTimeout(closeLlm, 600);
      } catch (e) {
        alert('请求失败：' + e.message);
        llmSave.disabled = false;
      }
    };
  }

  ['llmBase', 'llmKey', 'llmModel', 'llmPath'].forEach(id => {
    $(id)?.addEventListener('input', () => { if (state.LLM_TESTED) llmInvalidate(); });
  });

  const appClose = $('appCfgClose');
  if (appClose) appClose.onclick = closeAppCfg;
  const appCancel = $('appCfgCancel');
  if (appCancel) appCancel.onclick = closeAppCfg;
  const appMask = $('appMask');
  if (appMask) appMask.onclick = e => { if (e.target === appMask) closeAppCfg(); };

  const appSave = $('appCfgSave');
  if (appSave) {
    appSave.onclick = async () => {
      appSave.disabled = true;
      try {
        const r = await fetch('/api/app-settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            app_binary: $('appBinInp')?.value.trim() || '',
            session_root: $('appLogInp')?.value.trim() || '',
            user_workspace: $('wsInp')?.value.trim() || ''
          })
        });
        const d = await r.json().catch(() => ({ ok: false, message: '响应解析失败' }));
        if (!d.ok) {
          appCfgMsg('err', d.message || '保存失败');
        } else {
          fillAppCfg(d);
          appCfgMsg('ok', '✓ 已保存（下次运行测试生效）');
        }
      } catch (e) {
        appCfgMsg('err', '请求失败：' + e.message);
      }
      appSave.disabled = false;
    };
  }

  const appReset = $('appCfgReset');
  if (appReset) {
    appReset.onclick = async () => {
      if (!confirm('清除本机覆盖（应用路径 / 日志目录），恢复默认配置？')) return;
      try {
        const r = await fetch('/api/app-settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ reset: true })
        });
        const d = await r.json().catch(() => null);
        if (d && d.ok) {
          if ($('appBinInp')) $('appBinInp').value = '';
          if ($('appLogInp')) $('appLogInp').value = '';
          if ($('wsInp')) $('wsInp').value = '';
          fillAppCfg(d);
          appCfgMsg('ok', '✓ 已恢复默认配置');
        }
      } catch (e) {}
    };
  }
}
