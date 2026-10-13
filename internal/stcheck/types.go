package stcheck

import (
	"fmt"
	"strings"
)

// Literal pseudo-types: integer / real literals adapt to the context.
var (
	litInt  = &TypeRef{kind: tkBasic, name: "#INT"}
	litReal = &TypeRef{kind: tkBasic, name: "#REAL"}
)

func typeName(t *TypeRef) string {
	if t == nil {
		return "?"
	}
	switch t {
	case litInt:
		return "整数常量"
	case litReal:
		return "实数常量"
	}
	switch t.kind {
	case tkArray:
		return "ARRAY OF " + typeName(t.elem)
	case tkString:
		return "STRING"
	case tkNamed:
		return shortName(t.name)
	}
	return t.name
}

// combine gives the result type of an arithmetic / logic operation when it
// can be determined, otherwise nil.
func combine(l, r *TypeRef) *TypeRef {
	if l == nil || r == nil {
		return nil
	}
	if l == litReal || r == litReal {
		if (l == litReal || l == litInt) && (r == litReal || r == litInt) {
			return litReal
		}
	}
	if l == litInt || l == litReal {
		return r
	}
	if r == litInt || r == litReal {
		return l
	}
	if l.kind == tkBasic && r.kind == tkBasic && l.name == r.name {
		return l
	}
	if l.kind == tkNamed && r.kind == tkNamed && strings.EqualFold(shortName(l.name), shortName(r.name)) {
		return l
	}
	return nil
}

var intRank = map[string]int{"SINT": 1, "INT": 2, "DINT": 3, "LINT": 4, "USINT": 1, "UINT": 2, "UDINT": 3, "ULINT": 4, "BYTE": 1, "WORD": 2, "DWORD": 3, "LWORD": 4}

func isSigned(n string) bool   { return n == "SINT" || n == "INT" || n == "DINT" || n == "LINT" }
func isUnsigned(n string) bool { return n == "USINT" || n == "UINT" || n == "UDINT" || n == "ULINT" }
func isBitStr(n string) bool   { return n == "BYTE" || n == "WORD" || n == "DWORD" || n == "LWORD" }
func isRealT(n string) bool    { return n == "REAL" || n == "LREAL" }

type convLevel int

const (
	convOK convLevel = iota
	convWarn
	convError
)

func isNumeric(n string) bool { return isSigned(n) || isUnsigned(n) || isRealT(n) }

// implicitConv classifies assigning a value of type v to a target of type t.
// Calibrated on compiled Sysmac projects: integer sign changes of the same
// size are accepted by Sysmac; integer narrowing / bit-string <-> integer are
// reported as warnings; mixing number / BOOL / TIME / STRING and REAL ->
// integer are errors.
func implicitConv(t, v string) convLevel {
	if t == v {
		return convOK
	}
	switch v {
	case "#INT":
		if isNumeric(t) || isBitStr(t) {
			return convOK
		}
		return convError
	case "#REAL":
		if isRealT(t) {
			return convOK
		}
		return convError
	}
	switch {
	case (isSigned(v) || isUnsigned(v)) && (isSigned(t) || isUnsigned(t)):
		if intRank[t] >= intRank[v] {
			return convOK
		}
		return convWarn
	case (isSigned(v) || isUnsigned(v)) && isRealT(t):
		return convOK
	case isRealT(v) && isRealT(t):
		if t == "LREAL" {
			return convOK
		}
		return convWarn
	case isRealT(v) && (isSigned(t) || isUnsigned(t) || isBitStr(t)):
		return convError
	case isBitStr(v) && isBitStr(t):
		if intRank[t] >= intRank[v] {
			return convOK
		}
		return convWarn
	case (isBitStr(v) && (isSigned(t) || isUnsigned(t))) || (isBitStr(t) && (isSigned(v) || isUnsigned(v))):
		return convWarn
	}
	if t == "BOOL" || v == "BOOL" || basicTypes[t] && basicTypes[v] {
		return convError
	}
	return convWarn
}

// assignable reports a value whose type cannot be assigned to the target.
func (c *checker) assignable(target, value *TypeRef, p pos, what string) {
	t, v := c.deref(target), c.deref(value)
	if t == nil || v == nil {
		return
	}
	if t.kind == tkBasic && v.kind == tkBasic {
		lvl := implicitConv(t.name, v.name)
		if lvl == convOK {
			return
		}
		hint := ""
		if v.name != "#INT" && v.name != "#REAL" && t.name != "BOOL" && v.name != "BOOL" {
			hint = fmt.Sprintf("，用 %s_TO_%s(...) 转换", v.name, t.name)
		}
		if lvl == convError {
			c.add(SevError, p, "E_TYPE", "类型不匹配：%s 是 %s，值是 %s%s", what, t.name, typeName(v), hint)
		} else {
			c.add(SevWarning, p, "W_TYPE", "%s 是 %s，值是 %s，可能丢失数据或 Studio 不接受%s", what, t.name, typeName(v), hint)
		}
		return
	}
	if t.kind == tkString && v.kind == tkBasic && v.name != "" {
		c.add(SevError, p, "E_TYPE", "类型不匹配：%s 是 STRING，赋给它的值是 %s", what, typeName(v))
		return
	}
	if t.kind == tkBasic && v.kind == tkString {
		c.add(SevError, p, "E_TYPE", "类型不匹配：%s 是 %s，赋给它的值是 STRING", what, t.name)
		return
	}
	if t.kind == tkNamed && v.kind == tkNamed {
		tt, vt := c.env.lookupType(t.name), c.env.lookupType(v.name)
		if tt != nil && vt != nil && tt != vt && tt.Kind == TDEnum && vt.Kind == TDEnum {
			c.add(SevError, p, "E_TYPE", "类型不匹配：%s 是枚举 %s，赋给它的值是枚举 %s", what, tt.Name, vt.Name)
		}
	}
}
