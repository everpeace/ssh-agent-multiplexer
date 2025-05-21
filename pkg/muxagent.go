// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package pkg

import (
	"bytes"
	"crypto" // For crypto.Signer
	"errors"
	"fmt"
	"os"      // For os.Environ, exec.Command
	"os/exec" // For exec.Command
	"strings" // For strings.Join, etc.

	"github.com/rs/zerolog/log"
	"go.uber.org/multierr"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Second import block removed by consolidation

var _ agent.ExtendedAgent = &MuxAgent{}

type MuxAgent struct {
	AddTargets          []*Agent
	Targets             []*Agent
	SelectTargetCommand string
}

func NewMuxAgent(targets []*Agent, addTargets []*Agent, selectTargetCommand string) agent.Agent {
	return &MuxAgent{
		AddTargets:          addTargets,
		Targets:             targets,
		SelectTargetCommand: selectTargetCommand,
	}
}

// List implements agent.Agent
func (m *MuxAgent) List() ([]*agent.Key, error) {
	var err error
	keys := []*agent.Key{}
	m.iterate(func(a *Agent) bool {
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
	m.iterate(func(a *Agent) bool {
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
	m.iterate(func(a *Agent) bool {
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
	agt *Agent
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
	return nil, errors.New("not found for suitable signer")
}

func (m *MuxAgent) publicKeyToAgentMapping() ([]publicKeyToAgent, error) {
	pkToAgents := []publicKeyToAgent{}
	var err error
	m.iterate(func(a *Agent) bool {
		var signers []ssh.Signer
		signers, err = a.Signers()
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
	m.iterate(func(a *Agent) bool {
		logger := log.With().Str("method", "Signers").Str("path", a.path).Logger()
		var _signers []ssh.Signer
		_signers, err = a.Signers()
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

func (m *MuxAgent) iterate(f func(a *Agent) bool) {
	agentsToIterate := make([]*Agent, 0, len(m.Targets)+len(m.AddTargets))
	if len(m.AddTargets) > 0 {
		agentsToIterate = append(agentsToIterate, m.AddTargets...)
	}
	agentsToIterate = append(agentsToIterate, m.Targets...)

	for _, agt := range agentsToIterate {
		// It's possible for an agent in m.Targets to be nil if the input was bad,
		// though current main.go logic for -t doesn't allow nil agents in m.Targets.
		// However, a defensive check here is good.
		if agt == nil {
			continue
		}
		if stop := f(agt); stop {
			return
		}
	}
}

// Add implements agent.Agent
func (m *MuxAgent) Add(key agent.AddedKey) error {
	if len(m.AddTargets) == 0 {
		log.Error().Msg("Failed to add a key: no add-target specified")
		return errors.New("add functionality disabled: no add-target specified")
	}

	var selectedAgent *Agent

	if len(m.AddTargets) == 1 {
		selectedAgent = m.AddTargets[0]
		log.Debug().Str("path", selectedAgent.path).Msg("Selected single agent for adding key")
	} else {
		// Multiple AddTargets
		if m.SelectTargetCommand == "" {
			log.Error().Msg("Multiple add-targets specified but no select-target-command configured")
			return errors.New("multiple add-targets specified but no select-target-command configured")
		}

		var targetPaths []string
		for _, agent := range m.AddTargets {
			targetPaths = append(targetPaths, agent.path)
		}
		targetsEnvVar := strings.Join(targetPaths, "\n")

		// Construct SSH_AGENT_MUX_KEY_INFO
		var sshPubKey ssh.PublicKey
		var pubKeyErr error
		keyInfoParts := []string{}

		if privKey, ok := key.PrivateKey.(crypto.Signer); ok {
			pub := privKey.Public()
			sshPubKey, pubKeyErr = ssh.NewPublicKey(pub)
			if pubKeyErr != nil {
				log.Warn().Err(pubKeyErr).Msg("Failed to derive ssh.PublicKey from private key's public part")
			}
		} else {
			log.Warn().Msgf("Private key type %T does not implement crypto.Signer, cannot derive public key", key.PrivateKey)
			pubKeyErr = fmt.Errorf("private key type %T does not implement crypto.Signer", key.PrivateKey)
		}

		if key.Comment != "" {
			keyInfoParts = append(keyInfoParts, fmt.Sprintf("COMMENT=%s", key.Comment))
		} else {
			keyInfoParts = append(keyInfoParts, "COMMENT=") // Ensure key is present
		}

		if pubKeyErr == nil && sshPubKey != nil {
			keyInfoParts = append(keyInfoParts, fmt.Sprintf("TYPE=%s", sshPubKey.Type()))
			keyInfoParts = append(keyInfoParts, fmt.Sprintf("FINGERPRINT_SHA256=%s", ssh.FingerprintSHA256(sshPubKey)))
		} else {
			keyInfoParts = append(keyInfoParts, "TYPE=unknown")
			keyInfoParts = append(keyInfoParts, "FINGERPRINT_SHA256=unknown")
		}
		keyInfoString := strings.Join(keyInfoParts, ";")

		cmd := exec.Command(m.SelectTargetCommand)
		env := os.Environ()
		env = append(env, "SSH_AGENT_MUX_TARGETS="+targetsEnvVar)
		env = append(env, "SSH_AGENT_MUX_KEY_INFO="+keyInfoString)
		cmd.Env = env

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		log.Debug().Str("command", m.SelectTargetCommand).Strs("env_targets", targetPaths).Msg("Executing select-target-command")
		err := cmd.Run()
		if err != nil {
			log.Error().Err(err).Str("command", m.SelectTargetCommand).Str("stderr", stderr.String()).Msg("Failed to execute select-target-command")
			return fmt.Errorf("failed to execute select-target-command '%s': %w. Stderr: %s", m.SelectTargetCommand, err, stderr.String())
		}

		selectedTargetPath := strings.TrimSpace(stdout.String())
		if selectedTargetPath == "" {
			log.Error().Str("command", m.SelectTargetCommand).Msg("select-target-command returned empty output")
			return errors.New("select-target-command returned empty output")
		}

		log.Debug().Str("command", m.SelectTargetCommand).Str("selected_path_raw", stdout.String()).Str("selected_path_trimmed", selectedTargetPath).Msg("select-target-command output")

		for _, agent := range m.AddTargets {
			if agent.path == selectedTargetPath {
				selectedAgent = agent
				break
			}
		}

		if selectedAgent == nil {
			log.Error().Str("command", m.SelectTargetCommand).Str("returned_path", selectedTargetPath).Msg("select-target-command returned an invalid target path")
			return fmt.Errorf("select-target-command returned an invalid target path: '%s'", selectedTargetPath)
		}
		log.Debug().Str("path", selectedAgent.path).Str("command", m.SelectTargetCommand).Msg("Selected agent for adding key via command")
	}

	logger := log.With().Str("method", "Add").Str("path", selectedAgent.path).Logger()
	err := selectedAgent.Add(key)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to add a key")
		return err
	}

	logger.Debug().Msg("Added key")
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
	m.iterate(func(a *Agent) bool {
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

// Extension implements agent.ExtendedAgent.
func (m *MuxAgent) Extension(extensionType string, contents []byte) ([]byte, error) {
	var resp []byte
	var errs error

	m.iterate(func(a *Agent) bool {
		logger := log.With().Str("method", "Extension").Str("path", a.path).Logger()
		var err error
		resp, err = a.Extension(extensionType, contents)
		if err != nil {
			if err == agent.ErrExtensionUnsupported {
				logger.Debug().Err(err).Msg("Try next agent")
				return false
			}
			logger.Warn().Err(err).Msg("Failed to run extension. Try next Agent")
			errs = multierr.Append(errs, fmt.Errorf("Extension failed on %s: %w", a.path, err))
			return false
		}
		logger.Debug().Msg("Removed all keys")
		return true
	})

	if resp != nil {
		return resp, nil
	}

	return nil, errs
}

// SignWithFlags implements agent.ExtendedAgent.
func (m *MuxAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	mapping, err := m.publicKeyToAgentMapping()
	if err != nil {
		return nil, err
	}
	for _, e := range mapping {
		logger := log.With().Str("method", "SignWithFlags").Str("path", e.agt.path).Logger()
		if e.pk.Type() == key.Type() && bytes.Equal(e.pk.Marshal(), key.Marshal()) {
			signature, err := e.agt.SignWithFlags(key, data, flags)
			if err != nil {
				logger.Error().Err(err).Msg("Failed to sign")
				return nil, err
			}
			logger.Debug().Msg("Signed")
			return signature, nil
		}
	}
	return nil, errors.New("not found for suitable signer")
}
