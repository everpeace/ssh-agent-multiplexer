# ssh-agent-multiplexer 

This is a small program which multiplexes running ssh agents.

If you wanted to aggregate two agents, just do below. Note that you must to specify one `--add-target` so that `ssh-add <key>` can add a key to the multiplexer agent.

```shell
$ ssh-agents-multiplexer --listen multiplex.sock --add-target agent1.sock --target agent2.sock
2022-09-28T23:06:59+09:00 INF revision=0dbfb04 version=0.0.1-dev
2022-09-28T23:06:59+09:00 INF Agent multiplexer listening listen=/var/folders/2s/41bhrr7d0r76pkkb9kgjtk0h0000gn/T/ssh-agent-multiplexer-90668.sock
```

Then, an multiplexed agent is listening at `multiplex.sock`.  You can use the socket as an usual ssh agent.

```shell
$ export SSH_AUTH_SOCK=/var/folders/2s/41bhrr7d0r76pkkb9kgjtk0h0000gn/T/ssh-agent-multiplexer-90668.sock

# this shows all the keys in target agents
$ ssh-add -l

# this will add <private_key> to add-target
$ ssh-add <private_key>

# this removes <public_key> from a target keeping it
$ ssh-add -d <public_key>

# forward the multiplexing agent to some.host.  Thus, you can use all the key in target agents
$ ssh -A some.host
```
