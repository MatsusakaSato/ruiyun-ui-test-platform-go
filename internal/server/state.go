package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ruiyun-ui-test-platform-go/internal/canon"
	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/evaluator"
	"ruiyun-ui-test-platform-go/internal/llm"
	"ruiyun-ui-test-platform-go/internal/rounds"
	"ruiyun-ui-test-platform-go/internal/sysutil"

	"gopkg.in/yaml.v3"
)

// MaxLogLines 控制台回放缓冲上限
const MaxLogLines = 800

// ------------------------------------------------------------------ RunState

// RunOptions /api/run 的入参
type RunOptions struct {
	Cases       int
	ReproTimes  int
	ReproLimit  int
	CaseItems   []map[string]any
	MaxInflight int
	AutoConfirm *bool
}

// RunState 同一时刻只允许一轮运行
type RunState struct {
	mu                sync.Mutex
	proc              *exec.Cmd
	runID             string
	startedAt         float64
	lines             []string
	exitCode          *int
	consolePath       string
	stopped           bool
	terminationReason string
	procDone          bool
	queue             []map[string]any
	queueFilledFor    string

	allowLAN bool
	selfExe  string
}

// NewRunState 构造运行状态
func NewRunState(allowLAN bool, selfExe string) *RunState {
	return &RunState{allowLAN: allowLAN, selfExe: selfExe}
}

func (s *RunState) lineLocked(text string) {
	s.lines = append(s.lines, time.Now().Format("15:04:05")+"  "+text)
	if len(s.lines) > MaxLogLines {
		s.lines = s.lines[len(s.lines)-MaxLogLines:]
	}
}

// Start 启动一轮测试。返回 (ok, runID 或错误消息)
func (s *RunState) Start(opts RunOptions) (bool, string) {
	s.mu.Lock()
	if s.proc != nil && s.proc.Process != nil && s.processAlive() {
		s.mu.Unlock()
		return false, "已有测试正在运行，请等待完成"
	}
	s.runID = time.Now().Format("run_20060102_150405")
	s.startedAt = float64(time.Now().UnixNano()) / 1e9
	s.lines = nil
	s.exitCode = nil
	s.stopped = false
	s.terminationReason = ""
	s.procDone = false
	s.queue = []map[string]any{}
	for i, c := range opts.CaseItems {
		id := strOr(c["id"], "")
		if id == "" {
			id = fmt.Sprintf("CASE-%03d", i+1)
		}
		s.queue = append(s.queue, map[string]any{
			"case_id":   id,
			"name":      strOr(c["name"], ""),
			"prompt":    strOr(c["prompt"], ""),
			"status":    "",
			"elapsed_s": nil,
		})
	}
	s.queueFilledFor = ""

	runDir := filepath.Join(config.RoundsDir(), s.runID)
	_ = os.MkdirAll(runDir, 0755)
	s.consolePath = filepath.Join(runDir, "console.log")

	args := []string{"pipeline", "--run-id", s.runID}
	if len(opts.CaseItems) > 0 {
		cf := filepath.Join(runDir, "cases.yaml")
		payload, err := yaml.Marshal(map[string]any{"cases": opts.CaseItems})
		if err == nil {
			_ = os.WriteFile(cf, payload, 0644)
			args = append(args, "--cases-file", cf)
		}
	}
	if opts.Cases != 0 {
		args = append(args, "--cases", fmt.Sprint(opts.Cases))
	}
	if opts.ReproTimes != 0 {
		args = append(args, "--repro-times", fmt.Sprint(opts.ReproTimes))
		if opts.ReproLimit != 0 {
			args = append(args, "--repro-limit", fmt.Sprint(opts.ReproLimit))
		}
	}
	if opts.MaxInflight != 0 {
		args = append(args, "--max-inflight", fmt.Sprint(opts.MaxInflight))
	}
	if opts.AutoConfirm != nil {
		state := "off"
		if *opts.AutoConfirm {
			state = "on"
		}
		args = append(args, "--auto-confirm", state)
	}

	cmd := exec.Command(s.selfExe, args...)
	cmd.Dir = config.RootDir
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.mu.Unlock()
		return false, fmt.Sprintf("启动失败: %v", err)
	}
	cmd.Stderr = cmd.Stdout
	cmd.SysProcAttr = procAttrNewSession()
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return false, fmt.Sprintf("启动失败: %v", err)
	}
	s.proc = cmd

	go s.pump(cmd, stdout, runDir)
	s.lineLocked(fmt.Sprintf("[平台] 测试已启动 · run_id=%s", s.runID))
	s.lineLocked(fmt.Sprintf("[平台] 命令: %s", strings.Join(args[1:], " ")))
	runID := s.runID
	s.mu.Unlock()
	return true, runID
}

