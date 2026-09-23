// Package sysutil 提供跨平台系统工具与辅助操作（进程管理、窗口置顶、Finder/Explorer 定位）
package sysutil

import (
	"os"
	"path/filepath"
	"strings"
)

// RevealTarget 在文件管理器中选中或定位目标文件/目录
func RevealTarget(target string) (bool, string) {
	p := target
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				p = home
			} else if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
				p = filepath.Join(home, p[2:])
			}
		}
	}

	st, err := os.Stat(p)
	if err != nil {
		parent := filepath.Dir(p)
		if pst, perr := os.Stat(parent); perr == nil && pst.IsDir() {
			p = parent
		} else {
			return false, "路径不存在: " + target
		}
	} else if !st.IsDir() {
		// p 是文件
	}

	return revealInFileManager(p, st != nil && !st.IsDir())
}
