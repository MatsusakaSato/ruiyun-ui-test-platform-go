/* ================= 通用工具函数 ================= */

export const $ = id => document.getElementById(id);

export const esc = s => String(s ?? '').replace(/[&<>"]/g, c => ({
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;'
}[c]));

export const fmtPct = v => (v == null ? '—' : (v * 100).toFixed(0) + '%');

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

export let IS_WIN = /Win/i.test(navigator.userAgent || '');
export const setPlatformWin = (v) => { IS_WIN = !!v; };
export const FM_NAME = () => IS_WIN ? '资源管理器' : '访达';

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
