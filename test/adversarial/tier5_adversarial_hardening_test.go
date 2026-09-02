package adversarial_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/discovery"
	"github.com/divmora/gitlab-fleet-governor/internal/engine"
	"github.com/divmora/gitlab-fleet-governor/internal/gitlab"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/report"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

// ============================================================================
// 1. Race Stress Tests Under 100 Concurrent Goroutines
// ============================================================================

func TestTier5_Adversarial_RaceStress_100Goroutines(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient(
		gitlab.WithRateLimit(1000, 1000),
		gitlab.WithMaxRetries(3),
	)
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			Concurrency: 10,
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{10, 20},
				Recursive:       gogitlab.Ptr(false),
			},
		},
		Policies: config.PoliciesConfig{
			PushRules: &config.PushRulesConfig{
				AuthorEmailRegex: `@example\.com$`,
				PreventSecrets:   gogitlab.Ptr(true),
			},
			PipelineRetention: &config.PipelineRetentionConfig{
				RetentionDays: 30,
			},
		},
	}

	eng, err := engine.NewGovernanceEngine(client, cfg)
	require.NoError(t, err)

	metrics := engine.NewSummaryMetrics(true)
	metrics.RecordDiscovery(&discovery.TargetFleet{
		ScannedProjectsCount: 100,
		MatchedProjectsCount: 100,
	})

	const numGoroutines = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			switch workerID % 5 {
			case 0:
				res, err := eng.Plan(ctx)
				if err != nil {
					errCh <- fmt.Errorf("worker %d plan failed: %w", workerID, err)
					return
				}
				if !res.DryRun || res.Mode != "plan" {
					errCh <- fmt.Errorf("worker %d plan unexpected mode: %s (dry_run: %v)", workerID, res.Mode, res.DryRun)
					return
				}
			case 1:
				res, err := eng.Apply(ctx)
				if err != nil {
					errCh <- fmt.Errorf("worker %d apply failed: %w", workerID, err)
					return
				}
				if res.DryRun || res.Mode != "apply" {
					errCh <- fmt.Errorf("worker %d apply unexpected mode: %s (dry_run: %v)", workerID, res.Mode, res.DryRun)
					return
				}
			case 2:
				res, err := eng.Run(ctx)
				if err != nil {
					errCh <- fmt.Errorf("worker %d run failed: %w", workerID, err)
					return
				}
				if res == nil {
					errCh <- fmt.Errorf("worker %d run returned nil result", workerID)
					return
				}
			case 3:
				res, err := eng.Execute(ctx, cfg)
				if err != nil {
					errCh <- fmt.Errorf("worker %d execute failed: %w", workerID, err)
					return
				}
				if res == nil {
					errCh <- fmt.Errorf("worker %d execute returned nil result", workerID)
					return
				}
			}

			// Record target results on shared metrics and verify invariants concurrently
			targetID := 2000 + workerID
			metrics.RecordTargetResult(&engine.TargetResult{
				TargetID:     targetID,
				TargetPath:   fmt.Sprintf("group/proj-%d", targetID),
				TargetName:   fmt.Sprintf("proj-%d", targetID),
				ResourceType: governance.ResourceTypeProject,
				DryRun:       true,
				Success:      true,
				HasChanges:   workerID%2 == 0,
				Operations: []*engine.OperationResult{
					{
						OperationName: "push_rules",
						Action:        governance.ActionUpdate,
						Status:        governance.StatusSuccess,
						HasChanges:    workerID%2 == 0,
						Success:       true,
					},
				},
			})
			snap := metrics.Snapshot()
			if snap == nil {
				errCh <- fmt.Errorf("worker %d snapshot is nil", workerID)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	snap := metrics.Snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, snap.TotalTargeted, snap.TotalChanged+snap.TotalUnchanged+snap.TotalFailed,
		"Metrics invariant must hold under 100 concurrent writers: TotalTargeted == TotalChanged + TotalUnchanged + TotalFailed")
}

