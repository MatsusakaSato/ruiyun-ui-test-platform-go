package pyre

import (
	"os"
	"syscall"
	"testing"
)

// TestOSErrorFidelity 锁定 Python `OSError.__str__` 的文案。
//
// 参照实测（项目 .venv 的 Python 3.9）：
//
//	>>> try: os.mkdir("/System/x")
//	... except OSError as e: print(e)
//	[Errno 1] Operation not permitted: '/System/x'
func TestOSErrorFidelity(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&os.PathError{Op: "mkdir", Path: "/a/b", Err: syscall.EPERM},
			"[Errno 1] Operation not permitted: '/a/b'"},
		{&os.PathError{Op: "open", Path: "/a/c", Err: syscall.ENOENT},
			"[Errno 2] No such file or directory: '/a/c'"},
		{&os.PathError{Op: "open", Path: "/a/d", Err: syscall.EACCES},
			"[Errno 13] Permission denied: '/a/d'"},
		{&os.PathError{Op: "mkdir", Path: "/a/e", Err: syscall.EEXIST},
			"[Errno 17] File exists: '/a/e'"},
		{&os.PathError{Op: "mkdir", Path: "/a/f", Err: syscall.ENOSPC},
			"[Errno 28] No space left on device: '/a/f'"},
		// 未收录的 errno 走首字母大写兜底
		{&os.PathError{Op: "write", Path: "/a/g", Err: syscall.EPIPE},
			"[Errno 32] Broken pipe: '/a/g'"},
		// 非 PathError 原样返回，不伪造 Python 形状
		{os.ErrNotExist, "file does not exist"},
	}
	for _, tc := range cases {
		if got := OSError(tc.err); got != tc.want {
			t.Errorf("OSError(%v) = %q，期望 %q", tc.err, got, tc.want)
		}
	}
}
