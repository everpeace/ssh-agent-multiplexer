# ssh-agent-multiplexer 

This is a small program which multiplexes running ssh agents.

If you wanted to aggregate two agents, just do this:

```shell
ssh-agents-multiplexer --listen multiplex.sock --targets agent1.sock --targets agent2.sock
```

Then, an multiplexed agent is listening at `multiplex.sock`.  You can use the socket as an usual ssh agent.

```shell
$ export SSH_AUTH_SOCK=multiplex.sock
$ ssh -A some.host
```
