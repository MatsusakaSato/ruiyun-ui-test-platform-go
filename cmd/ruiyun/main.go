// 睿云智能工作台 · UI 测试平台统一入口。
//
// 四个子命令：
//
//	ruiyun serve     可视化平台本地服务（默认子命令）
//	ruiyun pipeline  三段式流水线（UI 自动化 → 日志校验 → 报告）
//	ruiyun repro     复现率验证器
//	ruiyun discover  UI 诊断工具：导出 DOM 中可交互元素
//
// 服务端以子进程方式拉起 `ruiyun pipeline`（复用同一可执行文件），
// 因此 pipeline 子命令必须与 serve 同体。
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/pipeline"
	"ruiyun-ui-test-platform-go/internal/server"
	"ruiyun-ui-test-platform-go/internal/testcasedb"
)

var (
	// Version 版本号，可通过构建参数 -ldflags "-X main.Version=..." 注入
	Version = "1.0.0-dev"
	// Commit Git 提交哈希
	Commit = "none"
	// BuildTime 构建时间戳
	BuildTime = "unknown"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"

	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			usage()
			os.Exit(0)
		case "version", "--version", "-v":
			printVersion()
			os.Exit(0)
		default:
			// 首个非选项参数即子命令
			if !strings.HasPrefix(args[0], "-") {
				cmd = args[0]
				args = args[1:]
			}
		}
	}

	initRootDir()

	switch cmd {
	case "serve":
		os.Exit(runServe(args))
	case "pipeline":
		os.Exit(runPipeline(args))
	case "repro":
		os.Exit(runRepro(args))
	case "discover":
		os.Exit(runDiscover(args))
	case "version":
		printVersion()
		os.Exit(0)
	case "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令：%s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func printVersion() {
	fmt.Printf("ruiyun %s (commit: %s, built: %s, %s/%s, %s)\n",
		Version, Commit, BuildTime, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

func usage() {
	fmt.Fprint(os.Stderr, `用法：ruiyun [子命令] [选项]

子命令：
  serve       启动可视化平台本地服务（默认）
  pipeline    执行三段式流水线（UI 自动化 → 日志校验 → 报告）
  repro       复现率验证器
  discover    UI 诊断工具：导出 DOM 中可交互元素
  version     查看版本信息
  help        查看帮助说明

示例：
  ruiyun                              # 启动平台服务（127.0.0.1:8765）
  ruiyun serve --port 8765 --allow-lan
  ruiyun pipeline --cases 1 --run-id run_20260101_000000
  ruiyun version
`)
}

// initRootDir 项目根目录：优先「当前工作目录含 config.template.yaml」，
// 否则回退到可执行文件所在目录 —— 让双击/绝对路径启动也能找到配置与 web 资源。
func initRootDir() {
	if _, err := os.Stat(filepath.Join(config.RootDir, "config.template.yaml")); err == nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir := filepath.Dir(exe)
	if _, err := os.Stat(filepath.Join(dir, "config.template.yaml")); err == nil {
		config.RootDir = dir
	}
}

// ------------------------------------------------------------------ serve

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 8765, "监听端口")
	host := fs.String("host", "127.0.0.1", "监听地址")
	allowLAN := fs.Bool("allow-lan", false,
		"临时放行局域网访问：绑定 0.0.0.0 并放行私网 IP 的 Host/Origin（默认仅允许 127.0.0.1 / localhost）")
	_ = fs.Parse(args)

	bindHost := *host
	if *allowLAN {
		if bindHost == "127.0.0.1" {
			bindHost = "0.0.0.0"
		}
		fmt.Println("⚠ 已临时放行局域网访问（--allow-lan）：私网 IP 可访问本服务，" +
			"公网来源仍被拒绝。用完请去掉该参数重启。")
	}

	// 启动前先确保预设用例库存在（首次运行会建表并灌入内置用例）
	if _, err := testcasedb.InitDB(""); err != nil {
		fmt.Fprintf(os.Stderr, "初始化用例库失败：%v\n", err)
	}

	selfExe, err := os.Executable()
	if err != nil {
		selfExe = os.Args[0]
	}

	srv := server.NewServer(*allowLAN, selfExe)

	addr := fmt.Sprintf("%s:%d", bindHost, *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听 %s 失败：%v\n", addr, err)
		return 1
	}
	fmt.Printf("测试平台服务已启动: http://%s\n", addr)

	httpSrv := &http.Server{Handler: srv.Handler()}
	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "服务异常退出：%v\n", err)
		return 1
	}
	return 0
}

// ------------------------------------------------------------------ pipeline

func runPipeline(args []string) int {
	fs := flag.NewFlagSet("pipeline", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "配置文件路径")
	cases := fs.Int("cases", 0, "只执行前 N 条用例")
	casesFile := fs.String("cases-file", "", "手动指定的用例文件（YAML/JSON）")
	reproTimes := fs.Int("repro-times", 0, "对每条 bug 签名自动复现 N 次（0=关闭）")
	reproLimit := fs.Int("repro-limit", 0, "最多验证多少个 bug 签名（0=全部）")
	maxInflight := fs.Int("max-inflight", 0, "同时在途用例上限（0=沿用配置）")
	autoConfirm := fs.String("auto-confirm", "", "自动点击确认卡片：on / off（不传则沿用配置）")
	keepApp := fs.Bool("keep-app", false, "[已废弃] 应用常驻不关闭，该参数无任何作用")
	runID := fs.String("run-id", "", "轮次 ID（平台传入，用于归档该轮全部数据）")
	reportName := fs.String("report-name", "ruiyun_report.html", "报告文件名")
	_ = fs.Parse(args)

	if *keepApp {
		fmt.Println("提示：--keep-app 已失效（应用现在常驻，运行结束不会关闭）")
	}

	var ac *bool
	switch strings.ToLower(strings.TrimSpace(*autoConfirm)) {
	case "on", "true", "1":
		v := true
		ac = &v
	case "off", "false", "0":
		v := false
		ac = &v
	case "":
		// 未显式传入：沿用 config.yaml 的 app.auto_confirm
	default:
		fmt.Fprintf(os.Stderr, "无效的 --auto-confirm 取值：%s（只支持 on / off）\n", *autoConfirm)
		return 2
	}

	code, err := pipeline.RunPipeline(pipeline.PipelineOptions{
		ConfigPath:  *cfgPath,
		CasesLimit:  *cases,
		CasesFile:   *casesFile,
		ReproTimes:  *reproTimes,
		ReproLimit:  *reproLimit,
		MaxInflight: *maxInflight,
		AutoConfirm: ac,
		RunID:       *runID,
		ReportName:  *reportName,
		Logger:      func(s string) { fmt.Println(s) },
	})
	if err != nil && code != 2 {
		fmt.Fprintf(os.Stderr, "流水线异常：%v\n", err)
	}
	return code
}
