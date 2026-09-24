# Configuration Reference

GitLab Fleet Governor uses a declarative schema supporting interchangeable **YAML** and **JSON** configurations.

---

## Top-Level Schema

```yaml
version: "v1"       # Required. Schema version: "v1"
settings: { ... }   # Global runtime and client parameters
targets: { ... }    # Fleet discovery and target selector rules
policies: { ... }   # Governance policy declarations
```

---

## Environment Variable Substitution

All string values in YAML/JSON support environment variable substitution:

- `${VAR_NAME}`: Resolves to the environment variable value.
- `${VAR_NAME:-default_value}`: Resolves to the environment variable value or fallback default if unset/empty.

Example:
```yaml
settings:
  gitlab:
    base_url: "${GITLAB_BASE_URL:-https://gitlab.com/api/v4}"
    token: "${GITLAB_TOKEN}"
```

---

## Configuration Sources & Precedence

Configurations are resolved in the following priority order:

1. CLI flag: `--config <path>`, `-c <path>`, or `-c -` (standard input).
2. Inline environment content: `CONFIG_CONTENT`, `CONFIG_YAML`, `CONFIG_JSON`.
3. Source reference environment: `CONFIG_SOURCE`, `CONFIG_FILE` (supports file paths or `s3://bucket/key`).
4. Default candidate files in the working directory:
   - `gitlab-fleet-governor.yaml`, `.gitlab-fleet-governor.yaml`
   - `gitlab-fleet-governor.yml`, `.gitlab-fleet-governor.yml`
   - `gitlab-fleet-governor.json`, `.gitlab-fleet-governor.json`
   - `config.yaml`, `config.yml`, `config.json`

---

## `settings` Reference

| Field | Type | Default | Description |
|---|---|---|---|
| `dry_run` | bool | `true` | When true, previews changes without mutating GitLab state. |
| `concurrency` | int | `10` | Number of concurrent worker goroutines. |
| `log_level` | string | `"info"` | Log level: `"debug"`, `"info"`, `"warn"`, `"error"`. |
| `log_format` | string | `"text"` | Log format: `"text"` (colored human-readable) or `"json"`. |
| `report_format`| string | `"table"`| Report output format: `"table"`, `"json"`, `"csv"`, `"markdown"`. |
| `output_file_path` | string | `""` | Optional file path to save the summary report. |
| `gitlab.base_url` | string | `"https://gitlab.com/api/v4"` | GitLab REST API v4 base URL. |
| `gitlab.token` | string | `""` | Authentication token (`GITLAB_TOKEN` / `PRIVATE_TOKEN`). |
| `gitlab.token_type` | string | `"private_token"` | Auth token type: `"private_token"`, `"oauth"`, `"job_token"`. |
| `gitlab.timeout_seconds` | int | `30` | HTTP request timeout in seconds. |
| `gitlab.rate_limit_rps` | float | `30.0` | Client-side proactive token-bucket request rate limit. |
| `gitlab.rate_limit_burst` | int | `50` | Client-side token-bucket burst capacity. |
| `gitlab.max_retries` | int | `3` | Maximum retry attempts for HTTP 429 and 5xx errors. |
| `gitlab.retry_base_delay_ms`| int | `500` | Base exponential backoff delay in milliseconds. |
| `gitlab.retry_max_delay_ms` | int | `30000` | Cap for exponential backoff delay in milliseconds. |
| `license.key` | string | `""` | Commercial enterprise license key token string. |
| `license.file` | string | `""` | Path to commercial enterprise license key file. |

> [!TIP]
> For full details on subscription plans, feature entitlements (Free Community Tier, Pro Plan, Enterprise Plan), and the Community zero-check guarantee, see the **[Subscription Plans & Feature Matrix](plans.md)**.

---

## `targets` Reference

### `group_selector`

Controls group hierarchy discovery:

```yaml
targets:
  group_selector:
    group_ids_include: [101, 102]
    group_ids_exclude: [999]
    group_paths_include:
      - "engineering"
      - "platform/infrastructure"
    group_paths_exclude:
      - "engineering/archived"
    recursive: true # BFS traversal of all child subgroups (default: true)
```

### `project_selector`

Filters projects within discovered groups:

```yaml
targets:
  project_selector:
    namespaces_include:
      - "engineering/services"
    namespaces_exclude:
      - "engineering/services/deprecated"
    project_name_regex_include: '.*-service$'
    project_name_regex_exclude: '^temp-.*'
    topics_include: ["tier-1", "backend"]
    topics_exclude: ["experimental"]
    visibility: "private" # "public", "internal", "private", or "any"
    archived: false       # false (active only), true (archived only), omit (any)
    exclude_marked_for_deletion: true # true (exclude pending deletion), false (include), omit (defaults to true if archived is false)
    id_range:
      min: 1
      max: 50000
```

---

## `policies` Reference

### 1. `push_rules`

