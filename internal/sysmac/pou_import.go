package sysmac

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// POUImportRequest copies one POU subtree (FunctionBlock, Function or Program
// with all child entities and payload files) from a source Solution — typically
// a library project — into a group of a target Solution.
//
// Like CloneProgram it gives every entity new IDs, remaps every old ID (dashed
// and 32-hex compact form) inside the copied index fragment and payload files,
// and splices the fragment into the target .oem as text so the rest of the
// target index stays byte-for-byte identical. The source is only read.
type POUImportRequest struct {
	SourceProjectPath string
	SourceID          string
	TargetProjectPath string
	TargetParentID    string
	ProtectedRoot     string
}

type POUImportResult struct {
	NewEntityID   string            `json:"new_entity_id"`
	Name          string            `json:"name"`
	Type          string            `json:"type"`
	OEMPath       string            `json:"oem_path"`
	OEMBackupPath string            `json:"oem_backup_path"`
	CreatedFiles  []string          `json:"created_files"`
	IDMap         map[string]string `json:"id_map"`
}

var pouTypes = map[string]bool{"FunctionBlock": true, "Function": true, "Program": true}

func ImportPOU(request POUImportRequest) (*POUImportResult, error) {
	sourceRoot, err := filepath.Abs(request.SourceProjectPath)
	if err != nil {
		return nil, err
	}
	targetRoot, err := filepath.Abs(request.TargetProjectPath)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(sourceRoot, targetRoot) {
		return nil, errors.New("source and target Solution are the same; use sysmac_program_clone")
	}
	if request.ProtectedRoot != "" {
		protected, err := filepath.Abs(request.ProtectedRoot)
		if err != nil {
			return nil, err
		}
		if isWithin(protected, targetRoot) {
			return nil, errors.New("refusing write inside protected Sysmac project root")
		}
	}
	if applicationLocked(targetRoot) {
		return nil, errors.New("the target Solution is open in Sysmac Studio (.applicationlock found); close it first")
	}

	sourceOEM, err := findOEM(sourceRoot)
	if err != nil {
		return nil, err
	}
	sourceData, err := os.ReadFile(sourceOEM)
	if err != nil {
		return nil, fmt.Errorf("read source OEM index: %w", err)
	}
	sourceEntities, err := scanEntities(sourceData)
	if err != nil {
		return nil, err
	}
	sourceIndex := findEntityIndex(sourceEntities, request.SourceID)
	if sourceIndex < 0 {
		return nil, fmt.Errorf("source entity %q not found", request.SourceID)
	}
	source := sourceEntities[sourceIndex]
	if !pouTypes[source.typ] {
		return nil, fmt.Errorf("source entity %q is %s; only FunctionBlock, Function and Program can be imported", request.SourceID, source.typ)
	}

	targetOEM, err := findOEM(targetRoot)
	if err != nil {
		return nil, err
	}
	targetData, err := os.ReadFile(targetOEM)
	if err != nil {
		return nil, fmt.Errorf("read target OEM index: %w", err)
	}
	targetEntities, err := scanEntities(targetData)
	if err != nil {
		return nil, err
	}
	parentIndex := findEntityIndex(targetEntities, request.TargetParentID)
	if parentIndex < 0 {
		return nil, fmt.Errorf("target parent %q not found", request.TargetParentID)
	}
	for _, entity := range targetEntities {
		if pouTypes[entity.typ] && strings.EqualFold(entity.name, source.name) {
			return nil, fmt.Errorf("target already has a POU named %q (%s)", source.name, entity.id)
		}
	}
	lastChild := -1
	for index, entity := range targetEntities {
		if entity.parent == parentIndex {
			lastChild = index
		}
	}
	if lastChild < 0 {
		return nil, errors.New("target parent has no child entities to insert after")
	}

	// Subtree and ID map.
	var subtree []int
	for index := range sourceEntities {
		if index == sourceIndex || isUnder(sourceEntities, index, sourceIndex) {
			subtree = append(subtree, index)
		}
	}
	idMap := map[string]string{}
	mapID := func(old string) {
		if !uuidPattern.MatchString(old) || len(old) != 36 {
			return
		}
		key := strings.ToLower(old)
		if _, ok := idMap[key]; !ok {
			idMap[key] = newEntityID()
		}
	}
	for _, index := range subtree {
		entity := sourceEntities[index]
		mapID(entity.id)
		mapID(entity.trackingID)
		if entity.typ == "Inline" {
			mapID(entity.name)
		}
	}
	for _, entity := range targetEntities {
		if _, clash := idMap[strings.ToLower(entity.id)]; clash {
			// IDs are random; a clash would mean a broken generator.
			return nil, errors.New("generated ID collides with target; retry")
		}
	}

	// Fragment: remap IDs and re-indent to the target depth.
	fragment := remapIDs(append([]byte{}, sourceData[source.span.start:source.span.end]...), idMap)
	sourceNewline := detectNewline(sourceData)
	targetNewline := detectNewline(targetData)
	sourceIndent := lineIndent(sourceData, source.span.start)
	targetIndent := lineIndent(targetData, targetEntities[lastChild].span.start)
	fragment = bytes.ReplaceAll(fragment,
		append(append([]byte{}, sourceNewline...), sourceIndent...),
		append(append([]byte{}, targetNewline...), targetIndent...))
	if !bytes.Equal(sourceNewline, targetNewline) {
		fragment = bytes.ReplaceAll(fragment, sourceNewline, targetNewline)
	}

	var updated bytes.Buffer
	insertAt := targetEntities[lastChild].span.end
	updated.Write(targetData[:insertAt])
	updated.Write(targetNewline)
	updated.Write(targetIndent)
	updated.Write(fragment)
	updated.Write(targetData[insertAt:])

	check, err := scanEntities(updated.Bytes())
	if err != nil {
		return nil, fmt.Errorf("imported OEM index does not parse: %w", err)
	}
	if len(check) != len(targetEntities)+len(subtree) {
		return nil, fmt.Errorf("unexpected entity count after import: %d, want %d", len(check), len(targetEntities)+len(subtree))
	}
	seen := map[string]bool{}
	for _, entity := range check {
		key := strings.ToLower(entity.id)
		if seen[key] {
			return nil, fmt.Errorf("duplicate entity id %s after import", entity.id)
		}
		seen[key] = true
	}

	// Plan payload copies.
	sourceDir := filepath.Dir(sourceOEM)
	targetDir := filepath.Dir(targetOEM)
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return nil, err
	}
	type plannedFile struct{ from, to string }
	var planned []plannedFile
	for _, index := range subtree {
		oldID := sourceEntities[index].id
		for _, entry := range entries {
			name := entry.Name()
			lower := strings.ToLower(name)
			if entry.IsDir() || !strings.HasPrefix(lower, strings.ToLower(oldID)+".") || strings.HasSuffix(lower, ".backup") {
				continue
			}
			planned = append(planned, plannedFile{filepath.Join(sourceDir, name), filepath.Join(targetDir, idMap[strings.ToLower(oldID)]+name[len(oldID):])})
		}
	}

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
			data = remapIDs(data, idMap)
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

	backup := targetOEM + ".backup"
	if err := os.WriteFile(backup, targetData, 0600); err != nil {
		rollback()
		return nil, err
	}
	tmp, err := os.CreateTemp(targetDir, ".sysmac-oem-*.tmp")
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
	if err := os.Rename(tmpPath, targetOEM); err != nil {
		rollback()
		return nil, err
	}
	return &POUImportResult{
		NewEntityID:   idMap[strings.ToLower(source.id)],
		Name:          source.name,
		Type:          source.typ,
		OEMPath:       targetOEM,
		OEMBackupPath: backup,
		CreatedFiles:  created,
		IDMap:         idMap,
	}, nil
}
