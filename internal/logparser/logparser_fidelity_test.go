package logparser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/models"
)

// 本文件锁住 P0 差分验证（219 份真实会话 vs Python 原版）中发现并修复的
// logparser 缺陷。断言的是 Python 原版 core/log_parser.py 的实际行为。

// writeSession 构造一个最小但结构真实的会话目录。
func writeSession(t *testing.T, meta map[string]any, messages []map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "session.meta.json"), mb, 0o644); err != nil {
		t.Fatalf("写 meta 失败: %v", err)
	}
	pb, _ := json.Marshal(map[string]any{"messages": messages})
	if err := os.WriteFile(filepath.Join(dir, "session.messages.json"), pb, 0o644); err != nil {
		t.Fatalf("写 messages 失败: %v", err)
	}
	return dir
}

func baseMeta() map[string]any {
	return map[string]any{
		"session_id": "sess_fixture_0001",
		"title":      "测试会话",
		"status":     "active",
		"mode":       "plan",
		"created_at": "2026-09-14T14:23:12.166515",
		"updated_at": "2026-09-14T14:26:14.789526",
	}
}

// TestMetaKeysAreSnakeCase 锁住 meta.json 的键名。
//
// session.meta.json 用的是 snake_case（session_id / created_at / updated_at）。
// 早期实现读的是 id / createdAt / updatedAt，而取值又走
// fmt.Sprintf("%v", nil)，于是 219/219 个会话的这三个字段全部变成字面量
// 字符串 "<nil>" —— session_id 是 UI 主键，属 P0。
func TestMetaKeysAreSnakeCase(t *testing.T) {
	dir := writeSession(t, baseMeta(), []map[string]any{
		{"role": "user", "content": "你好", "timestamp": "2026-09-14T14:23:12.166515"},
	})
	tr, err := ParseSession(dir)
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	if tr.SessionID != "sess_fixture_0001" {
		t.Errorf("SessionID=%q，期望 sess_fixture_0001（读错键会得到 \"<nil>\"）", tr.SessionID)
	}
	if tr.CreatedAt != "2026-09-14T14:23:12.166515" {
		t.Errorf("CreatedAt=%q", tr.CreatedAt)
	}
	if tr.UpdatedAt != "2026-09-14T14:26:14.789526" {
		t.Errorf("UpdatedAt=%q", tr.UpdatedAt)
	}
	for _, v := range []string{tr.SessionID, tr.CreatedAt, tr.UpdatedAt} {
		if v == "<nil>" {
			t.Fatalf("出现字面量 \"<nil>\"：又用 Sprintf 取了缺失的键")
		}
	}
}

// TestSessionIDFallsBackToDirName 锁住 Python 的 `meta.get("session_id") or session_dir.name`。
func TestSessionIDFallsBackToDirName(t *testing.T) {
	meta := baseMeta()
	delete(meta, "session_id")
	dir := writeSession(t, meta, []map[string]any{
		{"role": "user", "content": "hi", "timestamp": "2026-09-14T14:23:12.166515"},
	})
	tr, err := ParseSession(dir)
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	if tr.SessionID != filepath.Base(dir) {
		t.Errorf("SessionID=%q，期望目录名 %q", tr.SessionID, filepath.Base(dir))
	}
}

// TestSourceFileIsSessionDir 锁住 source_file 的语义（Python 存目录，不是文件）。
func TestSourceFileIsSessionDir(t *testing.T) {
	dir := writeSession(t, baseMeta(), []map[string]any{
		{"role": "user", "content": "hi", "timestamp": "2026-09-14T14:23:12.166515"},
	})
	tr, _ := ParseSession(dir)
	if tr.SourceFile != dir {
		t.Errorf("SourceFile=%q，期望会话目录 %q", tr.SourceFile, dir)
	}
}

