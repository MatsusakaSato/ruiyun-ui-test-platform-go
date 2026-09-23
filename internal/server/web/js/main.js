/* ================= 应用启动入口与公共方法导出 ================= */

import { $, esc, revealInFinder } from './utils.js';
import { state } from './state.js';
import { initConsole } from './console.js';
import { initNav, switchPage, renderHistoryPane, loadEnv, refreshAppStatus, loadAutoClick } from './nav.js';
import { initHome, renderRunCases, doRun, setRunning, renderStatus, goToLatestHistory } from './page-home.js';
import { initCases, loadUploads, pickAttach, uploadFiles, openLib, closeLib, rmAttach, savePreset, renderCases, delCase, setCaseTab, loadPresetLib } from './page-cases.js';
import { initExcelImport } from './excel-import.js';
import { initHistory, loadRounds, openRound, delRound, setTab, pickCase, startEval, stopEval, refreshEvalStatus, pollEval, openRoundById, copyRoundId } from './page-history.js';
import { initModals, loadLLM, openLlm, revealCfgPath } from './modals.js';

// 将 HTML 模板内联交互所依赖的函数挂载到 window，确保 100% 兼容
Object.assign(window, {
  switchPage,
  setCaseTab,
  doRun,
  goToLatestHistory,
  revealCfgPath,
  revealInFinder,
  rmAttach,
  pickAttach,
  openLib,
  closeLib,
  savePreset,
  delCase,
  uploadFiles,
  openRound,
  delRound,
  setTab,
  pickCase,
  startEval,
  stopEval,
  openLlm,
  openRoundById,
  copyRoundId
});

// 全局快捷键
document.addEventListener('keydown', e => {
  if (e.key === 'Escape') {
    closeLib();
  }
});

// 初始化启动
(async function init() {
  initConsole();
  initNav();
  initHome();
  initCases();
  initExcelImport();
  initHistory();
  initModals();

  try {
    const c = await (await fetch('/api/config')).json();
    const verEl = $('ver');
    if (verEl) verEl.textContent = c.app_name || '';
  } catch (e) {}

  const step = async fn => {
    try { await fn(); } catch (e) { console.error('init step failed:', e); }
  };

  await step(loadLLM);
  await step(loadAutoClick);
  await step(loadUploads);
  step(refreshEvalStatus).then(s => { if (s && s.running) pollEval(); });
  step(loadPresetLib);
  step(renderCases);
  await step(loadEnv);

  let saved = state.currentPage;
  try { saved = localStorage.getItem(state.PAGE_KEY) || state.currentPage; } catch (e) {}
  await step(loadRounds);
  switchPage(state.PAGES.indexOf(saved) >= 0 ? saved : 'home');

  try {
    const j = await (await fetch('/api/run/status')).json();
    renderStatus(j);
    if (j.running) {
      if (state.currentPage !== 'home') switchPage('home');
      setRunning(true);
    }
  } catch (e) {}

  setInterval(refreshAppStatus, 5000);
  if (state.currentPage === 'history') renderHistoryPane();
})();
