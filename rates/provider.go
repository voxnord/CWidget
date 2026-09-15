package rates

import "context"

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