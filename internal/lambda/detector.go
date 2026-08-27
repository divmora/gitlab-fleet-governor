package lambda

import (
	"bytes"
	"encoding/json"
	"strings"
)

// DetectEventType inspects a raw JSON payload and determines the trigger event type.
func DetectEventType(data []byte) (EventType, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "{}" || string(trimmed) == "null" {
		return EventTypeDirectInvocation, nil
	}

	// 1. Probe for S3 ObjectCreated Event
	var s3Probe struct {
		Records []struct {
			EventSource string `json:"eventSource"`
			EventName   string `json:"eventName"`
			S3          struct {
				Bucket struct {
					Name string `json:"name"`
				} `json:"bucket"`
				Object struct {
					Key string `json:"key"`
				} `json:"object"`
			} `json:"s3"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(trimmed, &s3Probe); err == nil && len(s3Probe.Records) > 0 {
		if s3Probe.Records[0].EventSource == "aws:s3" || s3Probe.Records[0].S3.Bucket.Name != "" {
			return EventTypeS3Put, nil
		}
	}

	// 2. Probe for EventBridge Scheduled Event / CloudWatch Event
	var ebProbe struct {
		Source     string   `json:"source"`
		DetailType string   `json:"detail-type"`
		Resources  []string `json:"resources"`
	}
	if err := json.Unmarshal(trimmed, &ebProbe); err == nil {
		if ebProbe.Source == "aws.events" || ebProbe.DetailType == "Scheduled Event" || hasEventBridgeResource(ebProbe.Resources) {
			return EventTypeEventBridgeSchedule, nil
		}
	}

	// 3. Probe for API Gateway HTTP/REST Proxy Event
	var apigwProbe struct {
		HTTPMethod string `json:"httpMethod"`
		Path       string `json:"path"`
	}
	if err := json.Unmarshal(trimmed, &apigwProbe); err == nil && apigwProbe.HTTPMethod != "" {
		return EventTypeAPIGateway, nil
	}

	// 4. Default: Direct JSON Invocation
	return EventTypeDirectInvocation, nil
}

func hasEventBridgeResource(resources []string) bool {
	for _, r := range resources {
		if strings.Contains(r, ":events:") || strings.Contains(r, "rule/") {
			return true
		}
	}
	return false
}
