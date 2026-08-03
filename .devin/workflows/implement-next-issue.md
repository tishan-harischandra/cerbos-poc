---
description: Implement one unblocked GitHub issue end to end - branch, TDD, PR, self-review loop, merge, close
---

Run exactly one issue from selection to closed. Do not batch issues here; the
outer `/agent-loop` workflow handles repetition.

All helper scripts live in `scripts/agentloop/` and are run from the repo root.

## 1. Select the issue

// turbo
1. Run `scripts/agentloop/next-unblocked-issue.sh --format json`.
   - Exit code 3 means nothing is ready: stop and report that the queue is
     drained or fully blocked.
   - Otherwise note the issue number as `N`.
2. If the caller named a specific issue, use that number instead, but first
   confirm it appears in `scripts/agentloop/next-unblocked-issue.sh --all`.
   Never start an issue with unmet blockers.

## 2. Start the branch

1. Run `scripts/agentloop/start-issue.sh N`. It refuses on a dirty tree, syncs
   `main`, creates `issue-N-<slug>`, and prints the issue body.
2. Read the printed body carefully. Extract the acceptance criteria verbatim
   into your plan, one plan item per criterion.
3. Read `docs/PRD_Cerbos_Authorization_Prototype.md` and the sections of
   `docs/Cerbos_Multi_Tenant_Authorization_Design_v1.3.md` the issue cites.
   Use the vocabulary from those documents in code and test names.

## 3. Implement with the tdd skill

Invoke the `tdd` skill and follow it strictly.

- Vertical slices only. One failing test, then the minimal code that passes it,
  then the next test. Never write a batch of tests up front.
- Tests exercise public interfaces and describe behaviour. A test that breaks
  under an internal refactor is a defect in the test.
- Commit after each green cycle. Subject line in imperative mood, no issue
  number in the subject; the PR carries the link.
- Honour these project constraints while implementing:
  - Permission precedence lives only in Cerbos policy via
    `sys:permission-evaluator`. Go code that orders deny over grant is a defect.
  - The ADS assembles `permissionContext` data and never computes a verdict.
  - The IdP adapter is a library behind dependency inversion, selected by env.
  - Liquibase changelogs must run on both Postgres and Oracle.
  - One Cerbos policy file per resource.
- Run the repo's build and test entry points before moving on. If none exist
  yet because this is an early slice, the slice itself must create them.

## 4. Open the pull request

1. Write the PR body to a scratch file: what changed, how each acceptance
   criterion is met, and how it was verified.
2. Run `scripts/agentloop/open-pr.sh N --body-file <path>`. It pushes the
   branch, opens the PR, appends `Closes #N`, and prints the PR number.

## 5. Self-review loop

Repeat until clean or the cap trips:

1. Run `scripts/agentloop/review-pr.sh N`. It prints the acceptance criteria,
   the commits, the diff, and the blocking-findings checklist, and consumes one
   review round.
2. Invoke the `graphify` skill for the deeper pass over the files this PR
   touches, then read `graphify-out/GRAPH_REPORT.md`. Treat new untested
   hotspots, surprising cross-community coupling, and affected critical flows as
   findings.
3. Judge honestly against the checklist. List every blocking finding with file,
   line and the required change. "Looks fine" without having read the diff is
   not a review.
4. If there are findings: fix them with the same TDD discipline, commit, push,
   and go back to step 1.
5. If there are no findings: repeat the review once more. Two consecutive clean
   rounds are required before merging.
6. Exit code 4 from `review-pr.sh` means the round cap is exhausted. Write the
   unresolved findings to a file and run
   `scripts/agentloop/escalate.sh N --reason-file <path>`, then stop this issue.

## 6. Merge and close

1. Run `scripts/agentloop/merge-and-close.sh N --comment-file <path>` with a
   short delivery note. It refuses on conflicts, red required checks, or a
   blocked merge state; it squash-merges, deletes the branch, syncs `main` and
   verifies the issue is closed.
2. Confirm with `gh issue view N --json state` that the issue is `CLOSED`.

## 7. Report

State the issue number, the merged PR, the acceptance criteria satisfied, the
tests added, and the number of review rounds it took.
