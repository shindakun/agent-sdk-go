// Command plugins demonstrates loading a local plugin and confirming it loaded
// by inspecting the init system message. Mirrors the upstream plugin_example.py.
//
// The demo plugin lives in ./demo-plugin: a .claude-plugin/plugin.json plus a
// commands/greet.md. The command is auto-discovered from the commands/ directory
// (plugin.json does not list it), and appears as the /demo-plugin:greet slash
// command in the session.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	// Resolve ./demo-plugin relative to this source file so the example runs
	// from any working directory.
	_, thisFile, _, _ := runtime.Caller(0)
	pluginPath := filepath.Join(filepath.Dir(thisFile), "demo-plugin")
	fmt.Println("Loading plugin from:", pluginPath)

	var loaded []claude.PluginInfo
	for msg, err := range claude.Query(ctx, "Hello!",
		claude.WithPlugins(claude.SdkPluginConfig{Type: "local", Path: pluginPath}),
		claude.WithMaxTurns(1),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if sm, ok := msg.(*claude.SystemMessage); ok && sm.Subtype == "init" {
			loaded = sm.Plugins
		}
	}

	if len(loaded) == 0 {
		fmt.Println("No plugins reported in the init message.")
		os.Exit(1)
	}
	fmt.Println("Plugins loaded:")
	for _, p := range loaded {
		fmt.Printf("  - %s (path: %s, source: %s)\n", p.Name, p.Path, p.Source)
	}
}
