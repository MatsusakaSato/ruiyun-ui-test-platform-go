package assertor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/canon"
	"ruiyun-ui-test-platform-go/internal/models"
)

// PAYLOAD_CONTENT_FIELDS 结构化返回中承载正文/数据的载荷字段
var payloadContentFields = map[string]bool{
	"file_content":     true,
	"content":          true,
	"text":             true,
	"markdown":         true,
	"markdown_content": true,
	"body":             true,
	"data":             true,
	"code":             true,
	"answer":           true,
	"result_text":      true,
	"doc_content":      true,
}

type RuleMeta struct {
	Severity string
	Label    string
}

var Rules = map[string]RuleMeta{
	"TOOL_CALL_FAILED":      {"P0", "工具调用失败"},
	"TOOL_RESULT_MISSING":   {"P0", "工具调用无返回"},
	"ORPHAN_TOOL_CALL":      {"P0", "调用链路 ID 不一致"},
	"LOOP_CONSECUTIVE":      {"P0", "死循环（同工具连续调用）"},
	"MAX_ITERATIONS_HIT":    {"P0", "触达 ReAct 迭代上限"},
	"NO_FINAL_ANSWER":       {"P0", "会话无最终答复"},
	"OUTPUT_TRUNCATED":      {"P1", "最终答案被强制截断（完结率不足）"},
	"DUPLICATE_CALL":        {"P1", "同参数重复调用"},
	"LOOP_TOTAL":            {"P1", "同工具高频调用"},
	"EMPTY_REQUIRED_ARG":    {"P1", "必填参数为空被放行"},
	"SESSION_NOT_CLOSED":    {"P1", "会话未正常收尾"},
	"CONFIRM_MANUAL_NEEDED": {"P0", "确认卡片自动点击失败，需人工介入"},
}

// RuleOrder 规则的**声明顺序**。
// rule_rows 按此声明顺序生成后做稳定排序；
// Go 的 map 遍历会随机化，必须显式保留顺序，否则同严重度同数量的规则行会乱序。
var RuleOrder = []string{
	"TOOL_CALL_FAILED", "TOOL_RESULT_MISSING", "ORPHAN_TOOL_CALL",
	"LOOP_CONSECUTIVE", "MAX_ITERATIONS_HIT", "NO_FINAL_ANSWER",
	"OUTPUT_TRUNCATED", "DUPLICATE_CALL", "LOOP_TOTAL",
	"EMPTY_REQUIRED_ARG", "SESSION_NOT_CLOSED", "CONFIRM_MANUAL_NEEDED",
}

func makeFinding(rule string, trace *models.ExecutionTrace, detail string, tool string, step *int, evidence string) *models.Finding {
	sev := "P2"
	if r, ok := Rules[rule]; ok {
		sev = r.Severity
	}
	evi := evidence
	if utf8.RuneCountInString(evi) > 400 {
		runes := []rune(evi)
		evi = string(runes[:400])
	}
	return &models.Finding{
		Rule:      rule,
		Severity:  sev,
		SessionID: trace.SessionID,
		Detail:    detail,
		Tool:      tool,
		StepIndex: step,
		Evidence:  evi,
	}
}

var reException = regexp.MustCompile(`(?i)(?:^|[` + canon.SpaceClass + `\[\(\"'])(?:\w+)?exception[` + canon.SpaceClass + `]*:`)
var reRaisedException = regexp.MustCompile(`(?i)\b(?:unhandled|uncaught|raised|raise)[` + canon.SpaceClass + `]+(?:an?[` + canon.SpaceClass + `]+)?(?:\w+)?exception\b`)

func matchMarker(textLower, marker string) bool {
	m := strings.ToLower(strings.TrimSpace(marker))
	if m == "" {
		return false
	}
	if m == "exception" || m == "exception:" {
		return reException.MatchString(textLower) ||
			reRaisedException.MatchString(textLower) ||
			strings.Contains(textLower, "[exception]")
	}
	return strings.Contains(textLower, m)
}

