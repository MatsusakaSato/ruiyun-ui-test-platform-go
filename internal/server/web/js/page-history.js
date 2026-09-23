/* ================= 历史记录与详情面板（轨迹/报告/问题/质量评估） ================= */

import { $, esc, fmtPct, FM_NAME, sessionLink, revealInFinder } from './utils.js';
import { state } from './state.js';
import { renderHistoryPane } from './nav.js';
import { openLlm } from './modals.js';

/* ---------- 历史轮次列表与查询 ---------- */
export function roundQueryUrl() {
  const q = new URLSearchParams({
    limit: state.ROUND_LIMIT,
    offset: state.RF.offset,
    date_from: state.RF.date_from,
    date_to: state.RF.date_to,
    keyword: state.RF.keyword
  });
  return '/api/rounds?' + q;
}

export async function loadRounds() {
  let j;
  try {
    j = await (await fetch(roundQueryUrl())).json();
  } catch (e) {
    const rList = $('roundList');
    if (rList) rList.innerHTML = `<div class="round-empty">读取失败：${esc(e.message)}</div>`;
    return;
  }
  state.ROUND_DIR = (j.dir || '').replace(/\/+$/, '');
  state.lastRounds = j.rounds || [];
  state.ROUND_TOTAL = j.total || 0;
  renderRounds();
}

export function renderRounds() {
  const box = $('roundList');
  if (!box) return;
  const from = state.ROUND_TOTAL ? state.RF.offset + 1 : 0;
  const to = Math.min(state.RF.offset + state.lastRounds.length, state.ROUND_TOTAL);
  const rPage = $('roundPage');
  if (rPage) rPage.textContent = `第 ${from}–${to} 条 / 共 ${state.ROUND_TOTAL} 条`;
  const rCount = $('roundCount');
  if (rCount) rCount.textContent = state.ROUND_TOTAL ? `（${state.ROUND_TOTAL} 轮）` : '';

  const rPrev = $('roundPrev');
  if (rPrev) rPrev.disabled = state.RF.offset <= 0;
  const rNext = $('roundNext');
  if (rNext) rNext.disabled = state.RF.offset + state.ROUND_LIMIT >= state.ROUND_TOTAL;

  const filterOn = state.RF.date_from || state.RF.date_to || state.RF.keyword;
  const hSum = $('histSum');
  if (hSum) {
    hSum.textContent = state.ROUND_TOTAL
      ? (filterOn ? `筛选出 ${state.ROUND_TOTAL} 轮` : `共 ${state.ROUND_TOTAL} 轮`)
      : (filterOn ? '没有符合条件的轮次' : '还没有轮次归档');
  }

  if (!state.lastRounds.length) {
    box.innerHTML = '<div class="round-empty">' + (filterOn ? '没有符合条件的轮次' : '暂无轮次') + '</div>';
    return;
  }

  box.innerHTML = state.lastRounds.map(r => {
    const s = r.summary || {};
    const okc = s.passed ?? 0, all = s.cases ?? 0, fnd = s.findings ?? 0;
    const folder = state.ROUND_DIR ? state.ROUND_DIR + '/' + r.run_id : '';
    return `<div class="round-item ${state.currentRound === r.run_id ? 'active' : ''}" onclick="openRound('${r.run_id}')">
      <div class="rid">${folder
        ? `<a class="rid-link" href="#" data-path="${esc(folder)}"
            title="在${FM_NAME()}中打开该轮次文件夹：${esc(folder)}"
            onclick="event.stopPropagation();revealInFinder(event,this)">${esc(r.run_id)}</a>`
        : esc(r.run_id)}</div>
      <div class="rmeta">
        <span>${esc(r.finished_at)}</span>
        <span>通过 ${okc}/${all} 条</span>
        <span>${fnd ? fnd + ' 个问题' : '无问题'}</span>
        ${artLink(r)}
      </div>
      ${promptPreview(r)}
      <button class="round-del" title="删除该轮次归档"
              onclick="delRound(event,'${r.run_id}')">×</button>
    </div>`;
  }).join('');
}

export function roundQuery() {
  state.RF.offset = 0;
  loadRounds();
}

export function roundToday(d) {
  const x = d || new Date();
  const p = n => String(n).padStart(2, '0');
  return `${x.getFullYear()}-${p(x.getMonth() + 1)}-${p(x.getDate())}`;
}

export function artLink(r) {
  const a = r.artifacts || {};
  if (!a.count || !a.root) return '';
  const dirTip = a.multi_dir
    ? '本轮产物分散在多个目录，这里打开它们最接近的公共目录'
    : '本轮产物所在目录';
  const srcTip = a.source === 'evaluation' ? '（该轮次没有流水线产物清单，这里是评估阶段抽出来的）' : '';
  const tip = `${dirTip}：${a.root}${srcTip}`;
  const names = (a.paths || []).map(p => String(p).replace(/\\/g, '/').split('/').pop());
  const list = names.slice(0, 12).join('\n') + (names.length > 12 ? `\n…共 ${names.length} 件` : '');
  return `<span class="rart${a.exists ? '' : ' busy'}" data-path="${esc(a.root)}"
      title="📁 ${esc(tip)}${list ? '\n\n产物：\n' + esc(list) : ''}${a.exists ? '' : '\n\n（该目录当前不存在）'}"
      onclick="event.stopPropagation();revealInFinder(event,this)">📁 产物目录 ${a.count} 件</span>`;
}

const PREVIEW_CHARS = 42;
export function promptPreview(r) {
  const cases = (r.cases || []).filter(c => (c.prompt || '').trim());
  if (!cases.length) return '';
  const cut = t => {
    const t2 = t.trim().replace(/\s+/g, ' ');
    return t2.length > PREVIEW_CHARS ? t2.slice(0, PREVIEW_CHARS) + '…' : t2;
  };
  const first = cut(cases[0].prompt);
  const more = cases.length > 1 ? `<span class="rp-more">+${cases.length - 1}</span>` : '';
  const full = cases.map((c, i) => `${i + 1}. ${(c.prompt || '').trim()}`).join('\n');
  return `<div class="rprompt" title="${esc(full)}">${esc(first)}${more}</div>`;
}

