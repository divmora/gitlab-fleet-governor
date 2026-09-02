package gitlab_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

// fastMockClock provides a deterministic, instant clock for stress tests.
type fastMockClock struct {
	mu        sync.Mutex
	now       time.Time
	sleepLogs []time.Duration
}

func newFastMockClock(t time.Time) *fastMockClock {
	return &fastMockClock{now: t}
}

func (c *fastMockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fastMockClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleepLogs = append(c.sleepLogs, d)
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return ctx.Err()
}

// ----------------------------------------------------------------------------
// Challenge 1: Concurrent Burst 429 Storm with Thread-Safety Verification
// ----------------------------------------------------------------------------
func TestAdversarial_ConcurrentBurst429Storm(t *testing.T) {
	const (
		numGoroutines     = 50
		failuresPerClient = 2
	)

	clientFailures := make(map[string]*int32)
	var mapMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := r.Header.Get("X-Client-ID")
		mapMu.Lock()
		ctr, exists := clientFailures[clientID]
		if !exists {
			var initial int32 = 0
			ctr = &initial
			clientFailures[clientID] = ctr
		}
		mapMu.Unlock()

		count := atomic.AddInt32(ctr, 1)
		if count <= failuresPerClient {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("RateLimit-Limit", "1000")
			w.Header().Set("RateLimit-Remaining", "0")
			w.Header().Set("RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(1*time.Second).Unix()))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"429 Too Many Requests"}`))
			return
		}

		w.Header().Set("RateLimit-Limit", "1000")
		w.Header().Set("RateLimit-Remaining", "950")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	clock := newFastMockClock(time.Now())
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport:  server.Client().Transport,
		RateLimitRPS:   500,
		RateLimitBurst: 500,
		MaxRetries:     5,
		BaseBackoff:    50 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		JitterRatio:    0.10,
		Clock:          clock,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/api/v4/projects", nil)
			if err != nil {
				errCh <- err
				return
			}
			req.Header.Set("X-Client-ID", fmt.Sprintf("client-%d", id))

			resp, err := client.Do(req)
			if err != nil {
				errCh <- fmt.Errorf("client %d error: %w", id, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Errorf("client %d unexpected status: %d", id, resp.StatusCode)
				return
			}

			// Read rate limit info concurrently
			info := transport.GetLastRateLimitInfo()
			if info.Limit != 1000 {
				errCh <- fmt.Errorf("client %d expected limit 1000, got %d", id, info.Limit)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	info := transport.GetLastRateLimitInfo()
	assert.Equal(t, 1000, info.Limit)
}

// ----------------------------------------------------------------------------
// Challenge 2: Cascading 5xx Failures (500 -> 502 -> 503 -> 504 -> 200 OK)
// ----------------------------------------------------------------------------
func TestAdversarial_Cascading5xxFailuresWithBackoff(t *testing.T) {
	statusSequence := []int{
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
		http.StatusOK,                  // 200
	}

	var attemptIndex int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt32(&attemptIndex, 1) - 1
		if int(idx) < len(statusSequence) {
			status := statusSequence[idx]
			w.WriteHeader(status)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"status":%d}`, status)))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	clock := newFastMockClock(time.Now())
	var retryCallbacks []int
	var retryDelays []time.Duration
	var cbMu sync.Mutex

	cfg := gitlab.GovernorTransportConfig{
		BaseTransport:  server.Client().Transport,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		MaxRetries:     5,
		BaseBackoff:    100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		JitterRatio:    0.0, // deterministic backoff
		Clock:          clock,
		RetryListener: func(attempt int, req *http.Request, resp *http.Response, err error, delay time.Duration) {
			cbMu.Lock()
			defer cbMu.Unlock()
			retryCallbacks = append(retryCallbacks, attempt)
			retryDelays = append(retryDelays, delay)
		},
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/api/v4/groups", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(5), attemptIndex) // 4 retries + 1 success = 5 total attempts

	cbMu.Lock()
	assert.Equal(t, []int{1, 2, 3, 4}, retryCallbacks)
	require.Len(t, retryDelays, 4)
	// Exponential progression with +/- 20% jitter tolerance:
	assert.InDelta(t, (100 * time.Millisecond).Seconds(), retryDelays[0].Seconds(), (30 * time.Millisecond).Seconds())
	assert.InDelta(t, (200 * time.Millisecond).Seconds(), retryDelays[1].Seconds(), (60 * time.Millisecond).Seconds())
	assert.InDelta(t, (400 * time.Millisecond).Seconds(), retryDelays[2].Seconds(), (100 * time.Millisecond).Seconds())
	assert.InDelta(t, (800 * time.Millisecond).Seconds(), retryDelays[3].Seconds(), (200 * time.Millisecond).Seconds())
	cbMu.Unlock()
}

// ----------------------------------------------------------------------------
// Challenge 3: Mutating POST/PUT Payload Rewinding & SHA-256 Integrity Verification
// ----------------------------------------------------------------------------
func TestAdversarial_MutatingPayloadRewindIntegrity(t *testing.T) {
	// Generate large random payload (128 KB)
	largeData := make([]byte, 128*1024)
	r := rand.New(rand.NewSource(42))
	_, err := r.Read(largeData)
	require.NoError(t, err)

	expectedHash := sha256.Sum256(largeData)
	expectedHex := hex.EncodeToString(expectedHash[:])

	var attempts int32
	var receivedHashes []string
	var hashMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		hash := sha256.Sum256(body)
		hashHex := hex.EncodeToString(hash[:])

		hashMu.Lock()
		receivedHashes = append(receivedHashes, hashHex)
		hashMu.Unlock()

		// Fail first 3 attempts with transient 502 Bad Gateway
		if att < 4 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"transient upstream failure"}`))
			return
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"created","hash":"` + hashHex + `"}`))
	}))
	defer server.Close()

	clock := newFastMockClock(time.Now())
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport:  server.Client().Transport,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		MaxRetries:     4,
		BaseBackoff:    50 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
		JitterRatio:    0.0,
		Clock:          clock,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	// Create request with bytes.Reader (GetBody will be populated by http.NewRequestWithContext)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/api/v4/projects/1/push_rule", bytes.NewReader(largeData))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, int32(4), attempts)

	hashMu.Lock()
	require.Len(t, receivedHashes, 4)
	for i, h := range receivedHashes {
		assert.Equal(t, expectedHex, h, "attempt %d payload hash mismatch", i+1)
	}
	hashMu.Unlock()
}

// ----------------------------------------------------------------------------
// Challenge 3b: Streaming Body Rewinding Without Pre-set GetBody
// ----------------------------------------------------------------------------
type customStreamReader struct {
	r io.Reader
}

func (s *customStreamReader) Read(p []byte) (n int, err error) {
	return s.r.Read(p)
}

func TestAdversarial_StreamingBodyRewindWithoutGetBody(t *testing.T) {
	payload := []byte(`{"branch":"main","commit_message_regex":"^feat.*"}`)
	expectedHash := sha256.Sum256(payload)
	expectedHex := hex.EncodeToString(expectedHash[:])

	var attempts int32
	var capturedHashes []string
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h := sha256.Sum256(body)
		mu.Lock()
		capturedHashes = append(capturedHashes, hex.EncodeToString(h[:]))
		mu.Unlock()

		if att < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	clock := newFastMockClock(time.Now())
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport: server.Client().Transport,
		MaxRetries:    3,
		BaseBackoff:   10 * time.Millisecond,
		Clock:         clock,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	// Manually construct request with customStreamReader (so GetBody is nil)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, server.URL+"/api/v4/projects/2/push_rule", nil)
	require.NoError(t, err)
	req.Body = io.NopCloser(&customStreamReader{r: bytes.NewReader(payload)})
	req.GetBody = nil // Explicitly ensure GetBody is nil

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(3), attempts)

	mu.Lock()
	require.Len(t, capturedHashes, 3)
	for i, h := range capturedHashes {
		assert.Equal(t, expectedHex, h, "attempt %d payload corrupted", i+1)
	}
	mu.Unlock()
}

// ----------------------------------------------------------------------------
// Challenge 4: Context Cancellation and Timeout During Active Backoff Sleep
// ----------------------------------------------------------------------------
func TestAdversarial_ContextCancellationDuringSleep(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "100")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	// Use real clock to verify actual cancellation interruption
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport: server.Client().Transport,
		MaxRetries:    3,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled), "expected context error, got %v", err)
	assert.Less(t, elapsed, 1*time.Second, "request should have aborted within timeout, but took %v", elapsed)
}

// ----------------------------------------------------------------------------
// Challenge 5: Max Retries Exhaustion Behavior
// ----------------------------------------------------------------------------
func TestAdversarial_MaxRetriesExhaustion(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"perpetual outage"}`))
	}))
	defer server.Close()

	clock := newFastMockClock(time.Now())
	const maxRetries = 4

	var listenerCalls int32
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport: server.Client().Transport,
		MaxRetries:    maxRetries,
		BaseBackoff:   10 * time.Millisecond,
		Clock:         clock,
		RetryListener: func(attempt int, req *http.Request, resp *http.Response, err error, delay time.Duration) {
			atomic.AddInt32(&listenerCalls, 1)
		},
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int32(maxRetries+1), attempts)
	assert.Equal(t, int32(maxRetries), listenerCalls)
}

