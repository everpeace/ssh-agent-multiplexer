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
	"sync"    // For sync.RWMutex

	"github.com/rs/zerolog/log"
	"go.uber.org/multierr"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/everpeace/ssh-agent-multiplexer/pkg/config" // For UpdateConfig
)

// Second import block removed by consolidation

var _ agent.ExtendedAgent = &MuxAgent{}

type MuxAgent struct {
	mu                  sync.RWMutex
	AddTargets          []*Agent
	Targets             []*Agent
	SelectTargetCommand string
}

// NewMuxAgent creates a new MuxAgent.
// NewMuxAgent creates a new MuxAgent from the given application configuration.
// It initializes the target agents based on the paths provided in the config.
func NewMuxAgent(cfg *config.AppConfig) agent.ExtendedAgent {
	log.Debug().Msg("MuxAgent: Initializing from AppConfig")

	addTargets := make([]*Agent, 0, len(cfg.AddTargets))
	for _, atPath := range cfg.AddTargets {
		// MustNewAgent will handle creating/connecting to the agent.
		// It might panic if a path is invalid or connection fails,
		// which is acceptable for a "Must" constructor.
		addTargets = append(addTargets, MustNewAgent(atPath))
	}
	log.Debug().Int("count", len(addTargets)).Msg("MuxAgent: Initialized AddTargets")

	targets := make([]*Agent, 0, len(cfg.Targets))
	for _, tPath := range cfg.Targets {
		targets = append(targets, MustNewAgent(tPath))
	}
	log.Debug().Int("count", len(targets)).Msg("MuxAgent: Initialized Targets")

	muxAgent := &MuxAgent{
		// mu is initialized as the zero value for sync.RWMutex
		AddTargets:          addTargets,
		Targets:             targets,
		SelectTargetCommand: cfg.SelectTargetCommand,
	}
	log.Debug().Str("command", muxAgent.SelectTargetCommand).Msg("MuxAgent: Set SelectTargetCommand")
	log.Info().Msg("MuxAgent: Initialized successfully from AppConfig")
	return muxAgent
}

// UpdateConfig updates the MuxAgent's configuration.
// This includes re-initializing target agents and other settings.
func (m *MuxAgent) UpdateConfig(newCfg *config.AppConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	log.Info().Msg("MuxAgent: Updating configuration")

	// Re-initialize AddTargets
	newAddTargets := make([]*Agent, 0, len(newCfg.AddTargets))
	for _, atPath := range newCfg.AddTargets {
		// Assuming pkg.MustNewAgent handles creating/connecting to the agent.
		// If an agent with the same path existed, this creates a new connection.
		// TODO: Consider more sophisticated agent lifecycle management (e.g., reuse if path matches).
		newAddTargets = append(newAddTargets, MustNewAgent(atPath))
	}
	m.AddTargets = newAddTargets
	log.Debug().Int("count", len(m.AddTargets)).Msg("MuxAgent: Updated AddTargets")

	// Re-initialize Targets
	newTargets := make([]*Agent, 0, len(newCfg.Targets))
	for _, tPath := range newCfg.Targets {
		newTargets = append(newTargets, MustNewAgent(tPath))
	}
	m.Targets = newTargets
	log.Debug().Int("count", len(m.Targets)).Msg("MuxAgent: Updated Targets")

	// Update SelectTargetCommand
	m.SelectTargetCommand = newCfg.SelectTargetCommand
	log.Debug().Str("command", m.SelectTargetCommand).Msg("MuxAgent: Updated SelectTargetCommand")

	log.Info().Msg("MuxAgent: Configuration updated successfully")
}

// GetTargetPaths is a test-only helper to inspect current target paths.
func (m *MuxAgent) GetTargetPaths() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	paths := make([]string, len(m.Targets))
	for i, agent := range m.Targets {
		paths[i] = agent.path // Assuming Agent struct has a 'path' field
	}
	return paths
}

// GetAddTargetPaths is a test-only helper to inspect current addTarget paths.
func (m *MuxAgent) GetAddTargetPaths() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	paths := make([]string, len(m.AddTargets))
	for i, agent := range m.AddTargets {
		paths[i] = agent.path // Assuming Agent struct has a 'path' field
	}
	return paths
}

