package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"ruiyun-ui-test-platform-go/internal/assertor"
	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/driver"
	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/metrics"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/report"
	"ruiyun-ui-test-platform-go/internal/repro"
	"ruiyun-ui-test-platform-go/internal/sysutil"
	"ruiyun-ui-test-platform-go/internal/testcasedb"
	"ruiyun-ui-test-platform-go/internal/trajectory"
)

const RuleSeparator = "──────────────────────────────────────────────────────────"

var StatusCN = map[string]string{
	"PASS":    "通过",
	"FAIL":    "断言失败",
	"UI_FAIL": "UI 失败",
}

func evTS(ev map[string]any) float64 {
	if ts, ok := ev["ts"].(float64); ok {
		return ts
	}
	return 0.0
}

func eventsForSession(events []map[string]any, sessDir string, tSend float64) []any {
	key := sessDir
	var owned []any
	for _, e := range events {
		if fmt.Sprintf("%v", e["sess"]) == key {
			owned = append(owned, e)
		}
	}
	if len(owned) > 0 {
		return owned
	}
	for _, e := range events {
		s := fmt.Sprintf("%v", e["sess"])
		if (s == "" || s == "<nil>") && evTS(e) >= tSend-1.0 {
			owned = append(owned, e)
		}
	}
	return owned
}

type inflightItem struct {
	res   *models.CaseResult
	sess  string
	done  bool
	tSend float64
}

