/* ================= 首页运行台与测试控制 ================= */

import { $, esc } from './utils.js';
import { state } from './state.js';
import { noteConsoleContent, setConsoleOpen, renderConsoleLines, syncConsoleNew } from './console.js';
import { switchPage, syncEnvUI, syncAutoClickUI } from './nav.js';
import { syncCaseState } from './page-cases.js';
import { loadRounds, openRoundById } from './page-history.js';

export function renderRunCases() {
  const box = $('runCases');
  if (!box) return;
  const n = state.CASES.filter(c => c.prompt.trim()).length;
  const qCnt = $('queueCount');
  if (qCnt) qCnt.textContent = n ? `（${n} 条）` : '';

  if (!state.RUN_CASES.length) {
    box.innerHTML = '<div class="run-empty">'
      + (state.RUNNING
        ? '<div style="display:flex;align-items:center;gap:9px;padding:12px 4px"><span class="spinner-blue"></span><span>正在启动测试，用例即将列出…</span></div>'
        : '还没有要跑的用例。到 '
          + '<button class="home-link" onclick="switchPage(\'cases\')">测试用例</button>'
          + ' 页从预设库里勾选加入本轮，或手动添加一行。')
      + '</div>';
    const hlWrap = $('homeHistLinkWrap');
    if (hlWrap) hlWrap.innerHTML = '';
    return;
  }

  const total = state.RUN_CASES.length;
  const done = state.RUN_CASES.filter(c => c.status).length;
  const passCount = state.RUN_CASES.filter(c => c.status === 'PASS').length;
  const failCount = state.RUN_CASES.filter(c => c.status && c.status !== 'PASS').length;
  const isAllDone = total > 0 && !state.RUNNING && done === total;
  const firstUnfinished = state.RUN_CASES.findIndex(c => !c.status);
  const inflight = Math.max(1, typeof state.INFLIGHT === 'number' ? state.INFLIGHT : 1);

  let topHtml = '';
  if (state.RUNNING) {
    const pct = total ? Math.round((done / total) * 100) : 0;
    topHtml = `<div class="run-progress-box">
      <div class="rpb-header">
        <div class="rpb-title">
          <span class="spinner-blue"></span>
          <span>测试执行中</span>
          <span class="rpb-counts">（已完成 ${done} / ${total} 条）</span>
        </div>
        <div class="rpb-pct">${pct}%</div>
      </div>
      <div class="rpb-track">
        <div class="rpb-bar" style="width:${Math.max(pct, 5)}%"></div>
      </div>
    </div>`;
  } else if (isAllDone) {
    const isFullPass = (passCount === total);
    const bannerCls = isFullPass ? 'banner-ok' : 'banner-warn';
    const icon = isFullPass ? '✓' : '⚠️';
    const title = isFullPass ? '本轮用例全部测试通过！' : '本轮用例测试完成（部分未通过）';
    const statsText = isFullPass
      ? `共 <b>${total}</b> 条用例全部通过。测试结果与排查数据已归档。`
      : `共 <b>${total}</b> 条用例：<span class="t-pass">${passCount} 通过</span> · <span class="t-fail">${failCount} 未通过</span>。测试结果与排查数据已归档。`;

    topHtml = `<div class="run-done-banner ${bannerCls}">
      <div class="rdb-left">
        <div class="rdb-title">
          <span class="rdb-icon">${icon}</span>
          <span>${title}</span>
        </div>
        <div class="rdb-stats">${statsText}</div>
      </div>
      <button class="rdb-btn" onclick="goToLatestHistory()" title="跳转至历史记录第一条查看本轮测试报告">查看</button>
    </div>`;
  } else {
    topHtml = `<div class="run-empty" style="padding:0 2px 8px">共 ${total} 条${done ? `，已完成 ${done} 条` : ''}</div>`;
  }

  const rowsHtml = state.RUN_CASES.map((c, i) => {
    let badgeHtml = '';
    let rowCls = 'run-row';
    if (c.status === 'PASS') {
      badgeHtml = '<span class="badge b-ok">✓ 通过</span>';
    } else if (c.status === 'FAIL') {
      badgeHtml = '<span class="badge b-fail">✕ 断言失败</span>';
    } else if (c.status === 'UI_FAIL') {
      badgeHtml = '<span class="badge b-uifail">⚠ UI 失败</span>';
    } else if (state.RUNNING) {
      if (firstUnfinished !== -1 && i >= firstUnfinished && i < firstUnfinished + inflight) {
        badgeHtml = '<span class="badge b-executing"><span class="spinner-inline"></span> 测试中…</span>';
        rowCls += ' is-executing';
      } else {
        badgeHtml = '<span class="badge b-idle"><span class="dot-pulse"></span> 排队中</span>';
      }
    } else {
      badgeHtml = '<span class="badge b-idle">等待中</span>';
    }
    return `<div class="${rowCls}">
      <span class="rr-no">${String(i + 1).padStart(2, '0')}</span>
      <span class="rr-prompt" title="${esc(c.prompt || '')}">${esc(c.prompt || '（空）')}</span>
      ${c.elapsed_s != null ? `<span class="rr-time">${c.elapsed_s}s</span>` : ''}
      ${badgeHtml}
    </div>`;
  }).join('');

  box.innerHTML = topHtml + rowsHtml;
}

