package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"ruiyun-ui-test-platform-go/internal/pyre"
)

var (
	configMu sync.Mutex

	// RootDir 项目根目录，运行时可由 main 初始化
	RootDir string
)

func init() {
	// 默认使用当前工作目录或可执行文件所在目录
	wd, err := os.Getwd()
	if err == nil {
		RootDir = wd
	}
}

// ExpandHome 展开路径中的 ~
func ExpandHome(pathStr string) string {
	if strings.HasPrefix(pathStr, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if pathStr == "~" {
				return home
			}
			if strings.HasPrefix(pathStr, "~/") || strings.HasPrefix(pathStr, "~\\") {
				return filepath.Join(home, pathStr[2:])
			}
		}
	}
	return pathStr
}

func firstExisting(cands []string, fallback string) string {
	for _, c := range cands {
		if c != "" {
			exp := ExpandHome(c)
			if _, err := os.Stat(exp); err == nil {
				return c
			}
		}
	}
	return fallback
}

// DefaultAppBinary 获取平台默认应用路径
func DefaultAppBinary() string {
	if runtime.GOOS == "windows" {
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		return filepath.Join(pf, "srtclaw", "睿云智能工作台.exe")
	}
	return "/Applications/睿云智能工作台.app/Contents/MacOS/睿云智能工作台"
}

// DefaultAgentConfig 获取平台默认 agent 配置文件
func DefaultAgentConfig() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = os.Getenv("USERPROFILE")
		}
		if appdata == "" {
			appdata = `C:\Users\Default\AppData\Roaming`
		}
		c1 := filepath.Join(appdata, "srtclaw", "config", "config.yaml")
		home, _ := os.UserHomeDir()
		c2 := filepath.Join(home, ".srtclaw", "config", "config.yaml")
		return firstExisting([]string{c1, c2}, c1)
	}
	return "~/.srtclaw/config/config.yaml"
}

const (
	DefaultSessionRoot = "~/.srtclaw/workspace/session"

	SrcLocal   = "local"
	SrcConfig  = "config"
	SrcDefault = "default"
)

// AppOverrides .app_settings.json 结构
type AppOverrides struct {
	AppBinary     string `json:"app_binary"`
	SessionRoot   string `json:"session_root"`
	UserWorkspace string `json:"user_workspace"`
	SavedAt       string `json:"saved_at,omitempty"`
}

func settingsFile() string {
	return filepath.Join(RootDir, ".app_settings.json")
}

func templateConfigFile() string {
	return filepath.Join(RootDir, "config.template.yaml")
}

// LoadOverrides 读取界面覆盖项
func LoadOverrides() AppOverrides {
	path := settingsFile()
	data, err := os.ReadFile(path)
	if err != nil {
		return AppOverrides{}
	}
	var ov AppOverrides
	if err := json.Unmarshal(data, &ov); err != nil {
		return AppOverrides{}
	}
	ov.AppBinary = strings.TrimSpace(ov.AppBinary)
	ov.SessionRoot = strings.TrimSpace(ov.SessionRoot)
	ov.UserWorkspace = strings.TrimSpace(ov.UserWorkspace)
	return ov
}

// SaveOverrides 原子写入覆盖项并设置 0600 权限
func SaveOverrides(appBinary, sessionRoot, userWs string) (AppOverrides, error) {
	configMu.Lock()
	defer configMu.Unlock()

	ov := AppOverrides{
		AppBinary:     strings.TrimSpace(appBinary),
		SessionRoot:   strings.TrimSpace(sessionRoot),
		UserWorkspace: strings.TrimSpace(userWs),
		SavedAt:       time.Now().Format("2006-01-02 15:04:05"),
	}

	target := settingsFile()
	tmp := target + fmt.Sprintf(".%d.tmp", time.Now().UnixNano())

	data, err := json.MarshalIndent(ov, "", "  ")
	if err != nil {
		return AppOverrides{}, err
	}

	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return AppOverrides{}, err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return AppOverrides{}, err
	}
	_ = os.Chmod(target, 0600)
	return ov, nil
}

