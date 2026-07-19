//go:build integration

package integration_test

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/optimiweb/kumo/test/fixtures"
)

const exampleHost = "example.com"
const exampleOrigin = "http://example.com"

// fixtureSite serves test/fixtures/example.com behind httptest while crawl
// URLs stay on http://example.com (resolved and dialed to the test server).
type fixtureSite struct {
	Server *httptest.Server
	root   fs.FS

	mu   sync.Mutex
	hits map[string]int
}

func startExampleCom(t *testing.T) *fixtureSite {
	t.Helper()
	root, err := fs.Sub(fixtures.ExampleCom, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	site := &fixtureSite{root: root, hits: make(map[string]int)}
	site.Server = httptest.NewServer(http.HandlerFunc(site.serve))
	t.Cleanup(site.Server.Close)
	return site
}

func (s *fixtureSite) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "" {
		p = "/"
	}

	s.mu.Lock()
	s.hits[p]++
	s.mu.Unlock()

	switch p {
	case "/moved", "/moved/":
		http.Redirect(w, r, "/new-home", http.StatusFound)
		return
	case "/gone", "/gone/":
		http.NotFound(w, r)
		return
	case "/error", "/error/":
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	content, ctype, ok := s.read(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(content)
}

func (s *fixtureSite) Hits(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func (s *fixtureSite) HitPaths() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.hits))
	for k, v := range s.hits {
		out[k] = v
	}
	return out
}

func (s *fixtureSite) read(urlPath string) ([]byte, string, bool) {
	for _, rel := range fixtureCandidates(urlPath) {
		b, err := fs.ReadFile(s.root, rel)
		if err != nil {
			continue
		}
		return b, contentType(rel), true
	}
	return nil, "", false
}

func fixtureCandidates(urlPath string) []string {
	p := path.Clean("/" + urlPath)
	p = strings.TrimPrefix(p, "/")
	if p == "." || p == "" {
		return []string{"index.html"}
	}

	var out []string
	out = append(out, p)
	if !strings.Contains(path.Base(p), ".") {
		out = append(out, p+".html")
		out = append(out, path.Join(p, "index.html"))
	}
	if strings.HasSuffix(urlPath, "/") {
		out = append(out, path.Join(p, "index.html"))
	}
	return out
}

func contentType(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(rel, ".xml"):
		return "application/xml"
	case strings.HasSuffix(rel, ".txt"):
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
