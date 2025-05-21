package config_test

import (
	"io" // For discarding output
	"os"
	"path/filepath"
	"reflect"
	"runtime" // Still needed for OS-specific path construction in tests for xdg default
	"testing"

	"github.com/adrg/xdg" // For xdg.ConfigHome in tests
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
	useCustomConfigDir   bool   // True if configContent/configFileRelPath refers to an XDG path (either XDG_CONFIG_HOME or default)
	expectedConfig       config.AppConfig
	expectLoadError      bool   
	expectedLoadErrorMsg string 
	expectParseError     bool   
	preTestHook          func(t *testing.T, workingDir string, tempUserHomeDir string, tempXDGConfigHome string)
	postTestHook         func(t *testing.T)
}

func TestAppConfiguration(t *testing.T) {
	originalUserHomeDir := os.Getenv("HOME")
	originalXDGConfigHome := os.Getenv("XDG_CONFIG_HOME")

	//nolint:errcheck
	defer os.Setenv("HOME", originalUserHomeDir)
	//nolint:errcheck
	defer os.Setenv("XDG_CONFIG_HOME", originalXDGConfigHome)


	testCases := []configTestCase{
		{
			name: "no config file, no flags",
			args: []string{},
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
			name: "config file via --config flag, valid TOML",
			args: []string{"--config", "custom_config.toml"},
			configContent: `
debug = true
listen = "/tmp/custom.sock"
targets = ["/target/from/config.sock"]
add_targets = ["/add/from/config.sock"]
select_target_command = "custom_select_cmd"
`,
			configFileRelPath: "custom_config.toml", // This will be created in testWorkingDir
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "/tmp/custom.sock",
				Targets:             []string{"/target/from/config.sock"},
				AddTargets:          []string{"/add/from/config.sock"},
				SelectTargetCommand: "custom_select_cmd",
				// ConfigFilePathUsed will be updated by the test to the absolute path
			},
		},
		{
			name:                 "config file via --config flag, non-existent file",
			args:                 []string{"--config", "non_existent.toml"},
			configFileRelPath:    "non_existent.toml",
			expectLoadError:      true,
			expectedLoadErrorMsg: "failed to read specified config file",
		},
		{
			name: "config file from default local location (.ssh-agent-multiplexer.toml)",
			args: []string{},
			configContent: `
debug = true
listen = "/tmp/default_local.sock"
targets = ["/target/default_local.sock"]
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml", // Created in testWorkingDir
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
			name: "xdg_config_home_loads",
			args: []string{},
			configContent: `
debug = true
listen = "xdg_home_wins"
`,
			configFileRelPath:  "ssh-agent-multiplexer/config.toml", // Relative to XDG_CONFIG_HOME
			useCustomConfigDir: true, // Signals this is an XDG test type
			preTestHook: func(t *testing.T, workingDir string, tempUserHomeDir string, tempXDGConfigHome string) {
				// tempXDGConfigHome is already set by the main test loop for this hook
				// Just need to create the file inside it
				// The path will be [tempXDGConfigHome]/ssh-agent-multiplexer/config.toml
				_, cleanup := createTempConfigFile(t, filepath.Join(tempXDGConfigHome, "ssh-agent-multiplexer"), "config.toml", `
debug = true
listen = "xdg_home_wins"
`)
				t.Cleanup(cleanup)
			},
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "xdg_home_wins",
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
		{
			name: "xdg_default_user_config_path_loads (no XDG_CONFIG_HOME)",
			args: []string{},
			configContent: `
debug = false
listen = "xdg_default_path_wins"
`,
			configFileRelPath:  "ssh-agent-multiplexer/config.toml", // Relative to default XDG base
			useCustomConfigDir: true, // Signals this is an XDG test type
			preTestHook: func(t *testing.T, workingDir string, tempUserHomeDir string, tempXDGConfigHome string) {
				// Ensure XDG_CONFIG_HOME is unset for this test case
				err := os.Unsetenv("XDG_CONFIG_HOME")
				require.NoError(t, err)
				// xdg.ConfigHome will now be used by the library, which respects $HOME (mocked to tempUserHomeDir)
				// The config file needs to be created in the path xdg.SearchConfigFile would find.
				// xdg.ConfigHome is the base path (e.g. ~/.config or ~/Library/Application Support)
				// We need to create [xdg.ConfigHome]/ssh-agent-multiplexer/config.toml
				
				// Construct the path where xdg.SearchConfigFile will look.
				// xdg.ConfigHome already includes tempUserHomeDir due to t.Setenv("HOME", tempUserHomeDir)
				expectedXDGDefaultDir := filepath.Join(xdg.ConfigHome, "ssh-agent-multiplexer")

				_, cleanup := createTempConfigFile(t, expectedXDGDefaultDir, "config.toml", `
debug = false
listen = "xdg_default_path_wins"
`)
				t.Cleanup(cleanup)
			},
			expectedConfig: config.AppConfig{
				Debug:               false,
				Listen:              "xdg_default_path_wins",
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
		{
			name: "local_file_over_xdg_config",
			args: []string{},
			preTestHook: func(t *testing.T, workingDir string, tempUserHomeDir string, tempXDGConfigHome string) {
				// Local file (should win)
				_, cleanupLocal := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = true
listen = "local_wins_over_xdg"
`)
				t.Cleanup(cleanupLocal)

				// XDG_CONFIG_HOME file (should be ignored)
				_, cleanupXDG := createTempConfigFile(t, filepath.Join(tempXDGConfigHome, "ssh-agent-multiplexer"), "config.toml", `
debug = false
listen = "xdg_should_be_ignored"
`)
				t.Cleanup(cleanupXDG)
			},
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "local_wins_over_xdg",
				SelectTargetCommand: "ssh-agent-mux-select",
				// ConfigFilePathUsed updated by test
			},
		},
		{
			name: "config_flag_over_xdg_and_local",
			args: []string{"--config", "override.toml"},
			configContent: ` # This content is for override.toml
debug = true
listen = "flag_override_wins"
`,
			configFileRelPath: "override.toml", // Created in testWorkingDir
			preTestHook: func(t *testing.T, workingDir string, tempUserHomeDir string, tempXDGConfigHome string) {
				// Local file (should be ignored)
				_, cleanupLocal := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = false
listen = "local_should_be_ignored_by_flag"
`)
				t.Cleanup(cleanupLocal)

				// XDG_CONFIG_HOME file (should be ignored)
				_, cleanupXDG := createTempConfigFile(t, filepath.Join(tempXDGConfigHome, "ssh-agent-multiplexer"), "config.toml", `
debug = false
listen = "xdg_should_be_ignored_by_flag"
`)
				t.Cleanup(cleanupXDG)
			},
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "flag_override_wins",
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
			
			// Mock HOME
			err = os.Setenv("HOME", tempUserHomeDir)
			require.NoError(t, err)
			
			// Mock XDG_CONFIG_HOME for relevant tests, or ensure it's clean for others
			tempXDGConfigHome, err := os.MkdirTemp("", "xdgconfighome_")
			require.NoError(t, err)
			defer func() { _ = os.RemoveAll(tempXDGConfigHome) }()
			
			if tc.name == "xdg_config_home_loads" || tc.name == "local_file_over_xdg_config" || tc.name == "config_flag_over_xdg_and_local" {
				err = os.Setenv("XDG_CONFIG_HOME", tempXDGConfigHome)
				require.NoError(t, err)
			} else {
				// For tests like "xdg_default_user_config_path_loads", ensure XDG_CONFIG_HOME is unset or empty
				// so adrg/xdg falls back to default behavior based on HOME and OS.
				err = os.Unsetenv("XDG_CONFIG_HOME")
				require.NoError(t, err)
			}
			// Crucial for xdg library to re-read env vars
			xdg.Reload()


			testWorkingDir, err := os.MkdirTemp("", "testworkdir_")
			require.NoError(t, err)
			defer func() { _ = os.RemoveAll(testWorkingDir) }()

			if tc.preTestHook != nil {
				tc.preTestHook(t, testWorkingDir, tempUserHomeDir, tempXDGConfigHome)
			}

			var configFileArgForLoad string
			var expectedConfigPathForAssertion string 

			if len(tc.args) > 0 && (tc.args[0] == "--config" || tc.args[0] == "-c") {
				// Config file is specified by flag, create it in testWorkingDir
				// tc.configFileRelPath is the name of this override file.
				absPath, cleanup := createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
				defer cleanup()
				configFileArgForLoad = absPath
				expectedConfigPathForAssertion = absPath
			} else if tc.configContent != "" && !tc.useCustomConfigDir {
				// This is for local .ssh-agent-multiplexer.toml (not XDG related)
				absPath, cleanup := createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
				defer cleanup()
				expectedConfigPathForAssertion = absPath
			}
			// For XDG tests (tc.useCustomConfigDir = true), files are created in preTestHook.
			// expectedConfigPathForAssertion will be set by actualLoadedPath later for these.


			originalWD, err := os.Getwd()
			require.NoError(t, err)
			err = os.Chdir(testWorkingDir)
			require.NoError(t, err)
			//nolint:errcheck
			defer os.Chdir(originalWD)

			v, actualLoadedPath, errLoad := config.LoadViperConfig(configFileArgForLoad)
			
			if tc.expectLoadError {
				require.Error(t, errLoad, "Expected LoadViperConfig to error")
				if tc.expectedLoadErrorMsg != "" {
					assert.Contains(t, errLoad.Error(), tc.expectedLoadErrorMsg, "LoadViperConfig error message mismatch")
				}
				return
			}
			require.NoError(t, errLoad, "LoadViperConfig failed unexpectedly: %v", errLoad)
			require.NotNil(t, v, "Viper instance should not be nil after LoadViperConfig")

			if configFileArgForLoad == "" && actualLoadedPath != "" { // A default file was loaded
				expectedConfigPathForAssertion = actualLoadedPath
			} else if configFileArgForLoad == "" && actualLoadedPath == "" { // No file loaded
				expectedConfigPathForAssertion = ""
			}
			// If configFileArgForLoad is not empty, expectedConfigPathForAssertion was set when creating it.


			fs := pflag.NewFlagSet("testflags", pflag.ContinueOnError)
			fs.SetOutput(io.Discard)

			err = config.DefineAndBindFlags(v, fs)
			require.NoError(t, err, "DefineAndBindFlags failed: %v")

			pflagArgs := tc.args
			if configFileArgForLoad != "" { // Filter out --config if it was passed to LoadViperConfig
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

			appCfg := config.GetAppConfig(v, actualLoadedPath)
			require.NotNil(t, appCfg, "GetAppConfig returned nil")

			assert.Equal(t, tc.expectedConfig.Debug, appCfg.Debug, "Mismatch for 'Debug'")
			assert.Equal(t, tc.expectedConfig.Listen, appCfg.Listen, "Mismatch for 'Listen'")

			expectedTargets := tc.expectedConfig.Targets
			if expectedTargets == nil { expectedTargets = []string{} }
			actualTargets := appCfg.Targets
			if actualTargets == nil { actualTargets = []string{} }
			assert.True(t, reflect.DeepEqual(expectedTargets, actualTargets), "Mismatch for 'Targets'. Expected %v, got %v", expectedTargets, actualTargets)

			expectedAddTargets := tc.expectedConfig.AddTargets
			if expectedAddTargets == nil { expectedAddTargets = []string{} }
			actualAddTargets := appCfg.AddTargets
			if actualAddTargets == nil { actualAddTargets = []string{} }
			assert.True(t, reflect.DeepEqual(expectedAddTargets, actualAddTargets), "Mismatch for 'AddTargets'. Expected %v, got %v", expectedAddTargets, actualAddTargets)
			
			assert.Equal(t, tc.expectedConfig.SelectTargetCommand, appCfg.SelectTargetCommand, "Mismatch for 'SelectTargetCommand'")
			assert.Equal(t, expectedConfigPathForAssertion, appCfg.ConfigFilePathUsed, "Mismatch for 'ConfigFilePathUsed' in test %s", tc.name)

			if tc.postTestHook != nil {
				tc.postTestHook(t)
			}
			
			// Restore original env vars for next test case
			err = os.Setenv("HOME", originalUserHomeDir)
			require.NoError(t, err)
			err = os.Setenv("XDG_CONFIG_HOME", originalXDGConfigHome)
			require.NoError(t, err)
			xdg.Reload()
		})
	}
}
