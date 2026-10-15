package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"ruiyun-ui-test-platform-go/internal/config"
)

// 用临时 HOME 把附件库目录收敛到测试沙箱里，再验证 /api/open 的权限边界。
// 只覆盖「不真正拉起系统程序」的分支（缺参 / 越界 / 不存在），避免测试误开文件。
func TestHandleOpenFileGuards(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	uploads := config.UploadsDir() // 临时 HOME 下 = <tmp>/.ruiyun-autotest/uploads
	if err := os.MkdirAll(uploads, 0755); err != nil {
		t.Fatal(err)
	}
	// 库内一个真实文件（但本测试不会走到真正打开它的分支）
	inside := filepath.Join(uploads, "子目录", "合同.pdf")
	if err := os.MkdirAll(filepath.Dir(inside), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(tmp, "..", "etc", "passwd") // 绝对路径、但在附件库之外

	cases := []struct {
		name      string
		path      string
		denied    bool
		denyCode  int
	}{
		{"缺少 path 参数", "", true, http.StatusBadRequest},
		{"库外路径被拒", outside, true, http.StatusForbidden},
		{"库内但不存在被拒", filepath.Join(uploads, "不存在.pdf"), true, http.StatusNotFound},
		// 库内存在 → 通过所有关卡走到真正打开（此处不验 200，避免无 GUI 环境下 open 报错干扰）
		{"库内存在 → 放行", inside, false, 0},
	}
	s := &Server{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"path": c.path})
			req := httptest.NewRequest(http.MethodPost, "/api/open", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			s.handleOpenFile(rec, req)
			if c.denied {
				if rec.Code != c.denyCode {
					t.Errorf("期望被拒状态码 %d，实际 %d（%s）", c.denyCode, rec.Code, rec.Body.String())
				}
			} else {
				for _, deny := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
					if rec.Code == deny {
						t.Errorf("库内文件被错误地拒绝了（%d）：%s", rec.Code, rec.Body.String())
					}
				}
			}
		})
	}
}
