package nodeagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/nodeagent/protocol"
)

// A database outage must not look like bad credentials: an agent that gets 401
// would conclude it was revoked, while a server error is simply retried.
func TestAuthenticateDatabaseFailureIsNotACredentialError(t *testing.T) {
	url := os.Getenv("NODERA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NODERA_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close() // every query now fails
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	svc := New(pool, nil, key)
	hdr := func(k string) string {
		return map[string]string{protocol.HeaderAgentID: uuid.NewString()}[k]
	}
	_, err = svc.Authenticate(context.Background(), hdr, "POST", "/agent/v1/heartbeat", nil)
	if err == nil || errors.Is(err, errBadCreds) {
		t.Fatalf("a database failure must surface as a server error, got %v", err)
	}
}
