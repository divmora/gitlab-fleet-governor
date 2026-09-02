package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleDirectInvocation processes direct JSON payloads.
func (h *Handler) handleDirectInvocation(ctx context.Context, rawEvent []byte, startTime time.Time) *LambdaResponse {
	payload := &DirectInvocationPayload{}
	if len(rawEvent) > 0 && string(rawEvent) != "{}" && string(rawEvent) != "null" {
		if err := json.Unmarshal(rawEvent, payload); err != nil {
			return BuildErrorResponse(EventTypeDirectInvocation, http.StatusBadRequest, fmt.Errorf("invalid direct invocation JSON payload: %w", err), startTime)
		}
	}

	cfg, sourceDesc, err := h.resolver.ResolveFromDirectPayload(ctx, payload)
	if err != nil {
		return BuildErrorResponse(EventTypeDirectInvocation, http.StatusBadRequest, fmt.Errorf("failed to resolve policy config: %w", err), startTime)
	}

	dryRun := true
	if cfg.Settings.DryRun != nil {
		dryRun = *cfg.Settings.DryRun
	}

	execRes, err := h.executeEngine(ctx, cfg)
	return BuildLambdaResponse(EventTypeDirectInvocation, sourceDesc, dryRun, execRes, err, startTime)
}
