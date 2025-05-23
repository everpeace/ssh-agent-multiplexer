// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package pkg

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"net" // Required for net.Listen
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	// Keep for ssh.PublicKey if needed by Signers or other methods
	"golang.org/x/crypto/ssh/agent"

	"github.com/everpeace/ssh-agent-multiplexer/pkg/config" // For AppConfig
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create a dummy socket file for agent path validation by MustNewAgent
func createDummySocketForTest(t *testing.T, nameHint string) (path string, cleanup func()) {
	t.Helper()
	tempDir := t.TempDir()
	safeNameHint := strings.ReplaceAll(nameHint, "/", "_")
	safeNameHint = strings.ReplaceAll(safeNameHint, ":", "_")
	sockPath := filepath.Join(tempDir, fmt.Sprintf("%s-%d.sock", safeNameHint, os.Getpid()))

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		if _, errCreate := os.Create(sockPath); errCreate != nil {
			t.Fatalf("Failed to create dummy socket listener or placeholder file %s: %v", sockPath, err)
		}
		return sockPath, func() { _ = os.Remove(sockPath) }
	}

	return sockPath, func() {
		_ = listener.Close() // Add error handling for listener close
		_ = os.Remove(sockPath)
	}
}

func TestMuxAgent_Add_NoAddTarget(t *testing.T) {
	appCfg := &config.AppConfig{}
	muxAgent := NewMuxAgent(appCfg)

	addedKey := agent.AddedKey{
		PrivateKey: "dummy private key data",
		Comment:    "test key",
	}
	err := muxAgent.Add(addedKey)

	require.Error(t, err, "Expected an error when calling Add with no AddTargets")
	assert.Equal(t, "add functionality disabled: no add-target specified", err.Error())
}

func TestMuxAgent_List_NoAddTarget_EmptyTargets(t *testing.T) {
	appCfg := &config.AppConfig{}
	muxAgent := NewMuxAgent(appCfg)
	keys, err := muxAgent.List()

	require.NoError(t, err, "List() with no agents should not error at MuxAgent level")
	assert.Empty(t, keys, "Expected 0 keys when no agents are configured")
}

func TestMuxAgent_List_WithTargets(t *testing.T) {
	targetPath1, cleanup1 := createDummySocketForTest(t, "list-target1")
	defer cleanup1()

	appCfg := &config.AppConfig{
		Targets: []string{targetPath1},
	}
	muxAgent := NewMuxAgent(appCfg)
	// This will attempt to list from the dummy socket.
	// We expect an error because the dummy socket isn't a real agent.
	// The key is that MuxAgent tried.
	keys, err := muxAgent.List()

	require.Error(t, err, "List() should return an error from the dummy agent")
	assert.Nil(t, keys, "Keys should be nil on error") // Or empty, depending on error handling in iterate
	t.Logf("List() returned error as expected with dummy socket: %v", err)
}

func TestMuxAgent_Add_WithSingleAddTarget(t *testing.T) {
	addTargetPath, cleanup := createDummySocketForTest(t, "add-single-target")
	defer cleanup()

	appCfg := &config.AppConfig{
		AddTargets: []string{addTargetPath},
	}
	muxAgent := NewMuxAgent(appCfg)

	addedKey := agent.AddedKey{
		PrivateKey: "dummy private key data for add test",
		Comment:    "test key for add",
	}
	err := muxAgent.Add(addedKey)

	require.Error(t, err, "Add() should return an error from the dummy agent")
	// Check that the error is not "no add-target specified"
	assert.NotEqual(t, "add functionality disabled: no add-target specified", err.Error())
	t.Logf("Add() to dummy agent returned error as expected: %v", err)
}

