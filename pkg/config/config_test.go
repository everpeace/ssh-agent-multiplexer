// Licensed to Shingo Omura under one or more agreements.
// Shingo Omura licenses this file to you under the Apache 2.0 License.
// See the LICENSE file in the project root for more information.

package config_test

import (
	"io" // For discarding output
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/everpeace/ssh-agent-multiplexer/pkg/config"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a temporary config file.
// It returns the path to the created file and a cleanup function.
func createTempConfigFile(t *testing.T, dir string, filename string, content string) (string, func()) {
	t.Helper()
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "configtest")
		require.NoError(t, err, "Failed to create temp dir")
	} else {
		err := os.MkdirAll(dir, 0755)
		require.NoError(t, err, "Failed to create specified dir")
	}

	tmpfn := filepath.Join(dir, filename)
	err := os.WriteFile(tmpfn, []byte(content), 0666)
	require.NoError(t, err, "Failed to write temp config file")

	cleanup := func() {
		err := os.RemoveAll(filepath.Dir(tmpfn))
		if err != nil {
			t.Logf("Warning: failed to clean up temp config directory %s: %v", filepath.Dir(tmpfn), err)
		}
	}
	return tmpfn, cleanup
}

type configTestCase struct {
	name                 string
	args                 []string
	configContent        string
	configFileRelPath    string
	useCustomConfigDir   bool // True if configContent/configFileRelPath refers to a user-specific dir (standard or macOS fallback)
	expectedConfig       config.AppConfig
	expectLoadError      bool
	expectedLoadErrorMsg string
	expectParseError     bool
	preTestHook          func(t *testing.T, workingDir string, appSpecificUserStdConfigDir string, tempUserHomeDir string)
	postTestHook         func(t *testing.T)
}

