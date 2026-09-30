//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// procAttrNewSession 让测试进程独立于服务所在进程组
// （等价于 setsid，进程自成会话首进程）
func procAttrNewSession() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// terminateProcess 强制终止测试进程，避免停止后继续执行并生成报告
func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
