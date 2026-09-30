package metrics

import (
	"fmt"
	"sort"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/assertor"
	"ruiyun-ui-test-platform-go/internal/canon"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/repro"
	"ruiyun-ui-test-platform-go/internal/trajectory"
)

// 平台不给问题分严重级别，只按规则表的固定顺序展示：
// 越靠前＝越贴近「调用根本没跑通」这类硬缺陷。排序键由 assertor.RuleRank 统一提供。

// pct 计算 a 占 b 的百分比：round(a * 100.0 / b, 1)，b 为 0 时返回 0.0。
//
// 注意两点：中间量是 a*100.0/b（不是 a*1000.0/b 再除 10），
// 舍入是银行家舍入 —— 写成 math.Round 会在恰好半值处产生 ties 方向错误。
func pct(a, b int) float64 {
	if b == 0 {
		return 0.0
	}
	return trajectory.RoundTo(float64(a)*100.0/float64(b), 1)
}

// ReproBlock 生成复现行的明细与汇总。
//
// 输入是流水线产出的 ReproRecipe（**不是**旧的 models.Recipe）——
// 读取的是 rd["key"] / rd["rule_name"] / rd["run_sessions"]，
// 用错类型会让报告里的「复现方法」整列错位。
func ReproBlock(recipes []*repro.ReproRecipe) ([]map[string]any, map[string]any) {
	rows := []map[string]any{}
	for _, r := range recipes {
		if r == nil || r.Attempts == 0 {
			continue
		}
		sessions := []string{}
		for _, run := range r.RunSessions {
			if run.SessionID != "" {
				sessions = append(sessions, run.SessionID)
			}
		}
		var rate any = 0.0
		if r.Rate != nil {
			rate = *r.Rate
		}
		rows = append(rows, map[string]any{
			"key":       r.Key,
			"name":      r.RuleName,
			"tool":      r.Tool,
			"attempts":  r.Attempts,
			"hits":      r.Hits,
			"rate":      rate,
			"stability": r.Stability,
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
		v := canon.Round(rateSum/float64(len(rows)), 3) // round 到 3 位小数
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

// ReproSubBlock 生成单条报错发现的 repro 子块
func ReproSubBlock(r *repro.ReproRecipe) map[string]any {
	runSessions := []map[string]any{}
	for _, s := range r.RunSessions {
		item := map[string]any{"session_id": s.SessionID, "hit": s.Hit}
		if s.Note != "" {
			item["note"] = s.Note
		}
		runSessions = append(runSessions, item)
	}
	var rate any
	if r.Rate != nil {
		rate = *r.Rate
	}
	return map[string]any{
		"prompt":        r.Prompt,
		"prompt_source": r.PromptSource,
		"verify_desc":   r.VerifyDesc,
		"expected":      r.Expected,
		"actual":        r.Actual,
		"attempts":      r.Attempts,
		"hits":          r.Hits,
		"rate":          rate,
		"stability":     r.Stability,
		"run_sessions":  runSessions,
	}
}

// BuildMetrics 生成测试报告与轮次归档所需的全部指标
func BuildMetrics(caseResults []*models.CaseResult, cfg map[string]any, recipes []*repro.ReproRecipe, stageTimes map[string]float64) map[string]any {
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
	ruleRows := []map[string]any{}
	// 按规则注册表 RuleOrder 的**声明顺序**生成，再稳定排序；
	// range map 会随机化顺序，sort.Slice 又是不稳定排序 —— 两处都得改。
	for _, rule := range assertor.RuleOrder {
		ruleRows = append(ruleRows, map[string]any{
			"rule":  rule,
			"name":  assertor.Rules[rule].Label,
			"count": ruleCounter[rule],
		})
	}
	// 命中数多的排前面；数量相同则保持规则表声明顺序（SliceStable + 上文的生成顺序）
	sort.SliceStable(ruleRows, func(i, j int) bool {
		return ruleRows[i]["count"].(int) > ruleRows[j]["count"].(int)
	})

	// 命中的规则种类数（去重计数），报告与界面用它替代原来的 P0/P1 分级脚注
	ruleKinds := 0
	for _, n := range ruleCounter {
		if n > 0 {
			ruleKinds++
		}
	}

	// 工具维度
	callTotal := 0
	toolCounter := make(map[string]int)
	var toolOrder []string
	toolFailed := make(map[string]int)
	toolTruncated := make(map[string]int)

	for _, c := range caseResults {
		if c.Trace != nil {
			for _, tc := range c.Trace.ToolCalls {
				callTotal++
				if _, seen := toolCounter[tc.Name]; !seen {
					toolOrder = append(toolOrder, tc.Name)
				}
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
	// 按次数降序，**并列时保持首次出现顺序**（稳定）
	toolSorted := []toolCountItem{}
	for _, nm := range toolOrder {
		toolSorted = append(toolSorted, toolCountItem{Name: nm, Count: toolCounter[nm]})
	}
	sort.SliceStable(toolSorted, func(i, j int) bool {
		return toolSorted[i].Count > toolSorted[j].Count
	})

	toolRows := []map[string]any{}
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
	expected := []string{}
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
	covered := []string{}
	missing := []string{}
	for _, t := range expected {
		if usedSet[t] {
			covered = append(covered, t)
		} else {
			missing = append(missing, t)
		}
	}
	usedList := []string{}
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
		v := canon.Round(sum/float64(len(firstResponses)), 2) // round 到 2 位小数
		avgFirstResponse = &v
	}

	// sessions_closed：统计 objective.status.session_closed 为真的用例数
	closedCount := 0
	for _, o := range caseObjective {
		obj, _ := o.(map[string]any)
		if obj == nil {
			continue
		}
		if st, ok := obj["status"].(map[string]any); ok {
			if b, ok := st["session_closed"].(bool); ok && b {
				closedCount++
			}
		}
	}

	// Skill 使用（仅本轮，从 read_skill_file 返回解析）
	// skill_rows 是**列表**，按 key 首次出现顺序；sessions 是该 skill 覆盖的用例号（已排序）
	type skillRowAgg struct {
		name     string
		sessions map[string]bool
		files    []string
		calls    int
	}
	skillAgg := map[string]*skillRowAgg{}
	var skillRowOrder []string
	for _, c := range caseResults {
		if c.Trace == nil {
			continue
		}
		for _, tc := range c.Trace.ToolCalls {
			sn := trajectory.SkillName(tc)
			if sn == "" && tc.Name != "read_skill_file" {
				continue
			}
			key := sn
			if key == "" {
				key = trajectory.UnknownSkill
			}
			row, ok := skillAgg[key]
			if !ok {
				row = &skillRowAgg{name: key, sessions: map[string]bool{}, files: []string{}}
				skillAgg[key] = row
				skillRowOrder = append(skillRowOrder, key)
			}
			row.calls++
			row.sessions[c.CaseID] = true
			for _, fp := range trajectory.SkillFiles(tc) {
				row.files = appendUniqueStr(row.files, fp)
			}
		}
	}
	skillRows := []map[string]any{}
	for _, key := range skillRowOrder {
		row := skillAgg[key]
		ss := []string{}
		for sid := range row.sessions {
			ss = append(ss, sid)
		}
		sort.Strings(ss)
		skillRows = append(skillRows, map[string]any{
			"name": row.name, "sessions": ss, "files": row.files, "calls": row.calls,
		})
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
			"sessions_closed": closedCount,
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
		caseRows = append(caseRows, map[string]any{
			"case_id":        c.CaseID,
			"name":           c.Name,
			"prompt":         c.Prompt,
			"status":         c.Status(),
			"session_id":     c.SessionID,
			"tool_calls":     toolCalls,
			"thinking_steps": thinkingCount,
			"findings":       len(c.Findings),
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
	findingsRows := []map[string]any{}
	// 复现配方索引：key 恒为 "<rule>:<tool 或 '-'>"
	recipeByKey := map[string]*repro.ReproRecipe{}
	for _, r := range recipes {
		if r != nil {
			recipeByKey[r.Key] = r
		}
	}
	// 必须稳定排序：按规则表的声明顺序归拢，同规则的发现行保持原序（用例顺序）。
	// 用 sort.Slice 会让同规则的发现行随机换位。
	sort.SliceStable(findings, func(i, j int) bool {
		return assertor.RuleRank(findings[i].Rule) < assertor.RuleRank(findings[j].Rule)
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
		row := map[string]any{
			"rule":       f.Rule,
			"name":       ruleName,
			"session_id": f.SessionID,
			"tool":       f.Tool,
			"detail":     f.Detail,
			"impact":     trajectory.RuleImpact[f.Rule],
			"evidence":   f.Evidence,
			"step_index": f.StepIndex,
			"case_id":    cID,
		}
		// 按 "<rule>:<tool 或 '-'>" 组装索引键
		toolKey := f.Tool
		if toolKey == "" {
			toolKey = "-"
		}
		if rep, ok := recipeByKey[f.Rule+":"+toolKey]; ok {
			row["repro"] = ReproSubBlock(rep)
		}
		findingsRows = append(findingsRows, row)
	}

	reproRows, reproSummary := ReproBlock(recipes)

	totalElapsed := 0.0
	if stageTimes != nil {
		totalElapsed = stageTimes["total"]
	}

	return map[string]any{
		"generated_at": "", // 恒为空串（时间由上层写文件时决定）
		"summary": map[string]any{
			"cases":                len(caseResults),
			"passed":               len(passed),
			"failed":               len(failed),
			"ui_failed":            len(uiFailed),
			"pass_rate":            pct(len(passed), len(caseResults)),
			"findings":             len(findings),
			"rule_kinds":           ruleKinds,
			"tool_calls_total":     callTotal,
			"tool_calls_failed":    failsCount,
			"tool_fail_rate":       pct(failsCount, callTotal),
			"tool_calls_truncated": truncsCount,
			"elapsed_total_s":      trajectory.RoundToOneDecimal(totalElapsed),
		},
		"objective":      objective,
		"case_objective": caseObjective,
		"skill_rows":     skillRows,
		"rule_rows":      ruleRows,
		"tool_rows":      toolRows,
		"coverage":       coverage,
		"case_rows":      caseRows,
		"findings_rows":  findingsRows,
		"repro_rows":     nonNilRows(reproRows),
		"repro_summary":  reproSummary,
	}
}

func appendUniqueStr(list []string, v string) []string {
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

func nonNilRows(rows []map[string]any) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	return rows
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
