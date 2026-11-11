package client_test

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/client"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
	"github.com/rahelmartim/go-rpc-rsa-crud/service"
)

func newClient(t *testing.T) *client.Client {
	t.Helper()
	serverKey, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	s := &service.Server{Catalog: catalog.New(), Key: serverKey}
	near, far := net.Pipe()
	go s.ServeConn(far)
	c := client.New(near, clientKey)
	t.Cleanup(func() { c.Close() })
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCommands(t *testing.T) {
	c := newClient(t)
	steps := []struct{ line, out, err string }{
		{"", "", ""},
		{"list", "", "commands start with a dot"},
		{".dance", "", ".dance is not a command"},
		{".login admin secret", "", ".login is not a command"},
		{".LIST", "the catalog is empty", ""},
		{".create rice 4.50", "created #1  rice  4.50", ""},
		{".create coffee 12,9", "created #2  coffee  12.90", ""},
		{".create tea", "", "usage: .create <name> <price>"},
		{".create tea free", "", "a price is a number"},
		{".read 1", "#1  rice  4.50", ""},
		{".read one", "", "the id is a whole number"},
		{".read 0", "", "the id is a whole number"},
		{".read 9", "", "no product has this id"},
		{".update 1 brown_rice 5", "updated #1  brown_rice  5.00", ""},
		{".update 1 x", "", "usage: .update <id> <name> <price>"},
		{".delete 2", "deleted #2", ""},
		{".delete 2", "", "no product has this id"},
		{".list", "#1  brown_rice  5.00", ""},
		{".help", ".create <name> <price>", ""},
	}
	for _, s := range steps {
		out, err := c.Exec(s.line)
		if s.err == "" && err != nil {
			t.Fatalf("%q: unexpected error %v", s.line, err)
		}
		if s.err != "" && (err == nil || !strings.Contains(err.Error(), s.err)) {
			t.Fatalf("%q: error %v, want %q", s.line, err, s.err)
		}
		if !strings.Contains(out, s.out) {
			t.Fatalf("%q: output %q, want %q", s.line, out, s.out)
		}
	}
	if _, err := c.Exec(".quit"); !errors.Is(err, client.ErrQuit) {
		t.Fatalf(".quit: %v", err)
	}
}

func TestSealNeedsTheServerKey(t *testing.T) {
	key, _ := keys.Generate()
	near, _ := net.Pipe()
	c := client.New(near, key)
	defer c.Close()
	if _, err := c.List(); !errors.Is(err, service.ErrNoHandshake) {
		t.Fatalf("a call before the handshake: %v", err)
	}
}

func TestTraceSeesBothSides(t *testing.T) {
	c := newClient(t)
	var calls []client.Call
	c.Trace = func(call client.Call) { calls = append(calls, call) }
	if _, err := c.Create("tea", "3.50"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("%d traced calls", len(calls))
	}
	call := calls[0]
	if call.Method != "Create" || string(call.Request) != `{"name":"tea","price":"3.50"}` {
		t.Fatalf("request %q", call.Request)
	}
	if len(call.Sealed) != 1 || len(call.Sealed[0]) != 256 || len(call.ReplySealed) != 1 {
		t.Fatal("the request or the reply is not sealed")
	}
	if !strings.Contains(string(call.Reply), `"name":"tea"`) || call.Err != nil {
		t.Fatalf("reply %q, %v", call.Reply, call.Err)
	}
}

func TestDialFails(t *testing.T) {
	key, _ := keys.Generate()
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if _, err := client.Dial(addr, key); err == nil {
		t.Fatal("Dial to a closed port worked")
	}
}
