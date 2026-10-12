package sysmac

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cloneFixtureOEM = "\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"utf-8\"?>\r\n" +
	"<sysmac schemaVersion=\"1\">\r\n" +
	"  <Entity type=\"Solution\" subtype=\"\" id=\"aaaaaaaa-0000-4000-8000-000000000001\" name=\"Sol\" version=\"0\" trackingId=\"aaaaaaaa-0000-4000-8000-000000000002\">\r\n" +
	"    <AccessInfos />\r\n" +
	"    <ChildEntities>\r\n" +
	"      <Entity type=\"Group\" subtype=\"IecPrograms\" id=\"aaaaaaaa-0000-4000-8000-000000000003\" name=\"Programs\" version=\"0\" trackingId=\"aaaaaaaa-0000-4000-8000-000000000004\">\r\n" +
	"        <AccessInfos />\r\n" +
	"        <ChildEntities>\r\n" +
	"          <Entity type=\"Program\" subtype=\"MultipartLadder\" id=\"bbbbbbbb-0000-4000-8000-000000000001\" name=\"PRG_Src\" version=\"0\" dateCreated=\"10/09/2026 08:34:41\" dateLastModified=\"10/09/2026 09:22:35\" author=\"Administrator\" trackingId=\"bbbbbbbb-0000-4000-8000-000000000002\" DN=\"PRG_Src\">\r\n" +
	"            <AccessInfos />\r\n" +
	"            <ChildEntities>\r\n" +
	"              <Entity type=\"Variables\" subtype=\"Ladder\" id=\"bbbbbbbb-0000-4000-8000-000000000003\" name=\"vars\" version=\"0\" trackingId=\"bbbbbbbb-0000-4000-8000-000000000004\">\r\n" +
	"                <AccessInfos />\r\n" +
	"                <ChildEntities />\r\n" +
	"              </Entity>\r\n" +
	"              <Entity type=\"PouBody\" subtype=\"Ladder\" id=\"cccccccc-0000-4000-8000-000000000001\" name=\"Input\" version=\"0\" trackingId=\"cccccccc-0000-4000-8000-000000000002\" DN=\"Input\">\r\n" +
	"                <AccessInfos />\r\n" +
	"                <ChildEntities>\r\n" +
	"                  <Entity type=\"Inline\" subtype=\"StructuredText\" id=\"cccccccc-0000-4000-8000-000000000003\" name=\"cccccccc-0000-4000-8000-0000000000e1\" version=\"0\" trackingId=\"cccccccc-0000-4000-8000-0000000000e1\">\r\n" +
	"                    <AccessInfos />\r\n" +
	"                    <ChildEntities />\r\n" +
	"                  </Entity>\r\n" +
	"                </ChildEntities>\r\n" +
	"              </Entity>\r\n" +
	"              <Entity type=\"PouBody\" subtype=\"Ladder\" id=\"dddddddd-0000-4000-8000-000000000001\" name=\"Driver\" version=\"0\" trackingId=\"dddddddd-0000-4000-8000-000000000002\" DN=\"Driver\">\r\n" +
	"                <AccessInfos />\r\n" +
	"                <ChildEntities>\r\n" +
	"                  <Entity type=\"Inline\" subtype=\"StructuredText\" id=\"dddddddd-0000-4000-8000-000000000003\" name=\"dddddddd-0000-4000-8000-0000000000e1\" version=\"0\" trackingId=\"dddddddd-0000-4000-8000-0000000000e1\">\r\n" +
	"                    <AccessInfos />\r\n" +
	"                    <ChildEntities />\r\n" +
	"                  </Entity>\r\n" +
	"                </ChildEntities>\r\n" +
	"              </Entity>\r\n" +
	"            </ChildEntities>\r\n" +
	"          </Entity>\r\n" +
	"          <Entity type=\"Program\" subtype=\"MultipartLadder\" id=\"eeeeeeee-0000-4000-8000-000000000001\" name=\"PRG_Next\" version=\"0\" trackingId=\"eeeeeeee-0000-4000-8000-000000000002\" DN=\"PRG_Next\">\r\n" +
	"            <AccessInfos />\r\n" +
	"            <ChildEntities />\r\n" +
	"          </Entity>\r\n" +
	"        </ChildEntities>\r\n" +
	"      </Entity>\r\n" +
	"    </ChildEntities>\r\n" +
	"  </Entity>\r\n" +
	"</sysmac>"

