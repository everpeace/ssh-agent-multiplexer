// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify" // Added for config reloading
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"
	"golang.org/x/crypto/ssh/agent"

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
)

var (
	// injected build time
	Version  string
	Revision string
)

// Comment out the old global config variables
// var (
// 	listen              string
// 	targets             []string
// 	addTargets          []string
// 	selectTargetCommand string
// 	debug               bool
// 	configFile          string
// )

// Comment out or remove the old setupConfiguration function
/*
func setupConfiguration(args []string) (v *viper.Viper, helpFlag *bool, versionFlag *bool, err error) {
	// ... old implementation ...
}
*/

// loadAndApplyConfig loads configuration from file and command-line flags,
// then applies it.
// The `currentLogger` parameter is used for logging within this function.
func loadAndApplyConfig(configFlagValue string, currentLogger zerolog.Logger) (*config.AppConfig, error) {
	// Load Viper Config
	v, configFileUsed, err := config.LoadViperConfig(configFlagValue)
	if err != nil {
		currentLogger.Error().Err(err).Msgf("Failed to load configuration")
		return nil, err
	}

	// Create Main FlagSet for parsing command-line arguments
	// We re-create the flagset each time to ensure it's clean for parsing,
	// especially important if this function is called during a reload.
	// Use ContinueOnError to handle parsing errors locally instead of exiting.
	mainFlagSet := pflag.NewFlagSet(os.Args[0], pflag.ContinueOnError)
	mainFlagSet.SetOutput(io.Discard) // Suppress pflag output during reparsing for reloads

	// Define standard flags (help, version, config).
	// Although help/version are primarily for initial startup, defining them here
	// ensures consistency if this function's scope changes.
	// The config flag is defined to ensure the flagset is aware of it, using the initially parsed value.
	mainFlagSet.BoolP("version", "v", false, "Print version and exit")
	mainFlagSet.BoolP("help", "h", false, "Print the help")
	mainFlagSet.StringP("config", "c", configFlagValue, "Path to TOML configuration file.") // Use already determined configFlagValue

	// Define and Bind application-specific flags to Viper
	if err := config.DefineAndBindFlags(v, mainFlagSet); err != nil {
		currentLogger.Error().Err(err).Msg("Failed to define or bind flags")
		return nil, err
	}

	// Parse Command-Line Arguments again.
	// This ensures that command-line arguments always override file/default settings,
	// even during a configuration reload.
	if err := mainFlagSet.Parse(os.Args[1:]); err != nil {
		currentLogger.Error().Err(err).Msg("Failed to parse command-line arguments")
		// For reloads, a parsing error might not be fatal, but for initial load it would be.
		// The caller (main) will decide if this error is fatal.
		return nil, err
	}

	// Get Final Application Configuration by populating AppConfig from Viper.
	appCfg := config.GetAppConfig(v, configFileUsed)

	// Update global logging level based on the loaded configuration.
	// This is a side-effect; be mindful if concurrent operations require different log levels.
	newLogLevel := zerolog.InfoLevel
	if appCfg.Debug {
		newLogLevel = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(newLogLevel)

	// Log information about the loaded configuration.
	if appCfg.ConfigFilePathUsed != "" {
		currentLogger.Info().Str("path", appCfg.ConfigFilePathUsed).Msg("Loaded configuration from")
	}
	currentLogger.Info().Object("config", appCfg).Msg("Effective config")

	// Validate the loaded configuration.
	// Errors here indicate an invalid configuration that cannot be applied.
	for _, t := range appCfg.Targets {
		for _, at := range appCfg.AddTargets {
			if t == at {
				err := fmt.Errorf("target path '%s' must not be the same as an add-target path", t)
				currentLogger.Error().Err(err).Msg("Configuration validation error")
				return nil, err
			}
		}
	}
	if len(appCfg.AddTargets) > 1 && appCfg.SelectTargetCommand == "" {
		err := fmt.Errorf("when specifying multiple --add-target agents, --select-target-command must also be provided")
		currentLogger.Error().Err(err).Msg("Configuration validation error")
		return nil, err
	}

	return appCfg, nil
}

func main() {
	// Initial, basic logger setup. This will be refined by loadAndApplyConfig.
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339, NoColor: true})
	zerolog.SetGlobalLevel(zerolog.InfoLevel) // Default to Info level.

	// 1. Pre-parse the --config flag value from command line arguments.
	// This is done separately because the config file path influences how other flags are loaded.
	configFlagValue := ""
	preParseFs := pflag.NewFlagSet("preparse", pflag.ContinueOnError) // ContinueOnError to suppress exit on error.
	preParseFs.SetOutput(io.Discard)                                  // Suppress output during this pre-parse.
	preParseFs.StringVarP(&configFlagValue, "config", "c", "", "Path to a configuration file")
	// Parse all arguments; we only care about --config here. Other errors are ignored at this stage.
	_ = preParseFs.Parse(os.Args[1:])

	// Load initial configuration.
	appCfg, err := loadAndApplyConfig(configFlagValue, log.Logger)
	if err != nil {
		// If the initial configuration load fails, it's a fatal error.
		log.Fatal().Err(err).Msg("Failed to load initial configuration")
	}

	// Handle --help and --version flags. This should happen after attempting to load config
	// so that flags defined in config an also be shown in help message.
	// Create a temporary flagset for this, as pflag.ExitOnError is desired here.
	// This flagset should mirror the one in loadAndApplyConfig for consistency of help messages.
	helpVersionFlagSet := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError) // ExitOnError handles help/errors.
	helpVersionFlagSet.BoolP("version", "v", false, "Print version and exit")
	helpVersionFlagSet.BoolP("help", "h", false, "Print the help")
	helpVersionFlagSet.StringP("config", "c", configFlagValue, "Path to TOML configuration file.")
	// Use a temporary Viper instance to define other application flags for the help message.
	// The config file itself is not read again here; it's just for flag definitions.
	tempViper, _, _ := config.LoadViperConfig(configFlagValue) // Errors ignored, already handled by initial load.
	if err := config.DefineAndBindFlags(tempViper, helpVersionFlagSet); err != nil {
		log.Fatal().Err(err).Msg("Failed to define flags for help/version handling")
	}
	// Parse arguments against this specific flagset.
	if err := helpVersionFlagSet.Parse(os.Args[1:]); err != nil {
		// pflag.ExitOnError should handle this, but if not, log fatal.
		log.Fatal().Err(err).Msg("Failed to parse flags for help/version handling")
	}
	// Check if --help or --version were actually called.
	// pflag.ExitOnError might have already exited if --help was used.
	if help, _ := helpVersionFlagSet.GetBool("help"); help {
		// This block might be redundant if pflag.ExitOnError handles help, but included for clarity.
		_, _ = fmt.Fprintf(os.Stdout, "Usage of %s:\n", os.Args[0])
		helpVersionFlagSet.PrintDefaults()
		os.Exit(0)
	}
	if ver, _ := helpVersionFlagSet.GetBool("version"); ver {
		fmt.Printf("Version=%s, Revision=%s\n", Version, Revision)
		os.Exit(0)
	}

	// Log application startup information.
	log.Info().Str("version", Version).Str("revision", Revision).Msg("ssh-agent-multiplexer starting")

	// Setup signal handling for graceful shutdown.
	signalCtx, cancelSignalCtx := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancelSignalCtx()

	// Setup configuration reloading using fsnotify if a config file is in use.
	if appCfg.ConfigFilePathUsed != "" {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			log.Error().Err(err).Msg("Failed to create fsnotify watcher. Configuration reloading will be disabled.")
		} else {
			// Watch the directory of the config file, not the file itself.
			// This handles cases where editors save by renaming temp files, which can change the inode.
			configFileDir := filepath.Dir(appCfg.ConfigFilePathUsed)
			err = watcher.Add(configFileDir)
			if err != nil {
				log.Error().Err(err).Str("dir", configFileDir).Msg("Failed to add config directory to fsnotify watcher. Reloading may not work reliably.")
			} else {
				log.Info().Str("path", appCfg.ConfigFilePathUsed).Msg("Watching config file for changes")
				go func() {
					defer func() {
						if err := watcher.Close(); err != nil {
							log.Error().Err(err).Msg("Error closing fsnotify watcher")
						}
					}()
					for {
						select {
						case <-signalCtx.Done(): // Application is shutting down.
							log.Info().Msg("Stopping config watcher due to application shutdown.")
							return
						case event, ok := <-watcher.Events:
							if !ok { // Channel closed.
								log.Info().Msg("Config watcher events channel closed.")
								return
							}
							// Check if the event is for the specific config file and is a write event.
							// Using event.Has(fsnotify.Write) is more robust.
							// Need to compare resolved absolute path of event.Name with appCfg.ConfigFilePathUsed
							// as event.Name might be relative or different casing on some systems.
							if filepath.Clean(event.Name) == filepath.Clean(appCfg.ConfigFilePathUsed) && event.Has(fsnotify.Write) {
								log.Info().Str("file", event.Name).Msg("Config file modification detected. Attempting to reload configuration.")
								newCfg, err := loadAndApplyConfig(configFlagValue, log.Logger)
								if err != nil {
									log.Error().Err(err).Msg("Failed to reload configuration after modification. Continuing with previous configuration.")
									// Keep watching for further changes.
								} else {
									// Configuration reloaded, now update the MuxAgent.
									// Ensure 'agt' is the MuxAgent instance.
									// The type assertion is to make it explicit we expect a MuxAgent.
									if muxAgent, ok := agt.(*pkg.MuxAgent); ok {
										log.Info().Msg("Attempting to update MuxAgent with new configuration.")
										muxAgent.UpdateConfig(newCfg) // Update the shared MuxAgent instance.
										log.Info().Msg("MuxAgent UpdateConfig called. See MuxAgent logs for details.")

										// Check if the listen address changed, which requires a restart.
										// 'effectiveListen' holds the listen address used at startup.
										if newCfg.Listen != "" && newCfg.Listen != effectiveListen {
											log.Warn().
												Str("current_listen_address", effectiveListen).
												Str("new_listen_address", newCfg.Listen).
												Msg("Listen address changed in configuration. A full application restart is required to apply this change.")
										}
										// Update appCfg for other parts of the application if they were to read it,
										// though currently most operational parameters are set up once.
										// For instance, logging level is globally set in loadAndApplyConfig.
										// Other changes like target agents are handled by MuxAgent.UpdateConfig.
										appCfg = newCfg
									} else {
										log.Error().Msg("Agent instance is not of type *pkg.MuxAgent. Cannot update configuration.")
									}
								}
							}
						case err, ok := <-watcher.Errors:
							if !ok { // Channel closed.
								log.Info().Msg("Config watcher errors channel closed.")
								return
							}
							log.Error().Err(err).Msg("Error from config watcher.")
						}
					}
				}()
			}
		}
	}

	// Initialize socket to listen.
	// TODO: This part (and agent creation below) will need to be refactored
	// if the listen address or agent configurations can change on reload and need to be applied live.
	effectiveListen := appCfg.Listen
	if effectiveListen == "" {
		sockDir := "." // Default to current directory if no config file path is available for context
		if appCfg.ConfigFilePathUsed != "" {
			var pathErr error
			sockDir, pathErr = filepath.Abs(filepath.Dir(appCfg.ConfigFilePathUsed))
			if pathErr != nil {
				log.Fatal().Err(pathErr).Str("configFilePath", appCfg.ConfigFilePathUsed).Msg("Failed to resolve absolute config path dir for default socket.")
			}
		}
		effectiveListen = filepath.Join(sockDir, "agent.sock")
		log.Info().Str("path", effectiveListen).Msgf("Listen path is not configured, using default.")
	}

	l, err := (&net.ListenConfig{}).Listen(signalCtx, "unix", effectiveListen)
	if err != nil {
		log.Fatal().Err(err).Str("listen_path", effectiveListen).Msg("Failed to listen on socket")
	}
	cleanupCtx, cancelCleanupCtx := context.WithCancel(context.Background()) // Context for managing cleanup.
	go func() {
		<-signalCtx.Done() // Wait for shutdown signal.
		log.Info().Msg("Shutdown signal received. Closing listener socket.")
		logger := log.With().Str("listen", effectiveListen).Logger()
		if err := l.Close(); err != nil {
			logger.Error().Err(err).Msg("Failed to close the listener socket during shutdown.")
		} else {
			logger.Info().Msg("Successfully closed the listener socket.")
		}
		cancelCleanupCtx() // Signal that cleanup is complete.
	}()

	// Create agents.
	// TODO: Agent setup will need to be dynamic if configuration reloads can change them.
	var addTargetAgents []*pkg.Agent
	if len(appCfg.AddTargets) == 0 {
		log.Warn().Msg("No add-target agents specified. The multiplexer cannot add any keys. Please specify --add-target if you want.")
	}
	if len(appCfg.AddTargets) > 0 {
		for _, atPath := range appCfg.AddTargets {
			addTargetAgents = append(addTargetAgents, pkg.MustNewAgent(atPath))
		}
	}

	targetAgents := []*pkg.Agent{}
	for _, t := range appCfg.Targets {
		targetAgents = append(targetAgents, pkg.MustNewAgent(t))
	}

	if len(targetAgents)+len(addTargetAgents) == 0 {
		log.Warn().Msg("No target agents specified. The multiplexer would not so useful. Please specify --target/--add-target.")
	}

	// TODO: `agt` should be updated if the configuration reloads and agent settings change.
	// This requires careful synchronization and is currently a placeholder for future work.
	// agt is declared here and will be captured by the config watcher goroutine.
	// It will also be used by the connection handling goroutines.
	// It's important that agt itself (the pointer) is not reassigned after this point,
	// but rather its internal state is updated via UpdateConfig.
	var agt agent.ExtendedAgent // Use the interface type
	agt = pkg.NewMuxAgent(targetAgents, addTargetAgents, appCfg.SelectTargetCommand)
	log.Debug().Msg("Succeed to connect all the target agents.")

	// Main accept loop for incoming agent connections.
	log.Info().Str("listen", effectiveListen).Msg("SSH Agent Multiplexer listening")
	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-signalCtx.Done(): // Shutdown initiated by signal.
				log.Info().Msg("Server shutting down (signal received before accept).")
			case <-cleanupCtx.Done(): // Shutdown initiated by listener close.
				log.Info().Msg("Server shutting down (listener closed before accept).")
			default:
				// Log unexpected errors from Accept. net.ErrClosed is expected during shutdown.
				if !errors.Is(err, net.ErrClosed) {
					log.Error().Err(err).Msg("Failed to accept new connection.")
				}
			}
			break // Exit loop on error or shutdown signal.
		}
		go func(conn net.Conn) {
			defer func() { _ = conn.Close() }() // Ensure connection is closed.
			// TODO: The `agt` used here is the one captured at goroutine creation.
			// If `agt` can be updated by config reload, a mechanism to use the latest `agt` is needed.
			errServe := agent.ServeAgent(agt, conn)
			if errServe != nil && errServe != io.EOF && !errors.Is(errServe, net.ErrClosed) {
				log.Error().Err(errServe).Msg("Error serving agent connection.")
			}
		}(c)
	}

	<-cleanupCtx.Done() // Wait for the listener cleanup goroutine to complete.
	log.Info().Msg("Agent multiplexer exited gracefully.")
}
