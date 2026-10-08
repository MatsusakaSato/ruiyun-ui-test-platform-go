package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"ruiyun-ui-test-platform-go/internal/canon"

	"ruiyun-ui-test-platform-go/internal/cdp"
	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/llm"
	"ruiyun-ui-test-platform-go/internal/notify"
	"ruiyun-ui-test-platform-go/internal/rounds"
	"ruiyun-ui-test-platform-go/internal/sysutil"
	"ruiyun-ui-test-platform-go/internal/testcasedb"
	"ruiyun-ui-test-platform-go/internal/xlsx"
)

// ------------------------------------------------------------------ 应用实例

// appIsRunning 以调试端口是否响应为准（与驱动 is_up 同口径）
func appIsRunning(cfg map[string]any) bool {
	port := 9222
	if cfg == nil {
		if c, err := config.LoadConfigDict(); err == nil {
			cfg = c
		}
	}
	if app, ok := cfg["app"].(map[string]any); ok {
		switch v := app["debug_port"].(type) {
		case int:
			if v > 0 {
				port = v
			}
		case int64:
			if v > 0 {
				port = int(v)
			}
		case float64:
			if v > 0 {
				port = int(v)
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				port = n
			}
		}
	}
	_, err := cdp.HTTPJSON(fmt.Sprintf("http://127.0.0.1:%d/json/version", port), 2*time.Second)
	return err == nil
}

// closeApp 关闭应用实例 —— 不区分是否由平台启动（按可执行文件名匹配系统进程）
func closeApp() (bool, string) {
	cfg, err := config.LoadConfigDict()
	if err != nil {
		return false, fmt.Sprintf("读取配置失败：%T: %v", err, err)
	}
	eff := config.EffectiveConfig(cfg)
	app, _ := eff["app"].(map[string]any)
	binary := ""
	if app != nil {
		binary = strings.TrimSpace(fmt.Sprintf("%v", app["binary"]))
		if binary == "<nil>" {
			binary = ""
		}
	}
	if binary == "" {
		return false, "config.yaml 未配置 app.binary，无法定位进程"
	}
	exeName := filepath.Base(binary)
	if exeName == "" || exeName == "." {
		return false, "binary 配置无法解析为可执行文件名"
	}
	if err := sysutil.KillProcesses(binary); err != nil {
		return false, "未找到运行中的应用进程"
	}
	for i := 0; i < 20; i++ {
		if !appIsRunning(nil) {
			return true, "应用已关闭"
		}
		time.Sleep(500 * time.Millisecond)
	}
	return true, "已发出关闭信号（端口未及时释放，可能仍在退出中）"
}

// ------------------------------------------------------------------ 附件库

// MaxUploadBytes 附件上传上限（64MB）
const MaxUploadBytes = 64 * 1024 * 1024

var unsafeFilename = regexp.MustCompile(`[\x00-\x1f/\\:*?"<>|]`)

// safeFilename 附件名净化：只取基名并去掉危险字符（防目录穿越）
func safeFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = unsafeFilename.ReplaceAllString(base, "_")
	base = strings.Trim(base, " .")
	if base == "" {
		return "attachment"
	}
	return base
}

// uniqueUploadDir 每个附件一个独立目录（按时间命名，冲突时加序号）
func uniqueUploadDir() string {
	base := time.Now().Format("20060102_150405")
	root := config.UploadsDir()
	d := filepath.Join(root, base)
	i := 1
	for {
		if _, err := os.Stat(d); err != nil {
			return d
		}
		i++
		d = filepath.Join(root, fmt.Sprintf("%s_%d", base, i))
	}
}

