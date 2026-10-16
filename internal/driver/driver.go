package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"ruiyun-ui-test-platform-go/internal/cdp"
	"ruiyun-ui-test-platform-go/internal/sysutil"
)

// 常量定义
var (
	DenyWords = []string{"拒绝", "取消", "关闭", "停止", "终止"}

	ConfirmActionCN = map[string]string{
		"qcard-option":  "选中选项",
		"qcard-confirm": "确认本题",
		"qcard-submit":  "提交",
		"qcard-fill":    "填写自定义答案",
		"keyword":       "确认授权",
		"first-option":  "确认首个选项",
	}

	InputSelectors = []string{
		"textarea",
		"div[contenteditable=\"true\"]",
		"[contenteditable=\"true\"]",
		"input[type=\"text\"]",
		"[role=\"textbox\"]",
	}

	SendKeywords = []string{"发送", "send", "submit"}

	ImageExts = map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".webp": true, ".bmp": true, ".svg": true,
	}
)

const (
	AttachButtonSelector = "button.chat-input-btn--attach"
	ComposerSelector     = "div.chat-input-shell"
)

func citeLine(mode, q, question, answer, extra string) string {
	label, ok := ConfirmActionCN[mode]
	if !ok {
		label = mode
		if label == "" {
			label = "自动确认"
		}
	}
	head := label
	if q != "" {
		head = fmt.Sprintf("第 %s 题", q)
	}

	var parts []string
	if q != "" {
		if label != "" {
			parts = append(parts, label)
		}
	}
	if answer != "" {
		parts = append(parts, answer)
	}
	body := strings.Join(parts, " · ")

	if question != "" {
		if body != "" {
			body = fmt.Sprintf("%s —— %s", body, question)
		} else {
			body = question
		}
	}
	if extra != "" {
		if body != "" {
			body = fmt.Sprintf("%s（%s）", body, extra)
		} else {
			body = fmt.Sprintf("（%s）", extra)
		}
	}

	res := fmt.Sprintf("[确认] %s %s", time.Now().Format("15:04:05"), head)
	if body != "" {
		res += " " + body
	}
	return res
}

func citeEmit(text string) {
	fmt.Println(text)
}

// RuiyunUIDriver 驱动实现
type RuiyunUIDriver struct {
	Cfg                map[string]any
	Binary             string
	Port               int
	LaunchTimeout      float64
	SessionRoot        string
	Proc               *exec.Cmd
	CDP                *cdp.CDPSession
	TargetURL          string
	EnvProfileDesc     string
	InputSelector      string
	LaunchedByUs       bool
	ReusedExisting     bool
	AppExited          bool
	RestartIfNoPort    bool
	UIReadyTimeout     float64
	AutoConfirm        bool
	ConfirmKeywords    []string
	ConfirmCooldown    float64
	ConfirmMaxClicks   int
	ConfirmEvents      []map[string]any
	ClickedGroups      map[string]map[string]any
	ConfirmCDPStreak   int
	ConfirmHumanNeeded bool
	ConfirmHumanSess   string
	HumanReported      bool

	QCardOwnerSess       string
	QCardViewSess        string
	CurrentSess          string
	QCardFPSig           string
	QCardFPSince         float64
	QCardFPClicks        map[string]int
	QCardCardClicks      int
	QCardCardSince       float64
	QCardLastClick       float64
	QCardMinClickGap     float64
	QCardSameLimit       int
	QCardStallS          float64
	QCardCardClicksLimit int
	QCardCardMaxS        float64
	QCardFillText        string
	AutoConfirmCycleS    float64
	ViewCycleDisabled    bool
	CycleFailStreak      int
	CycleVisited         map[string]bool
	mu                   sync.Mutex
}

// NewRuiyunUIDriver 构造驱动
func NewRuiyunUIDriver(cfg map[string]any) *RuiyunUIDriver {
	app, _ := cfg["app"].(map[string]any)
	if app == nil {
		app = make(map[string]any)
	}
	paths, _ := cfg["paths"].(map[string]any)
	if paths == nil {
		paths = make(map[string]any)
	}

	binary := fmt.Sprintf("%v", app["binary"])
	port := 9222
	if p, ok := app["debug_port"].(int); ok && p > 0 {
		port = p
	}
	launchTimeout := 60.0
	if lt, ok := app["launch_timeout_s"].(float64); ok && lt > 0 {
		launchTimeout = lt
	} else if lt, ok := app["launch_timeout_s"].(int); ok && lt > 0 {
		launchTimeout = float64(lt)
	}

	sessRoot := fmt.Sprintf("%v", paths["session_root"])

	restartIfNoPort := true
	if r, ok := app["restart_if_no_debug_port"].(bool); ok {
		restartIfNoPort = r
	}

	uiReadyTimeout := 180.0
	if ut, ok := app["ui_ready_timeout_s"].(float64); ok && ut > 0 {
		uiReadyTimeout = ut
	} else if ut, ok := app["ui_ready_timeout_s"].(int); ok && ut > 0 {
		uiReadyTimeout = float64(ut)
	}

	autoConfirm := false
	if ac, ok := app["auto_confirm"].(bool); ok {
		autoConfirm = ac
	}

	confirmKeywords := make([]string, 0)
	if kws, ok := app["confirm_keywords"].([]any); ok {
		for _, kw := range kws {
			confirmKeywords = append(confirmKeywords, fmt.Sprintf("%v", kw))
		}
	}

	confirmCooldown := 3.0
	if cc, ok := app["confirm_cooldown_s"].(float64); ok && cc > 0 {
		confirmCooldown = cc
	} else if cc, ok := app["confirm_cooldown_s"].(int); ok && cc > 0 {
		confirmCooldown = float64(cc)
	}

	confirmMaxClicks := 200
	if cm, ok := app["confirm_max_clicks"].(int); ok && cm > 0 {
		confirmMaxClicks = cm
	}

	qcardMinClickGap := 0.4
	if v, ok := app["qcard_min_click_interval_s"].(float64); ok && v > 0 {
		qcardMinClickGap = v
	}
	qcardSameLimit := 6
	if v, ok := app["qcard_same_card_limit"].(int); ok && v > 0 {
		qcardSameLimit = v
	}
	qcardStallS := 30.0
	if v, ok := app["qcard_stall_s"].(float64); ok && v > 0 {
		qcardStallS = v
	}
	qcardCardClicksLimit := 12
	if v, ok := app["qcard_card_clicks_limit"].(int); ok && v > 0 {
		qcardCardClicksLimit = v
	}
	qcardCardMaxS := 120.0
	if v, ok := app["qcard_card_max_s"].(float64); ok && v > 0 {
		qcardCardMaxS = v
	}
	qcardFillText := "请你按最合理的方式继续，不用再问我"
	if s, ok := app["qcard_fill_text"].(string); ok && strings.TrimSpace(s) != "" {
		qcardFillText = s
	}
	autoConfirmCycleS := 6.0
	if v, ok := app["auto_confirm_cycle_s"].(float64); ok {
		autoConfirmCycleS = v
	} else if v, ok := app["auto_confirm_cycle_s"].(int); ok {
		autoConfirmCycleS = float64(v)
	}

	return &RuiyunUIDriver{
		Cfg:                  cfg,
		Binary:               binary,
		Port:                 port,
		LaunchTimeout:        launchTimeout,
		SessionRoot:          sessRoot,
		RestartIfNoPort:      restartIfNoPort,
		UIReadyTimeout:       uiReadyTimeout,
		AutoConfirm:          autoConfirm,
		ConfirmKeywords:      confirmKeywords,
		ConfirmCooldown:      confirmCooldown,
		ConfirmMaxClicks:     confirmMaxClicks,
		ConfirmEvents:        make([]map[string]any, 0),
		ClickedGroups:        make(map[string]map[string]any),
		QCardFPClicks:        make(map[string]int),
		QCardMinClickGap:     qcardMinClickGap,
		QCardSameLimit:       qcardSameLimit,
		QCardStallS:          qcardStallS,
		QCardCardClicksLimit: qcardCardClicksLimit,
		QCardCardMaxS:        qcardCardMaxS,
		QCardFillText:        qcardFillText,
		AutoConfirmCycleS:    autoConfirmCycleS,
		CycleVisited:         make(map[string]bool),
	}
}

