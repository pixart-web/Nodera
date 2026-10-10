package ops

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	"github.com/nodera/nodera/internal/tools"
)

// ToolRegistrar is satisfied by *tools.Registry.
type ToolRegistrar interface {
	RegisterHandler(toolKey string, h tools.Handler)
}

// BridgeTools registers a Tool Gateway handler for every operation that
// declares a ToolKey. The gateway has already checked permission and risk tier
// and (for privileged/critical tools) obtained a human approval before the
// handler runs, so the handler submits the operation with SubmitTrusted. The
// handler returns the job reference; the work itself still runs as a normal
// persisted, audited operation.
func (e *Engine) BridgeTools(reg ToolRegistrar) {
	for _, d := range e.defs {
		if d.ToolKey == "" {
			continue
		}
		d := d
		reg.RegisterHandler(d.ToolKey, func(ctx context.Context, ac authctx.AuthContext, resourceType, resourceID string, params map[string]any) (any, error) {
			payload := map[string]any{}
			for k, v := range params {
				payload[k] = v
			}
			var projectID uuid.UUID
			if resourceType == "project" {
				id, err := uuid.Parse(resourceID)
				if err != nil {
					return nil, apierr.Validation("resource_id must be a project id")
				}
				projectID = id
			} else {
				payload["resource_type"], payload["resource_id"] = resourceType, resourceID
				if s, ok := params["project_id"].(string); ok {
					if id, err := uuid.Parse(s); err == nil {
						projectID = id
					}
				}
			}
			ref, err := e.SubmitTrusted(ctx, ac, SubmitInput{Operation: d.Name, ProjectID: projectID, Payload: payload})
			if err != nil {
				return nil, err
			}
			return map[string]any{"job_id": ref.JobID, "operation": d.Name}, nil
		})
	}
}

// PayloadResource decodes the resource id/type injected by BridgeTools.
func PayloadResource(payload json.RawMessage) (resourceType string, id uuid.UUID) {
	var p struct {
		Type string `json:"resource_type"`
		ID   string `json:"resource_id"`
	}
	_ = json.Unmarshal(payload, &p)
	id, _ = uuid.Parse(p.ID)
	return p.Type, id
}
