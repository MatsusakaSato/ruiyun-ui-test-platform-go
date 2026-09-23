# AGENTS.md — Go 重写项目协作说明

> 本文件由**另一个 AI 代理**（DSH）写入，用于与正在做 Go 重写的 **gemini** 协作。
> 最后更新：2026-09-23（Python oracle 的两处问题已获授权修复/提交；六层差分仍全绿）
> 对应 Python 原版：`../ruiyun-ui-test-platform`（**只读，任何情况下都不要修改它**）

---

## 0. ✅ 主干状态（最新复查）

`go build ./...` ✅ 通过　`go test ./...` ✅ 全部通过。

> 之前 `internal/server/embed.go` 的 `//go:embed web/*` 找不到文件
> （`go:embed` 的路径相对于 .go 文件所在目录解析），
> 你已把 `web/` 放进 `internal/server/` 解决 —— 👍 **已确认恢复，保留此条仅供回溯。**

**六层差分的退出码现在全是 0**（下面 §4 的表），可以直接当 CI 门禁用了。

`testcasedb` 当前导出（供写 `pipeline` / `server` 时对照）：

```
GetDBPath / GetConnection / InitDB / CountCases / GetPresetCases /
QueryPresetCases / AddPresetCase / AddPresetCases / DeletePresetCases /
GetPresetLabelsIndex / CaseSeq
```

注意 `GetPresetCases(customPath string)` / `CountCases(customPath string)` 等
**都带 `customPath` 参数**（传 `""` 表示用默认库），返回的是 `[]map[string]any`，
不需要再 `.ToDict()`。

---

## 1. 这个项目的目标

把 `ruiyun-ui-test-platform`（Python）的**全部后端功能**用 Go 完整复现，且**行为保真**：
前端 4,120 行 JS 一行不改，所有 JSON 字段必须与 Python 版逐一对齐。

**验收口径不是"能跑"，而是"与 Python 原版语义等价"**（见 §4）。

---

## 2. ⚠️ 请先读：我改动过的文件

为降低冲突，以下文件我已修改/新增。**如果你正在改同一个文件，请先看本文件和
[`docs/P0-差分验证报告.md`](docs/P0-差分验证报告.md) 再合并。**

### 我修改的实现文件（含修复，请勿回退）

| 文件 | 改动内容 |
|---|---|
| `internal/logparser/logparser.go` | **11 处保真度修复**（见 §3.3） |
| `internal/models/models.go` | 重写 `ToolCall.Signature()`；重写 `writePyJSONString`（U+2028/`\b`/`\f` 转义，全局影响，见 §3.10 第 50 条）；**本轮 `CleanWhitespace` 改为委托 `pyre.CleanSpace`（公开名保留，调用点不用改；原手写 6 字符表与 `evaluator.normPrompt` 不一致，见 §3.11 第 62 条）** |
| `internal/formatcheck/formatcheck.go` | **本轮 6 处**：Unicode `\s`、列表排序、非 nil 空集合、`pyre.Strip`（见 §3.10） |
| `internal/safetyscan/safetyscan.go` | **本轮 8 处 + 新增有序 API**：遍历顺序、片段窗口、`_MAX_HITS`、`FlattenArguments`、否定前瞻（见 §3.10） |
| `internal/assertor/assertor.go` | **5 处**（见 §3.5）+ **本轮 2 处空白语义**：`reException` / `reRaisedException`（见 §3.11） |
| `internal/xlsx/xlsx.go` | 3 处保真度修复（见 §3.1） |
| `internal/testcasedb/testcasedb.go` | 3 处查询语义修复（见 §3.2）；**其 `GetPresetLabelsIndex` 的索引键逻辑未动，但键值语义随 `CleanWhitespace` 一起修正**（见 §3.11 第 62 条） |
| `internal/artifacts/artifacts.go` | **重写 `ExtractArtifacts` + 新增 `DisplayAbsPath`**（见 §3.6） |
| `internal/trajectory/trajectory.go` | **本轮大改**：步骤时间线、skill 结构、robust 舍入、round_objective（见 §3.7） |
| `internal/metrics/metrics.go` | **本轮大改**：排序稳定性、`skill_rows`、`sessions_closed`、`pct`（见 §3.8） |
| `internal/evaluator/evaluator.go` | **2 处产物路径**改用 `DisplayAbsPath`；**本轮 3 处空白语义**：`whitespaceRegex`（`normPrompt`）、两处代码围栏剥离（见 §3.11） |
| `internal/rubric/rubric.go` | 删除未使用的 `fmt` import（曾导致全模块编译失败） |
| `internal/pyre/pyre.go` | **【新增包】** Python `\s` / `str.strip()` 的语义等价实现（见 §3.10 第 42、47 条）；**本轮补 `CleanSpace` / `ContainsSpace`**（见 §3.11） |
| `internal/llm/llm.go` | **本轮 3 处空白语义**（`reBearer` / `reAPIKey`）+ **补上 Python 有而 Go 完全缺失的 API Key 前置校验（空值 + 含空白）**（见 §3.11 第 60、63 条）⚠️ 这是你在改的文件 |
| `internal/config/settings.go` | **本轮 1 处**：`WriteEnvProfile` 的 `reEnv` 空白语义（见 §3.11 第 61 条）⚠️ 这是你在改的文件 |

### 我新增的文件（基础设施，可放心使用）

```
cmd/tracediff/  cmd/assertdiff/  cmd/xlsxdiff/  cmd/dbdiff/
cmd/metricsdiff/  cmd/evaldiff/                          六个只读差分探针

tools/diff/trace_ref.py         + run_trace_diff.py     分析层
tools/diff/assert_ref.py        + run_assert_diff.py    断言层
tools/diff/xlsx_ref.py          + run_diff.py           xlsx 层
tools/diff/db_ref.py            + run_db_diff.py        DB 层
tools/diff/metrics_ref.py       + run_metrics_diff.py   指标层（trajectory + metrics）
tools/diff/eval_ref.py          + run_eval_diff.py      评估前置层（formatcheck + safetyscan + intent）

internal/logparser/logparser_fidelity_test.go        12 个回归测试
internal/assertor/assertor_fidelity_test.go           9 个回归测试
internal/xlsx/xlsx_fidelity_test.go                   5 个回归测试
internal/testcasedb/testcasedb_fidelity_test.go       4 个回归测试
internal/trajectory/trajectory_fidelity_test.go      10 个回归测试（本轮新增）
internal/artifacts/artifacts_fidelity_test.go         6 个回归测试
internal/pyre/pyre_fidelity_test.go                   6 个回归测试（本轮新增）
internal/models/models_pyjson_test.go                 4 个回归测试（本轮新增）
internal/formatcheck/formatcheck_fidelity_test.go     7 个回归测试（本轮新增）
internal/safetyscan/safetyscan_fidelity_test.go      10 个回归测试（本轮新增）

docs/P0-差分验证报告.md                          完整发现与证据
.gitignore
```

---

## 3. 已修复的真实缺陷（**请勿回退，全部有回归测试保护**）

累计 **70 处**（最近三轮新增 29 处）。都不是风格问题，
而是**用真实生产数据差分出来的行为偏差**。

### 3.1 `internal/xlsx/`（3 处）

1. **关键词补全分支必须互斥** —— Python 是 `if/elif` 链（只补一个目标）。顺序 `if`
   会让同时命中「课件」(ppt) 与「表格」(excel) 的句子多推一个目标。
2. **空集合必须序列化为 `[]` 而不是 `null`** —— Go `var s []string` 是 nil。
3. **禁止 `for k := range map` 决定输出顺序** —— 已用 `kindOrder` 固定。

### 3.2 `internal/testcasedb/`（3 处）

4. **多选目标是「或」语义** —— 单条 `EXISTS (... WHERE value IN (?,?))`。
   *真实库实测 或=863 条、且=7 条*。
5. **`limit` 钳制 = `max(1, min(limit or 50, 500))`** —— 负数仍是真值，`limit=-5` → **1**。
6. **附件筛选只认 `"yes"` / `"no"`** —— Python 不 trim、不小写。

