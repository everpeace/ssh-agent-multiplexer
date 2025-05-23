// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

//go:build darwin
// +build darwin

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func promptUserForSelection(targets []string, keyInfo string) (string, error) {
	appleScriptTemplate := `
set promptSentence to "%s"
set agentChoice to {%s}
set selectedAgent to choose from list agentChoice with prompt promptSentence cancel button name "Cancel" OK button name "Select"
selectedAgent
`

	escapedTargets := []string{}
	for _, target := range targets {
		// Escape double quotes in target paths for AppleScript string literals
		escapedTarget := strings.ReplaceAll(target, `"`, `\\"`)
		escapedTargets = append(escapedTargets, fmt.Sprintf(`"%s"`, escapedTarget))
	}

	promptMessage := "[ssh-agent-multiplexer]\nSelect SSH Agent"
	if keyInfo != "" {
		// Escape double quotes in keyInfo for AppleScript string literals
		escapedKeyInfo := strings.ReplaceAll(keyInfo, `"`, `\\"`)
		promptMessage += fmt.Sprintf(" for key '%s'", escapedKeyInfo)
	}

	appleScript := fmt.Sprintf(appleScriptTemplate, promptMessage, strings.Join(escapedTargets, ","))
	cmd := exec.Command("osascript", "-e", appleScript)

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
		// The 'if _, ok := err.(*exec.ExitError); ok' block was removed as 'ok' was unused (SA4006).
		// The surrounding error handling (logging and fallback) is sufficient.
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
