package gitlab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Clock abstracts time operations to enable deterministic, zero-delay testing.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// RateLimitInfo captures the latest parsed GitLab rate limit headers.
type RateLimitInfo struct {
	Limit      int
	Remaining  int
	Reset      time.Time
	ObservedAt time.Time
}

// RetryListener is a callback invoked when a request is retried.
type RetryListener func(attempt int, req *http.Request, resp *http.Response, err error, delay time.Duration)

// GovernorTransportConfig holds configuration options for GovernorTransport.
type GovernorTransportConfig struct {
	BaseTransport  http.RoundTripper
	RateLimitRPS   float64
	RateLimitBurst int
	MaxRetries     int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	JitterRatio    float64
	Logger         *slog.Logger
	RetryListener  RetryListener
	Clock          Clock
}

// DefaultGovernorTransportConfig returns sensible production defaults.
func DefaultGovernorTransportConfig() GovernorTransportConfig {
	return GovernorTransportConfig{
		BaseTransport:  http.DefaultTransport,
		RateLimitRPS:   30.0,
		RateLimitBurst: 50,
		MaxRetries:     3,
		BaseBackoff:    500 * time.Millisecond,
		MaxBackoff:     30 * time.Second,
		JitterRatio:    0.10,
		Clock:          realClock{},
	}
}

// GovernorTransport implements http.RoundTripper with proactive token-bucket
// rate limiting and reactive backoff/retry for HTTP 429 and transient 5xx errors.
type GovernorTransport struct {
	base          http.RoundTripper
	limiter       *rate.Limiter
	maxRetries    int
	baseBackoff   time.Duration
	maxBackoff    time.Duration
	jitterRatio   float64
	logger        *slog.Logger
	retryListener RetryListener
	clock         Clock

	mu            sync.RWMutex
	lastRateLimit RateLimitInfo
}

// NewGovernorTransport constructs a new GovernorTransport.
func NewGovernorTransport(cfg GovernorTransportConfig) *GovernorTransport {
	if cfg.BaseTransport == nil {
		cfg.BaseTransport = http.DefaultTransport
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 500 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.JitterRatio <= 0 {
		cfg.JitterRatio = 0.10
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}

	var limiter *rate.Limiter
	if cfg.RateLimitRPS > 0 {
		burst := cfg.RateLimitBurst
		if burst <= 0 {
			burst = int(cfg.RateLimitRPS)
			if burst <= 0 {
				burst = 1
			}
		}
		limiter = rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), burst)
	}

	return &GovernorTransport{
		base:          cfg.BaseTransport,
		limiter:       limiter,
		maxRetries:    cfg.MaxRetries,
		baseBackoff:   cfg.BaseBackoff,
		maxBackoff:    cfg.MaxBackoff,
		jitterRatio:   cfg.JitterRatio,
		logger:        cfg.Logger,
		retryListener: cfg.RetryListener,
		clock:         cfg.Clock,
	}
}

// RoundTrip executes the HTTP request with proactive rate limiting and reactive retries.
func (t *GovernorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Preserve request body for potential retries
	if req.Body != nil && req.GetBody == nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to buffer request body for retry support: %w", err)
		}
		_ = req.Body.Close()
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
		req.Body, _ = req.GetBody()
	}

	var lastResp *http.Response
	var lastErr error

	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		// 1. Proactive Rate Limiting
		if t.limiter != nil {
			if err := t.limiter.Wait(req.Context()); err != nil {
				if lastResp != nil && lastResp.Body != nil {
					_ = lastResp.Body.Close()
				}
				return nil, fmt.Errorf("proactive rate limiter wait canceled: %w", err)
			}
		}

		// Rewind body if retrying
		if attempt > 0 && req.GetBody != nil {
			newBody, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("failed to rewind request body for retry attempt %d: %w", attempt, err)
			}
			req.Body = newBody
		}

		// 2. Perform HTTP request
		resp, err := t.base.RoundTrip(req)
		lastResp = resp
		lastErr = err

		// Check if request succeeded or non-retryable
		if err == nil {
			t.updateRateLimitInfo(resp.Header)
			if !t.isRetryableStatusCode(resp.StatusCode) {
				return resp, nil
			}
		} else {
			if !t.isRetryableError(err) {
				return nil, err
			}
		}

		// If maximum attempts reached, stop retrying
		if attempt == t.maxRetries {
			break
		}

		// 3. Calculate retry delay
		delay := t.calculateDelay(attempt, resp, err)

		if t.logger != nil {
			statusCode := 0
			if resp != nil {
				statusCode = resp.StatusCode
			}
			t.logger.WarnContext(req.Context(), "HTTP request failed, retrying after delay",
				slog.Int("attempt", attempt+1),
				slog.Int("max_retries", t.maxRetries),
				slog.Int("status_code", statusCode),
				slog.Duration("delay", delay),
				slog.String("url", req.URL.String()),
				slog.Any("error", err),
			)
		}

		if t.retryListener != nil {
			t.retryListener(attempt+1, req, resp, err, delay)
		}

		// Drain and close response body prior to waiting
		if resp != nil && resp.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
		}

		// 4. Sleep with context awareness
		if sleepErr := t.clock.Sleep(req.Context(), delay); sleepErr != nil {
			return nil, fmt.Errorf("context canceled during retry backoff: %w", sleepErr)
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("request failed after %d retries: %w", t.maxRetries, lastErr)
	}
	return lastResp, nil
}

