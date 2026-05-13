.PHONY: build test demo clean

build:
	go build -o s01-min-loop .

test:
	go test ./...

demo: build
	@echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}' | ./s01-min-loop

clean:
	rm -f s01-min-loop
