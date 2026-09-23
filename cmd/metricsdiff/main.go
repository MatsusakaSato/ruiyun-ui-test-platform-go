// Command metricsdiff 是 P0 差分验证工装的「指标层」探针。
//
// 用法: metricsdiff <sessionRoot> <rulesJSONPath> <inputsJSONPath> <maxIterations>
//
// 对每个真实会话走完整链路：
//
//	解析轨迹(logparser) → 跑断言(assertor) → 构造 CaseResult
//	→ BuildCaseDetail(逐用例) → BuildRoundDetail / BuildMetrics(整轮聚合)
//
// 用例上那些**来自轨迹之外**的字段（case_id / elapsed_s / 附件 / 确认事件…）
// 由 inputsJSON 提供，与 Python 侧读同一份文件，保证输入逐字节一致。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ruiyun-ui-test-platform-go/internal/assertor"
	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/metrics"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/trajectory"
)

func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "用法: metricsdiff <sessionRoot> <rulesJSON> <inputsJSON> <maxIterations>")
		os.Exit(2)
	}
	root, rulesPath, inputsPath := os.Args[1], os.Args[2], os.Args[3]
	maxIter, _ := strconv.Atoi(os.Args[4])

	readJSON := func(p string, dst any) {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", p, err)
			os.Exit(1)
		}
		if err := json.Unmarshal(b, dst); err != nil {
			fmt.Fprintf(os.Stderr, "解析 %s 失败: %v\n", p, err)
			os.Exit(1)
		}
	}
	var rules map[string]any
	readJSON(rulesPath, &rules)
	var inp struct {
		Cfg      map[string]any            `json:"cfg"`
		Sessions map[string]map[string]any `json:"sessions"`
		Order    []string                  `json:"order"`
	}
	readJSON(inputsPath, &inp)

	names := inp.Order
	if len(names) == 0 {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "sess_") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
	}

	var caseResults []*models.CaseResult
	perCase := map[string]any{}
	parseErrs := map[string]string{}

	for _, name := range names {
		meta := inp.Sessions[name]
		trace, err := logparser.ParseSession(filepath.Join(root, name))
		if err != nil || trace == nil {
			msg := "nil trace"
			if err != nil {
				msg = err.Error()
			}
			parseErrs[name] = msg
			continue
		}

		findings := assertor.RunAssertions(trace, rules, maxIter)
		cr := &models.CaseResult{
			Trace:     trace,
			Findings:  findings,
			SessionID: trace.SessionID,
		}
		// 轨迹之外、由外部输入决定的字段
		if v, ok := meta["case_id"].(string); ok {
			cr.CaseID = v
		}
		if v, ok := meta["name"].(string); ok {
			cr.Name = v
		}
		if v, ok := meta["prompt"].(string); ok {
			cr.Prompt = v
		}
		if v, ok := meta["ui_ok"].(bool); ok {
			cr.UIOk = v
		}
		if v, ok := meta["ui_error"].(string); ok {
			cr.UIError = v
		}
		if v, ok := meta["elapsed_s"].(float64); ok {
			cr.ElapsedS = v
		}
		if v, ok := meta["auto_confirms"].(float64); ok {
			cr.AutoConfirms = int(v)
		}
		if v, ok := meta["attach_note"].(string); ok {
			cr.AttachNote = v
		}
		if v, ok := meta["wait_note"].(string); ok {
			cr.WaitNote = v
		}
		if v, ok := meta["waited_limit"].(bool); ok {
			cr.WaitedLimit = v
		}
		if arr, ok := meta["attachments"].([]any); ok {
			for _, a := range arr {
				if s, ok := a.(string); ok {
					cr.Attachments = append(cr.Attachments, s)
				}
			}
		}
		if arr, ok := meta["confirm_events"].([]any); ok {
			cr.ConfirmEvents = arr
		}
		if arr, ok := meta["expected_tools"].([]any); ok {
			for _, a := range arr {
				if s, ok := a.(string); ok {
					cr.ExpectedTools = append(cr.ExpectedTools, s)
				}
			}
		}
		caseResults = append(caseResults, cr)
	}

	// 整轮的 findings-by-step 索引（键格式与 trajectory.stepStatus 内部一致："<sessionID>:<step>"）
	byStep := map[string][]*models.Finding{}
	for _, c := range caseResults {
		for _, f := range c.Findings {
			if f.StepIndex != nil {
				key := fmt.Sprintf("%s:%d", c.SessionID, *f.StepIndex)
				byStep[key] = append(byStep[key], f)
			}
		}
	}
	for _, c := range caseResults {
		d := trajectory.BuildCaseDetail(c, byStep, inp.Cfg)
		perCase[c.CaseID] = d
	}

	roundDetail := trajectory.BuildRoundDetail(caseResults, nil, nil, map[string]float64{}, inp.Cfg)
	md := metrics.BuildMetrics(caseResults, inp.Cfg, nil, map[string]float64{})

	delete(roundDetail, "cases") // 逐用例已单独哈希

	// 交叉校验用：每个步骤参数的**未截断、键已排序**规范形。
	// args_summary / evidence 都是 json.dumps(arguments) 的截断展示；
	// 两侧这一份完全相等，即可证明那两个字段的唯一差异就是「键序」。
	argsCanon := map[string]map[string]string{}
	for _, c := range caseResults {
		m := map[string]string{}
		if c.Trace != nil {
			for _, tc := range c.Trace.ToolCalls {
				m[strconv.Itoa(tc.Index)] = models.PyJSONDumps(tc.Arguments)
			}
		}
		argsCanon[c.CaseID] = m
	}

	out := map[string]any{
		"args_canon":   argsCanon,
		"cases":        perCase,
		"parse_errors": parseErrs,
		"round_detail": roundDetail,
		"metrics":      md,
		"case_count":   len(caseResults),
	}
	b, _ := json.Marshal(out)
	var norm any
	_ = json.Unmarshal(b, &norm)
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	_ = enc.Encode(norm)
	os.Stdout.WriteString(sb.String())
}
