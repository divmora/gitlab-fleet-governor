# PCI DSS v4.0 starter policy

Change control, least-privilege access and MFA for repositories that build software in or connected to a cardholder data environment (CDE).

> [!IMPORTANT]
> This template is a **starting point**, not a certification. It configures GitLab controls that **support evidence** for the framework below. Whether your organisation meets a control is decided by your own policies, processes and your auditor or assessor. Review every setting with your compliance team before enforcing it.

## Use it

```bash
gitlab-fleet-governor init --template pci-dss \
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

How [`policy.yaml`](policy.yaml) settings relate to PCI DSS v4.0. A setting **supports evidence** for a control; it does not satisfy the control on its own.

| Control | Topic | Policy settings |
|---|---|---|
| 6.2.3 / 6.2.4 | Bespoke software reviewed and protected against common attacks | `approval_rules`, `push_rules.prevent_secrets`, `project_settings.only_allow_merge_if_pipeline_succeeds` |
| 6.5.1 – 6.5.2 | Changes are managed and approved | `protected_branches`, `approval_rules.settings.allow_author_approval: false`, `protected_tags` |
| 7.2.1 – 7.2.2 | Access assigned by least privilege | `group_settings.project_creation_level`, `protected_branches[].allowed_to_merge`, `project_settings.forking_access_level` |
| 7.2.4 | User accounts and access reviewed at least every six months | `members.enforce_expires_at`, `members.max_expiration_days: 180` |
| 7.2.5 | System and application accounts restricted | `deploy_keys.deny_push`, `deploy_tokens.allowed_scopes`, `job_token_scope.limit_access` |
| 8.3.9 / 8.6.3 | Credentials for accounts are managed and rotated | `deploy_keys.max_age_days`, `deploy_tokens.max_expiration_days`, `deploy_tokens.revoke_expired` |
| 8.4.2 | MFA for all access into the CDE | `group_settings.require_two_factor_authentication` |
| 10.5.1 | Retain audit history for at least 12 months | `pipeline_retention.retention_days: 365`, `audit` reports |

## Customise

- Start in dry-run and read the `run` and `audit` reports before enforcing.
- Adjust `members.max_expiration_days`, `deploy_tokens.max_expiration_days` and `pipeline_retention.retention_days` to your own access-review and retention policies.
- Use [per-instance overrides](../../docs/configuration.md#how-overrides-merge) and `gitlab-fleet-governor config render` if different groups need different values.
