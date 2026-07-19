package httpx

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Resolver looks up host addresses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DefaultResolver uses the system resolver.
type DefaultResolver struct{}

// LookupNetIP implements Resolver.
func (DefaultResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

// Config configures the safe HTTP client.
type Config struct {
	UserAgent       string
	ConnectTimeout  time.Duration
	TLSTimeout      time.Duration
	HeaderTimeout   time.Duration
	BodyTimeout     time.Duration
	TotalTimeout    time.Duration
	MaxHeaderBytes  int
	HeaderAllowlist map[string]struct{}
	Resolver        Resolver
	DialContext     func(ctx context.Context, network, address string) (net.Conn, error)
	// AllowAddrs optionally permits specific addresses that would otherwise be
	// rejected by the public egress policy. Production collectors must leave
	// this empty. Tests may inject loopback addresses for httptest.
	AllowAddrs []netip.Addr
}

// Client performs single-hop safe fetches.
type Client struct {
	cfg Config
}

// NewClient constructs a safe HTTP client configuration.
func NewClient(cfg Config) *Client {
	if cfg.Resolver == nil {
		cfg.Resolver = DefaultResolver{}
	}
	if cfg.HeaderAllowlist == nil {
		cfg.HeaderAllowlist = map[string]struct{}{}
	}
	return &Client{cfg: cfg}
}

// Request is a single-hop fetch request.
type Request struct {
	URL    string
	Method string
}

// Response is a bounded single-hop response.
type Response struct {
	StatusCode   int
	Headers      map[string][]string
	Body         []byte
	WireBytes    int64
	DecodedBytes int64
	Code         string
	Duration     time.Duration
}

// OK reports whether the response completed as an HTTP response.
func (r Response) OK() bool { return r.Code == "" }

// Do executes one GET/HEAD with controlled egress and body limits.
func (c *Client) Do(ctx context.Context, req Request, wireLimit, decodedLimit int64) Response {
	start := time.Now()
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method != "GET" && method != "HEAD" {
		return Response{Code: "method_denied", Duration: time.Since(start)}
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return Response{Code: "policy_denied", Duration: time.Since(start)}
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if !c.addrAllowed(ip) {
			return Response{Code: "address_denied", Duration: time.Since(start)}
		}
		return c.doPinned(ctx, req, u, host, port, ip, wireLimit, decodedLimit, start)
	}

	addrs, err := c.cfg.Resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		if ctx.Err() != nil {
			return Response{Code: codeFromCtx(ctx), Duration: time.Since(start)}
		}
		return Response{Code: "dns_failed", Duration: time.Since(start)}
	}
	addr, err := c.pickAddr(addrs)
	if err != nil {
		code := "address_denied"
		if len(addrs) == 0 {
			code = "dns_failed"
		}
		return Response{Code: code, Duration: time.Since(start)}
	}
	return c.doPinned(ctx, req, u, host, port, addr, wireLimit, decodedLimit, start)
}

func (c *Client) addrAllowed(a netip.Addr) bool {
	if a.Is4In6() {
		a = a.Unmap()
	}
	for _, allow := range c.cfg.AllowAddrs {
		aa := allow
		if aa.Is4In6() {
			aa = aa.Unmap()
		}
		if aa == a {
			return true
		}
	}
	return !IsUnsafeAddr(a)
}

func (c *Client) pickAddr(addrs []netip.Addr) (netip.Addr, error) {
	if len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("dns_failed")
	}
	// If any address is neither safe nor explicitly allowed, reject the set.
	for _, a := range addrs {
		if !c.addrAllowed(a) {
			return netip.Addr{}, fmt.Errorf("address_denied")
		}
	}
	return addrs[0], nil
}

func (c *Client) doPinned(
	ctx context.Context,
	req Request,
	u *url.URL,
	serverName, port string,
	addr netip.Addr,
	wireLimit, decodedLimit int64,
	start time.Time,
) Response {
	httpClient := c.buildClient(serverName, port, addr)
	httpReq, err := http.NewRequestWithContext(ctx, strings.ToUpper(req.Method), u.String(), nil)
	if err != nil {
		return Response{Code: "protocol_failed", Duration: time.Since(start)}
	}
	httpReq.Header.Set("User-Agent", c.cfg.UserAgent)
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("Accept-Encoding", "gzip, identity")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return classifyTransportErr(ctx, err, time.Since(start))
	}
	defer resp.Body.Close()

	if c.cfg.MaxHeaderBytes > 0 {
		n := 0
		for k, vs := range resp.Header {
			n += len(k)
			for _, v := range vs {
				n += len(v)
			}
		}
		if n > c.cfg.MaxHeaderBytes {
			return Response{Code: "header_too_large", Duration: time.Since(start)}
		}
	}

	headers := filterHeaders(resp.Header, c.cfg.HeaderAllowlist)
	if strings.EqualFold(req.Method, "HEAD") {
		return Response{
			StatusCode: resp.StatusCode,
			Headers:    headers,
			Duration:   time.Since(start),
		}
	}

	bodyCtx := ctx
	if c.cfg.BodyTimeout > 0 {
		var cancel context.CancelFunc
		bodyCtx, cancel = context.WithTimeout(ctx, c.cfg.BodyTimeout)
		defer cancel()
	}
	br := ReadBody(bodyCtx, resp.Body, resp.Header.Get("Content-Encoding"), wireLimit, decodedLimit)
	if br.Code != "" {
		return Response{
			StatusCode:   resp.StatusCode,
			Headers:      headers,
			WireBytes:    br.WireBytes,
			DecodedBytes: br.DecodedBytes,
			Code:         br.Code,
			Duration:     time.Since(start),
		}
	}
	return Response{
		StatusCode:   resp.StatusCode,
		Headers:      headers,
		Body:         br.Data,
		WireBytes:    br.WireBytes,
		DecodedBytes: br.DecodedBytes,
		Duration:     time.Since(start),
	}
}

func (c *Client) buildClient(serverName, port string, addr netip.Addr) *http.Client {
	baseDial := c.cfg.DialContext
	if baseDial == nil {
		d := &net.Dialer{Timeout: c.cfg.ConnectTimeout, KeepAlive: 30 * time.Second}
		baseDial = d.DialContext
	}
	pinned := net.JoinHostPort(addr.String(), port)
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		if !c.addrAllowed(addr) {
			return nil, fmt.Errorf("address_denied")
		}
		return baseDial(ctx, network, pinned)
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          32,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   c.cfg.TLSTimeout,
		ResponseHeaderTimeout: c.cfg.HeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext:           dial,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		},
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: c.cfg.TotalTimeout,
		Jar:     nil,
	}
}

func filterHeaders(h http.Header, allow map[string]struct{}) map[string][]string {
	out := make(map[string][]string)
	if len(allow) == 0 {
		return out
	}
	for k, vs := range h {
		lk := strings.ToLower(k)
		if _, ok := allow[lk]; ok {
			out[lk] = append([]string(nil), vs...)
		}
	}
	return out
}

func classifyTransportErr(ctx context.Context, err error, d time.Duration) Response {
	if ctx.Err() != nil {
		return Response{Code: codeFromCtx(ctx), Duration: d}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "address_denied"):
		return Response{Code: "address_denied", Duration: d}
	case strings.Contains(msg, "tls"), strings.Contains(msg, "certificate"):
		return Response{Code: "tls_failed", Duration: d}
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return Response{Code: "request_timeout", Duration: d}
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "connect"):
		return Response{Code: "connect_failed", Duration: d}
	default:
		return Response{Code: "transport_failed", Duration: d}
	}
}
