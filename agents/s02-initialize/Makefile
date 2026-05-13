.PHONY: build test demo clean

build:
	go build -o s02-initialize .

test:
	go test ./...

demo: build
	@{ \
	  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}'; \
	  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'; \
	  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}'; \
	} | ./s02-initialize

clean:
	rm -f s02-initialize
