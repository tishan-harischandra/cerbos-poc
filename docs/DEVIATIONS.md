# Deviations from design v1.3

Every place this prototype does something other than what
[`Cerbos_Multi_Tenant_Authorization_Design_v1.3.md`](Cerbos_Multi_Tenant_Authorization_Design_v1.3.md)
describes, with the reason. A deviation nobody wrote down becomes, a year
later, either a bug report against the design or a rediscovered constraint;
either way the reasoning is gone.

Deviations are grouped by kind. "Substitution" means the design's intent is
met by different means; "narrowing" means less was built than described;
"addition" means something exists here that the design does not mention.

## Substitutions

### S1. Go, not Java (§7.2, §11)

The design writes its adapter interfaces in Java. Every backend here is Go.

Nothing in the design depends on the language: the interfaces are shapes, not
JVM types. Go removes the JVM from the images and from the host toolchain
entirely, which is what makes `scripts/go.sh` - a container-backed toolchain,
no local install - possible. Liquibase, which is genuinely a JVM tool, runs as
a container and never as a dependency of a service image.

### S2. The identity directory is a library, not a service (§4.1)

§4.1's component table lists an "IdP adapter" alongside the ADS and the
Administration Service, which reads as a deployable. Here it is
`libs/idpdirectory`: a port, two adapters, and a provider factory selected by
`IDP_TYPE`.

A network hop between a service and its own directory client buys nothing -
there is no independent scaling story, no separate failure domain worth
having, and one more thing to deploy. The property the design actually wants
is that no consumer knows which product is installed, and that is enforced
here by an architecture test rather than by a process boundary
(`tests/architecture/adapterimports_test.go`).

### S3. The Admin Console is served by the Administration Service (§9, ADR-008)

The design implies a separately deployed console. Here the Angular bundle is
built into the Administration Service image, which serves it and proxies its
API calls. ADR-008 records why: the console is an asset surface of its own
BFF, and a second nginx deployment existed only to add a CORS problem.

### S4. Leader election is a port with five adapters (§14, ADR-009)

§14 assumes Kubernetes and would naturally use a `Lease`. The singleton
workloads here - the outbox publisher and the policy controller - depend on
`libs/leaderlock` instead, and an operator picks a mechanism with
`LEADER_ELECTION_TYPE`.

Two deployment paths exist (compose and Kubernetes) and neither should require
rebuilding an image to run correctly on the other. ADR-009 also records what
this costs: four of the five adapters are leases, not mutual exclusion, and
the port can only ever promise its weakest member.

### S5. Capability revision changes are polled, not pushed (§12.6)

§12.6 offers server-sent notification of a revision change as an option and is
explicit that it is not a security mechanism. The Business UI reloads its
snapshot instead.

The convergence budget is seconds, the ADS already invalidates on a Kafka
event, and an SSE channel would add a connection per browser tab for a
rendering hint. §12.6 sanctions the choice.

### S6. §12.1's worked examples are mapped onto catalog resources (§12.1)

The five hand-authored capabilities in
`deploy/cerbos/catalog/ui-capabilities/clinical-worked-examples.yaml` keep the
document's expression structure exactly - including the nested `anyOf` - but
their leaves name resources that exist in the committed FHIR catalog
(`person`, `clinical_impression`, `observation`, `medication_request`,
`service_request`) rather than the illustrative names the document coined
(`patient_demographics`, `clinical_note_collection`, ...).

The document predates the FHIR manifest. Fabricating five more resources and
five more policies to satisfy illustrative naming would have added catalog
surface that nothing else uses; the mapping is recorded in full in that file's
own header, and CI validates every leaf against the active catalog.

### S7. Batched `CheckResources`, not the query-plan adapter (§15.2)

§15.2 suggests Cerbos's query plan for very large lists. The capability
evaluator deduplicates leaves and issues a bounded number of batched
`CheckResources` calls instead (`DefaultMaxResourcesPerCheck`).

A query plan is a filter pushed into a data store, and this prototype's data
access is a page at a time behind a PEP. Batching is what the measured path
needs; the query plan would be machinery built against a guess.

