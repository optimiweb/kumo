package httpx

import (
	"bytes"
	"compress/gzip"
	"context"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestReadBodyWireLimit(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 50)
	res := ReadBody(context.Background(), bytes.NewReader(data), "", 40, 1000)
	if res.Code != "wire_body_too_large" {
		t.Fatalf("code=%s", res.Code)
	}
}

func TestReadBodyGzipDecodedLimit(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(bytes.Repeat([]byte("x"), 5000))
	_ = zw.Close()
	res := ReadBody(context.Background(), bytes.NewReader(buf.Bytes()), "gzip", 10000, 100)
	if res.Code != "decoded_body_too_large" {
		t.Fatalf("code=%s wire=%d decoded=%d", res.Code, res.WireBytes, res.DecodedBytes)
	}
}

func TestReadBodyBrotliOK(t *testing.T) {
	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	original := "Hello Brotli World! Fast and modern compression."
	_, _ = bw.Write([]byte(original))
	_ = bw.Close()

	res := ReadBody(context.Background(), bytes.NewReader(buf.Bytes()), "br", 10000, 1000)
	if res.Code != "" || string(res.Data) != original {
		t.Fatalf("got code=%s, data=%q, want %q", res.Code, string(res.Data), original)
	}
}

func TestReadBodyBrotliDecodedLimit(t *testing.T) {
	var buf bytes.Buffer
	bw := brotli.NewWriter(&buf)
	_, _ = bw.Write(bytes.Repeat([]byte("b"), 5000))
	_ = bw.Close()
	res := ReadBody(context.Background(), bytes.NewReader(buf.Bytes()), "br", 10000, 100)
	if res.Code != "decoded_body_too_large" {
		t.Fatalf("code=%s wire=%d decoded=%d", res.Code, res.WireBytes, res.DecodedBytes)
	}
}

func TestReadBodyExactLimitOK(t *testing.T) {
	data := []byte("hello")
	res := ReadBody(context.Background(), bytes.NewReader(data), "identity", 5, 5)
	if res.Code != "" || string(res.Data) != "hello" {
		t.Fatalf("%+v", res)
	}
}

func TestReadBodyUnsupportedEncoding(t *testing.T) {
	res := ReadBody(context.Background(), strings.NewReader("x"), "deflate", 100, 100)
	if res.Code != "unsupported_content_encoding" {
		t.Fatalf("code=%s", res.Code)
	}
}
