/* 多轮对话冒烟验证（2026-10-16）
   用真实 index.html 建 DOM，导入真实前端模块，只 stub 网络与弹窗。
   验证：单/多轮标签、轮次编辑区、提交体形状、队列行轮次弹窗、运行提交体。

   运行：本文件必须与 node_modules 同级（ESM 不认 NODE_PATH）：
         cp tests/turns-smoke.mjs /Users/amano/.workbuddy/binaries/node/workspace/
         cd /Users/amano/.workbuddy/binaries/node/workspace && node turns-smoke.mjs
   （本文件放在项目内是为了受工作区写权限约束，依赖仍取工作区外的 node_modules） */
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { JSDOM } from 'jsdom';

const WEB = '/Users/amano/WorkSpace/ruiyun-ui-test-platform-go/internal/server/web';
const results = [];
const ok = (name, cond, extra = '') => results.push([!!cond, name, extra]);

const dom = new JSDOM(readFileSync(WEB + '/index.html', 'utf8'), {
  url: 'http://127.0.0.1:8765/cases',
  pretendToBeVisual: true
});
const { window } = dom;
for (const k of ['document', 'navigator', 'localStorage', 'KeyboardEvent', 'MouseEvent', 'Event', 'getComputedStyle']) {
  try { globalThis[k] = window[k]; }
  catch (e) { Object.defineProperty(globalThis, k, { value: window[k], configurable: true }); }
}
globalThis.window = window;
globalThis.requestAnimationFrame = cb => setTimeout(() => cb(Date.now()), 0);
globalThis.alert = () => {};
globalThis.confirm = () => true;

let fetches = [];
const LIB = {
  cases: [
    { id: 'C001', name: '备课-001', prompt: ['读一下记忆文件'], labels: { scene: '备课' } },
    { id: 'C002', name: '多轮-002', prompt: ['第一轮：读记忆', '第二轮：改短', '第三轮：翻译'],
      labels: { scene: '备课' }, turn_count: 3 }
  ],
  total: 2,
  scenes: ['备课'],
  difficulties: [],
  tags: []
};
let runBody = null;
let addBody = null;
globalThis.fetch = async (url, opt) => {
  const u = String(url);
  const method = (opt || {}).method || 'GET';
  fetches.push({ url: u, method, body: (opt || {}).body });
  if (u.includes('/api/run') && method === 'POST') {
    runBody = JSON.parse((opt || {}).body || '{}');
    return { ok: true, status: 200, json: async () => ({ ok: true, run_id: 'run_x', message: 'run_x' }) };
  }
  if (u.includes('/api/preset-cases') && method === 'POST') {
    addBody = JSON.parse((opt || {}).body || '{}');
    return { ok: true, status: 200, json: async () => ({ ok: true, case: { id: 'C999' }, message: '已添加' }) };
  }
  return { ok: true, status: 200, json: async () => LIB };
};

const { state } = await import(pathToFileURL(WEB + '/js/state.js').href);
const pc = await import(pathToFileURL(WEB + '/js/page-cases.js').href);
const ph = await import(pathToFileURL(WEB + '/js/page-home.js').href);
const hist = await import(pathToFileURL(WEB + '/js/page-history.js').href);
const u = await import(pathToFileURL(WEB + '/js/utils.js').href);
const caseTurns = u.caseTurns;
const caseTurnCount = u.caseTurnCount;

const $ = id => document.getElementById(id);
const click = el => el.dispatchEvent(new window.MouseEvent('click', { bubbles: true, cancelable: true }));
const tick = () => new Promise(r => setTimeout(r, 0));

pc.initCases();
await pc.loadPresetLib();
await tick();

/* ---- 1. 工具函数 ---- */
ok('toTurns 认数组', JSON.stringify(u.toTurns(['a', 'b'])) === '["a","b"]');
ok('toTurns 对裸字符串兜底为单轮', JSON.stringify(u.toTurns('a')) === '["a"]');
ok('toTurns 空值返回空数组', u.toTurns(null).length === 0 && u.toTurns(undefined).length === 0);
ok('turnCount 忽略空轮', u.turnCount(['a', '', '  ']) === 1);
ok('isMultiTurn 判定正确', u.isMultiTurn(['a']) === false && u.isMultiTurn(['a', 'b']) === true);
ok('单轮标签文案', u.turnsBadge(['a']).includes('单轮'));
ok('多轮标签带轮数', u.turnsBadge(['a', 'b', 'c']).includes('多轮 · 3'));
ok('空用例不显示标签', u.turnsBadge([]) === '');