```yaml
policies:
  push_rules:
    author_email_regex: '@company\.com$'
    branch_name_regex: '^(main|release\/.*|feat\/.*)$'
    commit_message_regex: '^(feat|fix|docs|chore)(\(.*\))?: .+'
    commit_message_negative_regex: '(?i)wip|do not merge'
    file_name_regex: '(id_rsa|\.pem|\.pfx|\.key)$'
    max_file_size: 20 # Megabytes (MB)
    commit_committer_check: true
    member_check: true
    prevent_secrets: true
    deny_delete_tag: true
    reject_unsigned_commits: true
    reject_non_dco_commits: true
```

### 2. `protected_branches`

```yaml
policies:
  protected_branches:
    - name: "main"
      allowed_to_push:
        - access_level: 0 # 0=No Access, 30=Developer, 40=Maintainer, 60=Admin
      allowed_to_merge:
        - access_level: 40
      allowed_to_unprotect:
        - access_level: 40
      allow_force_push: false
      code_owner_approval_required: true

    - name: "release/*"
      allowed_to_push:
        - access_level: 40
      allowed_to_merge:
        - access_level: 40
      allow_force_push: false
      code_owner_approval_required: true
```

### 3. `approval_rules`

```yaml
policies:
  approval_rules:
    settings:
      allow_author_approval: false
      allow_committer_approval: false
      allow_overrides_to_approver_list_per_merge_request: false
      retain_approvals_on_push: true
      selective_code_owner_removals: true
      require_password_to_approve: false
    rules:
      - name: "Security Gate"
        approvals_required: 2
        user_usernames: ["sec_lead", "appsec_bot"]
        group_paths: ["security/appsec-reviewers"]
        protected_branch_names: ["main", "release/*"]
        rule_type: "regular"
    prune: true # Deletes unmanaged named approval rules
```

### 4. `project_settings` & `pipeline_retention`

```yaml
policies:
  project_settings:
    default_branch: "main"
    squash_option: "always" # "never", "always", "default_on", "default_off"
    merge_method: "rebase_merge" # "merge", "rebase_merge", "ff"
    only_allow_merge_if_pipeline_succeeds: true
    allow_merge_on_skipped_pipeline: false
    only_allow_merge_if_all_discussions_are_resolved: true
    remove_source_branch_after_merge: true
    keep_latest_artifact: true
    printing_merge_request_link_enabled: true
    auto_cancel_pending_pipelines: "enabled"
    auto_devops_enabled: false
    container_expiration_policy:
      cadence: "7d"
      enabled: true
      keep_n: 10
      older_than: "30d"

  pipeline_retention:
    retention_days: 30 # Maps to ci_delete_pipelines_in_seconds = 2592000
```

### 5. `variables`

```yaml
policies:
  variables:
    - key: "GLOBAL_REGISTRY_URL"
      value: "registry.company.internal"
      variable_type: "env_var" # "env_var" or "file"
      protected: true
      masked: false
      raw: true
      environment_scope: "*"
      description: "Internal container registry mirror"

    - key: "PROD_DEPLOY_KEY"
      value: "${SECRET_PROD_DEPLOY_KEY}"
      protected: true
      masked: true
      environment_scope: "production"
```

### 6. `runners`

```yaml
policies:
  runners:
    shared_runners_enabled: true
    group_runners_enabled: true
    runners:
      - id: 42
        paused: false
        locked: true
        tag_list: ["linux", "docker", "gpu"]
        run_untagged: false
        access_level: "ref_protected"
```

### 7. `compliance`

```yaml
policies:
  compliance:
    framework_name: "SOC2"
    prune: false
```

### 8. `webhooks`

```yaml
policies:
  webhooks:
    - url: "https://siem.internal.company.com/api/v1/gitlab-events"
      push_events: true
      merge_requests_events: true
      pipeline_events: true
      job_events: false
      enable_ssl_verification: true
      secret_token: "${SIEM_WEBHOOK_SECRET}"
```

### 9. `members`

```yaml
policies:
  members:
    max_access_level: 40 # Flags owner roles
    enforce_expires_at: true
    max_expiration_days: 90
    denied_members: ["external_contractor_bad"]
    allowed_members:
      - username: "security_auditor"
        access_level: 20 # Reporter
        expires_at: "2026-12-31"

### 10. `target_branch_rules`

Declaratively govern GitLab Merge Request Branch Workflow default targets based on source branch naming patterns:

```yaml
policies:
  target_branch_rules:
    prune: true # Deletes unmanaged target branch rules
    rules:
      - name: "feature/*"
        target_branch: "develop"
      - name: "hotfix/*"
        target_branch: "main"
      - name: "release/*"
        target_branch: "production"
