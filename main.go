// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"context"
	"errors" // For error handling
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync" // For appConfigLock
	"syscall"
	"time"

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
	"github.com/fsnotify/fsnotify" // For config file watching
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"
	"github.com/spf13/viper" // For configuration loading
	"golang.org/x/crypto/ssh/agent"
)

var (
	// injected build time
	Version  string
	Revision string
)

// Global application configuration and its mutex
var (
	currentAppConfig *config.AppConfig
	appConfigLock    sync.RWMutex
)

// agentCreatorWrapper serves as a placeholder for the actual pkg.NewAgent.
// It matches the required signature: func(path string) (*pkg.Agent, error).
func agentCreatorWrapper(path string) (*pkg.Agent, error) {
	if path == "" {
		return nil, errors.New("agent path cannot be empty in agentCreatorWrapper")
	}
	// In Step 2, we use pkg.MustNewAgent for simplicity, assuming valid paths.
	// The actual pkg.NewAgent (to be modified in Step 3) will handle errors properly.
	// Now, using NewAgent as per Step 3 requirements.
	return pkg.NewAgent(path)
}

// loadAndApplyConfig loads, validates, and applies a new configuration.
// It updates the provided muxAgent with new agents and returns the new AppConfig.
// If any step fails, it logs the error and returns (nil, err) to indicate
// that the old configuration should be retained.
func loadAndApplyConfig(
	configFilePath string,
	muxAgent *pkg.MuxAgent, // Can be nil for initial load
	currentConfig *config.AppConfig, // The last known valid config; empty for initial load
	agentCreator func(path string) (*pkg.Agent, error),
) (*config.AppConfig, error) {
	log.Debug().Str("configFilePath", configFilePath).Msg("Attempting to load and apply configuration")

	// 1. Load Viper Config
	v, configFileUsed, err := config.LoadViperConfig(configFilePath)
	if err != nil {
		log.Error().Err(err).Str("path", configFilePath).Msg("Failed to load viper configuration")
		return nil, err // Indicate failure, keep old config
	}
	if configFileUsed != "" {
		log.Info().Str("path", configFileUsed).Msg("Configuration successfully loaded from file")
	} else {
		log.Info().Msg("No configuration file loaded; using defaults and environment variables.")
	}

	// 2. Populate new AppConfig
	newCfg := config.GetAppConfig(v, configFileUsed)
	log.Debug().Object("newConfig", newCfg).Msg("Newly parsed application configuration")

	// 3. Validation
	for _, t := range newCfg.Targets {
		for _, at := range newCfg.AddTargets {
			if t == at {
				valErr := fmt.Errorf("validation error: target path '%s' cannot also be an add-target path", t)
				log.Error().Err(valErr).Msg("Configuration validation failed")
				return nil, valErr
			}
		}
	}
	if len(newCfg.AddTargets) > 1 && newCfg.SelectTargetCommand == "" {
		valErr := errors.New("validation error: select-target-command is required when multiple add-target agents are specified")
		log.Error().Err(valErr).Msg("Configuration validation failed")
		return nil, valErr
	}
	log.Debug().Msg("Configuration validation successful")

	// 4. Listen Address Handling
	if currentConfig.Listen != "" && newCfg.Listen != currentConfig.Listen {
		log.Warn().
			Str("currentListen", currentConfig.Listen).
			Str("newListen", newCfg.Listen).
			Msg("Listen address change detected. Dynamic update of listen address is not supported. Using original address.")
		newCfg.Listen = currentConfig.Listen // Revert to the original listen address
	}

	// 5. Agent Creation & Update
	var successfulNewTargets []*pkg.Agent
	var successfulNewAddTargets []*pkg.Agent

	log.Debug().Msg("Creating/updating target agents")
	for _, path := range newCfg.Targets {
		agent, agentErr := agentCreator(path)
		if agentErr != nil {
			log.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to new target agent; skipping.")
			continue
		}
		successfulNewTargets = append(successfulNewTargets, agent)
	}

	log.Debug().Msg("Creating/updating add-target agents")
	for _, path := range newCfg.AddTargets {
		agent, agentErr := agentCreator(path)
		if agentErr != nil {
			log.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to new add-target agent; skipping.")
			continue
		}
		successfulNewAddTargets = append(successfulNewAddTargets, agent)
	}

	if muxAgent != nil {
		log.Info().
			Int("targetsCount", len(successfulNewTargets)).
			Int("addTargetsCount", len(successfulNewAddTargets)).
			Msg("Updating MuxAgent with new agent lists")
		muxAgent.Update(successfulNewTargets, successfulNewAddTargets, newCfg.SelectTargetCommand) // Update method assumed to exist
	}

	log.Info().Object("appliedConfig", newCfg).Msg("Configuration successfully loaded and applied")
	return newCfg, nil
}

