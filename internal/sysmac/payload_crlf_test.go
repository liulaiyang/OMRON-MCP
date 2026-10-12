package sysmac

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Multi-section ST sections (Sysmac FakeMultiSectionST) are JSON ladder bodies.
// Sysmac Studio only parses them as JSON when the file ends with "}\r\n"; an LF
// ending makes the section show up as an empty ladder rung.

const multiSectionBody = `{"CLs":[{"__type":"IST","EID":"4ad8e7b6-917d-45b8-8a8e-fd22703ebd8c","H":130,"TXT":";\u000d\u000a\/\/Test","W":400}],"CMT":"STMultiSection(EC3D2F6D-714D-45C0-BDF9-C23D8DDD0032)","LRI":1,"RRI":2,"VLs":[]}`

func writeLadderFixture(t *testing.T, onDisk []byte) (root, payload string) {
	t.Helper()
	root = t.TempDir()
	oem := `<sysmac schemaVersion="1"><Entity type="Solution" name="Test" id="solution-1"><ChildEntities><Entity type="PouBody" subtype="Ladder" name="Other" id="section-1" /></ChildEntities></Entity></sysmac>`
	if err := os.WriteFile(filepath.Join(root, "project.oem"), []byte(oem), 0600); err != nil {
		t.Fatal(err)
	}
	payload = filepath.Join(root, "section-1.xml")
	if err := os.WriteFile(payload, onDisk, 0600); err != nil {
		t.Fatal(err)
	}
	return root, payload
}

func diskOriginal() []byte {
	return append(append(append([]byte{}, utf8BOM...), multiSectionBody...), "\r\n"...)
}

func TestWriteEntityPayloadNormalizesLadderJSONToCRLF(t *testing.T) {
	cases := map[string][]byte{
		"LF ending":         []byte(multiSectionBody + "\n"),
		"no ending":         []byte(multiSectionBody),
		"CRLF ending":       []byte(multiSectionBody + "\r\n"),
		"trailing spaces":   []byte(multiSectionBody + "  \n\n"),
		"BOM and LF ending": append(append([]byte{}, utf8BOM...), multiSectionBody+"\n"...),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			root, payload := writeLadderFixture(t, diskOriginal())
			if _, err := WriteEntityPayloadAtomic(root, "section-1", input, ""); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(payload)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasSuffix(got, []byte("}\r\n")) {
				t.Fatalf("file must end with }\\r\\n, got %q", got[len(got)-3:])
			}
			if !bytes.Equal(got, diskOriginal()) {
				t.Fatalf("written payload differs from canonical form:\n got %q\nwant %q", got, diskOriginal())
			}
		})
	}
}

func TestWriteEntityPayloadKeepsNoBOMWhenOriginalHasNone(t *testing.T) {
	root, payload := writeLadderFixture(t, []byte(multiSectionBody+"\r\n"))
	if _, err := WriteEntityPayloadAtomic(root, "section-1", []byte(multiSectionBody+"\n"), ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(payload)
	if bytes.HasPrefix(got, utf8BOM) {
		t.Fatal("BOM must not be added when neither the original nor the input had one")
	}
	if !bytes.HasSuffix(got, []byte("}\r\n")) {
		t.Fatalf("file must end with }\\r\\n, got %q", got)
	}
}

func TestWriteEntityPayloadRejectsInvalidLadderJSON(t *testing.T) {
	root, payload := writeLadderFixture(t, diskOriginal())
	_, err := WriteEntityPayloadAtomic(root, "section-1", []byte(`{"CLs":[`), "")
	if err == nil {
		t.Fatal("expected invalid JSON to be rejected")
	}
	got, _ := os.ReadFile(payload)
	if !bytes.Equal(got, diskOriginal()) {
		t.Fatal("original payload must be left untouched on rejection")
	}
}

func TestWriteEntityPayloadRejectsXMLOverLadderJSON(t *testing.T) {
	root, payload := writeLadderFixture(t, diskOriginal())
	_, err := WriteEntityPayloadAtomic(root, "section-1", []byte(`<StructuredTextModel><Text>x</Text></StructuredTextModel>`), "")
	if err == nil {
		t.Fatal("expected XML replacement of a JSON ladder body to be rejected")
	}
	got, _ := os.ReadFile(payload)
	if !bytes.Equal(got, diskOriginal()) {
		t.Fatal("original payload must be left untouched on rejection")
	}
}

func TestWriteEntityPayloadXMLUnchanged(t *testing.T) {
	root := t.TempDir()
	oem := `<sysmac schemaVersion="1"><Entity type="Solution" name="Test" id="solution-1"><ChildEntities><Entity type="Variables" name="Vars" id="vars-1" /></ChildEntities></Entity></sysmac>`
	if err := os.WriteFile(filepath.Join(root, "project.oem"), []byte(oem), 0600); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(root, "vars-1.xml")
	if err := os.WriteFile(payload, []byte(`<a>old</a>`), 0600); err != nil {
		t.Fatal(err)
	}
	input := []byte("<a>new</a>\n")
	if _, err := WriteEntityPayloadAtomic(root, "vars-1", input, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(payload)
	if !bytes.Equal(got, input) {
		t.Fatalf("XML payloads must be written verbatim, got %q", got)
	}
	if _, err := WriteEntityPayloadAtomic(root, "vars-1", []byte("<a>"), ""); err == nil {
		t.Fatal("invalid XML must still be rejected")
	}
}
