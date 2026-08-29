# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0](https://github.com/divmora/gitlab-fleet-governor/compare/v0.1.0...v0.2.0) (2026-08-29)


### Features

* **docs:** add interactive Policy Studio, schema validator, and LLM documentation hub ([9ca0fdb](https://github.com/divmora/gitlab-fleet-governor/commit/9ca0fdba009572f2ebaae221a23513de66224511))
* **ui:** add full-section code highlighting and auto-scroll for DAG node selection ([2134e26](https://github.com/divmora/gitlab-fleet-governor/commit/2134e26c1fb6014264ee4b7a3e064fdb8ed1e195))
* **ui:** add Policy Studio with interactive DAG inline toggles, live validator, and Pages build ([7aa25f3](https://github.com/divmora/gitlab-fleet-governor/commit/7aa25f31b377fc9b0a71c56f7fdfeec5195bba40))
* **ui:** convert editor header actions to pure icon buttons and remove Windows CI runner ([a523297](https://github.com/divmora/gitlab-fleet-governor/commit/a523297250a088273b8e4bf7981c12ff55dcf820))
* **ui:** unify code editor header with compact actions and maximize vertical height ([876c9c2](https://github.com/divmora/gitlab-fleet-governor/commit/876c9c2d0a629a8b4d828179ab766048f4abc20f))


### Bug Fixes

* **ci:** align go.mod with golangci-lint toolchain and resolve context leak in pagination test ([20aa523](https://github.com/divmora/gitlab-fleet-governor/commit/20aa523a22a9b6424add556f1965ec59750d21fd))
* **lint:** resolve golangci-lint staticcheck, gosimple, and unused warnings ([24870ab](https://github.com/divmora/gitlab-fleet-governor/commit/24870ab519b5e8f344104cda278c19a9444de452))
* **testutil:** eliminate mockserver data race via struct cloning and add rich dark-mode tooltips ([6bc424e](https://github.com/divmora/gitlab-fleet-governor/commit/6bc424e7f873683d13335a6924d7fb47e9cb8561))
* **ui:** align project selector visibility validation with 'any' ([0fbb95c](https://github.com/divmora/gitlab-fleet-governor/commit/0fbb95c7302b1bb8fe6541f6fb9e5816891c38e0))

## [0.1.0] - 2026-08-25

### ✨ Features
- **cli**: Implement modern Cobra CLI suite featuring `run`, `validate`, `lambda`, and `version` subcommands.
- **config**: Support declarative YAML and JSON policy schemas with strict schema validation, RE2 regular expressions, environment variable interpolation (`${VAR}` and `${VAR:-default}`), and pluggable configuration loading (local file, standard input, environment variables, AWS S3).
- **discovery**: Scalable GitLab group hierarchy traversal using breadth-first search (BFS) with cycle detection and project targeting filtering by namespace, regex, visibility, archived status, topics, and ID ranges.
- **gitlab-client**: Resilient GitLab REST API v4 client wrapper with token bucket rate limiting (proactive RPS and burst limits) and exponential backoff retry with jitter on HTTP 429 and 5xx errors.
- **governance**: Comprehensive policy governance operations suite:
  - Project and group push rules enforcement (author email, branch names, commit messages, file names, max size, secret scanning, DCO, signed commits).
  - Protected branch rule upsert and access level enforcement.
  - Merge request approval settings and named approval rules with dynamic user/group resolution.
  - Arbitrary project settings enforcement (merge strategies, pipeline success requirements, artifact retention).
  - High-level pipeline retention policy governance (`retention_days`).
  - Project and group CI/CD variables and secrets governance.
  - CI/CD runner fleet configuration and protection governance.
  - Compliance framework label enforcement.
  - Webhook integration endpoint governance.
  - Member role level assertions and expiration governance.
- **engine**: Concurrent worker pool dispatcher with bounded concurrency channel, dry-run simulation mode, and structured tabular/JSON summary reporting.
- **lambda**: AWS Lambda serverless execution handler supporting EventBridge scheduled triggers, S3 Put events, and direct JSON invocation payloads.
- **ci-cd**: Cross-platform GoReleaser build automation, multi-arch Docker container images (`ghcr.io/divmora/gitlab-fleet-governor`), Release Please automation, and MkDocs documentation workflow.

### 🔒 Security
- Strict access level assertions and member expiration governance.
- Zero credential logging with token masking and support for protected/masked CI/CD variables.

### 📚 Documentation
- Comprehensive documentation site covering Getting Started, Architecture, Configuration Schema, Governance Operations, CI/CD Automation, and AWS Lambda Deployment.
