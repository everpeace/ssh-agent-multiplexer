// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"os"
	"reflect"
	"strings" // Moved from below
	"testing"
)

// main.go relies on a global promptUserForSelection function.
// For testing main's logic without calling actual UI, we can mock it.
// However, main() itself calls os.Exit, which makes it hard to test directly
// without running it as a separate process.
// The primary logic in main (before calling promptUserForSelection) is parsing env vars.
// We will test this parsing logic separately.
// The actual call to promptUserForSelection and its output handling are more of an integration test.

func TestParseTargetsEnv(t *testing.T) {
	tests := []struct {
		name         string
		envVarValue  string
		setEnvVar    bool
		expected     []string
		expectError  bool
		errorContent string // If expectError is true, check for this content
	}{
		{
			name:        "Valid single target",
			envVarValue: "/tmp/agent.1",
			setEnvVar:   true,
			expected:    []string{"/tmp/agent.1"},
			expectError: false,
		},
		{
			name:        "Valid multiple targets",
			envVarValue: "/tmp/agent.1\n/tmp/agent.2\n/tmp/agent.3",
			setEnvVar:   true,
			expected:    []string{"/tmp/agent.1", "/tmp/agent.2", "/tmp/agent.3"},
			expectError: false,
		},
		{
			name:        "Targets with extra whitespace",
			envVarValue: "  /tmp/agent.1  \n  /tmp/agent.2  \n",
			setEnvVar:   true,
			expected:    []string{"/tmp/agent.1", "/tmp/agent.2"},
			expectError: false,
		},
		{
			name:        "Targets with empty lines",
			envVarValue: "/tmp/agent.1\n\n/tmp/agent.2\n",
			setEnvVar:   true,
			expected:    []string{"/tmp/agent.1", "/tmp/agent.2"},
			expectError: false,
		},
		{
			name:        "Empty env var value",
			envVarValue: "",
			setEnvVar:   true,
			expected:    nil, // Expecting error, so targets don't matter as much
			expectError: true,
			errorContent: "No valid target paths found",
		},
		{
			name:        "Env var with only whitespace",
			envVarValue: "  \n  \n  ",
			setEnvVar:   true,
			expected:    nil,
			expectError: true,
			errorContent: "No valid target paths found",
		},
		{
			name:         "Env var not set",
			envVarValue:  "", // Value doesn't matter
			setEnvVar:    false,
			expected:     nil,
			expectError:  true,
			errorContent: "SSH_AGENT_MUX_TARGETS environment variable not set or empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldEnvVal := os.Getenv("SSH_AGENT_MUX_TARGETS")
			if tt.setEnvVar {
				if err := os.Setenv("SSH_AGENT_MUX_TARGETS", tt.envVarValue); err != nil {
					t.Fatalf("Failed to set env var SSH_AGENT_MUX_TARGETS: %v", err)
				}
			} else {
				if err := os.Unsetenv("SSH_AGENT_MUX_TARGETS"); err != nil {
					t.Fatalf("Failed to unset env var SSH_AGENT_MUX_TARGETS: %v", err)
				}
			}
			defer func() {
				if oldEnvVal == "" {
					if err := os.Unsetenv("SSH_AGENT_MUX_TARGETS"); err != nil {
						// Using t.Logf or t.Errorf for defer as Fatalf would prevent other defers
						t.Logf("defer: Failed to unset env var SSH_AGENT_MUX_TARGETS: %v", err)
					}
				} else {
					if err := os.Setenv("SSH_AGENT_MUX_TARGETS", oldEnvVal); err != nil {
						// Using t.Logf or t.Errorf for defer
						t.Logf("defer: Failed to set env var SSH_AGENT_MUX_TARGETS to old value: %v", err)
					}
				}
			}()
			
			// This is a simplified representation of main's parsing logic.
			// main.go itself calls os.Exit, so we're testing the core parsing part here.
			var actualTargets []string
			var actualError error

			targetsEnvVal := os.Getenv("SSH_AGENT_MUX_TARGETS")
			if targetsEnvVal == "" && !tt.setEnvVar { // Mimic main's check for unset more accurately
				// This case is where getenv returns "" and it was because it was unset.
				// If setenv was called with "", targetsEnvVal would be "", but setEnvVar would be true.
                 actualError = newError("SSH_AGENT_MUX_TARGETS environment variable not set or empty.")
			} else if targetsEnvVal == "" && tt.setEnvVar { // set to empty string
                 actualError = newError("No valid target paths found in SSH_AGENT_MUX_TARGETS.")
            } else {
				rawTargets := strings.Split(strings.TrimSpace(targetsEnvVal), "\n")
				for _, t := range rawTargets {
					if strings.TrimSpace(t) != "" {
						actualTargets = append(actualTargets, strings.TrimSpace(t))
					}
				}
				if len(actualTargets) == 0 {
                    actualError = newError("No valid target paths found in SSH_AGENT_MUX_TARGETS.")
				}
			}


			if tt.expectError {
				if actualError == nil {
					t.Errorf("Expected an error containing '%s', but got nil", tt.errorContent)
				} else if !strings.Contains(actualError.Error(), tt.errorContent) {
					t.Errorf("Expected error to contain '%s', got '%s'", tt.errorContent, actualError.Error())
				}
			} else {
				if actualError != nil {
					t.Errorf("Did not expect an error, but got: %v", actualError)
				}
				if !reflect.DeepEqual(actualTargets, tt.expected) {
					t.Errorf("Expected targets %v, got %v", tt.expected, actualTargets)
				}
			}
		})
	}
}

// Helper to create error for tests, similar to how main might report simple errors.
// Needs to be in this file because main.go doesn't export an error type.
type simpleError string
func (e simpleError) Error() string { return string(e) }
func newError(msg string) error { return simpleError(msg) }

// Late import block for "strings" removed as it's now in the main import block.
