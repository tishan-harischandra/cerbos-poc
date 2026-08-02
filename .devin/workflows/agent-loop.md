---
description: Run the autonomous loop - keep implementing unblocked issues until every slice is closed
---

The outer driver. It repeats `/implement-next-issue` until the tracker is
drained. Everything is unattended: no human approval is requested between
issues.

## Preflight

// turbo
1. Run `scripts/agentloop/status.sh` and report the starting position.
// turbo
2. Run `bash scripts/agentloop/tests/run-tests.sh` to confirm the selection
   logic is sound before trusting it. Stop if it fails.
3. Confirm the working tree is clean and the current branch is `main`.

## Loop

Repeat:

1. Run `scripts/agentloop/next-unblocked-issue.sh --format json`.
   - Exit code 3: go to Termination.
2. Execute the whole of `.windsurf/workflows/implement-next-issue.md` for that
   issue number, start to finish, including the merge and the issue close.
3. If that issue escalated instead of merging, do not retry it. It now carries
   the `needs-human` label; selection will still offer it next time, so record
   it in your running notes and skip any issue you have already escalated in
   this session.
4. Post a one-line progress note: issue number, PR number, outcome.
5. Continue with the next iteration.

Guard rails for the loop itself:

- Never work two issues at once. One branch, one PR, one issue.
- Never start an issue whose blockers are open, even if it looks easy.
- Never merge without two consecutive clean self-review rounds.
- Never weaken or delete a test to make a build pass. If a pre-existing test
  fails, that is a finding to fix, not an obstacle to remove.
- If `main` breaks after a merge, the very next iteration fixes `main` before
  taking another issue.
- If the same infrastructure failure blocks three issues in a row, stop the
  loop and report; something environmental is wrong.

## Termination

// turbo
1. Run `scripts/agentloop/status.sh`.
2. Report: slices closed, slices parked with `needs-human` and why, open PRs
   left behind, and what a human needs to decide to unblock the remainder.