// GetLastRateLimitInfo returns the most recently observed rate limit headers.
func (t *GovernorTransport) GetLastRateLimitInfo() RateLimitInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastRateLimit
}

func (t *GovernorTransport) updateRateLimitInfo(h http.Header) {
	info := ParseRateLimitHeaders(h, t.clock.Now())
	if info != nil {
		t.mu.Lock()
		t.lastRateLimit = *info
		t.mu.Unlock()
	}
}

func (t *GovernorTransport) isRetryableStatusCode(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

func (t *GovernorTransport) isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func (t *GovernorTransport) calculateDelay(attempt int, resp *http.Response, err error) time.Duration {
	now := t.clock.Now()

	// Check if server provided Retry-After header
	if resp != nil {
		if delay, ok := ParseRetryAfter(resp.Header, now); ok {
			return t.applyJitter(delay)
		}

		// If 429 and RateLimit-Reset header is present
		if resp.StatusCode == http.StatusTooManyRequests {
			if resetTime, ok := ParseRateLimitReset(resp.Header); ok {
				if resetTime.After(now) {
					delay := resetTime.Sub(now)
					return t.applyJitter(delay)
				}
			}
		}
	}

	// Exponential backoff: T_base * 2^attempt
	multiplier := 1 << attempt
	if multiplier > 1024 {
		multiplier = 1024
	}
	baseDelay := t.baseBackoff * time.Duration(multiplier)
	if baseDelay > t.maxBackoff {
		baseDelay = t.maxBackoff
	}

	return t.applyJitter(baseDelay)
}

func (t *GovernorTransport) applyJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if t.jitterRatio <= 0 {
		return d
	}

	// Jitter: +/- (jitterRatio * d)
	jitterRange := float64(d) * t.jitterRatio
	// Uniform random in [-jitterRange, +jitterRange]
	offset := (rand.Float64()*2.0 - 1.0) * jitterRange
	final := time.Duration(float64(d) + offset)
	if final < 0 {
		final = 0
	}
	if final > t.maxBackoff {
		final = t.maxBackoff
	}
	return final
}

// ParseRateLimitHeaders extracts rate limit information from HTTP response headers.
func ParseRateLimitHeaders(h http.Header, now time.Time) *RateLimitInfo {
	limitStr := h.Get("RateLimit-Limit")
	remStr := h.Get("RateLimit-Remaining")
	if limitStr == "" && remStr == "" {
		return nil
	}

	info := &RateLimitInfo{
		ObservedAt: now,
	}

	if limit, err := strconv.Atoi(limitStr); err == nil {
		info.Limit = limit
	}
	if rem, err := strconv.Atoi(remStr); err == nil {
		info.Remaining = rem
	}

	if resetTime, ok := ParseRateLimitReset(h); ok {
		info.Reset = resetTime
	}

	return info
}

// ParseRetryAfter parses the Retry-After header as either seconds or HTTP-Date.
func ParseRetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	val := h.Get("Retry-After")
	if val == "" {
		return 0, false
	}

	// Try integer seconds first
	if seconds, err := strconv.ParseInt(val, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}

	// Try HTTP-Date formats (RFC 1123, RFC 850, ANSIC)
	if t, err := http.ParseTime(val); err == nil {
		if t.After(now) {
			return t.Sub(now), true
		}
		return 0, true
	}

	return 0, false
}

// ParseRateLimitReset parses RateLimit-Reset and RateLimit-ResetTime headers.
func ParseRateLimitReset(h http.Header) (time.Time, bool) {
	resetStr := h.Get("RateLimit-Reset")
	if resetStr != "" {
		if ts, err := strconv.ParseInt(resetStr, 10, 64); err == nil && ts > 0 {
			// Check if timestamp is in milliseconds (> 10^11)
			if ts > 100000000000 {
				return time.UnixMilli(ts), true
			}
			return time.Unix(ts, 0), true
		}
	}

	resetTimeStr := h.Get("RateLimit-ResetTime")
	if resetTimeStr != "" {
		if t, err := http.ParseTime(resetTimeStr); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}
