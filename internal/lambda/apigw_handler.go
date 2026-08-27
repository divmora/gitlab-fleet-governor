package lambda

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleAPIGatewayEvent processes API Gateway / HTTP API proxy events and returns APIGatewayProxyResponse.
func (h *Handler) handleAPIGatewayEvent(ctx context.Context, rawEvent []byte, startTime time.Time) (*APIGatewayProxyResponse, error) {
	var apigwReq APIGatewayProxyRequest
	if err := json.Unmarshal(rawEvent, &apigwReq); err != nil {
		return formatAPIGatewayError(http.StatusBadRequest, fmt.Errorf("failed to parse API Gateway request: %w", err)), nil
	}

	bodyBytes := []byte(apigwReq.Body)
	if apigwReq.IsBase64 {
		decoded, err := base64.StdEncoding.DecodeString(apigwReq.Body)
		if err != nil {
			return formatAPIGatewayError(http.StatusBadRequest, fmt.Errorf("failed to decode base64 body: %w", err)), nil
		}
		bodyBytes = decoded
	}

	lambdaResp := h.handleDirectInvocation(ctx, bodyBytes, startTime)
	lambdaResp.EventType = EventTypeAPIGateway

	bodyJSON, _ := json.Marshal(lambdaResp)
	return &APIGatewayProxyResponse{
		StatusCode: lambdaResp.StatusCode,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: string(bodyJSON),
	}, nil
}

func formatAPIGatewayError(statusCode int, err error) *APIGatewayProxyResponse {
	resp := LambdaResponse{
		StatusCode: statusCode,
		Status:     "FAILED",
		EventType:  EventTypeAPIGateway,
		Errors:     []string{err.Error()},
	}
	body, _ := json.Marshal(resp)
	return &APIGatewayProxyResponse{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(body),
	}
}
