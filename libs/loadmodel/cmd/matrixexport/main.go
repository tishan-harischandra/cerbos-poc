// Command matrixexport writes the load model's role/resource/action permission
// matrix to an .xlsx workbook.
//
// The rows are not transcribed by hand: they come from the same
// loadmodel.Population.RolePermissions the seeder writes to the database, so
// the spreadsheet cannot drift from what a load run actually grants.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/tishan-harischandra/cerbos-poc/libs/assignmentstore"
	"github.com/tishan-harischandra/cerbos-poc/libs/loadmodel"
)

func main() {
	out := flag.String("out", "role-permission-matrix.xlsx", "path of the workbook to write")
	flag.Parse()

	// A fixed instant keeps the workbook byte-identical between runs; the
	// validity windows are laid out around it exactly as a seed's are.
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	full, err := loadmodel.New(loadmodel.FullLoadConfig())
	if err != nil {
		fail(err)
	}
	demo, err := loadmodel.New(loadmodel.DemoConfig())
	if err != nil {
		fail(err)
	}

	sheets := []sheet{
		matrixSheet("Full load matrix", full, at),
		matrixSheet("Demo matrix", demo, at),
		configSheet(full, demo),
		notesSheet(),
	}

	f, err := os.Create(*out)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	if err := writeWorkbook(f, sheets); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s\n", *out)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "matrixexport:", err)
	os.Exit(1)
}

// matrixSheet lays the population's role permissions out in the requested
// grid: one row per (tenant, resource type, action), one column per canonical
// role, and a mark where the role grants that action.
func matrixSheet(name string, p *loadmodel.Population, at time.Time) sheet {
	roles := p.RoleNames()
	column := make(map[string]int, len(roles))
	for i, role := range roles {
		// Two header columns of labels, then the tenant column, then the
		// resource type and action columns.
		column[role] = 4 + i
	}

	type rowKey struct {
		tenant   string
		resource string
		action   string
	}
	granted := make(map[rowKey]map[string]assignmentstore.RolePermission)
	var order []rowKey
	for _, permission := range p.RolePermissions(at) {
		key := rowKey{permission.Key.TenantID, permission.Key.ResourceKey, permission.Key.ActionKey}
		if _, seen := granted[key]; !seen {
			granted[key] = make(map[string]assignmentstore.RolePermission, len(roles))
			order = append(order, key)
		}
		granted[key][permission.Key.RoleExternalID] = permission
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].tenant != order[j].tenant {
			return order[i].tenant < order[j].tenant
		}
		if order[i].resource != order[j].resource {
			return order[i].resource < order[j].resource
		}
		return order[i].action < order[j].action
	})

	width := 4 + len(roles)
	header := make([]string, width)
	header[3] = "Roles"
	labels := make([]string, width)
	labels[0] = "Tenant"
	labels[1] = "Resource Type"
	labels[2] = "Action"
	for _, role := range roles {
		labels[column[role]-1] = role
	}

	rows := [][]string{header, labels}
	for _, key := range order {
		row := make([]string, width)
		row[0] = key.tenant
		row[1] = key.resource
		row[2] = key.action
		for role, permission := range granted[key] {
			row[column[role]-1] = mark(permission, at)
		}
		rows = append(rows, row)
	}

	return sheet{name: name, rows: rows, freezeRows: 2, freezeCols: 3}
}

// mark is what one cell says: "x" for a grant that is in force, and a spelled
// out reason for one that is not, because a blank cell and a disabled or
// expired row mean different things (§8.3).
func mark(permission assignmentstore.RolePermission, at time.Time) string {
	switch {
	case !permission.Enabled:
		return "disabled"
	case !permission.ValidUntil.IsZero() && permission.ValidUntil.Before(at):
		return "expired"
	case permission.ValidFrom.After(at):
		return "not yet in force"
	default:
		return "x"
	}
}

func configSheet(full, demo *loadmodel.Population) sheet {
	fullCfg, demoCfg := full.Config(), demo.Config()
	rows := [][]string{
		{"Dimension", "Full load", "Demo"},
		{"Tenants", count(fullCfg.Tenants), count(demoCfg.Tenants)},
		{"Hospitals per tenant", count(fullCfg.HospitalsPerTenant), count(demoCfg.HospitalsPerTenant)},
		{"Canonical roles", count(fullCfg.CanonicalRoles), count(demoCfg.CanonicalRoles)},
		{"Users", count(fullCfg.Users), count(demoCfg.Users)},
		{"Roles per user", count(fullCfg.RolesPerUser), count(demoCfg.RolesPerUser)},
		{"Role mappings", count(fullCfg.Users * fullCfg.RolesPerUser), count(demoCfg.Users * demoCfg.RolesPerUser)},
		{"Override every Nth user", count(fullCfg.OverrideEveryNthUser), count(demoCfg.OverrideEveryNthUser)},
		{"Reference resource", loadmodel.ResourceKey, loadmodel.ResourceKey},
		{"Actions", fmt.Sprint(loadmodel.Actions), fmt.Sprint(loadmodel.Actions)},
		{"role_permission rows", count(fullCfg.Tenants * fullCfg.CanonicalRoles * len(loadmodel.Actions)),
			count(demoCfg.Tenants * demoCfg.CanonicalRoles * len(loadmodel.Actions))},
	}
	return sheet{name: "Configuration", rows: rows, freezeRows: 1}
}

func count(n int) string { return fmt.Sprint(n) }

func notesSheet() sheet {
	return sheet{name: "Notes", rows: [][]string{
		{"Generated by libs/loadmodel/cmd/matrixexport from libs/loadmodel, not transcribed by hand."},
		{"Rows come from Population.RolePermissions, the same call the load seeder writes to the database."},
		{"Every canonical role grants every action on the reference resource by construction: the load model's"},
		{"purpose is decision throughput at population scale, and the breadth of the FHIR catalog is covered"},
		{"instead by libs/cataloggen and the Cerbos policy test suite."},
		{"A cell is blank only where no role_permission row exists at all; 'disabled' and 'expired' rows exist"},
		{"but grant nothing, which is not the same as an explicit deny (design v1.3 section 8.3)."},
		{"This matrix is role grants only. User GRANT/REVOKE overrides are generated per user by"},
		{"Population.Overrides and are not a role-level concept, so they have no column here."},
		{"Permission precedence is evaluated exclusively in Cerbos policies; nothing in this sheet expresses it."},
	}}
}
