package metrics

import (
	"fmt"
	"math"
	"sort"
	"time"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/assertor"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/trajectory"
)

var severityOrder = map[string]int{
	"P0": 0, "P1": 1, "P2": 2,
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0.0
	}
	return math.Round(float64(a)*1000.0/float64(b)) / 10.0
}

func ReproBlock(recipes []*models.Recipe) ([]map[string]any, map[string]any) {
	var rows []map[string]any
	for _, r := range recipes {
		if r.TotalRuns == 0 {
			continue
		}
		var sessions []string
		for _, run := range r.Runs {
			if run.SessionID != "" {
				sessions = append(sessions, run.SessionID)
			}
		}
		rows = append(rows, map[string]any{
			"key":       fmt.Sprintf("%s:%s", r.Rule, r.Tool),
			"severity":  r.Severity,
			"name":      r.Detail,
			"tool":      r.Tool,
			"attempts":  r.TotalRuns,
			"hits":      r.FailedRuns,
			"rate":      r.Rate,
			"stability": r.Status,
			"sessions":  sessions,
		})
	}

	stableCount := 0
	likelyCount := 0
	flakyCount := 0
	rateSum := 0.0
	for _, r := range rows {
		st := fmt.Sprintf("%v", r["stability"])
		if st == "必现" || st == "stable" {
			stableCount++
		} else if st == "高概率复现" || st == "likely" {
			likelyCount++
		} else if st == "偶发" || st == "flaky" {
			flakyCount++
		}
		if rt, ok := r["rate"].(float64); ok {
			rateSum += rt
		}
	}

	var avgRate *float64
	if len(rows) > 0 {
		v := math.Round(rateSum/float64(len(rows))*1000) / 1000
		avgRate = &v
	}

	summary := map[string]any{
		"verified": len(rows),
		"stable":   stableCount,
		"likely":   likelyCount,
		"flaky":    flakyCount,
		"avg_rate": avgRate,
	}
	return rows, summary
}

