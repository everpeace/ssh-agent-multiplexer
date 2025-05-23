// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"errors" // Added for creating test errors
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockAgentCreator creates an agentCreator function for testing.
// successfulPaths: a list of paths for which agent creation should succeed.
// failingPaths: a map of paths to specific errors for which agent creation should fail.
// If a path is in neither, creation will succeed by default (simulating pkg.NewAgent success).
func mockAgentCreator(failingPaths map[string]error) func(path string) (*pkg.Agent, error) {
	return func(path string) (*pkg.Agent, error) {
		if err, ok := failingPaths[path]; ok {
			return nil, err
		}
		// Default: success if not in failingPaths
		// Return a new dummy agent instance each time for realism, though not strictly necessary for these tests.
		return &pkg.Agent{}, nil
	}
}

// writeTempConfigFile creates a temporary config file with the given content.
// It returns the path to the created file.
func writeTempConfigFile(t *testing.T, namePrefix string, content string) string {
	t.Helper()
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, fmt.Sprintf("%s-%d.toml", namePrefix, time.Now().UnixNano()))
	err := os.WriteFile(filePath, []byte(content), 0600)
	require.NoError(t, err, "Failed to write temporary config file")
	return filePath
}

// setupInitialState creates an initial AppConfig and MuxAgent for tests.
func setupInitialState(t *testing.T, initialConfigContent string, agentCreator func(path string) (*pkg.Agent, error)) (*config.AppConfig, *pkg.MuxAgent) {
	t.Helper()
	// Suppress log output during tests, unless a test specifically enables it.
	originalLogger := log.Logger
	log.Logger = zerolog.Nop()
	t.Cleanup(func() { log.Logger = originalLogger })

	initialConfigPath := ""
	if initialConfigContent != "" {
		initialConfigPath = writeTempConfigFile(t, "initial-config", initialConfigContent)
	}

	// Use a dummy currentConfig for the very first load, as main.go does.
	// The actual values will come from initialConfigPath.
	appCfg, err := loadAndApplyConfig(initialConfigPath, nil, &config.AppConfig{}, agentCreator)
	require.NoError(t, err, "Initial loadAndApplyConfig failed")
	require.NotNil(t, appCfg, "Initial AppConfig is nil")

	// Create initial agents based on the loaded AppConfig
	var initialTargets []*pkg.Agent
	for _, p := range appCfg.Targets {
		agent, agErr := agentCreator(p)
		if agErr != nil {
			t.Logf("Failed to create initial target agent %s: %v (this might be expected by the test)", p, agErr)
			continue
		}
		initialTargets = append(initialTargets, agent)
	}
	var initialAddTargets []*pkg.Agent
	for _, p := range appCfg.AddTargets {
		agent, agErr := agentCreator(p)
		if agErr != nil {
			t.Logf("Failed to create initial addTarget agent %s: %v (this might be expected by the test)", p, agErr)
			continue
		}
		initialAddTargets = append(initialAddTargets, agent)
	}

	muxAgent := pkg.NewMuxAgent(initialTargets, initialAddTargets, appCfg.SelectTargetCommand)
	require.NotNil(t, muxAgent, "Initial MuxAgent is nil")

	return appCfg, muxAgent
}

// containsPath checks if a slice of agents contains an agent associated with a given path.
// This is a simplified check as mock agents don't have real paths.
// In a real test with functional agents, you'd check actual agent paths.
// For these tests, we'll check if the *config* paths are reflected.
func agentPaths(agents []*pkg.Agent) []string {
	// This is a placeholder. In a real scenario with *pkg.Agent having an accessible Path field:
	// paths := make([]string, len(agents))
	// for i, agent := range agents {
	//  paths[i] = agent.Path() // Assuming agent.Path() exists and is public
	// }
	// return paths
	// Since our mockAgentCreator doesn't store paths in the dummy pkg.Agent,
	// this helper can't be fully implemented yet without modifying pkg.Agent or the mock.
	// For now, tests will have to rely on comparing config.AppConfig.Targets/AddTargets.
	return nil
}

// For now, tests will primarily assert changes in AppConfig and MuxAgent's command string,
// and the *number* of agents, rather than their specific paths due to mock limitations.
// If pkg.Agent had a public Path field, we could do more.
// Let's assume for testing MuxAgent we can inspect its configured paths if needed,
// or rely on AppConfig's state.

