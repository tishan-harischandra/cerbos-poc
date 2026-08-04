package oraclestore

import "github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"

// The adapter is useless if it drifts from the port, and the contract suite only
// notices with a live database. Fail the build instead.
var (
	_ assignmentstore.Store           = (*Store)(nil)
	_ assignmentstore.SchemaInspector = inspector{}
)