func getErrorMarkers(cfg map[string]any) []string {
	if cfg == nil {
		return nil
	}
	var res []string
	if mList, ok := cfg["error_markers"].([]any); ok {
		for _, v := range mList {
			// 逐个取出 cfg["error_markers"] 并小写化
			// 必须小写：配置里同时存在 "[ERROR]" 与 "[error]"，不小写会被当成
			// 两个不同特征，命中列表里会多出一项。
			s := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", v)))
			if s != "" {
				res = append(res, s)
			}
		}
	} else if mList, ok := cfg["error_markers"].([]string); ok {
		for _, v := range mList {
			if s := strings.ToLower(strings.TrimSpace(v)); s != "" {
				res = append(res, s)
			}
		}
	}
	return res
}

// CheckToolFailed 检查单个工具调用是否失败
func CheckToolFailed(tc *models.ToolCall, trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	markers := getErrorMarkers(cfg)
	var hits []string
	obj := tc.ResultObj

	if m, ok := obj.(map[string]any); ok {
		if m["success"] == false {
			hits = append(hits, "success=false")
		}
		if m["is_error"] == true || m["isError"] == true {
			hits = append(hits, "is_error=true")
		}
		if errVal, hasErr := m["error"]; hasErr {
			if s, isStr := errVal.(string); isStr {
				if strings.TrimSpace(s) != "" {
					hits = append(hits, "error field")
				}
			} else if errVal != nil {
				hits = append(hits, "error field")
			}
		}

		// 显式成功保护：明确 success=true 且无 error 字段时，绝不误判
		if m["success"] == true && len(hits) == 0 {
			return nil
		}

		if len(hits) == 0 {
			diagKeys := map[string]bool{
				"error": true, "stderr": true, "traceback": true,
				"err": true, "msg": true, "message": true, "reason": true,
			}
			var diagTexts []string
			for k := range diagKeys {
				if v, exists := m[k]; exists && v != nil {
					diagTexts = append(diagTexts, fmt.Sprintf("%v", v))
				}
			}
			diagText := strings.ToLower(strings.Join(diagTexts, " "))
			if diagText != "" {
				for _, marker := range markers {
					if matchMarker(diagText, marker) {
						hits = append(hits, marker)
					}
				}
			}

			if len(hits) == 0 {
				var metaParts []string
				for k, v := range m {
					if !payloadContentFields[strings.ToLower(k)] && v != nil {
						metaParts = append(metaParts, fmt.Sprintf("%v", v))
					}
				}
				metaText := strings.ToLower(strings.Join(metaParts, " "))
				for _, marker := range markers {
					if matchMarker(metaText, marker) {
						hits = append(hits, marker)
					}
				}
			}
		}
	} else {
		// 非结构化文本结果
		text := ""
		if tc.RawResult != nil {
			text = strings.ToLower(strings.TrimSpace(*tc.RawResult))
		}
		for _, marker := range markers {
			if matchMarker(text, marker) {
				hits = append(hits, marker)
			}
		}
	}

	// 空结果判定
	if len(hits) == 0 && (tc.RawResult == nil || strings.TrimSpace(*tc.RawResult) == "") {
		step := tc.Index
		return []*models.Finding{
			makeFinding("TOOL_RESULT_MISSING", trace, fmt.Sprintf("%s 返回空结果", tc.Name), tc.Name, &step, ""),
		}
	}

	if len(hits) > 0 {
		hitSet := make(map[string]bool)
		for _, h := range hits {
			hitSet[h] = true
		}
		var sortedHits []string
		for h := range hitSet {
			sortedHits = append(sortedHits, h)
		}
		sort.Strings(sortedHits)

		head := ""
		if tc.RawResult != nil {
			head = strings.ReplaceAll(*tc.RawResult, "\n", " ")
			if len([]rune(head)) > 200 {
				head = string([]rune(head)[:200])
			}
		}
		step := tc.Index
		return []*models.Finding{
			makeFinding("TOOL_CALL_FAILED", trace,
				fmt.Sprintf("%s 调用失败（命中特征：%s）", tc.Name, strings.Join(sortedHits, ", ")),
				tc.Name, &step, head),
		}
	}

	return nil
}

// CheckConsecutiveLoop 死循环检测（连续调用同工具）
func CheckConsecutiveLoop(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	th := 5
	if v, ok := cfg["loop_consecutive_threshold"].(int); ok && v > 0 {
		th = v
	}
	var out []*models.Finding
	run := 1
	prev := ""
	for i, tc := range trace.ToolCalls {
		if tc.Name == prev {
			run++
		} else {
			run = 1
			prev = tc.Name
		}
		if run == th {
			step := trace.ToolCalls[i].Index
			out = append(out, makeFinding("LOOP_CONSECUTIVE", trace,
				fmt.Sprintf("%s 连续调用 %d 次（阈值 %d）", tc.Name, run, th),
				tc.Name, &step, ""))
		}
	}
	return out
}

