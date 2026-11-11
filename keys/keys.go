// Package keys has the RSA operations of the program: key pairs, the key
// exchange format, and messages sealed with RSA-OAEP and SHA-256.
package keys

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
)

// Bits is the size of every key that this program makes.
const Bits = 2048

// MinBits is the smallest key that a peer can send.
const MinBits = 2048

// The label binds a ciphertext to its use: a message of this program.
var label = []byte("go-rpc-rsa-crud message")

// MaxBlocks limits the size of one sealed message.
const MaxBlocks = 2048

var (
	ErrNotRSA     = errors.New("the key is not an RSA public key")
	ErrWeakKey    = fmt.Errorf("the key is smaller than %d bits", MinBits)
	ErrTooLarge   = errors.New("the message is too large to seal")
	ErrCannotOpen = errors.New("the message cannot be opened with this key")
)

// Generate makes a new key pair.
func Generate() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, Bits)
}

// Marshal writes a public key in the PKIX (DER) format.
func Marshal(pub *rsa.PublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(pub)
}

// Parse reads a public key that a peer sent, and refuses weak keys.
func Parse(der []byte) (*rsa.PublicKey, error) {
	if len(der) == 0 || len(der) > 4096 {
		return nil, ErrNotRSA
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, ErrNotRSA
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, ErrNotRSA
	}
	if pub.N.BitLen() < MinBits {
		return nil, ErrWeakKey
	}
	return pub, nil
}

// Fingerprint is the start of the SHA-256 of the key, such as
// "3f:a1:09:…". People compare fingerprints to know that two keys are
// the same.
func Fingerprint(pub *rsa.PublicKey) string {
	der, err := Marshal(pub)
	if err != nil {
		return "?"
	}
	sum := sha256.Sum256(der)
	parts := make([]string, 12)
	for i := range parts {
		parts[i] = fmt.Sprintf("%02x", sum[i])
	}
	return strings.Join(parts, ":")
}

// Encrypt encrypts a short message for the owner of the public key.
func Encrypt(pub *rsa.PublicKey, message []byte) ([]byte, error) {
	return rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, message, label)
}

// Decrypt opens a message with the private key.
func Decrypt(priv *rsa.PrivateKey, ciphertext []byte) ([]byte, error) {
	return rsa.DecryptOAEP(sha256.New(), nil, priv, ciphertext, label)
}

// BlockSize is the largest part of a message that one RSA-OAEP block can
// hold: 190 bytes for a 2048-bit key.
func BlockSize(pub *rsa.PublicKey) int {
	return pub.Size() - 2*sha256.Size - 2
}

// Seal encrypts a message of any size for the owner of the public key. It
// cuts the message into parts, and encrypts each part as one block.
func Seal(pub *rsa.PublicKey, message []byte) ([][]byte, error) {
	size := BlockSize(pub)
	n := max(1, (len(message)+size-1)/size)
	if n > MaxBlocks {
		return nil, ErrTooLarge
	}
	blocks := make([][]byte, 0, n)
	for i := 0; i < n; i++ {
		part := message[i*size : min(len(message), (i+1)*size)]
		block, err := Encrypt(pub, part)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

// Open decrypts the blocks of Seal with the private key.
func Open(priv *rsa.PrivateKey, blocks [][]byte) ([]byte, error) {
	if len(blocks) == 0 || len(blocks) > MaxBlocks {
		return nil, ErrCannotOpen
	}
	var message []byte
	for _, block := range blocks {
		if len(block) != priv.Size() {
			return nil, ErrCannotOpen
		}
		part, err := Decrypt(priv, block)
		if err != nil {
			return nil, ErrCannotOpen
		}
		message = append(message, part...)
	}
	return message, nil
}
