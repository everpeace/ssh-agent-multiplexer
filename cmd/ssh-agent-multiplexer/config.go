// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/BurntSushi/toml"
	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newConfigCmd(configFlagValue string) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}

	configCmd.AddCommand(newConfigPathCmd(configFlagValue))
	configCmd.AddCommand(newConfigPrintCmd(configFlagValue))
	configCmd.AddCommand(newConfigEditCmd(configFlagValue))
	configCmd.AddCommand(newConfigPrintDefaultCmd())

	return configCmd
}

func newConfigPrintDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "print-default",
		Short: "Output default config",
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := toml.Marshal(config.DefaultAppConfig())
			if err != nil {
				return fmt.Errorf("failed to marshal default config: %w", err)
			}
			fmt.Println(string(raw))
			return nil
		},
	}
}

func newConfigPathCmd(configFlagValue string) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Output effective config path",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedPath, err := config.ResolveConfigFilePath(configFlagValue)
			if err != nil {
				return fmt.Errorf("failed to resolve config file path: %w", err)
			}
			if resolvedPath != "" {
				fmt.Println(resolvedPath)
				return nil
			}

			log.Debug().Msg("No existing config file found, using default path")
			defaultConfigPath, err := config.DefaultConfigFilePath()
			if err != nil {
				return fmt.Errorf("failed to get default config file path: %w", err)
			}
			fmt.Println(defaultConfigPath)
			return nil
		},
	}
}

func newConfigPrintCmd(configFlagValue string) *cobra.Command {
	return &cobra.Command{
		Use:     "print",
		Aliases: []string{"show"},
		Short:   "Output effective config",
		RunE: func(cmd *cobra.Command, args []string) error {
			configFilePath, err := config.LoadViperConfig(configFlagValue)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			log.Debug().Msgf("Config file used: %s", configFilePath)
			log.Debug().Msg("Effective config in TOML format:")
			if err := toml.NewEncoder(os.Stdout).Encode(viper.AllSettings()); err != nil {
				return fmt.Errorf("failed to encode config to TOML: %w", err)
			}
			return nil
		},
	}
}

func newConfigEditCmd(configFlagValue string) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open config in editor",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedPath, err := config.ResolveConfigFilePath(configFlagValue)
			if err != nil {
				return fmt.Errorf("failed to resolve config file path: %w", err)
			}
			if resolvedPath == "" {
				log.Debug().Msg("No existing config file found, using default path")
				defaultConfigPath, err := config.DefaultConfigFilePath()
				if err != nil {
					return fmt.Errorf("failed to get default config file path: %w", err)
				}
				resolvedPath = defaultConfigPath
				raw, err := toml.Marshal(config.DefaultAppConfig())
				if err != nil {
					return fmt.Errorf("failed to marshal default config: %w", err)
				}
				if err := os.WriteFile(resolvedPath, raw, 0644); err != nil {
					return fmt.Errorf("failed to write default config to file: %w", err)
				}
				log.Debug().Msgf("Created new config file at: %s", resolvedPath)
			}
			log.Debug().Msgf("Config file used: %s", resolvedPath)

			editor := os.Getenv("EDITOR")
			if editor == "" {
				log.Debug().Msgf("EDITOR variable is not set. Using 'vi'")
				editor = "vi"
			}

			commandStrs := []string{editor, resolvedPath}
			log.Debug().Strs("command", commandStrs).Msgf("Executing")

			command := exec.Command(commandStrs[0], commandStrs[1])
			command.Stdin = os.Stdin
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			if err := command.Run(); err != nil {
				fmt.Println("Error opening editor:", err)
				return err
			}
			return nil
		},
	}
}
