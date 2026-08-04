# Agent loop

Deterministic plumbing for the autonomous issue loop. The agent decides what to
build and whether a review is clean; these scripts do the GitHub and git work so
that issue selection, branching and merging are reproducible and testable.

Driven by two workflows:

- `/agent-loop` - repeat until every slice is closed
- `/implement-next-issue` - one issue, start to closed

## Scripts

| Script | Purpose |
| --- | --- |
| `next-unblocked-issue.sh` | Next open issue whose `## Blocked by` references are all closed |
| `start-issue.sh` | Clean-tree check, sync `main`, create `issue-N-<slug>`, print the issue |
| `open-pr.sh` | Push the branch and open the PR with `Closes #N` |
| `review-pr.sh` | Assemble the self-review packet, count review rounds, enforce the cap |
| `merge-and-close.sh` | Gate, squash-merge, delete the branch, verify the issue closed |
| `escalate.sh` | Park an issue whose merge gate refused, label `needs-human`, return to `main` |
| `status.sh` | Progress report: closed, parked, open PRs, ready to start |

`select-ready.jq` holds the readiness rule on its own so it can be tested
without touching GitHub.

## Rules encoded here

- Only `## Blocked by` counts. `## Parent` and prose references to `#N` do not.
- The PRD tracking issue (label `prd`) is never selected for implementation.
- An issue with an open PR on its branch is skipped unless
  `--include-in-progress` is passed.
- The self-review cycle is capped at `AGENTLOOP_MAX_REVIEW_ROUNDS` (default 8);
  `review-pr.sh` exits 4 once reached so the loop merges instead of spinning.
- The PR merges when a review round finds nothing or when the cap is reached,
  whichever comes first. Escalation is reserved for a merge gate that refuses,
  such as red CI or a conflict.

## Exit codes

`0` success, `1` usage or tooling error, `3` nothing ready to start,
`4` review round cap reached (stop reviewing and merge).

## Environment

- `AGENTLOOP_DEFAULT_BRANCH` (default `main`)
- `AGENTLOOP_MAX_REVIEW_ROUNDS` (default `8`)

Per-issue state lives in `.agentloop/issue-N.json` and is git-ignored.

## Tests

```bash
bash scripts/agentloop/tests/run-tests.sh
```

Runs offline against `tests/fixtures/issues.json`. Requires `jq`; `gh` is not
used.
