# Configuration Reference

GitLab Fleet Governor uses a declarative schema supporting interchangeable **YAML** and **JSON** configurations.

---

## Top-Level Schema

```yaml
version: "v1"       # Required. Schema version: "v1"
settings: { ... }   # Global runtime and client parameters
targets: { ... }    # Fleet discovery and target selector rules
instances: [ ... ]  # Optional. Several GitLab instances in one run (Enterprise)
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

### `settings.notifications`

Sends a summary after `run`, `audit` and each Lambda run to Slack, Microsoft Teams, AWS SNS or any HTTPS endpoint (Pro, features `notify.slack`, `notify.teams`, `notify.sns`, `notify.webhook`).

```yaml
settings:
  notifications:
    slack:
      webhook_url: "${SLACK_WEBHOOK_URL}"   # incoming webhook
      channel: "#security-alerts"           # optional
      min_severity: warning                 # info | warning (default) | critical
    teams:
      webhook_url: "${TEAMS_WEBHOOK_URL}"   # Teams Workflows webhook (Adaptive Card)
      min_severity: critical
    sns:
      topic_arn: "arn:aws:sns:us-east-1:123456789012:governance-alerts"
    webhook:
      url: "${ALERT_WEBHOOK_URL}"           # receives the JSON summary
```

| Result | Severity |
|---|---|
| `run`: failed operation or run error | `critical` |
| `run`: drift (plan) or applied change | `warning` |
| `audit`: CRITICAL or HIGH finding | `critical` |
| `audit`: MEDIUM or LOW finding | `warning` |
| Nothing to report | `info` |

A channel is notified when the overall severity reaches its `min_severity`, so `info` means every run. Each message lists the counts and the five most severe items at or above the threshold. SNS and `webhook` receive the full JSON summary (`command`, `mode`, `severity`, `counts`, `items`, ...).

**Flags and environment.** `run` and `audit` also take `--notify-slack-webhook`, `--notify-slack-channel`, `--notify-teams-webhook`, `--notify-sns-topic-arn`, `--notify-webhook-url` and `--notify-min-severity`. Precedence is flag, then policy, then environment. The environment variables are `GLFG_SLACK_WEBHOOK_URL`, `GLFG_SLACK_CHANNEL`, `GLFG_TEAMS_WEBHOOK_URL`, `GLFG_SNS_TOPIC_ARN`, `GLFG_NOTIFY_WEBHOOK_URL` and `GLFG_NOTIFY_MIN_SEVERITY`. A `--notify-min-severity` flag applies to every channel.

> [!IMPORTANT]
> Webhook URLs are credentials. Keep them in the environment or in `${VAR}` references, not in the policy file or on the command line, where they end up in shell history and process listings.
> - Only `https://` URLs are accepted, redirects are not followed, and URLs are never logged or echoed in errors.
> - Each request times out after 5 seconds. A failed notification is logged as a warning and never changes the command's exit code.
> - For SNS, the AWS credentials need `sns:Publish` on the topic. The region is taken from the ARN.
> - On Lambda, notification targets come only from the function's `GLFG_*` environment variables. `settings.notifications` in any config is ignored there, and an event-supplied config that sets it is rejected (see [lambda.md](lambda.md)).

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

## `instances` Reference

A policy can govern several GitLab instances in one run, for example gitlab.com and a self-managed instance. Governing more than one instance requires an **Enterprise** licence (feature `platform.multi_instance`); without one, `plan` and dry-run still work and print a warning, and `apply` is refused. The same applies to `audit`, which runs in dry-run unless `settings.dry_run` is `false` or `--dry-run=false` is passed, like its other commercial features.

