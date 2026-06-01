// Command plugins demonstrates loading a local plugin and confirming it appears
// in the init system message. Mirrors the upstream plugin_example.py example.
// The demo plugin lives in ./demo-plugin (a .claude-plugin/plugin.json plus a
// custom /greet command).
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	claude "github.com/shindakun/agent-sdk-go"
)

func main() {
	ctx := context.Background()

	// Resolve ./demo-plugin relative to this source file so the example runs
	// from any working directory.
	_, thisFile, _, _ := runtime.Caller(0)
	pluginPath := filepath.Join(filepath.Dir(thisFile), "demo-plugin")
	fmt.Println("Loading plugin from:", pluginPath)

	found := false
	for msg, err := range claude.Query(ctx,
		"What custom slash commands are available from loaded plugins?",
		claude.WithPlugins(claude.SdkPluginConfig{Type: "local", Path: pluginPath}),
	) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		switch m := msg.(type) {
		case *claude.SystemMessage:
			if m.Subtype == "init" && strings.Contains(string(m.Data), "demo-plugin") {
				found = true
			}
		case *claude.ResultMessage:
			fmt.Println(m.Result)
		}
	}
	if found {
		fmt.Println("✓ demo-plugin was loaded (seen in the init message).")
	} else {
		fmt.Println("(plugin not detected in the init message)")
	}
}
