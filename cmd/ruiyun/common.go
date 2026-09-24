package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"ruiyun-ui-test-platform-go/internal/config"
)

// loadEffectiveCfg 读 config.yaml 并做三层合并（.app_settings.json > config.yaml > 内置默认）。
// 对应 Python 的 `yaml.safe_load(config_path().read_text())` + `effective_config(cfg)`。
// cfgPath 为空时使用默认配置路径。
func loadEffectiveCfg(cfgPath string) map[string]any {
	var raw map[string]any
	if cfgPath == "" {
		raw, _ = config.LoadConfigDict()
	} else {
		raw = readYAMLMap(cfgPath)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return config.EffectiveConfig(raw)
}

// readYAMLMap 读一个 YAML 文件为 map（失败返回 nil）
func readYAMLMap(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// pyVal 复刻 Python 打印用的值渲染（nil → "None"）
func pyVal(v any) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprintf("%v", v)
}

// pyReprStr 复刻 Python 的 {x!r}：None 无引号，字符串加引号
func pyReprStr(v any) string {
	if v == nil {
		return "None"
	}
	if s, ok := v.(string); ok {
		return pyReprQuote(s)
	}
	return fmt.Sprintf("%v", v)
}

// pyReprQuote 复刻 Python repr(str)：单引号优先，含 ' 而无 " 时改用双引号
func pyReprQuote(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if byte(r) == quote && r < 128 {
				b.WriteByte('\\')
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// truncRunes 按码点截断（Python 的切片语义）
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// mapList 把 any 归一化成 []map[string]any
func mapList(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// unescapeHTMLish 复刻 Python json.dumps 不转义 < > & / U+2028 / U+2029 的行为
// （Go 的 encoding/json 默认会把它转成 \u003c 等）
func unescapeHTMLish(data []byte) []byte {
	s := string(data)
	s = strings.ReplaceAll(s, `\u003c`, "<")
	s = strings.ReplaceAll(s, `\u003e`, ">")
	s = strings.ReplaceAll(s, `\u0026`, "&")
	s = strings.ReplaceAll(s, `\u2028`, "\u2028")
	s = strings.ReplaceAll(s, `\u2029`, "\u2029")
	return []byte(s)
}
