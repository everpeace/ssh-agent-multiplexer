// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package pkg

import (
	"net"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/ssh/agent"
)

type Agent struct {
	agent.Agent
	path string
}

func MustNewAgent(path string) Agent {
	logger := log.With().Str("path", path).Logger()
	conn, err := net.Dial("unix", path)
	if err != nil {
		logger.Fatal().Msg("Failed to connect to the agent")
	}
	logger.Debug().Msg("Connected the agent successfully")
	return Agent{
		Agent: agent.NewClient(conn),
		path:  path,
	}
}