// TestStepIndexStartsAtZeroAndCarriesAcrossMessages 锁住步骤序号。
//
// Python 是「先取当前 step_index，循环末尾再 +1」，首个步骤 index=0，
// 且序号跨 assistant 消息连续累加。早期实现先 ++ 再取，导致全部 index 偏移 +1
// （真实差分 1037/1037 处不一致）。
func TestStepIndexStartsAtZeroAndCarriesAcrossMessages(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "第一个问题", "timestamp": "2026-09-14T14:23:12.166515"},
		{
			"role": "assistant", "timestamp": "2026-09-14T14:23:14.586857",
			"completedAt": "2026-09-14T14:23:20.000000",
			"timelineSteps": []map[string]any{
				{"type": "thinking", "content": "想想"},
				{"type": "tool_call", "name": "mcp_paid_search", "toolCallId": "call_1",
					"arguments": map[string]any{"query": "a"},
					"result":    map[string]any{"event_type": "chat.tool_result", "session_id": "sess_fixture_0001"}},
			},
		},
		{
			"role": "assistant", "timestamp": "2026-09-14T14:25:00.000000",
			"completedAt": "2026-09-14T14:25:10.000000",
			"timelineSteps": []map[string]any{
				{"type": "tool_call", "name": "todo_complete", "toolCallId": "call_2",
					"arguments": map[string]any{"idx": 1},
					"result":    map[string]any{"event_type": "chat.tool_result"}},
			},
		},
	}
	tr, err := ParseSession(writeSession(t, baseMeta(), msgs))
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	if len(tr.ThinkingSteps) != 1 || tr.ThinkingSteps[0].Index != 0 {
		t.Errorf("首个 thinking 步骤 index 应为 0，实得 %+v", tr.ThinkingSteps)
	}
	if len(tr.ToolCalls) != 2 {
		t.Fatalf("工具调用数=%d，期望 2", len(tr.ToolCalls))
	}
	if tr.ToolCalls[0].Index != 1 {
		t.Errorf("首个 tool_call index=%d，期望 1", tr.ToolCalls[0].Index)
	}
	if tr.ToolCalls[1].Index != 2 {
		t.Errorf("第二个 tool_call index=%d，期望 2（跨消息连续累加）", tr.ToolCalls[1].Index)
	}
}

// TestToolCallMetaFromResultShell 锁住 event_type / session_id / request_id 的提取。
//
// Python: meta_res = raw_res if isinstance(raw_res, dict) else {}，
// 然后 event_type=meta_res.get("event_type","")、
// session_id=meta_res.get("session_id","") or trace.session_id、
// request_id=meta_res.get("request_id","") or msg.get("request_id","")。
func TestToolCallMetaFromResultShell(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "问题", "timestamp": "2026-09-14T14:23:12.166515"},
		{
			"role": "assistant", "timestamp": "2026-09-14T14:23:14.000000",
			"completedAt": "2026-09-14T14:23:20.000000",
			"request_id":  "msg_fallback_req",
			"timelineSteps": []map[string]any{
				{"type": "tool_call", "name": "mcp_fetch_webpage", "toolCallId": "call_a",
					"arguments": map[string]any{"url": "https://x"},
					"result": map[string]any{
						"event_type": "chat.tool_result",
						"session_id": "sess_from_shell",
						"request_id": "msg_from_shell",
					}},
				{"type": "tool_call", "name": "todo_complete", "toolCallId": "call_b",
					"arguments": map[string]any{"idx": 2},
					"result":    map[string]any{"result": "ok"}},
			},
		},
	}
	tr, err := ParseSession(writeSession(t, baseMeta(), msgs))
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	if len(tr.ToolCalls) != 2 {
		t.Fatalf("工具调用数=%d", len(tr.ToolCalls))
	}
	c0, c1 := tr.ToolCalls[0], tr.ToolCalls[1]
	if c0.EventType != "chat.tool_result" {
		t.Errorf("EventType=%q，期望 chat.tool_result", c0.EventType)
	}
	if c0.SessionID != "sess_from_shell" {
		t.Errorf("SessionID=%q，期望取外壳里的 sess_from_shell", c0.SessionID)
	}
	if c0.RequestID != "msg_from_shell" {
		t.Errorf("RequestID=%q，期望取外壳里的 msg_from_shell", c0.RequestID)
	}
	// 外壳里没有 session_id / request_id 时必须回退
	if c1.SessionID != "sess_fixture_0001" {
		t.Errorf("回退 SessionID=%q，期望 trace.SessionID", c1.SessionID)
	}
	if c1.RequestID != "msg_fallback_req" {
		t.Errorf("回退 RequestID=%q，期望 msg_fallback_req", c1.RequestID)
	}
}

