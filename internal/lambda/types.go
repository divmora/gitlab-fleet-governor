package lambda

import (
	"encoding/json"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// EventType identifies the trigger event classification.
type EventType string

const (
	EventTypeDirectInvocation    EventType = "DIRECT_INVOCATION"
	EventTypeEventBridgeSchedule EventType = "EVENTBRIDGE_SCHEDULED"
	EventTypeS3Put               EventType = "S3_OBJECT_CREATED"
	EventTypeAPIGateway          EventType = "API_GATEWAY_PROXY"
	EventTypeUnknown             EventType = "UNKNOWN"
)

// EventBridgeScheduledEvent matches the AWS EventBridge / CloudWatch Scheduled Event schema.
type EventBridgeScheduledEvent struct {
	Version    string          `json:"version"`
	ID         string          `json:"id"`
	DetailType string          `json:"detail-type"`
	Source     string          `json:"source"`
	Account    string          `json:"account"`
	Time       string          `json:"time"`
	Region     string          `json:"region"`
	Resources  []string        `json:"resources"`
	Detail     json.RawMessage `json:"detail"`
}

// S3Event matches the AWS S3 ObjectCreated notification schema.
type S3Event struct {
	Records []S3EventRecord `json:"Records"`
}

// S3EventRecord represents a single S3 event record.
type S3EventRecord struct {
	EventVersion string   `json:"eventVersion"`
	EventSource  string   `json:"eventSource"`
	AWSRegion    string   `json:"awsRegion"`
	EventTime    string   `json:"eventTime"`
	EventName    string   `json:"eventName"`
	S3           S3Entity `json:"s3"`
}

// S3Entity encapsulates S3 bucket and object metadata.
type S3Entity struct {
	SchemaVersion string         `json:"s3SchemaVersion"`
	Configuration string         `json:"configurationId"`
	Bucket        S3BucketEntity `json:"bucket"`
	Object        S3ObjectEntity `json:"object"`
}

// S3BucketEntity represents the S3 bucket details.
type S3BucketEntity struct {
	Name string `json:"name"`
	ARN  string `json:"arn"`
}

// S3ObjectEntity represents the S3 object details.
type S3ObjectEntity struct {
	Key       string `json:"key"`
	Size      int64  `json:"size"`
	ETag      string `json:"eTag"`
	Sequencer string `json:"sequencer"`
}

// DirectInvocationPayload represents direct Lambda invocation parameters.
type DirectInvocationPayload struct {
	Action              string               `json:"action,omitempty"`
	Config              *config.PolicyConfig `json:"config,omitempty"`
	ConfigS3URI         string               `json:"config_s3_uri,omitempty"`
	ConfigContent       string               `json:"config_content,omitempty"`
	ConfigYAML          string               `json:"config_yaml,omitempty"`
	ConfigJSON          string               `json:"config_json,omitempty"`
	DryRun              *bool                `json:"dry_run,omitempty"`
	Concurrency         int                  `json:"concurrency,omitempty"`
	LogLevel            string               `json:"log_level,omitempty"`
	LogFormat           string               `json:"log_format,omitempty"`
	GroupIDsInclude     []int                `json:"group_ids_include,omitempty"`
	GroupIDsExclude     []int                `json:"group_ids_exclude,omitempty"`
	GroupPathsInclude   []string             `json:"group_paths_include,omitempty"`
	GroupPathsExclude   []string             `json:"group_paths_exclude,omitempty"`
	NamespacesInclude   []string             `json:"namespaces_include,omitempty"`
	NamespacesExclude   []string             `json:"namespaces_exclude,omitempty"`
	ProjectRegexInclude string               `json:"project_regex_include,omitempty"`
	ProjectRegexExclude string               `json:"project_regex_exclude,omitempty"`
}

// APIGatewayProxyRequest represents an API Gateway HTTP/REST proxy request.
type APIGatewayProxyRequest struct {
	HTTPMethod string            `json:"httpMethod"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	IsBase64   bool              `json:"isBase64Encoded"`
}

// APIGatewayProxyResponse represents an API Gateway HTTP/REST proxy response.
type APIGatewayProxyResponse struct {
	StatusCode        int                 `json:"statusCode"`
	Headers           map[string]string   `json:"headers"`
	MultiValueHeaders map[string][]string `json:"multiValueHeaders,omitempty"`
	Body              string              `json:"body"`
	IsBase64Encoded   bool                `json:"isBase64Encoded"`
}

// LambdaResponse is the standardized structured response returned by all Lambda invocations.
type LambdaResponse struct {
	StatusCode          int                    `json:"statusCode"`
	Status              string                 `json:"status"` // "SUCCESS", "PARTIAL_SUCCESS", "FAILED"
	EventType           EventType              `json:"event_type"`
	ConfigSource        string                 `json:"config_source,omitempty"`
	DryRun              bool                   `json:"dry_run"`
	Summary             *ExecutionSummary      `json:"summary,omitempty"`
	ChangedProjects     []ProjectChangeSummary `json:"changed_projects,omitempty"`
	Metrics             *ExecutionMetrics      `json:"metrics,omitempty"`
	Errors              []string               `json:"errors,omitempty"`
	ExecutionDurationMS int64                  `json:"execution_duration_ms"`
}

// ExecutionSummary encapsulates high-level fleet operation counts.
type ExecutionSummary struct {
	ScannedGroups     int `json:"scanned_groups"`
	MatchedGroups     int `json:"matched_groups"`
	ScannedProjects   int `json:"scanned_projects"`
	MatchedProjects   int `json:"matched_projects"`
	AppliedOperations int `json:"applied_operations"`
	SkippedOperations int `json:"skipped_operations"`
	FailedOperations  int `json:"failed_operations"`
}

// ProjectChangeSummary summarizes changes made to a single project.
type ProjectChangeSummary struct {
	ProjectID   int      `json:"project_id"`
	ProjectPath string   `json:"project_path"`
	Action      string   `json:"action"`
	Operations  []string `json:"operations"`
}

// ExecutionMetrics provides execution timing and fleet statistics.
type ExecutionMetrics struct {
	TotalScanned   int           `json:"total_scanned"`
	TotalTargeted  int           `json:"total_targeted"`
	TotalChanged   int           `json:"total_changed"`
	TotalUnchanged int           `json:"total_unchanged"`
	TotalFailed    int           `json:"total_failed"`
	Duration       time.Duration `json:"duration"`
}
