// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"bytes"
	"context" // Added

	// Added
	"fmt" // Added
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create a temporary TOML config file
func createTempConfigFile(t *testing.T, initialConfig *config.AppConfig) (filePath string, cleanupFunc func()) {
	t.Helper()
	tmpDir := t.TempDir()
	configFile, err := os.Create(filepath.Join(tmpDir, "config.toml"))
	require.NoError(t, err)

	if initialConfig != nil {
		encoder := toml.NewEncoder(configFile)
		require.NoError(t, encoder.Encode(initialConfig))
	}
	require.NoError(t, configFile.Close())

	return configFile.Name(), func() {
		// Cleanup is handled by t.TempDir()
	}
}

// Helper to update the temporary config file
func updateTempConfigFile(t *testing.T, filePath string, newConfigData interface{}) {
	t.Helper()
	content := &bytes.Buffer{}
	switch data := newConfigData.(type) {
	case *config.AppConfig:
		encoder := toml.NewEncoder(content)
		require.NoError(t, encoder.Encode(data))
	case string:
		_, err := content.WriteString(data)
		require.NoError(t, err)
	default:
		t.Fatalf("unsupported type for newConfigData: %T", newConfigData)
	}

	err := os.WriteFile(filePath, content.Bytes(), 0644)
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond) // Adjusted sleep after some initial observation
}

// Helper to capture zerolog output
func captureLogs(t *testing.T) (logOutput *bytes.Buffer, originalLogger zerolog.Logger, cleanupFunc func()) {
	t.Helper()
	originalLogger = log.Logger
	logBuffer := &bytes.Buffer{}
	// Create a new logger that writes to the buffer, keeping original global level if needed for other parts.
	// For tests, often simpler to just set the level directly on this test logger.
	testLogger := zerolog.New(logBuffer).With().Timestamp().Logger()
	log.Logger = testLogger // Redirect global logger

	return logBuffer, originalLogger, func() {
		log.Logger = originalLogger
	}
}

// Helper to create a dummy socket file for agent path validation by MustNewAgent
func createDummySocket(t *testing.T, agentPath string) (path string, cleanup func()) {
	t.Helper()
	// Ensure the agentPath is in a temporary directory for cleanup
	// For simplicity, we'll create it in a sub-temp dir or ensure the base path is cleanable.
	// Here, agentPath is expected to be like "/tmp/agent.name.sock"
	// This helper assumes the parent directory of agentPath might not exist,
	// but for the current tests, agentPath is usually in /tmp/
	// A more robust solution would place it under t.TempDir().

	// If agentPath is just a name, join with tempDir
	var sockPath string
	if !filepath.IsAbs(agentPath) {
		sockPath = filepath.Join(t.TempDir(), agentPath)
	} else { // If it's an absolute path (like /tmp/...), use it but be careful with cleanup
		sockPath = agentPath
		// Ensure we are not creating sockets in weird places for tests
		require.True(t, strings.HasPrefix(sockPath, os.TempDir()), "dummy socket path should be in OS temp dir for safety")
	}

	_ = os.Remove(sockPath) // remove if exists from a previous failed test
	f, err := os.Create(sockPath)
	require.NoError(t, err, "Failed to create dummy socket file: %s", sockPath)
	_ = f.Close() // Add error handling for file close
	return sockPath, func() {
		_ = os.Remove(sockPath)
	}
}

func setupTestMuxAgent(t *testing.T, initialCfg *config.AppConfig) *pkg.MuxAgent {
	t.Helper()
	// cleanups will store functions to remove dummy sockets after the agent is presumably done.
	// Note: If NewMuxAgent panics (due to MustNewAgent failing), these deferrals might not all run
	// as expected if the panic is in a loop. However, MustNewAgent panicking would fail the test,
	// and t.TempDir() or specific defer os.Remove should handle socket cleanup eventually.
	var cleanups []func()
	defer func() {
		// Run cleanups in reverse order of creation, though order doesn't strictly matter here.
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}()

	// Create dummy sockets for all paths defined in AddTargets.
	// NewMuxAgent (via MustNewAgent) will try to connect to these.
	for _, p := range initialCfg.AddTargets {
		_, cleanup := createDummySocket(t, p)
		cleanups = append(cleanups, cleanup) // Add cleanup func to list
	}

	// Create dummy sockets for all paths defined in Targets.
	for _, p := range initialCfg.Targets {
		_, cleanup := createDummySocket(t, p)
		cleanups = append(cleanups, cleanup) // Add cleanup func to list
	}

	// Now, call the refactored NewMuxAgent with the AppConfig.
	// NewMuxAgent itself will call MustNewAgent on the paths from initialCfg.
	muxAgentInstance := pkg.NewMuxAgent(initialCfg)

	// Ensure it's the concrete type for calling test helpers like GetTargetPaths.
	concreteAgent, ok := muxAgentInstance.(*pkg.MuxAgent)
	require.True(t, ok, "NewMuxAgent did not return *pkg.MuxAgent as expected by type assertion")
	return concreteAgent
}

