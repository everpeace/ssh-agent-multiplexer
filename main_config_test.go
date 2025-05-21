package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
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
		err := os.RemoveAll(filepath.Dir(tmpfn)) // remove the whole directory created for the test
		if err != nil {
			// Log the error but don't fail the test at cleanup time, as the primary test assertions are more important.
			t.Logf("Warning: failed to clean up temp config directory %s: %v", filepath.Dir(tmpfn), err)
		}
	}
	return tmpfn, cleanup
}

type configTestCase struct {
	name                string
	args                []string
	configContent       string    // Content for the primary temporary config file
	configFileRelPath   string    // Relative path for the config file (e.g., ".ssh-agent-multiplexer.toml" or "custom/myconf.toml")
	useCustomConfigDir  bool      // If true, configFileRelPath is inside a custom (temporary) user config dir
	expectedDebug       bool
	expectedListen      string
	expectedTargets     []string
	expectedAddTargets  []string
	expectedSelectCmd   string
	expectError         bool
	expectedErrorMsg    string // Substring to check in error message
	preTestHook         func(t *testing.T, workingDir string, userConfigDir string) // For setup like creating default config files
	postTestHook        func(t *testing.T)                // For cleanup like unsetting env vars
}

func TestSetupConfiguration(t *testing.T) {
	originalUserHomeDir := os.Getenv("HOME")
	defer os.Setenv("HOME", originalUserHomeDir)

	testCases := []configTestCase{
		{
			name:              "no config file, no flags",
			args:              []string{},
			expectedDebug:     false, // pflag default
			expectedListen:    "",    // pflag default
			expectedTargets:   nil,   // pflag default
			expectedAddTargets:nil,   // pflag default
			expectedSelectCmd: "ssh-agent-mux-select", // pflag default
			expectError:       false,
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
			expectedDebug:     true,
			expectedListen:    "/tmp/custom.sock",
			expectedTargets:   []string{"/target/from/config.sock"},
			expectedAddTargets:[]string{"/add/from/config.sock"},
			expectedSelectCmd: "custom_select_cmd",
			expectError:       false,
		},
		{
			name:              "config file via --config flag, non-existent file",
			args:              []string{"--config", "non_existent.toml"},
			configFileRelPath: "non_existent.toml", // This path is used in args, but file is not created
			expectError:       true,
			expectedErrorMsg:  "specified config file not found",
		},
		{
			name: "config file via --config flag, malformed TOML",
			args: []string{"--config", "malformed.toml"},
			configContent: `
debug = true
listen = "/tmp/malformed.sock"
targets = ["/target/from/config.sock" # Missing closing quote
`,
			configFileRelPath: "malformed.toml",
			expectError:       true,
			expectedErrorMsg:  "failed to read specified config file",
		},
		{
			name: "config file from default location (.ssh-agent-multiplexer.toml)",
			args: []string{},
			configContent: `
debug = true
listen = "/tmp/default_local.sock"
targets = ["/target/default_local.sock"]
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml", // This will be created in current working dir for the test
			expectedDebug:     true,
			expectedListen:    "/tmp/default_local.sock",
			expectedTargets:   []string{"/target/default_local.sock"},
			expectedAddTargets:nil,
			expectedSelectCmd: "ssh-agent-mux-select", // pflag default as not in config
			expectError:       false,
		},
		{
			name: "config file from user config dir (~/.config/ssh-agent-multiplexer/config.toml)",
			args: []string{},
			configContent: `
debug = false # Explicitly false
listen = "/tmp/user_config.sock"
add_targets = ["/add/user_config.sock"]
`,
			configFileRelPath:  "config.toml", // This will be placed in mocked user config dir
			useCustomConfigDir: true,
			expectedDebug:      false,
			expectedListen:     "/tmp/user_config.sock",
			expectedTargets:    nil,
			expectedAddTargets: []string{"/add/user_config.sock"},
			expectedSelectCmd:  "ssh-agent-mux-select",
			expectError:        false,
		},
		{
			name: "precedence: pflag default < config file",
			args: []string{},
			configContent: `
debug = true # Default is false
listen = "/tmp/config_listen.sock" # Default is ""
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml",
			expectedDebug:     true,
			expectedListen:    "/tmp/config_listen.sock",
			expectError:       false,
		},
		{
			name: "precedence: config file < command-line flag",
			args: []string{"--debug=false", "--listen", "/tmp/flag_listen.sock", "--target", "/flag/target.sock"},
			configContent: `
debug = true
listen = "/tmp/config_listen.sock"
targets = ["/config/target.sock"]
add_targets = ["/config/add.sock"]
select_target_command = "config_cmd"
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml", // Will be created in working dir
			expectedDebug:     false, // Flag overrides config
			expectedListen:    "/tmp/flag_listen.sock", // Flag overrides config
			expectedTargets:   []string{"/flag/target.sock"}, // Flag overrides config
			expectedAddTargets:[]string{"/config/add.sock"}, // From config (flag not set)
			expectedSelectCmd: "config_cmd", // From config (flag not set)
			expectError:       false,
		},
		{
			name: "precedence: pflag default < command-line flag (no config)",
			args: []string{"--debug=true", "--listen=/tmp/flag_only.sock", "-t", "/t1.sock", "-t", "/t2.sock"},
			expectedDebug:     true,
			expectedListen:    "/tmp/flag_only.sock",
			expectedTargets:   []string{"/t1.sock", "/t2.sock"},
			expectedAddTargets:nil,
			expectedSelectCmd: "ssh-agent-mux-select",
			expectError:       false,
		},
		{
			name: "empty values from config",
			args: []string{},
			configContent: `
listen = "" 
targets = []
add_targets = []
select_target_command = ""
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml",
			expectedDebug:     false,
			expectedListen:    "",
			expectedTargets:   []string{},
			expectedAddTargets:[]string{},
			expectedSelectCmd: "",
			expectError:       false,
		},
		{
            name: "user config dir takes precedence over local if --config not used",
            args: []string{}, // No --config flag
            preTestHook: func(t *testing.T, workingDir string, userConfigDir string) {
                // Create a config in the user config dir
                _, cleanupUser := createTempConfigFile(t, userConfigDir, "config.toml", `
debug = true
listen = "user_config_dir_wins"
`)
                t.Cleanup(cleanupUser) // Ensure user config is cleaned up

                // Create a config in the local working dir (which should be ignored)
                _, cleanupLocal := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = false
listen = "local_dir_should_be_ignored"
`)
                t.Cleanup(cleanupLocal) // Ensure local config is cleaned up
            },
            useCustomConfigDir: true, // This ensures setupConfiguration looks in userConfigDir
            expectedDebug:      true,
            expectedListen:     "user_config_dir_wins",
            expectError:        false,
        },
		{
            name: "local .ssh-agent-multiplexer.toml is used if no user config dir and no --config",
            args: []string{}, // No --config flag
            preTestHook: func(t *testing.T, workingDir string, userConfigDir string) {
                // Do NOT create a user config dir file for this test.
                // Create a config in the local working dir
                _, cleanupLocal := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = true
listen = "local_dir_wins_now"
`)
                t.Cleanup(cleanupLocal)
            },
            // useCustomConfigDir is false, os.UserHomeDir will point to a temp clean dir
            // where no user config exists, so local should be picked up.
            expectedDebug:  true,
            expectedListen: "local_dir_wins_now",
            expectError:    false,
        },
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			currentDir, err := os.Getwd()
			require.NoError(t, err)

			// Create a temporary directory to simulate the user's home for config
			tempUserHomeDir, err := os.MkdirTemp("", "userhome")
			require.NoError(t, err)
			defer os.RemoveAll(tempUserHomeDir)
			
			// Set HOME env var to our temporary home directory for consistent user config path resolution
			// This is important for testing ~/.config/ssh-agent-multiplexer/config.toml
			err = os.Setenv("HOME", tempUserHomeDir)
			require.NoError(t, err)


			var cleanupConfigFunc func()
			var finalArgs []string = tc.args

			// Base working directory for the test execution (where .ssh-agent-multiplexer.toml might be)
			testWorkingDir, err := os.MkdirTemp("", "testworkdir")
			require.NoError(t, err)
			defer os.RemoveAll(testWorkingDir)
			
			// Path for user config: tempUserHomeDir/.config/ssh-agent-multiplexer
			userConfigDirPath := filepath.Join(tempUserHomeDir, ".config", "ssh-agent-multiplexer")

			if tc.preTestHook != nil {
				// Pass the correct directories to the preTestHook
				tc.preTestHook(t, testWorkingDir, userConfigDirPath)
			}

			if tc.configContent != "" {
				var configFilePath string
				if tc.useCustomConfigDir { // Create config in the mocked user config dir
					configFilePath, cleanupConfigFunc = createTempConfigFile(t, userConfigDirPath, tc.configFileRelPath, tc.configContent)
				} else if strings.HasPrefix(tc.args[0], "--config") { // Config specified by flag, create it relative to testWorkingDir
					configFilePath, cleanupConfigFunc = createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
					// Adjust args to point to the absolute path of the temp config file
					newArgs := make([]string, len(tc.args))
					copy(newArgs, tc.args)
					newArgs[1] = configFilePath // Assuming --config is the first arg and path is the second
					finalArgs = newArgs
				} else { // Default local config (.ssh-agent-multiplexer.toml)
					configFilePath, cleanupConfigFunc = createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
				}
				if cleanupConfigFunc != nil {
					defer cleanupConfigFunc()
				}
			}
			
			// Change current working directory to testWorkingDir for the duration of this test case
			// This is to correctly test the scenario where .ssh-agent-multiplexer.toml is in current dir
			originalWD, err := os.Getwd()
			require.NoError(t, err)
			err = os.Chdir(testWorkingDir)
			require.NoError(t, err)
			defer os.Chdir(originalWD)


			v, _, _, err := setupConfiguration(finalArgs)

			if tc.expectError {
				require.Error(t, err, "Expected an error but got none")
				if tc.expectedErrorMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedErrorMsg, "Error message mismatch")
				}
			} else {
				require.NoError(t, err, "Expected no error but got: %v", err)
				require.NotNil(t, v, "Viper instance should not be nil")

				assert.Equal(t, tc.expectedDebug, v.GetBool("debug"), "Mismatch for 'debug'")
				assert.Equal(t, tc.expectedListen, v.GetString("listen"), "Mismatch for 'listen'")
				
				// For slices, nil and empty slice are different in Viper/Pflag.
				// Pflag defaults to nil, but config can make it an empty slice.
				// Test for semantic equality.
				expectedTargets := tc.expectedTargets
				if expectedTargets == nil { expectedTargets = []string{} } // Treat nil expectation as empty for comparison if Viper returns empty
				actualTargets := v.GetStringSlice("targets")
				if actualTargets == nil { actualTargets = []string{} }
				assert.True(t, reflect.DeepEqual(expectedTargets, actualTargets), "Mismatch for 'targets'. Expected %v, got %v", expectedTargets, actualTargets)


				expectedAddTargets := tc.expectedAddTargets
				if expectedAddTargets == nil { expectedAddTargets = []string{} }
				actualAddTargets := v.GetStringSlice("add_targets")
				if actualAddTargets == nil { actualAddTargets = []string{} }
				assert.True(t, reflect.DeepEqual(expectedAddTargets, actualAddTargets), "Mismatch for 'add_targets'. Expected %v, got %v", expectedAddTargets, actualAddTargets)
				
				assert.Equal(t, tc.expectedSelectCmd, v.GetString("select_target_command"), "Mismatch for 'select_target_command'")
			}
			if tc.postTestHook != nil {
				tc.postTestHook(t)
			}
		})
	}
}