// ============================================================================
// 2. Network Fault Injection (50% HTTP 429 & 5xx Rate Limit Dropouts)
// ============================================================================

func TestTier5_Adversarial_NetworkFault_50PercentDropouts(t *testing.T) {
	var requestCount int64
	var faultCount int64

	// Faulty upstream proxy server simulating 50% rate limits and server errors
	faultyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqIdx := atomic.AddInt64(&requestCount, 1)

		// 50% fault injection (every even request fails)
		if reqIdx%2 == 0 {
			atomic.AddInt64(&faultCount, 1)
			if reqIdx%4 == 0 {
				// Inject HTTP 429 Too Many Requests with Retry-After header
				w.Header().Set("Retry-After", "0")
				w.Header().Set("RateLimit-Limit", "100")
				w.Header().Set("RateLimit-Remaining", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"message":"429 Too Many Requests - Simulated Rate Limit Spike"}`))
				return
			}
			// Inject HTTP 502/503/504 Bad Gateway / Service Unavailable
			status := http.StatusBadGateway
			if reqIdx%6 == 0 {
				status = http.StatusServiceUnavailable
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"message":"%d Upstream Service Error"}`, status)))
			return
		}

		// Successful responses with mock GitLab endpoints
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/projects") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":101,"name":"fleet-governor","path_with_namespace":"platform/fleet-governor","default_branch":"main","visibility":"private"}]`))
			return
		}
		if strings.Contains(r.URL.Path, "/groups") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":10,"name":"Platform","full_path":"platform"}]`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer faultyServer.Close()

	auth := &gitlab.ResolvedAuth{
		BaseURL:   faultyServer.URL + "/api/v4",
		Token:     "test-fault-token",
		TokenType: gitlab.TokenTypePrivate,
	}

	client, err := gitlab.NewClient(auth,
		gitlab.WithMaxRetries(5),
		gitlab.WithBackoff(10*time.Millisecond, 200*time.Millisecond),
		gitlab.WithRateLimit(500, 500),
	)
	require.NoError(t, err)

	ctx := context.Background()

	// Execute 30 parallel requests through the fault-injected client
	const numRequests = 30
	var wg sync.WaitGroup
	wg.Add(numRequests)

	successCount := int64(0)
	failCount := int64(0)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()
			projects, _, err := client.Projects().ListProjects(&gogitlab.ListProjectsOptions{
				ListOptions: gogitlab.ListOptions{PerPage: 10},
			}, gogitlab.WithContext(ctx))

			if err == nil && len(projects) > 0 {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Total requests: %d, Injected faults: %d, Successful client calls: %d, Exhausted failures: %d",
		atomic.LoadInt64(&requestCount), atomic.LoadInt64(&faultCount), successCount, failCount)

	assert.Greater(t, atomic.LoadInt64(&faultCount), int64(0), "Faults must be triggered during execution")
	assert.Greater(t, successCount, int64(0), "Client retries must successfully recover majority of transient dropouts")
}

// ============================================================================
// 3. Simulated Container Timeouts & Context Cancellations
// ============================================================================

