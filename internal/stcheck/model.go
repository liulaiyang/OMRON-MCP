package stcheck

import (
	"sort"
	"strings"
)

type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
)

// Diagnostic is one finding. Line/Column are 1-based within the checked text.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Line     int      `json:"line"`
	Column   int      `json:"column"`
	Message  string   `json:"message"`
	Source   string   `json:"source,omitempty"` // the source line, trimmed
}

// ---------- types ----------

type typeKind int

const (
	tkUnknown typeKind = iota
	tkBasic
	tkString
	tkArray
	tkNamed
)

// TypeRef is a parsed type expression such as "ARRAY[0..3] OF REAL".
type TypeRef struct {
	kind typeKind
	name string // basic: upper name; named: as written (namespace allowed)
	elem *TypeRef
	dims int
	text string
}

var basicTypes = map[string]bool{
	"BOOL": true, "BYTE": true, "WORD": true, "DWORD": true, "LWORD": true,
	"SINT": true, "INT": true, "DINT": true, "LINT": true,
	"USINT": true, "UINT": true, "UDINT": true, "ULINT": true,
	"REAL": true, "LREAL": true, "TIME": true, "DATE": true, "TIME_OF_DAY": true, "TOD": true,
	"DATE_AND_TIME": true, "DT": true, "LTIME": true,
}

var bitStringTypes = map[string]int{"BYTE": 8, "WORD": 16, "DWORD": 32, "LWORD": 64,
	"SINT": 8, "INT": 16, "DINT": 32, "LINT": 64, "USINT": 8, "UINT": 16, "UDINT": 32, "ULINT": 64}

func isIntegerType(name string) bool {
	switch name {
	case "SINT", "INT", "DINT", "LINT", "USINT", "UINT", "UDINT", "ULINT":
		return true
	}
	return false
}

// ParseTypeRef parses a Sysmac type text. Unknown syntax yields kind unknown.
func ParseTypeRef(text string) *TypeRef {
	toks := simpleTokens(text)
	i := 0
	t := parseTypeTokens(toks, &i)
	if t == nil {
		return &TypeRef{kind: tkUnknown, text: text}
	}
	t.text = strings.TrimSpace(text)
	return t
}

func parseTypeTokens(toks []string, i *int) *TypeRef {
	if *i >= len(toks) {
		return nil
	}
	w := toks[*i]
	up := strings.ToUpper(w)
	switch {
	case up == "ARRAY":
		*i++
		dims := 1
		if *i < len(toks) && toks[*i] == "[" {
			depth := 0
			for ; *i < len(toks); *i++ {
				switch toks[*i] {
				case "[":
					depth++
				case "]":
					depth--
				case ",":
					if depth == 1 {
						dims++
					}
				}
				if depth == 0 {
					*i++
					break
				}
			}
		}
		if *i < len(toks) && strings.ToUpper(toks[*i]) == "OF" {
			*i++
		}
		elem := parseTypeTokens(toks, i)
		if elem == nil {
			elem = &TypeRef{kind: tkUnknown}
		}
		return &TypeRef{kind: tkArray, elem: elem, dims: dims}
	case up == "STRING" || up == "WSTRING":
		*i++
		if *i < len(toks) && toks[*i] == "[" {
			for *i < len(toks) && toks[*i] != "]" {
				*i++
			}
			*i++
		}
		return &TypeRef{kind: tkString, name: up}
	case up == "POINTER" || up == "REFERENCE" || up == "REF_TO":
		*i++
		if *i < len(toks) && strings.ToUpper(toks[*i]) == "TO" {
			*i++
		}
		return parseTypeTokens(toks, i)
	case w == "(":
		for *i < len(toks) && toks[*i] != ")" {
			*i++
		}
		*i++
		return &TypeRef{kind: tkUnknown}
	case basicTypes[up]:
		*i++
		return &TypeRef{kind: tkBasic, name: up}
	case up == "VOID" || strings.HasPrefix(up, "__ANY") || strings.HasPrefix(up, "ANY"):
		*i++
		return &TypeRef{kind: tkUnknown, name: up}
	default:
		if isWordToken(w) {
			*i++
			return &TypeRef{kind: tkNamed, name: w}
		}
	}
	return nil
}

func isWordToken(w string) bool {
	if w == "" {
		return false
	}
	r := []rune(w)[0]
	return r == '_' || r == '\\' || isIdentStart(r)
}

// simpleTokens splits declaration text into words and symbols.
func simpleTokens(s string) []string {
	var out []string
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(r) && r[j] != c {
				if r[j] == '$' {
					j++
				}
				j++
			}
			if j < len(r) {
				j++
			}
			out = append(out, string(r[i:j]))
			i = j
		case isIdentPart(c) || c == '\\' || c == '#':
			j := i
			for j < len(r) && (isIdentPart(r[j]) || r[j] == '\\' || r[j] == '#' || (r[j] == '.' && j+1 < len(r) && isIdentPart(r[j+1]) && j > i && isDigitRune(r[j-1]) && isDigitRune(r[j+1]))) {
				j++
			}
			out = append(out, string(r[i:j]))
			i = j
		default:
			if i+1 < len(r) {
				two := string(r[i : i+2])
				if two == ":=" || two == ".." || two == "=>" {
					out = append(out, two)
					i += 2
					continue
				}
			}
			out = append(out, string(c))
			i++
		}
	}
	return out
}

func isDigitRune(r rune) bool { return r >= '0' && r <= '9' }

// shortName returns the last namespace segment: "Standard\Axis\ST_Axis" -> "ST_Axis".
func shortName(n string) string {
	n = strings.TrimSpace(n)
	if i := strings.LastIndex(n, "\\"); i >= 0 {
		return n[i+1:]
	}
	return n
}