export async function startRun(body) {
  if (state.RUNNING) return;
  state.LATEST_RUN_ID = '';
  const r = await fetch('/api/run', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  const j = await r.json();
  if (!j.ok) { alert(j.message); return; }

  const cons = $('console');
  if (cons) cons.textContent = '';
  state.CONSOLE_PENDING = 0;
  noteConsoleContent(true);
  setConsoleOpen(true);
  switchPage('home');

  state.RUN_CASES = state.CASES.filter(c => c.prompt.trim()).map((c, i) => ({
    case_id: 'CASE-' + String(i + 1).padStart(3, '0'),
    prompt: c.prompt.trim(),
    status: '',
    elapsed_s: null
  }));
  renderRunCases();
  setRunning(true);
}

export function doRun() {
  if (state.RUNNING) { alert('已有测试在运行，请等它结束或先终止'); return; }
  const items = state.CASES.filter(c => c.prompt.trim())
    .map((c, i) => ({
      id: 'CASE-' + String(i + 1).padStart(3, '0'),
      prompt: c.prompt.trim(),
      labels: c.labels || {},
      attachments: (c.attachments || []).map(a => a.path)
    }));
  if (!items.length) { alert('请至少填写一条用例的提问'); return; }
  startRun({ case_items: items, max_inflight: state.INFLIGHT, auto_confirm: state.AUTO_CLICK });
}

export function setRunning(v) {
  state.RUNNING = v;
  document.querySelectorAll('.btn-run').forEach(b => {
    if (v) {
      b.innerHTML = '<span class="btn-spinner"></span> 测试运行中…';
      b.classList.add('is-running');
    } else {
      b.textContent = '▶ 运行测试';
      b.classList.remove('is-running');
    }
  });
  const btnStop = $('btnStop');
  if (btnStop) btnStop.style.display = v ? '' : 'none';

  syncCaseState();
  syncEnvUI();
  syncAutoClickUI();

  if (v && !state.pollTimer) state.pollTimer = setInterval(pollStatus, 1200);
  if (!v && state.pollTimer) { clearInterval(state.pollTimer); state.pollTimer = null; }
}

export async function pollStatus() {
  const j = await (await fetch('/api/run/status')).json();
  renderStatus(j);
  if (!j.running) {
    setRunning(false);
    if (j.run_id) state.LATEST_RUN_ID = j.run_id;
    await loadRounds();
    if (j.archived && j.run_id) { await openRoundById(j.run_id); }
    renderRunCases();
  }
}

export function paintConsole(lines) {
  const has = !!(lines && lines.length);
  noteConsoleContent(has);
  if (!has) return;
  if (!state.CONSOLE_OPEN) {
    state.CONSOLE_PENDING = lines.length;
    syncConsoleNew();
    return;
  }
  renderConsoleLines(lines);
  if (state.CONSOLE_PENDING) { state.CONSOLE_PENDING = 0; syncConsoleNew(); }
}

export function renderStatus(j) {
  const b = $('statusBadge');
  if (b) {
    if (j.running) {
      b.className = 'badge b-run';
      b.innerHTML = '<span class="badge-dot-live"></span>运行中 · ' + j.elapsed_s + 's';
    } else if (j.finished_ok || (j.exit_code === 0 && j.archived)) {
      b.className = 'badge b-ok'; b.textContent = '完成';
    } else if (j.stopped) {
      b.className = 'badge b-err'; b.textContent = '已终止（未产出报告）';
    } else if (j.exit_code != null) {
      b.className = 'badge b-err'; b.textContent = '异常退出（退出码 ' + j.exit_code + '）';
    } else {
      b.className = 'badge b-idle'; b.textContent = '空闲';
    }
  }

  const cl = $('consoleLive');
  if (cl) cl.style.display = j.running ? 'inline-flex' : 'none';

  if (Array.isArray(j.cases) && j.cases.length) {
    state.RUN_CASES = j.cases;
    renderRunCases();
  }
  paintConsole(j.lines);
}

export async function goToLatestHistory(runId) {
  switchPage('history');
  let rid = runId || state.LATEST_RUN_ID;
  if (!rid) {
    if (!state.lastRounds || !state.lastRounds.length) {
      await loadRounds();
    }
    if (state.lastRounds && state.lastRounds.length) {
      rid = state.lastRounds[0].run_id;
    }
  }
  if (rid) {
    await openRoundById(rid);
  }
  const rList = $('roundList');
  if (rList) rList.scrollTop = 0;
}

export function initHome() {
  const btnRun = $('btnRun');
  if (btnRun) btnRun.onclick = doRun;

  const btnStop = $('btnStop');
  if (btnStop) {
    btnStop.onclick = async () => {
      if (!confirm('确定终止当前测试？\n\n测试进程会被停止，本轮不产出报告，\n已经跑完的用例结果也会一并丢弃。\n被测应用不受影响，继续常驻。')) return;
      btnStop.disabled = true;
      try {
        const r = await fetch('/api/run/stop', { method: 'POST' });
        const j = await r.json();
        if (!j.ok) alert('终止失败：' + (j.message || '未知错误'));
      } catch (e) {
        alert('请求失败：' + e.message);
      }
      btnStop.disabled = false;
    };
  }

  const caseRows = $('caseRows');
  if (caseRows) {
    caseRows.addEventListener('keydown', e => {
      if (e.key !== 'Enter' || e.isComposing || e.shiftKey) return;
      e.preventDefault();
      doRun();
    });
  }
}
