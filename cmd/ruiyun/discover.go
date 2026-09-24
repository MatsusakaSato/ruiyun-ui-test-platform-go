package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/driver"
)

// runDiscover 对应 Python `discover_ui.py`：
// 连接已开启调试端口的睿云智能工作台，导出 DOM 中可交互元素。
func runDiscover(args []string) int {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	launch := fs.Bool("launch", false, "应用未运行时自动以调试模式启动（已运行则复用）")
	settle := fs.Float64("settle", 4.0, "连接后等待渲染的秒数")
	closeApp := fs.Bool("close-app", false, "探查结束后关闭应用（默认保持运行，遵循平台常驻策略）")
	_ = fs.Parse(args)

	cfg := loadEffectiveCfg("")
	drv := driver.NewRuiyunUIDriver(cfg)

	if *launch {
		ok, how := drv.EnsureReady()
		if !ok {
			fmt.Println("[x] 应用启动或调试端口就绪失败")
			return 1
		}
		if how == "reused" {
			fmt.Println("[i] 复用已运行的应用")
		} else {
			fmt.Println("[i] 已启动应用")
		}
	} else {
		if !drv.Attach(30.0) {
			fmt.Println("[x] 无法连接渲染进程")
			return 1
		}
	}

	time.Sleep(time.Duration(*settle * float64(time.Second)))
	info := drv.Discover()

	fmt.Printf("URL   : %v\n", pyVal(info["url"]))
	fmt.Printf("TITLE : %v\n", pyVal(info["title"]))
	bodyText, _ := info["bodyText"].(string)
	fmt.Printf("BODY  : %d 字符\n", len([]rune(bodyText)))

	fmt.Println("\n--- 输入类元素 ---")
	for i, el := range mapList(info["inputs"]) {
		if i >= 12 {
			break
		}
		fmt.Printf("  <%v> visible=%v rect=%v\n", pyVal(el["tag"]), pyVal(el["visible"]), pyVal(el["rect"]))
		fmt.Printf("      placeholder=%s aria=%s\n", pyReprStr(el["placeholder"]), pyReprStr(el["ariaLabel"]))
		cls, _ := el["cls"].(string)
		fmt.Printf("      class=%s\n", truncRunes(cls, 90))
	}

	fmt.Println("\n--- 按钮类元素 ---")
	for i, el := range mapList(info["buttons"]) {
		if i >= 25 {
			break
		}
		if vis, ok := el["visible"].(bool); ok && !vis {
			continue
		}
		cls, _ := el["cls"].(string)
		fmt.Printf("  <%v> text=%s aria=%s rect=%v class=%s\n",
			pyVal(el["tag"]), pyReprStr(el["text"]), pyReprStr(el["ariaLabel"]),
			pyVal(el["rect"]), truncRunes(cls, 70))
	}

	fmt.Println("\n完整 JSON 已写入 artifacts/ui_dom.json")
	out := filepath.Join(config.RootDir, "artifacts")
	_ = os.MkdirAll(out, 0755)
	data, err := json.MarshalIndent(info, "", "  ")
	if err == nil {
		// Python 用 ensure_ascii=False；MarshalIndent 也不做 ASCII 转义，
		// 但会把 < > & 转义成 \u003c 等，这里换掉以保持一致
		data = unescapeHTMLish(data)
		_ = os.WriteFile(filepath.Join(out, "ui_dom.json"), data, 0644)
	}

	if *closeApp {
		drv.KillApp()
		fmt.Println("[i] 已按 --close-app 关闭应用")
	} else {
		drv.Detach()
		fmt.Println("[i] 已断开连接（应用保持运行）")
	}
	return 0
}
