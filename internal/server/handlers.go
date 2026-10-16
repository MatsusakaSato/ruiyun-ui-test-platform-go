package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
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

func (s *Server) handleTemplateDownload(w http.ResponseWriter, r *http.Request) {
	file := filepath.Join(config.RootDir, "resource", "template.xlsx")
	if st, err := os.Stat(file); err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="template.xlsx"`)
	http.ServeFile(w, r, file)
}

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

// ------------------------------------------------------------------ 导入时附件规范化

// resolveAttachment 尝试把 Excel 附件列里的一格字符串落到附件库，返回库内绝对路径。
//
// 三类来源：
//   - http(s) 链接：服务端下载到附件库；下载失败（网络/4xx5xx/超限）返回原因。
//   - 本地绝对路径且真实存在：复制进附件库；不存在/是目录/超限返回原因。
//   - 其它（相对路径、无法识别）：返回原因，不做处理。
//
// 失败时 path 为空、reason 为给用户看的中文说明。调用方据此**剔除**该附件并提示，
// 不再像早期那样「原样保留」—— 把 URL / 外部路径写进库，只会在运行时变成
// 「附件不存在，未投递」，或让 /api/open 因越界而 403。
func resolveAttachment(src string) (path, reason string) {
	s := strings.TrimSpace(src)
	if s == "" {
		return "", ""
	}
	if isHTTPLink(s) {
		fp, err := downloadToUploads(s)
		if err != nil {
			return "", fmt.Sprintf("链接无法下载（%v）", err)
		}
		return fp, ""
	}
	if !filepath.IsAbs(s) {
		return "", "既不是 http(s) 链接，也不是本机绝对路径"
	}
	st, err := os.Stat(s)
	if err != nil {
		return "", "本机找不到该文件"
	}
	if st.IsDir() {
		return "", "指向的是目录，不是文件"
	}
	// 已经在附件库内的路径原样保留：手动新增传来的附件本就是库内已落好盘的文件，
	// 再复制一份只会在每次保存时多留一个副本，且返回的路径与用户选中的对不上。
	if inUploadsDir(s) {
		return s, ""
	}
	fp, err := copyToUploads(s)
	if err != nil {
		return "", fmt.Sprintf("复制进附件库失败（%v）", err)
	}
	return fp, ""
}

func isHTTPLink(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// attachmentShapeIssue 静态判断附件条目是否「看起来就无法解析」，不发起任何网络请求。
// 供导入预览提前提示；链接能否真正下载，只有导入时才验证。
func attachmentShapeIssue(src string) string {
	s := strings.TrimSpace(src)
	if s == "" {
		return ""
	}
	if isHTTPLink(s) {
		if _, err := url.Parse(s); err != nil {
			return "链接格式不正确"
		}
		return ""
	}
	if !filepath.IsAbs(s) {
		return "既不是 http(s) 链接，也不是本机绝对路径"
	}
	if st, err := os.Stat(s); err != nil || st.IsDir() {
		return "本机找不到该文件"
	}
	return ""
}

// copyToUploads 把本机一个已存在的文件原样复制进附件库，返回库内绝对路径。
func copyToUploads(src string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if len(data) > MaxUploadBytes {
		return "", fmt.Errorf("文件超过 %dMB 上限", MaxUploadBytes/1048576)
	}
	return saveUpload(filepath.Base(src), data)
}

// downloadToUploads 把 http(s) 链接下载进附件库，返回库内绝对路径。
// 超时 60s，正文上限与上传一致（MaxUploadBytes），文件名优先取 Content-Disposition，
// 否则取 URL 基名，再不济用 "download"。
func downloadToUploads(link string) (string, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(link)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, MaxUploadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(data) > MaxUploadBytes {
		return "", fmt.Errorf("文件超过 %dMB 上限", MaxUploadBytes/1048576)
	}
	return saveUpload(filenameFromResponse(resp, link), data)
}

// filenameFromResponse 从响应头或 URL 推断落盘文件名，缺失时回退 "download"。
func filenameFromResponse(resp *http.Response, link string) string {
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if fn, ok := params["filename"]; ok && strings.TrimSpace(fn) != "" {
				return fn
			}
			// RFC 5987 形式：filename*=UTF-8''xxx
			if fn, ok := params["filename*"]; ok && strings.TrimSpace(fn) != "" {
				return strings.TrimSpace(fn)
			}
		}
	}
	if u, err := url.Parse(link); err == nil {
		if base := filepath.Base(u.Path); base != "" && base != "." && base != "/" {
			return base
		}
	}
	return "download"
}

// ingestItemAttachmentsEx 就地把一行用例的 attachments 规范化成附件库内的绝对路径，
// 并回报无法解析、已被剔除的条目。rowIdx 是表格里 1 起的行号，用于提示定位。
//
// 策略是「剔除 + 回报」：无效附件不留在库里制造悬空引用；剔除后若一个都不剩，
// 连带把 labels.attachment 标记回落为 false（与「挂了附件才需要附件」的约定一致）。
func ingestItemAttachmentsEx(item map[string]any, rowIdx int) (ingested, dropped int, failed []map[string]any) {
	raw, ok := item["attachments"]
	if !ok || raw == nil {
		return 0, 0, nil
	}
	var srcs []string
	switch list := raw.(type) {
	case []string:
		srcs = list
	case []any:
		for _, v := range list {
			srcs = append(srcs, fmt.Sprintf("%v", v))
		}
	default:
		return 0, 0, nil
	}
	kept := make([]string, 0, len(srcs))
	for _, s := range srcs {
		orig := strings.TrimSpace(s)
		if orig == "" {
			continue
		}
		fp, reason := resolveAttachment(orig)
		if fp == "" {
			dropped++
			failed = append(failed, map[string]any{
				"row":    rowIdx,
				"name":   strOr(item["name"], ""),
				"value":  orig,
				"reason": reason,
			})
			continue
		}
		ingested++
		kept = append(kept, fp)
	}
	if len(kept) == 0 {
		delete(item, "attachments")
		clearAttachmentLabel(item)
		return ingested, dropped, failed
	}
	item["attachments"] = kept
	return ingested, dropped, failed
}

// clearAttachmentLabel 把「需要附件」标记回落到 false（附件被全部剔除时调用）。
// RowsToItems 会在顶层与 labels 两处都写这个标记，两处都要回落，否则入库时
// extractCaseFields 会从顶层读到 true，留下「标记需要附件、却一个附件都没有」的脏数据。
func clearAttachmentLabel(item map[string]any) {
	if _, has := item["attachment"]; has {
		item["attachment"] = false
	}
	labels, _ := item["labels"].(map[string]any)
	if labels == nil {
		return
	}
	if _, has := labels["attachment"]; has {
		labels["attachment"] = false
	}
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
		one("keyword", ""), splitCSV(one("scene", "")),
		splitCSV(one("difficulties", "")), splitCSV(one("tags", "")), one("attachment", ""),
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

// handleNotifyConfigGet 通知页配置面板的群列表（token / secret 已打码）
func (s *Server) handleNotifyConfigGet(w http.ResponseWriter) {
	writeJSON(w, 200, notify.PublicRobots())
}

// handleNotifyConfigPost 保存 / 清空通知页的群配置。
//
//	{"action":"save","robots":[{"key","name","token","secret"}]}
//	{"action":"clear"}            清空自定义，回落内置默认
//	{"action":"reveal","key":"…"} 取某群的明文 token / secret
//
// 约定：token / secret 留空表示「沿用原来的值」（前端只回显打码提示，不回传明文）。
// 新群没有原值可沿用，token 留空则直接报错——空 token 存下来也发不出去。
func (s *Server) handleNotifyConfigPost(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	switch strings.TrimSpace(strOr(body["action"], "")) {
	case "save":
		cur, _ := notify.LoadRobots()
		byKey := make(map[string]notify.RobotEntry, len(cur))
		for _, c := range cur {
			byKey[c.Key] = c
		}

		raw := toMapList(body["robots"])
		entries := make([]notify.RobotEntry, 0, len(raw))
		for i, m := range raw {
			key := strings.TrimSpace(strOr(m["key"], ""))
			if key == "" {
				key = fmt.Sprintf("custom-%d", i+1)
			}
			name := strings.TrimSpace(strOr(m["name"], ""))
			token := strings.TrimSpace(strOr(m["token"], ""))
			secret := strings.TrimSpace(strOr(m["secret"], ""))
			if old, ok := byKey[key]; ok {
				if token == "" {
					token = old.Token
				}
				if secret == "" {
					secret = old.Secret
				}
			}
			if token == "" {
				label := name
				if label == "" {
					label = key
				}
				writeJSON(w, 400, map[string]any{
					"ok": false, "message": fmt.Sprintf("「%s」缺少 Token，没法发送", label)})
				return
			}
			entries = append(entries, notify.RobotEntry{Key: key, Name: name, Token: token, Secret: secret})
		}
		if len(entries) == 0 {
			writeJSON(w, 400, map[string]any{
				"ok": false, "message": "至少保留一个群；想恢复内置的正式群 / 测试群请点「恢复默认」"})
			return
		}
		if err := notify.SaveRobots(entries); err != nil {
			writeJSON(w, 500, map[string]any{
				"ok": false, "message": fmt.Sprintf("保存失败：%v", err)})
			return
		}
		out := notify.PublicRobots()
		out["message"] = fmt.Sprintf("已保存 %d 个群", len(entries))
		writeJSON(w, 200, out)

	case "clear":
		notify.ClearRobots()
		out := notify.PublicRobots()
		out["message"] = "已恢复内置的正式群 / 测试群"
		writeJSON(w, 200, out)

	case "reveal":
		key := strings.TrimSpace(strOr(body["key"], ""))
		res, ok := notify.RevealRobot(key)
		if !ok {
			writeJSON(w, 404, map[string]any{"ok": false, "message": "群不存在（可能已被删除）"})
			return
		}
		writeJSON(w, 200, res)

	default:
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": "未知操作（支持 save / clear / reveal）"})
	}
}

// handleNotifySend 发送钉钉群通知（「发送通知」页）。
// 请求体 {"message": "...", "group": "<配置里的群 key>"}；
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
		// 附件是「先落盘、再提交」：逐个落到附件库（已在库内的原样保留，不重复复制），
		// 中间可能已被附件库删除（或从库里挑中的文件被删）；
		// 这类悬空路径入库只会在运行时变成「附件不存在，未投递」，所以在写入前就剔除，
		// 并把剔除清单回传前端提示。其余正常附件照常入库，不因个别失效而整单失败。
		_, dropped, failed := ingestItemAttachmentsEx(item, 1)
		ok, msg, c := testcasedb.AddPresetCase(item, "")
		out := map[string]any{"ok": ok, "message": msg, "case": c}
		if dropped > 0 {
			out["attachments_dropped"] = dropped
			out["invalid_attachments"] = failed
		}
		writeJSON(w, codeFor(ok, 400), out)
	case "update":
		item, _ := body["item"].(map[string]any)
		if item == nil {
			item = body
		}
		// 编号可以平铺在顶层，也可以在 item 里；两处都没有就直接拒掉，
		// 免得后续按「新增」写出一条新用例（那是 add 的语义）。
		if strings.TrimSpace(strOr(item["id"], "")) == "" {
			item["id"] = strOr(body["id"], "")
		}
		if strings.TrimSpace(strOr(item["id"], "")) == "" {
			writeJSON(w, 400, map[string]any{"ok": false, "message": "缺少用例编号（id）"})
			return
		}
		// 附件与新增同路：先落盘 / 剔除失效再写入，剔除清单照样回传前端提示
		_, dropped, failed := ingestItemAttachmentsEx(item, 1)
		ok, msg, c := testcasedb.UpdatePresetCase(item, "")
		out := map[string]any{"ok": ok, "message": msg, "case": c}
		if dropped > 0 {
			out["attachments_dropped"] = dropped
			out["invalid_attachments"] = failed
		}
		writeJSON(w, codeFor(ok, 400), out)
	case "add_many":
		items := toMapList(body["items"])
		for i, it := range items {
			ingestItemAttachmentsEx(it, i+1)
		}
		ok, msg, added := testcasedb.AddPresetCases(items, false, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "added": added})
	case "delete":
		ids := toStringList(body["ids"])
		ok, msg, removed := testcasedb.DeletePresetCases(ids, "")
		writeJSON(w, codeFor(ok, 400), map[string]any{"ok": ok, "message": msg, "removed": removed})
	default:
		writeJSON(w, 400, map[string]any{
			"ok": false, "message": "未知操作（支持 add / add_many / update / delete）"})
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
	difficultyCol := getCol("difficulty_col", intPtrFromAny(meta["difficulty_col"]))
	attachCol := getCol("attachment_col", intPtrFromAny(meta["attachment_col"]))
	headIdx := intPtrFromAny(meta["head_idx"])

	promptIdx := 0
	if promptCol != nil {
		promptIdx = *promptCol
	}
	// 多轮列：轮号(1-based) -> 列下标
	turnCols := map[int]int{}
	if tm, ok := meta["turn_cols"].(map[int]int); ok {
		turnCols = tm
	} else if tm, ok := meta["turn_cols"].(map[string]any); ok {
		for k, v := range tm {
			n, err1 := strconv.Atoi(strings.TrimSpace(k))
			ci, err2 := strconv.Atoi(strings.TrimSpace(fmt.Sprintf("%v", v)))
			if err1 == nil && err2 == nil {
				turnCols[n] = ci
			}
		}
	}

	got := xlsx.RowsToItems(rows, headIdx, xlsx.CaseCols{
		Prompt:     promptIdx,
		Scene:      sceneCol,
		Attachment: attachCol,
		Difficulty: difficultyCol,
		TurnCols:   turnCols,
	})
	items := toMapList(got["items"])
	skipped := intFromAny(got["skipped"], 0)

	// 模板判定：必须有可识别的表头行，且表头里能认出提问列（Query内容 / 提问 …）。
	// 平台已提供标准模板，因此不再保留「手动指定列」的旁路。
	validTemplate, _ := meta["valid_template"].(bool)
	templateErr := strOr(meta["template_error"], "")

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
		suspicious := []map[string]any{}
		for i, it := range items {
			if i < 200 {
				// prompt 自多轮改造起是数组；预览按轮返回，供前端显示「多轮 · N」
				turns := toStringList(it["prompt"])
				previewTurns := make([]string, 0, len(turns))
				for _, t := range turns {
					if len([]rune(t)) > 200 {
						t = string([]rune(t)[:200])
					}
					previewTurns = append(previewTurns, t)
				}
				promptText := ""
				if len(previewTurns) > 0 {
					promptText = previewTurns[0]
				}
				preview = append(preview, map[string]any{
					"prompt":      previewTurns,
					"turns":       previewTurns,
					"turn_count":  len(previewTurns),
					"prompt_text": promptText, // 兼容旧前端/日志的单行展示
					"scene":       strOr(it["scene"], ""),
					"difficulty":  strOr(it["difficulty"], ""),
					"attachments": orEmptyList(it["attachments"]),
				})
			}
			for _, a := range toStringList(it["attachments"]) {
				if len(suspicious) >= 100 {
					break
				}
				if reason := attachmentShapeIssue(a); reason != "" {
					suspicious = append(suspicious, map[string]any{
						"row":    i + 1,
						"name":   strOr(it["name"], ""),
						"value":  a,
						"reason": reason,
					})
				}
			}
		}
		writeJSON(w, 200, map[string]any{
			"ok":                     true,
			"file":                   name,
			"sheet":                  strOr(table["sheet"], ""),
			"header":                 header,
			"width":                  width,
			"prompt_col":             promptCol,
			"scene_col":              sceneCol,
			"difficulty_col":         difficultyCol,
			"attachment_col":         attachCol,
			"total":                  len(items),
			"skipped":                skipped,
			"items":                  preview,
			"items_total":            len(items),
			"valid_template":         validTemplate,
			"template_error":         templateErr,
			"suspicious_attachments": suspicious,
		})
		return
	}

	if !validTemplate {
		if templateErr == "" {
			templateErr = "文件不符合系统提供的导入模板"
		}
		writeJSON(w, 400, map[string]any{"ok": false, "message": templateErr})
		return
	}

	// 导入（非预览）时才把附件列里的本地绝对路径 / http(s) 链接规范化进附件库。
	// 预览阶段原样返回原始串，便于用户核对表格里写了什么。
	// 无效附件（下载失败的链接、本机不存在的路径等）一律剔除并逐条回报，不留悬空引用。
	var ingestedTotal, droppedTotal int
	invalidAttachments := []map[string]any{}
	for i, it := range items {
		n, d, failed := ingestItemAttachmentsEx(it, i+1)
		ingestedTotal += n
		droppedTotal += d
		invalidAttachments = append(invalidAttachments, failed...)
	}
	ok, msg, added, duplicates := testcasedb.AddPresetCasesEx(items, truthyAny(body["replace"]), "")
	if ok && added == 0 && duplicates > 0 {
		msg = fmt.Sprintf("未新增用例：导入的 %d 条全部与库中已有用例重复", duplicates)
	}
	out := map[string]any{
		"ok": ok, "message": msg, "added": added, "duplicates": duplicates,
		"file": name, "total": len(items), "skipped": skipped}
	// 附件规范化汇总：多少条原始条目被拉进附件库、多少条因无法解析被剔除。
	if ingestedTotal > 0 {
		out["attachments_ingested"] = ingestedTotal
	}
	if droppedTotal > 0 {
		out["attachments_dropped"] = droppedTotal
		out["invalid_attachments"] = invalidAttachments
	}
	writeJSON(w, codeFor(ok, 400), out)
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

// inUploadsDir 判断绝对路径是否落在附件库目录内。
// Windows 路径大小写不敏感：统一小写后按目录级前缀判断（避免 /uploads2 误命中 /uploads）。
func inUploadsDir(abs string) bool {
	baseDir, err := filepath.Abs(config.UploadsDir())
	if err != nil {
		baseDir = config.UploadsDir()
	}
	return strings.HasPrefix(strings.ToLower(abs),
		strings.ToLower(baseDir)+strings.ToLower(string(os.PathSeparator)))
}

func (s *Server) handleUploadDelete(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	target, err := filepath.Abs(strOr(body["path"], ""))
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "只允许删除附件库内的文件"})
		return
	}
	if !inUploadsDir(target) {
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

	// 已入库的用例可能还引用着这个路径，不摘掉就是悬空路径，
	// 只会在运行时投递时才报「附件不存在」。清单更新失败不影响文件已删除的事实，只回报原因。
	out := map[string]any{"ok": true, "message": "已从附件库删除"}
	if cleaned, err := testcasedb.RemoveAttachmentFromAllCases(target, ""); err != nil {
		out["message"] = fmt.Sprintf("已从附件库删除；用例引用清理失败：%v", err)
	} else {
		out["cleaned_cases"] = cleaned
	}
	writeJSON(w, 200, out)
}

// handleOpenFile 用系统默认程序打开附件（预设用例库「附件」气泡里点文件名触发）。
// 这是唯一会拉起本机程序的接口，路径必须收敛在附件库目录内。
func (s *Server) handleOpenFile(w http.ResponseWriter, r *http.Request) {
	body := readJSONBody(r)
	raw := strings.TrimSpace(strOr(body["path"], ""))
	if raw == "" {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "缺少 path 参数"})
		return
	}
	target, err := filepath.Abs(raw)
	if err != nil {
		writeJSON(w, 400, map[string]any{"ok": false, "message": "路径不合法"})
		return
	}
	if !inUploadsDir(target) {
		writeJSON(w, 403, map[string]any{"ok": false, "message": "只允许打开附件库内的文件"})
		return
	}
	if st, err := os.Stat(target); err != nil || st.IsDir() {
		writeJSON(w, 404, map[string]any{"ok": false, "message": "文件不存在或已被删除"})
		return
	}
	ok, msg := sysutil.OpenTarget(target)
	out := map[string]any{"ok": ok, "message": msg, "path": ""}
	if ok {
		out["path"] = msg
	}
	writeJSON(w, codeFor(ok, 500), out)
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
			// 裸字符串 = 单轮用例（内部调用方仍可能这么传）
			m = map[string]any{"prompt": []any{s}}
		} else {
			m, _ = item.(map[string]any)
			if m == nil {
				continue
			}
		}
		// prompt 是数组（多轮）；单轮也是长度 1 的数组。
		// 兼容前端可能传来的字符串形态：按单轮处理。
		var turns []string
		switch v := m["prompt"].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				turns = append(turns, s)
			}
		default:
			for _, t := range toStringList(m["prompt"]) {
				if s := strings.TrimSpace(t); s != "" {
					turns = append(turns, s)
				}
			}
		}
		if len(turns) == 0 {
			continue
		}
		if len(turns) > testcasedb.MaxTurns {
			turns = turns[:testcasedb.MaxTurns]
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
			"prompt":       turns,
			"turns":        turns,
			"turn_count":   len(turns),
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
	case *int:
		return t
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
