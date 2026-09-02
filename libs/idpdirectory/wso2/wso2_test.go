package wso2_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/directorycontract"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory/wso2"
	"github.com/tishan-harischandra/cerbos-poc/libs/tokenverifier"
)

// The shared cross-tenant contract (issue #85): even a stub that implements
// none of the organization reads must never answer with data for a tenant
// it does not serve - ErrUnimplemented satisfies that as surely as
// ErrUnknownTenant would.
func TestDirectoryContract(t *testing.T) {
	directorycontract.Run(t, func(t *testing.T) (idpdirectory.IdentityDirectory, idpdirectory.TenantID) {
		directory, err := wso2.New(wso2.Config{BaseURL: "https://identity.internal", TenantID: "tenant-a"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return directory, idpdirectory.TenantID("tenant-b")
	})
}

// The stub proves the seam: it satisfies the whole port, so a second provider
// needs no change to the interface, and it fails loudly on every operation, so
// selecting it can never be mistaken for an empty directory.
func TestEveryOperationReportsThatItIsNotImplemented(t *testing.T) {
	directory, err := wso2.New(wso2.Config{
		BaseURL:  "https://identity.internal",
		TenantID: "tenant-a",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Typed as the port, so this fails to compile if the stub ever stops
	// implementing the whole interface.
	var port idpdirectory.IdentityDirectory = directory
	ctx := context.Background()

	operations := map[string]func() error{
		"SearchUsers": func() error {
			_, err := port.SearchUsers(ctx, "tenant-a", idpdirectory.UserSearch{})
			return err
		},
		"SearchRoles": func() error {
			_, err := port.SearchRoles(ctx, "tenant-a", idpdirectory.RoleSearch{})
			return err
		},
		"GetUser": func() error {
			_, err := port.GetUser(ctx, "tenant-a", "user-doctor")
			return err
		},
		"GetRole": func() error {
			_, err := port.GetRole(ctx, "tenant-a", "doctor")
			return err
		},
		"GetUserRoles": func() error {
			_, err := port.GetUserRoles(ctx, "tenant-a", "user-doctor")
			return err
		},
		"ResolveRuntimeRoles": func() error {
			_, err := port.ResolveRuntimeRoles(ctx, tokenverifier.VerifiedToken{}, "tenant-a")
			return err
		},
		"OrganizationsOfTenant": func() error {
			_, err := port.OrganizationsOfTenant(ctx, "tenant-a", idpdirectory.OrganizationSearch{})
			return err
		},
		"OrganizationsOfUser": func() error {
			_, err := port.OrganizationsOfUser(ctx, "tenant-a", "user-doctor")
			return err
		},
		"MembersOfOrganization": func() error {
			_, err := port.MembersOfOrganization(ctx, "tenant-a", "org-north", idpdirectory.PageRequest{})
			return err
		},
	}

	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			err := operation()
			if !errors.Is(err, idpdirectory.ErrUnimplemented) {
				t.Errorf("%s error = %v, want %v", name, err, idpdirectory.ErrUnimplemented)
			}
			// The message has to say which SCIM2 API the work would use, so
			// the error is a starting point rather than a dead end.
			if err != nil && !strings.Contains(err.Error(), "SCIM2") {
				t.Errorf("%s error = %q, want it to name the SCIM2 API", name, err)
			}
		})
	}
}
