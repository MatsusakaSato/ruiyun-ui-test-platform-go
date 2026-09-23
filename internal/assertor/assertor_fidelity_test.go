package assertor

import (
	"testing"

	"ruiyun-ui-test-platform-go/internal/models"
)

// ---------------------------------------------------------------------------
// 断言层保真度回归测试
//
// 全部由「219 份真实会话 / 1037 次工具调用 / 229 条 Finding」的差分得出。
// 差分命令：python3 tools/diff/run_assert_diff.py
// ---------------------------------------------------------------------------

// TestErrorMarkersAreLowercased 配置里同时存在 "[ERROR]" 与 "[error]"。
// Python 侧是 markers = [m.lower() ...]，若 Go 不小写，命中列表里会多出一项
// "[ERROR]"，导致 detail 全量不一致（真实差分：12 条 TOOL_CALL_FAILED）。
func TestErrorMarkersAreLowercased(t *testing.T) {
	cfg := map[string]any{
		"error_markers": []any{"[ERROR]", "[error]", "failed to"},
	}
	got := getErrorMarkers(cfg)
	want := []string{"[error]", "[error]", "failed to"}
	if len(got) != len(want) {
		t.Fatalf("markers 数量: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("markers[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

// TestToolFailedHitsAreDedupedAndSorted 复刻 Python 的 ', '.join(sorted(set(hits)))。
// 注意 "[" 的码位 (0x5B) 大于大写字母、小于小写字母，所以 "[error]" 排在最前。
func TestToolFailedHitsAreDedupedAndSorted(t *testing.T) {
	cfg := map[string]any{
		"error_markers": []any{"[ERROR]", "[error]", "timed out", "failed to"},
	}
	raw := "[error] request failed to complete, timed out"
	tc := &models.ToolCall{
		Index: 0, ToolCallID: "c1", Name: "mcp_fetch_webpage",
		RawResult: &raw, ResultObj: nil,
	}
	fs := CheckToolFailed(tc, &models.ExecutionTrace{SessionID: "s"}, cfg)
	if len(fs) != 1 {
		t.Fatalf("应产出 1 条 finding，得到 %d", len(fs))
	}
	want := "mcp_fetch_webpage 调用失败（命中特征：[error], failed to, timed out）"
	if fs[0].Detail != want {
		t.Errorf("detail:\n got %q\nwant %q", fs[0].Detail, want)
	}
}

// TestTotalLoopOrderIsDeterministic Python 的 Counter 遍历顺序 = 首次出现顺序。
// Go 若直接 range map，finding 顺序会随机化（真实差分：6 条 LOOP_TOTAL 顺序错乱）。
func TestTotalLoopOrderIsDeterministic(t *testing.T) {
	trace := &models.ExecutionTrace{SessionID: "s"}
	// 先大量 bash，再大量 read_file，最后少量 grep_files
	for i := 0; i < 3; i++ {
		trace.ToolCalls = append(trace.ToolCalls, &models.ToolCall{Index: i, Name: "bash"})
	}
	for i := 3; i < 6; i++ {
		trace.ToolCalls = append(trace.ToolCalls, &models.ToolCall{Index: i, Name: "read_file"})
	}
	cfg := map[string]any{"loop_total_threshold": 2}

	// 跑 20 次，顺序必须完全一致
	var first string
	for run := 0; run < 20; run++ {
		fs := CheckTotalLoop(trace, cfg)
		if len(fs) != 2 {
			t.Fatalf("应产出 2 条 finding，得到 %d", len(fs))
		}
		got := fs[0].Tool + "|" + fs[1].Tool
		if run == 0 {
			first = got
			if got != "bash|read_file" {
				t.Fatalf("顺序应为首次出现顺序 bash|read_file，得到 %q", got)
			}
			continue
		}
		if got != first {
			t.Fatalf("第 %d 次运行顺序漂移：%q != %q", run, got, first)
		}
	}
}

// TestDuplicateCallOrderIsDeterministic 同上：Python 用 seen.setdefault 的 dict，
// 顺序 = 首次出现顺序（真实差分：10 条 DUPLICATE_CALL 顺序错乱）。
func TestDuplicateCallOrderIsDeterministic(t *testing.T) {
	trace := &models.ExecutionTrace{SessionID: "s"}
	mk := func(i int, name, q string) *models.ToolCall {
		return &models.ToolCall{
			Index: i, Name: name,
			Arguments: map[string]any{"query": q},
		}
	}
	// b 先出现两次，a 后出现两次 → 顺序必须是 b 在前
	trace.ToolCalls = []*models.ToolCall{
		mk(0, "search_session", "x"), mk(1, "search_session", "x"),
		mk(2, "bash", "ls"), mk(3, "bash", "ls"),
	}
	cfg := map[string]any{}

	var first string
	for run := 0; run < 20; run++ {
		fs := CheckDuplicate(trace, cfg)
		if len(fs) != 2 {
			t.Fatalf("应产出 2 条 finding，得到 %d", len(fs))
		}
		got := fs[0].Tool + "|" + fs[1].Tool
		if run == 0 {
			first = got
			if got != "search_session|bash" {
				t.Fatalf("顺序应为首次出现顺序 search_session|bash，得到 %q", got)
			}
			continue
		}
		if got != first {
			t.Fatalf("第 %d 次运行顺序漂移：%q != %q", run, got, first)
		}
	}
}

// TestDuplicateCallReportsSecondOccurrence Python 是 step=items[1].index。
func TestDuplicateCallReportsSecondOccurrence(t *testing.T) {
	trace := &models.ExecutionTrace{SessionID: "s"}
	for _, i := range []int{5, 9, 14} {
		trace.ToolCalls = append(trace.ToolCalls, &models.ToolCall{
			Index: i, Name: "read_file", Arguments: map[string]any{"p": "a"},
		})
	}
	fs := CheckDuplicate(trace, map[string]any{})
	if len(fs) != 1 {
		t.Fatalf("应产出 1 条 finding，得到 %d", len(fs))
	}
	if fs[0].StepIndex == nil || *fs[0].StepIndex != 9 {
		t.Errorf("step_index 应为第二次出现的 9，得到 %v", fs[0].StepIndex)
	}
	if fs[0].Detail != "read_file 使用完全相同的参数重复调用 3 次" {
		t.Errorf("detail 不符: %q", fs[0].Detail)
	}
}

// TestOutputTruncatedDetailHasNoLeadingSpace
// Python 是 f"...。{len} 字）。" + " ".join(...)，单个信号时 '。' 后**没有空格**。
func TestOutputTruncatedDetailHasNoLeadingSpace(t *testing.T) {
	trace := &models.ExecutionTrace{
		SessionID:   "s",
		FinalAnswer: "这是一段尚未生成完的内容",
		IsStreaming: true,
		CompletedAt: "2026-09-14T14:23:12",
	}
	fs := CheckOutputTruncated(trace, map[string]any{})
	if len(fs) != 1 {
		t.Fatalf("应产出 1 条 finding，得到 %d", len(fs))
	}
	d := fs[0].Detail
	if len(d) == 0 || d[len(d)-1] == ' ' {
		t.Fatalf("detail 末尾不应有空格: %q", d)
	}
	if !contains(d, "字）。【流式未结束】") {
		t.Errorf("'。' 与 '【' 之间不应有空格，得到: %q", d)
	}
}

// TestPyReprMatchesPythonRepr Python 侧用的是 {tail!r}，换行会变成字面量 \n。
func TestPyReprMatchesPythonRepr(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", `'abc'`},
		{"a\nb", `'a\nb'`},
		{"a\\b", `'a\\b'`},
		{"a\tb", `'a\tb'`},
		{"中文 📋", `'中文 📋'`},    // 非 ASCII 可打印字符原样保留
		{"it's", `"it's"`},    // 含单引号但不含双引号 → 改用双引号
		{`a'b"c`, `'a\'b"c'`}, // 两者都有 → 用单引号并转义
	}
	for _, c := range cases {
		if got := pyRepr(c.in); got != c.want {
			t.Errorf("pyRepr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEmptyArgsEvidenceUsesPythonJSONFormat
// Python 是 json.dumps(args, ensure_ascii=False)：有 ", " 与 ": " 分隔符，
// 中文不转义。Go 直接 json.Marshal 会输出紧凑且转义 <>& 的形式。
func TestEmptyArgsEvidenceUsesPythonJSONFormat(t *testing.T) {
	trace := &models.ExecutionTrace{SessionID: "s"}
	trace.ToolCalls = []*models.ToolCall{{
		Index:            0,
		Name:             "write_file",
		Arguments:        map[string]any{"md_file_name": "李白.md", "markdown_content": ""},
		EmptyRequiredArg: []string{"markdown_content"},
	}}
	fs := CheckEmptyArgs(trace, map[string]any{})
	if len(fs) != 1 {
		t.Fatalf("应产出 1 条 finding，得到 %d", len(fs))
	}
	want := `{"markdown_content": "", "md_file_name": "李白.md"}`
	if fs[0].Evidence != want {
		t.Errorf("evidence:\n got %q\nwant %q", fs[0].Evidence, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
