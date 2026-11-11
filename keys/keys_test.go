package keys

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"regexp"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if key.N.BitLen() != Bits {
		t.Fatalf("the key has %d bits", key.N.BitLen())
	}
	message := []byte(`{"user":"admin","password":"chatuba"}`)
	ct, err := Encrypt(&key.PublicKey, message)
	if err != nil {
		t.Fatal(err)
	}
	if len(ct) != key.Size() || bytes.Contains(ct, []byte("chatuba")) {
		t.Fatalf("the ciphertext has %d bytes or shows the password", len(ct))
	}
	again, _ := Encrypt(&key.PublicKey, message)
	if bytes.Equal(ct, again) {
		t.Fatal("OAEP is random: two encryptions must differ")
	}
	plain, err := Decrypt(key, ct)
	if err != nil || !bytes.Equal(plain, message) {
		t.Fatalf("Decrypt = %q, %v", plain, err)
	}

	other, _ := Generate()
	if _, err := Decrypt(other, ct); err == nil {
		t.Fatal("another key opened the message")
	}
	ct[10] ^= 1
	if _, err := Decrypt(key, ct); err == nil {
		t.Fatal("a changed ciphertext was opened")
	}
}

func TestOtherLabelFails(t *testing.T) {
	key, _ := Generate()
	ct, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, &key.PublicKey, []byte("hi"), []byte("another program"))
	if _, err := Decrypt(key, ct); err == nil {
		t.Fatal("a message for another use was opened")
	}
}

func TestMarshalParse(t *testing.T) {
	key, _ := Generate()
	der, err := Marshal(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := Parse(der)
	if err != nil || pub.N.Cmp(key.N) != 0 || pub.E != key.E {
		t.Fatalf("Parse = %v, %v", pub, err)
	}
}

func TestParseRefusesBadKeys(t *testing.T) {
	if _, err := Parse(nil); !errors.Is(err, ErrNotRSA) {
		t.Errorf("empty: %v", err)
	}
	if _, err := Parse([]byte("not a key")); !errors.Is(err, ErrNotRSA) {
		t.Errorf("junk: %v", err)
	}
	if _, err := Parse(make([]byte, 5000)); !errors.Is(err, ErrNotRSA) {
		t.Errorf("too long: %v", err)
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	if _, err := Parse(der); !errors.Is(err, ErrNotRSA) {
		t.Errorf("ECDSA key: %v", err)
	}
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	der, _ = Marshal(&small.PublicKey)
	if _, err := Parse(der); !errors.Is(err, ErrWeakKey) {
		t.Errorf("1024-bit key: %v", err)
	}
}

func TestFingerprint(t *testing.T) {
	a, _ := Generate()
	b, _ := Generate()
	fa := Fingerprint(&a.PublicKey)
	if !regexp.MustCompile(`^([0-9a-f]{2}:){11}[0-9a-f]{2}$`).MatchString(fa) {
		t.Fatalf("fingerprint %q", fa)
	}
	if fa != Fingerprint(&a.PublicKey) || fa == Fingerprint(&b.PublicKey) {
		t.Fatal("a fingerprint must be the same for one key and differ between keys")
	}
}
