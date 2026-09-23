package trajectory

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/artifacts"
	"ruiyun-ui-test-platform-go/internal/models"
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

// RoundToOneDecimal 保留 1 位小数
func RoundToOneDecimal(v float64) float64 {
	return math.Round(v*10) / 10
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

func skillName(tc *models.ToolCall) string {
	if tc.Name != "read_skill_file" {
		return ""
	}
	if m, ok := tc.ResultObj.(map[string]any); ok {
		return fmt.Sprintf("%v", m["skill_name"])
	}
	return ""
}

func skillFiles(tc *models.ToolCall) []string {
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

func argSummary(arguments any, limit int) string {
	b, err := json.Marshal(arguments)
	s := ""
	if err == nil {
		s = string(b)
	} else {
		s = fmt.Sprintf("%v", arguments)
	}
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

	timing := map[string]any{
		"platform_s":  RoundToOneDecimal(caseResult.ElapsedS),
		"stage_times": stageTimes,
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
		"expected": expected,
		"hit":      hit,
		"missing":  missing,
		"extra":    extra,
	}

	return o
}

func mergeConfirmEvents(steps []map[string]any, trace *models.ExecutionTrace, events []any) []map[string]any {
	var items []map[string]any
	for _, rawEv := range events {
		ev, ok := rawEv.(map[string]any)
		if !ok {
			continue
		}
		mode := fmt.Sprintf("%v", ev["mode"])
		lbl := ConfirmModeLabel[mode]
		if lbl == "" {
			lbl = mode
		}
		tsVal := 0.0
		if ts, ok := ev["ts"].(float64); ok {
			tsVal = ts
		}
		items = append(items, map[string]any{
			"type":     "auto_confirm",
			"time":     fmt.Sprintf("%v", ev["time"]),
			"ts":       tsVal,
			"mode":     mode,
			"label":    lbl,
			"text":     fmt.Sprintf("%v", ev["text"]),
			"q":        fmt.Sprintf("%v", ev["q"]),
			"question": fmt.Sprintf("%v", ev["question"]),
		})
	}
	if len(items) == 0 {
		return steps
	}

	// 混排事件与步骤
	var res []map[string]any
	res = append(res, steps...)
	for _, it := range items {
		delete(it, "ts")
		res = append(res, it)
	}
	return res
}

// BuildCaseDetail 构建单用例详情
func BuildCaseDetail(caseResult *models.CaseResult, findingsByStep map[string][]*models.Finding, cfg map[string]any) map[string]any {
	trace := caseResult.Trace
	if trace == nil {
		steps := mergeConfirmEvents([]map[string]any{}, nil, caseResult.ConfirmEvents)
		return map[string]any{
			"case_id":          caseResult.CaseID,
			"name":             caseResult.Name,
			"prompt":           caseResult.Prompt,
			"status":           caseResult.Status(),
			"session_id":       caseResult.SessionID,
			"session_dir":      "",
			"session_log_path": "",
			"ui_error":         caseResult.UIError,
			"elapsed_s":        RoundToOneDecimal(caseResult.ElapsedS),
			"attachments":      caseResult.Attachments,
			"attach_note":      caseResult.AttachNote,
			"waited_limit":     caseResult.WaitedLimit,
			"wait_note":        caseResult.WaitNote,
			"auto_confirms":    caseResult.AutoConfirms,
			"steps":            steps,
			"tools":            []map[string]any{},
			"skills":           []map[string]any{},
			"objective":        map[string]any{},
			"artifacts":        []map[string]any{},
			"artifact_kinds":   []string{},
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
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].idx < merged[j].idx
	})

	var steps []map[string]any
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
				sFiles = skillFiles(tc)
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
				"skill_name":   skillName(tc),
				"skill_files":  sFiles,
				"issues":       issueRules,
			})
		}
	}

	steps = mergeConfirmEvents(steps, trace, caseResult.ConfirmEvents)

	// 工具统计
	type toolStat struct {
		Name      string `json:"name"`
		Calls     int    `json:"calls"`
		Fail      int    `json:"fail"`
		Truncated int    `json:"truncated"`
	}
	toolStats := make(map[string]*toolStat)
	for _, tc := range trace.ToolCalls {
		st, ok := toolStats[tc.Name]
		if !ok {
			st = &toolStat{Name: tc.Name}
			toolStats[tc.Name] = st
		}
		st.Calls++
		s := stepStatus(tc, findingsByStep, trace.SessionID)
		if s == "fail" {
			st.Fail++
		} else if s == "truncated" {
			st.Truncated++
		}
	}
	var toolsList []*toolStat
	for _, st := range toolStats {
		toolsList = append(toolsList, st)
	}
	sort.Slice(toolsList, func(i, j int) bool {
		return toolsList[i].Calls > toolsList[j].Calls
	})

	// Skill 聚合
	type skillStat struct {
		Name     string   `json:"name"`
		Files    []string `json:"files"`
		Sessions []string `json:"sessions"`
		Calls    int      `json:"calls"`
	}
	skillsMap := make(map[string]*skillStat)
	for _, tc := range trace.ToolCalls {
		sn := skillName(tc)
		if sn == "" {
			continue
		}
		sk, ok := skillsMap[sn]
		if !ok {
			sk = &skillStat{Name: sn, Files: []string{}, Sessions: []string{caseResult.CaseID}}
			skillsMap[sn] = sk
		}
		sk.Calls++
		for _, f := range skillFiles(tc) {
			hasF := false
			for _, ef := range sk.Files {
				if ef == f {
					hasF = true
					break
				}
			}
			if !hasF && f != "" {
				sk.Files = append(sk.Files, f)
			}
		}
	}
	var skillsList []*skillStat
	for _, sk := range skillsMap {
		skillsList = append(skillsList, sk)
	}

	wsRoot := ""
	if cfg != nil {
		if paths, ok := cfg["paths"].(map[string]any); ok {
			wsRoot = fmt.Sprintf("%v", paths["workspace_root"])
		}
	}
	artSet := artifacts.ExtractArtifacts(trace, wsRoot)
	var artItems []map[string]any
	for _, a := range artSet.Items {
		artItems = append(artItems, map[string]any{
			"kind":     a.Kind,
			"path":     a.RelPath,
			"note":     a.Note,
			"abs_path": a.FullPath,
		})
	}
	var artKinds []string
	for k := range artSet.Kinds() {
		artKinds = append(artKinds, k)
	}
	sort.Strings(artKinds)

	var findingsDTO []map[string]any
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
		"case_id":          caseResult.CaseID,
		"name":             caseResult.Name,
		"prompt":           caseResult.Prompt,
		"status":           caseResult.Status(),
		"session_id":       caseResult.SessionID,
		"session_dir":      trace.SourceFile,
		"session_log_path": trace.SourceFile,
		"ui_error":         caseResult.UIError,
		"attachments":      caseResult.Attachments,
		"attach_note":      caseResult.AttachNote,
		"waited_limit":     caseResult.WaitedLimit,
		"wait_note":        caseResult.WaitNote,
		"auto_confirms":    caseResult.AutoConfirms,
		"elapsed_s":        RoundToOneDecimal(caseResult.ElapsedS),
		"mode":             trace.Mode,
		"created_at":       trace.CreatedAt,
		"answer_excerpt":   ansExcerpt,
		"answer_chars":     utf8.RuneCountInString(trace.FinalAnswer),
		"reasoning_chars":  trace.ReasoningChars,
		"tool_call_count":  len(trace.ToolCalls),
		"thinking_count":   len(trace.ThinkingSteps),
		"steps":            steps,
		"tools":            toolsList,
		"skills":           skillsList,
		"objective":        objective,
		"artifacts":        artItems,
		"artifact_kinds":   artKinds,
		"findings":         findingsDTO,
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

	type toolAgg struct {
		Name      string `json:"name"`
		Calls     int    `json:"calls"`
		Fail      int    `json:"fail"`
		Truncated int    `json:"truncated"`
	}
	toolsMap := make(map[string]*toolAgg)
	for _, d := range details {
		if tList, ok := d["tools"].([]*struct {
			Name      string `json:"name"`
			Calls     int    `json:"calls"`
			Fail      int    `json:"fail"`
			Truncated int    `json:"truncated"`
		}); ok {
			for _, t := range tList {
				agg, exists := toolsMap[t.Name]
				if !exists {
					agg = &toolAgg{Name: t.Name}
					toolsMap[t.Name] = agg
				}
				agg.Calls += t.Calls
				agg.Fail += t.Fail
				agg.Truncated += t.Truncated
			}
		}
	}

	var roundTools []*toolAgg
	for _, v := range toolsMap {
		roundTools = append(roundTools, v)
	}
	sort.Slice(roundTools, func(i, j int) bool {
		return roundTools[i].Calls > roundTools[j].Calls
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

	return map[string]any{
		"cases":                details,
		"round_artifacts":      roundArtifacts,
		"round_artifact_kinds": roundKinds,
		"round_tools":          roundTools,
		"metrics":              metrics,
		"repro_rows":           reproRows,
	}
}
