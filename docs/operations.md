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
