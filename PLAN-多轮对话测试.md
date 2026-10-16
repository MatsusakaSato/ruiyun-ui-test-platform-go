# 多轮对话测试 · 实施方案

> 状态：**切轮可行性已验证通过**（未动一行生产代码）
> 前置规则：本方案中**所有**未决项一律先问后做，不自行决定。已写入
> `.workbuddy/memory/MEMORY.md` 头号协作规则。

---

## 0. 你已拍板的决策（本方案的事实基准）

| # | 决策点 | 你的选择 |
|---|---|---|
| 1 | 多轮载体 | **A** 一个用例内定义 N 条提问，依次发进**同一个会话** |
| 2 | 存储 | 复用 `prompt` 列，**全库统一为 JSON 数组格式** |
| 3 | 存量数据 | **删掉旧数据**（预设用例 + 附件库 + 历史轮次归档），prompt 直接上新格式 |
| 4 | 删除时机 | **立即删**，不等功能完成 |
| 5 | 断言粒度 | **C** 两级都要：整会话 + 每轮 |
| 6 | 逐轮断言配置 | **C** 不开放配置，只把断言结果**按轮拆分展示** |
| 7 | 执行模型 | 用例**内串行**、用例**间并行**（保持 `max_inflight=5`） |
| 8 | 某轮失败 | **立即中止**该用例剩余轮次 |
| 9 | 超时 | `case_timeout_s` 语义改为**每轮上限**；**另加** `case_total_timeout_s`，默认 **3600s** |
| 10 | 后端 API | 复用 `/api/preset-cases`，`prompt` 字段改收**数组** |
| 11 | 前端形态 | 用例仍是**一行**，新增**只读标签**自动标识单轮/多轮（轮数>1 即多轮） |
| 12 | 多轮编辑入口 | 在现有「新增/编辑用例」对话框 `#caseAddMask` 内**增加轮次编辑区** |
| 13 | Excel | **支持多轮**：新增多列 `prompt_1` … `prompt_8` |
| 14 | 轮数上限 | **8 轮** |
| 15 | 报告 | HTML 报告**按轮展开** |
| 16 | 评估 | 评估页加**开关**：整用例评分 ↔ 逐轮评分 |
| 17 | 判重键 | 全部轮内容**按顺序**拼接后参与判重 |
| 18 | 通过性判定 | **B 只看会话级**（Q1）——轮级 finding 仅用于定位展示 |
| 19 | 接口兼容 | **A 只接受数组**（Q2），字符串入参一律拒绝 |
| 20 | 清库机制 | **B 我直接删**（Q3）：不加 CLI 命令、不加 API 接口 |
| 21 | 清库范围 | **A 全清**：`testcases` 表 + 附件库 `uploads/` + 历史轮次 `rounds/` |
| 22 | 耗时字段 | **A** `ElapsedS` 保持整用例耗时，**另**在每轮记各自耗时（Q4） |
| 23 | 触顶标记 | **A** 用例级 + 轮级**都标**（Q5） |
| 24 | 附件 | **A** 维持用例级，**只在第一轮投递**（Q6） |
| 25 | 无日志轮 | **A** 生成**空轮记录**（Answer/ToolCalls 为空、标未完成）（Q7） |
| 26 | 提交方式 | **A** 按层分多个 commit（数据模型/执行/前端/报告）（Q8） |
| 27 | 断言展示 | **A** 分开展示：用例级一块、每轮各一块 |
| 28 | 前置验证 | **A** 先验证切轮可行性，再写代码 |

---

## 1. 改动范围总览

```
数据模型  internal/models/models.go          ExecutionTrace 增加轮次结构
          internal/logparser/logparser.go    按轮切分（现有 TurnPrompt 基础）
存储      internal/testcasedb/testcasedb.go  prompt 列存 JSON 数组 + 迁移/清库
          internal/config/settings.go        新增 case_total_timeout_s
执行      internal/driver/driver.go          同会话多轮发送 + 每轮等待
          internal/pipeline/pipeline.go      用例内串行发送循环
断言      internal/assertor/assertor.go      按轮跑断言
评估      internal/evaluator/evaluator.go    逐轮评分开关
服务端    internal/server/handlers.go        数组入参、清库接口
          internal/server/server.go          路由
前端      web/index.html                     轮次编辑区 + 单/多轮标签
          web/js/page-cases.js               轮次增删、提交体、标签渲染
          web/js/page-home.js                提交体带轮次
          web/js/state.js                    轮次状态
          web/js/page-eval.js 相关           评估开关
          web/css/cases.css                  样式
报告      internal/report/template.html      按轮展开
配置      config.template.yaml               新参数 + 注释
```

---

## 2. 数据模型

### 2.1 `prompt` 列新格式

`prompt` 列仍为 `TEXT`，值统一为 **JSON 字符串数组**：

