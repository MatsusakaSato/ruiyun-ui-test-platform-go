package trajectory

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/artifacts"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/pyre"
)

var RuleImpact = map[string]string{
	"TOOL_CALL_FAILED":      "该步骤未获得有效数据，agent 可能基于缺失信息继续作答或转向低质量替代路径",
	"TOOL_RESULT_MISSING":   "工具无返回，agent 失去该环节依据，任务链路出现空洞",
	"ORPHAN_TOOL_CALL":      "调用链路声明与实际执行不一致，问题排查与审计无法对账",
	"LOOP_CONSECUTIVE":      "重复无效调用，浪费配额与时间，可能持续到触达迭代上限",
	"LOOP_TOTAL":            "单工具高频调用，资源消耗异常，通常是策略失控的前兆",
	"MAX_ITERATIONS_HIT":    "达到迭代上限被强制终止，任务大概率未完成",
	"NO_FINAL_ANSWER":       "用户得不到任何回复，整轮任务无产出",
	"OUTPUT_TRUNCATED":      "最终答复被中途切断，用户看到的是不完整内容，任务未真正完结",
	"DUPLICATE_CALL":        "同参数重复调用，冗余消耗配额与时间",
	"EMPTY_REQUIRED_ARG":    "空参数调用被放行且返回成功，任务实际未完成，结果具有欺骗性",
	"SESSION_NOT_CLOSED":    "会话停留在流式状态，前端可能一直处于加载中",
	"CONFIRM_MANUAL_NEEDED": "确认卡片自动点击多次失败，任务卡在等待人工授权，测试流程无法自动推进",
}

var ConfirmModeLabel = map[string]string{
	"qcard-option":  "选中选项",
	"qcard-confirm": "确认本题",
	"qcard-submit":  "提交",
	"qcard-fill":    "填写自定义答案",
	"keyword":       "关键词命中授权按钮",
	"first-option":  "结构兜底点首个选项",
}

// ToolStat 工具统计
type ToolStat struct {
	Name      string `json:"name"`
	Calls     int    `json:"calls"`
	Fail      int    `json:"fail"`
	Truncated int    `json:"truncated"`
}

// SkillEntry 逐用例的 skill 条目。
// Python 侧字段是 {"name","files","steps"} —— **没有** calls/sessions，
// 而 steps 记录用到该 skill 的步骤序号。多写字段会让前端拿到不存在的契约。
type SkillEntry struct {
	Name  string   `json:"name"`
	Files []string `json:"files"`
	Steps []int    `json:"steps"`
}

// RoundSkill 轮次级 skill 汇总（Python 侧 {"name","sessions","files"} —— 同样没有 calls）
type RoundSkill struct {
	Name     string   `json:"name"`
	Sessions int      `json:"sessions"`
	Files    []string `json:"files"`
}

// UnknownSkill 只读了 SKILL.md、结果里却没有 skill_name 时的回退名
const UnknownSkill = "(未识别技能)"

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

// EstTokens 字符量折算 token 的工程估算：CJK≈1.6 字符/token，其余≈4 字符/token
func EstTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk := 0
	other := 0
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			cjk++
		} else {
			other++
		}
	}
	return int(float64(cjk)/1.6 + float64(other)/4.0)
}

// PyRound 复刻 Python 的 round(x, nd)：银行家舍入（ties-to-even）。
//
// 实现已统一搬到 pyre.Round —— 全项目只保留一份 Python 舍入语义，
// 这里保留公开名只是为了不动既有调用点。
func PyRound(x float64, nd int) float64 {
	return pyre.Round(x, nd)
}

func RoundToOneDecimal(v float64) float64 {
	return PyRound(v, 1)
}

func parseEpoch(tsStr string) float64 {
	if tsStr == "" {
		return 0
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, tsStr, time.Local); err == nil {
			return float64(t.UnixNano()) / 1e9
		}
	}
	return 0
}

