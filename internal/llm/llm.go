package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/pyre"
)

const (
	DefaultTimeoutS  = 12.0
	MaxDetailChars   = 300
	UserAgent        = "Ruiyun-UI-Test-Platform/1.0 (llm-probe)"
	PlaceholderModel = "gpt-3.5-turbo"
	ChatMaxBytes     = 2 * 1024 * 1024

	CatOK          = "ok"
	CatAuth        = "auth"
	CatNotFound    = "not_found"
	CatTimeout     = "timeout"
	CatNetwork     = "network"
	CatTLS         = "tls"
	CatServerError = "server_error"
	CatInvalid     = "invalid_input"
)

var (
	reBearer = regexp.MustCompile(`(?i)(bearer[` + pyre.SpaceClass + `]+)[A-Za-z0-9._\-]{6,}`)
	reAPIKey = regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|apikey|authorization|access[_-]?token)["']?[` + pyre.SpaceClass + `]*[:=][` + pyre.SpaceClass + `]*["']?)[^"'` + pyre.SpaceClass + `,}]{4,}`)
)

// Redact 抹去敏感凭证
func Redact(text string, apiKey string) string {
	if text == "" {
		return ""
	}
	out := text
	if apiKey != "" {
		out = strings.ReplaceAll(out, apiKey, "***")
	}
	out = reBearer.ReplaceAllString(out, "${1}***")
	out = reAPIKey.ReplaceAllString(out, "${1}***")
	return out
}

func clip(text string, limit int) string {
	t := strings.TrimSpace(text)
	r := []rune(t)
	if len(r) <= limit {
		return t
	}
	return string(r[:limit]) + "…"
}

type ProbeResult struct {
	OK        bool   `json:"ok"`
	Stage     string `json:"stage"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Status    *int   `json:"status"`
	Detail    string `json:"detail"`
	LatencyMs int    `json:"latency_ms"`
}

func (pr *ProbeResult) ToDict() map[string]any {
	return map[string]any{
		"ok":         pr.OK,
		"stage":      pr.Stage,
		"category":   pr.Category,
		"message":    pr.Message,
		"status":     pr.Status,
		"detail":     pr.Detail,
		"latency_ms": pr.LatencyMs,
	}
}

// LLMClient 大模型客户端
type LLMClient struct {
	httpClient *http.Client
}

func NewLLMClient(timeout time.Duration) *LLMClient {
	// 使用系统代理设置 (http.ProxyFromEnvironment)
	return &LLMClient{
		httpClient: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: timeout,
		},
	}
}

