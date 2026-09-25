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

// terminateProcess 先发 SIGTERM，给进程留出优雅退出的机会
func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}
