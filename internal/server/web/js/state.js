/* ================= 全局状态与常量 ================= */

export const state = {
  // 运行与轮询状态
  RUNNING: false,
  pollTimer: null,
  currentRound: null,
  currentCase: 0,
  currentTab: 'trace',

  // 路由与页面
  PAGES: ['home', 'cases', 'history', 'notify'],
  PAGE_KEY: 'ruiyun_page',
  currentPage: 'home',

  // 首页当前用例
  RUN_CASES: [],
  LATEST_RUN_ID: '',

  // 环境配置与应用实例
  ENV_CFG: null,
  ENV_ERR: '',

  // 模型设置
  LLM_STORE: 'ruiyun_llm_cfg',
  LLM_SRV: { configured: false, base_url: '', model: '', key_hint: '', saved_at: '' },
  LLM_TESTED: false,

  // 并发数与设置
  AUTO_CLICK: true,
  INFLIGHT: 5,
  INFLIGHT_MIN: 1,
  INFLIGHT_MAX: 20,

  // 控制台
  CONSOLE_OPEN: true,
  CONSOLE_PENDING: 0,
  CONSOLE_TOUCHED: false,
  CONSOLE_HEIGHT: parseInt(localStorage.getItem('ruiyun_console_h') || '210', 10) || 210,

  // 应用设置描述
  APP_CFG_DESC: null,

  // 本轮队列用例与附件库
  // prompt 是**轮次数组**（单轮也是长度 1 的数组），与接口/库里的格式一致
  CASES: [{ prompt: [''], attachments: [] }],
  UPLOADS: [],
  // 附件库弹窗的目标：-1 未打开 / 数字 = 队列行序号 / 'caseadd' = 新增用例对话框
  LIB_FOR: -1,

  // 新增用例对话框内待保存的附件（{ name, path, size }），提交时取 path 数组入库
  CA_ATTS: [],
  CA_ATT_MAX: 5,
  // 对话框里待保存的自定义标签（字符串数组，数量与内容都不限）；
  // CA_EDIT_ID 非空表示对话框正处于「编辑已有用例」状态，提交时走 action:'update'
  CA_TAGS: [],
  CA_EDIT_ID: '',
  // 对话框中待保存的多轮提问（字符串数组，第 1 轮与 #caPrompt 双向同步）
  CA_TURNS: [''],

  // 队列行「轮次」弹窗：TURNS_ROW 为队列行序号（-1 表示未打开），TURNS_DRAFT 为待保存轮次
  TURNS_ROW: -1,
  TURNS_DRAFT: [],

  // 预设用例库
  // 筛选条件：scenes / difficulties / tags 为空数组表示不限；attachment 取 ''（不限）/ 'yes' / 'no'，
  // 与后端 GET /api/preset-cases 的 scene、difficulties、tags（均为逗号拼接）、attachment 参数一一对应。
  LIB_LIMIT: 50,
  LIB: { keyword: '', scenes: [], difficulties: [], tags: [], attachment: '', offset: 0 },
  LIB_ROWS: [],
  LIB_TOTAL: 0,
  LIB_SCENES: [],
  LIB_DIFFICULTIES: [],
  LIB_TAGS: [],
  PSEL: new Set(),
  libKwTimer: null,

  // Excel 导入
  IMPX: null,

  // 历史轮次
  ROUND_DIR: '',
  lastRounds: [],
  ROUND_LIMIT: 20,
  RF: { date_from: '', date_to: '', keyword: '', offset: 0 },
  ROUND_TOTAL: 0,
  lastRoundData: null,

  // 质量评估
  EV_BASIS: { objective: '客观', llm: '模型', hybrid: '混合' },
  EV_RUBRIC: {},
  EVAL_STATE: null,
  EVAL_TIMER: null
};

// 桥接至 window，确保模板内联表达式（如 CASES[i].prompt=this.value）能够无缝访问
['RUNNING', 'CASES', 'currentRound', 'currentCase', 'currentTab', 'currentPage', 'RUN_CASES', 'UPLOADS', 'PSEL', 'lastRounds'].forEach(prop => {
  try {
    Object.defineProperty(window, prop, {
      get() { return state[prop]; },
      set(val) { state[prop] = val; },
      configurable: true,
      enumerable: true
    });
  } catch (e) {}
});

// 暴露 state 供控制台调试
window.state = state;