// listUploads 附件库清单（按修改时间倒序）
func listUploads() []map[string]any {
	out := []map[string]any{}
	root := config.UploadsDir()
	for _, p := range walkFilesUnsorted(root) {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		abs, aerr := filepath.Abs(p)
		if aerr != nil {
			abs = p
		}
		out = append(out, map[string]any{
			"name":  info.Name(),
			"path":  abs,
			"size":  info.Size(),
			"mtime": time.Unix(info.ModTime().Unix(), 0).Format("2006-01-02 15:04"),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		mi, _ := out[i]["mtime"].(string)
		mj, _ := out[j]["mtime"].(string)
		return mi > mj
	})
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}

// walkFilesUnsorted 深度优先、**不排序**遍历目录下的所有文件。
//
// ⚠ 这是 listUploads 顺序稳定的关键：底层目录序（**不排序**）才是稳定依据，
// 而 Go 的 `filepath.Walk` / `os.ReadDir` 会按文件名排序。
// mtime 只精确到分钟，同一批上传的附件 mtime 字符串大量并列，此时稳定排序的结果
// 完全由遍历序决定 —— 用排序过的遍历会让清单顺序错位（实测 5 条全错位）。
func walkFilesUnsorted(dir string) []string {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	entries, err := f.ReadDir(-1) // -1 = 一次读完，保持底层目录序（不排序）
	_ = f.Close()
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			out = append(out, walkFilesUnsorted(p)...)
			continue
		}
		out = append(out, p)
	}
	return out
}

// saveUpload 落盘到附件库，返回写入的绝对路径
func saveUpload(name string, data []byte) (string, error) {
	d := uniqueUploadDir()
	if err := os.MkdirAll(d, 0755); err != nil {
		return "", err
	}
	p := filepath.Join(d, safeFilename(name))
	if err := os.WriteFile(p, data, 0644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return abs, nil
}

// ------------------------------------------------------------------ GET 处理器

func (s *Server) handleRoundsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	one := func(k, d string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return v[0]
		}
		return d
	}
	intOr := func(name string, def int) int {
		raw := one(name, "")
		if raw == "" {
			return def
		}
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return def
		}
		return n
	}
	res := rounds.Query(rounds.QueryOptions{
		DateFrom: one("date_from", ""),
		DateTo:   one("date_to", ""),
		Keyword:  one("keyword", ""),
		Limit:    intOr("limit", 20),
		Offset:   intOr("offset", 0),
	})
	res["dir"] = config.RoundsDir()
	writeJSON(w, 200, res)
}

func (s *Server) handleRoundDetail(w http.ResponseWriter, r *http.Request) {
	// 用未解码的原始路径，按 `/` 切分取第 4 段作为 rid
	raw := rawPath(r)
	parts := strings.Split(raw, "/")
	rid := ""
	if len(parts) > 3 {
		rid = parts[3]
	}
	if strings.HasSuffix(raw, "/report.html") {
		f := filepath.Join(config.RoundsDir(), rid, "report.html")
		data, err := os.ReadFile(f)
		if err != nil {
			writeText(w, 404, "not found")
			return
		}
		writeBody(w, 200, data, "text/html; charset=utf-8")
		return
	}
	data := rounds.Load(rid)
	if data == nil {
		writeJSON(w, 404, map[string]any{"error": "round not found"})
		return
	}
	writeJSON(w, 200, data)
}

func (s *Server) handleConfig(w http.ResponseWriter) {
	cfg, err := config.LoadConfigDict()
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	eff := config.EffectiveConfig(cfg)
	appCfg, _ := eff["app"].(map[string]any)
	name := ""
	if appCfg != nil {
		name = strOr(appCfg["name"], "")
	}
	if name == "" {
		name = "睿云智能工作台"
	}
	caseTimeout := 1200
	if appCfg != nil {
		caseTimeout = intFromAny(eff["case_timeout_s"], 1200)
	}
	count, _ := testcasedb.CountCases("")
	autoConfirm := false
	if appCfg != nil {
		if b, ok := appCfg["auto_confirm"].(bool); ok {
			autoConfirm = b
		}
	}
	writeJSON(w, 200, map[string]any{
		"app_name":       name,
		"case_timeout_s": caseTimeout,
		"preset_count":   count,
		"auto_confirm":   autoConfirm,
	})
}

func (s *Server) handlePresetCasesGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	one := func(k, d string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return v[0]
		}
		return d
	}
	limit, offset := 50, 0
	if raw := strings.TrimSpace(one("limit", "50")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if raw := strings.TrimSpace(one("offset", "0")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			offset = n
		}
	}
	splitCSV := func(s string) []string {
		var out []string
		for _, t := range strings.Split(s, ",") {
			if strings.TrimSpace(t) != "" {
				out = append(out, t)
			}
		}
		return out
	}
	res, err := testcasedb.QueryPresetCases(
		one("keyword", ""), one("scene", ""),
		splitCSV(one("targets", "")), one("attachment", ""),
		splitCSV(one("ids", "")), limit, offset, one("order", "desc"), "")
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleEnvGet(w http.ResponseWriter) {
	data, err := config.ReadEnvConfig(appIsRunning(nil))
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": fmt.Sprintf("%T: %v", err, err)})
		return
	}
	writeJSON(w, 200, data)
}

