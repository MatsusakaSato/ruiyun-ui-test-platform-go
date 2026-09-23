package repro

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"ruiyun-ui-test-platform-go/internal/assertor"
	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/models"
)

const (
	StableThreshold = 1.0 // 100% -> 必现
	LikelyThreshold = 0.6 // >=60% -> 高概率复现，否则偶发
)

// ReproRecipe 一条 bug 的最小复现路径
type ReproRecipe struct {
	Key           string         `json:"key"` // rule:tool 唯一键
	Rule          string         `json:"rule"`
	RuleName      string         `json:"rule_name"`
	Severity      string         `json:"severity"`
	Tool          string         `json:"tool"`
	Prompt        string         `json:"prompt"`
	PromptSource  string         `json:"prompt_source"` // original / synthesized / unknown
	SourceSession string         `json:"source_session"`
	TriggerArgs   map[string]any `json:"trigger_args"`
	Evidence      string         `json:"evidence"`
	VerifyRule    string         `json:"verify_rule"`
	VerifyDesc    string         `json:"verify_desc"`
	Expected      string         `json:"expected"`
	Actual        string         `json:"actual"`
	Steps         []string       `json:"steps"`
	Attempts      int            `json:"attempts"`
	Hits          int            `json:"hits"`
	Rate          *float64       `json:"rate"`
	Stability     string         `json:"stability"` // 必现 / 高概率复现 / 偶发
	RunSessions   []SessionHit   `json:"run_sessions"`
	Occurrences   int            `json:"occurrences"`
	Error         string         `json:"error,omitempty"`
}

type SessionHit struct {
	SessionID string `json:"session_id"`
	Hit       bool   `json:"hit"`
	Note      string `json:"note,omitempty"`
}

// ToolPrompts 工具 -> 最易触发它的提示词（原始会话提问缺失时的兜底）
var ToolPrompts = map[string]string{
	"read_memory":                 "请读取你的记忆文件，然后告诉我里面当前有多少条记录。",
	"mcp_write_workspace_file":    "请在工作区新建一个文件，文件名 repro_test.txt，内容写「复现验证」，完成后告诉我完整路径。",
	"mcp_paid_search":             "请搜索李白的详细生平资料并整理介绍，内容尽量详细完整。",
	"mcp_fetch_webpage":           "请帮我抓取 https://baike.baidu.com/item/李白/1043 这个网页的正文内容。",
	"convert_markdown_to_docx":    "请把一段 Markdown 文档转换成 docx 并保存到工作区。",
	"academic_resource_collector": "请帮我检索「李白生平研究」相关的学术文献综述。",
	"todo_create":                 "请帮我列一个 3 步的任务清单来规划这次资料整理。",
	"todo_complete":               "请帮我列一个 3 步的任务清单并逐项完成它。",
}

func verifyDesc(rule, tool string, cfg map[string]any) string {
	r, _ := cfg["rules"].(map[string]any)
	if r == nil {
		r = map[string]any{}
	}
	switch rule {
	case "TOOL_CALL_FAILED":
		return fmt.Sprintf("复现会话日志中出现 %s 调用，且结果命中错误特征（success=false / error 字段 / [ERROR] 等）", tool)
	case "EMPTY_REQUIRED_ARG":
		return fmt.Sprintf("复现会话日志中 %s 被调用时必填参数为空，且调用未被拦截", tool)
	case "LOOP_CONSECUTIVE", "LOOP_TOTAL":
		conThresh := 5
		if v, ok := r["loop_consecutive_threshold"].(int); ok && v > 0 {
			conThresh = v
		}
		totalThresh := 8
		if v, ok := r["loop_total_threshold"].(int); ok && v > 0 {
			totalThresh = v
		}
		return fmt.Sprintf("复现会话日志中 %s 调用次数达到阈值（连续 ≥ %d 或总数 ≥ %d）", tool, conThresh, totalThresh)
	case "DUPLICATE_CALL":
		return fmt.Sprintf("复现会话日志中 %s 出现完全相同参数的重复调用", tool)
	default:
		return fmt.Sprintf("复现会话日志中再次命中断言规则 %s", rule)
	}
}