// CheckTotalLoop 同工具高频调用检测
func CheckTotalLoop(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	th := 8
	if v, ok := cfg["loop_total_threshold"].(int); ok && v > 0 {
		th = v
	}
	counts := make(map[string]int)
	// 计数需按**首次出现顺序**遍历。
	// 直接用 map range 会让 finding 顺序随机化（实测：6 条 finding 顺序错乱）。
	var order []string
	for _, tc := range trace.ToolCalls {
		if _, seen := counts[tc.Name]; !seen {
			order = append(order, tc.Name)
		}
		counts[tc.Name]++
	}
	var out []*models.Finding
	for _, nm := range order {
		if c := counts[nm]; c >= th {
			out = append(out, makeFinding("LOOP_TOTAL", trace,
				fmt.Sprintf("%s 单会话共调用 %d 次（阈值 %d）", nm, c, th), nm, nil, ""))
		}
	}
	return out
}

// CheckDuplicate 同参数重复调用
func CheckDuplicate(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	type key struct {
		name string
		sig  string
	}
	seen := make(map[key][]*models.ToolCall)
	// 按**首次出现顺序**遍历。
	// 直接用 map range 会让 finding 顺序随机化（实测：10 条 finding 顺序错乱）。
	var order []key
	for _, tc := range trace.ToolCalls {
		k := key{name: tc.Name, sig: tc.Signature()}
		if _, ok := seen[k]; !ok {
			order = append(order, k)
		}
		seen[k] = append(seen[k], tc)
	}
	var out []*models.Finding
	for _, k := range order {
		items := seen[k]
		if len(items) >= 2 {
			step := items[1].Index
			evi := k.sig
			if len([]rune(evi)) > 200 {
				evi = string([]rune(evi)[:200])
			}
			out = append(out, makeFinding("DUPLICATE_CALL", trace,
				fmt.Sprintf("%s 使用完全相同的参数重复调用 %d 次", k.name, len(items)),
				k.name, &step, evi))
		}
	}
	return out
}

// CheckEmptyArgs 必填参数为空检测
func CheckEmptyArgs(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	var out []*models.Finding
	for _, tc := range trace.ToolCalls {
		if len(tc.EmptyRequiredArg) > 0 {
			step := tc.Index
			// 取 arguments 的紧凑 JSON 表示并截断到 300 字。
			// 分隔符与转义必须一致（紧凑格式会全量不一致）。
			// ⚠️ 这里**不做**键排序，用的是 JSON 源文件里的键序；
			// Go 的 map 不保留键序，此处按有序输出，属已知的显示层差异（见报告）。
			evi := models.JSONDumps(tc.Arguments)
			if len([]rune(evi)) > 300 {
				evi = string([]rune(evi)[:300])
			}
			out = append(out, makeFinding("EMPTY_REQUIRED_ARG", trace,
				fmt.Sprintf("%s 必填参数为空仍被放行：%s", tc.Name, strings.Join(tc.EmptyRequiredArg, ", ")),
				tc.Name, &step, evi))
		}
	}
	return out
}

// CheckOrphan 链路孤儿调用检查
func CheckOrphan(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	if len(trace.AssociatedToolCallIDs) == 0 && len(trace.ToolCalls) == 0 {
		return nil
	}
	diff := trace.OrphanIDs()
	if len(diff) > 0 {
		evi := strings.Join(diff, ", ")
		if len(diff) > 6 {
			evi = strings.Join(diff[:6], ", ") + "..."
		}
		return []*models.Finding{
			makeFinding("ORPHAN_TOOL_CALL", trace,
				fmt.Sprintf("associatedToolCallIds 与 timelineSteps 的调用 ID 不匹配（声明 %d / 实际 %d）",
					len(trace.AssociatedToolCallIDs), len(trace.ToolCalls)),
				"", nil, evi),
		}
	}
	return nil
}

// CheckMaxIterations 触达迭代上限检查
func CheckMaxIterations(trace *models.ExecutionTrace, limit int, cfg map[string]any) []*models.Finding {
	if limit > 0 && len(trace.ToolCalls) >= limit {
		step := trace.ToolCalls[len(trace.ToolCalls)-1].Index
		return []*models.Finding{
			makeFinding("MAX_ITERATIONS_HIT", trace,
				fmt.Sprintf("工具调用步数 %d 已触达 max_iterations=%d", len(trace.ToolCalls), limit),
				"", &step, ""),
		}
	}
	return nil
}

