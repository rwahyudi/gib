package ibcli

import (
	"strings"
	"testing"
)

func TestFernetEncryptDecryptRoundTrip(t *testing.T) {
	key, err := generateFernetKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	token, err := encryptFernet(key, "secret-password")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(token, encryptedPasswordPrefix) {
		t.Fatalf("token prefix = %q", token)
	}
	plaintext, err := decryptFernet(key, token)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plaintext != "secret-password" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}
