// Package stcheck performs an offline pre-check of Omron Sysmac Structured
// Text: syntax, symbol resolution (variables, struct members, FB / function
// parameters) and a few Sysmac-specific rules. It does not replace the Sysmac
// Studio build; it catches the common mistakes before code is written.
package stcheck

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tKeyword
	tInt
	tReal
	tString
	tTyped // typed literal: INT#5, T#1s, E_Mode#Auto
	tOp
)

type token struct {
	kind tokKind
	text string // original text
	up   string // upper-case text (identifiers / keywords / ops)
	line int
	col  int
	// typed literal parts
	prefix string // part before '#'
	value  string // part after '#'
}

func (t token) String() string {
	if t.kind == tEOF {
		return "文件结尾"
	}
	return "'" + t.text + "'"
}

var keywords = map[string]bool{
	"IF": true, "THEN": true, "ELSIF": true, "ELSE": true, "END_IF": true,
	"CASE": true, "OF": true, "END_CASE": true,
	"FOR": true, "TO": true, "BY": true, "DO": true, "END_FOR": true,
	"WHILE": true, "END_WHILE": true, "REPEAT": true, "UNTIL": true, "END_REPEAT": true,
	"EXIT": true, "RETURN": true,
	"AND": true, "OR": true, "XOR": true, "NOT": true, "MOD": true,
	"TRUE": true, "FALSE": true,
}

// Prefixes of typed literals whose value part is a duration / date text.
var timePrefixes = map[string]bool{
	"T": true, "TIME": true, "LT": true, "LTIME": true,
	"D": true, "DATE": true, "TOD": true, "TIME_OF_DAY": true,
	"DT": true, "DATE_AND_TIME": true,
}

type lexError struct {
	line, col int
	msg       string
}

type lexer struct {
	src    []rune
	pos    int
	line   int
	col    int
	tokens []token
	errs   []lexError
	warns  []lexError
}

func lex(src string) ([]token, []lexError, []lexError) {
	l := &lexer{src: []rune(strings.TrimPrefix(src, "\uFEFF")), line: 1, col: 1}
	l.run()
	return l.tokens, l.errs, l.warns
}

func (l *lexer) peek(o int) rune {
	if l.pos+o < len(l.src) {
		return l.src[l.pos+o]
	}
	return 0
}

