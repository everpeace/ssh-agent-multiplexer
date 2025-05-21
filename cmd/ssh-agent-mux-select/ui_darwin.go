// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

//go:build darwin
// +build darwin

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func promptUserForSelection(targets []string, keyInfo string) (string, error) {
	var script strings.Builder
	script.WriteString(`'Tell application "System Events" to choose from list {`)
	for i, target := range targets {
		if i > 0 {
			script.WriteString(", ")
		}
		// Escape double quotes in target paths for AppleScript string literals
		escapedTarget := strings.ReplaceAll(target, `"`, `\\"`)
		script.WriteString(`"` + escapedTarget + `"`)
	}
	script.WriteString(`} with prompt "`)

	promptMessage := "Select SSH Key Target:"
	if keyInfo != "" {
		// Escape double quotes in keyInfo for AppleScript string literals
		escapedKeyInfo := strings.ReplaceAll(keyInfo, `"`, `\\"`)
		promptMessage = fmt.Sprintf("Select SSH Key Target for key '%s':", escapedKeyInfo)
	}
	script.WriteString(promptMessage)
	script.WriteString(`" default items {"` + strings.ReplaceAll(targets[0], `"`, `\\"`) + `"} cancel button name "Cancel" OK button name "Select"'`)

	cmd := exec.Command("osascript", "-e", script.String())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil {
		// Check if the error is due to user cancellation (exit code 1 for osascript)
		// and if stderr contains "User canceled." (osascript behavior may vary)
		// For "choose from list", if cancel is hit, exit code is 0 and stdout is empty.
		// If there's an actual error, stderr will often have info.
		// ExitError type assertion is important here.
		if exitErr, ok := err.(*exec.ExitError); ok {
			// osascript returns exit code 1 on cancel for some dialogs,
			// but for "choose from list", it's more nuanced.
			// stdout is empty string with exit code 0 if "Cancel" is pressed.
			// stderr might say "User canceled." for other types of dialogs with exit code 1.
			// Let's check stdout first for the "choose from list" cancel case.
			// If stdout is empty and err is nil, it means cancel. If stdout is empty and err is NOT nil, it's an error.
			// The promptUserCLIFallback will be called if we return an error here.
		}
		// If any error, log and fallback
		fmt.Fprintf(os.Stderr, "AppleScript GUI failed or was canceled: %v, stderr: %s. Falling back to CLI.\n", err, stderr.String())
		return promptUserCLIFallback(targets, keyInfo)
	}

	selected := strings.TrimSpace(stdout.String())

	// If 'osascript' "choose from list" is canceled, it returns an empty string, and err is nil.
	if selected == "" {
		fmt.Fprintln(os.Stderr, "User canceled AppleScript dialog. Falling back to CLI.")
		return promptUserCLIFallback(targets, keyInfo)
	}

	// osascript might return "false" (as a string) if nothing is selected and OK is pressed (though default items usually prevent this)
	// or if the list is empty (which our main.go should prevent).
	// We should ensure the selected item is one of the original targets.
	for _, t := range targets {
		if t == selected {
			return selected, nil
		}
	}

	fmt.Fprintf(os.Stderr, "AppleScript returned an unexpected selection: '%s'. Falling back to CLI.\n", selected)
	return promptUserCLIFallback(targets, keyInfo)
}
