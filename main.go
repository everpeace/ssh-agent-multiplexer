// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"fmt"
	"io" // For pflag.ErrHelp and preParseFs.SetOutput(io.Discard)
	"os"
	"os/signal"
	"syscall"
	"time" // Required for initial logger setup if it uses TimeFormat

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
	"github.com/everpeace/ssh-agent-multiplexer/pkg/config" // Needed for config.DefineAndBindFlags
	"github.com/everpeace/ssh-agent-multiplexer/pkg/server" // Updated import path (single instance)
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"
	"github.com/spf13/viper" // Needed for config.DefineAndBindFlags
)

var (
	// injected build time
	Version  string
	Revision string
)

func main() {
	// 1. Pre-parse --config flag value to pass to NewApp if specified.
	configFlagValue := ""
	preParseFs := pflag.NewFlagSet("preparse", pflag.ContinueOnError)
	preParseFs.SetOutput(io.Discard) // Suppress output during this preparse.
	preParseFs.StringVarP(&configFlagValue, "config", "c", "", "Path to a configuration file.")
	_ = preParseFs.Parse(os.Args[1:])

	// 2. Define and Parse All Command-Line Flags.
	v := viper.New() // Viper instance for flag binding.
	mainFlagSet := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	mainFlagSet.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		mainFlagSet.PrintDefaults()
	}

	mainFlagSet.BoolP("version", "v", false, "Print version and exit.")
	mainFlagSet.BoolP("help", "h", false, "Print this help message and exit.")
	mainFlagSet.StringP("config", "c", configFlagValue, "Path to a configuration file.")

	// var cliListenOverride string
	// mainFlagSet.StringVarP(&cliListenOverride, "listen", "l", "", "Path to the unix domain socket to listen on. Overrides config file if set.")

	// Define other flags that server.NewApp will expect to be bound in Viper.
	// config.DefineAndBindFlags handles this.
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

	// 3. Minimal Initial Logging Setup.
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	zerolog.SetGlobalLevel(zerolog.InfoLevel) // App can change this based on its config.

	// 4. Agent Creator Function.
	agentCreatorFunc := pkg.NewAgent

	// 5. Create and Initialize the App.
	// Note: `cliListenOverride` is directly from parsed flags.
	// `configFlagValue` (path to config file) is also from flags.
	// Other config values (debug, targets, etc.) are expected to be picked up by
	// server.NewApp through its internal call to config.GetAppConfig(v, configFileUsed),
	// where 'v' has been populated by config.DefineAndBindFlags and mainFlagSet.Parse().
	app, err := server.NewApp(configFlagValue, agentCreatorFunc, Version, Revision)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize application.")
	}

	// 6. Setup OS Signal Handling for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 7. Start the application in a goroutine.
	appErrChan := make(chan error, 1) // Channel to receive error from app.Start()
	go func() {
		log.Info().Msg("Application starting...")
		appErrChan <- app.Start()
	}()

	// 8. Wait for a shutdown signal or an error from app.Start().
	select {
	case s := <-sigChan:
		log.Info().Str("signal", s.String()).Msg("Received OS signal, initiating graceful shutdown...")
	case err := <-appErrChan:
		if err != nil {
			log.Error().Err(err).Msg("Application exited with error.")
		} else {
			log.Info().Msg("Application exited normally.")
		}
	}

	// 9. Stop the application.
	// server.App.Stop() is void and handles its own logging for errors during shutdown.
	app.Stop()

	log.Info().Msg("Shutdown complete.")
	os.Exit(0)
}