### 3.3 `internal/logparser/`（11 处）

全部由 **219 份真实会话 / 1037 次工具调用** 差分得出。

7. **🔴 `session.meta.json` 键名读错（P0）** —— 实际是 snake_case
   （`session_id` / `created_at` / `updated_at`），原实现读 `id` / `createdAt` /
   `updatedAt`，取值又走 `fmt.Sprintf("%v", nil)`，于是 **219/219 个会话**的这三个字段
   全变成字面量字符串 **`"<nil>"`**。`session_id` 是 UI 主键。
   → 已加 `pyStr()`（复刻 Python `d.get(k, "")`），并补 `session_id` 回退目录名。
8. **`source_file` 语义** —— Python 存**会话目录**，原实现存 messages.json 路径。
9. **步骤序号偏移 +1** —— Python 是「先取当前 `step_index`，循环末尾再 `+1`」，
   首个步骤 index=0 且**跨消息连续累加**；**1037/1037 处不一致**，并污染 `message_spans`。
10. **`event_type` 完全没提取** —— 应取结果外壳的 `event_type`。
11. **`session_id` / `request_id` 未按 Python 规则回退**。
12. **`turn_prompt` 多轮会话取错** —— 应记录**触发它的那一轮**提问。
13. **派生耗时未取整** —— Python 是 `round(x, 2)`；**且缺 `generation_s` 的 `elif` 兜底分支**。
14. **`message_spans` 无条件记录** —— Python 只在 `span_last >= span_first` 时记录
    （17 个真实会话受影响）。
15. **`reasoning_chars` 来源错误** —— Python 累加整条消息的 `reasoningContent`。
16. **`final_answer` 跳过空串** —— Python 取最后一条 `content is not None`，**空串也算**。
17. **`empty_required_arg` nil → `null`** —— Python 返回 `[]`（1037 次调用全不一致）。

### 3.4 `internal/models/`（1 处）

18. **🔴 `Signature()` 格式与 Python 不一致** —— `core/trajectory.py:68,86` 会取签名的
    **长度**参与 `tool_arg_chars` 与 token 估算，格式不同会**直接污染指标**。
    已实现 `PyJSONDumps()` 精确复刻 `json.dumps(v, ensure_ascii=False, sort_keys=True)`。
    真实语料实测 **3626 个 int 字面量、52 个非整数 float、整数值 float 0 个**，
    故「整数值还原为整数形态」在现有语料上**完全精确**。
    ⚠️ 残留边界：出现 `8.0` 这类字面量或超出 2^53 的大整数会不一致。

### 3.5 `internal/assertor/`（5 处，**本轮新增**）

本轮做了**断言层差分**：219 会话 → **229 条 Finding**，修复前 **37 条**finding 有差异。

19. **🔴 `error_markers` 没有小写（影响 12 条 finding）** —— Python 是
    `markers = [m.lower() for m in cfg.get("error_markers", [])]`。配置里**同时存在**
    `"[ERROR]"` 与 `"[error]"`，Go 不小写就被当成两个不同特征，
    detail 里会多出一项 `[ERROR]`。
    *（`matchMarker` 内部本来就会小写，所以**匹配没错，错的是记录下来的命中列表**。）*
20. **🔴 `LOOP_TOTAL` 的 finding 顺序随机（6 条）** —— Python 的 `Counter` 是 dict 子类，
    遍历顺序 = **首次出现顺序**；Go 直接 `range` map → 顺序每次运行都不同。
21. **🔴 `DUPLICATE_CALL` 的 finding 顺序随机（10 条）** —— 同上。Python 用
    `seen.setdefault(...)` 的 dict，遍历顺序 = 首次出现顺序。
22. **`OUTPUT_TRUNCATED` detail 多一个空格** —— Python 是
    `f"...字）。" + " ".join(...)`，单个信号时 `。` 后**没有空格**；Go 写了 `"。 %s"`。
23. **`OUTPUT_TRUNCATED` 的结尾摘录没做 repr 转义（3 条）** —— Python 是
    `{body[-60:]!r}`，换行会变成**字面量 `\n`**（反斜杠 + n），Go 直接输出真实换行。
    已实现 `pyRepr()` 复刻 Python `repr(str)`：单引号优先、含 `'` 而无 `"` 时改用双引号、
    转义 `\ \n \r \t`、**非 ASCII 可打印字符原样保留**（Python 3 的 repr 不做 ASCII 转义）。

### 3.6 `internal/artifacts/`（3 处，**本轮新增**）

做**指标层差分**时暴露：产物清单在 Go 侧与 Python 侧**语义完全不同**。
`ExtractArtifacts` 已按 `core/artifacts.py:extract_artifacts` 逐行重写。

24. **🔴 `abs_path` 从不做「存在性校验」（前端会给出坏链接）** ——
    Python 的 `resolve_abs_path` 是**六级回落 + 每级都必须 `is_file()` 通过**，
    解析不到就返回 **空串**，界面据此显示「本机未找到」。
    Go 原实现直接把 `full_path` 原样返回（或拿配置根硬拼），
    **产出一个并不存在的路径**，前端「查看产物」按钮会指向空气。
    → 已新增 `DisplayAbsPath()`，逐级对齐；并新增 `okFile()` / `searchByName()`
    （按文件名有界查找，深度 ≤ 4、访问量 ≤ 4000，且要求末两级路径匹配）。
25. **🔴 `note` 文案与触发条件都不对** —— Python 只有两种：
    `"正文由同名写入调用回填"` / `"仅确认产物存在，正文不可抽取"`；
    且回填来自 `written[stem]`，**不限文件类型**，而 Go 只对 `docx` 回填、
    文案是自创的 `"正文来自前序 %s"`。
26. **🔴 `written` 登记时机错** —— Python 在**判定是否 producer 之前**就登记
    `written[stem(relative_path)] = body`（所有带正文的调用都参与回填）；
    Go 只登记 producer 的产物树节点，导致回填命中率不同。
27. **`artifacts_version` 字段整个缺失** —— Python 输出 `EXTRACT_VERSION`（=3），
    注释写明「读取方据此判断要不要重算老归档」。已补上。
28. **`_call_failed` 语义** —— Python 只有「`success is False`」或
    「有 `error` 且 `success is not True`」才算失败；Go 用的是 `tc.Failed`
    （来自 logparser 的另一套判定），会让**失败的转换调用照样进产物清单**。

### 3.7 `internal/trajectory/`（**本轮大改，8 处**）

29. **🔴 步骤时间线用了不稳定的 `sort.Slice`（影响 87 处 `i` / 61 处 `type`）** ——
    Python 是 `merged.sort(key=lambda x: x[1])`，`list.sort()` **稳定**，
    且它**先把所有 tool call 入列、再把 thinking 入列**，
    所以**下标相同时 tool 排在 thinking 前面**。
    Go 用 `sort.Slice` 让同下标顺序随机化，整条时间线随之漂移。
    → 改 `sort.SliceStable`。
30. **🔴 自动确认事件没有按时间窗混排（29 个用例时间线整体错位）** ——
    Python 的 `_merge_confirm_events` 是**基于消息时间窗**的：
    以每条 assistant 消息的 `[at, done_at)` 为段，事件 ts 落段内就插在该段步骤之后，
    早于首段置于最前、其余追加末尾，段内按 ts 升序；
    未被任何段覆盖的步骤保持原序追加。
    Go 原实现**只是把事件全部追加到末尾**。
    → 已按 `core/trajectory.py:231-318` 完整重写（含 `isoToEpochOpt` 本地时区解释、
    `cleanConfirmItems` 剥掉 `ts`/`anchor`/`attribution` 三个内部定位字段）。
