# Dockerfile
FROM gcr.io/distroless/static-debian11
COPY ssh-agent-multiplexer 	/usr/bin/ssh-agent-multiplexer
COPY ssh-agent-mux-select 	/usr/bin/ssh-agent-mux-select
ENTRYPOINT ["/usr/bin/ssh-agent-multiplexer"]