```json
["第一轮提问", "第二轮提问", "第三轮提问"]
```

- 单轮用例也存数组：`["只有一个提问"]`
- **不新增列**（你选的是复用 prompt 列）
- `rowToCase` 对外仍返回 `prompt` 字段，但类型由 string 变为 **string[]**

### 2.2 清库（立即执行，不可逆）

| 目标 | 位置 | 处理 |
|---|---|---|
| 预设用例 | SQLite `testcases` 表 | `DELETE FROM testcases` |
| 附件库 | `config.UploadsDir()` | 清空目录内文件 |
| 历史轮次 | `config.RoundsDir()` | 删除全部轮次子目录 |

**执行前我会把待删清单（各表行数 / 文件数 / 目录数 + 绝对路径）列给你确认一次**，
因为删除不可逆。这是唯一一处我建议加一道确认——其余已按你「立即删」的要求执行。

### 2.3 判重键

```
key = CleanWhitespace(join(turns, "\u0000")) + scene + difficulty + attachment
```

轮序参与判重：`["A","B"]` 与 `["B","A"]` 视为不同用例。

---

## 3. 日志与轨迹：按轮切分

`logparser.ParseSession` 现在已为每个 `ToolCall` 打 `TurnPrompt` 标记，且
`trace.TurnCount` 已在统计 user 消息数。**现有基础足够**，需要新增结构化输出：

```go
// models.ExecutionTrace 新增
type TurnTrace struct {
    Index      int             // 0-based
    Prompt     string          // 本轮 user 内容
    UserAt     string
    DoneAt     string
    Answer     string          // 本轮最终答复
    ToolCalls  []*ToolCall     // 本轮工具调用（按 TurnPrompt 归属）
    ThinkingSteps []*ThinkingStep
    FirstStep  int             // 全局 step index 区间
    LastStep   int
    ElapsedS   float64
    Findings   []*Finding      // 每轮断言结果（两级中的「轮」级）
}
```

`ExecutionTrace` 增加 `Turns []*TurnTrace`，**同时保留**现有平铺字段
（`ToolCalls`/`ThinkingSteps`/`FinalAnswer`），保证整会话级断言与既有报告逻辑不破。

⚠️ 关键风险：`TurnPrompt` 按「消息时间顺序最近一条 user」归属。**已用真实日志验证完毕，见 §12。**
结论：切轮方案**成立**，但归属必须改为**按 assistant 消息边界切**，不能只靠「最近一条 user」。

---

## 4. 执行模型（driver + pipeline）

### 4.1 现状
`pipeline.RunUICases` 对每个用例：`ResetToNewTask` → 投附件 → `TypeText` → `Send` →
`WaitNewSession` 拿会话目录 → 加入 inflight，靠 `WaitSomeSettled` 等生成完成。

### 4.2 多轮改造
每个用例变成**内部串行**的多轮循环：

```
ResetToNewTask
for i, turn := range turns:
    if i == 0:
        投附件（仅第一轮）
        TypeText(turn); Send(); sess = WaitNewSession(...)   # 拿会话目录
    else:
        TypeText(turn); Send(); WaitTurnSettled(sess)        # 同一会话，不再新建
    WaitTurnSettled(sess, perTurnTimeout)                    # 每轮等生成完成
    if 该轮失败/超时: 中止该用例剩余轮次; break
```

**难点**：多轮以后不能再靠「新会话目录出现」判断发送成功，必须改用
`driver.WaitSettled(sessDir, ...)`（它已经在看 `session.messages.json` 的
size+mtime 静默 + `looksComplete`）。

需要新增 `driver` 能力：
- `SendFollowUp(sess string)` —— 在同一会话内发送并确认「本轮 assistant 消息已开始增加」
- 复用 `WaitSettled` 判定本轮结束

**并发模型**：用例间仍并行（`max_inflight=5`），但每个用例在自己的 goroutine 里
串行跑它的 N 轮。现有 inflight 结构（`res`/`sess`/`done`/`tSend`）需要改成
「用例级 inflight + 轮级进度」。

⚠️ 这会让 `pipeline.go` 的等待/收集循环有较大重写，是本次**最大的单点风险**。

### 4.3 超时
- `turn_timeout`（原 `case_timeout_s`，1200s）= **每轮**上限
- `case_total_timeout_s`（新，3600s）= 整用例上限，先到先停
- 语义沿用现有「等待上限」而非「超时判异常」：达到上限停止等待、按已有日志出结果，
  不写 `ui_error`、不影响通过性

---

## 5. 断言（两级）

- **轮级**：每轮独立跑 `assertor.RunAssertions`，结果挂 `TurnTrace.Findings`
- **会话级**：沿用现有逻辑跑全量 trace，结果挂 `CaseResult.Findings`
- `CaseResult.Status()` 判定口径需你确认（见 §9 待确认 Q1）
- 逐轮**不开放配置**（你选的 C）：每轮不设独立 `expect_tools`，规则沿用全局 `rules:`

