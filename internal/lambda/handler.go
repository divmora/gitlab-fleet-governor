package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// EngineExecutor is the mockable execution interface for the GovernanceEngine.
type EngineExecutor interface {
	Execute(ctx context.Context, cfg *config.PolicyConfig) (*engine.ExecutionResult, error)
}

// GitLabClientFactory creates a GitLabClient given settings.
type GitLabClientFactory func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error)

// EngineFactory creates an EngineExecutor given a client and config.
type EngineFactory func(client gitlab.GitLabClient, cfg *config.PolicyConfig) EngineExecutor

// Handler is the central AWS Lambda handler for all cloud triggers.
type Handler struct {
	s3Client      config.S3ClientAPI
	resolver      *ConfigResolver
	clientFactory GitLabClientFactory
	engineFactory EngineFactory
	lookupEnv     config.EnvLookupFunc
}

// HandlerOption configures Handler.
type HandlerOption func(*Handler)

// WithS3Client injects a mock or custom S3 client.
func WithS3Client(s3Client config.S3ClientAPI) HandlerOption {
	return func(h *Handler) {
		h.s3Client = s3Client
	}
}

// WithClientFactory overrides the default GitLab client factory.
func WithClientFactory(factory GitLabClientFactory) HandlerOption {
	return func(h *Handler) {
		h.clientFactory = factory
	}
}

// WithEngineFactory overrides the default GovernanceEngine factory.
func WithEngineFactory(factory EngineFactory) HandlerOption {
	return func(h *Handler) {
		h.engineFactory = factory
	}
}

// WithEnvLookup overrides the environment lookup function.
func WithEnvLookup(lookup config.EnvLookupFunc) HandlerOption {
	return func(h *Handler) {
		h.lookupEnv = lookup
	}
}

// NewHandler constructs an initialized Handler with defaults.
func NewHandler(opts ...HandlerOption) *Handler {
	h := &Handler{
		lookupEnv: os.LookupEnv,
		clientFactory: func(cfg *config.GitLabSettingsConfig, lookup ...config.EnvLookupFunc) (gitlab.GitLabClient, error) {
			var lookupFn gitlab.EnvLookupFunc
			if len(lookup) > 0 && lookup[0] != nil {
				lookupFn = gitlab.EnvLookupFunc(lookup[0])
			}
			return gitlab.NewClientFromConfig(cfg, lookupFn)
		},
		engineFactory: func(client gitlab.GitLabClient, cfg *config.PolicyConfig) EngineExecutor {
			dryRun := true
			if cfg != nil && cfg.Settings.DryRun != nil {
				dryRun = *cfg.Settings.DryRun
			}
			concurrency := 10
			if cfg != nil && cfg.Settings.Concurrency > 0 {
				concurrency = cfg.Settings.Concurrency
			}
			return engine.NewEngine(client, engine.WithConcurrency(concurrency), engine.WithDryRun(dryRun))
		},
	}

	for _, opt := range opts {
		opt(h)
	}

	h.resolver = NewConfigResolver(h.s3Client, h.lookupEnv)
	return h
}

// HandleRequest is the main Lambda entry point invoked by lambda.Start.
func (h *Handler) HandleRequest(ctx context.Context, rawEvent json.RawMessage) (resp any, err error) {
	startTime := time.Now()
	var eventType EventType = EventTypeUnknown

	// Panic Recovery Harness for AWS Lambda
	defer func() {
		if r := recover(); r != nil {
			stackTrace := string(debug.Stack())
			resp = BuildErrorResponse(eventType, http.StatusInternalServerError, fmt.Errorf("panic in lambda handler: %v\nstack: %s", r, stackTrace), startTime)
			err = nil
		}
	}()

	detectedType, detErr := DetectEventType(rawEvent)
	if detErr != nil {
		resp = BuildErrorResponse(EventTypeUnknown, http.StatusBadRequest, fmt.Errorf("failed to detect event type: %w", detErr), startTime)
		return resp, nil
	}
	eventType = detectedType

	switch eventType {
	case EventTypeS3Put:
		resp = h.handleS3Event(ctx, rawEvent, startTime)
	case EventTypeEventBridgeSchedule:
		resp = h.handleEventBridgeEvent(ctx, rawEvent, startTime)
	case EventTypeAPIGateway:
		resp, err = h.handleAPIGatewayEvent(ctx, rawEvent, startTime)
		return resp, err
	case EventTypeDirectInvocation:
		resp = h.handleDirectInvocation(ctx, rawEvent, startTime)
	default:
		resp = BuildErrorResponse(eventType, http.StatusBadRequest, fmt.Errorf("unsupported event type: %s", eventType), startTime)
	}

	return resp, nil
}
