package stcheck

import "fmt"

// ---------- AST ----------

type pos struct{ line, col int }

type expr interface{ position() pos }

type (
	identExpr struct {
		p    pos
		name string
	}
	literalExpr struct {
		p   pos
		tok token
	}
	memberExpr struct {
		p      pos
		x      expr
		member string
		bit    bool // .0 .. .63
	}
	indexExpr struct {
		p       pos
		x       expr
		indices []expr
	}
	callExpr struct {
		p    pos
		fn   expr
		args []callArg
	}
	unaryExpr struct {
		p  pos
		op string
		x  expr
	}
	binaryExpr struct {
		p    pos
		op   string
		l, r expr
	}
	badExpr struct{ p pos }
)

type callArg struct {
	p      pos
	name   string // formal name, "" for positional
	output bool   // name => var
	value  expr
}

func (e *identExpr) position() pos   { return e.p }
func (e *literalExpr) position() pos { return e.p }
func (e *memberExpr) position() pos  { return e.p }
func (e *indexExpr) position() pos   { return e.p }
func (e *callExpr) position() pos    { return e.p }
func (e *unaryExpr) position() pos   { return e.p }
func (e *binaryExpr) position() pos  { return e.p }
func (e *badExpr) position() pos     { return e.p }

type stmt interface{ position() pos }

type (
	assignStmt struct {
		p      pos
		target expr
		value  expr
	}
	callStmt struct {
		p    pos
		call *callExpr
	}
	ifStmt struct {
		p     pos
		conds []expr
		thens [][]stmt
		els   []stmt
	}
	caseElem struct {
		labels []caseLabel
		body   []stmt
	}
	caseLabel struct {
		lo, hi expr // hi nil unless range
	}
	caseStmt struct {
		p     pos
		sel   expr
		elems []caseElem
		els   []stmt
	}
	forStmt struct {
		p            pos
		v            *identExpr
		from, to, by expr
		body         []stmt
	}
	whileStmt struct {
		p    pos
		cond expr
		body []stmt
	}
	repeatStmt struct {
		p    pos
		body []stmt
		cond expr
	}
	exitStmt   struct{ p pos }
	returnStmt struct{ p pos }
	emptyStmt  struct{ p pos }
)

func (s *assignStmt) position() pos { return s.p }
func (s *callStmt) position() pos   { return s.p }
func (s *ifStmt) position() pos     { return s.p }
func (s *caseStmt) position() pos   { return s.p }
func (s *forStmt) position() pos    { return s.p }
func (s *whileStmt) position() pos  { return s.p }
func (s *repeatStmt) position() pos { return s.p }
func (s *exitStmt) position() pos   { return s.p }
func (s *returnStmt) position() pos { return s.p }
func (s *emptyStmt) position() pos  { return s.p }

// ---------- parser ----------

type parser struct {
	toks  []token
	i     int
	diags []Diagnostic
	loops int
}

type bailout struct{}

func parse(toks []token) ([]stmt, []Diagnostic) {
	p := &parser{toks: toks}
	var body []stmt
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(bailout); !ok {
					panic(r)
				}
			}
		}()
		body = p.stmtList(nil)
		if p.cur().kind != tEOF {
			p.errorf(p.cur(), "E_SYNTAX", "多余的 %s（可能缺少对应的 IF / CASE / FOR 等开始语句）", p.cur())
		}
	}()
	return body, p.diags
}

func (p *parser) cur() token { return p.toks[p.i] }
func (p *parser) next() token {
	t := p.toks[p.i]
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}
func (p *parser) peekTok(o int) token {
	if p.i+o < len(p.toks) {
		return p.toks[p.i+o]
	}
	return p.toks[len(p.toks)-1]
}

func (p *parser) isKw(t token, kw string) bool { return t.kind == tKeyword && t.up == kw }
func (p *parser) isOp(t token, op string) bool { return t.kind == tOp && t.text == op }

func (p *parser) errorf(t token, code, format string, args ...any) {
	p.diags = append(p.diags, Diagnostic{Severity: SevError, Code: code, Line: t.line, Column: t.col, Message: fmt.Sprintf(format, args...)})
	if len(p.diags) > 30 {
		panic(bailout{})
	}
}

