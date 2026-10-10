// Package flags implements feature flags: a global default per flag (managed
// by platform administrators) with per-organisation overrides (managed by the
// organisation owner). Engines consult Enabled before accepting work, so a
// feature can be switched off for a tenant without a deployment.
package flags

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/rbac"
)

type AuditRecorder interface {
	Record(ctx context.Context, ac authctx.AuthContext, e audit.Entry) error
}
type PlatformAuthorizer interface {
	Require(ctx context.Context, ac authctx.AuthContext, key string) error
}

type Service struct {
	pool     *pgxpool.Pool
	audit    AuditRecorder
	platform PlatformAuthorizer
}

func New(pool *pgxpool.Pool, a AuditRecorder, p PlatformAuthorizer) *Service {
	return &Service{pool: pool, audit: a, platform: p}
}

type Flag struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Default     bool   `json:"default"`
	Override    *bool  `json:"override"`
	Enabled     bool   `json:"enabled"`
}

func (s *Service) List(ctx context.Context, ac authctx.AuthContext) ([]Flag, error) {
	rows, err := s.pool.Query(ctx, `SELECT f.key, f.description, f.default_enabled, o.enabled
		FROM feature_flags f LEFT JOIN organization_feature_flags o ON o.flag_key=f.key AND o.organization_id=$1 ORDER BY f.key`, ac.OrganizationID)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to list flags", err)
	}
	defer rows.Close()
	out := []Flag{}
	for rows.Next() {
		var f Flag
		if err := rows.Scan(&f.Key, &f.Description, &f.Default, &f.Override); err != nil {
			return nil, err
		}
		f.Enabled = f.Default
		if f.Override != nil {
			f.Enabled = *f.Override
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Enabled reports the effective value for an organisation. An unknown flag is
// treated as disabled (fail closed).
func (s *Service) Enabled(ctx context.Context, org uuid.UUID, key string) bool {
	var v bool
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(o.enabled, f.default_enabled) FROM feature_flags f
		LEFT JOIN organization_feature_flags o ON o.flag_key=f.key AND o.organization_id=$1 WHERE f.key=$2`, org, key).Scan(&v)
	return err == nil && v
}

// SetOverride sets (or, with enabled=nil, clears) the organisation override.
func (s *Service) SetOverride(ctx context.Context, ac authctx.AuthContext, key string, enabled *bool) error {
	if err := rbac.Require(ac, "organization.manage"); err != nil {
		return err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM feature_flags WHERE key=$1)`, key).Scan(&exists); err != nil || !exists {
		return apierr.NotFound("flag")
	}
	var err error
	if enabled == nil {
		_, err = s.pool.Exec(ctx, `DELETE FROM organization_feature_flags WHERE organization_id=$1 AND flag_key=$2`, ac.OrganizationID, key)
	} else {
		_, err = s.pool.Exec(ctx, `INSERT INTO organization_feature_flags (organization_id, flag_key, enabled) VALUES ($1,$2,$3)
			ON CONFLICT (organization_id, flag_key) DO UPDATE SET enabled=EXCLUDED.enabled`, ac.OrganizationID, key, *enabled)
	}
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to save flag", err)
	}
	_ = s.audit.Record(ctx, ac, audit.Entry{Action: "flags.override.set", ResourceType: "feature_flag", ResourceID: key, Success: true, ResultingState: map[string]any{"enabled": enabled}})
	return nil
}

// SetDefault changes the global default (platform administrators only).
func (s *Service) SetDefault(ctx context.Context, ac authctx.AuthContext, key string, enabled bool) error {
	if s.platform == nil {
		return apierr.New(apierr.CodeInternal, "platform authorizer not wired")
	}
	if err := s.platform.Require(ctx, ac, "platform.flags.manage"); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE feature_flags SET default_enabled=$2 WHERE key=$1`, key, enabled)
	if err != nil {
		return apierr.Wrap(apierr.CodeInternal, "failed to save flag", err)
	}
	if tag.RowsAffected() == 0 {
		return apierr.NotFound("flag")
	}
	_ = s.audit.Record(ctx, ac, audit.Entry{Action: "flags.default.set", ResourceType: "feature_flag", ResourceID: key, Success: true, ResultingState: map[string]any{"enabled": enabled}})
	return nil
}

var _ = errors.New
var _ = pgx.ErrNoRows
