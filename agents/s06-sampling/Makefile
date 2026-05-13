.PHONY: build test demo clean

build:
	go build -o s06-sampling .

test:
	go test ./...

# A four-message demo: initialize, initialized, tools/list, tools/call summarize.
# The summarize tool triggers a server-initiated sampling/createMessage which
# the default StubSampler answers in-process — so the demo is fully self-
# contained and does not require a real LLM.
demo: build
	@printf '%s\n%s\n%s\n%s\n' \
	  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"sampling":{}},"clientInfo":{"name":"demo","version":"0"}}}' \
	  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
	  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"summarize","arguments":{"text":"hello, sampling"}}}' \
	  | ./s06-sampling

clean:
	rm -f s06-sampling
