.PHONY: build test demo clean

build:
	go build -o s08-streamable-http .

test:
	go test ./...

# The demo runs the server *and* an in-process client (-demo flag),
# then exits. No background process to clean up.
demo: build
	./s08-streamable-http -addr 127.0.0.1:8087 -demo

clean:
	rm -f s08-streamable-http