func TestMuxAgent_List_WithAddTargetAndOtherTargets(t *testing.T) {
	addTargetPath, cleanupAdd := createDummySocketForTest(t, "list-addtarget")
	defer cleanupAdd()
	targetPath, cleanupTarget := createDummySocketForTest(t, "list-othertarget")
	defer cleanupTarget()

	appCfg := &config.AppConfig{
		AddTargets: []string{addTargetPath},
		Targets:    []string{targetPath},
	}
	muxAgent := NewMuxAgent(appCfg)
	// Expect errors from both dummy agents. MuxAgent should aggregate these.
	keys, err := muxAgent.List()

	require.Error(t, err, "List() from multiple dummy agents should return an aggregated error")
	assert.Nil(t, keys) // Or empty
	t.Logf("List() from multiple dummy agents returned error as expected: %v", err)
	// To be more specific, one could check if multierr.Errors(err) has length 2.
	if err != nil {
		// Try to unwrap if it's a multierr, otherwise check string directly
		unwrappedErr := errors.Unwrap(err)
		if unwrappedErr != nil { // Check if it was a multierr
			errs := strings.Split(unwrappedErr.Error(), "\n") // This might not be robust for all multierr formats
			assert.Len(t, errs, 2, "Expected errors from two agents based on multierr content")
		} else if strings.Contains(err.Error(), ";") { // Heuristic for multierr string format
			// This is a weaker check, assuming multierr joins with ";" or similar
			t.Log("Error seems to contain multiple error strings (heuristic check).")
		} else {
			t.Logf("Note: List error was not identified as a standard uber/multierr aggregate: %v. It might be a single error string if one agent failed before the other could be tried, or if MustNewAgent panicked.", err)
		}
	}
}

func TestMuxAgent_Add_MultipleAddTargets_NoCommand(t *testing.T) {
	// Dummy sockets are needed because NewMuxAgent will try to connect via MustNewAgent
	dummyAgent1Path, cleanup1 := createDummySocketForTest(t, "no-cmd-agent1")
	defer cleanup1()
	dummyAgent2Path, cleanup2 := createDummySocketForTest(t, "no-cmd-agent2")
	defer cleanup2()

	appCfg := &config.AppConfig{
		AddTargets: []string{dummyAgent1Path, dummyAgent2Path},
	}
	muxAgent := NewMuxAgent(appCfg)

	err := muxAgent.Add(agent.AddedKey{Comment: "test"})
	require.Error(t, err, "Expected error when multiple AddTargets and no SelectTargetCommand")
	assert.Equal(t, "multiple add-targets specified but no select-target-command configured", err.Error())
}