```yaml
version: "v1"
settings:
  gitlab:
    timeout_seconds: 60      # shared defaults for every instance
targets:                     # used by instances without their own targets
  group_selector:
    group_paths_include: ["platform"]
instances:
  - name: saas
    base_url: "https://gitlab.com/api/v4"
    token: "${GITLAB_SAAS_TOKEN}"
    targets:
      group_selector:
        group_paths_include: ["enterprise-fleet"]
  - name: self-managed
    base_url: "https://gitlab.internal.example.com/api/v4"
    token: "${GITLAB_SELF_MANAGED_TOKEN}"
    rate_limit_rps: 20
    policies:                # merged over the top-level policies
      push_rules:
        max_file_size: 50    # prevent_secrets: true is kept
      protected_branches:    # matched by name
        - name: main         # only this rule changes
          allowed_to_merge:
            - access_level: 40
        - name: develop      # not protected on this instance
          $remove: true
        - name: hotfix/*     # added on this instance
policies:                    # apply to every instance
  push_rules:
    prevent_secrets: true
    max_file_size: 10
  protected_branches:
    - name: main
      allowed_to_merge:
        - access_level: 30
    - name: develop
```

| Field | Description |
|---|---|
| `name` | Required. Lowercase letters, digits, `-` and `_`, up to 63 characters; unique. Shown in reports and logs. |
| `base_url` | The instance's REST API URL. Defaults to `https://gitlab.com/api/v4`. |
| `token` | Required. The instance's own token, usually `${VAR}`. |
| `token_type`, `timeout_seconds`, `rate_limit_rps`, `rate_limit_burst`, `max_retries`, `retry_base_delay_ms`, `retry_max_delay_ms` | As in `settings.gitlab`. Unset values inherit `settings.gitlab`, then the built-in defaults. Each instance has its own rate limiter and retries. |
| `targets` | Group and project selectors for this instance. When unset, the top-level `targets` apply. |
| `policies` | Overrides for this instance, merged over the top-level `policies` (see below). When unset, the top-level policies apply unchanged. |

Behaviour:

- **No environment fallbacks.** An instance's client uses only its own `base_url` and `token`. `GITLAB_BASE_URL`, `CI_API_V4_URL`, `GITLAB_TOKEN` and the other token variables are ignored, so one instance's credential is never sent to another. `settings.gitlab.token` can't be combined with `instances`, and `settings.gitlab.base_url` is not used.
- **One instance at a time.** Instances run in the order listed. If one fails, the rest still run, and the command exits with an error naming each failed instance.
- **One fleet for licensing.** Projects on every instance count towards the licence's project limit together, and the licence's host scope must cover each instance.
- **Reports.** Table and Markdown reports list each instance and show paths as `instance:path`. CSV gains a leading `instance` column, and JSON adds `instance` to each target and an `instances` summary. Single-instance reports are unchanged.
- **Policy overrides.** An instance's `policies` is merged over the top-level `policies` before the run. Use [`config render`](#checking-the-effective-policy) to see the result.
- **Other commands.** `audit` covers every instance in one report (see below). `export` and `merge` work on one instance at a time: pass `--instance <name>`.

#### Auditing several instances

`audit` audits each instance with its own client and writes one report:

- Every finding and user carries its instance. JSON adds an `instance` field and an `instances` summary, with each instance's URL, audit identity, project count, violation count and any error.
- CSV gains a leading `Instance` column, and the XLSX workbook adds an `Instance` column to each sheet plus a **GitLab Instances** sheet.
- Table, Markdown and HTML reports list the instances and show paths as `instance:path`.
- Projects are counted per instance, so project 42 on two instances is two projects, and the licence attestation covers the whole fleet.
- If an instance fails, the others are still audited and the report is still written, with the failure recorded. The command then exits with an error naming the failed instances.

### How overrides merge

| Override value | Effect |
|---|---|
| A mapping (e.g. `push_rules: {max_file_size: 50}`) | Merged field by field; fields the override doesn't mention are kept. |
| A list with a natural key (table below) | Merged item by item. An item whose key matches a top-level item is merged into it, an item with a new key is added, and an item with `$remove: true` drops the matching top-level item. |
| `{$replace: [...]}` on such a list | Replaces the top-level list as a whole. |
| Any other list or value | Replaces the top-level value. Lists inside a matched item, such as `allowed_to_merge`, replace too. |
| `null` | Removes the section or field on this instance, so it is no longer managed there. Nothing is deleted from GitLab. |

