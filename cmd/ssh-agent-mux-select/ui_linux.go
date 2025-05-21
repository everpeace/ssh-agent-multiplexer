// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

//go:build linux
// +build linux

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func promptUserForSelection(targets []string, keyInfo string) (string, error) {
	var err error

	// Attempt Zenity
	zenityPath, err := exec.LookPath("zenity")
	if err == nil {
		promptText := "Select SSH Key Target:"
		if keyInfo != "" {
			// Basic sanitization for display, though Zenity should handle most things.
			escapedKeyInfo := strings.ReplaceAll(keyInfo, "\"", "\\\"")
			escapedKeyInfo = strings.ReplaceAll(escapedKeyInfo, "`", "\\`")
			escapedKeyInfo = strings.ReplaceAll(escapedKeyInfo, "$", "\\$")
			escapedKeyInfo = strings.ReplaceAll(escapedKeyInfo, "\\", "\\\\")
			promptText = fmt.Sprintf("Select SSH Key Target (Key: %s):", escapedKeyInfo)
		}

		args := []string{"--list", "--column=Targets", "--hide-header", "--text=" + promptText}
		for _, target := range targets {
			args = append(args, target)
		}

		cmd := exec.Command(zenityPath, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err == nil {
			selected := strings.TrimSpace(stdout.String())
			// Ensure the selected item is one of the original targets
			for _, t := range targets {
				if t == selected {
					return selected, nil
				}
			}
			fmt.Fprintf(os.Stderr, "Zenity returned an unexpected selection: '%s'. Falling back.\n", selected)
			// Continue to Kdialog or CLI fallback
		} else {
			fmt.Fprintf(os.Stderr, "Zenity failed: %v, stderr: %s. Trying Kdialog.\n", err, stderr.String())
			// Continue to Kdialog or CLI fallback
		}
	} else {
		fmt.Fprintln(os.Stderr, "Zenity not found in PATH. Trying Kdialog.")
	}

	// Attempt Kdialog
	kdialogPath, err := exec.LookPath("kdialog")
	if err == nil {
		promptText := "Select SSH Key Target:"
		if keyInfo != "" {
			// Basic sanitization for display
			escapedKeyInfo := strings.ReplaceAll(keyInfo, "\"", "\\\"")
			escapedKeyInfo = strings.ReplaceAll(escapedKeyInfo, "`", "\\`")
			escapedKeyInfo = strings.ReplaceAll(escapedKeyInfo, "$", "\\$")
			escapedKeyInfo = strings.ReplaceAll(escapedKeyInfo, "\\", "\\\\")
			promptText = fmt.Sprintf("Select SSH Key Target (Key: %s):", escapedKeyInfo)
		}

		var args []string
		// For Kdialog --menu, items are tag1 item1 tag2 item2 ...
		// Here, tags can be simple numbers or the items themselves if tags are not distinct.
		// For simplicity, using the target path as both tag and item.
		// Or, more simply, use --radiolist which is closer to Zenity's list.
		// kdialog --radiolist "Select target:" item1 "" on item2 "" off ...
		// However, --menu is more common. For --menu, we need pairs.
		// Let's use index as tag and target as item.
		args = []string{"--menu", promptText}
		for i, target := range targets {
			args = append(args, fmt.Sprintf("%d", i+1), target) // Tag, Item
		}

		cmd := exec.Command(kdialogPath, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err == nil {
			selectedTag := strings.TrimSpace(stdout.String())
			// Kdialog --menu returns the "tag" of the selected item.
			// We need to find the target associated with this tag.
			// Since we used index+1 as tag:
			selectedIndex := -1
			fmt.Sscanf(selectedTag, "%d", &selectedIndex) // Basic parsing
			if selectedIndex > 0 && selectedIndex <= len(targets) {
				return targets[selectedIndex-1], nil
			}
			fmt.Fprintf(os.Stderr, "Kdialog returned an unexpected selection tag: '%s'. Falling back to CLI.\n", selectedTag)
		} else {
			fmt.Fprintf(os.Stderr, "Kdialog failed: %v, stderr: %s. Falling back to CLI.\n", err, stderr.String())
		}
	} else {
		fmt.Fprintln(os.Stderr, "Kdialog not found in PATH. Falling back to CLI.")
	}

	// Fallback to CLI
	return promptUserCLIFallback(targets, keyInfo)
}
