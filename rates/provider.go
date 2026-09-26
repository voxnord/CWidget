// Package rates abstracts currency rate sources behind the Provider
// interface (the Adapter pattern) and manages fetching, failover and caching.
package rates

import (
	"context"
	"time"
)

// Provider is the contract for an external API adapter.
// To add a new service: implement this interface and add a single entry to
// the provider factory in main.go. Nothing else needs to change.
type Provider interface {
    Name() string
    // Fetch downloads a full current snapshot of rates (a payload once an
    // hour is negligible). A "pair not supported" error is not needed here:
    // coverage is defined by the presence of codes in Points, and the manager
    // falls through to the next provider on its own.
    Fetch(ctx context.Context) (*Table, error)
}

// PublicationAware is an optional interface a Provider may implement to
// declare when NEW quotes are expected to appear. The manager uses it to
// place a tick right after the expected publication (plus a grace lag), so
// the newest official rate shows up in minutes instead of waiting up to a
// full refresh interval. Providers without the hint are polled on the
// regular interval only — the abstraction stays optional.
type PublicationAware interface {
    // NextPublication returns the expected moment a new QuoteDate becomes
    // available, strictly after "after".
    NextPublication(after time.Time) time.Time
}