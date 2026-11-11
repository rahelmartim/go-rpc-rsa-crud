// Package client calls the catalog service with net/rpc.
package client

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/rpc"
	"time"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
	"github.com/rahelmartim/go-rpc-rsa-crud/service"
)

// Client is one connection to the server.
type Client struct {
	rpc *rpc.Client
	// Key is the key pair of the client.
	Key *rsa.PrivateKey
	// ServerKey is the public key that the server sent in the handshake.
	ServerKey *rsa.PublicKey
	// LastLogin is the last login message that Login sent.
	LastLogin Sealed
	// Trace, if set, receives every call after its reply.
	Trace func(method string, args, reply any, err error, took time.Duration)
}

// Dial connects to a server over TCP and does the handshake.
func Dial(addr string, key *rsa.PrivateKey) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	c := New(conn, key)
	if err := c.Handshake(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// New uses a connection that is already open. Call Handshake before the
// other methods.
func New(conn io.ReadWriteCloser, key *rsa.PrivateKey) *Client {
	return &Client{rpc: rpc.NewClient(conn), Key: key}
}

func (c *Client) call(method string, args, reply any) error {
	start := time.Now()
	err := c.rpc.Call(service.Name+"."+method, args, reply)
	var se rpc.ServerError
	if errors.As(err, &se) {
		// The server sends errors as text. Keep the text, without the type.
		err = errors.New(string(se))
	}
	if c.Trace != nil {
		c.Trace(method, args, reply, err, time.Since(start))
	}
	return err
}

// Handshake sends the public key of the client and keeps the key of the
// server.
func (c *Client) Handshake() error {
	der, err := keys.Marshal(&c.Key.PublicKey)
	if err != nil {
		return err
	}
	var reply service.HandshakeReply
	if err := c.call("Handshake", service.HandshakeArgs{ClientKey: der}, &reply); err != nil {
		return err
	}
	pub, err := keys.Parse(reply.ServerKey)
	if err != nil {
		return err
	}
	c.ServerKey = pub
	return nil
}

// Sealed is a login message: its plaintext, and the ciphertext that goes
// on the wire.
type Sealed struct {
	Plaintext  []byte
	Ciphertext []byte
}

// Seal encrypts the credentials with the public key of the server. Only
// the private key of the server can open them.
func (c *Client) Seal(user, password string) (Sealed, error) {
	if c.ServerKey == nil {
		return Sealed{}, service.ErrNoHandshake
	}
	plain, err := json.Marshal(service.Credentials{User: user, Password: password})
	if err != nil {
		return Sealed{}, err
	}
	ct, err := keys.Encrypt(c.ServerKey, plain)
	if err != nil {
		return Sealed{}, errors.New("the user and the password are too long to encrypt")
	}
	return Sealed{Plaintext: plain, Ciphertext: ct}, nil
}

// SendLogin sends a sealed login message.
func (c *Client) SendLogin(sealed Sealed) (string, error) {
	var reply service.Message
	err := c.call("Login", service.LoginArgs{Ciphertext: sealed.Ciphertext}, &reply)
	return reply.Text, err
}

// Login seals the credentials and sends them.
func (c *Client) Login(user, password string) error {
	sealed, err := c.Seal(user, password)
	if err != nil {
		return err
	}
	c.LastLogin = sealed
	_, err = c.SendLogin(sealed)
	return err
}

func (c *Client) Logout() error {
	return c.call("Logout", service.Message{}, &service.Message{})
}

func (c *Client) Create(name, price string) (catalog.Product, error) {
	var p catalog.Product
	err := c.call("Create", service.ProductArgs{Name: name, Price: price}, &p)
	return p, err
}

func (c *Client) Read(id int) (catalog.Product, error) {
	var p catalog.Product
	err := c.call("Read", service.IDArgs{ID: id}, &p)
	return p, err
}

func (c *Client) Update(id int, name, price string) (catalog.Product, error) {
	var p catalog.Product
	err := c.call("Update", service.ProductArgs{ID: id, Name: name, Price: price}, &p)
	return p, err
}

func (c *Client) Delete(id int) error {
	return c.call("Delete", service.IDArgs{ID: id}, &service.Message{})
}

func (c *Client) List() ([]catalog.Product, error) {
	var reply service.ListReply
	err := c.call("List", service.Message{}, &reply)
	return reply.Products, err
}

func (c *Client) Close() error {
	return c.rpc.Close()
}
