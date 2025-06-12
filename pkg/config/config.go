// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

// Package config defines the application's configuration structure.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/rs/zerolog"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	DefaultSelectTargetCommand = "ssh-agent-mux-select"
)

var (
	_ zerolog.LogObjectMarshaler = &AppConfig{}
)

// AppConfig holds the application's configuration values,
// typically populated from command-line flags and a configuration file.
type AppConfig struct {
	// Listen is the socket path or address for the multiplexer to listen on.
	// If empty, "<directory of ConfigFilePathUsed or current dir>/agent.sock" is used.
	Listen string `toml:"listen"`

	// Targets is a list of paths to target SSH agents to proxy for read-only operations.
	Targets []string `toml:"targets"`

	// AddTargets is a list of paths to target SSH agents that can handle adding keys via ssh-add.
	AddTargets []string `toml:"add_targets"`

	// SelectTargetCommand is the command to execute to select a target agent
	// when multiple AddTargets are specified and an ssh-add operation occurs.
	SelectTargetCommand string `toml:"select_target_command"`

	// Debug enables debug logging.
	Debug bool `toml:"debug"`

	// ConfigFilePathUsed stores the path of the configuration file that was loaded.
	// This will be empty if no configuration file was used.
	ConfigFilePathUsed string `toml:"-"`
}

// MarshalZerologObject implements zerolog.LogObjectMarshaler.
func (a *AppConfig) MarshalZerologObject(e *zerolog.Event) {
	e.Str("listen", a.Listen).
		Strs("targets", a.Targets).
		Strs("addTargets", a.AddTargets).
		Str("selectTargetCommand", a.SelectTargetCommand).
		Bool("debug", a.Debug).
		Str("configFilePathUsed", a.ConfigFilePathUsed)
}

func DefaultConfigFilePath() (string, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config directory: %w", err)
	}
	return filepath.Join(userConfigDir, "ssh-agent-multiplexer", "config.toml"), nil
}

func DefaultAppConfig() *AppConfig {
	return &AppConfig{
		Listen:              "", // Default to "<config_file_dir>/agent.sock"
		Targets:             []string{},
		AddTargets:          []string{},
		SelectTargetCommand: DefaultSelectTargetCommand,
		Debug:               false,
	}
}

func ResolveConfigFilePath(configFilePathOverride string) (string, error) {
	// Handle configFilePathOverride
	if configFilePathOverride != "" {
		if _, err := os.Stat(configFilePathOverride); err == nil {
			return configFilePathOverride, nil
		}
		return "", fmt.Errorf("specified config file does not exist: %s", configFilePathOverride)
	}

	// Try Local Directory (./.ssh-agent-multiplexer.toml)
	localConfigPath := "./.ssh-agent-multiplexer.toml"
	if _, err := os.Stat(localConfigPath); err == nil {
		return localConfigPath, nil
	}

	// Try User Config Directory ([os.UserConfigDir()]/ssh-agent-multiplexer/config.toml)
	userConfigDir, ucdErr := os.UserConfigDir()
	if ucdErr == nil {
		standardUserConfigPath := filepath.Join(userConfigDir, "ssh-agent-multiplexer", "config.toml")
		if _, err := os.Stat(standardUserConfigPath); err == nil {
			return standardUserConfigPath, nil
		}
	}

	// macOS XDG-style fallback: ~/.config/ssh-agent-multiplexer/config.toml
	if runtime.GOOS == "darwin" {
		homeDir := os.Getenv("HOME")
		if homeDir != "" {
			macXDGConfigPath := filepath.Join(homeDir, ".config", "ssh-agent-multiplexer", "config.toml")
			if _, err := os.Stat(macXDGConfigPath); err == nil {
				return macXDGConfigPath, nil
			}
		}
	}

	return "", nil
}