// ClearOverrides 清除覆盖项
func ClearOverrides() {
	configMu.Lock()
	defer configMu.Unlock()
	_ = os.Remove(settingsFile())
}

// DefaultWorkspace 默认用户工作区目录
func DefaultWorkspace() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".ruiyun-autotest")
}

// UserWorkspace 获取用户工作区目录并自动创建
func UserWorkspace() string {
	ov := LoadOverrides()
	ws := ov.UserWorkspace
	if ws == "" || ws == "null" || ws == "~" {
		ws = DefaultWorkspace()
	}
	exp := ExpandHome(ws)
	_ = os.MkdirAll(exp, 0755)
	return exp
}

func migrateLegacy(src, dst string) {
	if _, err := os.Stat(dst); err == nil {
		return // dst 存在，不迁移
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return // src 不存在，无法迁移
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0755)
	_ = os.Rename(src, dst)
}

func migrateFirst(sources []string, dst string) {
	for _, src := range sources {
		if _, err := os.Stat(dst); err == nil {
			return
		}
		migrateLegacy(src, dst)
	}
}

// PresetPath 预设用例数据库文件：工作区/testcases.db
func PresetPath() string {
	ws := UserWorkspace()
	dst := filepath.Join(ws, "testcases.db")
	legacy := []string{
		filepath.Join(RootDir, "testcases.db"),
		filepath.Join(RootDir, "workspace", "testcases.db"),
	}
	migrateFirst(legacy, dst)
	if ws != DefaultWorkspace() {
		migrateLegacy(filepath.Join(DefaultWorkspace(), "testcases.db"), dst)
	}
	return dst
}

// UploadsDir 附件库目录：工作区/uploads
func UploadsDir() string {
	ws := UserWorkspace()
	dst := filepath.Join(ws, "uploads")
	legacy := []string{
		filepath.Join(RootDir, "artifacts", "uploads"),
		filepath.Join(RootDir, "workspace", "uploads"),
	}
	migrateFirst(legacy, dst)
	if ws != DefaultWorkspace() {
		migrateLegacy(filepath.Join(DefaultWorkspace(), "uploads"), dst)
	}
	_ = os.MkdirAll(dst, 0755)
	return dst
}

// RoundsDir 轮次归档目录：工作区/rounds
func RoundsDir() string {
	ws := UserWorkspace()
	dst := filepath.Join(ws, "rounds")
	legacy := []string{
		filepath.Join(RootDir, "artifacts", "rounds"),
		filepath.Join(RootDir, "workspace", "rounds"),
	}
	migrateFirst(legacy, dst)
	if ws != DefaultWorkspace() {
		migrateLegacy(filepath.Join(DefaultWorkspace(), "rounds"), dst)
	}
	_ = os.MkdirAll(dst, 0755)
	return dst
}

// ConfigPath 平台配置文件：工作区/config.yaml
func ConfigPath() string {
	ws := UserWorkspace()
	dst := filepath.Join(ws, "config.yaml")
	legacy := []string{filepath.Join(RootDir, "config.yaml")}
	migrateFirst(legacy, dst)
	if ws != DefaultWorkspace() {
		migrateLegacy(filepath.Join(DefaultWorkspace(), "config.yaml"), dst)
	}

	if _, err := os.Stat(dst); os.IsNotExist(err) {
		tpl := templateConfigFile()
		if data, err := os.ReadFile(tpl); err == nil {
			_ = os.MkdirAll(filepath.Dir(dst), 0755)
			_ = os.WriteFile(dst, data, 0644)
		}
	}
	return dst
}

