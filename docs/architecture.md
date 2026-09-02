# Architectural Design & Principles

GitLab Fleet Governor is constructed following Clean Architecture principles, ensuring clear separation between configuration domain models, API communication, discovery pipelines, reconcilers, execution engine, and reporting adapters.

---

## Package Organization

```
gitlab-fleet-governor/
├── cmd/
│   └── gitlab-fleet-governor/main.go  # Entrypoint with Lambda auto-detection
├── pkg/
│   └── version/                       # Build version & runtime metadata
├── internal/
│   ├── cli/                           # Cobra CLI subcommands & flags
│   ├── config/                        # Schema, unmarshaling, envsubst, loader, validator
│   ├── gitlab/                        # Resilient API client wrapper & rate limiting
│   ├── discovery/                     # BFS group traversal & project filter pipeline
│   ├── governance/                    # 10 governance reconcilers & diff engine
│   ├── engine/                        # Bounded worker pool & execution orchestrator
│   ├── report/                        # ASCII table, JSON, CSV, Markdown reporters
│   ├── logging/                       # Structured slog handlers (colored text & JSON)
│   ├── lambda/                        # AWS Lambda event adapters & handlers
│   └── testutil/                      # Mock GitLab server & test harnesses
```

---

## Core Technical Highlights

### 1. Resilient API Client
- **Token-Bucket Rate Limiter**: Proactively paces outgoing requests using `golang.org/x/time/rate` (configurable RPS and burst capacity) to avoid triggering upstream HTTP 429 rate limits.
- **Reactive Backoff with Full Jitter**: Traps HTTP 429 (respecting `Retry-After` headers) and transient HTTP 5xx errors, retrying with exponential backoff and randomized jitter to prevent thundering herd syndromes.
- **Keyset Streaming Pagination**: Leverages GitLab's high-efficiency `id_after` keyset pagination for fleets with tens of thousands of repositories, streaming items over Go channels to maintain flat memory profiles.

### 2. High-Throughput Bounded Worker Pool
- Uses bounded Go worker goroutines with channel-based task dispatching.
- Each worker encapsulates panic recovery harnesses to prevent fleet-wide aborts when encountering isolated API anomalies.

### 3. Granular Diff Engine ($S_D \ominus S_L$)
- Computes fine-grained attribute differences between desired policy and live state.
- Generates structured diffs (`field`, `old_value`, `new_value`, `action`) powering simulation summaries before any mutating API calls are executed.

### 4. Zero-External-Dependency Mock GitLab Server
- Implements an in-memory, thread-safe HTTP mock server (`internal/testutil/mockserver`) supporting 26+ GitLab REST v4 endpoints for unit, integration, and adversarial testing without live network dependencies.
