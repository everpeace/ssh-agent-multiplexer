package pkg

import (
	"errors"
	"reflect"
	"testing"

	"golang.org/x/crypto/ssh" // Keep for ssh.PublicKey if needed by Signers or other methods
	"golang.org/x/crypto/ssh/agent"
)

// mockAgent implements agent.ExtendedAgent for testing.
type mockAgent struct {
	keys            []*agent.Key
	listCalled      bool
	addCalled       bool
	addedKey        agent.AddedKey
	signers         []ssh.Signer
	signersCalled   bool
	removeCalled    bool
	removeAllCalled bool
	lockCalled      bool
	unlockCalled    bool
	extensionCalled bool
	path            string // For logging/identification if necessary
}

// List implements agent.Agent
func (m *mockAgent) List() ([]*agent.Key, error) {
	m.listCalled = true
	return m.keys, nil
}

// Sign implements agent.Agent
func (m *mockAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	// For this test suite, Sign is not directly tested on the mock.
	return nil, errors.New("mockAgent.Sign not implemented")
}

// Add implements agent.Agent
func (m *mockAgent) Add(key agent.AddedKey) error {
	m.addCalled = true
	m.addedKey = key
	return nil
}

// Remove implements agent.Agent
func (m *mockAgent) Remove(key ssh.PublicKey) error {
	m.removeCalled = true
	// For this test suite, Remove is not directly tested on the mock.
	return nil
}

// RemoveAll implements agent.Agent
func (m *mockAgent) RemoveAll() error {
	m.removeAllCalled = true
	// For this test suite, RemoveAll is not directly tested on the mock.
	return nil
}

// Lock implements agent.Agent
func (m *mockAgent) Lock(passphrase []byte) error {
	m.lockCalled = true
	// For this test suite, Lock is not directly tested on the mock.
	return nil
}

// Unlock implements agent.Agent
func (m *mockAgent) Unlock(passphrase []byte) error {
	m.unlockCalled = true
	// For this test suite, Unlock is not directly tested on the mock.
	return nil
}

// Signers implements agent.Agent
func (m *mockAgent) Signers() ([]ssh.Signer, error) {
	m.signersCalled = true
	return m.signers, nil
}

// Extension implements agent.ExtendedAgent
func (m *mockAgent) Extension(extensionType string, contents []byte) ([]byte, error) {
	m.extensionCalled = true
	// For this test suite, Extension is not directly tested on the mock.
	return nil, agent.ErrExtensionUnsupported
}

// SignWithFlags implements agent.ExtendedAgent
func (m *mockAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	// For this test suite, SignWithFlags is not directly tested on the mock.
	return nil, errors.New("mockAgent.SignWithFlags not implemented")
}


func TestMuxAgent_Add_NoAddTarget(t *testing.T) {
	muxAgent := NewMuxAgent([]*Agent{}, nil, "") // Targets, AddTargets, SelectTargetCommand

	addedKey := agent.AddedKey{
		PrivateKey: "dummy private key data", // Minimal data
		Comment:    "test key",
	}
	err := muxAgent.Add(addedKey)

	if err == nil {
		t.Fatalf("Expected an error when calling Add with no AddTargets, got nil")
	}

	expectedErrMsg := "add functionality disabled: no add-target specified"
	if err.Error() != expectedErrMsg {
		t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
	}
}

