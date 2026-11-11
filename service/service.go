// Package service is the RPC server of the catalog. Clients call it with
// net/rpc over a TCP connection (or any other connection).
//
// A connection starts with a key exchange (Handshake). After it, every
// call is sealed: the client encrypts its request with the public key of
// the server, and the server encrypts its reply with the public key of the
// client. Someone who reads the wire sees the method names, and nothing
// else.
package service

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"sync"
	"sync/atomic"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
)

// Name is the name of the RPC service: the methods are "Products.Create",
// "Products.Read", and so on.
const Name = "Products"

// The arguments and replies of the RPC methods. net/rpc encodes them with
// encoding/gob.
type (
	HandshakeArgs  struct{ ClientKey []byte }
	HandshakeReply struct{ ServerKey []byte }
	// Sealed is an encrypted message: RSA-OAEP blocks of JSON.
	Sealed struct{ Blocks [][]byte }
)

// Request is the plaintext of a sealed request.
type Request struct {
	ID    int    `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Price string `json:"price,omitempty"`
}

// Result is the plaintext of a sealed reply. The errors of the catalog are
// in the reply too, so they are encrypted like the rest.
type Result struct {
	Product  *catalog.Product  `json:"product,omitempty"`
	Products []catalog.Product `json:"products,omitempty"`
	Text     string            `json:"text,omitempty"`
	Error    string            `json:"error,omitempty"`
}

var (
	ErrNoHandshake = errors.New("do the handshake first")
	ErrCannotOpen  = errors.New("the server cannot open this request with its key")
	ErrBadRequest  = errors.New("the request is not valid JSON")
)

// Server serves the catalog to many connections.
type Server struct {
	Catalog *catalog.Catalog
	Key     *rsa.PrivateKey
	// Log receives one line for each event of a connection. It can be nil.
	Log func(conn int, text string)

	conns atomic.Int64
}

func (s *Server) log(conn int, format string, args ...any) {
	if s.Log != nil {
		s.Log(conn, fmt.Sprintf(format, args...))
	}
}

// ServeConn serves one connection until the client closes it.
func (s *Server) ServeConn(conn net.Conn) {
	id := int(s.conns.Add(1))
	rs := rpc.NewServer()
	if err := rs.RegisterName(Name, &Session{server: s, conn: id}); err != nil {
		s.log(id, "cannot register the service: %v", err)
		conn.Close()
		return
	}
	s.log(id, "connection opened")
	rs.ServeConn(conn)
	s.log(id, "connection closed")
}

// Serve accepts connections until the listener closes.
func (s *Server) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go s.ServeConn(conn)
	}
}

// Session is the state of one connection. Its exported methods are the
// RPC methods.
type Session struct {
	server *Server
	conn   int

	mu        sync.Mutex
	clientKey *rsa.PublicKey
}

// Handshake exchanges public keys. The server keeps the key of the client
// to seal its replies, and replies with its own key.
func (s *Session) Handshake(args HandshakeArgs, reply *HandshakeReply) error {
	pub, err := keys.Parse(args.ClientKey)
	if err != nil {
		return fmt.Errorf("the client key is not valid: %w", err)
	}
	der, err := keys.Marshal(&s.server.Key.PublicKey)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.clientKey = pub
	s.mu.Unlock()
	reply.ServerKey = der
	s.server.log(s.conn, "handshake: client key %s", keys.Fingerprint(pub))
	return nil
}

// serve opens a sealed request, runs it, and seals the result for the
// client.
func (s *Session) serve(method string, args Sealed, reply *Sealed, run func(Request) Result) error {
	s.mu.Lock()
	clientKey := s.clientKey
	s.mu.Unlock()
	if clientKey == nil {
		return ErrNoHandshake
	}
	plain, err := keys.Open(s.server.Key, args.Blocks)
	if err != nil {
		s.server.log(s.conn, "%s: cannot open the request", method)
		return ErrCannotOpen
	}
	var req Request
	if err := json.Unmarshal(plain, &req); err != nil {
		s.server.log(s.conn, "%s: the request is not JSON", method)
		return ErrBadRequest
	}
	// Log the parsed request, not the raw bytes: the client cannot make
	// the log hold anything else.
	shown, _ := json.Marshal(req)
	s.server.log(s.conn, "opened %s with the server key: %s", method, shown)
	result := run(req)
	if result.Error != "" {
		s.server.log(s.conn, "%s refused: %s", method, result.Error)
	}
	out, err := json.Marshal(result)
	if err != nil {
		return err
	}
	blocks, err := keys.Seal(clientKey, out)
	if err != nil {
		return err
	}
	reply.Blocks = blocks
	s.server.log(s.conn, "sealed the reply with the client key: %d bytes in %d blocks", len(out), len(blocks))
	return nil
}

func failed(err error) Result {
	return Result{Error: err.Error()}
}

func (s *Session) Create(args Sealed, reply *Sealed) error {
	return s.serve("Create", args, reply, func(r Request) Result {
		p, err := s.server.Catalog.Create(r.Name, r.Price)
		if err != nil {
			return failed(err)
		}
		s.server.log(s.conn, "created #%d %s at %s", p.ID, p.Name, catalog.FormatPrice(p.Price))
		return Result{Product: &p}
	})
}

func (s *Session) Read(args Sealed, reply *Sealed) error {
	return s.serve("Read", args, reply, func(r Request) Result {
		p, err := s.server.Catalog.Get(r.ID)
		if err != nil {
			return failed(err)
		}
		return Result{Product: &p}
	})
}

func (s *Session) Update(args Sealed, reply *Sealed) error {
	return s.serve("Update", args, reply, func(r Request) Result {
		p, err := s.server.Catalog.Update(r.ID, r.Name, r.Price)
		if err != nil {
			return failed(err)
		}
		s.server.log(s.conn, "updated #%d to %s at %s", p.ID, p.Name, catalog.FormatPrice(p.Price))
		return Result{Product: &p}
	})
}

func (s *Session) Delete(args Sealed, reply *Sealed) error {
	return s.serve("Delete", args, reply, func(r Request) Result {
		if err := s.server.Catalog.Delete(r.ID); err != nil {
			return failed(err)
		}
		s.server.log(s.conn, "deleted #%d", r.ID)
		return Result{Text: fmt.Sprintf("deleted #%d", r.ID)}
	})
}

func (s *Session) List(args Sealed, reply *Sealed) error {
	return s.serve("List", args, reply, func(Request) Result {
		return Result{Products: s.server.Catalog.List()}
	})
}
