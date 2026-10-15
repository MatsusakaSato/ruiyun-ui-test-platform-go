# 睿云智能工作台 · 全自动 UI 测试平台

面向 Electron 应用「睿云智能工作台」的 UI 自动化测试工具。平台以 Chrome DevTools Protocol
接管渲染进程，逐条发送用例，依据会话日志判断执行链路是否正常（工具调用失败、死循环、重复
调用、答复截断等），生成 HTML 报告。

## 技术栈

| 层 | 选型 |
| --- | --- |
| 语言 | Go 1.26；HTTP 服务使用标准库 `net/http` |
| 前端 | 原生 HTML / CSS / ES Module，无框架与构建步骤；`go:embed` 编入二进制 |
| 浏览器控制 | Chrome DevTools Protocol（HTTP + WebSocket，`gorilla/websocket`） |
| 存储 | SQLite（`modernc.org/sqlite`，纯 Go，WAL）；配置为 YAML（`yaml.v3`） |
| 表格 | `excelize/v2` 读 xlsx；`golang.org/x/text` 解码 GBK / GB18030 编码的 CSV |
| 报告 | `html/template` 配合内嵌模板，输出单文件 HTML |
| 质量评分 | 任意 OpenAI 兼容的 `/chat/completions` 接口 |
| 通知 | 钉钉自定义机器人 webhook，支持加签 |
| 构建 | Makefile；`make dist` 以 `CGO_ENABLED=0` 交叉编译五平台并生成 SHA-256 校验和 |

## 功能

### 用例管理

- 字段：提问（必填）、场景、难度、自定义标签、附件。场景与难度为自由标签；自定义标签数量与
  内容不限，不得含逗号（逗号兼作输入确认键与筛选参数分隔符）；附件存本机绝对路径，运行时投递。
- 筛选：关键字（编号 / 名称 / 提问）、场景、难度、自定义标签、附件需求。场景与难度命中任一，
  自定义标签须全部命中。列表每页 50 条，支持批量删除、加入本轮、逐行编辑（编号与序号不变）。
- 导入：Excel / CSV，表头须含「提问」，「场景」「难度」「附件」可选；编号与名称由服务端生成，
  表格中相应列忽略。先校验预览，再按追加或覆盖写入（覆盖清空当前库）。
- 附件库：工作区 `uploads/`，每个附件一个时间戳目录，可上传、选取、删除；删除文件时同步移除
  全部用例的引用。单文件上限 64MB。

### 执行

- 队列支持逐行修改提问与挂载附件；回车在当前行下方插入新用例，`Ctrl/⌘ + 回车` 启动测试。
- 发送串行、生成并行：前一条用例的新会话目录出现后才发送下一条；在途上限 `max_inflight`
  （默认 5，可调 1–20，取 1 退化为完全串行）。
- 单条流程：点击「新建任务」→ 投递附件（可选）→ 输入提问 → 发送 → 等待新会话 → 等待生成结束。
  等待上限默认 1200 秒，语义为等待上限而非超时判异常：达到上限即停止等待，按已落盘日志输出
  结果，不计入 UI 异常。
- 自动点击确认（可开关）：会话等待期间轮询主对话区，代点确认卡片并作答选项卡（自定义输入框
  填入「请你按最合理的方式继续，不用再问我」）。拒绝、取消、关闭、停止类按钮不点击；同状态、
  同卡片、累计次数均设熔断阈值，触发后停用并标记「需人工确认」。
- 应用常驻：平台不主动关闭应用，运行结束后保留实例，后续探测到调试端口即复用。应用在运行但
  未开启调试端口时（如手动启动），默认先关闭再以调试模式重启；Electron 为单实例应用，旧实例
  存活时新进程不会打开调试端口。关闭应用使用顶栏「关闭实例」。

### 缺陷判定

硬规则 12 条，阈值配置于 `config.yaml` 的 `rules` 段：