func TestMuxAgent_List_NoAddTarget_EmptyTargets(t *testing.T) {
	muxAgent := NewMuxAgent([]*Agent{}, nil, "") // AddTargets is nil, Targets is empty
	keys, err := muxAgent.List()

	if err != nil {
		t.Errorf("List() with no AddTarget and no Targets returned error: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("Expected 0 keys when AddTarget is nil and Targets is empty, got %d", len(keys))
	}
}

func TestMuxAgent_List_NoAddTarget_WithTargets(t *testing.T) {
	dummyKey1 := &agent.Key{Format: "ssh-rsa", Blob: []byte("testkey1"), Comment: "key1"}
	mockListAgent := &mockAgent{keys: []*agent.Key{dummyKey1}}

	targetAgent := &Agent{
		agent: mockListAgent,
		path:  "mock/target1",
	}

	muxAgent := NewMuxAgent([]*Agent{targetAgent}, nil, "") // AddTargets is nil

	keys, err := muxAgent.List()

	if err != nil {
		t.Fatalf("List() with a target and no AddTarget returned error: %v", err)
	}
	if !mockListAgent.listCalled {
		t.Errorf("Expected mockTarget.List to be called")
	}
	if len(keys) != 1 {
		t.Fatalf("Expected 1 key, got %d", len(keys))
	}
	if !reflect.DeepEqual(keys[0], dummyKey1) {
		t.Errorf("Expected key [%v], got [%v]", dummyKey1, keys[0])
	}
}

func TestMuxAgent_Add_WithAddTarget(t *testing.T) {
	mockAddAgent := &mockAgent{path: "mock/addtarget"}
	addAgentInstance := &Agent{
		agent: mockAddAgent,
		path:  "mock/addtarget", // Ensure path is set for mockAgent
	}

	// For SingleAddTarget, NewMuxAgent expects a slice for addTargets
	muxAgent := NewMuxAgent([]*Agent{}, []*Agent{addAgentInstance}, "") // selectTargetCommand is empty

	addedKey := agent.AddedKey{
		PrivateKey: "dummy private key data for add test",
		Comment:    "test key for add",
	}
	err := muxAgent.Add(addedKey)

	if err != nil {
		t.Fatalf("Expected no error when calling Add with a single AddTarget, got %v", err)
	}
	if !mockAddAgent.addCalled {
		t.Fatalf("Expected mockAddTarget.Add to be called")
	}
	if !reflect.DeepEqual(mockAddAgent.addedKey, addedKey) {
		t.Errorf("Expected added key to be '%v', got '%v'", addedKey, mockAddAgent.addedKey)
	}
}

func TestMuxAgent_List_WithAddTargetAndOtherTargets(t *testing.T) {
	dummyKeyAddTarget := &agent.Key{Format: "ssh-rsa", Blob: []byte("addtargetkey"), Comment: "keyAddTgt"}
	mockAddTargetAgent := &mockAgent{keys: []*agent.Key{dummyKeyAddTarget}, path: "mock/addtarget"}
	addAgentInstance := &Agent{agent: mockAddTargetAgent, path: "mock/addtarget"}

	dummyKeyTarget1 := &agent.Key{Format: "ssh-rsa", Blob: []byte("target1key"), Comment: "keyTgt1"}
	mockTarget1Agent := &mockAgent{keys: []*agent.Key{dummyKeyTarget1}, path: "mock/target1"}
	target1Instance := &Agent{agent: mockTarget1Agent, path: "mock/target1"}

	// NewMuxAgent now takes a slice for addTargets
	muxAgent := NewMuxAgent([]*Agent{target1Instance}, []*Agent{addAgentInstance}, "")

	keys, err := muxAgent.List()
	if err != nil {
		t.Fatalf("List() with AddTarget and other targets returned error: %v", err)
	}

	if !mockAddTargetAgent.listCalled {
		t.Errorf("Expected AddTarget.List to be called")
	}
	if !mockTarget1Agent.listCalled {
		t.Errorf("Expected Target1.List to be called")
	}

	expectedKeyCount := 2
	if len(keys) != expectedKeyCount {
		t.Fatalf("Expected %d keys, got %d", expectedKeyCount, len(keys))
	}

	// The order in iterate is AddTargets then Targets.
	// So keys[0] should be from addAgentInstance, keys[1] from target1Instance.
	foundAddTargetKey := false
	foundTarget1Key := false
	for _, k := range keys {
		if reflect.DeepEqual(k, dummyKeyAddTarget) {
			foundAddTargetKey = true
		}
		if reflect.DeepEqual(k, dummyKeyTarget1) {
			foundTarget1Key = true
		}
	}

	if !foundAddTargetKey {
		t.Errorf("Expected to find AddTarget key [%v] in List results, but not found. Keys: %v", dummyKeyAddTarget, keys)
	}
	if !foundTarget1Key {
		t.Errorf("Expected to find Target1 key [%v] in List results, but not found. Keys: %v", dummyKeyTarget1, keys)
	}
}

// --- New Test Cases for Multiple Add Targets and SelectTargetCommand ---

func TestMuxAgent_Add_MultipleAddTargets_NoCommand(t *testing.T) {
	mockAgent1 := &Agent{agent: &mockAgent{path: "agent1.sock"}, path: "agent1.sock"}
	mockAgent2 := &Agent{agent: &mockAgent{path: "agent2.sock"}, path: "agent2.sock"}

	// selectTargetCommand is empty
	muxAgent := NewMuxAgent([]*Agent{}, []*Agent{mockAgent1, mockAgent2}, "")

	err := muxAgent.Add(agent.AddedKey{Comment: "test"})
	if err == nil {
		t.Fatalf("Expected error when multiple AddTargets and no SelectTargetCommand, got nil")
	}
	expectedMsg := "multiple add-targets specified but no select-target-command configured"
	if err.Error() != expectedMsg {
		t.Errorf("Expected error '%s', got '%s'", expectedMsg, err.Error())
	}
}

// Helper function to create and compile a temporary Go script
func createSelectTargetScript(t *testing.T, goCode string) (scriptPath string, cleanup func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "select_script_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	scriptFile := filepath.Join(tempDir, "select_script.go")
	err = os.WriteFile(scriptFile, []byte(goCode), 0644)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to write script file: %v", err)
	}

	compiledPath := filepath.Join(tempDir, "select_script")
	if runtime.GOOS == "windows" {
		compiledPath += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", compiledPath, scriptFile)
	cmd.Dir = tempDir // Ensure context is correct for build if script has local imports (not in this case)
	output, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to compile script: %v\nOutput:\n%s", err, string(output))
	}

	return compiledPath, func() { os.RemoveAll(tempDir) }
}