func TestAppConfiguration(t *testing.T) {
	originalUserHomeDir := os.Getenv("HOME")
	//nolint:errcheck
	defer os.Setenv("HOME", originalUserHomeDir)

	testCases := []configTestCase{
		{
			name: "no config file, no flags",
			args: []string{},
			preTestHook: func(t *testing.T, workingDir string, appSpecificUserStdConfigDir string, tempUserHomeDir string) {
				// Ensure no config file exists
				if err := os.Remove(filepath.Join(workingDir, ".ssh-agent-multiplexer.toml")); err != nil {
					t.Logf("Warning: failed to remove file: %v", err)
				}
				if err := os.Remove(filepath.Join(appSpecificUserStdConfigDir, "config.toml")); err != nil {
					t.Logf("Warning: failed to remove file: %v", err)
				}
				if err := os.Remove(filepath.Join(tempUserHomeDir, ".config", "ssh-agent-multiplexer", "config.toml")); err != nil {
					t.Logf("Warning: failed to remove file: %v", err)
				}
			},
			expectedConfig: config.AppConfig{
				Debug:               false,
				Listen:              "",
				Targets:             []string{},
				AddTargets:          []string{},
				SelectTargetCommand: "ssh-agent-mux-select",
				ConfigFilePathUsed:  "",
			},
		},
		{
			name: "environment variable expansion in config",
			args: []string{"--config", "env_expansion_config.toml"}, // Use --config to ensure this specific file is loaded
			configContent: `
debug = false
listen = "${TEST_LISTEN_PATH}/socket.sock"
targets = ["${TEST_TARGET_PATH_1}/agent.sock", "/absolute/path/agent.sock", "${NOT_SET_VAR}/path", "$ONLY_DOLLAR/path"]
add_targets = ["${TEST_ADD_TARGET_PATH}/add.sock"]
select_target_command = "echo ${TEST_SELECT_CMD_ARG}"
`,
			configFileRelPath: "env_expansion_config.toml",
			preTestHook: func(t *testing.T, workingDir string, appSpecificUserStdConfigDir string, tempUserHomeDir string) {
				t.Setenv("TEST_LISTEN_PATH", "/tmp/expanded_listen")
				t.Setenv("TEST_TARGET_PATH_1", "/tmp/expanded_target1")
				t.Setenv("ONLY_DOLLAR", "/tmp/expand_target_only_dollar")
				t.Setenv("TEST_ADD_TARGET_PATH", "/tmp/expanded_add_target")
				t.Setenv("TEST_SELECT_CMD_ARG", "expanded_arg")
				// NOT_SET_VAR is intentionally not set to test fallback to empty string
			},
			expectedConfig: config.AppConfig{
				Debug:               false,
				Listen:              "/tmp/expanded_listen/socket.sock",
				Targets:             []string{"/tmp/expanded_target1/agent.sock", "/absolute/path/agent.sock", "/path", "/tmp/expand_target_only_dollar/path"},
				AddTargets:          []string{"/tmp/expanded_add_target/add.sock"},
				SelectTargetCommand: "echo expanded_arg",
				// ConfigFilePathUsed will be updated by the test
			},
		},
		{
			name: "config file via --config flag, valid TOML",
			args: []string{"--config", "custom_config.toml"},
			configContent: `
debug = true
listen = "/tmp/custom.sock"
targets = ["/target/from/config.sock"]
add_targets = ["/add/from/config.sock"]
select_target_command = "custom_select_cmd"
`,
			configFileRelPath: "custom_config.toml",
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "/tmp/custom.sock",
				Targets:             []string{"/target/from/config.sock"},
				AddTargets:          []string{"/add/from/config.sock"},
				SelectTargetCommand: "custom_select_cmd",
				// ConfigFilePathUsed will be updated by the test
			},
		},
		{
			name:                 "config file via --config flag, non-existent file",
			args:                 []string{"--config", "non_existent.toml"},
			configFileRelPath:    "non_existent.toml",
			expectLoadError:      true,
			expectedLoadErrorMsg: "specified config file does not exist",
		},
		{
			name: "config file from default local location (.ssh-agent-multiplexer.toml)",
			args: []string{},
			configContent: `
debug = true
listen = "/tmp/default_local.sock"
targets = ["/target/default_local.sock"]
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml",
			preTestHook: func(t *testing.T, workingDir string, appSpecificUserStdConfigDir string, tempUserHomeDir string) {
				// Create the default local config file
				_, cleanup := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = true
listen = "/tmp/default_local.sock"
targets = ["/target/default_local.sock"]
`)
				t.Cleanup(cleanup)
			},
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "/tmp/default_local.sock",
				Targets:             []string{"/target/default_local.sock"},
				AddTargets:          []string{},
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
		{
			name: "config_file_from_user_config_dir (standard)",
			args: []string{},
			configContent: `
debug = false
listen = "/tmp/user_config_standard.sock"
add_targets = ["/add/user_config_standard.sock"]
`,
			configFileRelPath:  "config.toml",
			useCustomConfigDir: true,
			expectedConfig: config.AppConfig{
				Debug:               false,
				Listen:              "/tmp/user_config_standard.sock",
				Targets:             []string{},
				AddTargets:          []string{"/add/user_config_standard.sock"},
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
		{
			name: "local_directory_config_takes_precedence_over_user_config_dir",
			args: []string{},
			preTestHook: func(t *testing.T, workingDir string, appSpecificUserStdConfigDir string, tempUserHomeDir string) {
				// Local file (should win)
				_, cleanupLocal := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = true
listen = "local_wins"
targets = ["/local/target.sock"]
`)
				t.Cleanup(cleanupLocal)
				// Standard user config dir file (should be ignored)
				_, cleanupUser := createTempConfigFile(t, appSpecificUserStdConfigDir, "config.toml", `
debug = false
listen = "user_config_should_be_ignored"
targets = ["/user/target.sock"]
`)
				t.Cleanup(cleanupUser)
			},
			useCustomConfigDir: true, // To ensure appSpecificUserStdConfigDir is calculated and used by preHook
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "local_wins",
				Targets:             []string{"/local/target.sock"},
				AddTargets:          []string{},
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
		{
			name: "macos_library_over_dot_config_fallback",
			args: []string{},
			preTestHook: func(t *testing.T, workingDir string, appSpecificUserStdConfigDir string, tempUserHomeDir string) {
				if runtime.GOOS != "darwin" {
					t.Skip("Skipping macOS specific test on non-darwin platform")
				}
				// Standard macOS user config path (e.g., ~/Library/Application Support/ssh-agent-multiplexer/config.toml)
				// appSpecificUserStdConfigDir is [MockedHOME]/Library/Application Support/ssh-agent-multiplexer on macOS
				_, cleanupStdUser := createTempConfigFile(t, appSpecificUserStdConfigDir, "config.toml", `
debug = true
listen = "library_wins_on_macos"
`)
				t.Cleanup(cleanupStdUser)

				// macOS .config fallback path (e.g., ~/.config/ssh-agent-multiplexer/config.toml)
				dotConfigPath := filepath.Join(tempUserHomeDir, ".config", "ssh-agent-multiplexer")
				_, cleanupDotConfigUser := createTempConfigFile(t, dotConfigPath, "config.toml", `
debug = false
listen = "dot_config_should_be_ignored_on_macos_if_library_exists"
`)
				t.Cleanup(cleanupDotConfigUser)
			},
			useCustomConfigDir: true,
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "library_wins_on_macos",
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
		{
			name: "macos_dot_config_fallback_loads",
			args: []string{},
			preTestHook: func(t *testing.T, workingDir string, appSpecificUserStdConfigDir string, tempUserHomeDir string) {
				if runtime.GOOS != "darwin" {
					t.Skip("Skipping macOS specific test on non-darwin platform")
				}
				// No local file.
				// No standard user config file (appSpecificUserStdConfigDir will be empty or non-existent for LoadViperConfig).

				// macOS .config fallback path (e.g., ~/.config/ssh-agent-multiplexer/config.toml)
				dotConfigPath := filepath.Join(tempUserHomeDir, ".config", "ssh-agent-multiplexer")
				_, cleanupDotConfigUser := createTempConfigFile(t, dotConfigPath, "config.toml", `
debug = true
listen = "dot_config_wins_on_macos_as_fallback"
`)
				t.Cleanup(cleanupDotConfigUser)
			},
			useCustomConfigDir: true,
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "dot_config_wins_on_macos_as_fallback",
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempUserHomeDir, err := os.MkdirTemp("", "userhome_")
			require.NoError(t, err)
			defer func() { _ = os.RemoveAll(tempUserHomeDir) }()

			err = os.Setenv("HOME", tempUserHomeDir)
			require.NoError(t, err)
			// No XDG_CONFIG_HOME manipulation needed for reverted logic.

			testWorkingDir, err := os.MkdirTemp("", "testworkdir_")
			require.NoError(t, err)
			defer func() { _ = os.RemoveAll(testWorkingDir) }()

			// This is the directory derived from os.UserConfigDir() using the mocked HOME
			mockedStdUserConfigDirBase, err := os.UserConfigDir()
			require.NoError(t, err, "os.UserConfigDir() failed during test setup")
			appSpecificUserStdConfigDir := filepath.Join(mockedStdUserConfigDirBase, "ssh-agent-multiplexer")

			if tc.preTestHook != nil {
				// Pass tempUserHomeDir for macOS ~/.config fallback creation if needed
				tc.preTestHook(t, testWorkingDir, appSpecificUserStdConfigDir, tempUserHomeDir)
			}

			var configFileArgForLoad string
			var expectedConfigPathForAssertion string

			if len(tc.args) > 0 && (tc.args[0] == "--config" || tc.args[0] == "-c") {
				// This test case uses the --config flag.
				// tc.configFileRelPath is the filename for the --config file.
				// tc.configContent determines if the file should actually be created.
				if tc.configContent != "" || tc.name == "config file via --config flag, valid TOML" { // Second condition for clarity on existing test
					// If content is provided, or it's the test that expects a valid (even if empty) file, create it.
					absPath, cleanup := createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
					defer cleanup()
					configFileArgForLoad = absPath
					expectedConfigPathForAssertion = absPath
				} else {
					// No content, so this is for testing a non-existent --config file.
					// Construct the path but don't create the file.
					configFileArgForLoad = filepath.Join(testWorkingDir, tc.configFileRelPath)
					expectedConfigPathForAssertion = "" // No file should be loaded
				}
			} else if tc.configContent != "" {
				// For default path tests (not using --config flag) where content is provided.
				if tc.useCustomConfigDir {
					// This branch is for user-specific directories (standard or macOS fallback)
					// The preTestHook is responsible for creating files in these specific locations.
					// We rely on actualLoadedPath to be the source of truth for expectedConfigPathForAssertion.
					// If configContent is provided here, it implies a generic user config test.
					// The macOS specific tests create their files entirely within preTestHook.
					if tc.name == "config_file_from_user_config_dir (standard)" {
						absPath, cleanup := createTempConfigFile(t, appSpecificUserStdConfigDir, tc.configFileRelPath, tc.configContent)
						defer cleanup()
						expectedConfigPathForAssertion = absPath
					}
					// For macOS tests, preTestHook handles file creation.
				} else {
					// Local .ssh-agent-multiplexer.toml
					absPath, cleanup := createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
					defer cleanup()
					expectedConfigPathForAssertion = absPath
				}
			}

			originalWD, err := os.Getwd()
			require.NoError(t, err)
			err = os.Chdir(testWorkingDir)
			require.NoError(t, err)
			//nolint:errcheck
			defer os.Chdir(originalWD)

			actualLoadedPath, errLoad := config.LoadViperConfig(configFileArgForLoad)

			if tc.expectLoadError {
				require.Error(t, errLoad, "Expected LoadViperConfig to error")
				if tc.expectedLoadErrorMsg != "" {
					assert.Contains(t, errLoad.Error(), tc.expectedLoadErrorMsg, "LoadViperConfig error message mismatch")
				}
				return
			}
			require.NoError(t, errLoad, "LoadViperConfig failed unexpectedly: %v", errLoad)

			if configFileArgForLoad == "" { // A default path was used or no file found
				expectedConfigPathForAssertion = actualLoadedPath // actualLoadedPath is the source of truth
			}
			// If configFileArgForLoad is not empty, expectedConfigPathForAssertion was set when creating the --config file.

			fs := pflag.NewFlagSet("testflags", pflag.ContinueOnError)
			fs.SetOutput(io.Discard)

			err = config.DefineAndBindFlags(fs)
			require.NoError(t, err, "DefineAndBindFlags failed: %v")

			pflagArgs := tc.args
			if configFileArgForLoad != "" {
				tempArgs := []string{}
				for i := 0; i < len(tc.args); i++ {
					if tc.args[i] == "--config" || tc.args[i] == "-c" {
						i++
						continue
					}
					tempArgs = append(tempArgs, tc.args[i])
				}
				pflagArgs = tempArgs
			}

			err = fs.Parse(pflagArgs)
			if tc.expectParseError {
				require.Error(t, err, "Expected fs.Parse to error")
				return
			}
			require.NoError(t, err, "fs.Parse failed: %v", err)

			appCfg := config.GetAppConfig(actualLoadedPath)
			require.NotNil(t, appCfg, "GetAppConfig returned nil")

			assert.Equal(t, tc.expectedConfig.Debug, appCfg.Debug, "Mismatch for 'Debug'")
			assert.Equal(t, tc.expectedConfig.Listen, appCfg.Listen, "Mismatch for 'Listen'")

			expectedTargets := tc.expectedConfig.Targets
			if expectedTargets == nil {
				expectedTargets = []string{}
			}
			actualTargets := appCfg.Targets
			if actualTargets == nil {
				actualTargets = []string{}
			}
			assert.True(t, reflect.DeepEqual(expectedTargets, actualTargets), "Mismatch for 'Targets'. Expected %v, got %v", expectedTargets, actualTargets)

			expectedAddTargets := tc.expectedConfig.AddTargets
			if expectedAddTargets == nil {
				expectedAddTargets = []string{}
			}
			actualAddTargets := appCfg.AddTargets
			if actualAddTargets == nil {
				actualAddTargets = []string{}
			}
			assert.True(t, reflect.DeepEqual(expectedAddTargets, actualAddTargets), "Mismatch for 'AddTargets'. Expected %v, got %v", expectedAddTargets, actualAddTargets)

			assert.Equal(t, tc.expectedConfig.SelectTargetCommand, appCfg.SelectTargetCommand, "Mismatch for 'SelectTargetCommand'")
			assert.Equal(t, expectedConfigPathForAssertion, appCfg.ConfigFilePathUsed, "Mismatch for 'ConfigFilePathUsed' in test %s", tc.name)

			if tc.postTestHook != nil {
				tc.postTestHook(t)
			}

			err = os.Setenv("HOME", originalUserHomeDir)
			require.NoError(t, err)
			// No XDG_CONFIG_HOME to restore as it wasn't set globally by the test loop.
		})
	}
}