// BuildMetrics 生成测试报告与轮次归档所需的全部指标
func BuildMetrics(caseResults []*models.CaseResult, cfg map[string]any, recipes []*models.Recipe, stageTimes map[string]float64) map[string]any {
	var findings []*models.Finding
	var uiFailed []*models.CaseResult
	var passed []*models.CaseResult
	var failed []*models.CaseResult

	for _, c := range caseResults {
		findings = append(findings, c.Findings...)
		if !c.UIOk {
			uiFailed = append(uiFailed, c)
		} else if c.Passed() {
			passed = append(passed, c)
		} else {
			failed = append(failed, c)
		}
	}

	// 规则维度
	ruleCounter := make(map[string]int)
	for _, f := range findings {
		ruleCounter[f.Rule]++
	}
	var ruleRows []map[string]any
	for rule, meta := range assertor.Rules {
		cnt := ruleCounter[rule]
		ruleRows = append(ruleRows, map[string]any{
			"rule":     rule,
			"name":     meta.Label,
			"severity": meta.Severity,
			"count":    cnt,
		})
	}
	sort.Slice(ruleRows, func(i, j int) bool {
		sI := severityOrder[fmt.Sprintf("%v", ruleRows[i]["severity"])]
		sJ := severityOrder[fmt.Sprintf("%v", ruleRows[j]["severity"])]
		if sI != sJ {
			return sI < sJ
		}
		return ruleRows[i]["count"].(int) > ruleRows[j]["count"].(int)
	})

	sevCounter := make(map[string]int)
	for _, f := range findings {
		sevCounter[f.Severity]++
	}
	severity := []map[string]any{
		{"severity": "P0", "count": sevCounter["P0"]},
		{"severity": "P1", "count": sevCounter["P1"]},
		{"severity": "P2", "count": sevCounter["P2"]},
	}

	// 工具维度
	callTotal := 0
	toolCounter := make(map[string]int)
	toolFailed := make(map[string]int)
	toolTruncated := make(map[string]int)

	for _, c := range caseResults {
		if c.Trace != nil {
			for _, tc := range c.Trace.ToolCalls {
				callTotal++
				toolCounter[tc.Name]++
			}
		}
	}
	for _, f := range findings {
		if f.Rule == "TOOL_CALL_FAILED" || f.Rule == "TOOL_RESULT_MISSING" {
			toolFailed[f.Tool]++
		}
	}

	type toolCountItem struct {
		Name  string
		Count int
	}
	var toolSorted []toolCountItem
	for nm, c := range toolCounter {
		toolSorted = append(toolSorted, toolCountItem{Name: nm, Count: c})
	}
	sort.Slice(toolSorted, func(i, j int) bool {
		return toolSorted[i].Count > toolSorted[j].Count
	})

	var toolRows []map[string]any
	for _, item := range toolSorted {
		fl := toolFailed[item.Name]
		tr := toolTruncated[item.Name]
		toolRows = append(toolRows, map[string]any{
			"tool":      item.Name,
			"calls":     item.Count,
			"failed":    fl,
			"truncated": tr,
			"fail_rate": pct(fl, item.Count),
		})
	}

	// 工具覆盖度
	var expected []string
	expectedSet := make(map[string]bool)
	usedSet := make(map[string]bool)
	for nm := range toolCounter {
		usedSet[nm] = true
	}
	for _, c := range caseResults {
		for _, t := range c.ExpectedTools {
			if !expectedSet[t] {
				expectedSet[t] = true
				expected = append(expected, t)
			}
		}
	}
	var covered []string
	var missing []string
	for _, t := range expected {
		if usedSet[t] {
			covered = append(covered, t)
		} else {
			missing = append(missing, t)
		}
	}
	var usedList []string
	for t := range usedSet {
		usedList = append(usedList, t)
	}
	sort.Strings(usedList)

	coverage := map[string]any{
		"expected":      expected,
		"used":          usedList,
		"covered":       covered,
		"missing":       missing,
		"rate":          pct(len(covered), len(expected)),
		"distinct_used": len(usedList),
	}

	// 客观度量
	caseObjective := make(map[string]any)
	for _, c := range caseResults {
		caseObjective[c.CaseID] = trajectory.BuildObjective(c, stageTimes)
	}

	turns := 0
	toolCallsCount := 0
	thinkSteps := 0
	failsCount := 0
	truncsCount := 0
	inTok := 0
	outTok := 0
	var firstResponses []float64

	for _, rawObj := range caseObjective {
		obj, ok := rawObj.(map[string]any)
		if !ok {
			continue
		}
		if req, ok := obj["requests"].(map[string]any); ok {
			turns += getInt(req["turns"])
			toolCallsCount += getInt(req["tool_calls"])
			thinkSteps += getInt(req["thinking_steps"])
		}
		if st, ok := obj["status"].(map[string]any); ok {
			failsCount += getInt(st["tool_fail"])
			truncsCount += getInt(st["tool_truncated"])
		}
		if vol, ok := obj["volume"].(map[string]any); ok {
			inTok += getInt(vol["input_tokens_est"])
			outTok += getInt(vol["output_tokens_est"])
		}
		if tm, ok := obj["timing"].(map[string]any); ok {
			if fr, ok := tm["first_response_s"].(float64); ok && fr >= 0 {
				firstResponses = append(firstResponses, fr)
			}
		}
	}

	var avgFirstResponse *float64
	if len(firstResponses) > 0 {
		sum := 0.0
		for _, v := range firstResponses {
			sum += v
		}
		v := math.Round(sum/float64(len(firstResponses))*100) / 100
		avgFirstResponse = &v
	}

	objective := map[string]any{
		"requests": map[string]any{
			"turns":          turns,
			"tool_calls":     toolCallsCount,
			"thinking_steps": thinkSteps,
			"llm_responses":  turns,
		},
		"status": map[string]any{
			"tool_fail":       failsCount,
			"tool_truncated":  truncsCount,
			"error_rate":      pct(failsCount, toolCallsCount),
			"truncated_rate":  pct(truncsCount, toolCallsCount),
			"sessions_closed": len(caseResults) - len(uiFailed),
			"sessions_total":  len(caseResults),
		},
		"tokens": map[string]any{
			"input_est":  inTok,
			"output_est": outTok,
			"total_est":  inTok + outTok,
			"note":       "估算值：日志未记录官方 usage；输入=提示词+工具结果回灌，输出=思考+答复+调用参数（CJK≈1.6字符/token）",
		},
		"timing": map[string]any{
			"avg_first_response_s": avgFirstResponse,
			"stage_times":          stageTimes,
			"note":                 "步骤级耗时日志未记录，仅提供会话/消息级耗时与平台实测耗时",
		},
	}

	// 用例行列表
	var caseRows []map[string]any
	for _, c := range caseResults {
		tr := c.Trace
		toolNames := []string{}
		thinkingCount := 0
		toolCalls := 0
		answerChars := 0
		if tr != nil {
			toolNames = tr.ToolNames()
			thinkingCount = len(tr.ThinkingSteps)
			toolCalls = len(tr.ToolCalls)
			answerChars = utf8.RuneCountInString(tr.FinalAnswer)
		}
		p0Count := 0
		p1Count := 0
		for _, f := range c.Findings {
			if f.Severity == "P0" {
				p0Count++
			} else if f.Severity == "P1" {
				p1Count++
			}
		}
		caseRows = append(caseRows, map[string]any{
			"case_id":        c.CaseID,
			"name":           c.Name,
			"prompt":         c.Prompt,
			"status":         c.Status(),
			"session_id":     c.SessionID,
			"tool_calls":     toolCalls,
			"thinking_steps": thinkingCount,
			"findings":       len(c.Findings),
			"p0":             p0Count,
			"p1":             p1Count,
			"elapsed_s":      trajectory.RoundToOneDecimal(c.ElapsedS),
			"ui_error":       c.UIError,
			"waited_limit":   c.WaitedLimit,
			"wait_note":      c.WaitNote,
			"auto_confirms":  c.AutoConfirms,
			"tools":          toolNames,
			"answer_chars":   answerChars,
		})
	}

	// 问题发现行列表
	var findingsRows []map[string]any
	sort.Slice(findings, func(i, j int) bool {
		sI := severityOrder[findings[i].Severity]
		sJ := severityOrder[findings[j].Severity]
		if sI != sJ {
			return sI < sJ
		}
		return findings[i].Rule < findings[j].Rule
	})

	for _, f := range findings {
		ruleName := f.Rule
		if r, ok := assertor.Rules[f.Rule]; ok {
			ruleName = r.Label
		}
		cID := ""
		for _, c := range caseResults {
			if c.SessionID == f.SessionID {
				cID = c.CaseID
				break
			}
		}
		findingsRows = append(findingsRows, map[string]any{
			"rule":       f.Rule,
			"name":       ruleName,
			"severity":   f.Severity,
			"session_id": f.SessionID,
			"tool":       f.Tool,
			"detail":     f.Detail,
			"impact":     trajectory.RuleImpact[f.Rule],
			"evidence":   f.Evidence,
			"step_index": f.StepIndex,
			"case_id":    cID,
		})
	}

	reproRows, reproSummary := ReproBlock(recipes)

	totalElapsed := 0.0
	if stageTimes != nil {
		totalElapsed = stageTimes["total"]
	}

	return map[string]any{
		"generated_at": time.Now().Format("2006-01-02 15:04:05"),
		"summary": map[string]any{
			"cases":                len(caseResults),
			"passed":               len(passed),
			"failed":               len(failed),
			"ui_failed":            len(uiFailed),
			"pass_rate":            pct(len(passed), len(caseResults)),
			"findings":             len(findings),
			"p0":                   sevCounter["P0"],
			"p1":                   sevCounter["P1"],
			"tool_calls_total":     callTotal,
			"tool_calls_failed":    failsCount,
			"tool_fail_rate":       pct(failsCount, callTotal),
			"tool_calls_truncated": truncsCount,
			"elapsed_total_s":      trajectory.RoundToOneDecimal(totalElapsed),
		},
		"objective":      objective,
		"case_objective": caseObjective,
		"rule_rows":      ruleRows,
		"severity":       severity,
		"tool_rows":      toolRows,
		"coverage":       coverage,
		"case_rows":      caseRows,
		"findings_rows":  findingsRows,
		"repro_rows":     reproRows,
		"repro_summary":  reproSummary,
	}
}

func getInt(v any) int {
	if n, ok := v.(int); ok {
		return n
	}
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