// ProbeProvider 供应商探活
func ProbeProvider(baseURL, apiKey, model, probePath string, timeoutS float64) *ProbeResult {
	bURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	key := pyre.Strip(apiKey) // Python 是 (api_key or "").strip()
	mName := strings.TrimSpace(model)

	if timeoutS <= 0 {
		timeoutS = DefaultTimeoutS
	}
	client := NewLLMClient(time.Duration(timeoutS * float64(time.Second)))

	t0 := time.Now()
	elapsedMs := func() int {
		return int(time.Since(t0).Milliseconds())
	}

	if bURL == "" || key == "" {
		return &ProbeResult{
			OK:        false,
			Stage:     "input",
			Category:  CatInvalid,
			Message:   "供应商地址与 API Key 均不能为空",
			LatencyMs: elapsedMs(),
		}
	}

	// API Key 绝不含空白。把「Key + 模型名/地址」一起粘进来是最常见的坑：
	// 直接判入参错误，比发出去换回一个含义模糊的 401 更容易定位。
	// 对应 core/llm_client.py:242（原文逐字保留）。
	if pyre.ContainsSpace(key) {
		return &ProbeResult{
			OK:       false,
			Stage:    "preflight",
			Category: CatInvalid,
			Message: fmt.Sprintf("API Key 中不能含空格或换行（当前 %d 字符）——"+
				"很可能把「模型名」一起粘进来了。请只填 Key 原文，模型名填到「模型名」字段",
				utf8.RuneCountInString(key)),
			LatencyMs: elapsedMs(),
		}
	}

	doReq := func(method, urlStr string, bodyData []byte) (int, string, error) {
		req, err := http.NewRequest(method, urlStr, bytes.NewReader(bodyData))
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Authorization", "Bearer "+key)
		if len(bodyData) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := client.httpClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer resp.Body.Close()

		limitedReader := io.LimitReader(resp.Body, ChatMaxBytes)
		b, _ := io.ReadAll(limitedReader)
		return resp.StatusCode, string(b), nil
	}

	// 1. 指定自定义路径
	if probePath != "" {
		p := probePath
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		code, respBody, err := doReq("GET", bURL+p, nil)
		if err != nil {
			return classifyError(err, "custom_path", elapsedMs(), key)
		}
		return classifyStatus(code, respBody, "custom_path", elapsedMs(), key, bURL+p)
	}

	// 2. 携带模型名时，默认用 POST /chat/completions 验证最小推理
	if mName != "" {
		reqBody, _ := json.Marshal(map[string]any{
			"model": mName,
			"messages": []map[string]string{
				{"role": "user", "content": "ping"},
			},
			"max_tokens": 1,
		})
		chatURL := bURL + "/chat/completions"
		code, respBody, err := doReq("POST", chatURL, reqBody)
		if err == nil {
			if code == 200 {
				return &ProbeResult{
					OK:        true,
					Stage:     "chat",
					Category:  CatOK,
					Message:   fmt.Sprintf("连通成功（模型 %s 鉴权正常）", mName),
					Status:    &code,
					Detail:    clip(Redact(respBody, key), MaxDetailChars),
					LatencyMs: elapsedMs(),
				}
			}
			if code != 404 && code != 405 {
				return classifyStatus(code, respBody, "chat", elapsedMs(), key, chatURL)
			}
		}
		// 404/405 回退尝试 GET /models
		modelsURL := bURL + "/models"
		codeM, respM, errM := doReq("GET", modelsURL, nil)
		if errM != nil {
			return classifyError(errM, "models", elapsedMs(), key)
		}
		return classifyStatus(codeM, respM, "models", elapsedMs(), key, modelsURL)
	}

	// 3. 未填模型名，尝试 GET /models
	modelsURL := bURL + "/models"
	code, respBody, err := doReq("GET", modelsURL, nil)
	if err == nil {
		if code == 200 {
			return &ProbeResult{
				OK:        true,
				Stage:     "models",
				Category:  CatOK,
				Message:   "连通成功（GET /models 鉴权正常）",
				Status:    &code,
				Detail:    clip(Redact(respBody, key), MaxDetailChars),
				LatencyMs: elapsedMs(),
			}
		}
		if code != 404 && code != 405 {
			return classifyStatus(code, respBody, "models", elapsedMs(), key, modelsURL)
		}
	}

	// 回退 POST /chat/completions 占位模型
	reqBody, _ := json.Marshal(map[string]any{
		"model": PlaceholderModel,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
		"max_tokens": 1,
	})
	chatURL := bURL + "/chat/completions"
	codeC, respC, errC := doReq("POST", chatURL, reqBody)
	if errC != nil {
		return classifyError(errC, "chat", elapsedMs(), key)
	}
	return classifyStatus(codeC, respC, "chat", elapsedMs(), key, chatURL)
}

func classifyStatus(code int, body, stage string, latency int, key, urlStr string) *ProbeResult {
	redacted := clip(Redact(body, key), MaxDetailChars)
	statusVal := code

	if code == 200 {
		return &ProbeResult{
			OK:        true,
			Stage:     stage,
			Category:  CatOK,
			Message:   "连通成功，鉴权正常",
			Status:    &statusVal,
			Detail:    redacted,
			LatencyMs: latency,
		}
	}

	cat := CatServerError
	msg := fmt.Sprintf("供应商返回 HTTP %d", code)

	if code == 401 || code == 403 {
		cat = CatAuth
		msg = "鉴权失败：API Key 无效或无权访问该模型"
	} else if code == 404 {
		cat = CatNotFound
		msg = fmt.Sprintf("端点未找到 (404)：请核对地址路径是否正确（%s）", urlStr)
	} else if code == 429 {
		cat = CatAuth
		msg = "请求过于频繁或额度已耗尽 (429)"
	}

	return &ProbeResult{
		OK:        false,
		Stage:     stage,
		Category:  cat,
		Message:   msg,
		Status:    &statusVal,
		Detail:    redacted,
		LatencyMs: latency,
	}
}

