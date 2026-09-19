package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/testhelpers"
)

func testEncryptionKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate test encryption key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestSecretsSetListDeleteAndReveal(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "secrets-owner@nodera.dev")

	secretsSvc, err := secrets.New(pool, h.audit, testEncryptionKey(t))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}

	meta, err := secretsSvc.Set(ctx, ac, "ai_provider.openai.api_key", "sk-super-secret-value", "OpenAI key")
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if meta.Key != "ai_provider.openai.api_key" {
		t.Fatalf("unexpected key: %s", meta.Key)
	}

	list, err := secretsSvc.List(ctx, ac)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != meta.ID {
		t.Fatalf("expected exactly the one secret in the list, got %+v", list)
	}

	revealed, err := secretsSvc.Reveal(ctx, ac, "ai_provider.openai.api_key")
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if revealed != "sk-super-secret-value" {
		t.Fatalf("revealed value mismatch: got %q", revealed)
	}

	// Set again with the same key upserts rather than duplicating.
	if _, err := secretsSvc.Set(ctx, ac, "ai_provider.openai.api_key", "sk-rotated-value", "rotated"); err != nil {
		t.Fatalf("Set (rotate): %v", err)
	}
	revealed, err = secretsSvc.Reveal(ctx, ac, "ai_provider.openai.api_key")
	if err != nil {
		t.Fatalf("Reveal (after rotate): %v", err)
	}
	if revealed != "sk-rotated-value" {
		t.Fatalf("expected rotated value, got %q", revealed)
	}

	if err := secretsSvc.Delete(ctx, ac, "ai_provider.openai.api_key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := secretsSvc.Reveal(ctx, ac, "ai_provider.openai.api_key"); err == nil {
		t.Fatal("expected Reveal to fail for a deleted secret")
	}
}

// UpdateDescription changes only the description metadata — the plaintext
// value must survive untouched, proving this path never re-encrypts or
// otherwise disturbs the stored ciphertext.
func TestSecretsUpdateDescriptionLeavesValueUntouched(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "secrets-update-owner@nodera.dev")

	secretsSvc, err := secrets.New(pool, h.audit, testEncryptionKey(t))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}

	if _, err := secretsSvc.Set(ctx, ac, "update-desc-key", "sk-should-not-change", "original description"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	updated, err := secretsSvc.UpdateDescription(ctx, ac, "update-desc-key", "corrected description")
	if err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}
	if updated.Description != "corrected description" {
		t.Fatalf("expected description to be updated, got %q", updated.Description)
	}

	revealed, err := secretsSvc.Reveal(ctx, ac, "update-desc-key")
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if revealed != "sk-should-not-change" {
		t.Fatalf("UpdateDescription must never change the plaintext value, got %q", revealed)
	}

	if _, err := secretsSvc.UpdateDescription(ctx, ac, "does-not-exist", "x"); err == nil {
		t.Fatal("expected UpdateDescription on a nonexistent secret to fail")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

func TestSecretsUpdateDescriptionRequiresManagePermission(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "secrets-update-perm-owner@nodera.dev")

	secretsSvc, err := secrets.New(pool, h.audit, testEncryptionKey(t))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	if _, err := secretsSvc.Set(ctx, ac, "guarded-key", "value", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	memberAC := h.newMemberContext(t, ctx, ac.OrganizationID, "secrets-update-perm-member@nodera.dev")

	if _, err := secretsSvc.UpdateDescription(ctx, memberAC, "guarded-key", "should not apply"); err == nil {
		t.Fatal("expected a member without secrets.manage to be forbidden from updating a secret's description")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// A different organization's owner must never be able to read or reveal
// another organization's secret, even with a valid encryption key and a
// fully-privileged AuthContext of their own (ADR-004 tenant isolation).
func TestSecretsAreTenantIsolated(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ownerA, _ := h.newOwnerContext(t, ctx, "secrets-a@nodera.dev")
	ownerB, _ := h.newOwnerContext(t, ctx, "secrets-b@nodera.dev")

	key := testEncryptionKey(t)
	secretsSvc, err := secrets.New(pool, h.audit, key)
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}

	if _, err := secretsSvc.Set(ctx, ownerA, "shared-key-name", "org-a-secret", ""); err != nil {
		t.Fatalf("Set (org A): %v", err)
	}

	list, err := secretsSvc.List(ctx, ownerB)
	if err != nil {
		t.Fatalf("List (org B): %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected org B to see zero secrets, got %d", len(list))
	}

	if _, err := secretsSvc.Reveal(ctx, ownerB, "shared-key-name"); err == nil {
		t.Fatal("expected org B to be unable to reveal org A's secret")
	}
}

// A wrong encryption key must fail to decrypt rather than silently
// returning garbage (GCM is authenticated, so tampering/wrong-key is
// detected, not just garbled).
func TestSecretsWrongKeyFailsToDecrypt(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "secrets-wrongkey@nodera.dev")

	writer, err := secrets.New(pool, h.audit, testEncryptionKey(t))
	if err != nil {
		t.Fatalf("secrets.New (writer): %v", err)
	}
	if _, err := writer.Set(ctx, ac, "some-key", "value", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	reader, err := secrets.New(pool, h.audit, testEncryptionKey(t)) // different random key
	if err != nil {
		t.Fatalf("secrets.New (reader): %v", err)
	}
	if _, err := reader.Reveal(ctx, ac, "some-key"); err == nil {
		t.Fatal("expected Reveal with the wrong encryption key to fail")
	}
}
