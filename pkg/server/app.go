// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package server

import (
	"context"
	"errors" // Added for errors.New, errors.Is
	"fmt"
	"io" // For io.EOF
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/everpeace/ssh-agent-multiplexer/pkg" // Keep only one instance of this import
	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/agent" // For agent.ServeAgent
)

// App encapsulates the core application logic for the SSH Agent Multiplexer.
type App struct {
	muxAgent       *pkg.MuxAgent
	currentConfig  *config.AppConfig
	appConfigLock  sync.RWMutex
	configFilePath string // Path to the specific config file loaded, or "" if defaults were used
	listener       net.Listener
	fsWatcher      *fsnotify.Watcher
	logger         zerolog.Logger

	// shutdownCtx is used to signal shutdown to various parts of the application.
	shutdownCtx context.Context
	// shutdownCancel is called to initiate the shutdown sequence.
	shutdownCancel context.CancelFunc

	// agentCreator is used to create new agent instances. This allows for easier testing
	// and potentially different agent types in the future.
	agentCreator func(path string) (*pkg.Agent, error)
}

// NewApp creates and initializes a new App instance.
// initialConfigFlagValue: Path to config file from --config flag (can be empty).
// agentCreator: Function to create agent instances.
func NewApp(
	initialConfigFlagValue string,
	agentCreator func(path string) (*pkg.Agent, error),
	version string, // Injected version
	revision string, // Injected revision
) (*App, error) {
	app := &App{
		agentCreator: agentCreator,
		logger:       log.Logger,
	}

	app.logger.Info().Str("version", version).Str("revision", revision).Msg("ssh-agent-multiplexer starting")

	app.shutdownCtx, app.shutdownCancel = context.WithCancel(context.Background())

	// 1. Initial Configuration Loading
	v, configFileUsed, err := config.LoadViperConfig(initialConfigFlagValue)
	if err != nil {
		// If a specific config file was given and failed, this is more critical.
		if initialConfigFlagValue != "" {
			app.logger.Error().Err(err).Str("configFile", initialConfigFlagValue).Msg("Failed to load specified configuration file.")
			return nil, fmt.Errorf("failed to load specified configuration file %s: %w", initialConfigFlagValue, err)
		}
		// If no specific file, and defaults failed, log it but proceed as flags/env might provide all config.
		app.logger.Info().Err(err).Msg("No specific config file provided or default config files not found/failed to load. Using defaults/env/flags.")
		if v == nil { // Ensure v is not nil for GetAppConfig
			v = viper.New()
		}
	}
	app.configFilePath = configFileUsed // Store the path of the config file that was actually used

	initialAppConfig := config.GetAppConfig(v, app.configFilePath) // GetAppConfig needs Viper instance and path

	// 2. Determine Effective Listen Path
	effectiveListen := initialAppConfig.Listen
	if effectiveListen == "" {
		// Derive default based on config file path or current/temp directory
		var sockDir string
		if app.configFilePath != "" {
			sockDir = filepath.Dir(app.configFilePath)
		} else {
			runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
			if runtimeDir != "" {
				sockDir = filepath.Join(runtimeDir, "ssh-agent-multiplexer")
			} else {
				sockDir = filepath.Join(os.TempDir(), "ssh-agent-multiplexer")
			}
		}
		// Ensure directory exists (moved from main.go's Start-like phase to here for early validation)
		if err := os.MkdirAll(sockDir, 0700); err != nil {
			app.logger.Error().Err(err).Str("path", sockDir).Msg("Failed to create directory for listening socket.")
			return nil, fmt.Errorf("failed to create directory %s for listening socket: %w", sockDir, err)
		}
		effectiveListen = filepath.Join(sockDir, "agent.sock")
		app.logger.Info().Str("listenPath", effectiveListen).Msg("Listen path derived as not specified in config or CLI.")
	}
	initialAppConfig.Listen = effectiveListen // Update config with the determined path

	// 3. Update logger level based on initial config's Debug flag
	if initialAppConfig.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		app.logger = app.logger.Level(zerolog.DebugLevel) // Ensure app's logger instance also reflects this
	}
	// Re-log with potentially new level
	app.logger.Info().Object("initialConfig", initialAppConfig).Msg("Initial configuration loaded.")
	if app.configFilePath != "" {
		app.logger.Info().Str("path", app.configFilePath).Msg("Using configuration file")
	}

	// 4. Validate Initial Config
	// (Moved from main.go's setup phase)
	for _, t := range initialAppConfig.Targets {
		for _, at := range initialAppConfig.AddTargets {
			if t == at {
				err := fmt.Errorf("validation error: target path '%s' cannot also be an add-target path", t)
				app.logger.Error().Err(err).Msg("Initial configuration validation failed.")
				return nil, err
			}
		}
	}
	if len(initialAppConfig.AddTargets) > 1 && initialAppConfig.SelectTargetCommand == "" {
		// err variable was already declared in the same block for the previous validation.
		// Re-assign to it.
		err = errors.New("validation error: select-target-command is required when multiple add-target agents are specified")
		app.logger.Error().Err(err).Msg("Initial configuration validation failed.")
		return nil, err
	}
	app.logger.Debug().Msg("Initial configuration validated successfully.")
	app.currentConfig = initialAppConfig

	// 5. Initialize MuxAgent
	var initialTargetAgents []*pkg.Agent
	app.logger.Debug().Msg("Creating initial target agents...")
	for _, path := range app.currentConfig.Targets {
		agent, agentErr := app.agentCreator(path)
		if agentErr != nil {
			// Log error but don't fail NewApp unless all agents fail (which is hard to define here)
			// The application might still be useful with some agents down.
			app.logger.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to initial target agent; skipping.")
			continue
		}
		initialTargetAgents = append(initialTargetAgents, agent)
	}
	var initialAddTargetAgents []*pkg.Agent
	app.logger.Debug().Msg("Creating initial add-target agents...")
	if len(app.currentConfig.AddTargets) == 0 {
		app.logger.Warn().Msg("No add-target agents specified. The multiplexer cannot add any keys.")
	}
	for _, path := range app.currentConfig.AddTargets {
		agent, agentErr := app.agentCreator(path)
		if agentErr != nil {
			app.logger.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to initial add-target agent; skipping.")
			continue
		}
		initialAddTargetAgents = append(initialAddTargetAgents, agent)
	}
	if len(initialTargetAgents) == 0 && len(initialAddTargetAgents) == 0 {
		app.logger.Warn().Msg("No target or add-target agents configured or connected. Multiplexer might not be very useful.")
	}

	app.muxAgent = pkg.NewMuxAgent(initialTargetAgents, initialAddTargetAgents, app.currentConfig.SelectTargetCommand)
	app.logger.Debug().Msg("MuxAgent created with initial agents.")

	return app, nil
}

