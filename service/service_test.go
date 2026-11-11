package service_test

import (
	"crypto/rand"
	"crypto/rsa"
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

const password = "chatuba"

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
	return &service.Server{Catalog: catalog.New(), Key: serverKey, User: "admin", Password: password, Log: book.add}, book
}

// connect opens an in-memory connection to the server.
func connect(t *testing.T, s *service.Server) *client.Client {
	t.Helper()
	near, far := net.Pipe()
	go s.ServeConn(far)
	c := client.New(near, clientKey)
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
	near, far := net.Pipe()
	go s.ServeConn(far)
	raw := rpc.NewClient(near)
	defer raw.Close()

	err := raw.Call("Products.Login", service.LoginArgs{Ciphertext: []byte("x")}, &service.Message{})
	wantError(t, err, service.ErrNoHandshake.Error())
	err = raw.Call("Products.Create", service.ProductArgs{Name: "rice", Price: "1"}, &catalog.Product{})
	wantError(t, err, service.ErrNoHandshake.Error())
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
	near, far := net.Pipe()
	go s.ServeConn(far)
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	c := client.New(near, small)
	defer c.Close()
	wantError(t, c.Handshake(), "smaller than 2048 bits")

	raw := rpc.NewClient(rawConn(s))
	defer raw.Close()
	err := raw.Call("Products.Handshake", service.HandshakeArgs{ClientKey: []byte("junk")}, &service.HandshakeReply{})
	wantError(t, err, "not an RSA public key")
}

func rawConn(s *service.Server) net.Conn {
	near, far := net.Pipe()
	go s.ServeConn(far)
	return near
}

func TestLoginIsNeeded(t *testing.T) {
	s, _ := newServer(t)
	c := connect(t, s)
	_, err := c.Create("rice", "1")
	wantError(t, err, "log in first")
	_, err = c.List()
	wantError(t, err, "log in first")
	_, err = c.Read(1)
	wantError(t, err, "log in first")
	wantError(t, c.Delete(1), "log in first")
	if len(s.Catalog.List()) != 0 {
		t.Fatal("a call without login changed the catalog")
	}
}

func TestWrongPasswordAndTheLimit(t *testing.T) {
	s, book := newServer(t)
	c := connect(t, s)
	wantError(t, c.Login("admin", "wrong"), "wrong user or password")
	wantError(t, c.Login("root", password), "wrong user or password")
	wantError(t, c.Login("admin", ""), "wrong user or password")
	// After three failures, even the right password does not work on this
	// connection.
	wantError(t, c.Login("admin", password), "too many failed logins")

	// A new connection starts again.
	if err := connect(t, s).Login("admin", password); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(book.text(), password) || strings.Contains(book.text(), "wrong") {
		t.Fatalf("the log shows a password:\n%s", book.text())
	}
}

func TestAnEmptyServerPasswordRefusesAll(t *testing.T) {
	s, _ := newServer(t)
	s.Password = ""
	wantError(t, connect(t, s).Login("admin", ""), "wrong user or password")
}

func TestLoginIsPerConnection(t *testing.T) {
	s, _ := newServer(t)
	alice, bob := connect(t, s), connect(t, s)
	if err := alice.Login("admin", password); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.Create("rice", "4.50"); err != nil {
		t.Fatal(err)
	}
	_, err := bob.Read(1)
	wantError(t, err, "log in first")

	// The catalog is shared: after its login, bob sees the product of alice.
	if err := bob.Login("admin", password); err != nil {
		t.Fatal(err)
	}
	if p, err := bob.Read(1); err != nil || p.Name != "rice" {
		t.Fatalf("Read = %+v, %v", p, err)
	}
	if err := alice.Logout(); err != nil {
		t.Fatal(err)
	}
	_, err = alice.List()
	wantError(t, err, "log in first")
	if _, err := bob.List(); err != nil {
		t.Fatalf("the logout of alice ended the login of bob: %v", err)
	}
}

func TestCRUD(t *testing.T) {
	s, book := newServer(t)
	c := connect(t, s)
	if err := c.Login("admin", password); err != nil {
		t.Fatal(err)
	}
	rice, err := c.Create("rice", "4.50")
	if err != nil || rice.ID != 1 || rice.Price != 450 {
		t.Fatalf("Create = %+v, %v", rice, err)
	}
	if _, err := c.Create("coffee", "12.90"); err != nil {
		t.Fatal(err)
	}
	if p, err := c.Update(1, "brown_rice", "5"); err != nil || p.Name != "brown_rice" {
		t.Fatalf("Update = %+v, %v", p, err)
	}
	if err := c.Delete(2); err != nil {
		t.Fatal(err)
	}
	list, err := c.List()
	if err != nil || len(list) != 1 || list[0].Name != "brown_rice" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	_, err = c.Read(2)
	wantError(t, err, "no product has this id")
	_, err = c.Update(7, "tea", "1")
	wantError(t, err, "no product has this id")
	wantError(t, c.Delete(7), "no product has this id")
	_, err = c.Create("tea", "free")
	wantError(t, err, "a price is a number")
	_, err = c.Create("", "1")
	wantError(t, err, "a name has")

	for _, line := range []string{"created #1 rice at 4.50", "updated #1 to brown_rice at 5.00", "deleted #2", "read refused: no product has this id"} {
		if !strings.Contains(book.text(), line) {
			t.Errorf("the log has no %q:\n%s", line, book.text())
		}
	}
}

func TestBadCiphertexts(t *testing.T) {
	s, _ := newServer(t)
	c := connect(t, s)

	junk := make([]byte, serverKey.Size())
	_, _ = rand.Read(junk)
	_, err := c.SendLogin(client.Sealed{Ciphertext: junk})
	wantError(t, err, "cannot be decrypted")
	_, err = c.SendLogin(client.Sealed{Ciphertext: []byte("short")})
	wantError(t, err, "cannot be decrypted")

	// A login sealed for another server key cannot be opened.
	c2 := connect(t, s)
	c2.ServerKey = &strangeKey.PublicKey
	wantError(t, c2.Login("admin", password), "cannot be decrypted")

	// Encrypted, but not JSON.
	ct, _ := keys.Encrypt(&serverKey.PublicKey, []byte("admin:chatuba"))
	c3 := connect(t, s)
	_, err = c3.SendLogin(client.Sealed{Ciphertext: ct})
	wantError(t, err, "cannot be decrypted")
}

func TestTheWireHidesThePassword(t *testing.T) {
	s, _ := newServer(t)
	near, far := net.Pipe()
	go s.ServeConn(far)
	rec := &recorder{Conn: near}
	c := client.New(rec, clientKey)
	defer c.Close()
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	if err := c.Login("admin", password); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.sent.String(), password) {
		t.Fatal("the password is on the wire in plain text")
	}
	if _, err := c.Create("rice", "1"); err != nil {
		t.Fatal(err)
	}
	// The CRUD calls are not encrypted: only the login is.
	if !strings.Contains(rec.sent.String(), "rice") {
		t.Fatal("the test recorder did not see the bytes")
	}
}

type recorder struct {
	net.Conn
	mu   sync.Mutex
	sent strings.Builder
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.sent.Write(p)
	r.mu.Unlock()
	return r.Conn.Write(p)
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
			if err := c.Login("admin", password); err != nil {
				t.Error(err)
				return
			}
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
