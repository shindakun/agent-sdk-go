package claude

import (
	"context"
	"encoding/json"
)

// ContextUsage reports the context-window breakdown returned by the CLI. Raw
// holds the full payload for fields not modeled here.
type ContextUsage struct {
	Raw json.RawMessage
}

// McpServerStatus reports the state of one MCP server.
type McpServerStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Raw    json.RawMessage
}

// Interrupt stops the current turn.
func (c *Client) Interrupt(ctx context.Context) error {
	_, err := c.sendControl(ctx, "interrupt", nil)
	return err
}

// SetModel switches the active model for subsequent turns.
func (c *Client) SetModel(ctx context.Context, model string) error {
	_, err := c.sendControl(ctx, "set_model", map[string]any{"model": model})
	return err
}

// SetPermissionMode changes the permission mode at runtime.
func (c *Client) SetPermissionMode(ctx context.Context, mode PermissionMode) error {
	_, err := c.sendControl(ctx, "set_permission_mode", map[string]any{"mode": string(mode)})
	return err
}

// GetContextUsage returns the current context-window usage breakdown.
func (c *Client) GetContextUsage(ctx context.Context) (ContextUsage, error) {
	payload, err := c.sendControl(ctx, "get_context_usage", nil)
	if err != nil {
		return ContextUsage{}, err
	}
	return ContextUsage{Raw: payload}, nil
}

// McpStatus returns the status of configured MCP servers.
func (c *Client) McpStatus(ctx context.Context) (json.RawMessage, error) {
	return c.sendControl(ctx, "mcp_status", nil)
}

// McpReconnect reconnects a failed MCP server.
func (c *Client) McpReconnect(ctx context.Context, serverName string) error {
	_, err := c.sendControl(ctx, "mcp_reconnect", map[string]any{"serverName": serverName})
	return err
}

// McpToggle enables or disables an MCP server.
func (c *Client) McpToggle(ctx context.Context, serverName string, enabled bool) error {
	_, err := c.sendControl(ctx, "mcp_toggle", map[string]any{
		"serverName": serverName,
		"enabled":    enabled,
	})
	return err
}

// StopTask stops a running task by id.
func (c *Client) StopTask(ctx context.Context, taskID string) error {
	_, err := c.sendControl(ctx, "stop_task", map[string]any{"task_id": taskID})
	return err
}

// sendControl issues an SDK->CLI control request via the session engine.
func (c *Client) sendControl(ctx context.Context, subtype string, extra map[string]any) (json.RawMessage, error) {
	sess, err := c.session()
	if err != nil {
		return nil, err
	}
	payload, err := sess.engineRef().SendControl(ctx, subtype, extra)
	if err != nil {
		return nil, &ControlProtocolError{Subtype: subtype, Message: err.Error()}
	}
	return payload, nil
}