// CheckFinalAnswer 无最终答复检查
func CheckFinalAnswer(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	if strings.TrimSpace(trace.FinalAnswer) == "" {
		return []*models.Finding{
			makeFinding("NO_FINAL_ANSWER", trace, "会话结束但未产出任何最终答复", "", nil, ""),
		}
	}
	return nil
}

func unclosedStructure(text string) string {
	fence := strings.Count(text, "```")
	if fence%2 == 1 {
		return fmt.Sprintf("代码块 ``` 出现 %d 次（奇数，未闭合）", fence)
	}
	lines := strings.Split(text, "\n")
	var nonEmptyLines []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmptyLines = append(nonEmptyLines, l)
		}
	}
	if len(nonEmptyLines) > 0 {
		last := nonEmptyLines[len(nonEmptyLines)-1]
		if strings.HasPrefix(strings.TrimLeft(last, " \t"), "|") && strings.HasSuffix(strings.TrimRight(last, " \t"), "|") {
			return "结尾停在 Markdown 表格行，表格未收尾"
		}
	}
	if strings.Count(text, "![") > strings.Count(text, ")") && strings.Count(text, "![") > 0 {
		return "存在未闭合的图片语法 ![...](...)"
	}
	return ""
}

type truncSignal struct {
	name   string
	detail string
}

// toRepr 生成字符串的带引号转义形态，供截断信号的「实际结尾」摘录使用。
//
// 结尾摘录取 {body[-60:]} 并按此形态转义，所以换行会显示成字面量 \n、反斜杠变成 \\，
// 而可打印的非 ASCII 字符（中文、emoji）保持原样（不做 ASCII 转义）。
// Go 若直接用 %s 输出原始切片，凡是含换行的最终答案都会全量不一致。
func toRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = '"'
	}
	var sb strings.Builder
	sb.WriteByte(quote)
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 128 && byte(r) == quote {
				sb.WriteByte('\\')
				sb.WriteByte(quote)
			} else if r < 0x20 || r == 0x7f {
				sb.WriteString(fmt.Sprintf(`\x%02x`, r))
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte(quote)
	return sb.String()
}

func truncationSignals(text string, trace *models.ExecutionTrace, cfg map[string]any) []truncSignal {
	var ocfg map[string]any
	if v, ok := cfg["output_truncation"].(map[string]any); ok {
		ocfg = v
	}

	var caps []int
	if cList, ok := cfg["truncation_caps"].([]any); ok {
		for _, c := range cList {
			if n, ok := c.(int); ok {
				caps = append(caps, n)
			}
		}
	} else if cList, ok := cfg["truncation_caps"].([]int); ok {
		caps = cList
	}
	if len(caps) == 0 {
		caps = []int{500, 1000, 2000, 4000, 8000, 16000}
	}

	var naturalEndings []string
	if eList, ok := cfg["natural_endings"].([]any); ok {
		for _, e := range eList {
			naturalEndings = append(naturalEndings, fmt.Sprintf("%v", e))
		}
	} else if eList, ok := cfg["natural_endings"].([]string); ok {
		naturalEndings = eList
	}

	tol := 0.02
	if v, ok := ocfg["cap_tolerance"].(float64); ok && v > 0 {
		tol = v
	}
	minLen := 300
	if v, ok := ocfg["min_len_for_dangling"].(int); ok && v > 0 {
		minLen = v
	}
	var dangling []string
	if dList, ok := ocfg["dangling_tails"].([]any); ok {
		for _, d := range dList {
			dangling = append(dangling, fmt.Sprintf("%v", d))
		}
	} else if dList, ok := ocfg["dangling_tails"].([]string); ok {
		dangling = dList
	}

	body := strings.TrimRight(text, " \t\r\n")
	bodyRunes := []rune(body)
	n := len(bodyRunes)

	tailRunes := func(k int) string {
		if k > n {
			k = n
		}
		return string(bodyRunes[n-k:])
	}

	var hits []truncSignal

	// 信号 A：流式未结束
	if trace.IsStreaming {
		hits = append(hits, truncSignal{"流式未结束", "会话 isStreaming=true，内容尚未生成完就被采集"})
	}

	// 信号 B：缺 completedAt
	if trace.CompletedAt == "" {
		hits = append(hits, truncSignal{"缺完成标记", "消息无 completedAt 字段，说明生成未走完正常结束流程"})
	}

	// 信号 C：结尾为接续词
	if n >= minLen && len(bodyRunes) > 0 {
		for _, d := range dangling {
			if strings.HasSuffix(body, d) {
				hits = append(hits, truncSignal{
					"结尾为接续词",
					fmt.Sprintf("以 %s 收尾（共 %d 字），该字/词后必须还有下文，实际结尾：…%s", toRepr(d), n, toRepr(tailRunes(60))),
				})
				break
			}
		}
	}

	// 信号 D：命中截断上限
	for _, c := range caps {
		lo := float64(c) * (1.0 - tol)
		hi := float64(c) * (1.0 + tol)
		if float64(n) >= lo && float64(n) <= hi {
			hasNaturalEnding := false
			for _, ending := range naturalEndings {
				if strings.HasSuffix(body, ending) {
					hasNaturalEnding = true
					break
				}
			}
			if !hasNaturalEnding {
				pct := float64(n-c) / float64(c) * 100.0
				hits = append(hits, truncSignal{
					"命中截断上限",
					fmt.Sprintf("长度 %d 字落在上限 %d 的 ±%.0f%% 范围内（偏差 %+.1f%%），且结尾无自然收尾符，实际结尾：…%s",
						n, c, tol*100, pct, toRepr(tailRunes(60))),
				})
				break
			}
		}
	}

	// 信号 E：结构未闭合
	unclosed := unclosedStructure(text)
	if unclosed != "" {
		hits = append(hits, truncSignal{"结构未闭合", fmt.Sprintf("%s，实际结尾：…%s", unclosed, toRepr(tailRunes(60)))})
	}

	return hits
}

