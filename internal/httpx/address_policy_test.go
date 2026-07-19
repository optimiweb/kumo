package httpx

import (
	"net/netip"
	"testing"
)

func TestUnsafeAddrs(t *testing.T) {
	cases := []string{"127.0.0.1", "::1", "10.0.0.1", "192.168.1.1", "169.254.1.1", "fc00::1", "2001:db8::1"}
	for _, c := range cases {
		if !IsUnsafeAddr(netip.MustParseAddr(c)) {
			t.Fatalf("%s should be unsafe", c)
		}
	}
	if IsUnsafeAddr(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public should be safe")
	}
}

func TestMixedAnswersRejected(t *testing.T) {
	_, err := PickFirstSafe([]netip.Addr{
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("10.0.0.1"),
	})
	if err == nil {
		t.Fatal("expected rejection")
	}
}
