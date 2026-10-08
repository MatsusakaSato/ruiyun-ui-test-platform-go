package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignURLFormat(t *testing.T) {
	rb := robots["official"]
	u := signURL(rb, 1700000000000)
	for _, want := range []string{
		"access_token=" + rb.Token,
		"timestamp=1700000000000",
		"sign=",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("signURL 输出缺少 %q：%s", want, u)
		}
	}
	// 同一时刻戳签名必须确定（HMAC 无随机性）
	if signURL(rb, 42) != signURL(rb, 42) {
		t.Fatal("同一时间戳两次签名结果不一致")
	}
}

func TestUnknownGroup(t *testing.T) {
	if _, err := SendCustomRobotGroupMessageTo("nope", "x", ""); err == nil {
		t.Fatal("未知群组应返回错误")
	}
}

func TestSendTextWithStubServer(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	oldBase := endpointBase
	endpointBase = srv.URL
	defer func() { endpointBase = oldBase }()

	res, err := SendCustomRobotGroupMessage("hello 钉钉", "13800000000")
	if err != nil {
		t.Fatalf("SendCustomRobotGroupMessage 出错：%v", err)
	}
	if !res.OK() {
		t.Fatalf("应返回 errcode=0，得到 %+v", res)
	}
	if gotPath != "/robot/send" {
		t.Fatalf("请求路径错误：%s", gotPath)
	}
	if gotBody["msgtype"] != "text" {
		t.Fatalf("msgtype 应为 text：%v", gotBody["msgtype"])
	}
	text, _ := gotBody["text"].(map[string]any)
	if text == nil || text["content"] != "hello 钉钉" {
		t.Fatalf("text.content 错误：%v", gotBody["text"])
	}
	// 与原脚本一致：atMobiles 始终包含 phone（即使为空），isAtAll=false，atUserIds 为空数组
	at, _ := gotBody["at"].(map[string]any)
	if at == nil {
		t.Fatal("缺少 at 字段")
	}
	if at["isAtAll"] != false {
		t.Fatalf("isAtAll 应为 false：%v", at["isAtAll"])
	}
	mobiles, _ := at["atMobiles"].([]any)
	if len(mobiles) != 1 || mobiles[0] != "13800000000" {
		t.Fatalf("atMobiles 错误：%v", at["atMobiles"])
	}
	if _, ok := at["atUserIds"].([]any); !ok {
		t.Fatalf("atUserIds 应为数组：%v", at["atUserIds"])
	}
}

func TestSendBadJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	oldBase := endpointBase
	endpointBase = srv.URL
	defer func() { endpointBase = oldBase }()

	_, err := SendCustomRobotGroupMessage("x", "")
	if err == nil {
		t.Fatal("非 JSON 响应应返回错误")
	}
}
