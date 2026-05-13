.PHONY: build test demo clean

build:
	go build -o s05-prompts .

test:
	go test ./...

# A four-message demo: initialize, list, get, complete.
demo: build
	@printf '%s\n%s\n%s\n%s\n%s\n' \
	  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"0"}}}' \
	  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
	  '{"jsonrpc":"2.0","id":2,"method":"prompts/list","params":{}}' \
	  '{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"summarize-file","arguments":{"path":"README.md","style":"bullets"}}}' \
	  '{"jsonrpc":"2.0","id":4,"method":"completion/complete","params":{"ref":{"type":"ref/prompt","name":"summarize-file"},"argument":{"name":"path","value":"R"}}}' \
	  | ./s05-prompts

clean:
	rm -f s05-prompts