// RunUICases 流水线执行用例：发送串行、生成并行，最多 max_inflight 条同时在途
func RunUICases(cfg map[string]any, cases []map[string]any, uidriver *driver.RuiyunUIDriver, logger func(string)) []*models.CaseResult {
	if logger == nil {
		logger = func(string) {}
	}
	var results []*models.CaseResult

	caseTimeout := 1200.0
	if ct, ok := cfg["case_timeout_s"].(float64); ok && ct > 0 {
		caseTimeout = ct
	} else if ct, ok := cfg["case_timeout_s"].(int); ok && ct > 0 {
		caseTimeout = float64(ct)
	}

	newSessTimeout := 90.0
	if nst, ok := cfg["new_session_timeout_s"].(float64); ok && nst > 0 {
		newSessTimeout = nst
	} else if nst, ok := cfg["new_session_timeout_s"].(int); ok && nst > 0 {
		newSessTimeout = float64(nst)
	}

	maxInflight := 5
	if mi, ok := cfg["max_inflight"].(int); ok && mi > 0 {
		maxInflight = mi
	}

	type pendingCase struct {
		index int
		data  map[string]any
	}
	var pending []pendingCase
	for i, c := range cases {
		pending = append(pending, pendingCase{index: i + 1, data: c})
	}

	var inflight []*inflightItem
	nTotal := len(cases)
	drainLogged := false

	maxIter := repro.ReadMaxIterations(cfg)
	rulesMap, _ := cfg["rules"].(map[string]any)

	collect := func(res *models.CaseResult, sess string, done bool, tSend float64) {
		if !done {
			res.WaitedLimit = true
			res.WaitNote = fmt.Sprintf("已达等待上限（%.0f 分钟），按已落盘日志出结果", caseTimeout/60.0)
		}
		res.ConfirmEvents = eventsForSession(uidriver.ConfirmEvents, sess, tSend)
		res.AutoConfirms = len(res.ConfirmEvents)
		res.UIOk = true

		tr, err := logparser.ParseSession(sess)
		if err != nil || tr == nil {
			res.UIError = "会话日志解析失败"
			res.UIOk = false
		} else {
			res.Trace = tr
			res.Findings = assertor.RunAssertions(tr, rulesMap, maxIter)
		}
		results = append(results, res)

		statusText := res.Status()
		if cn, ok := StatusCN[statusText]; ok {
			statusText = cn
		}
		numCalls := 0
		if res.Trace != nil {
			numCalls = len(res.Trace.ToolCalls)
		}
		overNote := ""
		if !done {
			overNote = "（等待超时，按已有日志出结果）"
		}
		logger(fmt.Sprintf("        已收取 %s · 会话 %s · 工具调用 %d 次 · 问题 %d 个 · %s%s",
			res.CaseID, filepath.Base(sess), numCalls, len(res.Findings), statusText, overNote))
	}

	for len(pending) > 0 || len(inflight) > 0 {
		// 1) 填满在途窗口
		for len(pending) > 0 && len(inflight) < maxInflight {
			p := pending[0]
			pending = pending[1:]
			caseData := p.data
			i := p.index

			cid := fmt.Sprintf("%v", caseData["id"])
			if cid == "" || cid == "<nil>" {
				cid = fmt.Sprintf("CASE-%03d", i)
			}
			name := fmt.Sprintf("%v", caseData["name"])
			if name == "" || name == "<nil>" {
				name = cid
			}
			prompt := strings.TrimSpace(fmt.Sprintf("%v", caseData["prompt"]))

			titleDisp := cid
			if name != cid {
				titleDisp = cid + " " + name
			}
			promptDisp := prompt
			if len([]rune(promptDisp)) > 60 {
				promptDisp = string([]rune(promptDisp)[:60]) + "..."
			}
			logger(fmt.Sprintf("\n发送 %d/%d · %s", i, nTotal, titleDisp))
			logger(fmt.Sprintf("        提问：%s", promptDisp))

			var expTools []string
			if ets, ok := caseData["expect_tools"].([]any); ok {
				for _, t := range ets {
					expTools = append(expTools, fmt.Sprintf("%v", t))
				}
			}

			res := &models.CaseResult{
				CaseID:        cid,
				Name:          name,
				Prompt:        prompt,
				ExpectedTools: expTools,
				Attachments:   make([]string, 0),
				Findings:      make([]*models.Finding, 0),
			}
			t0 := float64(time.Now().UnixNano()) / 1e9

			// 回到「新建任务」首页
			if !uidriver.ResetToNewTask(15.0) {
				res.UIOk = false
				st := uidriver.PageState()
				if st["login_like"] == true {
					res.UIError = "应用停留在登录页（页面上有登录/密码框）：请先在该应用里完成登录，再运行测试"
				} else {
					res.UIError = "没能回到「新建任务」首页"
				}
				logger(fmt.Sprintf("        失败：%s（页面 %v · 新建任务按钮 %v · 输入区 %v）",
					res.UIError, st["href"], st["has_new_task"], st["has_composer"]))
				res.ElapsedS = float64(time.Now().UnixNano())/1e9 - t0
				results = append(results, res)
				continue
			}

			// 附件投递
			var attachPaths []string
			if atts, ok := caseData["attachments"].([]any); ok {
				for _, a := range atts {
					if s := strings.TrimSpace(fmt.Sprintf("%v", a)); s != "" && s != "<nil>" {
						attachPaths = append(attachPaths, s)
					}
				}
			}
			if len(attachPaths) > 0 {
				res.Attachments = attachPaths
				okAtt, msgAtt := uidriver.AttachFiles(attachPaths, 8.0)
				res.AttachNote = msgAtt
				if okAtt {
					logger("        附件：已引用")
				} else {
					logger("        附件：投递失败 —— " + msgAtt)
					res.UIError = "附件未投递：" + msgAtt
				}
			}

			before := uidriver.SnapshotSessions()
			if err := uidriver.TypeText(prompt); err != nil {
				res.UIOk = false
				res.UIError = fmt.Sprintf("界面操作出错: %v", err)
				logger(fmt.Sprintf("        失败：%s", res.UIError))
				res.ElapsedS = float64(time.Now().UnixNano())/1e9 - t0
				results = append(results, res)
				continue
			}
			time.Sleep(500 * time.Millisecond)

			how, _ := uidriver.Send()
			sess, err := uidriver.WaitNewSession(before, newSessTimeout)
			if err != nil || sess == "" {
				st := uidriver.ComposerState()
				res.UIError = fmt.Sprintf("发送后没有产生新会话日志（发送方式 %s；输入框里 %v 字，发送按钮 %v）",
					how, st["text_len"], st["send_button"])
				res.ElapsedS = float64(time.Now().UnixNano())/1e9 - t0
				results = append(results, res)
				logger(fmt.Sprintf("        失败：没有产生新会话（发送方式 %s，输入框 %v 字，发送按钮 %v）",
					how, st["text_len"], st["send_button"]))
				continue
			}

			res.SessionID = filepath.Base(sess)
			uidriver.SetCurrentSess(sess)
			inflight = append(inflight, &inflightItem{
				res:   res,
				sess:  sess,
				done:  false,
				tSend: t0,
			})
			logger(fmt.Sprintf("        已发送 · 会话 %s 已创建（进行中 %d/%d）",
				filepath.Base(sess), len(inflight), maxInflight))
		}

		// 2) 等待至少一条在途会话稳定
		if len(inflight) == 0 {
			continue
		}
		if len(pending) == 0 && !drainLogged {
			drainLogged = true
			logger(fmt.Sprintf("\n  %d 条用例已全部发送，进行中 %d 条 —— 等待生成完成后依次收取…", nTotal, len(inflight)))
		}

		// 自动确认人工介入检测
		if uidriver.ConfirmHumanNeeded && !uidriver.HumanReported {
			uidriver.HumanReported = true
			hs := uidriver.ConfirmHumanSess
			var owner *inflightItem
			for _, it := range inflight {
				if hs != "" && it.sess == hs {
					owner = it
					break
				}
			}
			if owner == nil {
				owner = inflight[0]
			}
			res0 := owner.res
			res0.Findings = append(res0.Findings, &models.Finding{
				Rule:      "CONFIRM_MANUAL_NEEDED",
				Severity:  "P0",
				SessionID: res0.SessionID,
				Detail:    "确认卡片自动点击连续 5 次失败，任务卡在等待人工授权。请在应用界面手动处理；处理后的结果仍会被正常采集，但该用例已标记需人工复核。",
				Evidence:  "详见控制台「确认」相关日志，以及本用例时间线里的自动确认条目",
			})
			res0.UIOk = true
			logger(fmt.Sprintf("        自动确认连续失败，需要你手动处理：用例 %s 卡在确认卡片，请在应用界面点一下", res0.CaseID))
		}

		var dirs []string
		var pairs []driver.InflightPair
		for _, it := range inflight {
			dirs = append(dirs, it.sess)
			pairs = append(pairs, driver.InflightPair{
				SessionDir: it.sess,
				Prompt:     it.res.Prompt,
			})
		}

		settled := uidriver.WaitSomeSettled(dirs, caseTimeout, 3.0, pairs, uidriver.AutoConfirmCycleS)
		settledMap := make(map[string]bool)
		for _, s := range settled {
			settledMap[s] = true
		}

		if len(settledMap) > 0 {
			var remaining []*inflightItem
			now := float64(time.Now().UnixNano()) / 1e9
			for _, it := range inflight {
				if settledMap[it.sess] {
					it.res.ElapsedS = now - it.tSend
					collect(it.res, it.sess, true, it.tSend)
				} else {
					remaining = append(remaining, it)
				}
			}
			inflight = remaining
		} else {
			// 全部等满超时
			now := float64(time.Now().UnixNano()) / 1e9
			for _, it := range inflight {
				it.res.ElapsedS = now - it.tSend
				collect(it.res, it.sess, false, it.tSend)
			}
			inflight = nil
		}
	}

	return results
}

