// Package directoryapi exposes the identity directory to the Admin Console.
//
// The console needs to pick users and roles when it edits the permission
// matrix, and it must do so without ever holding an identity provider
// credential (§7.3, §16.1): the ADS holds the service account, the browser
// holds only its own token. The handlers here depend on the provider-neutral
// port, so the console cannot tell Keycloak from WSO2 either.
package directoryapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/tishan-harischandra/cerbos-poc/apps/ads/internal/tokenauth"
	"github.com/tishan-harischandra/cerbos-poc/libs/idpdirectory"
)

// Config holds the handlers' collaborators.
type Config struct {
	Directory idpdirectory.IdentityDirectory
	Logger    *slog.Logger
}

type userPayload struct {
	ExternalID  string `json:"externalId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Enabled     bool   `json:"enabled"`
}

type rolePayload struct {
	// CanonicalID is the §7.5 identifier the role-permission matrix is keyed
	// by. The console writes it back verbatim.
	CanonicalID string `json:"canonicalId"`
	ExternalID  string `json:"externalId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type pagePayload[T any] struct {
	Items   []T  `json:"items"`
	Offset  int  `json:"offset"`
	Limit   int  `json:"limit"`
	HasMore bool `json:"hasMore"`
}

// NewUsersHandler serves GET /internal/directory/users.
func NewUsersHandler(cfg Config) http.Handler {
	logger := loggerOf(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, page, ok := requestContext(w, r)
		if !ok {
			return
		}

		found, err := cfg.Directory.SearchUsers(r.Context(),
			idpdirectory.TenantID(identity.TenantID),
			idpdirectory.UserSearch{Query: r.URL.Query().Get("query"), Page: page})
		if err != nil {
			fail(w, logger, r, "searching users", err)
			return
		}

		items := make([]userPayload, 0, len(found.Items))
		for _, user := range found.Items {
			items = append(items, userPayload{
				ExternalID:  user.ExternalID,
				Username:    user.Username,
				DisplayName: user.DisplayName,
				Email:       user.Email,
				Enabled:     user.Enabled,
			})
		}
		writeJSON(w, http.StatusOK, pagePayload[userPayload]{
			Items: items, Offset: found.Offset, Limit: found.Limit, HasMore: found.HasMore,
		})
	})
}

// NewRolesHandler serves GET /internal/directory/roles.
func NewRolesHandler(cfg Config) http.Handler {
	logger := loggerOf(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, page, ok := requestContext(w, r)
		if !ok {
			return
		}

		found, err := cfg.Directory.SearchRoles(r.Context(),
			idpdirectory.TenantID(identity.TenantID),
			idpdirectory.RoleSearch{Query: r.URL.Query().Get("query"), Page: page})
		if err != nil {
			fail(w, logger, r, "searching roles", err)
			return
		}

		items := make([]rolePayload, 0, len(found.Items))
		for _, role := range found.Items {
			items = append(items, rolePayload{
				CanonicalID: role.CanonicalID,
				ExternalID:  role.ExternalID,
				Name:        role.Name,
				Description: role.Description,
			})
		}
		writeJSON(w, http.StatusOK, pagePayload[rolePayload]{
			Items: items, Offset: found.Offset, Limit: found.Limit, HasMore: found.HasMore,
		})
	})
}

// NewUserRolesHandler serves GET /internal/directory/users/{externalId}/roles
// (issue #17): the roles directly assigned to one user, from the same
// authoritative source SearchRoles reads - the user-override screen's
// "underlying role result" preview is computed from this list, not a
// second copy of it.
func NewUserRolesHandler(cfg Config) http.Handler {
	logger := loggerOf(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := tokenauth.From(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "a bearer token is required")
			return
		}

		externalID := r.PathValue("externalId")
		roles, err := cfg.Directory.GetUserRoles(r.Context(),
			idpdirectory.TenantID(identity.TenantID), externalID)
		if err != nil {
			fail(w, logger, r, "reading a user's roles", err)
			return
		}

		items := make([]rolePayload, 0, len(roles))
		for _, role := range roles {
			items = append(items, rolePayload{
				CanonicalID: role.CanonicalID, ExternalID: role.ExternalID,
				Name: role.Name, Description: role.Description,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
}

// requestContext resolves who is asking and which window they want, answering
// the caller itself when either is unusable.
func requestContext(w http.ResponseWriter, r *http.Request) (tokenauth.Identity, idpdirectory.PageRequest, bool) {
	identity, ok := tokenauth.From(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "a bearer token is required")
		return identity, idpdirectory.PageRequest{}, false
	}
	// A tenant in the query string is refused rather than ignored: a console
	// built against it would appear to work until the day it was pointed at
	// another tenant's data.
	if r.URL.Query().Has("tenantId") {
		writeError(w, http.StatusBadRequest,
			"the tenant is taken from the caller's token; remove tenantId")
		return identity, idpdirectory.PageRequest{}, false
	}

	page, err := pageOf(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return identity, page, false
	}
	return identity, page, true
}

// pageOf reads the window, refusing anything it cannot read rather than
// silently substituting a default: a console paging with a broken parameter
// would otherwise loop over page one forever.
func pageOf(r *http.Request) (idpdirectory.PageRequest, error) {
	page := idpdirectory.PageRequest{Limit: idpdirectory.DefaultPageLimit}
	query := r.URL.Query()

	if raw := query.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return page, errors.New("offset must be a number of zero or more")
		}
		page.Offset = offset
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > idpdirectory.MaxPageLimit {
			return page, errors.New("limit must be between 1 and " +
				strconv.Itoa(idpdirectory.MaxPageLimit))
		}
		page.Limit = limit
	}
	return page, nil
}

// fail logs the provider's own error and tells the caller nothing about it. The
// message may quote the request the adapter made, which carried the service
// account credential (§16.1).
func fail(w http.ResponseWriter, logger *slog.Logger, r *http.Request, operation string, err error) {
	logger.ErrorContext(r.Context(), operation+" failed", slog.Any("error", err))
	writeError(w, http.StatusServiceUnavailable, "the identity directory could not be reached")
}

func loggerOf(cfg Config) *slog.Logger {
	if cfg.Logger != nil {
		return cfg.Logger
	}
	return slog.Default()
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
