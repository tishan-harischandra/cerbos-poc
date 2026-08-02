# Selects issues that are ready to be worked on.
#
# Input : the JSON array produced by
#         gh issue list --state all --json number,title,url,state,body,labels
# Args  : $branches - JSON array of head branch names of currently open PRs
# Output: array of ready issues, ascending by number, each shaped as
#         {number, title, url, labels, blockers, unmet, has_open_pr}
#
# An issue is ready when it is open, is not the PRD tracking issue, and every
# issue referenced under its "## Blocked by" heading is closed. References under
# any other heading (notably "## Parent") are deliberately ignored.

def blockers:
  ("\n" + (.body // ""))
  | split("\n## ")
  | map(select(test("^blocked by"; "i")))
  | join("\n")
  | [ scan("#([0-9]+)")[0] | tonumber ];

. as $issues
| ([ $issues[] | select(.state == "CLOSED") | .number ]) as $closed
| ($branches // []) as $open_branches
| [ $issues[] | select(.state == "OPEN") ]
| map(select(([.labels[]?.name] | index("prd")) == null))
| map({
    number: .number,
    title: .title,
    url: .url,
    labels: [.labels[]?.name],
    blockers: blockers
  })
| map(. + { unmet: [ .blockers[] | select(IN($closed[]) | not) ] })
| map(
    . as $i
    | . + {
        has_open_pr: (
          [ $open_branches[]
            | select(startswith("issue-" + ($i.number | tostring) + "-")) ]
          | length > 0
        )
      }
  )
| map(select((.unmet | length) == 0))
| sort_by(.number)
