// The client connects to the server and reads commands, such as
// ".login admin secret" or ".create rice 4.50". Type .help for the list.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/rahelmartim/go-rpc-rsa-crud/client"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:1234", "address of the server")
	flag.Parse()

	key, err := keys.Generate()
	if err != nil {
		log.Fatal(err)
	}
	c, err := client.Dial(*addr, key)
	if err != nil {
		log.Fatalf("cannot connect to %s: %v", *addr, err)
	}
	defer c.Close()
	fmt.Printf("connected to %s\nclient key %s\nserver key %s\ntype .help for the commands\n",
		*addr, keys.Fingerprint(&key.PublicKey), keys.Fingerprint(c.ServerKey))

	in := bufio.NewScanner(os.Stdin)
	for fmt.Print("> "); in.Scan(); fmt.Print("> ") {
		out, err := c.Exec(in.Text())
		if errors.Is(err, client.ErrQuit) {
			fmt.Println(out)
			return
		}
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		if out != "" {
			fmt.Println(out)
		}
	}
}
