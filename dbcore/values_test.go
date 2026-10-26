package dbcore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestValueContracts(t *testing.T) {
	if SHA256DigestValue([]byte("abc")) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatal("digest")
	}
	for _, value := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 64)} {
		if ValidateDigestValue(value) == nil {
			t.Fatal(value)
		}
	}
	if !VerifyDigestValue([]byte("abc"), SHA256DigestValue([]byte("abc"))) || VerifyDigestValue([]byte("ab"), SHA256DigestValue([]byte("abc"))) {
		t.Fatal("verification")
	}
	for _, value := range []string{"", "a.b", "bad-name", strings.Repeat("a", 64)} {
		if ValidateIdentifier(value) == nil {
			t.Fatal(value)
		}
	}
	if QuoteIdentifier(`a"b`) != `"a""b"` {
		t.Fatal("quoting")
	}
	if _, err := AllocateUUID7Many(context.Background(), nil, -1); err == nil {
		t.Fatal("negative allocation")
	}
	if got, err := AllocateUUID7Many(context.Background(), nil, 0); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
func TestConnectionEncoding(t *testing.T) {
	c := Config{Host: "::1", Port: 5432, User: "a@b", Password: "a/b?", Database: "test", SSLMode: "require"}
	dsn, err := c.DSN()
	if err != nil || !strings.Contains(dsn, "a%40b:a%2Fb%3F@[::1]:5432") {
		t.Fatal(dsn, err)
	}
	c.SSLMode = "invalid"
	if _, err = c.DSN(); err == nil {
		t.Fatal("ssl mode accepted")
	}
	if !errors.Is(ErrSchemaVersion, ErrSchemaVersion) {
		t.Fatal("sentinel")
	}
}
