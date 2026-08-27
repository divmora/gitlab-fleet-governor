package mockserver

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// FaultRule defines a matching criteria and error response to inject.
type FaultRule struct {
	Method            string
	PathPrefix        string
	StatusCode        int
	FailCount         int
	Count             int // alias for FailCount
	RetryAfterSeconds int
	Message           string
}

// FaultEngine controls deterministic fault injection for mock server endpoints.
type FaultEngine struct {
	mu sync.Mutex

	rules []FaultRule

	rateLimitHeadersActive bool
	rateLimitLimit         int
	rateLimitRemaining     int
	rateLimitReset         time.Time
}

// NewFaultEngine creates a new FaultEngine.
func NewFaultEngine() *FaultEngine {
	return &FaultEngine{
		rules: make([]FaultRule, 0),
	}
}

// AddRule adds an arbitrary FaultRule to the engine.
func (fe *FaultEngine) AddRule(rule FaultRule) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if rule.FailCount == 0 && rule.Count > 0 {
		rule.FailCount = rule.Count
	}
	fe.rules = append(fe.rules, rule)
}

// Clear clears all active fault rules.
func (fe *FaultEngine) Clear() {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	fe.rules = make([]FaultRule, 0)
	fe.rateLimitHeadersActive = false
}

// Inject429 injects HTTP 429 Too Many Requests on matching endpoints for failCount times.
func (fe *FaultEngine) Inject429(method, pathPrefix string, failCount, retryAfterSeconds int) {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	fe.rules = append(fe.rules, FaultRule{
		Method:            strings.ToUpper(method),
		PathPrefix:        pathPrefix,
		StatusCode:        http.StatusTooManyRequests,
		FailCount:         failCount,
		RetryAfterSeconds: retryAfterSeconds,
		Message:           `{"message":"429 Too Many Requests - Rate limit exceeded"}`,
	})
}

// Inject5xx injects HTTP 500/502/503/504 errors on matching endpoints for failCount times.
func (fe *FaultEngine) Inject5xx(method, pathPrefix string, statusCode, failCount int) {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	fe.rules = append(fe.rules, FaultRule{
		Method:     strings.ToUpper(method),
		PathPrefix: pathPrefix,
		StatusCode: statusCode,
		FailCount:  failCount,
		Message:    fmt.Sprintf(`{"message":"%d %s"}`, statusCode, http.StatusText(statusCode)),
	})
}

// SetRateLimitHeaders configures global rate limit headers to attach to all responses.
func (fe *FaultEngine) SetRateLimitHeaders(limit, remaining int, resetTime time.Time) {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	fe.rateLimitHeadersActive = true
	fe.rateLimitLimit = limit
	fe.rateLimitRemaining = remaining
	fe.rateLimitReset = resetTime
}

// MatchAndApply checks if the incoming request matches any fault injection rules.
// If matched, it decrements the rule's FailCount and returns the injected response.
func (fe *FaultEngine) MatchAndApply(r *http.Request) (bool, int, map[string]string, []byte) {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	headers := make(map[string]string)

	// Attach global rate limit headers if configured
	if fe.rateLimitHeadersActive {
		headers["RateLimit-Limit"] = fmt.Sprintf("%d", fe.rateLimitLimit)
		headers["RateLimit-Remaining"] = fmt.Sprintf("%d", fe.rateLimitRemaining)
		headers["RateLimit-Reset"] = fmt.Sprintf("%d", fe.rateLimitReset.Unix())
	}

	for i := range fe.rules {
		rule := &fe.rules[i]
		if rule.FailCount <= 0 {
			continue
		}

		if rule.Method != "" && rule.Method != r.Method {
			continue
		}

		if rule.PathPrefix != "" && !strings.HasPrefix(r.URL.Path, rule.PathPrefix) {
			continue
		}

		// Matched rule: decrement counter
		rule.FailCount--

		if rule.RetryAfterSeconds > 0 {
			headers["Retry-After"] = fmt.Sprintf("%d", rule.RetryAfterSeconds)
		}

		return true, rule.StatusCode, headers, []byte(rule.Message)
	}

	return false, 0, headers, nil
}