func stepStatus(tc *models.ToolCall, findingsByStep map[string][]*models.Finding, sessionID string) string {
	key := fmt.Sprintf("%s:%d", sessionID, tc.Index)
	findings := findingsByStep[key]
	for _, f := range findings {
		if f.Rule == "TOOL_CALL_FAILED" || f.Rule == "TOOL_RESULT_MISSING" {
			return "fail"
		}
	}

	if tc.RawResult == nil || len(strings.TrimSpace(*tc.RawResult)) == 0 {
		return "empty"
	}

	if m, ok := tc.ResultObj.(map[string]any); ok {
		if m["success"] == false || m["is_error"] == true || m["isError"] == true {
			return "fail"
		}
		if errVal, exists := m["error"]; exists && errVal != nil {
			if s, isStr := errVal.(string); isStr && len(strings.TrimSpace(s)) > 0 {
				return "fail"
			}
		}
		if m["truncated"] == true {
			return "truncated"
		}
	}
	return "ok"
}

func SkillName(tc *models.ToolCall) string {
	if tc.Name != "read_skill_file" {
		return ""
	}
	if m, ok := tc.ResultObj.(map[string]any); ok {
		return nilSafeStr(m["skill_name"])
	}
	return ""
}

func SkillFiles(tc *models.ToolCall) []string {
	if m, ok := tc.ResultObj.(map[string]any); ok {
		if rf, ok := m["read_files"].([]any); ok {
			var files []string
			for _, f := range rf {
				files = append(files, fmt.Sprintf("%v", f))
			}
			return files
		}
	}
	if m, ok := tc.Arguments.(map[string]any); ok {
		rel := fmt.Sprintf("%v", m["relative_path"])
		if rel != "" && rel != "<nil>" {
			return []string{rel}
		}
	}
	return []string{}
}

// nilSafeStr 复刻 Python 的 `str(x or "")`：缺失/None 一律得到空串。
// 切忌用 fmt.Sprintf("%v", nil) —— 那会产出字面量字符串 "<nil>"。
func nilSafeStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// nonNilSlices 把 nil 切片换成空切片：Python 产出 []，Go 的 nil 会序列化成 null，
// 而前端对 `?? []` 与 `null` 的处理并不等价。
func nonNilSlices(v any) []string {
	switch t := v.(type) {
	case []string:
		if t == nil {
			return []string{}
		}
		return t
	}
	return []string{}
}

func argSummary(arguments any, limit int) string {
	// Python: json.dumps(arguments, ensure_ascii=False)
	// 分隔符必须是 ", " / ": "，紧凑格式会与 Python 全量不一致。
	s := models.PyJSONDumps(arguments)
	runes := []rune(s)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return s
}

