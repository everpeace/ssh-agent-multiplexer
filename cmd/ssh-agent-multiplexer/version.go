// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version number of SSH Agent Multiplexer",
		Run: func(cmd *cobra.Command, args []string) {
			log.Info().Msgf("SSH Agent Multiplexer Version=%s, Revision=%s", Version, Revision)
		},
	}
}