func (s *RunState) processAlive() bool {
	if s.proc == nil || s.proc.Process == nil {
		return false
	}
	return !s.procDone
}

func (s *RunState) pump(cmd *exec.Cmd, stdout io.ReadCloser, runDir string) {
	var logf *os.File
	if f, err := os.Create(s.consolePath); err == nil {
		logf = f
	}
	if logf != nil {
		defer func() { _ = logf.Close() }()
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		text := strings.TrimRight(sc.Text(), "\r\n")
		s.mu.Lock()
		s.lineLocked(text)
		if strings.HasPrefix(text, "[终止] ") && !s.stopped {
			s.stopped = true
			s.terminationReason = strings.TrimPrefix(text, "[终止] ")
		}
		s.mu.Unlock()
		if logf != nil {
			_, _ = logf.WriteString(sc.Text() + "\n")
			_ = logf.Sync()
		}
	}
	if err := sc.Err(); err != nil {
		s.mu.Lock()
		s.lineLocked(fmt.Sprintf("[平台] 读取控制台输出异常: %v", err))
		s.mu.Unlock()
		if logf != nil {
			_, _ = logf.WriteString(fmt.Sprintf("[平台] 读取控制台输出异常: %v\n", err))
			_ = logf.Sync()
		}
	}
	_ = cmd.Wait()

	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	s.mu.Lock()
	c := code
	s.exitCode = &c
	s.procDone = true
	archived := false
	if st, err := os.Stat(filepath.Join(config.RoundsDir(), s.runID, "round_summary.json")); err == nil && !st.IsDir() {
		archived = true
	}
	note := "，⚠ 未产出轮次归档（可能中途失败）"
	if archived {
		note = "，轮次数据已归档 ✓"
	}
	s.lineLocked(fmt.Sprintf("[平台] 进程退出 code=%d%s", code, note))
	s.mu.Unlock()
}

// Stop 手动终止正在运行的测试
func (s *RunState) Stop() (bool, string) {
	s.mu.Lock()
	if s.proc == nil || s.proc.Process == nil || !s.processAlive() {
		s.mu.Unlock()
		return false, "当前没有正在运行的测试"
	}
	proc := s.proc
	s.stopped = true
	s.terminationReason = "用户手动终止测试"
	s.mu.Unlock()

	cfg, _ := config.LoadConfigDict()
	eff := config.EffectiveConfig(cfg)
	app, _ := eff["app"].(map[string]any)
	binary := strings.TrimSpace(fmt.Sprintf("%v", app["binary"]))
	var appErr error
	if binary != "" && binary != "<nil>" {
		appErr = sysutil.KillProcessesNow(binary)
	}
	terminateProcess(proc)
	s.mu.Lock()
	if appErr != nil {
		s.terminationReason += "；关闭被测应用失败：" + appErr.Error()
	}
	s.lineLocked("[平台] " + s.terminationReason)
	s.mu.Unlock()
	return true, "已终止测试并关闭被测应用进程"
}

// Status /api/run/status 的响应体
func (s *RunState) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	running := s.processAlive()
	doneOK := false
	if s.runID != "" && !running {
		if st, err := os.Stat(filepath.Join(config.RoundsDir(), s.runID, "round_summary.json")); err == nil && !st.IsDir() {
			doneOK = true
		}
	}
	finishedOK := s.exitCode != nil && *s.exitCode == 0
	elapsed := 0.0
	if s.startedAt != 0 {
		elapsed = round1(float64(time.Now().UnixNano())/1e9 - s.startedAt)
	}
	exitCode := any(nil)
	if s.exitCode != nil {
		exitCode = *s.exitCode
	}
	lines := []string{}
	if n := len(s.lines); n > 90 {
		lines = append(lines, s.lines[n-90:]...)
	} else {
		lines = append(lines, s.lines...)
	}
	return map[string]any{
		"running":            running,
		"run_id":             s.runID,
		"started_at":         s.startedAt,
		"elapsed_s":          elapsed,
		"exit_code":          exitCode,
		"archived":           doneOK,
		"finished_ok":        finishedOK,
		"stopped":            s.stopped,
		"termination_reason": s.terminationReason,
		"lines":              lines,
		"cases":              s.queueStatusLocked(doneOK),
	}
}

