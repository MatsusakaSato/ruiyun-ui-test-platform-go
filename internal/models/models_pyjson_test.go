package models

import "testing"

// TestWritePyJSONStringMatchesPython 逐条比对字符串转义与
// Python `json.dumps(s, ensure_ascii=False)`。
//
// 背景（实测发现，会造成安全扫描漏报）：
// Go 的 `json.Encoder` + `SetEscapeHTML(false)` 与 Python 有**三处**不一致：
//
//  1. U+2028 / U+2029 被转义成 `\u2028` / `\u2029`（Go 为 JSONP 安全如此设计），
//     Python 原样输出。真实语料里存在这两个字符，
//     转义后 `rm\u2028-rf\u2028/` 不再匹配 `rm\s+-rf\s+/`
//     → **破坏性命令命中丢失、has_redline 误判为 False**。
//  2. `\b`(0x08) 被写成 `\u0008`，Python 写 `\b`。
//  3. `\f`(0x0c) 被写成 `\u000c`，Python 写 `\f`。
func TestWritePyJSONStringMatchesPython(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空串", "", `""`},
		{"普通中文", "李白", `"李白"`},
		{"双引号", `a"b`, `"a\"b"`},
		{"反斜杠", `a\b`, `"a\\b"`},
		{"换行", "a\nb", `"a\nb"`},
		{"回车", "a\rb", `"a\rb"`},
		{"制表", "a\tb", `"a\tb"`},
		// 2) 退格必须是短转义 \b，不是 \u0008
		{"退格", "a\bb", `"a\bb"`},
		// 3) 换页必须是短转义 \f，不是 \u000c
		{"换页", "a\fb", `"a\fb"`},
		// 其余控制字符用 \u00xx（小写十六进制）
		{"NUL", "a\x00b", `"a\u0000b"`},
		{"BEL", "a\x07b", `"a\u0007b"`},
		{"VT", "a\x0bb", `"a\u000bb"`},
		{"ESC", "a\x1bb", `"a\u001bb"`},
		{"文件分隔", "a\x1cb", `"a\u001cb"`},
		// 1) 🔴 U+2028 / U+2029 必须原样输出（不能转义）
		{"行分隔U2028", "a\u2028b", "\"a\u2028b\""},
		{"段分隔U2029", "a\u2029b", "\"a\u2029b\""},
		// 这些 Python 也都不转义
		{"小于号", "a<b", `"a<b"`},
		{"大于号", "a>b", `"a>b"`},
		{"和号", "a&b", `"a&b"`},
		{"斜杠", "a/b", `"a/b"`},
		{"全角空格", "a\u3000b", "\"a\u3000b\""},
		{"NBSP", "a\u00a0b", "\"a\u00a0b\""},
		{"EMOJI", "a😊b", `"a😊b"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PyJSONDumps(c.in)
			if got != c.want {
				t.Errorf("PyJSONDumps(%q)\n  got  = %s\n  want = %s", c.in, got, c.want)
			}
		})
	}
}

// TestPyJSONU2028NotEscaped 是最关键的一条：单独钉住 U+2028。
//
// 它直接对应真实差分：B-0004 用例里 Go `has_redline=False`、Python `True`。
func TestPyJSONU2028NotEscaped(t *testing.T) {
	// 复刻真实场景：工具实参 {"cmd": "rm\u2028-rf\u2028/"}
	got := PyJSONDumpsSourceOrder(map[string]any{"cmd": "rm\u2028-rf\u2028/"})
	want := "{\"cmd\": \"rm\u2028-rf\u2028/\"}"
	if got != want {
		t.Fatalf("PyJSONDumpsSourceOrder 转义了 U+2028：\n  got  = %q\n  want = %q", got, want)
	}
	for _, bad := range []string{`\u2028`, `\u2029`} {
		if contains(got, bad) {
			t.Errorf("输出里出现了转义序列 %s —— 会让 rm\\s+-rf 匹配不上", bad)
		}
	}
}

// TestPyJSONSeparatorsAndOrdering 覆盖分隔符与键序。
//
// 注意：Go 的 map 无键序，所以即使 SourceOrder 变体也**只能**输出排序键。
// 这不是缺陷，而是已知限制（见 AGENTS.md §3.9b）——
// 本测试把该行为**显式固化**，避免有人误以为它是源文件键序。
func TestPyJSONSeparatorsAndOrdering(t *testing.T) {
	// 分隔符必须是 ", " / ": "（不是 json.Marshal 的紧凑 "," / ":"）
	got := PyJSONDumpsSourceOrder(map[string]any{"a": float64(1), "b": "x"})
	want := `{"a": 1, "b": "x"}`
	if got != want {
		t.Errorf("分隔符不对：got %q want %q", got, want)
	}

	// 切片：顺序必须保留，且元素间是 ", "
	gotSlice := PyJSONDumpsSourceOrder([]any{float64(3), "b", true, nil})
	wantSlice := `[3, "b", true, null]`
	if gotSlice != wantSlice {
		t.Errorf("切片：got %q want %q", gotSlice, wantSlice)
	}

	// 嵌套
	gotNest := PyJSONDumpsSourceOrder(map[string]any{
		"tasks": []any{"甲", "乙"},
		"n":     float64(2),
	})
	wantNest := `{"n": 2, "tasks": ["甲", "乙"]}`
	if gotNest != wantNest {
		t.Errorf("嵌套：got %q want %q", gotNest, wantNest)
	}
}

// TestPyJSONIntegralFloatForm 覆盖 int/float 形态还原（§3.4 的既有行为，防回退）。
func TestPyJSONIntegralFloatForm(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{float64(100), "100"}, // 整数值 float → 整数形态（Python json.loads 得到 int）
		{float64(1.5), "1.5"}, // 真小数保持
		{float64(-3), "-3"},   // 负数
		{int(7), "7"},         // 原生 int
		{int64(9), "9"},       // int64
		{nil, "null"},
		{true, "true"},
		{false, "false"},
	}
	for _, c := range cases {
		if got := PyJSONDumps(c.in); got != c.want {
			t.Errorf("PyJSONDumps(%v) = %q，want %q", c.in, got, c.want)
		}
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