func (s *Server) handleAppSettingsGet(w http.ResponseWriter) {
	cfg, _ := config.LoadConfigDict()
	if cfg == nil {
		cfg = map[string]any{}
	}
	writeJSON(w, 200, config.Describe(cfg))
}

// ------------------------------------------------------------------ POST 处理器

func (s *Server) handleEnvPost(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	if appIsRunning(nil) {
		writeJSON(w, 409, map[string]any{
			"ok": false, "message": "应用正在运行，环境为只读。请先关闭实例再切换。"})
		return
	}
	ok, msg := config.WriteEnvProfile(strOr(body["profile"], ""))
	data := map[string]any{}
	if ok {
		if d, err := config.ReadEnvConfig(appIsRunning(nil)); err == nil {
			data = d
		}
	}
	out := map[string]any{"ok": ok, "message": msg}
	if ok {
		for k, v := range data {
			out[k] = v
		}
	}
	writeJSON(w, codeFor(ok, 400), out)
}

// handleNotifySend 发送钉钉群通知（「发送通知」页）。
// 请求体 {"message": "...", "group": "official"|"test"}；
// 统一返回 {"ok": bool, "message": string}。
func (s *Server) handleNotifySend(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	msg := strings.TrimSpace(strOr(body["message"], ""))
	group := strings.TrimSpace(strOr(body["group"], ""))
	if msg == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "通知内容不能为空"})
		return
	}
	if group == "" {
		group = "official"
	}
	res, err := notify.SendCustomRobotGroupMessageTo(group, msg, "")
	if err != nil {
		writeJSON(w, 502, map[string]any{"ok": false, "message": "钉钉消息发送出错：" + err.Error()})
		return
	}
	if !res.OK() {
		writeJSON(w, 502, map[string]any{
			"ok": false, "message": "钉钉消息" + res.Describe()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "message": "已发送到" + notify.GroupLabel(group)})
}

func (s *Server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	ok, msg := s.state.Start(RunOptions{
		Cases:       intFromAny(body["cases"], 0),
		ReproTimes:  intFromAny(body["repro_times"], 0),
		ReproLimit:  intFromAny(body["repro_limit"], 0),
		CaseItems:   normalizeCases(body["case_items"]),
		MaxInflight: intFromAny(body["max_inflight"], 0),
		AutoConfirm: boolPtr(body["auto_confirm"]),
	})
	rid := ""
	if ok {
		rid = msg
	}
	writeJSON(w, codeFor(ok, 409), map[string]any{"ok": ok, "run_id": rid, "message": msg})
}

func (s *Server) handleLLMConfigPost(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	if truthyAny(body["clear"]) {
		llm.ClearConfig()
		out := map[string]any{"ok": true, "message": "已清空后端模型配置"}
		for k, v := range llm.PublicConfig() {
			out[k] = v
		}
		writeJSON(w, 200, out)
		return
	}
	if truthyAny(body["reveal"]) {
		writeJSON(w, 200, map[string]any{
			"ok": true, "api_key": llm.LoadConfig().APIKey})
		return
	}
	baseURL := strings.TrimSpace(strOr(body["base_url"], ""))
	apiKey := strings.TrimSpace(strOr(body["api_key"], ""))
	if apiKey == "" && truthyAny(body["keep_key"]) {
		apiKey = llm.LoadConfig().APIKey
	}
	model := strings.TrimSpace(strOr(body["model"], ""))
	if baseURL == "" || apiKey == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "供应商地址与 API Key 均为必填"})
		return
	}
	if model == "" {
		writeJSON(w, 400, map[string]any{
			"ok": false,
			"message": "模型名为必填：请填写模型名或端点 ID" +
				"（留空会导致判分请求使用占位模型名而失败）"})
		return
	}
	if err := llm.SaveConfig(baseURL, apiKey, model); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	out := map[string]any{"ok": true, "message": "已保存到服务端"}
	for k, v := range llm.PublicConfig() {
		out[k] = v
	}
	writeJSON(w, 200, out)
}

