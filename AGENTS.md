# AGENTS.md — Go 重写项目协作说明

> 本文件由**另一个 AI 代理**（DSH）写入，用于与正在做 Go 重写的 **gemini** 协作。
> 最后更新：2026-09-23 17:52
> 对应 Python 原版：`../ruiyun-ui-test-platform`（**只读，任何情况下都不要修改它**）

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
| `internal/xlsx/xlsx.go` | 3 处保真度修复（见 §3.1） |
| `internal/testcasedb/testcasedb.go` | 3 处查询语义修复（见 §3.2） |
| `internal/artifacts/artifacts.go` | 删除未使用的 `encoding/json` import（曾导致 `go build ./...` 失败） |
| `internal/trajectory/trajectory.go` | 补 `strings` import（曾导致全模块编译失败） |
| `internal/rubric/rubric.go` | 删除未使用的 `fmt` import（曾导致全模块编译失败） |

### 我新增的文件（基础设施，可放心使用）

```
cmd/xlsxdiff/                        xlsx 层差分探针（Go 侧）
cmd/dbdiff/                          DB 层差分探针（Go 侧，只读）
tools/diff/xlsx_ref.py               xlsx 层 Python 参照（import 原版实现）
tools/diff/db_ref.py                 DB 层 Python 参照（import 原版实现）
tools/diff/run_diff.py               xlsx 层差分运行器（CI 门禁）
tools/diff/run_db_diff.py            DB 层差分运行器（CI 门禁，自动复制库）
internal/xlsx/xlsx_fidelity_test.go        5 个回归测试
internal/testcasedb/testcasedb_fidelity_test.go  4 个回归测试
docs/P0-差分验证报告.md               完整发现与证据
.gitignore
```

---

## 3. 已修复的 6 个真实缺陷（**请勿回退，已有回归测试保护**）

这些不是风格问题，是**用真实生产数据差分出来的行为偏差**。改动都写了注释说明原因。

### 3.1 `internal/xlsx/`

1. **关键词补全分支必须互斥** —— Python 是 `if/elif` 链（只补一个目标）。写成顺序 `if`
   会导致同一句同时命中「课件」(ppt) 与「表格」(excel) 时**多推一个目标**，进而改变
   期望交付物 → 影响下游断言与评分。
2. **空集合必须序列化为 `[]` 而不是 `null`** —— Go 的 `var s []string` 是 nil，
   `encoding/json` 会输出 `null`；Python 输出 `[]`。前端做 `?? []` 防御式读取，
   二者在 JS 中**不等价**。涉及 `items` / `attachments` / `expectTools` / `targets`。
3. **禁止用 `for k := range map` 决定输出顺序** —— Go 的 map 迭代顺序是**随机**的。
   已用 `kindOrder` 固定语义推断的遍历顺序，保证同一输入可复现。

### 3.2 `internal/testcasedb/`

4. **多选目标是「或」语义** —— 必须是单条 `EXISTS (... WHERE value IN (?,?))`。
   若给每个目标生成一条 `EXISTS` 再用 `AND` 串起来就变成「且」。
   *真实库实测：或=863 条，且=7 条* —— 这是用户可感知的功能错误。
5. **`limit` 钳制必须是 `max(1, min(limit or 50, 500))`** —— 注意 Python 里
   **负数仍是真值**，所以 `limit=-5` 应钳到 **1** 而不是回落 50；且必须有 **500 上限**。
6. **附件筛选只认字面量 `"yes"` / `"no"`** —— Python 不 trim、不小写；
   不要额外接受 `"1"/"true"/"0"/"false"`。

### 3.3 ⚠️ 已知但**故意未改**的一处

`rowToCase` 比 Python 多输出两个顶层键 `scene` / `targets`。
已确认**无害**（前端只读 `c.labels.scene` / `c.labels.targets`，Go 内部也无消费者），
但它是与 oracle 的契约偏离。若你要追求严格字节一致，删掉那两行即可 —— 我没擅自删，
因为这是设计选择而非缺陷。

---

## 4. 差分工装 —— 请把它当作验收门禁

**这是本项目的"生命线"。** Python 原版是唯一权威（oracle），改动后请务必跑差分。

```bash
cd ruiyun-ui-test-platform-go
export GOCACHE=/Users/amano/WorkSpace/.gocache     # 沙箱下需要指向可写目录

# xlsx 层：真实 38MB 用例表，1000 条
python3 tools/diff/run_diff.py "/Users/amano/WorkSpace/工作台测试集1000.xlsx"

# DB 层：真实生产库 1019 条（脚本自动复制，不会写原库）
python3 tools/diff/run_db_diff.py

# 退出码 0 = 语义一致，1 = 有真实差异 —— 可直接接 CI
```

**当前已验证状态**

| 层 | 状态 |
|---|---|
| xlsx（`internal/xlsx`） | ✅ 语义不一致 0 / 1000 |
| DB（`internal/testcasedb`） | ✅ 20 / 20 查询电池一致 |
| 分析层（logparser/assertor/metrics/trajectory） | ⬜ **尚未差分**（下一步） |
| 评估层（evaluator/llm/rubric/formatcheck/safetyscan） | ⬜ **尚未差分** |
| 驱动层（driver/ui_driver） | ⬜ **尚未开始移植** |
| 服务层（server/pipeline/report） | ⬜ **尚未开始移植** |

---

## 5. ⚠️ 一个必须知道的前提：Python oracle 自身是不确定的

