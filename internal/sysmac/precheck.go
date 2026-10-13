package sysmac

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rjboer/omron-mcp/internal/stcheck"
)

// PrecheckVariable is a variable the caller is about to add (checked as if it
// were already in the POU's variable table).
type PrecheckVariable struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Group string `json:"group,omitempty"` // VAR (default), VAR_INPUT, VAR_OUTPUT, VAR_IN_OUT, VAR_EXTERNAL
}

type PrecheckRequest struct {
	ProjectPath string
	// EntityID: a POU (program / FB / function), one of its bodies or a
	// multi-section ST section. Empty = check every ST body in the project.
	EntityID string
	// Code: proposed source to check instead of what is on disk (requires an
	// EntityID that resolves to exactly one body, or to an FB / function).
	Code           string
	ExtraVariables []PrecheckVariable
	StudioPath     string
	// WarnExternal reports globals not registered as external variables.
	WarnExternal bool
}

type PrecheckBody struct {
	EntityID    string               `json:"entity_id"`
	POU         string               `json:"pou"`
	Section     string               `json:"section,omitempty"`
	Source      string               `json:"source"` // disk | proposed
	Errors      int                  `json:"errors"`
	Warnings    int                  `json:"warnings"`
	Diagnostics []stcheck.Diagnostic `json:"diagnostics,omitempty"`
}

type PrecheckResult struct {
	Status      string         `json:"status"` // ok | errors | warnings
	Errors      int            `json:"errors"`
	Warnings    int            `json:"warnings"`
	Checked     int            `json:"checked_bodies"`
	Bodies      []PrecheckBody `json:"bodies"`
	Environment map[string]int `json:"environment"`
	CxifDir     string         `json:"cxif_dir,omitempty"`
	Notes       []string       `json:"notes,omitempty"`
}

type pouInfo struct {
	pou    *stcheck.POU
	id     string
	bodies []bodyInfo
}

type bodyInfo struct {
	id      string
	section string
	kind    string // st | section | ladder
}

type precheckProject struct {
	root   string
	env    *stcheck.Env
	pous   []*pouInfo
	byID   map[string]*pouInfo // POU id and body ids -> POU
	bodyBy map[string]bodyInfo
	notes  []string
	cxif   string
}

// Precheck runs the offline Structured Text pre-check.
func Precheck(req PrecheckRequest) (*PrecheckResult, error) {
	proj, err := loadPrecheckProject(req.ProjectPath, req.StudioPath)
	if err != nil {
		return nil, err
	}
	res := &PrecheckResult{Environment: proj.env.Counts(), CxifDir: proj.cxif, Notes: proj.notes}

	type job struct {
		info *pouInfo
		body bodyInfo
		code string
		src  string
	}
	var jobs []job
	switch {
	case req.EntityID == "":
		if req.Code != "" {
			return nil, errors.New("code requires entity_id (the body, section or FB / function it is meant for)")
		}
		for _, pi := range proj.pous {
			for _, b := range pi.bodies {
				jobs = append(jobs, job{info: pi, body: b})
			}
		}
	default:
		id := strings.ToLower(req.EntityID)
		pi := proj.byID[id]
		if pi == nil {
			return nil, fmt.Errorf("entity %q is not a program, FB, function, ST body or multi-section ST section of this project", req.EntityID)
		}
		if b, ok := proj.bodyBy[id]; ok {
			jobs = append(jobs, job{info: pi, body: b})
		} else if req.Code != "" && pi.pou.Kind != stcheck.KindProgram {
			b := bodyInfo{id: pi.id, kind: "st"}
			if len(pi.bodies) == 1 {
				b = pi.bodies[0]
			}
			jobs = append(jobs, job{info: pi, body: b})
		} else if req.Code != "" {
			return nil, errors.New("for a multi-section program pass the section entity id together with code")
		} else {
			for _, b := range pi.bodies {
				jobs = append(jobs, job{info: pi, body: b})
			}
		}
		if req.Code != "" {
			jobs[0].code = sectionCodeText(req.Code)
		}
	}

	for _, j := range jobs {
		pou := j.info.pou
		if len(req.ExtraVariables) > 0 && req.EntityID != "" {
			pou = clonePOU(pou)
			for i, v := range req.ExtraVariables {
				g := strings.ToUpper(strings.TrimSpace(v.Group))
				if g == "" {
					g = "VAR"
				}
				pou.AddVar(&stcheck.Var{Name: v.Name, TypeText: v.Type, Group: g, Order: 1000 + i})
			}
		}
		text := j.code
		src := "proposed"
		if text == "" {
			src = "disk"
			if j.body.kind == "ladder" {
				continue
			}
			t, err := readBodyText(proj.root, j.body)
			if err != nil {
				res.Notes = append(res.Notes, fmt.Sprintf("%s/%s: %v", pou.Name, j.body.section, err))
				continue
			}
			text = t
		}
		r := proj.env.Check(text, stcheck.Options{POU: pou, WarnGlobalsNotExternal: req.WarnExternal})
		res.Checked++
		res.Errors += r.Errors
		res.Warnings += r.Warnings
		res.Bodies = append(res.Bodies, PrecheckBody{EntityID: j.body.id, POU: pou.Name, Section: j.body.section, Source: src, Errors: r.Errors, Warnings: r.Warnings, Diagnostics: r.Diagnostics})
	}
	sort.SliceStable(res.Bodies, func(a, b int) bool { return res.Bodies[a].Errors > res.Bodies[b].Errors })
	switch {
	case res.Errors > 0:
		res.Status = "errors"
	case res.Warnings > 0:
		res.Status = "warnings"
	default:
		res.Status = "ok"
	}
	return res, nil
}

