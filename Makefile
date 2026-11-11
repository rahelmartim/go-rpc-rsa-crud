SITE := site
PORT ?= 8080

.PHONY: test web serve server client clean

## test: vet and run the Go tests with the race detector
test:
	go vet ./...
	go test -race ./...

## web: build The RPC Gazette (WebAssembly) into site/
web:
	rm -rf $(SITE) && mkdir -p $(SITE)
	cp web/index.html web/style.css web/app.js $(SITE)/
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" $(SITE)/
	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o $(SITE)/gazette.wasm ./cmd/gazette-wasm

## serve: build the page and serve it on http://localhost:$(PORT)
serve: web
	python3 -m http.server $(PORT) --directory $(SITE)

## server: run the TCP server on 127.0.0.1:1234
server:
	go run ./cmd/server

## client: run the command-line client
client:
	go run ./cmd/client

clean:
	rm -rf $(SITE)
