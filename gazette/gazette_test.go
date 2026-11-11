package gazette

import (
	"encoding/hex"
	"strings"
	"sync"
	"testing"
)

var (
	once   sync.Once
	shared *Engine
)

// engine returns one engine for all tests, after a reset. Making the keys
// is the slow part.
func engine(t *testing.T) *Engine {
	t.Helper()
	once.Do(func() {
		e, err := New()
		if err != nil {
			t.Fatal(err)
		}
		shared = e
	})
	if shared == nil {
		t.Fatal("no engine")
	}
	if _, err := shared.Reset(); err != nil {
		t.Fatal(err)
	}
	return shared
}

func exec(t *testing.T, e *Engine, line string) (Entry, State) {
	t.Helper()
	entry, state, _ := e.Exec(line)
	return entry, state
}

func TestStartsWithAHandshake(t *testing.T) {
	s := engine(t).State()
	x := s.Handshake
	if x == nil || x.Method != "Products.Handshake" || x.Error != "" {
		t.Fatalf("handshake: %+v", x)
	}
	if x.Sent < 294 || x.Received < 294 || len(x.Sealed) != 0 {
		t.Fatalf("a handshake carries two public keys, not sealed: %+v", x)
	}
	if s.ClientKnows != s.ServerKey || s.ClientKey == s.ServerKey {
		t.Fatalf("keys: %+v", s)
	}
	if len(s.Products) != 3 || s.Bits != 2048 || s.BlockSize != 190 || s.Calls != 1 {
		t.Fatalf("state: %+v", s)
	}
}

func TestACommandIsSealedBothWays(t *testing.T) {
	e := engine(t)
	entry, s := exec(t, e, ".create tea 3.50")
	if entry.Error != "" || entry.Output != "created #4  tea  3.50" || len(s.Products) != 4 {
		t.Fatalf("entry: %+v", entry)
	}
	if len(entry.Calls) != 1 {
		t.Fatalf("%d calls", len(entry.Calls))
	}
	x := entry.Calls[0]
	if x.Method != "Products.Create" || x.Request != `{"name":"tea","price":"3.50"}` {
		t.Fatalf("request: %+v", x)
	}
	if len(x.Sealed) != 1 || len(x.Sealed[0]) != 512 || len(x.ReplySealed) != 1 {
		t.Fatalf("sealed blocks: %d and %d", len(x.Sealed), len(x.ReplySealed))
	}
	if !strings.Contains(x.Reply, `"name":"tea"`) {
		t.Fatalf("reply %q", x.Reply)
	}
	// The sealed blocks are what went on the wire, and the plaintext is not.
	if !strings.Contains(x.SentHex, x.Sealed[0]) || !strings.Contains(x.RecvHex, x.ReplySealed[0]) {
		t.Fatal("the sealed blocks are not in the bytes on the wire")
	}
	for _, word := range []string{"tea", "3.50", "name"} {
		if strings.Contains(x.SentHex+x.RecvHex, hex.EncodeToString([]byte(word))) {
			t.Fatalf("%q is on the wire in plain text", word)
		}
	}
	if !strings.Contains(x.SentHex, hex.EncodeToString([]byte("Products.Create"))) {
		t.Fatal("the method name is missing from the wire")
	}
	if !strings.Contains(strings.Join(x.Server, "\n"), "created #4 tea at 3.50") {
		t.Fatalf("server lines: %q", x.Server)
	}
}

func TestErrors(t *testing.T) {
	e := engine(t)
	entry, _ := exec(t, e, ".read 99")
	if entry.Error != "no product has this id" || len(entry.Calls) != 1 || entry.Calls[0].Reply != `{"error":"no product has this id"}` {
		t.Fatalf("read 99: %+v", entry)
	}
	if len(entry.Calls[0].ReplySealed) != 1 {
		t.Fatal("the error came back without a seal")
	}
	// A typing error never reaches the wire.
	entry, s := exec(t, e, ".create tea")
	if !strings.Contains(entry.Error, "usage") || len(entry.Calls) != 0 || s.Calls != 2 {
		t.Fatalf("usage error: %+v, %d calls", entry, s.Calls)
	}
	if entry, _ = exec(t, e, ".quit"); entry.Error == "" {
		t.Fatal(".quit must not close the page connection")
	}
	if entry, _ = exec(t, e, ".list"); entry.Error != "" || !strings.Contains(entry.Output, "#3  bread  0.80") {
		t.Fatalf("the connection does not work after .quit: %+v", entry)
	}
}

func TestLongListsUseManyBlocks(t *testing.T) {
	e := engine(t)
	for i := range 6 {
		exec(t, e, ".create product_number_"+string(rune('a'+i))+" 1")
	}
	entry, _ := exec(t, e, ".list")
	if n := len(entry.Calls[0].ReplySealed); n < 2 {
		t.Fatalf("a list of 9 products came in %d block", n)
	}
}

func TestResetStartsAgain(t *testing.T) {
	e := engine(t)
	exec(t, e, ".delete 1")
	s, err := e.Reset()
	if err != nil || len(s.Products) != 3 || s.Calls != 1 || s.Handshake == nil {
		t.Fatalf("reset: %v %+v", err, s)
	}
}
