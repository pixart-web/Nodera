// Package authctx defines the request-scoped authentication/authorization
// context threaded through every domain service call. Domain services accept
// an AuthContext explicitly (never read it from a global) so tenant scoping
// and permission checks are impossible to accidentally skip.
package authctx

import (
	"context"

	"github.com/google/uuid"
)

// ActorType distinguishes a human user from a non-human service account, so
// audit records and permission decisions can tell them apart.
type ActorType string

const (
	ActorUser           ActorType = "user"
	ActorServiceAccount ActorType = "service_account"
	ActorSystem         ActorType = "system" // internal jobs/migrations, never a client request
)

// AuthContext is the authenticated, tenant-scoped identity of the caller of
// a domain service method. It is resolved once per request by the identity
// module and passed explicitly from there on.
type AuthContext struct {
	ActorType      ActorType
	ActorID        uuid.UUID
	ActorLabel     string // human-readable snapshot for audit records
	OrganizationID uuid.UUID
	Permissions    map[string]struct{} // resolved permission keys for this actor within OrganizationID
	SessionID      uuid.UUID           // zero value if authenticated via API token
	CorrelationID  string
}

// HasPermission reports whether the actor holds the given permission key
// within its current organization. This is the single choke point RBAC
// checks must go through — see internal/rbac.
func (a AuthContext) HasPermission(key string) bool {
	_, ok := a.Permissions[key]
	return ok
}

// System returns an AuthContext representing the platform itself (migrations,
// scheduled jobs, startup seeding) — never derived from a client request.
func System(orgID uuid.UUID) AuthContext {
	return AuthContext{
		ActorType:      ActorSystem,
		ActorLabel:     "system",
		OrganizationID: orgID,
		Permissions:    nil, // system context bypasses permission checks entirely; see rbac.Check
	}
}

type ctxKey struct{}

func WithAuthContext(ctx context.Context, ac AuthContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, ac)
}

func FromContext(ctx context.Context) (AuthContext, bool) {
	ac, ok := ctx.Value(ctxKey{}).(AuthContext)
	return ac, ok
}
