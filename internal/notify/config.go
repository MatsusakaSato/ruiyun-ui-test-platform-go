package notify

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"ruiyun-ui-test-platform-go/internal/config"
)

// RobotEntry 一个钉钉群机器人。
//
// Key 是稳定标识（发送接口靠它找群），Name 只是展示名，两者解耦：
// 改展示名不会影响已在用的调用，删掉再建也不怕重名。
type RobotEntry struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Token  string `json:"token"`
	Secret string `json:"secret"`
}

// builtinRobots 内置兜底：由 send_msg_dingding.py 平移而来，界面上没有自定义配置时使用。
// 「正式群 / 测试群」两个 key 是历史约定，别改 —— 外部调用（含旧脚本）按 key 找群。
var builtinRobots = []RobotEntry{
	{Key: "official", Name: "正式群",
		Token:  "da06ba7eadce8271882a8c2f254deaf884876e5797151d57246f86ede81831b4",
		Secret: "SEC6edbcbb663ecf7649505f7b0048fce73ba4dd062ab5cb92610a1472db0335844"},
	{Key: "test", Name: "测试群",
		Token:  "8df7a1da92bd3931adb6db868e0e63a0f216fb61498ca9ba5dc14139b5166341",
		Secret: "SECb214460c10dfb43fb1bb2d9477195ad2b1698ba58dc96ab6768c8cdaa2a2233d"},
}

// ConfirmKey 发送前需要二次确认的群：「正式群」是外部真实群，误发代价大。
// 用户自建的群不弹确认，保持「测试群可直接发」的手感。
const ConfirmKey = "official"

// secretsFile .notify_secrets.json 的结构
type secretsFile struct {
	Robots  []RobotEntry `json:"robots"`
	SavedAt string       `json:"saved_at,omitempty"`
}

func builtinByKey(key string) (RobotEntry, bool) {
	for _, r := range builtinRobots {
		if r.Key == key {
			return r, true
		}
	}
	return RobotEntry{}, false
}

func trimEntry(r RobotEntry) RobotEntry {
	return RobotEntry{
		Key:    strings.TrimSpace(r.Key),
		Name:   strings.TrimSpace(r.Name),
		Token:  strings.TrimSpace(r.Token),
		Secret: strings.TrimSpace(r.Secret),
	}
}

// LoadRobots 读取自定义群列表；没有配置、配置损坏或全无有效条目时回落到内置默认。
// 第二个返回值为 true 表示当前用的是内置兜底（不是用户配的）。
func LoadRobots() ([]RobotEntry, bool) {
	data, err := os.ReadFile(config.NotifySecretsPath())
	if err != nil {
		return builtinRobots, true
	}
	var f secretsFile
	if json.Unmarshal(data, &f) != nil {
		return builtinRobots, true
	}
	out := make([]RobotEntry, 0, len(f.Robots))
	seen := make(map[string]bool)
	for _, raw := range f.Robots {
		e := trimEntry(raw)
		// token 为空的条目发不出去，直接丢弃；key 重复的只认第一条
		if e.Key == "" || e.Token == "" || seen[e.Key] {
			continue
		}
		seen[e.Key] = true
		if e.Name == "" {
			e.Name = e.Key
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return builtinRobots, true
	}
	return out, false
}

// SaveRobots 保存自定义群列表（0600，含 token，不进版本库）
func SaveRobots(robots []RobotEntry) error {
	sp := config.NotifySecretsPath()
	clean := make([]RobotEntry, 0, len(robots))
	for _, r := range robots {
		e := trimEntry(r)
		if e.Key == "" || e.Token == "" {
			continue
		}
		if e.Name == "" {
			e.Name = e.Key
		}
		clean = append(clean, e)
	}
	data, err := json.MarshalIndent(secretsFile{
		Robots:  clean,
		SavedAt: time.Now().Format("2006-01-02 15:04:05"),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(sp, data, 0600); err != nil {
		return err
	}
	_ = os.Chmod(sp, 0600)
	return nil
}

// ClearRobots 删除自定义配置，回落内置默认
func ClearRobots() {
	_ = os.Remove(config.NotifySecretsPath())
}

// Lookup 按 key 找群（自定义优先，其次内置兜底）。
// 内置也查是为了兼容「已清空配置但仍在用 official/test」的老调用。
func Lookup(key string) (RobotEntry, bool) {
	k := strings.TrimSpace(key)
	if k == "" {
		return RobotEntry{}, false
	}
	robots, builtin := LoadRobots()
	for _, r := range robots {
		if r.Key == k {
			return r, true
		}
	}
	if !builtin {
		return RobotEntry{}, false
	}
	return builtinByKey(k)
}

// maskHint 生成打码提示：只露末尾 4 位。
// token 有 64 位十六进制，露首尾反而更容易被拼出来，只给尾 4 位足够辨认是哪一个。
func maskHint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 4 {
		return strings.Repeat("•", len(r))
	}
	return "••••••" + string(r[len(r)-4:])
}

// PublicRobots 脱敏后的群列表，供配置面板回显。
// 明文 token / secret 只能由 RevealRobot 单独取，不随列表一起发。
func PublicRobots() map[string]any {
	robots, builtin := LoadRobots()
	list := make([]map[string]any, 0, len(robots))
	for _, r := range robots {
		list = append(list, map[string]any{
			"key":         r.Key,
			"name":        r.Name,
			"token_hint":  maskHint(r.Token),
			"secret_hint": maskHint(r.Secret),
			"has_secret":  r.Secret != "",
		})
	}
	return map[string]any{
		"ok":      true,
		"robots":  list,
		"builtin": builtin,
	}
}

// RevealRobot 取某个群的明文 token / secret（配置面板里的「显示」用）
func RevealRobot(key string) (map[string]any, bool) {
	r, ok := Lookup(key)
	if !ok {
		return nil, false
	}
	return map[string]any{
		"ok":     true,
		"key":    r.Key,
		"name":   r.Name,
		"token":  r.Token,
		"secret": r.Secret,
	}, true
}
