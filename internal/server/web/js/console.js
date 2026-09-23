/* ================= 控制台相关逻辑 ================= */

import { $, esc } from './utils.js';
import { state } from './state.js';

export function setConsoleHeight(h, save = true) {
  state.CONSOLE_HEIGHT = Math.max(70, Math.min(Math.round(h), Math.round(window.innerHeight * 0.85)));
  const c = $('console');
  if (c && state.CONSOLE_OPEN) c.style.height = state.CONSOLE_HEIGHT + 'px';
  const btnH = $('btnConsoleH');
  if (btnH) btnH.textContent = state.CONSOLE_HEIGHT + 'px';
  if (save) {
    try { localStorage.setItem('ruiyun_console_h', String(state.CONSOLE_HEIGHT)); } catch (e) {}
  }
}

export function setConsoleOpen(open, userAction = false) {
  if (userAction) state.CONSOLE_TOUCHED = true;
  state.CONSOLE_OPEN = open;
  $('consoleWrap')?.classList.toggle('collapsed', !open);
  const btnConsole = $('btnConsole');
  if (btnConsole) btnConsole.textContent = open ? '收起' : '展开';
  const btnH = $('btnConsoleH');
  if (btnH) btnH.style.display = open ? '' : 'none';
  if (open) {
    state.CONSOLE_PENDING = 0;
    syncConsoleNew();
    const c = $('console');
    if (c) {
      c.style.height = state.CONSOLE_HEIGHT + 'px';
      c.scrollTop = c.scrollHeight;     // 展开时直接看到最新一行
    }
  }
}

export function noteConsoleContent(hasLines) {
  $('consoleWrap')?.classList.toggle('compact', !hasLines);
  const c = $('console');
  if (c && state.CONSOLE_OPEN) {
    if (!hasLines && !localStorage.getItem('ruiyun_console_h')) {
      c.style.height = '96px';
    } else {
      c.style.height = state.CONSOLE_HEIGHT + 'px';
    }
  }
  if (hasLines && !state.CONSOLE_OPEN && !state.CONSOLE_TOUCHED) setConsoleOpen(true);
}

export function renderConsoleLines(lines) {
  const c = $('console');
  if (!c) return;
  c.innerHTML = (lines || []).map(l => {
    const i = l.indexOf('  ');
    return '<span class="ln-time">' + esc(l.slice(0, i)) + '</span> ' + esc(l.slice(i + 2));
  }).join('\n');
  if (state.CONSOLE_OPEN) c.scrollTop = c.scrollHeight;
}

export function syncConsoleNew() {
  const el = $('consoleNew');
  if (!el) return;
  el.textContent = (!state.CONSOLE_OPEN && state.CONSOLE_PENDING > 0)
    ? `有 ${state.CONSOLE_PENDING} 行新日志` : '';
}

export function initConsole() {
  const btn = $('btnConsole');
  if (btn) btn.onclick = () => setConsoleOpen(!state.CONSOLE_OPEN, true);

  const resizer = $('consoleResizer');
  const consoleEl = $('console');
  if (resizer && consoleEl) {
    let startY = 0, startH = 0, isDragging = false;

    resizer.addEventListener('mousedown', e => {
      if (e.button !== 0) return;
      isDragging = true;
      startY = e.clientY;
      startH = consoleEl.getBoundingClientRect().height;
      resizer.classList.add('is-dragging');
      document.body.style.cursor = 'row-resize';
      document.body.style.userSelect = 'none';

      const onMouseMove = ev => {
        if (!isDragging) return;
        const dy = ev.clientY - startY;
        setConsoleHeight(startH + dy, false);
      };
      const onMouseUp = () => {
        if (!isDragging) return;
        isDragging = false;
        resizer.classList.remove('is-dragging');
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        setConsoleHeight(consoleEl.getBoundingClientRect().height, true);
        window.removeEventListener('mousemove', onMouseMove);
        window.removeEventListener('mouseup', onMouseUp);
      };
      window.addEventListener('mousemove', onMouseMove);
      window.addEventListener('mouseup', onMouseUp);
    });

    resizer.addEventListener('dblclick', () => {
      setConsoleHeight(210, true);
    });
  }

  const btnH = $('btnConsoleH');
  if (btnH) {
    const PRESETS = [140, 240, 450];
    btnH.onclick = () => {
      let next = PRESETS.find(p => p > state.CONSOLE_HEIGHT + 20);
      if (!next) next = PRESETS[0];
      setConsoleHeight(next, true);
    };
    btnH.textContent = state.CONSOLE_HEIGHT + 'px';
  }

  if (localStorage.getItem('ruiyun_console_h')) {
    setConsoleHeight(state.CONSOLE_HEIGHT, false);
  }
  setConsoleOpen(true);
}