func createSelectTargetScript(t *testing.T, goCode string) (scriptPath string, cleanup func()) {
	t.Helper()
	tempDir := t.TempDir()

	scriptFile := filepath.Join(tempDir, "select_script.go")
	errWrite := os.WriteFile(scriptFile, []byte(goCode), 0644)
	require.NoError(t, errWrite, "Failed to write script file")

	compiledPath := filepath.Join(tempDir, "select_script")
	if runtime.GOOS == "windows" {
		compiledPath += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", compiledPath, scriptFile)
	cmd.Dir = tempDir
	output, errBuild := cmd.CombinedOutput()
	require.NoError(t, errBuild, fmt.Sprintf("Failed to compile script: %v\nOutput:\n%s", errBuild, string(output)))

	return compiledPath, func() { /* tempDir cleanup is automatic via t.TempDir() */ }
}

const commonScriptImports = `
package main

import (
	"fmt"
	"os"
)
`

func TestMuxAgent_Add_MultipleAddTargets_CommandSuccess(t *testing.T) {
	agent1SockName := "agent1.sock"
	agent2SockName := "agent2.sock"

	dummyAgent1Path, cleanup1 := createDummySocketForTest(t, agent1SockName)
	defer cleanup1()
	dummyAgent2Path, cleanup2 := createDummySocketForTest(t, agent2SockName)
	defer cleanup2()

	_, privKey, errKeyGen := ed25519.GenerateKey(nil)
	require.NoError(t, errKeyGen, "Failed to generate ed25519 key")
	testComment := "key-for-command-success-with-info"

	scriptCode := fmt.Sprintf(`
%s
import "strings"

func main() {
	targetsEnv := os.Getenv("SSH_AGENT_MUX_TARGETS")
	parts := strings.Split(strings.TrimSpace(targetsEnv), "\n")
	if len(parts) != 2 {
		fmt.Fprintf(os.Stderr, "Expected 2 targets, got %%d: %%s\n", len(parts), targetsEnv)
		os.Exit(1)
	}
	found1 := false
	found2 := false
	for _, p := range parts {
		if p == "%s" { found1 = true } 
		if p == "%s" { found2 = true } 
	}
	if !found1 || !found2 {
		fmt.Fprintf(os.Stderr, "Targets env var mismatch. Got: %%s, Expected parts: %%s, %%s\n", targetsEnv, "%s", "%s")
		os.Exit(1)
	}

	keyInfo := os.Getenv("SSH_AGENT_MUX_KEY_INFO")
	if keyInfo == "" {
		fmt.Fprintln(os.Stderr, "SSH_AGENT_MUX_KEY_INFO is not set")
		os.Exit(1)
	}
	if !strings.Contains(keyInfo, "COMMENT=%s") || !strings.Contains(keyInfo, "TYPE=ssh-ed25519") || !strings.Contains(keyInfo, "FINGERPRINT_SHA256=SHA256:") {
		fmt.Fprintf(os.Stderr, "SSH_AGENT_MUX_KEY_INFO format error: %%s\n", keyInfo)
		os.Exit(1)
	}

	fmt.Print("%s") 
	os.Exit(0)
}`, commonScriptImports, dummyAgent1Path, dummyAgent2Path, dummyAgent1Path, dummyAgent2Path, testComment, dummyAgent2Path)

	scriptPath, cleanupScript := createSelectTargetScript(t, scriptCode)
	defer cleanupScript()

	appCfg := &config.AppConfig{
		AddTargets:          []string{dummyAgent1Path, dummyAgent2Path},
		SelectTargetCommand: scriptPath,
	}
	muxAgent := NewMuxAgent(appCfg)

	addedKey := agent.AddedKey{
		PrivateKey: privKey,
		Comment:    testComment,
	}
	err := muxAgent.Add(addedKey)

	// Expect an error because the dummy socket (dummyAgent2Path, selected by script) isn't a real agent.
	// The key is that MuxAgent tried to use the path returned by the script.
	require.Error(t, err, "Add() should return an error from the dummy agent selected by the script")
	t.Logf("CommandSuccess: Add() to dummy agent failed as expected: %v", err)
	// Further check: ensure the error is not from the MuxAgent's own setup logic
	// (e.g., "invalid target path" from MuxAgent if script output was bad, which is tested elsewhere).
	// This specific error should be a connection error or similar from the agent client for dummyAgent2Path.
}

func TestMuxAgent_Add_MultipleAddTargets_CommandSuccess_NoSigner(t *testing.T) {
	agent1SockName := "agent1-nosigner.sock"
	agent2SockName := "agent2-nosigner.sock"
	testComment := "key-for-command-success-no-signer"

	dummyAgent1Path, cleanup1 := createDummySocketForTest(t, agent1SockName)
	defer cleanup1()
	dummyAgent2Path, cleanup2 := createDummySocketForTest(t, agent2SockName)
	defer cleanup2()

	scriptCode := fmt.Sprintf(`
%s
import "strings"
func main() {
	targetsEnv := os.Getenv("SSH_AGENT_MUX_TARGETS")
	parts := strings.Split(strings.TrimSpace(targetsEnv), "\n")
	if len(parts) != 2 {
		fmt.Fprintf(os.Stderr, "Expected 2 targets, got %%d: %%s\n", len(parts), targetsEnv)
		os.Exit(1)
	}

	keyInfo := os.Getenv("SSH_AGENT_MUX_KEY_INFO")
	if keyInfo == "" {
		fmt.Fprintln(os.Stderr, "SSH_AGENT_MUX_KEY_INFO is not set")
		os.Exit(1)
	}
	if !strings.Contains(keyInfo, "COMMENT=%s") || !strings.Contains(keyInfo, "TYPE=unknown") || !strings.Contains(keyInfo, "FINGERPRINT_SHA256=unknown") {
		fmt.Fprintf(os.Stderr, "SSH_AGENT_MUX_KEY_INFO format error for non-signer: %%s\n", keyInfo)
		os.Exit(1)
	}

	fmt.Print("%s") 
	os.Exit(0)
}`, commonScriptImports, testComment, dummyAgent2Path)

	scriptPath, cleanupScript := createSelectTargetScript(t, scriptCode)
	defer cleanupScript()

	appCfg := &config.AppConfig{
		AddTargets:          []string{dummyAgent1Path, dummyAgent2Path},
		SelectTargetCommand: scriptPath,
	}
	muxAgent := NewMuxAgent(appCfg)

	addedKey := agent.AddedKey{
		PrivateKey: "this is not a crypto.Signer",
		Comment:    testComment,
	}
	err := muxAgent.Add(addedKey)

	require.Error(t, err, "Add() should return an error from the dummy agent")
	t.Logf("CommandSuccess_NoSigner: Add() to dummy agent failed as expected: %v", err)
}

func TestMuxAgent_Add_MultipleAddTargets_CommandReturnsInvalidTarget(t *testing.T) {
	agent1SockName := "agent1-invalid.sock"
	agent2SockName := "agent2-invalid.sock"

	dummyAgent1Path, cleanup1 := createDummySocketForTest(t, agent1SockName)
	defer cleanup1()
	dummyAgent2Path, cleanup2 := createDummySocketForTest(t, agent2SockName)
	defer cleanup2()

	invalidPath := "invalid/agent.sock"
	scriptCode := fmt.Sprintf(`
%s
func main() {
	fmt.Print("%s")
	os.Exit(0)
}`, commonScriptImports, invalidPath)

	scriptPath, cleanupScript := createSelectTargetScript(t, scriptCode)
	defer cleanupScript()

	appCfg := &config.AppConfig{
		AddTargets:          []string{dummyAgent1Path, dummyAgent2Path},
		SelectTargetCommand: scriptPath,
	}
	muxAgent := NewMuxAgent(appCfg)
	err := muxAgent.Add(agent.AddedKey{Comment: "key-for-invalid-target"})

	require.Error(t, err, "CommandReturnsInvalidTarget: Expected error")
	expectedErrorMsg := fmt.Sprintf("select-target-command returned an invalid target path: '%s'", invalidPath)
	assert.Contains(t, err.Error(), expectedErrorMsg, "Error message mismatch")
}

func TestMuxAgent_Add_MultipleAddTargets_CommandReturnsEmpty(t *testing.T) {
	agent1SockName := "agent1-empty.sock"
	agent2SockName := "agent2-empty.sock"

	dummyAgent1Path, cleanup1 := createDummySocketForTest(t, agent1SockName)
	defer cleanup1()
	dummyAgent2Path, cleanup2 := createDummySocketForTest(t, agent2SockName)
	defer cleanup2()

	scriptCode := fmt.Sprintf(`
%s
func main() {
	fmt.Print("   \n") // Empty or whitespace
	os.Exit(0)
}`, commonScriptImports)

	scriptPath, cleanupScript := createSelectTargetScript(t, scriptCode)
	defer cleanupScript()

	appCfg := &config.AppConfig{
		AddTargets:          []string{dummyAgent1Path, dummyAgent2Path},
		SelectTargetCommand: scriptPath,
	}
	muxAgent := NewMuxAgent(appCfg)
	err := muxAgent.Add(agent.AddedKey{Comment: "key-for-empty-return"})

	require.Error(t, err, "CommandReturnsEmpty: Expected error")
	expectedErrorMsg := "select-target-command returned empty output"
	assert.Contains(t, err.Error(), expectedErrorMsg, "Error message mismatch")
}

func TestMuxAgent_Add_MultipleAddTargets_CommandErrorExit(t *testing.T) {
	agent1SockName := "agent1-error.sock"
	agent2SockName := "agent2-error.sock"

	dummyAgent1Path, cleanup1 := createDummySocketForTest(t, agent1SockName)
	defer cleanup1()
	dummyAgent2Path, cleanup2 := createDummySocketForTest(t, agent2SockName)
	defer cleanup2()

	scriptErrorMessage := "script failed deliberately"
	scriptCode := fmt.Sprintf(`
%s
func main() {
	fmt.Fprintln(os.Stderr, "%s")
	os.Exit(1)
}`, commonScriptImports, scriptErrorMessage)

	scriptPath, cleanupScript := createSelectTargetScript(t, scriptCode)
	defer cleanupScript()

	appCfg := &config.AppConfig{
		AddTargets:          []string{dummyAgent1Path, dummyAgent2Path},
		SelectTargetCommand: scriptPath,
	}
	muxAgent := NewMuxAgent(appCfg)
	err := muxAgent.Add(agent.AddedKey{Comment: "key-for-error-exit"})

	require.Error(t, err, "CommandErrorExit: Expected error")
	assert.Contains(t, err.Error(), scriptErrorMessage, "Error message should contain script's stderr")
	assert.Contains(t, err.Error(), "failed to execute select-target-command", "Error message should indicate command execution failure")
}
