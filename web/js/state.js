/* ================= 全局状态与常量 ================= */

export const state = {
  // 运行与轮询状态
  RUNNING: false,
  pollTimer: null,
  currentRound: null,
  currentCase: 0,
  currentTab: 'trace',

  // 路由与页面
  PAGES: ['home', 'cases', 'history'],
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
  CASES: [{ prompt: '', attachments: [] }],
  UPLOADS: [],
  LIB_FOR: -1,

  // 预设用例库
  LIB_LIMIT: 50,
  LIB: { keyword: '', scene: '', targets: [], attachment: '', offset: 0 },
  LIB_ROWS: [],
  LIB_TOTAL: 0,
  LIB_SCENES: [],
  LIB_TARGETS: [],
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
