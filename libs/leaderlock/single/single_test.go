package single_test

import (
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/leaderlockcontract"
	"github.com/tishan-harischandra/cerbos-poc/libs/leaderlock/single"
)

func TestSingleSatisfiesTheLeaderElectionContract(t *testing.T) {
	leaderlockcontract.Run(t, leaderlockcontract.Contract{
		NewContender: func(t *testing.T) leaderlockcontract.Contender {
			t.Helper()
			return leaderlockcontract.Contender{Elector: single.New()}
		},
		// SINGLE coordinates with nobody: it is for a deployment scaled
		// to one replica, and for tests. Saying so here is what keeps
		// the contention cases from being quietly skipped for adapters
		// that do have to win them.
		AlwaysLeader: true,
	})
}

var _ leaderlock.Elector = single.New()
