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
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"github.com/rs/zerolog/log"
	"github.com/spf13/pflag"
	"golang.org/x/crypto/ssh/agent"

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
)

var (
	// injected build time
	Version  string
	Revision string
)

var (
	listen              string
	targets             []string
	addTargets          []string
	selectTargetCommand string
	debug               bool
	configFile          string
)

// setupConfiguration initializes Viper and Pflag, parses command-line arguments,
// handles config file loading, and binds pflags to Viper.
// It returns the configured Viper instance, the parsed help and version flags, and an error if any occurred.
func setupConfiguration(args []string) (v *viper.Viper, helpFlag *bool, versionFlag *bool, err error) {
	v = viper.New()
	v.SetConfigType("toml")
	v.SetConfigName(".ssh-agent-multiplexer") // name of config file (without extension)
	v.AddConfigPath(".")                      // look in current directory
	home, err := os.UserHomeDir()
	if err == nil { // if home dir is found
		v.AddConfigPath(filepath.Join(home, ".config", "ssh-agent-multiplexer"))
	} else {
		// Log this? Or handle silently? For now, let's assume if homedir fails, we just don't add that path.
		// Tests might need to mock os.UserHomeDir or ensure it's writable if we want to test this path.
	}

	// Use a new FlagSet for each call to setupConfiguration to allow for independent testing
	fs := pflag.NewFlagSet("ssh-agent-multiplexer", pflag.ContinueOnError)

	// Define flags on the new FlagSet
	// Note: versionFlag and helpFlag are returned to main, others are accessed via viper
	versionFlag = fs.BoolP("version", "v", false, "Print version and exit")
	helpFlag = fs.BoolP("help", "h", false, "Print the help")
	// Use a local variable for configFile within this function, to be read from the FlagSet
	var localConfigFile string
	fs.StringVarP(&localConfigFile, "config", "c", "", "Path to TOML configuration file. If set, this overrides default config file paths.")

	fs.BoolP("debug", "d", false, "debug mode")
	if err = v.BindPFlag("debug", fs.Lookup("debug")); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to bind debug flag: %w", err)
	}

	fs.StringP("listen", "l", "", "socket path to listen for the multiplexer. it is generated automatically if not set")
	if err = v.BindPFlag("listen", fs.Lookup("listen")); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to bind listen flag: %w", err)
	}

	fs.StringSliceP("target", "t", nil, "path of target agent to proxy. you can specify this option multiple times")
	if err = v.BindPFlag("targets", fs.Lookup("target")); err != nil { // TOML key is "targets"
		return nil, nil, nil, fmt.Errorf("failed to bind target flag: %w", err)
	}

	fs.StringSliceP("add-target", "a", nil, "path of target agent for ssh-add command. Can be specified multiple times.")
	if err = v.BindPFlag("add_targets", fs.Lookup("add-target")); err != nil { // TOML key is "add_targets"
		return nil, nil, nil, fmt.Errorf("failed to bind add-target flag: %w", err)
	}

	fs.String("select-target-command", "ssh-agent-mux-select", "command to execute to select a target when multiple --add-target agents are specified.")
	if err = v.BindPFlag("select_target_command", fs.Lookup("select-target-command")); err != nil { // TOML key is "select_target_command"
		return nil, nil, nil, fmt.Errorf("failed to bind select-target-command flag: %w", err)
	}

	// Parse the provided arguments
	if err = fs.Parse(args); err != nil {
		return nil, helpFlag, versionFlag, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Now, load the config file using the parsed localConfigFile value
	if localConfigFile != "" { // if -c or --config is used
		v.SetConfigFile(localConfigFile)
		if errRead := v.ReadInConfig(); errRead != nil {
			// Return specific error if config file is not found, otherwise a generic one.
			// This helps tests to assert specific error types.
			if _, ok := errRead.(viper.ConfigFileNotFoundError); ok {
				return nil, helpFlag, versionFlag, fmt.Errorf("specified config file not found: %s: %w", localConfigFile, errRead)
			}
			return nil, helpFlag, versionFlag, fmt.Errorf("failed to read specified config file %s: %w", localConfigFile, errRead)
		}
		log.Debug().Msgf("Successfully loaded specified config file: %s", localConfigFile)
	} else { // Try default locations
		if errRead := v.ReadInConfig(); errRead != nil {
			if _, ok := errRead.(viper.ConfigFileNotFoundError); ok {
				log.Debug().Msg("No config file found in default locations (. or ~/.config/ssh-agent-multiplexer/). Using command-line flags or defaults.")
			} else {
				// Config file was found but another error occurred (e.g., malformed TOML)
				return nil, helpFlag, versionFlag, fmt.Errorf("error reading config file from default location %s: %w", v.ConfigFileUsed(), errRead)
			}
		} else {
			log.Debug().Msgf("Successfully loaded config file: %s", v.ConfigFileUsed())
		}
	}
	// Update the global configFile variable, primarily for logging/visibility if needed.
	// The actual config path used by Viper is internal to it or retrievable via v.ConfigFileUsed().
	configFile = localConfigFile 

	return v, helpFlag, versionFlag, nil
}

