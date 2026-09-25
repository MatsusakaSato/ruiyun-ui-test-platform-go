# 睿云智能工作台 · 全自动 UI 测试平台

面向 **Electron 桌面应用（睿云智能工作台）** 的全自动 UI 测试平台。通过 CDP（Chrome DevTools
Protocol）驱动真实界面，按用例自动发起多轮会话，再从 agent 调用链路日志中判定**硬性缺陷**
（工具调用失败、死循环、重复调用、内容截断等），输出带证据链的 HTML 报告，并支持对缺陷做
N 次复现率验证。

> 目标定位：**只测「硬 bug」，不评判输出内容质量**。所有判定都基于可观测的结构化信号
> （工具调用序列、错误特征词、截断特征），不做主观打分。

---

## 功能概览

| 能力 | 说明 |
| --- | --- |
| **UI 自动化驱动** | 基于 CDP 接入 Electron 渲染进程，真实点击、输入、确认；窗口置顶、单实例复用 |
| **三段式流水线** | Stage 1 UI 自动化 → Stage 2 日志校验（断言/安全扫描） → Stage 3 报告与归档 |
| **硬 bug 判定** | 工具调用失败、连续/高频死循环、答复截断、破坏性命令红线扫描 |
| **复现率验证** | 对每条 bug 签名自动复现 N 次，给出稳定性（稳定复现 / 偶发 / 不可复现） |
| **可视化平台** | 内嵌 Web 界面：用例库管理、轮次历史、在线报告、日志控制台、环境档案切换 |
| **用例库** | SQLite 预设用例库，支持新增/导入（Excel、JSON）、按去重键合并 |
| **LLM 质量评估** | 可选接入大模型对答复做格式化与覆盖度评估（与硬 bug 判定相互独立） |
| **跨平台** | macOS / Linux / Windows 均可构建运行，产物可交叉编译 |

---

## 快速开始

### 环境要求

- Go **1.26+**
- 被测应用已安装（默认路径见下，可在界面或配置中覆盖）

### 构建

```bash
# 编译到 bin/ruiyun
make build

# 静态检查 + 格式检查
make lint

# 多平台交叉编译到 dist/（darwin/linux 各 amd64+arm64，windows amd64）
make cross-compile
```

也可直接使用 `go build`：

```bash
go build -o bin/ruiyun ./cmd/ruiyun
```

### 运行

`ruiyun` 是统一入口，包含四个子命令：

```bash
# 启动可视化平台本地服务（默认子命令），浏览器访问 http://127.0.0.1:8765
ruiyun serve
ruiyun serve --port 8765 --allow-lan     # 临时放行局域网访问

# 执行三段式流水线（UI 自动化 → 日志校验 → 报告）
ruiyun pipeline --cases 1

# 复现率验证器
ruiyun repro --times 3

# UI 诊断工具：导出 DOM 中可交互元素
ruiyun discover

ruiyun version
ruiyun help
```

