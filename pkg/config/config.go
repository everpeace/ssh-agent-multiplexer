// Package config defines the application's configuration structure.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// AppConfig holds the application's configuration values,
// typically populated from command-line flags and a configuration file.
type AppConfig struct {
	// Listen is the socket path or address for the multiplexer to listen on.
	// If empty, a path is auto-generated in the system's temporary directory.
	Listen string

	// Targets is a list of paths to target SSH agents to proxy for read-only operations.
	Targets []string

	// AddTargets is a list of paths to target SSH agents that can handle adding keys via ssh-add.
	AddTargets []string

	// SelectTargetCommand is the command to execute to select a target agent
	// when multiple AddTargets are specified and an ssh-add operation occurs.
	SelectTargetCommand string

	// Debug enables debug logging.
	Debug bool

	// ConfigFilePathUsed stores the path of the configuration file that was loaded.
	// This will be empty if no configuration file was used.
	ConfigFilePathUsed string
}

// LoadViperConfig initializes a new Viper instance, sets up configuration paths,
// and attempts to read a TOML configuration file.
//
// Parameters:
//   - configFilePathOverride: If not empty, this path will be used directly to load the config file.
//
// Returns:
//   - *viper.Viper: The configured Viper instance.
//   - string: The path of the configuration file that was successfully loaded. Empty if no file was loaded.
//   - error: An error if one occurred during config file loading (unless it's a 'file not found' error
//     when searching default paths, which is not treated as an error).
func LoadViperConfig(configFilePathOverride string) (*viper.Viper, string, error) {
	v := viper.New()
	v.SetConfigType("toml")

	var configFileUsed string

	if configFilePathOverride != "" {
		v.SetConfigFile(configFilePathOverride)
		if err := v.ReadInConfig(); err != nil {
			// If a specific file is given and not found, or any other read error, it's an error.
			return v, "", fmt.Errorf("failed to read specified config file %s: %w", configFilePathOverride, err)
		}
		configFileUsed = v.ConfigFileUsed()
	} else {
		// Search in default locations

		// 1. Current directory: ./.ssh-agent-multiplexer.toml
		v.AddConfigPath(".")
		v.SetConfigName(".ssh-agent-multiplexer") // Look for .ssh-agent-multiplexer.toml

		err := v.ReadInConfig()
		if err == nil {
			configFileUsed = v.ConfigFileUsed()
			return v, configFileUsed, nil // Successfully read from current dir
		}

		// If error is not "file not found", then it's a real parsing error.
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return v, "", fmt.Errorf("error reading config from ./'.ssh-agent-multiplexer.toml': %w", err)
		}

		// File not found in current dir, try user config dir.
		// Reset config name for the next search path if AddConfigPath is used with SetConfigName.
		// Alternatively, and more robustly, create a new viper instance or clear paths if necessary.
		// For this case, we will set a specific file path for the user directory to avoid ambiguity with SetConfigName.
		
		userConfigDir, err := os.UserConfigDir()
		if err == nil {
			userConfigFilePath := filepath.Join(userConfigDir, "ssh-agent-multiplexer", "config.toml")
			v.SetConfigFile(userConfigFilePath) // Explicitly set this path
			
			errRead := v.ReadInConfig()
			if errRead == nil {
				configFileUsed = v.ConfigFileUsed()
				return v, configFileUsed, nil // Successfully read from user config dir
			}
			if _, ok := errRead.(viper.ConfigFileNotFoundError); !ok {
				// If it's not a "file not found" error, then it's a real parsing error for the user config file.
				return v, "", fmt.Errorf("error reading config from user config directory %s: %w", userConfigFilePath, errRead)
			}
			// If user config file is also not found, it's okay, proceed without error.
		}
		// If os.UserConfigDir() failed or file not found in user dir, proceed silently.
		// At this point, no config file was found in default locations, which is not an error.
	}

	return v, configFileUsed, nil
}

// DefineAndBindFlags defines the application's command-line flags on the provided FlagSet
// and binds them to Viper configuration keys.
//
// Parameters:
//   - v: The Viper instance to bind flags to.
//   - fs: The pflag.FlagSet instance on which to define the flags.
//
// Returns:
//   - error: An error if any flag binding operation fails.
func DefineAndBindFlags(v *viper.Viper, fs *pflag.FlagSet) error {
	// Define standard flags (help, version, config)
	// These are not typically bound to Viper in the same way as app-specific config,
	// but they need to be defined on the FlagSet.
	fs.BoolP("version", "v", false, "Print version and exit")
	fs.BoolP("help", "h", false, "Print the help")
	fs.StringP("config", "c", "", "Path to TOML configuration file. If set, this overrides default config file paths.")

	// Define application-specific flags and bind them
	var err error

	fs.BoolP("debug", "d", false, "debug mode")
	if err = v.BindPFlag("debug", fs.Lookup("debug")); err != nil {
		return fmt.Errorf("failed to bind 'debug' flag: %w", err)
	}

	fs.StringP("listen", "l", "", "socket path to listen for the multiplexer. it is generated automatically if not set")
	if err = v.BindPFlag("listen", fs.Lookup("listen")); err != nil {
		return fmt.Errorf("failed to bind 'listen' flag: %w", err)
	}

	fs.StringSliceP("target", "t", nil, "path of target agent to proxy. you can specify this option multiple times")
	if err = v.BindPFlag("targets", fs.Lookup("target")); err != nil { // TOML key is "targets"
		return fmt.Errorf("failed to bind 'targets' flag: %w", err)
	}

	fs.StringSliceP("add-target", "a", nil, "path of target agent for ssh-add command. Can be specified multiple times.")
	if err = v.BindPFlag("add_targets", fs.Lookup("add-target")); err != nil { // TOML key is "add_targets"
		return fmt.Errorf("failed to bind 'add_targets' flag: %w", err)
	}

	fs.String("select-target-command", "ssh-agent-mux-select", "command to execute to select a target when multiple --add-target agents are specified.")
	if err = v.BindPFlag("select_target_command", fs.Lookup("select-target-command")); err != nil { // TOML key is "select_target_command"
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
func GetAppConfig(v *viper.Viper, configFileUsedPath string) *AppConfig {
	return &AppConfig{
		Debug:               v.GetBool("debug"),
		Listen:              v.GetString("listen"),
		Targets:             v.GetStringSlice("targets"),
		AddTargets:          v.GetStringSlice("add_targets"),
		SelectTargetCommand: v.GetString("select_target_command"),
		ConfigFilePathUsed:  configFileUsedPath,
	}
}
