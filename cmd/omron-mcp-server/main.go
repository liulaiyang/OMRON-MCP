// Command omron-mcp-server runs only the MCP stdio server (no GUI), so it can be
// cross-compiled without cgo. Accepts the same "--mcp" flag as omron-mcp.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/rjboer/omron-mcp/internal/mcp"
)

var buildCommit = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(buildCommit)
		return
	}
	if err := mcp.NewServer().RunStdio(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
