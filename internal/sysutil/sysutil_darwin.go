//go:build darwin

package sysutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func revealInFileManager(target string, isFile bool) (bool, string) {
	cmd := exec.Command("open", "-R", target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return false, fmt.Sprintf("open -R 执行失败: %s", msg)
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

// ClipboardImage 将图片文件写入系统剪贴板（macOS: osascript）
func ClipboardImage(path string) (bool, string) {
	script := "on run argv\n    set the clipboard to (POSIX file (item 1 of argv))\nend run\n"
	cmd := exec.Command("osascript", "-e", script, path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Sprintf("写入系统剪贴板失败: %s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return true, ""
}

// ReadAppVersion macOS 从 Info.plist 读取版本号
func ReadAppVersion(binaryPath string) (version string, bundleID string) {
	// binary: .../Contents/MacOS/AppName -> Info.plist is in .../Contents/Info.plist
	binDir := filepath.Dir(binaryPath)
	contentsDir := filepath.Dir(binDir)
	plistPath := filepath.Join(contentsDir, "Info.plist")

	data, err := os.ReadFile(plistPath)
	if err != nil {
		return "", ""
	}
	content := string(data)
	// 简单的 plist 键值提取
	extractKey := func(key string) string {
		idx := strings.Index(content, "<key>"+key+"</key>")
		if idx == -1 {
			return ""
		}
		sub := content[idx:]
		valStart := strings.Index(sub, "<string>")
		if valStart == -1 {
			return ""
		}
		valStart += len("<string>")
		valEnd := strings.Index(sub[valStart:], "</string>")
		if valEnd == -1 {
			return ""
		}
		return sub[valStart : valStart+valEnd]
	}

	return extractKey("CFBundleShortVersionString"), extractKey("CFBundleIdentifier")
}
