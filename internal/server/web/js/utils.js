/* ================= 通用工具函数 ================= */

export const $ = id => document.getElementById(id);

export const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;'
}[c]));

export const fmtPct = v => (v == null ? '—' : (v * 100).toFixed(0) + '%');

/* ---------- 多轮对话 ---------- */

// 单个用例允许的最大轮数（与后端 testcasedb.MaxTurns、Excel prompt_1..prompt_8 一致）
export const MAX_TURNS = 8;

// 把任意来源的 prompt 归一成轮次数组。
// 约定：接口与库里 prompt 一律是字符串数组（单轮也是长度 1 的数组）；
// 这里对裸字符串兜底，避免历史调用方直接把页面打崩。
export function toTurns(p) {
  if (Array.isArray(p)) return p.map(t => String(t ?? ''));
  if (typeof p === 'string' && p !== '') return [p];
  return [];
}

// caseTurns 从「用例对象」里取全部轮次。
//
// 归档同时给了两个字段：prompts 是完整轮次数组，prompt 只保留了第 1 轮
// （历史兼容）。只看 prompt 会把多轮用例显示成单轮，所以统一走这里：
// 有 prompts 就用它，否则回落到 prompt（旧数据只有 prompt）。
export function caseTurns(c) {
  if (c && Array.isArray(c.prompts) && c.prompts.length) return toTurns(c.prompts);
  return toTurns(c && c.prompt);
}

// caseTurnCount 用例的轮数（含 prompts 回落）
export function caseTurnCount(c) {
  return caseTurns(c).filter(t => String(t).trim() !== '').length;
}

// 第 1 轮（列表、标题、日志等单行展示用）
export function firstTurn(p) {
  const t = toTurns(p);
  return t.length ? t[0] : '';
}

// 轮数
export function turnCount(p) {
  return toTurns(p).filter(t => String(t).trim() !== '').length;
}

// 是否多轮用例（轮数 > 1）
export function isMultiTurn(p) {
  return turnCount(p) > 1;
}

// 单轮 / 多轮标签的 HTML。空用例不显示标签。
export function turnsBadge(p) {
  const n = turnCount(p);
  if (n === 0) return '';
  if (n === 1) return '<span class="tag t-turn">单轮</span>';
  return `<span class="tag t-turn t-turn-multi">多轮 · ${n}</span>`;
}

export function fmtSize(n) {
  n = Number(n || 0);
  if (n >= 1048576) return (n / 1048576).toFixed(1) + 'MB';
  if (n >= 1024) return Math.round(n / 1024) + 'KB';
  return n + 'B';
}

export function fileToBase64(f) {
  return new Promise((res, rej) => {
    const r = new FileReader();
    r.onload = () => res(String(r.result || '').split(',')[1] || '');
    r.onerror = () => rej(new Error('读取文件失败'));
    r.readAsDataURL(f);
  });
}

// 把服务端回报的无效附件整理成可读的多行提示
export function fmtInvalidAttachments(list) {
  return (list || []).map(x => {
    const who = x.name ? `「${x.name}」` : `第 ${x.row} 行`;
    return `· ${who}：${x.value}\n  原因：${x.reason}`;
  }).join('\n');
}

// 注意不要用 /Win/i 这种宽匹配：macOS 上某些内核的 UA 带 "(darwin)"，拆开就是 …ar-win…
export let IS_WIN = /Windows NT|Win64|Win32/i.test(navigator.userAgent || '');
export const setPlatformWin = (v) => { IS_WIN = !!v; };
export const FM_NAME = () => IS_WIN ? '资源管理器' : '访达';

/* 组合键里修饰键的显示名：macOS 显示 Ctrl/⌘，Windows 只显示 Ctrl */
export const MOD_KEY = () => IS_WIN ? 'Ctrl' : 'Ctrl/⌘';

export async function doReveal(p) {
  try {
    const r = await fetch('/api/reveal', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: p })
    });
    const j = await r.json();
    if (!j.ok) alert('无法定位文件：' + (j.message || '未知错误'));
  } catch (e) {
    alert('请求失败：' + e.message);
  }
}

/* 用系统默认程序打开本机文件（服务端执行，只放行附件库目录内的路径） */
export async function doOpenFile(p) {
  try {
    const r = await fetch('/api/open', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: p })
    });
    const j = await r.json();
    if (!j.ok) alert('无法打开文件：' + (j.message || '未知错误'));
  } catch (e) {
    alert('请求失败：' + e.message);
  }
}

/* 复制文本到剪贴板；done 在复制动作结束后回调（无论成功与否，行为与 copyRoundId 一致） */
export function copyText(text, done) {
  const finish = () => { if (done) done(); };
  if (!navigator.clipboard) {
    const ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); } catch (e) {}
    document.body.removeChild(ta);
    finish();
    return;
  }
  navigator.clipboard.writeText(text).then(finish).catch(finish);
}

export async function revealInFinder(ev, el) {
  if (ev && ev.preventDefault) ev.preventDefault();
  const p = el.getAttribute('data-path');
  if (!p) return;
  el.classList.add('busy');
  await doReveal(p);
  el.classList.remove('busy');
}

export function sessionLink(c, label) {
  const txt = label ?? (c.session_id || '—');
  const p = c.session_log_path;
  if (!p) return `<span class="mono">${esc(txt)}</span>`;
  return `<a class="sess-link mono" href="#" data-path="${esc(p)}"
             title="在${FM_NAME()}中显示会话文件夹：${esc(p)}"
             onclick="revealInFinder(event,this)">${esc(txt)}</a>`;
}