// TODO: Add a specific test for help and version flags if setupConfiguration is expected to handle them
// in a way that main can use (e.g. by returning specific error types or parsed flag values).
// The current setupConfiguration returns the *bool for help/version, so main directly uses them.
// This test suite focuses on viper config values.
// Test for pflag.ErrHelp if --help is passed.

func TestSetupConfiguration_HelpFlag(t *testing.T) {
    // Reset pflag for this test if it uses global pflag.CommandLine
    // However, setupConfiguration now uses its own FlagSet, so this is less of an issue.
    
    _, help, version, err := setupConfiguration([]string{"--help"})
    
    // pflag.ContinueOnError makes Parse return pflag.ErrHelp when --help is encountered
    assert.ErrorIs(t, err, pflag.ErrHelp, "Expected pflag.ErrHelp")
    assert.NotNil(t, help, "help flag pointer should be returned")
    if help != nil {
        assert.True(t, *help, "--help flag should be true")
    }
    assert.NotNil(t, version, "version flag pointer should be returned")
    if version != nil {
        assert.False(t, *version, "--version flag should be false")
    }
}

func TestSetupConfiguration_VersionFlag(t *testing.T) {
    _, help, version, err := setupConfiguration([]string{"--version"})

    assert.ErrorIs(t, err, pflag.ErrHelp, "Expected pflag.ErrHelp for --version with ContinueOnError") // pflag treats --version like --help with ContinueOnError
    assert.NotNil(t, version, "version flag pointer should be returned")
    if version != nil {
        assert.True(t, *version, "--version flag should be true")
    }
    assert.NotNil(t, help, "help flag pointer should be returned")
    if help != nil {
        assert.False(t, *help, "--help flag should be false")
    }
}
