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
	"path"
	"syscall"
	"time"

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

func main() {
	// 1. Pre-parse --config flag
	configFlagValue := ""
	preParseFs := pflag.NewFlagSet("preparse", pflag.ContinueOnError)
	preParseFs.SetOutput(io.Discard) // Suppress output during preparse
	preParseFs.StringVarP(&configFlagValue, "config", "c", "", "Path to a configuration file")
	// Parse all args, but we only care about --config. Errors are ignored.
	_ = preParseFs.Parse(os.Args[1:])

	// 2. Load Viper Config
	v, configFileUsed, err := config.LoadViperConfig(configFlagValue)
	if err != nil {
		// Log fatal only if a specific config file was given and failed to load.
		// If configFlagValue is empty, LoadViperConfig only errors on malformed default config, not missing.
		log.Fatal().Err(err).Msgf("Failed to load configuration")
	}

	// 3. Create Main FlagSet
	mainFlagSet := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError) // ExitOnError will handle parsing errors and print usage.

	// 4. Define and Bind Flags
	if err := config.DefineAndBindFlags(v, mainFlagSet); err != nil {
		log.Fatal().Err(err).Msg("Failed to define or bind flags")
	}

	// 5. Parse Command-Line Arguments
	if err := mainFlagSet.Parse(os.Args[1:]); err != nil {
		// ExitOnError above should handle this, but good practice to check.
		log.Fatal().Err(err).Msg("Failed to parse command-line arguments")
	}

	// 6. Handle --help and --version flags
	if help, _ := mainFlagSet.GetBool("help"); help {
		// For pflag.ExitOnError, help is typically handled automatically.
		// This explicit check is more for pflag.ContinueOnError or if customized help is needed.
		// However, pflag.ExitOnError usually exits before this point if --help is passed.
		// If we reach here, it means ExitOnError didn't exit (e.g. if it was changed to ContinueOnError).
		_, _ = fmt.Fprintf(os.Stdout, "Usage of %s:\n", os.Args[0])
		mainFlagSet.PrintDefaults()
		os.Exit(0)
	}
	if ver, _ := mainFlagSet.GetBool("version"); ver {
		fmt.Printf("Version=%s, Revision=%s\n", Version, Revision)
		os.Exit(0)
	}

	// 7. Get Final Application Configuration
	appCfg := config.GetAppConfig(v, configFileUsed)

	// 8. Logging Setup
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339, NoColor: true})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if appCfg.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	log.Info().Str("version", Version).Str("revision", Revision).Msg("ssh-agent-multiplexer starting")
	if appCfg.ConfigFilePathUsed != "" {
		log.Info().Str("path", appCfg.ConfigFilePathUsed).Msg("Loaded configuration from")
	}
	log.Info().Object("config", appCfg).Msg("Effective config")

	// 9. Validation (using appCfg)
	for _, t := range appCfg.Targets {
		for _, at := range appCfg.AddTargets {
			if t == at {
				log.Fatal().Msg("Target paths must not include add-target path")
			}
		}
	}
	if len(appCfg.AddTargets) > 1 && appCfg.SelectTargetCommand == "" {
		log.Fatal().Msg("When specifying multiple --add-target agents, --select-target-command must also be provided.")
	}

	// 10. Initializing socket to listen (using appCfg)
	effectiveListen := appCfg.Listen
	if effectiveListen == "" {
		effectiveListen = path.Join(os.TempDir(), fmt.Sprintf("ssh-agent-multiplexer-%d.sock", os.Getpid()))
	}

	// Setup signal handling and listener
	signalCtx, cancelSignalCtx := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancelSignalCtx()
	l, err := (&net.ListenConfig{}).Listen(signalCtx, "unix", effectiveListen)
	if err != nil {
		log.Fatal().Err(err).Str("listen_path", effectiveListen).Msg("Failed to listen on socket")
	}
	cleanupCtx, cancelCleanupCtx := context.WithCancel(context.Background())
	go func() {
		<-signalCtx.Done()
		logger := log.With().Str("listen", effectiveListen).Logger()
		if err := l.Close(); err != nil {
			logger.Error().Err(err).Msg("Failed to close the socket") // Changed to Error from Fatal for graceful shutdown
		} else {
			logger.Info().Msg("Closed the socket")
		}
		cancelCleanupCtx()
	}()

	// Create agents (using appCfg)
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

	agt := pkg.NewMuxAgent(targetAgents, addTargetAgents, appCfg.SelectTargetCommand)
	log.Debug().Msg("Succeed to connect all the target agents.")

	// Main accept loop
	log.Info().Str("listen", effectiveListen).Msg("SSH Agent Multiplexer listening")
	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-signalCtx.Done(): // Graceful shutdown initiated
				log.Info().Msg("Server shutting down due to signal.")
			case <-cleanupCtx.Done(): // Graceful shutdown initiated by closing listener
				log.Info().Msg("Server shutting down due to listener closed.")
			default: // Other accept error
				log.Error().Err(err).Msg("Failed to accept connection")
			}
			break // Exit loop on error or shutdown signal
		}
		go func(conn net.Conn) {
			defer func() { _ = conn.Close() }() // Ensure connection is closed for each goroutine
			err := agent.ServeAgent(agt, conn)
			if err != nil && err != io.EOF {
				log.Error().Err(err).Msg("Error in serving agent")
			}
		}(c)
	}

	<-cleanupCtx.Done() // Wait for cleanup goroutine (socket close) to complete
	log.Info().Msg("Agent multiplexer exited gracefully")
}