func (s *RunState) queueStatusLocked(archived bool) []map[string]any {
	if archived && s.runID != "" && s.queueFilledFor != s.runID {
		f := filepath.Join(config.RoundsDir(), s.runID, "round_summary.json")
		if data, err := os.ReadFile(f); err == nil {
			var summary map[string]any
			if err := json.Unmarshal(data, &summary); err == nil && summary != nil {
				rows, _ := summary["cases"].([]any)
				byID := map[string]map[string]any{}
				for _, r := range rows {
					if rm, ok := r.(map[string]any); ok {
						byID[strOr(rm["case_id"], "")] = rm
					}
				}
				for i, q := range s.queue {
					var r map[string]any
					if v, ok := byID[strOr(q["case_id"], "")]; ok {
						r = v
					} else if i < len(rows) {
						r, _ = rows[i].(map[string]any)
					}
					if r != nil {
						q["status"] = strOr(r["status"], "")
						q["elapsed_s"] = r["elapsed_s"]
					}
				}
				s.queueFilledFor = s.runID
			}
		}
	}
	out := make([]map[string]any, 0, len(s.queue))
	for _, q := range s.queue {
		cp := map[string]any{}
		for k, v := range q {
			cp[k] = v
		}
		out = append(out, cp)
	}
	return out
}

// RunID 当前轮次 ID
func (s *RunState) RunID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runID
}

// ClearRunID 清掉运行状态指向（删除轮次后调用）
func (s *RunState) ClearRunID() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runID = ""
}

// ProcRunning 是否有子进程在跑
func (s *RunState) ProcRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processAlive()
}

// ------------------------------------------------------------------ EvalState

// EvalState 质量评估后台任务：同一时刻只允许一个评估在跑
type EvalState struct {
	mu            sync.Mutex
	running       bool
	runID         string
	startedAt     float64
	lines         []string
	done          int
	total         int
	errMsg        string
	finished      bool
	stopped       bool
	summary       map[string]any
	lastPreflight map[string]any
}

// NewEvalState 构造评估状态
func NewEvalState() *EvalState {
	return &EvalState{}
}

func (e *EvalState) line(text string) {
	e.lines = append(e.lines, time.Now().Format("15:04:05")+"  "+text)
	if len(e.lines) > MaxLogLines {
		e.lines = e.lines[len(e.lines)-MaxLogLines:]
	}
}

func llmConfigDict() map[string]any {
	s := llm.LoadConfig()
	return map[string]any{
		"base_url": s.BaseURL,
		"api_key":  s.APIKey,
		"model":    s.Model,
		"saved_at": s.SavedAt,
	}
}

func (e *EvalState) preflight() map[string]any {
	defer func() {
		if r := recover(); r != nil {
			// 探活异常也当作一次「不可用」的结果，不向上抛
		}
	}()
	res := evaluator.PreflightCheck(map[string]any{"llm": llmConfigDict()})
	if res == nil {
		return map[string]any{"ok": false, "message": "启动前校验无结果"}
	}
	return res
}

// Start 启动质量评估
func (e *EvalState) Start(runID string, maxCases, concurrency int) (bool, string) {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return false, "已有评估任务在运行，请等待完成"
	}
	f := filepath.Join(config.RoundsDir(), runID, "round_detail.json")
	if st, err := os.Stat(f); err != nil || st.IsDir() {
		e.mu.Unlock()
		return false, fmt.Sprintf("轮次不存在或缺少 round_detail.json：%s", runID)
	}
	e.mu.Unlock()

	// 探活在锁外执行（最长约 12s），避免阻塞停止/状态查询
	pf := e.preflight()
	e.mu.Lock()
	e.lastPreflight = pf
	e.mu.Unlock()
	if ok, _ := pf["ok"].(bool); !ok {
		msg := strOr(pf["message"], "模型不可用，已中止评估")
		e.mu.Lock()
		e.line(fmt.Sprintf("[评估] 已拒绝启动：%s", msg))
		e.mu.Unlock()
		return false, fmt.Sprintf("模型不可用，未开始评估：%s", msg)
	}

	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return false, "已有评估任务在运行，请等待完成"
	}
	e.runID = runID
	e.startedAt = float64(time.Now().UnixNano()) / 1e9
	e.lines = nil
	e.done, e.total = 0, 0
	e.errMsg = ""
	e.finished = false
	e.stopped = false
	e.summary = map[string]any{}
	e.running = true
	e.mu.Unlock()

	go e.run(runID, maxCases, concurrency)
	e.mu.Lock()
	e.line(fmt.Sprintf("[评估] 已启动 · run_id=%s", runID))
	e.mu.Unlock()
	return true, runID
}

