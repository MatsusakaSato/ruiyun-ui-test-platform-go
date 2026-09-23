package pyre

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// pySpaceCodePoints 是 Python `re` 对 `\s` 的**权威**码点集合。
//
// 由 Python 3.9 实测生成（遍历 U+0000–U+10FFFF 做 re.match(r"\s", c)）：
//
//	python3 -c "import re;print([hex(c) for c in range(0x110000) if re.match(r'\s',chr(c))])"
//
// 共 29 个。Go 的 `unicode.IsSpace` 只有 25 个 —— 少的正是 U+001C–U+001F。
var pySpaceCodePoints = []rune{
	0x0009, 0x000A, 0x000B, 0x000C, 0x000D,
	0x001C, 0x001D, 0x001E, 0x001F,
	0x0020, 0x0085, 0x00A0, 0x1680,
	0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A,
	0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
}

// TestIsSpaceMatchesPython 逐码点比对 IsSpace 与 Python 的 `\s` 集合。
//
// 这是本包存在的根本理由：Go 的 `\s` 只认 5 个字符（\t \n \f \r 空格），
// Python 认 29 个，中文语料里 U+3000（全角空格）到处都是。
func TestIsSpaceMatchesPython(t *testing.T) {
	want := map[rune]bool{}
	for _, r := range pySpaceCodePoints {
		want[r] = true
	}

	// 1) 集合内的必须全为 true
	for _, r := range pySpaceCodePoints {
		if !IsSpace(r) {
			t.Errorf("IsSpace(U+%04X) = false，Python \\s 认它", r)
		}
	}

	// 2) 全码点扫描：不能多认
	extra := 0
	for r := rune(0); r < 0x110000; r++ {
		if IsSpace(r) && !want[r] {
			if extra < 10 {
				t.Errorf("IsSpace(U+%04X) = true，但 Python \\s 不认它", r)
			}
			extra++
		}
	}
	if extra > 0 {
		t.Errorf("IsSpace 多认了 %d 个码点（Python 集合只有 %d 个）", extra, len(pySpaceCodePoints))
	}
}

// TestGoRegexpWouldMissThese 是「为什么不能用 Go 的 \s」的**可执行证据**。
//
// 如果哪天 Go 的 regexp 改了 \s 语义（或有人把 pyre.SpaceClass 换回 `\s`），
// 这个测试会失败并明确告诉后来者原因。
func TestGoRegexpWouldMissThese(t *testing.T) {
	// 真实语料里实测出现过的字符（219 份会话扫描结果）：
	//   U+2003 ×64、U+3000 ×21、U+000B ×19、U+2028 ×1
	realCorpus := []rune{0x2003, 0x3000, 0x000B, 0x2028}

	goSpace := regexp.MustCompile(`\s`)
	pySpace := regexp.MustCompile(`[` + SpaceClass + `]`)

	for _, r := range realCorpus {
		s := string(r)
		if goSpace.MatchString(s) {
			t.Errorf("Go 的 \\s 竟然匹配 U+%04X —— 本包的前提变了，请复核全部调用点", r)
		}
		if !pySpace.MatchString(s) {
			t.Errorf("pyre.SpaceClass 没匹配 U+%04X，Python 会匹配它", r)
		}
	}

	// Go \s 的真实集合必须恰好是这 5 个
	goSet := []rune{'\t', '\n', '\f', '\r', ' '}
	for r := rune(0); r < 0x110000; r++ {
		got := goSpace.MatchString(string(r))
		want := false
		for _, g := range goSet {
			if r == g {
				want = true
			}
		}
		if got != want {
			t.Fatalf("Go 的 \\s 集合与预期不符：U+%04X got=%v want=%v", r, got, want)
		}
	}
	if len(goSet) != 5 {
		t.Fatal("Go \\s 集合长度应为 5")
	}
}

// TestSpaceClassAllPythonSpaces 确认 SpaceClass 正则在 29 个字符上全命中。
func TestSpaceClassAllPythonSpaces(t *testing.T) {
	re := regexp.MustCompile(`^[` + SpaceClass + `]$`)
	for _, r := range pySpaceCodePoints {
		if !re.MatchString(string(r)) {
			t.Errorf("SpaceClass 未匹配 U+%04X", r)
		}
	}
	// 反例：这些**不是** Python 的 \s，不能被匹配
	for _, r := range []rune{'a', 'A', '0', '中', '\u180e', '\u200b'} {
		if re.MatchString(string(r)) {
			t.Errorf("SpaceClass 错误匹配了 U+%04X", r)
		}
	}
}

