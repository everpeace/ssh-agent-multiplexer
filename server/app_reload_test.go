// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package server_test // Changed from package main

import (
	"errors" // Added for creating test errors
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
	// "github.com/everpeace/ssh-agent-multiplexer/pkg/config" // Unused import
	"github.com/everpeace/ssh-agent-multiplexer/server" // Now using server.App
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

// setupInitialState creates an initial server.App instance for tests.
func setupInitialState(t *testing.T, initialConfigContent string, agentCreator func(path string) (*pkg.Agent, error)) *server.App {
	t.Helper()
	originalLogger := log.Logger
	log.Logger = zerolog.Nop() // Suppress logs during test setup
	t.Cleanup(func() { log.Logger = originalLogger })

	initialConfigPath := ""
	if initialConfigContent != "" {
		initialConfigPath = writeTempConfigFile(t, "initial-config", initialConfigContent)
	}

	// For NewApp, cliListenOverride is empty, version/revision are test values.
	app, err := server.NewApp(initialConfigPath, "", agentCreator, "test-version", "test-revision")
	require.NoError(t, err, "server.NewApp failed during setupInitialState")
	require.NotNil(t, app, "server.NewApp returned nil app during setupInitialState")

	// The app instance now holds currentConfig and muxAgent internally.
	return app
}

// For now, tests will primarily assert changes in AppConfig and MuxAgent's command string,
// and the *number* of agents, rather than their specific paths due to mock limitations.
// If pkg.Agent had a public Path field, we could do more.
// Let's assume for testing MuxAgent we can inspect its configured paths if needed,
// or rely on AppConfig's state.

func TestSuccessfulReload(t *testing.T) {
	// t.Skip("Test needs to be refactored for server.App structure") // Skip until refactored
	initialConfigContent := `
debug = false
listen = "/tmp/agent.sock"
targets = ["/tmp/agent1.sock", "/tmp/agent2.sock"]
add_targets = ["/tmp/agent-add1.sock"]
select_target_command = "select-old"
`
	mockCreator := mockAgentCreator(nil)
	app := setupInitialState(t, initialConfigContent, mockCreator) // app is *server.App

	// Initial state assertions (from app.currentConfig, accessed thread-safely)
	app.AppConfigLock().RLock() // Using a conceptual getter for the lock
	assert.Equal(t, "/tmp/agent.sock", app.CurrentConfig().Listen)
	assert.ElementsMatch(t, []string{"/tmp/agent1.sock", "/tmp/agent2.sock"}, app.CurrentConfig().Targets)
	assert.ElementsMatch(t, []string{"/tmp/agent-add1.sock"}, app.CurrentConfig().AddTargets)
	assert.Equal(t, "select-old", app.CurrentConfig().SelectTargetCommand)
	initialConfigPath := app.CurrentConfig().ConfigFilePathUsed // Save for later update
	app.AppConfigLock().RUnlock()

	// New configuration
	newConfigContent := `
debug = true
listen = "/tmp/agent.sock" # Listen address should not change
targets = ["/tmp/agent1.sock", "/tmp/agent3.sock"]
add_targets = ["/tmp/agent-add2.sock"]
select_target_command = "select-new"
`
	// Overwrite the initial config file to simulate a change to the watched file.
	// NewApp sets app.configFilePath to the path of the file it loaded.
	err := os.WriteFile(initialConfigPath, []byte(newConfigContent), 0600)
	require.NoError(t, err, "Failed to overwrite temp config file for reload")

	// Trigger reload using the test helper
	err = app.TestReloadConfig()
	require.NoError(t, err, "app.TestReloadConfig() returned an error")

	// Verify AppConfig reflects new settings (accessed thread-safely)
	app.AppConfigLock().RLock()
	reloadedAppCfg := app.CurrentConfig()
	assert.True(t, reloadedAppCfg.Debug)
	assert.Equal(t, "/tmp/agent.sock", reloadedAppCfg.Listen) // Should remain unchanged
	assert.ElementsMatch(t, []string{"/tmp/agent1.sock", "/tmp/agent3.sock"}, reloadedAppCfg.Targets)
	assert.ElementsMatch(t, []string{"/tmp/agent-add2.sock"}, reloadedAppCfg.AddTargets)
	assert.Equal(t, "select-new", reloadedAppCfg.SelectTargetCommand)
	assert.Equal(t, initialConfigPath, reloadedAppCfg.ConfigFilePathUsed) // Path should be the same
	app.AppConfigLock().RUnlock()

	// Conceptually, muxAgent internal state should have been updated by muxAgent.Update()
	// We can't directly inspect muxAgent.Targets etc. as they are not exported and no getters.
	// The test relies on loadAndApplyConfig correctly preparing agent lists and calling Update.
	// The successful creation of agents for new paths (/tmp/agent3.sock, /tmp/agent-add2.sock)
	// is implicitly tested by mockCreator not returning errors for them.
}

func TestReloadWithSyntacticallyIncorrectTOML(t *testing.T) {
	// t.Skip("Test needs to be refactored for server.App structure") // Skip until refactored
	initialConfigContent := `
debug = false
listen = "/tmp/agent-initial.sock"
targets = ["/tmp/initial-target.sock"]
`
	mockCreator := mockAgentCreator(nil)
	app := setupInitialState(t, initialConfigContent, mockCreator)

	// Store initial state for comparison
	app.AppConfigLock().RLock()
	originalDebug := app.CurrentConfig().Debug
	originalListen := app.CurrentConfig().Listen
	originalTargets := append([]string(nil), app.CurrentConfig().Targets...) // Deep copy
	initialConfigPath := app.CurrentConfig().ConfigFilePathUsed
	app.AppConfigLock().RUnlock()

	malformedConfigContent := `
debug = true
listen = "/tmp/agent-malformed.sock"
targets = ["/tmp/malformed-target.sock" # Missing closing quote
`
	// Overwrite the existing config file path that app is using
	err := os.WriteFile(initialConfigPath, []byte(malformedConfigContent), 0600)
	require.NoError(t, err, "Failed to overwrite temp config file with malformed content")

	// Attempt to load the malformed config
	reloadErr := app.TestReloadConfig()

	// Assertions
	require.Error(t, reloadErr, "app.TestReloadConfig() should return an error for malformed TOML")
	// Viper's error might be specific, e.g. "Near line 3, column 33" for missing quote.
	// For now, just checking it's non-nil is fine.

	// Verify that the application's current configuration has not changed.
	app.AppConfigLock().RLock()
	currentActualCfg := app.CurrentConfig()
	assert.Equal(t, originalDebug, currentActualCfg.Debug, "Debug flag should not change on failed reload")
	assert.Equal(t, originalListen, currentActualCfg.Listen, "Listen address should not change on failed reload")
	assert.ElementsMatch(t, originalTargets, currentActualCfg.Targets, "Targets should not change on failed reload")
	app.AppConfigLock().RUnlock()

	// MuxAgent state is conceptually unchanged because reloadConfigAndApplyInternal should have returned an error
	// before calling muxAgent.Update().
}

func TestReloadWithSemanticallyIncorrectConfig(t *testing.T) {
	// t.Skip("Test needs to be refactored for server.App structure") // Skip until refactored
	initialConfigContent := `
debug = false
listen = "/tmp/agent-initial-semantic.sock"
targets = ["/tmp/target1.sock"]
add_targets = ["/tmp/add-target1.sock"]
`
	mockCreator := mockAgentCreator(nil)
	app := setupInitialState(t, initialConfigContent, mockCreator)

	// Store initial state for comparison
	app.AppConfigLock().RLock()
	originalDebug := app.CurrentConfig().Debug
	originalListen := app.CurrentConfig().Listen
	originalTargets := append([]string(nil), app.CurrentConfig().Targets...)
	originalAddTargets := append([]string(nil), app.CurrentConfig().AddTargets...)
	initialConfigPath := app.CurrentConfig().ConfigFilePathUsed
	app.AppConfigLock().RUnlock()

	// Semantically incorrect: target1.sock is both a target and an add_target
	semanticallyIncorrectContent := `
debug = true # This change should not be applied
listen = "/tmp/agent-initial-semantic.sock" # Listen address should not change anyway
targets = ["/tmp/target1.sock", "/tmp/target2.sock"]
add_targets = ["/tmp/target1.sock"] # Error: target1.sock also in targets
`
	// Overwrite the existing config file path that app is using
	err := os.WriteFile(initialConfigPath, []byte(semanticallyIncorrectContent), 0600)
	require.NoError(t, err, "Failed to overwrite temp config file with semantically incorrect content")

	reloadErr := app.TestReloadConfig()

	require.Error(t, reloadErr, "app.TestReloadConfig() should return an error for semantically incorrect config")
	// Check for a specific error message
	assert.Contains(t, reloadErr.Error(), "target path '/tmp/target1.sock' cannot also be an add-target path", "Error message mismatch")

	// Verify that the application's current configuration has not changed.
	app.AppConfigLock().RLock()
	currentActualCfg := app.CurrentConfig()
	assert.Equal(t, originalDebug, currentActualCfg.Debug, "Debug flag should not change on failed reload")
	assert.Equal(t, originalListen, currentActualCfg.Listen, "Listen address should not change on failed reload")
	assert.ElementsMatch(t, originalTargets, currentActualCfg.Targets, "Targets should not change on failed reload")
	assert.ElementsMatch(t, originalAddTargets, currentActualCfg.AddTargets, "AddTargets should not change on failed reload")
	app.AppConfigLock().RUnlock()
}

func TestReloadWithUnreachableAgents(t *testing.T) {
	// t.Skip("Test needs to be refactored for server.App structure") // Skip until refactored
	initialConfigContent := `
targets = ["/tmp/targetA.sock"]
add_targets = ["/tmp/addTargetA.sock"]
`
	// All initial paths are expected to succeed for NewApp.
	mockCreatorForSetup := mockAgentCreator(nil)
	app := setupInitialState(t, initialConfigContent, mockCreatorForSetup)

	app.AppConfigLock().RLock()
	initialConfigPath := app.CurrentConfig().ConfigFilePathUsed
	app.AppConfigLock().RUnlock()

	// New configuration with some reachable and some unreachable agents
	newConfigContent := `
targets = ["/tmp/targetA.sock", "/tmp/targetB.sock", "/tmp/targetU.sock"]
add_targets = ["/tmp/addTargetA.sock", "/tmp/addTargetB.sock", "/tmp/addTargetU.sock"]
select_target_command = "new-cmd"
`
	// Overwrite the existing config file path that app is using
	err := os.WriteFile(initialConfigPath, []byte(newConfigContent), 0600)
	require.NoError(t, err, "Failed to overwrite temp config file with new agent list")

	// Configure mockAgentCreator for the reload: targetU and addTargetU will fail
	failingPaths := map[string]error{
		"/tmp/targetU.sock":    errors.New("connection refused for targetU"),
		"/tmp/addTargetU.sock": errors.New("connection refused for addTargetU"),
	}
	// IMPORTANT: The agentCreator used by app.TestReloadConfig() is the one set in app by NewApp.
	// We need to update app.agentCreator for this test case.
	// This highlights a potential need for a more flexible way to set agentCreator for reloads in tests,
	// or for TestReloadConfig to accept an agentCreator.
	// For now, we will update it directly on the app instance for the purpose of this test.
	// originalAgentCreator := app.GetAgentCreator() // GetAgentCreator does not exist, NewApp sets it.
	// We need to set it specifically for the reload, then restore.
	// The agentCreator used by NewApp is mockCreatorForSetup.
	// For the reload part of the test, we need a different agentCreator.
	app.SetAgentCreatorForTest(mockAgentCreator(failingPaths))
	// No need to restore originalAgentCreator if SetAgentCreatorForTest is only for this call context,
	// or if each test sets up its own App instance. setupInitialState creates a new App each time.

	reloadErr := app.TestReloadConfig()
	require.NoError(t, reloadErr, "app.TestReloadConfig() should not return an error if some agents are unreachable but config is valid")

	// 1. Verify AppConfig reflects the new configuration (including unreachable paths)
	app.AppConfigLock().RLock()
	reloadedAppCfg := app.CurrentConfig()
	assert.ElementsMatch(t, []string{"/tmp/targetA.sock", "/tmp/targetB.sock", "/tmp/targetU.sock"}, reloadedAppCfg.Targets)
	assert.ElementsMatch(t, []string{"/tmp/addTargetA.sock", "/tmp/addTargetB.sock", "/tmp/addTargetU.sock"}, reloadedAppCfg.AddTargets)
	assert.Equal(t, "new-cmd", reloadedAppCfg.SelectTargetCommand)
	app.AppConfigLock().RUnlock()

	// 2. Verify MuxAgent's conceptual state (what would be passed to Update)
	// This part of the test relies on the internal logic of reloadConfigAndApplyInternal creating these lists
	// and passing them to muxAgent.Update. Since we can't inspect muxAgent directly without getters,
	// we are verifying that the *AppConfig* is updated correctly, and that TestReloadConfig (and thus
	// reloadConfigAndApplyInternal) did not error out. The logging within reloadConfigAndApplyInternal
	// would show which agents were skipped.
	// The number of agents in muxAgent would be (initial targets/addTargets that were re-verified + new successfully connected ones).
	// This test is more about `reloadConfigAndApplyInternal` correctly processing the config and not erroring out
	// when some agents are bad, and that the `currentConfig` reflects the *intended* state from the file.
	// The check that *only successful* agents are passed to `muxAgent.Update` is implicitly part of `reloadConfigAndApplyInternal`'s logic.
}

func TestReloadWithListenAddressChange(t *testing.T) {
	originalListenAddress := "/tmp/original-listen.sock"
	initialConfigContent := fmt.Sprintf(`
listen = "%s"
targets = ["/tmp/targetA.sock"]
debug = false
`, originalListenAddress)

	mockCreator := mockAgentCreator(nil)
	app := setupInitialState(t, initialConfigContent, mockCreator)

	// Get initial config path from the app instance
	app.AppConfigLock().RLock()
	initialConfigPath := app.CurrentConfig().ConfigFilePathUsed
	app.AppConfigLock().RUnlock()
	require.NotEmpty(t, initialConfigPath, "Initial config path should be set in app")


	// New configuration attempts to change listen address and adds a target
	newListenAddress := "/tmp/new-listen.sock"
	newConfigContent := fmt.Sprintf(`
listen = "%s"
targets = ["/tmp/targetA.sock", "/tmp/targetB.sock"] # targetB added
debug = true # Another change that should be applied
`, newListenAddress)

	// Overwrite the existing config file path that app is using
	err := os.WriteFile(initialConfigPath, []byte(newConfigContent), 0600)
	require.NoError(t, err, "Failed to overwrite temp config file for listen address change test")

	// Capture logs from the app's specific logger instance
	originalAppLogger := app.Logger() // Get the app's current logger
	originalGlobalLogger := log.Logger // Save global logger state
	var logOutput strings.Builder
	testLogger := zerolog.New(&logOutput).With().Timestamp().Logger()
	app.SetLoggerForTest(testLogger) // Set app's logger to the capturing one
	log.Logger = testLogger          // Also set global logger to ensure consistency if app logic resets it from its own

	t.Cleanup(func() {
		app.SetLoggerForTest(originalAppLogger) // Restore app's original logger
		log.Logger = originalGlobalLogger       // Restore global logger
	})

	reloadErr := app.TestReloadConfig()
	require.NoError(t, reloadErr, "app.TestReloadConfig() should not return an error for listen address change")

	// Assertions
	currentCfg := app.CurrentConfig() // Get a thread-safe copy

	// 1. Listen address should NOT have changed in the applied config
	assert.Equal(t, originalListenAddress, currentCfg.Listen, "Listen address should not change dynamically")

	// 2. Other valid changes SHOULD be applied
	assert.True(t, currentCfg.Debug, "Debug flag should have been updated")
	assert.ElementsMatch(t, []string{"/tmp/targetA.sock", "/tmp/targetB.sock"}, currentCfg.Targets, "Targets list should be updated")

	// 3. Warning message should have been logged
	logStr := logOutput.String()
	assert.Contains(t, logStr, "Listen address change detected", "Expected warning about listen address change was not logged")
	// More specific checks for addresses can be added if log format is stable and reliable
	// assert.Contains(t, logStr, originalListenAddress)
	// assert.Contains(t, logStr, newListenAddress)

	// MuxAgent's state conceptually reflects the addition of targetB because reloadConfigAndApplyInternal
	// would have called muxAgent.Update() with agents for targetA and targetB.
}

func TestReloadEmptyConfigClearsTargets(t *testing.T) {
	initialConfigContent := `
targets = ["/tmp/targetA.sock", "/tmp/targetB.sock"]
add_targets = ["/tmp/addTargetA.sock"]
select_target_command = "some-cmd"
debug = true
`
	mockCreator := mockAgentCreator(nil)
	app := setupInitialState(t, initialConfigContent, mockCreator)

	// Get initial config path from the app instance
	app.AppConfigLock().RLock()
	initialConfigPath := app.CurrentConfig().ConfigFilePathUsed
	app.AppConfigLock().RUnlock()
	require.NotEmpty(t, initialConfigPath, "Initial config path should be set in app")

	// New configuration is empty or explicitly clears targets
	emptyConfigContent := `
# targets and add_targets are omitted, which should result in empty slices
# select_target_command is also omitted, should be empty string
# debug is omitted, should revert to default (false for bools if not specified)
`
	// Overwrite the existing config file path that app is using
	err := os.WriteFile(initialConfigPath, []byte(emptyConfigContent), 0600)
	require.NoError(t, err, "Failed to overwrite temp config file with empty content")

	reloadErr := app.TestReloadConfig()
	require.NoError(t, reloadErr, "app.TestReloadConfig() should not return an error for empty/cleared config")

	// Assertions
	currentCfg := app.CurrentConfig() // Get a thread-safe copy
	assert.Empty(t, currentCfg.Targets, "Targets should be empty after reloading with empty config")
	assert.Empty(t, currentCfg.AddTargets, "AddTargets should be empty after reloading with empty config")
	assert.Empty(t, currentCfg.SelectTargetCommand, "SelectTargetCommand should be empty string")
	assert.False(t, currentCfg.Debug, "Debug should revert to default (false) when omitted")

	// MuxAgent's state should reflect no targets or addTargets.
	// This is implicitly tested by verifying currentCfg and that reloadConfigAndApplyInternal's
	// agent creation loops would yield empty lists for muxAgent.Update().
}

// TestDummy is removed as we now have a real test. // This comment should be removed if TestDummy was already removed