func (d *RuiyunUIDriver) applyEnvProfile(envMap map[string]string) string {
	app, _ := d.Cfg["app"].(map[string]any)
	profile := strings.TrimSpace(fmt.Sprintf("%v", app["env_profile"]))
	if profile == "" || profile == "<nil>" {
		return "(未指定环境，沿用当前进程已有的环境变量)"
	}
	profiles, _ := app["env_profiles"].(map[string]any)
	pairs, _ := profiles[profile].(map[string]any)
	applied := 0
	for k, v := range pairs {
		if _, exists := envMap[k]; !exists {
			envMap[k] = fmt.Sprintf("%v", v)
			applied++
		}
	}
	return fmt.Sprintf("%s（注入 %d 个变量，另 %d 个已被现有环境占用）", profile, applied, len(pairs)-applied)
}

// IsUp 判断调试端口是否可用
func (d *RuiyunUIDriver) IsUp() bool {
	tgts, err := cdp.ListTargets(d.Port)
	return err == nil && len(tgts) > 0
}

// Launch 唤醒应用
func (d *RuiyunUIDriver) Launch(wait bool) bool {
	if d.IsUp() {
		d.ReusedExisting = true
		return true
	}

	args := []string{
		d.Binary,
		fmt.Sprintf("--remote-debugging-port=%d", d.Port),
		"--remote-allow-origins=*",
		"--disable-gpu",
		"--disable-gpu-compositing",
		"--no-sandbox",
	}

	envMap := make(map[string]string)
	for _, envStr := range os.Environ() {
		parts := strings.SplitN(envStr, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	delete(envMap, "ELECTRON_RUN_AS_NODE")
	if _, ok := envMap["ELECTRON_ENABLE_LOGGING"]; !ok {
		envMap["ELECTRON_ENABLE_LOGGING"] = "0"
	}

	profileDesc := d.applyEnvProfile(envMap)
	d.EnvProfileDesc = profileDesc
	appCfg, _ := d.Cfg["app"].(map[string]any)
	logEnv := true
	if v, ok := appCfg["log_env_profile"].(bool); ok {
		logEnv = v
	}
	if logEnv {
		fmt.Printf("[应用] 启动环境：%s\n", profileDesc)
	}

	stale := sysutil.RunningAppPIDs(d.Binary)
	if len(stale) > 0 {
		if !d.RestartIfNoPort {
			fmt.Printf("[应用] 应用已在运行，但没有调试端口（pid %v），而配置里禁止自动重启：请先在界面点「✕ 关闭实例」，再运行测试\n", stale)
			return false
		}
		fmt.Printf("[应用] 发现 %d 个已运行的应用实例（缺少调试端口），先关闭再以调试模式启动\n", len(stale))
		_ = sysutil.KillProcesses(d.Binary)
	}

	var envSlice []string
	for k, v := range envMap {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	cmd, err := sysutil.SpawnProcess(args, envSlice)
	if err != nil {
		fmt.Printf("[应用] 启动失败: %v\n", err)
		return false
	}
	d.Proc = cmd
	d.LaunchedByUs = true

	if !wait {
		return true
	}

	ok := cdp.WaitForPort(d.Port, d.LaunchTimeout)
	if !ok && d.RestartIfNoPort {
		again := sysutil.RunningAppPIDs(d.Binary)
		if len(again) > 0 {
			fmt.Printf("[应用] 新进程被已有实例接管，关闭 %d 个已有实例后重试一次…\n", len(again))
			_ = sysutil.KillProcesses(d.Binary)
			cmd, err = sysutil.SpawnProcess(args, envSlice)
			if err == nil {
				d.Proc = cmd
				ok = cdp.WaitForPort(d.Port, d.LaunchTimeout)
			}
		}
	}
	if !ok {
		d.reportLaunchFailure()
	}
	return ok
}

func (d *RuiyunUIDriver) reportLaunchFailure() {
	hint := ""
	if _, err := os.Stat(d.Binary); err != nil {
		hint = fmt.Sprintf("可执行文件不存在：%s", d.Binary)
	} else {
		alive := sysutil.RunningAppPIDs(d.Binary)
		if len(alive) > 0 {
			hint = fmt.Sprintf("新进程还在运行，但端口 %d 没就绪；当前有 %d 个应用进程 —— 可能是安全软件拦截了调试端口，或该端口被别的程序占用", d.Port, len(alive))
		} else {
			hint = "应用进程不存在：启动参数或可执行文件路径有问题"
		}
	}
	fmt.Printf("[应用] 等待 %.0fs，调试端口 %d 仍未就绪 —— %s\n", d.LaunchTimeout, d.Port, hint)
}

// Attach 连接主界面
func (d *RuiyunUIDriver) Attach(timeoutS float64) bool {
	if timeoutS <= 0 {
		timeoutS = d.UIReadyTimeout
	}
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	for time.Now().Before(deadline) {
		targets, err := cdp.ListTargets(d.Port)
		if err == nil {
			for _, tgt := range targets {
				if tgt.Type != "page" || tgt.WebSocketDebuggerURL == "" {
					continue
				}
				session, err := cdp.NewCDPSession(tgt.WebSocketDebuggerURL, 10*time.Second)
				if err != nil {
					continue
				}
				session.EnableRuntime()
				val, err := session.EvalJS(ReadyJS, 10*time.Second, false)
				if err == nil && val != nil {
					if m, ok := val.(map[string]any); ok {
						if ready, _ := m["ready"].(bool); ready {
							d.CDP = session
							d.TargetURL = fmt.Sprintf("%v", m["href"])
							return true
						}
					}
				}
				_ = session.Close()
			}
		}
		time.Sleep(1 * time.Second)
	}

	targets, _ := cdp.ListTargets(d.Port)
	fmt.Printf("[应用] 等待 %.0fs，仍没有可用的主界面（调试端口上共 %d 个页面）\n", timeoutS, len(targets))
	return false
}

// EnsureReady 确保就绪
func (d *RuiyunUIDriver) EnsureReady() (bool, string) {
	if !d.Launch(true) {
		return false, "launch_failed"
	}
	how := "launched"
	if d.ReusedExisting {
		how = "reused"
	}
	if !d.Attach(0) {
		return false, how
	}
	return true, how
}

// Detach 断开 CDP 连接
func (d *RuiyunUIDriver) Detach() {
	if d.CDP != nil {
		_ = d.CDP.Close()
		d.CDP = nil
	}
}

// KillApp 关闭自己启动的应用
func (d *RuiyunUIDriver) KillApp() {
	if d.Proc != nil && d.LaunchedByUs {
		sysutil.TerminateProcessGroup(d.Proc)
		d.Proc = nil
		d.LaunchedByUs = false
	}
}

// CheckAppAlive 检查被测应用进程是否仍在运行
func (d *RuiyunUIDriver) CheckAppAlive() bool {
	if d.AppExited {
		return false
	}
	if strings.TrimSpace(d.Binary) == "" || len(sysutil.RunningAppPIDs(d.Binary)) > 0 {
		return true
	}
	d.AppExited = true
	citeEmit("[应用] 检测到被测应用进程已退出")
	return false
}

func (d *RuiyunUIDriver) Shutdown() {
	d.Detach()
}

func (d *RuiyunUIDriver) Discover() map[string]any {
	if d.CDP == nil {
		return map[string]any{}
	}
	res, err := d.CDP.EvalJS(DiscoverJS, 20*time.Second, false)
	if err == nil && res != nil {
		if m, ok := res.(map[string]any); ok {
			return m
		}
	}
	return map[string]any{}
}

func (d *RuiyunUIDriver) PageState() map[string]any {
	if d.CDP == nil {
		return map[string]any{}
	}
	res, err := d.CDP.EvalJS(PageStateJS, 15*time.Second, false)
	if err == nil && res != nil {
		if m, ok := res.(map[string]any); ok {
			return m
		}
	}
	return map[string]any{}
}

func (d *RuiyunUIDriver) DumpDOMSnapshot(name ...string) map[string]any {
	snapshotName := "ui_dom_error.json"
	if len(name) > 0 && name[0] != "" {
		snapshotName = name[0]
	}
	out := map[string]any{
		"page": d.PageState(),
		"dom":  d.Discover(),
	}
	artifactsDir := filepath.Join(".", "artifacts")
	_ = os.MkdirAll(artifactsDir, 0755)
	p := filepath.Join(artifactsDir, snapshotName)
	b, _ := json.MarshalIndent(out, "", "  ")
	_ = os.WriteFile(p, b, 0644)

	st, _ := out["page"].(map[string]any)
	summary := fmt.Sprintf("页面 %v | 登录页=%v | 输入区=%v | 正文 %v",
		st["href"], st["login_like"], st["has_composer"], st["body_head"])
	return map[string]any{
		"path":    p,
		"summary": summary,
	}
}

func (d *RuiyunUIDriver) resolveInput(waitS float64) string {
	if d.InputSelector != "" {
		return d.InputSelector
	}
	deadline := time.Now().Add(time.Duration(waitS * float64(time.Second)))
	for time.Now().Before(deadline) {
		for _, sel := range InputSelectors {
			selJSON, _ := json.Marshal(sel)
			expr := fmt.Sprintf(`(() => { const els=[...document.querySelectorAll(%s)];
 const v=els.find(e=>{const r=e.getBoundingClientRect(); return r.width>0&&r.height>0;});
 return v ? %s : null; })()`, selJSON, selJSON)
			val, err := d.CDP.EvalJS(expr, 15*time.Second, false)
			if err == nil && val != nil {
				resStr := fmt.Sprintf("%v", val)
				if resStr == sel {
					d.InputSelector = sel
					return sel
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	return ""
}

func (d *RuiyunUIDriver) focusInput(sel string) bool {
	selJSON, _ := json.Marshal(sel)
	expr := fmt.Sprintf(`(() => { const e=document.querySelector(%s);
 if(!e) return false; e.focus();
 const r=e.getBoundingClientRect();
 return r.width>0&&r.height>0; })()`, selJSON)
	val, err := d.CDP.EvalJS(expr, 15*time.Second, false)
	if err == nil && val != nil {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func (d *RuiyunUIDriver) elementCenter(sel string) map[string]float64 {
	selJSON, _ := json.Marshal(sel)
	expr := fmt.Sprintf(`(() => { const e=document.querySelector(%s);
 if(!e) return null; const r=e.getBoundingClientRect();
 if(r.width<=0||r.height<=0) return null;
 return {x: Math.round(r.x+r.width/2), y: Math.round(r.y+r.height/2)}; })()`, selJSON)
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err == nil && val != nil {
		if m, ok := val.(map[string]any); ok {
			var x, y float64
			if v, ok := m["x"].(float64); ok {
				x = v
			}
			if v, ok := m["y"].(float64); ok {
				y = v
			}
			return map[string]float64{"x": x, "y": y}
		}
	}
	return nil
}

func (d *RuiyunUIDriver) ClickAtPoint(pt map[string]float64) error {
	x, y := pt["x"], pt["y"]
	if err := d.CDP.DispatchMouseEvent("mousePressed", x, y, "left", 1); err != nil {
		return err
	}
	return d.CDP.DispatchMouseEvent("mouseReleased", x, y, "left", 1)
}

func (d *RuiyunUIDriver) clickSelector(sel string) bool {
	pt := d.elementCenter(sel)
	if pt == nil {
		return false
	}
	return d.ClickAtPoint(pt) == nil
}

func (d *RuiyunUIDriver) inputTextNow(sel string) string {
	selJSON, _ := json.Marshal(sel)
	expr := fmt.Sprintf(`(() => { const e=document.querySelector(%s);
 if(!e) return '';
 return (e.value !== undefined ? e.value : e.innerText || ''); })()`, selJSON)
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err == nil && val != nil {
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func (d *RuiyunUIDriver) ClearInput(sel string) {
	selJSON, _ := json.Marshal(sel)
	expr := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s);
  if (!el) return false;
  el.focus();
  if (el.value !== undefined) {
    const isTA = el.tagName === 'TEXTAREA';
    const proto = isTA ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
    setter.call(el, '');
  } else {
    el.innerText = '';
  }
  el.dispatchEvent(new Event('input', { bubbles: true }));
  return true;
})()`, selJSON)
	_, _ = d.CDP.EvalJS(expr, 15*time.Second, false)
	time.Sleep(200 * time.Millisecond)
}

// TypeText 输入文本
func (d *RuiyunUIDriver) TypeText(text string) error {
	sel := d.resolveInput(30.0)
	if sel == "" {
		info := d.DumpDOMSnapshot()
		return fmt.Errorf("未能定位输入框（现场已导出 %v）：%v", info["path"], info["summary"])
	}

	if !d.focusInput(sel) {
		d.InputSelector = ""
		sel = d.resolveInput(5.0)
		if sel == "" || !d.focusInput(sel) {
			return fmt.Errorf("输入框不可见，无法聚焦：%s", sel)
		}
	}

	d.ClearInput(sel)
	_ = d.CDP.InsertText(text)
	time.Sleep(400 * time.Millisecond)

	checkPrefix := text
	if len([]rune(checkPrefix)) > 12 {
		checkPrefix = string([]rune(checkPrefix)[:12])
	}
	if strings.Contains(d.inputTextNow(sel), checkPrefix) {
		return nil
	}

	// 回退 1
	if d.clickSelector(sel) {
		d.focusInput(sel)
		_ = d.CDP.InsertText(text)
		time.Sleep(400 * time.Millisecond)
		if strings.Contains(d.inputTextNow(sel), checkPrefix) {
			return nil
		}
	}

	// 回退 2
	payload, _ := json.Marshal(text)
	selJSON, _ := json.Marshal(sel)
	expr := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s);
  if (!el) return false;
  const isTA = el.tagName === 'TEXTAREA';
  const isInput = el.tagName === 'INPUT';
  if (isTA || isInput) {
    const proto = isTA ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    const setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
    setter.call(el, %s);
  } else {
    el.innerText = %s;
  }
  let ev;
  try {
    ev = new InputEvent('input', {bubbles: true, inputType: 'insertText', data: %s});
  } catch (err) {
    ev = new Event('input', {bubbles: true});
  }
  el.dispatchEvent(ev);
  el.dispatchEvent(new Event('change', {bubbles: true}));
  return true;
})()`, selJSON, payload, payload, payload)
	_, _ = d.CDP.EvalJS(expr, 15*time.Second, false)
	time.Sleep(300 * time.Millisecond)

	landed := d.inputTextNow(sel)
	if !strings.Contains(landed, checkPrefix) {
		info := d.DumpDOMSnapshot("ui_dom_input_failed.json")
		return fmt.Errorf("文本未写入输入框（当前内容 %q），已中止发送；现场已导出 %v：%v", landed, info["path"], info["summary"])
	}
	fmt.Println("[应用] 文本是靠「直接改写页面内容」写进去的：应用自身的输入逻辑没有生效")
	d.InputSelector = ""
	return nil
}

func (d *RuiyunUIDriver) AttachCount() *int {
	selJSON, _ := json.Marshal(AttachButtonSelector)
	expr := fmt.Sprintf(`(() => {
  const b = document.querySelector(%s);
  if (!b) return null;
  const m = /[(](\d+)\s*[/]\s*\d+[)]/.exec(b.getAttribute('title') || '');
  return m ? Number(m[1]) : null;
})()`, selJSON)
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err == nil && val != nil {
		if f, ok := val.(float64); ok {
			cnt := int(f)
			return &cnt
		}
	}
	return nil
}

func (d *RuiyunUIDriver) waitAttachCount(target int, timeoutS float64) bool {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	for time.Now().Before(deadline) {
		cnt := d.AttachCount()
		if cnt != nil && *cnt >= target {
			return true
		}
		time.Sleep(400 * time.Millisecond)
	}
	return false
}

func (d *RuiyunUIDriver) composerDropPoint() map[string]float64 {
	selJSON, _ := json.Marshal(ComposerSelector)
	expr := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s) || document.querySelector('textarea.chat-input-textarea');
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (r.width <= 0 || r.height <= 0) return null;
  return {x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height * 0.35)};
})()`, selJSON)
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err == nil && val != nil {
		if m, ok := val.(map[string]any); ok {
			var x, y float64
			if v, ok := m["x"].(float64); ok {
				x = v
			}
			if v, ok := m["y"].(float64); ok {
				y = v
			}
			return map[string]float64{"x": x, "y": y}
		}
	}
	return nil
}

func (d *RuiyunUIDriver) dragFiles(paths []string) error {
	pt := d.composerDropPoint()
	if pt == nil {
		return fmt.Errorf("找不到输入区，无法投递附件")
	}
	data := map[string]any{
		"items":              []map[string]string{{"mimeType": "text/plain", "data": ""}},
		"files":              paths,
		"dragOperationsMask": 1,
	}
	for _, ev := range []string{"dragEnter", "dragOver", "drop"} {
		if err := d.CDP.DispatchDragEvent(ev, pt["x"], pt["y"], data); err != nil {
			return err
		}
		time.Sleep(350 * time.Millisecond)
	}
	return nil
}

func (d *RuiyunUIDriver) pasteImage(path string) (bool, string) {
	ok, errStr := sysutil.ClipboardImage(path)
	if !ok {
		return false, errStr
	}
	sel := d.resolveInput(5.0)
	if sel == "" {
		return false, "找不到输入框，无法粘贴"
	}
	d.focusInput(sel)
	time.Sleep(200 * time.Millisecond)

	// Cmd+V / Ctrl+V
	modifiers := 4
	vk := 86
	if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
		modifiers = 2
	}

	keyParams := map[string]any{
		"type":                  "rawKeyDown",
		"commands":              []string{"paste"},
		"modifiers":             modifiers,
		"key":                   "v",
		"code":                  "KeyV",
		"windowsVirtualKeyCode": vk,
		"nativeVirtualKeyCode":  vk,
	}
	_, err := d.CDP.Call("Input.dispatchKeyEvent", keyParams, 10*time.Second)
	if err != nil {
		return false, fmt.Sprintf("粘贴事件发送失败: %v", err)
	}
	keyParams["type"] = "keyUp"
	delete(keyParams, "commands")
	_, _ = d.CDP.Call("Input.dispatchKeyEvent", keyParams, 10*time.Second)
	return true, "已发送粘贴指令"
}

// AttachFiles 投递本地文件附件
func (d *RuiyunUIDriver) AttachFiles(paths []string, timeoutS float64) (bool, string) {
	if len(paths) == 0 {
		return true, ""
	}
	var wanted []string
	for _, p := range paths {
		if s := strings.TrimSpace(p); s != "" {
			wanted = append(wanted, s)
		}
	}
	if len(wanted) == 0 {
		return true, ""
	}

	var missing []string
	for _, p := range wanted {
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return false, "附件不存在，未投递：" + strings.Join(missing, "、")
	}

	before := d.AttachCount()
	if before == nil {
		return false, "无法读取「引用本地文件」计数（附件按钮不在页面上）"
	}

	var errs []string
	if err := d.dragFiles(wanted); err != nil {
		errs = append(errs, fmt.Sprintf("拖拽投递异常: %v", err))
	}
	if d.waitAttachCount(*before+len(wanted), timeoutS) {
		cnt := d.AttachCount()
		return true, fmt.Sprintf("已引用 %d 个本地文件（本轮投递 %d 个）", *cnt, len(wanted))
	}

	var imgs []string
	for _, p := range wanted {
		ext := strings.ToLower(filepath.Ext(p))
		if ImageExts[ext] {
			imgs = append(imgs, p)
		}
	}
	if len(imgs) > 0 {
		ok, msg := d.pasteImage(imgs[0])
		if !ok {
			errs = append(errs, msg)
		} else if d.waitAttachCount(*before+1, timeoutS) {
			return true, fmt.Sprintf("已通过粘贴引用 1 个图片文件（%s）", filepath.Base(imgs[0]))
		}
	}

	after := d.AttachCount()
	afterStr := "未知"
	if after != nil {
		afterStr = fmt.Sprintf("%d", *after)
	}
	errMsg := fmt.Sprintf("附件未投递成功（引用计数未增加：投递前 %d、投递后 %s，期望 +%d）", *before, afterStr, len(wanted))
	if len(errs) > 0 {
		errMsg += "；" + strings.Join(errs, "；")
	}
	return false, errMsg
}

func (d *RuiyunUIDriver) composerReady() bool {
	expr := `(() => { const e=document.querySelector('textarea');
 if(!e) return false; const r=e.getBoundingClientRect();
 return r.width>0 && r.height>0; })()`
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err == nil && val != nil {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

// ResetToNewTask 回到「新建任务」首页
func (d *RuiyunUIDriver) ResetToNewTask(waitS float64) bool {
	if waitS <= 0 {
		waitS = 15.0
	}
	expr := `(() => {
  const btns = [...document.querySelectorAll('button')];
  const b = btns.find(x => ((x.innerText||'').trim().includes('新建任务'))
                || ((x.className||'').toString().includes('sidebar-primary-action')));
  if (!b) return null;
  b.click();
  return (b.innerText||'').trim().slice(0, 20);
})()`
	val, err := d.CDP.EvalJS(expr, 15*time.Second, false)
	if err != nil || val == nil {
		return false
	}

	deadline := time.Now().Add(time.Duration(waitS * float64(time.Second)))
	for time.Now().Before(deadline) {
		if d.composerReady() {
			time.Sleep(400 * time.Millisecond)
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func (d *RuiyunUIDriver) sendButton(click bool) string {
	kwsJSON, _ := json.Marshal(SendKeywords)
	clickStr := "false"
	if click {
		clickStr = "true"
	}
	expr := fmt.Sprintf(SendButtonJSTemplate, kwsJSON, clickStr)
	val, err := d.CDP.EvalJS(expr, 15*time.Second, false)
	if err == nil && val != nil {
		return fmt.Sprintf("%v", val)
	}
	return ""
}

func (d *RuiyunUIDriver) inputTextNowAny() string {
	sel := d.InputSelector
	if sel == "" {
		sel = d.resolveInput(3.0)
	}
	if sel != "" {
		return d.inputTextNow(sel)
	}
	return ""
}

func (d *RuiyunUIDriver) waitInputCleared(timeoutS float64) bool {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	for time.Now().Before(deadline) {
		if strings.TrimSpace(d.inputTextNowAny()) == "" {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// sendOutcome 判定一次发送尝试的结果。
//
// 判据只有一条：**输入框是否被清空**。应用收下内容后必然清空输入框，
// 所以「还留着原文」就等于发送没生效。
// 抽成纯函数是为了能直接单测这条判据 —— Send 本身依赖 CDP，无法离线跑。
//
// 返回 (是否成功, 是否需要继续尝试下一种发送方式)。
func sendOutcome(landedBefore string, clearedAfter bool) (ok bool, keepTrying bool) {
	if strings.TrimSpace(landedBefore) == "" {
		// 本来就是空的：没有「已清空」这个信号可用，按成功处理（沿用既有行为）
		return true, false
	}
	if clearedAfter {
		return true, false
	}
	return false, true
}

// sendFailureError 组装发送失败的报错文案（输入框残留内容 + 按钮状态）
func sendFailureError(left, desc, snapshotPath string) error {
	head := left
	if len([]rune(head)) > 40 {
		head = string([]rune(head)[:40])
	}
	if desc != "" && !strings.HasPrefix(desc, "disabled:") {
		return fmt.Errorf("发送未生效：输入框仍留有内容（%q），发送按钮 %s；现场已导出 %v",
			head, desc, snapshotPath)
	}
	if strings.HasPrefix(desc, "disabled:") {
		return fmt.Errorf("发送未生效：输入框仍留有内容（%q），发送按钮不可点（%s）；现场已导出 %v",
			head, desc, snapshotPath)
	}
	return fmt.Errorf("发送未生效：输入框仍留有内容（%q）；未找到可用的发送按钮；现场已导出 %v",
		head, snapshotPath)
}

// Send 发送当前输入框里的内容，成功返回发送方式，失败返回 error。
//
// 「发送成功」的判据是**输入框被清空**：无论是点发送按钮还是按回车，
// 应用把内容收下之后都会清空输入框。若三种方式试完输入框还留着原文，
// 就说明这次发送根本没生效 —— 必须报错，不能当成功返回。
//
// 这一点对多轮至关重要：发送没生效时，会话日志不会有任何新消息，
// 上层只能等到「本轮等待超时」，报出来的原因是「等待超时」而不是
// 「发送失败」，排查时会往错误的方向找。历史实现恒返回 nil error，
// 调用方（流水线、复现率验证器）的 err 分支形同虚设。
func (d *RuiyunUIDriver) Send() (string, error) {
	landed := d.inputTextNowAny()
	wasEmpty := strings.TrimSpace(landed) == ""
	desc := d.sendButton(true)
	clicked := desc != "" && !strings.HasPrefix(desc, "disabled:")

	// 输入框本来就是空的：没有可判断的「已清空」信号，沿用既有行为按成功返回。
	if ok, _ := sendOutcome(landed, false); ok && wasEmpty {
		if clicked {
			return fmt.Sprintf("button(%s)", desc), nil
		}
		_ = d.CDP.PressKey("Enter", "Enter", 13, 13, "")
		return "enter(空输入框)", nil
	}

	if clicked && d.waitInputCleared(1.5) {
		return fmt.Sprintf("button(%s)", desc), nil
	}

	_ = d.CDP.PressKey("Enter", "Enter", 13, 13, "")
	if d.waitInputCleared(1.2) {
		return "enter", nil
	}

	_ = d.CDP.PressKey("Enter", "Enter", 13, 13, "\r")
	if d.waitInputCleared(1.2) {
		return "enter(char)", nil
	}

	// 三种方式都没让输入框清空 —— 这次发送没生效
	info := d.DumpDOMSnapshot("ui_send_failed.json")
	return "", sendFailureError(d.inputTextNowAny(), desc, fmt.Sprintf("%v", info["path"]))
}

func (d *RuiyunUIDriver) ComposerState() map[string]any {
	sel := d.InputSelector
	if sel == "" {
		sel = d.resolveInput(3.0)
	}
	text := ""
	if sel != "" {
		text = d.inputTextNow(sel)
	}
	textHead := text
	if len([]rune(textHead)) > 40 {
		textHead = string([]rune(textHead)[:40])
	}
	return map[string]any{
		"input_selector": sel,
		"text_len":       len([]rune(text)),
		"text_head":      textHead,
		"send_button":    d.sendButton(false),
	}
}

// SnapshotSessions 获取会话目录快照
func (d *RuiyunUIDriver) SnapshotSessions() map[string]bool {
	res := make(map[string]bool)
	entries, err := os.ReadDir(d.SessionRoot)
	if err != nil {
		return res
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "sess_") {
			res[e.Name()] = true
		}
	}
	return res
}

// WaitNewSession 等待新会话产生
func (d *RuiyunUIDriver) WaitNewSession(before map[string]bool, timeoutS float64) (string, error) {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	for time.Now().Before(deadline) {
		if !d.CheckAppAlive() {
			return "", fmt.Errorf("被测应用已退出")
		}
		current := d.SnapshotSessions()
		var newestDir string
		var newestMtime int64
		for name := range current {
			if !before[name] {
				p := filepath.Join(d.SessionRoot, name)
				if fi, err := os.Stat(p); err == nil {
					mt := fi.ModTime().UnixNano()
					if mt > newestMtime {
						newestMtime = mt
						newestDir = p
					}
				}
			}
		}
		if newestDir != "" {
			return newestDir, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !d.CheckAppAlive() {
		return "", fmt.Errorf("被测应用已退出")
	}
	return "", nil
}

// assistantMessageCount 统计会话里「已完成」的 assistant 消息条数。
//
// 多轮发送靠它判断「本轮是否真的开始了」与「本轮是否答完了」：
// 第 N 轮发送后会话里应当多出一条 assistant 消息，只有这条消息
// 不再处于流式状态，才说明本轮生成结束。
// 只看 session.messages.json（发送后会立即出现该轮的用户消息）。
func assistantMessageCount(sessDir string) int {
	data, err := os.ReadFile(filepath.Join(sessDir, "session.messages.json"))
	if err != nil {
		return -1
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return -1
	}
	msgs, ok := root["messages"].([]any)
	if !ok {
		return -1
	}
	n := 0
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", mm["role"]) != "assistant" {
			continue
		}
		if isStreaming, ok := mm["isStreaming"].(bool); ok && isStreaming {
			continue // 还在流式输出，这轮没结束
		}
		content := strings.TrimSpace(fmt.Sprintf("%v", mm["content"]))
		completedAt := fmt.Sprintf("%v", mm["completedAt"])
		if content != "" || (completedAt != "" && completedAt != "<nil>") {
			n++
		}
	}
	return n
}

// AssistantCount 取会话里已完成 assistant 消息的条数（导出给流水线用）。
func (d *RuiyunUIDriver) AssistantCount(sessDir string) int {
	return assistantMessageCount(sessDir)
}

// composerBusy 判断输入区当前是否处于「生成中」：此时应用会把发送按钮
// 换成停止按钮，输入框也不接受新内容，这时候发送必然失败。
//
// 判据取「可见按钮里有没有 stop 类按钮」——比读文案稳，不受中英文与
// 提示语改动影响。看 DOM 实测：生成中输入区只有 chat-input-btn--stop。
func (d *RuiyunUIDriver) composerBusy() bool {
	if d.CDP == nil {
		return false // 未连接 CDP（如单测环境）：无从判断，按「不在生成」处理
	}
	expr := `(() => {
  const els = [...document.querySelectorAll('button,[role="button"]')];
  for (const el of els) {
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) continue;
    const blob = ((el.className||'').toString() + ' '
                + (el.getAttribute('aria-label')||'') + ' '
                + (el.getAttribute('title')||'')).toLowerCase();
    if (blob.includes('stop') || blob.includes('停止')) return true;
  }
  return false;
})()`
	val, err := d.CDP.EvalJS(expr, 15*time.Second, false)
	if err != nil || val == nil {
		return false
	}
	b, _ := val.(bool)
	return b
}

// WaitComposerReady 等输入区回到「可以发送下一轮」的状态。
//
// 两个条件都满足才算就绪：
//  1. 停止按钮消失（应用不再生成）
//  2. 会话日志静默且本轮答复已完整（sessDir 为空时跳过这条）
//
// 为什么需要它：WaitNewSession 只等会话目录出现，而目录在**发送瞬间**就
// 建好了，并不代表这一轮答完。多轮时若不等就发下一轮，应用仍在生成、
// 发送按钮已被换成停止按钮，发送必然失败（线上 run_20261016_162406）。
func (d *RuiyunUIDriver) WaitComposerReady(timeoutS, quietS float64, sessDir string) bool {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	msgFile := ""
	if sessDir != "" {
		msgFile = filepath.Join(sessDir, "session.messages.json")
	}
	var lastSig [2]int64
	lastChange := time.Now()

	for time.Now().Before(deadline) {
		if !d.CheckAppAlive() {
			return false
		}
		if msgFile != "" {
			fi, err := os.Stat(msgFile)
			var sig [2]int64
			if err == nil {
				sig = [2]int64{fi.Size(), fi.ModTime().UnixNano()}
			}
			if sig != lastSig {
				lastSig = sig
				lastChange = time.Now()
			}
		}
		quietOK := msgFile == "" || (time.Since(lastChange).Seconds() >= quietS && looksComplete(msgFile))
		if !d.composerBusy() && quietOK {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// userMessageCount 数会话里「已完成」的 user 消息；取不到时返回 -1。
//
// 与 assistantMessageCount 的区别：assistant 的内容是逐步流式写入的，
// user 消息则在应用收下提问时就落盘了 —— 所以用它核对「某一轮到底有没有
// 发出去」比 assistant 更直接。
func userMessageCount(sessDir string) int {
	data, err := os.ReadFile(filepath.Join(sessDir, "session.messages.json"))
	if err != nil {
		return -1
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return -1
	}
	msgs, ok := root["messages"].([]any)
	if !ok {
		return -1
	}
	n := 0
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", mm["role"]) != "user" {
			continue
		}
		if strings.TrimSpace(fmt.Sprintf("%v", mm["content"])) != "" {
			n++
		}
	}
	return n
}

// UserMessageCount 会话里已完成提问的条数（取不到返回 -1）
func (d *RuiyunUIDriver) UserMessageCount(sessDir string) int {
	return userMessageCount(sessDir)
}

// WaitTurnDone 等待「第 N 轮」生成完成。
//
// 与 WaitSettled 的区别（多轮必须用它）：
// WaitSettled 只要求「会话日志静默且末条是已完成 assistant」——
// 在轮间调用时，上一轮的日志本来就是静默且完成的，它会立刻返回 true，
// 于是后一轮还没开始生成就被当成结束（会少发/漏收轮次）。
// 这里先要求 assistant 条数比发送前**多**（说明本轮真的开始了），
// 再要求日志静默，两个条件都满足才算本轮结束。
//
// prevCount 为发送前的 assistant 条数（用 assistantMessageCount 取）。
// 取不到时传 -1：会把首次可读到的条数当成本轮基准，仍要求**更多**
// assistant 才算本轮开始，绝不因此跳过判据。
//
// 为什么不能因取不到条数就放行：那样上一轮残留的静默状态会被当成本轮
// 已完成，函数立刻返回 true，而本轮其实一次都没发出去。实测现象就是
// 控制台报「第 2 轮已完成」、会话里却只有一轮。
func (d *RuiyunUIDriver) WaitTurnDone(sessDir string, prevCount int, timeoutS, quietS float64) bool {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	msgFile := filepath.Join(sessDir, "session.messages.json")
	var lastSig [2]int64
	lastChange := time.Now()
	started := false

	for time.Now().Before(deadline) {
		if !d.CheckAppAlive() {
			return false
		}
		if d.maybeAutoConfirm(false, "") != nil {
			lastChange = time.Now()
		}
		fi, err := os.Stat(msgFile)
		var sig [2]int64
		if err == nil {
			sig = [2]int64{fi.Size(), fi.ModTime().UnixNano()}
		}
		if sig != lastSig {
			lastSig = sig
			lastChange = time.Now()
		}
		// 本轮开始的判据：assistant 条数**多于**发送前的条数。
		//
		// prevCount 可能是 -1（发送前那一刻文件还没写成 / 解析不了）。
		// 这种情况**不能**当作「跳过判据」：一旦跳过，上一轮残留的静默状态
		// 会被当成本轮已完成，函数立刻返回 true，本轮根本没发出去 ——
		// 实测就是「第 2 轮已完成」但会话里只有一轮的原因。
		// 正确做法是等到能读出真实条数为止（用本轮的局部基准）。
		if !started {
			if cur := assistantMessageCount(sessDir); cur >= 0 {
				if prevCount < 0 {
					// 发送前取不到基准：用首次可读到的条数当基准，
					// 之后必须看到比它更多的 assistant 才算本轮开始。
					prevCount = cur
				} else if cur > prevCount {
					started = true
				}
			}
		}
		// 本轮结束的判据：已开始 + 日志静默 + 末条是已完成 assistant
		if started {
			if sig[0] > 0 && time.Since(lastChange).Seconds() >= quietS && looksComplete(msgFile) {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func looksComplete(msgFile string) bool {
	data, err := os.ReadFile(msgFile)
	if err != nil {
		return false
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return false
	}
	msgs, ok := root["messages"].([]any)
	if !ok || len(msgs) < 2 {
		return false
	}
	last, ok := msgs[len(msgs)-1].(map[string]any)
	if !ok {
		return false
	}
	if fmt.Sprintf("%v", last["role"]) != "assistant" {
		return false
	}
	if isStreaming, ok := last["isStreaming"].(bool); ok && isStreaming {
		return false
	}
	content := strings.TrimSpace(fmt.Sprintf("%v", last["content"]))
	completedAt := fmt.Sprintf("%v", last["completedAt"])
	return content != "" || (completedAt != "" && completedAt != "<nil>")
}

func (d *RuiyunUIDriver) SetCurrentSess(sess string) {
	d.CurrentSess = sess
}

func (d *RuiyunUIDriver) ownerSess(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if d.QCardViewSess != "" {
		return d.QCardViewSess
	}
	return d.CurrentSess
}

func (d *RuiyunUIDriver) qcardFacts() map[string]any {
	val, err := d.CDP.EvalJS(QCardJS, 10*time.Second, false)
	if err == nil && val != nil {
		if m, ok := val.(map[string]any); ok {
			return m
		}
	}
	return nil
}

func (d *RuiyunUIDriver) qcardClick(kind string, idx int, text string) map[string]any {
	kJSON, _ := json.Marshal(kind)
	iJSON, _ := json.Marshal(idx)
	tJSON, _ := json.Marshal(text)
	expr := fmt.Sprintf(QCardClickJSTemplate, kJSON, iJSON, tJSON)
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err == nil && val != nil {
		if m, ok := val.(map[string]any); ok {
			return m
		}
	}
	return map[string]any{"ok": false, "reason": "eval-error"}
}

func (d *RuiyunUIDriver) qcardFP(f map[string]any) string {
	if f == nil || f["found"] != true {
		return "(无卡片)"
	}
	var opts []string
	if options, ok := f["options"].([]any); ok {
		for _, o := range options {
			if om, ok := o.(map[string]any); ok {
				sel := ""
				if om["selected"] == true {
					sel = "#"
				}
				opts = append(opts, fmt.Sprintf("%v%s", om["label"], sel))
			}
		}
	}
	btnKind := ""
	if primary, ok := f["primary"].(map[string]any); ok && primary != nil {
		btnKind = fmt.Sprintf("%v", primary["kind"])
	} else if skip, ok := f["skip"].(map[string]any); ok && skip != nil {
		btnKind = fmt.Sprintf("%v", skip["kind"])
	}
	return fmt.Sprintf("%v::%v::%s::%s", f["counter"], f["question"], strings.Join(opts, "|"), btnKind)
}

func (d *RuiyunUIDriver) qcardReset() {
	d.QCardFPSig = ""
	d.QCardFPSince = 0.0
	d.QCardFPClicks = make(map[string]int)
	d.QCardCardClicks = 0
	d.QCardCardSince = 0.0
	d.QCardLastClick = 0.0
	d.QCardOwnerSess = ""
}

func (d *RuiyunUIDriver) qcardStop(why string) {
	d.AutoConfirm = false
	d.ConfirmHumanNeeded = true
	d.ConfirmHumanSess = d.ownerSess("")
	citeEmit(fmt.Sprintf("[确认] %s，已停用自动确认，需要你在应用界面手动选择", why))
}

func (d *RuiyunUIDriver) qcardEmit(mode string, facts map[string]any, res map[string]any, ownerSess string) map[string]any {
	fp := d.qcardFP(facts)
	d.QCardFPClicks[fp]++
	d.QCardLastClick = float64(time.Now().UnixNano()) / 1e9
	q := fmt.Sprintf("%v", facts["counter"])
	question := fmt.Sprintf("%v", facts["question"])

	if res["ok"] != true {
		citeEmit(citeLine(mode, q, question, "", fmt.Sprintf("没点动：%v", res["reason"])))
		return nil
	}

	d.QCardCardClicks++
	cls := "agent-question-composer__primary"
	if mode == "qcard-option" {
		cls = "agent-question-option"
	} else if mode == "qcard-fill" {
		cls = "agent-question-other"
	}

	ev := map[string]any{
		"time":     time.Now().Format("15:04:05"),
		"ts":       float64(time.Now().UnixNano()) / 1e9,
		"mode":     mode,
		"text":     fmt.Sprintf("%v", res["text"]),
		"cls":      cls,
		"q":        q,
		"question": question,
		"sess":     d.ownerSess(ownerSess),
	}
	d.ConfirmEvents = append(d.ConfirmEvents, ev)
	citeEmit(citeLine(mode, q, question, fmt.Sprintf("%v", ev["text"]), ""))
	return ev
}

func (d *RuiyunUIDriver) qcardHandle(ownerSess string) map[string]any {
	if d.CDP == nil {
		return nil
	}
	f := d.qcardFacts()
	if f == nil || f["found"] != true {
		d.qcardReset()
		return nil
	}
	if ownerSess != "" && d.QCardOwnerSess == "" {
		d.QCardOwnerSess = ownerSess
	}

	now := float64(time.Now().UnixNano()) / 1e9
	if d.QCardCardSince == 0 {
		d.QCardCardSince = now
	}
	fp := d.qcardFP(f)
	if fp != d.QCardFPSig {
		d.QCardFPSig = fp
		d.QCardFPSince = now
		d.QCardLastClick = 0.0
	}

	if d.QCardFPClicks[fp] >= d.QCardSameLimit {
		d.qcardStop(fmt.Sprintf("同一状态已尝试 %d 次仍未推进（题 %v）", d.QCardSameLimit, f["counter"]))
		return nil
	}
	if d.QCardCardClicks >= d.QCardCardClicksLimit {
		d.qcardStop(fmt.Sprintf("本张卡片累计点击 %d 次仍未完成", d.QCardCardClicks))
		return nil
	}
	if now-d.QCardCardSince > d.QCardCardMaxS {
		d.qcardStop(fmt.Sprintf("卡片存活超过 %.0fs 仍未完成", d.QCardCardMaxS))
		return nil
	}
	if now-d.QCardFPSince > d.QCardStallS {
		d.qcardStop(fmt.Sprintf("停滞 %.0fs 无进展（题 %v）", d.QCardStallS, f["counter"]))
		return nil
	}
	if now-d.QCardLastClick < d.QCardMinClickGap {
		return nil
	}
	if submitting, ok := f["submitting"].(bool); ok && submitting {
		return nil
	}

	var opts []map[string]any
	if options, ok := f["options"].([]any); ok {
		for _, o := range options {
			if om, ok := o.(map[string]any); ok {
				if om["visible"] == true && om["disabled"] != true {
					opts = append(opts, om)
				}
			}
		}
	}

	answered := false
	if options, ok := f["options"].([]any); ok {
		for _, o := range options {
			if om, ok := o.(map[string]any); ok && om["selected"] == true {
				answered = true
				break
			}
		}
	}
	if customInput, ok := f["customInput"].(string); ok && strings.TrimSpace(customInput) != "" {
		answered = true
	}

	if !answered {
		if len(opts) > 0 {
			return d.qcardEmit("qcard-option", f, d.qcardClick("option", 0, ""), ownerSess)
		}
		if f["customInput"] != nil && f["inputDisabled"] != true {
			cIn := strings.TrimSpace(fmt.Sprintf("%v", f["customInput"]))
			if cIn == "" {
				return d.qcardEmit("qcard-fill", f, d.qcardClick("fill-other", 0, d.QCardFillText), ownerSess)
			}
		}
		return nil
	}

	if primary, ok := f["primary"].(map[string]any); ok && primary != nil && primary["disabled"] != true {
		mode := "qcard-confirm"
		if isLast, ok := f["isLast"].(bool); ok && isLast {
			mode = "qcard-submit"
		}
		return d.qcardEmit(mode, f, d.qcardClick("primary", 0, ""), ownerSess)
	}
	return nil
}

func compactConfirmError(err error) string {
	if err == nil {
		return "未知错误"
	}
	msg := strings.TrimSpace(err.Error())
	runes := []rune(msg)
	if len(runes) > 240 {
		msg = string(runes[:240]) + "…"
	}
	return msg
}

func (d *RuiyunUIDriver) confirmFail(msg string) {
	d.ConfirmCDPStreak++
	if d.ConfirmCDPStreak == 1 || d.ConfirmCDPStreak == 5 {
		citeEmit(fmt.Sprintf("[自动确认] Runtime.evaluate 连续失败（%d 次）：%s", d.ConfirmCDPStreak, msg))
	}
	if d.ConfirmCDPStreak >= 5 {
		d.AutoConfirm = false
		citeEmit("[自动确认] 连续 5 次执行失败，已停用自动点击；当前状态不代表存在待处理确认卡片")
	}
}

func autoConfirmScript(confirmKeywords []string) string {
	if confirmKeywords == nil {
		confirmKeywords = []string{}
	}
	prefJSON, _ := json.Marshal(confirmKeywords)
	denyJSON, _ := json.Marshal(DenyWords)
	return fmt.Sprintf(AutoConfirmJSTemplate, prefJSON, denyJSON)
}

func (d *RuiyunUIDriver) maybeAutoConfirm(skipQCard bool, qcardOwner string) map[string]any {
	if !d.AutoConfirm || d.CDP == nil {
		return nil
	}
	if len(d.ConfirmEvents) >= d.ConfirmMaxClicks {
		d.AutoConfirm = false
		citeEmit(fmt.Sprintf("[确认] 自动确认累计已达 %d 次，已停用自动点击；当前状态不代表存在待处理确认卡片", d.ConfirmMaxClicks))
		return nil
	}

	if !skipQCard {
		if qev := d.qcardHandle(qcardOwner); qev != nil {
			return qev
		}
	}

	expr := autoConfirmScript(d.ConfirmKeywords)

	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err != nil {
		firstErr := err
		d.Detach()
		if !d.Attach(15.0) {
			d.confirmFail("首次错误：" + compactConfirmError(firstErr) + "；重连页面失败")
			return nil
		}
		val, err = d.CDP.EvalJS(expr, 10*time.Second, false)
		if err != nil {
			d.confirmFail("首次错误：" + compactConfirmError(firstErr) + "；重连后错误：" + compactConfirmError(err))
			return nil
		}
	}

	if val == nil {
		d.ConfirmCDPStreak = 0
		return nil
	}

	info, ok := val.(map[string]any)
	if !ok || len(info) == 0 {
		d.ConfirmCDPStreak = 0
		return nil
	}

	now := float64(time.Now().UnixNano()) / 1e9
	mode := fmt.Sprintf("%v", info["mode"])
	sig := fmt.Sprintf("%v", info["text"])
	if mode == "first-option" {
		var texts []string
		if ts, ok := info["texts"].([]any); ok {
			for _, t := range ts {
				texts = append(texts, fmt.Sprintf("%v", t))
			}
		}
		sort.Strings(texts)
		sig = strings.Join(texts, "|")
	}

	rec, ok := d.ClickedGroups[sig]
	if ok {
		tsVal, _ := rec["ts"].(float64)
		if now-tsVal < d.ConfirmCooldown {
			return nil
		}
	} else {
		rec = map[string]any{"attempts": 0}
	}
	rec["ts"] = now
	att, _ := rec["attempts"].(int)
	rec["attempts"] = att + 1
	d.ClickedGroups[sig] = rec

	if mode == "first-option" && att+1 > 5 {
		d.AutoConfirm = false
		d.ConfirmHumanNeeded = true
		d.ConfirmHumanSess = d.ownerSess(qcardOwner)
		citeEmit("[确认] 同一个选项卡片点了 5 次仍未消失，已停用自动确认，需要你在应用界面手动处理")
		return nil
	}

	ev := map[string]any{
		"time":      time.Now().Format("15:04:05"),
		"ts":        now,
		"mode":      mode,
		"text":      fmt.Sprintf("%v", info["text"]),
		"cls":       fmt.Sprintf("%v", info["cls"]),
		"parentCls": fmt.Sprintf("%v", info["parentCls"]),
		"sess":      d.ownerSess(qcardOwner),
	}
	d.ConfirmEvents = append(d.ConfirmEvents, ev)
	citeEmit(citeLine(mode, "", fmt.Sprintf("%v", info["question"]), fmt.Sprintf("%v", ev["text"]), ""))
	time.Sleep(800 * time.Millisecond)
	return ev
}

// WaitSettled 等待会话稳定
func (d *RuiyunUIDriver) WaitSettled(sessDir string, timeoutS, quietS float64) bool {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	var lastSig [2]int64
	lastChange := time.Now()

	for time.Now().Before(deadline) {
		if !d.CheckAppAlive() {
			return false
		}
		if d.maybeAutoConfirm(false, "") != nil {
			lastChange = time.Now()
		}
		msgFile := filepath.Join(sessDir, "session.messages.json")
		var sig [2]int64
		if fi, err := os.Stat(msgFile); err == nil {
			sig = [2]int64{fi.Size(), fi.ModTime().UnixNano()}
		}
		if sig != lastSig {
			lastSig = sig
			lastChange = time.Now()
		} else {
			if sig[0] > 0 && time.Since(lastChange).Seconds() >= quietS && looksComplete(msgFile) {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// SidebarItem 侧边栏任务项
type SidebarItem struct {
	Idx     int    `json:"idx"`
	Title   string `json:"title"`
	Time    string `json:"time"`
	Unread  bool   `json:"unread"`
	Status  string `json:"status"`
	Visible bool   `json:"visible"`
}

func (d *RuiyunUIDriver) sidebarItems() []SidebarItem {
	expr := `(() => {
  const aside = document.querySelector('aside.session-sidebar,aside[class*="session-sidebar"]');
  if (!aside) return null;
  const nodes = [...aside.querySelectorAll('.session-sidebar-task-item')];
  return nodes.map((el, idx) => {
    const btn = el.querySelector('.session-sidebar-task-item__button');
    const tm = el.querySelector('.session-sidebar-task-item__time');
    const r = el.getBoundingClientRect();
    return { idx,
      title: ((btn && btn.innerText) || '').trim(),
      time: ((tm && tm.innerText) || '').trim(),
      unread: !!el.querySelector('[class*="unread"]'),
      status: el.getAttribute('data-status-kind') || '',
      visible: r.width > 0 && r.height > 0 };
  }).filter(x => x.visible);
})()`
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	if err != nil || val == nil {
		return nil
	}
	b, _ := json.Marshal(val)
	var items []SidebarItem
	_ = json.Unmarshal(b, &items)
	return items
}

var timeTailRegex = regexp.MustCompile(`\s*(\d+\s*(?:分钟|小时|天)前|刚刚|昨天|前天)$`)

func normKey(t string) string {
	t = strings.Join(strings.Fields(t), "")
	for i := 0; i < 2; i++ {
		t = timeTailRegex.ReplaceAllString(t, "")
		t = strings.TrimRight(t, ".。…")
	}
	return t
}

func (d *RuiyunUIDriver) findItem(items []SidebarItem, prompt string) *SidebarItem {
	key := normKey(prompt)
	if key == "" {
		return nil
	}
	var cands []SidebarItem
	for _, it := range items {
		t := normKey(it.Title)
		if t == "" {
			continue
		}
		if strings.HasPrefix(key, t) || strings.HasPrefix(t, key[:minInt(len([]rune(key)), len([]rune(t)))]) {
			cands = append(cands, it)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	for _, it := range cands {
		if it.Unread {
			return &it
		}
	}
	for _, it := range cands {
		sig := fmt.Sprintf("%s::%s", it.Title, it.Time)
		if !d.CycleVisited[sig] {
			return &it
		}
	}
	return &cands[0]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (d *RuiyunUIDriver) openConversation(idx int) bool {
	expr := fmt.Sprintf(`((idx) => {
  const aside = document.querySelector('aside.session-sidebar,aside[class*="session-sidebar"]');
  const el = aside.querySelectorAll('.session-sidebar-task-item')[idx];
  if (!el) return null;
  const btn = el.querySelector('.session-sidebar-task-item__button') || el;
  btn.click();
  return (btn.innerText || '').trim().slice(0, 40);
})(%d)`, idx)
	val, err := d.CDP.EvalJS(expr, 10*time.Second, false)
	return err == nil && val != nil
}

// InflightPair 会话目录与提示词映射
type InflightPair struct {
	SessionDir string
	Prompt     string
}

// CycleAndConfirm 切换在途会话巡检确认卡片
func (d *RuiyunUIDriver) CycleAndConfirm(pairs []InflightPair, remaining map[string]bool) int {
	if !d.AutoConfirm || d.CDP == nil || d.ViewCycleDisabled {
		return 0
	}
	var todo []InflightPair
	for _, p := range pairs {
		if remaining[p.SessionDir] {
			todo = append(todo, p)
		}
	}
	if len(todo) == 0 {
		return 0
	}

	cur := d.qcardFacts()
	if cur != nil && cur["found"] == true {
		ev := d.qcardHandle(d.QCardViewSess)
		if ev != nil {
			return 1
		}
		return 0
	}

	items := d.sidebarItems()
	if items == nil {
		d.CycleFailStreak++
		if d.CycleFailStreak >= 3 {
			d.ViewCycleDisabled = true
			citeEmit("[确认] 连续 3 轮读不到会话列表，已停用视图巡检，改为只扫描当前会话（后台会话的确认卡片可能因此被漏掉）")
		}
		return 0
	}
	d.CycleFailStreak = 0
	d.CycleVisited = make(map[string]bool)
	clicked := 0

	for _, p := range todo {
		it := d.findItem(items, p.Prompt)
		if it == nil {
			continue
		}
		sig := fmt.Sprintf("%s::%s", it.Title, it.Time)
		d.CycleVisited[sig] = true
		if !d.openConversation(it.Idx) {
			continue
		}
		d.QCardViewSess = p.SessionDir
		time.Sleep(500 * time.Millisecond)

		ev := d.qcardHandle(p.SessionDir)
		if ev != nil {
			clicked++
		}
		if d.QCardOwnerSess != "" {
			break
		}
		ev2 := d.maybeAutoConfirm(true, p.SessionDir)
		if ev2 != nil {
			clicked++
		}
	}
	if clicked > 0 {
		citeEmit(fmt.Sprintf("[确认] 本轮切换 %d 条会话，共确认 %d 次", len(d.CycleVisited), clicked))
	}
	return clicked
}

// WaitSomeSettled 滑动窗口多会话等待
func (d *RuiyunUIDriver) WaitSomeSettled(sessions []string, timeoutS, quietS float64, inflightPairs []InflightPair, cycleS float64) []string {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	sigs := make(map[string][2]int64)
	lastChange := make(map[string]time.Time)
	for _, s := range sessions {
		lastChange[s] = time.Now()
	}

	remaining := make([]string, len(sessions))
	copy(remaining, sessions)

	var settled []string
	nextCycle := time.Now().Add(time.Duration(cycleS * float64(time.Second)))

	for time.Now().Before(deadline) && len(remaining) > 0 {
		if !d.CheckAppAlive() {
			return settled
		}
		if len(inflightPairs) > 0 && cycleS > 0 && !d.ViewCycleDisabled && time.Now().After(nextCycle) {
			remMap := make(map[string]bool)
			for _, r := range remaining {
				remMap[r] = true
			}
			if d.CycleAndConfirm(inflightPairs, remMap) > 0 {
				for s := range lastChange {
					lastChange[s] = time.Now()
				}
			}
			nextCycle = time.Now().Add(time.Duration(cycleS * float64(time.Second)))
		}

		if d.maybeAutoConfirm(false, "") != nil {
			for s := range lastChange {
				lastChange[s] = time.Now()
			}
		}

		now := time.Now()
		for _, s := range remaining {
			mf := filepath.Join(s, "session.messages.json")
			var sig [2]int64
			if fi, err := os.Stat(mf); err == nil {
				sig = [2]int64{fi.Size(), fi.ModTime().UnixNano()}
			}
			if sig != sigs[s] {
				sigs[s] = sig
				lastChange[s] = now
			} else if sig[0] > 0 && now.Sub(lastChange[s]).Seconds() >= quietS && looksComplete(mf) {
				settled = append(settled, s)
			}
		}

		if len(settled) > 0 {
			return settled
		}
		time.Sleep(500 * time.Millisecond)
	}
	return settled
}
