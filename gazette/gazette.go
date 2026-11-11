// Package gazette runs a server and a client in one process, over an
// in-memory connection, and records every call with its bytes. The
// browser page (The RPC Gazette) uses it through WebAssembly.
package gazette

import (
	"crypto/rsa"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/client"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
	"github.com/rahelmartim/go-rpc-rsa-crud/service"
)

// The demo account. The page shows it, so it is not a secret.
const (
	DemoUser     = "admin"
	DemoPassword = "chatuba"
)

const (
	maxExchanges = 60
	maxDesk      = 80
	maxHexBytes  = 1024
)

// Exchange is one RPC call: what the client sent, and what came back.
type Exchange struct {
	Seq      int            `json:"seq"`
	Time     time.Time      `json:"time"`
	Method   string         `json:"method"`
	Args     map[string]any `json:"args"`
	Reply    map[string]any `json:"reply,omitempty"`
	Error    string         `json:"error,omitempty"`
	Sent     int            `json:"sent"`
	Received int            `json:"received"`
	SentHex  string         `json:"sentHex"`
	RecvHex  string         `json:"recvHex"`
	Millis   float64        `json:"millis"`
}

// DeskLine is one line of the server log.
type DeskLine struct {
	Seq  int       `json:"seq"`
	Time time.Time `json:"time"`
	Conn int       `json:"conn"`
	Text string    `json:"text"`
}