| 规则 | 判定条件 |
| --- | --- |
| `TOOL_CALL_FAILED` | `success=false`、`is_error` 为真、`error` 非空，或诊断文本命中 `error_markers` |
| `TOOL_RESULT_MISSING` | 工具返回为空 |
| `ORPHAN_TOOL_CALL` | 声明的调用 ID 与实际调用不一致 |
| `LOOP_CONSECUTIVE` | 同一工具连续调用达阈值（默认 5） |
| `LOOP_TOTAL` | 单会话内同一工具调用总次数达阈值（默认 8） |
| `DUPLICATE_CALL` | 同参数重复调用 |
| `MAX_ITERATIONS_HIT` | 达到 agent 迭代上限 |
| `EMPTY_REQUIRED_ARG` | 工具必填参数为空 |
| `NO_FINAL_ANSWER` | 无最终答复 |
| `OUTPUT_TRUNCATED` | 流式未结束、缺少完成时间、结尾为接续词、长度落在截断上限附近或结构未闭合 |
| `SESSION_NOT_CLOSED` | 会话结束时仍在生成 |
| `CONFIRM_MANUAL_NEEDED` | 自动确认熔断，需人工介入 |

格式判定另有两项：产物类型一致性（期望类型由提问原文推断）与要求项覆盖率（要求项由模型自
提问抽取后逐项匹配），各占 0.5 权重。

### 报告与归档

- 归档至工作区 `rounds/<run_id>/`，含 `console.log`、`cases.yaml`、`round_summary.json`、
  `round_detail.json`、`report.html`；执行过质量评估的轮次另有 `evaluation.json`。
- 报告默认输出至程序运行目录的 `report/ruiyun_report.html`（文件名由 `--report-name` 指定），
  含本轮概览（用例数、通过数、断言失败数、UI 失败数、问题数与规则种类）、逐用例的调用链路与
  客观信息（耗时、工具调用、思考步数、token 估算、调用链自洽性、会话收尾）、每条问题的表现、
  影响、证据与复现方法。
- 历史记录页按日期与关键字检索轮次；左栏列通过数与问题数，右栏详情含四个页签：轨迹时间线
  （思考、自动确认、工具调用、产物，可展开查看参数与结果全文）、客观信息、问题发现、质量评估。

### 质量评估（可选）

- 入口：历史记录页选中轮次 →「质量评估」页签 →「开始评估」。
- 维度与档位锚点定义于 `internal/rubric/rubric.go`：20 个维度、4 个分组（结果质量、教学专业
  质量、安全与可靠、系统与 Agent 能力），按场景选取适用维度。
- 打分由模型完成，本地算法仅提供参考分；分数须落在已定义档位内，档位外记「未评」。命中安全
  红线时安全维度强制 0 分，覆盖模型判定。
- 结果写入轮次目录；界面按用例展示得分、参考分、判定理由与原始 JSON。

### 复现率验证

按「规则 + 工具」聚合成配方，取提问最短的会话为复现入口，重复 N 次统计命中率：100% 记为必现，
不低于 60% 记为高概率，其余记为偶发。可随流水线执行（`--repro-times N`）或用 `ruiyun repro`
单独执行。

### 通知

「发送通知」页手动发送至钉钉群：正式群发送前确认，其余群直接发送。token 与加签密钥存于工作区
（权限 0600，不纳入版本库），界面支持增删群、显示明文与恢复默认。通知模板与预设用例同库，
支持新增、重命名、修改与删除。流水线不自动推送通知。

## 使用方法

### 环境要求

- Go 1.26 及以上，仅编译需要。
- 被测应用已安装，默认路径 macOS 为 `/Applications/睿云智能工作台.app/Contents/MacOS/睿云智能工作台`，
  Windows 为 `C:\Program Files\srtclaw\睿云智能工作台.exe`；路径可在界面修改。
- 可选 `air`，仅 `ruiyun dev` 使用，未安装时回退至内置文件监听。
- 无 Node.js 与 Python 依赖。

### 编译

