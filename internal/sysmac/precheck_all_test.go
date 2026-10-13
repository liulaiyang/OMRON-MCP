package sysmac

import (
	"os"
	"strings"
	"testing"
)

// TestPrecheckAppendEverywhere appends an undeclared assignment to every body
// and expects exactly that one error (proves each body parses to the end).
func TestPrecheckAppendEverywhere(t *testing.T) {
	root := os.Getenv("PRECHECK_PROJECT")
	if root == "" {
		t.Skip("PRECHECK_PROJECT not set")
	}
	proj, err := loadPrecheckProject(root, os.Getenv("PRECHECK_STUDIO"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, pi := range proj.pous {
		if pi.pou.Opaque {
			continue
		}
		for _, b := range pi.bodies {
			text, err := readBodyText(root, b)
			if err != nil {
				continue
			}
			r := proj.env.Check(text+"\r\nzzUndeclared := 1;\r\n", stcheckOptions(pi))
			if r.Errors != 1 || !strings.Contains(r.Diagnostics[len(r.Diagnostics)-1].Code, "E_UNDECLARED") {
				t.Errorf("%s/%s: %+v", pi.pou.Name, b.section, r.Diagnostics)
			}
			n++
		}
	}
	t.Logf("checked %d bodies", n)
}