func expectedVsActual(rule, tool string, f *models.Finding) (string, string) {
	switch rule {
	case "TOOL_CALL_FAILED":
		evi := f.Evidence
		if len([]rune(evi)) > 120 {
			evi = string([]rune(evi)[:120])
		}
		return fmt.Sprintf("%s 返回调用成功或明确的业务结果", tool),
			fmt.Sprintf("%s 返回失败：%s", tool, evi)
	case "EMPTY_REQUIRED_ARG":
		evi := f.Evidence
		if len([]rune(evi)) > 120 {
			evi = string([]rune(evi)[:120])
		}
		return fmt.Sprintf("%s 校验必填参数并拒绝空值调用", tool),
			fmt.Sprintf("空参数调用被放行：%s", evi)
	default:
		return "调用行为正常收敛", f.Detail
	}
}

// BuildRecipe 从一条 Finding 构建最小复现配方。优先取原始会话的真实提问。
func BuildRecipe(finding *models.Finding, tracesBySID map[string]*models.ExecutionTrace, cfg map[string]any) *ReproRecipe {
	rule, tool := finding.Rule, finding.Tool
	trace := tracesBySID[finding.SessionID]

	prompt, source := "", ""
	var triggerArgs map[string]any

	if trace != nil {
		var candidates []*models.ToolCall
		for _, tc := range trace.ToolCalls {
			if tool == "" || tc.Name == tool {
				candidates = append(candidates, tc)
			}
		}
		var withTurnPrompt []*models.ToolCall
		for _, tc := range candidates {
			if strings.TrimSpace(tc.TurnPrompt) != "" {
				withTurnPrompt = append(withTurnPrompt, tc)
			}
		}
		if len(withTurnPrompt) > 0 {
			candidates = withTurnPrompt
		}

		if len(candidates) > 0 {
			best := candidates[0]
			bestLen := len([]rune(best.TurnPrompt))
			for _, tc := range candidates[1:] {
				l := len([]rune(tc.TurnPrompt))
				if l < bestLen {
					best = tc
					bestLen = l
				}
			}
			if m, ok := best.Arguments.(map[string]any); ok {
				triggerArgs = m
			}
			if strings.TrimSpace(best.TurnPrompt) != "" {
				prompt, source = strings.TrimSpace(best.TurnPrompt), "original"
			}
		}

		if prompt == "" && strings.TrimSpace(trace.UserPrompt) != "" {
			prompt, source = strings.TrimSpace(trace.UserPrompt), "original"
		}
	}

	if prompt == "" {
		if p, ok := ToolPrompts[tool]; ok {
			prompt = p
		} else if trace != nil {
			prompt = trace.UserPrompt
		}
		if prompt != "" {
			source = "synthesized"
		} else {
			source = "unknown"
		}
	}

	ruleName := rule
	if meta, ok := assertor.Rules[rule]; ok {
		ruleName = meta.Label
	}

	toolDisplay := tool
	if toolDisplay == "" {
		toolDisplay = "-"
	}

	sessionRoot := ""
	if paths, ok := cfg["paths"].(map[string]any); ok {
		sessionRoot = fmt.Sprintf("%v", paths["session_root"])
	}

	recipe := &ReproRecipe{
		Key:           fmt.Sprintf("%s:%s", rule, toolDisplay),
		Rule:          rule,
		RuleName:      ruleName,
		Severity:      finding.Severity,
		Tool:          tool,
		Prompt:        prompt,
		PromptSource:  source,
		SourceSession: finding.SessionID,
		TriggerArgs:   triggerArgs,
		Evidence:      finding.Evidence,
		VerifyRule:    rule,
		VerifyDesc:    verifyDesc(rule, tool, cfg),
	}
	recipe.Expected, recipe.Actual = expectedVsActual(rule, tool, finding)
	recipe.Steps = []string{
		"启动睿云智能工作台，进入「新建任务」首页",
		fmt.Sprintf("在输入框原样输入以下提示词：%s", prompt),
		"发送后等待任务执行结束（输入框清空且回复停止滚动）",
		fmt.Sprintf("打开日志目录 %s 下最新会话的 session.messages.json", sessionRoot),
		"按验证标准核对（也可直接用自动化复现核对）",
	}

	return recipe
}