// ------------------------------------------------------------------ 通知预设模板

func (s *Server) handleNotifyTemplatesGet(w http.ResponseWriter) {
	items, err := testcasedb.ListNotifyTemplates("")
	if err != nil {
		writeJSON(w, 500, map[string]any{
			"ok": false, "message": fmt.Sprintf("读取预设模板失败：%v", err), "items": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": items})
}

func (s *Server) handleNotifyTemplatesPost(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	action := strings.TrimSpace(strOr(body["action"], ""))
	id := strings.TrimSpace(strOr(body["id"], ""))
	name := strOr(body["name"], "")
	content := strOr(body["content"], "")

	switch action {
	case "add":
		ok, msg, item := testcasedb.AddNotifyTemplate(name, content, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "item": item})
	case "rename":
		ok, msg, item := testcasedb.RenameNotifyTemplate(id, name, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "item": item})
	case "update":
		ok, msg, item := testcasedb.UpdateNotifyTemplate(id, name, content, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "item": item})
	case "delete":
		ok, msg := testcasedb.DeleteNotifyTemplate(id, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg})
	default:
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": "未知操作（支持 add / rename / delete）"})
	}
}

func (s *Server) handlePresetCasesPost(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	action := strings.TrimSpace(strOr(body["action"], ""))
	switch action {
	case "add":
		item, _ := body["item"].(map[string]any)
		if item == nil {
			item = body
		}
		ok, msg, c := testcasedb.AddPresetCase(item, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "case": c})
	case "add_many":
		items := toMapList(body["items"])
		ok, msg, added := testcasedb.AddPresetCases(items, false, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "added": added})
	case "delete":
		ids := toStringList(body["ids"])
		ok, msg, removed := testcasedb.DeletePresetCases(ids, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "removed": removed})
	default:
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": "未知操作（支持 add / add_many / delete）"})
	}
}

func (s *Server) handlePresetImport(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	name := strOr(body["name"], "")
	raw := strOr(body["data_base64"], "")
	if name == "" || raw == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "缺少文件名或文件内容"})
		return
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": fmt.Sprintf("文件内容无法解码：%v", err)})
		return
	}
	if len(data) > 100*1024*1024 {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "文件超过 100MB，请拆分后再导入"})
		return
	}
	table, err := xlsx.ReadTable(name, data, 5000)
	if err != nil {
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": fmt.Sprintf("解析失败：%T: %v", err, err)})
		return
	}
	rows := toRows(table["rows"])
	meta := xlsx.DetectColumns(rows)

	getCol := func(key string, def *int) *int {
		if _, present := body[key]; !present {
			return def
		}
		v := body[key]
		if v == nil || v == "" || v == -1 || v == "-1" || v == float64(-1) {
			return nil
		}
		switch t := v.(type) {
		case float64:
			n := int(t)
			return &n
		case int:
			n := t
			return &n
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return &n
			}
		}
		return def
	}
	promptCol := getCol("prompt_col", intPtrFromAny(meta["prompt_col"]))
	sceneCol := getCol("scene_col", intPtrFromAny(meta["scene_col"]))
	targetCol := getCol("target_col", intPtrFromAny(meta["target_col"]))
	nameCol := getCol("name_col", intPtrFromAny(meta["name_col"]))
	attachCol := getCol("attachment_col", intPtrFromAny(meta["attachment_col"]))
	skillCol := intPtrFromAny(meta["skill_col"])
	headIdx := intPtrFromAny(meta["head_idx"])

	promptIdx := 0
	if promptCol != nil {
		promptIdx = *promptCol
	}
	got := xlsx.RowsToItems(rows, headIdx, promptIdx, sceneCol, targetCol, nameCol, attachCol, skillCol)
	items := toMapList(got["items"])
	skipped := intFromAny(got["skipped"], 0)

	if truthyAny(body["parse_only"]) {
		header := toStringList(meta["header"])
		width := len(header)
		for i, r := range rows {
			if i >= 50 {
				break
			}
			if len(r) > width {
				width = len(r)
			}
		}
		preview := []map[string]any{}
		for i, it := range items {
			if i >= 200 {
				break
			}
			prompt := strOr(it["prompt"], "")
			if len([]rune(prompt)) > 200 {
				prompt = string([]rune(prompt)[:200])
			}
			preview = append(preview, map[string]any{
				"id":          strOr(it["id"], ""),
				"name":        strOr(it["name"], ""),
				"prompt":      prompt,
				"scene":       strOr(it["scene"], ""),
				"targets":     orEmptyList(it["targets"]),
				"attachments": orEmptyList(it["attachments"]),
			})
		}
		writeJSON(w, 200, map[string]any{
			"ok":             true,
			"file":           name,
			"sheet":          strOr(table["sheet"], ""),
			"header":         header,
			"width":          width,
			"prompt_col":     promptCol,
			"scene_col":      sceneCol,
			"target_col":     targetCol,
			"name_col":       nameCol,
			"attachment_col": attachCol,
			"total":          len(items),
			"skipped":        skipped,
			"items":          preview,
			"items_total":    len(items),
		})
		return
	}

	ok, msg, added := testcasedb.AddPresetCases(items, truthyAny(body["replace"]), "")
	writeJSON(w, codeFor(ok, 400), map[string]any{
		"ok": ok, "message": msg, "added": added,
		"file": name, "total": len(items), "skipped": skipped})
}

