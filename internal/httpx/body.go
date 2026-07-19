package httpx

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
)

// BodyResult is a bounded decoded body.
type BodyResult struct {
	Data         []byte
	WireBytes    int64
	DecodedBytes int64
	Code         string
}

// ReadBody reads and decodes a response body with independent limits.
func ReadBody(ctx context.Context, r io.Reader, contentEncoding string, wireLimit, decodedLimit int64) BodyResult {
	if wireLimit <= 0 || decodedLimit <= 0 {
		return BodyResult{Code: "protocol_failed"}
	}
	wireLR := io.LimitReader(r, wireLimit+1)
	compressed, err := readAllContext(ctx, wireLR, wireLimit+1)
	if err != nil {
		if ctx.Err() != nil {
			return BodyResult{Code: codeFromCtx(ctx), WireBytes: int64(len(compressed))}
		}
		return BodyResult{Code: "transport_failed", WireBytes: int64(len(compressed))}
	}
	if int64(len(compressed)) > wireLimit {
		return BodyResult{Code: "wire_body_too_large", WireBytes: int64(len(compressed))}
	}
	wireN := int64(len(compressed))

	enc := strings.ToLower(strings.TrimSpace(contentEncoding))
	var decoded []byte
	switch enc {
	case "", "identity":
		if wireN > decodedLimit {
			return BodyResult{Code: "decoded_body_too_large", WireBytes: wireN}
		}
		decoded = compressed
	case "gzip", "x-gzip":
		gr, err := gzip.NewReader(strings.NewReader(string(compressed)))
		if err != nil {
			return BodyResult{Code: "content_decode_failed", WireBytes: wireN}
		}
		defer gr.Close()
		decoded, err = readAllContext(ctx, io.LimitReader(gr, decodedLimit+1), decodedLimit+1)
		if err != nil {
			if ctx.Err() != nil {
				return BodyResult{Code: codeFromCtx(ctx), WireBytes: wireN}
			}
			return BodyResult{Code: "content_decode_failed", WireBytes: wireN}
		}
		if int64(len(decoded)) > decodedLimit {
			return BodyResult{Code: "decoded_body_too_large", WireBytes: wireN, DecodedBytes: int64(len(decoded))}
		}
	default:
		return BodyResult{Code: "unsupported_content_encoding", WireBytes: wireN}
	}
	return BodyResult{
		Data:         decoded,
		WireBytes:    wireN,
		DecodedBytes: int64(len(decoded)),
	}
}

func readAllContext(ctx context.Context, r io.Reader, max int64) ([]byte, error) {
	buf := make([]byte, 0, 512)
	tmp := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return buf, err
		}
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if int64(len(buf)) > max {
				return buf, fmt.Errorf("limit exceeded")
			}
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
}

func codeFromCtx(ctx context.Context) string {
	if ctx.Err() == context.DeadlineExceeded {
		return "request_timeout"
	}
	return "cancelled"
}