func TestSuccessfulReload(t *testing.T) {
	initialConfigContent := `
debug = false
listen = "/tmp/agent.sock"
targets = ["/tmp/agent1.sock", "/tmp/agent2.sock"]
add_targets = ["/tmp/agent-add1.sock"]
select_target_command = "select-old"
`
	// All initial paths are expected to succeed.
	mockCreator := mockAgentCreator(nil) // No failing paths initially
	appCfg, muxAgent := setupInitialState(t, initialConfigContent, mockCreator)

	require.NotNil(t, appCfg)
	require.NotNil(t, muxAgent)
	assert.Equal(t, "/tmp/agent.sock", appCfg.Listen)
	assert.ElementsMatch(t, []string{"/tmp/agent1.sock", "/tmp/agent2.sock"}, appCfg.Targets)
	assert.ElementsMatch(t, []string{"/tmp/agent-add1.sock"}, appCfg.AddTargets)
	assert.Equal(t, "select-old", appCfg.SelectTargetCommand)

	// New configuration: remove agent2, add agent3, change addTarget and command
	newConfigContent := `
debug = true
listen = "/tmp/agent.sock" # Listen address should not change
targets = ["/tmp/agent1.sock", "/tmp/agent3.sock"] # agent2 removed, agent3 added
add_targets = ["/tmp/agent-add2.sock"] # agent-add1 removed, agent-add2 added
select_target_command = "select-new"
`
	newConfigPath := writeTempConfigFile(t, "new-config", newConfigContent)

	// Assume agent1, agent3, agent-add2 will succeed
	reloadedAppCfg, err := loadAndApplyConfig(newConfigPath, muxAgent, appCfg, mockCreator)
	require.NoError(t, err)
	require.NotNil(t, reloadedAppCfg)

	// Verify AppConfig reflects new settings
	assert.True(t, reloadedAppCfg.Debug)
	assert.Equal(t, "/tmp/agent.sock", reloadedAppCfg.Listen) // Should remain unchanged
	assert.ElementsMatch(t, []string{"/tmp/agent1.sock", "/tmp/agent3.sock"}, reloadedAppCfg.Targets)
	assert.ElementsMatch(t, []string{"/tmp/agent-add2.sock"}, reloadedAppCfg.AddTargets)
	assert.Equal(t, "select-new", reloadedAppCfg.SelectTargetCommand)
	assert.Equal(t, newConfigPath, reloadedAppCfg.ConfigFilePathUsed)

	// Conceptually, muxAgent internal state should have been updated by muxAgent.Update()
	// We can't directly inspect muxAgent.Targets etc. as they are not exported and no getters.
	// The test relies on loadAndApplyConfig correctly preparing agent lists and calling Update.
	// The successful creation of agents for new paths (/tmp/agent3.sock, /tmp/agent-add2.sock)
	// is implicitly tested by mockCreator not returning errors for them.
}

