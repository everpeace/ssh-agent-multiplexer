# ssh-agent-multiplexer 

This is a small program which multiplexes running ssh agents.

If you wanted to 
- aggregate two agents `agent1.sock` and `agent2.sock`
- use `agent1.sock` for target agent for adding keys (via `ssh-add`), 

just run like this:

```shell
# Note: `--add-target` is required.
$ ssh-agents-multiplexer --add-target agent1.sock --target agent2.sock
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

## License

Apache License, Version 2.0.  
See [LICENSE](LICENSE).
