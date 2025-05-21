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
)

var (
	// injected build time
	Version  string
	Revision string
)

var (
	listen    string
	targets   []string
	addTarget string
	debug     bool
)

func main() {
	version := pflag.BoolP("version", "v", false, "Print version and exit")
	help := pflag.BoolP("help", "h", false, "Print the help")
	pflag.BoolVarP(&debug, "debug", "d", false, "debug mode")
	pflag.StringVarP(&listen, "listen", "l", "", "socket path to listen for the multiplexer. it is generated automatically if not set")
	pflag.StringSliceVarP(&targets, "target", "t", nil, "path of target agent to proxy. you can specify this option multiple times")
	pflag.StringVarP(&addTarget, "add-target", "a", "", "path of target agent for ssh-add command")
	pflag.Parse()

	if *help {
		pflag.Usage()
		os.Exit(1)
	}

	if *version {
		fmt.Printf("Version=%s, Revision=%s", Version, Revision)
		os.Exit(0)
	}

	// setup logger, signal handlers
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339, NoColor: true})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	log.Info().Str("version", Version).Str("revision", Revision).Msg("")

	// validation
	for _, t := range targets {
		if t == addTarget {
			log.Fatal().Msg("target paths must not include add-target path")
		}
	}

	// initializing socket to listen
	if listen == "" {
		listen = path.Join(os.TempDir(), fmt.Sprintf("ssh-agent-multiplexer-%d.sock", os.Getpid()))
	}

	signalCtx, cancelSignalCtx := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancelSignalCtx()
	l, err := (&net.ListenConfig{}).Listen(signalCtx, "unix", listen)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to listen")
	}
	cleanupCtx, cancelCleanupCtx := context.WithCancel(context.Background())
	go func() {
		<-signalCtx.Done()
		logger := log.With().Str("listen", listen).Logger()
		if err := l.Close(); err != nil {
			logger.Fatal().Err(err).Msg("Failed to close the socket")
		}
		logger.Info().Msg("Closed the socket")
		cancelCleanupCtx()
	}()

	// create agents
	targetAgents := []*pkg.Agent{}
	for _, t := range targets {
		targetAgents = append(targetAgents, pkg.MustNewAgent(t))
	}
	var addAgent *pkg.Agent
	if addTarget != "" {
		addAgent = pkg.MustNewAgent(addTarget)
	}
	agt := pkg.NewMuxAgent(targetAgents, addAgent)
	log.Debug().Msg("Succeed to connect all the target agents.")

	log.Info().Str("listen", listen).Msg("Agent multiplexer listening")
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