---

## 6. 服务端 API

`/api/preset-cases` 的 `add` / `update`：

```json
{ "action": "add", "prompt": ["第一轮", "第二轮"], "labels": {...}, "attachments": [...] }
```

- `prompt` 接受数组；**是否同时兼容旧字符串入参** → 见 §9 待确认 Q2
- 校验：非空、≤8 轮、每轮非空
- `QueryPresetCases` 返回的 `prompt` 变为数组
- 清库：需要新接口（`POST /api/admin/reset`）或由我直接执行一次性脚本 → 见 §9 待确认 Q3

---

## 7. 前端

### 7.1 用例库列表（`page-cases.js`）
- 行内 `input.prompt` **保留为第 1 轮输入框**，快速录入单轮用例的体验不变
- 行上新增**只读标签**：轮数 > 1 时显示「多轮 · N」，否则显示「单轮」
- 运行队列（`#paneQueue`）每行同理

### 7.2 新增/编辑对话框（`#caseAddMask`）
- 现有「提问」字段 = 第 1 轮
- 下方新增**轮次编辑区**：第 2..N 轮，可 ＋ 添加 / ✕ 删除，上限 8 轮
- 提交体 `prompt` 变为数组
- 必填校验：第 1 轮必填；已添加的轮次不允许为空

### 7.3 提交链路
`page-home.js doRun()` 的 `items[].prompt` 由 string 变 string[]；
`state.CASES` 每项的 `prompt` 由 string 变 string[]。

⚠️ 循环 import（page-home ⇄ page-cases）保持不变，仍靠「函数声明 + 仅使用时调用」。

### 7.4 评估页
加开关「整用例评分 / 逐轮评分」，切换后评估请求带上粒度参数。

---

## 8. 报告

`report/template.html` 按轮展开：每个用例下按 `TurnTrace` 渲染各轮的
提问 / 工具调用 / 思考步 / 答复。会话级汇总保留在用例头部。

---

## 9. 待你确认的剩余问题（**不答复我不动手**）

### Q1 通过性判定口径
两级断言后，`CaseResult.Passed()` 怎么算？
- **A** 会话级无 finding **且** 每轮均无 finding → PASS（最严）
- **B** 只看会话级（轮级仅展示，不影响通过）
- **C** 任一轮有 finding 即 FAIL，其余情况看会话级

### Q2 接口兼容
`/api/preset-cases` 的 `prompt` 字段：
- **A** 只接受数组（干净，旧调用方全改）
- **B** 数组与字符串都接受，字符串自动当单轮（兼容性更好）

### Q3 清库执行方式
- **A** 我写一次性命令 `ruiyun reset-cases`，你手动跑
- **B** 我加 `POST /api/admin/reset`，你在设置里点
- **C** 我在你确认清单后**直接**执行删除（一次性，不进代码库）

### Q4 时间字段语义
`CaseResult.ElapsedS` 现在是整用例耗时。多轮后：
- **A** 保持整用例耗时，另在每轮记各自耗时
- **B** `ElapsedS` 改为「所有轮耗时之和」（多轮下有重叠时与 A 不同）
- **C** 其他

### Q5 等待上限触达的用户可见性
现有 `waited_limit` / `wait_note` 是**用例级**。多轮后某轮触顶时：
- **A** 只标用例级，文案里说明是第几轮触顶
- **B** 用例级 + 轮级都标，UI 分别展示

### Q6 附件与轮的关系
现有附件是**用例级**、在第一轮投递。多轮后：
- **A** 维持：附件只在第一轮投递
- **B** 允许每轮各自带附件（改动显著变大）
- **C** 其他

### Q7 无日志边界的处理
多轮中若某一轮的 user 消息写入了日志、但 assistant 完全没回（应用崩/卡死），
该轮在 `TurnTrace` 里怎么表示？
- **A** 生成一个 `Answer` 为空、`ToolCalls` 为空的轮记录，标为未完成
- **B** 该轮不生成 `TurnTrace`，仅在用例级记 UIError
- **C** 其他

### Q8 代码提交方式
功能完成后：
- **A** 一个 commit 全部提交
- **B** 按层分多个 commit（数据模型 / 执行 / 前端 / 报告）
- **C** 先不提交，等你 review

---

## 10. 验证计划

1. **切轮正确性（最高风险，先做）**：造一个 3 轮真实会话，确认
   `TurnPrompt` 归属与 `TurnTrace` 切分正确；特别验证
   「多轮是否可能合并进一条 assistant 消息」。