```bash
make build    # 编译至 bin/ruiyun，注入 git 版本号
make dist     # 交叉编译 darwin/arm64、darwin/amd64、linux/amd64、linux/arm64、windows/amd64 至 dist/
make release  # 通过 gh 将 dist/ 产物发布至 GitHub Release
```

### 启动

```bash
./bin/ruiyun                      # 等同于 ruiyun serve，监听 127.0.0.1:8765
./bin/ruiyun serve --port 9000    # 指定端口
./bin/ruiyun serve --allow-lan    # 绑定 0.0.0.0 并放行私网 IP；临时用途，用毕须移除并重启
```

浏览器访问 `http://127.0.0.1:8765`。写操作（POST / DELETE）执行同源校验：Host 与 Origin 须为
`127.0.0.1`、`localhost` 或 `::1`，启用 `--allow-lan` 时放行私网地址。首次启动将仓库内
`config.template.yaml` 复制为工作区 `config.yaml`，后续以工作区文件为准；工作区默认
`~/.ruiyun-autotest/`（Windows 为 `%USERPROFILE%\.ruiyun-autotest\`），可在「设置 → 应用与
工作区」修改，修改后旧数据自动迁移。

### 界面操作顺序

1. 「设置 → 应用与工作区」配置被测应用路径与会话日志根目录（`sess_*` 的父目录）；配置有误将
   无法接入应用。
2. 「测试用例 → 库」准备用例：自 Excel 导入，或点击「+ 新增用例」创建（提问必填，其余可留空）；
   勾选后「加入本轮」，在「待执行」核对。
3. 顶栏确认「环境」档案（开发 / 线上，环境变量仅在应用启动时注入）；在设置中按需开启
   「自动点击确认」、调整并发数。
4. 点击「运行测试」；顶栏「终止测试」可中断，中断后不生成报告。
5. 「历史记录」查看该轮轨迹与问题，需要内容评分时点击「开始评估」；通知在「发送通知」页发送。

### 命令行

`ruiyun` 省略子命令时等同于 `ruiyun serve`；首个子命令之后的参数交由该子命令解析。

| 子命令 | 参数 | 说明 |
| --- | --- | --- |
| `serve` | `-port`（8765）、`-host`（127.0.0.1）、`-allow-lan` | 启动本地平台服务 |
| `dev` | `-port`（8765）、`-no-air` | 前台开发模式：改 `web/` 免编译即时刷新，改 `*.go` 自动重编译并重启 |
| `pipeline` | `-cases`、`-cases-file`、`-run-id`、`-report-name`、`-max-inflight`、`-auto-confirm`（`on` / `off`）、`-repro-times`、`-repro-limit` | 三段式流水线：UI 自动化 → 日志校验 → 报告与归档 |
| `repro` | `-list`、`-rule`、`-tool`、`-all`、`-times`（3）、`-update-report`、`-render-only` | 复现率验证器 |
| `discover` | `-launch`、`-settle`（4.0）、`-close-app` | 导出 DOM 中可交互元素至 `artifacts/ui_dom.json` |
| `version` / `help` | — | 版本信息（含 commit 与构建时间）/ 帮助 |

```bash
./bin/ruiyun pipeline --cases 1 --run-id run_20260101_000000
./bin/ruiyun repro --list                     # 仅列出 bug 配方，不执行验证
./bin/ruiyun repro --rule TOOL_CALL_FAILED --times 5
```

`-keep-app` 仍可解析但已废弃，不再生效。`-cases-file` 接受 YAML / JSON（裸数组或
`{cases: [...]}`）与 SQLite 库文件；未指定时使用预设用例库。

### 配置

配置由三层合并，优先级为本机覆盖（`.app_settings.json`）> 工作区 `config.yaml` > 内置默认。

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `app.binary` | 空 | 被测应用可执行文件路径，留空用内置默认 |
| `app.debug_port` | 9222 | Electron 渲染进程调试端口 |
| `app.ui_ready_timeout_s` | 180 | 等待主界面渲染完成的最长秒数；端口就绪不等于界面可用 |
| `app.env_profile` | `dev` | 启动时注入的环境档案；`production` 为空档案 |
| `app.auto_confirm` | true | 自动点击确认的初始状态；界面开关仅作用于本次运行 |
| `app.close_app_after_run` | false | 运行结束后是否关闭由平台启动的实例 |
| `app.restart_if_no_debug_port` | true | 应用在运行但无调试端口时，是否先关闭再以调试模式启动 |
| `paths.session_root` | 空 | 会话日志根目录，留空用 `~/.srtclaw/workspace/session` |
| `case_timeout_s` | 1200 | 单条用例等待上限 |
| `max_inflight` | 5 | 同时在途的会话数上限 |
| `expected_tools` | 5 项 | `todo_create`、`todo_complete`、`mcp_paid_search`、`mcp_write_workspace_file`、`read_memory` |
| `rules.*` | 见模板 | 断言阈值：连续调用次数、错误特征词、截断上限值与自然收尾字符等 |
| `llm.eval_concurrency` | 3 | 质量评估并发数 |

模型配置存工作区 `.llm_secrets.json`，群机器人配置存工作区 `.notify_secrets.json`，权限均为
0600，不纳入版本库。

### 数据目录

| 内容 | 位置 |
| --- | --- |
| 配置 | 工作区 `config.yaml` |
| 本机覆盖（应用路径 / 日志目录 / 工作区） | `os.UserConfigDir()/ruiyun-ui-test-platform/.app_settings.json` |
| 预设用例、通知模板 | 工作区 `testcases.db` |
| 附件库 | 工作区 `uploads/`，每个附件一个时间戳目录 |
| 轮次归档 | 工作区 `rounds/<run_id>/` |
| 报告 | 程序运行目录 `report/`，归档时复制一份至轮次目录 |

### 目录结构

```
cmd/ruiyun/         CLI 入口（serve / dev / pipeline / repro / discover）
internal/
  server/           本地 HTTP 服务与内嵌 Web 界面（web/ 为前端源码）
  driver/ cdp/      UI 驱动与 CDP 客户端
  pipeline/         流水线编排
  testcasedb/       用例库与通知模板（SQLite）
  logparser/ trajectory/  会话日志解析与轨迹重建
  assertor/         断言规则
  formatcheck/ rubric/ intent/ evaluator/  格式判定、评分维度、意图推断与评估
  safetyscan/       答复与产物的安全扫描
  report/           报告渲染（内嵌 HTML 模板）
  rounds/ metrics/  轮次归档读取与指标汇总
  repro/            复现率验证
  notify/           钉钉群通知
  xlsx/ artifacts/ canon/  表格解析、产物抽取、文本归一化
