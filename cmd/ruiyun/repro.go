package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ruiyun-ui-test-platform-go/internal/assertor"
	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/driver"
	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/metrics"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/report"
	"ruiyun-ui-test-platform-go/internal/repro"
	"ruiyun-ui-test-platform-go/internal/sysutil"
)

// runRepro 对指定 bug 签名做 N 次 UI 自动化复现，
// 量化稳定性。支持 --list（零成本列配方）/ --render-only（不驱动 UI 重渲染报告）。
func runRepro(args []string) int {
	fs := flag.NewFlagSet("repro", flag.ExitOnError)
	cfgPath := fs.String("config", config.ConfigPath(), "配置文件路径")
	listOnly := fs.Bool("list", false, "仅列出复现配方，不执行")
	rule := fs.String("rule", "", "限定断言规则，如 TOOL_CALL_FAILED")
	tool := fs.String("tool", "", "限定工具名，如 read_memory")
	all := fs.Bool("all", false, "验证全部 bug 签名")
	times := fs.Int("times", 3, "每个配方重复次数")
	keepApp := fs.Bool("keep-app", false, "[已废弃] 应用常驻不关闭，该参数无任何作用")
	updateReport := fs.Bool("update-report", false, "验证后重渲染 HTML 报告")
	renderOnly := fs.Bool("render-only", false,
		"不驱动 UI，用上次验证结果（artifacts/repro_results.json）重渲染报告")
	_ = fs.Parse(args)

	if *keepApp {
		fmt.Println("提示：--keep-app 已失效（应用现在常驻，运行结束不会关闭）")
	}

	log := func(msg string) { fmt.Println(msg) }

	cfg := loadEffectiveCfg(*cfgPath)
	traces := loadTraces(cfg)
	findings := allFindings(cfg, traces)
	recipes := repro.BuildRecipes(findings, traces, cfg)

	if *rule != "" {
		recipes = filterRecipes(recipes, func(r *repro.ReproRecipe) bool { return r.Rule == *rule })
	}
	if *tool != "" {
		recipes = filterRecipes(recipes, func(r *repro.ReproRecipe) bool { return r.Tool == *tool })
	}

	if len(recipes) == 0 {
		log("没有匹配的 bug 签名（日志可能已全部修复）")
		return 0
	}

	// ------------------------------------------------ 仅重渲染
	if *renderOnly {
		var saved []*repro.ReproRecipe
		savedPath := filepath.Join(config.RootDir, "artifacts", "repro_results.json")
		if data, err := os.ReadFile(savedPath); err == nil {
			var dicts []map[string]any
			if err := json.Unmarshal(data, &dicts); err == nil {
				for _, d := range dicts {
					saved = append(saved, recipeFromDict(d))
				}
			}
		}
		rp, err := renderReproReport(cfg, traces, findings, saved)
		if err != nil {
			fmt.Fprintf(os.Stderr, "渲染报告失败：%v\n", err)
			return 1
		}
		log(fmt.Sprintf("报告已更新 %s", rp))
		return 0
	}

	// ------------------------------------------------ 列表模式
	if *listOnly {
		log(fmt.Sprintf("共 %d 个 bug 签名的复现配方：\n", len(recipes)))
		for _, r := range recipes {
			log(fmt.Sprintf("[%s] %s  %s", r.Severity, r.RuleName, r.Key))
			src := "合成"
			if r.PromptSource == "original" {
				src = "原始提问"
			}
			log(fmt.Sprintf("  来源会话 : %s（提示词取自%s）", r.SourceSession, src))
			log(fmt.Sprintf("  复现提示词: %s", r.Prompt))
			if len(r.TriggerArgs) > 0 {
				argsJSON := models.JSONDumps(r.TriggerArgs)
				log(fmt.Sprintf("  触发参数  : %s", truncRunes(argsJSON, 160)))
			}
			log(fmt.Sprintf("  验证标准  : %s", r.VerifyDesc))
			log(fmt.Sprintf("  预期/实际 : %s ↔ %s", r.Expected, truncRunes(r.Actual, 80)))
			log("")
		}
		return 0
	}

	// ------------------------------------------------ 验证模式
	targets := recipes
	if !*all {
		targets = recipes[:1]
	}
	log(fmt.Sprintf("待验证签名 %d 个 × %d 次 = 至多 %d 轮 UI 对话\n",
		len(targets), *times, len(targets)*(*times)))

	drv := driver.NewRuiyunUIDriver(cfg)
	ok, how := drv.EnsureReady()
	if !ok {
		if how == "launch_failed" {
			log("✗ 应用启动失败或无法接入渲染进程")
		} else {
			log("✗ 无法接入渲染进程")
		}
		return 2
	}
	reused := "已启动应用"
	if how == "reused" {
		reused = "复用已运行的应用"
	}
	log(fmt.Sprintf("✓ %s | 界面 %s\n", reused, drv.TargetURL))

	for _, r := range targets {
		log(fmt.Sprintf("▶ 验证 %s（%s）", r.Key, r.RuleName))
		log(fmt.Sprintf("  提示词: %s", truncRunes(r.Prompt, 70)))
		repro.VerifyRecipe(r, drv, cfg, *times, log)
		rate := 0.0
		if r.Rate != nil {
			rate = *r.Rate
		}
		log(fmt.Sprintf("  ⇒ 复现 %d/%d = %.0f%% → %s\n", r.Hits, r.Attempts, rate*100, r.Stability))
	}
	drv.Detach() // 仅断开连接，应用保持运行

	out := filepath.Join(config.RootDir, "artifacts", "repro_results.json")
	if err := saveRecipes(targets, out); err != nil {
		fmt.Fprintf(os.Stderr, "写入复现结果失败：%v\n", err)
	}
	log(fmt.Sprintf("结果已写入 %s", out))

	if *updateReport {
		rp, err := renderReproReport(cfg, traces, findings, targets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "渲染报告失败：%v\n", err)
		} else {
			log(fmt.Sprintf("报告已更新 %s", rp))
		}
	}

	// ------------------------------------------------ 汇总
	log(strings.Repeat("=", 70))
	for _, r := range targets {
		rate := 0.0
		if r.Rate != nil {
			rate = *r.Rate
		}
		log(fmt.Sprintf("[%s] %-48s %d/%d = %.0f%%  %s",
			r.Severity, r.Key, r.Hits, r.Attempts, rate*100, r.Stability))
	}
	log(strings.Repeat("=", 70))
	return 0
}

