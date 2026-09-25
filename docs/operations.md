# Operations Suite

GitLab Fleet Governor implements an ordered suite of 12 declarative governance reconcilers. Each reconciler inspects the live resource state ($S_L$), compares it against the desired policy declaration ($S_D$), produces an attribute diff ($S_D \ominus S_L$), and idempotently applies mutations when dry-run is disabled.

---

## Reconciler Execution Order

Operations execute sequentially per targeted project/group in the following deterministic order:

| Order | Operation Identifier | Scope | Mutating API Endpoints |
|---|---|---|---|
| 10 | `push_rules` | Project & Group | `GET/POST/PUT /projects/:id/push_rule`, `GET/POST/PUT /groups/:id/push_rule` |
| 15 | `repository_files` | Project | `GET/POST/PUT /projects/:id/repository/files/:file_path`, `POST /projects/:id/merge_requests` |
| 20 | `protected_branches` | Project | `GET/POST/PATCH/DELETE /projects/:id/protected_branches/:name` |
| 30 | `approval_rules` | Project | `GET/PUT /projects/:id/approvals`, `GET/POST/PUT/DELETE /projects/:id/approval_rules` |
| 40 | `project_settings` | Project | `GET/PUT /projects/:id` |
| 45 | `target_branch_rules` | Project | `POST /api/graphql` (`projectTargetBranchRuleCreate`, `projectTargetBranchRuleDestroy`) |
| 50 | `pipeline_retention` | Project | `GET/PUT /projects/:id` (`ci_delete_pipelines_in_seconds`) |
| 60 | `variables` | Project & Group | `GET/POST/PUT/DELETE /projects/:id/variables`, `/groups/:id/variables` |
| 70 | `runners` | Project | `GET/PUT /projects/:id`, `GET/PUT /runners/:id` |
| 80 | `compliance` | Project | `GET/PUT /projects/:id` (`compliance_framework_setting`) |
| 90 | `webhooks` | Project & Group | `GET/POST/PUT/DELETE /projects/:id/hooks`, `/groups/:id/hooks` |
| 100 | `members` | Project & Group | `GET/POST/PUT/DELETE /projects/:id/members`, `/groups/:id/members` |

---

## Reconciler Deep Dives

### 1. Push Rules Reconciler (`push_rules`)
- **GitLab API Quirks Handled**: GitLab returns HTTP 404 when querying push rules for a project/group that has never had push rules configured. The reconciler catches HTTP 404 and transitions cleanly from `PUT` (update) to `POST` (create).
- **Attribute Diffing**: Checks regular expressions, file size thresholds, commit committer checks, member checks, secret prevention, and signed commit requirements.

### 2. Repository Files Reconciler (`repository_files`)
- **Declarative Synchronization**: Synchronizes files across fleet repositories (e.g. `CODEOWNERS`, `SECURITY.md`, `.editorconfig`, baseline `.gitlab-ci.yml` includes).
- **Enforcement Modes**:
  - `direct_commit`: Direct commit to target branch (or dynamic fallback to project default branch). Catches HTTP 403 Forbidden on protected branches where pushing is disallowed and provides actionable guidance.
  - `merge_request`: Checks for existing open merge requests and branch state to maintain idempotency, commits updates without duplicate MRs, and creates MRs when needed.
  - `audit_only`: Detects drift without performing mutations.
- **Normalization & Remediation**: Normalizes CRLF and LF line endings to eliminate false-positive drift, strips leading slashes on paths, and supports `ensure_contains` substring checks.

### 3. Protected Branches Reconciler (`protected_branches`)
- **Upsert Mechanics**: If the branch protection does not exist, it issues `POST /projects/:id/protected_branches`. If it exists but drift is detected in access levels or force push settings, it executes `PATCH` or recreates the protection atomically.
- **Access Level Normalization**: Translates role names (`No Access`, `Developer`, `Maintainer`, `Admin`) into integer access levels (`0`, `30`, `40`, `60`).

