# 睿云智能工作台 · 全自动 UI 测试平台

面向 Electron 桌面应用（睿云智能工作台）的全自动 UI 测试平台。通过 CDP 驱动真实界面按用例
发起多轮会话，从 agent 调用链路日志中判定**硬性缺陷**（工具调用失败、死循环、重复调用、内容截断），
输出带证据链的 HTML 报告，并支持缺陷复现率验证。

只测「硬 bug」，不评判输出质量：所有判定都基于可观测的结构化信号，不做主观打分。

## 功能

- **UI 驱动**：CDP 接入渲染进程，真实点击/输入/确认，窗口置顶、单实例复用
- **三段式流水线**：UI 自动化 → 日志校验（断言 + 安全扫描）→ 报告归档
- **硬 bug 判定**：工具调用失败、连续/高频死循环、答复截断、破坏性命令红线
- **复现率验证**：每条 bug 签名自动复现 N 次，给出稳定复现/偶发/不可复现
- **可视化平台**：内嵌 Web 界面，管理用例库、轮次历史、在线报告、日志控制台、环境档案
- **用例库**：SQLite 存储，支持新增、Excel/JSON 导入与去重合并
- **LLM 评估**：可选接入大模型做格式化/覆盖度评估（与硬 bug 判定独立）
- **跨平台**：macOS / Linux / Windows 可构建运行，支持交叉编译

## 快速开始

需要 Go 1.26+，以及已安装的被测应用。

```bash
make build          # 编译到 bin/ruiyun
make lint           # vet + 格式检查
make cross-compile  # 交叉编译到 dist/
```

统一入口 `ruiyun`，含四个子命令：

```bash
ruiyun serve                        # 启动平台服务（默认），访问 http://127.0.0.1:8765
ruiyun serve --allow-lan            # 临时放行局域网访问
ruiyun pipeline --cases 1           # 执行流水线
ruiyun repro --times 3              # 复现率验证
ruiyun discover                     # 导出 DOM 可交互元素
ruiyun version | ruiyun help
```

首次运行会把内置 `config.template.yaml` 复制到用户工作区（macOS `~/.ruiyun-autotest/`，
Windows `%USERPROFILE%\.ruiyun-autotest\`），并创建默认数据目录。

## 配置

三层合并，优先级从高到低：`.app_settings.json`（界面写入）> `<用户工作区>/config.yaml` >
内置 `config.template.yaml`。日常修改请改工作区里的 `config.yaml`。

| 配置块 | 作用 |
| --- | --- |
| `app` | 应用路径、调试端口、启动/就绪超时、自动确认、环境档案 |
| `paths` | 会话日志根、工作区根、产物/报告/日志目录 |
| `rules` | 判定阈值：截断上限、死循环次数、错误特征词、自然收尾字符 |
| `expected_tools` | 期望被调用的工具白名单 |
| `llm` / `format_weights` | 大模型参数与质量评估权重 |

关键行为：

- **应用常驻**：平台不主动关闭应用，运行结束保持存活；下次探测到调试端口直接复用
- **单实例唤醒**：Electron 单实例下旧实例会吞掉新进程参数、调试端口永不开监听，故启动前先清理残留实例
- **自动确认**：agent 常停在「请求确认」等待人工点击，无人干预会产出「假通过」；开启后自动点击，
  但**拒绝/取消/关闭/停止类选项永不点击**

## 架构

```
cmd/ruiyun/          CLI 入口（serve / pipeline / repro / discover）
internal/
  server/            本地 HTTP 服务 + 内嵌 Web 前端（web/）
  pipeline/          三段式流水线编排
  driver/  cdp/      UI 驱动与 CDP 客户端
  config/            配置加载与三层合并
  testcasedb/        SQLite 用例库    xlsx/  Excel 解析
  logparser/ trajectory/  调用链路日志解析与轨迹重建
  assertor/ evaluator/ metrics/  硬 bug 断言、评估与指标聚合
  safetyscan/ formatcheck/ rubric/  安全扫描、格式校验、评分细则
  llm/ repro/        大模型接入、复现率验证
  rounds/ report/ artifacts/ intent/  归档读取、报告、产物、意图识别
  canon/ models/ sysutil/  归一化工具、领域模型、系统工具
scripts/             构建脚本、开发启动脚本
```

### `internal/canon`：归一化与舍入

集中处理「同一文本/数值必须归一到同一结果」的细节，供全项目复用：

- **空白字符集**：完整的 29 码点 Unicode White_Space（含 U+3000 全角空格、U+00A0 NBSP、
  U+2028/U+2029）。Go 正则的 `\s` 只覆盖 5 个字符，中文与排版语料会**静默漏匹配**，
  故相关正则一律用 `canon.SpaceClass`
- **strip 系列**：`Strip` / `CleanSpace` / `ContainsSpace` 覆盖 U+001C–U+001F
  （`strings.TrimSpace` 会漏掉这四个 ASCII 分隔符）
- **`Round`**：银行家舍入（ties-to-even），以浮点二进制精确值为准。
  `math.Round(x*10)/10` 既方向错又有二次舍入（`31.25 → 31.3`，正确 `31.2`），
  项目内小数舍入一律走 `canon.Round`
- **`FormatOSError`**：把文件系统错误格式化为 `[Errno N] 首字母大写消息: '路径'`，用于界面提示

## 开发与测试

```bash
make fmt / vet / lint / clean
go test ./...
```

开发期启动被测应用（非平台自身逻辑）：

```bash
./scripts/launch_app_dev.sh --dry-run   # 查看环境变量与命令
./scripts/launch_app_dev.sh             # 前台启动
./scripts/launch_app_dev.sh --detach    # 后台脱离并等待 CDP 端口就绪
./scripts/launch_app_dev.sh --stop      # 结束应用
```

Windows 用 `scripts/launch_app_dev.ps1`，参数同名。

> `go test` 目前只覆盖核心纯函数（用例去重键等），其余模块以端到端流水线运行覆盖。

## 构建产物

`make cross-compile` 在 `dist/` 生成各平台压缩包及 `checksums.txt`（SHA-256）。
平台：darwin / linux 各 amd64+arm64，windows amd64。每个包内含对应平台的 `ruiyun` 与
`config.template.yaml`。

## 许可

内部项目，未附带开源许可证。