func clonePOU(p *stcheck.POU) *stcheck.POU {
	c := &stcheck.POU{Name: p.Name, Kind: p.Kind, Return: p.Return}
	for _, v := range p.Vars {
		vv := *v
		c.AddVar(&vv)
	}
	return c
}

func loadPrecheckProject(root, studioPath string) (*precheckProject, error) {
	oemPath, err := findOEM(root)
	if err != nil {
		return nil, err
	}
	doc, err := loadOEMDocument(oemPath)
	if err != nil {
		return nil, err
	}
	proj := &precheckProject{root: root, env: stcheck.NewEnv(), byID: map[string]*pouInfo{}, bodyBy: map[string]bodyInfo{}}

	// controller device = the Device subtree holding the global variables
	var controller *oemNode
	walkEntities(doc, func(n *oemNode, ancestors []*oemNode) {
		if controller == nil && attr(n, "type") == "Variables" && attr(n, "subtype") == "Global" {
			for i := len(ancestors) - 1; i >= 0; i-- {
				if attr(ancestors[i], "type") == "Device" {
					controller = ancestors[i]
					return
				}
			}
		}
	})
	if controller == nil {
		return nil, errors.New("no controller with global variables found in the project")
	}

	proj.loadSystem(root, attr(controller, "id"), studioPath)

	walkEntities(controller, func(n *oemNode, ancestors []*oemNode) {
		typ, sub, id := attr(n, "type"), attr(n, "subtype"), attr(n, "id")
		switch {
		case typ == "DataType":
			proj.loadDataTypes(filepath.Join(root, id+".xml"), attr(n, "namespace"))
		case typ == "Variables" && sub == "Global":
			vars, err := readSLWDAll(filepath.Join(root, id+".xml"))
			if err != nil {
				proj.notes = append(proj.notes, "全局变量读取失败: "+err.Error())
				return
			}
			for _, v := range vars {
				proj.env.AddGlobal(v)
			}
		case (typ == "Program" || typ == "FunctionBlock" || typ == "Function") && (sub == "StructuredText" || sub == "MultipartLadder" || sub == "Ladder"):
			proj.loadPOU(n)
		}
	})
	for _, pi := range proj.pous {
		proj.env.AddPOU(pi.pou)
	}
	return proj, nil
}

func walkEntities(n *oemNode, fn func(n *oemNode, ancestors []*oemNode)) {
	var rec func(n *oemNode, anc []*oemNode)
	rec = func(n *oemNode, anc []*oemNode) {
		if n.Name.Local == "Entity" {
			fn(n, anc)
			anc = append(anc, n)
		}
		for _, ch := range n.Children {
			rec(ch, anc)
		}
	}
	rec(n, nil)
}

func childEntities(n *oemNode) []*oemNode {
	var out []*oemNode
	for _, ch := range n.Children {
		if ch.Name.Local == "ChildEntities" {
			for _, e := range ch.Children {
				if e.Name.Local == "Entity" {
					out = append(out, e)
				}
			}
		}
	}
	return out
}