func (p *parser) expectKw(kw string, context string) bool {
	if p.isKw(p.cur(), kw) {
		p.next()
		return true
	}
	p.errorf(p.cur(), "E_SYNTAX", "%s 缺少 %s，这里是 %s", context, kw, p.cur())
	return false
}

func (p *parser) expectOp(op string, context string) bool {
	if p.isOp(p.cur(), op) {
		p.next()
		return true
	}
	p.errorf(p.cur(), "E_SYNTAX", "%s 缺少 '%s'，这里是 %s", context, op, p.cur())
	return false
}

// optionalSemi consumes the ';' after END_xxx (Sysmac accepts it with or without).
func (p *parser) optionalSemi() {
	if p.isOp(p.cur(), ";") {
		p.next()
	}
}

var blockEnders = map[string]bool{
	"END_IF": true, "ELSIF": true, "ELSE": true, "END_CASE": true, "END_FOR": true,
	"END_WHILE": true, "UNTIL": true, "END_REPEAT": true,
}

// stmtList parses statements until a block-ending keyword (or EOF). In CASE
// bodies inCase is non-nil and a "label :" sequence also ends the list.
func (p *parser) stmtList(inCase *bool) []stmt {
	var list []stmt
	for {
		t := p.cur()
		if t.kind == tEOF {
			return list
		}
		if t.kind == tKeyword && blockEnders[t.up] {
			return list
		}
		if inCase != nil && p.looksLikeCaseLabel() {
			return list
		}
		start := p.i
		s := p.statement()
		if s != nil {
			list = append(list, s)
		}
		if p.i == start { // no progress: skip a token to avoid loops
			p.errorf(p.cur(), "E_SYNTAX", "无法解析的 %s", p.cur())
			p.next()
		}
	}
}

// looksLikeCaseLabel scans ahead for "<labels> :" (not ":=") at depth 0.
func (p *parser) looksLikeCaseLabel() bool {
	depth := 0
	for k := p.i; k < len(p.toks); k++ {
		t := p.toks[k]
		switch {
		case t.kind == tEOF:
			return false
		case t.kind == tKeyword && t.up != "TRUE" && t.up != "FALSE" && t.up != "NOT" && t.up != "MOD" && t.up != "AND" && t.up != "OR" && t.up != "XOR":
			return false
		case t.kind == tOp && (t.text == "(" || t.text == "["):
			depth++
		case t.kind == tOp && (t.text == ")" || t.text == "]"):
			depth--
		case t.kind == tOp && depth == 0 && (t.text == ";" || t.text == ":=" || t.text == "=>"):
			return false
		case t.kind == tOp && depth == 0 && t.text == ":":
			return true
		}
	}
	return false
}

