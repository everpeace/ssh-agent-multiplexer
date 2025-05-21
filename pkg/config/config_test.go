package config_test

import (
	"io" // For discarding output
	"os"
	"path/filepath"
	"reflect"
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
		err := os.RemoveAll(filepath.Dir(tmpfn)) // remove the whole directory created for the test
		if err != nil {
			// Log the error but don't fail the test at cleanup time, as the primary test assertions are more important.
			t.Logf("Warning: failed to clean up temp config directory %s: %v", filepath.Dir(tmpfn), err)
		}
	}
	return tmpfn, cleanup
}

type configTestCase struct {
	name                 string
	args                 []string
	configContent        string // Content for the primary temporary config file
	configFileRelPath    string // Relative path for the config file (e.g., ".ssh-agent-multiplexer.toml" or "custom/myconf.toml")
	useCustomConfigDir   bool   // If true, configFileRelPath is inside a custom (temporary) user config dir
	expectedConfig       config.AppConfig
	expectLoadError      bool   // True if LoadViperConfig is expected to error
	expectedLoadErrorMsg string // Substring for LoadViperConfig error
	expectParseError     bool   // True if fs.Parse is expected to error
	preTestHook          func(t *testing.T, workingDir string, userConfigDir string)
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
			configFileRelPath: "custom_config.toml",
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "/tmp/custom.sock",
				Targets:             []string{"/target/from/config.sock"},
				AddTargets:          []string{"/add/from/config.sock"},
				SelectTargetCommand: "custom_select_cmd",
				ConfigFilePathUsed:  "custom_config.toml",
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
			name: "config file via --config flag, malformed TOML",
			args: []string{"--config", "malformed.toml"},
			configContent: `
debug = true
listen = "/tmp/malformed.sock"
targets = ["/target/from/config.sock" # Missing closing quote
`,
			configFileRelPath:    "malformed.toml",
			expectLoadError:      true,
			expectedLoadErrorMsg: "failed to read specified config file",
		},
		{
			name: "config file from default location (.ssh-agent-multiplexer.toml)",
			args: []string{},
			configContent: `
debug = true
listen = "/tmp/default_local.sock"
targets = ["/target/default_local.sock"]
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml",
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "/tmp/default_local.sock",
				Targets:             []string{"/target/default_local.sock"},
				AddTargets:          []string{},
				SelectTargetCommand: "ssh-agent-mux-select",
				ConfigFilePathUsed:  ".ssh-agent-multiplexer.toml",
			},
		},
		{
			name: "config file from user config dir (~/.config/ssh-agent-multiplexer/config.toml)",
			args: []string{},
			configContent: `
debug = false # Explicitly false
listen = "/tmp/user_config.sock"
add_targets = ["/add/user_config.sock"]
`,
			configFileRelPath:  "config.toml",
			useCustomConfigDir: true,
			expectedConfig: config.AppConfig{
				Debug:               false,
				Listen:              "/tmp/user_config.sock",
				Targets:             []string{},
				AddTargets:          []string{"/add/user_config.sock"},
				SelectTargetCommand: "ssh-agent-mux-select",
				ConfigFilePathUsed:  "config.toml",
			},
		},
		{
			name: "precedence: pflag default < config file",
			args: []string{},
			configContent: `
debug = true 
listen = "/tmp/config_listen.sock" 
select_target_command = "config_cmd_prec"
targets = ["/cfg_t1.sock"]
add_targets = ["/cfg_at1.sock"]
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml",
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "/tmp/config_listen.sock",
				Targets:             []string{"/cfg_t1.sock"},
				AddTargets:          []string{"/cfg_at1.sock"},
				SelectTargetCommand: "config_cmd_prec",
				ConfigFilePathUsed:  ".ssh-agent-multiplexer.toml",
			},
		},
		{
			name: "precedence: config file < command-line flag",
			args: []string{"--debug=false", "--listen", "/tmp/flag_listen.sock", "--target", "/flag/target.sock", "--add-target", "/flag/add.sock", "--select-target-command", "flag_cmd"},
			configContent: `
debug = true
listen = "/tmp/config_listen.sock"
targets = ["/config/target.sock"]
add_targets = ["/config/add.sock"]
select_target_command = "config_cmd"
`,
			configFileRelPath: ".ssh-agent-multiplexer.toml",
			expectedConfig: config.AppConfig{
				Debug:               false,
				Listen:              "/tmp/flag_listen.sock",
				Targets:             []string{"/flag/target.sock"},
				AddTargets:          []string{"/flag/add.sock"},
				SelectTargetCommand: "flag_cmd",
				ConfigFilePathUsed:  ".ssh-agent-multiplexer.toml",
			},
		},
		{
			name: "precedence: pflag default < command-line flag (no config)",
			args: []string{"--debug=true", "--listen=/tmp/flag_only.sock", "-t", "/t1.sock", "-t", "/t2.sock", "-a", "/at1.sock", "--select-target-command", "flag_select_only"},
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "/tmp/flag_only.sock",
				Targets:             []string{"/t1.sock", "/t2.sock"},
				AddTargets:          []string{"/at1.sock"},
				SelectTargetCommand: "flag_select_only",
				ConfigFilePathUsed:  "",
			},
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
			expectedConfig: config.AppConfig{
				Debug:               false,
				Listen:              "",
				Targets:             []string{},
				AddTargets:          []string{},
				SelectTargetCommand: "",
				ConfigFilePathUsed:  ".ssh-agent-multiplexer.toml",
			},
		},
		{
			name: "user config dir has precedence over local .ssh-agent-multiplexer.toml if --config not used",
			args: []string{},
			preTestHook: func(t *testing.T, workingDir string, userConfigDir string) {
				_, cleanupUser := createTempConfigFile(t, userConfigDir, "config.toml", `
debug = true
listen = "user_config_dir_wins"
`)
				t.Cleanup(cleanupUser)

				_, cleanupLocal := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = false
listen = "local_dir_should_be_ignored"
`)
				t.Cleanup(cleanupLocal)
			},
			useCustomConfigDir: true,
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "user_config_dir_wins",
				Targets:             []string{},
				AddTargets:          []string{},
				SelectTargetCommand: "ssh-agent-mux-select",
				ConfigFilePathUsed:  filepath.Join("mocked_user_home", ".config", "ssh-agent-multiplexer", "config.toml"),
			},
		},
		{
			name: "local .ssh-agent-multiplexer.toml is used if no user config and no --config flag",
			args: []string{},
			preTestHook: func(t *testing.T, workingDir string, userConfigDir string) {
				_, cleanupLocal := createTempConfigFile(t, workingDir, ".ssh-agent-multiplexer.toml", `
debug = true
listen = "local_dir_wins_now"
`)
				t.Cleanup(cleanupLocal)
			},
			expectedConfig: config.AppConfig{
				Debug:               true,
				Listen:              "local_dir_wins_now",
				Targets:             []string{},
				AddTargets:          []string{},
				SelectTargetCommand: "ssh-agent-mux-select",
				ConfigFilePathUsed:  ".ssh-agent-multiplexer.toml",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempUserHomeDir, err := os.MkdirTemp("", "userhome")
			require.NoError(t, err)
			defer func() { _ = os.RemoveAll(tempUserHomeDir) }()
			originalHome := os.Getenv("HOME")
			err = os.Setenv("HOME", tempUserHomeDir)
			require.NoError(t, err)
			if originalHome == "" {
				//nolint:errcheck
				defer os.Unsetenv("HOME")
			} else {
				//nolint:errcheck
				defer os.Setenv("HOME", originalHome)
			}

			testWorkingDir, err := os.MkdirTemp("", "testworkdir")
			require.NoError(t, err)
			defer func() { _ = os.RemoveAll(testWorkingDir) }()

			userConfigDirPath := filepath.Join(tempUserHomeDir, ".config", "ssh-agent-multiplexer")

			if tc.preTestHook != nil {
				tc.preTestHook(t, testWorkingDir, userConfigDirPath)
			}

			var configFileArgForLoad string
			var expectedConfigPathInAppCfg string
			var cleanupTempFile func()

			if tc.configContent != "" {
				var createdConfigPath string
				if tc.useCustomConfigDir {
					createdConfigPath, cleanupTempFile = createTempConfigFile(t, userConfigDirPath, tc.configFileRelPath, tc.configContent)
					expectedConfigPathInAppCfg = createdConfigPath
				} else if len(tc.args) > 0 && tc.args[0] == "--config" {
					createdConfigPath, cleanupTempFile = createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
					configFileArgForLoad = createdConfigPath
					expectedConfigPathInAppCfg = createdConfigPath
				} else {
					createdConfigPath, cleanupTempFile = createTempConfigFile(t, testWorkingDir, tc.configFileRelPath, tc.configContent)
					expectedConfigPathInAppCfg = createdConfigPath
				}
				if cleanupTempFile != nil {
					defer cleanupTempFile()
				}
			} else if len(tc.args) > 0 && tc.args[0] == "--config" {
				configFileArgForLoad = filepath.Join(testWorkingDir, tc.configFileRelPath)
			}

			if tc.expectedConfig.ConfigFilePathUsed != "" && !filepath.IsAbs(tc.expectedConfig.ConfigFilePathUsed) {
				if expectedConfigPathInAppCfg != "" {
					tc.expectedConfig.ConfigFilePathUsed = expectedConfigPathInAppCfg
				} else if tc.useCustomConfigDir {
					tc.expectedConfig.ConfigFilePathUsed = filepath.Join(userConfigDirPath, tc.configFileRelPath)
				}
			}

			originalWD, err := os.Getwd()
			require.NoError(t, err)
			err = os.Chdir(testWorkingDir)
			require.NoError(t, err)
			//nolint:errcheck
			defer os.Chdir(originalWD)

			if tc.expectedConfig.ConfigFilePathUsed == ".ssh-agent-multiplexer.toml" {
				tc.expectedConfig.ConfigFilePathUsed = filepath.Join(testWorkingDir, ".ssh-agent-multiplexer.toml")
			}

			v, actualLoadedPath, err := config.LoadViperConfig(configFileArgForLoad)
			if tc.expectLoadError {
				require.Error(t, err, "Expected LoadViperConfig to error")
				if tc.expectedLoadErrorMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedLoadErrorMsg, "LoadViperConfig error message mismatch")
				}
				return
			}
			require.NoError(t, err, "LoadViperConfig failed unexpectedly: %v", err)
			require.NotNil(t, v, "Viper instance should not be nil after LoadViperConfig")

			if configFileArgForLoad == "" && actualLoadedPath != "" {
				tc.expectedConfig.ConfigFilePathUsed = actualLoadedPath
			}

			fs := pflag.NewFlagSet("testflags", pflag.ContinueOnError)
			fs.SetOutput(io.Discard)

			err = config.DefineAndBindFlags(v, fs)
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

			appCfg := config.GetAppConfig(v, actualLoadedPath)
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
			assert.Equal(t, tc.expectedConfig.ConfigFilePathUsed, appCfg.ConfigFilePathUsed, "Mismatch for 'ConfigFilePathUsed'")

			if tc.postTestHook != nil {
				tc.postTestHook(t)
			}
		})
	}
}