const commonScriptImports = `
package main

import (
	"fmt"
	"os"
	"strings"
)
`

// Need to import ed25519 for key generation
import (
	"crypto/ed25519"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"fmt"
)


func TestMuxAgent_Add_MultipleAddTargets_CommandSuccess(t *testing.T) {
	agent1Path := "agent1.sock"
	agent2Path := "agent2.sock" // This one will be selected by the script
	mockAgent1 := &mockAgent{path: agent1Path}
	mockAgent2 := &mockAgent{path: agent2Path} // The agent we expect to be called

	agentInstance1 := &Agent{agent: mockAgent1, path: agent1Path}
	agentInstance2 := &Agent{agent: mockAgent2, path: agent2Path}

	// Generate a real key for testing PrivateKey type assertion and info derivation
	_, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("Failed to generate ed25519 key: %v", err)
	}
	testComment := "key-for-command-success-with-info"

	scriptCode := fmt.Sprintf(`
%s
func main() {
	targetsEnv := os.Getenv("SSH_AGENT_MUX_TARGETS")
	// Allow any order for targetsEnv
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
		fmt.Fprintf(os.Stderr, "Targets env var mismatch. Got: %%s, Expected parts: %s, %s\n", targetsEnv, "%s", "%s")
		os.Exit(1)
	}

	keyInfo := os.Getenv("SSH_AGENT_MUX_KEY_INFO")
	if keyInfo == "" {
		fmt.Fprintln(os.Stderr, "SSH_AGENT_MUX_KEY_INFO is not set")
		os.Exit(1)
	}
	// Basic format check
	if !strings.Contains(keyInfo, "COMMENT=%s") || !strings.Contains(keyInfo, "TYPE=ssh-ed25519") || !strings.Contains(keyInfo, "FINGERPRINT_SHA256=SHA256:") {
		fmt.Fprintf(os.Stderr, "SSH_AGENT_MUX_KEY_INFO format error: %%s\n", keyInfo)
		os.Exit(1)
	}

	fmt.Print("%s") // Script selects agent2Path
	os.Exit(0)
}`, commonScriptImports, agent1Path, agent2Path, agent1Path, agent2Path, testComment, agent2Path) // agent2Path is selected

	scriptPath, cleanup := createSelectTargetScript(t, scriptCode)
	defer cleanup()

	muxAgent := NewMuxAgent([]*Agent{}, []*Agent{agentInstance1, agentInstance2}, scriptPath)
	addedKey := agent.AddedKey{
		PrivateKey: privKey, // Use the real crypto.Signer
		Comment:    testComment,
	}
	err = muxAgent.Add(addedKey)

	if err != nil {
		// If the script exits with error, err will contain stderr output.
		t.Fatalf("CommandSuccess: Add() failed: %v", err)
	}
	if mockAgent1.addCalled {
		t.Errorf("CommandSuccess: agent1 should not have Add called")
	}
	if !mockAgent2.addCalled {
		t.Fatalf("CommandSuccess: agent2 should have Add called but was not")
	}
	if mockAgent2.addedKey.Comment != addedKey.Comment {
		t.Errorf("CommandSuccess: agent2 received wrong key. Expected comment '%s', got '%s'", addedKey.Comment, mockAgent2.addedKey.Comment)
	}
}

