package crawl

import (
	"context"
	"crypto/sha256"
	"net/url"
	"testing"
)

func TestURLIdentityHostPath(t *testing.T) {
	const raw = "https://Example.com:443/a/b?q=1"
	id, err := NewURLIdentity(IdentityKey{1}, raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if id.Host() != parsed.Hostname() {
		t.Fatalf("Host() = %q, want %q", id.Host(), parsed.Hostname())
	}
	if id.Path() != "/a/b" {
		t.Fatalf("Path() = %q, want /a/b", id.Path())
	}
}

func TestURLIdentityEmptyPath(t *testing.T) {
	id, err := NewURLIdentity(IdentityKey{2}, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if id.Path() != "/" {
		t.Fatalf("Path() = %q, want /", id.Path())
	}
}

func TestDefaultIdentityKeyUnchanged(t *testing.T) {
	res, err := DefaultIdentity(context.Background(), IdentityRequest{
		RawURL: "https://Example.com:443/a/b?q=1",
		Method: MethodGET,
		Source: SourceSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != IdentityAccepted {
		t.Fatalf("state = %v", res.State)
	}
	canonical, err := CanonicalFetchURL("https://Example.com:443/a/b?q=1")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(string(MethodGET) + "\n" + canonical))
	if res.Identity.Key() != want {
		t.Fatal("DefaultIdentity key changed")
	}
	if res.Identity.Host() == "" || res.Identity.Path() != "/a/b" {
		t.Fatalf("host=%q path=%q", res.Identity.Host(), res.Identity.Path())
	}
}