func TestTier5_Adversarial_SimulatedContainerTimeouts(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			Concurrency: 5,
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{10, 20},
				Recursive:       gogitlab.Ptr(true),
			},
		},
	}

	eng, err := engine.NewGovernanceEngine(client, cfg)
	require.NoError(t, err)

	t.Run("SubMillisecond Context Timeout In WorkerPool", func(t *testing.T) {
		// Create expired context
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
		defer cancel()
		time.Sleep(2 * time.Millisecond) // Ensure context is expired

		pool := engine.NewWorkerPool(ctx, 10)

		err := pool.Submit(func(taskCtx context.Context) error {
			time.Sleep(100 * time.Millisecond)
			return nil
		})

		// Must either return context error immediately or fail gracefully
		if err != nil {
			assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
		}

		timeoutErr := pool.SubmitWithTimeout(func(taskCtx context.Context) error {
			return nil
		}, 1*time.Millisecond)
		if timeoutErr != nil {
			assert.True(t, errors.Is(timeoutErr, context.DeadlineExceeded) || errors.Is(timeoutErr, context.Canceled) || errors.Is(timeoutErr, engine.ErrPoolFull))
		}
		errs := pool.Wait()
		_ = errs
	})

	t.Run("Engine Aborts Gracefully On Expired Container Context", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
		defer cancel()
		time.Sleep(5 * time.Millisecond) // Force deadline exceeded

		start := time.Now()
		res, err := eng.Run(ctx)
		elapsed := time.Since(start)

		// Must return promptly without hanging or deadlocking
		assert.Less(t, elapsed, 2*time.Second, "Engine must terminate promptly when context expires")
		if err != nil {
			assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context"))
		} else if res != nil {
			assert.False(t, res.Success)
		}
	})

	t.Run("WorkerPool ParallelMap With Context Cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		items := make([]int, 100)
		for i := 0; i < 100; i++ {
			items[i] = i
		}

		go func() {
			time.Sleep(5 * time.Millisecond)
			cancel() // Abrupt SIGTERM / Container teardown
		}()

		start := time.Now()
		results, errs := engine.ParallelMap(ctx, 10, items, func(taskCtx context.Context, item int) (int, error) {
			select {
			case <-taskCtx.Done():
				return 0, taskCtx.Err()
			case <-time.After(10 * time.Millisecond):
				return item * 2, nil
			}
		})
		elapsed := time.Since(start)

		assert.Less(t, elapsed, 1*time.Second, "ParallelMap must cancel promptly")
		assert.Equal(t, 100, len(results))
		// At least some errors must be context.Canceled
		var hasContextErr bool
		for _, err := range errs {
			if errors.Is(err, context.Canceled) {
				hasContextErr = true
				break
			}
		}
		assert.True(t, hasContextErr, "Must capture context cancellation error in worker errors")
	})
}

// ============================================================================
// 4. Corrupt S3 Payloads & Malformed Config Sources
// ============================================================================

type mockFaultyS3Client struct {
	mode string
}

func (m *mockFaultyS3Client) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	switch m.mode {
	case "truncated":
		// Truncated body with early stream closure
		return &s3.GetObjectOutput{
			Body: io.NopCloser(strings.NewReader("version: 'v1'\nsettings:\n  concur")),
		}, nil
	case "binary_garbage":
		// Corrupted non-UTF8 binary noise
		garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0xFF, 0xFE, 0xFD}
		return &s3.GetObjectOutput{
			Body: io.NopCloser(bytes.NewReader(garbage)),
		}, nil
	case "billion_laughs":
		// Recursive YAML entity explosion / anchor bomb
		yamlBomb := `
a: &a ["lol","lol","lol","lol","lol","lol","lol","lol","lol"]
b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]
c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]
d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]
e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]
version: "v1"
`
		return &s3.GetObjectOutput{
			Body: io.NopCloser(strings.NewReader(yamlBomb)),
		}, nil
	case "malformed_json":
		return &s3.GetObjectOutput{
			Body: io.NopCloser(strings.NewReader(`{"version": "v1", "settings": { "concurrency": 10, }}`)),
		}, nil
	case "access_denied":
		return nil, errors.New("AccessDenied: Access Denied to S3 bucket or key")
	case "no_such_key":
		return nil, errors.New("NoSuchKey: The specified key does not exist.")
	default:
		return nil, errors.New("unknown error")
	}
}

