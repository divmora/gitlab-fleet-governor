# GitLab Fleet Governor

<div align="center">

[![CI Pipeline](https://github.com/divmora/gitlab-fleet-governor/actions/workflows/ci.yml/badge.svg)](https://github.com/divmora/gitlab-fleet-governor/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/divmora/gitlab-fleet-governor?sort=semver)](https://github.com/divmora/gitlab-fleet-governor/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/divmora/gitlab-fleet-governor)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Documentation](https://img.shields.io/badge/docs-GitHub_Pages-brightgreen)](https://divmora.github.io/gitlab-fleet-governor/)

**Production-grade, declarative policy-as-code and governance automation engine in Go for GitLab fleets.**

[Features](#-key-features) •
[Quickstart](#-quickstart) •
[CLI Usage](#-cli-usage-reference) •
[Policy Reference](#-declarative-policy-reference) •
[Operations Suite](#-governance-operations-suite) •
[AWS Lambda](#-aws-lambda--serverless) •
[CI/CD Integration](#-cicd-pipeline-integration) •
[Documentation](https://divmora.github.io/gitlab-fleet-governor/)

</div>

---

## 🚀 Overview

Managing security baselines, branch protections, push rules, and compliance standards across thousands of repositories in large GitLab organizations is tedious, error-prone, and prone to configuration drift.

**GitLab Fleet Governor** (`gitlab-fleet-governor`) is a modern policy engine written in Go that treats GitLab organization governance as version-controlled code. It enables platform, security, and DevSecOps teams to:
- **Discover**: Traverse group hierarchies using recursive BFS and match projects using rich multi-filter criteria.
- **Diff ($S_D \ominus S_L$)**: Simulate policy changes and inspect attribute-level drift in dry-run mode before mutating resources.
- **Enforce**: Idempotently apply governance rules with bounded worker concurrency, token-bucket rate limiting, and exponential retry backoff.
- **Deploy Anywhere**: Run as a standalone CLI tool, in CI/CD pipelines (GitLab CI, GitHub Actions), in Docker containers, or serverless in AWS Lambda.

---

## 🌟 Key Features

- 📜 **Declarative Policy-as-Code**: Author policies in interchangeable YAML or JSON with `${ENV_VAR:-default}` substitution.
- 🌳 **Fleet-Scale Discovery**: Recursive Breadth-First Search (BFS) group hierarchy traversal with cycle detection and project filter pipelines.
- 🛡️ **10 Complete Governance Reconcilers**:
  1. **Push Rules**: Author emails, branch regexes, commit message format, file size limits, secret prevention, signed commits, and DCO sign-offs.
  2. **Protected Branches**: Push/merge/unprotect access tiers, force push bans, code owner approvals.
  3. **MR Approval Rules**: Global approval settings, named multi-approver matrices, username/group resolution.
  4. **Project Settings**: Squash policies, merge methods, discussion resolution gates, artifact retention.
  5. **Pipeline Retention**: Automated GitLab pipeline history deletion (`retention_days` converted to `ci_delete_pipelines_in_seconds`).
  6. **CI/CD Variables**: Scoped environment variables, masked secrets, protected flags, raw values, drift pruning.
  7. **Runner Governance**: Shared/group runner toggles, tag enforcement, pause/locked controls.
  8. **Compliance Frameworks**: Automated assignment of compliance framework labels (SOC2, PCI-DSS, ISO27001).
  9. **Webhooks & Integrations**: Organization webhook endpoint provisioning, trigger filters, HMAC secret tokens, SSL verification.
  10. **Member & Access Audit**: Over-privileged user detection, mandatory expiration dates, inherited maintainer deduplication.
- ⚡ **High Resilience & Concurrency**: Bounded worker pool, token-bucket rate limiting, reactive 429 backoff with full jitter, keyset streaming pagination.
- ☁️ **Dual Runtime & AWS Lambda**: Auto-detects `AWS_LAMBDA_FUNCTION_NAME` and handles EventBridge cron, S3 Put Object, and direct JSON events.
- 📊 **Multi-Format Summary Reports**: ASCII terminal tables, structured JSON, CSV, and Markdown audit reports.

---

## ⚡ Quickstart

### Step 1: Install `gitlab-fleet-governor`

```bash
# Via Go 1.25+
go install github.com/divmora/gitlab-fleet-governor/cmd/gitlab-fleet-governor@latest

# Or download pre-built binary from GitHub Releases
curl -sSL "https://github.com/divmora/gitlab-fleet-governor/releases/latest/download/gitlab-fleet-governor_Linux_x86_64.tar.gz" | tar -xz
sudo mv gitlab-fleet-governor /usr/local/bin/
```

### Step 2: Configure Environment Variables

```bash
export GITLAB_TOKEN="glpat-xxxxxxxxxxxxxxxxxxxx"
export GITLAB_BASE_URL="https://gitlab.com/api/v4" # Or your self-managed URL
```

### Step 3: Run Validation & Dry-Run Simulation

```bash
# Validate policy syntax
gitlab-fleet-governor validate -c examples/minimal.yaml

# Run dry-run simulation
gitlab-fleet-governor run -c examples/minimal.yaml --dry-run
```

---

## 🛠️ CLI Usage Reference

### Global Flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--config` | `-c` | `""` | Path to policy file, `-` for stdin, or `s3://bucket/key` |
| `--dry-run` | | `true` | Simulation mode (preview changes without mutating state) |
| `--concurrency` | | `10` | Number of parallel worker goroutines |
| `--log-level` | | `"info"` | Log level (`debug`, `info`, `warn`, `error`) |
| `--log-format` | | `"text"` | Log format (`text` or `json`) |
| `--report-format` | | `"table"` | Report format (`table`, `json`, `csv`, `markdown`) |
| `--output-file` | | `""` | File path to write summary report |
| `--no-color` | | `false` | Disable terminal color output |

### Subcommands

#### `run`
Executes fleet governance reconciliation:
```bash
# Dry-run preview
gitlab-fleet-governor run -c policies/enterprise.yaml

# Apply mutations live
gitlab-fleet-governor run -c policies/enterprise.yaml --dry-run=false

# Output Markdown report to file
gitlab-fleet-governor run -c policies/enterprise.yaml --report-format markdown --output-file report.md
```

#### `validate`
Validates configuration schema, regular expressions, and permissions without remote API calls:
```bash
gitlab-fleet-governor validate -c policies/enterprise.yaml
```

#### `lambda`
Emulates local AWS Lambda invocation with a JSON event payload:
```bash
gitlab-fleet-governor lambda --event examples/lambda-event.json
```

#### `version`
Displays version, git commit, build date, Go version, and platform info:
```bash
gitlab-fleet-governor version
gitlab-fleet-governor version --json
```

---

## 📋 Declarative Policy Reference

Policies are authored in YAML or JSON:

```yaml
version: "v1"

settings:
  dry_run: true
  concurrency: 12
  log_level: "info"
  report_format: "table"

targets:
  group_selector:
    group_paths_include: ["enterprise-fleet"]
    recursive: true
  project_selector:
    archived: false
    visibility: "private"

policies:
  push_rules:
    author_email_regex: '@enterprise\.com$'
    prevent_secrets: true
    reject_unsigned_commits: true
    max_file_size: 20

  protected_branches:
    - name: "main"
      allowed_to_push:
        - access_level: 0
      allowed_to_merge:
        - access_level: 40
      code_owner_approval_required: true

  pipeline_retention:
    retention_days: 30
```

See [Configuration Reference](https://divmora.github.io/gitlab-fleet-governor/configuration/) for full schema documentation.

---

## 🧩 Governance Operations Suite

```
 1. push_rules           Enforce commit & push policies (author regex, file size, secrets, GPG/DCO)
 2. protected_branches   Branch protection tiers, merge/push access levels, code owners
 3. approval_rules       Merge request approval settings & named reviewer matrices
 4. project_settings     Merge strategies, squash settings, discussion resolution gates
 5. pipeline_retention   Automated pipeline history cleanup (retention_days -> ci_delete_pipelines_in_seconds)
 6. variables            Scoped CI/CD variables, masked secrets, protected flags, drift pruning
 7. runners              Shared & group runner controls, maintenance pause/lock status, tags
 8. compliance           Compliance framework labeling (SOC2, PCI-DSS, ISO27001)
 9. webhooks             Fleet-wide security and audit webhook integrations
10. members              Access level ceilings, mandatory expiration dates, over-privileged detection
```

---

## ☁️ AWS Lambda & Serverless

GitLab Fleet Governor auto-detects AWS Lambda when `AWS_LAMBDA_FUNCTION_NAME` is set. It natively supports:
- **EventBridge Scheduled Cron**: Run periodic audits every hour.
- **S3 Put Object Triggers**: Reactively enforce policies whenever a policy YAML is uploaded to S3.
- **Direct JSON Invocations**: Execute ad-hoc targeted scans.

See [AWS Lambda Guide](https://divmora.github.io/gitlab-fleet-governor/lambda/) for deployment details.

---

## 🔄 CI/CD Pipeline Integration

Easily integrate into GitLab CI (`.gitlab-ci.yml`) or GitHub Actions:

```yaml
# GitHub Actions Example
- name: Run Fleet Governor Dry-Run
  uses: docker://ghcr.io/divmora/gitlab-fleet-governor:latest
  env:
    GITLAB_TOKEN: ${{ secrets.GITLAB_ADMIN_TOKEN }}
  with:
    args: run -c policies/enterprise.yaml --dry-run
```

See [CI/CD Integration Guide](https://divmora.github.io/gitlab-fleet-governor/ci-cd/) for full workflows.

---

## 🤝 Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) before submitting pull requests.

---

## 📄 License

This project is licensed under the [Apache License 2.0](LICENSE).