// BuildObjective 单用例客观信息：调用链路之外的量化数据
func BuildObjective(caseResult *models.CaseResult, stageTimes map[string]float64) map[string]any {
	trace := caseResult.Trace
	o := map[string]any{
		"timing":   map[string]any{},
		"volume":   map[string]any{},
		"requests": map[string]any{},
		"status":   map[string]any{},
		"coverage": map[string]any{},
	}
	if trace == nil {
		return o
	}

	st := stageTimes
	if st == nil {
		st = map[string]float64{}
	}
	timing := map[string]any{
		"platform_s":  RoundToOneDecimal(caseResult.ElapsedS),
		"stage_times": st,
		"note":        "日志未记录单次工具调用耗时，故仅提供会话/消息级耗时",
	}
	if trace.FirstResponseS >= 0 {
		timing["first_response_s"] = trace.FirstResponseS
	} else {
		timing["first_response_s"] = nil
	}
	if trace.GenerationS >= 0 {
		timing["generation_s"] = trace.GenerationS
	} else {
		timing["generation_s"] = nil
	}
	if trace.SessionS >= 0 {
		timing["session_s"] = trace.SessionS
	} else {
		timing["session_s"] = nil
	}
	o["timing"] = timing

	toolResultChars := 0
	toolArgChars := 0
	for _, tc := range trace.ToolCalls {
		if tc.RawResult != nil {
			toolResultChars += utf8.RuneCountInString(*tc.RawResult)
		}
		toolArgChars += utf8.RuneCountInString(tc.Signature())
	}
	promptChars := utf8.RuneCountInString(caseResult.Prompt)
	thinkingChars := trace.ReasoningChars
	answerChars := utf8.RuneCountInString(trace.FinalAnswer)

	var thinkingText strings.Builder
	for _, th := range trace.ThinkingSteps {
		thinkingText.WriteString(th.Content)
	}

	inputTokens := EstTokens(caseResult.Prompt)
	for _, tc := range trace.ToolCalls {
		tContent := tc.Body
		if tContent == "" && tc.RawResult != nil {
			tContent = *tc.RawResult
		}
		inputTokens += EstTokens(tContent)
	}

	outputTokens := EstTokens(trace.FinalAnswer) + EstTokens(thinkingText.String())
	for _, tc := range trace.ToolCalls {
		outputTokens += EstTokens(tc.Signature())
	}

	o["volume"] = map[string]any{
		"prompt_chars":      promptChars,
		"thinking_chars":    thinkingChars,
		"answer_chars":      answerChars,
		"tool_result_chars": toolResultChars,
		"input_tokens_est":  inputTokens,
		"output_tokens_est": outputTokens,
		"total_tokens_est":  inputTokens + outputTokens,
		"note":              "token 为估算值（CJK≈1.6字符/token，其余≈4字符/token）；日志未记录官方 usage，仅可观测部分：输入=提示词+工具结果回灌，输出=思考+答复+调用参数",
	}

	toolSet := make(map[string]bool)
	for _, tc := range trace.ToolCalls {
		toolSet[tc.Name] = true
	}

	o["requests"] = map[string]any{
		"turns":          trace.TurnCount,
		"tool_calls":     len(trace.ToolCalls),
		"thinking_steps": len(trace.ThinkingSteps),
		"distinct_tools": len(toolSet),
	}

	n := len(trace.ToolCalls)
	failCount := 0
	for _, f := range caseResult.Findings {
		if f.Rule == "TOOL_CALL_FAILED" || f.Rule == "TOOL_RESULT_MISSING" {
			failCount++
		}
	}
	emptyCount := 0
	truncCount := 0
	for _, tc := range trace.ToolCalls {
		if tc.RawResult != nil && strings.TrimSpace(*tc.RawResult) == "" {
			emptyCount++
		}
		if m, ok := tc.ResultObj.(map[string]any); ok && m["truncated"] == true {
			truncCount++
		}
	}
	closed := !trace.IsStreaming && trace.CompletedAt != ""
	errRate := 0.0
	truncRate := 0.0
	if n > 0 {
		errRate = RoundToOneDecimal(float64(failCount) * 100.0 / float64(n))
		truncRate = RoundToOneDecimal(float64(truncCount) * 100.0 / float64(n))
	}

	closedDesc := "正常收尾"
	if !closed {
		closedDesc = "未正常收尾（缺 completedAt 或仍在流式）"
	}

	o["status"] = map[string]any{
		"tool_calls":       n,
		"tool_ok":          int(math.Max(0, float64(n-failCount-truncCount-emptyCount))),
		"tool_fail":        failCount,
		"tool_truncated":   truncCount,
		"tool_empty":       emptyCount,
		"error_rate":       errRate,
		"truncated_rate":   truncRate,
		"session_closed":   closed,
		"closed_desc":      closedDesc,
		"chain_consistent": len(trace.OrphanIDs()) == 0,
	}

	expected := caseResult.ExpectedTools
	if expected == nil {
		expected = []string{}
	}
	var hit []string
	var missing []string
	var extra []string

	expSet := make(map[string]bool)
	for _, e := range expected {
		expSet[e] = true
		if toolSet[e] {
			hit = append(hit, e)
		} else {
			missing = append(missing, e)
		}
	}
	for t := range toolSet {
		if !expSet[t] {
			extra = append(extra, t)
		}
	}
	sort.Strings(extra)

	o["coverage"] = map[string]any{
		"expected": nonNilSlices(expected),
		"hit":      nonNilSlices(hit),
		"missing":  nonNilSlices(missing),
		"extra":    nonNilSlices(extra),
	}

	return o
}

// evEpoch 复刻 Python _ev_epoch：float(ev.get("ts") or 0)
func evEpoch(ev map[string]any) float64 {
	if f, ok := ev["ts"].(float64); ok {
		return f
	}
	if i, ok := ev["ts"].(int); ok {
		return float64(i)
	}
	return 0
}

// isoToEpochOpt 复刻 Python _iso_to_epoch：本地无时区 ISO 串 → epoch 秒，
// 解析失败返回 ok=false（Python 返回 None）。**无时区串按本地时区解释**，
// 与 datetime.fromisoformat(...).timestamp() 一致。
func isoToEpochOpt(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, time.Local); err == nil {
			return float64(t.UnixNano()) / 1e9, true
		}
	}
	return 0, false
}

