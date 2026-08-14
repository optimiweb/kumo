package crawl

import "testing"

func TestSourceCodeValidate(t *testing.T) {
	valid := []SourceCode{
		SourceSeed, SourceLink, SourceRedirect, SourceSitemap,
		SourceRobots, SourceCanonical, SourceHreflang, SourceLog,
	}
	for _, s := range valid {
		if err := s.Validate(); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	if err := SourceCode("unknown").Validate(); err == nil {
		t.Fatal("unknown source should fail")
	}
}
