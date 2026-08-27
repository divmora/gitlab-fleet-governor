package lambda_test

import (
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/lambda"
)

func TestDetectEventType(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		expected lambda.EventType
	}{
		{
			name:     "empty payload",
			payload:  "",
			expected: lambda.EventTypeDirectInvocation,
		},
		{
			name:     "empty json object",
			payload:  "{}",
			expected: lambda.EventTypeDirectInvocation,
		},
		{
			name: "s3 put object notification",
			payload: `{
				"Records": [
					{
						"eventVersion": "2.1",
						"eventSource": "aws:s3",
						"eventName": "ObjectCreated:Put",
						"s3": {
							"bucket": {"name": "governance-bucket"},
							"object": {"key": "policies/governance.yaml"}
						}
					}
				]
			}`,
			expected: lambda.EventTypeS3Put,
		},
		{
			name: "eventbridge scheduled event source",
			payload: `{
				"version": "0",
				"id": "53ac4291-b770-435c-a07e-826bb64a71bd",
				"detail-type": "Scheduled Event",
				"source": "aws.events",
				"time": "2026-08-25T12:00:00Z",
				"resources": ["arn:aws:events:us-east-1:123456789012:rule/my-schedule"]
			}`,
			expected: lambda.EventTypeEventBridgeSchedule,
		},
		{
			name: "eventbridge rule arn in resources",
			payload: `{
				"resources": ["arn:aws:events:us-east-1:123456789012:rule/hourly-audit"]
			}`,
			expected: lambda.EventTypeEventBridgeSchedule,
		},
		{
			name: "api gateway proxy request",
			payload: `{
				"httpMethod": "POST",
				"path": "/govern",
				"body": "{\"dry_run\": true}"
			}`,
			expected: lambda.EventTypeAPIGateway,
		},
		{
			name: "direct invocation with inline config",
			payload: `{
				"config_yaml": "version: 'v1'\nsettings:\n  concurrency: 5",
				"dry_run": false
			}`,
			expected: lambda.EventTypeDirectInvocation,
		},
		{
			name: "direct invocation with s3 uri",
			payload: `{
				"config_s3_uri": "s3://my-bucket/policy.yaml"
			}`,
			expected: lambda.EventTypeDirectInvocation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lambda.DetectEventType([]byte(tt.payload))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("DetectEventType() = %v, expected %v", got, tt.expected)
			}
		})
	}
}
