package service_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net"
	"net/rpc"
	"strings"
	"sync"
	"testing"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/client"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
	"github.com/rahelmartim/go-rpc-rsa-crud/service"
)

var (
	keyOnce    sync.Once
	serverKey  *rsa.PrivateKey
	clientKey  *rsa.PrivateKey
	strangeKey *rsa.PrivateKey
)

func testKeys(t *testing.T) {
	t.Helper()
	keyOnce.Do(func() {
		serverKey, _ = keys.Generate()
		clientKey, _ = keys.Generate()
		strangeKey, _ = keys.Generate()
	})
	if serverKey == nil || clientKey == nil || strangeKey == nil {
		t.Fatal("cannot make the test keys")
	}
}

type logBook struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBook) add(conn int, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf("[%d] %s", conn, text))
}

func (l *logBook) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newServer(t *testing.T) (*service.Server, *logBook) {
	testKeys(t)
	book := &logBook{}
	return &service.Server{Catalog: catalog.New(), Key: serverKey, Log: book.add}, book
}

// pipe opens an in-memory connection to the server.
func pipe(s *service.Server) net.Conn {
	near, far := net.Pipe()
	go s.ServeConn(far)
	return near
}

// connect opens a connection and does the handshake.
func connect(t *testing.T, s *service.Server) *client.Client {
	t.Helper()
	c := client.New(pipe(s), clientKey)
	t.Cleanup(func() { c.Close() })
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	return c
}

func wantError(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("got error %v, want %q", err, text)
	}
}

func TestHandshakeComesFirst(t *testing.T) {
	s, _ := newServer(t)
	raw := rpc.NewClient(pipe(s))
	defer raw.Close()
	blocks, _ := keys.Seal(&serverKey.PublicKey, []byte(`{"name":"rice","price":"1"}`))
	err := raw.Call("Products.Create", service.Sealed{Blocks: blocks}, &service.Sealed{})
	wantError(t, err, service.ErrNoHandshake.Error())
	if len(s.Catalog.List()) != 0 {
		t.Fatal("a call before the handshake changed the catalog")
	}
}

func TestHandshakeExchangesKeys(t *testing.T) {
	s, book := newServer(t)
	c := connect(t, s)
	if c.ServerKey.N.Cmp(serverKey.N) != 0 {
		t.Fatal("the client did not get the server key")
	}
	if !strings.Contains(book.text(), "handshake: client key "+keys.Fingerprint(&clientKey.PublicKey)) {
		t.Fatalf("the server did not get the client key:\n%s", book.text())
	}
}

func TestHandshakeRefusesWeakKeys(t *testing.T) {
	s, _ := newServer(t)
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	c := client.New(pipe(s), small)
	defer c.Close()
	wantError(t, c.Handshake(), "smaller than 2048 bits")

	raw := rpc.NewClient(pipe(s))
	defer raw.Close()
	err := raw.Call("Products.Handshake", service.HandshakeArgs{ClientKey: []byte("junk")}, &service.HandshakeReply{})
	wantError(t, err, "not an RSA public key")
}

func TestCRUD(t *testing.T) {
	s, book := newServer(t)
	c := connect(t, s)
	rice, err := c.Create("rice", "4.50")
	if err != nil || rice.ID != 1 || rice.Price != 450 {
		t.Fatalf("Create = %+v, %v", rice, err)
	}
	if _, err := c.Create("coffee", "12.90"); err != nil {
		t.Fatal(err)
	}
	if p, err := c.Read(1); err != nil || p.Name != "rice" {
		t.Fatalf("Read = %+v, %v", p, err)
	}
	if p, err := c.Update(1, "brown rice", "5"); err != nil || p.Name != "brown rice" {
		t.Fatalf("Update = %+v, %v", p, err)
	}
	if err := c.Delete(2); err != nil {
		t.Fatal(err)
	}
	list, err := c.List()
	if err != nil || len(list) != 1 || list[0].Name != "brown rice" {
		t.Fatalf("List = %+v, %v", list, err)
	}
	for _, line := range []string{"created #1 rice at 4.50", "updated #1 to brown rice at 5.00", "deleted #2",
		`opened Create with the server key: {"name":"rice","price":"4.50"}`, "sealed the reply with the client key"} {
		if !strings.Contains(book.text(), line) {
			t.Errorf("the log has no %q:\n%s", line, book.text())
		}
	}
}

