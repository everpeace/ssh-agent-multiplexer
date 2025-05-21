// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func promptUserCLIFallback(targets []string, keyInfo string) (string, error) {
	fmt.Fprintln(os.Stderr, "Failed to display GUI dialog. Falling back to CLI prompt.")
	if keyInfo != "" {
		fmt.Fprintf(os.Stderr, "Select a target agent for the key '%s':\n", keyInfo)
	} else {
		fmt.Fprintln(os.Stderr, "Select a target agent for the key:")
	}

	for i, target := range targets {
		fmt.Fprintf(os.Stderr, "%d: %s\n", i+1, target)
	}

	fmt.Fprint(os.Stderr, "Enter number: ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	selection, err := strconv.Atoi(input)
	if err != nil {
		return "", fmt.Errorf("invalid input, not a number: %w", err)
	}

	if selection < 1 || selection > len(targets) {
		return "", fmt.Errorf("invalid selection: number out of range")
	}

	return targets[selection-1], nil
}

// This function is defined here as a placeholder for non-Darwin/non-Linux builds.
// It will use the CLI fallback directly.
// The build system uses build tags to select the correct ui_*.go file.
// If no other ui_*.go file is selected (e.g. on Windows, or if built without tags),
// this one will be used if it doesn't have conflicting build tags.
// To ensure it's only used when others aren't, we could add: //go:build !darwin && !linux
// For now, we'll assume that if a platform-specific file is present, it will be chosen.
// If only this and main.go are present, this will provide the promptUserForSelection.
// However, the problem description implies ui_fallback.go is for the *function* promptUserCLIFallback,
// and that ui_darwin.go and ui_linux.go will *call* it.
// The main.go expects promptUserForSelection to be defined.
// So, for builds on other platforms OR if explicit platform files are missing,
// we need a default promptUserForSelection.
// Let's provide a default implementation of promptUserForSelection that calls the CLI.
// This will be overridden by platform-specific versions due to build tags.

//go:build !darwin && !linux
// +build !darwin,!linux

func promptUserForSelection(targets []string, keyInfo string) (string, error) {
	fmt.Fprintln(os.Stderr, "No platform-specific UI available, using CLI fallback.")
	return promptUserCLIFallback(targets, keyInfo)
}
