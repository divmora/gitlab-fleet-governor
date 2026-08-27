# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
