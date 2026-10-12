package notify

import (
	"os"
	"strings"
	"testing"

	"ruiyun-ui-test-platform-go/internal/config"
)

func TestLoadRobotsFallsBackToBuiltin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	robots, builtin := LoadRobots()
	if !builtin {
		t.Fatal("没有配置文件时应回落内置")
	}
	if len(robots) != 2 {
		t.Fatalf("内置应有 2 个群，实际 %d", len(robots))
	}
	if r, ok := Lookup("official"); !ok || r.Name != "正式群" || r.Token == "" {
		t.Fatalf("内置 official 查不到或字段缺失：%+v ok=%v", r, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("未知 key 不该查得到")
	}
}

func TestLoadRobotsBadJSONFallsBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := os.WriteFile(config.NotifySecretsPath(), []byte("{不是合法 json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, builtin := LoadRobots(); !builtin {
		t.Fatal("配置损坏时应回落内置，而不是把通知功能弄挂")
	}
}

func TestSaveAndLoadCustomRobots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// 空 token 的条目发不出去，保存时就该丢掉
	if err := SaveRobots([]RobotEntry{
		{Key: "k1", Name: "客服群", Token: "tok-1", Secret: "sec-1"},
		{Key: "k2", Name: "空 token", Token: "   "},
	}); err != nil {
		t.Fatalf("SaveRobots 失败：%v", err)
	}

	robots, builtin := LoadRobots()
	if builtin {
		t.Fatal("已保存自定义配置，不该回落内置")
	}
	if len(robots) != 1 || robots[0].Key != "k1" || robots[0].Name != "客服群" {
		t.Fatalf("自定义群读取不符：%+v", robots)
	}
	// 配了自定义就不再回落内置：否则会冒出用户没配过的群
	if _, ok := Lookup("official"); ok {
		t.Fatal("配了自定义后不应再回落内置")
	}
	if r, ok := Lookup("k1"); !ok || r.Token != "tok-1" || r.Secret != "sec-1" {
		t.Fatalf("自定义 k1 查不到：%+v ok=%v", r, ok)
	}
	if got := GroupLabel("k1"); got != "客服群" {
		t.Errorf("GroupLabel 应取配置里的展示名，实际 %q", got)
	}
	if got := GroupLabel("nope"); got != "nope" {
		t.Errorf("未知 key 应原样返回，实际 %q", got)
	}

	// 文件里是明文 token，权限必须是 0600
	st, err := os.Stat(config.NotifySecretsPath())
	if err != nil {
		t.Fatalf("配置文件不存在：%v", err)
	}
	if perm := st.Mode().Perm(); perm != 0600 {
		t.Errorf("配置文件权限应为 0600，实际 %o", perm)
	}
}

func TestClearRobotsRestoresBuiltin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := SaveRobots([]RobotEntry{{Key: "k1", Token: "t"}}); err != nil {
		t.Fatal(err)
	}
	if _, builtin := LoadRobots(); builtin {
		t.Fatal("保存后不该是内置")
	}

	ClearRobots()
	robots, builtin := LoadRobots()
	if !builtin || len(robots) != 2 {
		t.Fatalf("清空后应回落内置，实际 builtin=%v len=%d", builtin, len(robots))
	}
}

func TestPublicRobotsMasksSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := SaveRobots([]RobotEntry{
		{Key: "k1", Name: "群一", Token: "abcdefghijklmn", Secret: "SECxyz1234"},
	}); err != nil {
		t.Fatal(err)
	}

	pub := PublicRobots()
	if pub["builtin"] != false {
		t.Errorf("builtin 应为 false：%v", pub["builtin"])
	}
	list, _ := pub["robots"].([]map[string]any)
	if len(list) != 1 {
		t.Fatalf("应返回 1 个群：%+v", pub["robots"])
	}
	th, _ := list[0]["token_hint"].(string)
	if th == "" || strings.Contains(th, "abcdefghijklmn") {
		t.Errorf("列表里不能出现明文 token：%q", th)
	}
	if !strings.HasSuffix(th, "klmn") {
		t.Errorf("提示应露出末 4 位，实际 %q", th)
	}

	// 明文只能单独 reveal
	rev, ok := RevealRobot("k1")
	if !ok || rev["token"] != "abcdefghijklmn" || rev["secret"] != "SECxyz1234" {
		t.Fatalf("RevealRobot 不符：%+v ok=%v", rev, ok)
	}
	if _, ok := RevealRobot("nope"); ok {
		t.Error("未知 key 不该 reveal 成功")
	}
}

func TestSignURLWithoutSecret(t *testing.T) {
	// secret 留空 = 机器人用「自定义关键词」而非「加签」，此时不能带 timestamp/sign
	u := signURL(Robot{Token: "tok-only"}, 1700000000000)
	if !strings.Contains(u, "access_token=tok-only") {
		t.Fatalf("缺少 access_token：%s", u)
	}
	if strings.Contains(u, "sign=") || strings.Contains(u, "timestamp=") {
		t.Fatalf("无 secret 时不该带签名参数：%s", u)
	}
}