| List | Items matched by |
|---|---|
| `protected_branches` | `name` |
| `protected_tags` (or `protected_tags.rules`), `protected_environments` (or `.rules`) | `name` |
| `approval_rules.rules`, `target_branch_rules.rules` | `name` |
| `variables` | `key` and `environment_scope` (unset means `*`) |
| `webhooks` | `url` |
| `repository_files` | `path` |
| `branch_merges` | `source_branch` and `target_branch` |
| `runners.runners` | `id` |
| `members.allowed_members` | `username` |

The merged result is checked like the top-level policies. Unknown fields, invalid values, an item without its key, a duplicate key, a `$remove` that matches nothing, and a marker used anywhere else all fail at load with an `instances[N].policies…` error. Licence features are worked out from each instance's merged policies.

### Checking the effective policy

`config render` prints the policy each instance is governed with, after inheritance and overrides, without calling any API:

```bash
gitlab-fleet-governor config render -c policy.yaml
gitlab-fleet-governor config render -c policy.yaml --instance self-managed --format json
```

GitLab tokens, the licence key, webhook `secret_token` values and the values of masked variables are printed as `<redacted>`.

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

### 3. `protected_environments`

Declaratively govern deployment access tiers, required approval counts, and approver rules across fleet environments. Supports pruning unmanaged environments, wildcard pattern expansion (`dr-*`, `prod-*`), and automatic username and group path resolution:

```yaml
policies:
  protected_environments:
    prune: true # Optional: unprotect environments not declared in policy or matching wildcards
    rules:
      - name: "production"
        required_approval_count: 2
        deploy_access_levels:
          - access_level: 40 # 30=Developer, 40=Maintainer, 60=Admin
        approval_rules:
          - access_level: 40
            required_approvals: 2
            group_path: "security/appsec" # Resolves group path to group ID
      - name: "staging"
        required_approval_count: 1
        deploy_access_levels:
          - access_level: 30
      - name: "dr-*" # Wildcard expansion against project environments
        required_approval_count: 1
        deploy_access_levels:
          - access_level: 30
            username: "deploy-service-bot" # Resolves username to user ID
```

> [!TIP]
> **Backward Compatibility**: `protected_environments` also accepts a direct list of environment rules without the `prune` wrapper (e.g. `protected_environments: [{ name: "production", ... }]`).

### 4. `protected_tags`

Declaratively govern tag creation rules on release tags and Git version patterns across the fleet. Supports wildcard matching (`v*`, `release-*`), granular `allowed_to_create` tiers (No Access: 0, Developer: 30, Maintainer: 40, Owner: 50, Admin: 60), automatic username and group path resolution, and pruning unmanaged protected tags:

```yaml
policies:
  protected_tags:
    prune: true # Optional: unprotect tags not declared in policy
    rules:
      - name: "v*"
        allowed_to_create:
          - access_level: 40 # 40=Maintainer
      - name: "release-*"
        allowed_to_create:
          - access_level: 40
          - group_path: "release-managers" # Resolves group path to group ID
      - name: "*-hotfix"
        allowed_to_create:
          - access_level: 40
          - username: "release-bot" # Resolves username to user ID
```

> [!TIP]
> **Backward Compatibility**: `protected_tags` also accepts a direct list of tag rules without the `prune` wrapper (e.g. `protected_tags: [{ name: "v*", allowed_to_create: [{ access_level: 40 }] }]`).

### 5. `approval_rules`

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

