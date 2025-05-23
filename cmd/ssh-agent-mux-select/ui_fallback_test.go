// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestPromptUserCLIFallback(t *testing.T) {
	targets := []string{"/tmp/agent.1", "/tmp/agent.2", "/tmp/agent.3"}
	keyInfo := "test-key-info"

	tests := []struct {
		name         string
		input        string
		expectedPath string
		expectError  bool
		errorContent string
		keyInfo      string // Test with and without keyInfo
	}{
		{
			name:         "Valid selection first target",
			input:        "1\n",
			expectedPath: targets[0],
			expectError:  false,
			keyInfo:      keyInfo,
		},
		{
			name:         "Valid selection last target",
			input:        fmt.Sprintf("%d\n", len(targets)),
			expectedPath: targets[len(targets)-1],
			expectError:  false,
			keyInfo:      keyInfo,
		},
		{
			name:         "Valid selection no keyInfo",
			input:        "1\n",
			expectedPath: targets[0],
			expectError:  false,
			keyInfo:      "", // Test without keyInfo
		},
		{
			name:         "Invalid input not a number",
			input:        "abc\n",
			expectError:  true,
			errorContent: "invalid input, not a number",
			keyInfo:      keyInfo,
		},
		{
			name:         "Invalid selection too low",
			input:        "0\n",
			expectError:  true,
			errorContent: "invalid selection: number out of range",
			keyInfo:      keyInfo,
		},
		{
			name:         "Invalid selection too high",
			input:        fmt.Sprintf("%d\n", len(targets)+1),
			expectError:  true,
			errorContent: "invalid selection: number out of range",
			keyInfo:      keyInfo,
		},
		{
			name:         "Empty input",
			input:        "\n",
			expectError:  true,
			errorContent: "invalid input, not a number", // Because strconv.Atoi("") fails
			keyInfo:      keyInfo,
		},
		{
			name:        "EOF on input",
			input:       "", // Simulates immediate EOF
			expectError: true,
			// errorContent will depend on "failed to read input" or specific EOF error from ReadString
			// For bufio.Reader.ReadString, EOF is often "EOF" or part of a larger message.
			// Let's check for a substring.
			errorContent: "failed to read input",
			keyInfo:      keyInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock stdin
			mockStdinReader, mockStdinWriter, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe for stdin: %v", err)
			}
			_, err = mockStdinWriter.Write([]byte(tt.input))
			if err != nil {
				t.Fatalf("Failed to write to mock stdin: %v", err)
			}
			if err := mockStdinWriter.Close(); err != nil {
				t.Logf("Test '%s': Failed to close mockStdinWriter: %v", tt.name, err)
			}

			originalStdin := os.Stdin
			os.Stdin = mockStdinReader
			defer func() {
				os.Stdin = originalStdin
				if err := mockStdinReader.Close(); err != nil {
					t.Logf("Test '%s': Failed to close mockStdinReader in defer: %v", tt.name, err)
				}
			}()

			// Capture stderr (where prompts are written)
			originalStderr := os.Stderr
			stderrReader, stderrWriter, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe for stderr: %v", err)
			}
			os.Stderr = stderrWriter
			defer func() {
				os.Stderr = originalStderr
				if err := stderrReader.Close(); err != nil {
					t.Logf("Test '%s': Failed to close stderrReader in defer: %v", tt.name, err)
				}
				if err := stderrWriter.Close(); err != nil { // Already captured by this point by the test logic
					t.Logf("Test '%s': Failed to close stderrWriter in defer: %v", tt.name, err)
				}
			}()

			selectedPath, err := promptUserCLIFallback(targets, tt.keyInfo)

			// Stop capturing stderr and read its content
			// Note: The deferred close for stderrWriter will run after this explicit close.
			// This is fine as multiple Close calls on os.File are often idempotent (though not ideal).
			// The main purpose here is to ensure the write side is closed before reading.
			if err := stderrWriter.Close(); err != nil {
				t.Fatalf("Test '%s': Failed to close stderrWriter: %v", tt.name, err) // Fatal as it might affect reading output
			}
			stderrOutputBytes, _ := io.ReadAll(stderrReader) // Error on ReadAll is not handled yet by errcheck, but not part of this task.
			stderrOutput := string(stderrOutputBytes)
			// t.Logf("Stderr output for test '%s':\n%s", tt.name, stderrOutput) // For debugging

			// Check keyInfo in prompt
			if tt.keyInfo != "" {
				expectedPromptFragment := fmt.Sprintf("for the key '%s'", tt.keyInfo)
				if !strings.Contains(stderrOutput, expectedPromptFragment) {
					t.Errorf("Expected stderr prompt to contain '%s', got:\n%s", expectedPromptFragment, stderrOutput)
				}
			} else {
				expectedPromptFragment := "Select a target agent for the key:"
				if !strings.Contains(stderrOutput, expectedPromptFragment) {
					t.Errorf("Expected stderr prompt to contain '%s' (no keyInfo), got:\n%s", expectedPromptFragment, stderrOutput)
				}
			}

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected an error containing '%s', but got nil", tt.errorContent)
				} else if !strings.Contains(err.Error(), tt.errorContent) {
					t.Errorf("Expected error to contain '%s', got '%s'", tt.errorContent, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Did not expect an error, but got: %v", err)
				}
				if selectedPath != tt.expectedPath {
					t.Errorf("Expected selected path '%s', got '%s'", tt.expectedPath, selectedPath)
				}
			}
		})
	}
}

