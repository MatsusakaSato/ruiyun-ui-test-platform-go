package main

// ruiyun dev —— 开发模式（热更新）。
//
//	ruiyun dev      前台运行（实时看编译日志，Ctrl-C 退出）
//
// 两级热更新：
//  1. 前端 web/*：RUYIYUN_DEV=1 时服务直接读磁盘（internal/server/dev.go），
//     改 HTML/CSS/JS 免编译，由 SSE 通知浏览器刷新。
//  2. 后端 *.go：优先交给 air（存在就用），否则用本文件内置的监听循环，
//     指纹变化即重新编译并重启子进程。
//
// 全部用标准库实现，不依赖 bash / lsof / pkill，Windows 上同样可用。

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ruiyun-ui-test-platform-go/internal/config"
)

const devPortDefault = 8765

// runDev 前台热重载：优先 air，没装就用内置的指纹监听循环。
func runDev(args []string) int {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	port := fs.Int("port", devPortDefault, "服务端口")
	noAir := fs.Bool("no-air", false, "不使用 air，强制用内置的监听循环")
	_ = fs.Parse(args)

	// 曾经的 start / stop / restart / status 子操作已移除；被当成位置参数传进来时明确报错，
	// 免得用户以为后台服务起来了，实际是前台跑着热重载。
	if rest := fs.Args(); len(rest) > 0 {
		fmt.Fprintf(os.Stderr, "[dev] 不再支持 %q：请直接前台运行 ruiyun dev（Ctrl-C 退出）\n",
			strings.Join(rest, " "))
		return 2
	}

	// 优先交给 air：Go 生态现成的热重载工具
	if !*noAir && os.Getenv("RUYIYUN_DEV_NO_AIR") == "" {
		if p, err := exec.LookPath("air"); err == nil {
			return runAir(p)
		}
	}

	fmt.Println("[dev] 未使用 air（可选：go install github.com/air-verse/air@latest），改用内置监听")
	return devWatchLoop(*port)
}

func devBinaryPath() string {
	name := "ruiyun.dev"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(config.RootDir, "bin", name)
}

func runAir(airPath string) int {
	fmt.Println("[dev] 使用 air 热重载：*.go 改动自动重启；web/* 免编译热更新")
	cmd := exec.Command(airPath)
	cmd.Env = append(os.Environ(), "RUYIYUN_DEV=1")
	cmd.Dir = config.RootDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[dev] air 退出：%v\n", err)
		return 1
	}
	return 0
}

// devWatchLoop 内置热重载：编译 → 启动 serve 子进程 → 监听 *.go 指纹 → 变化即重建重启
func devWatchLoop(port int) int {
	bin := devBinaryPath()
	fmt.Println("[dev] 首次编译…")
	if err := buildDevBinary(bin); err != nil {
		fmt.Fprintf(os.Stderr, "[dev] 编译失败：%v\n", err)
		return 1
	}

	child, err := startServeChild(bin, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dev] 启动服务失败：%v\n", err)
		return 1
	}
	defer func() { stopServeChild(child) }()

	// Ctrl-C 在这里收尾，避免 serve 子进程变孤儿继续占着端口
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	fp := goFingerprint()
	fmt.Printf("[dev] 监听中 http://127.0.0.1:%d：改 web/ 即时生效，改 *.go 自动重启（Ctrl-C 退出）\n", port)

	for {
		select {
		case <-sigCh:
			fmt.Println("\n[dev] 收到退出信号，正在停止服务…")
			stopServeChild(child)
			return 0
		case <-time.After(1 * time.Second):
		}

		cur := goFingerprint()
		if cur == fp {
			continue
		}
		fmt.Println("[dev] 检测到 Go 代码改动 → 重新编译…")
		if err := buildDevBinary(bin); err != nil {
			fmt.Fprintf(os.Stderr, "[dev] 编译失败，服务保持运行：%v\n", err)
			fp = cur
			continue
		}
		stopServeChild(child)
		child, err = startServeChild(bin, port)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[dev] 重启失败：%v\n", err)
			return 1
		}
		fp = cur
	}
}

func buildDevBinary(out string) error {
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, "./cmd/ruiyun")
	cmd.Dir = config.RootDir
	cmd.Env = os.Environ()
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v：%s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

func startServeChild(bin string, port int) (*exec.Cmd, error) {
	cmd := exec.Command(bin, "serve", "--port", strconv.Itoa(port))
	cmd.Env = append(os.Environ(), "RUYIYUN_DEV=1")
	cmd.Dir = config.RootDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// 与 runner 同组：Ctrl-C 时随 runner 一起收掉
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func stopServeChild(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// serve 子进程是我们亲自 fork 的，只有等 Wait() 返回才能确定它真的退出了：
	// 在那之前它是僵尸进程，用 kill(pid,0) 探测会一直判定为「还活着」。
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// goFingerprint 所有影响编译结果的文件指纹（路径 + 修改时间 + 大小）
func goFingerprint() string {
	var sb strings.Builder
	skip := map[string]bool{
		"bin": true, "dist": true, ".git": true, "report": true,
		"artifacts": true, "logs": true, ".workbuddy": true, "node_modules": true,
	}
	_ = filepath.WalkDir(config.RootDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
			return nil
		}
		if st, e := d.Info(); e == nil {
			fmt.Fprintf(&sb, "%s|%d|%d\n", p, st.ModTime().UnixNano(), st.Size())
		}
		return nil
	})
	return sb.String()
}
