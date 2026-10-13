package sysmac

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestPrecheckRealProject runs only when PRECHECK_PROJECT points at a project.
func TestPrecheckRealProject(t *testing.T) {
	root := os.Getenv("PRECHECK_PROJECT")
	if root == "" {
		t.Skip("PRECHECK_PROJECT not set")
	}
	res, err := Precheck(PrecheckRequest{ProjectPath: root, StudioPath: os.Getenv("PRECHECK_STUDIO"), EntityID: os.Getenv("PRECHECK_ENTITY"), WarnExternal: os.Getenv("PRECHECK_EXT") != ""})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
}