func (p *parser) statement() stmt {
	t := p.cur()
	ps := pos{t.line, t.col}
	if t.kind == tKeyword {
		switch t.up {
		case "IF":
			return p.ifStatement()
		case "CASE":
			return p.caseStatement()
		case "FOR":
			return p.forStatement()
		case "WHILE":
			p.next()
			cond := p.expression()
			p.expectKw("DO", "WHILE 语句")
			p.loops++
			body := p.stmtList(nil)
			p.loops--
			p.expectKw("END_WHILE", "WHILE 语句")
			p.optionalSemi()
			return &whileStmt{p: ps, cond: cond, body: body}
		case "REPEAT":
			p.next()
			p.loops++
			body := p.stmtList(nil)
			p.loops--
			p.expectKw("UNTIL", "REPEAT 语句")
			cond := p.expression()
			p.expectKw("END_REPEAT", "REPEAT 语句")
			p.optionalSemi()
			return &repeatStmt{p: ps, body: body, cond: cond}
		case "EXIT":
			p.next()
			if p.loops == 0 {
				p.errorf(t, "E_EXIT", "EXIT 只能用在 FOR / WHILE / REPEAT 循环里")
			}
			p.expectOp(";", "EXIT")
			return &exitStmt{p: ps}
		case "RETURN":
			p.next()
			p.expectOp(";", "RETURN")
			return &returnStmt{p: ps}
		}
		p.errorf(t, "E_SYNTAX", "这里不能出现关键字 %s", t)
		p.next()
		return nil
	}
	if p.isOp(t, ";") {
		p.next()
		return &emptyStmt{p: ps}
	}
	// assignment or call
	lhs := p.designator()
	if lhs == nil {
		p.errorf(t, "E_SYNTAX", "语句不能以 %s 开头", t)
		p.skipToSemi()
		return nil
	}
	if p.isOp(p.cur(), "(") {
		call := p.callArgs(lhs)
		if p.isOp(p.cur(), ":=") {
			p.errorf(p.cur(), "E_SYNTAX", "函数调用的结果不能被赋值")
			p.skipToSemi()
			return nil
		}
		p.expectOp(";", "调用语句末尾")
		return &callStmt{p: ps, call: call}
	}
	if p.isOp(p.cur(), ":=") {
		p.next()
		val := p.expression()
		if !p.isOp(p.cur(), ";") {
			p.errorf(p.cur(), "E_SYNTAX", "赋值语句末尾缺少 ';'，这里是 %s", p.cur())
			p.skipToSemi()
			return &assignStmt{p: ps, target: lhs, value: val}
		}
		p.next()
		return &assignStmt{p: ps, target: lhs, value: val}
	}
	if p.isOp(p.cur(), "=") {
		p.errorf(p.cur(), "E_SYNTAX", "赋值要用 ':='，不是 '='")
		p.skipToSemi()
		return nil
	}
	p.errorf(p.cur(), "E_SYNTAX", "%s 后面应该是 ':=' 或 '('，这里是 %s", exprText(lhs), p.cur())
	p.skipToSemi()
	return nil
}

func (p *parser) skipToSemi() {
	for {
		t := p.cur()
		if t.kind == tEOF {
			return
		}
		if p.isOp(t, ";") {
			p.next()
			return
		}
		if t.kind == tKeyword && (blockEnders[t.up] || t.up == "IF" || t.up == "CASE" || t.up == "FOR" || t.up == "WHILE" || t.up == "REPEAT") {
			return
		}
		p.next()
	}
}

func (p *parser) ifStatement() stmt {
	t := p.next()
	s := &ifStmt{p: pos{t.line, t.col}}
	s.conds = append(s.conds, p.expression())
	p.expectKw("THEN", "IF 语句")
	s.thens = append(s.thens, p.stmtList(nil))
	for p.isKw(p.cur(), "ELSIF") {
		p.next()
		s.conds = append(s.conds, p.expression())
		p.expectKw("THEN", "ELSIF")
		s.thens = append(s.thens, p.stmtList(nil))
	}
	if p.isKw(p.cur(), "ELSE") {
		p.next()
		s.els = p.stmtList(nil)
	}
	if !p.isKw(p.cur(), "END_IF") {
		p.errorf(p.cur(), "E_BLOCK", "第 %d 行的 IF 缺少 END_IF，这里是 %s", t.line, p.cur())
		return s
	}
	p.next()
	p.optionalSemi()
	return s
}

func (p *parser) caseStatement() stmt {
	t := p.next()
	s := &caseStmt{p: pos{t.line, t.col}}
	s.sel = p.expression()
	p.expectKw("OF", "CASE 语句")
	inCase := true
	for {
		c := p.cur()
		if c.kind == tEOF || p.isKw(c, "END_CASE") || p.isKw(c, "ELSE") {
			break
		}
		if !p.looksLikeCaseLabel() {
			p.errorf(c, "E_SYNTAX", "CASE 分支应该以 '标签:' 开头，这里是 %s", c)
			p.skipToSemi()
			continue
		}
		var el caseElem
		for {
			lo := p.expression()
			var hi expr
			if p.isOp(p.cur(), "..") {
				p.next()
				hi = p.expression()
			}
			el.labels = append(el.labels, caseLabel{lo: lo, hi: hi})
			if p.isOp(p.cur(), ",") {
				p.next()
				continue
			}
			break
		}
		p.expectOp(":", "CASE 标签")
		el.body = p.stmtList(&inCase)
		s.elems = append(s.elems, el)
	}
	if p.isKw(p.cur(), "ELSE") {
		p.next()
		s.els = p.stmtList(nil)
	}
	if !p.isKw(p.cur(), "END_CASE") {
		p.errorf(p.cur(), "E_BLOCK", "第 %d 行的 CASE 缺少 END_CASE，这里是 %s", t.line, p.cur())
		return s
	}
	p.next()
	p.optionalSemi()
	return s
}

