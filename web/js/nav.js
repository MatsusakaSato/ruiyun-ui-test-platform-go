/* ================= 侧边栏导航、设置菜单与环境管理 ================= */

import { $, esc } from './utils.js';
import { state } from './state.js';
import { openRoundById } from './page-history.js';
import { openAppCfg, openLlm } from './modals.js';

export function switchPage(name) {
  if (state.PAGES.indexOf(name) < 0) name = 'home';
  state.currentPage = name;
  state.PAGES.forEach(p => {
    const el = $('page' + p.charAt(0).toUpperCase() + p.slice(1));
    if (el) {
      el.classList.toggle('on', p === name);
      el.hidden = (p !== name);
    }
  });
  document.querySelectorAll('.nav-item').forEach(b => {
    b.classList.toggle('on', b.dataset.page === name);
  });
  document.querySelector('.main')?.classList.toggle('main-history', name === 'history');
  try { localStorage.setItem(state.PAGE_KEY, name); } catch (e) {}
  if (name === 'history') renderHistoryPane();
}

export function renderHistoryPane() {
  if (state.currentRound || !(state.lastRounds && state.lastRounds.length)) return;
  openRoundById(state.lastRounds[0].run_id);
}

/* ---------- 环境与应用实例 ---------- */
export async function loadEnv() {
  let j = null;
  try {
    j = await (await fetch('/api/env')).json();
  } catch (e) {
    envLoadFailed('无法连接本地服务：' + e.message);
    return;
  }
  if (!j || j.error) {
    envLoadFailed((j && j.error) || '服务返回异常');
    return;
  }
  const list = Array.isArray(j.profiles) ? j.profiles : [];
  if (!list.length) {
    envLoadFailed(`服务没有下发任何环境档案（${j.config_path || 'config.yaml'} 读取失败？）`);
    return;
  }
  state.ENV_ERR = '';
  state.ENV_CFG = j;
  const sel = $('optEnv');
  if (sel) {
    sel.innerHTML = list.map(p =>
      `<option value="${esc(p.key)}" title="${esc(p.desc)}">${esc(p.label)}</option>`).join('');
    if (list.some(p => p.key === j.current)) sel.value = j.current;
    else sel.selectedIndex = 0;
  }
  $('wrapEnv')?.removeAttribute('title');
  syncEnvUI();
}

export function envLoadFailed(msg) {
  state.ENV_ERR = msg;
  state.ENV_CFG = { profiles: [], current: '', app_running: false };
  const sel = $('optEnv');
  if (sel) {
    sel.innerHTML = '<option value="">环境档案不可用</option>';
    sel.value = '';
  }
  const wrap = $('wrapEnv');
  if (wrap) wrap.title = msg;
  syncEnvUI();
}

export function syncEnvUI() {
  if (!state.ENV_CFG) return;
  const running = !!state.ENV_CFG.app_running;
  const optEnv = $('optEnv');
  if (optEnv) optEnv.disabled = running || !!state.ENV_ERR;
  $('wrapEnv')?.classList.toggle('dimmed', running || !!state.ENV_ERR);

  const closable = running && !state.RUNNING;
  const btnClose = $('btnCloseApp');
  if (btnClose) {
    btnClose.disabled = !closable;
    btnClose.title = running
      ? (state.RUNNING ? '有测试正在运行，无法关闭' : '关闭应用实例')
      : '应用当前未运行';
  }
}

export async function refreshAppStatus() {
  try {
    const j = await (await fetch('/api/app/status')).json();
    if (state.ENV_CFG) {
      state.ENV_CFG.app_running = !!j.running;
      syncEnvUI();
    }
  } catch (e) {}
}

/* ---------- 自动点击确认 ---------- */
export function syncAutoClickUI() {
  const sw = $('swAutoClick');
  if (sw) sw.classList.toggle('on', state.AUTO_CLICK);
  const item = $('smAutoClick');
  if (item) {
    item.disabled = state.RUNNING;
    item.title = (state.RUNNING ? '测试运行中，改动会在下次运行生效：' : '')
      + '开启后，平台会替你点击确认卡片、作答选项卡（拒绝/取消类按钮永不点）';
  }
}

