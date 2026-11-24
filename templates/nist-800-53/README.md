# NIST SP 800-53 Rev. 5 starter policy

Access control, configuration management and integrity controls drawn from NIST SP 800-53 Rev. 5.

> [!IMPORTANT]
> This template is a **starting point**, not a certification. It configures GitLab controls that **support evidence** for the framework below. Whether your organisation meets a control is decided by your own policies, processes and your auditor or assessor. Review every setting with your compliance team before enforcing it.

## Use it

```bash
gitlab-fleet-governor init --template nist-800-53 \
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

How [`policy.yaml`](policy.yaml) settings relate to NIST SP 800-53 Rev. 5. A setting **supports evidence** for a control; it does not satisfy the control on its own.

| Control | Topic | Policy settings |
|---|---|---|
| AC-2 | Account management | `members.enforce_expires_at`, `members.max_expiration_days`, `deploy_tokens.require_expires_at` |
| AC-3 | Access enforcement | `protected_branches`, `project_settings.visibility`, `job_token_scope.limit_access` |
| AC-6 | Least privilege | `group_settings.project_creation_level`, `deploy_keys.deny_push`, `deploy_tokens.allowed_scopes` |
| IA-2(1) | Multi-factor authentication for privileged accounts | `group_settings.require_two_factor_authentication` |
| CM-3 | Configuration change control | `approval_rules`, `project_settings.only_allow_merge_if_pipeline_succeeds` |
| CM-5 | Access restrictions for change | `protected_branches[].allowed_to_push: 0`, `protected_tags` |
| AU-11 | Audit record retention | `pipeline_retention.retention_days` |
| SA-10 | Developer configuration management | `protected_branches`, `approval_rules`, `push_rules.reject_non_dco_commits` |
| SI-7 | Software, firmware and information integrity | `push_rules.reject_unsigned_commits`, `push_rules.prevent_secrets` |

## Customise

- Start in dry-run and read the `run` and `audit` reports before enforcing.
- Adjust `members.max_expiration_days`, `deploy_tokens.max_expiration_days` and `pipeline_retention.retention_days` to your own access-review and retention policies.
- Use [per-instance overrides](../../docs/configuration.md#how-overrides-merge) and `gitlab-fleet-governor config render` if different groups need different values.