func (p *precheckProject) loadPOU(n *oemNode) {
	kind := stcheck.KindProgram
	switch attr(n, "type") {
	case "FunctionBlock":
		kind = stcheck.KindFB
	case "Function":
		kind = stcheck.KindFunction
	}
	pi := &pouInfo{pou: &stcheck.POU{Name: attr(n, "name"), Kind: kind}, id: strings.ToLower(attr(n, "id"))}
	for _, ch := range childEntities(n) {
		typ, sub, id := attr(ch, "type"), attr(ch, "subtype"), strings.ToLower(attr(ch, "id"))
		switch {
		case typ == "Variables":
			vars, err := readSLWDAll(filepath.Join(p.root, id+".xml"))
			if err != nil {
				p.notes = append(p.notes, pi.pou.Name+" 变量表读取失败，它的参数和内部变量不做核对: "+err.Error())
				pi.pou.Opaque = true
				continue
			}
			for _, v := range vars {
				if v.Group == "RETURN" {
					pi.pou.Return = stcheck.ParseTypeRef(v.TypeText)
				}
				pi.pou.AddVar(v)
			}
		case typ == "PouBody" && sub == "StructuredText":
			pi.bodies = append(pi.bodies, bodyInfo{id: id, section: attr(ch, "name"), kind: "st"})
		case typ == "PouBody" && sub == "Ladder":
			pi.bodies = append(pi.bodies, bodyInfo{id: id, section: attr(ch, "name"), kind: "section"})
		}
	}
	p.pous = append(p.pous, pi)
	p.byID[pi.id] = pi
	for _, b := range pi.bodies {
		p.byID[b.id] = pi
		p.bodyBy[b.id] = b
	}
}

