package gitlab_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
)

type mockClock struct {
	now       time.Time
	sleepLogs []time.Duration
}

func (m *mockClock) Now() time.Time {
	return m.now
}

func (m *mockClock) Sleep(ctx context.Context, d time.Duration) error {
	m.sleepLogs = append(m.sleepLogs, d)
	m.now = m.now.Add(d)
	return ctx.Err()
}

func TestGovernorTransport_429RetryWithRetryAfter(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att < 3 {
			w.Header().Set("Retry-After", "2")
			w.Header().Set("RateLimit-Limit", "600")
			w.Header().Set("RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"429 Too Many Requests"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer server.Close()

	clock := &mockClock{now: time.Now()}
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport:  server.Client().Transport,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		MaxRetries:     3,
		BaseBackoff:    100 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		JitterRatio:    0.05,
		Clock:          clock,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(3), attempts)
	assert.Len(t, clock.sleepLogs, 2)
	for _, sleepDuration := range clock.sleepLogs {
		assert.InDelta(t, 2*time.Second, sleepDuration, float64(200*time.Millisecond))
	}

	info := transport.GetLastRateLimitInfo()
	assert.Equal(t, 600, info.Limit)
}

func TestGovernorTransport_500ExponentialBackoff(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"500 Internal Server Error"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	clock := &mockClock{now: time.Now()}
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport:  server.Client().Transport,
		RateLimitRPS:   100,
		RateLimitBurst: 100,
		MaxRetries:     4,
		BaseBackoff:    500 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		JitterRatio:    0.0,
		Clock:          clock,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(3), attempts)
	require.Len(t, clock.sleepLogs, 2)
	assert.InDelta(t, (500 * time.Millisecond).Seconds(), clock.sleepLogs[0].Seconds(), (100 * time.Millisecond).Seconds())
	assert.InDelta(t, (1000 * time.Millisecond).Seconds(), clock.sleepLogs[1].Seconds(), (200 * time.Millisecond).Seconds())
}

func TestGovernorTransport_RequestBodyRewind(t *testing.T) {
	var attempts int32
	var capturedBodies []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		body, _ := io.ReadAll(r.Body)
		capturedBodies = append(capturedBodies, string(body))

		if att == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"created"}`))
	}))
	defer server.Close()

	clock := &mockClock{now: time.Now()}
	cfg := gitlab.GovernorTransportConfig{
		BaseTransport: server.Client().Transport,
		MaxRetries:    2,
		Clock:         clock,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	reqBody := `{"name":"test-project","visibility":"private"}`
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, bytes.NewBufferString(reqBody))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), attempts)
	require.Len(t, capturedBodies, 2)
	assert.Equal(t, reqBody, capturedBodies[0])
	assert.Equal(t, reqBody, capturedBodies[1])
}

func TestGovernorTransport_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := gitlab.GovernorTransportConfig{
		BaseTransport: server.Client().Transport,
		MaxRetries:    3,
	}
	transport := gitlab.NewGovernorTransport(cfg)
	client := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	_, err = client.Do(req)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

func TestGovernorTransport_NonRetryableStatusCodes(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
	}))
	defer server.Close()

	clock := &mockClock{now: time.Now()}
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

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, int32(1), attempts)
	assert.Empty(t, clock.sleepLogs)
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	futureDate := now.Add(5 * time.Second).Format(http.TimeFormat)

	h := make(http.Header)
	h.Set("Retry-After", futureDate)

	delay, ok := gitlab.ParseRetryAfter(h, now)
	assert.True(t, ok)
	assert.Equal(t, 5*time.Second, delay)
}
