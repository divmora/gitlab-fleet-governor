# ISO/IEC 27001:2022 starter policy

Access control, secure development and change management for an ISO/IEC 27001:2022 information security management system.

> [!IMPORTANT]
> This template is a **starting point**, not a certification. It configures GitLab controls that **support evidence** for the framework below. Whether your organisation meets a control is decided by your own policies, processes and your auditor or assessor. Review every setting with your compliance team before enforcing it.

## Use it

```bash
gitlab-fleet-governor init --template iso-27001 \
  --group acme/platform --email-domain acme.com --reviewer-group acme/security \
  -o policy.yaml
gitlab-fleet-governor validate -c policy.yaml
gitlab-fleet-governor run -c policy.yaml            # dry-run preview of the gaps
gitlab-fleet-governor audit -c policy.yaml -o audit.xlsx
```

| Placeholder | `init` flag | Meaning |
|---|---|---|
| `REPLACE_WITH_GROUP_PATH` | `--group` | Top-level group to govern (also allow-listed for CI job tokens) |
| `REPLACE_WITH_EMAIL_DOMAIN` | `--email-domain` | Corporate commit email domain, e.g. `acme.com` |
| `REPLACE_WITH_REVIEWER_GROUP` | `--reviewer-group` | Group whose members approve merge requests |

**Requirements.**
- **Licence:** dry-run previews are free. Enforcing (`--dry-run=false`) and `audit` need a **Pro** licence for `group_settings`, `approval_rules`, `protected_tags`, `pipeline_retention`, `job_token_scope`, `deploy_keys` and `deploy_tokens`, and an **Enterprise** licence for `compliance` and for audits beyond 25 projects. See [plans](../../docs/plans.md).
- **GitLab:** `membership_lock`, `prevent_forking_outside_group` and code owner approval need GitLab Premium, and `secret_push_protection` needs Ultimate.
- **Compliance framework:** the `compliance` block assigns a compliance framework that must already exist in GitLab.

## Control mapping

How [`policy.yaml`](policy.yaml) settings relate to ISO/IEC 27001:2022 Annex A. A setting **supports evidence** for a control; it does not satisfy the control on its own.

| Control | Topic | Policy settings |
|---|---|---|
| A.5.15 | Access control | `group_settings`, `project_settings.visibility`, `protected_branches` |
| A.5.18 | Access rights | `members.enforce_expires_at`, `members.max_expiration_days`, `group_settings.request_access_enabled` |
| A.8.2 | Privileged access rights | `protected_branches[].allowed_to_merge`, `group_settings.project_creation_level`, `protected_tags` |
| A.8.4 | Access to source code | `project_settings.visibility`, `project_settings.forking_access_level`, `deploy_keys`, `deploy_tokens` |
| A.8.5 | Secure authentication | `group_settings.require_two_factor_authentication` |
| A.8.9 | Configuration management | the whole policy, applied and audited fleet-wide |
| A.8.15 | Logging | `pipeline_retention.retention_days`, `audit` reports |
| A.8.25 | Secure development life cycle | `project_settings.only_allow_merge_if_pipeline_succeeds`, `push_rules.prevent_secrets` |
| A.8.32 | Change management | `approval_rules`, `protected_branches`, `project_settings.only_allow_merge_if_all_discussions_are_resolved` |

## Customise

- Start in dry-run and read the `run` and `audit` reports before enforcing.
- Adjust `members.max_expiration_days`, `deploy_tokens.max_expiration_days` and `pipeline_retention.retention_days` to your own access-review and retention policies.
- Use [per-instance overrides](../../docs/configuration.md#how-overrides-merge) and `gitlab-fleet-governor config render` if different groups need different values.
