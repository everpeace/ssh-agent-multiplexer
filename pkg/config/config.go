// Package config defines the application's configuration structure.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	// "path/filepath" // No longer needed directly in LoadViperConfig if xdg handles all user paths
	// "runtime" // No longer needed as xdg handles OS-specifics

	"github.com/adrg/xdg"
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
	// No need to set v.SetConfigType("toml") if SetConfigFile includes the extension.

	// Handle configFilePathOverride
	if configFilePathOverride != "" {
		v.SetConfigFile(configFilePathOverride)
		err := v.ReadInConfig()
		if err != nil {
			// Any error is fatal if a specific config file is provided.
			return v, "", fmt.Errorf("failed to read specified config file %s: %w", configFilePathOverride, err)
		}
		return v, v.ConfigFileUsed(), nil
	}

	// Try Local Directory (./.ssh-agent-multiplexer.toml)
	localConfigPath := "./.ssh-agent-multiplexer.toml"
	v.SetConfigFile(localConfigPath)
	err := v.ReadInConfig()
	if err == nil {
		return v, v.ConfigFileUsed(), nil
	}
	// Check if the error is a "file not found" type.
	var configFileNotFoundError viper.ConfigFileNotFoundError
	var pathError *fs.PathError
	if !(errors.As(err, &configFileNotFoundError) || (errors.As(err, &pathError) && errors.Is(pathError.Err, fs.ErrNotExist))) {
		// This is not a "file not found" error, so it's a significant error (e.g., parse error).
		return v, "", fmt.Errorf("error reading local config %s: %w", localConfigPath, err)
	}
	// If it was a "file not found" error, proceed to check XDG paths.

	// Try XDG Paths
	xdgConfigFilePath, xdgErr := xdg.SearchConfigFile("ssh-agent-multiplexer/config.toml")
	if xdgErr == nil { // File found by XDG library
		v.SetConfigFile(xdgConfigFilePath)
		err = v.ReadInConfig() // Re-assign err
		if err == nil {
			return v, v.ConfigFileUsed(), nil
		}
		// If ReadInConfig fails (e.g. parse error), this is a hard error for an existing file.
		return v, "", fmt.Errorf("error reading XDG config file %s: %w", xdgConfigFilePath, err)
	} else if !errors.Is(xdgErr, xdg.ErrNotFound) {
		// An error occurred with xdg.SearchConfigFile itself, other than not finding a file.
		// This could be a permission issue or other critical setup problem with XDG directories.
		// For now, as per instruction, we are treating this as non-fatal for loading if it's not ErrNotFound,
		// but logging or returning might be more robust in a real application.
		// The instructions said "errors other than ErrNotFound from SearchConfigFile are rare and proceed."
		// This means if xdgErr is NOT ErrNotFound, we might have an issue.
		// Let's re-evaluate: if xdgErr is not nil AND not ErrNotFound, it's an unexpected XDG error.
		// The prompt is a bit ambiguous: "assume errors other than ErrNotFound from SearchConfigFile are rare and proceed."
		// This implies we *don't* return an error for these rare XDG setup issues and just act like no file was found.
		// This seems to align with "proceed as if only ErrNotFound is ignorable" if "proceed" means "continue trying other paths"
		// or in this case, "fall through to no config found".
		// So, if xdgErr is not nil AND not xdg.ErrNotFound, we simply proceed (treat as if no file found via XDG).
		// No specific error returned here for xdg internal errors other than file not found.
	}
	// If xdgErr is xdg.ErrNotFound, or another error we chose to ignore from xdg.SearchConfigFile, proceed.

	// No Config File Found through any method
	return v, "", nil
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