func (e *EvalState) run(runID string, maxCases, concurrency int) {
	defer func() {
		e.mu.Lock()
		e.finished = true
		e.running = false
		e.mu.Unlock()
	}()
	defer func() {
		if r := recover(); r != nil {
			e.mu.Lock()
			e.errMsg = fmt.Sprintf("%T: %v", r, r)
			e.line(fmt.Sprintf("[评估] 失败：%s", e.errMsg))
			e.mu.Unlock()
		}
	}()

	notPreflight := false
	res, err := evaluator.EvaluateRound(runID, evaluator.EvaluateOptions{
		MaxCases: maxCases,
		Cfg: map[string]interface{}{
			"llm": map[string]interface{}{"eval_concurrency": concurrency},
		},
		OnProgress: func(done, total int, message string) {
			e.mu.Lock()
			e.done, e.total = done, total
			e.line(message)
			e.mu.Unlock()
		},
		Cancel: func() bool {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.stopped
		},
		Preflight: &notPreflight,
	})
	if err != nil && res == nil {
		e.mu.Lock()
		e.errMsg = err.Error()
		e.line(fmt.Sprintf("[评估] 失败：%s", e.errMsg))
		e.mu.Unlock()
		return
	}
	if aborted, _ := res["aborted"].(bool); aborted {
		e.mu.Lock()
		e.errMsg = strOr(res["error"], "模型不可用，已中止评估")
		e.line(fmt.Sprintf("[评估] 已中止：%s", e.errMsg))
		e.mu.Unlock()
		return
	}
	e.mu.Lock()
	if sm, ok := res["summary"].(map[string]interface{}); ok {
		e.summary = sm
	} else {
		e.summary = map[string]any{}
	}
	if er := strOr(res["error"], ""); er != "" {
		e.errMsg = er
	}
	casesLen := 0
	if cs, ok := res["cases"].([]interface{}); ok {
		casesLen = len(cs)
	} else if cs, ok := res["cases"].([]map[string]interface{}); ok {
		casesLen = len(cs)
	}
	e.line(fmt.Sprintf("[评估] 完成：用例 %d 条，综合分 %v", casesLen, e.summary["overall_score_100"]))
	e.mu.Unlock()
}

// Stop 请求停止评估
func (e *EvalState) Stop() (bool, string) {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return false, "当前没有正在运行的评估"
	}
	e.stopped = true
	e.line("[评估] 已请求停止（当前用例完成后退出）")
	e.mu.Unlock()
	return true, "已请求停止"
}

// LastPreflight 最近一次启动前探活结果
func (e *EvalState) LastPreflight() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastPreflight
}

// Status /api/eval/status 的响应体
func (e *EvalState) Status() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	elapsed := 0.0
	if e.startedAt != 0 {
		elapsed = round1(float64(time.Now().UnixNano())/1e9 - e.startedAt)
	}
	lines := []string{}
	if n := len(e.lines); n > 60 {
		lines = append(lines, e.lines[n-60:]...)
	} else {
		lines = append(lines, e.lines...)
	}
	summary := e.summary
	if summary == nil {
		summary = map[string]any{}
	}
	return map[string]any{
		"running":   e.running,
		"run_id":    e.runID,
		"done":      e.done,
		"total":     e.total,
		"error":     e.errMsg,
		"finished":  e.finished,
		"stopped":   e.stopped,
		"summary":   summary,
		"elapsed_s": elapsed,
		"lines":     lines,
	}
}

// ------------------------------------------------------------------ 平台相关

// round1 四舍五入保留 1 位小数
func round1(v float64) float64 {
	return canon.Round(v, 1)
}

// deleteRound 供路由层调用
func deleteRound(runID string, running bool) (bool, string) {
	return rounds.Delete(runID, running)
}
