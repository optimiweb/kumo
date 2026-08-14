package engine

import (
	"testing"

	"github.com/optimiweb/kumo/crawl"
)

func TestSourceFromRelation(t *testing.T) {
	cases := []struct {
		rel  crawl.DiscoveryRelation
		want crawl.SourceCode
	}{
		{crawl.RelationRedirect, crawl.SourceRedirect},
		{crawl.RelationSitemap, crawl.SourceSitemap},
		{crawl.RelationRobotsSitemap, crawl.SourceSitemap},
		{crawl.RelationCanonical, crawl.SourceCanonical},
		{crawl.RelationHreflang, crawl.SourceHreflang},
		{crawl.RelationLog, crawl.SourceLog},
		{crawl.RelationLink, crawl.SourceLink},
		{crawl.RelationUnspecified, crawl.SourceLink},
		{crawl.DiscoveryRelation(255), crawl.SourceLink},
	}
	for _, tc := range cases {
		if got := SourceFromRelation(tc.rel); got != tc.want {
			t.Fatalf("sourceFromRelation(%v) = %q, want %q", tc.rel, got, tc.want)
		}
	}
}