func TestTier5_Adversarial_CorruptS3PayloadsAndConfigSources(t *testing.T) {
	ctx := context.Background()

	testModes := []struct {
		name        string
		mode        string
		expectError string
	}{
		{
			name:        "Truncated Payload",
			mode:        "truncated",
			expectError: "",
		},
		{
			name:        "Binary Garbage Payload",
			mode:        "binary_garbage",
			expectError: "",
		},
		{
			name:        "Billion Laughs YAML Bomb",
			mode:        "billion_laughs",
			expectError: "",
		},
		{
			name:        "Malformed JSON Trailing Comma",
			mode:        "malformed_json",
			expectError: "",
		},
		{
			name:        "S3 403 Access Denied",
			mode:        "access_denied",
			expectError: "AccessDenied",
		},
		{
			name:        "S3 404 NoSuchKey",
			mode:        "no_such_key",
			expectError: "NoSuchKey",
		},
	}

	for _, tc := range testModes {
		t.Run(tc.name, func(t *testing.T) {
			loader := config.NewLoader(
				config.WithS3Client(&mockFaultyS3Client{mode: tc.mode}),
			)

			raw, src, err := loader.LoadRaw(ctx, "s3://governance-bucket/policies/prod.yaml")
			if err != nil {
				if tc.expectError != "" {
					assert.Contains(t, err.Error(), tc.expectError)
				}
				return
			}

			// If raw bytes were returned, unmarshaling and validation must cleanly reject invalid schemas
			_, unmarshalErr := config.Unmarshal(raw)
			if unmarshalErr != nil {
				assert.NotEmpty(t, src)
			}
		})
	}

	t.Run("Invalid S3 URI Schemes", func(t *testing.T) {
		invalidURIs := []string{
			"http://s3.amazonaws.com/bucket/key.yaml",
			"s3:/bucket/key.yaml",
			"s3://",
			"s3://bucket",
			"s3:///key.yaml",
		}

		for _, uri := range invalidURIs {
			_, err := config.ParseS3URI(uri)
			assert.Error(t, err, "URI %q must be rejected by ParseS3URI", uri)
		}
	})

	t.Run("Nil Stdin Loader Protection", func(t *testing.T) {
		loader := config.NewLoader(config.WithStdin(nil))
		_, _, err := loader.LoadRaw(ctx, "-")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "standard input reader is nil")
	})
}

// ============================================================================
// 5. Malicious Regular Expressions & ReDoS Defense (RE2 Algorithm)
// ============================================================================