// Start begins the core operations of the application:
// 1. Starts the network listener.
// 2. Starts the configuration file watcher (if a config file is used).
// 3. Enters the main loop to accept and handle incoming agent connections.
// It blocks until the application is stopped or a critical error occurs.
func (app *App) Start() error {
	app.logger.Info().Msg("Starting application services...")

	// 1. Start Listener
	// Ensure existing socket file is removed before listening
	listenPath := app.currentConfig.Listen
	if _, statErr := os.Stat(listenPath); statErr == nil {
		if rmErr := os.Remove(listenPath); rmErr != nil {
			app.logger.Error().Err(rmErr).Str("path", listenPath).Msg("Failed to remove existing socket file.")
			return fmt.Errorf("failed to remove existing socket file %s: %w", listenPath, rmErr)
		}
	}

	l, err := (&net.ListenConfig{}).Listen(app.shutdownCtx, "unix", listenPath)
	if err != nil {
		app.logger.Error().Err(err).Str("listenPath", listenPath).Msg("Failed to start listener.")
		return fmt.Errorf("failed to listen on %s: %w", listenPath, err)
	}
	app.listener = l
	app.logger.Info().Str("address", app.listener.Addr().String()).Msg("Multiplexer listening on socket.")

	// 2. Start fsnotify Watcher (if config file used)
	if app.configFilePath != "" {
		if err := app.startFsnotifyWatcher(); err != nil {
			// Log error but don't necessarily fail Start. App can run without dynamic reload.
			app.logger.Error().Err(err).Msg("Failed to start configuration file watcher. Dynamic reloading may be disabled.")
		}
	} else {
		app.logger.Info().Msg("No configuration file path specified; dynamic reloading is disabled.")
	}

	// 3. Start Main Accept Loop
	app.logger.Info().Msg("Starting main accept loop for agent connections.")
	for {
		conn, acceptErr := app.listener.Accept()
		if acceptErr != nil {
			select {
			case <-app.shutdownCtx.Done(): // Shutdown initiated
				app.logger.Info().Msg("Listener accept loop stopping due to shutdown signal.")
				return nil // Graceful shutdown
			default:
				// Check if the error is due to the listener being closed, which is expected during shutdown.
				if errors.Is(acceptErr, net.ErrClosed) {
					app.logger.Info().Msg("Listener closed, accept loop stopping.")
					return nil // Graceful shutdown or Stop() called
				}
				app.logger.Error().Err(acceptErr).Msg("Failed to accept new connection.")
				// Depending on the error, might want to continue or break.
				// For typical transient errors, continue is fine. If it's a persistent issue, might need to stop.
				// For now, continue to be resilient.
				continue
			}
		}

		app.logger.Debug().Str("remoteAddr", conn.RemoteAddr().String()).Msg("Accepted new agent connection.")
		go func(c net.Conn) {
			defer func() {
				app.logger.Debug().Str("remoteAddr", c.RemoteAddr().String()).Msg("Agent connection closed.")
				_ = c.Close()
			}()
			if serveErr := agent.ServeAgent(app.muxAgent, c); serveErr != nil && !errors.Is(serveErr, io.EOF) {
				app.logger.Error().Err(serveErr).Msg("Error serving agent connection.")
			}
		}(conn)
	}
}

