// Package service is the RPC server of the catalog. Clients call it with
// net/rpc over a TCP connection (or any other connection).
//
// Each connection has its own session: first a key exchange (Handshake),
// then a login that is encrypted with the public key of the server
// (Login), then the CRUD calls. A login is valid only on its connection.
package service

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
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

// MaxLoginTries is the number of failed logins that one connection can have.
const MaxLoginTries = 3

// The arguments and replies of the RPC methods. net/rpc encodes them with
// encoding/gob, so every type has at least one exported field.
type (
	HandshakeArgs  struct{ ClientKey []byte }
	HandshakeReply struct{ ServerKey []byte }
	LoginArgs      struct{ Ciphertext []byte }
	ProductArgs    struct {
		ID    int
		Name  string
		Price string
	}
	IDArgs    struct{ ID int }
	ListReply struct{ Products []catalog.Product }
	// Message is a reply with a short text, and the argument of the calls
	// that need no argument.
	Message struct{ Text string }
)

// Credentials is the plaintext of a login message, before the encryption.
type Credentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

var (
	ErrNoHandshake   = errors.New("do the handshake first")
	ErrNotLoggedIn   = errors.New("log in first")
	ErrBadLogin      = errors.New("wrong user or password")
	ErrTooManyTries  = fmt.Errorf("too many failed logins on this connection: at most %d", MaxLoginTries)
	ErrBadCiphertext = errors.New("the login message cannot be decrypted with the server key")
)

// Server serves the catalog to many connections.
type Server struct {
	Catalog  *catalog.Catalog
	Key      *rsa.PrivateKey
	User     string
	Password string
	// Log receives one line for each event of a connection. It can be nil.
	// It never receives a password.
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

func (s *Server) check(c Credentials) bool {
	// Compare hashes in constant time, so that the time of the answer says
	// nothing about the password.
	u1, u2 := sha256.Sum256([]byte(c.User)), sha256.Sum256([]byte(s.User))
	p1, p2 := sha256.Sum256([]byte(c.Password)), sha256.Sum256([]byte(s.Password))
	user := subtle.ConstantTimeCompare(u1[:], u2[:])
	pass := subtle.ConstantTimeCompare(p1[:], p2[:])
	return user&pass == 1 && s.Password != ""
}

// Session is the state of one connection. Its exported methods are the
// RPC methods.
type Session struct {
	server *Server
	conn   int

	mu        sync.Mutex
	clientKey *rsa.PublicKey
	user      string
	failures  int
}

// Handshake exchanges public keys. The server keeps the key of the client,
// and replies with its own key.
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
	s.clientKey, s.user = pub, ""
	s.mu.Unlock()
	reply.ServerKey = der
	s.server.log(s.conn, "handshake: client key %s", keys.Fingerprint(pub))
	return nil
}

// Login decrypts the credentials with the private key of the server.
func (s *Session) Login(args LoginArgs, reply *Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clientKey == nil {
		return ErrNoHandshake
	}
	if s.failures >= MaxLoginTries {
		return ErrTooManyTries
	}
	var c Credentials
	plain, err := []byte(nil), ErrBadCiphertext
	if len(args.Ciphertext) == s.server.Key.Size() {
		plain, err = keys.Decrypt(s.server.Key, args.Ciphertext)
	}
	if err != nil || json.Unmarshal(plain, &c) != nil {
		s.failures++
		s.server.log(s.conn, "login refused: the message cannot be decrypted")
		return ErrBadCiphertext
	}
	if !s.server.check(c) {
		s.failures++
		s.server.log(s.conn, "login refused for %q (%d of %d tries)", truncate(c.User, 32), s.failures, MaxLoginTries)
		return ErrBadLogin
	}
	s.user, s.failures = c.User, 0
	s.server.log(s.conn, "decrypted the login, %s is logged in", c.User)
	reply.Text = "welcome, " + c.User
	return nil
}

// Logout ends the login of this connection.
func (s *Session) Logout(_ Message, reply *Message) error {
	s.mu.Lock()
	user := s.user
	s.user = ""
	s.mu.Unlock()
	if user != "" {
		s.server.log(s.conn, "%s logged out", user)
	}
	reply.Text = "logged out"
	return nil
}

func (s *Session) authorized() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clientKey == nil {
		return ErrNoHandshake
	}
	if s.user == "" {
		return ErrNotLoggedIn
	}
	return nil
}

func (s *Session) refuse(method string, err error) error {
	s.server.log(s.conn, "%s refused: %v", method, err)
	return err
}

func (s *Session) Create(args ProductArgs, reply *catalog.Product) error {
	if err := s.authorized(); err != nil {
		return s.refuse("create", err)
	}
	p, err := s.server.Catalog.Create(args.Name, args.Price)
	if err != nil {
		return s.refuse("create", err)
	}
	s.server.log(s.conn, "created #%d %s at %s", p.ID, p.Name, catalog.FormatPrice(p.Price))
	*reply = p
	return nil
}

func (s *Session) Read(args IDArgs, reply *catalog.Product) error {
	if err := s.authorized(); err != nil {
		return s.refuse("read", err)
	}
	p, err := s.server.Catalog.Get(args.ID)
	if err != nil {
		return s.refuse("read", err)
	}
	s.server.log(s.conn, "read #%d", p.ID)
	*reply = p
	return nil
}

func (s *Session) Update(args ProductArgs, reply *catalog.Product) error {
	if err := s.authorized(); err != nil {
		return s.refuse("update", err)
	}
	p, err := s.server.Catalog.Update(args.ID, args.Name, args.Price)
	if err != nil {
		return s.refuse("update", err)
	}
	s.server.log(s.conn, "updated #%d to %s at %s", p.ID, p.Name, catalog.FormatPrice(p.Price))
	*reply = p
	return nil
}

func (s *Session) Delete(args IDArgs, reply *Message) error {
	if err := s.authorized(); err != nil {
		return s.refuse("delete", err)
	}
	if err := s.server.Catalog.Delete(args.ID); err != nil {
		return s.refuse("delete", err)
	}
	s.server.log(s.conn, "deleted #%d", args.ID)
	reply.Text = fmt.Sprintf("deleted #%d", args.ID)
	return nil
}

func (s *Session) List(_ Message, reply *ListReply) error {
	if err := s.authorized(); err != nil {
		return s.refuse("list", err)
	}
	reply.Products = s.server.Catalog.List()
	s.server.log(s.conn, "listed %d products", len(reply.Products))
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