resource/           导入模板 template.xlsx
```

### 开发

```bash
make dev     # 热重载：改 web/* 免编译即时刷新，改 *.go 自动重编译并重启
go test ./...
go vet ./...
```

前端资源经 `go:embed` 编入二进制，改 `internal/server/web/` 后须重新 `make build` 才能在非 dev
模式生效；`make dev` 直接读磁盘。运行中的服务进程使用启动时的二进制，改 Go 代码后须重新编译
并重启。

## 已知边界

- HTML 报告仅含硬规则与客观指标，不含模型评分；评估结果单独存于轮次目录，且须手动触发。
- 应用常驻策略下，测试默认复用正在运行的应用实例；需要全新实例时先在界面「关闭实例」。
- 报告仅可从磁盘打开（`report/` 或轮次目录），界面未提供入口；token 用量按字符量估算，非接口
  返回的真实用量。
- 请求全部发往本机服务，平台无账号体系；同源校验仅防跨站请求，不限制本机其他程序。
- 复现率样本少于 3 次时，结论仅供参考。
- 界面的「并发数」与「自动点击确认」仅作用于本次运行，不写回 `config.yaml`。
- `pipeline --config` 当前不影响实际加载的配置，实际读取工作区 `config.yaml`。

## 许可

内部项目，未附带开源许可证。
