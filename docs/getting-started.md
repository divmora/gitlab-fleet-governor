# Getting Started

This guide walks you through installing `gitlab-fleet-governor`, configuring credentials, and executing your first dry-run policy audit.

---

## Prerequisites

- **GitLab Instance**: GitLab.com (SaaS) or Self-Managed GitLab instance (v15.0+).
- **GitLab API Token**: Personal Access Token, Group Access Token, Project Access Token, or CI Job Token with `api` scope.
- **Go**: 1.25+ (if compiling from source).

---

## Installation

### Option 1: Pre-Built Binary (Recommended)

Download the latest release binary for your operating system and architecture from [GitHub Releases](https://github.com/divmora/gitlab-fleet-governor/releases):

```bash
# Example for Linux AMD64
curl -sSL "https://github.com/divmora/gitlab-fleet-governor/releases/latest/download/gitlab-fleet-governor_Linux_x86_64.tar.gz" | tar -xz
sudo mv gitlab-fleet-governor /usr/local/bin/
gitlab-fleet-governor version
```

### Option 2: Go Install

```bash
go install github.com/divmora/gitlab-fleet-governor/cmd/gitlab-fleet-governor@latest
```

### Option 3: Docker Container

```bash
docker pull ghcr.io/divmora/gitlab-fleet-governor:latest
docker run --rm ghcr.io/divmora/gitlab-fleet-governor:latest version
```

---

## Authentication Setup

Export your GitLab API credentials and target instance URL:

```bash
# Personal Access Token or Group Access Token
export GITLAB_TOKEN="glpat-xxxxxxxxxxxxxxxxxxxx"

# For Self-Managed GitLab instances (defaults to https://gitlab.com/api/v4)
export GITLAB_BASE_URL="https://gitlab.example.com/api/v4"
```

Alternatively, `gitlab-fleet-governor` recognizes `PRIVATE_TOKEN`, `CI_JOB_TOKEN`, and `CI_API_V4_URL` automatically.

---

## Creating Your First Policy

Create a file named `governance.yaml`:

```yaml
version: "v1"

settings:
  dry_run: true
  concurrency: 8
  log_level: "info"
  report_format: "table"

targets:
  group_selector:
    group_paths_include:
      - "my-organization/core-team"
    recursive: true
  project_selector:
    archived: false
    visibility: "private"

policies:
  push_rules:
    author_email_regex: '@myorg\.com$'
    branch_name_regex: '^(main|develop|(feature|bugfix|hotfix)\/[a-zA-Z0-9_-]+)$'
    prevent_secrets: true
    reject_unsigned_commits: true
    max_file_size: 10

  protected_branches:
    - name: "main"
      allowed_to_push:
        - access_level: 0 # No one can push directly
      allowed_to_merge:
        - access_level: 40 # Maintainers only
      code_owner_approval_required: true
      allow_force_push: false

  pipeline_retention:
    retention_days: 30
```

---

## Validating Configuration

Check your policy for schema errors, regular expression syntax, and access level correctness without contacting GitLab:

```bash
gitlab-fleet-governor validate -c governance.yaml
```

Output:
```
✔ Configuration 'governance.yaml' is valid! (Schema: v1, Concurrency: 8, DryRun: true)
```

---

## Executing Dry-Run Simulation

Simulate the governance run to preview detected drift across your fleet:

```bash
gitlab-fleet-governor run -c governance.yaml
```

Or explicitly enforce dry-run from the command line:

```bash
gitlab-fleet-governor run -c governance.yaml --dry-run
```

---

## Applying Mutations to the Fleet

Once the diff report is verified, apply the policy by setting `--dry-run=false`:

```bash
gitlab-fleet-governor run -c governance.yaml --dry-run=false
```

---

## Next Steps

- Explore [Configuration Reference](configuration.md) for full YAML/JSON schema details.
- Learn about the [Operations Suite](operations.md) to manage MR approvals, CI/CD variables, runners, and webhooks.
- Set up [AWS Lambda](lambda.md) or [CI/CD Pipelines](ci-cd.md) for automated continuous governance.
