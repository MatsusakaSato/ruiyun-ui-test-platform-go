package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ruiyun-ui-test-platform-go/internal/config"
)

// ------------------------------------------------------------------ 开发模式
//
// 环境变量 RUYIYUN_DEV=1 时启用（scripts/dev.sh 会带上）：
//   - 静态资源改为**从磁盘读取** internal/server/web/，改 HTML/CSS/JS 不用重新编译
//   - 提供 SSE 端点 /api/dev/events，文件变动时通知浏览器刷新（css 单独热替换）
//
// 未设置该变量时，本文件所有逻辑都不生效，行为与生产完全一致（走 go:embed）。

const devEnvKey = "RUYIYUN_DEV"

// DevEnabled 是否处于开发模式
func DevEnabled() bool {
	v := strings.TrimSpace(os.Getenv(devEnvKey))
	return v == "1" || strings.EqualFold(v, "true")
}

// devWebDir 定位磁盘上的 web 目录；未启用或找不到时返回 false
func devWebDir() (string, bool) {
	if !DevEnabled() {
		return "", false
	}
	var cands []string
	if envDir := strings.TrimSpace(os.Getenv("RUYIYUN_WEB_DIR")); envDir != "" {
		cands = append(cands, envDir)
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "internal", "server", "web"))
	}
	if config.RootDir != "" {
		cands = append(cands, filepath.Join(config.RootDir, "internal", "server", "web"))
	}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "..", "..", "internal", "server", "web"))
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !st.IsDir() {
			return c, true
		}
	}
	return "", false
}

// webSnapshot 记录目录下每个文件的修改时间与大小，用于判断变动
func webSnapshot(dir string) map[string]string {
	out := make(map[string]string)
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if st, e := d.Info(); e == nil {
			out[p] = fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
		}
		return nil
	})
	return out
}

func snapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// diffKeys 返回发生新增 / 删除 / 内容变化的相对路径
func diffKeys(a, b map[string]string) []string {
	var keys []string
	for k, v := range b {
		if a[k] != v {
			keys = append(keys, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// handleDevEvents SSE：web 目录有变动时推送 "css" 或 "full"
func (s *Server) handleDevEvents(w http.ResponseWriter, r *http.Request) {
	if !DevEnabled() {
		writeText(w, 404, "not found")
		return
	}
	dir, ok := devWebDir()
	if !ok {
		writeText(w, 404, "dev web dir not found（设置 RUYIYUN_WEB_DIR 或从项目根目录启动）")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeText(w, 500, "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(": dev events connected\n\n"))
	flusher.Flush()

	prev := webSnapshot(dir)
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			cur := webSnapshot(dir)
			if snapEqual(prev, cur) {
				continue
			}
			kind := "full"
			cssOnly := true
			for _, k := range diffKeys(prev, cur) {
				if !strings.HasSuffix(strings.ToLower(k), ".css") {
					cssOnly = false
					break
				}
			}
			if cssOnly {
				kind = "css"
			}
			prev = cur
			_, _ = fmt.Fprintf(w, "data: %s\n\n", kind)
			flusher.Flush()
		}
	}
}

// devLiveReloadScript 注入到 index.html 的热更新脚本
const devLiveReloadScript = `
<!-- dev: 热更新（仅 RUYIYUN_DEV=1 时注入） -->
<script>
(function () {
  try {
    var es = new EventSource('/api/dev/events');
    es.onmessage = function (ev) {
      var kind = String(ev.data || 'full').trim();
      if (kind === 'css') {
        document.querySelectorAll('link[rel=stylesheet]').forEach(function (l) {
          l.href = l.href.split('?')[0] + '?t=' + Date.now();
        });
        console.log('[dev] CSS 已热更新');
      } else {
        console.log('[dev] 检测到文件变动，刷新页面');
        location.reload();
      }
    };
    es.onerror = function () { /* 断开后浏览器会自动重连 */ };
    console.log('[dev] 热更新已连接：改 web/ 下文件会即时生效');
  } catch (e) {}
})();
</script>
`

// injectDevLiveReload 把热更新脚本插到 </body> 前
func injectDevLiveReload(html []byte) []byte {
	s := string(html)
	idx := strings.LastIndex(s, "</body>")
	if idx < 0 {
		return append(html, []byte(devLiveReloadScript)...)
	}
	return []byte(s[:idx] + devLiveReloadScript + s[idx:])
}