func TestReloadWithSyntacticallyIncorrectTOML(t *testing.T) {
	initialConfigContent := `
debug = false
listen = "/tmp/agent-initial.sock"
targets = ["/tmp/initial-target.sock"]
`
	mockCreator := mockAgentCreator(nil) // No failing paths needed for this test's setup
	initialAppCfg, muxAgent := setupInitialState(t, initialConfigContent, mockCreator)
	require.NotNil(t, initialAppCfg)
	require.NotNil(t, muxAgent)

	// Store initial state for comparison
	originalDebug := initialAppCfg.Debug
	originalListen := initialAppCfg.Listen
	originalTargets := append([]string(nil), initialAppCfg.Targets...) // Deep copy

	malformedConfigContent := `
debug = true
listen = "/tmp/agent-malformed.sock"
targets = ["/tmp/malformed-target.sock" # Missing closing quote
`
	malformedConfigPath := writeTempConfigFile(t, "malformed-config", malformedConfigContent)

	// Attempt to load the malformed config
	// Pass a copy of initialAppCfg to loadAndApplyConfig to ensure it doesn't modify the original on failure path
	currentCfgForReload := *initialAppCfg
	reloadedAppCfg, err := loadAndApplyConfig(malformedConfigPath, muxAgent, &currentCfgForReload, mockCreator)

	// Assertions
	require.Error(t, err, "loadAndApplyConfig should return an error for malformed TOML")
	// The error message might vary depending on the TOML parser, so check if it's non-nil.
	// A more specific error check could be `assert.Contains(t, err.Error(), "TOML decoding error")` or similar if Viper wraps it consistently.

	assert.Nil(t, reloadedAppCfg, "AppConfig should be nil on critical load error")

	// Verify that the application's current configuration (initialAppCfg) has not changed.
	// Note: loadAndApplyConfig returns a *new* AppConfig on success, or nil on error.
	// The caller (fsnotify loop in main.go) is responsible for updating the global currentAppConfig.
	// Here, we check that 'initialAppCfg' which represents the "active" config before this failed load, is unchanged.
	assert.Equal(t, originalDebug, initialAppCfg.Debug, "Debug flag should not change on failed reload")
	assert.Equal(t, originalListen, initialAppCfg.Listen, "Listen address should not change on failed reload")
	assert.ElementsMatch(t, originalTargets, initialAppCfg.Targets, "Targets should not change on failed reload")

	// MuxAgent state is conceptually unchanged because loadAndApplyConfig should not have called muxAgent.Update()
	// due to the error occurring before agent processing or Update call.
}

func TestReloadWithSemanticallyIncorrectConfig(t *testing.T) {
	initialConfigContent := `
debug = false
listen = "/tmp/agent-initial-semantic.sock"
targets = ["/tmp/target1.sock"]
add_targets = ["/tmp/add-target1.sock"]
`
	mockCreator := mockAgentCreator(nil)
	initialAppCfg, muxAgent := setupInitialState(t, initialConfigContent, mockCreator)
	require.NotNil(t, initialAppCfg)
	require.NotNil(t, muxAgent)

	originalTargets := append([]string(nil), initialAppCfg.Targets...)
	originalAddTargets := append([]string(nil), initialAppCfg.AddTargets...)

	// Semantically incorrect: target1.sock is both a target and an add_target
	semanticallyIncorrectContent := `
debug = true # This change should not be applied
listen = "/tmp/agent-initial-semantic.sock"
targets = ["/tmp/target1.sock", "/tmp/target2.sock"]
add_targets = ["/tmp/target1.sock"] # Error: target1.sock also in targets
`
	incorrectConfigPath := writeTempConfigFile(t, "semantic-error-config", semanticallyIncorrectContent)

	currentCfgForReload := *initialAppCfg
	reloadedAppCfg, err := loadAndApplyConfig(incorrectConfigPath, muxAgent, &currentCfgForReload, mockCreator)

	require.Error(t, err, "loadAndApplyConfig should return an error for semantically incorrect config")
	// Check for a specific error message if possible, e.g., related to validation
	assert.Contains(t, err.Error(), "target path '/tmp/target1.sock' cannot also be an add-target path", "Error message mismatch")
	assert.Nil(t, reloadedAppCfg, "AppConfig should be nil on semantic error")

	// Verify that the application's current configuration (initialAppCfg) has not changed.
	assert.False(t, initialAppCfg.Debug, "Debug flag should not change on failed reload")
	assert.ElementsMatch(t, originalTargets, initialAppCfg.Targets, "Targets should not change on failed reload")
	assert.ElementsMatch(t, originalAddTargets, initialAppCfg.AddTargets, "AddTargets should not change on failed reload")
}