31. **🔴 逐用例 `skills` 的结构完全不对** —— Python 是
    `{"name", "files", "steps"}`（**没有** `calls`/`sessions`）；
    Go 的 `SkillStat` 多输出 `calls`/`sessions`、缺 `steps`。
    → 新增 `SkillEntry` 类型。
    另：Python 还有 `(未识别技能)` 回退（只读了 `SKILL.md` 但结果无 `skill_name`），
    Go 完全没有这条分支。已补 `UnknownSkill` 常量。
32. **🔴 轮次级 `round_skills` 结构不对且顺序随机** —— Python 是
    `{"name", "sessions", "files"}`（**没有** `calls`），
    且来自 `dict.setdefault` 的插入序 + 稳定排序。→ 新增 `RoundSkill` 类型。
33. **🔴 `tools` / `round_tools` 同调用次数时顺序随机** ——
    `for _, st := range toolStats` 是 map 遍历，`sort.Slice` 又不稳定，
    而 Python 的 `sorted(..., key=lambda x: -x["calls"])` 是**稳定排序**。
    → 记录 `toolOrder`（首次出现顺序）+ `sort.SliceStable`。
34. **🔴 `round_objective` 整块缺失** —— Python 的 `build_round_detail` 会输出
    轮次级 volume / requests / status / timing 汇总，Go 根本没有这个键。
    → 已按 `core/trajectory.py:534-543` 补全。
35. **🔴 `avg_first_response_s` / `error_rate` 等百分比用了 `math.Round`** ——
    Python 的 `round()` 是**银行家舍入**。实测
    `round(31.25, 1)`：Python = **31.2**，`math.Round` = **31.3**。
    → 已实现 `PyRound(x, nd)`（`math/big.Rat` 精确 ties-to-even），
    `RoundToOneDecimal` 改为转调它。
    ⚠️ 这条推翻了 §6 陷阱 5 里「当前无差异」的旧结论 —— 真实语料**确实存在**
    恰好 `.xx5` 的比率，只是原先没跑到这一层。

### 3.8 `internal/metrics/`（**本轮大改，6 处**）

36. **🔴 `rule_rows` 顺序随机** —— Python 按 `RULES.items()` 的**声明顺序**生成后稳定排序；
    Go 是 `for rule, meta := range assertor.Rules`（map 遍历）+ `sort.Slice`（不稳定）→ 双重随机。
    → `assertor` 新增 `RuleOrder` 声明序切片，改用 `sort.SliceStable`。
37. **🔴 `tool_rows` 顺序随机** —— Python 的 `Counter.most_common()`
    并列时保持**首次出现顺序**；Go 又是 map 遍历 + 不稳定排序。→ 同法修复。
38. **🔴 `findings_rows` 顺序随机** —— Python 的 `sorted(findings, key=...)` 稳定；
    Go 用 `sort.Slice`。→ 改 `sort.SliceStable`。
39. **🔴 `skill_rows` 整块缺失** —— Python 输出一个**列表**
    （`{name, sessions:[用例号…], files, calls}`，sessions 已排序）；
    Go 根本没这个键。→ 已补，并把 `trajectory` 的 `skillName`/`skillFiles`
    导出为 `SkillName`/`SkillFiles` 复用。
40. **🔴 `sessions_closed` 算法不对** —— Python 是
    `sum(1 for o in all_obj if o["status"]["session_closed"])`；
    Go 写成了 `len(caseResults) - len(uiFailed)`。实测 **219 vs 188**。
41. **`generated_at` 不该取当前时间** —— Python 恒为 `""`（由上层写文件时决定）。→ 改为 `""`。

### 3.9 ⚠️ 已知但**故意未改**的两处

**a) `rowToCase` 多输出两个顶层键。** Go 输出比 Python 多 `scene` / `targets`。
已确认**无害**（前端只读 `c.labels.scene` / `c.labels.targets`），但属契约偏离。
要严格一致删掉那两行即可 —— 我没擅自删，因为这是设计选择而非缺陷。

**b) `EMPTY_REQUIRED_ARG` 的 `evidence` 键序（6 条 finding）。**
Python 是 `json.dumps(t.arguments, ensure_ascii=False)` —— **注意没有 `sort_keys`**，
所以它保留的是 **JSON 源文件里的键序**；Go 的 `map[string]any` **不保留键序**，
只能按有序输出。

```python
# Python（源文件键序）
{"md_file_name": "李白生平详细介绍.md", "markdown_content": ""}
# Go（键排序）
{"markdown_content": "", "md_file_name": "李白生平详细介绍.md"}
```

**为什么没修**：彻底解决要在**解码侧**保留键序（自定义 decoder 或 `json.Number` 式的
有序容器），会波及所有 `Arguments.(map[string]any)` 的类型断言与 `PyJSONDumps`，
为了 6 个**纯展示字符串**的键序去动已经验证通过的 logparser，**风险大于收益**。

**差分工装的处理**：`run_assert_diff.py` 会把两侧 evidence 归一化成
「键排序后的紧凑 JSON」再比一次。**只有归一化后完全相等**才归入这一已知类别；
否则仍算真实差异。所以这不是"放过"，而是"证明唯一差异就是键序"。


### 3.10 评估**前置层**（`formatcheck` / `safetyscan` / `intent`）（**15 处，本轮新增**）

这三层是 `evaluator` 打分的事实来源，且**全部是确定性纯函数** ——
所以在没有 stub OpenAI 的情况下就能做**完整**差分。语料 667 个用例
（219 真实会话 × 3 变体 + 10 个 Unicode 对抗用例）。修复前 **4353 处**差异，现在 **0**。

#### A. 总根因：Python 的 `\s` ≠ Go 的 `\s`（新建 `internal/pyre`）

**42. 🔴 `formatcheck` 的 `reSplit` / `rePunct` 直接用了 Go 的 `\s`。**
Python 的 `\s` 是 **29 个**码点（`str.isspace()` 集合），Go 的只有 **5 个**
（`\t \n \f \r 空格`）。差异集合里有 4 个在中文语料里**极常见**：

| 字符 | 真实语料出现次数 | 场景 |
|---|---|---|
| U+2003 EM SPACE | **64** | 排版空格 |
| U+3000 全角空格 | **21** | 中文排版（几乎每篇中文文档都有） |
| U+000B 垂直制表 | **19** | 复制粘贴带出 |
| U+2028 行分隔符 | **1** | 同上 |

后果：`_norm()` 归一会把 `"包含　页眉"` 变成 `"包含页眉"`，Go 不变 →
**要求项覆盖率静默算错**（实测 A-0000：Go `0.2` / Python `0.6`，分值 1 vs 2）。

→ 已新建包 `internal/pyre`：`SpaceClass` 常量可直接嵌进字符类，
`IsSpace()` 供逐字符路径用。**推导依据**：Python 的 `\s` 集合 ==
Go `unicode.IsSpace` 的集合 **加上** U+001C–U+001F。

**47. 🔴 `strings.TrimSpace` ≠ Python 的 `str.strip()`。**
同一个 29 字符集合问题：Go 的 `TrimSpace` 走 `unicode.IsSpace`，
**不会**去掉 U+001C–U+001F，而 Python 的 `str.strip()` 会。
实测：要求项 `"\x1c目录"` → Python 得 `"目录"`、Go 得 `"\x1c目录"`。
→ `pyre.Strip` / `TrimLeft` / `TrimRight`。

> **规则：凡 Python 原文写了 `\s` 或 `.strip()`，Go 侧一律用 `pyre.*`。**
> 已核对 Python 侧所有用 `\s` 的文件：`assertor.py:60,61`、`llm_client.py:94,242,410`、
> `format_check.py:19,20`、`testcase_db.py:415`、`evaluator.py:86,492,493,614,843`、`server.py:198`。
> **`internal/evaluator` 与 `internal/testcasedb` 里还有同类问题未修**
> （`evaluator.go:43,762,763`、`assertor.go:79,80`、`llm.go:35,36`、`config/settings.go:480`、
> `testcasedb` 的 prompt 去重键）—— 见 §7.3 待办。
> ⚠️ **JS 侧的 `\s` 不用改**（`internal/driver/scripts.go` 里的正则交给浏览器引擎，
> JS 的 `\s` 本身就是 Unicode 感知的）。

