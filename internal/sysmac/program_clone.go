package sysmac

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ProgramCloneRequest describes a deep copy of a POU subtree (for example a
// multi-section ST program) inside one native Sysmac Solution directory.
//
// Unlike the generic OEM "clone" mutation, a deep clone
//   - copies every payload file of every entity in the subtree (<id>.xml,
//     <id>.BookmarkGroup, ...) to the new entity IDs,
//   - gives every entity a new id and trackingId,
//   - remaps every old ID that appears inside the copied subtree and its payload
//     files, so the "EID" of each JSON section body and the name/trackingId of
//     its "Inline StructuredText" child entity stay in sync,
//   - splices the new subtree into the .oem index as text, so the rest of the
//     index (BOM, XML declaration, CRLF, formatting) is preserved byte-for-byte,
//   - keeps multi-section JSON bodies ending in "}\r\n".
type ProgramCloneRequest struct {
	ProjectPath     string
	SourceID        string
	NewName         string
	InsertAfterID   string
	ExcludeSections []string
	ProtectedRoot   string
	Now             time.Time
}

type ProgramCloneResult struct {
	NewEntityID      string            `json:"new_entity_id"`
	NewName          string            `json:"new_name"`
	OEMPath          string            `json:"oem_path"`
	OEMBackupPath    string            `json:"oem_backup_path"`
	CreatedFiles     []string          `json:"created_files"`
	Sections         []ClonedSection   `json:"sections"`
	ExcludedSections []string          `json:"excluded_sections,omitempty"`
	IDMap            map[string]string `json:"id_map"`
}

type ClonedSection struct {
	Name     string `json:"name"`
	EntityID string `json:"entity_id"`
	InlineID string `json:"inline_entity_id,omitempty"`
	EID      string `json:"eid,omitempty"`
}

var uuidPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// compactUUIDPattern matches a UUID written without dashes (Sysmac uses this
// form for build artefact names such as "FB_<trackingId>").
var compactUUIDPattern = regexp.MustCompile(`[0-9a-fA-F]{32}`)

// remapIDs replaces every mapped UUID in data, both in dashed form and in the
// 32-hex-digit compact form, keeping the letter case of the compact form.
func remapIDs(data []byte, idMap map[string]string) []byte {
	data = uuidPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		if replacement, ok := idMap[strings.ToLower(string(match))]; ok {
			return []byte(replacement)
		}
		return match
	})
	compact := make(map[string]string, len(idMap))
	for old, replacement := range idMap {
		compact[strings.ReplaceAll(old, "-", "")] = strings.ReplaceAll(replacement, "-", "")
	}
	return compactUUIDPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		replacement, ok := compact[strings.ToLower(string(match))]
		if !ok {
			return match
		}
		if bytes.ToUpper(match)[0] == match[0] && bytes.Equal(match, bytes.ToUpper(match)) {
			return []byte(strings.ToUpper(replacement))
		}
		return []byte(replacement)
	})
}

const sysmacDateLayout = "01/02/2006 15:04:05"

type byteSpan struct{ start, end int }

type fragEntity struct {
	span       byteSpan
	depth      int
	typ        string
	subtype    string
	id         string
	name       string
	trackingID string
	parent     int
}

// scanEntities returns every <Entity> element of data with its exact byte span.
func scanEntities(data []byte) ([]fragEntity, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var result []fragEntity
	var stack []int
	for {
		offset := int(decoder.InputOffset())
		token, err := decoder.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse OEM index: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local != "Entity" {
				stack = append(stack, -1)
				continue
			}
			parent := -1
			for index := len(stack) - 1; index >= 0; index-- {
				if stack[index] >= 0 {
					parent = stack[index]
					break
				}
			}
			entity := fragEntity{span: byteSpan{start: offset}, depth: len(stack), parent: parent}
			for _, item := range value.Attr {
				switch item.Name.Local {
				case "type":
					entity.typ = item.Value
				case "subtype":
					entity.subtype = item.Value
				case "id":
					entity.id = item.Value
				case "name":
					entity.name = item.Value
				case "trackingId":
					entity.trackingID = item.Value
				}
			}
			result = append(result, entity)
			stack = append(stack, len(result)-1)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, errors.New("invalid OEM XML nesting")
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top >= 0 {
				result[top].span.end = int(decoder.InputOffset())
			}
		}
	}
	if len(stack) != 0 {
		return nil, errors.New("invalid OEM XML nesting")
	}
	return result, nil
}

