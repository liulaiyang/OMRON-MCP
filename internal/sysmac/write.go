package sysmac

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func WriteStructuredTextEntity(projectRoot, entityID, text, protectedRoot string) error {
	payload, err := StructuredTextEntityPayloadPath(projectRoot, entityID)
	if err != nil {
		return err
	}
	return WriteStructuredTextAtomic(payload, text, protectedRoot)
}

// StructuredTextEntityPayloadPath resolves a supported Structured Text body payload.
func StructuredTextEntityPayloadPath(projectRoot, entityID string) (string, error) {
	project, err := loadOEMProject(projectRoot)
	if err != nil {
		return "", err
	}
	entity, ok := project.EntitiesByID[entityID]
	if !ok {
		return "", fmt.Errorf("Sysmac entity %q not found", entityID)
	}
	if entity.Type != "PouBody" || entity.Subtype != "StructuredText" {
		return "", fmt.Errorf("Sysmac entity %q is %s/%s, not a Structured Text body", entityID, entity.Type, entity.Subtype)
	}
	return findEntityPayload(projectRoot, entityID)
}

// EntityPayloadPath resolves the XML payload for any indexed native entity.
func EntityPayloadPath(projectRoot, entityID string) (string, error) {
	project, err := loadOEMProject(projectRoot)
	if err != nil {
		return "", err
	}
	if _, ok := project.EntitiesByID[entityID]; !ok {
		return "", fmt.Errorf("Sysmac entity %q not found", entityID)
	}
	return findEntityPayload(projectRoot, entityID)
}

func ReadEntityPayload(projectRoot, entityID string) ([]byte, string, error) {
	path, err := EntityPayloadPath(projectRoot, entityID)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read entity payload: %w", err)
	}
	return data, path, nil
}

func WriteEntityPayloadAtomic(projectRoot, entityID string, data []byte, protectedRoot string) (string, error) {
	path, err := EntityPayloadPath(projectRoot, entityID)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if protectedRoot != "" {
		protected, err := filepath.Abs(protectedRoot)
		if err != nil {
			return "", err
		}
		if isWithin(protected, resolved) {
			return "", errors.New("refusing write inside protected Sysmac project root")
		}
	}
	original, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	data, err = prepareEntityPayload(original, data)
	if err != nil {
		return "", err
	}
	backup := resolved + ".backup"
	if err := os.WriteFile(backup, original, 0600); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), ".sysmac-payload-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, resolved); err != nil {
		return "", err
	}
	return backup, nil
}

func loadOEMProject(root string) (OEMProject, error) {
	oem, err := findOEM(root)
	if err != nil {
		return OEMProject{}, err
	}
	return ParseOEM(oem)
}

func findEntityPayload(root, entityID string) (string, error) {
	var matches []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".xml") {
			return nil
		}
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if strings.EqualFold(base, entityID) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find entity payload: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no payload file found for Sysmac entity %q", entityID)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple payload files found for Sysmac entity %q", entityID)
	}
	return matches[0], nil
}

func WriteStructuredTextAtomic(path, text, protectedRoot string) error {
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}
	if protectedRoot != "" {
		protectedPath, err := filepath.Abs(protectedRoot)
		if err != nil {
			return fmt.Errorf("resolve protected root: %w", err)
		}
		if isWithin(protectedPath, resolvedPath) {
			return errors.New("refusing write inside protected Sysmac project root")
		}
	}

	original, err := os.ReadFile(resolvedPath)
	if err != nil {
		return fmt.Errorf("read source before write: %w", err)
	}
	var document structuredTextWriteModel
	if err := xml.Unmarshal(original, &document); err != nil {
		return fmt.Errorf("parse Structured Text before write: %w", err)
	}
	document.Text = text
	updated, err := xml.Marshal(&document)
	if err != nil {
		return fmt.Errorf("marshal Structured Text: %w", err)
	}
	updated = append([]byte("<?xml version=\"1.0\" encoding=\"utf-8\"?>\r\n"), updated...)

	backupPath := resolvedPath + ".backup"
	if err := os.WriteFile(backupPath, original, 0600); err != nil {
		return fmt.Errorf("write source backup: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolvedPath), ".sysmac-write-*.tmp")
	if err != nil {
		return fmt.Errorf("create atomic write file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err := tmp.Write(updated); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary source: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("flush temporary source: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary source: %w", err)
	}
	if err := os.Rename(tmpPath, resolvedPath); err != nil {
		return fmt.Errorf("replace source atomically: %w", err)
	}
	return nil
}

type structuredTextWriteModel struct {
	XMLName xml.Name
	Text    string `xml:"Text"`
}

func isWithin(root, path string) bool {
	root = strings.TrimRight(filepath.Clean(root), string(filepath.Separator)) + string(filepath.Separator)
	path = strings.TrimRight(filepath.Clean(path), string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(root))
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ladderJSONLineEnd is the exact terminator Sysmac Studio requires for JSON
// ladder bodies. LadderBodyPersister.GetFileFormat (NexProgramming.dll) only
// treats a body as JSON when the file ends with "}\r\n"; anything else falls
// back to the legacy parser, which fails and shows an empty ladder rung. This
// affects every FakeMultiSectionST ("multi-section ST") section.
var ladderJSONLineEnd = []byte("\r\n")

// isJSONPayload reports whether the payload (ignoring a UTF-8 BOM and leading
// whitespace) is a JSON document rather than XML.
func isJSONPayload(data []byte) bool {
	body := bytes.TrimLeft(bytes.TrimPrefix(data, utf8BOM), " \t\r\n")
	return bytes.HasPrefix(body, []byte("{"))
}

// prepareEntityPayload validates a replacement payload against the kind of
// payload currently on disk and normalizes JSON ladder bodies so Sysmac Studio
// can read them back.
func prepareEntityPayload(original, data []byte) ([]byte, error) {
	if !isJSONPayload(data) {
		if isJSONPayload(original) {
			return nil, errors.New("entity payload on disk is a JSON ladder body; replacement must be JSON too")
		}
		return data, validateXMLPayload(data)
	}
	return normalizeLadderJSONPayload(original, data)
}

func validateXMLPayload(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("entity payload is not valid XML")
		}
	}
}

// normalizeLadderJSONPayload trims surrounding whitespace, validates the JSON,
// terminates it with CRLF and keeps the UTF-8 BOM when the file on disk (or the
// caller) had one.
func normalizeLadderJSONPayload(original, data []byte) ([]byte, error) {
	keepBOM := bytes.HasPrefix(original, utf8BOM) || bytes.HasPrefix(data, utf8BOM)
	body := bytes.TrimSpace(bytes.TrimPrefix(data, utf8BOM))
	if !json.Valid(body) {
		return nil, errors.New("entity payload is not valid JSON")
	}
	out := make([]byte, 0, len(utf8BOM)+len(body)+len(ladderJSONLineEnd))
	if keepBOM {
		out = append(out, utf8BOM...)
	}
	out = append(out, body...)
	out = append(out, ladderJSONLineEnd...)
	return out, nil
}