#### B. `formatcheck` 其余 5 处

**43. 🔴 `TypeConsistency` 的 `expected` / `produced` / `hit` 三个列表未排序。**
Python 是 `sorted(kinds)` / `sorted(got)` / `sorted(hit)`；
Go 直接 `for k := range kinds` —— 既与 Python 顺序不同，又**每次运行都变**（map 随机序）。
→ 三个列表都排序（回归测试连跑 20 次比对输出一致性）。

**44/45. 🔴 空集合序列化成 `null` 而不是 `[]`。**
`var hit []string` 是 nil → JSON `null`；Python 的列表推导天然给 `[]`。
影响 `RequirementCoverage` 的 `hit`/`missing` 与 `TypeConsistency` 的三个列表。
→ 真实差分里这是 11 处 formatcheck 差异的来源。

**46. `BuildContract` 的 `[]string` 分支漏了 `strip()`。**
Python 只有一条列表推导（两个来源都走 `str(t).strip().lower()`），
Go 写成了 `[]any` / `[]string` 两个分支，**后者没 strip**。

#### C. `safetyscan` 8 处

**48. 🔴 规则正则里的 `\s` 非 Unicode（同 42）。**
`rm\s+-[A-Za-z]*[rf][A-Za-z]*\s+/` 之类。
→ 在 `init()` 里用 `translatePyRegex` 统一把 `\s` / `\S` 换成 `pyre.SpaceClass` 等价式。

**49. 🔴 `FlattenArguments` 用了 `json.Marshal`（紧凑 + 转义 `< > &`）。**
Python 是 `json.dumps(arguments, ensure_ascii=False)`：分隔符 `", "` / `": "`、
**不转义** `< > &`。→ 改用 `models.PyJSONDumpsSourceOrder`。

**50. 🔴🔴 `models.writePyJSONString` 用 `json.Encoder` —— 与 Python 有 3 处不一致（全局影响）。**
这是本轮**最有价值的发现**，因为它污染的是**全项目公用的** `PyJSONDumps`：

| 字符 | Python `ensure_ascii=False` | Go `json.Encoder` + `SetEscapeHTML(false)` |
|---|---|---|
| U+2028 / U+2029 | **原样输出** | 转义成 `\u2028` / `\u2029`（Go 为 JSONP 安全如此设计） |
| `\b` (0x08) | `\b` | `\u0008` |
| `\f` (0x0c) | `\f` | `\u000c` |

**第一行会造成安全扫描漏报**（实测 B-0004）：
工具实参 `{"cmd": "rm\u2028-rf\u2028/"}` 被 Go 展开成字面量 `rm\u2028-rf\u2028/`，
于是 `rm\s+-rf\s+/` **匹配不上** → `has_redline` 误判为 `False`，
而 Python 是 `True`。**破坏性命令的工具实参命中直接丢失。**