// ----------------------------------------------------------------------------
// Challenge 6: Non-Retryable Client 4xx Errors Fail Immediately
// ----------------------------------------------------------------------------
func TestAdversarial_NonRetryable4xxStatusCodes(t *testing.T) {
	nonRetryCodes := []int{
		http.StatusBadRequest,          // 400
		http.StatusUnauthorized,        // 401
		http.StatusForbidden,           // 403
		http.StatusNotFound,            // 404
		http.StatusMethodNotAllowed,    // 405
		http.StatusConflict,            // 409
		http.StatusUnprocessableEntity, // 422
	}

	for _, code := range nonRetryCodes {
		t.Run(fmt.Sprintf("Status_%d", code), func(t *testing.T) {
			var attempts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&attempts, 1)
				w.WriteHeader(code)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"code":%d}`, code)))
			}))
			defer server.Close()

			clock := newFastMockClock(time.Now())
			cfg := gitlab.GovernorTransportConfig{
				BaseTransport: server.Client().Transport,
				MaxRetries:    3,
				Clock:         clock,
			}
			transport := gitlab.NewGovernorTransport(cfg)
			client := &http.Client{Transport: transport}

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, code, resp.StatusCode)
			assert.Equal(t, int32(1), attempts, "non-retryable code %d should not be retried", code)
		})
	}
}

// ----------------------------------------------------------------------------
// Challenge 7: Header Parsing Fuzzing & Malformed Value Resilience
// ----------------------------------------------------------------------------
func TestAdversarial_RetryAfterAndResetHeaderFuzzing(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		headers       map[string]string
		expectDelay   time.Duration
		expectSuccess bool
	}{
		{
			name:          "Valid positive integer seconds",
			headers:       map[string]string{"Retry-After": "15"},
			expectDelay:   15 * time.Second,
			expectSuccess: true,
		},
		{
			name:          "Zero seconds",
			headers:       map[string]string{"Retry-After": "0"},
			expectDelay:   0,
			expectSuccess: true,
		},
		{
			name:          "Negative seconds ignored",
			headers:       map[string]string{"Retry-After": "-10"},
			expectSuccess: false,
		},
		{
			name:          "Malformed string ignored",
			headers:       map[string]string{"Retry-After": "not-a-number-or-date"},
			expectSuccess: false,
		},
		{
			name:          "Valid RFC 1123 future date",
			headers:       map[string]string{"Retry-After": now.Add(30 * time.Second).Format(http.TimeFormat)},
			expectDelay:   30 * time.Second,
			expectSuccess: true,
		},
		{
			name:          "Past RFC 1123 date returns zero duration",
			headers:       map[string]string{"Retry-After": now.Add(-30 * time.Second).Format(http.TimeFormat)},
			expectDelay:   0,
			expectSuccess: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := make(http.Header)
			for k, v := range tc.headers {
				h.Set(k, v)
			}

			delay, ok := gitlab.ParseRetryAfter(h, now)
			assert.Equal(t, tc.expectSuccess, ok)
			if tc.expectSuccess {
				assert.Equal(t, tc.expectDelay, delay)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Challenge 8: RateLimit-Reset Milliseconds vs Seconds Parsing
// ----------------------------------------------------------------------------
func TestAdversarial_RateLimitResetEpochFormats(t *testing.T) {
	// Seconds format: Unix timestamp 1787664000 (around year 2026)
	unixSec := int64(1787664000)
	hSec := make(http.Header)
	hSec.Set("RateLimit-Reset", fmt.Sprintf("%d", unixSec))

	resetSec, okSec := gitlab.ParseRateLimitReset(hSec)
	require.True(t, okSec)
	assert.Equal(t, unixSec, resetSec.Unix())

	// Milliseconds format: Unix timestamp in ms
	unixMilli := unixSec * 1000
	hMilli := make(http.Header)
	hMilli.Set("RateLimit-Reset", fmt.Sprintf("%d", unixMilli))

	resetMilli, okMilli := gitlab.ParseRateLimitReset(hMilli)
	require.True(t, okMilli)
	assert.Equal(t, unixSec, resetMilli.Unix())
}