export async function delRound(ev, rid) {
  if (ev && ev.stopPropagation) ev.stopPropagation();
  if (!confirm(`确定删除轮次 ${rid} ？\n\n该轮次的归档数据（用例结果、报告、控制台日志）会被永久删除，无法恢复。`)) return;
  try {
    const r = await fetch('/api/rounds/' + encodeURIComponent(rid), { method: 'DELETE' });
    const j = await r.json();
    if (!j.ok) { alert('删除失败：' + (j.message || '未知错误')); return; }
    if (state.currentRound === rid) {
      state.currentRound = null;
      const dp = $('detailPane');
      if (dp) dp.innerHTML = '<div class="card"><div class="empty">该轮次已删除，请选择其他轮次</div></div>';
    }
    if (state.lastRounds.length === 1 && state.RF.offset > 0) state.RF.offset -= state.ROUND_LIMIT;
    await loadRounds();
    if (state.currentPage === 'history') renderHistoryPane();
  } catch (e) {
    alert('请求失败：' + e.message);
  }
}

export function syncRoundActive(rid) {
  document.querySelectorAll('.round-item').forEach(el => {
    const idEl = el.querySelector('.rid');
    const isAct = !!idEl && (idEl.textContent.trim() === rid || idEl.getAttribute('data-rid') === rid);
    el.classList.toggle('active', isAct);
  });
}

export async function openRound(rid) {
  state.currentRound = rid;
  state.currentCase = 0;
  syncRoundActive(rid);
  const j = await (await fetch('/api/rounds/' + rid)).json();
  if (j.error) { alert('读不到该轮次的数据：' + rid); return; }
  state.lastRoundData = j;
  renderRound(j);
  syncRoundActive(rid);
}

export async function openRoundById(rid) {
  state.currentRound = rid;
  state.currentCase = 0;
  syncRoundActive(rid);
  const j = await (await fetch('/api/rounds/' + rid)).json();
  if (!j.error) {
    state.lastRoundData = j;
    renderRound(j);
  }
  syncRoundActive(rid);
}

export function copyRoundId(rid, btn) {
  const onDone = () => {
    if (btn) {
      const orig = btn.textContent;
      btn.textContent = '已复制!';
      setTimeout(() => { btn.textContent = orig; }, 1500);
    }
  };
  if (!navigator.clipboard) {
    const ta = document.createElement('textarea');
    ta.value = rid;
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); } catch (e) {}
    document.body.removeChild(ta);
    onDone();
    return;
  }
  navigator.clipboard.writeText(rid).then(onDone).catch(() => onDone());
}

/* ---------- 详情面板渲染 ---------- */
export function kpi(label, value, foot, cls) {
  return `<div class="kpi"><div class="label">${label}</div>
  <div class="value ${cls || ''}">${value}</div><div class="foot">${foot || ''}</div></div>`;
}

export function renderRound(data) {
  state.lastRoundData = data;
  const s = data.summary || {}, d = data.detail || {}, m = d.metrics || {}, sm = m.summary || s;
  const rp = m.repro_summary || {};
  let html = '';

  const runId = data.run_id || s.run_id || '';
  const finishedAt = s.finished_at || '';
  const runMode = s.run_mode || 'UI 自动化';
  const elapsed = s.elapsed_s != null ? `${s.elapsed_s}s` : '';
  const artCount = Array.isArray(data.artifacts) ? data.artifacts.length : (s.round_artifacts ? s.round_artifacts.length : 0);

  html += `<div class="round-detail-hd">
    <div class="rd-title">
      <span>轮次</span>
      <span class="mono" style="font-size:13.5px;color:var(--accent);user-select:all">${esc(runId)}</span>
      <button class="rd-copy" onclick="copyRoundId('${esc(runId)}', this)" title="复制轮次号">复制</button>
    </div>
    <span class="rd-badge">${esc(runMode)}</span>
    <div class="rd-meta">
      ${finishedAt ? `<span>🕒 ${esc(finishedAt)}</span>` : ''}
      ${elapsed ? `<span>⏱ 耗时 <b>${elapsed}</b></span>` : ''}
      ${artCount > 0 ? `<span class="pill ok" style="font-weight:600">📦 交付产物 ${artCount} 个</span>` : ''}
    </div>
  </div>`;

  const cases = d.cases || [];
  html += '<div class="kpis">'
    + kpi('本轮用例', cases.length, `通过 ${sm.passed ?? 0} · 断言失败 ${sm.failed ?? 0} · UI失败 ${sm.ui_failed ?? 0}`, 'v-acc')
    + kpi('问题发现', sm.findings ?? 0, `P0 ${sm.p0 ?? 0} · P1 ${sm.p1 ?? 0}`, (sm.findings ?? 0) > 0 ? 'v-bad' : 'v-ok')
    + '</div>';

  if (rp.verified) {
    html += `<div class="card" style="margin-bottom:14px"><h3 class="sec">复现率验证</h3>
      <div style="display:flex;gap:22px;flex-wrap:wrap;font-size:12.5px;color:var(--tx2)">
        <span>已验证 <b style="color:var(--tx)">${rp.verified}</b> 个问题</span>
        <span>必现 <b style="color:var(--p0)">${rp.stable}</b></span>
        <span>高概率 <b style="color:var(--p1)">${rp.likely}</b></span>
        <span>偶发 <b>${rp.flaky}</b></span>
        <span>平均复现率 <b style="color:var(--accent)">${fmtPct(rp.avg_rate)}</b></span>
      </div></div>`;
  }

  html += `<div class="tabs">
    <button class="tab ${state.currentTab === 'trace' ? 'active' : ''}" onclick="setTab('trace')">用例详情</button>
    <button class="tab ${state.currentTab === 'obj' ? 'active' : ''}" onclick="setTab('obj')">客观信息</button>
    <button class="tab ${state.currentTab === 'bugs' ? 'active' : ''}" onclick="setTab('bugs')">问题发现</button>
    <button class="tab ${state.currentTab === 'eval' ? 'active' : ''}" onclick="setTab('eval')">质量评估</button>
  </div><div class="card" id="tabBody"></div>`;

  const dp = $('detailPane');
  if (dp) {
    dp.innerHTML = html;
    dp.scrollTop = 0;
  }
  setTab(state.currentTab);
}