func (p *parser) forStatement() stmt {
	t := p.next()
	s := &forStmt{p: pos{t.line, t.col}}
	v := p.cur()
	if v.kind != tIdent {
		p.errorf(v, "E_SYNTAX", "FOR 后面应该是循环变量，这里是 %s", v)
	} else {
		p.next()
		s.v = &identExpr{p: pos{v.line, v.col}, name: v.text}
	}
	p.expectOp(":=", "FOR 语句")
	s.from = p.expression()
	p.expectKw("TO", "FOR 语句")
	s.to = p.expression()
	if p.isKw(p.cur(), "BY") {
		p.next()
		s.by = p.expression()
	}
	p.expectKw("DO", "FOR 语句")
	p.loops++
	s.body = p.stmtList(nil)
	p.loops--
	if !p.isKw(p.cur(), "END_FOR") {
		p.errorf(p.cur(), "E_BLOCK", "第 %d 行的 FOR 缺少 END_FOR，这里是 %s", t.line, p.cur())
		return s
	}
	p.next()
	p.optionalSemi()
	return s
}

// ---------- expressions ----------

func (p *parser) expression() expr { return p.orExpr() }

func (p *parser) binLevel(next func() expr, ops ...string) expr {
	left := next()
	for {
		t := p.cur()
		matched := ""
		for _, op := range ops {
			if (t.kind == tKeyword && t.up == op) || (t.kind == tOp && t.text == op) {
				matched = op
			}
		}
		if matched == "" {
			return left
		}
		p.next()
		right := next()
		left = &binaryExpr{p: pos{t.line, t.col}, op: matched, l: left, r: right}
	}
}

func (p *parser) orExpr() expr  { return p.binLevel(p.xorExpr, "OR") }
func (p *parser) xorExpr() expr { return p.binLevel(p.andExpr, "XOR") }
func (p *parser) andExpr() expr { return p.binLevel(p.cmpExpr, "AND", "&") }
func (p *parser) cmpExpr() expr { return p.binLevel(p.addExpr, "=", "<>", "<", ">", "<=", ">=") }
func (p *parser) addExpr() expr { return p.binLevel(p.mulExpr, "+", "-") }
func (p *parser) mulExpr() expr { return p.binLevel(p.powExpr, "*", "/", "MOD") }
func (p *parser) powExpr() expr { return p.binLevel(p.unary, "**") }

func (p *parser) unary() expr {
	t := p.cur()
	if p.isKw(t, "NOT") || p.isOp(t, "-") || p.isOp(t, "+") {
		p.next()
		return &unaryExpr{p: pos{t.line, t.col}, op: t.up, x: p.unary()}
	}
	return p.primary()
}

func (p *parser) primary() expr {
	t := p.cur()
	ps := pos{t.line, t.col}
	switch {
	case t.kind == tInt || t.kind == tReal || t.kind == tString || t.kind == tTyped || p.isKw(t, "TRUE") || p.isKw(t, "FALSE"):
		p.next()
		return &literalExpr{p: ps, tok: t}
	case p.isOp(t, "("):
		p.next()
		e := p.expression()
		p.expectOp(")", "括号表达式")
		return e
	case p.isOp(t, "["):
		// array literal is only allowed in initial values, not in code
		p.errorf(t, "E_SYNTAX", "ST 程序里不能用 [ ... ] 数组常量")
		p.skipBalanced("[", "]")
		return &badExpr{p: ps}
	case t.kind == tIdent:
		d := p.designator()
		if p.isOp(p.cur(), "(") {
			return p.callArgs(d)
		}
		return d
	}
	p.errorf(t, "E_SYNTAX", "这里应该是表达式，却是 %s", t)
	if t.kind != tEOF && !(t.kind == tKeyword && blockEnders[t.up]) && !p.isOp(t, ";") {
		p.next()
	}
	return &badExpr{p: ps}
}

