# CI/CD Pipeline Integration

Automate policy validation, pull request simulation, and continuous fleet enforcement in GitLab CI or GitHub Actions.

---

## Pattern: Dual-Stage Governance Pipeline

A recommended governance pipeline operates in two stages:
1. **MR / PR Stage (Simulation & Drift Detection)**:
   - Validates configuration syntax (`validate`).
   - Runs `run --dry-run` against targeted groups.
   - Posts the generated Markdown summary diff as a comment on the merge request.
2. **Main Branch Stage (Continuous Enforcement)**:
   - Triggered on merge to `main` or scheduled cron.
   - Runs `run --dry-run=false` to idempotently remediate drift.

---

## GitLab CI/CD Pipeline (`.gitlab-ci.yml`)

```yaml
stages:
  - validate
  - plan
  - enforce

variables:
  GITLAB_TOKEN: "${FLEET_GOVERNOR_TOKEN}"
  GITLAB_BASE_URL: "${CI_API_V4_URL}"

default:
  image:
    name: ghcr.io/divmora/gitlab-fleet-governor:latest
    entrypoint: [""]

# Stage 1: Validate policy schema
validate-policy:
  stage: validate
  script:
    - gitlab-fleet-governor validate -c policies/enterprise.yaml

# Stage 2: Preview drift on Merge Requests
plan-governance:
  stage: plan
  script:
    - gitlab-fleet-governor run -c policies/enterprise.yaml --dry-run --report-format markdown --output-file drift-report.md
    - cat drift-report.md
  artifacts:
    name: "governance-drift-report"
    paths:
      - drift-report.md
    expire_in: 7 days
  rules:
    - if: '$CI_PIPELINE_SOURCE == "merge_request_event"'

# Stage 3: Enforce policies on default branch
apply-governance:
  stage: enforce
  script:
    - gitlab-fleet-governor run -c policies/enterprise.yaml --dry-run=false
  rules:
    - if: '$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH'
    - if: '$CI_PIPELINE_SOURCE == "schedule"'
```

---

## GitHub Actions Workflow (`.github/workflows/fleet-governor.yml`)

```yaml
name: Fleet Governance Automation

on:
  pull_request:
    branches: [main]
    paths:
      - 'policies/**'
  push:
    branches: [main]
    paths:
      - 'policies/**'
  schedule:
    - cron: '0 */4 * * *' # Every 4 hours
  workflow_dispatch:
    inputs:
      dry_run:
        description: 'Run in dry-run simulation mode'
        required: true
        default: 'true'
        type: boolean

jobs:
  governance:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout Policies
        uses: actions/checkout@v4

      - name: Run GitLab Fleet Governor
        uses: docker://ghcr.io/divmora/gitlab-fleet-governor:latest
        env:
          GITLAB_TOKEN: ${{ secrets.GITLAB_ADMIN_TOKEN }}
          GITLAB_BASE_URL: "https://gitlab.com/api/v4"
        with:
          args: >-
            run -c policies/enterprise.yaml
            --dry-run=${{ github.event_name == 'pull_request' || (github.event_name == 'workflow_dispatch' && inputs.dry_run) }}
            --report-format markdown
            --output-file /tmp/report.md

      - name: Post PR Drift Summary
        if: github.event_name == 'pull_request'
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const report = fs.readFileSync('/tmp/report.md', 'utf8');
            github.rest.issues.createComment({
              issue_number: context.issue.number,
              owner: context.repo.owner,
              repo: context.repo.repo,
              body: `### 🛡️ GitLab Fleet Governor Simulation Report\n\n${report}`
            });
```
