# ADR-013: The domain model is adopter data, not a platform artifact

This is a platform: any SaaS should be able to adopt it. One SaaS's domain
model is currently compiled into it. `libs/cataloggen` declares
`//go:embed manifest.yaml` and its own package doc calls that manifest "the
only hand-edited input"; from it the generator emits the resource-action
catalog, one Cerbos resource policy per resource, the principal and
per-resource JSON schemas, the exhaustive test suite and the database
catalog seed. The capability catalog reaches the services the same way,
copied out of the `cerbos-poc/cerbos-assets` image by an initContainer into
an `emptyDir` every pod mounts. A 156-entry FHIR resource list is therefore
part of the platform binary and the platform's images, and no second adopter
can supply a different one without rebuilding both.

Two concerns were conflated: what the platform *is*, and what one adopter's
product *contains*.

## The decision

**Adopter-specific content is sourced from the adopter's own git
repository, never from a platform artifact.** An installation is configured
with a repository URL, a ref, a subdirectory, credentials and a poll
interval. That repository holds the two authored inputs - the resource
manifest and the capability catalog - and nothing generated.

**The adopter authors; the platform generates.** Cerbos resource policies,
the JSON schemas and the exhaustive test suite stay generated from the
manifest, but at sync time rather than build time. They are never committed
by the adopter, because requiring an adopter to commit generated policies
would require them to run this platform's generator inside their own
pipeline - the opposite of being adoptable.

**Cerbos keeps `storage.driver: disk`.** Cerbos offers storage drivers that
read a repository directly, and pointing one at the adopter's repository
looks superficially like the same idea. It cannot work here: the policies
Cerbos must load are derived from the manifest, not present in the
repository. The platform generates a tree and hands Cerbos a directory,
exactly as `libs/policyrelease`'s `atomicInstall` and the pod-local Admin
API `ReloadStore` already do (§13.2).

**Only the syncer talks to git.** The ADS stays filesystem-only, reading
whatever populated its volume, as it does today. This keeps a source-control
system out of the decision service's failure domain and its credential
surface, preserving §13.1's posture that the controller is always the one
dialling out.

**A sync that fails validation does not activate.** The last good ref keeps
serving. Validation means: the manifest is well formed, every capability
leaf resolves against it, the generated tree compiles, and the generated
test suite passes. This is the gate `libs/policyrelease` already applies to
a Gitea tag, applied to a wider input.

## Why git rather than the database

The platform already stores authorization data in tables, so "put the
catalog in a table too" is the obvious move. It is the wrong one, and the
property that separates the two cases is who authors the content and how
often.

The role matrix and user overrides are written by tenant administrators, at
runtime, continuously, per tenant, and must converge within seconds. They
need a transactional revision counter, an append-only audit row per change
and an invalidation path. They are correctly in the database today.

The resource model and the capability catalog are written by the adopter's
own engineers as part of shipping their product. They change at release
cadence, are installation-wide, and want review, diffs, blame, rollback and
signed commits. Putting them in a table means rebuilding every one of those
in application code as a CRUD API, a validation layer, an audit trail and a
versioning scheme. Git already is that system. It also serves the audit
requirement better than a table would: a commit author and a reviewed diff
says more about who changed what, and why, than an `actor_id` and two JSON
documents.

This is the same "reviewed artifact in, database record of what's active"
shape ADR-011 reused for the tenant registry, applied to the one class of
content where the reviewed artifact is all that is needed.

Note that `authorization_resource`, `authorization_action` and
`ui_capability_definition` exist in the schema and are read by no service at
runtime - they are populated from `deploy/liquibase/changelog/data/*.csv` and
never read back. This ADR does not promote them to the runtime source. Their
existence was not an argument for the database; it was the reason the
database looked like the answer.

## Why not ship it in the assets image

