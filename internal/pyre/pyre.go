// Package pyre 提供「Python re 语义」的兼容工具。
//
// 存在的唯一理由：**Python 的 `\s` 和 Go 的 `\s` 不是一回事**。
//
//	Python `re` 对 str 模式：\s == str.isspace() 集合，共 29 个字符
//	Go   `regexp`：        \s == [\t\n\f\r ]，只有 5 个字符
//
// Go 少掉的 24 个里，有几个在中文语料里**非常常见**：
//
//	U+3000 全角空格      —— 中文排版里到处都是
//	U+00A0 NBSP          —— 从网页/Word 复制来的文本常见
//	U+2003 EM SPACE      —— 排版空格
//	U+000B 垂直制表符     —— 真实会话里出现过
//	U+2028 行分隔符       —— 真实会话里出现过
//
// 实测真实生产语料（219 份会话）：
//
//	U+2003 ×64、U+3000 ×21、U+000B ×19、U+2028 ×1
//
// 凡是从 Python 移植过来、原文写了 `\s` 的正则，Go 侧都应该把 `\s`
// 换成 SpaceClass，否则会出现「Python 归一化掉了、Go 没归一化」的
// 静默行为偏差 —— 不报错，只是结果不同。
package pyre

import (
	"strings"
	"unicode"
)

// SpaceClass 是可直接嵌进 Go 正则**字符类**里的字符串，语义与 Python 的 `\s` 完全一致。
//
// 用法（注意：它本身不含方括号，要自己加）：
//
//	regexp.MustCompile(`[，,、；;：:/（）()\[\]【】和与及` + pyre.SpaceClass + `]+`)
//
// 顺序与 Python 的码点序一致，便于人工对照。
const SpaceClass = `\t\n\v\f\r\x1c-\x1f \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

// Strip 复刻 Python 的 `str.strip()`（不带参数）。
//
// ⚠️ **不是** `strings.TrimSpace`：Go 的 TrimSpace 走 `unicode.IsSpace`，
// 少掉 U+001C–U+001F 这四个 ASCII 分隔符，而 Python 的 `str.strip()` 会去掉它们。
// 实测差分：要求项 `"\x1c目录"` Python 给 `"目录"`、Go 给 `"\x1c目录"`。
//
// 凡是从 Python 移植过来、原文写了 `.strip()` 的地方，都应改用本函数。
func Strip(s string) string {
	return strings.TrimFunc(s, IsSpace)
}

// TrimLeft 复刻 Python 的 `str.lstrip()`。
func TrimLeft(s string) string {
	return strings.TrimLeftFunc(s, IsSpace)
}

// TrimRight 复刻 Python 的 `str.rstrip()`。
func TrimRight(s string) string {
	return strings.TrimRightFunc(s, IsSpace)
}

// CleanSpace 复刻 Python 的 `re.sub(r"\s+", "", s)` —— 删除**全部** Python 空白。
//
// 与 `Strip` 的区别：Strip 只去首尾，CleanSpace 去所有位置。
// 用于 prompt 归一化（去重键 / 预设索引键），凡 Python 写了
// `re.sub(r"\s+", "", ...)` 的地方都用它。
//
// ⚠️ 不要用 `strings.Map(unicode.IsSpace)` 或手写字符表代替：
// Go 的 `unicode.IsSpace` 少 U+001C–U+001F，而手写表几乎必然漏字符
// （历史上 `models.CleanWhitespace` 手写了 6 个，漏掉 23 个）。
func CleanSpace(s string) string {
	// 快路径：全是 ASCII 且无空白时直接返回，避免无谓分配
	if !strings.ContainsFunc(s, IsSpace) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ContainsSpace 复刻 Python 的 `re.search(r"\s", s)` —— 是否含任意 Python 空白。
//
// ⚠️ 不是 `strings.ContainsAny(s, " \t\n\r")`：那会漏掉 24 个字符。
// 用在 API Key 校验等处（core/llm_client.py:242,410）。
func ContainsSpace(s string) bool {
	return strings.ContainsFunc(s, IsSpace)
}

// IsSpace 判断单个码点是否落在 Python `\s` 的集合内。
//
// 推导：Python 的 `\s` 集合 == Go `unicode.IsSpace` 的集合 **加上** U+001C–U+001F
// （ASCII 文件/组分隔符/记录分隔符/单元分隔符）—— 这四个 Go 的 White_Space
// 属性不含，但 Python 的 str.isspace() 认。
//
// 供不便走正则的路径使用（例如逐字符归一化）。
func IsSpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}