// List implements agent.Agent
func (m *MuxAgent) List() ([]*agent.Key, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var err error
	keys := []*agent.Key{}
	m.iterate(func(a *Agent) bool { // iterate is now called under RLock
		logger := log.With().Str("method", "List").Str("path", a.path).Logger()
		_keys, errList := a.List() // Use new variable for error inside loop
		if errList != nil {
			logger.Error().Err(errList).Msg("Failed to List keys")
			err = multierr.Append(err, errList) // Collect errors
			return true                         // Continue to try other agents if one fails
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	var errCol error
	m.iterate(func(a *Agent) bool { // iterate is now called under RLock
		logger := log.With().Str("method", "Lock").Str("path", a.path).Logger()
		err := a.Lock(passphrase)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to Lock. Ignored for this agent, trying others.")
			errCol = multierr.Append(errCol, err) // Collect errors
		} else {
			logger.Debug().Msg("Lock succeeded")
		}
		return false // Continue with other agents even if one fails or succeeds
	})
	return errCol // Return collected errors, or nil if all succeeded
}

// Unlock implements agent.Agent
func (m *MuxAgent) Unlock(passphrase []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var errCol error
	m.iterate(func(a *Agent) bool { // iterate is now called under RLock
		logger := log.With().Str("method", "Unlock").Str("path", a.path).Logger()
		err := a.Unlock(passphrase)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to Unlock. Ignored for this agent, trying others.")
			errCol = multierr.Append(errCol, err) // Collect errors
		} else {
			logger.Debug().Msg("UnLock succeeded")
		}
		return false // Continue with other agents
	})
	return errCol // Return collected errors, or nil if all succeeded
}

type publicKeyToAgent struct {
	pk  ssh.PublicKey
	agt *Agent
}

// Sign implements agent.Agent
func (m *MuxAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mapping, err := m.publicKeyToAgentMapping() // publicKeyToAgentMapping is now called under RLock
	if err != nil {
		return nil, err
	}
	for _, e := range mapping {
		logger := log.With().Str("method", "Sign").Str("path", e.agt.path).Logger()
		// Compare public keys using ssh.KeysEqual for robustness
		if ssh.KeysEqual(e.pk, key) {
			signature, errSign := e.agt.Sign(key, data)
			if errSign != nil {
				logger.Error().Err(errSign).Msg("Failed to sign")
				return nil, errSign
			}
			logger.Debug().Msg("Signed")
			return signature, nil
		}
	}
	return nil, errors.New("not found for suitable signer")
}

// publicKeyToAgentMapping constructs a mapping of public keys to their respective agents.
// This method must be called with at least a read lock held on m.mu.
func (m *MuxAgent) publicKeyToAgentMapping() ([]publicKeyToAgent, error) {
	pkToAgents := []publicKeyToAgent{}
	var errCol error
	// iterate is already correctly handling the lock for reading m.Targets and m.AddTargets
	m.iterate(func(a *Agent) bool {
		var signers []ssh.Signer
		var errSigners error
		signers, errSigners = a.Signers()
		if errSigners != nil {
			log.Warn().Err(errSigners).Str("path", a.path).Msg("Failed to get signers from agent, skipping this agent for mapping")
			errCol = multierr.Append(errCol, errSigners)
			return false // Continue with other agents
		}
		for _, signer := range signers {
			pkToAgents = append(pkToAgents, publicKeyToAgent{
				pk:  signer.PublicKey(),
				agt: a,
			})
		}
		return false
	})
	if errCol != nil {
		// Return partial mapping along with errors if some agents failed
		return pkToAgents, fmt.Errorf("encountered errors while building public key to agent mapping: %w", errCol)
	}
	return pkToAgents, nil
}

// Signers implements agent.Agent
func (m *MuxAgent) Signers() ([]ssh.Signer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	signers := []ssh.Signer{}
	var errCol error
	m.iterate(func(a *Agent) bool { // iterate is now called under RLock
		logger := log.With().Str("method", "Signers").Str("path", a.path).Logger()
		var _signers []ssh.Signer
		var errSigners error
		_signers, errSigners = a.Signers()
		if errSigners != nil {
			logger.Error().Err(errSigners).Msg("Failed to get Signers")
			errCol = multierr.Append(errCol, errSigners) // Collect errors
			return true                                 // Stop if critical, or false to continue
		}
		signers = append(signers, _signers...)
		logger.Debug().Msgf("Signers() returns %d signers", len(_signers)) // Corrected log level
		return false
	})
	if errCol != nil {
		return nil, errCol
	}
	return signers, nil
}

// iterate calls the given function f for each agent in m.AddTargets and m.Targets.
// It is the caller's responsibility to ensure that m.mu is appropriately locked (read or write)
// before calling iterate, as iterate itself does not acquire locks.
func (m *MuxAgent) iterate(f func(a *Agent) bool) {
	// Create a combined list of agents to iterate over.
	// This list is created while the lock (from the calling method) is held.
	agentsToIterate := make([]*Agent, 0, len(m.AddTargets)+len(m.Targets))
	if len(m.AddTargets) > 0 { // Check length before appending
		agentsToIterate = append(agentsToIterate, m.AddTargets...)
	}
	if len(m.Targets) > 0 { // Check length before appending
		agentsToIterate = append(agentsToIterate, m.Targets...)
	}

	for _, agt := range agentsToIterate {
		if agt == nil { // Defensive check
			continue
		}
		if stop := f(agt); stop {
			return // Allow early exit if f returns true
		}
	}
}

// Add implements agent.Agent
func (m *MuxAgent) Add(key agent.AddedKey) error {
	m.mu.RLock() // RLock because we read AddTargets and SelectTargetCommand
	// If SelectTargetCommand execution was to modify MuxAgent state, it would need a Write Lock,
	// but it only selects an agent. The actual Add operation is on an external agent.
	defer m.mu.RUnlock()

	if len(m.AddTargets) == 0 { // Check under RLock
		log.Error().Msg("Failed to add a key: no add-target specified")
		return errors.New("add functionality disabled: no add-target specified")
	}

	var selectedAgent *Agent

	if len(m.AddTargets) == 1 { // Check under RLock
		selectedAgent = m.AddTargets[0]
		log.Debug().Str("path", selectedAgent.path).Msg("Selected single agent for adding key")
	} else {
		// Multiple AddTargets
		if m.SelectTargetCommand == "" { // Check under RLock
			log.Error().Msg("Multiple add-targets specified but no select-target-command configured")
			return errors.New("multiple add-targets specified but no select-target-command configured")
		}

		var targetPaths []string
		for _, agent := range m.AddTargets { // Access m.AddTargets under RLock
			targetPaths = append(targetPaths, agent.path)
		}
		targetsEnvVar := strings.Join(targetPaths, "\n")

		// Construct SSH_AGENT_MUX_KEY_INFO
		// This part does not depend on MuxAgent's mutable state directly, so RLock is fine.
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

		cmd := exec.Command(m.SelectTargetCommand) // Access m.SelectTargetCommand under RLock
		env := os.Environ()
		env = append(env, "SSH_AGENT_MUX_TARGETS="+targetsEnvVar)
		env = append(env, "SSH_AGENT_MUX_KEY_INFO="+keyInfoString)
		cmd.Env = env

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		log.Debug().Str("command", m.SelectTargetCommand).Strs("env_targets", targetPaths).Msg("Executing select-target-command")
		errCmd := cmd.Run()
		if errCmd != nil {
			log.Error().Err(errCmd).Str("command", m.SelectTargetCommand).Str("stderr", stderr.String()).Msg("Failed to execute select-target-command")
			return fmt.Errorf("failed to execute select-target-command '%s': %w. Stderr: %s", m.SelectTargetCommand, errCmd, stderr.String())
		}

		selectedTargetPath := strings.TrimSpace(stdout.String())
		if selectedTargetPath == "" {
			log.Error().Str("command", m.SelectTargetCommand).Msg("select-target-command returned empty output")
			return errors.New("select-target-command returned empty output")
		}

		log.Debug().Str("command", m.SelectTargetCommand).Str("selected_path_raw", stdout.String()).Str("selected_path_trimmed", selectedTargetPath).Msg("select-target-command output")

		for _, agent := range m.AddTargets { // Access m.AddTargets under RLock
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
	// The actual Add operation on the selectedAgent is external to MuxAgent's state.
	errAdd := selectedAgent.Add(key)
	if errAdd != nil {
		logger.Error().Err(errAdd).Msg("Failed to add a key")
		return errAdd
	}

	logger.Debug().Msg("Added key")
	return nil
}

// Remove implements agent.Agent
func (m *MuxAgent) Remove(key ssh.PublicKey) error {
	m.mu.RLock() // RLock for publicKeyToAgentMapping which reads agent lists
	defer m.mu.RUnlock()

	mapping, errMap := m.publicKeyToAgentMapping()
	if errMap != nil {
		// Even if mapping is partial, try to use it.
		log.Warn().Err(errMap).Msg("Error building public key to agent mapping during Remove, proceeding with potentially partial map.")
		// Depending on desired strictness, one might return errMap here.
	}

	for _, e := range mapping {
		logger := log.With().Str("method", "Remove").Str("path", e.agt.path).Logger()
		if ssh.KeysEqual(e.pk, key) {
			// The Remove operation on selectedAgent is external.
			errRemove := e.agt.Remove(key)
			if errRemove != nil {
				logger.Error().Err(errRemove).Msg("Failed to remove a key")
				return errRemove
			}
			logger.Debug().Msg("Removed a key")
			return nil // Key found and remove attempted (successfully or not)
		}
	}
	log.Warn().Str("method", "Remove").Msg("Not found a key to remove. Ignored")
	// This means the key was not found among any of the agents' listed keys.
	// This is not necessarily an error from MuxAgent's perspective.
	return nil // Or return an error like "key not found" if that's preferred.
}

// RemoveAll implements agent.Agent
func (m *MuxAgent) RemoveAll() error {
	m.mu.RLock() // RLock because iterate reads agent lists
	defer m.mu.RUnlock()

	var errCol error
	m.iterate(func(a *Agent) bool { // iterate is now called under RLock
		logger := log.With().Str("method", "RemoveAll").Str("path", a.path).Logger()
		err := a.RemoveAll()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to remove all keys for this agent. Ignored, trying others.")
			errCol = multierr.Append(errCol, err)
		} else {
			logger.Debug().Msg("Removed all keys for this agent")
		}
		return false // Continue with other agents
	})
	return errCol // Return collected errors, or nil if all succeeded
}

// Extension implements agent.ExtendedAgent.
func (m *MuxAgent) Extension(extensionType string, contents []byte) ([]byte, error) {
	m.mu.RLock() // RLock because iterate reads agent lists
	defer m.mu.RUnlock()

	var finalResp []byte
	var errs error
	var success bool

	m.iterate(func(a *Agent) bool { // iterate is now called under RLock
		logger := log.With().Str("method", "Extension").Str("path", a.path).Str("type", extensionType).Logger()
		resp, err := a.Extension(extensionType, contents)
		if err != nil {
			if errors.Is(err, agent.ErrExtensionUnsupported) {
				logger.Debug().Msg("Extension unsupported by this agent, trying next.")
				return false // Continue to next agent
			}
			logger.Warn().Err(err).Msg("Failed to run extension on this agent. Trying next.")
			errs = multierr.Append(errs, fmt.Errorf("extension '%s' failed on agent %s: %w", extensionType, a.path, err))
			return false // Continue to next agent
		}
		// Extension supported and successfully executed by this agent.
		logger.Debug().Msg("Extension successful on this agent.")
		finalResp = resp
		success = true
		return true // Stop iteration, we found an agent that handled it.
	})

	if success {
		return finalResp, nil
	}
	if errs != nil {
		// If no agent succeeded, and we collected errors (other than unsupported)
		return nil, fmt.Errorf("extension '%s' failed on all agents or was unsupported: %w", extensionType, errs)
	}
	// If no agent succeeded and no errors (e.g., all unsupported, or no agents)
	return nil, agent.ErrExtensionUnsupported // Standard error if no agent supports it.
}

// SignWithFlags implements agent.ExtendedAgent.
func (m *MuxAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	m.mu.RLock() // RLock for publicKeyToAgentMapping
	defer m.mu.RUnlock()

	mapping, errMap := m.publicKeyToAgentMapping()
	if errMap != nil {
		log.Warn().Err(errMap).Msg("Error building public key to agent mapping during SignWithFlags, proceeding with potentially partial map.")
		// Depending on desired strictness, one might return errMap here.
	}

	for _, e := range mapping {
		logger := log.With().Str("method", "SignWithFlags").Str("path", e.agt.path).Logger()
		if ssh.KeysEqual(e.pk, key) {
			signature, errSign := e.agt.SignWithFlags(key, data, flags)
			if errSign != nil {
				// Check if the error is because the agent doesn't support this extension
				if errors.Is(errSign, agent.ErrExtensionUnsupported) {
					logger.Debug().Err(errSign).Msg("Agent does not support SignWithFlags, trying next.")
					continue // Try next agent if this one doesn't support the extension
				}
				logger.Error().Err(errSign).Msg("Failed to sign with flags")
				return nil, errSign
			}
			logger.Debug().Msg("Signed with flags")
			return signature, nil
		}
	}
	// If loop completes, no suitable signer found or all supporting agents failed
	// If at least one agent was tried and returned ErrExtensionUnsupported, and others didn't match the key,
	// it's tricky to decide what to return. Standard agent behavior is often to return an error from the *last*
	// agent tried if it matched the key but failed, or a generic "no signer" if no key matched.
	// For simplicity, if we exit the loop, it means no agent successfully signed.
	// If errMap was the only error, mapping would be empty, and this loop wouldn't run.
	return nil, errors.New("no suitable signer found or SignWithFlags unsupported by matching agent")
}
