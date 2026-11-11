// Package gazette runs a server and a client in one process, over an
// in-memory connection, and records every call: the plaintext, the sealed
// blocks, and the bytes on the wire. The browser page (The RPC Gazette)
// uses it through WebAssembly.
package gazette

import (
	"crypto/rsa"
	"encoding/hex"
	"errors"
	"net"
	"sync"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/client"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
	"github.com/rahelmartim/go-rpc-rsa-crud/service"
)

const maxWireBytes = 2048

// Exchange is one RPC call.
type Exchange struct {
	Seq         int      `json:"seq"`
	Method      string   `json:"method"`
	Request     string   `json:"request"`     // what the client wrote
	Sealed      []string `json:"sealed"`      // the request blocks, in hex
	ReplySealed []string `json:"replySealed"` // the reply blocks, in hex
	Reply       string   `json:"reply"`       // what the client read
	Server      []string `json:"server"`      // what the server wrote in its log
	Error       string   `json:"error,omitempty"`
	Sent        int      `json:"sent"`
	Received    int      `json:"received"`
	SentHex     string   `json:"sentHex"`
	RecvHex     string   `json:"recvHex"`
	Millis      float64  `json:"millis"`
}

// Entry is one typed command and what came of it.
type Entry struct {
	Line   string     `json:"line"`
	Output string     `json:"output"`
	Error  string     `json:"error,omitempty"`
	Calls  []Exchange `json:"calls"`
}

// State is what the page shows next to the entries.
type State struct {
	ClientKey   string            `json:"clientKey"`
	ServerKey   string            `json:"serverKey"`
	ClientKnows string            `json:"clientKnows"` // the server key, as the client received it
	Bits        int               `json:"bits"`
	BlockSize   int               `json:"blockSize"`
	Handshake   *Exchange         `json:"handshake"`
	Products    []catalog.Product `json:"products"`
	Calls       int               `json:"calls"`
	Bytes       int               `json:"bytes"`
}

// Engine holds one server and one connected client.
type Engine struct {
	mu        sync.Mutex // one command at a time
	serverKey *rsa.PrivateKey
	clientKey *rsa.PrivateKey
	server    *service.Server
	client    *client.Client
	handshake *Exchange
	current   []Exchange // the calls of the running command
	seq       int
	bytes     int

	wireMu   sync.Mutex // the bytes and the log lines of the running call
	sent     []byte
	received []byte
	desk     []string
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
	e.server = &service.Server{Catalog: cat, Key: e.serverKey, Log: e.logDesk}
	near, far := net.Pipe()
	go e.server.ServeConn(far)
	e.client = client.New(&tap{Conn: near, e: e}, e.clientKey)
	e.client.Trace = e.trace
	e.seq, e.bytes, e.current = 0, 0, nil
	e.wireMu.Lock()
	e.sent, e.received, e.desk = nil, nil, nil
	e.wireMu.Unlock()
	err := e.client.Handshake()
	if len(e.current) > 0 {
		e.handshake = &e.current[0]
	}
	e.current = nil
	return err
}

func (e *Engine) logDesk(_ int, text string) {
	e.wireMu.Lock()
	e.desk = append(e.desk, text)
	e.wireMu.Unlock()
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

// trace runs after each call of the client, in the goroutine of Exec.
func (e *Engine) trace(c client.Call) {
	e.wireMu.Lock()
	sent, received, desk := e.sent, e.received, e.desk
	e.sent, e.received, e.desk = nil, nil, nil
	e.wireMu.Unlock()

	e.seq++
	e.bytes += len(sent) + len(received)
	x := Exchange{
		Seq: e.seq, Method: service.Name + "." + c.Method,
		Request: string(c.Request), Reply: string(c.Reply), Server: desk,
		Sealed: hexBlocks(c.Sealed), ReplySealed: hexBlocks(c.ReplySealed),
		Sent: len(sent), Received: len(received), SentHex: hexOf(sent), RecvHex: hexOf(received),
		Millis: float64(c.Took.Microseconds()) / 1000,
	}
	if x.Server == nil {
		x.Server = []string{}
	}
	if c.Err != nil {
		x.Error = c.Err.Error()
	}
	e.current = append(e.current, x)
}

func hexBlocks(blocks [][]byte) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = hex.EncodeToString(b)
	}
	return out
}

func hexOf(b []byte) string {
	if len(b) > maxWireBytes {
		b = b[:maxWireBytes]
	}
	return hex.EncodeToString(b)
}

// State returns what the page shows next to the entries.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state()
}

func (e *Engine) state() State {
	s := State{
		ClientKey: keys.Fingerprint(&e.clientKey.PublicKey), ServerKey: keys.Fingerprint(&e.serverKey.PublicKey),
		Bits: keys.Bits, BlockSize: keys.BlockSize(&e.serverKey.PublicKey), Handshake: e.handshake,
		Products: e.server.Catalog.List(), Calls: e.seq, Bytes: e.bytes,
	}
	if e.client.ServerKey != nil {
		s.ClientKnows = keys.Fingerprint(e.client.ServerKey)
	}
	return s
}

// Exec runs a command line, such as ".create rice 4.50".
func (e *Engine) Exec(line string) (Entry, State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.current = nil
	out, err := e.client.Exec(line)
	if errors.Is(err, client.ErrQuit) {
		out, err = "", errors.New("the page keeps its connection open. Use Start over for a new one")
	}
	entry := Entry{Line: line, Output: out, Calls: e.current}
	if entry.Calls == nil {
		entry.Calls = []Exchange{}
	}
	if err != nil {
		entry.Error = err.Error()
	}
	e.current = nil
	return entry, e.state(), err
}

// Reset opens a new connection to a new server, with the same keys.
func (e *Engine) Reset() (State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.connect()
	return e.state(), err
}

// Close ends the connection.
func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.client.Close()
}