func (s *Server) handleAppSettingsPost(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	if truthyAny(body["reset"]) {
		config.ClearOverrides()
		cfg, _ := config.LoadConfigDict()
		if cfg == nil {
			cfg = map[string]any{}
		}
		out := map[string]any{"ok": true, "message": "已恢复默认配置"}
		for k, v := range config.Describe(cfg) {
			out[k] = v
		}
		writeJSON(w, 200, out)
		return
	}
	binary := strings.TrimSpace(strOr(body["app_binary"], ""))
	sessionRoot := strings.TrimSpace(strOr(body["session_root"], ""))
	userWs := strings.TrimSpace(strOr(body["user_workspace"], ""))
	for _, pair := range [][2]string{{"应用路径", binary}, {"日志目录", sessionRoot}, {"工作区目录", userWs}} {
		v := pair[1]
		if v != "" && (strings.HasPrefix(v, "|") || strings.HasPrefix(v, ";")) {
			writeJSON(w, 400, map[string]any{
				"ok": false, "message": fmt.Sprintf("%s含非法字符", pair[0])})
			return
		}
	}
	if binary != "" {
		exp := config.ExpandHome(binary)
		if _, err := os.Stat(exp); err != nil {
			hint := "请确认 .app/Contents/MacOS/ 下的可执行文件"
			if runtime.GOOS == "windows" {
				hint = `请确认路径为可执行文件（Windows 示例：C:\Program Files\srtclaw\睿云智能工作台.exe）`
			}
			writeJSON(w, 400, map[string]any{
				"ok":      false,
				"message": fmt.Sprintf("应用路径不存在：%s（%s）", binary, hint)})
			return
		}
	}
	if userWs != "" {
		parent := filepath.Dir(config.ExpandHome(userWs))
		if st, err := os.Stat(parent); err != nil || !st.IsDir() {
			writeJSON(w, 400, map[string]any{
				"ok":      false,
				"message": fmt.Sprintf("工作区目录的父目录不存在：%s", userWs)})
			return
		}
	}
	if _, err := config.SaveOverrides(binary, sessionRoot, userWs); err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	if userWs != "" {
		_ = config.UserWorkspace() // 立即建目录，让「打开工作区」随时可用
	}
	cfg, _ := config.LoadConfigDict()
	if cfg == nil {
		cfg = map[string]any{}
	}
	out := map[string]any{"ok": true, "message": "已保存（下次运行测试时生效）"}
	for k, v := range config.Describe(cfg) {
		out[k] = v
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	runID := strings.TrimSpace(strOr(body["run_id"], ""))
	if !rounds.RunIDPattern.MatchString(runID) {
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": fmt.Sprintf("run_id 含非法字符：%s", runID)})
		return
	}
	ok, msg := s.eval.Start(runID,
		intFromAny(body["max_cases"], 0),
		intFromAny(body["concurrency"], 3))
	rid := ""
	if ok {
		rid = msg
	}
	var preflight any
	if pf := s.eval.LastPreflight(); pf != nil {
		preflight = pf
	}
	writeJSON(w, codeFor(ok, 409), map[string]any{
		"ok": ok, "run_id": rid, "message": msg, "preflight": preflight})
}

func (s *Server) handleLLMTest(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	timeoutS := 12.0
	switch t := body["timeout_s"].(type) {
	case float64:
		timeoutS = t
	case int:
		timeoutS = float64(t)
	case string:
		if v, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			timeoutS = v
		}
	}
	result := llm.ProbeProvider(
		strOr(body["base_url"], ""),
		strOr(body["api_key"], ""),
		strOr(body["model"], ""),
		strOr(body["probe_path"], ""),
		timeoutS,
	)
	writeJSON(w, 200, result.ToDict())
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	name := strings.TrimSpace(strOr(body["name"], ""))
	b64 := strOr(body["data_base64"], "")
	if name == "" || b64 == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "缺少文件名或文件内容"})
		return
	}
	raw, err := decodeBase64Tolerant(b64)
	if err != nil {
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": fmt.Sprintf("内容不是合法 base64：%v", err)})
		return
	}
	if len(raw) > MaxUploadBytes {
		writeJSON(w, 413, map[string]any{
			"ok": false,
			"message": fmt.Sprintf("文件过大（%.1fMB），上限 %dMB",
				float64(len(raw))/1048576.0, MaxUploadBytes/1048576)})
		return
	}
	fp, err := saveUpload(name, raw)
	if err != nil {
		writeJSON(w, 500, map[string]any{
			"ok": false, "message": fmt.Sprintf("写入附件库失败：%s", canon.FormatOSError(err))})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "name": filepath.Base(fp), "path": fp,
		"size": len(raw), "message": "已上传到本机附件库"})
}

