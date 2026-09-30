//go:build windows

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
	winPath := filepath.Clean(target)
	var cmd *exec.Cmd
	if isFile {
		cmd = exec.Command("explorer", "/select,", winPath)
	} else {
		cmd = exec.Command("explorer", winPath)
	}
	_ = cmd.Start()
	return true, winPath
}

// RunningAppPIDs 获取匹配可执行文件名称的进程 pid 列表
func RunningAppPIDs(binaryPath string) []int {
	name := filepath.Base(binaryPath)
	if name == "" {
		return nil
	}
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("IMAGENAME eq %s", name), "/FO", "CSV", "/NH")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var pids []int
	lines := bytes.Split(out, []byte("\n"))
	for _, line := range lines {
		s := bytes.TrimSpace(line)
		if !bytes.HasPrefix(s, []byte("\"")) {
			continue
		}
		cols := bytes.Split(s, []byte(","))
		if len(cols) < 2 {
			continue
		}
		pidStr := string(bytes.Trim(bytes.TrimSpace(cols[1]), "\""))
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

// KillProcesses 关闭所有匹配的进程
func KillProcesses(binaryPath string) error {
	name := filepath.Base(binaryPath)
	if name == "" {
		return nil
	}

	_ = exec.Command("taskkill", "/T", "/IM", name).Run()
	for i := 0; i < 20; i++ {
		if len(RunningAppPIDs(binaryPath)) == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	_ = exec.Command("taskkill", "/F", "/T", "/IM", name).Run()
	for i := 0; i < 20; i++ {
		if len(RunningAppPIDs(binaryPath)) == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// KillProcessesNow 立即强制关闭所有匹配应用进程及其子进程
func KillProcessesNow(binaryPath string) error {
	var failures []string
	for _, pid := range RunningAppPIDs(binaryPath) {
		if err := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run(); err != nil {
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
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
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
	_ = cmd.Process.Kill()
}

// ClipboardImage 将图片文件写入系统剪贴板（Windows: PowerShell + WinForms）
func ClipboardImage(path string) (bool, string) {
	ps := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $img=[System.Drawing.Image]::FromFile($env:RUIYUN_CLIP_IMG); [System.Windows.Forms.Clipboard]::SetImage($img); $img.Dispose()`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-STA", "-Command", ps)
	cmd.Env = append(os.Environ(), "RUIYUN_CLIP_IMG="+path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Sprintf("写入系统剪贴板失败: %s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return true, ""
}

// ReadAppVersion Windows PE 版本资源读取
func ReadAppVersion(binaryPath string) (version string, bundleID string) {
	return "", ""
}