func readBodyText(root string, b bodyInfo) (string, error) {
	path := filepath.Join(root, b.id+".xml")
	if b.kind == "st" {
		return ReadStructuredText(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.TrimPrefix(string(data), "\uFEFF")
	var lad struct {
		CLs []struct {
			Type string `json:"__type"`
			TXT  string `json:"TXT"`
		} `json:"CLs"`
		CMT string `json:"CMT"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &lad); err != nil {
		return "", fmt.Errorf("不是 JSON 格式的分段（可能是真正的梯形图），跳过")
	}
	if !strings.Contains(lad.CMT, "STMultiSection") {
		return "", fmt.Errorf("不是多段 ST 分段，跳过")
	}
	var parts []string
	for _, cl := range lad.CLs {
		if cl.Type == "IST" {
			parts = append(parts, cl.TXT)
		}
	}
	return strings.Join(parts, "\r\n"), nil
}

// readSLWDAll parses an SLWD file keeping group, constant and read-only flags.
func readSLWDAll(path string) ([]*stcheck.Var, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), "\uFEFF"), "\r\n", "\n")
	var out []*stcheck.Var
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "++D=") && !strings.HasPrefix(line, "+++D=") {
			continue
		}
		fields, err := tokenizeSLWDFields(line)
		if err != nil {
			return nil, err
		}
		v := &stcheck.Var{}
		for _, f := range fields {
			k, val, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			switch strings.TrimLeft(k, "+") {
			case "D":
				v.TypeText = val
			case "N":
				v.Name = val
			case "G":
				v.Group = val
			case "Const":
				v.Const = val == "1"
			case "RO":
				v.ReadOnly = val == "1"
			case "Ord":
				v.Order, _ = strconv.Atoi(val)
			}
		}
		if v.Name == "" {
			continue
		}
		v.Type = stcheck.ParseTypeRef(v.TypeText)
		out = append(out, v)
	}
	return out, nil
}

type dtXML struct {
	Name      string  `xml:"Name,attr"`
	BaseType  string  `xml:"BaseType,attr"`
	ArrayType string  `xml:"ArrayType,attr"`
	EnumValue string  `xml:"EnumValue,attr"`
	Children  []dtXML `xml:"DataType"`
}

func (p *precheckProject) loadDataTypes(path, namespace string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // container entity without payload
	}
	var doc struct {
		Types []dtXML `xml:"DataType"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		p.notes = append(p.notes, filepath.Base(path)+" 数据类型解析失败: "+err.Error())
		return
	}
	var visit func(d dtXML)
	visit = func(d dtXML) {
		base := strings.ToUpper(d.BaseType)
		if d.BaseType == "" {
			for _, ch := range d.Children { // container node
				visit(ch)
			}
			return
		}
		full := d.Name
		if namespace != "" {
			full = namespace + "\\" + d.Name
		}
		td := &stcheck.TypeDef{Name: d.Name, FullName: full}
		switch base {
		case "STRUCT", "UNION":
			if base == "UNION" {
				td.Kind = stcheck.TDUnion
			}
			for _, m := range d.Children {
				t := m.BaseType
				if m.ArrayType != "" {
					t = "ARRAY" + m.ArrayType + " OF " + m.BaseType
				}
				td.Members = append(td.Members, &stcheck.Var{Name: m.Name, TypeText: t, Type: stcheck.ParseTypeRef(t), Group: "MEMBER"})
			}
		case "ENUM":
			td.Kind = stcheck.TDEnum
			for _, m := range d.Children {
				td.Enums = append(td.Enums, m.Name)
			}
		default:
			td.Kind = stcheck.TDAlias
			t := d.BaseType
			if d.ArrayType != "" {
				t = "ARRAY" + d.ArrayType + " OF " + d.BaseType
			}
			td.Alias = stcheck.ParseTypeRef(t)
		}
		p.env.AddType(td)
	}
	for _, d := range doc.Types {
		visit(d)
	}
}

// loadSystem registers Sysmac system types / instructions from the builder's
// CxIF interface files for the controller model and version.
func (p *precheckProject) loadSystem(root, deviceID, studioPath string) {
	studio := ResolveStudioInstall(studioPath)
	if studio == "" {
		p.notes = append(p.notes, "没找到 Sysmac Studio 安装目录，系统指令（MC_*、TON 等）无法核对，相关调用会被报为不存在；用 studio_path 参数或环境变量 "+StudioInstallEnv+" 指定")
		return
	}
	model, ver, packages := "NJ501", "", []string(nil)
	if data, err := os.ReadFile(filepath.Join(root, deviceID+".xml")); err == nil {
		var dev struct {
			Major    string `xml:"MajorVersion"`
			Minor    string `xml:"MinorVersion"`
			Model    string `xml:"ModelName"`
			Packages string `xml:"Packages"`
		}
		if xml.Unmarshal(data, &dev) == nil {
			if dev.Model != "" {
				model = dev.Model
			}
			if dev.Major != "" {
				ver = dev.Major + "." + dev.Minor
			}
			for _, pk := range strings.Split(dev.Packages, ",") {
				if f := strings.Fields(pk); len(f) > 0 {
					packages = append(packages, f...)
				}
			}
		}
	}
	base := modelCxifDir(filepath.Join(studio, "builder2", "CxIF"), model)
	dir := pickVersionDir(base, ver)
	if dir == "" {
		p.notes = append(p.notes, "没找到 "+base+" 下的系统接口文件")
		return
	}
	p.cxif = dir
	dirs := []string{dir}
	for i := 0; i+1 < len(packages); i += 2 {
		if d := pickVersionDir(filepath.Join(base, packages[i]), packages[i+1]); d != "" {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*.cxif"))
		sort.Strings(files)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			p.env.LoadCxif(string(data))
		}
	}
}

// modelCxifDir maps a controller model to its CxIF folder: the exact name if
// present, else the longest folder name that prefixes the model
// (NJ501 -> NJ, NX102 -> NX10, NX1P2 -> NX1P, NX701 -> NX7).
func modelCxifDir(root, model string) string {
	if st, err := os.Stat(filepath.Join(root, model)); err == nil && st.IsDir() {
		return filepath.Join(root, model)
	}
	entries, _ := os.ReadDir(root)
	best := ""
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(strings.ToUpper(model), strings.ToUpper(e.Name())) && len(e.Name()) > len(best) {
			best = e.Name()
		}
	}
	if best == "" {
		return filepath.Join(root, model)
	}
	return filepath.Join(root, best)
}

// pickVersionDir returns base\<ver> if present, else the highest version <= ver,
// else the highest version available.
func pickVersionDir(base, want string) string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	parse := func(s string) float64 {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return -1
		}
		return f
	}
	w := parse(want)
	best, bestV := "", -1.0
	highest, highestV := "", -1.0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		v := parse(e.Name())
		if v < 0 {
			continue
		}
		if v > highestV {
			highest, highestV = e.Name(), v
		}
		if w >= 0 && v <= w && v > bestV {
			best, bestV = e.Name(), v
		}
	}
	if best == "" {
		best = highest
	}
	if best == "" {
		return ""
	}
	return filepath.Join(base, best)
}

func stcheckOptions(pi *pouInfo) stcheck.Options {
	return stcheck.Options{POU: pi.pou}
}

// sectionCodeText accepts either plain ST or a multi-section JSON payload and
// returns the ST text.
func sectionCodeText(code string) string {
	s := strings.TrimSpace(strings.TrimPrefix(code, "\uFEFF"))
	if !strings.HasPrefix(s, "{") || !strings.Contains(s, "\"CLs\"") {
		return code
	}
	var lad struct {
		CLs []struct {
			Type string `json:"__type"`
			TXT  string `json:"TXT"`
		} `json:"CLs"`
	}
	if json.Unmarshal([]byte(s), &lad) != nil {
		return code
	}
	var parts []string
	for _, cl := range lad.CLs {
		if cl.Type == "IST" {
			parts = append(parts, cl.TXT)
		}
	}
	return strings.Join(parts, "\r\n")
}
