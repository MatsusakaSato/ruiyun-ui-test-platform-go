// Package server 提供本地可视化平台服务。
//
// 纯标准库实现，职责：
//   - 一键运行：后台子进程执行本程序自带的 pipeline 子命令，实时回收 stdout
//   - 轮次归档：读取用户工作区 rounds/<run_id>/ 的落盘数据
//   - REST API：轮次列表 / 轮次详情 / 运行状态 / 预设用例 / 文件管理器定位
//
// 路由与响应字段是前端 JS（4,120 行）依赖的稳定契约。
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"

	"ruiyun-ui-test-platform-go/internal/llm"
)

// Server 平台 HTTP 服务
type Server struct {
	// AllowLAN 对应 --allow-lan 启动参数，临时放行局域网访问
	AllowLAN bool
	// SelfExe 当前可执行文件路径（用于拉起 pipeline 子进程）
	SelfExe string

	state *RunState
	eval  *EvalState
}

// NewServer 构造服务
func NewServer(allowLAN bool, selfExe string) *Server {
	s := &Server{
		AllowLAN: allowLAN,
		SelfExe:  selfExe,
		state:    NewRunState(allowLAN, selfExe),
		eval:     NewEvalState(),
	}
	return s
}

// State 暴露运行状态（供测试与 main 使用）
func (s *Server) State() *RunState { return s.state }

// Eval 暴露评估状态
func (s *Server) Eval() *EvalState { return s.eval }

// ------------------------------------------------------------------ 响应工具

func writeJSON(w http.ResponseWriter, code int, obj any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		_, _ = w.Write([]byte("{}"))
		return
	}
	// json.Encoder 默认会追加换行，响应体不能带这个换行
	_, _ = w.Write([]byte(strings.TrimRight(buf.String(), "\n")))
}

func writeBody(w http.ResponseWriter, code int, body []byte, ctype string) {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func writeText(w http.ResponseWriter, code int, text string) {
	writeBody(w, code, []byte(text), "text/plain; charset=utf-8")
}

// readJSONBody 读 body 解析 JSON，任何异常都退化成空 dict
func readJSONBody(r *http.Request) map[string]any {
	raw, err := io.ReadAll(r.Body)
	if err != nil || len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

// ------------------------------------------------------------------ 同源校验

// isPrivateHost 私网地址判断（主机名一律不算私网 —— 放行它等于向 DNS 重绑定开门）
func isPrivateHost(host string) bool {
	h := strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if h == "localhost" || h == "::1" || strings.HasPrefix(h, "127.") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil || ip.To4() == nil {
		return false
	}
	b := ip.To4()
	return b[0] == 10 || (b[0] == 172 && b[1] >= 16 && b[1] <= 31) || (b[0] == 192 && b[1] == 168)
}

// hostOnly 取主机名并去掉方括号（用于 Origin；能正确解析 IPv6 字面量）。
func hostOnly(hostport string) string {
	if hostport == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(strings.TrimSpace(h), "[]")
	}
	return strings.Trim(strings.TrimSpace(hostport), "[]")
}

// hostFromHeader 从 Host 头取主机名：取 `:` 前的第一段并去掉方括号。
//
// ⚠ 这里**故意**使用这种朴素切分方式（连同它的缺陷）：IPv6 字面量 `[::1]:8791`
// 会被切成 `"["`、再去掉方括号成空串，于是被拒。
// 端到端实测：改用 net.SplitHostPort 会得到 `::1` 而放行 —— 会放宽安全面，
// 故保留这种切分方式，让 IPv6 字面量 Host 一律被拒。
//
// 另注：Go 的 net/http 会把 Host 头搬进 r.Host，r.Header.Get("Host") **恒为空**，
// 所以调用方必须传 r.Host，否则 forbidden 里回显的 Host 会是 -。
func hostFromHeader(raw string) string {
	first := raw
	if i := strings.Index(raw, ":"); i >= 0 {
		first = raw[:i]
	}
	return strings.Trim(first, "[]")
}

// sameOriginOK Host 非本机，或带 Origin 但不是本机 → 一律拒绝。
func (s *Server) sameOriginOK(r *http.Request) bool {
	host := hostFromHeader(r.Host)
	ok := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if !ok && !(s.AllowLAN && isPrivateHost(host)) {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		oh := originHostname(origin)
		if !(oh == "127.0.0.1" || oh == "localhost" || oh == "::1") &&
			!(s.AllowLAN && isPrivateHost(oh)) {
			return false
		}
	}
	return true
}

func originHostname(origin string) string {
	o := strings.TrimSpace(origin)
	if i := strings.Index(o, "://"); i >= 0 {
		o = o[i+3:]
	}
	if i := strings.IndexAny(o, "/?#"); i >= 0 {
		o = o[:i]
	}
	return strings.ToLower(hostOnly(o))
}

func (s *Server) forbidden(w http.ResponseWriter, r *http.Request) {
	// 回显观测到的 Host/Origin（Host 头在 Go 里只能从 r.Host 取到）。
	writeJSON(w, 403, map[string]any{
		"ok":       false,
		"category": "forbidden",
		"message": fmt.Sprintf(
			"来源校验未通过：仅允许经 127.0.0.1 / localhost 访问本服务（Host=%s，Origin=%s）",
			orDash(r.Host), orDash(r.Header.Get("Origin"))),
	})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ------------------------------------------------------------------ 路由

// Handler 返回平台 HTTP 处理器
//
// ⚠ 刻意**不用** http.ServeMux：ServeMux 会做路径清洗（cleanPath），把
// `/api/rounds/../../etc/passwd` 307 重定向到 `/etc/passwd`；而路由匹配必须
// **不做任何路径清洗**（否则 rid 会被清洗掉，取不到 `..` 这类原始段）。
// 端到端实测到该不一致，故直接把 route 挂成根处理器。
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.route)
}

