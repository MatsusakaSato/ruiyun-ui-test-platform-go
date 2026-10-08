# 睿云智能工作台 · 全自动 UI 测试平台

面向 Electron 应用（睿云智能工作台）的自动化 UI 测试工具。用 CDP 驱动真实界面跑用例，
从会话日志中判定缺陷（工具调用失败、死循环、重复调用、内容截断），产出 HTML 报告并验证复现率。

只看客观信号，不评判输出质量。

## 使用

需要 Go 1.26+ 和已安装的被测应用。

```bash
make build           # 编译到 bin/ruiyun
make cross-compile   # 交叉编译到 dist/

ruiyun serve                      # 启动平台（默认），访问 http://127.0.0.1:8765
ruiyun pipeline --cases 1         # 跑流水线：UI 自动化 → 日志校验 → 报告
ruiyun repro --times 3            # 复现率验证
ruiyun discover                   # 导出界面可交互元素
ruiyun version | help
```

首次运行会把 `config.template.yaml` 复制到 `~/.ruiyun-autotest/config.yaml`（Windows 为
`%USERPROFILE%\.ruiyun-autotest\`），改配置就改这份。

## 结构

```
cmd/ruiyun/     CLI 入口（serve / pipeline / repro / discover）
internal/       各功能模块，核心：
  server/       本地 HTTP 服务 + 内嵌 Web 界面
  driver/ cdp/  UI 驱动与 CDP 客户端
  pipeline/     流水线编排
  testcasedb/   用例库（SQLite）
  logparser/ trajectory/  日志解析与轨迹重建
  assertor/ evaluator/    缺陷断言与评估
  notify/       钉钉群通知（流水线收尾推送结果摘要）
  canon/        文本归一化与数值舍入
scripts/        构建与开发启动脚本
```

开发：

```bash
make fmt / vet / lint
go test ./...

./scripts/launch_app_dev.sh --detach   # 后台启动被测应用（--dry-run / --stop 可选）
```

## 注意

- 平台不主动关闭应用，运行结束保持存活，下次复用调试端口。
- 开启自动确认后会自动点击确认卡片，但拒绝/取消/关闭/停止类选项永不点击。

## 许可

内部项目，未附带开源许可证。