// LoadCases 载入用例
func LoadCases(casesFile string) ([]map[string]any, error) {
	if casesFile == "" {
		return testcasedb.GetPresetCases("")
	}

	ext := strings.ToLower(filepath.Ext(casesFile))
	if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" {
		return testcasedb.GetPresetCases(casesFile)
	}

	data, err := os.ReadFile(casesFile)
	if err != nil {
		return nil, err
	}

	if ext == ".json" {
		var list []map[string]any
		if err := json.Unmarshal(data, &list); err == nil {
			return list, nil
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err == nil {
			if cs, ok := m["cases"].([]any); ok {
				var out []map[string]any
				for _, c := range cs {
					if cm, ok := c.(map[string]any); ok {
						out = append(out, cm)
					}
				}
				return out, nil
			}
		}
	} else {
		var list []map[string]any
		if err := yaml.Unmarshal(data, &list); err == nil {
			return list, nil
		}
		var m map[string]any
		if err := yaml.Unmarshal(data, &m); err == nil {
			if cs, ok := m["cases"].([]any); ok {
				var out []map[string]any
				for _, c := range cs {
					if cm, ok := c.(map[string]any); ok {
						out = append(out, cm)
					}
				}
				return out, nil
			}
		}
	}

	return []map[string]any{}, nil
}