// simulateMainWatcherLogic sets up and runs the core file watching logic from main.go.
func simulateMainWatcherLogic(
	t *testing.T,
	configFilePath string, // Path to the config file being watched
	configFlagValue string, // Value for -c flag, usually same as configFilePath for these tests
	muxAgent *pkg.MuxAgent,
	initialEffectiveListen string,
	watcherErrorChan chan error, // For signaling critical errors from watcher goroutine
	ctx context.Context, // For controlling the lifecycle of the watcher goroutine
) {
	t.Helper()

	currentAppConfig := &atomic.Value{}
	initialLoadedCfg, err := loadAndApplyConfig(configFlagValue, log.Logger) // log.Logger is the test logger
	require.NoError(t, err, "Initial loadAndApplyConfig failed during test setup")
	currentAppConfig.Store(initialLoadedCfg)

	watcher, err := fsnotify.NewWatcher()
	require.NoError(t, err)

	configFileDir := filepath.Dir(configFilePath)
	err = watcher.Add(configFileDir)
	require.NoError(t, err, "Failed to add config dir to watcher")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				// Send panic info to the error channel
				watcherErrorChan <- fmt.Errorf("panic in watcher goroutine: %v", r)
			}
			if errClose := watcher.Close(); errClose != nil {
				log.Error().Err(errClose).Msg("(Test) Error closing watcher in goroutine")
			}
			close(watcherErrorChan) // Signal goroutine completion
		}()

		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("(Test) Stopping config watcher due to context cancellation.")
				return
			case event, ok := <-watcher.Events:
				if !ok {
					log.Warn().Msg("(Test) Config watcher events channel closed unexpectedly.")
					// Don't send error here, as it might be part of normal shutdown if ctx was also just closed.
					return
				}

				loadedCfg := currentAppConfig.Load().(*config.AppConfig)
				// Ensure ConfigFilePathUsed is correctly populated by loadAndApplyConfig
				cfgFileToWatch := loadedCfg.ConfigFilePathUsed
				if cfgFileToWatch == "" { // If LoadViperConfig found no file and used defaults
					cfgFileToWatch = configFilePath // Fallback to the path we are explicitly watching for tests
				}

				if filepath.Clean(event.Name) == filepath.Clean(cfgFileToWatch) && event.Has(fsnotify.Write) {
					log.Info().Str("file", event.Name).Msg("(Test) Config file modification detected.")

					// Pass the -c flag value (configFlagValue) to loadAndApplyConfig, which is what main.go does.
					newCfg, errLoad := loadAndApplyConfig(configFlagValue, log.Logger)
					if errLoad != nil {
						log.Error().Err(errLoad).Msg("(Test) Failed to reload configuration. MuxAgent not updated.")
					} else {
						log.Info().Object("reloaded_config", newCfg).Msg("(Test) Configuration reloaded successfully.")
						muxAgent.UpdateConfig(newCfg)
						currentAppConfig.Store(newCfg) // Update the "active" config
						log.Info().Msg("(Test) MuxAgent UpdateConfig called.")

						if newCfg.Listen != "" && newCfg.Listen != initialEffectiveListen {
							log.Warn().
								Str("current_listen_address", initialEffectiveListen).
								Str("new_listen_address", newCfg.Listen).
								Msg("(Test) Listen address changed. Restart needed (simulated).")
						}
					}
				}
			case errWatch, ok := <-watcher.Errors:
				if !ok {
					log.Warn().Msg("(Test) Config watcher errors channel closed unexpectedly.")
					return
				}
				log.Error().Err(errWatch).Msg("(Test) Error from config watcher.")
				// Decide if this error is critical enough to send to watcherErrorChan
			}
		}
	}()
}

