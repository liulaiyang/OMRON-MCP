package stcheck

import (
	"fmt"
	"sort"
	"strings"
)

// Options controls one check.
type Options struct {
	// POU is the program / FB / function whose body is being checked (its
	// variable table supplies local names). Required.
	POU *POU
	// WarnGlobalsNotExternal reports globals that are used but not listed in
	// the POU's external variables.
	WarnGlobalsNotExternal bool
}

// Result of checking one body.
type Result struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
	Errors      int          `json:"errors"`
	Warnings    int          `json:"warnings"`
}

// Check parses and checks one ST body.
func (e *Env) Check(src string, opt Options) Result {
	toks, lexErrs, lexWarns := lex(src)
	var diags []Diagnostic
	for _, le := range lexErrs {
		diags = append(diags, Diagnostic{Severity: SevError, Code: "E_LEX", Line: le.line, Column: le.col, Message: le.msg})
	}
	for _, lw := range lexWarns {
		diags = append(diags, Diagnostic{Severity: SevWarning, Code: "W_LEX", Line: lw.line, Column: lw.col, Message: lw.msg})
	}
	body, perrs := parse(toks)
	diags = append(diags, perrs...)
	if opt.POU != nil {
		c := &checker{env: e, pou: opt.POU, opt: opt, fbCalls: map[string][]pos{}, reportedUndeclared: map[string]bool{}, reportedExternal: map[string]bool{}}
		c.checkDeclaredTypes()
		c.stmts(body)
		c.finish()
		diags = append(diags, c.diags...)
	}
	sort.SliceStable(diags, func(i, j int) bool {
		if diags[i].Line != diags[j].Line {
			return diags[i].Line < diags[j].Line
		}
		return diags[i].Column < diags[j].Column
	})
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i := range diags {
		if l := diags[i].Line; l > 0 && l <= len(lines) {
			s := strings.TrimSpace(lines[l-1])
			if r := []rune(s); len(r) > 120 {
				s = string(r[:120]) + "…"
			}
			diags[i].Source = s
		}
	}
	r := Result{Diagnostics: diags}
	for _, d := range diags {
		if d.Severity == SevError {
			r.Errors++
		} else {
			r.Warnings++
		}
	}
	return r
}

type checker struct {
	env                *Env
	pou                *POU
	opt                Options
	diags              []Diagnostic
	fbCalls            map[string][]pos
	reportedUndeclared map[string]bool
	reportedExternal   map[string]bool
}

