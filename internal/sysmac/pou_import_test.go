package sysmac

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const importSourceOEM = "\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"utf-8\"?>\r\n" +
	"<sysmac schemaVersion=\"1\">\r\n" +
	"  <Entity type=\"Solution\" subtype=\"\" id=\"11111111-0000-4000-8000-000000000001\" name=\"Lib\" version=\"0\" trackingId=\"11111111-0000-4000-8000-000000000002\">\r\n" +
	"    <AccessInfos />\r\n" +
	"    <ChildEntities>\r\n" +
	"      <Entity type=\"Group\" subtype=\"IecFunctionBlocks\" id=\"11111111-0000-4000-8000-000000000003\" name=\"FBs\" version=\"0\" trackingId=\"11111111-0000-4000-8000-000000000004\">\r\n" +
	"        <AccessInfos />\r\n" +
	"        <ChildEntities>\r\n" +
	"          <Entity type=\"FunctionBlock\" subtype=\"StructuredText\" id=\"22222222-0000-4000-8000-000000000001\" name=\"FB_Lib\" version=\"0\" trackingId=\"d5e7fa13-63f2-4331-aaef-6cd30b33a8a5\" DN=\"FB_Lib\">\r\n" +
	"            <AccessInfos />\r\n" +
	"            <ChildEntities>\r\n" +
	"              <Entity type=\"Variables\" subtype=\"StructuredTextFunctionBlock\" id=\"22222222-0000-4000-8000-000000000002\" name=\"vars\" version=\"0\" trackingId=\"22222222-0000-4000-8000-000000000003\">\r\n" +
	"                <AccessInfos />\r\n" +
	"                <ChildEntities />\r\n" +
	"              </Entity>\r\n" +
	"              <Entity type=\"PouBody\" subtype=\"StructuredText\" id=\"22222222-0000-4000-8000-000000000004\" name=\"body\" version=\"0\" trackingId=\"22222222-0000-4000-8000-000000000005\">\r\n" +
	"                <AccessInfos />\r\n" +
	"                <ChildEntities>\r\n" +
	"                  <Entity type=\"PouBodySourceHolder\" subtype=\"\" id=\"22222222-0000-4000-8000-000000000006\" name=\"PouBodySourceHolder\" version=\"0\" trackingId=\"22222222-0000-4000-8000-000000000007\">\r\n" +
	"                    <AccessInfos />\r\n" +
	"                    <ChildEntities />\r\n" +
	"                  </Entity>\r\n" +
	"                </ChildEntities>\r\n" +
	"              </Entity>\r\n" +
	"            </ChildEntities>\r\n" +
	"          </Entity>\r\n" +
	"        </ChildEntities>\r\n" +
	"      </Entity>\r\n" +
	"    </ChildEntities>\r\n" +
	"  </Entity>\r\n" +
	"</sysmac>"