// decodeBase64Tolerant 宽容 base64 解码（忽略字母表外字符）。
//
// Go 的 `base64.StdEncoding.DecodeString` 是**严格**的：遇到字母表外的字符
// （`!`、空格、换行…）立即报 `illegal base64 data at input byte N`。
// 宽容模式会**先丢弃**所有非字母表字符再解码，实测：
//
//	"!!!"      -> b''      （全被丢弃；严格模式会直接报错）
//	"Y W J j"  -> b'abc'   （空格被丢弃）
//	"YQ==extra"-> b'a'     （填充之后的尾巴被忽略）
//	"YQ==="    -> b'a'     （多余的 '=' 被忽略）
//	"===="     -> b''
//	"YQ"       -> Incorrect padding
//	"YQ="      -> Incorrect padding
//	"a"/"YWJjZ"-> number of data characters (N) cannot be 1 more than a multiple of 4
//
// 前端把 base64 塞进 URL/表单时经常带换行或空格，严格模式会让上传整单失败。
// 错误文案也逐字固定，前端会按 `内容不是合法 base64：{exc}` 的形式回显。
func decodeBase64Tolerant(s string) ([]byte, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	// 1) 丢弃字母表外的字符（保留 '='）
	var filtered strings.Builder
	for _, r := range s {
		if r == '=' || strings.ContainsRune(alphabet, r) {
			filtered.WriteRune(r)
		}
	}
	f := filtered.String()

	// 2) 在第一个 '=' 处截断：其后连续 '=' 是填充，再往后的一律忽略
	data := f
	padRun := 0
	if i := strings.IndexByte(f, '='); i >= 0 {
		data = f[:i]
		for j := i; j < len(f) && f[j] == '='; j++ {
			padRun++
		}
	}

	// 3) 数据字符数 ≡ 1 (mod 4) 是结构性非法（先报这个）
	if len(data)%4 == 1 {
		return nil, fmt.Errorf("Invalid base64-encoded string: number of data "+
			"characters (%d) cannot be 1 more than a multiple of 4", len(data))
	}

	// 4) 填充不足 → Incorrect padding；填充多余则忽略
	need := (4 - len(data)%4) % 4
	if padRun < need {
		return nil, fmt.Errorf("Incorrect padding")
	}
	return base64.StdEncoding.DecodeString(data + strings.Repeat("=", need))
}

