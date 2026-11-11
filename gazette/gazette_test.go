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
	if _, err := shared.Do("reset", nil); err != nil {
		t.Fatal(err)
	}
	return shared
}

func TestStartsWithAHandshake(t *testing.T) {
	e := engine(t)
	s := e.State()
	if len(s.Exchanges) != 1 || s.Exchanges[0].Method != "Products.Handshake" {
		t.Fatalf("exchanges: %+v", s.Exchanges)
	}
	x := s.Exchanges[0]
	if x.Sent < 294 || x.Received < 294 || x.Error != "" {
		t.Fatalf("a handshake carries two keys of 294 bytes: %+v", x)
	}
	if !strings.Contains(x.Args["ClientKey"].(string), s.ClientKey) || !strings.Contains(x.Reply["ServerKey"].(string), s.ServerKey) {
		t.Fatalf("the handshake fields do not name the keys: %+v", x)
	}
	if s.ClientKnows != s.ServerKey || s.ClientKey == s.ServerKey {
		t.Fatalf("keys: %+v", s)
	}
	if len(s.Products) != 3 || s.User != "" || s.Bits != 2048 {
		t.Fatalf("state: %+v", s)
	}
	if len(s.Desk) < 2 || !strings.Contains(s.Desk[1].Text, "handshake: client key "+s.ClientKey) {
		t.Fatalf("desk: %+v", s.Desk)
	}
}

func TestLoginShowsTheCiphertext(t *testing.T) {
	e := engine(t)
	s, err := e.Do("login", map[string]string{"user": "admin", "password": "nope"})
	if err == nil || s.Login == nil || s.Login.OK || s.Login.Error != "wrong user or password" {
		t.Fatalf("wrong password: %v %+v", err, s.Login)
	}
	s, err = e.Do("login", map[string]string{"user": DemoUser, "password": DemoPassword})
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "admin" || !s.Login.OK || s.Login.Bytes != 256 {
		t.Fatalf("login: %+v", s.Login)
	}
	if s.Login.Plaintext != `{"user":"admin","password":"chatuba"}` {
		t.Fatalf("plaintext %q", s.Login.Plaintext)
	}
	raw, err := hex.DecodeString(s.Login.Ciphertext)
	if err != nil || len(raw) != 256 {
		t.Fatalf("ciphertext: %v, %d bytes", err, len(raw))
	}
	last := s.Exchanges[len(s.Exchanges)-1]
	if last.Method != "Products.Login" || !strings.Contains(last.SentHex, s.Login.Ciphertext[:64]) {
		t.Fatal("the ciphertext is not in the bytes on the wire")
	}
	if strings.Contains(last.SentHex, hex.EncodeToString([]byte(DemoPassword))) {
		t.Fatal("the password is on the wire")
	}
}

func TestCRUDActions(t *testing.T) {
	e := engine(t)
	if _, err := e.Do("create", map[string]string{"name": "tea", "price": "3"}); err == nil || !strings.Contains(err.Error(), "log in first") {
		t.Fatalf("create before login: %v", err)
	}
	if _, err := e.Do("login", map[string]string{"user": DemoUser, "password": DemoPassword}); err != nil {
		t.Fatal(err)
	}
	s, err := e.Do("create", map[string]string{"name": "tea", "price": "3"})
	if err != nil || len(s.Products) != 4 || s.Products[3].Name != "tea" {
		t.Fatalf("create: %v %+v", err, s.Products)
	}
	if s, err = e.Do("update", map[string]string{"id": "4", "name": "green_tea", "price": "3.5"}); err != nil || s.Products[3].Price != 350 {
		t.Fatalf("update: %v %+v", err, s.Products)
	}
	if _, err = e.Do("read", map[string]string{"id": "4"}); err != nil {
		t.Fatal(err)
	}
	if s, err = e.Do("delete", map[string]string{"id": "1"}); err != nil || len(s.Products) != 3 {
		t.Fatalf("delete: %v", err)
	}
	if _, err = e.Do("read", map[string]string{"id": "x"}); err == nil {
		t.Fatal("a bad id was accepted")
	}
	if _, err = e.Do("dance", nil); err == nil {
		t.Fatal("an unknown action worked")
	}
	s, err = e.Do("list", nil)
	if err != nil {
		t.Fatal(err)
	}
	last := s.Exchanges[len(s.Exchanges)-1]
	if last.Method != "Products.List" || last.Reply["Products"] != "3 products" {
		t.Fatalf("list exchange: %+v", last)
	}
	if s, _ = e.Do("logout", nil); s.User != "" {
		t.Fatal("still logged in after logout")
	}
}

func TestGobSendsTypesOnce(t *testing.T) {
	e := engine(t)
	_, _ = e.Do("login", map[string]string{"user": DemoUser, "password": DemoPassword})
	first, _ := e.Do("read", map[string]string{"id": "1"})
	second, _ := e.Do("read", map[string]string{"id": "2"})
	a, b := first.Exchanges[len(first.Exchanges)-1], second.Exchanges[len(second.Exchanges)-1]
	if a.Received <= b.Received {
		t.Fatalf("the first reply of a type carries its definition: %d then %d bytes", a.Received, b.Received)
	}
}

func TestExec(t *testing.T) {
	e := engine(t)
	out, s, err := e.Exec(".login admin chatuba")
	if err != nil || out != "logged in as admin" || s.User != "admin" || s.Login == nil || !s.Login.OK {
		t.Fatalf("exec login: %q %v %+v", out, err, s.Login)
	}
	out, _, err = e.Exec(".list")
	if err != nil || !strings.Contains(out, "#2  coffee  12.90") {
		t.Fatalf("exec list: %q %v", out, err)
	}
	if _, _, err = e.Exec(".quit"); err == nil {
		t.Fatal(".quit must not close the page connection")
	}
	if _, s, _ = e.Exec(".list"); len(s.Exchanges) == 0 {
		t.Fatal("the connection closed")
	}
	before := s.Login
	_, s, _ = e.Exec(".read 1")
	if s.Login != before {
		t.Fatal("a command without login changed the login view")
	}
}

func TestResetStartsAgain(t *testing.T) {
	e := engine(t)
	_, _ = e.Do("login", map[string]string{"user": DemoUser, "password": DemoPassword})
	_, _ = e.Do("delete", map[string]string{"id": "1"})
	s, err := e.Do("reset", nil)
	if err != nil || len(s.Products) != 3 || s.User != "" || s.Login != nil || len(s.Exchanges) != 1 || s.Calls != 1 {
		t.Fatalf("reset: %v %+v", err, s)
	}
}
