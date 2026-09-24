//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// procAttrNewSession 让测试进程独立于服务所在进程组
// （Python 对应 start_new_session=True）
func procAttrNewSession() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// terminateProcess 先发 SIGTERM（Python 的 proc.terminate()）
func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}