// TestTurnPromptFollowsMultiTurn 锁住多轮会话的触发源。
//
// Python 的 current_turn_prompt 在每条 user 消息上刷新；每次工具调用记录的是
// **触发它的那一轮**提问，而不是会话首条提问。
func TestTurnPromptFollowsMultiTurn(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "第一轮问题", "timestamp": "2026-09-14T14:23:12.166515"},
		{"role": "assistant", "timestamp": "2026-09-14T14:23:14.000000",
			"completedAt": "2026-09-14T14:23:20.000000",
			"timelineSteps": []map[string]any{
				{"type": "tool_call", "name": "t1", "toolCallId": "c1",
					"arguments": map[string]any{}, "result": map[string]any{}},
			}},
		{"role": "user", "content": "第二轮问题", "timestamp": "2026-09-14T14:24:00.000000"},
		{"role": "assistant", "timestamp": "2026-09-14T14:24:02.000000",
			"completedAt": "2026-09-14T14:24:10.000000",
			"timelineSteps": []map[string]any{
				{"type": "tool_call", "name": "t2", "toolCallId": "c2",
					"arguments": map[string]any{}, "result": map[string]any{}},
			}},
	}
	tr, err := ParseSession(writeSession(t, baseMeta(), msgs))
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	if tr.TurnCount != 2 {
		t.Errorf("TurnCount=%d，期望 2", tr.TurnCount)
	}
	if tr.UserPrompt != "第一轮问题" {
		t.Errorf("UserPrompt=%q，期望首条提问", tr.UserPrompt)
	}
	if tr.ToolCalls[0].TurnPrompt != "第一轮问题" {
		t.Errorf("第 1 次调用 TurnPrompt=%q，期望「第一轮问题」", tr.ToolCalls[0].TurnPrompt)
	}
	if tr.ToolCalls[1].TurnPrompt != "第二轮问题" {
		t.Errorf("第 2 次调用 TurnPrompt=%q，期望「第二轮问题」", tr.ToolCalls[1].TurnPrompt)
	}
}

// TestDurationsRoundedTo2Decimals 锁住派生耗时的 2 位小数取整，
// 以及 generation_s 的 user->done 兜底分支（Python 的 elif）。
func TestDurationsRoundedTo2Decimals(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "q", "timestamp": "2026-09-14T14:23:12.166515"},
		{"role": "assistant", "timestamp": "2026-09-14T14:23:14.586857",
			"completedAt": "2026-09-14T14:26:14.789526", "content": "a"},
	}
	tr, err := ParseSession(writeSession(t, baseMeta(), msgs))
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	// (14:23:14.586857 - 14:23:12.166515) = 2.420342 -> 2.42
	if tr.FirstResponseS != 2.42 {
		t.Errorf("FirstResponseS=%v，期望 2.42（须 round 到 2 位）", tr.FirstResponseS)
	}
	// (14:26:14.789526 - 14:23:14.586857) = 180.202669 -> 180.2
	if tr.GenerationS != 180.2 {
		t.Errorf("GenerationS=%v，期望 180.2", tr.GenerationS)
	}
	// (14:26:14.789526 - 14:23:12.166515) = 182.623011 -> 182.62
	if tr.SessionS != 182.62 {
		t.Errorf("SessionS=%v，期望 182.62", tr.SessionS)
	}
}

// TestMessageSpansSkipStepLessMessages 锁住区间记录条件。
//
// Python 只在 span_last >= span_first 时记录；没有任何步骤的 assistant 消息
// 不产生区间。早期实现无条件 append，导致 17 个真实会话的区间数不一致。
func TestMessageSpansSkipStepLessMessages(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "q", "timestamp": "2026-09-14T14:23:12.166515"},
		{"role": "assistant", "timestamp": "2026-09-14T14:23:14.000000",
			"completedAt": "2026-09-14T14:23:20.000000",
			"timelineSteps": []map[string]any{
				{"type": "tool_call", "name": "t1", "toolCallId": "c1",
					"arguments": map[string]any{}, "result": map[string]any{}},
			}},
		// 这条没有任何步骤 —— 不应产生 message_span
		{"role": "assistant", "timestamp": "2026-09-14T14:24:00.000000",
			"completedAt": "2026-09-14T14:24:01.000000", "content": "空跑"},
	}
	tr, err := ParseSession(writeSession(t, baseMeta(), msgs))
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	if len(tr.MessageSpans) != 1 {
		t.Fatalf("MessageSpans=%d 条，期望 1（无步骤的消息不应记录）", len(tr.MessageSpans))
	}
	span := tr.MessageSpans[0]
	if span["first_step"] != 0 || span["last_step"] != 0 {
		t.Errorf("区间=[%v,%v]，期望 [0,0]", span["first_step"], span["last_step"])
	}
}

