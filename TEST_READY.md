# E2E Test Suite Ready

## Test Runner
- Command: `go test -v -count=1 -race -cover ./...`
- Expected: All tests pass with exit code 0 and zero race conditions

## Coverage Summary
| Tier | Description | Test File |
|------|-------------|-----------|
| **Tier 1: Feature Coverage** | Happy-path opaque-box tests covering all CLI commands, multi-source config loaders, discovery selectors, all 10 governance operations, and multi-format report generators. | `test/e2e/tier1_feature_test.go` |
| **Tier 2: Boundary & Corner** | Empty fleet, 1,000+ items keyset pagination, circular subgroup BFS traversal, HTTP 429 burst storm with full jitter backoff, HTTP 500/503 retry recovery, variable masking limits, corrupt/nil config handling, and AWS Lambda payload URL unescaping. | `test/e2e/tier2_boundary_test.go` |
| **Tier 3: Cross-Feature Combinations** | Multi-group BFS + project filtering + 10 governance operations + dry-run diffing in single run; S3 config + Lambda EventBridge + direct parameter overrides; concurrent fleet scan + token-bucket rate limiter with fault injection. | `test/e2e/tier3_combinatorial_test.go` |
| **Tier 4: Real-World Workloads** | Enterprise 50+ project multi-group fleet (17 groups, 65 projects), archived/active filtering, visibility rules, unmanaged initial drift, 4-phase lifecycle (Plan -> Apply -> Idempotency -> Multi-Format Reporting). | `test/e2e/tier4_realworld_workload_test.go` |
| **Tier 5: Adversarial Hardening** | 100-goroutine race stress testing, 50% HTTP 429/5xx fault injection, container timeouts, corrupt S3 streams/Billion Laughs YAML bombs, ReDoS defense with Go RE2 linear time guarantees, and secret leakage audits. | `test/adversarial/tier5_adversarial_hardening_test.go` |

## Feature Checklist
| Feature | Tier 1 | Tier 2 | Tier 3 | Tier 4 | Tier 5 |
|---------|:------:|:------:|:------:|:------:|:------:|
| CLI Commands (run, validate, version, lambda) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Config Engine (file, S3, stdin, envsubst, validation) | ✓ | ✓ | ✓ | ✓ | ✓ |
| GitLab Client (auth, rate limiting, retry, keyset pagination) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Target Selector (group BFS, cycle detection, 9-step filter) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Push Rules Reconciler (404 POST vs 200 PUT) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Protected Branches Reconciler (access levels, PATCH/recreate) | ✓ | ✓ | ✓ | ✓ | ✓ |
| MR Approval Rules Reconciler (inverted booleans, CachingResolver) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Project Settings Reconciler (squash, merge methods, pipelines) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Pipeline Retention Reconciler (days to seconds conversion) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Variables Reconciler (composite keys, masking, secret redaction, pruning) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Runners Reconciler (shared/group settings, runner assertions) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Compliance Frameworks Reconciler (GraphQL & REST assignment) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Webhooks Reconciler (URL normalization, triggers, secret tokens, pruning) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Members Permissions Audit (over-privileged, denied, expiration audit) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Operations Registry (strict dependency ordering 10..100) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Bounded Worker Pool (concurrency capping, panic recovery harness) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Summary Metrics Accumulator (thread-safe, drift tracking, snapshots) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Multi-Format Reporting (Table, JSON, CSV, Markdown, Summary) | ✓ | ✓ | ✓ | ✓ | ✓ |
| Structured Logging (slog text with colors, JSON, trace context) | ✓ | ✓ | ✓ | ✓ | ✓ |
| AWS Lambda Adapter (EventBridge cron, S3 Put, Direct JSON, API Gateway) | ✓ | ✓ | ✓ | ✓ | ✓ |