func (c *checker) add(sev Severity, p pos, code, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{Severity: sev, Code: code, Line: p.line, Column: p.col, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) finish() {}

// checkDeclaredTypes reports variables of the POU whose type cannot be resolved.
func (c *checker) checkDeclaredTypes() {
	for _, v := range c.pou.Vars {
		if bad := c.unresolved(v.Type); bad != "" {
			c.add(SevWarning, pos{0, 0}, "W_UNKNOWN_TYPE", "变量 %s 的类型 %s 在工程和系统里都找不到（%s 未定义）", v.Name, v.TypeText, bad)
		}
	}
}

func (c *checker) unresolved(t *TypeRef) string {
	for t != nil && t.kind == tkArray {
		t = t.elem
	}
	if t == nil || t.kind != tkNamed {
		return ""
	}
	if c.env.lookupType(t.name) != nil || c.env.POU(t.name) != nil {
		return ""
	}
	return t.name
}

func (c *checker) stmts(list []stmt) {
	for _, s := range list {
		c.stmt(s)
	}
}

func (c *checker) stmt(s stmt) {
	switch x := s.(type) {
	case *assignStmt:
		tt := c.assignTarget(x.target)
		vt := c.expr(x.value)
		c.assignable(tt, vt, x.value.position(), exprText(x.target))
	case *callStmt:
		c.call(x.call, true)
	case *ifStmt:
		for i, cond := range x.conds {
			c.cond(cond, "IF")
			c.stmts(x.thens[i])
		}
		c.stmts(x.els)
	case *caseStmt:
		c.expr(x.sel)
		for _, el := range x.elems {
			for _, l := range el.labels {
				c.caseLabel(l.lo)
				if l.hi != nil {
					c.caseLabel(l.hi)
				}
			}
			c.stmts(el.body)
		}
		c.stmts(x.els)
	case *forStmt:
		if x.v != nil {
			t := c.expr(x.v)
			if t != nil && t.kind == tkBasic && !isIntegerType(t.name) {
				c.add(SevError, x.v.p, "E_FOR_VAR", "FOR 循环变量 %s 必须是整数类型，现在是 %s", x.v.name, t.name)
			}
			c.checkWritable(x.v)
		}
		c.expr(x.from)
		c.expr(x.to)
		if x.by != nil {
			c.expr(x.by)
		}
		c.stmts(x.body)
	case *whileStmt:
		c.cond(x.cond, "WHILE")
		c.stmts(x.body)
	case *repeatStmt:
		c.stmts(x.body)
		c.cond(x.cond, "UNTIL")
	}
}

func (c *checker) caseLabel(e expr) {
	// enum values and constants are both fine as labels
	c.expr(e)
}

func (c *checker) assignTarget(e expr) *TypeRef {
	switch e.(type) {
	case *identExpr, *memberExpr, *indexExpr:
	default:
		c.add(SevError, e.position(), "E_ASSIGN", "赋值号左边必须是变量")
		return nil
	}
	t := c.expr(e)
	c.checkWritable(e)
	return t
}

func (c *checker) cond(e expr, what string) {
	t := c.deref(c.expr(e))
	if t == nil || t == litInt || t == litReal {
		if t != nil {
			c.add(SevError, e.position(), "E_COND_TYPE", "%s 条件必须是 BOOL，这里是数字常量", what)
		}
		return
	}
	if (t.kind == tkBasic && t.name != "BOOL") || t.kind == tkString || t.kind == tkArray {
		c.add(SevError, e.position(), "E_COND_TYPE", "%s 条件必须是 BOOL，这里是 %s（要写成比较，例如 x <> 0）", what, typeName(t))
	}
}

// checkWritable reports writes to constants and to FB instance outputs.
func (c *checker) checkWritable(e expr) {
	root := e
	for {
		switch x := root.(type) {
		case *memberExpr:
			if base, ok := x.x.(*identExpr); ok {
				if v := c.lookupVar(base.name); v != nil && v.Type != nil && v.Type.kind == tkNamed {
					if fb := c.env.POU(v.Type.name); fb != nil && fb.Kind == KindFB {
						if p := fb.Var(x.member); p != nil && p.Group == "VAR_OUTPUT" {
							c.add(SevError, x.p, "E_WRITE_OUTPUT", "不能在外部给功能块实例的输出 %s.%s 赋值", base.name, x.member)
						}
					}
				}
			}
			root = x.x
			continue
		case *indexExpr:
			root = x.x
			continue
		case *identExpr:
			if v := c.lookupVar(x.name); v != nil {
				if v.Const {
					c.add(SevError, x.p, "E_WRITE_CONST", "%s 是常量（CONSTANT），不能赋值", x.name)
				}
			}
		}
		return
	}
}

// lookupVar finds a local (POU) variable or a global.
func (c *checker) lookupVar(name string) *Var {
	if v := c.pou.Var(name); v != nil && v.Group != "RETURN" {
		return v
	}
	if c.pou.Kind == KindFunction && strings.EqualFold(name, c.pou.Name) {
		if v := c.pou.Var(name); v != nil {
			return v
		}
		return &Var{Name: name, Type: c.pou.Return, Group: "RETURN"}
	}
	return c.env.globals[strings.ToUpper(name)]
}

var systemFlags = map[string]string{
	"P_ON": "BOOL", "P_OFF": "BOOL", "P_CY": "BOOL", "P_FIRST_RUN": "BOOL", "P_FIRST_RUNMODE": "BOOL", "P_PRGER": "BOOL",
}

// resolveIdent returns the type of a bare identifier, or nil when unknown.
func (c *checker) resolveIdent(x *identExpr) *TypeRef {
	name := x.name
	up := strings.ToUpper(name)
	if v := c.pou.Var(name); v != nil {
		if v.Group == "RETURN" {
			return c.pou.Return
		}
		return v.Type
	}
	if c.pou.Kind == KindFunction && strings.EqualFold(name, c.pou.Name) {
		return c.pou.Return
	}
	if t, ok := systemFlags[up]; ok {
		if up == "P_FIRST_RUN" && c.pou.Kind != KindProgram {
			c.add(SevError, x.p, "E_FIRST_RUN", "P_First_Run 只能用在程序里，功能块 / 函数里请用 P_First_RunMode 或自己的首次标志")
		}
		return &TypeRef{kind: tkBasic, name: t}
	}
	if g, ok := c.env.globals[up]; ok {
		if c.opt.WarnGlobalsNotExternal && !c.reportedExternal[up] {
			c.reportedExternal[up] = true
			c.add(SevWarning, x.p, "W_EXTERNAL", "全局变量 %s 没有登记在本%s的外部变量里（Studio 编辑器会自动登记，MCP 写入不会；如果编译报错，用 sysmac_slwd_write 在 VAR_EXTERNAL 组加上它）", name, c.pou.Kind)
		}
		return g.Type
	}
	if list, ok := c.env.enumVals[up]; ok && len(list) > 0 {
		return &TypeRef{kind: tkNamed, name: list[0].FullName}
	}
	if p := c.env.POU(name); p != nil {
		if p.Kind == KindFunction {
			c.add(SevError, x.p, "E_FUNC_NO_CALL", "%s 是函数，调用时要加括号和参数", name)
		} else if p.Kind == KindFB {
			c.add(SevError, x.p, "E_FB_TYPE", "%s 是功能块类型，不是实例；先在变量表里声明一个实例再用", name)
		}
		return nil
	}
	if strings.HasPrefix(name, "_") {
		// System-defined variables (_EC_*, _MC_*, _Card*, _Dev*...) are not in the
		// project files; accept but remind about the external declaration.
		if c.opt.WarnGlobalsNotExternal && !c.reportedExternal[up] {
			c.reportedExternal[up] = true
			c.add(SevWarning, x.p, "W_SYSTEM_VAR", "%s 不在变量表里，按系统变量处理（未核对）；系统变量需要登记在本%s的外部变量里", name, c.pou.Kind)
		}
		return nil
	}
	if c.pou.Opaque {
		return nil
	}
	if !c.reportedUndeclared[up] {
		c.reportedUndeclared[up] = true
		msg := fmt.Sprintf("%s 未声明（本%s变量表、全局变量、枚举值里都没有）", name, c.pou.Kind)
		if s := c.suggestName(name); s != "" {
			msg += "，是不是 " + s + "？"
		}
		c.add(SevError, x.p, "E_UNDECLARED", "%s", msg)
	}
	return nil
}

func (c *checker) suggestName(name string) string {
	var cands []string
	for _, v := range c.pou.Vars {
		cands = append(cands, v.Name)
	}
	for _, v := range c.env.globals {
		cands = append(cands, v.Name)
	}
	for k, list := range c.env.enumVals {
		_ = k
		for _, t := range list {
			for _, ev := range t.Enums {
				if strings.EqualFold(ev, k) {
					cands = append(cands, ev)
				}
			}
		}
	}
	return closest(name, cands)
}

// expr checks an expression and returns its type when it is known.
func (c *checker) expr(e expr) *TypeRef {
	switch x := e.(type) {
	case *identExpr:
		return c.resolveIdent(x)
	case *literalExpr:
		return c.literal(x)
	case *memberExpr:
		base := c.expr(x.x)
		return c.member(x, base)
	case *indexExpr:
		base := c.expr(x.x)
		for _, ix := range x.indices {
			c.expr(ix)
		}
		if base == nil {
			return nil
		}
		base = c.deref(base)
		if base.kind == tkArray {
			if len(x.indices) != base.dims {
				c.add(SevError, x.p, "E_INDEX_DIMS", "%s 是 %d 维数组，这里给了 %d 个下标", exprText(x.x), base.dims, len(x.indices))
			}
			return base.elem
		}
		if base.kind == tkBasic || base.kind == tkString || base.kind == tkNamed {
			c.add(SevError, x.p, "E_NOT_ARRAY", "%s 不是数组，不能用 [ ] 下标", exprText(x.x))
		}
		return nil
	case *callExpr:
		return c.call(x, false)
	case *unaryExpr:
		t := c.expr(x.x)
		if x.op == "NOT" {
			return t
		}
		return t
	case *binaryExpr:
		l := c.expr(x.l)
		r := c.expr(x.r)
		switch x.op {
		case "=", "<>", "<", ">", "<=", ">=":
			return &TypeRef{kind: tkBasic, name: "BOOL"}
		}
		return combine(c.deref(l), c.deref(r))
	}
	return nil
}

func (c *checker) literal(x *literalExpr) *TypeRef {
	t := x.tok
	switch t.kind {
	case tInt:
		return litInt // integer literals adapt to the context type
	case tReal:
		return litReal
	case tString:
		return &TypeRef{kind: tkString, name: "STRING"}
	case tKeyword:
		return &TypeRef{kind: tkBasic, name: "BOOL"}
	case tTyped:
		if timePrefixes[t.prefix] || basicTypes[t.prefix] {
			name := t.prefix
			switch name {
			case "T":
				name = "TIME"
			case "LT":
				name = "LTIME"
			case "D":
				name = "DATE"
			case "TOD":
				name = "TIME_OF_DAY"
			case "DT":
				name = "DATE_AND_TIME"
			}
			if (name == "TIME" || name == "LTIME") && !validDuration(t.value) {
				c.add(SevError, x.p, "E_TIME_LITERAL", "时间常量 %s 写法不对（例：T#500ms、T#1s、T#1m30s）", t.text)
			}
			return &TypeRef{kind: tkBasic, name: name}
		}
		if t.prefix == "STRING" {
			return &TypeRef{kind: tkString, name: "STRING"}
		}
		// Enum literal: E_Type#Value
		td := c.env.lookupType(t.prefix)
		if td == nil {
			c.add(SevError, x.p, "E_UNKNOWN_TYPE", "%s 里的类型 %s 不存在", t.text, t.prefix)
			return nil
		}
		if td.Kind == TDEnum {
			found := false
			for _, v := range td.Enums {
				if strings.EqualFold(v, t.value) {
					found = true
				}
			}
			if !found {
				msg := fmt.Sprintf("枚举 %s 里没有 %s", td.Name, t.value)
				if s := closest(t.value, td.Enums); s != "" {
					msg += "，是不是 " + s + "？"
				}
				c.add(SevError, x.p, "E_ENUM_VALUE", "%s", msg)
			}
		}
		return &TypeRef{kind: tkNamed, name: td.FullName}
	}
	return nil
}

func validDuration(v string) bool {
	s := strings.ToLower(strings.ReplaceAll(v, "_", ""))
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	units := []string{"ms", "us", "ns", "d", "h", "m", "s"}
	for s != "" {
		i := 0
		for i < len(s) && (isDigitRune(rune(s[i])) || s[i] == '.') {
			i++
		}
		if i == 0 {
			return false
		}
		s = s[i:]
		ok := false
		for _, u := range units {
			if strings.HasPrefix(s, u) {
				s = s[len(u):]
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// deref resolves aliases of a named type.
func (c *checker) deref(t *TypeRef) *TypeRef {
	for i := 0; i < 8 && t != nil && t.kind == tkNamed; i++ {
		td := c.env.lookupType(t.name)
		if td == nil || td.Kind != TDAlias || td.Alias == nil {
			return t
		}
		t = td.Alias
	}
	return t
}

func (c *checker) member(x *memberExpr, base *TypeRef) *TypeRef {
	if base == nil {
		return nil
	}
	base = c.deref(base)
	if x.bit {
		if base.kind == tkBasic {
			bits, ok := bitStringTypes[base.name]
			if !ok {
				c.add(SevError, x.p, "E_BIT_ACCESS", "%s 是 %s，不能用 .%s 取位", exprText(x.x), base.name, x.member)
				return nil
			}
			var n int
			fmt.Sscan(x.member, &n)
			if n >= bits {
				c.add(SevError, x.p, "E_BIT_RANGE", "%s 是 %s（%d 位），位号 %s 超范围", exprText(x.x), base.name, bits, x.member)
			}
			return &TypeRef{kind: tkBasic, name: "BOOL"}
		}
		if base.kind == tkNamed || base.kind == tkArray || base.kind == tkString {
			c.add(SevError, x.p, "E_BIT_ACCESS", "%s 不是整数 / 位串类型，不能用 .%s 取位", exprText(x.x), x.member)
		}
		return nil
	}
	switch base.kind {
	case tkArray:
		c.add(SevError, x.p, "E_MEMBER_ON_ARRAY", "%s 是数组，访问成员前要先加下标 [ ]", exprText(x.x))
		return nil
	case tkBasic, tkString:
		c.add(SevError, x.p, "E_MEMBER", "%s 是 %s 类型，没有成员 %s", exprText(x.x), strings.TrimSpace(base.name), x.member)
		return nil
	case tkNamed:
		if fb := c.env.POU(base.name); fb != nil && fb.Kind == KindFB {
			if fb.Opaque {
				return nil
			}
			v := fb.Var(x.member)
			if v == nil {
				msg := fmt.Sprintf("功能块 %s 没有参数 %s", fb.Name, x.member)
				var names []string
				for _, p := range fb.Params() {
					names = append(names, p.Name)
				}
				if s := closest(x.member, names); s != "" {
					msg += "，是不是 " + s + "？"
				}
				c.add(SevError, x.p, "E_FB_MEMBER", "%s", msg)
				return nil
			}
			if v.Group != "VAR_INPUT" && v.Group != "VAR_OUTPUT" && v.Group != "VAR_IN_OUT" {
				c.add(SevError, x.p, "E_FB_INTERNAL", "%s.%s 是功能块 %s 的内部变量，外部不能访问", exprText(x.x), x.member, fb.Name)
				return nil
			}
			return v.Type
		}
		td := c.env.lookupType(base.name)
		if td == nil {
			return nil // unknown type: cannot check further
		}
		if td.Kind == TDEnum {
			c.add(SevError, x.p, "E_MEMBER", "%s 是枚举 %s，没有成员 %s", exprText(x.x), td.Name, x.member)
			return nil
		}
		if td.Kind == TDStruct || td.Kind == TDUnion {
			m := td.member(x.member)
			if m == nil {
				msg := fmt.Sprintf("结构体 %s 没有成员 %s", td.Name, x.member)
				var names []string
				for _, mm := range td.Members {
					names = append(names, mm.Name)
				}
				if s := closest(x.member, names); s != "" {
					msg += "，是不是 " + s + "？"
				}
				c.add(SevError, x.p, "E_MEMBER", "%s", msg)
				return nil
			}
			return m.Type
		}
	}
	return nil
}

// call checks an FB instance call or a function call.
func (c *checker) call(x *callExpr, statement bool) *TypeRef {
	// FB instance (possibly indexed / member)
	var calleeType *TypeRef
	var fnName string
	switch f := x.fn.(type) {
	case *identExpr:
		if v := c.lookupVar(f.name); v != nil {
			calleeType = v.Type
			c.resolveIdent(f) // external-variable warning etc.
		} else {
			fnName = f.name
		}
	default:
		calleeType = c.expr(x.fn)
	}
	if fnName == "" {
		calleeType = c.deref(calleeType)
		if calleeType == nil {
			c.argsExprs(x)
			return nil
		}
		if calleeType.kind == tkNamed {
			if fb := c.env.POU(calleeType.name); fb != nil && fb.Kind == KindFB {
				if fb.Opaque {
					c.argsExprs(x)
					return nil
				}
				key := strings.ToUpper(exprText(x.fn))
				if _, isIdent := x.fn.(*identExpr); isIdent {
					c.fbCalls[key] = append(c.fbCalls[key], x.p)
				}
				c.checkArgs(x, fb, true)
				return nil
			}
		}
		if calleeType.kind != tkUnknown {
			c.add(SevError, x.p, "E_NOT_CALLABLE", "%s 不是功能块实例，不能调用", exprText(x.fn))
		}
		c.argsExprs(x)
		return nil
	}
	p := c.env.POU(fnName)
	if p == nil {
		msg := fmt.Sprintf("%s 不存在（工程里和系统指令里都没有这个函数 / 功能块）", fnName)
		var names []string
		for _, q := range c.env.pous {
			names = append(names, q.Name)
		}
		if s := closest(fnName, names); s != "" {
			msg += "，是不是 " + s + "？"
		}
		c.add(SevError, x.p, "E_UNKNOWN_FUNC", "%s", msg)
		c.argsExprs(x)
		return nil
	}
	if p.Kind == KindFB {
		c.add(SevError, x.p, "E_FB_TYPE_CALL", "%s 是功能块类型，不能直接调用；要先声明实例（例：fb%s : %s;），再调用实例", p.Name, strings.TrimPrefix(p.Name, "FB_"), p.Name)
		c.argsExprs(x)
		return nil
	}
	if p.Kind == KindProgram {
		c.add(SevError, x.p, "E_PROGRAM_CALL", "%s 是程序，不能在 ST 里调用", p.Name)
		return nil
	}
	if p.Generic || p.Opaque {
		c.argsExprs(x)
		return p.Return
	}
	c.checkArgs(x, p, false)
	return p.Return
}

func (c *checker) argsExprs(x *callExpr) {
	for _, a := range x.args {
		if a.output {
			c.expr(a.value)
			c.checkWritable(a.value)
		} else {
			c.expr(a.value)
		}
	}
}

func (c *checker) checkArgs(x *callExpr, p *POU, isFB bool) {
	params := p.Params()
	seen := map[string]bool{}
	positional := 0
	for _, a := range x.args {
		if a.name == "" {
			positional++
			c.expr(a.value)
			continue
		}
		up := strings.ToUpper(a.name)
		if up == "EN" || up == "ENO" {
			c.expr(a.value)
			continue
		}
		v := p.Var(a.name)
		if v == nil || (v.Group != "VAR_INPUT" && v.Group != "VAR_OUTPUT" && v.Group != "VAR_IN_OUT") {
			var names []string
			for _, q := range params {
				names = append(names, q.Name)
			}
			msg := fmt.Sprintf("%s %s 没有参数 %s", p.Kind, p.Name, a.name)
			if s := closest(a.name, names); s != "" {
				msg += "，是不是 " + s + "？"
			}
			c.add(SevError, a.p, "E_PARAM", "%s", msg)
			c.expr(a.value)
			continue
		}
		if seen[up] {
			c.add(SevError, a.p, "E_PARAM_DUP", "参数 %s 重复", a.name)
		}
		seen[up] = true
		switch v.Group {
		case "VAR_OUTPUT":
			if !a.output {
				c.add(SevError, a.p, "E_PARAM_DIR", "%s 是输出参数，要用 '=>'（%s => 变量），不是 ':='", a.name, a.name)
			}
		default:
			if a.output {
				c.add(SevError, a.p, "E_PARAM_DIR", "%s 是输入%s参数，要用 ':='，不是 '=>'", a.name, map[bool]string{true: "输出", false: ""}[v.Group == "VAR_IN_OUT"])
			}
		}
		at := c.expr(a.value)
		switch {
		case a.output:
			c.assignable(at, v.Type, a.p, exprText(a.value))
		case v.Group == "VAR_IN_OUT":
			pt, vt := c.deref(v.Type), c.deref(at)
			if pt != nil && vt != nil && pt.kind == tkBasic && vt.kind == tkBasic && pt.name != vt.name && vt != litInt && vt != litReal {
				c.add(SevError, a.p, "E_INOUT_TYPE", "输入输出参数 %s 要求 %s，连接的变量是 %s（输入输出参数类型必须完全一致）", a.name, pt.name, vt.name)
			}
		default:
			c.assignable(v.Type, at, a.p, p.Name+"."+a.name)
		}
		if a.output || v.Group == "VAR_IN_OUT" {
			switch a.value.(type) {
			case *identExpr, *memberExpr, *indexExpr:
				if a.output {
					c.checkWritable(a.value)
				}
			default:
				c.add(SevError, a.p, "E_PARAM_VAR", "参数 %s 必须连接变量，不能是常量或表达式", a.name)
			}
		}
	}
	if isFB && positional > 0 {
		c.add(SevError, x.p, "E_FB_POSITIONAL", "调用功能块实例 %s 必须写参数名（参数名 := 值）", exprText(x.fn))
	}
	if !isFB && positional > 0 && len(seen) == 0 {
		k := 0
		for _, q := range params {
			if q.Group == "VAR_OUTPUT" || isENO(q.Name) {
				continue
			}
			if k >= len(x.args) {
				break
			}
			if q.Group == "VAR_INPUT" {
				c.assignable(q.Type, c.typeOfQuiet(x.args[k].value), x.args[k].p, p.Name+"."+q.Name)
			}
			k++
		}
	}
	if !isFB && positional > 0 {
		inputs := 0
		for _, q := range params {
			if q.Group != "VAR_OUTPUT" && !isENO(q.Name) {
				inputs++
			}
		}
		if positional > inputs {
			c.add(SevError, x.p, "E_PARAM_COUNT", "函数 %s 只有 %d 个输入参数，这里给了 %d 个", p.Name, inputs, positional)
		}
		if positional > 0 && len(seen) > 0 {
			c.add(SevWarning, x.p, "W_PARAM_MIX", "函数 %s 的调用混用了带名和不带名参数，建议统一写参数名", p.Name)
		}
	}
	if positional == 0 {
		for _, q := range params {
			if q.Group == "VAR_IN_OUT" && !seen[strings.ToUpper(q.Name)] {
				c.add(SevError, x.p, "E_INOUT_MISSING", "%s %s 的输入输出参数 %s 必须连接", p.Kind, p.Name, q.Name)
			}
		}
	}
}

// closest returns the candidate nearest to name (case-insensitive) or "".
func closest(name string, cands []string) string {
	best, bestD := "", 1<<30
	un := strings.ToUpper(name)
	n := len([]rune(name))
	if n < 3 {
		return ""
	}
	limit := 1
	if n > 5 {
		limit = 2
	}
	if n > 10 {
		limit = 3
	}
	for _, cand := range cands {
		uc := strings.ToUpper(cand)
		if uc == un {
			continue
		}
		d := levenshtein(un, uc)
		if d < bestD {
			best, bestD = cand, d
		}
	}
	if bestD <= limit {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if abs(len(ra)-len(rb)) > 4 {
		return 99
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// typeOfQuiet evaluates an expression type without emitting diagnostics.
func (c *checker) typeOfQuiet(e expr) *TypeRef {
	saved := len(c.diags)
	und := c.reportedUndeclared
	ext := c.reportedExternal
	c.reportedUndeclared = map[string]bool{}
	c.reportedExternal = map[string]bool{}
	t := c.expr(e)
	c.diags = c.diags[:saved]
	c.reportedUndeclared, c.reportedExternal = und, ext
	return t
}

func isENO(n string) bool { return strings.EqualFold(n, "EN") || strings.EqualFold(n, "ENO") }
