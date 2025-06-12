// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"os"
	"time"

	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	// injected build time
	Version  string
	Revision string
)

var (
	rootCmd = &cobra.Command{
		Use:   "ssh-agent-multiplexer",
		Short: "SSH Agent Multiplexer",
	}
	configFlagValue string
)

func main() {

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	rootCmd.PersistentFlags().StringVarP(&configFlagValue, "config", "c", "", "Path to a configuration file.")

	v := viper.New()
	if err := config.DefineAndBindFlags(v, rootCmd.PersistentFlags()); err != nil {
		log.Fatal().Err(err).Msg("Failed to define and bind application flags.")
	}

	rootCmd.AddCommand(newRunCmd())
	rootCmd.AddCommand(newVersionCmd())

	if err := rootCmd.Execute(); err != nil {
		log.Fatal().Err(err).Msg("Failed to execute command.")
	}
}
