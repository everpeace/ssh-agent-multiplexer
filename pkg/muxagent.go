// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package pkg

import (
	"bytes"
	"errors"

	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var _ agent.Agent = &MuxAgent{}

type MuxAgent struct {
	AddTarget Agent
	Targets   []Agent
}

func NewMuxAgent(targets []Agent, addTarget Agent) agent.Agent {
	return &MuxAgent{
		AddTarget: addTarget,
		Targets:   targets,
	}
}

// List implements agent.Agent
func (m *MuxAgent) List() ([]*agent.Key, error) {
	var err error
	keys := []*agent.Key{}
	m.iterate(func(a Agent) bool {
		logger := log.With().Str("method", "List").Str("path", a.path).Logger()
		_keys, err := a.List()
		if err != nil {
			logger.Error().Err(err).Msg("Failed to List keys")
			return true
		}
		keys = append(keys, _keys...)
		logger.Debug().Msgf("List() returns %d keys", len(_keys))
		return false
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// Lock implements agent.Agent
func (m *MuxAgent) Lock(passphrase []byte) error {
	m.iterate(func(a Agent) bool {
		logger := log.With().Str("method", "Lock").Str("path", a.path).Logger()
		err := a.Lock(passphrase)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to Lock. Ignored")
		}
		logger.Debug().Msg("Lock succeeded")
		return false
	})
	return nil
}

// Unlock implements agent.Agent
func (m *MuxAgent) Unlock(passphrase []byte) error {
	m.iterate(func(a Agent) bool {
		logger := log.With().Str("method", "Unlock").Str("path", a.path).Logger()
		err := a.Unlock(passphrase)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to Unlock. Ignored")
		}
		logger.Debug().Msg("UnLock succeeded")
		return false
	})
	return nil
}

type publicKeyToAgent struct {
	pk  ssh.PublicKey
	agt Agent
}

// Sign implements agent.Agent
func (m *MuxAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	mapping, err := m.publicKeyToAgentMapping()
	if err != nil {
		return nil, err
	}
	for _, e := range mapping {
		logger := log.With().Str("method", "Sign").Str("path", e.agt.path).Logger()
		if e.pk.Type() == key.Type() && bytes.Equal(e.pk.Marshal(), key.Marshal()) {
			signature, err := e.agt.Sign(key, data)
			if err != nil {
				logger.Error().Err(err).Msg("Failed to sign")
				return nil, err
			}
			logger.Debug().Msg("Signed")
			return signature, nil
		}
	}
	return nil, errors.New("Not found for suitable signer")
}

func (m *MuxAgent) publicKeyToAgentMapping() ([]publicKeyToAgent, error) {
	pkToAgents := []publicKeyToAgent{}
	var err error
	m.iterate(func(a Agent) bool {
		signers, err := a.Signers()
		if err != nil {
			return true
		}
		for _, signer := range signers {
			pkToAgents = append(pkToAgents, publicKeyToAgent{
				pk:  signer.PublicKey(),
				agt: a,
			})
		}
		return false
	})
	if err != nil {
		return nil, err
	}
	return pkToAgents, nil
}

// Signers implements agent.Agent
func (m *MuxAgent) Signers() ([]ssh.Signer, error) {
	signers := []ssh.Signer{}
	var err error
	m.iterate(func(a Agent) bool {
		logger := log.With().Str("method", "Signers").Str("path", a.path).Logger()
		_signers, err := a.Signers()
		if err != nil {
			logger.Error().Err(err).Msg("Failed to get Signers")
			return true
		}
		signers = append(signers, _signers...)
		logger.Error().Err(err).Msgf("Signers() returns %d signers", len(_signers))
		return false
	})
	if err != nil {
		return nil, err
	}
	return signers, nil
}

func (m *MuxAgent) iterate(f func(a Agent) bool) {
	for _, aux := range append(m.Targets, m.AddTarget) {
		if stop := f(aux); stop {
			return
		}
	}
}

// Add implements agent.Agent
func (m *MuxAgent) Add(key agent.AddedKey) error {
	logger := log.With().Str("method", "Add").Str("path", m.AddTarget.path).Logger()

	err := m.AddTarget.Add(key)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add a key")
		return err
	}

	logger.Debug().Msg("Added a key")
	return nil
}

// Remove implements agent.Agent
func (m *MuxAgent) Remove(key ssh.PublicKey) error {
	mapping, err := m.publicKeyToAgentMapping()
	if err != nil {
		return err
	}
	for _, e := range mapping {
		logger := log.With().Str("method", "Remove").Str("path", e.agt.path).Logger()
		if e.pk.Type() == key.Type() && bytes.Equal(e.pk.Marshal(), key.Marshal()) {
			err := e.agt.Remove(key)
			if err != nil {
				logger.Error().Err(err).Msg("Failed to remove a key")
				return err
			}
			logger.Debug().Msg("Removed a key")
			return nil
		}
	}
	log.Warn().Str("method", "Remove").Msg("Not found a key to remove. Ignored")
	return nil
}

// RemoveAll implements agent.Agent
func (m *MuxAgent) RemoveAll() error {
	m.iterate(func(a Agent) bool {
		logger := log.With().Str("method", "RemoveAll").Str("path", a.path).Logger()
		err := a.RemoveAll()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to remove all keys. Ignored")
			return false
		}
		logger.Debug().Msg("Removed all keys")
		return false
	})
	return nil
}