func (l *lexer) adv() rune {
	r := l.src[l.pos]
	l.pos++
	if r == '\n' {
		l.line++
		l.col = 1
	} else if r != '\r' {
		l.col++
	}
	return r
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (l *lexer) emit(kind tokKind, text string, line, col int) {
	l.tokens = append(l.tokens, token{kind: kind, text: text, up: strings.ToUpper(text), line: line, col: col})
}

func (l *lexer) run() {
	for l.pos < len(l.src) {
		r := l.peek(0)
		line, col := l.line, l.col
		switch {
		case r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\f' || r == '\u3000':
			if r == '\u3000' {
				l.warns = append(l.warns, lexError{line, col, "全角空格，Sysmac 可能不接受，建议换成半角空格"})
			}
			l.adv()
		case r == '(' && l.peek(1) == '*':
			l.blockComment("(*", "*)", line, col)
		case r == '/' && l.peek(1) == '*':
			l.warns = append(l.warns, lexError{line, col, "使用了 /* */ 注释，建议改用 (* *) 或 //"})
			l.blockComment("/*", "*/", line, col)
		case r == '/' && l.peek(1) == '/':
			for l.pos < len(l.src) && l.peek(0) != '\n' {
				l.adv()
			}
		case r == '\'' || r == '"':
			l.str(r, line, col)
		case unicode.IsDigit(r):
			l.number(line, col)
		case isIdentStart(r) || r == '\\':
			l.ident(line, col)
		default:
			l.op(line, col)
		}
	}
	l.tokens = append(l.tokens, token{kind: tEOF, line: l.line, col: l.col})
}

func (l *lexer) blockComment(open, close string, line, col int) {
	l.adv()
	l.adv()
	depth := 1
	for l.pos < len(l.src) {
		if open == "(*" && l.peek(0) == '(' && l.peek(1) == '*' {
			// Sysmac does not nest comments; just keep scanning for the close.
		}
		if string([]rune{l.peek(0), l.peek(1)}) == close {
			l.adv()
			l.adv()
			depth--
			if depth == 0 {
				return
			}
			continue
		}
		l.adv()
	}
	l.errs = append(l.errs, lexError{line, col, "注释 " + open + " 没有结束符 " + close})
}

func (l *lexer) str(q rune, line, col int) {
	l.adv()
	var b strings.Builder
	b.WriteRune(q)
	for l.pos < len(l.src) {
		r := l.adv()
		b.WriteRune(r)
		if r == '$' && l.pos < len(l.src) {
			b.WriteRune(l.adv())
			continue
		}
		if r == q {
			if q == '"' {
				l.warns = append(l.warns, lexError{line, col, "双引号字符串：Sysmac 字符串用单引号 '...'"})
			}
			l.emit(tString, b.String(), line, col)
			return
		}
		if r == '\n' {
			break
		}
	}
	l.errs = append(l.errs, lexError{line, col, "字符串没有结束引号"})
	l.emit(tString, b.String(), line, col)
}

func (l *lexer) digits(allowHex bool) string {
	var b strings.Builder
	for l.pos < len(l.src) {
		r := l.peek(0)
		if unicode.IsDigit(r) || r == '_' || (allowHex && strings.ContainsRune("abcdefABCDEF", r)) {
			b.WriteRune(l.adv())
			continue
		}
		break
	}
	return b.String()
}

func (l *lexer) number(line, col int) {
	start := l.pos
	l.digits(false)
	// based number: 16#FF, 2#1010, 8#17
	if l.peek(0) == '#' {
		base := string(l.src[start:l.pos])
		l.adv()
		digs := l.digits(true)
		if digs == "" {
			l.errs = append(l.errs, lexError{line, col, "进制数 " + base + "# 后面缺少数字"})
		}
		switch base {
		case "2", "8", "16":
		default:
			l.errs = append(l.errs, lexError{line, col, "不支持的进制 " + base + "#（只能 2#、8#、16#）"})
		}
		l.emit(tInt, string(l.src[start:l.pos]), line, col)
		return
	}
	kind := tInt
	if l.peek(0) == '.' && unicode.IsDigit(l.peek(1)) {
		kind = tReal
		l.adv()
		l.digits(false)
	}
	if (l.peek(0) == 'e' || l.peek(0) == 'E') && (unicode.IsDigit(l.peek(1)) || ((l.peek(1) == '+' || l.peek(1) == '-') && unicode.IsDigit(l.peek(2)))) {
		kind = tReal
		l.adv()
		if l.peek(0) == '+' || l.peek(0) == '-' {
			l.adv()
		}
		l.digits(false)
	}
	text := string(l.src[start:l.pos])
	if isIdentStart(l.peek(0)) {
		l.errs = append(l.errs, lexError{line, col, "数字后面直接跟了字母：'" + text + string(l.peek(0)) + "'（变量名不能以数字开头）"})
	}
	l.emit(kind, text, line, col)
}

func (l *lexer) ident(line, col int) {
	start := l.pos
	// namespace-qualified names: \\NX\ABS, Lib\FC_X
	for l.pos < len(l.src) {
		r := l.peek(0)
		if isIdentPart(r) || (r == '\\' && (isIdentStart(l.peek(1)) || l.peek(1) == '\\')) {
			l.adv()
			continue
		}
		break
	}
	text := string(l.src[start:l.pos])
	up := strings.ToUpper(text)
	if l.peek(0) == '#' && !strings.Contains(text, "\\") {
		// typed literal
		l.adv()
		vstart := l.pos
		switch {
		case timePrefixes[up]:
			for l.pos < len(l.src) {
				r := l.peek(0)
				if isIdentPart(r) || r == '.' || r == ':' || r == '-' || r == '+' {
					// stop at '-' or '+' that is not part of a date/time value: only when
					// preceded by a digit or letter, those are part of the literal
					l.adv()
					continue
				}
				break
			}
		default:
			if l.peek(0) == '-' || l.peek(0) == '+' {
				l.adv()
			}
			if unicode.IsDigit(l.peek(0)) {
				l.digits(false)
				if l.peek(0) == '#' {
					l.adv()
					l.digits(true)
				} else {
					if l.peek(0) == '.' && unicode.IsDigit(l.peek(1)) {
						l.adv()
						l.digits(false)
					}
					if (l.peek(0) == 'e' || l.peek(0) == 'E') && (unicode.IsDigit(l.peek(1)) || l.peek(1) == '-' || l.peek(1) == '+') {
						l.adv()
						if l.peek(0) == '-' || l.peek(0) == '+' {
							l.adv()
						}
						l.digits(false)
					}
				}
			} else {
				for l.pos < len(l.src) && isIdentPart(l.peek(0)) {
					l.adv()
				}
			}
		}
		value := string(l.src[vstart:l.pos])
		if value == "" {
			l.errs = append(l.errs, lexError{line, col, "'" + text + "#' 后面缺少值"})
		}
		l.tokens = append(l.tokens, token{kind: tTyped, text: text + "#" + value, up: strings.ToUpper(text + "#" + value), line: line, col: col, prefix: up, value: value})
		return
	}
	if keywords[up] {
		l.emit(tKeyword, text, line, col)
		return
	}
	l.emit(tIdent, text, line, col)
}

var ops3 = []string{}
var ops2 = []string{":=", "=>", "<>", "<=", ">=", "**", ".."}
var ops1 = "+-*/=<>&()[],;:.#^"

func (l *lexer) op(line, col int) {
	for _, op := range ops2 {
		if l.pos+1 < len(l.src) && string(l.src[l.pos:l.pos+2]) == op {
			l.adv()
			l.adv()
			l.emit(tOp, op, line, col)
			return
		}
	}
	r := l.adv()
	if strings.ContainsRune(ops1, r) {
		if r == '^' {
			l.errs = append(l.errs, lexError{line, col, "Sysmac ST 不支持指针运算符 ^"})
		}
		l.emit(tOp, string(r), line, col)
		return
	}
	name := fmt.Sprintf("%q", r)
	if r >= 0xFF00 && r <= 0xFFEF || r == '：' || r == '；' || r == '，' || r == '（' || r == '）' {
		l.errs = append(l.errs, lexError{line, col, "全角符号 " + name + "，请改成半角"})
	} else if !utf8.ValidRune(r) || unicode.IsPrint(r) {
		l.errs = append(l.errs, lexError{line, col, "无法识别的字符 " + name})
	} else {
		l.errs = append(l.errs, lexError{line, col, "不可见的特殊字符 " + name})
	}
}