func writeImportFixtures(t *testing.T) (source, target string) {
	t.Helper()
	source = t.TempDir()
	files := map[string]string{
		"11111111-0000-4000-8000-000000000001.oem":           importSourceOEM,
		"22222222-0000-4000-8000-000000000002.xml":           "[SLWD version=1.0]\r\n_EN=Variables\r\n",
		"22222222-0000-4000-8000-000000000004.xml":           "<StructuredTextModel><Text>x := 1;</Text></StructuredTextModel>",
		"22222222-0000-4000-8000-000000000004.BookmarkGroup": "<BookmarkGroup><Bookmarks/></BookmarkGroup>",
		"22222222-0000-4000-8000-000000000006.xml":           `<data><CxilLinkInformationList Offset="6" FileNameWithoutExtension="FB_d5e7fa1363f24331aaef6cd30b33a8a5" /></data>`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	target = writeCloneFixture(t)
	return source, target
}

func TestImportPOUCopiesSubtreeIntoTargetGroup(t *testing.T) {
	source, target := writeImportFixtures(t)
	sourceOEM, _ := os.ReadFile(filepath.Join(source, "11111111-0000-4000-8000-000000000001.oem"))
	targetPath := filepath.Join(target, "aaaaaaaa-0000-4000-8000-000000000001.oem")
	original, _ := os.ReadFile(targetPath)

	result, err := ImportPOU(POUImportRequest{SourceProjectPath: source, SourceID: "22222222-0000-4000-8000-000000000001", TargetProjectPath: target, TargetParentID: "aaaaaaaa-0000-4000-8000-000000000003"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "FB_Lib" || result.Type != "FunctionBlock" || len(result.CreatedFiles) != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}

	updated, _ := os.ReadFile(targetPath)
	insertAt := bytes.LastIndex(original, []byte("\r\n        </ChildEntities>\r\n      </Entity>\r\n    </ChildEntities>"))
	if !bytes.HasPrefix(updated, original[:insertAt]) || !bytes.HasSuffix(updated, original[insertAt:]) {
		t.Fatal("bytes outside the inserted subtree must be unchanged")
	}
	if bytes.Contains(bytes.ReplaceAll(updated, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("target must keep CRLF only")
	}
	if !bytes.Contains(updated, []byte("\r\n          <Entity type=\"FunctionBlock\"")) || !bytes.Contains(updated, []byte("\r\n                  <Entity type=\"PouBodySourceHolder\"")) {
		t.Fatal("fragment must be re-indented to the target depth")
	}
	if bytes.Contains(updated, []byte("22222222-0000")) || bytes.Contains(updated, []byte("d5e7fa13")) {
		t.Fatal("no source ID may remain in the target index")
	}

	project, err := ParseOEM(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	fb := project.EntitiesByID[result.NewEntityID]
	if fb == nil || fb.Name != "FB_Lib" || fb.ParentID != "aaaaaaaa-0000-4000-8000-000000000003" || len(fb.Children) != 2 {
		t.Fatalf("imported FB not indexed correctly: %+v", fb)
	}
	newTracking := strings.ReplaceAll(result.IDMap["d5e7fa13-63f2-4331-aaef-6cd30b33a8a5"], "-", "")
	holder, _ := os.ReadFile(filepath.Join(target, result.IDMap["22222222-0000-4000-8000-000000000006"]+".xml"))
	if !bytes.Contains(holder, []byte("FB_"+newTracking)) || bytes.Contains(holder, []byte("d5e7fa13")) {
		t.Fatalf("compact trackingId must be remapped in payloads: %s", holder)
	}
	body, _ := os.ReadFile(filepath.Join(target, result.IDMap["22222222-0000-4000-8000-000000000004"]+".xml"))
	if string(body) != "<StructuredTextModel><Text>x := 1;</Text></StructuredTextModel>" {
		t.Fatalf("body must be copied verbatim: %s", body)
	}
	if after, _ := os.ReadFile(filepath.Join(source, "11111111-0000-4000-8000-000000000001.oem")); !bytes.Equal(after, sourceOEM) {
		t.Fatal("source must be untouched")
	}

	if _, err := ImportPOU(POUImportRequest{SourceProjectPath: source, SourceID: "22222222-0000-4000-8000-000000000001", TargetProjectPath: target, TargetParentID: "aaaaaaaa-0000-4000-8000-000000000003"}); err == nil {
		t.Fatal("importing a POU name that already exists must be rejected")
	}
}

func TestImportPOURejectsNonPOUAndSameSolution(t *testing.T) {
	source, target := writeImportFixtures(t)
	if _, err := ImportPOU(POUImportRequest{SourceProjectPath: source, SourceID: "11111111-0000-4000-8000-000000000003", TargetProjectPath: target, TargetParentID: "aaaaaaaa-0000-4000-8000-000000000003"}); err == nil {
		t.Fatal("a group must not be importable")
	}
	if _, err := ImportPOU(POUImportRequest{SourceProjectPath: target, SourceID: "bbbbbbbb-0000-4000-8000-000000000001", TargetProjectPath: target, TargetParentID: "aaaaaaaa-0000-4000-8000-000000000003"}); err == nil {
		t.Fatal("same-solution import must be rejected")
	}
}
