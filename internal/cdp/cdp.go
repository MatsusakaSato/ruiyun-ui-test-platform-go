package cdp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// CDPError CDP 异常
type CDPError struct {
	Msg string
}

func (e *CDPError) Error() string {
	return e.Msg
}

// 本地 HTTP 客户端：显式禁用代理
var localHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: nil,
	},
	Timeout: 3 * time.Second,
}

// HTTPJSON 请求本地 JSON 端点
func HTTPJSON(urlStr string, timeout time.Duration) (any, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	client := localHTTPClient
	if timeout > 0 && timeout != localHTTPClient.Timeout {
		client = &http.Client{
			Transport: &http.Transport{Proxy: nil},
			Timeout:   timeout,
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var res any
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// WaitForPort 等待 Electron 调试端口就绪
func WaitForPort(port int, timeoutS float64) bool {
	deadline := time.Now().Add(time.Duration(timeoutS * float64(time.Second)))
	urlStr := fmt.Sprintf("http://127.0.0.1:%d/json/version", port)
	for time.Now().Before(deadline) {
		if _, err := HTTPJSON(urlStr, 2*time.Second); err == nil {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// TargetInfo CDP Target 描述
type TargetInfo struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// ListTargets 获取所有 targets
func ListTargets(port int) ([]TargetInfo, error) {
	urlStr := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	res, err := HTTPJSON(urlStr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	var targets []TargetInfo
	if err := json.Unmarshal(bytes, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

// PickPageTarget 从 targets 中挑选主界面渲染进程
func PickPageTarget(targets []TargetInfo, preferKeywords ...string) *TargetInfo {
	if len(preferKeywords) == 0 {
		preferKeywords = []string{"srtclaw", "睿云", "index.html", "localhost"}
	}
	var pages []TargetInfo
	for _, t := range targets {
		if t.Type == "page" {
			pages = append(pages, t)
		}
	}
	if len(pages) == 0 {
		return nil
	}
	for _, p := range pages {
		blob := strings.ToLower(p.URL + " " + p.Title)
		for _, kw := range preferKeywords {
			if strings.Contains(blob, strings.ToLower(kw)) {
				return &p
			}
		}
	}
	return &pages[0]
}

// CDPSession 一条渲染进程的 CDP 连接
type CDPSession struct {
	WSURL   string
	conn    *websocket.Conn
	idSeq   int64
	mu      sync.Mutex
	closed  bool
	timeout time.Duration
}

// NewCDPSession 创建 CDP 连接
func NewCDPSession(wsURL string, timeout time.Duration) (*CDPSession, error) {
	dialer := websocket.Dialer{
		Proxy:            nil,
		HandshakeTimeout: timeout,
	}
	header := http.Header{}
	header.Set("Origin", "http://127.0.0.1")

	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("CDP 连接失败: %w", err)
	}
	// 支持大报文（如 DISCOVER_JS 返回的大 DOM 树，16MB）
	conn.SetReadLimit(16 * 1024 * 1024)

	sess := &CDPSession{
		WSURL:   wsURL,
		conn:    conn,
		timeout: timeout,
	}
	return sess, nil
}

// Close 关闭连接
func (s *CDPSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.conn.Close()
}

// Call 发送 CDP 请求并等待响应
func (s *CDPSession) Call(method string, params any, timeout time.Duration) (map[string]any, error) {
	if timeout <= 0 {
		timeout = s.timeout
	}
	mid := atomic.AddInt64(&s.idSeq, 1)

	reqMap := map[string]any{
		"id":     mid,
		"method": method,
		"params": params,
	}
	if params == nil {
		reqMap["params"] = map[string]any{}
	}

	data, err := json.Marshal(reqMap)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, &CDPError{Msg: "CDP 连接已关闭"}
	}

	_ = s.conn.SetWriteDeadline(time.Now().Add(timeout))
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, &CDPError{Msg: fmt.Sprintf("CDP 发送异常: %v", err)}
	}

	deadline := time.Now().Add(timeout)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			return nil, &CDPError{Msg: fmt.Sprintf("CDP 调用超时: %s", method)}
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(remain))
		_, msgBytes, err := s.conn.ReadMessage()
		if err != nil {
			return nil, &CDPError{Msg: fmt.Sprintf("CDP 接收异常: %v", err)}
		}
		var resp map[string]any
		if err := json.Unmarshal(msgBytes, &resp); err != nil {
			continue
		}

		// 检查 id 是否匹配
		if idVal, ok := resp["id"]; ok {
			var respID int64
			switch v := idVal.(type) {
			case float64:
				respID = int64(v)
			case int64:
				respID = v
			}
			if respID == mid {
				if errObj, ok := resp["error"].(map[string]any); ok {
					return nil, &CDPError{Msg: fmt.Sprintf("%s -> %v", method, errObj)}
				}
				res, _ := resp["result"].(map[string]any)
				if res == nil {
					res = map[string]any{}
				}
				return res, nil
			}
		}
	}
}

// EnableRuntime 启用运行时
func (s *CDPSession) EnableRuntime() {
	_, _ = s.Call("Runtime.enable", nil, 10*time.Second)
}

// EvalJS 执行 JS 并获取结果
func (s *CDPSession) EvalJS(expression string, timeout time.Duration, awaitPromise bool) (any, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	res, err := s.Call("Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  awaitPromise,
		"userGesture":   true,
	}, timeout)
	if err != nil {
		return nil, err
	}
	if ex, ok := res["exceptionDetails"].(map[string]any); ok {
		desc := ""
		if expObj, ok := ex["exception"].(map[string]any); ok {
			desc = fmt.Sprintf("%v", expObj["description"])
		}
		if len(desc) > 300 {
			desc = desc[:300]
		}
		return nil, &CDPError{Msg: fmt.Sprintf("JS 异常: %s", desc)}
	}
	if result, ok := res["result"].(map[string]any); ok {
		return result["value"], nil
	}
	return nil, nil
}

// InsertText 插入文本
func (s *CDPSession) InsertText(text string) error {
	_, err := s.Call("Input.insertText", map[string]any{
		"text": text,
	}, 15*time.Second)
	return err
}

// PressKey 派发按键事件：rawKeyDown -> (可选) char -> keyUp
func (s *CDPSession) PressKey(key, code string, windowsVK, nativeVK int, text string) error {
	if key == "" {
		key = "Enter"
	}
	if code == "" {
		code = "Enter"
	}
	if windowsVK == 0 {
		windowsVK = 13
	}
	if nativeVK == 0 {
		nativeVK = 13
	}

	base := map[string]any{
		"key":                   key,
		"code":                  code,
		"windowsVirtualKeyCode": windowsVK,
		"nativeVirtualKeyCode":  nativeVK,
	}

	req1 := copyMap(base)
	req1["type"] = "rawKeyDown"
	if _, err := s.Call("Input.dispatchKeyEvent", req1, 10*time.Second); err != nil {
		return err
	}

	if text != "" {
		reqChar := copyMap(base)
		reqChar["type"] = "char"
		reqChar["text"] = text
		reqChar["unmodifiedText"] = text
		if _, err := s.Call("Input.dispatchKeyEvent", reqChar, 10*time.Second); err != nil {
			return err
		}
	}

	req2 := copyMap(base)
	req2["type"] = "keyUp"
	_, err := s.Call("Input.dispatchKeyEvent", req2, 10*time.Second)
	return err
}

// DispatchMouseEvent 派发鼠标事件
func (s *CDPSession) DispatchMouseEvent(eventType string, x, y float64, button string, clickCount int) error {
	params := map[string]any{
		"type":       eventType,
		"x":          x,
		"y":          y,
		"button":     button,
		"clickCount": clickCount,
	}
	_, err := s.Call("Input.dispatchMouseEvent", params, 10*time.Second)
	return err
}

// DispatchDragEvent 派发拖拽事件
func (s *CDPSession) DispatchDragEvent(eventType string, x, y float64, data any) error {
	params := map[string]any{
		"type": eventType,
		"x":    x,
		"y":    y,
		"data": data,
	}
	_, err := s.Call("Input.dispatchDragEvent", params, 10*time.Second)
	return err
}

func copyMap(m map[string]any) map[string]any {
	res := make(map[string]any, len(m))
	for k, v := range m {
		res[k] = v
	}
	return res
}