首次运行会在用户工作目录（macOS `~/.ruiyun-autotest/`，Windows `%USERPROFILE%\.ruiyun-autotest\`）
生成 `config.yaml`（复制自内置 [`config.template.yaml`](config.template.yaml)）以及默认的数据目录。

---

## 配置

配置采用三层合并，优先级从高到低：

1. `.app_settings.json`（界面「设置」写入，运行期覆盖）
2. `<用户工作区>/config.yaml`（日常修改请改这里）
3. 内置 `config.template.yaml`（仅作初始值，不会被回写）

主要配置块：

| 配置块 | 作用 |
| --- | --- |
| `app` | 应用路径、调试端口、启动/就绪超时、自动确认策略、环境档案 |
| `paths` | 会话日志根目录、工作区根目录、产物/报告/日志目录 |
| `rules` | 硬 bug 判定阈值：截断上限、死循环次数、错误特征词、自然收尾字符 |
| `expected_tools` | 期望被调用的工具白名单 |
| `llm` | 大模型接入参数（超时、并发、温度等） |
| `format_weights` | 质量评估的格式/覆盖度权重 |

关键行为说明：

- **应用常驻**：平台默认不主动关闭应用；运行结束保持存活，下次探测到调试端口直接复用。
- **单实例唤醒**：Electron 是单实例应用，旧实例存活时新进程的 argv 会被转交然后自己退出，
  导致调试端口永不开监听 —— 平台会在启动前清理残留实例。
- **自动确认**：agent 大任务常停在「请求确认」等待人工点击，无人干预会产出「假通过」。
  开启后平台用 CDP 轮询主对话区自动点击；**拒绝/取消/关闭/停止类选项永不点击**。

---

## 架构

```
cmd/ruiyun/            CLI 入口（serve / pipeline / repro / discover）
internal/
  server/              本地 HTTP 服务 + 内嵌 Web 前端（web/）
  pipeline/            三段式流水线编排
  driver/              CDP UI 驱动：点击、输入、确认、会话管理
  cdp/                 Chrome DevTools Protocol 客户端（WebSocket）
  config/              配置加载与三层合并
  testcasedb/          SQLite 预设用例库（去重、导入、检索）
  xlsx/                Excel 用例/产物解析
  logparser/           agent 调用链路日志解析（含字面量解析器）
  trajectory/          调用轨迹重建 + 轮次归档明细
  assertor/            硬 bug 断言（失败/死循环/截断）
  evaluator/           综合评估与指标计算
  metrics/             指标聚合（指标行、复现块）
  safetyscan/          破坏性命令安全扫描
  formatcheck/         应答格式化校验
  rubric/              评分细则
  llm/                 大模型接入（质量评估）
  repro/               复现率验证
  rounds/              轮次归档读取层
  report/              HTML 报告构建（模板内嵌）
  artifacts/           产物路径解析与识别
  intent/              意图/目标识别
  canon/               文本归一化与数值舍入工具
  models/              领域模型与 JSON 序列化
  sysutil/             跨平台系统工具（进程、窗口、文件管理器定位）
scripts/               构建与开发启动脚本（launch_app_dev.sh / .ps1）
```

### `internal/canon`：归一化与舍入的单一来源

`canon` 集中处理「同一段文本/数值在不同写法下必须归一到同一结果」的细节，供全项目复用：

- **空白字符集**：定义了完整的 Unicode White_Space 集合（29 个码点，含 U+3000 全角空格、
  U+00A0 NBSP、U+2028/U+2029 等）。Go 正则的 `\s` 只覆盖 5 个字符，中文与排版语料里会
  **静默漏匹配**，因此凡涉及空白归一化的正则都改用 `canon.SpaceClass`。
- **`strip` 系列**：`Strip` / `CleanSpace` / `ContainsSpace` 覆盖 U+001C–U+001F 这四个
  ASCII 分隔符 —— Go 的 `strings.TrimSpace` 会漏掉它们。
- **`Round`**：**银行家舍入（ties-to-even）**，且以浮点数的二进制精确值为准。
  `math.Round(x*10)/10` 这类写法既方向错、又有二次舍入（如 `31.25 → 31.3`，正确为 `31.2`），
  项目内所有小数舍入一律走 `canon.Round`。
- **`FormatOSError`**：把文件系统错误格式化成 `[Errno N] 首字母大写的消息: '路径'`，
  与 Go 默认的 `*os.PathError` 输出不同，用于界面上的失败提示。

---

## 开发

```bash
make fmt          # gofmt -w internal cmd
make vet          # go vet ./...
make lint         # vet + 格式检查
make clean        # 清理 bin/ dist/ 覆盖率文件
```

启动脚本（供开发期手动拉起被测应用，非平台自身逻辑）：

```bash
./scripts/launch_app_dev.sh --dry-run   # 查看将注入的环境变量与命令
./scripts/launch_app_dev.sh             # 前台启动
./scripts/launch_app_dev.sh --detach    # 后台脱离启动并等待 CDP 端口就绪
./scripts/launch_app_dev.sh --stop      # 结束应用
```

Windows 使用 `scripts/launch_app_dev.ps1`（参数同名）。

---

## 测试

```bash
go test ./...
```

> 当前回归测试集中在核心纯函数（用例去重键等）；更多模块以端到端流水线运行覆盖。

---

## 构建产物

`make cross-compile` 会在 `dist/` 下生成各平台压缩包及 `checksums.txt`（SHA-256），
每个包里包含对应平台的 `ruiyun` 可执行文件与 `config.template.yaml`。

---

## 许可

内部项目，未附带开源许可证。