// notImplementedResponse 未实现方法的 501 响应。
//
// 响应体必须逐字匹配约定了的固定格式：模板里那句
// `Error code explanation: HTTPStatus.NOT_IMPLEMENTED - Server does not support this operation.`
// 不是 "501"，枚举名与说明文字要原样保留。
// 前端只用 GET/POST/DELETE，这条路径实际不可达，但响应体仍要求一致。
func notImplementedResponse(w http.ResponseWriter, method string) {
	body := "<!DOCTYPE HTML PUBLIC \"-//W3C//DTD HTML 4.01//EN\"\n" +
		"        \"http://www.w3.org/TR/html4/strict.dtd\">\n" +
		"<html>\n" +
		"    <head>\n" +
		"        <meta http-equiv=\"Content-Type\" content=\"text/html;charset=utf-8\">\n" +
		"        <title>Error response</title>\n" +
		"    </head>\n" +
		"    <body>\n" +
		"        <h1>Error response</h1>\n" +
		"        <p>Error code: 501</p>\n" +
		"        <p>Message: Unsupported method ('" + method + "').</p>\n" +
		"        <p>Error code explanation: HTTPStatus.NOT_IMPLEMENTED - Server does not support this operation.</p>\n" +
		"    </body>\n" +
		"</html>\n"
	w.Header().Set("Content-Type", "text/html;charset=utf-8")
	w.WriteHeader(501)
	_, _ = w.Write([]byte(body))
}

// rawPath 取路由用的路径 —— **不做百分号解码**。
// Go 的 r.URL.Path 已经解码过（`%2f` 会变成 `/`），会让 /api/rounds/<rid> 与
// DELETE 的 run_id 取值走偏（实测 `..%2fetc` 报错文案就不同），所以改用
// EscapedPath，让路由看到未解码的原始路径。
func rawPath(r *http.Request) string {
	if p := r.URL.EscapedPath(); p != "" {
		return p
	}
	return r.URL.Path
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.doGet(w, r)
	case http.MethodPost:
		if !s.sameOriginOK(r) {
			s.forbidden(w, r)
			return
		}
		s.doPost(w, r)
	case http.MethodDelete:
		if !s.sameOriginOK(r) {
			s.forbidden(w, r)
			return
		}
		s.doDelete(w, r)
	default:
		// 未实现的方法回 501，不是 404。
		notImplementedResponse(w, r.Method)
	}
}

func (s *Server) doGet(w http.ResponseWriter, r *http.Request) {
	p := rawPath(r)

	if p == "/" || p == "/index.html" {
		data, err := readWebFile("index.html")
		if err != nil {
			writeText(w, 404, "dashboard 未找到")
			return
		}
		writeBody(w, 200, data, "text/html; charset=utf-8")
		return
	}

	// 静态资源支持（css, js, 图标等）
	if strings.HasPrefix(p, "/css/") || strings.HasPrefix(p, "/js/") || p == "/favicon.ico" {
		rel := strings.TrimPrefix(p, "/")
		if serveStatic(w, rel) {
			return
		}
		writeText(w, 404, "Not Found")
		return
	}

	switch {
	case p == "/api/rounds":
		s.handleRoundsList(w, r)
		return
	case strings.HasPrefix(p, "/api/rounds/"):
		s.handleRoundDetail(w, r)
		return
	case p == "/api/run/status":
		writeJSON(w, 200, s.state.Status())
		return
	case p == "/api/config":
		s.handleConfig(w)
		return
	case p == "/api/preset-cases":
		s.handlePresetCasesGet(w, r)
		return
	case p == "/api/env":
		s.handleEnvGet(w)
		return
	case p == "/api/app/status":
		writeJSON(w, 200, map[string]any{"running": appIsRunning(nil)})
		return
	case p == "/api/app-settings":
		s.handleAppSettingsGet(w)
		return
	case p == "/api/llm/config":
		writeJSON(w, 200, llm.PublicConfig())
		return
	case p == "/api/uploads":
		writeJSON(w, 200, map[string]any{"files": listUploads()})
		return
	case p == "/api/eval/status":
		writeJSON(w, 200, s.eval.Status())
		return
	}
	writeText(w, 404, "not found")
}