// SecretsPath 模型密钥文件：工作区/.llm_secrets.json
func SecretsPath() string {
	ws := UserWorkspace()
	dst := filepath.Join(ws, ".llm_secrets.json")
	legacy := []string{filepath.Join(RootDir, ".llm_secrets.json")}
	migrateFirst(legacy, dst)
	if ws != DefaultWorkspace() {
		migrateLegacy(filepath.Join(DefaultWorkspace(), ".llm_secrets.json"), dst)
	}
	return dst
}

// LoadConfigDict 加载解析 config.yaml 为字典
func LoadConfigDict() (map[string]any, error) {
	cp := ConfigPath()
	data, err := os.ReadFile(cp)
	if err != nil {
		return map[string]any{}, err
	}
	var res map[string]any
	if err := yaml.Unmarshal(data, &res); err != nil {
		return map[string]any{}, err
	}
	if res == nil {
		res = make(map[string]any)
	}
	return res, nil
}

func cleanVal(v any) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "null" || s == "~" || s == "<nil>" {
		return ""
	}
	return s
}

// EffectiveConfig 按三层优先级合并应用与路径配置
func EffectiveConfig(cfg map[string]any) map[string]any {
	// 深拷贝
	data, _ := json.Marshal(cfg)
	var eff map[string]any
	_ = json.Unmarshal(data, &eff)
	if eff == nil {
		eff = make(map[string]any)
	}

	ov := LoadOverrides()

	app, ok := eff["app"].(map[string]any)
	if !ok {
		app = make(map[string]any)
		eff["app"] = app
	}
	paths, ok := eff["paths"].(map[string]any)
	if !ok {
		paths = make(map[string]any)
		eff["paths"] = paths
	}

	binary := cleanVal(ov.AppBinary)
	if binary == "" {
		binary = cleanVal(app["binary"])
	}
	if binary == "" {
		binary = DefaultAppBinary()
	}
	app["binary"] = ExpandHome(binary)

	sessRoot := cleanVal(ov.SessionRoot)
	if sessRoot == "" {
		sessRoot = cleanVal(paths["session_root"])
	}
	if sessRoot == "" {
		sessRoot = DefaultSessionRoot
	}
	sr := ExpandHome(sessRoot)
	paths["session_root"] = sr

	wr := cleanVal(paths["workspace_root"])
	if wr != "" {
		paths["workspace_root"] = ExpandHome(wr)
	} else {
		paths["workspace_root"] = filepath.Dir(sr)
	}

	agentCfg := cleanVal(paths["agent_config"])
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(agentCfg, "/") {
			agentCfg = ""
		}
	}
	if agentCfg == "" {
		agentCfg = DefaultAgentConfig()
	}
	paths["agent_config"] = ExpandHome(agentCfg)

	return eff
}

func sourceDesc(ovVal, cfgVal string) string {
	if cleanVal(ovVal) != "" {
		return SrcLocal
	}
	if cleanVal(cfgVal) != "" {
		return SrcConfig
	}
	return SrcDefault
}