### 5. `project_settings` & `pipeline_retention`

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
    # Security settings
    visibility: "private"                     # "private", "internal", "public"
    public_jobs: false                        # hide job logs/artifacts from non-members
    request_access_enabled: false
    forking_access_level: "private"           # "disabled", "private", "enabled"
    ci_forward_deployment_enabled: true       # skip outdated deployment jobs
    ci_push_repository_for_job_token_allowed: false
    ci_pipeline_variables_minimum_override_role: "maintainer"  # "no_one_allowed", "developer", "maintainer", "owner"
    ci_restrict_pipeline_cancellation_role: "maintainer"       # "developer", "maintainer", "no_one" (Premium)

  pipeline_retention:
    retention_days: 30 # Maps to ci_delete_pipelines_in_seconds = 2592000
```

### 6. `variables`

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

### 7. `runners`

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

### 8. `compliance`

```yaml
policies:
  compliance:
    framework_name: "SOC2"
    prune: false
```

### 9. `webhooks`

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

### 10. `members`

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

### 11. `target_branch_rules`

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
      - name: "*"
        target_branch: "main" # Universal catch-all must be placed at the end
```

#### Deterministic Evaluation Order & Shadowing Guardrails
- **Evaluation Order**: GitLab evaluates target branch rules top-down on a first-match basis. The declared sequence in YAML (`rules[0]`, `rules[1]`, ...) defines the exact evaluation priority (`rules[0]` matches first).
- **Universal Catch-All (`*`)**: Always place universal catch-alls or general fallback patterns at the very end of the `rules` array. Defining `*` before specific patterns triggers a validator lint warning because earlier catch-alls shadow all subsequent rules.
- **Order Drift Remediation**: GitLab stores target branch rules in reverse creation order (`ORDER BY created_at DESC, id DESC`). Fleet Governor detects order drift (`target_branch_rules:evaluation_order`) and automatically resequences existing rules by destroying and recreating them in reverse sequence (`rules[N-1]` down to `rules[0]`), ensuring `rules[0]` appears top-most in GitLab UI.


### 12. `repository_files`

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
      target_branch: "main" # Optional. Dynamically falls back to project default branch (e.g. main/master)
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
      # target_branch omitted: dynamically defaults to project.default_branch
      enforcement: "direct_commit"
```

> [!NOTE]
> - `target_branch` is optional. When omitted, it dynamically resolves to the target project's `default_branch` (falling back to `"main"` if unset).
> - Line endings (`\r\n` vs `\n`) and leading/trailing whitespace are automatically normalized during drift evaluation to prevent false-positive changes across platforms.
> - When using `direct_commit` on branches with push restrictions (`allowed_to_push: 0`), GitLab Fleet Governor catches the HTTP 403 Forbidden and advises switching to `merge_request` enforcement.


### 13. `group_settings`

Group-level security and membership settings, applied to every targeted group (Pro, feature `governance.group_settings`). Unset fields are left as they are, and only settings that differ from the policy are sent to GitLab.

```yaml
policies:
  group_settings:
    visibility: private                       # private | internal | public
    require_two_factor_authentication: true
    two_factor_grace_period: 48               # hours
    project_creation_level: maintainer        # noone | owner | maintainer | developer | administrator
    subgroup_creation_level: owner            # owner | maintainer
    share_with_group_lock: true
    membership_lock: true                     # Premium
    prevent_forking_outside_group: true       # Premium
    request_access_enabled: false
    emails_enabled: true
    mentions_disabled: false
    ip_restriction_ranges: ["10.0.0.0/8"]     # Premium; [] removes the restriction
    allowed_email_domains: ["example.com"]    # Premium; [] removes the restriction
    default_branch_protection:                # applied to new projects' default branch
      allowed_to_push: [40]                   # 0 no one, 30 developer, 40 maintainer, 60 admin
      allowed_to_merge: [40]
      allow_force_push: false
      developer_can_initial_push: false
      code_owner_approval_required: true      # Premium
