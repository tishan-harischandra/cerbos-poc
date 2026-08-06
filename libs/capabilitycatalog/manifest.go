package capabilitycatalog

// ArchetypeResourceCount is the number of resources the five archetypes are
// mechanically instantiated across. Chosen so that 79 (mechanical) * 5
// (ArchetypeCount) + 5 (hand-authored §12.1 worked examples) is exactly the
// "exactly 400 capabilities" acceptance criterion (issue #10). See
// SelectArchetypeResources and the doc comment on GenerateArchetypeCapabilities.
const ArchetypeResourceCount = 79

// WorkedExampleCount is the number of hand-authored §12.1 worked examples
// (deploy/cerbos/catalog/ui-capabilities/clinical-worked-examples.yaml).
const WorkedExampleCount = 5

// TotalCapabilityCount is the acceptance-criterion total: exactly 400.
const TotalCapabilityCount = ArchetypeResourceCount*ArchetypeCount + WorkedExampleCount
