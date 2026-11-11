// The server serves the catalog over TCP with net/rpc.
//
//	RPC_PASSWORD=secret go run ./cmd/server -addr 127.0.0.1:1234
//
// Without RPC_PASSWORD, the server makes a random password and prints it.
package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
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
	user := flag.String("user", "admin", "user name of the login")
	flag.Parse()

	password := os.Getenv("RPC_PASSWORD")
	if password == "" {
		password = rand.Text()
		fmt.Printf("RPC_PASSWORD is not set. The password of %s for this run is: %s\n", *user, password)
	}

	key, err := keys.Generate()
	if err != nil {
		log.Fatal(err)
	}
	server := &service.Server{
		Catalog: catalog.New(), Key: key, User: *user, Password: password,
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