// TestReasoningCharsUsesReasoningContent 锁住 reasoning_chars 的来源。
//
// Python 累加的是整条消息的 reasoningContent，而不是 thinking 步骤内容之和。
// 二者在现有语料里恰好相等，但来源不同。
func TestReasoningCharsUsesReasoningContent(t *testing.T) {
	msgs := []map[string]any{
		{"role": "user", "content": "q", "timestamp": "2026-09-14T14:23:12.166515"},
		{"role": "assistant", "timestamp": "2026-09-14T14:23:14.000000",
			"completedAt":      "2026-09-14T14:23:20.000000",
			"reasoningContent": "一二三四五", // 5 个码点
			"timelineSteps": []map[string]any{
				{"type": "thinking", "content": "短"}, // 步骤内容只有 1 个字
			}},
	}
	tr, err := ParseSession(writeSession(t, baseMeta(), msgs))
	if err != nil {
		t.Fatalf("ParseSession 失败: %v", err)
	}
	if tr.ReasoningChars != 5 {
		t.Errorf("ReasoningChars=%d，期望 5（应取 reasoningContent 而非 thinking 步骤内容）", tr.ReasoningChars)
	}
}

// TestEmptyRequiredArgIsEmptySliceNotNil 锁住空切片语义（JSON null vs []）。
func TestEmptyRequiredArgIsEmptySliceNotNil(t *testing.T) {
	got := DetectEmptyRequiredArgs("mcp_paid_search", map[string]any{"query": "有值"})
	if got == nil {
		t.Fatal("返回 nil，会被序列化成 null；Python 原版是 []")
	}
	if len(got) != 0 {
		t.Errorf("期望空切片，实得 %v", got)
	}
	b, _ := json.Marshal(got)
	if string(b) != "[]" {
		t.Errorf("JSON 序列化=%s，期望 []", b)
	}
	// 命中时仍要报出来
	hit := DetectEmptyRequiredArgs("mcp_paid_search", map[string]any{"query": "   "})
	if len(hit) != 1 || hit[0] != "query" {
		t.Errorf("空 query 应命中，实得 %v", hit)
	}
}

// TestSignatureMatchesPythonJSONDumps 锁住签名指纹的格式。
//
// signature 的长度会参与 tool_arg_chars 与 token 估算，因此格式必须与
// Python 的 json.dumps(..., ensure_ascii=False, sort_keys=True) 一致：
// 冒号与逗号后带空格、键有序、不转义 HTML、整数不带小数点。
func TestSignatureMatchesPythonJSONDumps(t *testing.T) {
	cases := []struct {
		name string
		args any
		want string
	}{
		{"空对象", map[string]any{}, "{}"},
		{"键序与空格", map[string]any{"query": "李白", "max_results": 8},
			`{"max_results": 8, "query": "李白"}`},
		{"整数不带小数点", map[string]any{"idx": 1}, `{"idx": 1}`},
		{"嵌套", map[string]any{"a": []any{1.0, "x"}}, `{"a": [1, "x"]}`},
		{"不转义 HTML", map[string]any{"s": "a<b>&c"}, `{"s": "a<b>&c"}`},
		{"非整数浮点", map[string]any{"t": 0.5}, `{"t": 0.5}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc2 := &models.ToolCall{Arguments: tc.args}
			if got := tc2.Signature(); got != tc.want {
				t.Errorf("Signature()=%s，期望 %s", got, tc.want)
			}
		})
	}
}

// TestSignatureLengthFeedsMetrics 确认签名长度是稳定且可预期的，
// 因为它会直接进入 tool_arg_chars / token 估算。
func TestSignatureLengthFeedsMetrics(t *testing.T) {
	c := &models.ToolCall{Arguments: map[string]any{"query": "李白 生平", "max_results": 8}}
	// Python 的 len(str) 数的是码点，不是字节 —— 必须用 RuneCount
	want := utf8.RuneCountInString(`{"max_results": 8, "query": "李白 生平"}`)
	if got := utf8.RuneCountInString(c.Signature()); got != want {
		t.Errorf("signature 长度=%d，期望 %d", got, want)
	}
}