// ---------- symbols ----------

// Var is a declared variable / parameter / struct member.
type Var struct {
	Name     string
	TypeText string
	Type     *TypeRef
	Group    string // VAR, VAR_INPUT, VAR_OUTPUT, VAR_IN_OUT, VAR_EXTERNAL, VAR_GLOBAL, VAR_TEMP, RETURN, MEMBER
	Const    bool
	ReadOnly bool
	Order    int
}

type TypeDefKind int

const (
	TDStruct TypeDefKind = iota
	TDUnion
	TDEnum
	TDAlias
)

type TypeDef struct {
	Name     string // short
	FullName string // with namespace
	Kind     TypeDefKind
	Members  []*Var
	memberIx map[string]*Var
	Enums    []string
	Alias    *TypeRef
	System   bool
}

func (t *TypeDef) member(name string) *Var {
	if t.memberIx == nil {
		t.memberIx = map[string]*Var{}
		for _, m := range t.Members {
			t.memberIx[strings.ToUpper(m.Name)] = m
		}
	}
	return t.memberIx[strings.ToUpper(name)]
}

type POUKind int

const (
	KindProgram POUKind = iota
	KindFB
	KindFunction
)

func (k POUKind) String() string {
	switch k {
	case KindFB:
		return "功能块"
	case KindFunction:
		return "函数"
	}
	return "程序"
}

type POU struct {
	Name    string
	Kind    POUKind
	Vars    []*Var
	varIx   map[string]*Var
	Return  *TypeRef
	Generic bool // system generic function (ANY parameters): only the name is checked
	Opaque  bool // interface unknown (variable table unreadable): nothing inside is checked
	System  bool
}

func (p *POU) Var(name string) *Var {
	if p.varIx == nil {
		p.varIx = map[string]*Var{}
		for _, v := range p.Vars {
			k := strings.ToUpper(v.Name)
			if _, ok := p.varIx[k]; !ok {
				p.varIx[k] = v
			}
		}
	}
	return p.varIx[strings.ToUpper(name)]
}

// Params returns interface variables in declaration order.
func (p *POU) Params() []*Var {
	var out []*Var
	for _, v := range p.Vars {
		switch v.Group {
		case "VAR_INPUT", "VAR_OUTPUT", "VAR_IN_OUT":
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

func (p *POU) AddVar(v *Var) {
	if v.Type == nil {
		v.Type = ParseTypeRef(v.TypeText)
	}
	p.Vars = append(p.Vars, v)
	p.varIx = nil
}

// Env holds everything a check can resolve against.
type Env struct {
	types      map[string]*TypeDef   // upper full name
	typeShort  map[string][]*TypeDef // upper short name
	pous       map[string]*POU       // upper short name
	globals    map[string]*Var
	enumVals   map[string][]*TypeDef
	SystemLoad []string // human readable notes about what was loaded
}

func NewEnv() *Env {
	return &Env{types: map[string]*TypeDef{}, typeShort: map[string][]*TypeDef{}, pous: map[string]*POU{}, globals: map[string]*Var{}, enumVals: map[string][]*TypeDef{}}
}

func (e *Env) AddType(t *TypeDef) {
	if t.FullName == "" {
		t.FullName = t.Name
	}
	full := strings.ToUpper(t.FullName)
	if old, ok := e.types[full]; ok && !old.System && t.System {
		return // project definition wins over system
	}
	e.types[full] = t
	short := strings.ToUpper(t.Name)
	list := e.typeShort[short][:0:0]
	for _, x := range e.typeShort[short] {
		if !strings.EqualFold(x.FullName, t.FullName) {
			list = append(list, x)
		}
	}
	e.typeShort[short] = append(list, t)
	if t.Kind == TDEnum {
		for _, v := range t.Enums {
			k := strings.ToUpper(v)
			e.enumVals[k] = append(e.enumVals[k], t)
		}
	}
}

func (e *Env) AddPOU(p *POU) {
	k := strings.ToUpper(shortName(p.Name))
	if old, ok := e.pous[k]; ok && !old.System && p.System {
		return
	}
	e.pous[k] = p
}

func (e *Env) AddGlobal(v *Var) {
	if v.Type == nil {
		v.Type = ParseTypeRef(v.TypeText)
	}
	e.globals[strings.ToUpper(v.Name)] = v
}

func (e *Env) POU(name string) *POU { return e.pous[strings.ToUpper(shortName(name))] }

// lookupType resolves a named type; full namespace first, then short name.
func (e *Env) lookupType(name string) *TypeDef {
	n := strings.TrimLeft(strings.TrimSpace(name), "\\")
	if t, ok := e.types[strings.ToUpper(n)]; ok {
		return t
	}
	list := e.typeShort[strings.ToUpper(shortName(n))]
	if len(list) == 0 {
		return nil
	}
	// prefer a definition whose namespace matches the written prefix
	for _, t := range list {
		if strings.HasSuffix(strings.ToUpper(t.FullName), strings.ToUpper(n)) {
			return t
		}
	}
	return list[0]
}

// Counts returns a summary used in tool output.
func (e *Env) Counts() map[string]int {
	sys, user := 0, 0
	for _, p := range e.pous {
		if p.System {
			sys++
		} else {
			user++
		}
	}
	st, ut := 0, 0
	for _, t := range e.types {
		if t.System {
			st++
		} else {
			ut++
		}
	}
	return map[string]int{"system_pous": sys, "project_pous": user, "system_types": st, "project_types": ut, "globals": len(e.globals)}
}
