package crawl

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// TargetPolicy admits URLs before network activity.
type TargetPolicy interface {
	Admit(rawURL string, method Method) (ErrorCode, error)
}

// TargetPolicyFunc adapts a function to TargetPolicy.
type TargetPolicyFunc func(rawURL string, method Method) (ErrorCode, error)

// Admit implements TargetPolicy.
func (f TargetPolicyFunc) Admit(rawURL string, method Method) (ErrorCode, error) {
	return f(rawURL, method)
}

// BaselinePolicy enforces scheme, method, port, and userinfo/fragment rules.
type BaselinePolicy struct {
	AllowedPorts map[int]struct{}
	AllowedHosts map[string]struct{} // empty means any host structurally allowed
	PathPrefix   string
}

// DefaultBaselinePolicy returns HTTP(S) on 80/443 only.
func DefaultBaselinePolicy() BaselinePolicy {
	return BaselinePolicy{
		AllowedPorts: map[int]struct{}{80: {}, 443: {}},
	}
}

// Admit implements TargetPolicy.
func (p BaselinePolicy) Admit(rawURL string, method Method) (ErrorCode, error) {
	if err := method.Validate(); err != nil {
		return CodeMethodDenied, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return CodePolicyDenied, nil
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return CodeSchemeDenied, nil
	}
	if u.User != nil {
		return CodePolicyDenied, nil
	}
	if u.Fragment != "" {
		return CodePolicyDenied, nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return CodeHostDenied, nil
	}
	if len(p.AllowedHosts) > 0 {
		if _, ok := p.AllowedHosts[host]; !ok {
			return CodeHostDenied, nil
		}
	}
	port := u.Port()
	var portNum int
	if port == "" {
		if scheme == "https" {
			portNum = 443
		} else {
			portNum = 80
		}
	} else {
		portNum, err = strconv.Atoi(port)
		if err != nil {
			return CodePortDenied, nil
		}
	}
	if len(p.AllowedPorts) > 0 {
		if _, ok := p.AllowedPorts[portNum]; !ok {
			return CodePortDenied, nil
		}
	}
	if p.PathPrefix != "" {
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		if !strings.HasPrefix(path, p.PathPrefix) {
			return CodePathDenied, nil
		}
	}
	return CodeNone, nil
}

// HostPolicy restricts hosts to an allowlist.
func HostPolicy(hosts ...string) BaselinePolicy {
	p := DefaultBaselinePolicy()
	p.AllowedHosts = make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		p.AllowedHosts[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}
	return p
}

// IsUnsafeIP reports whether addr is a disallowed destination for public
// crawl egress.
func IsUnsafeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		return (ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127) ||
			(ip4[0] == 192 && ip4[1] == 0 && (ip4[2] == 0 || ip4[2] == 2)) ||
			(ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19 || (ip4[1] == 51 && ip4[2] == 100))) ||
			(ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113)
	}
	return len(ip) >= 4 && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 || len(ip) >= 1 && (ip[0]&0xfe) == 0xfc
}