func toIntOK(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	}
	return 0, false
}

// confirmLabel 复刻 Python `CONFIRM_MODE_LABEL.get(mode, mode or "自动确认")`。
// mode 为空时必须回退成「自动确认」，绝不能给前端一个空标签。
func confirmLabel(mode string) string {
	if lbl, ok := ConfirmModeLabel[mode]; ok && lbl != "" {
		return lbl
	}
	if mode != "" {
		return mode
	}
	return "自动确认"
}

// cleanConfirmItems 剥掉确认条目里只服务于「定位」的内部字段。
// 前端只需要「题干 + 用户点的答案」：ts/anchor/attribution 是实现细节。
func cleanConfirmItems(items []map[string]any) []map[string]any {
	for _, it := range items {
		if it["type"] == "auto_confirm" {
			delete(it, "ts")
			delete(it, "anchor")
			delete(it, "attribution")
		}
	}
	return items
}

// mergeConfirmEvents 把「平台自动确认」事件近似混排进步骤时间线。
//
// 为什么只能是近似：会话日志只记录**消息级**时间戳，timelineSteps 内的步骤本身
// 没有时间字段，因此「插到第 N 次工具调用之后」无法实现。这里以每条 assistant
// 消息的 [at, done_at) 为一个响应段：
//   - 事件 ts 落入某段 → 追加在该段步骤之后（段内按 ts 升序）；
//   - 早于首段 → 置于最前；晚于末段 / 无时间戳 / 无段可归 → 追加末尾；
//   - 无步骤（会话日志解析失败）时仍然输出事件条目，不静默丢弃。
func mergeConfirmEvents(steps []map[string]any, trace *models.ExecutionTrace, events []any) []map[string]any {
	if steps == nil {
		steps = []map[string]any{}
	}
	items := []map[string]any{}
	for _, rawEv := range events {
		ev, ok := rawEv.(map[string]any)
		if !ok {
			continue
		}
		mode := nilSafeStr(ev["mode"])
		lbl := confirmLabel(mode)
		attr := "time-window"
		if nilSafeStr(ev["sess"]) != "" {
			attr = "session"
		}
		items = append(items, map[string]any{
			"type":        "auto_confirm",
			"time":        nilSafeStr(ev["time"]),
			"ts":          evEpoch(ev),
			"mode":        mode,
			"label":       lbl,
			"text":        nilSafeStr(ev["text"]),
			"q":           nilSafeStr(ev["q"]),
			"question":    nilSafeStr(ev["question"]),
			"anchor":      "",
			"attribution": attr,
		})
	}
	if len(items) == 0 {
		return steps
	}

	type span struct {
		atTS, doneTS   float64
		hasAt, hasDone bool
		firstStep      any
		lastStep       any
		anchor         string
	}
	spans := []span{}
	if trace != nil {
		for _, sp := range trace.MessageSpans {
			if sp == nil {
				continue
			}
			atTS, hasAt := isoToEpochOpt(nilSafeStr(sp["at"]))
			doneTS, hasDone := isoToEpochOpt(nilSafeStr(sp["done_at"]))
			spans = append(spans, span{
				atTS: atTS, hasAt: hasAt, doneTS: doneTS, hasDone: hasDone,
				firstStep: sp["first_step"], lastStep: sp["last_step"],
				anchor: nilSafeStr(sp["at"]),
			})
		}
	}

	firstAt, hasFirstAt := 0.0, false
	for _, sp := range spans {
		if sp.hasAt && sp.atTS != 0 {
			firstAt, hasFirstAt = sp.atTS, true
			break
		}
	}

	head := []map[string]any{}
	tail := []map[string]any{}
	atSeg := map[int][]map[string]any{}
	for _, it := range items {
		ts, _ := it["ts"].(float64)
		hit := -1
		if ts > 0 {
			for i, sp := range spans {
				if !sp.hasAt {
					continue
				}
				// 段结束：消息完成时间；缺失则顺延到下一段起点（最后一段无上界）
				hi, hasHi := sp.doneTS, sp.hasDone && sp.doneTS != 0
				if !hasHi && i+1 < len(spans) && spans[i+1].hasAt {
					hi, hasHi = spans[i+1].atTS, true
				}
				if ts >= sp.atTS && (!hasHi || ts < hi) {
					hit = i
					break
				}
			}
		}
		if hit >= 0 {
			it["anchor"] = spans[hit].anchor
			atSeg[hit] = append(atSeg[hit], it)
		} else if ts > 0 && hasFirstAt && ts < firstAt {
			head = append(head, it)
		} else {
			tail = append(tail, it)
		}
	}

	out := []map[string]any{}
	out = append(out, head...)
	covered := map[int]bool{}
	for i, sp := range spans {
		lo, loOK := toIntOK(sp.firstStep)
		hi, hiOK := toIntOK(sp.lastStep)
		if loOK && hiOK {
			for si, st := range steps {
				iv, ok := st["i"]
				if !ok {
					continue
				}
				got, ok2 := toIntOK(iv)
				if ok2 && got >= lo && got <= hi {
					out = append(out, st)
					covered[si] = true
				}
			}
		}
		seg := atSeg[i]
		sort.SliceStable(seg, func(a, b int) bool {
			x, _ := seg[a]["ts"].(float64)
			y, _ := seg[b]["ts"].(float64)
			return x < y
		})
		out = append(out, seg...)
	}
	// 未被任何消息段覆盖的步骤（保持原序）
	for si, st := range steps {
		if !covered[si] {
			out = append(out, st)
		}
	}
	sort.SliceStable(tail, func(a, b int) bool {
		x, _ := tail[a]["ts"].(float64)
		y, _ := tail[b]["ts"].(float64)
		return x < y
	})
	out = append(out, tail...)
	return cleanConfirmItems(out)
}

