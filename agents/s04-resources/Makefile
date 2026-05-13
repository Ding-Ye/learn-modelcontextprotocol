.PHONY: build test demo clean

build:
	go build -o s04-resources .

test:
	go test ./...

# `make demo` pipes a four-message script through the server: initialize,
# initialized, list, read. You'll see one InitializeResult, one
# ListResourcesResult, one ReadResourceResult — and (if you wait ~100ms
# before EOF) one notifications/resources/updated frame after the demo's
# auto-mutate kicks in.
demo: build
	@printf '%s\n%s\n%s\n%s\n%s\n' \
	  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}' \
	  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	  '{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"mem://log.txt"}}' \
	  '{"jsonrpc":"2.0","id":3,"method":"resources/list"}' \
	  '{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"mem://log.txt"}}' \
	  | ./s04-resources

clean:
	rm -f s04-resources
