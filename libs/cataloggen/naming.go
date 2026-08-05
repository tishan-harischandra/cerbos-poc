package cataloggen

import "strings"

// PascalToSnake converts a PascalCase FHIR type name, such as
// "AllergyIntolerance", into the snake_case key used for the Cerbos resource
// kind, the catalog resource_key and the fhir_resource.resource_type value:
// "allergy_intolerance". All three must agree, or a decision request built
// from one and checked against a policy or schema built from another would
// silently fail to match.
func PascalToSnake(name string) string {
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				nextIsLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				prevIsLowerOrDigit := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
				if prevIsLowerOrDigit || (prev >= 'A' && prev <= 'Z' && nextIsLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r - 'A' + 'a')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// DisplayName derives a human-readable label from a PascalCase FHIR type
// name: "AllergyIntolerance" becomes "Allergy intolerance", matching the
// convention set by the hand-authored patient_record catalog entry
// ("Patient record") in §6.1.
func DisplayName(fhirType string) string {
	joined := strings.ReplaceAll(PascalToSnake(fhirType), "_", " ")
	if joined == "" {
		return joined
	}
	return strings.ToUpper(joined[:1]) + joined[1:]
}