// ------------------------------------------------------------------ 数据装配

// loadTraces 扫描会话根目录下的 sess_* 并解析
func loadTraces(cfg map[string]any) []*models.ExecutionTrace {
	paths, _ := cfg["paths"].(map[string]any)
	root := ""
	if paths != nil {
		root = fmt.Sprintf("%v", paths["session_root"])
	}
	if root == "" || root == "<nil>" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "sess_") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // 按字典序排序，保证历次处理顺序一致

	var traces []*models.ExecutionTrace
	for _, n := range names {
		if t, err := logparser.ParseSession(filepath.Join(root, n)); err == nil && t != nil {
			traces = append(traces, t)
		}
	}
	return traces
}

// allFindings 对全部会话跑断言
func allFindings(cfg map[string]any, traces []*models.ExecutionTrace) []*models.Finding {
	rulesMap, _ := cfg["rules"].(map[string]any)
	if rulesMap == nil {
		rulesMap = map[string]any{}
	}
	maxIter := repro.ReadMaxIterations(cfg)
	out := []*models.Finding{}
	for _, t := range traces {
		out = append(out, assertor.RunAssertions(t, rulesMap, maxIter)...)
	}
	return out
}

func filterRecipes(in []*repro.ReproRecipe, keep func(*repro.ReproRecipe) bool) []*repro.ReproRecipe {
	out := make([]*repro.ReproRecipe, 0, len(in))
	for _, r := range in {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// saveRecipes 落盘复现结果
func saveRecipes(recipes []*repro.ReproRecipe, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	dicts := make([]map[string]any, 0, len(recipes))
	for _, r := range recipes {
		dicts = append(dicts, r.ToDict())
	}
	data, err := json.MarshalIndent(dicts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, unescapeHTMLish(data), 0644)
}

// recipeFromDict 从字典字段装配 ReproRecipe（用于 --render-only 回读）
func recipeFromDict(d map[string]any) *repro.ReproRecipe {
	r := &repro.ReproRecipe{}
	r.Key = dictStr(d, "key")
	r.Rule = dictStr(d, "rule")
	r.RuleName = dictStr(d, "rule_name")
	r.Severity = dictStr(d, "severity")
	r.Tool = dictStr(d, "tool")
	r.Prompt = dictStr(d, "prompt")
	r.PromptSource = dictStr(d, "prompt_source")
	r.SourceSession = dictStr(d, "source_session")
	if ta, ok := d["trigger_args"].(map[string]any); ok {
		r.TriggerArgs = ta
	}
	r.Evidence = dictStr(d, "evidence")
	r.VerifyRule = dictStr(d, "verify_rule")
	r.VerifyDesc = dictStr(d, "verify_desc")
	r.Expected = dictStr(d, "expected")
	r.Actual = dictStr(d, "actual")
	if steps, ok := d["steps"].([]any); ok {
		for _, s := range steps {
			r.Steps = append(r.Steps, fmt.Sprintf("%v", s))
		}
	}
	r.Attempts = dictInt(d, "attempts")
	r.Hits = dictInt(d, "hits")
	switch v := d["rate"].(type) {
	case float64:
		rate := v
		r.Rate = &rate
	case int:
		rate := float64(v)
		r.Rate = &rate
	}
	r.Stability = dictStr(d, "stability")
	if rs, ok := d["run_sessions"].([]any); ok {
		for _, s := range rs {
			sm, _ := s.(map[string]any)
			if sm == nil {
				continue
			}
			hit, _ := sm["hit"].(bool)
			r.RunSessions = append(r.RunSessions, repro.SessionHit{
				SessionID: dictStr(sm, "session_id"), Hit: hit, Note: dictStr(sm, "note")})
		}
	}
	r.Occurrences = dictInt(d, "occurrences")
	r.Error = dictStr(d, "error")
	return r
}

// renderReproReport 重渲染报告——
// 以上次完整运行的 metrics 为基底，注入复现数据。
func renderReproReport(cfg map[string]any, traces []*models.ExecutionTrace,
	findings []*models.Finding, verified []*repro.ReproRecipe) (string, error) {

	metricsPath := filepath.Join(config.RootDir, "artifacts", "metrics.json")
	var m map[string]any
	if data, err := os.ReadFile(metricsPath); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	if m == nil {
		m = metrics.BuildMetrics(nil, cfg, nil, nil)
	}

	// 全量配方打底，再用已验证结果按 key 叠加（保持首次插入顺序）
	order := []string{}
	all := map[string]*repro.ReproRecipe{}
	for _, r := range repro.BuildRecipes(findings, traces, cfg) {
		if _, seen := all[r.Key]; !seen {
			order = append(order, r.Key)
		}
		all[r.Key] = r
	}
	for _, r := range verified {
		if _, seen := all[r.Key]; !seen {
			order = append(order, r.Key)
		}
		all[r.Key] = r
	}

	// findings_rows[].repro
	if rows, ok := m["findings_rows"].([]any); ok {
		for _, row := range rows {
			rm, _ := row.(map[string]any)
			if rm == nil {
				continue
			}
			toolKey := dictStr(rm, "tool")
			if toolKey == "" {
				toolKey = "-"
			}
			if rep, ok := all[dictStr(rm, "rule")+":"+toolKey]; ok {
				rm["repro"] = metrics.ReproSubBlock(rep)
			}
		}
	}

	list := make([]*repro.ReproRecipe, 0, len(order))
	for _, k := range order {
		list = append(list, all[k])
	}
	reproRows, reproSummary := metrics.ReproBlock(list)
	m["repro_rows"] = reproRows
	m["repro_summary"] = reproSummary

	appVersion, bundleID := sysutil.ReadAppVersion(appBinaryOf(cfg))
	rp := filepath.Join(config.RootDir, "report", "ruiyun_hardbug_report.html")
	if _, err := report.RenderReport(m, rp, appVersion, bundleID, "复现验证", 0); err != nil {
		return "", err
	}
	return rp, nil
}

func appBinaryOf(cfg map[string]any) string {
	app, _ := cfg["app"].(map[string]any)
	if app == nil {
		return ""
	}
	s := fmt.Sprintf("%v", app["binary"])
	if s == "<nil>" {
		return ""
	}
	return s
}

func dictStr(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[k].(string); ok {
		return s
	}
	if v, ok := m[k]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func dictInt(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
