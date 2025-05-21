# ssh-agent-multiplexer 

This is a small program which multiplexes running ssh agents.

If you would like to
- aggregate two agents `/path/to/work-agent.sock` and `/path/to/personal-agent.sock`
- use `/path/to/work-agent.sock` for target agent for adding keys (via `ssh-add`), 

just run like this:

```shell
# Note: 
#   --target: for agents which are read-only
#   --add-target: for agents which can support adding keys via ssh-add
$ ssh-agents-multiplexer --add-target /path/to/work-agent.sock --target /path/to/personal-agent.sock
2022-09-28T23:06:59+09:00 INF revision=4b48f3b version=0.0.2
2022-09-28T23:06:59+09:00 INF Agent multiplexer listening listen=/var/folders/2s/41bhrr7d0r76pkkb9kgjtk0h0000gn/T/ssh-agent-multiplexer-90668.sock
```

Then, an multiplexer agent is listening at `/var/..../ssh-agent-multiplexer-90668.sock`.  So, You can use the socket as an usual ssh agent.

```shell
$ export SSH_AUTH_SOCK=/var/folders/2s/41bhrr7d0r76pkkb9kgjtk0h0000gn/T/ssh-agent-multiplexer-90668.sock

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
  --target /path/to/another-agent.sock \
  # other flags...
```

In this scenario, when you run `ssh-add <your_key>`, `ssh-agent-multiplexer` needs to know which of the `--add-target` agents (`work-agent.sock` or `personal-agent.sock`) should receive the new key. This is where `--select-target-command` comes in.

### Selecting the target agent with `--select-target-command`

When more than one `--add-target` is specified, the `--select-target-command` flag is **required**. This flag specifies an external command or script that `ssh-agent-multiplexer` will execute each time an `ssh-add` operation (that adds a key) is invoked. The purpose of this command is to allow you (the user) to choose which of the specified `--add-target` agents will be used for the operation.

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
  --select-target-command "/usr/local/bin/ssh-agent-mux-select" \
  --listen /tmp/mux.sock
```

Now, running `ssh-add <your_key>` (while `SSH_AUTH_SOCK` points to `/tmp/mux.sock`) will trigger `/usr/local/bin/ssh-agent-mux-select`, allowing you to choose between `work-agent.sock` and `personal-agent.sock`.

### The `ssh-agent-mux-select` helper tool

This project now bundles a helper tool named `ssh-agent-mux-select`. If you've installed `ssh-agent-multiplexer` (e.g., via `go install` or from a release package), this tool should be available in your path. It's designed to be a user-friendly default for the `--select-target-command` flag.

`ssh-agent-mux-select` behavior:

*   **On macOS:** It attempts to display a native GUI dialog using AppleScript.
*   **On Linux:** It tries to use `zenity` or `kdialog` (if available) to show a graphical selection list.
*   **Fallback:** If GUI methods are unavailable, fail, or are canceled, it falls back to an interactive text-based prompt in the terminal where `ssh-agent-multiplexer` is running.

You can, of course, write your own custom scripts or commands to use with `--select-target-command` if `ssh-agent-mux-select` doesn't fit your specific workflow or if you prefer a different UI/UX for selection. Your custom script just needs to read the `SSH_AGENT_MUX_TARGETS` environment variable and print the chosen agent path to stdout.

## Release

The release process is fully automated by [tagpr](https://github.com/Songmu/tagpr). To release, just merge [the latest release PR](https://github.com/everpeace/ssh-agent-multiplexer/pulls?q=is:pr+is:open+label:tagpr).

## License

Apache License, Version 2.0.  
See [LICENSE](LICENSE).