// LoginView shows the last login message, before and after encryption.
type LoginView struct {
	Plaintext  string `json:"plaintext"`
	Ciphertext string `json:"ciphertext"` // hex
	Bytes      int    `json:"bytes"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// State is everything that the page shows.
type State struct {
	ClientKey    string            `json:"clientKey"`
	ServerKey    string            `json:"serverKey"`
	ClientKnows  string            `json:"clientKnows"` // the server key, as the client received it
	Bits         int               `json:"bits"`
	User         string            `json:"user"`
	Products     []catalog.Product `json:"products"`
	Exchanges    []Exchange        `json:"exchanges"`
	Desk         []DeskLine        `json:"desk"`
	Login        *LoginView        `json:"login"`
	Calls        int               `json:"calls"`
	Bytes        int               `json:"bytes"`
	DemoUser     string            `json:"demoUser"`
	DemoPassword string            `json:"demoPassword"`
}

// Engine holds one server and one connected client.
type Engine struct {
	mu        sync.Mutex // one action at a time
	serverKey *rsa.PrivateKey
	clientKey *rsa.PrivateKey
	server    *service.Server
	client    *client.Client
	user      string
	login     *LoginView
	exchanges []Exchange
	seq       int
	calls     int
	bytes     int

	wireMu   sync.Mutex // the bytes of the current call
	sent     []byte
	received []byte

	deskMu  sync.Mutex
	desk    []DeskLine
	deskSeq int
}

// New makes the keys, starts the server, and connects the client. Making
// two RSA keys takes a moment, most of all in a browser.
func New() (*Engine, error) {
	serverKey, err := keys.Generate()
	if err != nil {
		return nil, err
	}
	clientKey, err := keys.Generate()
	if err != nil {
		return nil, err
	}
	e := &Engine{serverKey: serverKey, clientKey: clientKey}
	if err := e.connect(); err != nil {
		return nil, err
	}
	return e, nil
}

// connect starts a new server with demo products, and a new connection.
func (e *Engine) connect() error {
	if e.client != nil {
		e.client.Close()
	}
	cat := catalog.New()
	for _, p := range [][2]string{{"rice", "4.50"}, {"coffee", "12.90"}, {"bread", "0.80"}} {
		if _, err := cat.Create(p[0], p[1]); err != nil {
			return err
		}
	}
	e.server = &service.Server{
		Catalog: cat, Key: e.serverKey, User: DemoUser, Password: DemoPassword, Log: e.logDesk,
	}
	near, far := net.Pipe()
	go e.server.ServeConn(far)
	e.client = client.New(&tap{Conn: near, e: e}, e.clientKey)
	e.client.Trace = e.trace
	e.user, e.login = "", nil
	e.exchanges, e.seq, e.calls, e.bytes = nil, 0, 0, 0
	e.deskMu.Lock()
	e.desk, e.deskSeq = nil, 0
	e.deskMu.Unlock()
	return e.client.Handshake()
}

func (e *Engine) logDesk(conn int, text string) {
	e.deskMu.Lock()
	defer e.deskMu.Unlock()
	e.deskSeq++
	e.desk = append(e.desk, DeskLine{Seq: e.deskSeq, Time: time.Now(), Conn: conn, Text: text})
	if len(e.desk) > maxDesk {
		e.desk = e.desk[len(e.desk)-maxDesk:]
	}
}

// tap counts the bytes of the client connection in both directions.
type tap struct {
	net.Conn
	e *Engine
}

func (t *tap) Write(p []byte) (int, error) {
	n, err := t.Conn.Write(p)
	t.e.wireMu.Lock()
	t.e.sent = append(t.e.sent, p[:n]...)
	t.e.wireMu.Unlock()
	return n, err
}

func (t *tap) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	t.e.wireMu.Lock()
	t.e.received = append(t.e.received, p[:n]...)
	t.e.wireMu.Unlock()
	return n, err
}

// trace runs after each call of the client, in the goroutine of Do.
func (e *Engine) trace(method string, args, reply any, err error, took time.Duration) {
	e.wireMu.Lock()
	sent, received := e.sent, e.received
	e.sent, e.received = nil, nil
	e.wireMu.Unlock()

	e.seq++
	e.calls++
	e.bytes += len(sent) + len(received)
	x := Exchange{
		Seq: e.seq, Time: time.Now(), Method: service.Name + "." + method,
		Args: describe(args), Sent: len(sent), Received: len(received),
		SentHex: hexOf(sent), RecvHex: hexOf(received), Millis: float64(took.Microseconds()) / 1000,
	}
	if err != nil {
		x.Error = err.Error()
	} else {
		x.Reply = describe(reply)
	}
	if method == "Login" && err == nil {
		if m, ok := reply.(*service.Message); ok {
			e.user = strings.TrimPrefix(m.Text, "welcome, ")
		}
	}
	if method == "Logout" && err == nil {
		e.user = ""
	}
	e.exchanges = append(e.exchanges, x)
	if len(e.exchanges) > maxExchanges {
		e.exchanges = e.exchanges[len(e.exchanges)-maxExchanges:]
	}
}

func hexOf(b []byte) string {
	if len(b) > maxHexBytes {
		b = b[:maxHexBytes]
	}
	return hex.EncodeToString(b)
}

// describe turns RPC arguments and replies into fields for the page.
func describe(v any) map[string]any {
	switch v := v.(type) {
	case service.HandshakeArgs:
		return map[string]any{"ClientKey": keyField(v.ClientKey)}
	case *service.HandshakeReply:
		return map[string]any{"ServerKey": keyField(v.ServerKey)}
	case service.LoginArgs:
		return map[string]any{"Ciphertext": fmt.Sprintf("%d encrypted bytes", len(v.Ciphertext))}
	case service.ProductArgs:
		m := map[string]any{"Name": v.Name, "Price": v.Price}
		if v.ID != 0 {
			m["ID"] = v.ID
		}
		return m
	case service.IDArgs:
		return map[string]any{"ID": v.ID}
	case service.Message:
		return map[string]any{}
	case *service.Message:
		return map[string]any{"Text": v.Text}
	case *catalog.Product:
		return map[string]any{"ID": v.ID, "Name": v.Name, "Price": catalog.FormatPrice(v.Price)}
	case *service.ListReply:
		return map[string]any{"Products": fmt.Sprintf("%d products", len(v.Products))}
	}
	return map[string]any{"value": fmt.Sprint(v)}
}

func keyField(der []byte) string {
	pub, err := keys.Parse(der)
	if err != nil {
		return fmt.Sprintf("%d bytes, not a valid key", len(der))
	}
	return fmt.Sprintf("%d bytes, RSA %d, fingerprint %s", len(der), pub.N.BitLen(), keys.Fingerprint(pub))
}

// State returns a copy of what the page shows.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state()
}

func (e *Engine) state() State {
	s := State{
		ClientKey: keys.Fingerprint(&e.clientKey.PublicKey), ServerKey: keys.Fingerprint(&e.serverKey.PublicKey),
		Bits: keys.Bits, User: e.user, Products: e.server.Catalog.List(), Login: e.login,
		Exchanges: append([]Exchange{}, e.exchanges...), Calls: e.calls, Bytes: e.bytes,
		DemoUser: DemoUser, DemoPassword: DemoPassword,
	}
	if e.client.ServerKey != nil {
		s.ClientKnows = keys.Fingerprint(e.client.ServerKey)
	}
	e.deskMu.Lock()
	s.Desk = append([]DeskLine{}, e.desk...)
	e.deskMu.Unlock()
	return s
}

// Do runs one action of the page. Every action returns the new state, and
// the error of the action if it failed.
func (e *Engine) Do(action string, args map[string]string) (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.do(action, args)
	return e.state(), err
}

// ErrUnknown is the error of an action that does not exist.
var ErrUnknown = errors.New("unknown action")

func (e *Engine) do(action string, args map[string]string) error {
	c := e.client
	id := func() (int, error) {
		n, err := strconv.Atoi(strings.TrimSpace(args["id"]))
		if err != nil || n < 1 {
			return 0, errors.New("the id is a whole number, such as 1")
		}
		return n, nil
	}
	switch action {
	case "state":
		return nil
	case "reset":
		return e.connect()
	case "login":
		sealed, err := c.Seal(args["user"], args["password"])
		if err != nil {
			e.login = &LoginView{Error: err.Error()}
			return err
		}
		_, err = c.SendLogin(sealed)
		e.login = viewOf(sealed, err)
		return err
	case "logout":
		return c.Logout()
	case "create":
		_, err := c.Create(args["name"], args["price"])
		return err
	case "read":
		n, err := id()
		if err != nil {
			return err
		}
		_, err = c.Read(n)
		return err
	case "update":
		n, err := id()
		if err != nil {
			return err
		}
		_, err = c.Update(n, args["name"], args["price"])
		return err
	case "delete":
		n, err := id()
		if err != nil {
			return err
		}
		return c.Delete(n)
	case "list":
		_, err := c.List()
		return err
	}
	return fmt.Errorf("%w: %s", ErrUnknown, action)
}

// Exec runs a command line, such as ".create rice 4.50", and returns its
// output.
func (e *Engine) Exec(line string) (string, State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.client.LastLogin = client.Sealed{}
	out, err := e.client.Exec(line)
	if errors.Is(err, client.ErrQuit) {
		out, err = "", errors.New("the page keeps its connection open. Use Start over to begin again")
	}
	if sealed := e.client.LastLogin; len(sealed.Ciphertext) > 0 {
		e.login = viewOf(sealed, err)
	}
	return out, e.state(), err
}

func viewOf(sealed client.Sealed, err error) *LoginView {
	v := &LoginView{
		Plaintext: string(sealed.Plaintext), Ciphertext: hex.EncodeToString(sealed.Ciphertext),
		Bytes: len(sealed.Ciphertext), OK: err == nil,
	}
	if err != nil {
		v.Error = err.Error()
	}
	return v
}

// Close ends the connection.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client.Close()
}
