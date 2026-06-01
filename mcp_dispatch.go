package claude

import (
	"context"
	"encoding/json"
	"fmt"
)

// handleMcpMessage routes an inbound mcp_message control request to the named
// in-process SDK MCP server. The full JSONRPC dispatch is implemented in M4;
// until then it reports that no server is available.
func (s *session) handleMcpMessage(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	return nil, fmt.Errorf("mcp_message dispatch not yet implemented")
}
