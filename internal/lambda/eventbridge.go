package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleEventBridgeEvent processes EventBridge scheduled cron events.
func (h *Handler) handleEventBridgeEvent(ctx context.Context, rawEvent []byte, startTime time.Time) *LambdaResponse {
	var ebEvent EventBridgeScheduledEvent
	if err := json.Unmarshal(rawEvent, &ebEvent); err != nil {
		return BuildErrorResponse(EventTypeEventBridgeSchedule, http.StatusBadRequest, fmt.Errorf("failed to parse EventBridge scheduled event: %w", err), startTime)
	}

	payload := &DirectInvocationPayload{}
	if len(ebEvent.Detail) > 0 && string(ebEvent.Detail) != "{}" && string(ebEvent.Detail) != "null" {
		_ = json.Unmarshal(ebEvent.Detail, payload)
	}

	cfg, sourceDesc, err := h.resolver.ResolveFromDirectPayload(ctx, payload)
	if err != nil {
		return BuildErrorResponse(EventTypeEventBridgeSchedule, http.StatusBadRequest, fmt.Errorf("failed to resolve policy config for EventBridge event: %w", err), startTime)
	}

	dryRun := true
	if cfg.Settings.DryRun != nil {
		dryRun = *cfg.Settings.DryRun
	}

	execRes, err := h.executeEngine(ctx, cfg)
	return BuildLambdaResponse(EventTypeEventBridgeSchedule, sourceDesc, dryRun, execRes, err, startTime)
}