func ReadMaxIterations(cfg map[string]any) int {
	paths, _ := cfg["paths"].(map[string]any)
	if paths == nil {
		return 0
	}
	agentConfig := fmt.Sprintf("%v", paths["agent_config"])
	if agentConfig == "" || agentConfig == "<nil>" {
		return 0
	}
	data, err := os.ReadFile(agentConfig)
	if err != nil {
		return 0
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return 0
	}
	if react, ok := root["react"].(map[string]any); ok {
		if mi, ok := react["max_iterations"].(int); ok {
			return mi
		}
	}
	return 0
}

// BuildRecipes 按 (rule, tool) 去重聚合配方，一条 bug 签名一份配方
func BuildRecipes(findings []*models.Finding, traces []*models.ExecutionTrace, cfg map[string]any) []*ReproRecipe {
	tracesBySID := make(map[string]*models.ExecutionTrace)
	for _, t := range traces {
		tracesBySID[t.SessionID] = t
	}
	maxIter := ReadMaxIterations(cfg)

	rulesMap, _ := cfg["rules"].(map[string]any)
	if rulesMap == nil {
		rulesMap = map[string]any{}
	}

	type findingTracePair struct {
		finding *models.Finding
		trace   *models.ExecutionTrace
	}

	sigSessions := make(map[string][]findingTracePair)
	for _, t := range traces {
		for _, f := range assertor.RunAssertions(t, rulesMap, maxIter) {
			toolStr := f.Tool
			if toolStr == "" {
				toolStr = "-"
			}
			key := fmt.Sprintf("%s:%s", f.Rule, toolStr)
			sigSessions[key] = append(sigSessions[key], findingTracePair{finding: f, trace: t})
		}
	}

	for _, f := range findings {
		toolStr := f.Tool
		if toolStr == "" {
			toolStr = "-"
		}
		key := fmt.Sprintf("%s:%s", f.Rule, toolStr)
		if t, ok := tracesBySID[f.SessionID]; ok {
			pairs := sigSessions[key]
			already := false
			for _, p := range pairs {
				if p.trace.SessionID == t.SessionID {
					already = true
					break
				}
			}
			if !already {
				sigSessions[key] = append(pairs, findingTracePair{finding: f, trace: t})
			}
		}
	}

	var recipes []*ReproRecipe
	for _, pairs := range sigSessions {
		if len(pairs) == 0 {
			continue
		}
		// 选提问最短的会话
		bestPair := pairs[0]
		bestPromptLen := len([]rune(strings.TrimSpace(bestPair.trace.UserPrompt)))
		for _, p := range pairs[1:] {
			pl := len([]rune(strings.TrimSpace(p.trace.UserPrompt)))
			if pl < bestPromptLen {
				bestPair = p
				bestPromptLen = pl
			}
		}

		recipe := BuildRecipe(bestPair.finding, map[string]*models.ExecutionTrace{bestPair.trace.SessionID: bestPair.trace}, cfg)
		if recipe.Prompt == "" {
			continue
		}
		recipe.Occurrences = len(pairs)
		recipes = append(recipes, recipe)
	}

	return recipes
}

func Classify(rate float64) string {
	if rate >= StableThreshold {
		return "必现"
	}
	if rate >= LikelyThreshold {
		return "高概率复现"
	}
	return "偶发"
}

// UIDriverReproInterface UI 自动化驱动接口，解耦 driver 实现
type UIDriverReproInterface interface {
	ResetToNewTask(waitS float64) bool
	SnapshotSessions() map[string]bool
	TypeText(text string) error
	Send() (string, error)
	WaitNewSession(before map[string]bool, timeoutS float64) (string, error)
	WaitSettled(sessDir string, timeoutS, quietS float64) bool
}

