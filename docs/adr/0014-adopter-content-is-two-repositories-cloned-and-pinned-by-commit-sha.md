# ADR-014: Adopter content is two repositories, cloned and pinned by commit SHA

ADR-013 decided that adopter-specific content is sourced from the adopter's
own git repository, and deliberately left three questions unresolved rather
than guessing them: whether the syncer keeps `libs/policyrelease`'s Gitea
API or clones a ref, whether resources and capabilities live in one
repository or two, and what the unit of an installation is. This ADR answers
all three, and the consistency question the second answer creates.

## The syncer clones a ref and pins the resolved commit SHA

`libs/policyrelease` reaches Gitea through three endpoints - `/tags`,
`/tag_protections` and `/archive/{commit}.tar.gz` - and `SelectTag` refuses
any tag that is not protected, because "pinning an unprotected tag would let
its target commit move underneath an already validated release"
(`libs/policyrelease/gitea.go`). That is the whole of the guarantee: the
content behind a validated release cannot change.

**A commit SHA has that property by construction, on any host.** Git is
content-addressed; a SHA cannot be repointed the way a tag can. ADR-013
already commits to deriving the catalog revision from the synced ref, so the
SHA is being resolved regardless. Keeping the Gitea path would buy nothing
the SHA does not already give, and would cost the premise: tag protection is
proprietary. `client_test.go` records the finding against a live 1.22/1.23
instance - Gitea's own `Tag` struct carries no `protected` field in any
version, and protection lives only in a separate `/tag_protections` resource
matched against tag names by `name_pattern`. A platform that any SaaS can
adopt cannot require every adopter to host on Gitea.

What is genuinely lost is not immutability but **authorization**: on Gitea,
only a user with tag-protection rights can mint a release tag. That is a
governance control, and this platform has none anyway - ADR-013 records that
changing the resource model "is a governance boundary this platform has no
maker-checker for (DEVIATIONS N3) and no audit row for". The control is not
being removed here; it is being recognised as never having covered this path,
and it is slice 5's to supply.

`libs/policyrelease` keeps its Gitea client for the root-policy release,
which is this platform's own artifact on this platform's own Gitea. Only the
adopter-content syncer is host-neutral.

## Resources and capabilities are two repositories

The resource manifest and the capability catalog are sourced from two
repositories with independent refs, each with its own URL, ref, subdirectory,
credentials and poll interval. This lets an adopter's UI and platform teams
own and release their inputs separately, which is the shape most adopters'
org charts already have.

The cost is real and is paid in the next section: §12.2 requires that "every
permission leaf must resolve to a known catalog resource and action"
(`libs/capabilitycatalog/validate.go`), so two independent refs can disagree
in a way one commit never could.

## Resources lead, capabilities follow, and removal is checked backwards

The dependency between the two inputs runs one way: a capability leaf names a
resource and an action, and nothing about a resource refers to a capability.
The activation rule follows that direction instead of treating the two refs
as symmetric.

- **A capability sync validates its leaves against the currently active
  resource catalog.** A dangling leaf fails the sync; the last good
  capability catalog keeps serving.
- **A resource sync validates backwards**: it refuses to activate if it would
  orphan a leaf in the currently active capability catalog. This is a set
  membership test over pairs already in memory, not a second policy run.

Each repository therefore keeps a single "last good" commit SHA. The
alternative - gating on a validated *pair* of SHAs - is strictly correct and
was rejected because it makes activation state combinatorial and forces a
capability-only change through the resource-side gate it has no reason to
run.

The consequence for an adopter is an ordinary expand/contract discipline:
to retire a resource, ship the capability removal first, then the resource
removal. A removal attempted in the other order is refused with the orphaned
leaves named, rather than silently breaking the §12.2 invariant.

That a capability catalog may lag its resource catalog is already a state
this platform tolerates by design: §12.5 has the PEP enforce regardless of
the snapshot, §12.6 treats capability refresh as polled and explicitly not a
security mechanism, and DEVIATIONS S5 records that choice. A lagging catalog
renders a stale UI hint; it cannot grant anything.

## The domain model stays installation-wide

ADR-013 assumes one resource model and one capability catalog per
installation, and observes that sourcing content per repository "does not by
itself add" a per-application dimension. That assumption is confirmed rather
than revisited here. Nothing in the schema carries application identity -
not `ui_capability_definition`, not the resource catalog, not
`permissionContext` - so adding the dimension would reach through the
snapshot endpoint, the role matrix and policy resource naming, which is a far
larger change than sourcing content. The per-repository `subdirectory`
setting leaves the door open without anyone having to walk through it now.

## Consequences

- The syncer needs git, not an HTTP client for one forge's REST API. A
  shallow clone of a ref, resolving to a SHA, replaces `SelectTag`,
  `ListTags` and the archive download for adopter content.
- **A moved ref is detected, not prevented.** With protected tags the forge
  refused the move; with SHAs the syncer observes that the ref now resolves
  to a different commit and validates it as new content before activating.
  An adopter who force-pushes their release branch gets a validation cycle,
  not a silent swap - but also no refusal.
- Credentials double: two repositories mean two secret references, and both
  must stay out of the registry, the database and any browser-reachable
  response, as ADR-011 already requires of tenant credentials.
- Two poll loops mean two failure domains. Either repository being
  unreachable is non-fatal and leaves that input's last good content serving,
  matching ADR-013's "a failed sync is non-fatal".
- The backward check makes a resource sync depend on capability state, which
  is the one place the otherwise one-way dependency runs in reverse. It is
  deliberately a membership test and not a policy evaluation, so it cannot
  become a second gate that disagrees with the first.
- `CAPABILITY_CATALOG_REVISION` becomes the capability repository's commit
  SHA, and the resource catalog revision the resource repository's, so the
  §12.4 snapshot reports two independently meaningful revisions instead of
  the hardcoded `1` that ADR-013 flags as drifting from `rootPolicyRevision`.
