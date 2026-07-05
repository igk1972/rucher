package store

import "context"

// Fake is a Store test double.
type Fake struct {
	Checkout string
	Revision string
	Err      error
}

func (f *Fake) Sync(ctx context.Context) (string, string, error) {
	return f.Checkout, f.Revision, f.Err
}