func TestConfigReload_SuccessfulUpdate(t *testing.T) {
	logBuf, originalLogger, logCleanup := captureLogs(t)
	defer logCleanup()
	// For this test, ensure debug logging from MuxAgent is visible if needed for UpdateConfig details
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	defer zerolog.SetGlobalLevel(zerolog.GlobalLevel()) // Reset after test

	initialTargetPaths := []string{"/tmp/agent.initial.sock"}
	initialCfg := &config.AppConfig{Targets: initialTargetPaths, Debug: true}
	configFilePath, _ := createTempConfigFile(t, initialCfg)
	// TempDir (from createTempConfigFile) handles cleanup of the dir and the file within.

	muxAgent := setupTestMuxAgent(t, initialCfg) // This also creates dummy sockets for initialCfg
	// Get actual paths used by muxAgent, which might be modified by createDummySocket
	assert.Equal(t, len(initialTargetPaths), len(muxAgent.GetTargetPaths()), "Initial MuxAgent targets count incorrect")

	watcherErrorChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure context is always cancelled

	simulateMainWatcherLogic(t, configFilePath, configFilePath, muxAgent, "", watcherErrorChan, ctx)

	// New configuration
	newTargetPaths := []string{"/tmp/agent.new.sock"}
	updatedCfg := &config.AppConfig{Targets: newTargetPaths, Debug: true}

	// Create dummy socket for the new agent path
	dummySockNewPath, cleanupNewSock := createDummySocket(t, newTargetPaths[0])
	defer cleanupNewSock()

	updateTempConfigFile(t, configFilePath, updatedCfg)

	expectedPaths := []string{dummySockNewPath}
	success := assert.Eventually(t, func() bool {
		currentPaths := muxAgent.GetTargetPaths()
		return assert.ObjectsAreEqual(expectedPaths, currentPaths)
	}, 3*time.Second, 100*time.Millisecond, "MuxAgent targets did not update. Expected: %v, Got: %v", expectedPaths, muxAgent.GetTargetPaths())

	if !success {
		t.Logf("Final MuxAgent Targets: %v", muxAgent.GetTargetPaths())
		t.Logf("Captured logs for failed assertion:\n%s", logBuf.String())
	}

	logContent := logBuf.String()
	assert.Contains(t, logContent, "MuxAgent: Updating configuration", "Log should show MuxAgent updating")
	assert.Contains(t, logContent, "MuxAgent: Updated Targets", "Log should show MuxAgent targets updated")

	cancel() // Stop the watcher
	select {
	case err, ok := <-watcherErrorChan:
		require.False(t, ok && err != nil, "Watcher exited with error: %v", err)
	case <-time.After(1 * time.Second):
		t.Log("Watcher did not close channel in time, might indicate issue if test fails.")
	}
	log.Logger = originalLogger // Restore logger fully
}

func TestConfigReload_InvalidTomlSyntax(t *testing.T) {
	logBuf, originalLogger, logCleanup := captureLogs(t)
	defer logCleanup()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	defer zerolog.SetGlobalLevel(zerolog.GlobalLevel())

	initialTargetPaths := []string{"/tmp/agent.initial-syntax.sock"}
	initialCfg := &config.AppConfig{Targets: initialTargetPaths, Debug: true}
	configFilePath, _ := createTempConfigFile(t, initialCfg)

	muxAgent := setupTestMuxAgent(t, initialCfg)
	originalAgentTargetPaths := muxAgent.GetTargetPaths() // Capture initial state based on dummy sockets

	watcherErrorChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	simulateMainWatcherLogic(t, configFilePath, configFilePath, muxAgent, "", watcherErrorChan, ctx)

	malformedToml := "this is not valid toml content \n targets = ['bad]"
	updateTempConfigFile(t, configFilePath, malformedToml)

	time.Sleep(500 * time.Millisecond) // Allow time for fsnotify and reload attempt, needs to be generous

	logs := logBuf.String()
	assert.Contains(t, logs, "Failed to load configuration", "Log should contain configuration load failure")
	// Viper's error for TOML might be generic or specific. "decode" or "unmarshal" are good keywords.
	assert.Contains(t, logs, "cannot unmarshal TOML", "Log should indicate TOML parsing error (actual message may vary based on viper/toml library)")

	assert.Equal(t, originalAgentTargetPaths, muxAgent.GetTargetPaths(), "MuxAgent targets should not have changed after invalid TOML reload")
	assert.NotContains(t, logs, "MuxAgent: Updating configuration", "MuxAgent should not attempt to update with invalid config")

	cancel()
	select {
	case err, ok := <-watcherErrorChan:
		require.False(t, ok && err != nil, "Watcher exited with error: %v", err)
	case <-time.After(1 * time.Second):
		t.Log("Watcher did not close channel in time for syntax error test.")
	}
	log.Logger = originalLogger
}

