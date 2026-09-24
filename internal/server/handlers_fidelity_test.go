package server

import "testing"

// TestPyB64DecodeFidelity 锁定 Python `base64.b64decode`（validate=False）的语义。
//
// 期望值全部由项目 .venv 的 Python 3.9 实测得到（不是推断）：
//
//	python3 -c "import base64; print(base64.b64decode('!!!'))"
func TestPyB64DecodeFidelity(t *testing.T) {
	ok := []struct {
		in   string
		want string
	}{
		{"!!!", ""},           // 非字母表字符全被丢弃
		{"YWJj", "abc"},       // 标准
		{"YQ==", "a"},         // 标准填充
		{"Y W J j", "abc"},    // 空格被丢弃
		{"", ""},              // 空串
		{"====", ""},          // 全是填充
		{"YQ===", "a"},        // 多余填充被忽略
		{"YQ==extra", "a"},    // 填充之后的尾巴被忽略
		{"YWJj\n", "abc"},     // 换行被丢弃
		{"YWJj\r\n", "abc"},   // CRLF 被丢弃
		{"YWJj!", "abc"},      // 尾部非法字符被丢弃
		{"YWJj", "abc"},       // 无填充但长度整除 4
		{"aGVsbG8=", "hello"}, // 常规
	}
	for _, tc := range ok {
		got, err := pyB64Decode(tc.in)
		if err != nil {
			t.Errorf("pyB64Decode(%q) 报错: %v（期望 %q）", tc.in, err, tc.want)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("pyB64Decode(%q) = %q，期望 %q", tc.in, string(got), tc.want)
		}
	}

	// 错误分支：文案必须与 Python 的 binascii.Error 一致
	bad := []struct{ in, want string }{
		{"YQ", "Incorrect padding"},
		{"YQ=", "Incorrect padding"},
		{"a", "Invalid base64-encoded string: number of data characters (1) cannot be 1 more than a multiple of 4"},
		{"Y", "Invalid base64-encoded string: number of data characters (1) cannot be 1 more than a multiple of 4"},
		{"YWJjZ", "Invalid base64-encoded string: number of data characters (5) cannot be 1 more than a multiple of 4"},
	}
	for _, tc := range bad {
		_, err := pyB64Decode(tc.in)
		if err == nil {
			t.Errorf("pyB64Decode(%q) 期望报错，实际成功", tc.in)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf("pyB64Decode(%q) 错误文案 = %q，期望 %q", tc.in, err.Error(), tc.want)
		}
	}
}
