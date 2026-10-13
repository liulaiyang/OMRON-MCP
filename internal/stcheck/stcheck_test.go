package stcheck

import (
	"strings"
	"testing"
)

const miniCxif = `MODULE NX
TYPE
	_eMC_DIRECTION : (_mcPositiveDirection := 0, _mcNegativeDirection := 2);
	_sAXIS_REF : STRUCT
		Status : BOOL;
	END_STRUCT;
END_TYPE
FUNCTION_BLOCK {a<builtin>} TON
VAR_INPUT
	In		:	BOOL;
	PT		:	TIME;
END_VAR
VAR_OUTPUT
	Q		:	BOOL;
	ET		:	TIME;
END_VAR
END_FUNCTION_BLOCK
FUNCTION_BLOCK MC_Power
	VAR_IN_OUT CONSTANT
		Axis	: _sAXIS_REF {scn<01275>};
	END_VAR
	VAR_INPUT
		Enable : BOOL;
	END_VAR
	VAR_OUTPUT
		Status : BOOL;
	END_VAR
END_FUNCTION_BLOCK
FUNCTION {alien<X>} REAL_TO_INT : INT
VAR_INPUT
	In : REAL;
END_VAR
END_FUNCTION
FUNCTION LIMIT : void
VAR_INPUT
	MN : void;
	In : void;
	MX : void;
END_VAR
END_FUNCTION
END_MODULE`

func testEnv() (*Env, *POU) {
	e := NewEnv()
	e.LoadCxif(miniCxif)
	e.AddType(&TypeDef{Name: "ST_Cmd", FullName: `Std\ST_Cmd`, Kind: TDStruct, Members: []*Var{
		{Name: "bStart", Type: ParseTypeRef("BOOL")},
		{Name: "arrPos", Type: ParseTypeRef("ARRAY[0..3] OF REAL")},
	}})
	e.AddType(&TypeDef{Name: "E_Mode", FullName: "E_Mode", Kind: TDEnum, Enums: []string{"Manual", "Auto"}})
	e.AddGlobal(&Var{Name: "gAxis1", TypeText: "_sAXIS_REF", Group: "VAR_GLOBAL"})
	p := &POU{Name: "FB_Test", Kind: KindFB}
	for _, v := range []*Var{
		{Name: "iStep", TypeText: "INT", Group: "VAR"},
		{Name: "rVel", TypeText: "REAL", Group: "VAR"},
		{Name: "bRun", TypeText: "BOOL", Group: "VAR"},
		{Name: "tonDelay", TypeText: "TON", Group: "VAR"},
		{Name: "fbPower", TypeText: "MC_Power", Group: "VAR"},
		{Name: "stCmd", TypeText: `Std\ST_Cmd`, Group: "VAR_IN_OUT"},
		{Name: "eMode", TypeText: "E_Mode", Group: "VAR"},
		{Name: "MAX_N", TypeText: "INT", Group: "VAR", Const: true},
		{Name: "Axis", TypeText: "_sAXIS_REF", Group: "VAR_IN_OUT"},
		{Name: "中文变量", TypeText: "BOOL", Group: "VAR"},
	} {
		p.AddVar(v)
	}
	return e, p
}

func codes(r Result) string {
	var c []string
	for _, d := range r.Diagnostics {
		c = append(c, d.Code)
	}
	return strings.Join(c, ",")
}

func TestCleanCode(t *testing.T) {
	e, p := testEnv()
	src := `(* header *)
// comment
tonDelay(In := bRun AND NOT stCmd.bStart, PT := T#1s500ms);
IF tonDelay.Q THEN
    iStep := 10;
ELSIF iStep >= 10 AND eMode = E_Mode#Auto THEN
    rVel := stCmd.arrPos[iStep MOD 4] * 2.0 + 1;
END_IF;
CASE iStep OF
    0, 1: bRun := TRUE;
    2..5:
        bRun := FALSE;
    ELSE
        iStep := 0;
END_CASE;
FOR iStep := 0 TO MAX_N BY 1 DO
    IF iStep > 2 THEN EXIT; END_IF;
END_FOR;
fbPower(Axis := Axis, Enable := bRun, Status => 中文变量);
iStep := REAL_TO_INT(rVel);
rVel := LIMIT(0.0, rVel, 100.0);
eMode := Manual;
bRun := iStep.0 OR (WORD#16#00FF <> 0);`
	r := e.Check(src, Options{POU: p})
	if r.Errors != 0 || r.Warnings != 0 {
		t.Fatalf("expected clean, got %+v", r.Diagnostics)
	}
}

func TestErrorsFound(t *testing.T) {
	e, p := testEnv()
	cases := map[string]string{
		"bRunn := TRUE;":                      "E_UNDECLARED",
		"stCmd.bStrat := TRUE;":               "E_MEMBER",
		"tonDelay(IN := TRUE, PT := 5);":      "E_TYPE",
		"tonDelay(In := TRUE, Q := bRun);":    "E_PARAM_DIR",
		"tonDelay(In := TRUE, PTT := T#1s);":  "E_PARAM",
		"tonDelay(TRUE, T#1s);":               "E_FB_POSITIONAL",
		"fbPower(Enable := TRUE);":            "E_INOUT_MISSING",
		"MAX_N := 3;":                         "E_WRITE_CONST",
		"tonDelay.Q := TRUE;":                 "E_WRITE_OUTPUT",
		"iStep := rVel;":                      "E_TYPE",
		"IF iStep THEN bRun := TRUE; END_IF;": "E_COND_TYPE",
		"IF bRun THEN iStep := 1;":            "E_BLOCK",
		"iStep = 1;":                          "E_SYNTAX",
		"eMode := E_Mode#Automatic;":          "E_ENUM_VALUE",
		"bRun := stCmd.arrPos.bStart;":        "E_MEMBER_ON_ARRAY",
		"bRun := iStep.16;":                   "E_BIT_RANGE",
		"TON(In := TRUE);":                    "E_FB_TYPE_CALL",
		"iStep := FOO(1);":                    "E_UNKNOWN_FUNC",
		"bRun := gAxis1.Status;":              "W_EXTERNAL",
		"tonDelay(In := TRUE, PT := T#1sec);": "E_TIME_LITERAL",
		"iStep := 1；":                         "E_LEX",
	}
	for src, want := range cases {
		r := e.Check(src, Options{POU: p, WarnGlobalsNotExternal: true})
		if !strings.Contains(codes(r), want) {
			t.Errorf("%q: want %s, got %s %+v", src, want, codes(r), r.Diagnostics)
		}
	}
}