`cerbos-poc/cerbos-assets` is already a separate, assets-only image, so a
catalog change does not rebuild `ads` or `admin-service`. It is still the
wrong home. The initContainer's image reference is part of the pod spec, so
changing it is a spec change: a catalog edit becomes a rolling restart of
the ADS, the Administration Service and Cerbos - a deployment event, with
churn on the decision path, for what is a content change. And an image the
platform publishes would carry one adopter's domain model to every other
adopter.

## Consequences

- **`//go:embed manifest.yaml` must go.** `libs/cataloggen` reads its
  manifest from a configured path instead. This is the smallest change in
  this ADR and the one everything else rests on: it is what stops the
  platform binary from containing a healthcare resource list.
- **CI can no longer prove the policy matrix.** The §19.1 suite - 11,303
  assertions across 156 resource policies, about 16 seconds as measured in
  `docs/MEASURED_FINDINGS.md` - is generated from the committed manifest and
  run in CI. When the manifest is adopter data, CI does not have it. That
  guarantee has to move to a sync-time gate that generates, compiles, runs
  the generated suite and refuses to activate on failure. This is the
  largest single loss in this decision and needs its own design; it is not
  solved by this ADR.
- **The capability catalog's load cost becomes a real constraint.** Measured
  on the committed catalog and on generated catalogs of the same shape (2.2
  leaves per capability): 400 capabilities cost 26 ms and 7.8 MiB peak RSS,
  but 100,000 cost 4.0 s and 838 MiB, and 60,000 already peak at 528.6 MiB
  against the 512Mi limit both `deploy/k8s/base/ads` and
  `deploy/k8s/base/admin-service` set - an OOMKill on the first capability
  request after a pod start, because the load is lazy. The catalog is
  therefore authored as one file per module, and the syncer emits a
  pre-decoded artifact beside it: the same 100,000 capabilities load in
  203 ms and 90 MiB peak from a `gob` artifact. The artifact is a sync
  output, never committed.
- **The capability catalog revision becomes the commit SHA.**
  `CAPABILITY_CATALOG_REVISION` is a hardcoded `1` in
  `deploy/k8s/base/ads/kustomization.yaml` today, while `rootPolicyRevision`
  comes from elsewhere, and the §12.4 snapshot reports both as though they
  were coordinated. Deriving the catalog revision from the synced ref closes
  that drift.
- **Capability convergence is the poll interval, not the permission SLO.** A
  capability snapshot is a rendering hint and the PEP enforces regardless
  (§12.5); §12.6 already treats its refresh as polled and explicitly not a
  security mechanism, and DEVIATIONS S5 records that choice. A catalog that
  converges in tens of seconds is consistent with that, and does not weaken
  the five-second permission convergence the role matrix path keeps.
- **A syncer becomes part of every installation.** The `policy-release`
  component is layered onto `dev-chaos` only today, "never onto base or
  prod", and exists for the §18 chaos rows. Generalising it into the
  component that sources adopter content promotes it to a production
  workload, with the leader election it already holds (ADR-009) and a
  failure mode - cannot reach the adopter's repository - that the chaos
  suite's existing "Gitea outage" row already covers in shape.
- **Changing the resource model becomes a privileged runtime operation.**
  Today it is a reviewed commit to this repository. Afterwards it is a
  reviewed commit to the adopter's repository plus an automatic activation,
  which is a governance boundary this platform has no maker-checker for
  (DEVIATIONS N3) and no audit row for, since policy releases are not
  audited at all.
- **An adopter now needs a repository and credentials the platform can
  read.** That is a new operational prerequisite for adoption, and it makes
  the platform dependent on an external system it does not run. The
  mitigation is that the dependency is the syncer's alone and a failed sync
  is non-fatal: the last good content keeps serving.
- **It makes a per-application dimension expressible.** Capabilities and
  resources are installation-wide here and carry no application identity.
  Sourcing them per repository or per subdirectory does not by itself add
  that dimension, but it is the first arrangement in which more than one
  domain model can exist at all.
