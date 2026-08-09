// Package single is the leader election adapter that coordinates with
// nothing: the caller is always the leader.
//
// It is for a deployment scaled to one replica, and for tests. Selecting it
// where more than one replica runs means every replica does the singleton
// work, which is exactly the failure the port exists to prevent - so it is a
// value an operator has to write down deliberately, never a default.
//
// Nothing outside the provider factory may import this package.
package single

import (
	"context"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
)

// Elector elects every caller immediately.
type Elector struct{}

// New returns the adapter.
func New() *Elector { return &Elector{} }

// Run calls onElected at once with a context that lives until ctx is done.
func (e *Elector) Run(ctx context.Context, election leaderlock.Name, onElected func(context.Context)) error {
	if err := election.Validate(); err != nil {
		return err
	}

	leaderCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	onElected(leaderCtx)

	<-ctx.Done()
	return nil
}
