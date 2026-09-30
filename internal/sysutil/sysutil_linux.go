//go:build linux

package sysutil

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func revealInFileManager(target string, isFile bool) (bool, string) {
	dir := target
	if isFile {
		dir = filepath.Dir(target)
	}
	cmd := exec.Command("xdg-open", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return false, fmt.Sprintf("xdg-open 执行失败: %s", msg)
	}
	return true, target
}

// RunningAppPIDs 获取匹配可执行文件名称的进程 pid 列表
func RunningAppPIDs(binaryPath string) []int {
	name := filepath.Base(binaryPath)
	if name == "" {
		return nil
	}
	out, err := exec.Command("pgrep", "-x", name).Output()
	if err != nil {
		return nil
	}
	lines := strings.Fields(string(out))
	var pids []int
	for _, l := range lines {
		if pid, err := strconv.Atoi(strings.TrimSpace(l)); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// KillProcesses 关闭所有匹配的进程（先优雅关闭后强杀）
func KillProcesses(binaryPath string) error {
	name := filepath.Base(binaryPath)
	if name == "" {
		return nil
	}

	_ = exec.Command("pkill", "-x", name).Run()
	for i := 0; i < 20; i++ {
		if len(RunningAppPIDs(binaryPath)) == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	_ = exec.Command("pkill", "-9", "-x", name).Run()
	for i := 0; i < 20; i++ {
		if len(RunningAppPIDs(binaryPath)) == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// KillProcessesNow 立即强制关闭所有匹配应用进程
func KillProcessesNow(binaryPath string) error {
	var failures []string
	for _, pid := range RunningAppPIDs(binaryPath) {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			failures = append(failures, fmt.Sprintf("pid %d: %v", pid, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("强制关闭应用进程失败：%s", strings.Join(failures, "; "))
	}
	return nil
}

// SpawnProcess 拉起独立会话的应用进程
func SpawnProcess(args []string, env []string) (*exec.Cmd, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("empty args")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// TerminateProcessGroup 终止进程组
func TerminateProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		_ = cmd.Process.Kill()
	}
}

// ClipboardImage 将图片文件写入系统剪贴板（Linux: xclip / wl-copy）
func ClipboardImage(path string) (bool, string) {
	if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-i", path)
		if err := cmd.Run(); err == nil {
			return true, ""
		}
	}
	if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command("wl-copy", "--type", "image/png")
		cmd.Stdin = nil
		_ = cmd.Run()
	}
	return false, "Linux 环境未找到支持的剪贴板工具 (xclip / wl-copy)"
}

// ReadAppVersion Linux 从二进制或包信息读取版本号
func ReadAppVersion(binaryPath string) (version string, bundleID string) {
	return "", ""
}
