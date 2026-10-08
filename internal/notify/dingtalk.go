// Package notify 钉钉自定义机器人消息发送。
//
// 由 send_msg_dingding.py 平移而来，保持原实现的做法：
// 机器人 access_token / 加签 secret 直接明文写在代码里，不读配置文件。
package notify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	apiHost = "https://oapi.dingtalk.com"
	apiPath = "/robot/send"
)

// Robot 一个钉钉自定义机器人（token / secret 明文，沿用原脚本的做法，不读配置文件）
type Robot struct {
	Token  string
	Secret string
}

// robots 群组表。正式 = 原脚本「正式」组；test = 原脚本「测试用」组。
var robots = map[string]Robot{
	// 正式
	"official": {
		Token:  "da06ba7eadce8271882a8c2f254deaf884876e5797151d57246f86ede81831b4",
		Secret: "SEC6edbcbb663ecf7649505f7b0048fce73ba4dd062ab5cb92610a1472db0335844",
	},
	// 测试用
	"test": {
		Token:  "8df7a1da92bd3931adb6db868e0e63a0f216fb61498ca9ba5dc14139b5166341",
		Secret: "SECb214460c10dfb43fb1bb2d9477195ad2b1698ba58dc96ab6768c8cdaa2a2233d",
	},
}

// GroupLabel 群组的中文显示名（与前端下拉框文案一致）
func GroupLabel(group string) string {
	if group == "test" {
		return "测试群"
	}
	return "正式群"
}

// endpointBase 允许单元测试替换为 httptest 服务器地址
var endpointBase = apiHost

// signURL 计算加签后的 webhook 地址。
// 与原脚本一致：hmac.new(secret, f'{timestamp}\n{secret}', sha256) → base64 → quote_plus。
func signURL(rb Robot, tsMilli int64) string {
	stringToSign := fmt.Sprintf("%d\n%s", tsMilli, rb.Secret)
	mac := hmac.New(sha256.New, []byte(rb.Secret))
	mac.Write([]byte(stringToSign))
	sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return fmt.Sprintf("%s%s?access_token=%s&timestamp=%d&sign=%s",
		endpointBase, apiPath, rb.Token, tsMilli, sign)
}

// SendResult 钉钉机器人响应（errcode=0 表示成功），对应原脚本返回的 resp.json()
type SendResult struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// OK 是否发送成功
func (r *SendResult) OK() bool { return r != nil && r.ErrCode == 0 }

// Describe 人读的结果描述，用于日志（对应原脚本的 logging.info("钉钉消息响应：%s", resp.text)）
func (r *SendResult) Describe() string {
	if r == nil {
		return "无响应"
	}
	if r.OK() {
		return "成功"
	}
	return fmt.Sprintf("失败（errcode=%d %s）", r.ErrCode, r.ErrMsg)
}

// SendCustomRobotGroupMessage 直接平移自原脚本
// send_custom_robot_group_message(msg="hello world", phone="")：
// 发送文本消息到正式群，phone 非空时 @ 对应手机号（原实现始终把 phone 放进 atMobiles）。
func SendCustomRobotGroupMessage(msg, phone string) (*SendResult, error) {
	return SendCustomRobotGroupMessageTo("official", msg, phone)
}

// SendCustomRobotGroupMessageTo 按群组发送：group 取 "official"（正式群）或 "test"（测试群）。
func SendCustomRobotGroupMessageTo(group, msg, phone string) (*SendResult, error) {
	rb, ok := robots[group]
	if !ok {
		return nil, fmt.Errorf("未知群组：%s（只支持 official / test）", group)
	}

	body := map[string]any{
		"at": map[string]any{
			"isAtAll":   false,
			"atUserIds": []string{},
			"atMobiles": []string{phone},
		},
		"text": map[string]string{
			"content": msg,
		},
		"msgtype": "text",
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := signURL(rb, time.Now().UnixMilli())
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	var res SendResult
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, fmt.Errorf("响应不是合法 JSON（HTTP %d）：%s", resp.StatusCode, string(b))
	}
	return &res, nil
}
