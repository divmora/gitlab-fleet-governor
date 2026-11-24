# SOC 2 starter policy

Change management, logical access and monitoring controls for teams preparing for a SOC 2 Type II examination.

> [!IMPORTANT]
> This template is a **starting point**, not a certification. It configures GitLab controls that **support evidence** for the framework below. Whether your organisation meets a control is decided by your own policies, processes and your auditor or assessor. Review every setting with your compliance team before enforcing it.

## Use it

```bash
gitlab-fleet-governor init --template soc2 \
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

How [`policy.yaml`](policy.yaml) settings relate to SOC 2 Trust Services Criteria (2017, revised points of focus 2022). A setting **supports evidence** for a control; it does not satisfy the control on its own.

| Control | Topic | Policy settings |
|---|---|---|
| CC6.1 | Logical access security | `group_settings.require_two_factor_authentication`, `protected_branches`, `project_settings.visibility` |
| CC6.2 | User registration and authorization | `group_settings.request_access_enabled`, `project_settings.request_access_enabled`, `members.enforce_expires_at` |
| CC6.3 | Access modification and removal | `members.max_expiration_days`, `deploy_tokens.require_expires_at`, `deploy_tokens.revoke_expired` |
| CC6.6 | Boundary protection for external access | `job_token_scope` (PCI/HIPAA templates), `deploy_keys.deny_push`, `deploy_tokens.allowed_scopes` |
| CC6.8 | Prevent unauthorized or malicious software | `push_rules.prevent_secrets`, `push_rules.file_name_regex`, `push_rules.member_check` |
| CC7.1 | Detect configuration changes | `audit` reports, notifications (`settings.notifications`) |
| CC8.1 | Authorize, test and approve changes | `approval_rules`, `project_settings.only_allow_merge_if_pipeline_succeeds`, `protected_branches[].allowed_to_merge` |
| CC8.1 | Change evidence | `pipeline_retention.retention_days`, `protected_tags` |

## Customise

- Start in dry-run and read the `run` and `audit` reports before enforcing.
- Adjust `members.max_expiration_days`, `deploy_tokens.max_expiration_days` and `pipeline_retention.retention_days` to your own access-review and retention policies.
- Use [per-instance overrides](../../docs/configuration.md#how-overrides-merge) and `gitlab-fleet-governor config render` if different groups need different values.
