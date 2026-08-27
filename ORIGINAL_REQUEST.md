# Original User Request

## 2026-08-25T19:15:09Z

Build GitLab Fleet Governor (gitlab-fleet-governor), a production-grade, declarative policy-as-code and governance automation engine in Golang for managing push rules, protected branches, merge request approval policies, project settings, and native GitLab pipeline retention policies across fleets of GitLab projects and groups.

Working directory: /Users/ngoyal16/divmora/github-repos/gitlab-fleet-governor
Integrity mode: development

## Requirements

### R1. Cobra CLI & Runtime Target Support
- Implement a modern CLI using github.com/spf13/cobra with subcommands: run, validate, lambda, version.
- Support dual execution modes: standard CLI/Docker process vs AWS Lambda function.
- Auto-detect AWS Lambda environment when AWS_LAMBDA_FUNCTION_NAME is present and start Lambda handler automatically without requiring subcommands.
- Support runtime flags: --config / -c, --dry-run, --concurrency, --log-level (debug, info, warn, error), --log-format (text, json).
- Structured logging using Go standard library log/slog supporting colored human-readable text and JSON output.

### R2. Declarative Configuration Engine (JSON & YAML)
- Support interchangeable .yaml, .yml, and .json configuration files with strict schema validation.
- Support environment variable substitution (${VAR_NAME} and ${VAR_NAME:-default}) across all config values.
- Support flexible configuration loading: local file path, standard input (-), environment variables (CONFIG_SOURCE, CONFIG_FILE, CONFIG_CONTENT, CONFIG_YAML, CONFIG_JSON), and AWS S3 URIs (s3://bucket/key).
- Target selectors:
  - group_selector: group_ids_include, group_ids_exclude, group_paths_include, group_paths_exclude, and recursive: true (BFS traversal of subgroups with cycle detection).
  - project_selector: namespaces_include, namespaces_exclude, project_name_regex_include, project_name_regex_exclude, visibility, archived, id_range (min, max).

### R3. GitLab API Client & Resilience
- Wrap github.com/xanzy/go-gitlab client with custom HTTP transport supporting configurable token authentication (PRIVATE_TOKEN, GITLAB_TOKEN, OAuth Bearer) and customizable base URL (CI_API_V4_URL, GITLAB_BASE_URL).
- Built-in rate limiting and exponential backoff retry with jitter for HTTP 429 and HTTP 5xx responses.
- Keyset pagination (id_after) and page-based pagination helpers for high-scale instance queries.

### R4. Complete Governance Operations Suite
- update_push_rule (Project & Group level): Enforce author email regex, branch name regex, commit message regex, file name regex, max file size, commit committer check, member check, prevent secrets, deny delete tag, reject unsigned commits (checking GET -> 404 POST vs PUT).
- update_protected_branches (Project level): Upsert branch protections (allowed_to_push, allowed_to_merge, allowed_to_unprotect, allow_force_push, code_owner_approval_required).
- approval_rules (Project & Group level): Upsert merge request approval settings (allow_author_approval, allow_committer_approval, allow_overrides_to_approver_list_per_merge_request, retain_approvals_on_push) and named approval rules with username-to-ID lookup.
- update_project_settings (Project level): Enforce arbitrary project settings (squash_option, merge_method, only_allow_merge_if_pipeline_succeeds, keep_latest_artifact, etc.).
- pipeline_retention (Project level): High-level native GitLab pipeline automatic deletion setting (retention_days -> ci_delete_pipelines_in_seconds).

### R5. Execution Engine, Concurrency & Summary Reporting
- Worker pool with bounded concurrency channel dispatching project and group operations in parallel.
- Dry-run simulation mode (dry_run: true by default) previewing all actions without performing mutating API calls.
- Summary reporter outputting formatted tables of scanned, matched, applied, skipped, and error counts.

### R6. AWS Lambda Integration
- Handler supporting EventBridge scheduled cron events, S3 Put Object triggers, and direct JSON invocations with config payload or S3 config references.

### R7. Automated Release Pipeline & Community Standards
- GitHub Actions workflows:
  - .github/workflows/ci.yml: Automated golangci-lint and go test -race -cover ./...
  - .github/workflows/docker-debug.yml: Builds and pushes multi-arch images (linux/amd64, linux/arm64) to ghcr.io/divmora/gitlab-fleet-governor/debug on pushes to main.
  - .github/workflows/release-please.yml: Uses googleapis/release-please-action@v4 to create Release PRs, tag releases, invoke GoReleaser for binary assets, and push official images to ghcr.io/divmora/gitlab-fleet-governor.
  - .github/workflows/pages.yml: Deploys documentation site in docs/ to GitHub Pages.
- .goreleaser.yaml: Cross-compiles Linux, macOS, Windows static binaries, AWS Lambda bootstrap zip, and checksums.txt.
- Open-source community files: LICENSE (Apache-2.0), README.md, AGENTS.md, CODE_OF_CONDUCT.md, CONTRIBUTING.md, Makefile, Dockerfile, Dockerfile.lambda, examples/config.sample.yaml, and examples/config.sample.json.
- Modern, fast, responsive documentation site in docs/.

## Acceptance Criteria

### Build & Unit Tests
- [ ] go build ./... compiles cleanly with zero errors.
- [ ] Comprehensive unit test suite with mock GitLab server passes with go test -v -race -cover ./...
- [ ] golangci-lint run ./... (or standard go vet ./...) reports 0 issues.

### CLI & Configuration
- [ ] ./bin/gitlab-fleet-governor validate -c examples/config.sample.yaml validates YAML successfully.
- [ ] ./bin/gitlab-fleet-governor validate -c examples/config.sample.json validates JSON successfully.
- [ ] ./bin/gitlab-fleet-governor run --dry-run executes smoothly and generates a complete summary report.
- [ ] Environment variable substitution (${VAR}) correctly replaces tokens in config files.

### Container & Lambda Compatibility
- [ ] Dockerfile and Dockerfile.lambda build static binaries cleanly.
- [ ] AWS Lambda handler correctly parses EventBridge, S3, and direct JSON payloads.

### Release & Documentation
- [ ] release-please-config.json and .release-please-manifest.json are valid and present.
- [ ] All 4 GitHub Actions workflows (ci.yml, docker-debug.yml, release-please.yml, pages.yml) are configured.
- [ ] docs/index.html renders a clean, searchable documentation site with all guides.
- [ ] LICENSE, README.md, AGENTS.md, CODE_OF_CONDUCT.md, CONTRIBUTING.md are present and comprehensive.

## 2026-08-25T19:17:52Z

User instruction update: Please ensure the Go version is set to 1.25 or higher across go.mod, Dockerfile, Dockerfile.lambda, Makefile, and GitHub Actions workflows (e.g., Go 1.26 as available in the local environment).

## 2026-08-25T19:25:01Z

User instruction update: Ensure the `toolchain` directive is specified in `go.mod` (e.g., `toolchain go1.26.0` or `toolchain go1.25.0`) so that the Go toolchain automatically downloads the matching Go version if missing.

## 2026-08-25T19:32:08Z

User instruction update: Use `gitlab.com/gitlab-org/api/client-go` (the official GitLab Go SDK package path) for the GitLab SDK across all client wrappers and operations.