export async function loadAutoClick() {
  try {
    const c = await (await fetch('/api/config')).json();
    if (typeof c.auto_confirm === 'boolean') state.AUTO_CLICK = c.auto_confirm;
  } catch (e) {}
  syncAutoClickUI();
}

/* ---------- 并发数 ---------- */
export function clampInflight(v) {
  if (v === null || v === undefined || String(v).trim() === '') return state.INFLIGHT;
  v = Math.floor(Number(v));
  if (!Number.isFinite(v)) return state.INFLIGHT;
  return Math.min(state.INFLIGHT_MAX, Math.max(state.INFLIGHT_MIN, v));
}

export function syncInflightUI() {
  const valEl = $('smInflightVal');
  if (valEl) valEl.textContent = state.INFLIGHT;
  const inp = $('inflightInput');
  if (inp && document.activeElement !== inp) inp.value = state.INFLIGHT;
}

export function toggleInflightPanel() {
  const p = $('inflightPanel');
  if (!p) return;
  const on = !p.classList.contains('on');
  p.classList.toggle('on', on);
  if (on) $('inflightInput')?.focus();
}

/* ---------- 导航与菜单事件初始化 ---------- */
export function initNav() {
  const optEnv = $('optEnv');
  if (optEnv) {
    optEnv.onchange = async () => {
      const key = optEnv.value;
      try {
        const r = await fetch('/api/env', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ profile: key })
        });
        const j = await r.json();
        if (!j.ok) {
          alert('切换环境失败：' + (j.message || '未知错误'));
          await loadEnv();
          return;
        }
        state.ENV_CFG = j;
        syncEnvUI();
        const label = ((j.profiles || []).find(p => p.key === j.current) || {}).label
          || (j.current ? j.current : '开发');
        alert(`已切换到「${label}」\n\n下次启动应用时生效。`);
      } catch (e) {
        alert('请求失败：' + e.message);
        await loadEnv();
      }
    };
  }

  const btnClose = $('btnCloseApp');
  if (btnClose) {
    btnClose.onclick = async () => {
      if (!confirm('确定关闭应用实例？\n\n关闭后可以重新选择环境，下次运行测试时会按所选环境启动。')) return;
      btnClose.disabled = true;
      try {
        const r = await fetch('/api/app/close', { method: 'POST' });
        const j = await r.json();
        if (!j.ok) { alert('关闭失败：' + (j.message || '未知错误')); }
        await refreshAppStatus();
      } catch (e) {
        alert('请求失败：' + e.message);
      }
      await refreshAppStatus();
    };
  }

  const btnSettings = $('btnSettings');
  if (btnSettings) {
    btnSettings.onclick = e => {
      e.stopPropagation();
      $('settingsMenu')?.classList.toggle('on');
    };
  }

  document.addEventListener('click', e => {
    const m = $('settingsMenu');
    if (m && m.classList.contains('on') && !e.target.closest('.settings-wrap')) {
      m.classList.remove('on');
    }
  });

  const menu = $('settingsMenu');
  if (menu) {
    menu.onclick = e => {
      const b = e.target.closest('.sm-item');
      if (!b) return;
      const act = b.dataset.act;
      if (act === 'inflight') { toggleInflightPanel(); return; }
      if (act === 'autoclick') {
        if (b.disabled) return;
        state.AUTO_CLICK = !state.AUTO_CLICK;
        syncAutoClickUI();
        return;
      }
      menu.classList.remove('on');
      if (act === 'app') openAppCfg();
      else if (act === 'model') openLlm();
    };
  }

  const inflightInp = $('inflightInput');
  if (inflightInp) {
    inflightInp.addEventListener('input', () => {
      if (inflightInp.value.trim() !== '') state.INFLIGHT = clampInflight(inflightInp.value);
      const valEl = $('smInflightVal');
      if (valEl) valEl.textContent = state.INFLIGHT;
    });
    inflightInp.addEventListener('blur', () => syncInflightUI());
    inflightInp.addEventListener('keydown', e => {
      if (e.key === 'Enter') { e.preventDefault(); syncInflightUI(); }
    });
  }

  const inflightPanel = $('inflightPanel');
  if (inflightPanel) inflightPanel.onclick = e => e.stopPropagation();

  syncInflightUI();
}