// PipelineOptions 运行参数
type PipelineOptions struct {
	ConfigPath  string
	CasesLimit  int
	CasesFile   string
	ReproTimes  int
	ReproLimit  int
	MaxInflight int
	AutoConfirm *bool
	RunID       string
	ReportName  string
	Logger      func(string)
}

// RunPipeline 执行完整流水线
func RunPipeline(opts PipelineOptions) (int, error) {
	logFn := opts.Logger
	if logFn == nil {
		logFn = func(s string) { fmt.Println(s) }
	}

	tStart := float64(time.Now().UnixNano()) / 1e9

	cfgMap, err := config.LoadConfigDict()
	if err != nil {
		cfgMap = make(map[string]any)
	}
	cfg := config.EffectiveConfig(cfgMap)

	if opts.MaxInflight > 0 {
		cfg["max_inflight"] = opts.MaxInflight
	}
	if opts.AutoConfirm != nil {
		appCfg, _ := cfg["app"].(map[string]any)
		if appCfg == nil {
			appCfg = make(map[string]any)
			cfg["app"] = appCfg
		}
		appCfg["auto_confirm"] = *opts.AutoConfirm
	}

	cases, err := LoadCases(opts.CasesFile)
	if err != nil || len(cases) == 0 {
		logFn("  本轮没有可用用例，已中止")
		return 2, err
	}
	if opts.CasesLimit > 0 && len(cases) > opts.CasesLimit {
		cases = cases[:opts.CasesLimit]
	}

	appCfg, _ := cfg["app"].(map[string]any)
	binaryPath := fmt.Sprintf("%v", appCfg["binary"])
	appVersion, bundleID := sysutil.ReadAppVersion(binaryPath)
	maxIter := repro.ReadMaxIterations(cfg)
	runMode := "UI 自动化"
	caseSrc := "本地 SQLite 预设库"
	if opts.CasesFile != "" {
		caseSrc = "手动输入"
	}

	logFn(RuleSeparator)
	logFn("睿云智能工作台 · UI 测试平台 (Go)")
	logFn(fmt.Sprintf("应用版本 %s · 用例 %d 条（%s）", orFallback(appVersion, "未读取到"), len(cases), caseSrc))

	maxInf := 5
	if mi, ok := cfg["max_inflight"].(int); ok && mi > 0 {
		maxInf = mi
	}
	confirmInfo := ""
	if opts.AutoConfirm != nil {
		if *opts.AutoConfirm {
			confirmInfo = " · 自动点击 开"
		} else {
			confirmInfo = " · 自动点击 关"
		}
	}
	logFn(fmt.Sprintf("并发 %d 条 · 迭代上限 %d · 模式 %s%s", maxInf, maxIter, runMode, confirmInfo))

	if autoConf, _ := appCfg["auto_confirm"].(bool); !autoConf {
		logFn("提示：自动点击已关闭，用例出现确认卡片时需要你自己在应用里点一下，否则会等到超时才出结果")
	}
	logFn(RuleSeparator)

	uidriver := driver.NewRuiyunUIDriver(cfg)
	logFn("\n[1/3] 连接应用…")
	ok, how := uidriver.EnsureReady()
	if !ok {
		if how == "launch_failed" {
			logFn("  失败：应用没能启动，或调试端口未就绪")
		} else {
			logFn("  失败：连不上应用界面（渲染进程不可用）")
		}
		return 2, fmt.Errorf("ensure ready failed: %s", how)
	}
	reusedStr := "新启动了应用"
	if how == "reused" {
		reusedStr = "复用正在运行的应用"
	}
	logFn(fmt.Sprintf("  已连接（%s）", reusedStr))

	results := RunUICases(cfg, cases, uidriver, logFn)
	uidriver.Detach()
	if closeApp, _ := appCfg["close_app_after_run"].(bool); closeApp {
		uidriver.KillApp()
		logFn("  · 已按 close_app_after_run=true 关闭应用进程")
	}

	tStage1 := float64(time.Now().UnixNano())/1e9 - tStart

	// Stage 2: 校验调用链路
	logFn("\n[2/3] 校验调用链路…")
	tStage2Start := float64(time.Now().UnixNano()) / 1e9
	rulesMap, _ := cfg["rules"].(map[string]any)
	var roundFindings []*models.Finding
	for _, c := range results {
		roundFindings = append(roundFindings, c.Findings...)
		if c.Trace != nil && len(c.Findings) == 0 {
			c.Findings = assertor.RunAssertions(c.Trace, rulesMap, maxIter)
		}
	}
	findingsNote := ""
	if len(roundFindings) == 0 {
		findingsNote = "（本轮未发现问题）"
	}
	logFn(fmt.Sprintf("  %d 条用例共命中 %d 个问题%s", len(results), len(roundFindings), findingsNote))
	tStage2 := float64(time.Now().UnixNano())/1e9 - tStage2Start

	// Stage 2.5: 复现率验证
	var recipes []*repro.ReproRecipe
	if opts.ReproTimes > 0 && len(roundFindings) > 0 {
		var roundTraces []*models.ExecutionTrace
		for _, c := range results {
			if c.Trace != nil {
				roundTraces = append(roundTraces, c.Trace)
			}
		}
		recipes = repro.BuildRecipes(roundFindings, roundTraces, cfg)
		if opts.ReproLimit > 0 && len(recipes) > opts.ReproLimit {
			sort.Slice(recipes, func(i, j int) bool {
				p0I := 1
				if recipes[i].Severity == "P0" {
					p0I = 0
				}
				p0J := 1
				if recipes[j].Severity == "P0" {
					p0J = 0
				}
				if p0I != p0J {
					return p0I < p0J
				}
				return recipes[i].Key < recipes[j].Key
			})
			recipes = recipes[:opts.ReproLimit]
		}
		logFn(fmt.Sprintf("\n[附加] 复现率验证：%d 个问题 × %d 次", len(recipes), opts.ReproTimes))
		for _, r := range recipes {
			pDisp := r.Prompt
			if len([]rune(pDisp)) > 52 {
				pDisp = string([]rune(pDisp)[:52])
			}
			logFn(fmt.Sprintf("  · %s  提问：%s", r.Key, pDisp))
			repro.VerifyRecipe(r, uidriver, cfg, opts.ReproTimes, func(m string) { logFn("    " + m) })
			rateVal := 0.0
			if r.Rate != nil {
				rateVal = *r.Rate
			}
			logFn(fmt.Sprintf("    复现 %d/%d = %.0f%% → %s", r.Hits, r.Attempts, rateVal*100, r.Stability))
		}
		uidriver.Detach()
		if closeApp, _ := appCfg["close_app_after_run"].(bool); closeApp {
			uidriver.KillApp()
			logFn("  已关闭应用进程")
		}
	}

	// Stage 3: 测试报告与归档
	logFn("\n[3/3] 生成测试报告…")
	tStage3Start := float64(time.Now().UnixNano()) / 1e9
	totalElapsed := float64(time.Now().UnixNano())/1e9 - tStart

	stageTimes := map[string]float64{
		"ui_automation_s": float64(int(tStage1*10)) / 10.0,
		"assert_s":        float64(int(tStage2*10)) / 10.0,
		"report_s":        0.0,
		"total":           float64(int(totalElapsed*10)) / 10.0,
	}

	var modelRecipes []*models.Recipe
	for _, r := range recipes {
		rateVal := 0.0
		if r.Rate != nil {
			rateVal = *r.Rate
		}
		modelRecipes = append(modelRecipes, &models.Recipe{
			CaseID:     r.Key,
			Rule:       r.Rule,
			Severity:   r.Severity,
			Detail:     r.Expected,
			Tool:       r.Tool,
			TotalRuns:  r.Attempts,
			FailedRuns: r.Hits,
			Rate:       rateVal,
			Status:     r.Stability,
		})
	}

	builtMetrics := metrics.BuildMetrics(results, cfg, modelRecipes, stageTimes)

	reportName := opts.ReportName
	if reportName == "" {
		reportName = "ruiyun_hardbug_report.html"
	}
	reportDir := filepath.Join(config.RootDir, "report")
	_ = os.MkdirAll(reportDir, 0755)
	reportPath := filepath.Join(reportDir, reportName)

	_, err = report.RenderReport(builtMetrics, reportPath, appVersion, bundleID, runMode, totalElapsed)
	if err != nil {
		logFn(fmt.Sprintf("  警告：报告渲染出错: %v", err))
	}
	tStage3 := float64(time.Now().UnixNano())/1e9 - tStage3Start
	stageTimes["report_s"] = float64(int(tStage3*10)) / 10.0

	artDir := filepath.Join(config.RootDir, "artifacts")
	_ = os.MkdirAll(artDir, 0755)
	bMetrics, _ := json.MarshalIndent(builtMetrics, "", "  ")
	_ = os.WriteFile(filepath.Join(artDir, "metrics.json"), bMetrics, 0644)

	var caseResultsDicts []map[string]any
	for _, r := range results {
		caseResultsDicts = append(caseResultsDicts, r.ToDict())
	}
	bCases, _ := json.MarshalIndent(caseResultsDicts, "", "  ")
	_ = os.WriteFile(filepath.Join(artDir, "case_results.json"), bCases, 0644)

	// 轮次归档
	if opts.RunID != "" {
		roundDir := filepath.Join(config.RoundsDir(), opts.RunID)
		_ = os.MkdirAll(roundDir, 0755)

		var reproRows []map[string]any
		for _, r := range recipes {
			rateVal := 0.0
			if r.Rate != nil {
				rateVal = *r.Rate
			}
			reproRows = append(reproRows, map[string]any{
				"key":       r.Key,
				"rule":      r.Rule,
				"severity":  r.Severity,
				"tool":      r.Tool,
				"prompt":    r.Prompt,
				"attempts":  r.Attempts,
				"hits":      r.Hits,
				"rate":      rateVal,
				"stability": r.Stability,
			})
		}

		detail := trajectory.BuildRoundDetail(results, builtMetrics, reproRows, stageTimes, cfg)
		bDetail, _ := json.Marshal(detail)
		_ = os.WriteFile(filepath.Join(roundDir, "round_detail.json"), bDetail, 0644)

		roundSummary := map[string]any{
			"run_id":                    opts.RunID,
			"run_mode":                  runMode,
			"finished_at":               time.Now().Format("2006-01-02 15:04:05"),
			"exit":                      "ok",
			"elapsed_s":                 float64(int(totalElapsed*10)) / 10.0,
			"app_version":               appVersion,
			"summary":                   builtMetrics["summary"],
			"repro_summary":             builtMetrics["repro_summary"],
			"cases":                     builtMetrics["case_rows"],
			"round_tools":               detail["round_tools"],
			"round_skills":              detail["round_skills"],
			"round_artifacts":           detail["round_artifacts"],
			"round_artifact_kinds":      detail["round_artifact_kinds"],
			"auto_confirm_events":       uidriver.ConfirmEvents,
			"auto_confirm_human_needed": uidriver.ConfirmHumanNeeded,
		}
		bSummary, _ := json.MarshalIndent(roundSummary, "", "  ")
		_ = os.WriteFile(filepath.Join(roundDir, "round_summary.json"), bSummary, 0644)

		if reportBytes, err := os.ReadFile(reportPath); err == nil {
			_ = os.WriteFile(filepath.Join(roundDir, "report.html"), reportBytes, 0644)
		}
		logFn(fmt.Sprintf("\n  本轮数据已归档：%s", roundDir))
	}

	summaryMap, _ := builtMetrics["summary"].(map[string]any)
	objMap, _ := builtMetrics["objective"].(map[string]any)
	reqMap, _ := objMap["requests"].(map[string]any)
	tokMap, _ := objMap["tokens"].(map[string]any)
	timMap, _ := objMap["timing"].(map[string]any)

	logFn("\n" + RuleSeparator)
	logFn("本轮结果")
	logFn(fmt.Sprintf("  用例        %v 条：通过 %v · 断言失败 %v · UI 失败 %v（通过率 %v%%）",
		summaryMap["cases"], summaryMap["passed"], summaryMap["failed"], summaryMap["ui_failed"], summaryMap["pass_rate"]))
	findingNote := ""
	if fCount, _ := summaryMap["findings"].(int); fCount == 0 {
		findingNote = "  ← 本轮未发现问题"
	}
	logFn(fmt.Sprintf("  问题发现    %v 个（P0 %v · P1 %v）%s",
		summaryMap["findings"], summaryMap["p0"], summaryMap["p1"], findingNote))
	logFn(fmt.Sprintf("  工具调用    %v 次，失败 %v 次（%v%%），截断 %v 次",
		summaryMap["tool_calls_total"], summaryMap["tool_calls_failed"], summaryMap["tool_fail_rate"], summaryMap["tool_calls_truncated"]))
	logFn(fmt.Sprintf("  对话用量    提问 %v 轮 · 思考 %v 步 · 估算 token %v（入 %v / 出 %v）",
		reqMap["turns"], reqMap["thinking_steps"], tokMap["total_est"], tokMap["input_est"], tokMap["output_est"]))
	logFn(fmt.Sprintf("  响应耗时    平均首响 %vs · 总耗时 %.1fs", timMap["avg_first_response_s"], totalElapsed))

	if rs, ok := builtMetrics["repro_summary"].(map[string]any); ok {
		if ver, ok := rs["verified"].(int); ok && ver > 0 {
			logFn(fmt.Sprintf("  复现率      已验证 %v 个问题：必现 %v · 高概率 %v · 偶发 %v",
				rs["verified"], rs["stable"], rs["likely"], rs["flaky"]))
		}
	}
	logFn(fmt.Sprintf("  报告        %s", reportPath))

	if len(uidriver.ConfirmEvents) > 0 {
		var parts []string
		for i, e := range uidriver.ConfirmEvents {
			if i >= 6 {
				break
			}
			parts = append(parts, fmt.Sprintf("%v 「%v」", e["time"], e["text"]))
		}
		more := ""
		if len(uidriver.ConfirmEvents) > 6 {
			more = "…"
		}
		logFn(fmt.Sprintf("  自动确认    %d 次 → %s%s", len(uidriver.ConfirmEvents), strings.Join(parts, "；"), more))
	}
	logFn(RuleSeparator)

	return 0, nil
}

func orFallback(s, fb string) string {
	if s == "" {
		return fb
	}
	return s
}