// VerifyRecipe 用 UI 自动化重复执行配方，统计断言命中率
func VerifyRecipe(recipe *ReproRecipe, driver UIDriverReproInterface, cfg map[string]any, times int, logger func(string)) *ReproRecipe {
	if logger == nil {
		logger = func(string) {}
	}
	recipe.Attempts = 0
	recipe.Hits = 0
	recipe.RunSessions = nil

	maxIter := ReadMaxIterations(cfg)
	rulesMap, _ := cfg["rules"].(map[string]any)
	if rulesMap == nil {
		rulesMap = map[string]any{}
	}

	caseTimeoutS := 1200.0
	if ct, ok := cfg["case_timeout_s"].(float64); ok && ct > 0 {
		caseTimeoutS = ct
	}

	for i := 0; i < times; i++ {
		recipe.Attempts++
		tag := fmt.Sprintf("[复现 %d/%d] %s", i+1, times, recipe.Key)

		if !driver.ResetToNewTask(15.0) {
			recipe.RunSessions = append(recipe.RunSessions, SessionHit{SessionID: "", Hit: false, Note: "无法回到首页"})
			logger(fmt.Sprintf("    %s ✗ 无法回到新建任务首页", tag))
			continue
		}

		before := driver.SnapshotSessions()
		if err := driver.TypeText(recipe.Prompt); err != nil {
			recipe.RunSessions = append(recipe.RunSessions, SessionHit{SessionID: "", Hit: false, Note: fmt.Sprintf("输入失败: %v", err)})
			logger(fmt.Sprintf("    %s ✗ 输入失败: %v", tag, err))
			continue
		}
		time.Sleep(500 * time.Millisecond)

		if _, err := driver.Send(); err != nil {
			recipe.RunSessions = append(recipe.RunSessions, SessionHit{SessionID: "", Hit: false, Note: fmt.Sprintf("发送失败: %v", err)})
			logger(fmt.Sprintf("    %s ✗ 发送失败: %v", tag, err))
			continue
		}

		sessDir, err := driver.WaitNewSession(before, 90.0)
		if err != nil || sessDir == "" {
			recipe.RunSessions = append(recipe.RunSessions, SessionHit{SessionID: "", Hit: false, Note: "未产生新会话"})
			logger(fmt.Sprintf("    %s ✗ 未产生新会话", tag))
			continue
		}

		driver.WaitSettled(sessDir, caseTimeoutS, 3.0)
		trace, err := logparser.ParseSession(sessDir)
		sessName := filepath.Base(sessDir)
		if err != nil || trace == nil {
			recipe.RunSessions = append(recipe.RunSessions, SessionHit{SessionID: sessName, Hit: false, Note: "日志解析失败"})
			logger(fmt.Sprintf("    %s ✗ 日志解析失败", tag))
			continue
		}

		findings := assertor.RunAssertions(trace, rulesMap, maxIter)
		hit := false
		for _, f := range findings {
			if f.Rule == recipe.Rule && (recipe.Tool == "" || f.Tool == recipe.Tool) {
				hit = true
				break
			}
		}

		recipe.RunSessions = append(recipe.RunSessions, SessionHit{SessionID: sessName, Hit: hit})
		if hit {
			recipe.Hits++
			logger(fmt.Sprintf("    %s ✓ 复现（会话 %s，工具调用 %d 次）", tag, sessName, len(trace.ToolCalls)))
		} else {
			logger(fmt.Sprintf("    %s ✗ 未复现（会话 %s，工具调用 %d 次）", tag, sessName, len(trace.ToolCalls)))
		}
	}

	if recipe.Attempts > 0 {
		rate := float64(recipe.Hits) / float64(recipe.Attempts)
		recipe.Rate = &rate
		recipe.Stability = Classify(rate)
	}

	return recipe
}