export function setTab(t) {
  state.currentTab = t;
  document.querySelectorAll('.tab').forEach(b => b.classList.remove('active'));
  if (window.event && window.event.target && window.event.target.classList) {
    window.event.target.classList.add('active');
  }
  const j = state.lastRoundData;
  if (!j) return;
  const d = j.detail || {}, m = d.metrics || {};
  const body = $('tabBody');
  if (!body) return;
  if (t === 'trace') body.innerHTML = traceTab(d);
  else if (t === 'obj') body.innerHTML = objTab(m, d);
  else if (t === 'eval') body.innerHTML = evalTab();
  else body.innerHTML = bugsTab(m, d);
}

export function pickCase(i) {
  state.currentCase = i;
  const j = state.lastRoundData || {}, d = j.detail || {}, m = d.metrics || {};
  const body = $('tabBody');
  if (!body) return;
  if (state.currentTab === 'trace') body.innerHTML = traceTab(d);
  else if (state.currentTab === 'obj') body.innerHTML = objTab(m, d);
  else if (state.currentTab === 'eval') body.innerHTML = evalTab();
  else body.innerHTML = bugsTab(m, d);
}

/* ---------- 客观信息 ---------- */
export function objTab(m, d) {
  const cases = d.cases || [];
  if (!cases.length) return '<div class="empty">本轮无用例数据</div>';
  state.currentCase = Math.min(state.currentCase, cases.length - 1);
  const c = cases[state.currentCase];
  const o = c.objective || {}, tm = o.timing || {}, tk = o.volume || {}, rq = o.requests || {}, stt = o.status || {};

  let h = '<div class="case-chips">' + cases.map((x, i) =>
      `<span class="chip-c ${i === state.currentCase ? 'active' : ''}" onclick="pickCase(${i})">${esc(x.case_id)} ${esc(x.name)} ${x.status === 'PASS' ? '✓' : x.status === 'FAIL' ? '✗' : '⚠'}</span>`).join('') + '</div>';

  h += `<div style="font-size:12.5px;margin-bottom:12px"><b>提问：</b>${esc(c.prompt)}</div>`;

  h += '<h3 class="sec" style="margin-top:0">耗时</h3><div class="tbl-card"><table>'
    + `<tr><td>平台记录 <span class="q tip-right" data-tip="从发出提问到测试结果写盘完成的总耗时">?</span></td><td style="text-align:right"><b>${c.elapsed_s ?? '—'}s</b></td></tr>`
    + `<tr><td>首响耗时</td><td style="text-align:right"><b>${tm.first_response_s ?? '—'}${tm.first_response_s != null ? 's' : ''}</b></td></tr>`
    + `<tr><td>生成耗时</td><td style="text-align:right"><b>${tm.generation_s ?? '—'}${tm.generation_s != null ? 's' : ''}</b></td></tr>`
    + `<tr><td>会话时长</td><td style="text-align:right"><b>${tm.session_s ?? '—'}${tm.session_s != null ? 's' : ''}</b></td></tr>`
    + '</table></div>';

  h += '<h3 class="sec" style="margin-top:16px">调用与状态</h3><div class="tbl-card"><table>'
    + `<tr><td>工具调用</td><td style="text-align:right"><b>${rq.tool_calls ?? 0}</b> 次</td></tr>`
    + `<tr><td>思考步数</td><td style="text-align:right"><b>${rq.thinking_steps ?? 0}</b> 步</td></tr>`
    + `<tr><td>调用成功 / 失败 / 截断</td><td style="text-align:right"><b>${stt.tool_ok ?? 0}</b> / <b style="color:${stt.tool_fail ? 'var(--p0)' : 'inherit'}">${stt.tool_fail ?? 0}</b> / <b style="color:${stt.tool_truncated ? 'var(--p1)' : 'inherit'}">${stt.tool_truncated ?? 0}</b></td></tr>`
    + `<tr><td>调用链是否自洽 <span class="q tip-right" data-tip="评判标准：&#10;校验消息声明的调用 ID（associatedToolCallIds）与实际执行的工具调用记录是否完全自洽匹配。&#10;若存在孤立或丢失的调用 ID，则判定为不自洽。">?</span></td><td style="text-align:right"><span class="badge ${stt.chain_consistent === false ? 'b-fail' : 'b-pass'}">${stt.chain_consistent === false ? '不自洽' : '自洽'}</span></td></tr>`
    + `<tr><td>会话收尾 <span class="q tip-right" data-tip="评判标准：&#10;校验会话是否已完成输出（isStreaming=false）并具有明确的完成标记（completedAt）。&#10;若缺少完成标记或停留在流式状态，判定为未正常收尾（易导致前端持续 Loading 假死）。">?</span></td><td style="text-align:right"><span class="badge ${stt.session_closed ? 'b-pass' : 'b-fail'}">${esc(stt.closed_desc || '—')}</span></td></tr>`
    + '</table></div>';

  h += '<h3 class="sec" style="margin-top:16px">Token 用量（估算） <span class="q tip-right" data-tip="估算规则：&#10;中文约 1.6 字符/token，英文及代码约 4 字符/token。&#10;包含提问、工具调用及模型思考与回复。">?</span></h3><div class="tbl-card"><table>'
    + `<tr><td>输入 Token</td><td style="text-align:right"><b>${tk.input_tokens_est ?? 0}</b></td></tr>`
    + `<tr><td>输出 Token</td><td style="text-align:right"><b>${tk.output_tokens_est ?? 0}</b></td></tr>`
    + `<tr><td><b>合计估算</b></td><td style="text-align:right"><b style="color:var(--accent)">${tk.total_tokens_est ?? 0}</b></td></tr>`
    + '</table></div>'
    + '<div class="fhint" style="margin-top:6px;font-size:11px">按中文字符≈1.6/token、英文及代码≈4/token 估算</div>';

  const tools = c.tools || [];
  h += '<h3 class="sec" style="margin-top:16px">用到的工具</h3><div class="tbl-card"><table><tr><th>工具</th><th>调用</th><th>失败</th><th>截断</th></tr>';
  for (const t of tools) {
    h += `<tr><td class="mono">${esc(t.name)}</td><td>${t.calls}</td>`
      + `<td>${t.fail ? `<span class="badge b-p0">${t.fail}</span>` : '—'}</td>`
      + `<td>${t.truncated ? `<span class="badge b-p1">${t.truncated}</span>` : '—'}</td></tr>`;
  }
  if (!tools.length) h += '<tr><td colspan="4" class="empty">没有调用任何工具</td></tr>';
  h += '</table></div>';

  const sk = c.skills || [];
  if (sk.length) {
    h += '<h3 class="sec" style="margin-top:16px">用到的 Skill</h3><div class="tbl-card"><table><tr><th>Skill</th><th>读取的文件</th></tr>';
    for (const x of sk) {
      h += `<tr><td><span class="badge" style="background:#ede9fe;color:#6d28d9">${esc(x.name)}</span></td>`
        + `<td class="mono">${esc((x.files || []).join(', ')) || '—'}</td></tr>`;
    }
    h += '</table></div>';
  }
  return h;
}