func sectionJSON(eid, text string) []byte {
	return []byte("\xEF\xBB\xBF{\"CLs\":[{\"__type\":\"IST\",\"EID\":\"" + eid + "\",\"H\":130,\"TXT\":\"" + text + "\",\"W\":400}],\"CMT\":\"STMultiSection(EC3D2F6D-714D-45C0-BDF9-C23D8DDD0032)\",\"LRI\":1,\"RRI\":2,\"VLs\":[]}\r\n")
}

func writeCloneFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string][]byte{
		"aaaaaaaa-0000-4000-8000-000000000001.oem":           []byte(cloneFixtureOEM),
		"bbbbbbbb-0000-4000-8000-000000000003.xml":           []byte("[SLWD version=1.0]\r\n_EN=Variables\r\n"),
		"cccccccc-0000-4000-8000-000000000001.xml":           sectionJSON("cccccccc-0000-4000-8000-0000000000e1", "x := 1;"),
		"cccccccc-0000-4000-8000-000000000001.BookmarkGroup": []byte("<BookmarkGroup><Bookmarks/></BookmarkGroup>"),
		"dddddddd-0000-4000-8000-000000000001.xml":           sectionJSON("dddddddd-0000-4000-8000-0000000000e1", ";"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCloneProgramDeepCopiesSectionsAndRemapsEIDs(t *testing.T) {
	root := writeCloneFixture(t)
	oemPath := filepath.Join(root, "aaaaaaaa-0000-4000-8000-000000000001.oem")
	original, _ := os.ReadFile(oemPath)

	result, err := CloneProgram(ProgramCloneRequest{
		ProjectPath:     root,
		SourceID:        "bbbbbbbb-0000-4000-8000-000000000001",
		NewName:         "PRG_New",
		ExcludeSections: []string{"Driver"},
		Now:             time.Date(2026, 10, 12, 15, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := os.ReadFile(oemPath)
	if !bytes.HasPrefix(updated, []byte("\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"utf-8\"?>\r\n")) {
		t.Fatal("BOM and XML declaration must be preserved")
	}
	if bytes.Contains(bytes.ReplaceAll(updated, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("index must keep CRLF line endings only")
	}
	if backup, _ := os.ReadFile(oemPath + ".backup"); !bytes.Equal(backup, original) {
		t.Fatal("backup must hold the original index")
	}
	// Everything outside the insertion is unchanged.
	insertAt := bytes.Index(original, []byte("          <Entity type=\"Program\" subtype=\"MultipartLadder\" id=\"eeeeeeee"))
	if !bytes.HasPrefix(updated, original[:insertAt-2]) || !bytes.HasSuffix(updated, original[insertAt-2:]) {
		t.Fatal("bytes outside the inserted subtree must be unchanged")
	}

	project, err := ParseOEM(oemPath)
	if err != nil {
		t.Fatal(err)
	}
	clone := project.EntitiesByID[result.NewEntityID]
	if clone == nil || clone.Name != "PRG_New" || clone.ParentID != "aaaaaaaa-0000-4000-8000-000000000003" {
		t.Fatalf("clone not indexed correctly: %+v", clone)
	}
	group := project.EntitiesByID["aaaaaaaa-0000-4000-8000-000000000003"]
	if names := []string{group.Children[0].Name, group.Children[1].Name, group.Children[2].Name}; strings.Join(names, ",") != "PRG_Src,PRG_New,PRG_Next" {
		t.Fatalf("clone must be inserted after the source, got %v", names)
	}
	if len(clone.Children) != 2 {
		t.Fatalf("clone must have variables + Input only (Driver excluded), got %d children", len(clone.Children))
	}
	if !strings.Contains(string(updated), `DN="PRG_New"`) || !strings.Contains(string(updated), `dateCreated="10/12/2026 15:04:05"`) {
		t.Fatal("name, DN and dates must be updated on the clone")
	}

	if len(result.Sections) != 1 || result.Sections[0].Name != "Input" {
		t.Fatalf("unexpected sections: %+v", result.Sections)
	}
	section := result.Sections[0]
	inline := project.EntitiesByID[section.InlineID]
	if inline == nil || inline.Name != section.EID || inline.TrackingID != section.EID {
		t.Fatalf("Inline entity name/trackingId must equal the new EID: %+v", inline)
	}
	payload, err := os.ReadFile(filepath.Join(root, section.EntityID+".xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(payload, []byte("}\r\n")) || !bytes.HasPrefix(payload, utf8BOM) {
		t.Fatalf("section payload must keep BOM and end with }\\r\\n: %q", payload)
	}
	var body struct {
		CLs []struct {
			EID string
			TXT string
		}
	}
	if err := json.Unmarshal(bytes.TrimPrefix(payload, utf8BOM), &body); err != nil {
		t.Fatal(err)
	}
	if body.CLs[0].EID != section.EID || body.CLs[0].TXT != "x := 1;" {
		t.Fatalf("section JSON must carry the new EID and the original text: %+v", body)
	}
	if _, err := os.Stat(filepath.Join(root, section.EntityID+".BookmarkGroup")); err != nil {
		t.Fatal("BookmarkGroup must be copied")
	}
	varsID := result.IDMap["bbbbbbbb-0000-4000-8000-000000000003"]
	if _, err := os.Stat(filepath.Join(root, varsID+".xml")); err != nil {
		t.Fatal("program variables payload must be copied")
	}
	if len(result.CreatedFiles) != 3 {
		t.Fatalf("expected 3 created files (vars, Input xml, Input bookmarks), got %v", result.CreatedFiles)
	}
	// Source files untouched.
	if data, _ := os.ReadFile(filepath.Join(root, "cccccccc-0000-4000-8000-000000000001.xml")); !bytes.Contains(data, []byte("cccccccc-0000-4000-8000-0000000000e1")) {
		t.Fatal("source section must be untouched")
	}
}

func TestCloneProgramInsertAfterAndGuards(t *testing.T) {
	root := writeCloneFixture(t)
	oemPath := filepath.Join(root, "aaaaaaaa-0000-4000-8000-000000000001.oem")
	request := ProgramCloneRequest{ProjectPath: root, SourceID: "bbbbbbbb-0000-4000-8000-000000000001", NewName: "PRG_Next"}
	if _, err := CloneProgram(request); err == nil {
		t.Fatal("duplicate sibling name must be rejected")
	}
	request.NewName = "PRG_X"
	request.ExcludeSections = []string{"Nope"}
	if _, err := CloneProgram(request); err == nil {
		t.Fatal("unknown excluded section must be rejected")
	}
	request.ExcludeSections = nil
	request.InsertAfterID = "eeeeeeee-0000-4000-8000-000000000001"
	result, err := CloneProgram(request)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := ParseOEM(oemPath)
	group := project.EntitiesByID["aaaaaaaa-0000-4000-8000-000000000003"]
	if group.Children[2].ID != result.NewEntityID {
		t.Fatal("clone must be inserted after insert_after_id")
	}
	if len(result.Sections) != 2 {
		t.Fatalf("expected both sections, got %+v", result.Sections)
	}
	if result.Sections[0].EID == result.Sections[1].EID {
		t.Fatal("each section must get its own EID")
	}

	if err := os.WriteFile(filepath.Join(root, "Sol.applicationlock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	request.NewName = "PRG_Y"
	if _, err := CloneProgram(request); err == nil || !strings.Contains(err.Error(), "applicationlock") {
		t.Fatalf("open Solution must be rejected, got %v", err)
	}
}