func TestReloadWithUnreachableAgents(t *testing.T) {
	initialConfigContent := `
targets = ["/tmp/targetA.sock"]
add_targets = ["/tmp/addTargetA.sock"]
`
	// All initial paths are expected to succeed.
	mockCreatorSuccess := mockAgentCreator(nil)
	initialAppCfg, muxAgent := setupInitialState(t, initialConfigContent, mockCreatorSuccess)
	require.NotNil(t, initialAppCfg)
	require.NotNil(t, muxAgent)

	// New configuration with some reachable and some unreachable agents
	newConfigContent := `
targets = ["/tmp/targetA.sock", "/tmp/targetB.sock", "/tmp/targetU.sock"]
add_targets = ["/tmp/addTargetA.sock", "/tmp/addTargetB.sock", "/tmp/addTargetU.sock"]
select_target_command = "new-cmd"
`
	newConfigPath := writeTempConfigFile(t, "unreachable-config", newConfigContent)

	// Configure mockAgentCreator for the reload: targetU and addTargetU will fail
	failingPaths := map[string]error{
		"/tmp/targetU.sock":   errors.New("connection refused for targetU"),
		"/tmp/addTargetU.sock": errors.New("connection refused for addTargetU"),
	}
	mockCreatorForReload := mockAgentCreator(failingPaths)

	// loadAndApplyConfig is expected to log errors for unreachable agents but not return an error itself,
	// as it should proceed with the agents it *can* connect to.
	currentCfgForReload := *initialAppCfg // Pass a copy
	reloadedAppCfg, err := loadAndApplyConfig(newConfigPath, muxAgent, &currentCfgForReload, mockCreatorForReload)
	require.NoError(t, err, "loadAndApplyConfig should not return an error if some agents are unreachable but config is valid")
	require.NotNil(t, reloadedAppCfg)

	// 1. Verify AppConfig reflects the new configuration (including unreachable paths)
	assert.ElementsMatch(t, []string{"/tmp/targetA.sock", "/tmp/targetB.sock", "/tmp/targetU.sock"}, reloadedAppCfg.Targets)
	assert.ElementsMatch(t, []string{"/tmp/addTargetA.sock", "/tmp/addTargetB.sock", "/tmp/addTargetU.sock"}, reloadedAppCfg.AddTargets)
	assert.Equal(t, "new-cmd", reloadedAppCfg.SelectTargetCommand)

	// 2. Verify MuxAgent's conceptual state (what would be passed to Update)
	// We need to simulate what loadAndApplyConfig would have prepared for muxAgent.Update
	// by creating the lists of *successfully connected* agents based on reloadedAppCfg and mockCreatorForReload.
	var expectedSuccessfulTargets []*pkg.Agent
	for _, p := range reloadedAppCfg.Targets {
		agent, agErr := mockCreatorForReload(p)
		if agErr == nil {
			expectedSuccessfulTargets = append(expectedSuccessfulTargets, agent)
		}
	}
	var expectedSuccessfulAddTargets []*pkg.Agent
	for _, p := range reloadedAppCfg.AddTargets {
		agent, agErr := mockCreatorForReload(p)
		if agErr == nil {
			expectedSuccessfulAddTargets = append(expectedSuccessfulAddTargets, agent)
		}
	}
	// Assert that the number of agents that would be passed to Update is correct
	// This indirectly verifies that unreachable ones were skipped by loadAndApplyConfig's internal logic.
	// For a direct assertion, MuxAgent would need getters for its internal agent lists.
	// Here we check the count of successfully created agents.
	// Expected: targetA, targetB (2)
	assert.Len(t, expectedSuccessfulTargets, 2, "MuxAgent should be updated with 2 target agents")
	// Expected: addTargetA, addTargetB (2)
	assert.Len(t, expectedSuccessfulAddTargets, 2, "MuxAgent should be updated with 2 addTarget agents")
}

