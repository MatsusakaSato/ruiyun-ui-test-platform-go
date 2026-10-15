// Package sysutil 提供跨平台系统工具与辅助操作（进程管理、窗口置顶、Finder/Explorer 定位）
package sysutil

import (
	"os"
	"path/filepath"
	"strings"
)

// expandHome 把开头的 ~ / ~/ 展开成本机家目录（其余原样返回）
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// OpenTarget 用系统默认程序打开文件（预设用例库附件气泡里点文件名触发）
func OpenTarget(target string) (bool, string) {
	p := expandHome(strings.TrimSpace(target))
	if p == "" {
		return false, "缺少 path 参数"
	}
	st, err := os.Stat(p)
	if err != nil {
		return false, "路径不存在：" + target
	}
	if st.IsDir() {
		return false, "不是文件：" + target
	}
	return openInDefaultApp(p)
}

// RevealTarget 在文件管理器中选中或定位目标文件/目录
func RevealTarget(target string) (bool, string) {
	p := expandHome(target)

	st, err := os.Stat(p)
	if err != nil {
		parent := filepath.Dir(p)
		if pst, perr := os.Stat(parent); perr == nil && pst.IsDir() {
			p = parent
		} else {
			// 全角冒号：文案会由前端直接展示，保持这一形式
			return false, "路径不存在：" + target
		}
	} else if !st.IsDir() {
		// p 是文件
	}

	return revealInFileManager(p, st != nil && !st.IsDir())
}