// CheckOutputTruncated 最终答案完结率检查
func CheckOutputTruncated(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	ans := strings.TrimSpace(trace.FinalAnswer)
	if ans == "" {
		return nil
	}
	signals := truncationSignals(ans, trace, cfg)
	if len(signals) == 0 {
		return nil
	}

	var names []string
	var details []string
	for _, s := range signals {
		names = append(names, s.name)
		details = append(details, fmt.Sprintf("【%s】%s", s.name, s.detail))
	}
	detailStr := fmt.Sprintf("最终答案疑似被强制截断（命中信号：%s；共 %d 字）。%s",
		strings.Join(names, "、"), utf8.RuneCountInString(ans), strings.Join(details, " "))

	ansRunes := []rune(ans)
	evidence := ans
	if len(ansRunes) > 200 {
		evidence = string(ansRunes[len(ansRunes)-200:])
	}

	return []*models.Finding{
		makeFinding("OUTPUT_TRUNCATED", trace, detailStr, "", nil, evidence),
	}
}

// CheckClosed 会话未关闭检查
func CheckClosed(trace *models.ExecutionTrace, cfg map[string]any) []*models.Finding {
	if trace.IsStreaming {
		return []*models.Finding{
			makeFinding("SESSION_NOT_CLOSED", trace, "会话仍处于流式未结束状态（isStreaming=true）", "", nil, ""),
		}
	}
	return nil
}

// RunAssertions 对单个会话执行全部断言规则
func RunAssertions(trace *models.ExecutionTrace, cfg map[string]any, maxIterations int) []*models.Finding {
	var findings []*models.Finding

	for _, tc := range trace.ToolCalls {
		findings = append(findings, CheckToolFailed(tc, trace, cfg)...)
	}

	findings = append(findings, CheckConsecutiveLoop(trace, cfg)...)
	findings = append(findings, CheckTotalLoop(trace, cfg)...)
	findings = append(findings, CheckDuplicate(trace, cfg)...)
	findings = append(findings, CheckEmptyArgs(trace, cfg)...)
	findings = append(findings, CheckOrphan(trace, cfg)...)
	findings = append(findings, CheckMaxIterations(trace, maxIterations, cfg)...)
	findings = append(findings, CheckFinalAnswer(trace, cfg)...)
	findings = append(findings, CheckOutputTruncated(trace, cfg)...)
	findings = append(findings, CheckClosed(trace, cfg)...)

	return findings
}