func (s *Server) doPost(w http.ResponseWriter, r *http.Request) {
	p := rawPath(r)
	switch p {
	case "/api/env":
		s.handleEnvPost(w, r)
	case "/api/run/stop":
		ok, msg := s.state.Stop()
		writeJSON(w, codeFor(ok, 409), map[string]any{"ok": ok, "message": msg})
	case "/api/app/close":
		if s.state.ProcRunning() {
			writeJSON(w, 409, map[string]any{"ok": false, "message": "有测试正在运行，无法关闭应用"})
			return
		}
		ok, msg := closeApp()
		writeJSON(w, codeFor(ok, 400), map[string]any{
			"ok": ok, "message": msg, "running": appIsRunning(nil)})
	case "/api/run":
		s.handleRunStart(w, r)
	case "/api/llm/config":
		s.handleLLMConfigPost(w, r)
	case "/api/preset-cases":
		s.handlePresetCasesPost(w, r)
	case "/api/preset-import":
		s.handlePresetImport(w, r)
	case "/api/app-settings":
		s.handleAppSettingsPost(w, r)
	case "/api/evaluate":
		s.handleEvaluate(w, r)
	case "/api/evaluate/stop":
		ok, msg := s.eval.Stop()
		writeJSON(w, codeFor(ok, 409), map[string]any{"ok": ok, "message": msg})
	case "/api/llm/test":
		s.handleLLMTest(w, r)
	case "/api/upload":
		s.handleUpload(w, r)
	case "/api/upload/delete":
		s.handleUploadDelete(w, r)
	case "/api/reveal":
		s.handleReveal(w, r)
	default:
		writeText(w, 404, "not found")
	}
}

func (s *Server) doDelete(w http.ResponseWriter, r *http.Request) {
	p := rawPath(r)
	if strings.HasPrefix(p, "/api/rounds/") {
		rid := strings.Trim(strings.TrimPrefix(p, "/api/rounds/"), "/")
		running := s.state.RunID() == rid && s.state.ProcRunning()
		ok, msg := deleteRound(rid, running)
		code := 200
		if !ok {
			if strings.Contains(msg, "不存在") {
				code = 404
			} else {
				code = 400
			}
		}
		ridOut := ""
		if ok {
			ridOut = rid
			if s.state.RunID() == rid {
				s.state.ClearRunID()
			}
		}
		writeJSON(w, code, map[string]any{"ok": ok, "run_id": ridOut, "message": msg})
		return
	}
	writeText(w, 404, "not found")
}

func codeFor(ok bool, failCode int) int {
	if ok {
		return 200
	}
	return failCode
}

// ------------------------------------------------------------------ 静态资源

func readWebFile(rel string) ([]byte, error) {
	sub, err := DistFS()
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(sub, rel)
}

// serveStatic 静态资源分支：防路径穿越 + 按扩展名猜 MIME
func serveStatic(w http.ResponseWriter, rel string) bool {
	clean := path.Clean("/" + rel)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || strings.HasPrefix(clean, "..") || strings.Contains(clean, "..") {
		return false
	}
	sub, err := DistFS()
	if err != nil {
		return false
	}
	st, err := fs.Stat(sub, clean)
	if err != nil || st.IsDir() {
		return false
	}
	data, err := fs.ReadFile(sub, clean)
	if err != nil {
		return false
	}
	ctype := mime.TypeByExtension(path.Ext(clean))
	if ctype != "" {
		base := ctype
		if i := strings.Index(base, ";"); i >= 0 {
			base = strings.TrimSpace(base[:i])
		}
		if strings.HasPrefix(base, "text/") || base == "application/javascript" {
			ctype = base + "; charset=utf-8"
		}
	} else {
		ctype = "application/octet-stream"
	}
	writeBody(w, 200, data, ctype)
	return true
}