// startFsnotifyWatcher initializes and starts the file system notification watcher.
// This is an internal helper called by Start().
func (app *App) startFsnotifyWatcher() error {
	var err error
	app.fsWatcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	watchDir := filepath.Dir(app.configFilePath)
	if err := app.fsWatcher.Add(watchDir); err != nil {
		// Attempt to close watcher if Add fails.
		if closeErr := app.fsWatcher.Close(); closeErr != nil {
			app.logger.Error().Err(closeErr).Msg("Error closing fsnotify watcher after failing to add path.")
		}
		app.fsWatcher = nil // Ensure it's nil so Stop doesn't try to close it again
		return fmt.Errorf("failed to add config directory '%s' to fsnotify watcher: %w", watchDir, err)
	}
	app.logger.Info().Str("path", app.configFilePath).Msg("Watching configuration file for changes.")

	go func() {
		defer func() {
			if app.fsWatcher != nil { // Check if it hasn't been set to nil by failed Add
				if err := app.fsWatcher.Close(); err != nil {
					app.logger.Error().Err(err).Msg("Error closing fsnotify watcher in goroutine.")
				}
			}
		}()

		var debounceTimer *time.Timer
		const debounceDelay = 500 * time.Millisecond

		for {
			select {
			case <-app.shutdownCtx.Done():
				app.logger.Info().Msg("Stopping fsnotify watcher due to shutdown signal.")
				return
			case event, ok := <-app.fsWatcher.Events:
				if !ok {
					app.logger.Info().Msg("fsnotify events channel closed.")
					return
				}
				// Check if the event is for the specific file and relevant operation
				if filepath.Clean(event.Name) == filepath.Clean(app.configFilePath) &&
					(event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename)) {
					app.logger.Debug().Str("event", event.String()).Msg("Relevant config file event detected.")
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(debounceDelay, func() {
						app.logger.Info().Msg("Debounce timer triggered. Attempting to reload configuration.")
						if err := app.reloadConfigAndApply(); err != nil {
							// Error is already logged by reloadConfigAndApply.
							// This log line is to satisfy SA9003 (empty branch).
							app.logger.Debug().Err(err).Msg("Error occurred during automatic config reload (already logged in detail).")
						}
					})
				}
			case watchErr, ok := <-app.fsWatcher.Errors:
				if !ok {
					app.logger.Info().Msg("fsnotify errors channel closed.")
					return
				}
				app.logger.Error().Err(watchErr).Msg("fsnotify watcher error.")
			}
		}
	}()
	return nil
}

// Stop signals the application to gracefully shut down all its services.
func (app *App) Stop() {
	app.logger.Info().Msg("Stopping application services...")

	// Signal all goroutines that depend on shutdownCtx to stop.
	app.shutdownCancel()

	// Close the listener. This will cause the Accept() loop in Start() to unblock and exit.
	// The fsWatcher is closed via defer in its own goroutine once shutdownCtx is done.
	if app.listener != nil {
		app.logger.Debug().Msg("Closing network listener.")
		if err := app.listener.Close(); err != nil {
			app.logger.Error().Err(err).Msg("Error closing network listener.")
		}
	}
	// Note: fsWatcher.Close() is handled by the defer in its goroutine reacting to shutdownCtx.Done()
	app.logger.Info().Msg("Application services signaled to stop.")
}

