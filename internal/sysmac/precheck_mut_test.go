package sysmac

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPrecheckMutations appends typical mistakes to FB_Axis and expects each to be reported.
func TestPrecheckMutations(t *testing.T) {
	root := os.Getenv("PRECHECK_PROJECT")
	if root == "" {
		t.Skip("PRECHECK_PROJECT not set")
	}
	body := "68d4e101-ccc4-4983-a950-2013ede1fa76"
	orig, err := ReadStructuredText(filepath.Join(root, body+".xml"))
	if err != nil {
		t.Skip("FB_Axis body not in this project")
	}
	cases := []struct{ code, want string }{
		{"bManul := TRUE;", "E_UNDECLARED"},
		{"SV.Sta.ActCmdd := 1;", "E_MEMBER"},
		{"SV.Sta.ActCmd.Foo := 1;", "E_MEMBER"},
		{"IF bManual THEN iStepStop := 1; ", "E_BLOCK"},
		{"iStepStop = 1;", "E_SYNTAX"},
		{"iStepStop := 1", "E_SYNTAX"},
		{"x := TON(In := TRUE, PT := T#1s);", "E_FB_TYPE_CALL"},
		{"MC_Power(Axis := Axis, Enable := TRUE);", "E_FB_TYPE_CALL"},
		{"FOR i := 0 TO 3 DO iStepStop := i; END_FOR; CMD_NONE := 3;", "E_WRITE_CONST"},
		{"iStepStop := Foo_Bar(1, 2);", "E_UNKNOWN_FUNC"},
		{"iStepStop := REAL_TO_INT(1.0, 2.0);", "E_PARAM_COUNT"},
		{"t1 := T#1x;", "E_TIME_LITERAL"},
		{"bManual := SV.Hmi.0;", "E_BIT_ACCESS"},
		{"iStepStop := iStepStop[1];", "E_NOT_ARRAY"},
		{"bManual := P_First_Run;", "E_FIRST_RUN"},
		{"iStepStop := 1；", "E_LEX"},
		{"EXIT;", "E_EXIT"},
		{"iStepStop := 1.5;", "E_TYPE"},
		{"bManual := iStepStop;", "E_TYPE"},
		{"IF iStepStop THEN bManual := TRUE; END_IF;", "E_COND_TYPE"},
		{"iStepStop := T#1s;", "E_TYPE"},
		{"iStepStop := TorqueRamp;", "E_TYPE"},
		{"iStepStop := REAL_TO_INT(bManual);", "E_TYPE"},
	}
	for _, c := range cases {
		code := orig + "\r\n" + c.code + "\r\n"
		res, err := Precheck(PrecheckRequest{ProjectPath: root, StudioPath: os.Getenv("PRECHECK_STUDIO"), EntityID: body, Code: code})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, b := range res.Bodies {
			for _, d := range b.Diagnostics {
				got = append(got, fmt.Sprintf("%s L%d %s", d.Code, d.Line, d.Message))
			}
		}
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, c.want) {
			t.Errorf("%q: want %s, got:\n%s", c.code, c.want, joined)
		} else {
			fmt.Printf("OK %-45q -> %s\n", c.code, got[0])
		}
	}
}