// TestMuxAgent_Add_MultipleAddTargets_CommandSuccess_NoPrivateKeyTypeAssertion tests
// the scenario where PrivateKey in agent.AddedKey is not a crypto.Signer
// and thus TYPE and FINGERPRINT_SHA256 should be "unknown".
func TestMuxAgent_Add_MultipleAddTargets_CommandSuccess_NoSigner(t *testing.T) {
	agent1Path := "agent1-nosigner.sock"
	agent2Path := "agent2-nosigner.sock" // This one will be selected by the script
	mockAgent1 := &mockAgent{path: agent1Path}
	mockAgent2 := &mockAgent{path: agent2Path} // The agent we expect to be called

	agentInstance1 := &Agent{agent: mockAgent1, path: agent1Path}
	agentInstance2 := &Agent{agent: mockAgent2, path: agent2Path}
	testComment := "key-for-command-success-no-signer"

	scriptCode := fmt.Sprintf(`
%s
func main() {
	targetsEnv := os.Getenv("SSH_AGENT_MUX_TARGETS")
	parts := strings.Split(strings.TrimSpace(targetsEnv), "\n")
	if len(parts) != 2 {
		fmt.Fprintf(os.Stderr, "Expected 2 targets, got %%d: %%s\n", len(parts), targetsEnv)
		os.Exit(1)
	}
	// Basic check for targets is sufficient here, focus is on keyInfo

	keyInfo := os.Getenv("SSH_AGENT_MUX_KEY_INFO")
	if keyInfo == "" {
		fmt.Fprintln(os.Stderr, "SSH_AGENT_MUX_KEY_INFO is not set")
		os.Exit(1)
	}
	// Expect "unknown" for TYPE and FINGERPRINT
	if !strings.Contains(keyInfo, "COMMENT=%s") || !strings.Contains(keyInfo, "TYPE=unknown") || !strings.Contains(keyInfo, "FINGERPRINT_SHA256=unknown") {
		fmt.Fprintf(os.Stderr, "SSH_AGENT_MUX_KEY_INFO format error for non-signer: %%s\n", keyInfo)
		os.Exit(1)
	}

	fmt.Print("%s") // Script selects agent2Path
	os.Exit(0)
}`, commonScriptImports, testComment, agent2Path) // agent2Path is selected

	scriptPath, cleanup := createSelectTargetScript(t, scriptCode)
	defer cleanup()

	muxAgent := NewMuxAgent([]*Agent{}, []*Agent{agentInstance1, agentInstance2}, scriptPath)
	addedKey := agent.AddedKey{
		PrivateKey: "this is not a crypto.Signer", // String instead of crypto.Signer
		Comment:    testComment,
	}
	err := muxAgent.Add(addedKey)

	if err != nil {
		t.Fatalf("CommandSuccess_NoSigner: Add() failed: %v", err)
	}
	if !mockAgent2.addCalled {
		t.Fatalf("CommandSuccess_NoSigner: agent2 should have Add called but was not")
	}
	if mockAgent2.addedKey.Comment != addedKey.Comment {
		t.Errorf("CommandSuccess_NoSigner: agent2 received wrong key. Expected comment '%s', got '%s'", addedKey.Comment, mockAgent2.addedKey.Comment)
	}
}