func main() {
	// 1. Pre-parse --config flag to determine if a specific config file is requested early.
	configFlagValue := ""
	preParseFs := pflag.NewFlagSet("preparse", pflag.ContinueOnError)
	preParseFs.SetOutput(io.Discard) // Suppress output during this preparse phase.
	preParseFs.StringVarP(&configFlagValue, "config", "c", "", "Path to a configuration file (e.g., /path/to/config.toml)")
	_ = preParseFs.Parse(os.Args[1:]) // Ignore errors, we only care about the configFlagValue

	// 2. Load initial Viper configuration. This is used for binding flags so they can override config file values.
	v, initialConfigFileUsed, err := config.LoadViperConfig(configFlagValue)
	if err != nil {
		if configFlagValue != "" {
			log.Error().Err(err).Str("configFile", configFlagValue).Msg("Failed to load specified configuration file during initial setup. Proceeding with defaults/env/flags.")
		} else {
			log.Info().Err(err).Msg("No specific config file provided or default config files not found/failed to load. Using defaults/env/flags.")
		}
		if v == nil {
			v = viper.New()
		}
	}

	// 3. Define and Parse Command-Line Flags.
	mainFlagSet := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	mainFlagSet.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		mainFlagSet.PrintDefaults()
	}
	mainFlagSet.BoolP("version", "v", false, "Print version and exit.")
	mainFlagSet.BoolP("help", "h", false, "Print this help message and exit.")
	mainFlagSet.StringP("config", "c", configFlagValue, "Path to a configuration file (e.g., /path/to/config.toml).")

	if err := config.DefineAndBindFlags(v, mainFlagSet); err != nil {
		log.Fatal().Err(err).Msg("Failed to define and bind application flags.")
	}

	if err := mainFlagSet.Parse(os.Args[1:]); err != nil {
		log.Fatal().Err(err).Msg("Failed to parse command-line arguments.")
	}

	if help, _ := mainFlagSet.GetBool("help"); help {
		mainFlagSet.Usage()
		os.Exit(0)
	}
	if ver, _ := mainFlagSet.GetBool("version"); ver {
		fmt.Printf("ssh-agent-multiplexer Version=%s, Revision=%s\n", Version, Revision)
		os.Exit(0)
	}

	// 4. Initial Configuration Load and Application.
	initialAppCfg, err := loadAndApplyConfig(initialConfigFileUsed, nil, &config.AppConfig{}, agentCreatorWrapper)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load and apply initial configuration.")
	}
	appConfigLock.Lock()
	currentAppConfig = initialAppCfg
	appConfigLock.Unlock()

	// 5. Logging Setup (based on the now-loaded configuration).
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
		// NoColor:    !currentAppConfig.ColorLog, // ColorLog field does not exist in AppConfig
	})
	if currentAppConfig.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	log.Info().Str("version", Version).Str("revision", Revision).Msg("ssh-agent-multiplexer starting")
	if currentAppConfig.ConfigFilePathUsed != "" {
		log.Info().Str("path", currentAppConfig.ConfigFilePathUsed).Msg("Using configuration file")
	}
	log.Info().Object("effectiveConfig", currentAppConfig).Msg("Initial configuration applied")

	// 6. Determine and Setup Listen Address.
	effectiveListen := currentAppConfig.Listen
	if effectiveListen == "" {
		var sockDir string
		if currentAppConfig.ConfigFilePathUsed != "" {
			sockDir = filepath.Dir(currentAppConfig.ConfigFilePathUsed)
		} else {
			runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
			if runtimeDir != "" {
				sockDir = filepath.Join(runtimeDir, "ssh-agent-multiplexer")
			} else {
				sockDir = filepath.Join(os.TempDir(), "ssh-agent-multiplexer")
			}
		}
		if err := os.MkdirAll(sockDir, 0700); err != nil {
			log.Fatal().Err(err).Str("path", sockDir).Msg("Failed to create directory for listening socket.")
		}
		effectiveListen = filepath.Join(sockDir, "agent.sock")
		log.Info().Str("listenPath", effectiveListen).Msg("Listen path derived as not specified in config.")
		appConfigLock.Lock()
		currentAppConfig.Listen = effectiveListen
		appConfigLock.Unlock()
	}

	// 7. Setup Signal Handling & Listener.
	signalCtx, cancelSignalCtx := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancelSignalCtx()

	if _, statErr := os.Stat(effectiveListen); statErr == nil {
		if rmErr := os.Remove(effectiveListen); rmErr != nil {
			log.Fatal().Err(rmErr).Str("path", effectiveListen).Msg("Failed to remove existing socket file.")
		}
	}

	listener, err := (&net.ListenConfig{}).Listen(signalCtx, "unix", effectiveListen)
	if err != nil {
		log.Fatal().Err(err).Str("listenPath", effectiveListen).Msg("Failed to listen on socket.")
	}
	log.Info().Str("address", listener.Addr().String()).Msg("Multiplexer listening on socket.")

	cleanupCtx, cancelCleanupCtx := context.WithCancel(context.Background())
	go func() {
		<-signalCtx.Done()
		log.Info().Msg("Shutdown signal received. Closing listener socket.")
		if err := listener.Close(); err != nil {
			log.Error().Err(err).Msg("Error closing listener socket during shutdown.")
		}
		cancelCleanupCtx()
	}()

	// 8. Create MuxAgent.
	var initialTargetAgents []*pkg.Agent
	for _, path := range currentAppConfig.Targets {
		agent, agentErr := agentCreatorWrapper(path)
		if agentErr != nil {
			log.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to initial target agent; skipping.")
			continue
		}
		initialTargetAgents = append(initialTargetAgents, agent)
	}
	var initialAddTargetAgents []*pkg.Agent
	if len(currentAppConfig.AddTargets) == 0 {
		log.Warn().Msg("No add-target agents specified. The multiplexer cannot add any keys.")
	}
	for _, path := range currentAppConfig.AddTargets {
		agent, agentErr := agentCreatorWrapper(path)
		if agentErr != nil {
			log.Error().Err(agentErr).Str("path", path).Msg("Failed to connect to initial add-target agent; skipping.")
			continue
		}
		initialAddTargetAgents = append(initialAddTargetAgents, agent)
	}
	if len(initialTargetAgents) == 0 && len(initialAddTargetAgents) == 0 {
		log.Warn().Msg("No target or add-target agents configured or connected. Multiplexer might not be very useful.")
	}
	// muxAgt is now of type *pkg.MuxAgent directly
	muxAgt := pkg.NewMuxAgent(initialTargetAgents, initialAddTargetAgents, currentAppConfig.SelectTargetCommand)
	log.Debug().Msg("MuxAgent successfully created with initial agents.")

	// 9. Setup fsnotify Goroutine for Configuration Reloading.
	appConfigLock.RLock()
	configPathToWatch := currentAppConfig.ConfigFilePathUsed
	appConfigLock.RUnlock()

	if configPathToWatch != "" {
		go func(filePath string, agentToUpdate *pkg.MuxAgent) {
			watcher, watchErr := fsnotify.NewWatcher()
			if watchErr != nil {
				log.Error().Err(watchErr).Msg("Failed to create fsnotify watcher for config reloading.")
				return
			}
			defer watcher.Close()

			watchDir := filepath.Dir(filePath)
			if err := watcher.Add(watchDir); err != nil {
				log.Error().Err(err).Str("path", watchDir).Msg("Failed to add config directory to fsnotify watcher.")
				return
			}
			log.Info().Str("path", filePath).Msg("Watching configuration file for changes.")

			var debounceTimer *time.Timer
			const debounceDelay = 500 * time.Millisecond

			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						log.Info().Msg("fsnotify events channel closed.")
						return
					}
					if filepath.Clean(event.Name) == filepath.Clean(filePath) &&
						(event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename)) {
						log.Debug().Str("event", event.String()).Msg("Relevant config file event detected.")
						if debounceTimer != nil {
							debounceTimer.Stop()
						}
						debounceTimer = time.AfterFunc(debounceDelay, func() {
							log.Info().Msg("Debounce timer triggered. Attempting to reload configuration.")
							appConfigLock.RLock()
							currentCfgForReload := *currentAppConfig
							cfgFileToReload := currentCfgForReload.ConfigFilePathUsed
							appConfigLock.RUnlock()

							reloadedCfg, reloadErr := loadAndApplyConfig(cfgFileToReload, agentToUpdate, &currentCfgForReload, agentCreatorWrapper)
							if reloadErr != nil {
								log.Error().Err(reloadErr).Msg("Failed to reload configuration; keeping previous settings.")
							} else {
								appConfigLock.Lock()
								currentAppConfig = reloadedCfg
								appConfigLock.Unlock()

								if currentAppConfig.Debug {
									zerolog.SetGlobalLevel(zerolog.DebugLevel)
								} else {
									zerolog.SetGlobalLevel(zerolog.InfoLevel)
								}
								log.Logger = log.Output(zerolog.ConsoleWriter{
									Out:        os.Stderr,
									TimeFormat: time.RFC3339,
									// NoColor:    !currentAppConfig.ColorLog, // ColorLog field does not exist in AppConfig
								})
								log.Info().Object("reloadedConfig", currentAppConfig).Msg("Configuration reloaded and applied successfully.")
							}
						})
					}
				case errWatcher, ok := <-watcher.Errors:
					if !ok {
						log.Info().Msg("fsnotify errors channel closed.")
						return
					}
					log.Error().Err(errWatcher).Msg("fsnotify watcher error.")
				}
			}
		}(configPathToWatch, muxAgt)
	} else {
		log.Info().Msg("No configuration file path specified or found; dynamic reloading is disabled.")
	}

	// 10. Main Accept Loop for agent connections.
	log.Info().Msg("Starting main accept loop for agent connections.")
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			select {
			case <-cleanupCtx.Done():
				log.Info().Msg("Listener closed as part of graceful shutdown.")
			default:
				log.Error().Err(acceptErr).Msg("Failed to accept new agent connection.")
			}
			break
		}
		log.Debug().Str("remoteAddr", conn.RemoteAddr().String()).Msg("Accepted new agent connection.")
		go func(c net.Conn) {
			defer func() {
				log.Debug().Str("remoteAddr", c.RemoteAddr().String()).Msg("Agent connection closed.")
				_ = c.Close()
			}()
			if serveErr := agent.ServeAgent(muxAgt, c); serveErr != nil && serveErr != io.EOF {
				log.Error().Err(serveErr).Msg("Error serving agent connection.")
			}
		}(conn)
	}

	<-cleanupCtx.Done()
	log.Info().Msg("SSH Agent Multiplexer exited gracefully.")
}