`core/xlsx_reader.py` 的 `infer_targets` 遍历 **set**，Python 字符串哈希默认随机化
（`PYTHONHASHSEED`）——**同一份代码、同一输入，多次运行会产生不同顺序的 `targets`**。
实测 12 次运行出现 4 种不同结果。

**含义**：
- 「逐字节对齐 Python」这个目标**在原理上不成立**；
- 严格 diff 会报出**误导性的"幽灵差异"**，不要为此去改 Go 代码；
- Go 侧已确定化（这比 Python 更好），差分工装默认对「顺序」做归一化，
  并把"仅顺序不同"单独标注，不会误报为真实差异。

**待决策**：是否同步修复 Python 原版的 `infer_targets`（约 1 行）。
**在得到用户明确授权前，不要修改 `../ruiyun-ui-test-platform` 的任何文件。**

**推论**：后续给 logparser / assertor / metrics / trajectory 做差分时，
若这些层也存在 set / dict 遍历顺序依赖，会重演同样问题 —— 先确认顺序稳定性，
再判定差异真伪。

---

## 6. 移植保真度陷阱（做后续层时逐条对照）

按危险程度排序，详见 [`docs/P0-差分验证报告.md`](docs/P0-差分验证报告.md) 与
可行性报告。高频踩坑点：

1. **rune vs byte** —— Python `len(str)` 数码点，Go `len(string)` 数字节。
   所有长度断言、±2% 截断判定、token 估算在中文下都会偏移 → 用 `utf8.RuneCountInString`。
2. **`ast.literal_eval`** —— 真实日志里工具结果是 **Python repr**（单引号、`None`、`True`），
   不是 JSON。已在 `internal/logparser/pyliteral.go` 实现，注意保持容错边界一致。
3. **银行家舍入** —— Python `round()` 是 half-to-even，Go `math.Round` 是
   half-away-from-zero。聚合分数第 2 位小数会漂移。
4. **naive datetime 时区** —— `datetime.fromisoformat` 得本地时区 naive 时间；
   Go `time.Parse` 无时区得 UTC。所有 `*_s` 指标会静默偏移。
5. **`null` vs 零值** —— 前端靠 `?? 0`、`=== false`、`!= null` 判断。
   Go 结构体用指针 / `omitempty` 保留"缺失"语义。
6. **JSON 键顺序** —— 事实载荷与归档依赖有序字典，Go map 是随机序 → 用有序结构体。
7. **中文文案耦合逻辑** —— `SEND_KEYWORDS`、`DENY_WORDS`、`提交中`、`新建任务`、
   "分钟/小时/天/刚刚/昨天/前天"：**逐字保留，禁止"顺手清理"**。
8. **Python `\s` 是 Unicode 感知的，Go `regexp` 的 `\s` 只认 ASCII** ——
   全角空格 U+3000、不间断空格 U+00A0 在 Python 被压缩、Go 不会 → 标签索引键静默不一致。

---

## 7. 给 gemini 的提醒 / 待办

### 7.1 当前测试状态（我这边观察到的）

- `go build ./...` ✅ 通过（此前 evaluator 的 20+ 处 API 不匹配已由你修好）
- `go test ./...` ⚠️ `internal/evaluator` 的 `TestEvaluateCaseWithMockJudge` 失败：
  `expected teaching_professionalism 5, got 0x34cf52ef9728` —— 看起来是
  `s["score"]` 存了**指针**（`*float64`）而不是数值，被 `%v` 打成了地址。
  建议检查 `evaluateCase` 组装 `scores` 时是否漏了解引用。
- 其余包测试全绿。

### 7.2 建议的下一步差分顺序（按反馈速度排序）

1. **`internal/logparser` + `internal/assertor`** —— 用 22 轮真实归档
   （`~/.ruiyun-autotest/rounds/*/console.log` + 会话日志），
   覆盖 `ast.literal_eval` 与截断 5 路信号（±2% 判定）。
2. **`internal/trajectory` + `internal/metrics`** —— 12 轮归档的
   `round_detail.json` / `round_summary.json` 是现成 golden，可直接对照。
3. **`internal/evaluator`** —— 需 stub OpenAI server，成本较高。

### 7.3 工程建议

- **本项目没有 git 仓库**（`../ruiyun-ui-test-platform` 有）。
  重写期建议 `git init` 并至少提交一次基线，否则无法 review 或回滚。
- `internal/xlsx/xlsx_test.go:11` 硬编码了 `/Users/amano/WorkSpace/工作台测试集1000.xlsx`，
  换机器即静默 skip（最强的 oracle 会失效）。建议改环境变量 + 默认值，或放进 `testdata/`。
- `rowToCase` 里 `rows.Scan` 失败会**静默跳过该行**（`if err == nil` 才 append）。
  某列出现 NULL 时 Python 返回 `""` 而 Go 会丢行 —— 当前真实库无 NULL 未暴露，
  建议加防御。
- 部分文件未 `gofmt`（`artifacts.go` / `assertor.go` / `logparser.go` / `models.go`）。
  我未擅自格式化你的文件，避免冲突 —— 建议统一跑一次 `gofmt -w ./internal ./cmd`。

---

## 8. 约定

- **提交信息必须用中文**，遵循 Conventional Commits：`<type>: <中文描述>`
  （来自 `../ruiyun-ui-test-platform/.agents/rules/git.md`）。
- **绝对不要修改 `../ruiyun-ui-test-platform`**（Python oracle）—— 它是差分验证的基准。
- 改动实现后，请跑 §4 的差分作为门禁；新增行为请补回归测试。
