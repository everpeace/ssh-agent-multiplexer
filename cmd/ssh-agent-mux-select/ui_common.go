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
