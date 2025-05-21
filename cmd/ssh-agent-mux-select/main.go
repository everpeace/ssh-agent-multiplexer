// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"fmt"
	"os"
	"strings"
)

var (
	Version  string
	Revision string
)

// This function will be implemented in platform-specific files
// (ui_darwin.go, ui_linux.go) and ui_fallback.go.
// It's declared here to satisfy the compiler for main.go.
// The actual implementation used will depend on build tags.
// func promptUserForSelection(targets []string, keyInfo string) (string, error)

func main() {
	targetsEnv := os.Getenv("SSH_AGENT_MUX_TARGETS")
	if targetsEnv == "" {
		fmt.Fprintln(os.Stderr, "Error: SSH_AGENT_MUX_TARGETS environment variable not set or empty.")
		os.Exit(1)
	}

	rawTargets := strings.Split(strings.TrimSpace(targetsEnv), "\n")
	var targets []string
	for _, t := range rawTargets {
		if strings.TrimSpace(t) != "" {
			targets = append(targets, strings.TrimSpace(t))
		}
	}

	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "Error: No valid target paths found in SSH_AGENT_MUX_TARGETS.")
		os.Exit(1)
	}

	keyInfo := os.Getenv("SSH_AGENT_MUX_KEY_INFO")

	selectedTarget, err := promptUserForSelection(targets, keyInfo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error selecting target: %v\n", err)
		os.Exit(1)
	}

	if selectedTarget == "" {
		fmt.Fprintln(os.Stderr, "Error: No target selected.")
		os.Exit(1)
	}

	// Print the selected target to stdout for the main ssh-agent-multiplexer process
	fmt.Print(selectedTarget)
}