func (s *Server) handleUploadDelete(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	target, err := filepath.Abs(strOr(body["path"], ""))
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "只允许删除附件库内的文件"})
		return
	}
	baseDir, err := filepath.Abs(config.UploadsDir())
	if err != nil {
		baseDir = config.UploadsDir()
	}
	// Windows 路径大小写不敏感：统一小写后按目录级前缀判断
	tLower := strings.ToLower(target)
	bLower := strings.ToLower(baseDir)
	if !strings.HasPrefix(tLower, bLower+strings.ToLower(string(os.PathSeparator))) {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "只允许删除附件库内的文件"})
		return
	}
	st, err := os.Stat(target)
	if err != nil || st.IsDir() {
		writeJSON(w, 404, map[string]any{"ok": false, "message": "文件不存在或已被删除"})
		return
	}
	if err := os.Remove(target); err != nil {
		writeJSON(w, 500, map[string]any{
			"ok": false, "message": fmt.Sprintf("删除失败：%v", err)})
		return
	}
	_ = os.Remove(filepath.Dir(target)) // 空壳目录一并清理（非空时忽略）
	writeJSON(w, 200, map[string]any{"ok": true, "message": "已从附件库删除"})
}

func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	target := strings.TrimSpace(strOr(body["path"], ""))
	if target == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "缺少 path 参数"})
		return
	}
	ok, msg := sysutil.RevealTarget(target)
	out := map[string]any{"ok": ok, "message": msg}
	if ok {
		out["path"] = msg
	} else {
		out["path"] = ""
	}
	writeJSON(w, codeFor(ok, 500), out)
}

// ------------------------------------------------------------------ 用例归一化

// normalizeCases 把界面传来的用例整理成 pipeline 可消费的结构
func normalizeCases(raw any) []map[string]any {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := []map[string]any{}
	for i, item := range list {
		var m map[string]any
		if s, ok := item.(string); ok {
			m = map[string]any{"prompt": s}
		} else {
			m, _ = item.(map[string]any)
			if m == nil {
				continue
			}
		}
		prompt := strings.TrimSpace(strOr(m["prompt"], ""))
		if prompt == "" {
			continue
		}
		id := strOr(m["id"], "")
		if id == "" {
			id = fmt.Sprintf("CASE-%03d", i+1)
		}
		name := strOr(m["name"], "")
		if name == "" {
			name = fmt.Sprintf("用例 %d", i+1)
		}
		expect := m["expect_tools"]
		if expect == nil {
			expect = []any{}
		}
		labels := m["labels"]
		if labels == nil {
			labels = map[string]any{}
		}
		attachments := []string{}
		if as, ok := m["attachments"].([]any); ok {
			for _, a := range as {
				s := fmt.Sprintf("%v", a)
				if strings.TrimSpace(s) != "" {
					attachments = append(attachments, s)
				}
			}
		}
		out = append(out, map[string]any{
			"id":           id,
			"name":         name,
			"prompt":       prompt,
			"expect_tools": expect,
			"labels":       labels,
			"attachments":  attachments,
		})
	}
	return out
}

// ------------------------------------------------------------------ 类型工具

func strOr(v any, def string) string {
	if v == nil {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func intFromAny(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return def
}

func intPtrFromAny(v any) *int {
	switch t := v.(type) {
	case int:
		n := t
		return &n
	case int64:
		n := int(t)
		return &n
	case float64:
		n := int(t)
		return &n
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return &n
		}
	}
	return nil
}

func boolPtr(v any) *bool {
	if b, ok := v.(bool); ok {
		return &b
	}
	return nil
}

// truthyAny 通用真值判断（用于 clear/reveal/keep_key/parse_only/replace）
func truthyAny(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

func toMapList(v any) []map[string]any {
	out := []map[string]any{}
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
	case []map[string]any:
		out = append(out, t...)
	}
	return out
}

func toStringList(v any) []string {
	out := []string{}
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			out = append(out, fmt.Sprintf("%v", x))
		}
	case []string:
		out = append(out, t...)
	}
	return out
}

func toRows(v any) [][]string {
	switch t := v.(type) {
	case [][]string:
		return t
	case []any:
		out := make([][]string, 0, len(t))
		for _, r := range t {
			switch rr := r.(type) {
			case []string:
				out = append(out, rr)
			case []any:
				row := make([]string, 0, len(rr))
				for _, c := range rr {
					row = append(row, fmt.Sprintf("%v", c))
				}
				out = append(out, row)
			}
		}
		return out
	}
	return nil
}

func orEmptyList(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}