/* ---- 2. 预设库列表 ---- */
const libHtml = $('libTable').innerHTML;
ok('库里单轮用例显示「单轮」', libHtml.includes('单轮'));
ok('库里多轮用例显示「多轮 · 3」', libHtml.includes('多轮 · 3'));
ok('多轮行只显示第 1 轮 + 省略提示', libHtml.includes('第一轮：读记忆') && libHtml.includes('等 3 轮'));
ok('多轮行的悬停 title 含全部轮次',
  libHtml.includes('第一轮：读记忆') && libHtml.includes('第三轮：翻译'));

/* ---- 3. 对话框轮次编辑区 ---- */
click($('btnLibAdd'));
ok('对话框打开', $('caseAddMask').classList.contains('on'));
ok('轮次区初始为空（单轮用例）', $('caTurns').children.length === 0);
ok('第 1 轮标签已改为「第 1 轮提问」', $('caPrompt').closest('.fld').querySelector('label').textContent.includes('第 1 轮'));
ok('轮次提示初始不显示多轮', $('caTurnHint').textContent === '');

$('caPrompt').value = '第一轮提问';
pc.addCaTurn();
ok('添加一轮后出现 1 行', $('caTurns').children.length === 1);
ok('新增行标为「第 2 轮」', $('caTurns').textContent.includes('第 2 轮'));
ok('轮次提示显示多轮', $('caTurnHint').textContent.includes('共 2 轮'));

pc.setCaTurn(1, '第二轮提问');
pc.addCaTurn();
pc.setCaTurn(2, '第三轮提问');
ok('三轮时共 2 行（第 2/3 轮）', $('caTurns').children.length === 2);
ok('三轮时提示 3 轮', $('caTurnHint').textContent.includes('共 3 轮'));

pc.delCaTurn(2);
ok('删除第 3 轮后剩 1 行', $('caTurns').children.length === 1);
ok('删除后提示回到 2 轮', $('caTurnHint').textContent.includes('共 2 轮'));

for (let k = 0; k < 10; k++) pc.addCaTurn();
ok('轮数被限制在 8', state.CA_TURNS.length === 8, '实际 ' + state.CA_TURNS.length);
ok('达到上限后「添加一轮」按钮禁用', $('caTurnAdd').disabled === true);

while (state.CA_TURNS.length > 2) state.CA_TURNS.pop();
pc.renderCaTurns();
pc.setCaTurn(1, '第二轮提问');

fetches = [];
click($('caseAddOk'));
await tick();
await tick();
ok('提交发出请求', fetches.some(f => f.method === 'POST' && f.url.includes('/api/preset-cases')));
ok('提交体 prompt 是数组', Array.isArray(addBody && addBody.prompt), JSON.stringify(addBody && addBody.prompt));
ok('提交体含两轮且内容正确',
  addBody && addBody.prompt.length === 2 && addBody.prompt[0] === '第一轮提问' && addBody.prompt[1] === '第二轮提问',
  JSON.stringify(addBody && addBody.prompt));

/* ---- 4. 队列行 ---- */
state.CASES = [{ prompt: [''], attachments: [] }];
pc.renderCases();
ok('队列初始 1 行', document.querySelectorAll('#caseRows .case-row').length === 1);
ok('空行不显示单/多轮标签', !$('caseRows').innerHTML.includes('t-turn'));

pc.setCaseTurn(0, 0, '队列第一轮');
pc.renderCases();
ok('填第 1 轮后显示「单轮」', $('caseRows').innerHTML.includes('单轮'));

const turnsBtn = document.querySelector('#caseRows .cl-acts button[onclick^="openTurns"]');
ok('队列行有「轮次」入口按钮', !!turnsBtn);
ok('入口按钮指向本行', turnsBtn && turnsBtn.getAttribute('onclick') === 'openTurns(0)',
  turnsBtn && turnsBtn.getAttribute('onclick'));
// 内联 onclick 依赖 main.js 把函数挂到 window（本冒烟只加载模块），故直接调用导出函数
pc.openTurns(0);
ok('轮次弹窗打开', $('turnsMask').hidden === false);
ok('弹窗里 1 行（仅第 1 轮）', $('turnsRows').children.length === 1);

pc.addDraftTurn();
pc.setDraftTurn(1, '队列第二轮');
pc.saveTurns();
ok('保存后队列行变多轮', u.turnCount(state.CASES[0].prompt) === 2, JSON.stringify(state.CASES[0].prompt));
ok('保存后弹窗关闭', $('turnsMask').hidden === true);
ok('队列行显示「多轮 · 2」', $('caseRows').innerHTML.includes('多轮 · 2'));

/* ---- 5. 运行提交体 ---- */
state.CASES = [
  { prompt: ['单轮问题'], attachments: [] },
  { prompt: ['第一轮', '第二轮'], attachments: [] }
];
state.RUNNING = false;
ph.doRun();
await tick();
ok('运行请求已发出', runBody !== null);
ok('case_items 两条', runBody && runBody.case_items.length === 2);
ok('单轮用例提交为长度 1 的数组',
  runBody && Array.isArray(runBody.case_items[0].prompt) && runBody.case_items[0].prompt.length === 1);