func findEntityIndex(entities []fragEntity, id string) int {
	for index, entity := range entities {
		if strings.EqualFold(entity.id, id) {
			return index
		}
	}
	return -1
}

func isUnder(entities []fragEntity, index, ancestor int) bool {
	for current := entities[index].parent; current >= 0; current = entities[current].parent {
		if current == ancestor {
			return true
		}
	}
	return false
}

// lineIndent returns the whitespace between the previous line break and pos.
func lineIndent(data []byte, pos int) []byte {
	start := pos
	for start > 0 && (data[start-1] == ' ' || data[start-1] == '\t') {
		start--
	}
	return data[start:pos]
}

func detectNewline(data []byte) []byte {
	if bytes.Contains(data, []byte("\r\n")) {
		return []byte("\r\n")
	}
	return []byte("\n")
}

func applicationLocked(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if strings.EqualFold(filepath.Ext(entry.Name()), ".applicationlock") {
			return true
		}
	}
	return false
}

func replaceAttrInStartTag(fragment []byte, name, value string) []byte {
	end := bytes.IndexByte(fragment, '>')
	if end < 0 {
		return fragment
	}
	tag := fragment[:end]
	pattern := regexp.MustCompile(`(\s` + regexp.QuoteMeta(name) + `=")[^"]*(")`)
	escaped := new(bytes.Buffer)
	_ = xml.EscapeText(escaped, []byte(value))
	replacement := []byte("${1}" + strings.ReplaceAll(escaped.String(), "$", "$$") + "${2}")
	newTag := pattern.ReplaceAll(tag, replacement)
	out := append([]byte{}, newTag...)
	return append(out, fragment[end:]...)
}