// For TestPromptUserCLIFallback_EOFCase where input is just "",
// bufio.NewReader(os.Stdin).ReadString('\n') directly gets an io.EOF
// This test specifically checks that io.EOF is handled.
func TestPromptUserCLIFallback_EOFCase(t *testing.T) {
	targets := []string{"/tmp/agent.1"}

	// Mock stdin to immediately EOF
	mockStdinReader, mockStdinWriter, errPipe := os.Pipe()
	if errPipe != nil {
		t.Fatalf("EOFCase: Failed to create pipe for stdin: %v", errPipe)
	}
	if err := mockStdinWriter.Close(); err != nil { // Close immediately to simulate EOF
		t.Logf("EOFCase: Failed to close mockStdinWriter: %v", err)
	}

	originalStdin := os.Stdin
	os.Stdin = mockStdinReader
	defer func() {
		os.Stdin = originalStdin
		if err := mockStdinReader.Close(); err != nil {
			t.Logf("EOFCase: Failed to close mockStdinReader in defer: %v", err)
		}
	}()

	// Capture stderr
	originalStderr := os.Stderr
	var stderrBuf bytes.Buffer
	rStderr, wStderr, errPipe := os.Pipe()
	if errPipe != nil {
		t.Fatalf("EOFCase: Failed to create pipe for stderr: %v", errPipe)
	}
	os.Stderr = wStderr
	defer func() {
		os.Stderr = originalStderr
		if err := rStderr.Close(); err != nil {
			t.Logf("EOFCase: Failed to close rStderr in defer: %v", err)
		}
		if err := wStderr.Close(); err != nil { // Already captured by this point by the test logic
			t.Logf("EOFCase: Failed to close wStderr in defer: %v", err)
		}
	}()

	_, err := promptUserCLIFallback(targets, "test-key")

	if err := wStderr.Close(); err != nil {
		t.Fatalf("EOFCase: Failed to close wStderr: %v", err) // Fatal as it might affect reading output
	}
	if _, err := io.Copy(&stderrBuf, rStderr); err != nil { // Read stderr content
		t.Errorf("EOFCase: Failed to copy stderr output: %v", err)
	}

	if err == nil {
		t.Fatalf("Expected an error on EOF, but got nil")
	}
	// Check if the error is or contains io.EOF or a message indicating read failure
	// The error from promptUserCLIFallback wraps the original error.
	if !strings.Contains(err.Error(), "failed to read input") && !strings.Contains(err.Error(), io.EOF.Error()) {
		t.Errorf("Expected error to relate to input read failure or EOF, got: %v", err)
	}
}
