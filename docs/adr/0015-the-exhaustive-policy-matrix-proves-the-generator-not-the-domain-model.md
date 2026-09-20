# ADR-015: The exhaustive policy matrix proves the generator, not the domain model

ADR-013 calls this its largest single loss: the §19.1 suite - 11,303
assertions across 156 resource policies, about 16 seconds - is generated from
the committed manifest and run in CI, and "when the manifest is adopter data,
CI does not have it". The obvious repair is to run the whole suite as a
sync-time activation gate. Before accepting that cost, it is worth asking
what the 11,303 assertions actually establish.

## What the suite measures

Every generated test file is the same precedence matrix - mandatory deny >
user REVOKE > user GRANT > role grant > default deny - instantiated for one
resource, with the same four principals (`doctor`,
`doctor_of_other_tenant`, `doctor_of_other_hospital`,
`administrator_tenant_wide`). Normalising the resource name away, two
generated suites are **byte-identical**:

```
$ diff <(sed 's/allergy_intolerance/RES/g; s/AllergyIntolerance/Res/g' allergy_intolerance_test.yaml) \
       <(sed 's/imaging_study/RES/g;      s/ImagingStudy/Res/g'      imaging_study_test.yaml)
$ echo $?
0
```

The suite is one generator-correctness proof replicated 156 times. It says
nothing about FHIR, about `AllergyIntolerance` as distinct from
`ImagingStudy`, or about the adopter's domain at all. It says: *for every
resource the generator emitted, the emitted policy orders precedence
correctly.*

That reframes the loss. The guarantee worth keeping is a property of the
generator, and a generator is a pure function this repository owns. Proving
it does not require the adopter's manifest - it requires coverage of the
generator's input space, which 156 fixed FHIR resources were never a
principled sample of anyway.

## The decision

**Generator correctness moves to a property test in platform CI.** Manifests
are synthesised - varying resource count, names, domains, the
included/excluded split, the action set and `lockableActions` - the tree is
generated from each, and the precedence matrix is asserted over the result.
This runs with no adopter content, and covers input shapes the committed FHIR
manifest never contained.

**The sync-time gate is bounded and O(1) in the adopter's resource count.**
On every sync: validate the manifest, generate the tree, `cerbos compile`
**the whole tree**, and run the precedence matrix over a **bounded sample**
of resources. A failure at any step leaves the last good content serving,
as ADR-013 requires.

## Why sampling is safe here and would not be elsewhere

Sampling is normally a way of trading coverage for time. It is not, here,
because the two things that can vary per resource are covered by the
unsampled step:

- **A pathological resource name** - one that collides with Cerbos syntax or
  produces invalid CEL - breaks that resource's policy and no other.
  `cerbos compile` runs over every generated policy, so it catches this for
  all of them. Compilation is the cheap half.
- **Precedence ordering** is emitted from one template and is identical
  modulo the name, as measured above. A sample establishes it for the
  template; the 156th repetition adds no information the 1st did not.

What sampling would lose is the ability to catch a generator that emits a
*correct* policy for most resources and a *wrong but compilable* one for a
specific name. That is precisely what the property test is for, and a
property test over synthesised adversarial names is a better instrument for
it than 156 real FHIR type names.

## Why the O(n) cost mattered

`docs/MEASURED_FINDINGS.md` records that "the policy suite is CPU-bound in
one container. Sixteen seconds on sixteen cores is not sixteen seconds on a
two-core runner." The activation path runs in a pod, not on a CI runner, and
the assertion count scales with the adopter's resource model: 156 resources
produce 11,303 assertions, so an adopter with 1,000 resources produces
roughly 72,000. Running the full suite at sync time would make activation
latency a function of how large the adopter's product is, and would force a
timeout policy - ADR-013 notes the design "needs a decision about what
happens when it is slow, not just when it fails". A bounded gate removes the
question instead of answering it.

## Consequences

- **The §19.1 row in `DESIGN_COVERAGE.md` changes meaning.** It stops being
  "11,303 assertions across 156 resource policies" and becomes a property
  over the generator plus a bounded activation gate. The exhaustive suite
  remains committed and run in CI for this repository's example manifest, so
  the number does not disappear - it stops being the guarantee.
- The sample size becomes a tunable with a floor, and a tunable on a
  correctness gate is a hazard. It needs a documented minimum and a test that
  the floor is enforced, or an operator will set it to zero to make a slow
  sync fast.
- A property test needs a manifest generator - synthesised manifests, ideally
  with adversarial names. That is new test infrastructure this repository
  does not have, and it is the real work of this decision. `libs/capabilityeval`
  already has a property test driven by a random source, so the pattern and
  the purity exemption for it both exist.
- The gate now depends on a Cerbos binary being available at sync time. The
  syncer already installs policy trees for Cerbos to load (§13.2,
  `atomicInstall` + `ReloadStore`), so it is in the right failure domain, but
  compiling is a new capability for that workload.
- **This weakens the per-activation evidence and the ADR should say so.**
  Today, every committed manifest has every resource proven before it ships.
  Afterwards, an adopter's manifest has every resource *compiled* and a
  sample *proven*, with the generator proven separately and more thoroughly.
  That is a different guarantee, not a strictly stronger one, and the case
  for it rests on the measurement above: the 156 repetitions were never
  independent evidence.
