.PHONY: build test demo clean

build:
	go build -o s03-tools .

test:
	go test ./...

# demo: drive a 4-message conversation through the server:
#   1) initialize       → InitializeResult
#   2) notifications/initialized (no reply)
#   3) tools/list       → ListToolsResult{tools: [echo, get_time]}
#   4) tools/call echo  → CallToolResult{content: [text "hi"]}
demo: build
	@printf '%s\n%s\n%s\n%s\n' \
	  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}' \
	  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
	  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}' \
	  | ./s03-tools

clean:
	rm -f s03-tools