func LoadViperConfig(configFilePathOverride string) (string, error) {
	configFilePath, err := ResolveConfigFilePath(configFilePathOverride)
	if err != nil {
		return "", err
	}

	if configFilePath != "" {
		viper.SetConfigFile(configFilePath)
		err = viper.ReadInConfig()
		if err != nil {
			return "", fmt.Errorf("failed to read config file %s: %w", configFilePath, err)
		}
	}

	return configFilePath, nil
}

// DefineAndBindFlags defines the application's command-line flags on the provided FlagSet
// and binds them to Viper configuration keys.
//
// Parameters:
//   - fs: The pflag.FlagSet instance on which to define the flags.
//
// Returns:
//   - error: An error if any flag binding operation fails.
func DefineAndBindFlags(fs *pflag.FlagSet) error {
	// Define application-specific flags and bind them
	var err error

	fs.BoolP("debug", "d", false, "debug mode")
	if err = viper.BindPFlag("debug", fs.Lookup("debug")); err != nil {
		return fmt.Errorf("failed to bind 'debug' flag: %w", err)
	}

	fs.StringP("listen", "l", "", `socket path to listen for the multiplexer. It listens at "<your config file dir>/agent.sock", if not set.`)
	if err = viper.BindPFlag("listen", fs.Lookup("listen")); err != nil {
		return fmt.Errorf("failed to bind 'listen' flag: %w", err)
	}

	fs.StringSliceP("target", "t", nil, "path of target agent to proxy. you can specify this option multiple times")
	if err = viper.BindPFlag("targets", fs.Lookup("target")); err != nil { // TOML key is "targets"
		return fmt.Errorf("failed to bind 'targets' flag: %w", err)
	}

	fs.StringSliceP("add-target", "a", nil, "path of target agent for ssh-add command. Can be specified multiple times.")
	if err = viper.BindPFlag("add_targets", fs.Lookup("add-target")); err != nil { // TOML key is "add_targets"
		return fmt.Errorf("failed to bind 'add_targets' flag: %w", err)
	}

	fs.String("select-target-command", DefaultSelectTargetCommand, "command to execute to select a target when multiple --add-target agents are specified.")
	if err = viper.BindPFlag("select_target_command", fs.Lookup("select-target-command")); err != nil { // TOML key is "select_target_command"
		return fmt.Errorf("failed to bind 'select_target_command' flag: %w", err)
	}

	return nil
}

// GetAppConfig populates and returns an AppConfig struct from a Viper instance.
// This function should be called *after* pflag.Parse() has been executed,
// so that Viper reflects any command-line overrides.
//
// Parameters:
//   - v: The Viper instance, which has been updated by parsed command-line flags.
//   - configFileUsedPath: The path of the configuration file that was loaded, if any.
//
// Returns:
//   - *AppConfig: The populated application configuration.
func GetAppConfig(configFileUsedPath string) *AppConfig {
	appConfig := &AppConfig{
		Debug:               viper.GetBool("debug"),
		Listen:              viper.GetString("listen"),
		Targets:             viper.GetStringSlice("targets"),
		AddTargets:          viper.GetStringSlice("add_targets"),
		SelectTargetCommand: viper.GetString("select_target_command"),
		ConfigFilePathUsed:  configFileUsedPath,
	}

	// Expand environment variables
	appConfig.Listen = os.ExpandEnv(appConfig.Listen)
	appConfig.SelectTargetCommand = os.ExpandEnv(appConfig.SelectTargetCommand)

	expandedTargets := make([]string, len(appConfig.Targets))
	for i, target := range appConfig.Targets {
		expandedTargets[i] = os.ExpandEnv(target)
	}
	appConfig.Targets = expandedTargets

	expandedAddTargets := make([]string, len(appConfig.AddTargets))
	for i, target := range appConfig.AddTargets {
		expandedAddTargets[i] = os.ExpandEnv(target)
	}
	appConfig.AddTargets = expandedAddTargets

	return appConfig
}