/* ---------- 轨迹时间线 ---------- */
export function traceTab(d) {
  const cases = d.cases || [];
  if (!cases.length) return '<div class="empty">本轮无用例数据</div>';
  state.currentCase = Math.min(state.currentCase, cases.length - 1);
  let h = '<div class="case-chips">' + cases.map((c, i) =>
    `<span class="chip-c ${i === state.currentCase ? 'active' : ''}" onclick="pickCase(${i})">${esc(c.case_id)} ${esc(c.name)} ${c.status === 'PASS' ? '✓' : c.status === 'FAIL' ? '✗' : '⚠'}</span>`).join('') + '</div>';
  h += caseTimeline(cases[state.currentCase]);
  return h;
}

export const CONFIRM_ACTION = {
  'qcard-option': '选中选项', 'qcard-confirm': '确认本题',
  'qcard-submit': '提交', 'qcard-fill': '填写自定义答案',
  'keyword': '确认授权', 'first-option': '确认首个选项'
};

export function confirmAction(st) {
  const ans = String(st.text || '').trim();
  if (ans) return ans;
  return CONFIRM_ACTION[st.mode] || st.label || '自动确认';
}

export function caseTimeline(c) {
  let h = `<div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">
    <span style="font-weight:700">${esc(c.name)}</span>
    <span class="mono">${esc(c.case_id)}</span>
    <span class="badge ${c.status === 'PASS' ? 'b-ok' : c.status === 'FAIL' ? 'b-fail' : 'b-uifail'}">${c.status === 'PASS' ? '通过' : c.status === 'FAIL' ? '断言失败' : 'UI 失败'}</span>
  </div>
  <div class="case-meta">
    <span>会话 ${sessionLink(c)}</span>
    <span>耗时 ${c.elapsed_s}s</span>
    <span>工具调用 <b>${c.tool_call_count ?? 0}</b></span>
    <span>思考步 <b>${c.thinking_count ?? 0}</b></span>
    <span>答复 ${c.answer_chars ?? 0} 字</span>
  </div>
  <div style="font-size:12.5px;margin-bottom:6px"><b>提问：</b>${esc(c.prompt)}</div>`;

  if ((c.attachments || []).length) {
    h += `<div style="font-size:12px;color:var(--tx2);margin-bottom:8px"><b>附件：</b>`
      + c.attachments.map(a => `<span class="badge" style="background:#eff6ff;color:#1d4ed8" title="${esc(a)}">`
        + `${esc(String(a).split('/').pop())}</span>`).join(' ')
      + (c.attach_note ? `<span style="color:var(--tx3);margin-left:6px">${esc(c.attach_note)}</span>` : '') + '</div>';
  }
  if (c.ui_error) h += `<div style="color:var(--p0);font-size:12.5px;margin-bottom:10px">UI 异常：${esc(c.ui_error)}</div>`;
  if (c.waited_limit) h += `<div style="color:var(--tx2);font-size:12.5px;margin-bottom:10px">· ${esc(c.wait_note || '已达等待上限')}</div>`;
  if (c.answer_excerpt) h += `<div class="answer">${esc(c.answer_excerpt)}</div>`;
  if (c.skills && c.skills.length) {
    h += `<div style="font-size:12px;color:var(--tx2);margin-bottom:12px">调用 Skill：
      ${c.skills.map(s => `<span class="badge" style="background:#ede9fe;color:#6d28d9">${esc(s.name)}</span>`).join(' ')}</div>`;
  }

  h += '<div class="tl">';
  for (const st of (c.steps || [])) {
    if (st.type === 'thinking') {
      const ctn = st.content || '';
      const head = ctn.replace(/\s+/g, ' ').slice(0, 60);
      h += `<div class="step thinking"><span class="dot"></span>
        <div class="step-head" onclick="this.parentNode.classList.toggle('open')">
          <span class="t-type">思考</span>
          <span class="t-name">${esc(head)}${ctn.length > 60 ? '…' : ''}</span>
          <span class="mono">${st.chars} 字</span>
        </div>
        <div class="step-body"><div class="pre pre-full">${esc(ctn) || '(空)'}</div></div>
      </div>`;
    } else if (st.type === 'auto_confirm') {
      const ans = String(st.text || '').trim();
      const title = String(st.question || '').trim();
      const head = title || (st.q ? `第 ${st.q} 题` : '自动确认');
      h += `<div class="step confirm"><span class="dot"></span>
        <div class="step-head" onclick="this.parentNode.classList.toggle('open')">
          <span class="t-type">确认</span>
          <span class="t-name">${esc(head.length > 72 ? head.slice(0, 72) + '…' : head)}</span>
          ${st.time ? `<span class="mono">${esc(st.time)}</span>` : ''}
        </div>
        <div class="step-body">
          ${title ? `<div class="kv"><b>问题</b><span class="pre pre-full">${esc(title)}</span></div>` : ''}
          <div class="kv"><b>操作</b><span class="pre pre-full">${esc(confirmAction(st))}</span></div>
        </div>
      </div>`;
    } else {
      const cls = st.skill_name ? 'skill' : (st.status === 'fail' ? 'fail' : (st.status === 'truncated' || st.status === 'empty') ? 'truncated' : '');
      const stTxt = st.status === 'fail' ? '<span class="badge b-p0">失败</span>'
        : st.status === 'truncated' ? '<span class="badge b-p1">截断</span>'
        : st.status === 'empty' ? '<span class="badge b-p1">空返回</span>'
        : '<span class="badge b-ok">成功</span>';
      h += `<div class="step tool ${cls}"><span class="dot"></span>
        <div class="step-head" onclick="this.parentNode.classList.toggle('open')">
          <span class="t-type">工具</span>
          <span class="t-name mono" style="color:var(--accent)">${esc(st.name)}</span>
          ${stTxt}
          ${st.skill_name ? `<span class="badge" style="background:#ede9fe;color:#6d28d9">skill: ${esc(st.skill_name)}</span>` : ''}
          <span class="mono" title="正文长度（取自字段 ${esc(st.body_from || 'raw')}）；括号内为含 JSON 外壳的总长">结果 ${st.result_len} 字${st.raw_len && st.raw_len !== st.result_len ? `（外壳 ${st.raw_len}）` : ''}</span>
          ${(st.issues && st.issues.length) ? `<span class="mono" style="color:var(--p0)">${st.issues.join(',')}</span>` : ''}
        </div>
        <div class="step-body">
          <div class="kv"><b>参数</b><span class="pre pre-full">${esc(st.args_summary)}</span></div>
          <div class="kv"><b>结果</b><span class="pre pre-full">${esc(st.result) || '(空)'}</span></div>
          ${st.skill_files && st.skill_files.length ? `<div class="kv"><b>读取</b><span class="pre pre-full">${esc(st.skill_files.join(', '))}</span></div>` : ''}
        </div>
      </div>`;
    }
  }
  for (const a of (c.artifacts || [])) h += artifactStep(a);
  return h + '</div>';
}

