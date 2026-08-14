package crawl

import "testing"

func TestRelationLogString(t *testing.T) {
	if got := RelationLog.String(); got != "log" {
		t.Fatalf("RelationLog.String() = %q, want log", got)
	}
}
