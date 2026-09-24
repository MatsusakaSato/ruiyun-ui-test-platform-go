package pyre

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unicode"
)

// pyStrerror 常见 errno 的 Python `os.strerror()` 文案。
//
// 直接用 Go 的 `syscall.Errno.Error()` 会得到**小写**英文
// （`operation not permitted`），而 Python 走 C 的 `strerror(3)`，
// 首字母是**大写**的（`Operation not permitted`）。
var pyStrerror = map[int]string{
	1:  "Operation not permitted",
	2:  "No such file or directory",
	13: "Permission denied",
	17: "File exists",
	20: "Not a directory",
	21: "Is a directory",
	28: "No space left on device",
	30: "Read-only file system",
	36: "File name too long",
}

// OSError 复刻 Python `OSError.__str__` 的文案：
//
//	[Errno 1] Operation not permitted: '/path/to/file'
//
// Go 的 `*os.PathError` 打印成 `mkdir /path: operation not permitted`
// （小写、带 op 前缀、路径不加引号），与 Python 完全不同。
// 这条文案会**直接显示在界面里**（附件上传失败、目录创建失败等），
// 因此按 Python 原文对齐而不是留着 Go 的默认写法。
//
// 注：这是**环境相关**的代码路径 —— 只有文件系统操作真的失败时才会走到，
// 可写环境下永不触发；对齐它是为了让失败提示与 Python 版一字不差。
func OSError(err error) string {
	var pe *os.PathError
	if !errors.As(err, &pe) {
		return fmt.Sprintf("%v", err)
	}
	errno, ok := pe.Err.(syscall.Errno)
	if !ok {
		return fmt.Sprintf("%v", err)
	}
	msg, hit := pyStrerror[int(errno)]
	if !hit {
		msg = capitalizeFirst(errno.Error())
	}
	if pe.Path == "" {
		return fmt.Sprintf("[Errno %d] %s", int(errno), msg)
	}
	return fmt.Sprintf("[Errno %d] %s: '%s'", int(errno), msg, pe.Path)
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