2. **单轮回归**：单轮用例全链路行为与改造前**逐字段一致**。
3. **多轮端到端**：3 轮用例跑通，日志、断言、报告、评估均正确。
4. **失败中止**：第 2 轮制造失败，确认第 3 轮不发、用例标失败。
5. **超时**：验证每轮上限与总上限两个参数各自生效。
6. **上限**：9 轮被拒（上限 8）。
7. **后端单测**：`internal/server/handlers_test.go`、`internal/testcasedb/testcasedb_test.go`。
8. **前端冒烟**：沿用 `.workbuddy/memory/MEMORY.md` 记载的 jsdom 冒烟脚本套路
   （`caseadd-smoke.mjs` 需同步更新，因提交体已变数组）。
9. **构建**：`make build` 通过；前端改动后必须重新 build（`//go:embed`）。

---

## 11. 实施顺序（每步做完向你汇报，不连续推进到需要决策的地方）

1. 清库（清单确认后）
2. 数据模型 + logparser 按轮切分 + **第 1 项验证**
3. testcasedb 数组格式 + 判重 + 单测
4. 服务端 API 数组化 + 单测
5. driver + pipeline 多轮执行（最大风险块）
6. 断言两级
7. 前端（列表标签 / 对话框轮次区 / 提交链路）
8. 报告按轮展开
9. 评估开关
10. 配置与文档、全量验证

---

## 12. 切轮可行性验证结果（已完成，真实日志，303 个会话）

> 数据源：`~/.srtclaw/workspace/session/`，303 个真实会话。
> 这一步在动手写代码**之前**跑完，因为整个方案建立在其结论上。

### 12.1 多轮会话真实存在且不少

| user 消息数 | 会话数 |
|---|---|
| 1 | 259 |
| 2 | 19 |
| 3 | 11 |
| 4 | 3 |
| 5 | 5 |
| 6 / 7 / 11 | 各 1 |

**41 个多轮会话**（≥2 轮），足以支撑多轮功能的真实需求，也说明测试场景确实会用到。

### 12.2 每轮 user 对应独立 assistant 消息（核心结论）

压缩连续同角色后的交替模式统计（仅多轮会话）：

| 模式 | 会话数 |
|---|---|
| `uaua` | 15 |
| `uauaua` | 9 |
| `uauauauaua` | 5 |
| `uauauaua` | 3 |
| 其他（含 `s` 系统角色 / 未回复） | 9 |

**主导形态是严格 `u…a` 交替，每轮答复是独立的一条 assistant 消息，不存在
「多轮被合并进一条 assistant 消息」的情况。** 因此按 assistant 消息边界切轮是可靠的。

### 12.3 步骤不会跨消息

多轮会话中共 **409** 个 `tool_call` 步骤，**全部**完整落在其所属 assistant 消息内，
异常 0 个。→ `toolCallId` / `name` / `arguments` 都在消息内可取，轮级归属无歧义。

### 12.4 发现现状缺陷：`TurnPrompt` 归属在异常会话下会错

41 个多轮会话中 **7 个** `user 数 ≠ assistant 数`，其中：

- `sess_1a142b80a10_a24a51`：`你晒黑` / `你是谁` **连续两条 user**，只回了一条
- `sess_1a09ef3918f_92f0f4`：第 2 轮 `1` 触发 `system` 报错
  `[181001] model call failed`，**assistant 完全没回**
- `sess_1a0ad4e74ab_707b2f`：两轮全是 `model call failed`，**0 条 assistant**

**影响**：现有 `currentTurnPrompt = 最近一条 user` 的写法，遇到
「user 无对应 assistant」时，会把**上一轮的** assistant 答复错记到**这一轮**名下。
这正是 Q7（无日志边界）场景在真实数据里的体现 —— **你选的 A（生成空轮记录）是必要的**，
否则轮号会整体错位。

### 12.5 对方案的三处修正

1. **切轮依据改为「user→assistant 消息对」**，而非仅靠 `TurnPrompt` 的最近-user 推断。
   未被任何 assistant 消费的 user 消息 → 生成**空轮记录**（对应你 Q7 的 A）。
2. **`system` 角色消息需要单独处理**：现 `logparser` 只认 `user` / `assistant`，
   `system` 被静默忽略。真实语料里有 `[181001] model call failed` 这类错误，
   应挂到该轮上作为失败证据 → **见下方待确认 Q9**。
3. **轮级耗时**用「该轮 user 的 msgTs → 该轮 assistant 的 completedAt」计算，
   与会话级 `ElapsedS` 区分开（对应你 Q4 的 A）。

### 12.6 由此新增的待确认问题

**Q9（新增，源自验证发现）**：轮内的 `system` 错误消息（如 `[181001] model call failed`）怎么处理？
- **A** 记为该轮的一条 finding（如 `TURN_MODEL_ERROR`），参与轮级展示
- **B** 只把该轮标为「未完成」，不额外生成 finding
- **C** 完全忽略 `system` 消息，维持现状