export function artifactStep(a) {
  const raw = String(a.abs_path || a.path || '');
  const name = raw ? raw.replace(/\\/g, '/').split('/').pop() : '';
  const label = name || a.kind || '(未命名产物)';
  const kind = a.kind ? `<span class="tag">${esc(a.kind)}</span>` : '';
  if (a.abs_path) {
    return `<div class="step artifact"><span class="dot"></span>
      <div class="step-head">
        <span class="t-type">产物</span>
        <a class="art-link" href="#" data-path="${esc(a.abs_path)}"
           title="在${FM_NAME()}中显示该文件（并打开其所在目录）：${esc(a.abs_path)}"
           onclick="revealInFinder(event,this)">${esc(label)}</a>
        ${kind}
      </div>
    </div>`;
  }
  const why = (a.path ? `工具只给了相对路径 ${a.path}，本机没找到对应文件：`
                      : '本次调用没有返回文件路径：')
    + (a.note ? `${a.note}` : '');
  return `<div class="step artifact miss"><span class="dot"></span>
    <div class="step-head" title="${esc(why)}">
      <span class="t-type">产物</span>
      <span class="t-name">${esc(label)}</span>
      ${kind}
      <span class="badge b-approx">本机未找到</span>
    </div>
  </div>`;
}

/* ---------- 问题发现 ---------- */
export function bugsTab(m, d) {
  const cases = d.cases || [];
  if (!cases.length) return '<div class="empty">本轮无用例数据</div>';
  state.currentCase = Math.min(state.currentCase, cases.length - 1);
  const c = cases[state.currentCase];
  const rows = c.findings || [];

  let h = '<div class="case-chips">' + cases.map((x, i) =>
      `<span class="chip-c ${i === state.currentCase ? 'active' : ''}" onclick="pickCase(${i})">${esc(x.case_id)} ${esc(x.name)} ${x.status === 'PASS' ? '✓' : x.status === 'FAIL' ? '✗' : '⚠'}</span>`).join('') + '</div>'
    + `<div style="font-size:12.5px;margin-bottom:12px"><b>提问：</b>${esc(c.prompt)}</div>`;

  if (!rows.length) {
    return h + `<div class="none-banner">
      <div class="big">✓ 没有发现问题</div>
      <div class="sm">已按全部硬性缺陷规则逐条检查本次调用链路（工具调用失败、死循环、重复调用、
      内容截断、必填参数为空、调用链自洽、会话收尾等），一条都没命中。</div></div>`;
  }

  for (const f of rows) {
    h += `<div class="finding">
      <div class="f-hd">
        <span class="badge b-${(f.severity || 'p2').toLowerCase()}">${esc(f.severity)}</span>
        <b style="font-size:12.5px">${esc(f.name)}</b>
        <span class="mono">${esc(f.rule)}</span>
        ${f.tool ? `<span class="mono">${esc(f.tool)}</span>` : ''}
        ${f.step_index != null ? `<span class="mono" style="color:var(--tx3)">第 ${f.step_index} 步</span>` : ''}
      </div>
      <div class="f-detail"><b>表现：</b>${esc(f.detail)}</div>
      ${f.impact ? `<div class="f-impact"><b>影响：</b>${esc(f.impact)}</div>` : ''}
      ${f.evidence ? `<div class="evi">${esc(f.evidence)}</div>` : ''}
      ${f.repro ? reproHtml(f.repro) : ''}
    </div>`;
  }
  return h;
}

export function reproHtml(r) {
  let h = `<div class="repro"><b>怎么复现</b>
    <div style="margin-top:5px" class="pre">${esc(r.prompt)}</div>
    <div style="margin-top:5px"><b>验证标准：</b>${esc(r.verify_desc)}</div>
    <div><b>预期：</b>${esc(r.expected)}</div>
    <div><b>实际：</b>${esc(r.actual)}</div>`;
  if (r.attempts) {
    h += `<div style="margin-top:5px"><b>自动验证：</b>${r.hits}/${r.attempts} 次命中 =
      <b style="color:var(--accent)">${fmtPct(r.rate)}</b> ${esc(r.stability || '')}</div>`;
  }
  return h + '</div>';
}

/* ---------- 质量评估 ---------- */
export function evPct(score, scale) {
  if (score == null) return 0;
  return scale === '1-5' ? Math.round((score - 1) / 4 * 100) : Math.round(score / 5 * 100);
}

export const EV_COLS_FALLBACK = [
  { id: 'outcome_teaching', label: '结果质量 / 教学专业质量', groups: ['result_quality', 'teaching_quality'] },
  { id: 'safety_reliability', label: '安全与可靠', groups: ['safety_reliability'] },
  { id: 'agent_capability', label: '系统与 Agent 能力', groups: ['agent_capability'] }
];

export function evCols(sm) {
  const cs = sm.columns;
  return (Array.isArray(cs) && cs.length) ? cs : EV_COLS_FALLBACK;
}