```

> [!NOTE]
> - `ip_restriction_ranges` and `allowed_email_domains` are compared without regard to order or case.
> - GitLab refuses to make a group more restricted than its projects or subgroups, for example `visibility: private` on a group that has a public project. The run reports that group as failed with GitLab's error.
> - Premium-only settings fail with GitLab's error on Free instances.
> - Group settings are applied to every group the targets select, including subgroups when `recursive` is on.


### 14. `job_token_scope`

Which projects and groups may use their CI/CD job tokens to access each targeted project (Pro, feature `governance.job_token_scope`).

```yaml
policies:
  job_token_scope:
    limit_access: true                        # "Limit access to this project"
    allowed_projects: ["platform/deployer", "4242"]   # full path or numeric ID
    allowed_groups: ["platform/release"]
    prune: true                               # remove entries that aren't listed
```

> [!NOTE]
> - Unset `allowed_projects` or `allowed_groups` leaves that list unmanaged. Without `prune`, listed entries are added and nothing is removed.
> - The project itself is always on its own allowlist and is never removed.
> - Entries are added before `limit_access` is turned on, so a project the policy keeps is never locked out in between.
> - An entry that can't be resolved fails the project with `cannot resolve project "…"`.

### 15. `secret_push_protection`

Turns GitLab secret push protection on or off for each targeted project (Pro, feature `governance.secret_push_protection`; requires GitLab Ultimate).

```yaml
policies:
  secret_push_protection:
    enabled: true
```


### 16. `deploy_keys`

Deploy keys enabled on each targeted project (Pro, feature `governance.deploy_keys`).

```yaml
policies:
  deploy_keys:
    keys:
      - title: "release-bot"
        key: "ssh-ed25519 AAAAC3Nz... release-bot"   # needed to add the key where it's missing
        can_push: true
      - title: "ci-reader"
        fingerprint: "SHA256:3+l2zztZvcsCJ5rYMz9NXMsm1cv9mJooUXy6v1qjPY0"   # match without the key
    deny_push: true        # keys not listed become read-only
    max_age_days: 90       # report keys older than this for rotation
    prune: false           # true removes keys not listed from the project
```

| Situation | Effect |
|---|---|
| Listed key missing, `key` given | Added with `can_push` (default `false`) |
| Listed key missing, no `key` | Reported (`AUDIT`); add the public key to have it added |
| Listed key present with a different `can_push` | Updated |
| Key not listed, `prune: true` | Removed from the project |
| Key not listed, `deny_push: true` and it can push | Made read-only |
| Key older than `max_age_days` | Reported for rotation |

Keys are matched by SHA256 fingerprint when `key` or `fingerprint` is set (as shown by `ssh-keygen -lf`), otherwise by title (case-insensitive). Removing a deploy key only disables it for that project; keys shared with other projects stay there.

### 17. `deploy_tokens`

Rules every project and group deploy token must meet (Pro, feature `governance.deploy_tokens`). Tokens are never created: GitLab shows a token's secret only once.

```yaml
policies:
  deploy_tokens:
    allowed_scopes: ["read_repository", "read_registry"]
    require_expires_at: true
    max_expiration_days: 365
    revoke_expired: true         # otherwise expired tokens are only reported
    revoke_noncompliant: false   # true revokes tokens that break the rules above
```

Without the `revoke_*` options, every finding is reported as `AUDIT` and nothing is changed. Valid scopes: `read_repository`, `read_registry`, `write_registry`, `read_package_registry`, `write_package_registry`, `read_virtual_registry`, `write_virtual_registry`.

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

> [!TIP]
> For what the CRL request sends, egress proxies, internal mirrors, persistent caching and air-gapped installs, see [Network & License Connectivity](network.md).

### Managing Commercial Licenses (`license-cli`)

DIVMORA provides the centralized `license-cli` utility from `github.com/divmora/license-go` for operators, platform teams, and enterprise administrators.

#### Installation

Install the official multi-product CLI via Go:

```bash
go install github.com/divmora/license-go/cmd/license-cli@v1.4.3
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

  # Resolve machine and environment fingerprint for node-locked licensing
  gitlab-fleet-governor license fingerprint

  # Output only the primary fingerprint ID (useful for license provisioning and scripts)
  gitlab-fleet-governor license fingerprint -q
  ```



