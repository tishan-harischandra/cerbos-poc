package architecture

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// The adapters are the only place a dialect may be named. Everything else talks
// to the assignmentstore port, so if service logic starts spelling MERGE or
// ON CONFLICT the port has stopped being a boundary and the second engine
// becomes a rewrite rather than a configuration change.
var dialectAdapterDirs = []string{
	"libs/assignmentstore/postgresstore",
	"libs/assignmentstore/oraclestore",
	// The leader election adapters are driver adapters of exactly the same
	// kind, behind their own port (ADR-009). PG_ADVISORY has no portable
	// equivalent to abstract - an advisory lock is a PostgreSQL feature -
	// and DATABASE is the dual-dialect one, so it carries both dialects on
	// purpose, the way postgresstore and oraclestore each carry theirs.
	// Nothing outside these two directories may name a dialect, which is
	// what keeps a service from learning how the cluster coordinates.
	"libs/leaderlock/pgadvisory",
	"libs/leaderlock/databaselock",
	// The load-test seeding harness writes straight into Keycloak's own
	// database, whose schema Keycloak owns and which this prototype only
	// ever runs on PostgreSQL (see keycloak-db in docker-compose.yml). It
	// is out of the assignmentstore port's dual-dialect scope for the same
	// reason Keycloak's schema itself is: nothing here claims Oracle
	// portability for Keycloak's own tables.
	"libs/keycloakbulkload",
}

// dialectMarkers are constructs that only one engine understands. Each is
// matched as a whole word so ordinary prose in a comment does not trip it.
var dialectMarkers = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{"ON CONFLICT", regexp.MustCompile(`(?i)\bON\s+CONFLICT\b`)},
	{"MERGE INTO", regexp.MustCompile(`(?i)\bMERGE\s+INTO\b`)},
	{"FROM dual", regexp.MustCompile(`(?i)\bFROM\s+dual\b`)},
	{"RETURNING", regexp.MustCompile(`(?i)\bRETURNING\b`)},
	{"information_schema", regexp.MustCompile(`(?i)\binformation_schema\b`)},
	{"pg_ catalog table", regexp.MustCompile(`(?i)\bpg_(class|index|constraint|attribute)\b`)},
	{"user_ catalog table", regexp.MustCompile(`(?i)\buser_(tables|constraints|indexes|ind_columns|cons_columns)\b`)},
	{"NVL", regexp.MustCompile(`(?i)\bNVL\s*\(`)},
	{"SYSDATE", regexp.MustCompile(`(?i)\bSYSDATE\b`)},
	{"ROWNUM", regexp.MustCompile(`(?i)\bROWNUM\b`)},
	{"JSONB", regexp.MustCompile(`(?i)\bJSONB\b`)},
	{"a pgx driver import", regexp.MustCompile(`"github\.com/jackc/pgx`)},
	{"a go-ora driver import", regexp.MustCompile(`"github\.com/sijms/go-ora`)},
}

// IsDialectAdapter reports whether a repository-relative path is allowed to name
// a dialect.
func IsDialectAdapter(relativePath string) bool {
	dir := filepath.ToSlash(filepath.Dir(relativePath))
	for _, allowed := range dialectAdapterDirs {
		if dir == allowed || strings.HasPrefix(dir, allowed+"/") {
			return true
		}
	}
	return false
}

// ScanForDialectLeak reports dialect-specific SQL or driver imports found outside
// the adapters.
//
// The scan is textual rather than syntactic because the leak it looks for lives
// inside string literals, where the type checker has nothing to say.
func ScanForDialectLeak(relativePath, source string, isAdapter bool) []Finding {
	if isAdapter {
		return nil
	}

	var findings []Finding
	for lineNumber, line := range strings.Split(source, "\n") {
		// The checker describes the markers it looks for, so its own source and
		// its tests would match every one of them.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, marker := range dialectMarkers {
			if marker.pattern.MatchString(line) {
				findings = append(findings, Finding{
					File:    relativePath,
					Line:    lineNumber + 1,
					Symbol:  marker.name,
					Message: fmt.Sprintf("%s is dialect-specific and belongs in a driver adapter", marker.name),
				})
			}
		}
	}
	return findings
}
