package stcheck

import (
	"regexp"
	"strings"
)

var (
	reCxifAttr    = regexp.MustCompile(`\{[^{}]*\}`)
	reCxifComment = regexp.MustCompile(`(?m)//.*$`)
	reBlockCmt    = regexp.MustCompile(`(?s)\(\*.*?\*\)`)
)

// LoadCxif reads a Sysmac builder interface file (builder2\CxIF\<model>\<ver>\*.cxif)
// and registers its system types and POU interfaces.
func (e *Env) LoadCxif(text string) (types, pous int) {
	text = strings.TrimPrefix(text, "\uFEFF")
	text = reBlockCmt.ReplaceAllString(text, " ")
	text = reCxifComment.ReplaceAllString(text, "")
	for {
		n := reCxifAttr.ReplaceAllString(text, " ")
		if n == text {
			break
		}
		text = n
	}
	toks := simpleTokens(text)
	c := &cxif{toks: toks, env: e}
	c.run()
	return c.nTypes, c.nPOUs
}

type cxif struct {
	toks   []string
	i      int
	env    *Env
	nTypes int
	nPOUs  int
}

func (c *cxif) at(k int) string {
	if c.i+k < len(c.toks) {
		return c.toks[c.i+k]
	}
	return ""
}
func (c *cxif) up(k int) string { return strings.ToUpper(c.at(k)) }

func (c *cxif) run() {
	for c.i < len(c.toks) {
		switch c.up(0) {
		case "TYPE":
			c.i++
			c.typeBlock()
		case "FUNCTION_BLOCK":
			c.i++
			c.pou(KindFB)
		case "FUNCTION":
			c.i++
			c.pou(KindFunction)
		default:
			c.i++
		}
	}
}

func (c *cxif) skipTo(sym string) {
	for c.i < len(c.toks) && c.toks[c.i] != sym {
		c.i++
	}
	if c.i < len(c.toks) {
		c.i++
	}
}

// typeBlock: name : STRUCT ... END_STRUCT ; | name : (a, b) ; | name : UNION ... END_UNION; | name : base ;
func (c *cxif) typeBlock() {
	for c.i < len(c.toks) {
		if c.up(0) == "END_TYPE" {
			c.i++
			return
		}
		name := c.at(0)
		if !isWordToken(name) || c.at(1) != ":" {
			c.i++
			continue
		}
		c.i += 2
		td := &TypeDef{Name: shortName(name), FullName: shortName(name), System: true}
		switch c.up(0) {
		case "STRUCT", "UNION":
			if c.up(0) == "UNION" {
				td.Kind = TDUnion
			}
			end := "END_" + c.up(0)
			c.i++
			for c.i < len(c.toks) && c.up(0) != end {
				td.Members = append(td.Members, c.memberDecls("MEMBER")...)
			}
			c.i++
			if c.at(0) == ";" {
				c.i++
			}
		case "(":
			td.Kind = TDEnum
			c.i++
			for c.i < len(c.toks) && c.at(0) != ")" {
				if isWordToken(c.at(0)) && (c.at(1) == ":=" || c.at(1) == "," || c.at(1) == ")") {
					td.Enums = append(td.Enums, c.at(0))
				}
				if c.at(0) == ":=" {
					c.i += 2
					continue
				}
				c.i++
			}
			c.skipTo(";")
		default:
			td.Kind = TDAlias
			start := c.i
			for c.i < len(c.toks) && c.at(0) != ";" && c.at(0) != ":=" {
				c.i++
			}
			td.Alias = ParseTypeRef(strings.Join(c.toks[start:c.i], " "))
			c.skipTo(";")
		}
		c.env.AddType(td)
		c.nTypes++
	}
}

// memberDecls parses "a, b : TYPE [:= init] ;" and returns the variables.
func (c *cxif) memberDecls(group string) []*Var {
	var names []string
	for c.i < len(c.toks) && c.at(0) != ":" {
		if isWordToken(c.at(0)) {
			names = append(names, c.at(0))
		}
		if strings.HasPrefix(c.up(0), "END_") {
			return nil
		}
		c.i++
	}
	c.i++ // ':'
	start := c.i
	depth := 0
	for c.i < len(c.toks) {
		t := c.at(0)
		if t == "[" || t == "(" {
			depth++
		} else if t == "]" || t == ")" {
			depth--
		} else if depth == 0 && (t == ";" || t == ":=") {
			break
		}
		c.i++
	}
	typeText := strings.Join(c.toks[start:c.i], " ")
	c.skipTo(";")
	var out []*Var
	for _, n := range names {
		out = append(out, &Var{Name: n, TypeText: typeText, Type: ParseTypeRef(typeText), Group: group})
	}
	return out
}

func (c *cxif) pou(kind POUKind) {
	name := c.at(0)
	if !isWordToken(name) {
		return
	}
	c.i++
	p := &POU{Name: shortName(name), Kind: kind, System: true}
	if kind == KindFunction && c.at(0) == ":" {
		c.i++
		start := c.i
		for c.i < len(c.toks) && !strings.HasPrefix(c.up(0), "VAR") && !strings.HasPrefix(c.up(0), "END_FUNCTION") {
			c.i++
		}
		p.Return = ParseTypeRef(strings.Join(c.toks[start:c.i], " "))
	}
	end := "END_FUNCTION"
	if kind == KindFB {
		end = "END_FUNCTION_BLOCK"
	}
	order := 0
	for c.i < len(c.toks) && c.up(0) != end {
		u := c.up(0)
		if strings.HasPrefix(u, "VAR") {
			group := u
			c.i++
			konst := false
			for c.up(0) == "CONSTANT" || c.up(0) == "RETAIN" {
				if c.up(0) == "CONSTANT" {
					konst = true
				}
				c.i++
			}
			for c.i < len(c.toks) && c.up(0) != "END_VAR" {
				for _, v := range c.memberDecls(group) {
					v.Const = konst
					v.Order = order
					order++
					p.AddVar(v)
				}
			}
			c.i++
			continue
		}
		c.i++
	}
	c.i++
	for _, v := range p.Vars {
		if v.Type.kind == tkUnknown && (v.Group == "VAR_INPUT" || v.Group == "VAR_IN_OUT" || v.Group == "VAR_OUTPUT") {
			p.Generic = true
		}
	}
	if p.Return != nil && p.Return.kind == tkUnknown {
		p.Generic = true
	}
	// Generic overload families (ABS, MAX, ...) are the names users call; the
	// typed variants (ABS_INT ...) also stay callable.
	c.env.AddPOU(p)
	c.nPOUs++
}
