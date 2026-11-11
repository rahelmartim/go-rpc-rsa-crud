// Package client calls the catalog service with net/rpc. Every request is
// sealed with the public key of the server, and every reply is opened with
// the private key of the client.
package client

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"time"

	"github.com/rahelmartim/go-rpc-rsa-crud/catalog"
	"github.com/rahelmartim/go-rpc-rsa-crud/keys"
	"github.com/rahelmartim/go-rpc-rsa-crud/service"
)

// Call describes one finished call, for a trace.
type Call struct {
	Method      string
	Request     []byte   // the plaintext of the request
	Sealed      [][]byte // the request as it went on the wire
	ReplySealed [][]byte // the reply as it came from the wire
	Reply       []byte   // the plaintext of the reply
	Err         error
	Took        time.Duration
}

// Client is one connection to the server.
type Client struct {
	rpc *rpc.Client
	// Key is the key pair of the client.
	Key *rsa.PrivateKey
	// ServerKey is the public key that the server sent in the handshake.
	ServerKey *rsa.PublicKey
	// Trace, if set, receives every call after its reply.
	Trace func(Call)
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
	err := c.rpc.Call(service.Name+"."+method, args, reply)
	var se rpc.ServerError
	if errors.As(err, &se) {
		// The server sends errors as text. Keep the text, without the type.
		err = errors.New(string(se))
	}
	return err
}

func (c *Client) trace(call Call) {
	if c.Trace != nil {
		c.Trace(call)
	}
}

// Handshake sends the public key of the client and keeps the key of the
// server. Public keys are not secret, so the handshake is not sealed.
func (c *Client) Handshake() error {
	start := time.Now()
	der, err := keys.Marshal(&c.Key.PublicKey)
	if err != nil {
		return err
	}
	var reply service.HandshakeReply
	err = c.call("Handshake", service.HandshakeArgs{ClientKey: der}, &reply)
	var pub *rsa.PublicKey
	if err == nil {
		pub, err = keys.Parse(reply.ServerKey)
	}
	call := Call{
		Method: "Handshake", Err: err, Took: time.Since(start),
		Request: []byte(fmt.Sprintf("public key of the client, %d bytes", len(der))),
	}
	if pub != nil {
		call.Reply = []byte(fmt.Sprintf("public key of the server, %d bytes", len(reply.ServerKey)))
	}
	c.trace(call)
	if err != nil {
		return err
	}
	c.ServerKey = pub
	return nil
}

// send seals a request, calls the method, and opens the reply.
func (c *Client) send(method string, req service.Request) (service.Result, error) {
	var result service.Result
	if c.ServerKey == nil {
		return result, service.ErrNoHandshake
	}
	start := time.Now()
	call := Call{Method: method}
	defer func() {
		call.Took = time.Since(start)
		c.trace(call)
	}()

	call.Request, _ = json.Marshal(req)
	sealed, err := keys.Seal(c.ServerKey, call.Request)
	if err != nil {
		call.Err = err
		return result, err
	}
	call.Sealed = sealed
	var reply service.Sealed
	if err := c.call(method, service.Sealed{Blocks: sealed}, &reply); err != nil {
		call.Err = err
		return result, err
	}
	call.ReplySealed = reply.Blocks
	plain, err := keys.Open(c.Key, reply.Blocks)
	if err != nil {
		call.Err = errors.New("the client cannot open the reply with its key")
		return result, call.Err
	}
	call.Reply = plain
	if err := json.Unmarshal(plain, &result); err != nil {
		call.Err = errors.New("the reply is not valid JSON")
		return result, call.Err
	}
	if result.Error != "" {
		call.Err = errors.New(result.Error)
		return result, call.Err
	}
	return result, nil
}

func product(r service.Result, err error) (catalog.Product, error) {
	if err != nil {
		return catalog.Product{}, err
	}
	if r.Product == nil {
		return catalog.Product{}, errors.New("the reply has no product")
	}
	return *r.Product, nil
}

func (c *Client) Create(name, price string) (catalog.Product, error) {
	return product(c.send("Create", service.Request{Name: name, Price: price}))
}

func (c *Client) Read(id int) (catalog.Product, error) {
	return product(c.send("Read", service.Request{ID: id}))
}

func (c *Client) Update(id int, name, price string) (catalog.Product, error) {
	return product(c.send("Update", service.Request{ID: id, Name: name, Price: price}))
}

func (c *Client) Delete(id int) error {
	_, err := c.send("Delete", service.Request{ID: id})
	return err
}

func (c *Client) List() ([]catalog.Product, error) {
	r, err := c.send("List", service.Request{})
	return r.Products, err
}

func (c *Client) Close() error {
	return c.rpc.Close()
}
