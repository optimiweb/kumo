package crawl

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithCommitRoundTrip(t *testing.T) {
	fn := func(context.Context) error { return errors.New("commit") }
	d := Ack().WithCommit(fn)
	if d.Kind() != DecisionAck {
		t.Fatalf("kind = %v, want Ack", d.Kind())
	}
	got := d.Commit()
	if got == nil {
		t.Fatal("Commit() = nil")
	}
	if err := got(context.Background()); err == nil || err.Error() != "commit" {
		t.Fatalf("Commit()() = %v", err)
	}
	if Ack().Commit() != nil {
		t.Fatal("Ack() should have a nil Commit")
	}
}

func TestDecisionValidateIgnoresCommit(t *testing.T) {
	if err := Ack().Validate(); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if err := Ack().WithCommit(func(context.Context) error { return errors.New("x") }).Validate(); err != nil {
		t.Fatalf("Ack+Commit: %v", err)
	}
	if err := Retry(time.Second, CodeTransportFailed).Validate(); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if err := Fail(CodeHandlerFailed).Validate(); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if err := DefaultDecision(NewFetchResult(FetchOutcomeHTTPResponse, CodeNone, "https://example.com/", 0)).Validate(); err != nil {
		t.Fatalf("DefaultDecision: %v", err)
	}
	var zero Decision
	if err := zero.Validate(); err == nil {
		t.Fatal("zero decision should fail")
	}
}