func (p *parser) skipBalanced(open, close string) {
	depth := 0
	for p.cur().kind != tEOF {
		t := p.next()
		if p.isOp(t, open) {
			depth++
		} else if p.isOp(t, close) {
			depth--
			if depth == 0 {
				return
			}
		}
	}
}

func (p *parser) designator() expr {
	t := p.cur()
	if t.kind != tIdent {
		return nil
	}
	p.next()
	var e expr = &identExpr{p: pos{t.line, t.col}, name: t.text}
	for {
		c := p.cur()
		switch {
		case p.isOp(c, "."):
			p.next()
			m := p.cur()
			switch m.kind {
			case tIdent:
				p.next()
				e = &memberExpr{p: pos{m.line, m.col}, x: e, member: m.text}
			case tInt:
				p.next()
				e = &memberExpr{p: pos{m.line, m.col}, x: e, member: m.text, bit: true}
			case tReal: // a.0.1 lexed as "0.1"? not valid anyway
				p.errorf(m, "E_SYNTAX", "'.' 后面的 %s 不是合法的成员名或位号", m)
				p.next()
			default:
				p.errorf(m, "E_SYNTAX", "'.' 后面应该是成员名，这里是 %s", m)
				return e
			}
		case p.isOp(c, "["):
			p.next()
			var idx []expr
			for {
				idx = append(idx, p.expression())
				if p.isOp(p.cur(), ",") {
					p.next()
					continue
				}
				break
			}
			p.expectOp("]", "数组下标")
			e = &indexExpr{p: pos{c.line, c.col}, x: e, indices: idx}
		default:
			return e
		}
	}
}

func (p *parser) callArgs(fn expr) *callExpr {
	open := p.next() // (
	call := &callExpr{p: fn.position(), fn: fn}
	if p.isOp(p.cur(), ")") {
		p.next()
		return call
	}
	for {
		t := p.cur()
		arg := callArg{p: pos{t.line, t.col}}
		if t.kind == tIdent && (p.isOp(p.peekTok(1), ":=") || p.isOp(p.peekTok(1), "=>")) {
			arg.name = t.text
			p.next()
			arg.output = p.next().text == "=>"
		}
		if arg.output {
			arg.value = p.designator()
			if arg.value == nil {
				p.errorf(p.cur(), "E_SYNTAX", "'=>' 后面应该是变量，这里是 %s", p.cur())
				arg.value = &badExpr{p: arg.p}
			}
		} else {
			arg.value = p.expression()
		}
		call.args = append(call.args, arg)
		if p.isOp(p.cur(), ",") {
			p.next()
			if p.isOp(p.cur(), ")") {
				p.errorf(p.cur(), "E_SYNTAX", "参数列表最后多了一个 ','")
				p.next()
				return call
			}
			continue
		}
		if p.isOp(p.cur(), ")") {
			p.next()
			return call
		}
		if p.isOp(p.cur(), ";") || p.cur().kind == tEOF {
			p.errorf(p.cur(), "E_SYNTAX", "第 %d 行的 '(' 缺少对应的 ')'", open.line)
			return call
		}
		p.errorf(p.cur(), "E_SYNTAX", "参数之间应该用 ',' 分隔，这里是 %s", p.cur())
		p.skipBalancedFrom()
		return call
	}
}

func (p *parser) skipBalancedFrom() {
	depth := 1
	for p.cur().kind != tEOF && !p.isOp(p.cur(), ";") {
		t := p.next()
		if p.isOp(t, "(") {
			depth++
		} else if p.isOp(t, ")") {
			depth--
			if depth == 0 {
				return
			}
		}
	}
}

func exprText(e expr) string {
	switch x := e.(type) {
	case *identExpr:
		return x.name
	case *memberExpr:
		return exprText(x.x) + "." + x.member
	case *indexExpr:
		return exprText(x.x) + "[...]"
	case *callExpr:
		return exprText(x.fn) + "(...)"
	case *literalExpr:
		return x.tok.text
	}
	return "表达式"
}
