package integration_test

import (
	"context"
	"testing"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/secrets"
	"github.com/nodera/nodera/internal/testhelpers"
)

// The full platform-secret lifecycle: set, list (metadata only), update
// description without re-supplying the value, reveal (Go-only, never
// HTTP), delete. Mirrors the org-scoped secrets test, over the
// platform-scope methods and gated by platform.secrets.manage/read
// instead of internal/rbac.
func TestPlatformSecrets_SetListRevealDelete(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "platform-secrets-owner@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.secrets.manage")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.secrets.read")

	secretsSvc, err := secrets.New(pool, h.audit, h.platform, testEncryptionKey(t))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}

	m, err := secretsSvc.SetPlatform(ctx, ac, "ai.anthropic.api_key", "sk-ant-super-secret-value", "Anthropic platform key")
	if err != nil {
		t.Fatalf("SetPlatform: %v", err)
	}
	if m.Key != "ai.anthropic.api_key" || m.Description != "Anthropic platform key" {
		t.Fatalf("unexpected meta: %+v", m)
	}

	list, err := secretsSvc.ListPlatform(ctx, ac)
	if err != nil {
		t.Fatalf("ListPlatform: %v", err)
	}
	if len(list) != 1 || list[0].Key != "ai.anthropic.api_key" {
		t.Fatalf("expected exactly one platform secret listed, got %+v", list)
	}

	value, err := secretsSvc.RevealPlatform(ctx, ac, "ai.anthropic.api_key")
	if err != nil {
		t.Fatalf("RevealPlatform: %v", err)
	}
	if value != "sk-ant-super-secret-value" {
		t.Fatalf("expected the real plaintext back, got %q", value)
	}

	updated, err := secretsSvc.UpdateDescriptionPlatform(ctx, ac, "ai.anthropic.api_key", "rotated key, same value")
	if err != nil {
		t.Fatalf("UpdateDescriptionPlatform: %v", err)
	}
	if updated.Description != "rotated key, same value" {
		t.Fatalf("expected description to update, got %+v", updated)
	}
	// The value must survive a description-only update untouched.
	value2, err := secretsSvc.RevealPlatform(ctx, ac, "ai.anthropic.api_key")
	if err != nil {
		t.Fatalf("RevealPlatform after description update: %v", err)
	}
	if value2 != "sk-ant-super-secret-value" {
		t.Fatalf("expected the value to survive a description-only update, got %q", value2)
	}

	if err := secretsSvc.DeletePlatform(ctx, ac, "ai.anthropic.api_key"); err != nil {
		t.Fatalf("DeletePlatform: %v", err)
	}
	if _, err := secretsSvc.RevealPlatform(ctx, ac, "ai.anthropic.api_key"); err == nil {
		t.Fatal("expected RevealPlatform to fail for a deleted secret")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeNotFound {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}

// A caller without any platform.secrets.* grant — even a full
// organization owner — is forbidden from every Platform* method. Org
// secrets permissions (secrets.manage/secrets.read) grant nothing here;
// these are genuinely separate authorization scopes.
func TestPlatformSecrets_RequiresPlatformGrant(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "no-platform-secrets-grant@nodera.dev")

	secretsSvc, err := secrets.New(pool, h.audit, h.platform, testEncryptionKey(t))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}

	if _, err := secretsSvc.SetPlatform(ctx, ac, "some.key", "value", ""); err == nil {
		t.Fatal("expected SetPlatform to require platform.secrets.manage")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}

	if _, err := secretsSvc.ListPlatform(ctx, ac); err == nil {
		t.Fatal("expected ListPlatform to require platform.secrets.read")
	} else if ae, ok := err.(*apierr.Error); !ok || ae.Code != apierr.CodeForbidden {
		t.Fatalf("expected FORBIDDEN, got %v", err)
	}
}

// A platform secret encrypted under one key can never be decrypted under
// a different one — the platform-scope counterpart of the org-scoped
// wrong-key test, proving the cipher/key handling isn't accidentally
// shared/cached across Service instances.
func TestPlatformSecrets_WrongKeyFailsToDecrypt(t *testing.T) {
	pool := testhelpers.RequirePool(t)
	ctx := context.Background()
	h := newHarness(pool)
	ac, _ := h.newOwnerContext(t, ctx, "platform-secrets-wrongkey@nodera.dev")
	h.grantPlatformPermission(t, ctx, ac.ActorID, "platform.secrets.manage")

	writer, err := secrets.New(pool, h.audit, h.platform, testEncryptionKey(t))
	if err != nil {
		t.Fatalf("secrets.New (writer): %v", err)
	}
	if _, err := writer.SetPlatform(ctx, ac, "cross-key-test", "top secret", ""); err != nil {
		t.Fatalf("SetPlatform: %v", err)
	}

	reader, err := secrets.New(pool, h.audit, h.platform, testEncryptionKey(t)) // different random key
	if err != nil {
		t.Fatalf("secrets.New (reader): %v", err)
	}
	if _, err := reader.RevealPlatform(ctx, ac, "cross-key-test"); err == nil {
		t.Fatal("expected RevealPlatform to fail when decrypting under a different key")
	}
}