// BuildCaseDetail 构建单用例详情
func BuildCaseDetail(caseResult *models.CaseResult, findingsByStep map[string][]*models.Finding, cfg map[string]any) map[string]any {
	trace := caseResult.Trace
	if trace == nil {
		steps := mergeConfirmEvents([]map[string]any{}, nil, caseResult.ConfirmEvents)
		return map[string]any{
			"case_id":           caseResult.CaseID,
			"name":              caseResult.Name,
			"prompt":            caseResult.Prompt,
			"status":            caseResult.Status(),
			"session_id":        caseResult.SessionID,
			"session_dir":       "",
			"session_log_path":  "",
			"ui_error":          caseResult.UIError,
			"elapsed_s":         RoundToOneDecimal(caseResult.ElapsedS),
			"attachments":       nonNilSlices(caseResult.Attachments),
			"attach_note":       caseResult.AttachNote,
			"waited_limit":      caseResult.WaitedLimit,
			"wait_note":         caseResult.WaitNote,
			"auto_confirms":     caseResult.AutoConfirms,
			"steps":             steps,
			"tools":             []map[string]any{},
			"skills":            []map[string]any{},
			"objective":         map[string]any{},
			"artifacts":         []map[string]any{},
			"artifact_kinds":    []string{},
			"artifacts_version": artifacts.ExtractVersion,
		}
	}

	objective := BuildObjective(caseResult, nil)

	type mergedItem struct {
		kind  string
		idx   int
		tc    *models.ToolCall
		think *models.ThinkingStep
	}
	var merged []mergedItem
	for _, tc := range trace.ToolCalls {
		merged = append(merged, mergedItem{kind: "tool", idx: tc.Index, tc: tc})
	}
	for _, th := range trace.ThinkingSteps {
		merged = append(merged, mergedItem{kind: "think", idx: th.Index, think: th})
	}
	// 必须稳定排序：Python 的 list.sort() 稳定，且它是先把 tool 全部入列、
	// 再把 thinking 入列，所以**下标相同时 tool 排在 thinking 前面**。
	// 用 sort.Slice 会让同下标的顺序随机化，整个时间线随之漂移。
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].idx < merged[j].idx
	})

	steps := []map[string]any{}
	thinkingNo := 0
	for _, m := range merged {
		if m.kind == "think" {
			thinkingNo++
			c := m.think.Content
			steps = append(steps, map[string]any{
				"i":       m.idx,
				"type":    "thinking",
				"n":       thinkingNo,
				"content": c,
				"chars":   utf8.RuneCountInString(c),
			})
		} else {
			tc := m.tc
			st := stepStatus(tc, findingsByStep, trace.SessionID)
			raw := ""
			if tc.RawResult != nil {
				raw = *tc.RawResult
			}
			rLen := utf8.RuneCountInString(tc.Body)
			if rLen == 0 {
				rLen = utf8.RuneCountInString(raw)
			}
			stepKey := fmt.Sprintf("%s:%d", trace.SessionID, tc.Index)
			var issueRules []string
			for _, f := range findingsByStep[stepKey] {
				issueRules = append(issueRules, f.Rule)
			}
			sort.Strings(issueRules)

			var sFiles []string
			if tc.Name == "read_skill_file" {
				sFiles = SkillFiles(tc)
			}

			steps = append(steps, map[string]any{
				"i":            tc.Index,
				"type":         "tool_call",
				"name":         tc.Name,
				"status":       st,
				"args_summary": argSummary(tc.Arguments, 160),
				"result":       raw,
				"result_len":   rLen,
				"raw_len":      utf8.RuneCountInString(raw),
				"body_from":    tc.BodyFrom,
				"result_empty": strings.TrimSpace(raw) == "",
				"skill_name":   SkillName(tc),
				"skill_files":  nonNilSlices(sFiles),
				"issues":       nonNilSlices(issueRules),
			})
		}
	}

	steps = mergeConfirmEvents(steps, trace, caseResult.ConfirmEvents)

	// 工具统计
	toolStats := make(map[string]*ToolStat)
	var toolOrder []string
	for _, tc := range trace.ToolCalls {
		st, ok := toolStats[tc.Name]
		if !ok {
			st = &ToolStat{Name: tc.Name}
			toolStats[tc.Name] = st
			toolOrder = append(toolOrder, tc.Name)
		}
		st.Calls++
		s := stepStatus(tc, findingsByStep, trace.SessionID)
		if s == "fail" {
			st.Fail++
		} else if s == "truncated" {
			st.Truncated++
		}
	}
	// 按首次出现顺序取出，再用**稳定**排序 —— Python 的 sorted() 是稳定排序，
	// 同调用次数时必须保持首次出现顺序，否则 finding 顺序会随运行漂移。
	toolsList := []*ToolStat{}
	for _, name := range toolOrder {
		toolsList = append(toolsList, toolStats[name])
	}
	sort.SliceStable(toolsList, func(i, j int) bool {
		return toolsList[i].Calls > toolsList[j].Calls
	})

	// Skill 聚合
	skillsMap := make(map[string]*SkillEntry)
	var skillOrder []string
	ensureSkill := func(name string) *SkillEntry {
		sk, ok := skillsMap[name]
		if !ok {
			sk = &SkillEntry{Name: name, Files: []string{}, Steps: []int{}}
			skillsMap[name] = sk
			skillOrder = append(skillOrder, name)
		}
		return sk
	}
	for _, tc := range trace.ToolCalls {
		sn := SkillName(tc)
		if sn == "" {
			continue
		}
		sk := ensureSkill(sn)
		for _, f := range SkillFiles(tc) {
			sk.Files = appendUnique(sk.Files, f)
		}
		sk.Steps = append(sk.Steps, tc.Index)
	}
	// 若只读了 SKILL.md 但结果里没有 skill_name，回退标记为未知技能
	for _, tc := range trace.ToolCalls {
		if tc.Name != "read_skill_file" || SkillName(tc) != "" {
			continue
		}
		sk := ensureSkill(UnknownSkill)
		sk.Steps = append(sk.Steps, tc.Index)
		for _, f := range SkillFiles(tc) {
			sk.Files = appendUnique(sk.Files, f)
		}
	}
	// 按首次出现顺序输出（Python 的 dict 保证插入有序；range map 会随机化）
	skillsList := []*SkillEntry{}
	for _, name := range skillOrder {
		skillsList = append(skillsList, skillsMap[name])
	}

	wsRoot := ""
	if cfg != nil {
		if paths, ok := cfg["paths"].(map[string]any); ok {
			wsRoot = fmt.Sprintf("%v", paths["workspace_root"])
		}
	}
	artSet := artifacts.ExtractArtifacts(trace, wsRoot)
	artItems := []map[string]any{}
	for _, a := range artSet.Items {
		artItems = append(artItems, map[string]any{
			"kind":     a.Kind,
			"path":     a.RelPath,
			"note":     a.Note,
			"abs_path": artifacts.DisplayAbsPath(a, wsRoot),
		})
	}
	var artKinds []string
	for k := range artSet.Kinds() {
		artKinds = append(artKinds, k)
	}
	sort.Strings(artKinds)
	if artKinds == nil {
		artKinds = []string{}
	}

	findingsDTO := []map[string]any{}
	for _, f := range caseResult.Findings {
		fm := map[string]any{
			"rule":       f.Rule,
			"severity":   f.Severity,
			"detail":     f.Detail,
			"tool":       f.Tool,
			"evidence":   f.Evidence,
			"step_index": f.StepIndex,
		}
		findingsDTO = append(findingsDTO, fm)
	}

	ansExcerpt := trace.FinalAnswer
	if utf8.RuneCountInString(ansExcerpt) > 600 {
		runes := []rune(ansExcerpt)
		ansExcerpt = string(runes[:600]) + "…"
	}

	return map[string]any{
		"case_id":           caseResult.CaseID,
		"name":              caseResult.Name,
		"prompt":            caseResult.Prompt,
		"status":            caseResult.Status(),
		"session_id":        caseResult.SessionID,
		"session_dir":       trace.SourceFile,
		"session_log_path":  trace.SourceFile,
		"ui_error":          caseResult.UIError,
		"attachments":       nonNilSlices(caseResult.Attachments),
		"attach_note":       caseResult.AttachNote,
		"waited_limit":      caseResult.WaitedLimit,
		"wait_note":         caseResult.WaitNote,
		"auto_confirms":     caseResult.AutoConfirms,
		"elapsed_s":         RoundToOneDecimal(caseResult.ElapsedS),
		"mode":              trace.Mode,
		"created_at":        trace.CreatedAt,
		"answer_excerpt":    ansExcerpt,
		"answer_chars":      utf8.RuneCountInString(trace.FinalAnswer),
		"reasoning_chars":   trace.ReasoningChars,
		"tool_call_count":   len(trace.ToolCalls),
		"thinking_count":    len(trace.ThinkingSteps),
		"steps":             steps,
		"tools":             toolsList,
		"skills":            skillsList,
		"objective":         objective,
		"artifacts":         artItems,
		"artifact_kinds":    artKinds,
		"artifacts_version": artifacts.ExtractVersion,
		"findings":          findingsDTO,
	}
}