// TestStripMatchesPython 比对 Strip/TrimLeft/TrimRight 与 Python 的
// str.strip()/lstrip()/rstrip()。
//
// 关键差异：Go 的 strings.TrimSpace 用的是 unicode.IsSpace，**不会**去掉
// U+001C–U+001F；Python 的 str.strip() 会。实测差分：要求项 "\x1c目录"
// Python 得到 "目录"、Go 得到 "\x1c目录"。
func TestStripMatchesPython(t *testing.T) {
	cases := []struct {
		in                  string
		wantStrip           string
		wantLeft, wantRight string
	}{
		{"\x1c目录\x1d", "目录", "目录\x1d", "\x1c目录"},
		{"\u3000全角\u3000", "全角", "全角\u3000", "\u3000全角"},
		{"\u00a0x\u00a0", "x", "x\u00a0", "\u00a0x"},
		{"\u2003y\u2028", "y", "y\u2028", "\u2003y"},
		{"  plain  ", "plain", "plain  ", "  plain"},
		{"\t\n\r x \v\f", "x", "x \v\f", "\t\n\r x"},
		{"no-space", "no-space", "no-space", "no-space"},
		{"", "", "", ""},
		{"\x1c\x1d\x1e\x1f", "", "", ""},
	}
	for _, c := range cases {
		if got := Strip(c.in); got != c.wantStrip {
			t.Errorf("Strip(%q) = %q，Python str.strip() 给 %q", c.in, got, c.wantStrip)
		}
		if got := TrimLeft(c.in); got != c.wantLeft {
			t.Errorf("TrimLeft(%q) = %q，Python str.lstrip() 给 %q", c.in, got, c.wantLeft)
		}
		if got := TrimRight(c.in); got != c.wantRight {
			t.Errorf("TrimRight(%q) = %q，Python str.rstrip() 给 %q", c.in, got, c.wantRight)
		}
	}
}

// TestStripDiffersFromStringsTrimSpace 明确记录 Strip 与 TrimSpace 的差别，
// 防止有人「顺手」把 pyre.Strip 改回 strings.TrimSpace。
func TestStripDiffersFromStringsTrimSpace(t *testing.T) {
	in := "\x1c目录\x1f"
	if got := strings.TrimSpace(in); got != in {
		t.Fatalf("strings.TrimSpace 竟然去掉了 U+001C/U+001F，本包前提变了：%q", got)
	}
	if got := Strip(in); got != "目录" {
		t.Fatalf("Strip(%q) = %q，want 目录", in, got)
	}
}

// TestSpaceClassIsValidUTF8NoBOM 顺带确认 SpaceClass 常量本身没写坏。
func TestSpaceClassIsValidUTF8NoBOM(t *testing.T) {
	if !utf8.ValidString(SpaceClass) {
		t.Fatal("SpaceClass 不是合法 UTF-8")
	}
	if _, err := regexp.Compile(`[` + SpaceClass + `]`); err != nil {
		t.Fatalf("SpaceClass 无法编译进字符类: %v", err)
	}
}

// TestCleanSpaceMatchesPython 比对 CleanSpace 与 `re.sub(r"\s+", "", s)`。
//
// 这条替换掉了一个真实缺陷：`models.CleanWhitespace` 原本手写 6 个字符表，
// 漏掉 Python 29 个里的 23 个；而 `evaluator.normPrompt` 用 Go 的 `\s`（5 个）。
// 两者一个建索引、一个查索引 —— 键对不上，预设标签就静默查不到。
func TestCleanSpaceMatchesPython(t *testing.T) {
	// 每一个 Python `\s` 字符都必须被删除
	for _, r := range pySpaceCodePoints {
		if got := CleanSpace("a" + string(r) + "b"); got != "ab" {
			t.Errorf("CleanSpace 未删除 U+%04X：got %q", r, got)
		}
	}
	// 非空白必须保留
	for _, r := range []rune{'a', 'Z', '0', '中', '😊', '\u200b', '\u180e'} {
		in := "x" + string(r) + "y"
		if got := CleanSpace(in); got != in {
			t.Errorf("CleanSpace 误删 U+%04X：got %q want %q", r, got, in)
		}
	}
	// 连续与首尾
	if got := CleanSpace("\u3000 a\u2003\u2003b \u2028"); got != "ab" {
		t.Errorf("CleanSpace 连续/首尾未清干净：got %q want %q", got, "ab")
	}
	if got := CleanSpace(""); got != "" {
		t.Errorf("CleanSpace(\"\") = %q", got)
	}
}

// TestContainsSpaceMatchesPython 比对 `re.search(r"\s", s)`。
func TestContainsSpaceMatchesPython(t *testing.T) {
	for _, r := range pySpaceCodePoints {
		if !ContainsSpace("a" + string(r) + "b") {
			t.Errorf("ContainsSpace 漏掉 U+%04X", r)
		}
	}
	for _, s := range []string{"", "abc", "sk-abc123", "中文", "a\u200bb"} {
		if ContainsSpace(s) {
			t.Errorf("ContainsSpace(%q) = true，Python re.search 给 false", s)
		}
	}
	// 真实场景：把「模型名」粘进 Key
	if !ContainsSpace("sk-abc gpt-4o") {
		t.Error("含空格的 API Key 必须被检出（Python 会判 preflight 失败）")
	}
}
