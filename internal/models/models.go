package models

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ToolCall 一次工具调用的完整快照。
type ToolCall struct {
	Index            int      `json:"index"`
	ToolCallID       string   `json:"tool_call_id"`
	Name             string   `json:"name"`
	Arguments        any      `json:"arguments"`
	RawResult        *string  `json:"raw_result"`
	ResultObj        any      `json:"result_obj,omitempty"`
	Body             string   `json:"body"`
	BodyFrom         string   `json:"body_from"`
	EventType        string   `json:"event_type"`
	SessionID        string   `json:"session_id"`
	RequestID        string   `json:"request_id"`
	DurationMs       *float64 `json:"duration_ms"`
	TurnPrompt       string   `json:"turn_prompt"`
	MsgAt            string   `json:"msg_at"`
	MsgDoneAt        string   `json:"msg_done_at"`
	Failed           bool     `json:"failed"`
	FailReason       string   `json:"fail_reason"`
	Truncated        bool     `json:"truncated"`
	EmptyRequiredArg []string `json:"empty_required_arg"`
}

func (tc *ToolCall) ResultLen() int {
	if tc.RawResult == nil {
		return 0
	}
	return utf8.RuneCountInString(*tc.RawResult)
}

func (tc *ToolCall) BodyLen() int {
	if tc.Body != "" {
		return utf8.RuneCountInString(tc.Body)
	}
	return tc.ResultLen()
}

func (tc *ToolCall) Signature() string {
	b, err := json.Marshal(tc.Arguments)
	if err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", tc.Arguments)
}

// ThinkingStep 思考步骤
type ThinkingStep struct {
	Index     int    `json:"index"`
	Content   string `json:"content"`
	MsgAt     string `json:"msg_at"`
	MsgDoneAt string `json:"msg_done_at"`
}

// ExecutionTrace 一个会话（一次端到端执行）的规范化轨迹。
type ExecutionTrace struct {
	SessionID             string           `json:"session_id"`
	Title                 string           `json:"title"`
	Status                string           `json:"status"`
	Mode                  string           `json:"mode"`
	CreatedAt             string           `json:"created_at"`
	UpdatedAt             string           `json:"updated_at"`
	UserPrompt            string           `json:"user_prompt"`
	FinalAnswer           string           `json:"final_answer"`
	ReasoningChars        int              `json:"reasoning_chars"`
	ToolCalls             []*ToolCall      `json:"tool_calls"`
	ThinkingSteps         []*ThinkingStep  `json:"thinking_steps"`
	MessageSpans          []map[string]any `json:"message_spans"`
	AssociatedToolCallIDs []string         `json:"associated_tool_call_ids"`
	IsStreaming           bool             `json:"is_streaming"`
	CompletedAt           string           `json:"completed_at"`
	RawMessageCount       int              `json:"raw_message_count"`
	SourceFile            string           `json:"source_file"`
	Error                 string           `json:"error"`
	UserAt                string           `json:"user_at"`
	AssistantAt           string           `json:"assistant_at"`
	FirstResponseS        float64          `json:"first_response_s"`
	GenerationS           float64          `json:"generation_s"`
	SessionS              float64          `json:"session_s"`
	TurnCount             int              `json:"turn_count"`
}

func (t *ExecutionTrace) ToolNames() []string {
	names := make([]string, len(t.ToolCalls))
	for i, tc := range t.ToolCalls {
		names[i] = tc.Name
	}
	return names
}

