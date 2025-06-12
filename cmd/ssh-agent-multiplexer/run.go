// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/everpeace/ssh-agent-multiplexer/pkg"
	"github.com/everpeace/ssh-agent-multiplexer/pkg/server"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the SSH Agent Multiplexer",
		Run: func(cmd *cobra.Command, args []string) {

			agentCreatorFunc := pkg.NewAgent

			app, err := server.NewApp(configFlagValue, agentCreatorFunc, Version, Revision)
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to initialize application.")
			}

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			appErrChan := make(chan error, 1)
			go func() {
				log.Info().Msg("Application starting...")
				appErrChan <- app.Start()
			}()

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

			app.Stop()

			log.Info().Msg("Shutdown complete.")
			os.Exit(0)
		},
	}
}
