package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// handleS3Event processes S3 ObjectCreated notification events.
func (h *Handler) handleS3Event(ctx context.Context, rawEvent []byte, startTime time.Time) *LambdaResponse {
	var s3Event S3Event
	if err := json.Unmarshal(rawEvent, &s3Event); err != nil {
		return BuildErrorResponse(EventTypeS3Put, http.StatusBadRequest, fmt.Errorf("failed to parse S3 event payload: %w", err), startTime)
	}

	if len(s3Event.Records) == 0 {
		return BuildErrorResponse(EventTypeS3Put, http.StatusBadRequest, fmt.Errorf("S3 event contains no records"), startTime)
	}

	var combinedErrors []string
	var allChangedProjects []ProjectChangeSummary
	summary := &ExecutionSummary{}
	metrics := &ExecutionMetrics{}
	var lastConfigSource string
	var lastDryRun bool

	for _, record := range s3Event.Records {
		bucket := record.S3.Bucket.Name
		rawKey := record.S3.Object.Key

		// S3 Event Object Keys are URL-encoded (e.g. spaces as '+' or '%20')
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			key = rawKey
		}

		// Filter out non-configuration files
		ext := strings.ToLower(filepath.Ext(key))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}

		s3URI := fmt.Sprintf("s3://%s/%s", bucket, key)
		lastConfigSource = s3URI

		cfg, sourceDesc, err := h.resolver.ResolveFromS3URI(ctx, s3URI)
		if err != nil {
			combinedErrors = append(combinedErrors, fmt.Sprintf("failed to resolve S3 config %q: %v", s3URI, err))
			continue
		}

		if cfg.Settings.DryRun != nil {
			lastDryRun = *cfg.Settings.DryRun
		} else {
			lastDryRun = true
		}

		execRes, err := h.executeEngine(ctx, cfg)
		if err != nil {
			combinedErrors = append(combinedErrors, fmt.Sprintf("engine execution failed for %q: %v", sourceDesc, err))
			continue
		}

		// Accumulate metrics and changes
		mergeEngineResult(summary, metrics, &allChangedProjects, execRes)
	}

	duration := time.Since(startTime)
	status := "SUCCESS"
	statusCode := http.StatusOK

	if len(combinedErrors) > 0 {
		if summary.AppliedOperations > 0 || summary.MatchedProjects > 0 {
			status = "PARTIAL_SUCCESS"
			statusCode = http.StatusOK
		} else {
			status = "FAILED"
			statusCode = http.StatusInternalServerError
		}
	}

	return &LambdaResponse{
		StatusCode:          statusCode,
		Status:              status,
		EventType:           EventTypeS3Put,
		ConfigSource:        lastConfigSource,
		DryRun:              lastDryRun,
		Summary:             summary,
		ChangedProjects:     allChangedProjects,
		Metrics:             metrics,
		Errors:              combinedErrors,
		ExecutionDurationMS: duration.Milliseconds(),
	}
}