// CloneProgram performs the deep clone described on ProgramCloneRequest.
func CloneProgram(request ProgramCloneRequest) (*ProgramCloneResult, error) {
	if strings.TrimSpace(request.NewName) == "" {
		return nil, errors.New("new program name is required")
	}
	root, err := filepath.Abs(request.ProjectPath)
	if err != nil {
		return nil, err
	}
	if request.ProtectedRoot != "" {
		protected, err := filepath.Abs(request.ProtectedRoot)
		if err != nil {
			return nil, err
		}
		if isWithin(protected, root) {
			return nil, errors.New("refusing write inside protected Sysmac project root")
		}
	}
	if applicationLocked(root) {
		return nil, errors.New("the Solution is open in Sysmac Studio (.applicationlock found); close it first")
	}
	oemPath, err := findOEM(root)
	if err != nil {
		return nil, err
	}
	original, err := os.ReadFile(oemPath)
	if err != nil {
		return nil, fmt.Errorf("read OEM index: %w", err)
	}
	entities, err := scanEntities(original)
	if err != nil {
		return nil, err
	}
	sourceIndex := findEntityIndex(entities, request.SourceID)
	if sourceIndex < 0 {
		return nil, fmt.Errorf("source entity %q not found", request.SourceID)
	}
	source := entities[sourceIndex]
	if source.parent < 0 {
		return nil, errors.New("the Solution root cannot be cloned")
	}
	for index, entity := range entities {
		if entity.parent == source.parent && index != sourceIndex && entity.name == request.NewName {
			return nil, fmt.Errorf("an entity named %q already exists next to the source", request.NewName)
		}
	}
	if source.name == request.NewName {
		return nil, fmt.Errorf("an entity named %q already exists next to the source", request.NewName)
	}
	afterIndex := sourceIndex
	if request.InsertAfterID != "" {
		afterIndex = findEntityIndex(entities, request.InsertAfterID)
		if afterIndex < 0 {
			return nil, fmt.Errorf("insert_after entity %q not found", request.InsertAfterID)
		}
		if entities[afterIndex].parent != source.parent {
			return nil, errors.New("insert_after entity must be a sibling of the source")
		}
	}

	// Collect the subtree, minus excluded direct-child sections.
	exclude := map[string]bool{}
	for _, name := range request.ExcludeSections {
		if name = strings.TrimSpace(name); name != "" {
			exclude[name] = true
		}
	}
	excludedRoots := map[int]bool{}
	var excludedNames []string
	for index, entity := range entities {
		if entity.parent == sourceIndex && entity.typ == "PouBody" && exclude[entity.name] {
			excludedRoots[index] = true
			excludedNames = append(excludedNames, entity.name)
		}
	}
	if len(excludedNames) != len(exclude) {
		found := map[string]bool{}
		for _, name := range excludedNames {
			found[name] = true
		}
		for name := range exclude {
			if !found[name] {
				return nil, fmt.Errorf("section %q not found in source", name)
			}
		}
	}
	var kept []int
	for index := range entities {
		if index != sourceIndex && !isUnder(entities, index, sourceIndex) {
			continue
		}
		skip := excludedRoots[index]
		for excludedRoot := range excludedRoots {
			if isUnder(entities, index, excludedRoot) {
				skip = true
			}
		}
		if !skip {
			kept = append(kept, index)
		}
	}

	// Build the fragment text without excluded sections (and their indentation line).
	fragment := append([]byte{}, original[source.span.start:source.span.end]...)
	type cut struct{ start, end int }
	var cuts []cut
	for excludedRoot := range excludedRoots {
		span := entities[excludedRoot].span
		start := span.start - len(lineIndent(original, span.start))
		// also remove the preceding line break so no blank line remains
		if start >= 2 && original[start-2] == '\r' && original[start-1] == '\n' {
			start -= 2
		} else if start >= 1 && original[start-1] == '\n' {
			start--
		}
		cuts = append(cuts, cut{start - source.span.start, span.end - source.span.start})
	}
	for i := 0; i < len(cuts); i++ {
		for j := i + 1; j < len(cuts); j++ {
			if cuts[j].start > cuts[i].start {
				cuts[i], cuts[j] = cuts[j], cuts[i]
			}
		}
	}
	for _, c := range cuts {
		fragment = append(fragment[:c.start], fragment[c.end:]...)
	}

	// New IDs for every id / trackingId / UUID-named Inline entity in the kept subtree.
	idMap := map[string]string{}
	mapID := func(old string) {
		if old == "" || !uuidPattern.MatchString(old) {
			return
		}
		key := strings.ToLower(old)
		if _, ok := idMap[key]; !ok {
			idMap[key] = newEntityID()
		}
	}
	for _, index := range kept {
		entity := entities[index]
		mapID(entity.id)
		mapID(entity.trackingID)
		if entity.typ == "Inline" && uuidPattern.MatchString(entity.name) && len(entity.name) == 36 {
			mapID(entity.name)
		}
	}
	remap := func(data []byte) []byte { return remapIDs(data, idMap) }
	fragment = remap(fragment)
	fragment = replaceAttrInStartTag(fragment, "name", request.NewName)
	if bytes.Contains(fragment[:bytes.IndexByte(fragment, '>')], []byte(` DN="`)) {
		fragment = replaceAttrInStartTag(fragment, "DN", request.NewName)
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.Format(sysmacDateLayout)
	fragment = regexp.MustCompile(`(\sdate(?:Created|LastModified)=")[^"]*(")`).ReplaceAll(fragment, []byte("${1}"+stamp+"${2}"))

	// Plan payload file copies.
	dirEntries, err := os.ReadDir(filepath.Dir(oemPath))
	if err != nil {
		return nil, err
	}
	type plannedFile struct{ from, to string }
	var planned []plannedFile
	for _, index := range kept {
		oldID := entities[index].id
		for _, entry := range dirEntries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasPrefix(strings.ToLower(name), strings.ToLower(oldID)+".") || strings.HasSuffix(strings.ToLower(name), ".backup") {
				continue
			}
			newName := idMap[strings.ToLower(oldID)] + name[len(oldID):]
			planned = append(planned, plannedFile{filepath.Join(filepath.Dir(oemPath), name), filepath.Join(filepath.Dir(oemPath), newName)})
		}
	}

	// Splice the new subtree after the insert-after sibling.
	after := entities[afterIndex]
	newline := detectNewline(original)
	indent := lineIndent(original, source.span.start)
	var updated bytes.Buffer
	updated.Write(original[:after.span.end])
	updated.Write(newline)
	updated.Write(indent)
	updated.Write(fragment)
	updated.Write(original[after.span.end:])

	// Validate the new index before touching any file.
	check, err := scanEntities(updated.Bytes())
	if err != nil {
		return nil, fmt.Errorf("cloned OEM index does not parse: %w", err)
	}
	seen := map[string]bool{}
	for _, entity := range check {
		key := strings.ToLower(entity.id)
		if seen[key] {
			return nil, fmt.Errorf("duplicate entity id %s after clone", entity.id)
		}
		seen[key] = true
	}
	if len(check) != len(entities)+len(kept) {
		return nil, fmt.Errorf("unexpected entity count after clone: %d, want %d", len(check), len(entities)+len(kept))
	}

	// Write payload files (never overwrite), then the index.
	var created []string
	rollback := func() {
		for _, path := range created {
			_ = os.Remove(path)
		}
	}
	for _, file := range planned {
		if _, err := os.Stat(file.to); err == nil {
			rollback()
			return nil, fmt.Errorf("target payload %s already exists", file.to)
		}
		data, err := os.ReadFile(file.from)
		if err != nil {
			rollback()
			return nil, err
		}
		if utf8.Valid(bytes.TrimPrefix(data, utf8BOM)) {
			data = remap(data)
			if isJSONPayload(data) {
				if data, err = normalizeLadderJSONPayload(data, data); err != nil {
					rollback()
					return nil, fmt.Errorf("%s: %w", file.from, err)
				}
			}
		}
		if err := os.WriteFile(file.to, data, 0644); err != nil {
			rollback()
			return nil, err
		}
		created = append(created, file.to)
	}
	backup := oemPath + ".backup"
	if err := os.WriteFile(backup, original, 0600); err != nil {
		rollback()
		return nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(oemPath), ".sysmac-oem-*.tmp")
	if err != nil {
		rollback()
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(updated.Bytes()); err != nil {
		tmp.Close()
		rollback()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		rollback()
		return nil, err
	}
	if err := os.Rename(tmpPath, oemPath); err != nil {
		rollback()
		return nil, err
	}

	result := &ProgramCloneResult{
		NewEntityID:      idMap[strings.ToLower(source.id)],
		NewName:          request.NewName,
		OEMPath:          oemPath,
		OEMBackupPath:    backup,
		CreatedFiles:     created,
		ExcludedSections: excludedNames,
		IDMap:            idMap,
	}
	for _, index := range kept {
		entity := entities[index]
		if entity.parent != sourceIndex || entity.typ != "PouBody" {
			continue
		}
		section := ClonedSection{Name: entity.name, EntityID: idMap[strings.ToLower(entity.id)]}
		for _, child := range kept {
			if entities[child].parent == index && entities[child].typ == "Inline" {
				section.InlineID = idMap[strings.ToLower(entities[child].id)]
				section.EID = idMap[strings.ToLower(entities[child].name)]
			}
		}
		result.Sections = append(result.Sections, section)
	}
	return result, nil
}