// BuildRoundDetail 构建整轮详情
func BuildRoundDetail(caseResults []*models.CaseResult, metrics map[string]any, reproRows []map[string]any, stageTimes map[string]float64, cfg map[string]any) map[string]any {
	findingsByStep := make(map[string][]*models.Finding)
	for _, c := range caseResults {
		for _, f := range c.Findings {
			if f.StepIndex != nil {
				key := fmt.Sprintf("%s:%d", c.SessionID, *f.StepIndex)
				findingsByStep[key] = append(findingsByStep[key], f)
			}
		}
	}

	var details []map[string]any
	for _, c := range caseResults {
		details = append(details, BuildCaseDetail(c, findingsByStep, cfg))
	}

	toolsMap := make(map[string]*ToolStat)
	skillsMap := make(map[string]*RoundSkill)
	var toolsOrder, skillsOrder []string

	for _, d := range details {
		if tList, ok := d["tools"].([]*ToolStat); ok {
			for _, t := range tList {
				agg, exists := toolsMap[t.Name]
				if !exists {
					agg = &ToolStat{Name: t.Name}
					toolsMap[t.Name] = agg
					toolsOrder = append(toolsOrder, t.Name)
				}
				agg.Calls += t.Calls
				agg.Fail += t.Fail
				agg.Truncated += t.Truncated
			}
		}
		if sList, ok := d["skills"].([]*SkillEntry); ok {
			for _, s := range sList {
				agg, exists := skillsMap[s.Name]
				if !exists {
					agg = &RoundSkill{Name: s.Name, Files: []string{}}
					skillsMap[s.Name] = agg
					skillsOrder = append(skillsOrder, s.Name)
				}
				agg.Sessions++ // 每出现一次（即每个用例一次）计一次
				for _, f := range s.Files {
					agg.Files = appendUnique(agg.Files, f)
				}
			}
		}
	}

	// 按**首次出现顺序**取出后再稳定排序：Python 的 dict.setdefault 保证插入有序，
	// sorted() 又是稳定排序，同调用次数时顺序不能漂移。
	roundTools := []*ToolStat{}
	for _, name := range toolsOrder {
		roundTools = append(roundTools, toolsMap[name])
	}
	sort.SliceStable(roundTools, func(i, j int) bool {
		return roundTools[i].Calls > roundTools[j].Calls
	})

	roundSkills := []*RoundSkill{}
	for _, name := range skillsOrder {
		roundSkills = append(roundSkills, skillsMap[name])
	}
	sort.SliceStable(roundSkills, func(i, j int) bool {
		return roundSkills[i].Sessions > roundSkills[j].Sessions
	})

	var roundArtifacts []map[string]any
	seenArt := make(map[string]bool)
	var roundKinds []string

	for _, d := range details {
		if arts, ok := d["artifacts"].([]map[string]any); ok {
			for _, a := range arts {
				k := fmt.Sprintf("%v:%v", a["kind"], a["path"])
				if !seenArt[k] {
					seenArt[k] = true
					roundArtifacts = append(roundArtifacts, a)
					kd := fmt.Sprintf("%v", a["kind"])
					if kd != "" {
						already := false
						for _, rk := range roundKinds {
							if rk == kd {
								already = true
								break
							}
						}
						if !already {
							roundKinds = append(roundKinds, kd)
						}
					}
				}
			}
		}
	}
	sort.Strings(roundKinds)

	if metrics == nil {
		metrics = map[string]any{}
	}
	if reproRows == nil {
		reproRows = []map[string]any{}
	}

	// ---- 轮次级客观汇总（Python build_round_detail 里的 round_objective）----
	sumObj := func(field, key string) int {
		total := 0
		for _, d := range details {
			obj, _ := d["objective"].(map[string]any)
			if len(obj) == 0 {
				continue
			}
			grp, _ := obj[field].(map[string]any)
			if grp == nil {
				continue
			}
			total += toIntValue(grp[key])
		}
		return total
	}
	aggIn := sumObj("volume", "input_tokens_est")
	aggOut := sumObj("volume", "output_tokens_est")
	aggCalls := sumObj("requests", "tool_calls")
	aggFail := sumObj("status", "tool_fail")
	aggTrunc := sumObj("status", "tool_truncated")

	// timings 只收「非空」的 objective.timing
	var timings []map[string]any
	for _, d := range details {
		obj, _ := d["objective"].(map[string]any)
		if len(obj) == 0 {
			continue
		}
		t, _ := obj["timing"].(map[string]any)
		if len(t) == 0 {
			continue
		}
		timings = append(timings, t)
	}
	var avgFirst any
	if len(timings) > 0 {
		sum := 0.0
		for _, t := range timings {
			sum += toFloatValue(t["first_response_s"])
		}
		avgFirst = PyRound(sum/float64(len(timings)), 2)
	}
	stMap := stageTimes
	if stMap == nil {
		stMap = map[string]float64{}
	}
	pctOf := func(a, b int) float64 {
		if b == 0 {
			return 0.0
		}
		return PyRound(float64(a)*100.0/float64(b), 1)
	}
	roundObjective := map[string]any{
		"volume": map[string]any{
			"input_tokens_est":  aggIn,
			"output_tokens_est": aggOut,
			"total_tokens_est":  aggIn + aggOut,
		},
		"requests": map[string]any{
			"turns":          sumObj("requests", "turns"),
			"tool_calls":     aggCalls,
			"thinking_steps": sumObj("requests", "thinking_steps"),
		},
		"status": map[string]any{
			"tool_fail":      aggFail,
			"tool_truncated": aggTrunc,
			"error_rate":     pctOf(aggFail, aggCalls),
			"truncated_rate": pctOf(aggTrunc, aggCalls),
		},
		"timing": map[string]any{
			"avg_first_response_s": avgFirst,
			"stage_times":          stMap,
		},
	}

	return map[string]any{
		"cases":                details,
		"round_artifacts":      roundArtifacts,
		"round_artifact_kinds": roundKinds,
		"round_tools":          roundTools,
		"round_skills":         roundSkills,
		"round_objective":      roundObjective,
		"metrics":              metrics,
		"repro_rows":           reproRows,
	}
}

func toIntValue(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

func toFloatValue(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	}
	return 0
}
