package engine

import "github.com/optimiweb/kumo/crawl"

func SourceFromRelation(r crawl.DiscoveryRelation) crawl.SourceCode {
	return sourceFromRelation(r)
}