// Describe 界面「应用设置」状态
func Describe(cfg map[string]any) map[string]any {
	if cfg == nil {
		cfg = make(map[string]any)
	}
	ov := LoadOverrides()
	rawApp, _ := cfg["app"].(map[string]any)
	rawPaths, _ := cfg["paths"].(map[string]any)

	eff := EffectiveConfig(cfg)
	app, _ := eff["app"].(map[string]any)
	paths, _ := eff["paths"].(map[string]any)
	ws := UserWorkspace()

	binary := cleanVal(app["binary"])
	sr := cleanVal(paths["session_root"])
	agentCfg := cleanVal(paths["agent_config"])

	binExists := false
	if binary != "" {
		if fi, err := os.Stat(binary); err == nil && !fi.IsDir() {
			binExists = true
		}
	}

	srExists := false
	if sr != "" {
		if fi, err := os.Stat(sr); err == nil && fi.IsDir() {
			srExists = true
		}
	}

	agentCfgExists := false
	if agentCfg != "" {
		if fi, err := os.Stat(agentCfg); err == nil && !fi.IsDir() {
			agentCfgExists = true
		}
	}

	wsExists := false
	if fi, err := os.Stat(ws); err == nil && fi.IsDir() {
		wsExists = true
	}

	return map[string]any{
		"platform":   runtime.GOOS,
		"is_windows": runtime.GOOS == "windows",
		"defaults": map[string]any{
			"binary":       ExpandHome(DefaultAppBinary()),
			"session_root": ExpandHome(DefaultSessionRoot),
		},
		"binary": map[string]any{
			"value":  binary,
			"source": sourceDesc(ov.AppBinary, cleanVal(rawApp["binary"])),
			"exists": binExists,
		},
		"session_root": map[string]any{
			"value":  sr,
			"source": sourceDesc(ov.SessionRoot, cleanVal(rawPaths["session_root"])),
			"exists": srExists,
		},
		"agent_config": map[string]any{
			"value":  agentCfg,
			"exists": agentCfgExists,
		},
		"workspace_root": map[string]any{
			"value": cleanVal(paths["workspace_root"]),
		},
		"workspace": map[string]any{
			"value":  ws,
			"source": sourceDesc(ov.UserWorkspace, ""),
			"exists": wsExists,
		},
	}
}

// WriteEnvProfile 把选定的环境档案写回 config.yaml 的 app.env_profile（保留注释）
func WriteEnvProfile(profile string) (bool, string) {
	key := strings.TrimSpace(profile)
	if key != "dev" && key != "production" {
		return false, fmt.Sprintf("未知环境档案：%s（只支持 dev / production）", key)
	}

	cp := ConfigPath()
	data, err := os.ReadFile(cp)
	if err != nil {
		return false, fmt.Sprintf("配置文件不可读：%v", err)
	}
	text := string(data)

	reEnv := regexp.MustCompile(`(?m)^([` + pyre.SpaceClass + `]*)env_profile:.*$`)
	if reEnv.MatchString(text) {
		text = reEnv.ReplaceAllString(text, fmt.Sprintf(`${1}env_profile: "%s"`, key))
	} else {
		reApp := regexp.MustCompile(`(?m)^(app:[ \t]*(?:#.*)?)$`)
		if reApp.MatchString(text) {
			text = reApp.ReplaceAllString(text, fmt.Sprintf("${1}\n  env_profile: \"%s\"", key))
		} else {
			return false, fmt.Sprintf("config.yaml 中未找到 app 段，无法写入 env_profile: %s", cp)
		}
	}

	if err := os.WriteFile(cp, []byte(text), 0644); err != nil {
		return false, fmt.Sprintf("写入失败：%v", err)
	}
	return true, key
}

// ReadEnvConfig 读取运行环境档案以及当前默认项
func ReadEnvConfig(appRunning bool) (map[string]any, error) {
	cfg, err := LoadConfigDict()
	if err != nil {
		return nil, err
	}
	app, _ := cfg["app"].(map[string]any)
	current := cleanVal(app["env_profile"])
	profilesCfg, _ := app["env_profiles"].(map[string]any)

	type labelInfo struct {
		label string
		desc  string
	}
	labels := map[string]labelInfo{
		"dev":        {"开发", "注入 dev 域名，连 api-dev.3ren.cn"},
		"production": {"线上", "不注入变量，走应用内置生产域名"},
	}

	var profiles []map[string]any
	for _, k := range []string{"dev", "production"} {
		var pairs map[string]any
		if profilesCfg != nil {
			pairs, _ = profilesCfg[k].(map[string]any)
		}
		info := labels[k]
		profiles = append(profiles, map[string]any{
			"key":   k,
			"label": info.label,
			"desc":  info.desc,
			"vars":  len(pairs),
		})
	}
	if current != "dev" && current != "production" {
		current = "dev"
	}

	return map[string]any{
		"current":     current,
		"profiles":    profiles,
		"config_path": ConfigPath(),
		"app_running": appRunning,
	}, nil
}
