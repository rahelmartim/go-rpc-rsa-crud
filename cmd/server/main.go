// The server serves the catalog over TCP with net/rpc. Every call after
// the handshake is sealed with RSA.
//
//	go run ./cmd/server -addr 127.0.0.1:1234
package main

import (
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
	"github.com/rahelmartim/go-rpc-rsa-crud/service"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:1234", "address to listen on")
	flag.Parse()

	key, err := keys.Generate()
	if err != nil {
		log.Fatal(err)
	}
	server := &service.Server{
		Catalog: catalog.New(), Key: key,
		Log: func(conn int, text string) { log.Printf("[conn %d] %s", conn, text) },
	}

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on %s, server key %s", l.Addr(), keys.Fingerprint(&key.PublicKey))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		l.Close()
	}()
	if err := server.Serve(l); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
	log.Print("stopped")
}