func TestErrorsComeBackSealed(t *testing.T) {
	s, book := newServer(t)
	c := connect(t, s)
	_, err := c.Read(2)
	wantError(t, err, "no product has this id")
	_, err = c.Update(7, "tea", "1")
	wantError(t, err, "no product has this id")
	wantError(t, c.Delete(7), "no product has this id")
	_, err = c.Create("tea", "free")
	wantError(t, err, "a price is a number")
	_, err = c.Create("", "1")
	wantError(t, err, "a name has")
	if !strings.Contains(book.text(), "Read refused: no product has this id") {
		t.Fatalf("log:\n%s", book.text())
	}

	// The error is inside the sealed reply: only the client key opens it.
	var traced client.Call
	c.Trace = func(call client.Call) { traced = call }
	_, _ = c.Read(9)
	if len(traced.ReplySealed) != 1 || strings.Contains(string(traced.ReplySealed[0]), "no product") {
		t.Fatal("the error is not sealed")
	}
	if _, err := keys.Open(strangeKey, traced.ReplySealed); err == nil {
		t.Fatal("another key opened the reply")
	}
	plain, err := keys.Open(clientKey, traced.ReplySealed)
	if err != nil || string(plain) != `{"error":"no product has this id"}` {
		t.Fatalf("reply %q, %v", plain, err)
	}
}

func TestRequestsTheServerCannotOpen(t *testing.T) {
	s, _ := newServer(t)
	raw := rpc.NewClient(pipe(s))
	defer raw.Close()
	der, _ := keys.Marshal(&clientKey.PublicKey)
	if err := raw.Call("Products.Handshake", service.HandshakeArgs{ClientKey: der}, &service.HandshakeReply{}); err != nil {
		t.Fatal(err)
	}
	call := func(blocks [][]byte) error {
		return raw.Call("Products.List", service.Sealed{Blocks: blocks}, &service.Sealed{})
	}
	sealedForAnother, _ := keys.Seal(&strangeKey.PublicKey, []byte("{}"))
	wantError(t, call(sealedForAnother), "cannot open this request")
	wantError(t, call(nil), "cannot open this request")
	wantError(t, call([][]byte{[]byte("short")}), "cannot open this request")
	notJSON, _ := keys.Seal(&serverKey.PublicKey, []byte("list please"))
	wantError(t, call(notJSON), "not valid JSON")

	// The connection still works after the bad requests.
	good, _ := keys.Seal(&serverKey.PublicKey, []byte("{}"))
	if err := call(good); err != nil {
		t.Fatal(err)
	}
}

func TestTheWireShowsNoPlaintext(t *testing.T) {
	s, _ := newServer(t)
	rec := &recorder{Conn: pipe(s)}
	c := client.New(rec, clientKey)
	defer c.Close()
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create("secret_recipe", "99.99"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(); err != nil {
		t.Fatal(err)
	}
	wire := rec.text()
	for _, word := range []string{"secret_recipe", "99.99", "9999", `"name"`} {
		if strings.Contains(wire, word) {
			t.Fatalf("%q is on the wire in plain text", word)
		}
	}
	// net/rpc sends the method names in plain text.
	if !strings.Contains(wire, "Products.Create") {
		t.Fatal("the recorder did not see the bytes")
	}
}

type recorder struct {
	net.Conn
	mu  sync.Mutex
	all strings.Builder
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.all.Write(p)
	r.mu.Unlock()
	return r.Conn.Write(p)
}

func (r *recorder) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	r.mu.Lock()
	r.all.Write(p[:n])
	r.mu.Unlock()
	return n, err
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.all.String()
}

func TestLongRepliesUseManyBlocks(t *testing.T) {
	s, _ := newServer(t)
	for i := range 30 {
		if _, err := s.Catalog.Create(fmt.Sprintf("product_%02d", i), "1.25"); err != nil {
			t.Fatal(err)
		}
	}
	c := connect(t, s)
	var traced client.Call
	c.Trace = func(call client.Call) { traced = call }
	list, err := c.List()
	if err != nil || len(list) != 30 {
		t.Fatalf("List = %d products, %v", len(list), err)
	}
	var result service.Result
	if err := json.Unmarshal(traced.Reply, &result); err != nil || len(result.Products) != 30 {
		t.Fatalf("traced reply: %v", err)
	}
	if want := (len(traced.Reply) + 189) / 190; len(traced.ReplySealed) != want || want < 2 {
		t.Fatalf("%d bytes in %d blocks, want %d", len(traced.Reply), len(traced.ReplySealed), want)
	}
}

func TestManyClientsOverTCP(t *testing.T) {
	s, _ := newServer(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go s.Serve(l)

	var wg sync.WaitGroup
	for w := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := client.Dial(l.Addr().String(), clientKey)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			for i := range 5 {
				if _, err := c.Create(fmt.Sprintf("w%d_%d", w, i), "1"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if n := len(s.Catalog.List()); n != 30 {
		t.Fatalf("%d products, want 30", n)
	}
}