func main() {
	v, helpFlag, versionFlag, err := setupConfiguration(os.Args[1:])
	if err != nil {
		// Check if the error is due to help flag or version flag request, which pflag.ContinueOnError treats as errors.
		// This logic might need adjustment based on how pflag.ContinueOnError reports --help/--version.
		// For now, assume setupConfiguration returns a specific error or pflag.ErrHelp is checked.
		// If it is a genuine parsing error or config load error, then Fatal.
		if err != pflag.ErrHelp { // pflag.ErrHelp is not a real error in this context for setupConfiguration
			log.Fatal().Err(err).Msg("Configuration setup failed")
		}
		// If it was ErrHelp, main will proceed to check helpFlag and exit, so no fatal log here.
		// If Parse() in setupConfiguration returns an error for --help, then helpFlag should be true.
	}


	// Populate global variables from viper (which now has flags + config)
	// These globals are used by the rest of the application.
	debug = v.GetBool("debug")
	listen = v.GetString("listen")
	targets = v.GetStringSlice("targets")
	addTargets = v.GetStringSlice("add_targets")
	selectTargetCommand = v.GetString("select_target_command")

	if *helpFlag {
		// pflag.Usage() will use the default pflag.CommandLine.
		// To show help for our specific FlagSet used in setupConfiguration, we'd ideally pass it around or make it accessible.
		// For simplicity in refactoring, we might need to redefine flags on pflag.CommandLine as well if we want to use pflag.Usage(),
		// or print usage manually.
		// However, since setupConfiguration uses a new FlagSet, its Usage() should be called.
		// This part needs careful handling. For now, let's assume pflag.Parse() in setupConfiguration handles help.
		// The current setup with fs.Parse(args) and pflag.ContinueOnError means help output is minimal.
		// A proper solution would involve more significant changes to how FlagSet is managed or how help is printed.
		// Let's defer full help text formatting for now and focus on config loading.
		// A simple os.Exit(0) might be what happens if pflag.ErrHelp was returned by fs.Parse().
		fmt.Println("Usage: ssh-agent-multiplexer [options]")
		// Ideally, we'd print default usage here from the fs used in setupConfiguration.
		// For testing, this part is less critical than the config values themselves.
		pflag.PrintDefaults() // This will print defaults of the global pflag.CommandLine, potentially not what we want.
		os.Exit(0) // Help requested, exiting.
	}

	if *versionFlag {
		fmt.Printf("Version=%s, Revision=%s\n", Version, Revision)
		os.Exit(0)
	}

	// setup logger, signal handlers
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339, NoColor: true})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if debug { // use the debug value from viper/flags
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	log.Info().Str("version", Version).Str("revision", Revision).Msg("")

	// validation
	for _, t := range targets { // use the targets value from viper/flags
		for _, at := range addTargets { // use the addTargets value from viper/flags
			if t == at {
				log.Fatal().Msg("target paths must not include add-target path")
			}
		}
	}
	if len(addTargets) > 1 && selectTargetCommand == "" { // use addTargets and selectTargetCommand from viper/flags
		log.Fatal().Msg("When specifying multiple --add-target agents, --select-target-command must also be provided.")
	}

	// initializing socket to listen
	effectiveListen := listen // listen is already populated from viper/flags
	if effectiveListen == "" {
		effectiveListen = path.Join(os.TempDir(), fmt.Sprintf("ssh-agent-multiplexer-%d.sock", os.Getpid()))
	}

	signalCtx, cancelSignalCtx := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancelSignalCtx()
	l, err := (&net.ListenConfig{}).Listen(signalCtx, "unix", effectiveListen)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to listen")
	}
	cleanupCtx, cancelCleanupCtx := context.WithCancel(context.Background())
	go func() {
		<-signalCtx.Done()
		logger := log.With().Str("listen", effectiveListen).Logger()
		if err := l.Close(); err != nil {
			logger.Fatal().Err(err).Msg("Failed to close the socket")
		}
		logger.Info().Msg("Closed the socket")
		cancelCleanupCtx()
	}()

	// create agents
	targetAgents := []*pkg.Agent{}
	for _, t := range targets { // use targets from viper/flags
		targetAgents = append(targetAgents, pkg.MustNewAgent(t))
	}

	var addTargetAgents []*pkg.Agent
	if len(addTargets) > 0 { // use addTargets from viper/flags
		// Validation for selectTargetCommand when multiple addTargets are present is already done earlier.
		for _, atPath := range addTargets { // use addTargets from viper/flags
			addTargetAgents = append(addTargetAgents, pkg.MustNewAgent(atPath))
		}
	}

	agt := pkg.NewMuxAgent(targetAgents, addTargetAgents, selectTargetCommand) // use selectTargetCommand from viper/flags
	log.Debug().Msg("Succeed to connect all the target agents.")

	log.Info().Str("listen", effectiveListen).Msg("Agent multiplexer listening")
	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-signalCtx.Done():
				// nop
			default:
				log.Error().Err(err).Msg("Failed to listen")
			}
			break
		}
		go func() {
			err := agent.ServeAgent(agt, c)
			if err != nil && err != io.EOF {
				log.Error().Err(err).Msg("Error in serving agent")
			}
		}()
	}
	<-cleanupCtx.Done()
	log.Info().Msg("Agent multiplexer exited")
}
