# ssh-agent-multiplexer

This is a small program which multiplexes running ssh agents.

## Install

### Homebrew

```console
brew tap everpeace/tap
brew install ssh-agent-multiplexer
```

### Binary

go to [Releases](https://github.com/everpeace/ssh-agent-multiplexer/releases) page and download the tarballs.

### Build from Source

```console
git clone https://github.com/everpeace/ssh-agent-multiplexer.git
cd ssh-agent-multiplexer.git

# Download dev tools to ./.dev
make setup

# Build: binaries will be built in dist/
make
```

## Quickstart

_This quickstart is available only when installed via Homebrew._

1.  Edit the default [config file](#configuration-file)
    *   for Mac: `~/Library/Application Support/ssh-agent-multiplexer/config.toml`
    *   for Linux: `~/.config/ssh-agent-multiplexer/config.toml`

    You can use [environment variables](#environment-variable-expansion) like `${SSH_AUTH_SOCK}` directly in string values. For example, to use your current SSH agent as one of the agents the multiplexer can forward to, and have the multiplexer listen on a new, dedicated socket:

    ```toml
    # Example for ~/Library/Application Support/ssh-agent-multiplexer/config.toml
    # or ~/.config/ssh-agent-multiplexer/config.toml

    # Let the multiplexer listen on a new socket, perhaps in your home directory for clarity
    listen = "${HOME}/mux.sock"

    # Add your existing SSH agent as a target for adding keys by expanding SSH_AUTH_SOCK
    add_targets = ["${SSH_AUTH_SOCK}"]

    # You can also add other read-only targets if needed
    # targets = ["/path/to/another/readonly-agent.sock"]
    ```

2.  Start ssh-agent-multiplexer service via homebrew services
    ```console
    # logs will be at "$HOMEBREW_PREFIX/var/log/ssh-agent-multiplexer.log"
    brew services start ssh-agent-multiplexer
    ```
3.  Set `SSH_AUTH_SOCK` to the `listen` path you defined in your config file (e.g., the value of `listen` in the example above).
    ```console
    export SSH_AUTH_SOCK="${HOME}/mux.sock" # Or whatever you set for 'listen' in your config
    ```
    Now, `ssh-add -l` should show keys from your original agent (forwarded through the multiplexer), and new keys added via `ssh-add` will go to that original agent.

Note: ssh-agent-multiplexer supports dynamic configuration reloading. You don't need to restart your service when you updated the config file. Please see [Dynamic Configuration Reloading](#dynamic-configuration-reloading) section for details.

## `ssh-agent-multiplexer` CLI

You can run `ssh-agent-multiplexer` on-demand. If you would like to
- aggregate two agents `/path/to/work-agent.sock` and `/path/to/personal-agent.sock`
- use `/path/to/work-agent.sock` for target agent for adding keys (via `ssh-add`), 

just run like this:

```shell
# Note: 
#   --target: for agents which are read-only
#   --add-target: for agents which can support adding keys via ssh-add
$ ssh-agents-multiplexer --add-target /path/to/work-agent.sock --target /path/to/personal-agent.sock --listen=/path/to/agent.sock
2022-09-28T23:06:59+09:00 INF revision=4b48f3b version=0.0.2
2022-09-28T23:06:59+09:00 INF Agent multiplexer listening listen=/path/to/agent.sock
```

Then, an multiplexer agent is listening at `/path/to/agent.sock/agent.sock`.  So, You can use the socket as an usual ssh agent.

```shell
$ export SSH_AUTH_SOCK=/path/to/agent.sock/agent.sock

# this shows all the keys in target agents (both --target and --add-target)
$ ssh-add -l

# this will add <private_key> to an agent specified by --add-target 
$ ssh-add <private_key>

# this removes <public_key> from a target keeping it
$ ssh-add -d <public_key>

# forward the multiplexing agent to some.host.  Thus, you can use all the key in target agents
$ ssh -A some.host
```

## Advanced Key Management with `ssh-add`

`ssh-agent-multiplexer` provides flexible options for managing keys with `ssh-add` when working with multiple SSH agents.

### Multiple `--add-target` agents

The `--add-target` flag can be specified multiple times. This allows you to proxy `ssh-add` operations (like adding a new key) to a selection of different target agents.

Example:

```shell
ssh-agent-multiplexer \
  --add-target /path/to/work-agent.sock \
  --add-target /path/to/personal-agent.sock \
  --target /path/to/agent.sock \
  # other flags...
```

In this scenario, when you run `ssh-add <your_key>`, `ssh-agent-multiplexer` needs to know which of the `--add-target` agents (`work-agent.sock` or `personal-agent.sock`) should receive the new key. This is where `--select-target-command` comes in.

### Selecting the target agent with `--select-target-command`

When more than one `--add-target` is specified, the `--select-target-command` flag can be specified ('`ssh-agent-mux-select`' is the default value. See the next section.). This flag specifies an external command or script that `ssh-agent-multiplexer` will execute each time an `ssh-add` operation (that adds a key) is invoked. The purpose of this command is to allow you (the user) to choose which of the specified `--add-target` agents will be used for the operation.

The `ssh-agent-multiplexer` will set the following environment variables when executing this command:

*   `SSH_AGENT_MUX_TARGETS`: A newline-separated list of the socket paths for all agents specified via `--add-target`.
*   `SSH_AGENT_MUX_KEY_INFO`: A string providing details about the key being added. This helps the selection script/tool display more context to the user. The string is semicolon-separated, containing key-value pairs.
    *   Format: `KEY1=value1;KEY2=value2;...`
    *   Potential pairs:
        *   `COMMENT`: The comment associated with the key (e.g., `COMMENT=user@host`). This will be `COMMENT=` if the original comment is empty.
        *   `TYPE`: The type of the SSH public key (e.g., `TYPE=ssh-rsa`, `TYPE=ecdsa-sha2-nistp256`). This will be `TYPE=unknown` if the type cannot be determined (e.g., if the private key type is not recognized or doesn't allow public key derivation in a standard way).
        *   `FINGERPRINT_SHA256`: The SHA256 fingerprint of the public key (e.g., `FINGERPRINT_SHA256=SHA256:ABC123...`). This will be `FINGERPRINT_SHA256=unknown` if the fingerprint cannot be determined.
    *   Example: `SSH_AGENT_MUX_KEY_INFO="COMMENT=my-ssh-key;TYPE=ssh-ed25519;FINGERPRINT_SHA256=SHA256:abcdef12345..."`

The command is expected to print the path of the chosen agent socket to its standard output (stdout).

Example:

```shell
ssh-agent-multiplexer \
  --add-target /path/to/work-agent.sock \
  --add-target /path/to/personal-agent.sock \
  --select-target-command "/path/to/ssh-agent-mux-select" \
  --listen /path/to/agent.sock
```

Now, running `ssh-add <your_key>` (while `SSH_AUTH_SOCK` points to `/path/to/agent.sock`) will trigger `/path/to/ssh-agent-mux-select`, allowing you to choose between `work-agent.sock` and `personal-agent.sock`.

### The `ssh-agent-mux-select` helper tool
This project now bundles a helper tool named `ssh-agent-mux-select`. If you've installed `ssh-agent-multiplexer` (e.g., via `go install` or from a release package), this tool should be available in your path. It's designed to be a user-friendly default for the `--select-target-command` flag.

`ssh-agent-mux-select` behavior:

*   **On macOS:** It attempts to display a native GUI dialog using AppleScript.
*   **On Linux:** It tries to use `zenity` or `kdialog` (if available) to show a graphical selection list.
*   **Fallback:** If GUI methods are unavailable, fail, or are canceled, it falls back to an interactive text-based prompt in the terminal where `ssh-agent-multiplexer` is running.

You can, of course, write your own custom scripts or commands to use with `--select-target-command` if `ssh-agent-mux-select` doesn't fit your specific workflow or if you prefer a different UI/UX for selection. Your custom script just needs to read the `SSH_AGENT_MUX_TARGETS` environment variable and print the chosen agent path to stdout.

## Configuration File

`ssh-agent-multiplexer` now supports configuration via a TOML (Tom's Obvious, Minimal Language) file. This allows you to set your preferred defaults without needing to specify them as command-line flags every time.

### Specifying a Configuration File

You can specify a custom configuration file path using the `--config` (or `-c`) command-line flag:

```shell
ssh-agent-multiplexer --config /path/to/your/custom-config.toml
```

### Default Search Paths

If the `--config` or `-c` flag is *not* used, `ssh-agent-multiplexer` will look for a configuration file in the following locations. The first file found will be loaded. The search order varies by operating system:

**On macOS:**

1.  `./.ssh-agent-multiplexer.toml` (in the current working directory)
2.  `~/Library/Application Support/ssh-agent-multiplexer/config.toml` (Standard macOS user configuration path)
3.  `~/.config/ssh-agent-multiplexer/config.toml` (Fallback user configuration path on macOS)

**On other systems (e.g., Linux):**

1.  `./.ssh-agent-multiplexer.toml` (in the current working directory)
2.  Path based on `os.UserConfigDir()` (e.g., `~/.config/ssh-agent-multiplexer/config.toml` if `XDG_CONFIG_HOME` is not set, or `$XDG_CONFIG_HOME/ssh-agent-multiplexer/config.toml` if it is). `os.UserConfigDir()` respects `$XDG_CONFIG_HOME` on Linux.

The first file found in these ordered lists will be loaded.

### Precedence of Configuration Sources

Configuration values are determined with the following precedence (highest to lowest):

1.  **Command-line arguments:** Flags passed directly when running the program (e.g., `--debug`, `--listen /tmp/my.sock`).
2.  **Configuration file values:** Settings defined in the loaded TOML configuration file.
3.  **Built-in default values:** Default values for options if not specified by flags or a config file (e.g., `select-target-command` defaults to `ssh-agent-mux-select`).

### Environment Variable Expansion

String values within the configuration file for options like `listen`, `targets`, `add_targets`, and `select_target_command` can utilize environment variables. This allows for more dynamic and user-specific configurations.

The application supports expansion for variables using the `${VAR}` or `$VAR` syntax. `os.ExpandEnv` is used for the substitution, which means:
- `${VAR}` is replaced by the value of the environment variable `VAR`.
- `$VAR` is also replaced by the value of `VAR`.
- If a variable is not set, it's replaced with an empty string. For example, if `UNDEFINED_VAR` is not set, `"${UNDEFINED_VAR}/path"` becomes `"/path"`.

**Example:**

```toml
# In your config.toml
listen = "${HOME}/my-agent.sock"  # Expands HOME to your home directory
targets = ["/var/run/another-agent.sock"]
add_targets = ["${SSH_AUTH_SOCK}"] # Expands to your current SSH_AUTH_SOCK
select_target_command = "my_script --path ${MY_CUSTOM_PATH}"
```

If `HOME` is `/Users/me`, `SSH_AUTH_SOCK` is `/tmp/ssh-XXXXXX/agent.123`, and `MY_CUSTOM_PATH` is `/opt/custom`, the above would effectively become:

```toml
listen = "/Users/me/my-agent.sock"
targets = ["/var/run/another-agent.sock"]
add_targets = ["/tmp/ssh-XXXXXX/agent.123"]
select_target_command = "my_script --path /opt/custom"
```

### Example Configuration File

Here is an example of a TOML configuration file (`.ssh-agent-multiplexer.toml` or `config.toml`) showing all available options:

```toml
# Example ssh-agent-multiplexer configuration file

# Enable debug logging
debug = false

# Socket path for the multiplexer to listen on.
# If commented out or not set, a path is auto-generated in the system's temporary directory.
# listen = "/path/to/your/mux.sock"

# Agents to proxy for read-only operations (equivalent to --target or -t flag).
targets = [
  # "/path/to/example/readonly-agent1.sock",
  # "/path/to/example/readonly-agent2.sock"
]

# Agents that can handle adding keys via ssh-add (equivalent to --add-target or -a flag).
# The TOML key is "add_targets".
add_targets = [
  # "/path/to/example/writable-agent1.sock",
  # Consider using your existing agent socket directly via environment variable expansion:
  # "${SSH_AUTH_SOCK}" # This will be expanded to the value of your SSH_AUTH_SOCK env var
]

# Command to use for selecting an agent when multiple add_targets are specified.
# (equivalent to --select-target-command flag). The TOML key is "select_target_command".
# select_target_command = "ssh-agent-mux-select" # This is the built-in default
```

The keys in the TOML file generally map to their respective command-line flags. For flags that can be specified multiple times (like `--target` and `--add-target`), these are represented as arrays in the TOML file.

### Dynamic Configuration Reloading

`ssh-agent-multiplexer` supports dynamic reloading of its configuration file. If the configuration file that was loaded at startup is modified, the application will detect the changes and attempt to reload the configuration automatically.

The following configuration settings can be dynamically changed and will be applied upon a successful reload:

*   `targets`: The list of read-only agent sockets.
*   `add_targets`: The list of agent sockets that can handle key additions.
*   `select_target_command`: The command used to select an agent when multiple `add_targets` are specified.
*   `debug`: The verbosity level of logging.

**Important:** The `listen` address (the socket path where `ssh-agent-multiplexer` listens) **cannot** be changed dynamically. If you modify the `listen` setting in the configuration file, a full restart of the `ssh-agent-multiplexer` application is required for this change to take effect.

## Release

The release process is fully automated by [tagpr](https://github.com/Songmu/tagpr). To release, just merge [the latest release PR](https://github.com/everpeace/ssh-agent-multiplexer/pulls?q=is:pr+is:open+label:tagpr).

## License

Apache License, Version 2.0.  
See [LICENSE](LICENSE).