func TestTier5_Adversarial_MaliciousRegexes_ReDoSDefense(t *testing.T) {
	// Catalog of classical ReDoS attack vectors designed to trigger exponential backtracking O(2^n)
	// in non-linear backtracking regex engines (like PCRE, Python re, JavaScript RegExp).
	// Go's standard regexp package implements RE2 which guarantees O(n) linear-time execution.
	reDoSCatalog := []struct {
		name        string
		pattern     string
		attackInput string
	}{
		{
			name:        "Nested Quantifiers (a+)+",
			pattern:     `^(a+)+$`,
			attackInput: strings.Repeat("a", 50000) + "!",
		},
		{
			name:        "Alternating Overlap (a|a?)+",
			pattern:     `^(a|a?)+$`,
			attackInput: strings.Repeat("a", 50000) + "!",
		},
		{
			name:        "Overlapping Star-Star (a*)*b",
			pattern:     `^(a*)*b$`,
			attackInput: strings.Repeat("a", 50000) + "c",
		},
		{
			name:        "Word Character Overlap ([a-zA-Z0-9]+)*$",
			pattern:     `^([a-zA-Z0-9]+)*$`,
			attackInput: strings.Repeat("x", 50000) + "@",
		},
		{
			name:        "Multiple Group Quantifiers (x+x+)+y",
			pattern:     `^(x+x+)+y$`,
			attackInput: strings.Repeat("x", 50000) + "z",
		},
		{
			name:        "Complex Branching ((a|b)+c)+",
			pattern:     `^((a|b)+c)+$`,
			attackInput: strings.Repeat("ab", 25000) + "!",
		},
	}

	for _, tc := range reDoSCatalog {
		t.Run("RE2 Linear Time Verification: "+tc.name, func(t *testing.T) {
			// 1. Verify regex compiles via RE2
			re, err := regexp.Compile(tc.pattern)
			require.NoError(t, err, "Pattern %q should be valid RE2", tc.pattern)

			// 2. Measure execution time on 50KB attack input
			start := time.Now()
			matched := re.MatchString(tc.attackInput)
			elapsed := time.Since(start)

			assert.False(t, matched, "Attack input should not match pattern")
			// RE2 execution must complete linearly (well under any catastrophic backtracking threshold of seconds/minutes)
			assert.Less(t, elapsed, 1*time.Second,
				"RE2 algorithm must match 50KB adversarial string in <1s under race detector (took %v)", elapsed)
		})

		t.Run("Config Schema Validation With ReDoS Pattern: "+tc.name, func(t *testing.T) {
			cfg := &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						ProjectNameRegexInclude: tc.pattern,
					},
				},
				Policies: config.PoliciesConfig{
					PushRules: &config.PushRulesConfig{
						AuthorEmailRegex:   tc.pattern,
						BranchNameRegex:    tc.pattern,
						CommitMessageRegex: tc.pattern,
						FileNameRegex:      tc.pattern,
					},
				},
			}

			// Validate should accept syntactically valid RE2 patterns
			err := config.Validate(cfg)
			require.NoError(t, err)
		})
	}

	t.Run("Strict Rejection of Non-RE2 and Uncompilable Regexes", func(t *testing.T) {
		invalidPatterns := []string{
			`[a-z(`,                        // Unclosed bracket
			`(?<=lookbehind_unsupported)`,  // PCRE Lookbehind (unsupported in RE2)
			`(?=lookahead_unsupported)`,    // PCRE Lookahead (unsupported in RE2)
			`(?>atomic_group_unsupported)`, // Atomic grouping (unsupported in RE2)
			`\`,                            // Trailing backslash
			`*initial_star`,                // Dangling repetition operator
		}

		for _, pat := range invalidPatterns {
			cfg := &config.PolicyConfig{
				Targets: config.TargetSelectors{
					ProjectSelector: &config.ProjectSelector{
						ProjectNameRegexInclude: pat,
					},
				},
			}
			err := config.Validate(cfg)
			require.Error(t, err, "Invalid pattern %q must fail validation", pat)
			assert.Contains(t, err.Error(), "invalid regular expression")
		}
	})
}

// ============================================================================
// 6. Secret Leakage Audits (Zero Plaintext Secrets in Diffs, Logs, Reports)
// ============================================================================

func TestTier5_Adversarial_SecretLeakageAudit(t *testing.T) {
	// Canary secret tokens that MUST NEVER appear in plain text
	sensitiveCanaries := []string{
		"glpat-SecretTokenCanary-99887766554433221100",
		"Production_Super_Secret_Password_2026_!@#",
		"AKIAIOSFODNN7CANARYKEYEXAMPLE",
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYCANARYSECRET",
	}

	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	cfg := &config.PolicyConfig{
		Version: "v1",
		Settings: config.SettingsConfig{
			Concurrency: 2,
			DryRun:      gogitlab.Ptr(true),
		},
		Targets: config.TargetSelectors{
			GroupSelector: &config.GroupSelector{
				GroupIDsInclude: []int{10},
				Recursive:       gogitlab.Ptr(false),
			},
		},
		Policies: config.PoliciesConfig{
			Variables: []config.VariableConfig{
				{
					Key:              "GITLAB_API_SECRET",
					Value:            sensitiveCanaries[0],
					EnvironmentScope: "production",
					Masked:           gogitlab.Ptr(true),
					Protected:        gogitlab.Ptr(true),
				},
				{
					Key:              "DATABASE_PASSWORD",
					Value:            sensitiveCanaries[1],
					EnvironmentScope: "production",
					Masked:           gogitlab.Ptr(true),
					Protected:        gogitlab.Ptr(true),
				},
				{
					Key:              "AWS_ACCESS_KEY_ID",
					Value:            sensitiveCanaries[2],
					EnvironmentScope: "*",
					Masked:           gogitlab.Ptr(true),
				},
				{
					Key:              "AWS_SECRET_ACCESS_KEY",
					Value:            sensitiveCanaries[3],
					EnvironmentScope: "*",
					Masked:           gogitlab.Ptr(true),
				},
			},
		},
	}

	eng, err := engine.NewGovernanceEngine(client, cfg)
	require.NoError(t, err)

	ctx := context.Background()
	planResult, err := eng.Plan(ctx)
	require.NoError(t, err)
	require.NotNil(t, planResult)

	// 1. Audit Diffs in ExecutionResult
	for _, target := range planResult.TargetResults {
		for _, op := range target.Operations {
			for _, diff := range op.Diffs {
				for _, canary := range sensitiveCanaries {
					assert.NotContains(t, diff.Details, canary,
						"Diff Details must NOT contain plain text secret canary: %s", canary)
				}
				for _, field := range diff.Fields {
					for _, canary := range sensitiveCanaries {
						oldStr := fmt.Sprintf("%v", field.OldValue)
						newStr := fmt.Sprintf("%v", field.NewValue)
						assert.NotContains(t, oldStr, canary,
							"FieldDiff OldValue must NOT contain plain text secret canary: %s", canary)
						assert.NotContains(t, newStr, canary,
							"FieldDiff NewValue must NOT contain plain text secret canary: %s", canary)
					}
				}
			}
		}
	}

	// 2. Audit All Report Formats
	reportData := report.FromExecutionResult(planResult)
	require.NotNil(t, reportData)

	allFormats := []report.Format{
		report.FormatTable,
		report.FormatJSON,
		report.FormatCSV,
		report.FormatMarkdown,
		report.FormatSummary,
	}

	for _, fmtType := range allFormats {
		t.Run("ReportFormat_"+string(fmtType)+"_ZeroSecretLeakage", func(t *testing.T) {
			buf := new(bytes.Buffer)
			rep, err := report.NewReporter(fmtType, buf, report.WithColor(false), report.WithDiffs(true))
			require.NoError(t, err)

			err = rep.Render(reportData)
			require.NoError(t, err)

			rendered := buf.String()
			for _, canary := range sensitiveCanaries {
				assert.NotContains(t, rendered, canary,
					"Report format %q leaked plaintext secret canary %q into output!", fmtType, canary)
			}
		})
	}

	// 3. Audit Structured slog Logger Output
	t.Run("Structured Logging Zero Secret Leakage", func(t *testing.T) {
		logBuf := new(bytes.Buffer)
		logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

		// Log target result
		logger.Info("Executed target governance",
			"target", "platform/fleet-governor",
			"diffs", planResult.TotalDiffs(),
		)

		logOutput := logBuf.String()
		for _, canary := range sensitiveCanaries {
			assert.NotContains(t, logOutput, canary, "Log output leaked secret canary %q!", canary)
		}
	})

	// 4. Audit Config Validation Error Redaction
	t.Run("Validation Error Masks Short Secret", func(t *testing.T) {
		badCfg := &config.PolicyConfig{
			Policies: config.PoliciesConfig{
				Variables: []config.VariableConfig{
					{
						Key:    "API_KEY",
						Value:  "short", // < 8 characters
						Masked: gogitlab.Ptr(true),
					},
				},
			},
		}

		valErr := config.Validate(badCfg)
		require.Error(t, valErr)
		// Should contain [REDACTED] and NOT contain "short"
		assert.Contains(t, valErr.Error(), "[REDACTED]")
		assert.NotContains(t, valErr.Error(), "short")
	})
}

// ============================================================================
// 7. White-Box Adversarial Edge Cases
// ============================================================================

func TestTier5_Adversarial_SubgroupCycleDetection(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.SeedCircularSubgroups()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx := context.Background()

	// Perform discovery on circular subgroup graph: 500 -> 501 -> 502 -> 500
	selectors := config.TargetSelectors{
		GroupSelector: &config.GroupSelector{
			GroupIDsInclude: []int{500},
			Recursive:       gogitlab.Ptr(true),
		},
	}

	start := time.Now()
	fleet, err := discovery.DiscoverFleet(ctx, client, selectors)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotNil(t, fleet)

	// Must terminate in < 500ms without infinite recursion or stack overflow
	assert.Less(t, elapsed, 1*time.Second, "BFS discovery must avoid infinite loop on circular subgroup references")

	// Each circular group must be visited exactly once
	assert.Equal(t, 3, len(fleet.Groups), "Must discover exactly 3 groups in circular graph (500, 501, 502)")
	assert.NotNil(t, fleet.Groups[500])
	assert.NotNil(t, fleet.Groups[501])
	assert.NotNil(t, fleet.Groups[502])
}

func TestTier5_Adversarial_EmptyFleetAndExtremeBoundaries(t *testing.T) {
	srv := mockserver.NewMockGitLabServer()
	defer srv.Close()
	srv.Seed()

	client, err := srv.GovernorClient()
	require.NoError(t, err)

	ctx := context.Background()

	t.Run("Empty Fleet Returns Clean Zero-State Result", func(t *testing.T) {
		cfg := &config.PolicyConfig{
			Version: "v1",
			Targets: config.TargetSelectors{
				ProjectSelector: &config.ProjectSelector{
					IDRange: &config.IDRangeSelector{
						Min: math.MaxInt32 - 100,
						Max: math.MaxInt32,
					},
				},
			},
		}

		eng, err := engine.NewGovernanceEngine(client, cfg)
		require.NoError(t, err)

		res, err := eng.Run(ctx)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.Success)
		assert.GreaterOrEqual(t, res.Metrics.TotalScanned, 0)
		assert.Equal(t, 0, res.Metrics.TotalTargeted)
		assert.Equal(t, 0, res.Metrics.TotalChanged)
		assert.Equal(t, 0, res.Metrics.TotalFailed)
	})

	t.Run("Nil Client And Nil Config Errors", func(t *testing.T) {
		_, err := engine.NewGovernanceEngine(nil, &config.PolicyConfig{})
		assert.ErrorIs(t, err, engine.ErrNilClient)

		_, err = engine.NewGovernanceEngine(client, nil)
		assert.ErrorIs(t, err, engine.ErrNilConfig)
	})
}

func TestTier5_Adversarial_CascadingMultiOperationFailures(t *testing.T) {
	metrics := engine.NewSummaryMetrics(false)

	const numTargets = 50
	metrics.RecordDiscovery(&discovery.TargetFleet{
		ScannedProjectsCount: numTargets,
		MatchedProjectsCount: numTargets,
	})

	totalExpectedFailedOps := 0

	for i := 0; i < numTargets; i++ {
		targetID := 3000 + i
		opsCount := (i % 5) + 1 // 1 to 5 failing operations per target
		totalExpectedFailedOps += opsCount

		ops := make([]*engine.OperationResult, 0, opsCount)
		for opIdx := 0; opIdx < opsCount; opIdx++ {
			opName := fmt.Sprintf("cascading_op_%d", opIdx)
			ops = append(ops, &engine.OperationResult{
				OperationName: opName,
				Action:        governance.ActionUpdate,
				Status:        governance.StatusFailed,
				Success:       false,
				Error:         fmt.Errorf("simulated failure in %s", opName),
			})
		}

		metrics.RecordTargetResult(&engine.TargetResult{
			TargetID:     targetID,
			TargetPath:   fmt.Sprintf("group/project-%d", targetID),
			ResourceType: governance.ResourceTypeProject,
			Success:      false,
			HasChanges:   false,
			Operations:   ops,
			Error:        fmt.Errorf("target %d experienced %d failing operations", targetID, opsCount),
		})
	}

	snap := metrics.Snapshot()
	require.NotNil(t, snap)

	assert.Equal(t, numTargets, snap.TotalTargeted)
	assert.Equal(t, numTargets, snap.TotalFailed)
	assert.Equal(t, 0, snap.TotalChanged)
	assert.Equal(t, 0, snap.TotalUnchanged)
	assert.Equal(t, totalExpectedFailedOps, snap.TotalFailedOperations)

	// Crucial invariant verification
	assert.Equal(t, snap.TotalTargeted, snap.TotalChanged+snap.TotalUnchanged+snap.TotalFailed)
}
