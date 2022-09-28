# Dockerfile
FROM gcr.io/distroless/static-debian11
COPY ssh-agent-multiplexer \
	/usr/bin/ssh-agent-multiplexer
ENTRYPOINT ["/usr/bin/ssh-agent-multiplexer"]