→ 已按 Python 的 `json.encoder.py_encode_basestring` + `ESCAPE_DCT` 手写转义：
`"` `\` `\b` `\f` `\n` `\r` `\t` 用短转义，其余 < 0x20 用 `\u00xx`（小写），
**其它一律原样**。回归测试 24 例逐字符比对。

**51. 🔴 `Scan` 的遍历嵌套颠倒，且内层 range 的是随机序 map。**
Python 是「**外层来源**（有序 dict 插入序）→ 内层规则（声明序）」；
Go 写成了「外层规则 → 内层 `for srcLabel, text := range sources`」。
后果有两层：① 顺序与 Python 不同；② 内层是 **Go map**，**每次运行都不同**
（`hits` / `redlines` / `exempt` 全线不确定）。
→ 新增 `Source` 类型 + `BuildSourcesOrdered` + `ScanOrdered`，改成 source-major。
`BuildSources` / `Scan` 保留旧签名但标 Deprecated（内部按标签排序，至少确定）。

**52. `Scan` 完全没有 `_MAX_HITS = 40` 的上限。** Python 收集到 40 条就 `break`。

**53. 🔴 片段窗口用**字节**偏移当**字符**偏移。**
Python 的 `m.start()` / `m.end()` 是**码点**下标，`text[start-20 : end+20]` 按字符切；
Go 的 `FindAllStringIndex` 给的是**字节**偏移，直接 `text[padStart:padEnd]` 会把多字节汉字
**切成半个**（输出 U+FFFD 乱码），窗口位置整体错位。
**真实语料实测 901 处 snippet 不一致。**
→ 新增 `byteToRuneIndex` 建立字节→码点映射，窗口按码点切。

**54. 片段多了一次 `TrimSpace`，且「脱敏 / 截断」顺序反了。**
Python 是 `redact(text[..])` 然后 `.replace("\n", " ")[:120]` ——
**没有 trim**，且脱敏在截断**之前**；Go 是切片 → `TrimSpace` → 截断 → `Redact`。
脱敏顺序不同会导致「跨 120 字边界的密钥」两边结果不一致。

**55. 🔴 `yaml.load` 的否定前瞻用「固定 60 字节窗口」模拟 —— 语义不等价。**
Python 是 `yaml\.load\s*\((?![^)]*SafeLoader)`：`(` 之后到**第一个 `)` 之前**
若出现 SafeLoader 则整条命中作废。Go 的 regexp（RE2）**不支持否定前瞻**，
原实现改用「往后看固定 60 字节」：短调用会看到**括号外**的 SafeLoader（**误豁免**），
长调用看不到括号内的（**漏豁免**）。
→ 新增 `yamlLoadExempt()`：从 `(` 之后扫到第一个 `)`（不含）。
已实测确认 Python 行为：
`yaml.load(open('x'), Loader=yaml.SafeLoader)` **仍会命中**（前瞻止于第一个 `)`），
`yaml.load(open("x", Loader=SafeLoader))` **不命中** —— Go 侧两者都对齐。

**56. `BuildSources` 返回无序 `map[string]string`。** 顺序有语义（见 51）→ 新增有序版本；
`put()` 复刻 Python dict 的「后写覆盖前写但**保持首次插入位置**」。

#### D. 顺带补的公开 API

- `safetyscan.HasRedline(hits []*Hit) bool` —— Python 有，Go 原本只有
  `len(Redlines(hits)) > 0`。
- `models.PyJSONDumpsSourceOrder(v)` —— `json.dumps` 的 `sort_keys=False` 变体。

#### E. ⚠️ 本轮新增的一个**静默降级陷阱**（未改行为，只加文档 + 测试）

`PyJSONDumps` 只支持 `nil / bool / string / json.Number / float64 / int / int64 /
[]any / []string / map[string]any`，与 Python `json.dumps` 的可序列化类型一致。
传入**结构体**会落到 `default: fmt.Sprintf("%v", x)` —— 输出既不是 JSON 也不是
Python 的 `str()`。当前 4 个生产调用点传的都是解码后的 map，**未触发**；
但这是「以后有人传结构体就静默出错」的雷，已在 `PyJSONDumps` 的文档注释里写明。


### 3.11 `\s` / `.strip()` 遗留站点清理（**7 处，本轮新增**）

§3.10 只修了 `formatcheck` 与 `safetyscan`。本轮把**其余同类站点全部清完**，
并补上两个此前完全缺失的校验。

**57. 🔴 `evaluator.normPrompt` 用了 Go 的 `\s`（只认 5 个字符）。**
对应 `core/evaluator.py:86` 的 `_norm_prompt` = `re.sub(r"\s+", "", ...)`。
它是 prompt 去重键（`:614`/`:843`）、预设索引查询键（`:142`）、
候选去重键（`:1048`）的**唯一**归一化函数 → 已改 `pyre.SpaceClass`。

**58. `evaluator` 代码围栏剥离**（`evaluator.py:492,493`）的两处 `\s*`。

**59. `assertor` 的 `reException` / `reRaisedException`**（`assertor.py:60,61`）。

**60. `llm.Redact` 的 `reBearer` / `reAPIKey`**（`llm_client.py:94,96,97`）
—— `[^"\s,}]` 里的 `\s` 也要换（否则密钥里的 Unicode 空白会被当成密钥字符）。

**61. `config.WriteEnvProfile` 的 `reEnv`**（`server.py:198`）。

**62. 🔴🔴 `models.CleanWhitespace` 手写 6 字符表，且与 `evaluator.normPrompt` 互相不一致。**
这是本轮最有价值的发现，因为它让**同一个索引的「建」与「查」用了两套语义**：

| 归一化函数 | 去掉几个字符 | 用在哪 |
|---|---|---|
| `models.CleanWhitespace`（手写表） | **6**（空格 `\t \n \r` U+3000 U+00A0） | `testcasedb.GetPresetLabelsIndex` **建键** |
| `evaluator.normPrompt`（Go `\s`） | **5** | `evaluator` 查预设 **查键** |
| Python `re.sub(r"\s+","")` | **29** | — |

后果：`testcasedb.go:702` 用 `CleanWhitespace` 建索引、`evaluator.go:142` 用 `normPrompt` 查索引
—— 只要 prompt 里含 U+3000/U+2003/U+000B 等字符，**两边键不同，预设标签静默查不到**。
手写表还漏掉真实语料里出现过的 U+000B(×19)、U+2003(×64)、U+2028(×1)。

→ `models.CleanWhitespace` 已改为委托 `pyre.CleanSpace`（**保留了公开名，
gemini 的调用点无需改动**）。全项目现在只有一套空白语义。

**63. 🔴 `llm.Chat` 完全缺失 API Key 前置校验；`ProbeProvider` 缺空白校验。**
Python `core/llm_client.py:406-414`（Chat）与 `:242`（ProbeProvider）都有：

```python
key = (api_key or "").strip()
if not key:                     return "未配置 API Key"
if re.search(r"\s", key):      return "API Key 含空格或换行，请检查是否粘贴了多余内容"
```

Go 的 `Chat` **两个都没有**（连空值都没判），`ProbeProvider` 只有空值判断。
后果：把「模型名」一起粘进 Key 时（最常见的坑）会白跑一次网络请求，
换回一句与真实原因无关的 401。→ 已按 Python 原文逐字补上（含报错文案与字符数统计），
请求头也改用归一化后的 key。

#### 新增 API

| API | 说明 |
|---|---|
| `pyre.CleanSpace(s) string` | 复刻 `re.sub(r"\s+", "", s)`（删除全部 Python 空白） |
| `pyre.ContainsSpace(s) bool` | 复刻 `re.search(r"\s", s)` |

> 自检：`grep -rn '\\s' --include='*.go' internal/ cmd/ | grep -v _test.go`
> 现在**应无输出**（JS 字符串里的正则在 `internal/driver/scripts.go`，
> 交给浏览器引擎执行，JS 的 `\s` 本身是 Unicode 感知的，不用改）。

### 3.12 评估**客观评分层**的纯函数（`_obj_*` / `_coerce_score` / `_workspace_root`）（**7 处，本轮新增**）

`core/evaluator.py` 里有一批**不依赖 LLM 的确定性纯函数**，它们是最终得分的一部分
（`_objective_scores` 的 6 个客观项、判分档位校验、产物解析根）。
本轮把它们逐个对照 Python 实测，**发现 7 处偏差并全部修复**，
新增 18 个回归测试（`internal/evaluator/evaluator_obj_fidelity_test.go`）。

> 这批函数此前**从未被任何差分层覆盖** —— 六层里最接近的是"评估前置层"，
> 但它只到 `formatcheck` / `safetyscan` / `intent`，没进 `evaluator` 的评分逻辑。

**64. 🔴 `objDeliveryEfficiency` 的 `minutes` 用了 `math.Round(mins*10)/10`。**
Python 是 `round(mins, 1)`。这里其实是**两重**错误：

1. 不是 ties-to-even（同 §3.7 第 35 条的银行家舍入问题）；
2. `*10` 引入**二次舍入** —— 连**非精确 tie** 都会错。

实测（`elapsedS` → `minutes`）：

| elapsedS | mins 真实值 | Python | Go 原先 |
|---|---|---|---|
| 1875 | 31.25（二进制精确 .5） | **31.2** | 31.3 |
| 15 | 0.25 | **0.2** | 0.3 |
| 21 | 0.35 ≈ 0.34999…（**不是** tie） | **0.3** | 0.4 |
| 9 | 0.15 ≈ 0.14999… | **0.1** | 0.2 |
| 747 | 12.45 ≈ 12.44999… | **12.4** | 12.5 |

→ 改用 `trajectory.PyRound(mins, 1)`（已对齐 Python 的实现），并加了
`0.25` 步长扫到 3000 秒的交叉校验测试。

**65. 🔴 `objToolSelection` 用 `strings.TrimSpace` 代替 Python 的 `.strip()`。**
同 §3.10 第 47 条的老问题：Go 的 `TrimSpace` 少 U+001C–U+001F。
实测 `expect_tools=["\x1cread_file\x1d"]`：Python 归一化成 `"read_file"` → 命中率 **1.0**；
Go 保留原样 → `expected` 对不上 → 命中率 **0**。
→ 改用 `pyre.Strip`。

**66. 🔴 `objToolSelection` 用 `s != "<nil>"` 把 nil 过滤掉了，Python 不会。**
Python 是 `[str(t).strip() for t in ... if str(t).strip()]`，
而 **`str(None) == "None"`（非空）** → Python **保留**它。
Go 因为 `fmt.Sprintf("%v", nil)` 产出 `"<nil>"`，就加了个 `!= "<nil>"` 的补丁把它丢掉。
实测 `expect_tools=[None, "read_file"]`：Python `expected=["None","read_file"]` → 命中率 **0.5**；
Go → `["read_file"]` → 命中率 **1.0**。
→ 新增 `pyStrValue()` 复刻 Python `str()`，并**去掉那个补丁**：

| 输入 | Python `str()` | Go `%v` | Go `pyStrValue` |
|---|---|---|---|
| `nil` | `"None"` | `"<nil>"` | `"None"` ✅ |
| `true` | `"True"` | `"true"` | `"True"` ✅ |
| `3.0` | `"3.0"` | `"3"` | `"3"`（见下方边界） |

**67. `objToolSelection` 的 `hit` 空集合序列化成 `null`。**
`var hit []string` 是 nil。Python 列表推导天然给 `[]`。→ 改 `hit := []string{}`。

**68. 🔴🔴 `coerceScore` 用 `fmt.Sscanf(v, "%f")` —— 这是**前缀**解析。**
Python 是 `v = float(raw)`，要求**整个**字符串合法，否则 `ValueError` → `return None`
（记为**未评**）。Go 的 `Sscanf` 只解析前缀、忽略尾部：

| 输入 | Python | Go 原先 |
|---|---|---|
| `"3"` / `"3.0"` | 3 | 3 ✅ |
| `"3abc"` | **None（未评）** | **3** ❌ |
| `"3,5"` | **None（未评）** | **3** ❌ |
| `"3分"` | **None（未评）** | **3** ❌ |
| `"3.0（满分5）"` | **None（未评）** | **3** ❌ |

**这条最危险**：LLM 判分最常见的输出形态就是 `"3分"` / `"3.0（满分5）"` 这类
带单位或说明的字符串。Go 会把**本该判为"模型未给出合法分值"的维度静默算成 3 分**，
拉高总分且无任何提示。→ 改用 `strconv.ParseFloat(pyre.Strip(v), 64)`（整串必须合法）。

**69. 🔴 `coerceScore` 缺 `bool` 分支。**
Python `float(True) == 1.0`，落进 `(1,2,3,4,5)` → 返回 **1**；
`float(False) == 0.0` → 在 `1-5` 下是 None，但在 `5-0` 档下返回 **0**。
Go 的 switch 没有 `bool`，直接落到 `default: return nil`。→ 已补。

**70. `workspaceRoot` 的 `strings.TrimSpace` → `pyre.Strip`**（一致性，影响极低）。
Python 是 `str(paths.get("workspace_root") or "").strip()`。
路径里不会出现 U+001C，但既然全项目已统一空白语义，这里也一并换掉。

#### ⚠️ 顺带确认**正确**、并已加测试锁住的三处

- `_obj_self_correction` —— bad 集合是 `TOOL_CALL_FAILED ∪ TOOL_RESULT_MISSING`；
  「同名、成功、参数不同」才算一次自纠正，且 `break` 在第一个同名成功调用处。
  Go 与之逐行一致（含 `break` 位置）。已加 5 个测试。
- `_scope_note` —— 三档文案 + 两种后缀，Go 逐字一致。
- `_workspace_root` 的回落链（`workspace_root` → `session_root` 父目录 → 空串）一致。
  *（我第一版测试用错了配置键 `{"workspace":...}`，是**测试**错、代码对 —— 已改。）*

#### ⚠️ 记录在案的一个残留边界（**不可达，未修**）

Go 的 `encoding/json` 把 JSON 的 `3` 与 `3.0` **都解成 `float64(3)`**，
而 Python 分别是 `int 3` 与 `float 3.0`。所以：

| JSON 字面量 | Python `exp` 元素 | Go `pyStrValue` |
|---|---|---|
| `3` | `"3"` | `"3"` ✅（靠 `PyJSONDumps` 的整数形态补偿） |
| `3.0` | `"3.0"` | `"3"` ❌ 边界在此 |

现实语料里 `expect_tools` **全是工具名字符串**，此路径不可达；已在测试里显式记录，
以免以后有人误以为"已经完全对齐"。这与 §3.4 第 18 条的残留边界同源。

---

## 4. 差分工装 —— 请把它当作验收门禁

**这是本项目的"生命线"。** Python 原版是唯一权威（oracle），改动后请务必跑差分。

```bash
cd ruiyun-ui-test-platform-go
export GOCACHE=/Users/amano/WorkSpace/.gocache     # 沙箱下需指向可写目录

python3 tools/diff/run_trace_diff.py      # 分析层：219 会话 / 1037 调用
python3 tools/diff/run_assert_diff.py     # 断言层：219 会话 → 229 条 Finding
python3 tools/diff/run_diff.py "/Users/amano/WorkSpace/工作台测试集1000.xlsx"   # xlsx：1000 行
python3 tools/diff/run_db_diff.py         # DB：1019 条（自动复制，不写原库）
python3 tools/diff/run_metrics_diff.py    # 指标层：219 用例 × (trajectory + metrics)
python3 tools/diff/run_eval_diff.py       # 评估前置层：667 用例 × (formatcheck + safetyscan + intent)
                                          #   加 --dump DIR 可落盘两侧原始输出做事后分析
                                          #   加 --show N 控制每层打印多少条样本

# 退出码 0 = 等价，1 = 有真实差异 —— 可直接接 CI
```

**差分器的三个关键设计**（你后续加层时请照做）：

1. **不要比哈希。** Go 与 Python 的浮点序列化不同（Go 输出 `100`、Python `100.0`），
   跨语言哈希必然不等。`run_metrics_diff.py` 把两侧结构都拉回来做**递归结构化比较**，
   数值一律按数值比（bool 除外），并按 JSON 路径报差异。
2. **键序类差异要用「交叉校验」证明，而不是"放过"。**
   `args_summary` / `evidence` 都是 `json.dumps(arguments)` 的**截断**展示，
   截断后无法直接归一化。所以两侧探针**额外输出一份 `args_canon`**
   （每步参数的**未截断、键已排序**规范形）。只有这份完全相等，
   才把 `args_summary` 的差异判为"纯键序"。**这是证明，不是豁免。**
3. **语料 = 真实数据 + 对抗注入。** 真实语料保证覆盖面，
   对抗注入保证**已知陷阱一定会被踩到**。`run_eval_diff.py` 除 219 个真实会话外，
   还给每个会话生成 2 个变体（要求项/安全源文本里塞 Unicode 空白），
   外加 10 个逐字符的纯对抗用例 —— **10 种 Python `\s` 独有字符各一个**。
   真实语料里 U+2003 只出现 64 次、U+2028 只有 1 次，
   靠真实数据覆盖是碰运气；对抗注入把它变成必然。

**当前已验证状态**

| 层 | 语料 | 状态 |
|---|---|---|
| xlsx（`internal/xlsx`） | 1000 行真实用例表 | ✅ 语义不一致 0 / 1000 |
| DB（`internal/testcasedb`） | 1019 条真实库 | ✅ 20 / 20 查询电池一致 |
| 分析层（`internal/logparser`） | 219 会话 / 1037 调用 | ✅ **25 个顶层 + 17 个调用级字段全部一致** |
| 断言层（`internal/assertor`） | 同上，**229 条 Finding** | ✅ **9 类规则计数逐类相同，零真实差异** |
| 指标层（`internal/trajectory` / `metrics`） | 219 用例全链路（parse→assert→CaseDetail→RoundDetail→Metrics） | ✅ **逐用例 detail 219/219 一致；round_detail 一致；metrics 零真实差异** |
| **评估前置层**（`internal/formatcheck` / `safetyscan` / `intent`） | 219 会话 × 3 变体 + 10 对抗 = **667 用例** | ✅ **formatcheck 0 差异；intent 0 差异；safetyscan 仅剩 282 处「JSON 对象键序」（归一化后 0 真实差异）** |
| 评估层的**客观纯函数**（`_obj_*` / `_coerce_score` / `_workspace_root` / `_scope_note`） | 无需 LLM，已逐个对照实测 | ✅ **7 处偏差已修（§3.12），18 个回归测试** |
| 评估层的 **judge / rubric 主流程**（`_judge_case` / `evaluate_round`） | 需 stub OpenAI | 🔶 产物路径已对齐（2 处）；安全扫描已改用**有序 API**；judge 主流程尚未差分 |
| 驱动层（`internal/driver` = ui_driver.py 1849 行） | — | 🔄 **你正在做**（最高风险） |
| 服务层（`internal/pipeline` / `server` / `report`） | — | 🔄 你正在做（当前可编译） |

> ✅ = 我已完成差分并零真实差异；🔶 = 部分对齐；🔄 = 你负责中；⬜ = 未开始。

---

## 5. ⚠️ 必须知道的前提：Python oracle 自身是不确定的

`core/xlsx_reader.py` 的 `infer_targets` 遍历 **set**，Python 字符串哈希默认随机化
（`PYTHONHASHSEED`）——**同一份代码、同一输入，多次运行会产生不同顺序的 `targets`**。
实测 12 次运行出现 4 种不同结果。

**含义**：
- 「逐字节对齐 Python」这个目标**在原理上不成立**；
- 严格 diff 会报出**误导性的"幽灵差异"**，不要为此去改 Go 代码；
- Go 侧已确定化（这比 Python 更好），差分工装默认对「顺序」归一化。

**待决策**：是否同步修复 Python 原版的 `infer_targets`（约 1 行）。
**在得到用户明确授权前，不要修改 `../ruiyun-ui-test-platform` 的任何文件。**

**好消息**：分析层与断言层已验证**无此问题**（229 条 Finding 的规则、
顺序、计数完全确定）。做后续层差分前仍请先确认顺序稳定性。

### 5.1 ✅ 已修复：oracle 的 `issues` 恒为空（原为确定性缺陷）

`core/trajectory.py:401`：

```python
"issues": sorted({f.rule for f in findings_by_step.get(tc.index, [])}),
```

但**唯一的调用方**（同文件 `:508`）传进来的字典 key 是
**`(session_id, step_index)` 元组**（`:506` `by_step.setdefault((c.session_id, f.step_index), ...)`），
用 `tc.index`（一个 int）去查**永远查不中** → **Python 产出的 `issues` 恒为 `[]`**。

**这几乎肯定是笔误**：同文件 `:141` 的 `_step_status` 不仅用的是正确形态
`findings_by_step.get((session_id, tc.index))`，还在 docstring 里**专门写了**
「findings_by_step 的 key 是 (session_id, step_index) 元组 —— 必须用同样形态查」。

**影响**：详情页每个工具步骤的「问题」徽标从来没显示过。

**处理**：Go 侧一直按正确语义实现（用 `sessionID:stepIndex` 复合键），
即 Go 的输出本就等于 Python **修好之后**的样子。差分器把这类差异单独标记为
`oracle缺陷`（判据：Python 为 `[]`、Go 为已排序的非空列表），不计入真实差异，
但也不掩盖 —— 每次运行都会打印计数。

**✅ 2026-09-23 已获用户授权并修复 Python 侧**（提交 `12b7ba3`，1 行）：

```python
- "issues": sorted({f.rule for f in findings_by_step.get(tc.index, [])}),
+ "issues": sorted({f.rule for f in findings_by_step.get((trace.session_id, tc.index), [])}),
```

修复后**六层差分全部仍为退出码 0**，且指标层的 `oracle缺陷` 计数
**由 159 降为 0** —— 说明两侧现在从"语义等价靠豁免"变成了"逐字段真等价"。

#### ⚠️ 顺带查出的一个 Go 侧潜在差异（**已证不可达，故意未改**）

Python 是**集合推导** `{f.rule for f in ...}`（**会去重**），
而 Go 是 `for _, f := range findingsByStep[stepKey] { issueRules = append(...) }`
（**不去重**），且 `nonNilSlices` 也只做 nil→`[]`。

修好 Python 后这本应立刻暴露成差异，但实测**没有** —— 因为
**每条规则在每个 (session, step) 上最多只产生一条 finding**：

| 规则 | 为何不可能重复 |
|---|---|
| `LOOP_TOTAL` | `_mk(...)` **不带 `step=`** → `step_index` 为 None，根本不进 `issues` |
| `DUPLICATE_CALL` | `step=items[1].index`，不同签名组的 `items[1]` 必为不同对象 → index 唯一 |
| `LOOP_CONSECUTIVE` | `run == th` 每次连续段只成立一次，且 index 唯一 |
| 其余规则 | 都是 `for t in tool_calls` 里每次调用 ≤1 条 |

实测 219 会话 / **229 条 finding**，按 `(session_id, step_index)` 分组后
**重复 rule 的组数为 0**。故 Go 少一次去重**当前完全不可达**，
**没有改 Go**（避免为一个不可达路径去动已验证的 `trajectory`）。
若将来新增的规则可能对同一步产生多条，**届时应给 Go 补去重**。

### 5.2 ✅ 已提交：`drivers/ui_driver.py` 的 `clickEl` 修复

原先 `../ruiyun-ui-test-platform` 的 `drivers/ui_driver.py` 有**未提交**的工作区改动
（+9/−2）。**✅ 2026-09-23 已获用户授权并提交**（提交 `e0bd234`，内容原样未改）。

> 新增 `clickEl(el)`：点击前先判 `el.disabled` / `aria-disabled === 'true'`，
> 禁用按钮（如「提交中」）不再被点，避免白吃一次尝试次数而误触发人工介入；
> 并把 `clicked` 字段带进 `keyword` / `first-option` 两种返回。

**判断**：这是对 oracle 的**真实 bug 修复**（不是误改）。
我确认 `internal/driver/scripts.go:97-133` **已经逐字同步**了这个改动 —— 👍 做得好，
oracle 与端口保持一致。

**当时为什么要紧**：该改动未 commit 时，一次 `git checkout .` / `git stash`
就会让 oracle **静默回退**，而 Go 侧仍带着修复 → 差分会出现**看起来莫名其妙**的差异，
真实运行行为也会退化。**现已 commit，这个隐患解除。**

> 提交历史：`e0bd234`（本改动，作者记为 gemini）、`12b7ba3`（§5.1 的 oracle 修复）。

---

## 6. 移植保真度陷阱（做后续层时逐条对照）

1. **rune vs byte** —— Python `len(str)` 数码点，Go `len(string)` 数字节。
   → `utf8.RuneCountInString`。
2. **`json.dumps` 的三种形态**（本轮被坑了两次，务必分清）：
   | 调用 | 分隔符 | 键序 | 用在哪 |
   |---|---|---|---|
   | `json.dumps(v, ensure_ascii=False, sort_keys=True)` | `", "` / `": "` | **排序** | `signature()` |
   | `json.dumps(v, ensure_ascii=False)` | `", "` / `": "` | **源文件序** | `check_empty_args` 的 evidence |
   | `json.Marshal`（Go 原生） | `","` / `":"` | 排序 | ❌ **任何地方都不要用** |

   共同点：都**不转义** `< > &`，中文都**不转义**为 `\uXXXX`。

   ⚠️ **还有一个更隐蔽的共同点（本轮才发现）：U+2028 / U+2029 不转义，
   `\b`/`\f` 用短转义。**
   Go 的 `json.Encoder` 即使 `SetEscapeHTML(false)` 也会把 U+2028/U+2029 转义
   （为 JSONP 安全），并把 `\b`/`\f` 写成 `\u0008`/`\u000c`。
   实测后果：`{"cmd": "rm\u2028-rf\u2028/"}` 展开后 `rm\s+-rf\s+/` **匹配不上**，
   **破坏性命令的工具实参命中直接丢失**（`has_redline` 误判 False）。
   → `internal/models` 已手写 `writePyJSONString`，**禁止改回 `json.Encoder`**。
3. **Python `repr()`** —— 换行变字面量 `\n`、反斜杠变 `\\`、非 ASCII 可打印字符原样保留。
   凡 Python 侧写了 `{x!r}`，Go 侧必须用 `pyRepr()`（见 `internal/assertor`）。
4. **`ast.literal_eval`** —— 真实日志里工具结果是 **Python repr**（单引号、`None`、
   `True`），不是 JSON。已在 `internal/logparser/pyliteral.go` 实现。
5. **🔴 银行家舍入（本轮已被真实数据打脸，务必重视）** —— Python `round()` 是
   **half-to-even**，Go 的 `math.Round` 是 **half-away-from-zero**。
   实测 `round(31.25, 1)`：Python **31.2** / `math.Round` **31.3**。
   已实现 `trajectory.PyRound(x, nd)`（基于 `math/big.Rat`，对浮点数的
   **二进制精确值**做 ties-to-even），`RoundToOneDecimal` 与 `metrics.pct` 均已转调它。
   **凡 Python 侧写 `round(...)` 的地方，一律用 `PyRound`，不要用 `math.Round`。**
6. **naive datetime 时区** —— 另注意 **Python 3.9 的 `fromisoformat` 不接受 `Z` 后缀**
   （会抛异常 → -1），而 Go 的 RFC3339 接受 —— 当前语料无 `Z`，未暴露。
7. **`null` vs 零值** —— 前端靠 `?? 0`、`=== false`、`!= null` 判断。
8. **JSON 键顺序** —— 事实载荷与归档依赖有序字典，Go map 是随机序。
9. **中文文案耦合逻辑** —— `SEND_KEYWORDS`、`DENY_WORDS`、`提交中`、`新建任务`、
   "分钟/小时/天/刚刚/昨天/前天"：**逐字保留，禁止"顺手清理"**。
10. **🔴 Python `\s` 是 Unicode 感知的（29 个码点），Go `regexp` 的 `\s` 只认 5 个。**
    这条本轮**真的炸了**（§3.10 第 42/48 条），不是理论风险。
    **凡 Python 原文写了 `\s`，Go 一律用 `pyre.SpaceClass`；
    凡写了 `.strip()`，一律用 `pyre.Strip`**（Go 的 `TrimSpace` 少 U+001C–U+001F）。
    自检命令：
    ```bash
    grep -rn '\\s' --include='*.go' internal/ | grep -v '_test.go'
    ```
    出现 `\s` 的地方逐个确认是不是 Python 语义（**JS 字符串里的不用改**）。
11. **`fmt.Sprintf("%v", nil)` 会产出字面量 `"<nil>"`** —— §3.3 第 7 条就是这么炸的。
    凡从 `map[string]any` 取值，一律用 `pyStr()`。
12. **Python 的 `.strip()` / `.lstrip()` / `.rstrip()` ≠ Go 的 `strings.TrimSpace`。**
    差的就是 U+001C–U+001F 这 4 个 ASCII 分隔符。见 §3.10 第 47 条。
13. **`PyJSONDumps` 不支持结构体** —— 传结构体会静默退化成 `fmt.Sprintf("%v")`。
    只传从 JSON 解出来的 map / 切片 / 标量。见 §3.10 E。

---

## 7. 给 gemini 的提醒 / 待办

### 7.1 当前状态

- **`go build ./...` ✅**、**`go test ./...` ✅**、**`go vet ./...` ✅** 全绿
- **六层差分全部 ✅ 退出码 0**：xlsx / DB / 分析层 / 断言层 / 指标层 / **评估前置层**
- 累计修复 **70 处**真实保真度缺陷；回归测试 **11 个文件 151 个用例**
- 你新增了 `internal/repro`、`internal/driver`、`internal/sysutil`、`internal/pipeline`、
  `internal/llm`、`internal/server` 👍

> 小提醒：`go build ./...` 一旦挂掉会连带让 `go test ./...` 整体非零退出。
> 长时间重构时建议保持主干可编译，或先 `t.Skip` 掉未完成的包。

### 7.2 ⚠️ 本轮我改了 `trajectory` 与 `metrics` 的**对外字段形状**，请同步

如果你正在写 `server` / `pipeline` / `report` 并引用了这些，请注意：

| 变更 | 旧 | 新 |
|---|---|---|
| `skills[]` 元素 | `SkillStat{name,files,sessions,calls}` | `SkillEntry{name,files,steps}` |
| `round_skills[]` 元素 | `SkillStat` | `RoundSkill{name,sessions,files}` |
| `BuildCaseDetail` 返回值 | 无 `artifacts_version` | 多 `artifacts_version` |
| `BuildRoundDetail` 返回值 | 无 `round_objective` | 多 `round_objective` |
| `BuildMetrics` 返回值 | 无 `skill_rows` | 多 `skill_rows` |
| `trajectory.skillName/skillFiles` | 未导出 | 已导出为 `SkillName`/`SkillFiles` |
| `artifacts.ResolveAbsPath` | 直接拼路径 | **已标 Deprecated**；改用 `DisplayAbsPath` 做存在性校验 |

**`internal/evaluator/evaluator.go` 的两处产物路径我已改完**（本轮）：

Python 的 `core/evaluator.py:427,1007` 用的是 **`resolve_abs_path`（带回退 + 存在性校验）**，
Go 原先调的是只做字符串拼接的 `ResolveAbsPath` —— 会给出**指向不存在文件**的路径。
两处（`"绝对路径"` 与 `"abs_path"`）均已改为 `artifacts.DisplayAbsPath(a, wsRoot)`。

> ⚠️ 我动了你的 `internal/evaluator/evaluator.go`（只改这 2 行 + 1 条注释）。
> 如果你正在同时改这个文件，请以 `DisplayAbsPath` 为准合并。
> 目前 `ResolveAbsPath` 已无生产调用方，**新代码请不要再用它**。

### 7.3 建议的下一步差分顺序

0. ~~先清掉 `pyre` 的遗留同类问题~~ —— **✅ 本轮已全部清完**（见 §3.11）。
1. **`internal/evaluator`** —— 需 stub OpenAI server，成本最高。
   - ~~不依赖 LLM 的纯函数部分~~ —— **✅ 本轮已做完**（§3.12）：
     `_obj_tool_selection` / `_obj_self_correction` / `_obj_delivery_efficiency` /
     `_coerce_score` / `_workspace_root` / `_scope_note` 已逐个对照 Python 实测，
     修掉 7 处偏差并加了 18 个回归测试。**这些函数现在可以当已验收黑盒。**
   - 产物路径那两处我已对齐（见 §7.2）；**安全扫描已改用有序 API**
     （`BuildSourcesOrdered` + `ScanOrdered`，见 §3.10 第 51/56 条，请勿用回 map 版本）。
   - **剩下的是 `_judge_case` / `evaluate_round` 主流程**（需要 stub OpenAI server）。
   - `formatcheck` / `safetyscan` / `intent` 三层**已经差分完毕**，可以直接当已验收的黑盒。
2. **`internal/driver`** —— 最难差分（依赖 Electron + CDP 调试端口），
   建议先做**纯函数部分**（`internal/driver/scripts.go` 里的 JS 注入脚本、
   URL/端口解析、重试与退避策略），把 CDP 交互留到最后。
3. **`internal/pipeline` / `server` / `report`** —— 端到端 HTTP 层，
   可用真实轮次归档做 golden。

### 7.4 工程建议

- ~~本项目没有 git 仓库~~ —— **已更正：仓库已存在，且基线之后的工作也已固化**。

  | 提交 | 内容 |
  |---|---|
  | `70fb621` | 基线（xlsx / testcasedb / assertor / evaluator / repro / report） |
  | 最新一次提交（`git log -1`） | **六层差分验证成果**：63 处保真度修复 + 133 个回归测试 + 全部差分工装 |

  > 这里刻意不写最新提交的哈希 —— 每次 `git commit --amend` 都会让它变，
  > 写进文档就会立刻过期。用 `git log -1` 查。

  ⚠️ 最新提交只是**当前已验证状态的快照**，其中
  `internal/{driver,pipeline,server}` 是**尚未完成的实现**（不代表已完工）。
  **往回退到基线 `70fb621` 会丢掉全部 63 处修复与 133 个测试，不要这么做。**
  后续建议按语义拆小步提交（提交信息用中文 Conventional Commits）。
- `.gitignore` 原先只列了 `/xlsxdiff` 与 `/dbdiff` 两个探针二进制，
  `/tracediff`、`/assertdiff`、`/metricsdiff`、`/evaldiff` 四个漏了
  —— 曾出现根目录残留 `evaldiff` 编译产物的风险。**已补全**。
- ~~`internal/xlsx/xlsx_test.go:11` 硬编码了绝对路径，换机器即静默 skip~~
  —— **✅ 已修**：改为按优先级回落
  `RUIYUN_XLSX_TEST` 环境变量 → `testdata/` → 上级目录 → `$HOME/WorkSpace/` → 当前开发机绝对路径；
  全都不存在时 skip 提示会列出**所有已尝试路径与配置方法**。
  （这条危险在于「最强的 oracle 静默失效」本身不报错 —— 现在至少喊得出来。）
- `rowToCase` 里 `rows.Scan` 失败会**静默跳过该行**（`if err == nil` 才 append）。
  某列出现 NULL 时 Python 返回 `""` 而 Go 会丢行 —— 当前真实库无 NULL 未暴露。
- 部分文件未 `gofmt`（当前是 `internal/driver/driver.go`、`internal/pipeline/pipeline.go`，
  都是你在改的）。我未擅自格式化以免冲突 —— 建议你收尾时跑一次
  `gofmt -w ./internal ./cmd`。

---

## 8. 约定

- **提交信息必须用中文**，遵循 Conventional Commits：`<type>: <中文描述>`
  （来自 `../ruiyun-ui-test-platform/.agents/rules/git.md`）。
- **未经用户明确授权，绝对不要修改 `../ruiyun-ui-test-platform`**（Python oracle）
  —— 它是差分验证的基准。
  **授权例外（2026-09-23，用户已明确批准两处，均已提交）**：
  `e0bd234` 提交了 `drivers/ui_driver.py` 的 `clickEl` 修复；
  `12b7ba3` 修复了 `core/trajectory.py:401` 的 `issues` 键形态笔误（见 §5.1）。
  除此之外**仍一律只读**；任何新的 oracle 改动都必须先拿到明确授权。
- 改动实现后，请跑 §4 的差分作为门禁；新增行为请补回归测试。
