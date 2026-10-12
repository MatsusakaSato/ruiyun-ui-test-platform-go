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
	"strings"
	"time"
)

const (
	apiHost = "https://oapi.dingtalk.com"
	apiPath = "/robot/send"
)

// Robot 一个钉钉自定义机器人（token / secret）。
// 具体取值来自配置：界面上配过就用配的，没配过回落到 config.go 里的 builtinRobots。
type Robot struct {
	Token  string
	Secret string
}

// GroupLabel 群的展示名（取自配置，见 config.go）；未知 key 原样返回，别编一个假名字
func GroupLabel(group string) string {
	if r, ok := Lookup(group); ok {
		return r.Name
	}
	return group
}

// endpointBase 允许单元测试替换为 httptest 服务器地址
var endpointBase = apiHost

// signURL 计算 webhook 地址。
// 有 secret 时按原脚本加签：hmac.new(secret, f'{timestamp}\n{secret}', sha256) → base64 → quote_plus；
// secret 留空表示该机器人用「自定义关键词」而不是「加签」，此时只带 access_token。
func signURL(rb Robot, tsMilli int64) string {
	if strings.TrimSpace(rb.Secret) == "" {
		return fmt.Sprintf("%s%s?access_token=%s", endpointBase, apiPath, rb.Token)
	}
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

// SendCustomRobotGroupMessageTo 按群发送。group 是配置里的 key：
// 默认有 "official"（正式群）与 "test"（测试群），界面上加了群就用新 key。
func SendCustomRobotGroupMessageTo(group, msg, phone string) (*SendResult, error) {
	entry, ok := Lookup(group)
	if !ok {
		return nil, fmt.Errorf("未知群组：%s（请先在发送通知页点齿轮添加）", strings.TrimSpace(group))
	}
	rb := Robot{Token: entry.Token, Secret: entry.Secret}

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