export function dimStdTip(s) {
  const r = state.EV_RUBRIC[s.key] || {};
  const anchors = r.anchors || {};
  const isNew = (s.nature !== undefined);
  const src = isNew
    ? '分值来源：' + ((s.basis === 'objective') ? '硬规则（安全红线，覆盖模型判定）'
                   : (s.score == null ? '未评分' : '模型判定'))
    : '分值来源：' + ((state.EV_BASIS[s.basis] || s.basis || '') + '（旧数据）');
  const lines = [(r.label || s.label || '') + '（' + src + '）'];
  const ks = Object.keys(anchors).sort((a, b) => Number(b) - Number(a));
  if (ks.length) {
    lines.push('评分标准：');
    ks.forEach(k => lines.push('  ' + anchors[k]));
  }
  if (r.how) lines.push('怎么判的：' + r.how);
  if (r.always)
    lines.push('适用范围：所有场景都评（与教学 / 非教学无关；没有可视产物时本地直接判不适用）');
  return lines.join('\n');
}

export function dimWhyTip(s) {
  const cut = (t, n) => { t = String(t ?? ''); return t.length > n ? t.slice(0, n) + '…' : t; };
  const lines = [];
  const why = String(s.reason || '').trim();
  if (s.score != null && why)
    lines.push((Number(s.score) >= 5 ? '判定理由：' : '未满分原因：') + cut(why, 300));
  if (s.hint != null && s.score != null && Math.abs(s.score - s.hint) >= 0.05)
    lines.push(`本地算出的参考分 ${s.hint}（同一套算法得出的确定值，只作对比，最终以模型判定为准）`);
  if (s.na_reason) lines.push('本轮不评分的原因：' + s.na_reason);
  if (!lines.length) lines.push('本次没有可展示的判定理由');
  return lines.join('\n');
}

export function evProducts(c) {
  if (!c) return null;
  const items = [];
  for (const a of (c.artifacts || []))
    items.push({ type: a.kind || '', path: a.path || '', abs: a.abs_path || '' });
  if (!items.length) {
    for (const it of ((((c.objective_facts || {}).产物) || {}).产物清单 || []))
      items.push({ type: it['类型'] || '', path: it['文件'] || '', abs: it['绝对路径'] || '' });
  }
  if (!items.length) return null;
  const nameOf = x => String(x.path || x.abs || '').split('/').pop() || '(未命名)';
  return { items, names: items.map(nameOf), ok: items.filter(x => x.abs) };
}

export function evArtBtn(p) {
  if (!p) return '';
  const tip = '产物：' + p.names.join('、')
    + (p.ok.length ? `\n点击在${FM_NAME()}中显示：${p.ok[0].abs}`
                 : '\n本机没找到产物文件（按工作区解析不出绝对路径）');
  if (!p.ok.length)
    return `<span class="evt-art disabled" title="${esc(tip)}">查看产物</span>`;
  return `<a class="evt-art" href="#" data-path="${esc(p.ok[0].abs)}" title="${esc(tip)}"`
    + ` onclick="revealInFinder(event,this)">查看产物${p.ok.length > 1 ? ' (' + p.ok.length + ')' : ''}</a>`;
}

export function evCell(a, c) {
  if (!a) return '<td class="evt-empty"></td>';
  const isNew = (a.nature !== undefined);
  const tag = isNew
    ? (a.basis === 'objective'
        ? `<span class="src-tag objective" title="确定性硬规则覆盖模型判定（安全红线）">硬规则</span>`
        : '')
    : (state.EV_BASIS[a.basis] ? `<span class="src-tag ${esc(a.basis)}">${esc(state.EV_BASIS[a.basis])}</span>` : '');
  const name = `<span class="evt-name" title="${esc(dimStdTip(a))}">${esc(a.label)}${tag}</span>`;
  const why = esc(dimWhyTip(a));
  const drift = (a.score != null && a.hint != null && Math.abs(a.score - a.hint) >= 0.05)
    ? `<span class="evt-n" title="本地算出的参考分 ${a.hint}：同一套算法得出的确定值，只作对比">参考 ${a.hint}</span>` : '';
  const cv = a.score == null
    ? `<span class="evt-na" title="${why}">—`
      + (a.na_reason ? `<i>${esc(a.na_reason.length > 14 ? a.na_reason.slice(0, 14) + '…' : a.na_reason)}</i>` : '') + '</span>'
    : `<span class="evt-val" title="${why}"><b class="evt-score ${a.score >= 4 ? 'ok' : (a.score >= 3 ? 'warn' : 'bad')}">`
      + `${a.score}</b>${drift}</span>`;
  const art = (a.key === 'aesthetics') ? evArtBtn(evProducts(c)) : '';
  return `<td><div class="evt-row">${name}${cv}${art}</div></td>`;
}

const clip = (s, n) => { s = String(s ?? ''); return s.length > n ? s.slice(0, n) + '…' : s; };

export function dimRank(sm) {
  const order = sm.dim_order || [];
  return (a, b) => {
    const ia = order.indexOf(a.key), ib = order.indexOf(b.key);
    return (ia < 0 ? 999 : ia) - (ib < 0 ? 999 : ib);
  };
}

