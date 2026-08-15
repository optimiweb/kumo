package crawl

import "time"

// EventListener receives lifecycle observability callbacks during a crawl.
type EventListener interface {
	OnFetchStart(work Work)
	OnFetchComplete(work Work, res FetchResult)
	OnWorkTransition(work Work, tres TransitionResult)
	OnOriginThrottled(origin string, delay time.Duration)
}

// NullEventListener is a no-op implementation of EventListener.
type NullEventListener struct{}

func (NullEventListener) OnFetchStart(Work)                       {}
func (NullEventListener) OnFetchComplete(Work, FetchResult)       {}
func (NullEventListener) OnWorkTransition(Work, TransitionResult) {}
func (NullEventListener) OnOriginThrottled(string, time.Duration) {}

// EventFuncs allows selectively implementing EventListener callbacks.
type EventFuncs struct {
	FetchStart      func(work Work)
	FetchComplete   func(work Work, res FetchResult)
	WorkTransition  func(work Work, tres TransitionResult)
	OriginThrottled func(origin string, delay time.Duration)
}

func (f EventFuncs) OnFetchStart(work Work) {
	if f.FetchStart != nil {
		f.FetchStart(work)
	}
}

func (f EventFuncs) OnFetchComplete(work Work, res FetchResult) {
	if f.FetchComplete != nil {
		f.FetchComplete(work, res)
	}
}

func (f EventFuncs) OnWorkTransition(work Work, tres TransitionResult) {
	if f.WorkTransition != nil {
		f.WorkTransition(work, tres)
	}
}

func (f EventFuncs) OnOriginThrottled(origin string, delay time.Duration) {
	if f.OriginThrottled != nil {
		f.OriginThrottled(origin, delay)
	}
}
