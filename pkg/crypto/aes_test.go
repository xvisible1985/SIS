package crypto_test

import (
	"strings"
	"testing"

	"sis/pkg/crypto"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func TestEncryptDecrypt(t *testing.T) {
	plain := "my-secret-api-key"
	enc, err := crypto.Encrypt(plain, testKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == plain {
		t.Fatal("encrypted text must differ from plaintext")
	}
	got, err := crypto.Decrypt(enc, testKey)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q", got, plain)
	}
}

func TestEncrypt_Nondeterministic(t *testing.T) {
	plain := "key"
	a, _ := crypto.Encrypt(plain, testKey)
	b, _ := crypto.Encrypt(plain, testKey)
	if a == b {
		t.Error("two encryptions of same plaintext must produce different ciphertext")
	}
}

func TestDecrypt_BadKey(t *testing.T) {
	enc, _ := crypto.Encrypt("hello", testKey)
	badKey := strings.Repeat("ff", 32)
	_, err := crypto.Decrypt(enc, badKey)
	if err == nil {
		t.Error("expected error with wrong key")
	}
}

func TestEncrypt_InvalidKey(t *testing.T) {
	_, err := crypto.Encrypt("hello", "tooshort")
	if err == nil {
		t.Error("expected error with invalid key")
	}
}
