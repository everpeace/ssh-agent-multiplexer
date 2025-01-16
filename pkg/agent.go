// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package pkg

import (
	"net"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var _ agent.ExtendedAgent = &Agent{}

type Agent struct {
	agent  agent.ExtendedAgent
	path   string
	logger zerolog.Logger

	lock sync.Mutex // protect updating agent
}

func MustNewAgent(path string) *Agent {
	logger := log.With().Str("path", path).Logger()
	a := &Agent{
		path:   path,
		logger: logger,
	}
	if err := a.connect(); err != nil {
		logger.Fatal().Msg("Failed to connect to the agent")
	}
	return a
}

func (a *Agent) connect() error {
	a.lock.Lock()
	defer a.lock.Unlock()

	conn, err := net.Dial("unix", a.path)
	a.logger.Debug().Msg("Connected the agent successfully")
	if err != nil {
		return err
	}
	a.agent = agent.NewClient(conn)
	return nil
}

func (a *Agent) retry(logger zerolog.Logger, f func() error) error {
	retryMax := 3
	var err error
	for try := 0; try < retryMax; try++ {
		err = f()
		if err != nil {
			logger.Debug().Err(err).Int("try", try+1).Msg("Trial failed, retrying with reconnecting...")
			_ = a.connect()
			continue
		}
		return nil
	}
	logger.Warn().Err(err).Int("retryMax", retryMax).Msg("Retry max reached")
	return err
}

// List returns the identities known to the agent.
func (a *Agent) List() ([]*agent.Key, error) {
	logger := a.logger.With().Str("method", "List").Logger()
	var ret []*agent.Key
	err := a.retry(logger, func() error {
		var err error
		ret, err = a.agent.List()
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ret, nil
}

// Sign has the agent sign the data using a protocol 2 key as defined
// in [PROTOCOL.agent] section 2.6.2.
func (a *Agent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	logger := a.logger.With().Str("method", "Sign").Logger()
	var ret *ssh.Signature
	err := a.retry(logger, func() error {
		var err error
		ret, err = a.agent.Sign(key, data)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ret, nil
}

// Add adds a private key to the agent.
func (a *Agent) Add(key agent.AddedKey) error {
	logger := a.logger.With().Str("method", "Add").Logger()
	return a.retry(logger, func() error {
		return a.agent.Add(key)
	})
}

// Remove removes all identities with the given public key.
func (a *Agent) Remove(key ssh.PublicKey) error {
	logger := a.logger.With().Str("method", "Remove").Logger()
	return a.retry(logger, func() error {
		return a.agent.Remove(key)
	})
}

// RemoveAll removes all identities.
func (a *Agent) RemoveAll() error {
	logger := a.logger.With().Str("method", "RemoveAll").Logger()
	return a.retry(logger, func() error {
		return a.agent.RemoveAll()
	})
}

// Lock locks the agent. Sign and Remove will fail, and List will empty an empty list.
func (a *Agent) Lock(passphrase []byte) error {
	logger := a.logger.With().Str("method", "Lock").Logger()
	return a.retry(logger, func() error {
		return a.agent.Lock(passphrase)
	})
}

// Unlock undoes the effect of Lock
func (a *Agent) Unlock(passphrase []byte) error {
	logger := a.logger.With().Str("method", "Unlock").Logger()
	return a.retry(logger, func() error {
		return a.agent.Unlock(passphrase)
	})
}

// Signers returns signers for all the known keys.
func (a *Agent) Signers() ([]ssh.Signer, error) {
	logger := a.logger.With().Str("method", "Sign").Logger()
	var ret []ssh.Signer
	err := a.retry(logger, func() error {
		var err error
		ret, err = a.agent.Signers()
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (a *Agent) Extension(extensionType string, contents []byte) ([]byte, error) {
	logger := a.logger.With().Str("method", "Extension").Logger()
	var ret []byte
	err := a.retry(logger, func() error {
		var err error
		ret, err = a.agent.Extension(extensionType, contents)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (a *Agent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	logger := a.logger.With().Str("method", "SignWithFlags").Logger()
	var ret *ssh.Signature
	err := a.retry(logger, func() error {
		var err error
		ret, err = a.agent.SignWithFlags(key, data, flags)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ret, nil
}