ok('多轮用例提交为长度 2 的数组',
  runBody && runBody.case_items[1].prompt.length === 2
  && runBody.case_items[1].prompt[0] === '第一轮' && runBody.case_items[1].prompt[1] === '第二轮');

/* ---- 6. 全空轮不进运行 ---- */
runBody = null;
state.RUNNING = false;
state.CASES = [{ prompt: ['', '  '], attachments: [] }];
ph.doRun();
await tick();
ok('全空轮用例不进运行提交体', runBody === null);

/* ---- 7. 评估口径开关 ---- */
// 只有多轮用例存在时开关才出现
state.lastRoundData = { run_id: 'run_1', cases: [{ case_id: 'C1', prompt: ['a'] }] };
ok('全是单轮用例时不显示口径开关', hist.roundHasMultiTurn() === false);
state.lastRoundData = { run_id: 'run_1', cases: [
  { case_id: 'C1', prompt: ['a'] },
  { case_id: 'C2', prompt: ['a', 'b'] }
] };
ok('存在多轮用例时显示口径开关', hist.roundHasMultiTurn() === true);

state.currentTab = 'eval';
state.EVAL_SCOPE = 'case';
let evalBody = null;
globalThis.fetch = async (url, opt) => {
  const u = String(url);
  fetches.push({ url: u, method: (opt || {}).method || 'GET', body: (opt || {}).body });
  if (u.includes('/api/evaluate')) {
    evalBody = JSON.parse((opt || {}).body || '{}');
    return { ok: true, status: 200, json: async () => ({ ok: true, run_id: 'run_1', message: 'run_1' }) };
  }
  return { ok: true, status: 200, json: async () => LIB };
};
state.LLM_SRV = { configured: true };
state.EVAL_STATE = null;

await hist.startEval();
ok('评估请求已发出', evalBody !== null);
ok('默认走整用例口径', evalBody && evalBody.mode === 'case', JSON.stringify(evalBody));

state.EVAL_SCOPE = 'turn';
evalBody = null;
await hist.startEval();
ok('选逐轮后提交 turn 口径', evalBody && evalBody.mode === 'turn', JSON.stringify(evalBody));

// 单轮用例集合下即使选了逐轮也回落成整用例
state.lastRoundData = { run_id: 'run_1', cases: [{ case_id: 'C1', prompt: ['a'] }] };
evalBody = null;
await hist.startEval();
ok('单轮用例下逐轮口径回落为整用例', evalBody && evalBody.mode === 'case', JSON.stringify(evalBody));

// setEvalScope 归一化非法值
hist.setEvalScope('turn');
ok('setEvalScope 接受 turn', state.EVAL_SCOPE === 'turn');
hist.setEvalScope('乱七八糟');
ok('setEvalScope 把非法值归一成 case', state.EVAL_SCOPE === 'case');

/* ---- 归档轮次字段（prompts 优先于 prompt）---- */
// 归档里 prompt 只保留第 1 轮、prompts 才是完整轮次。若只看 prompt，
// 多轮用例在历史页会被显示成单轮（用户报告的问题）。
const archived = { case_id: 'C1', prompt: '你好，我是翟嘉乐', prompts: ['你好，我是翟嘉乐', '我是谁？'], turn_count: 2 };

ok('caseTurns 优先读 prompts', JSON.stringify(caseTurns(archived)) === JSON.stringify(['你好，我是翟嘉乐', '我是谁？']),
  JSON.stringify(caseTurns(archived)));
ok('caseTurnCount 数到 2 轮', caseTurnCount(archived) === 2, String(caseTurnCount(archived)));
ok('caseTurns 在无 prompts 时回落 prompt', JSON.stringify(caseTurns({ prompt: '只有一轮' })) === JSON.stringify(['只有一轮']),
  JSON.stringify(caseTurns({ prompt: '只有一轮' })));
ok('caseTurns 在 prompts 为空数组时回落 prompt', JSON.stringify(caseTurns({ prompt: '回落', prompts: [] })) === JSON.stringify(['回落']),
  JSON.stringify(caseTurns({ prompt: '回落', prompts: [] })));
ok('caseTurns 容忍空对象', caseTurns({}).length === 0);
ok('caseTurns 容忍 null', caseTurns(null).length === 0);

// 历史页应据此识别出多轮
state.lastRoundData = { run_id: 'r', cases: [archived] };
ok('roundHasMultiTurn 能识别归档里的多轮', hist.roundHasMultiTurn() === true);

// 轮次折叠预览也要看到两轮
const prev = hist.promptPreview({ cases: [archived] });
ok('promptPreview 展示多轮（含 + 计数不适用但含首轮）', prev.includes('你好，我是翟嘉乐'), prev.slice(0, 120));