### 4. MR Approval Rules Reconciler (`approval_rules`)
- **General Settings**: Reconciles author approval bans, committer approval bans, approver list overrides, and approval retention on new commits.
- **Named Rule Resolution**: Automatically resolves approver usernames (`@alice`, `@bob`) to numeric GitLab user IDs and group paths (`security/appsec`) to group IDs with an in-memory cache to prevent redundant API queries.
- **Pruning**: When `prune: true` is configured, removes unmanaged legacy approval rules while preserving protected rules.

### 5. Project Settings Reconciler (`project_settings`)
- **Workflow Standardization**: Enforces squash options (`always`, `never`, `default_on`, `default_off`), merge strategies (`merge`, `rebase_merge`, `ff`), discussion resolution requirements, and artifact expiration overrides.
- **Container Expiration Policies**: Configures automated container registry cleanup cadence, retention counts, and regex preservation rules.

### 6. Target Branch Rules Reconciler (`target_branch_rules`)
- **GraphQL ID Specification**: GitLab's Target Branch Rules API is exclusively exposed via GraphQL. Mutations target `ProjectID!` (`gid://gitlab/Project/:id`) using `projectTargetBranchRuleCreate` and `projectTargetBranchRuleDestroy`.
- **Merge Request Workflow Routing**: Maps wildcard branch prefixes (e.g., `feat/*`, `hotfix/*`, `*`) to designated target branches (e.g., `staging`, `main`), ensuring consistent promotion paths across repositories.
- **Conflict Prevention & Invariant Checks**: Enforces that `source_branch_pattern` is unique per project and rejects self-targeting definitions (`source_branch_pattern == target_branch_name`).
- **Pruning**: When `prune_unmanaged: true` is configured, automatically removes out-of-policy branch rules while preserving desired routing configurations.

### 7. Pipeline Retention Reconciler (`pipeline_retention`)
- **Unit Conversion**: Translates human-friendly `retention_days` into GitLab's native `ci_delete_pipelines_in_seconds` (`days * 86400`).
- **Idempotency**: Avoids updating project settings if the retention seconds already match the target duration.

### 8. CI/CD Variables Reconciler (`variables`)
- **Composite Key Identification**: Uses the composite key `(key, environment_scope)` to accurately track scoped variables.
- **Secret Protection**: Compares values, masked flags, protected flags, and raw expansion flags. Automatically prunes untracked managed variables when drift deletion is enabled.

### 9. Runners Reconciler (`runners`)
- **Fleet Governance**: Governs shared runners enabled/disabled, group runner inheritance, maintenance pause states, locked status, and runner tag lists.

### 10. Compliance Framework Reconciler (`compliance`)
- **Framework Labeling**: Resolves compliance framework names (e.g. `SOC2`, `PCI-DSS`) to framework IDs and associates them with targeted repositories.

### 11. Webhooks Reconciler (`webhooks`)
- **URL Matching**: Identifies existing webhooks by URL endpoint.
- **Event Trigger Matrix**: Updates trigger flags (`push_events`, `merge_requests_events`, `pipeline_events`), SSL verification, and secret HMAC tokens.

### 12. Members Audit Reconciler (`members`)
- **Over-Privileged Detection**: Identifies and reports users whose role exceeds `max_access_level` (e.g. unexpected Maintainer/Owner grants).
- **Expiration Date Enforcement**: Verifies that every direct project member has an `expires_at` date configured within `max_expiration_days`.
- **Inherited Maintainer Deduplication**: Identifies redundant direct project permissions where group inheritance already provides sufficient access.

---

## Fleet Compliance & Security Audit Suite

In addition to the 12 policy mutation reconcilers, GitLab Fleet Governor provides a dedicated, non-mutating compliance auditing framework accessible via the `audit` command:

```bash
gitlab-fleet-governor audit -c governance.yaml -o fleet-audit.xlsx
```

### Audit Modules

The audit framework executes read-only inspections in parallel across all targeted groups and projects, classifying findings into standardized severity levels: `CRITICAL`, `HIGH`, `MEDIUM`, `LOW`, and `PASS`.