export function evCaseTable(c, i, sm) {
  const cols = evCols(sm);
  const dims = (c.scores || []).slice().sort(dimRank(sm));
  const byCol = cols.map(col => dims.filter(d => col.groups.includes(d.group)));
  const rows = Math.max(1, ...byCol.map(v => v.length));
  let t = '<table class="ev-tbl"><thead><tr>'
    + cols.map(col => `<th title="${esc(col.label)}">${esc(col.label)}</th>`).join('')
    + '</tr></thead><tbody>';
  for (let r = 0; r < rows; r++)
    t += '<tr>' + cols.map((col, j) => evCell(byCol[j][r], c)).join('') + '</tr>';
  t += '<tfoot><tr>' + cols.map((col, j) => {
    const vals = byCol[j].filter(d => d.score != null).map(d => d.score);
    const m = vals.length ? Math.round(vals.reduce((x, y) => x + y, 0) / vals.length * 10) / 10 : null;
    const tip = `${col.label} 在本用例已评分的 ${vals.length} 项指标上的平均分（不跨用例合并）`;
    return `<td class="evt-total" title="${esc(tip)}">维度均分 `
      + (m != null ? `<b>${m}</b>` : '<span style="color:var(--tx3)">—</span>') + '</td>';
  }).join('') + '</tr></tfoot></table>';
  const je = String(c.judge_error || '');
  const meta = [];
  const sceneLbl = c.scene ? ((c.scene_kind === 'teaching' ? '教学·' : '') + c.scene)
    : '未分类（默认按结果质量评）';
  meta.push(`<span class="ver">${esc(sceneLbl)}`
    + (c.scene_source === 'inferred'
      ? `<span class="tag-infer" title="用例未声明场景，按问题原文推断，命中：${esc((c.scene_evidence || []).join('、'))}">推断</span>`
      : '') + '</span>');
  if (c.requirement_kinds !== undefined) {
    meta.push(`<span class="ver">产物要求：`
      + ((c.requirement_kinds || []).length
        ? `<span class="mono">${c.requirement_kinds.map(esc).join('、')}</span>`
        : '无（未要求交付产物）')
      + ((c.requirement_source === 'inferred') ? '（按原文推断）' : '') + '</span>');
  }
  if (je) meta.push(`<span class="pill bad" title="${esc(je)}">打分失败</span>`);
  if (c.degraded_input) meta.push(`<span class="pill warn" title="${esc(c.input_note || '')}">降级输入</span>`);

  const factsBox = c.objective_facts
    ? `<div class="ev-facts" onclick="this.classList.toggle('open')">`
      + `<div class="ev-facts-hd">模型收到的客观事实（打分依据）`
      + `<span class="ev-facts-tip">点击展开 / 收起</span></div>`
      + `<pre class="pb-detail">${esc(JSON.stringify(c.objective_facts, null, 1))}</pre></div>` : '';
  return `<div class="card ev-case">
    <div class="ev-case-hd">
      <div class="ev-case-tt"><b style="font-size:12.5px">${esc(c.case_id)}</b>
        <span class="ev-case-prompt" title="${esc(c.prompt || '')}">${esc(clip(c.prompt || '（没有提问）', 78))}</span></div>
      <div class="ev-case-meta">${meta.join('')}</div>
      ${je ? `<div class="ev-case-err">打分失败的原因：${esc(je)}`
        + (c.judge_raw ? `<div class="pb-detail">${esc(c.judge_raw)}</div>` : '') + '</div>' : ''}
    </div>${t}${factsBox}</div>`;
}

export function evalTab() {
  const j = state.lastRoundData || {};
  if (!j.run_id) return '<div class="empty">请先选择一个轮次</div>';
  if (state.EVAL_STATE && state.EVAL_STATE.running) return evRunning();
  if (!j.evaluation) return evEmpty();
  return evResult(j.evaluation);
}

export function evEmpty() {
  const cfg = state.LLM_SRV && state.LLM_SRV.configured;
  return `<div class="empty" style="padding:36px 0;text-align:center">
    <div style="font-size:15px;font-weight:600;color:var(--tx);margin-bottom:8px">该轮次尚未进行质量评估</div>
    <div style="font-size:12.5px;color:var(--tx2);margin-bottom:18px;line-height:1.6;max-width:540px;margin-left:auto;margin-right:auto">
      调用大模型对用例的<b>结果质量</b>、<b>安全可靠</b>与 <b>Agent 系统能力</b>进行多维度自动化打分。
      ${cfg ? '' : '<br><span style="color:var(--tx3);font-size:11.5px">（提示：需在右上角「⚙ 设置 → 模型设置」中配置好可用模型）</span>'}
    </div>
    <div style="display:flex;gap:10px;justify-content:center;align-items:center">
      <button class="btn-primary" style="padding:8px 22px" onclick="startEval()">开始评估</button>
      ${cfg ? '' : '<button class="btn-ghost" onclick="openLlm()">⚙ 配置模型</button>'}
    </div>
  </div>`;
}

export function evRunning() {
  const s = state.EVAL_STATE || {}, done = s.done || 0, total = s.total || 0;
  const pct = total ? Math.round(done * 100 / total) : 0;
  return `<div class="eval-bar"><b style="font-size:12.5px">评估进行中</b>
      <span class="pill">${done} / ${total || '—'}</span>
      <span class="spacer"></span>
      <button class="btn-stop" onclick="stopEval()">停止</button></div>
    <div class="pbar"><i style="width:${pct}%"></i></div>
    <div class="eval-console">${(s.lines || []).map(esc).join('<br>') || '启动中…'}</div>`;
}

export function evAlert(sm) {
  const failed = sm.failed_cases || 0, cov = sm.llm_coverage || {};
  const exp = cov.expected || 0, got = cov.scored || 0;
  const zero = exp > 0 && got === 0;
  if (!failed && !zero) return '';
  const errs = sm.judge_errors || [];
  const raw = errs.map(e => String(e.reason || '')).join('\n');
  const reasons = errs.slice(0, 2).map(e => esc(e.reason || '') + (e.count > 1 ? `（${e.count} 条）` : '')).join('<br>');
  let tip = '';
  if (/403|allowlist|白名单|Source IP|来源 IP/i.test(raw))
    tip = '请到模型供应商控制台，把这个来源 IP 加进 API Key 白名单（出口 IP 可能会变，必要时按网段放行）。';
  else if (/未配置模型名/.test(raw))
    tip = '请先在「模型设置」里填好模型名或端点 ID，再重新评估。';
  else if (/模型输出无法解析/.test(raw))
    tip = '模型返回的不是合法 JSON：可以把 config.yaml 里的 llm.max_tokens 调大后重试。';
  return `<div class="ev-alert">
    <div class="ev-alert-hd">⚠ 本轮评估不完整${failed ? `：${failed} 条用例打分失败` : ''}</div>
    <div class="ev-alert-bd">
      ${zero ? `<div>交给模型打分的 <b>0 / ${exp}</b> 项拿到了分值 —— 本轮只剩硬规则的分，<b>不代表模型判定结果</b>。</div>` : ''}
      ${reasons ? `<div class="mono">${reasons}</div>` : ''}
      ${tip ? `<div>${tip}</div>` : ''}
    </div></div>`;
}