## Narrowings

### N1. No Cerbos scoped policies (§5.3)

§5.3 recommends deferring per-tenant scoped policies. Deferred: one shared
root policy per resource, tenant and hospital isolation as mandatory rules.

### N2. The WSO2 adapter is a port, not an implementation (§7.4)

`libs/idpdirectory/wso2` satisfies the port and the shared contract suite, but
the SCIM2 calls are not written. What the prototype is proving is provider
neutrality - that no consumer names a product - and that is proved by the port
and its architecture test, not by a second working integration.

### N3. No maker-checker or approval workflow (§9.2, §20 Phase 6)

§9.2 offers optional approval for high-risk changes and §20 defers governance
to a later phase. Not implemented. Every save is audited, which is the part
the runtime depends on.

### N4. No Admin Console screen for instance-scoped overrides (§6.2)

The optional `resource_instance_id` dimension exists in the schema, in
`permissionContext`, in the store contract and in the demo seed, and decisions
honour it. No console screen manages it, so it is reachable through the API
only.

### N5. No NetworkPolicy, mTLS, PodDisruptionBudget or topology spread (§14, §16.1)

`deploy/k8s` carries Deployments, StatefulSets, Services and KEDA
`ScaledObject`s. The §16.1 network posture is expressed in compose by not
publishing anything except the two browser entry points, and in Kubernetes by
ClusterIP Services - not by policy objects. The README's "remaining manual
steps" section names this, along with the Ingress and the real secrets a
cluster deploy still needs.

### N6. The exhaustive policy suite is not sharded (§19.1)

The PRD anticipated sharding the §19.1 matrix across CI jobs. Measured, the
whole suite - 11,303 assertions over 156 resource policies - compiles and runs
in about 16 seconds, so it stays one job. `docs/MEASURED_FINDINGS.md` records
the number so a future regression is visible before it needs sharding.

### N7. Not a conformant FHIR server (§6.1)

Resources are FHIR-named and carry a JSON payload. There is no profile
validation, no search parameter support and no conformance statement. The
catalog exists to give the authorization model realistic breadth.

## Additions

### A1. A polymorphic `fhir_resource` table (§8.1)

§8.1's table list has nowhere to keep the resource state the mandatory rules
evaluate against - `status != "LOCKED"` needs a stored status. One polymorphic
table (`resource_type`, `resource_id`, `tenant_id`, `hospital_id`, `status`,
...) backs the Resource Service and, since this slice, the capability
evaluator's target resolution.

One table rather than 156: the attributes the policies read are the same few
for every type, and they are columns rather than JSON fields so that no policy
condition ever depends on an engine's JSON operators.

### A2. Kubernetes manifests (§14)

The PRD placed Kubernetes out of scope and compose preserved the semantics.
That decision was reversed during implementation: `deploy/k8s` is a kustomize
layout with `dev`, `dev-redis`, `dev-chaos` and `prod` overlays, validated
against the Kubernetes API schema in CI (`make k8s-validate`) and exercised by
the chaos suite and the Kubernetes walkthrough run on a real kind cluster.

### A3. The resource count is not 119 (§6.1)

The original brief's "119 resources" is not treated as a contract. The
committed manifest (`libs/cataloggen/manifest.yaml`) lists the complete
concrete FHIR R6 resource list - 156 policies as committed - and is the single
auditable source of the real number.

### A4. A capability target resolver backed by the store (§12.3)

§12.3 says a `targetRef` is resolved server-side into a real instance with
trusted attributes, without saying where those attributes come from. The first
implementation supplied only tenant and hospital, which every resource schema
rejects for want of `status`: the snapshot endpoint could only ever answer
"denied". `StoreTargetResolver` reads the instance from the authorization
database, and reports the *stored* tenancy rather than the caller's so the
isolation rule can still fire on another tenant's instance.

A collection- or module-scoped `targetRef` names no instance, so it resolves
to the hospital and carries `status: ACTIVE`. That is a choice the design does
not make for us: a collection cannot be locked, and the locked-record rule
guards only the instance actions.