/* ---- 归档用例详情：多轮必须按时间线逐轮展示（调用链路 + 输出内容）---- */
// 归档只给平铺 steps 时，界面上所有轮次的步骤混在一起、输出只剩最后一轮。
// round_detail.json 的 turns[].steps / turns[].answer 才能按轮还原。
const stepTool = (i, name) => ({ i, type: 'tool_call', name, status: 'ok', args_summary: '{}', result: 'r', result_len: 1, raw_len: 1, issues: [] });

const archivedTurns = {
  case_id: 'C1', name: '多轮用例', status: 'PASS', session_id: 'sess_x',
  prompts: ['你好，我是翟嘉乐', '我是谁？'], turn_count: 2,
  elapsed_s: 12.3, tool_call_count: 2, thinking_count: 1, answer_chars: 5,
  answer_excerpt: '你是翟嘉乐',
  // 会话级平铺步骤（含一条不属于任何一轮的自动确认）
  steps: [stepTool(0, 'tool_a'), stepTool(1, 'tool_b'),
    { type: 'auto_confirm', time: '16:50:40', label: '确认授权', text: '允许' }],
  turns: [
    { index: 0, prompt: '你好，我是翟嘉乐', answer: '你好呀小助手答复', answer_excerpt: '你好呀小助手答复',
      answer_chars: 8, tool_calls: 1, thinking_steps: 0, elapsed_s: 8.1, steps: [stepTool(0, 'tool_a')] },
    { index: 1, prompt: '我是谁？', answer: '你是翟嘉乐', answer_excerpt: '你是翟嘉乐',
      answer_chars: 5, tool_calls: 1, thinking_steps: 1, elapsed_s: 4.2, steps: [stepTool(1, 'tool_b')] }
  ]
};
const multiHtml = hist.caseTimeline(archivedTurns);
ok('多轮用例按轮分段', (multiHtml.match(/class="turn-sec"/g) || []).length === 2,
  String((multiHtml.match(/class="turn-sec"/g) || []).length));
ok('每轮各带自己的提问', multiHtml.includes('你好，我是翟嘉乐') && multiHtml.includes('我是谁？'));
ok('每轮各带自己的输出内容', multiHtml.includes('你好呀小助手答复') && multiHtml.includes('你是翟嘉乐'));
ok('输出内容按时间线排列', multiHtml.indexOf('你好呀小助手答复') < multiHtml.indexOf('你是翟嘉乐'));
ok('不再把最后一轮答复重复到顶部', (multiHtml.match(/你是翟嘉乐/g) || []).length === 1,
  String((multiHtml.match(/你是翟嘉乐/g) || []).length));

const secs = multiHtml.split('class="turn-sec"').slice(1);
ok('第 1 轮只渲染本轮步骤', secs[0] && secs[0].includes('tool_a') && !secs[0].includes('tool_b'));
ok('第 2 轮只渲染本轮步骤', secs[1] && secs[1].includes('tool_b') && !secs[1].includes('tool_a'));
ok('会话级自动确认不丢', multiHtml.includes('自动确认') && multiHtml.includes('16:50:40'));

// 旧归档没有 turns[].steps：保持原来的平坦展示，不凭空造轮次
const legacyArchive = {
  case_id: 'C2', name: '旧归档', status: 'PASS', session_id: 'sess_y',
  prompts: ['第一轮', '第二轮'], turn_count: 2, elapsed_s: 3,
  tool_call_count: 1, thinking_count: 0, answer_chars: 4, answer_excerpt: '末轮答复',
  steps: [stepTool(0, 'tool_old')],
  turns: [
    { index: 0, prompt: '第一轮', answer: 'a0', answer_chars: 2 },
    { index: 1, prompt: '第二轮', answer: 'a1', answer_chars: 2 }
  ]
};
const legacyHtml = hist.caseTimeline(legacyArchive);
ok('旧归档回落平坦展示', !legacyHtml.includes('class="turn-sec"'));
ok('旧归档仍展示平铺步骤', legacyHtml.includes('tool_old'));
ok('旧归档仍展示末轮答复', legacyHtml.includes('末轮答复'));

/* ---- 汇总 ---- */
// startEval 会起 pollEval 的 setInterval 轮询；冒烟不验证轮询，
// 不清掉它 Node 会一直不退出（不是产品问题）。
if (state.EVAL_TIMER) { clearInterval(state.EVAL_TIMER); state.EVAL_TIMER = null; }

let pass = 0;
for (const [good, name, extra] of results) {
  if (good) { pass++; console.log('  ✓ ' + name); }
  else console.log('  ✗ ' + name + (extra ? '   [' + extra + ']' : ''));
}
console.log(`\n${pass}/${results.length} 通过`);
if (pass !== results.length) process.exit(1);
