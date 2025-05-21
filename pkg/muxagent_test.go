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
	muxAgent := NewMuxAgent([]*Agent{}, nil) // Targets, AddTarget

	addedKey := agent.AddedKey{
		PrivateKey:   "dummy private key data", // Minimal data
		Comment:      "test key",
		LifetimeSecs: 0,
		// Confirm field removed as it's not in older ssh/agent versions
	}
	err := muxAgent.Add(addedKey)

	if err == nil {
		t.Errorf("Expected an error when calling Add with no AddTarget, got nil")
	}

	expectedErrMsg := "add functionality disabled: no add-target specified"
	if err.Error() != expectedErrMsg {
		t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
	}
}

func TestMuxAgent_List_NoAddTarget_EmptyTargets(t *testing.T) {
	muxAgent := NewMuxAgent([]*Agent{}, nil) // AddTarget is nil, Targets is empty
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

	muxAgent := NewMuxAgent([]*Agent{targetAgent}, nil) // AddTarget is nil

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
		path:  "mock/addtarget",
	}

	muxAgent := NewMuxAgent([]*Agent{}, addAgentInstance)

	addedKey := agent.AddedKey{
		PrivateKey:   "dummy private key data for add test",
		Comment:      "test key for add",
		LifetimeSecs: 0,
		// Confirm field removed
	}
	err := muxAgent.Add(addedKey)

	if err != nil {
		t.Errorf("Expected no error when calling Add with an AddTarget, got %v", err)
	}
	if !mockAddAgent.addCalled {
		t.Errorf("Expected mockAddTarget.Add to be called")
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
	
	muxAgent := NewMuxAgent([]*Agent{target1Instance}, addAgentInstance)

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

	if !reflect.DeepEqual(keys[0], dummyKeyAddTarget) {
		t.Errorf("Expected first key to be from AddTarget [%v], got [%v]", dummyKeyAddTarget, keys[0])
	}
	if !reflect.DeepEqual(keys[1], dummyKeyTarget1) {
		t.Errorf("Expected second key to be from Target1 [%v], got [%v]", dummyKeyTarget1, keys[1])
	}
}