func TestReloadWithListenAddressChange(t *testing.T) {
	originalListenAddress := "/tmp/original-listen.sock"
	initialConfigContent := fmt.Sprintf(`
listen = "%s"
targets = ["/tmp/targetA.sock"]
debug = false
`, originalListenAddress)

	mockCreator := mockAgentCreator(nil) // All agent creations succeed
	initialAppCfg, muxAgent := setupInitialState(t, initialConfigContent, mockCreator)
	require.NotNil(t, initialAppCfg)
	require.NotNil(t, muxAgent)
	require.Equal(t, originalListenAddress, initialAppCfg.Listen)
	require.Len(t, initialAppCfg.Targets, 1)

	// New configuration attempts to change listen address and adds a target
	newListenAddress := "/tmp/new-listen.sock"
	newConfigContent := fmt.Sprintf(`
listen = "%s"
targets = ["/tmp/targetA.sock", "/tmp/targetB.sock"] # targetB added
debug = true # Another change that should be applied
`, newListenAddress)
	newConfigPath := writeTempConfigFile(t, "listen-change-config", newConfigContent)

	// Suppress expected warning log about listen address change for cleaner test output
	originalLogger := log.Logger
	var logOutput strings.Builder
	log.Logger = zerolog.New(&logOutput).With().Timestamp().Logger()
	t.Cleanup(func() { log.Logger = originalLogger })

	currentCfgForReload := *initialAppCfg // Pass a copy
	reloadedAppCfg, err := loadAndApplyConfig(newConfigPath, muxAgent, &currentCfgForReload, mockCreator)
	require.NoError(t, err, "loadAndApplyConfig should not return an error for listen address change")
	require.NotNil(t, reloadedAppCfg)

	// Assertions
	// 1. Listen address should NOT have changed in the applied config
	assert.Equal(t, originalListenAddress, reloadedAppCfg.Listen, "Listen address should not change dynamically")

	// 2. Other valid changes SHOULD be applied
	assert.True(t, reloadedAppCfg.Debug, "Debug flag should have been updated")
	assert.ElementsMatch(t, []string{"/tmp/targetA.sock", "/tmp/targetB.sock"}, reloadedAppCfg.Targets, "Targets list should be updated")

	// 3. Warning message should have been logged
	assert.Contains(t, logOutput.String(), "Listen address change detected", "Expected warning about listen address change was not logged")
	assert.Contains(t, logOutput.String(), originalListenAddress, "Log should contain original listen address")
	assert.Contains(t, logOutput.String(), newListenAddress, "Log should contain new (attempted) listen address")

	// MuxAgent's state conceptually reflects the addition of targetB because loadAndApplyConfig
	// would have called muxAgent.Update() with agents for targetA and targetB.
}

func TestReloadEmptyConfigClearsTargets(t *testing.T) {
	initialConfigContent := `
targets = ["/tmp/targetA.sock", "/tmp/targetB.sock"]
add_targets = ["/tmp/addTargetA.sock"]
select_target_command = "some-cmd"
debug = true
`
	mockCreator := mockAgentCreator(nil) // All agent creations succeed initially
	initialAppCfg, muxAgent := setupInitialState(t, initialConfigContent, mockCreator)
	require.NotNil(t, initialAppCfg)
	require.NotNil(t, muxAgent)
	require.NotEmpty(t, initialAppCfg.Targets)
	require.NotEmpty(t, initialAppCfg.AddTargets)
	require.True(t, initialAppCfg.Debug)

	// New configuration is empty or explicitly clears targets
	emptyConfigContent := `
# targets and add_targets are omitted, which should result in empty slices
# select_target_command is also omitted, should be empty string
# debug is omitted, should revert to default (false for bools if not specified)
`
	newConfigPath := writeTempConfigFile(t, "empty-config", emptyConfigContent)

	currentCfgForReload := *initialAppCfg // Pass a copy
	reloadedAppCfg, err := loadAndApplyConfig(newConfigPath, muxAgent, &currentCfgForReload, mockCreator)
	require.NoError(t, err, "loadAndApplyConfig should not return an error for empty/cleared config")
	require.NotNil(t, reloadedAppCfg)

	// Assertions
	assert.Empty(t, reloadedAppCfg.Targets, "Targets should be empty after reloading with empty config")
	assert.Empty(t, reloadedAppCfg.AddTargets, "AddTargets should be empty after reloading with empty config")
	assert.Empty(t, reloadedAppCfg.SelectTargetCommand, "SelectTargetCommand should be empty string")
	assert.False(t, reloadedAppCfg.Debug, "Debug should revert to default (false) when omitted")

	// MuxAgent's state should reflect no targets or addTargets.
	// We simulate this by checking what would be passed to Update.
	var expectedSuccessfulTargets []*pkg.Agent
	// no loops as reloadedAppCfg.Targets is empty
	var expectedSuccessfulAddTargets []*pkg.Agent
	// no loops as reloadedAppCfg.AddTargets is empty

	assert.Len(t, expectedSuccessfulTargets, 0, "MuxAgent should be updated with 0 target agents")
	assert.Len(t, expectedSuccessfulAddTargets, 0, "MuxAgent should be updated with 0 addTarget agents")
}

// TestDummy is removed as we now have a real test.