#### 1. User Access Expiration Hygiene (`user_access`)
- **Indefinite Expiration Anomaly Detection**: Audits direct project members to ensure access expiration dates (`expires_at`) are strictly enforced.
  - **Maintainers / Admins** without expiration: `CRITICAL` finding.
  - **Developers / Reporters** without expiration: `HIGH` finding.
  - **Guests** without expiration: `MEDIUM` finding.
  - Members with configured expiration: `PASS`.

#### 2. Protected Branch Security Posture (`protected_branches`)
- **Branch Protection Gaps**: Evaluates default branches and all branch protection rules:
  - **Unprotected Default Branch**: `CRITICAL` finding if the project's default branch has no branch protection configured.
  - **Force Push Allowed**: `HIGH` finding if branch protection permits force pushes (`allow_force_push: true`).
  - **Code Owner Review Disabled**: `MEDIUM` finding if `code_owner_approval_required` is `false`.
  - **Direct User Push Grants**: `MEDIUM` finding if individual users are granted direct push access instead of role-based group tiers.

#### 3. Protected Environments Deployment Gates (`protected_environments`)
- **Production Environment Hardening**: Inspects protected environments (matching `*prod*` / `production`):
  - **Zero Approval Gate**: `CRITICAL` finding if a production environment has `required_approval_count: 0`.
  - **Direct User Deploy Grants**: `MEDIUM` finding if individual users are granted direct deploy access without role-based access tiers.

#### 4. Repository Files Compliance (`repository_files`)
- **Standardized File Integrity**: Inspects repository files against declared `repository_files` policy requirements:
  - **Missing Policy File**: `CRITICAL` finding when required files (`CODEOWNERS`, security policies) are absent.
  - **Content Drift or Missing Segments**: `HIGH` finding when content differs from policy or missing required substrings from `ensure_contains`.
  - **Compliant File**: `PASS` finding when existing file content matches policy after line ending normalization.

---

## Executive Report Generation & Distribution

### Multi-Sheet Excel Workbooks (`.xlsx`)
When exported to `.xlsx` (default when `-o file.xlsx` is specified), GitLab Fleet Governor uses `excelize` to produce an executive workbook containing five distinct sheets:
1. **Executive Summary**: KPI metrics cards (Projects Scanned, Total Findings, Critical, High, Medium, Low, Passing Counts), overall compliance health score badge, and module breakdown tables.
2. **User Access**: Detailed direct membership audit with Project ID, Project Name, Project URL, Username, Role, Expiration Status, and Severity Badges. Contiguous rows for the same project are visually merged with clickable GitLab hyperlinks.
3. **Protected Branch Access**: Branch names, push access levels, merge access levels, force push status, code owner requirement, and severity findings.
4. **Protected Environments Access**: Environment names, deploy access tiers, approval thresholds, direct user deploy permissions, and findings.
5. **Repository Files**: File paths, target branches, drift status, missing line segments, and severity findings.

### Multi-Format Exporters
Reports can be exported to multiple formats via `--format` or file extension detection:
- `--format xlsx`: Formatted workbook with auto-filter headers, alternating rows, and column auto-sizing.
- `--format json`: Machine-parseable JSON containing all findings and summary metrics.
- `--format csv`: Tabular CSV output for spreadsheets and SIEM pipelines.
- `--format markdown`: GitHub-flavored Markdown table with severity badges.
- `--format html`: Standalone HTML report with responsive styling and metric cards.
- `--format table`: Colored terminal table.

### Headless SMTP Email Dispatcher
Audit reports can be dispatched automatically to compliance officers, auditors, or distribution lists:
- **Transport Security**: Direct TLS (port 465) or STARTTLS (port 587/25) with automatic fallback.
- **Authentication**: Supports standard `PLAIN` and `LOGIN` authentication mechanisms.
- **Attachment Packaging**: Generates and attaches the formatted `.xlsx` workbook to a rich multipart MIME message (HTML body + plain text alternative).
- **Automation CLI**: Configurable via flags (`--smtp-host`, `--smtp-to`, `--smtp-cc`, `--smtp-bcc`, etc.) or standard environment variables (`SMTP_HOST`, `SMTP_PASSWORD`, `SMTP_TO`, `SMTP_CC`, `SMTP_BCC`).

