package logparser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ParsePyLiteral 解析 Python repr 格式的字面量（兼容 dict、list、单/双引号字符串、True/False/None、数字）
func ParsePyLiteral(s string) (any, error) {
	p := &pyParser{src: []rune(strings.TrimSpace(s)), pos: 0}
	val, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	p.skipWhitespace()
	if p.pos < len(p.src) {
		return nil, fmt.Errorf("解析未完全消费，剩余: %s", string(p.src[p.pos:]))
	}
	return val, nil
}

type pyParser struct {
	src []rune
	pos int
}

func (p *pyParser) skipWhitespace() {
	for p.pos < len(p.src) && unicode.IsSpace(p.src[p.pos]) {
		p.pos++
	}
}

func (p *pyParser) peek() rune {
	p.skipWhitespace()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *pyParser) parseValue() (any, error) {
	p.skipWhitespace()
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("意外到达输入末尾")
	}

	ch := p.src[p.pos]
	switch ch {
	case '{':
		return p.parseDict()
	case '[':
		return p.parseList()
	case '(':
		return p.parseTuple()
	case '\'', '"':
		return p.parseString()
	case 'T':
		if p.consumeLiteral("True") {
			return true, nil
		}
	case 'F':
		if p.consumeLiteral("False") {
			return false, nil
		}
	case 'N':
		if p.consumeLiteral("None") {
			return nil, nil
		}
	}

	if ch == '-' || ch == '+' || unicode.IsDigit(ch) {
		return p.parseNumber()
	}

	return nil, fmt.Errorf("无法识别的字面量起始: '%c' (pos %d)", ch, p.pos)
}

func (p *pyParser) consumeLiteral(lit string) bool {
	runes := []rune(lit)
	if p.pos+len(runes) <= len(p.src) {
		for i, r := range runes {
			if p.src[p.pos+i] != r {
				return false
			}
		}
		p.pos += len(runes)
		return true
	}
	return false
}

func (p *pyParser) parseDict() (map[string]any, error) {
	p.pos++ // 跳过 '{'
	res := make(map[string]any)

	for {
		p.skipWhitespace()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("dict 未闭合")
		}
		if p.src[p.pos] == '}' {
			p.pos++
			return res, nil
		}

		keyVal, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		keyStr := fmt.Sprintf("%v", keyVal)

		p.skipWhitespace()
		if p.pos >= len(p.src) || p.src[p.pos] != ':' {
			return nil, fmt.Errorf("dict 缺少 ':' (pos %d)", p.pos)
		}
		p.pos++ // 跳过 ':'

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		res[keyStr] = val

		p.skipWhitespace()
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			p.pos++
		} else if p.pos < len(p.src) && p.src[p.pos] == '}' {
			p.pos++
			return res, nil
		} else {
			return nil, fmt.Errorf("dict 语法错误，期望 ',' 或 '}' (pos %d)", p.pos)
		}
	}
}

func (p *pyParser) parseList() ([]any, error) {
	p.pos++ // 跳过 '['
	var res []any

	for {
		p.skipWhitespace()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("list 未闭合")
		}
		if p.src[p.pos] == ']' {
			p.pos++
			return res, nil
		}

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		res = append(res, val)

		p.skipWhitespace()
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			p.pos++
		} else if p.pos < len(p.src) && p.src[p.pos] == ']' {
			p.pos++
			return res, nil
		} else {
			return nil, fmt.Errorf("list 语法错误，期望 ',' 或 ']' (pos %d)", p.pos)
		}
	}
}

func (p *pyParser) parseTuple() ([]any, error) {
	p.pos++ // 跳过 '('
	var res []any

	for {
		p.skipWhitespace()
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("tuple 未闭合")
		}
		if p.src[p.pos] == ')' {
			p.pos++
			return res, nil
		}

		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		res = append(res, val)

		p.skipWhitespace()
		if p.pos < len(p.src) && p.src[p.pos] == ',' {
			p.pos++
		} else if p.pos < len(p.src) && p.src[p.pos] == ')' {
			p.pos++
			return res, nil
		} else {
			return nil, fmt.Errorf("tuple 语法错误，期望 ',' 或 ')' (pos %d)", p.pos)
		}
	}
}

func (p *pyParser) parseString() (string, error) {
	quote := p.src[p.pos]
	p.pos++ // 跳过起始引号

	var sb strings.Builder
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '\\' {
			p.pos++
			if p.pos >= len(p.src) {
				return "", fmt.Errorf("字符串转义符未结束")
			}
			esc := p.src[p.pos]
			switch esc {
			case 'n':
				sb.WriteRune('\n')
			case 'r':
				sb.WriteRune('\r')
			case 't':
				sb.WriteRune('\t')
			case '\\':
				sb.WriteRune('\\')
			case '\'':
				sb.WriteRune('\'')
			case '"':
				sb.WriteRune('"')
			default:
				sb.WriteRune('\\')
				sb.WriteRune(esc)
			}
			p.pos++
			continue
		}
		if ch == quote {
			p.pos++ // 跳过结束引号
			return sb.String(), nil
		}
		sb.WriteRune(ch)
		p.pos++
	}
	return "", fmt.Errorf("字符串未闭合")
}

func (p *pyParser) parseNumber() (any, error) {
	start := p.pos
	hasDot := false

	if p.src[p.pos] == '-' || p.src[p.pos] == '+' {
		p.pos++
	}

	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if unicode.IsDigit(ch) {
			p.pos++
		} else if ch == '.' && !hasDot {
			hasDot = true
			p.pos++
		} else if ch == 'e' || ch == 'E' {
			p.pos++
			if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
				p.pos++
			}
		} else {
			break
		}
	}

	numStr := string(p.src[start:p.pos])
	if hasDot || strings.ContainsAny(numStr, "eE") {
		f, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	n, err := strconv.Atoi(numStr)
	if err == nil {
		return n, nil
	}
	n64, err := strconv.ParseInt(numStr, 10, 64)
	if err == nil {
		return n64, nil
	}
	return strconv.ParseFloat(numStr, 64)
}