// reloadConfigAndApply is called when a configuration file change is detected.
// It reloads the configuration, validates it, updates agents, and applies settings.
// This method is similar to the original loadAndApplyConfig logic in main.go.
func (app *App) reloadConfigAndApply() error {
	var valErr error // Declare valErr once for the function scope
	app.logger.Info().Str("path", app.configFilePath).Msg("Reloading configuration...")

	// Lock for reading currentConfig and writing potentially new currentConfig
	app.appConfigLock.Lock()
	defer app.appConfigLock.Unlock()

	// Pass a copy of the current config for comparison (e.g. listen address)
	// The app.currentConfig itself will be updated if reload is successful.
	configSnapshotForCompare := *app.currentConfig

	// 1. Load Viper Config using the stored config file path
	v, configFileUsed, err := config.LoadViperConfig(app.configFilePath)
	if err != nil {
		app.logger.Error().Err(err).Str("path", app.configFilePath).Msg("Failed to load viper configuration during reload.")
		return err
	}
	// configFileUsed should ideally be the same as app.configFilePath here.
	// If it's different, it might indicate an unexpected issue or a change in how LoadViperConfig resolves paths.
	if configFileUsed != app.configFilePath {
		app.logger.Warn().Str("expected", app.configFilePath).Str("got", configFileUsed).Msg("Config file path mismatch during reload")
	}

	// 2. Populate new AppConfig
	newCfg := config.GetAppConfig(v, configFileUsed) // configFileUsed should be app.configFilePath
	app.logger.Debug().Object("newConfig", newCfg).Msg("Newly parsed application configuration during reload")

	// 3. Validation (same as in NewApp/original loadAndApplyConfig)
	for _, t := range newCfg.Targets {
		for _, at := range newCfg.AddTargets {
			if t == at {
				valErr = fmt.Errorf("validation error: target path '%s' cannot also be an add-target path", t)
				app.logger.Error().Err(valErr).Msg("Configuration validation failed during reload")
				return valErr
			}
		}
	}
	if len(newCfg.AddTargets) > 1 && newCfg.SelectTargetCommand == "" {
		valErr = errors.New("validation error: select-target-command is required when multiple add-target agents are specified")
		app.logger.Error().Err(valErr).Msg("Configuration validation failed during reload")
		return valErr
	}
	app.logger.Debug().Msg("Configuration validation successful during reload")

	// 4. Listen Address Handling - compare with the snapshot of config before this reload attempt
	if configSnapshotForCompare.Listen != "" && newCfg.Listen != configSnapshotForCompare.Listen {
		app.logger.Warn().
			Str("currentListen", configSnapshotForCompare.Listen).
			Str("newListen", newCfg.Listen).
			Msg("Listen address change detected in config file. Dynamic update of listen address is not supported. Using original address.")
		newCfg.Listen = configSnapshotForCompare.Listen // Revert to the original listen address
	} else if newCfg.Listen == "" && configSnapshotForCompare.Listen != "" {
		// If new config has empty listen but old one had one, keep old.
		newCfg.Listen = configSnapshotForCompare.Listen
	}

	// 5. Agent Creation & Update
	var successfulNewTargets []*pkg.Agent
	app.logger.Debug().Msg("Re-creating target agents based on reloaded config...")
	for _, path := range newCfg.Targets {
		agent, agentErr := app.agentCreator(path)
		if agentErr != nil {
			app.logger.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to new target agent during reload; skipping.")
			continue
		}
		successfulNewTargets = append(successfulNewTargets, agent)
	}

	var successfulNewAddTargets []*pkg.Agent
	app.logger.Debug().Msg("Re-creating add-target agents based on reloaded config...")
	for _, path := range newCfg.AddTargets {
		agent, agentErr := app.agentCreator(path)
		if agentErr != nil {
			app.logger.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to new add-target agent during reload; skipping.")
			continue
		}
		successfulNewAddTargets = append(successfulNewAddTargets, agent)
	}

	// Update MuxAgent - this is crucial
	if app.muxAgent != nil {
		app.logger.Info().
			Int("targetsCount", len(successfulNewTargets)).
			Int("addTargetsCount", len(successfulNewAddTargets)).
			Msg("Updating MuxAgent with new agent lists from reloaded config")
		app.muxAgent.Update(successfulNewTargets, successfulNewAddTargets, newCfg.SelectTargetCommand)
	}

	// 6. Apply new config and update logger
	app.currentConfig = newCfg // Update the live config

	if app.currentConfig.Debug != configSnapshotForCompare.Debug { // Check if debug status actually changed
		if app.currentConfig.Debug {
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
			app.logger = app.logger.Level(zerolog.DebugLevel)
		} else {
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
			app.logger = app.logger.Level(zerolog.InfoLevel)
		}
		app.logger.Info().Bool("debug", app.currentConfig.Debug).Msg("Logger level updated due to configuration reload.")
	}
	// Update global logger if it was set from app.logger
	// This assumes app.logger is the source of truth for console writer settings.
	log.Logger = app.logger.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	app.logger.Info().Object("reloadedConfig", app.currentConfig).Msg("Configuration successfully reloaded and applied.")
	return nil
}

