package mcp

import (
	"encoding/json"
	"errors"

	"github.com/rjboer/omron-mcp/internal/sysmac"
)

func sysmacPrecheckTools() []tool {
	return []tool{
		{Name: "sysmac_st_precheck", Description: "Offline pre-check of Structured Text WITHOUT Sysmac Studio (seconds): syntax, undeclared variables, struct members, FB / function names and parameters, required IN_OUT parameters, constants written, IF/WHILE condition types, obvious type mismatches, globals not registered as external variables. Uses the project's own data types / variable tables and the Studio builder's system interface files (MC_*, TON, ...). Pass code to check proposed source before writing it (entity_id = the ST body, the multi-section ST section, or the FB / function). Without code it checks what is on disk; without entity_id it checks every ST body of the project. A clean result does not replace the Sysmac Studio build.", InputSchema: objectSchema(map[string]any{
			"project_path":    stringProperty("Native Solution directory (contains the .oem index)"),
			"entity_id":       stringProperty("Program, FB, function, ST body or multi-section ST section entity ID. Empty = whole project"),
			"code":            stringProperty("Proposed ST source to check instead of the file on disk (plain ST, not JSON)"),
			"extra_variables": map[string]any{"type": "array", "description": "Variables you are about to add to the POU's variable table, checked as if already declared", "items": map[string]any{"type": "object", "properties": map[string]any{"name": stringProperty("Variable name"), "type": stringProperty("Sysmac type, e.g. BOOL, ARRAY[0..3] OF REAL, Standard\\Axis\\ST_Axis, TON"), "group": stringProperty("VAR (default), VAR_INPUT, VAR_OUTPUT, VAR_IN_OUT, VAR_EXTERNAL")}, "required": []string{"name", "type"}}},
			"studio_path":     stringProperty("Optional Sysmac Studio install directory; default: $OMRON_SYSMAC_STUDIO or auto-detect"),
			"warn_external":   map[string]any{"type": "boolean", "description": "Warn when a global variable is used but not listed in the POU's external variables (default true)"},
			"max_diagnostics": map[string]any{"type": "integer", "description": "Maximum diagnostics returned per body (default 40)"},
		}, "project_path")},
	}
}

func sysmacPrecheckToolHandlers() map[string]sysmacToolHandler {
	return map[string]sysmacToolHandler{"sysmac_st_precheck": handleSTPrecheck}
}

func handleSTPrecheck(call sysmacToolCall) (map[string]any, error) {
	projectPath, err := call.string("project_path", true)
	if err != nil {
		return nil, err
	}
	req := sysmac.PrecheckRequest{ProjectPath: projectPath, WarnExternal: true}
	if req.EntityID, err = call.string("entity_id", false); err != nil {
		return nil, err
	}
	if req.Code, err = call.string("code", false); err != nil {
		return nil, err
	}
	if req.StudioPath, err = call.string("studio_path", false); err != nil {
		return nil, err
	}
	if raw, ok := call.arguments["extra_variables"]; ok {
		if err := json.Unmarshal(raw, &req.ExtraVariables); err != nil {
			return nil, errors.New("extra_variables must be an array of {name, type, group}")
		}
	}
	if raw, ok := call.arguments["warn_external"]; ok {
		if err := json.Unmarshal(raw, &req.WarnExternal); err != nil {
			return nil, errors.New("warn_external must be a boolean")
		}
	}
	limit := 40
	if raw, ok := call.arguments["max_diagnostics"]; ok {
		if json.Unmarshal(raw, &limit) != nil || limit <= 0 {
			return nil, errors.New("max_diagnostics must be a positive integer")
		}
	}
	res, err := sysmac.Precheck(req)
	if err != nil {
		return nil, err
	}
	// keep the answer compact: only bodies with findings, capped diagnostics
	var bodies []sysmac.PrecheckBody
	for _, b := range res.Bodies {
		if b.Errors == 0 && b.Warnings == 0 {
			continue
		}
		if len(b.Diagnostics) > limit {
			b.Diagnostics = b.Diagnostics[:limit]
		}
		bodies = append(bodies, b)
	}
	res.Bodies = bodies
	return toolSuccess(res), nil
}
