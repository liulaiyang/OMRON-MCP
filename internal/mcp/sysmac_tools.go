package mcp

// sysmacTools exposes the complete Sysmac tool catalog. Individual tool
// definitions and handlers live in focused files by operation family.
func sysmacTools() []tool {
	tools := make([]tool, 0, 19)
	tools = append(tools, sysmacBuildTools()...)
	tools = append(tools, sysmacInspectTextTools()...)
	tools = append(tools, sysmacSLWDTools()...)
	tools = append(tools, sysmacEntityTools()...)
	tools = append(tools, sysmacTransactionTools()...)
	return tools
}