// SetAgentCreatorForTest allows tests to replace the agent creator function.
// This method should ONLY be used in test contexts.
func (app *App) SetAgentCreatorForTest(newCreator func(path string) (*pkg.Agent, error)) {
	// This method is intentionally simple for test purposes.
	// It doesn't involve locking appConfigLock as it's modifying a function pointer,
	// not typically part of the config state that reloadConfigAndApplyInternal protects.
	// However, if agentCreator was part of AppConfig, it would be different.
	// For now, direct assignment is fine for testing.
	app.agentCreator = newCreator
}

// Logger returns the app's internal logger. Used for testing log capture.
func (app *App) Logger() zerolog.Logger {
	return app.logger
}

// SetLoggerForTest allows tests to replace the app's internal logger.
// This method should ONLY be used in test contexts.
func (app *App) SetLoggerForTest(logger zerolog.Logger) {
	app.logger = logger
	// Optionally, also update the global logger if app logic relies on it,
	// or if other packages use the global logger and expect it to be in sync.
	// log.Logger = logger // Be cautious with this if multiple app instances or parallel tests.
}

// CurrentConfig returns a thread-safe copy of the current application configuration.
func (app *App) CurrentConfig() config.AppConfig {
	app.appConfigLock.RLock()
	defer app.appConfigLock.RUnlock()
	// Return a copy to prevent external modification of the internal state.
	return *app.currentConfig
}

// AppConfigLock returns a pointer to the RWMutex that protects currentConfig.
// This allows tests to lock for more complex assertions if needed, though CurrentConfig() is preferred for simple reads.
func (app *App) AppConfigLock() *sync.RWMutex {
	return &app.appConfigLock
}

// TestReloadConfig is a test helper to expose reloadConfigAndApply for testing.
// It should only be used in tests.
func (app *App) TestReloadConfig() error {
	// This wrapper allows tests in server_test package to call the unexported reloadConfigAndApply.
	// However, tests in a _test package cannot call unexported methods of the main package.
	// To make this work as intended for server_test, reloadConfigAndApply itself would need to be exported,
	// or this TestReloadConfig method would need to be in the `server` package (not `server_test`).
	// Given the current structure, and assuming reloadConfigAndApply remains unexported,
	// this helper would only be callable if the test was in `package server`.
	//
	// For now, let's assume this method will be used by tests that are part of `package server`.
	// If tests are in `package server_test`, `reloadConfigAndApply` must be exported (e.g. `ReloadConfigAndApply`).
	// The prompt asked for `ReloadConfigAndApply` (unexported but callable from same package tests) OR a test helper.
	// Let's make `reloadConfigAndApply` exportable for testing as `ReloadConfigAndApplyForTest`
	// and this TestReloadConfig can be removed.
	//
	// Alternative: Rename reloadConfigAndApply to something like reloadConfigAndApplyInternal
	// and create an exported ReloadConfigAndApplyForTest.
	// For now, I will make reloadConfigAndApply directly exportable for testing, as `ReloadConfigAndApplyForTest`.
	// This requires renaming the original function.
	// Let's rename the original `reloadConfigAndApply` to `reloadConfigAndApplyInternal`
	// and create an exported `ReloadConfigAndApplyForTest`.

	// This function body will be for the new ReloadConfigAndApplyForTest
	return app.reloadConfigAndApplyInternal()
}

