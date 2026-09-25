//go:build windows

package server

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// procAttrNewSession Windows：CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS
// （让测试进程独立于服务所在进程组）
func procAttrNewSession() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}

// terminateProcess Windows 无 SIGTERM，直接强杀
func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
