// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// SelectTargetInput defines the structure for JSON input
type SelectTargetInput struct {
	Targets []string `json:"targets"`
	KeyInfo string   `json:"key_info,omitempty"`
}

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
	byteValue, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
		os.Exit(1)
	}

	var input SelectTargetInput
	err = json.Unmarshal(byteValue, &input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON input: %v\n", err)
		os.Exit(1)
	}

	// Validate targets
	var validTargets []string
	if input.Targets != nil {
		for _, t := range input.Targets {
			trimmedTarget := strings.TrimSpace(t)
			if trimmedTarget != "" {
				validTargets = append(validTargets, trimmedTarget)
			}
		}
	}

	if len(validTargets) == 0 {
		fmt.Fprintln(os.Stderr, "Error: No valid targets provided in JSON input.")
		os.Exit(1)
	}

	selectedTarget, err := promptUserForSelection(validTargets, input.KeyInfo)
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