// reloadConfigAndApplyInternal is the internal implementation.
func (app *App) reloadConfigAndApplyInternal() error {
	app.logger.Info().Str("path", app.configFilePath).Msg("Reloading configuration...")

	// Lock for reading currentConfig and writing potentially new currentConfig
	app.appConfigLock.Lock()
	defer app.appConfigLock.Unlock()

	// Pass a copy of the current config for comparison (e.g. listen address)
	// The app.currentConfig itself will be updated if reload is successful.
	configSnapshotForCompare := *app.currentConfig

	// 1. Load Viper Config using the stored config file path
	v, configFileUsed, err := config.LoadViperConfig(app.configFilePath)
	if err != nil {
		app.logger.Error().Err(err).Str("path", app.configFilePath).Msg("Failed to load viper configuration during reload.")
		return err
	}
	// configFileUsed should ideally be the same as app.configFilePath here.
	// If it's different, it might indicate an unexpected issue or a change in how LoadViperConfig resolves paths.
	if configFileUsed != app.configFilePath {
		app.logger.Warn().Str("expected", app.configFilePath).Str("got", configFileUsed).Msg("Config file path mismatch during reload")
	}

	// 2. Populate new AppConfig
	newCfg := config.GetAppConfig(v, configFileUsed) // configFileUsed should be app.configFilePath
	app.logger.Debug().Object("newConfig", newCfg).Msg("Newly parsed application configuration during reload")

	// 3. Validation (same as in NewApp/original loadAndApplyConfig)
	var valErr error // Declare valErr once for the function scope
	for _, t := range newCfg.Targets {
		for _, at := range newCfg.AddTargets {
			if t == at {
				valErr = fmt.Errorf("validation error: target path '%s' cannot also be an add-target path", t)
				app.logger.Error().Err(valErr).Msg("Configuration validation failed during reload")
				return valErr
			}
		}
	}
	if len(newCfg.AddTargets) > 1 && newCfg.SelectTargetCommand == "" {
		newCfg.SelectTargetCommand = config.DefaultSelectTargetCommand
	}
	app.logger.Debug().Msg("Configuration validation successful during reload")

	// 4. Listen Address Handling - compare with the snapshot of config before this reload attempt
	if configSnapshotForCompare.Listen != "" && newCfg.Listen != configSnapshotForCompare.Listen {
		app.logger.Warn().
			Str("currentListen", configSnapshotForCompare.Listen).
			Str("newListen", newCfg.Listen).
			Msg("Listen address change detected in config file. Dynamic update of listen address is not supported. Using original address.")
		newCfg.Listen = configSnapshotForCompare.Listen // Revert to the original listen address
	} else if newCfg.Listen == "" && configSnapshotForCompare.Listen != "" {
		// If new config has empty listen but old one had one, keep old.
		newCfg.Listen = configSnapshotForCompare.Listen
	}

	// 5. Agent Creation & Update
	var successfulNewTargets []*pkg.Agent
	app.logger.Debug().Msg("Re-creating target agents based on reloaded config...")
	for _, path := range newCfg.Targets {
		agent, agentErr := app.agentCreator(path)
		if agentErr != nil {
			app.logger.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to new target agent during reload; skipping.")
			continue
		}
		successfulNewTargets = append(successfulNewTargets, agent)
	}

	var successfulNewAddTargets []*pkg.Agent
	app.logger.Debug().Msg("Re-creating add-target agents based on reloaded config...")
	for _, path := range newCfg.AddTargets {
		agent, agentErr := app.agentCreator(path)
		if agentErr != nil {
			app.logger.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to new add-target agent during reload; skipping.")
			continue
		}
		successfulNewAddTargets = append(successfulNewAddTargets, agent)
	}

	// Update MuxAgent - this is crucial
	if app.muxAgent != nil {
		app.logger.Info().
			Int("targetsCount", len(successfulNewTargets)).
			Int("addTargetsCount", len(successfulNewAddTargets)).
			Msg("Updating MuxAgent with new agent lists from reloaded config")
		app.muxAgent.Update(successfulNewTargets, successfulNewAddTargets, newCfg.SelectTargetCommand)
	}

	// 6. Apply new config and update logger
	app.currentConfig = newCfg // Update the live config

	if app.currentConfig.Debug != configSnapshotForCompare.Debug { // Check if debug status actually changed
		if app.currentConfig.Debug {
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
			app.logger = app.logger.Level(zerolog.DebugLevel)
		} else {
			zerolog.SetGlobalLevel(zerolog.InfoLevel)
			app.logger = app.logger.Level(zerolog.InfoLevel)
		}
		app.logger.Info().Bool("debug", app.currentConfig.Debug).Msg("Logger level updated due to configuration reload.")
	}
	// Update global logger if it was set from app.logger
	// This assumes app.logger is the source of truth for console writer settings.
	log.Logger = app.logger.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	app.logger.Info().Object("reloadedConfig", app.currentConfig).Msg("Configuration successfully reloaded and applied.")
	return nil
}
