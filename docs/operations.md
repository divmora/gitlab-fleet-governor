# Operations Suite

GitLab Fleet Governor implements an ordered suite of 10 declarative governance reconcilers. Each reconciler inspects the live resource state ($S_L$), compares it against the desired policy declaration ($S_D$), produces an attribute diff ($S_D \ominus S_L$), and idempotently applies mutations when dry-run is disabled.

---

## Reconciler Execution Order

Operations execute sequentially per targeted project/group in the following deterministic order:

| Order | Operation Identifier | Scope | Mutating API Endpoints |
|---|---|---|---|
| 10 | `push_rules` | Project & Group | `GET/POST/PUT /projects/:id/push_rule`, `GET/POST/PUT /groups/:id/push_rule` |
| 20 | `protected_branches` | Project | `GET/POST/PATCH/DELETE /projects/:id/protected_branches/:name` |
| 30 | `approval_rules` | Project | `GET/PUT /projects/:id/approvals`, `GET/POST/PUT/DELETE /projects/:id/approval_rules` |
| 40 | `project_settings` | Project | `GET/PUT /projects/:id` |
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

### 2. Protected Branches Reconciler (`protected_branches`)
- **Upsert Mechanics**: If the branch protection does not exist, it issues `POST /projects/:id/protected_branches`. If it exists but drift is detected in access levels or force push settings, it executes `PATCH` or recreates the protection atomically.
- **Access Level Normalization**: Translates role names (`No Access`, `Developer`, `Maintainer`, `Admin`) into integer access levels (`0`, `30`, `40`, `60`).

### 3. MR Approval Rules Reconciler (`approval_rules`)
- **General Settings**: Reconciles author approval bans, committer approval bans, approver list overrides, and approval retention on new commits.
- **Named Rule Resolution**: Automatically resolves approver usernames (`@alice`, `@bob`) to numeric GitLab user IDs and group paths (`security/appsec`) to group IDs with an in-memory cache to prevent redundant API queries.
- **Pruning**: When `prune: true` is configured, removes unmanaged legacy approval rules while preserving protected rules.

### 4. Project Settings Reconciler (`project_settings`)
- **Workflow Standardization**: Enforces squash options (`always`, `never`, `default_on`, `default_off`), merge strategies (`merge`, `rebase_merge`, `ff`), discussion resolution requirements, and artifact expiration overrides.
- **Container Expiration Policies**: Configures automated container registry cleanup cadence, retention counts, and regex preservation rules.

### 5. Pipeline Retention Reconciler (`pipeline_retention`)
- **Unit Conversion**: Translates human-friendly `retention_days` into GitLab's native `ci_delete_pipelines_in_seconds` (`days * 86400`).
- **Idempotency**: Avoids updating project settings if the retention seconds already match the target duration.

### 6. CI/CD Variables Reconciler (`variables`)
- **Composite Key Identification**: Uses the composite key `(key, environment_scope)` to accurately track scoped variables.
- **Secret Protection**: Compares values, masked flags, protected flags, and raw expansion flags. Automatically prunes untracked managed variables when drift deletion is enabled.

### 7. Runners Reconciler (`runners`)
- **Fleet Governance**: Governs shared runners enabled/disabled, group runner inheritance, maintenance pause states, locked status, and runner tag lists.

### 8. Compliance Framework Reconciler (`compliance`)
- **Framework Labeling**: Resolves compliance framework names (e.g. `SOC2`, `PCI-DSS`) to framework IDs and associates them with targeted repositories.

### 9. Webhooks Reconciler (`webhooks`)
- **URL Matching**: Identifies existing webhooks by URL endpoint.
- **Event Trigger Matrix**: Updates trigger flags (`push_events`, `merge_requests_events`, `pipeline_events`), SSL verification, and secret HMAC tokens.

### 10. Members Audit Reconciler (`members`)
- **Over-Privileged Detection**: Identifies and reports users whose role exceeds `max_access_level` (e.g. unexpected Maintainer/Owner grants).
- **Expiration Date Enforcement**: Verifies that every direct project member has an `expires_at` date configured within `max_expiration_days`.
- **Inherited Maintainer Deduplication**: Identifies redundant direct project permissions where group inheritance already provides sufficient access.

---

## Fleet Compliance & Security Audit Suite

In addition to the 10 policy mutation reconcilers, GitLab Fleet Governor provides a dedicated, non-mutating compliance auditing framework accessible via the `audit` command:

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

---

## Executive Report Generation & Distribution

### Multi-Sheet Excel Workbooks (`.xlsx`)
When exported to `.xlsx` (default when `-o file.xlsx` is specified), GitLab Fleet Governor uses `excelize` to produce an executive workbook containing four distinct sheets:
1. **Executive Summary**: KPI metrics cards (Projects Scanned, Total Findings, Critical, High, Medium, Low, Passing Counts), overall compliance health score badge, and module breakdown tables.
2. **User Access**: Detailed direct membership audit with Project ID, Project Name, Project URL, Username, Role, Expiration Status, and Severity Badges. Contiguous rows for the same project are visually merged with clickable GitLab hyperlinks.
3. **Protected Branch Access**: Branch names, push access levels, merge access levels, force push status, code owner requirement, and severity findings.
4. **Protected Environments Access**: Environment names, deploy access tiers, approval thresholds, direct user deploy permissions, and findings.

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

