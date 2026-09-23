package models

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/pyre"
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

// Signature 参数指纹，用于重复调用检测与字符/token 估算。
//
// 必须逐字节复刻 Python 的 `json.dumps(self.arguments, ensure_ascii=False,
// sort_keys=True)`（默认分隔符 ", " 与 ": "）。原因不是洁癖：trajectory 会取它的
// **长度**参与 tool_arg_chars 与 token 估算（core/trajectory.py:68,86），
// 格式不同会直接污染指标。
//
// Go 的 json.Marshal 有三处差异：紧凑分隔符（无空格）、HTML 转义（< > &）、
// 以及 JSON 整数被统一解码成 float64 后会写成 "8" 还是 "8.0"。
func (tc *ToolCall) Signature() (out string) {
	defer func() {
		// 与 Python `except Exception: return str(self.arguments)` 对齐
		if recover() != nil {
			out = fmt.Sprintf("%v", tc.Arguments)
		}
	}()
	return PyJSONDumps(tc.Arguments)
}

// PyJSONDumps 复刻 Python 的 json.dumps(v, ensure_ascii=False, sort_keys=True)。
// PyJSONDumps 复刻 `json.dumps(v, ensure_ascii=False, sort_keys=True)`。
//
// ⚠️ 支持的类型与 Python 的 `json.dumps` **一致**，只有：
// nil / bool / string / json.Number / float64 / int / int64 / []any / []string /
// map[string]any。传入其它类型（尤其是**结构体**）会落到 default 分支，
// 用 `fmt.Sprintf("%v", x)` 输出 —— 那既不是 JSON 也不是 Python 的 `str()`，
// 属于**静默降级**。Python 那边对不可序列化对象会抛 TypeError
// （`flatten_arguments` 捕获后回落 `str(arguments)`，而 `str(obj)` 含内存地址、本身不确定）。
//
// 结论：**只传从 JSON 解出来的 map/切片/标量**。当前 4 个生产调用点都是如此。
func PyJSONDumps(v any) string {
	var sb strings.Builder
	writePyJSON(&sb, v)
	return sb.String()
}

// PyJSONDumpsSourceOrder 复刻 `json.dumps(v, ensure_ascii=False)`
// —— **不带 `sort_keys`** 的那个变体，分隔符同样是 `", "` / `": "`。
//
// 与 PyJSONDumps 的唯一差别是**不排序**。但 Go 的 `map[string]any` 本身
// 不保留键序，所以 map 分支实际仍会排序 —— 见 writePyJSONOpt 的说明。
// 对 **切片** 与标量，两者完全一致。
//
// 用在 core/safety_scan.flatten_arguments（安全扫描的文本源）等处。
func PyJSONDumpsSourceOrder(v any) string {
	var sb strings.Builder
	writePyJSONOpt(&sb, v, false)
	return sb.String()
}

// writePyJSONString 复刻 Python `json.dumps(s, ensure_ascii=False)` 的**字符串转义**。
//
// ⚠️ 不能用 `json.Encoder` + `SetEscapeHTML(false)` —— 它和 Python 有三处不一致，
// 其中第一处会造成**安全扫描漏报**（实测）：
//
//  1. Go 会把 U+2028/U+2029 转义成 `\u2028`/`\u2029`（为了 JSONP 安全），
//     Python 原样输出。这两个字符在真实语料里存在；
//     一旦被转义，`rm\u2028-rf\u2028/` 就不再匹配 `rm\s+-rf\s+/`，
//     **破坏性命令的工具实参命中直接丢失**（Go has_redline=False / Python=True）。
//  2. Go 把 \b 写成 `\u0008`，Python 写 `\b`。
//  3. Go 把 \f 写成 `\u000c`，Python 写 `\f`。
//
// Python 的规则（`json.encoder.py_encode_basestring` + ESCAPE_DCT）：
//
//	" \  → \" \\
//	\b \f \n \r \t → 对应短转义
//	其余 < 0x20      → \u00xx（小写十六进制）
//	其它一律**原样输出**（ensure_ascii=False）：< > & / U+2028 U+2029 非 ASCII 都不转义
func writePyJSONString(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				sb.WriteString(`\u00`)
				const hex = "0123456789abcdef"
				sb.WriteByte(hex[(r>>4)&0xF])
				sb.WriteByte(hex[r&0xF])
				continue
			}
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
}

func writePyJSON(sb *strings.Builder, v any) {
	writePyJSONOpt(sb, v, true)
}

// writePyJSONOpt 是 PyJSON 系列的唯一实现。
//
// sortKeys=false 时**仍然对 map 排序** —— 因为 Go 的 map 迭代顺序是随机的，
// 不排就完全不可复现。Python 那边保留的是 JSON **源文件**的键序，
// 要真正对齐必须在**解码侧**就保留键序（会波及所有 map[string]any 断言），
// 风险大于收益，故此处选择「确定但可能与 Python 键序不同」，
// 并由差分工装用「键排序后的规范形」交叉校验来证明差异**仅**在键序。
func writePyJSONOpt(sb *strings.Builder, v any, sortKeys bool) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if x {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case string:
		writePyJSONString(sb, x)
	case json.Number:
		sb.WriteString(x.String())
	case float64:
		// Python 只对 json.loads 得到的 int 输出不带小数点的形态；Go 侧统一解码为
		// float64，故把「整数值」还原成整数形态。
		// 真实语料实测：3626 个 int 字面量、52 个非整数 float、整数值 float 0 个，
		// 因此该还原在本语料上完全精确。
		if x == math.Trunc(x) && !math.IsInf(x, 0) && math.Abs(x) < (1<<53) {
			sb.WriteString(strconv.FormatInt(int64(x), 10))
		} else {
			sb.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
		}
	case int:
		sb.WriteString(strconv.Itoa(x))
	case int64:
		sb.WriteString(strconv.FormatInt(x, 10))
	case []any:
		sb.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				sb.WriteString(", ")
			}
			writePyJSONOpt(sb, e, sortKeys)
		}
		sb.WriteByte(']')
	case []string:
		sb.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				sb.WriteString(", ")
			}
			writePyJSONString(sb, e)
		}
		sb.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		// 注意：即使 sortKeys=false 这里也排了 —— Go map 无键序，
		// 不排会导致输出**每次运行都不同**。见 writePyJSONOpt 的说明。
		sort.Strings(keys) // UTF-8 字节序 == 码点序，与 Python 的排序一致
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			writePyJSONString(sb, k)
			sb.WriteString(": ")
			writePyJSONOpt(sb, x[k], sortKeys)
		}
		sb.WriteByte('}')
	default:
		sb.WriteString(fmt.Sprintf("%v", x))
	}
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

// CleanWhitespace 删除全部空白（等价 Python `re.sub(r"\s+", "", s)`）。
//
// ⚠️ 原实现是**手写的 6 字符表**（空格 \t \n \r U+3000 U+00A0），
// 漏掉 Python `\s` 29 个字符里的 23 个 —— 包括真实语料里出现过的
// U+000B(×19)、U+2003(×64)、U+2028(×1)。更糟的是它与
// `evaluator.normPrompt`（用 Go 的 `\s`，只认 5 个字符）**互相不一致**，
// 而两者分别负责「建索引」与「查索引」—— 键对不上，预设标签就查不到。
//
// 现已委托给 pyre.CleanSpace，保证全项目只有一套空白语义。
func CleanWhitespace(s string) string {
	return pyre.CleanSpace(s)
}