func (t *ExecutionTrace) OrphanIDs() []string {
	declared := make(map[string]struct{})
	for _, id := range t.AssociatedToolCallIDs {
		declared[id] = struct{}{}
	}
	actual := make(map[string]struct{})
	for _, tc := range t.ToolCalls {
		actual[tc.ToolCallID] = struct{}{}
	}

	diff := make(map[string]struct{})
	for k := range declared {
		if _, ok := actual[k]; !ok {
			diff[k] = struct{}{}
		}
	}
	for k := range actual {
		if _, ok := declared[k]; !ok {
			diff[k] = struct{}{}
		}
	}

	res := make([]string, 0, len(diff))
	for k := range diff {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

// Finding 一条命中的硬 bug。
type Finding struct {
	Rule      string `json:"rule"`
	Severity  string `json:"severity"` // P0 / P1 / P2
	SessionID string `json:"session_id"`
	Detail    string `json:"detail"`
	Tool      string `json:"tool,omitempty"`
	StepIndex *int   `json:"step_index,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
}

// CaseResult 一个测试用例的执行与断言结果。
type CaseResult struct {
	CaseID        string          `json:"case_id"`
	Name          string          `json:"name"`
	Prompt        string          `json:"prompt"`
	ExpectedTools []string        `json:"expected_tools"`
	SessionID     string          `json:"session_id"`
	Trace         *ExecutionTrace `json:"trace,omitempty"`
	Findings      []*Finding      `json:"findings"`
	UIOk          bool            `json:"ui_ok"`
	UIError       string          `json:"ui_error"`
	Attachments   []string        `json:"attachments"`
	AttachNote    string          `json:"attach_note"`
	WaitedLimit   bool            `json:"waited_limit"`
	WaitNote      string          `json:"wait_note"`
	AutoConfirms  int             `json:"auto_confirms"`
	ConfirmEvents []any           `json:"confirm_events,omitempty"`
	ElapsedS      float64         `json:"elapsed_s"`
}

func (cr *CaseResult) Passed() bool {
	return cr.UIOk && len(cr.Findings) == 0
}

func (cr *CaseResult) Status() string {
	if !cr.UIOk {
		return "UI_FAIL"
	}
	if len(cr.Findings) == 0 {
		return "PASS"
	}
	return "FAIL"
}

// ToDict 返回序列化为字典的结构（对应 Python 中的 to_dict()）
func (cr *CaseResult) ToDict() map[string]any {
	findings := make([]map[string]any, len(cr.Findings))
	for i, f := range cr.Findings {
		fm := map[string]any{
			"rule":       f.Rule,
			"severity":   f.Severity,
			"session_id": f.SessionID,
			"detail":     f.Detail,
			"tool":       f.Tool,
			"evidence":   f.Evidence,
		}
		if f.StepIndex != nil {
			fm["step_index"] = *f.StepIndex
		}
		findings[i] = fm
	}

	expectedTools := cr.ExpectedTools
	if expectedTools == nil {
		expectedTools = []string{}
	}
	attachments := cr.Attachments
	if attachments == nil {
		attachments = []string{}
	}

	return map[string]any{
		"case_id":        cr.CaseID,
		"name":           cr.Name,
		"prompt":         cr.Prompt,
		"expected_tools": expectedTools,
		"session_id":     cr.SessionID,
		"findings":       findings,
		"ui_ok":          cr.UIOk,
		"ui_error":       cr.UIError,
		"attachments":    attachments,
		"attach_note":    cr.AttachNote,
		"waited_limit":   cr.WaitedLimit,
		"wait_note":      cr.WaitNote,
		"auto_confirms":  cr.AutoConfirms,
		"elapsed_s":      cr.ElapsedS,
		"passed":         cr.Passed(),
		"status":         cr.Status(),
	}
}

// Recipe 复现配方与复现结果
type Recipe struct {
	CaseID     string        `json:"case_id"`
	Rule       string        `json:"rule"`
	Severity   string        `json:"severity"`
	Detail     string        `json:"detail"`
	Tool       string        `json:"tool"`
	TotalRuns  int           `json:"total_runs"`
	PassedRuns int           `json:"passed_runs"`
	FailedRuns int           `json:"failed_runs"`
	Rate       float64       `json:"rate"`
	Status     string        `json:"status"` // stable / likely / flaky / unverified
	Runs       []*CaseResult `json:"runs"`
}

func (r *Recipe) ToDict() map[string]any {
	runs := make([]map[string]any, len(r.Runs))
	for i, run := range r.Runs {
		runs[i] = run.ToDict()
	}
	return map[string]any{
		"case_id":     r.CaseID,
		"rule":        r.Rule,
		"severity":    r.Severity,
		"detail":      r.Detail,
		"tool":        r.Tool,
		"total_runs":  r.TotalRuns,
		"passed_runs": r.PassedRuns,
		"failed_runs": r.FailedRuns,
		"rate":        r.Rate,
		"status":      r.Status,
		"runs":        runs,
	}
}

// StringToRunes 辅助函数：安全转换为 rune 切片
func StringToRunes(s string) []rune {
	return []rune(s)
}

// RuneSubstr 切片字符串，防止越界并基于字符（而非字节）
func RuneSubstr(s string, start, length int) string {
	r := []rune(s)
	if start < 0 {
		start = 0
	}
	if start >= len(r) {
		return ""
	}
	end := start + length
	if end > len(r) {
		end = len(r)
	}
	return string(r[start:end])
}

// CleanWhitespace 压缩全部空白（含中文全角空格与不间断空格）
func CleanWhitespace(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\u3000' || r == '\u00a0' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