func TestConfigReload_SemanticallyInvalidConfig(t *testing.T) {
	logBuf, originalLogger, logCleanup := captureLogs(t)
	defer logCleanup()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	defer zerolog.SetGlobalLevel(zerolog.GlobalLevel())

	initialTargetPaths := []string{"/tmp/agent.initial-semantic.sock"}
	initialCfg := &config.AppConfig{Targets: initialTargetPaths, Debug: true}
	configFilePath, _ := createTempConfigFile(t, initialCfg)

	muxAgent := setupTestMuxAgent(t, initialCfg)
	originalAgentTargetPaths := muxAgent.GetTargetPaths()

	watcherErrorChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	simulateMainWatcherLogic(t, configFilePath, configFilePath, muxAgent, "", watcherErrorChan, ctx)

	semanticallyInvalidCfg := &config.AppConfig{
		Targets:    []string{"/tmp/agent.same-semantic.sock"},
		AddTargets: []string{"/tmp/agent.same-semantic.sock"}, // This is the semantic error
		Debug:      true,
	}
	// Dummy socket for this path isn't strictly needed as validation should fail before agent creation,
	// but if MustNewAgent was called by loadAndApplyConfig (it's not), it would be.

	updateTempConfigFile(t, configFilePath, semanticallyInvalidCfg)
	time.Sleep(500 * time.Millisecond) // Allow time for processing

	logs := logBuf.String()
	assert.Contains(t, logs, "Failed to load configuration", "Log should contain configuration load failure")
	assert.Contains(t, logs, "Configuration validation error", "Log should indicate validation error")
	assert.Contains(t, logs, "target path '/tmp/agent.same-semantic.sock' must not be the same as an add-target path", "Log should detail the specific validation error")

	assert.Equal(t, originalAgentTargetPaths, muxAgent.GetTargetPaths(), "MuxAgent targets should not change after semantically invalid reload")
	assert.NotContains(t, logs, "MuxAgent: Updating configuration", "MuxAgent should not attempt to update with semantically invalid config")

	cancel()
	select {
	case err, ok := <-watcherErrorChan:
		require.False(t, ok && err != nil, "Watcher exited with error: %v", err)
	case <-time.After(1 * time.Second):
		t.Log("Watcher did not close channel in time for semantic error test.")
	}
	log.Logger = originalLogger
}

func TestConfigReload_ListenAddressChangeWarning(t *testing.T) {
	logBuf, originalLogger, logCleanup := captureLogs(t)
	defer logCleanup()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	defer zerolog.SetGlobalLevel(zerolog.GlobalLevel())

	initialListenAddress := "/tmp/initial.sock"
	initialCfg := &config.AppConfig{Listen: initialListenAddress, Targets: []string{"/tmp/t1.sock"}, Debug: true}
	configFilePath, _ := createTempConfigFile(t, initialCfg)

	// Create dummy for initial target
	_, cleanupT1 := createDummySocket(t, "/tmp/t1.sock")
	defer cleanupT1()

	muxAgent := setupTestMuxAgent(t, initialCfg)

	watcherErrorChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Pass the initialListenAddress to simulateMainWatcherLogic
	simulateMainWatcherLogic(t, configFilePath, configFilePath, muxAgent, initialListenAddress, watcherErrorChan, ctx)

	newListenAddress := "/tmp/new.sock"
	updatedCfg := &config.AppConfig{Listen: newListenAddress, Targets: []string{"/tmp/t1.sock"}, Debug: true}
	// Targets are the same, only listen address changes

	updateTempConfigFile(t, configFilePath, updatedCfg)
	time.Sleep(250 * time.Millisecond)

	logs := logBuf.String()
	assert.Contains(t, logs, "MuxAgent: Updating configuration", "Log should show MuxAgent updating (even if only listen addr changed for main)")
	assert.Contains(t, logs, "Listen address changed in configuration. A full application restart is required to apply this change.")
	assert.Contains(t, logs, fmt.Sprintf("current_listen_address\":%q", initialListenAddress))
	assert.Contains(t, logs, fmt.Sprintf("new_listen_address\":%q", newListenAddress))

	cancel()
	select {
	case err, ok := <-watcherErrorChan:
		require.False(t, ok && err != nil, "Watcher exited with error: %v", err)
	case <-time.After(1 * time.Second):
		t.Log("Watcher did not close channel in time for listen address warning test.")
	}
	log.Logger = originalLogger
}