```

### 11. `repository_files`

Declaratively enforce and synchronize standardized files across fleet repositories (e.g. `CODEOWNERS`, `SECURITY.md`, `.editorconfig`, baseline `.gitlab-ci.yml` includes):

```yaml
policies:
  repository_files:
    - path: "CODEOWNERS"
      content: |
        # Enterprise CODEOWNERS policy
        # Auto-managed by gitlab-fleet-governor — do not edit manually
        *                              @platform/security-reviewers
        /src/                          @platform/backend-leads
      target_branch: "main"
      enforcement: "merge_request" # direct_commit | merge_request | audit_only
      mr_title: "chore: sync CODEOWNERS to enterprise policy"
      mr_labels: ["automated", "governance", "CODEOWNERS"]
      auto_merge: false

    - path: "SECURITY.md"
      content_file: "templates/SECURITY.md.tmpl" # Local path or s3:// URI
      target_branch: "main"
      enforcement: "merge_request"

    - path: ".gitlab-ci.yml"
      ensure_contains:
        - "include:"
        - "project: 'platform/ci-templates'"
      target_branch: "main"
      enforcement: "direct_commit"
```

---

## Commercial License Resolution

GitLab Fleet Governor is licensed under the Business Source License 1.1 (BSL 1.1):
- **Free Community Tier**: Governs up to 25 production projects/repositories with zero license key required. Unlimited in local development, staging, test environments, and non-mutating `--dry-run` modes.
- **Commercial Enterprise Tier**: Required when actively governing >25 production repositories.

### Token Resolution

License tokens are resolved in the following priority order:

1. **CLI Flag**: `--license-key="<token>"`
2. **CLI File Flag**: `--license-file="/path/to/token"`
3. **Policy Setting**: `settings.license.key` or `settings.license.file` in policy YAML/JSON
4. **Environment Variable**: `DIVMORA_LICENSE_KEY`
5. **Environment File**: `DIVMORA_LICENSE_FILE`
6. **Default System Path**: `/etc/divmora/license.key`

### Token Formats & Multi-Product Licensing

DIVMORA commercial licenses can be provided as either:
- **Canonical Compact Token**: `DIV1.<payload>.<signature>`
- **Armored PEM Block**: `-----BEGIN DIVMORA LICENSE KEY----- ... -----END DIVMORA LICENSE KEY-----`

Tokens can be scoped to specific products:
- `gitlab-fleet-governor`: Scoped strictly to GitLab Fleet Governor.
- `*` or `divmora-suite`: Enterprise master token valid across all DIVMORA products.

A single unified environment variable (`DIVMORA_LICENSE_KEY`) or system file (`/etc/divmora/license.key`) can be deployed across your infrastructure, and each tool cryptographically verifies that the token's product claim allows execution.

### Certificate Revocation List (CRL) Resolution

GitLab Fleet Governor supports offline Certificate Revocation Lists (`.divcrl`) as well as dynamic HTTPS CRL synchronization signed by the DIVMORA root key to immediately invalidate compromised, refunded, or superseded commercial licenses:

1. **CLI Flags**:
   - `--license-crl="<crl-token-or-pem>"`: Inline compact token or PEM block.
   - `--license-crl-file="/path/to/crl.divcrl"`: Offline filesystem path.
   - `--license-crl-url="https://crl.divmora.com/..."`: Remote HTTPS distribution point with ETag caching.
2. **Policy Settings**: `settings.license.crl`, `settings.license.crl_file`, or `settings.license.crl_url` in policy YAML/JSON
3. **Environment Variables**: `DIVMORA_CRL`, `DIVMORA_CRL_FILE`, or `DIVMORA_CRL_URL`
4. **Default System Path**: `/etc/divmora/crl.divcrl`

When an active commercial license matches an entry in the resolved CRL, operations halt with an actionable `COMMERCIAL LICENSE REVOKED` error identifying the revoked license ID, revocation timestamp, reason, and CRL ID.

### Managing Commercial Licenses (`license-cli`)

DIVMORA provides the centralized `license-cli` utility from `github.com/divmora/license-go` for operators, platform teams, and enterprise administrators.

#### Installation

Install the official multi-product CLI via Go:

```bash
go install github.com/divmora/license-go/cmd/license-cli@v1.3.1
```

#### Operator Workflows

- **Inspecting Licenses**:
  Decode and verify cryptographic claims, expiry dates, authorized accounts, and product scopes:
  ```bash
  license-cli inspect -license /path/to/license.key
  ```

- **Viewing Live Status Card & Fleet Quota**:
  Display the standardized status banner and evaluate current fleet metrics against token limits:
  ```bash
  license-cli status -license /path/to/license.key -usage "max_projects=42"
  ```

- **Air-Gapped Offline License Requests (`.divreq`)**:
  Generate cryptographically bound air-gapped license requests for secure nodes or VPCs without direct outbound access:
  ```bash
  license-cli request -product "gitlab-fleet-governor" -customer "Acme Corp" -out ./node.divreq
  ```

- **Native CLI Commands**:
  GitLab Fleet Governor also provides built-in inspection commands:
  ```bash
  # Display formatted terminal status card
  gitlab-fleet-governor license status

  # Output structured JSON for monitoring and alerts
  gitlab-fleet-governor license status --json

  # Headless compliance check for automated scripts and CI/CD pipelines
  gitlab-fleet-governor license check
  ```