func classifyError(err error, stage string, latency int, key string) *ProbeResult {
	errStr := Redact(err.Error(), key)
	cat := CatNetwork
	msg := fmt.Sprintf("网络连接异常: %s", errStr)

	if strings.Contains(strings.ToLower(errStr), "timeout") {
		cat = CatTimeout
		msg = "请求超时：无法在规定时间内连通供应商地址"
	} else if strings.Contains(strings.ToLower(errStr), "certificate") || strings.Contains(strings.ToLower(errStr), "tls") {
		cat = CatTLS
		msg = "TLS/SSL 证书校验异常"
	}

	return &ProbeResult{
		OK:        false,
		Stage:     stage,
		Category:  cat,
		Message:   msg,
		Detail:    clip(errStr, MaxDetailChars),
		LatencyMs: latency,
	}
}

// ChatResult 对话结果
type ChatResult struct {
	OK        bool                   `json:"ok"`
	Content   string                 `json:"content"`
	Error     string                 `json:"error"`
	Status    *int                   `json:"status"`
	Stage     string                 `json:"stage"`
	Usage     map[string]interface{} `json:"usage"`
	LatencyMs int                    `json:"latency_ms"`
}

// Chat 完整对话调用，兼容 Python core.llm_client.chat
func Chat(baseURL, apiKey, model string, messages []map[string]string, timeoutS float64, temperature float64, jsonMode bool, maxTokens int) ChatResult {
	bURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if timeoutS <= 0 {
		timeoutS = 60.0
	}
	t0 := time.Now()
	elapsedMs := func() int {
		return int(time.Since(t0).Milliseconds())
	}

	// ⚠️ 原实现**完全没有**这两个前置校验（Python 有，见 core/llm_client.py:406-414）。
	// 缺了它们，把「模型名」一起粘进 Key 时会白跑一次网络请求，
	// 换回一句与真实原因无关的 401。原文逐字保留。
	key := pyre.Strip(apiKey)
	if key == "" {
		return ChatResult{OK: false, Error: "未配置 API Key", Stage: "preflight",
			LatencyMs: elapsedMs()}
	}
	if pyre.ContainsSpace(key) {
		return ChatResult{OK: false, Error: "API Key 含空格或换行，请检查是否粘贴了多余内容",
			Stage: "preflight", LatencyMs: elapsedMs()}
	}

	client := NewLLMClient(time.Duration(timeoutS * float64(time.Second)))

	reqPayload := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": temperature,
		"stream":      false,
	}
	if maxTokens > 0 {
		reqPayload["max_tokens"] = maxTokens
	}
	if jsonMode {
		reqPayload["response_format"] = map[string]string{"type": "json_object"}
	}

	bodyData, err := json.Marshal(reqPayload)
	if err != nil {
		return ChatResult{OK: false, Error: err.Error(), Stage: "preflight", LatencyMs: elapsedMs()}
	}

	req, err := http.NewRequest("POST", bURL+"/chat/completions", bytes.NewReader(bodyData))
	if err != nil {
		return ChatResult{OK: false, Error: err.Error(), Stage: "preflight", LatencyMs: elapsedMs()}
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		return ChatResult{OK: false, Error: Redact(err.Error(), apiKey), Stage: "network", LatencyMs: elapsedMs()}
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, ChatMaxBytes))
	if err != nil {
		return ChatResult{OK: false, Error: err.Error(), Stage: "network", LatencyMs: elapsedMs()}
	}

	statusCode := resp.StatusCode
	if statusCode == 400 || statusCode == 422 {
		if jsonMode {
			// 重试去掉 response_format
			delete(reqPayload, "response_format")
			retryBody, _ := json.Marshal(reqPayload)
			retryReq, _ := http.NewRequest("POST", bURL+"/chat/completions", bytes.NewReader(retryBody))
			retryReq.Header.Set("User-Agent", UserAgent)
			retryReq.Header.Set("Authorization", "Bearer "+apiKey)
			retryReq.Header.Set("Content-Type", "application/json")
			if retryResp, err := client.httpClient.Do(retryReq); err == nil {
				defer retryResp.Body.Close()
				statusCode = retryResp.StatusCode
				bodyBytes, _ = io.ReadAll(io.LimitReader(retryResp.Body, ChatMaxBytes))
			}
		}
	}

	if statusCode < 200 || statusCode >= 300 {
		return ChatResult{
			OK:        false,
			Error:     fmt.Sprintf("模型调用失败（HTTP %d）：%s", statusCode, clip(Redact(string(bodyBytes), apiKey), MaxDetailChars)),
			Status:    &statusCode,
			Stage:     "http_error",
			LatencyMs: elapsedMs(),
		}
	}

	var resObj map[string]any
	if err := json.Unmarshal(bodyBytes, &resObj); err != nil {
		return ChatResult{
			OK:        false,
			Error:     "响应不是合法 JSON",
			Status:    &statusCode,
			Stage:     "parse",
			LatencyMs: elapsedMs(),
		}
	}

	choices, _ := resObj["choices"].([]any)
	if len(choices) == 0 {
		return ChatResult{
			OK:        false,
			Error:     "响应缺少 choices",
			Status:    &statusCode,
			Stage:     "parse",
			LatencyMs: elapsedMs(),
		}
	}
	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return ChatResult{
			OK:        false,
			Error:     "choice 格式异常",
			Status:    &statusCode,
			Stage:     "parse",
			LatencyMs: elapsedMs(),
		}
	}
	msg, ok := firstChoice["message"].(map[string]any)
	if !ok {
		return ChatResult{
			OK:        false,
			Error:     "message 格式异常",
			Status:    &statusCode,
			Stage:     "parse",
			LatencyMs: elapsedMs(),
		}
	}
	content := fmt.Sprintf("%v", msg["content"])

	usageMap := make(map[string]interface{})
	if u, ok := resObj["usage"].(map[string]any); ok {
		for k, v := range u {
			usageMap[k] = v
		}
	}

	return ChatResult{
		OK:        true,
		Content:   content,
		Status:    &statusCode,
		Stage:     "chat",
		Usage:     usageMap,
		LatencyMs: elapsedMs(),
	}
}