func TestMuxAgent_Add_MultipleAddTargets_CommandReturnsInvalidTarget(t *testing.T) {
	agent1Path := "agent1.sock"
	agent2Path := "agent2.sock"
	mockAgent1 := &mockAgent{path: agent1Path}
	mockAgent2 := &mockAgent{path: agent2Path}
	agentInstance1 := &Agent{agent: mockAgent1, path: agent1Path}
	agentInstance2 := &Agent{agent: mockAgent2, path: agent2Path}

	invalidPath := "invalid/agent.sock"
	scriptCode := fmt.Sprintf(`
%s
func main() {
	fmt.Print("%s")
	os.Exit(0)
}`, commonScriptImports, invalidPath)

	scriptPath, cleanup := createSelectTargetScript(t, scriptCode)
	defer cleanup()

	muxAgent := NewMuxAgent([]*Agent{}, []*Agent{agentInstance1, agentInstance2}, scriptPath)
	err := muxAgent.Add(agent.AddedKey{Comment: "key-for-invalid-target"})

	if err == nil {
		t.Fatalf("CommandReturnsInvalidTarget: Expected error, got nil")
	}
	expectedErrorMsg := fmt.Sprintf("select-target-command returned an invalid target path: '%s'", invalidPath)
	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("CommandReturnsInvalidTarget: Expected error message containing '%s', got '%s'", expectedErrorMsg, err.Error())
	}
	if mockAgent1.addCalled || mockAgent2.addCalled {
		t.Errorf("CommandReturnsInvalidTarget: No agent should have Add called")
	}
}

func TestMuxAgent_Add_MultipleAddTargets_CommandReturnsEmpty(t *testing.T) {
	agent1Path := "agent1.sock"
	agent2Path := "agent2.sock"
	mockAgent1 := &mockAgent{path: agent1Path}
	mockAgent2 := &mockAgent{path: agent2Path}
	agentInstance1 := &Agent{agent: mockAgent1, path: agent1Path}
	agentInstance2 := &Agent{agent: mockAgent2, path: agent2Path}

	scriptCode := fmt.Sprintf(`
%s
func main() {
	fmt.Print("   \n") // Empty or whitespace
	os.Exit(0)
}`, commonScriptImports)

	scriptPath, cleanup := createSelectTargetScript(t, scriptCode)
	defer cleanup()

	muxAgent := NewMuxAgent([]*Agent{}, []*Agent{agentInstance1, agentInstance2}, scriptPath)
	err := muxAgent.Add(agent.AddedKey{Comment: "key-for-empty-return"})

	if err == nil {
		t.Fatalf("CommandReturnsEmpty: Expected error, got nil")
	}
	expectedErrorMsg := "select-target-command returned empty output"
	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("CommandReturnsEmpty: Expected error message containing '%s', got '%s'", expectedErrorMsg, err.Error())
	}
}

func TestMuxAgent_Add_MultipleAddTargets_CommandErrorExit(t *testing.T) {
	agent1Path := "agent1.sock"
	agent2Path := "agent2.sock"
	mockAgent1 := &mockAgent{path: agent1Path}
	mockAgent2 := &mockAgent{path: agent2Path}
	agentInstance1 := &Agent{agent: mockAgent1, path: agent1Path}
	agentInstance2 := &Agent{agent: mockAgent2, path: agent2Path}

	scriptErrorMessage := "script failed deliberately"
	scriptCode := fmt.Sprintf(`
%s
func main() {
	fmt.Fprintln(os.Stderr, "%s")
	os.Exit(1)
}`, commonScriptImports, scriptErrorMessage)

	scriptPath, cleanup := createSelectTargetScript(t, scriptCode)
	defer cleanup()

	muxAgent := NewMuxAgent([]*Agent{}, []*Agent{agentInstance1, agentInstance2}, scriptPath)
	err := muxAgent.Add(agent.AddedKey{Comment: "key-for-error-exit"})

	if err == nil {
		t.Fatalf("CommandErrorExit: Expected error, got nil")
	}
	if !strings.Contains(err.Error(), scriptErrorMessage) {
		t.Errorf("CommandErrorExit: Expected error message to contain script's stderr ('%s'), got '%s'", scriptErrorMessage, err.Error())
	}
	if !strings.Contains(err.Error(), "failed to execute select-target-command") {
		t.Errorf("CommandErrorExit: Expected error message to indicate command execution failure, got '%s'", err.Error())
	}
}