export function evResult(ev) {
  const sm = ev.summary || {}, cases = ev.cases || [];
  state.EV_RUBRIC = ev.rubric || {};
  let h = '<div class="eval-bar"><span class="ver">生成于 ' + esc(ev.generated_at || '') + '</span>'
    + `<span class="ver">耗时 ${ev.elapsed_s ?? '—'}s</span>`
    + `<span class="ver">judge token ${(ev.judge_usage || {}).total_tokens ?? 0}</span>`
    + '<span class="spacer"></span><button class="btn-ghost" onclick="startEval()">重新评估</button></div>';
  h += evAlert(sm);
  const errs = (ev.errors || []).filter(e => e.kind !== 'judge');
  if (errs.length) {
    h += `<div class="note warn">${errs.length} 条用例处理异常：`
      + errs.slice(0, 3).map(e => esc((e.case_id || '') + ' ' + (e.error || ''))).join('；') + '</div>';
  }
  h += '<h3 class="sec">逐用例评分'
    + '<span class="gr-hint">鼠标停在指标名上看评分标准，停在分数上看打分理由；每张表最后一行是该用例各维度的平均分</span></h3>';
  cases.forEach((c, i) => { h += evCaseTable(c, i, sm); });
  h += '<h3 class="sec" style="margin-top:16px">成本与效率</h3>' + evCost(ev);
  return h;
}

export function evCost(ev) {
  const u = ev.judge_usage || {};
  let tin = 0, tout = 0;
  for (const c of ev.cases || []) {
    for (const s of (c.scores || [])) {
      if (s.key === 'cost_control') {
        tin += (s.evidence || {}).input_tokens_est || 0;
        tout += (s.evidence || {}).output_tokens_est || 0;
      }
    }
  }
  return '<div class="tbl-card"><table>'
    + `<tr><td>评审模型 token（输入 / 输出）</td><td style="text-align:right"><b>${u.input_tokens ?? 0} / ${u.output_tokens ?? 0}</b></td></tr>`
    + `<tr><td>被测智能体 token 估算（输入 / 输出）</td><td style="text-align:right"><b>${tin} / ${tout}</b></td></tr>`
    + `<tr><td>评估总耗时</td><td style="text-align:right"><b>${ev.elapsed_s ?? '—'}s</b></td></tr>`
    + '</table></div>'
    + '<div class="note" style="margin-top:8px">被测智能体的 token 是按字符量折算估出来的（日志里没有官方 usage）；评审模型的 token 来自接口返回的 usage。</div>';
}

export async function startEval() {
  const j = state.lastRoundData || {};
  if (!j.run_id) return;
  if (!state.LLM_SRV.configured) {
    alert('还没有配置模型：请先在「模型设置」里填好供应商地址、API Key 和模型名，并通过「测试连接」。');
    return;
  }
  try {
    const r = await fetch('/api/evaluate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ run_id: j.run_id })
    });
    const x = await r.json();
    if (!x.ok) {
      const p = x.preflight || {};
      alert('无法开始评估：' + (x.message || '未知错误')
        + (p.status ? `\n\nHTTP ${p.status}` : '')
        + (p.detail ? `\n供应商返回：${p.detail}` : ''));
      if (state.currentTab === 'eval' && $('tabBody')) $('tabBody').innerHTML = evalTab();
      return;
    }
    state.EVAL_STATE = { running: true, done: 0, total: 0, lines: [] };
    if (state.currentTab === 'eval' && $('tabBody')) $('tabBody').innerHTML = evalTab();
    pollEval();
  } catch (e) {
    alert('请求失败：' + e.message);
  }
}

export async function stopEval() {
  try { await fetch('/api/evaluate/stop', { method: 'POST' }); } catch (e) {}
}

export async function refreshEvalStatus() {
  try {
    state.EVAL_STATE = await (await fetch('/api/eval/status')).json();
  } catch (e) {
    return null;
  }
  if (state.currentTab === 'eval' && $('tabBody')) $('tabBody').innerHTML = evalTab();
  return state.EVAL_STATE;
}

export function pollEval() {
  if (state.EVAL_TIMER) clearInterval(state.EVAL_TIMER);
  state.EVAL_TIMER = setInterval(async () => {
    const s = await refreshEvalStatus();
    if (!s) return;
    if (!s.running && s.finished) {
      clearInterval(state.EVAL_TIMER);
      state.EVAL_TIMER = null;
      if (s.error) alert('评估结束，但有错误：' + s.error);
      if (state.currentRound) await openRoundById(state.currentRound);
    }
  }, 1500);
}

/* ---------- 注册历史页事件 ---------- */
export function initHistory() {
  const btnQuery = $('histQuery');
  if (btnQuery) {
    btnQuery.onclick = () => {
      state.RF.date_from = $('histFrom')?.value || '';
      state.RF.date_to = $('histTo')?.value || '';
      state.RF.keyword = $('histKw')?.value.trim() || '';
      roundQuery();
    };
  }

  const histKw = $('histKw');
  if (histKw) {
    histKw.addEventListener('keydown', e => {
      if (e.key === 'Enter') {
        e.preventDefault();
        $('histQuery')?.click();
      }
    });
  }

  const histToday = $('histToday');
  if (histToday) {
    histToday.onclick = () => {
      if ($('histFrom')) $('histFrom').value = roundToday();
      if ($('histTo')) $('histTo').value = roundToday();
      $('histQuery')?.click();
    };
  }

  const histWeek = $('histWeek');
  if (histWeek) {
    histWeek.onclick = () => {
      const t = new Date();
      const from = new Date(t.getTime() - 6 * 86400000);
      if ($('histFrom')) $('histFrom').value = roundToday(from);
      if ($('histTo')) $('histTo').value = roundToday(t);
      $('histQuery')?.click();
    };
  }

  const histClear = $('histClear');
  if (histClear) {
    histClear.onclick = () => {
      if ($('histFrom')) $('histFrom').value = '';
      if ($('histTo')) $('histTo').value = '';
      if ($('histKw')) $('histKw').value = '';
      state.RF.date_from = '';
      state.RF.date_to = '';
      state.RF.keyword = '';
      roundQuery();
    };
  }

  const rPrev = $('roundPrev');
  if (rPrev) rPrev.onclick = () => { state.RF.offset = Math.max(0, state.RF.offset - state.ROUND_LIMIT); loadRounds(); };
  const rNext = $('roundNext');
  if (rNext) rNext.onclick = () => { state.RF.offset += state.ROUND_LIMIT; loadRounds(); };
}