// ChatCompletion 执行对话完成请求
func ChatCompletion(baseURL, apiKey, model string, messages []map[string]any, temperature float64, maxTokens int, timeoutS float64) (string, error) {
	var strMsgs []map[string]string
	for _, m := range messages {
		sm := make(map[string]string)
		for k, v := range m {
			sm[k] = fmt.Sprintf("%v", v)
		}
		strMsgs = append(strMsgs, sm)
	}
	res := Chat(baseURL, apiKey, model, strMsgs, timeoutS, temperature, false, maxTokens)
	if !res.OK {
		return "", fmt.Errorf("%s", res.Error)
	}
	return res.Content, nil
}

// ------------------------------------------------ 密钥文件管理

type SecretsData struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	SavedAt string `json:"saved_at,omitempty"`
}

// LoadConfig 读取存储的密钥
func LoadConfig() SecretsData {
	sp := config.SecretsPath()
	data, err := os.ReadFile(sp)
	if err != nil {
		return SecretsData{}
	}
	var s SecretsData
	_ = json.Unmarshal(data, &s)
	return s
}

// SaveConfig 保存模型密钥
func SaveConfig(baseURL, apiKey, model string) error {
	sp := config.SecretsPath()
	s := SecretsData{
		BaseURL: strings.TrimSpace(baseURL),
		APIKey:  strings.TrimSpace(apiKey),
		Model:   strings.TrimSpace(model),
		SavedAt: time.Now().Format("2006-01-02 15:04:05"),
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(sp, data, 0600); err != nil {
		return err
	}
	_ = os.Chmod(sp, 0600)
	return nil
}

// ClearConfig 清空密钥
func ClearConfig() {
	_ = os.Remove(config.SecretsPath())
}

// PublicConfig 返回脱敏公开配置
func PublicConfig() map[string]any {
	cfg := LoadConfig()
	configured := cfg.BaseURL != "" && cfg.APIKey != ""
	keyMask := ""
	if len(cfg.APIKey) > 8 {
		keyMask = cfg.APIKey[:3] + "..." + cfg.APIKey[len(cfg.APIKey)-4:]
	} else if len(cfg.APIKey) > 0 {
		keyMask = "***"
	}
	return map[string]any{
		"configured": configured,
		"base_url":   cfg.BaseURL,
		"model":      cfg.Model,
		"key_mask":   keyMask,
	}
}
