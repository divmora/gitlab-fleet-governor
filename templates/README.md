# Compliance policy templates

Ready-made starter policies for common compliance frameworks. Each one is a complete, valid policy that you fill in with `gitlab-fleet-governor init`, preview in dry-run, and then enforce and audit.

> [!IMPORTANT]
> This template is a **starting point**, not a certification. It configures GitLab controls that **support evidence** for each framework. Whether your organisation meets a control is decided by your own policies, processes and your auditor or assessor. Review every setting with your compliance team before enforcing it.

| Template | Framework | Highlights |
|---|---|---|
| [`soc2`](soc2/README.md) | SOC 2 Trust Services Criteria (2017, revised points of focus 2022) | 2 approvals, 180-day access expiry, 365-day tokens |
| [`iso-27001`](iso-27001/README.md) | ISO/IEC 27001:2022 Annex A | Annex A access and change controls, 180-day access expiry |
| [`pci-dss`](pci-dss/README.md) | PCI DSS v4.0 | Signed commits, job token allowlist, 90-day tokens, 12-month evidence |
| [`hipaa`](hipaa/README.md) | HIPAA Security Rule (45 CFR §164.308 – §164.312) | Signed commits, 90-day access expiry, private forks only |
| [`nist-800-53`](nist-800-53/README.md) | NIST SP 800-53 Rev. 5 | Signed and DCO commits, 90-day access and tokens |
| [`fedramp`](fedramp/README.md) | FedRAMP Moderate baseline (NIST SP 800-53 Rev. 5) | NIST profile with forking disabled and 60-day tokens |

## Quick start

```bash
gitlab-fleet-governor init --list
gitlab-fleet-governor init --template soc2 --group acme/platform --email-domain acme.com --reviewer-group acme/security -o policy.yaml
gitlab-fleet-governor run -c policy.yaml
```

`init` fills in the `REPLACE_WITH_*` placeholders, validates the result and refuses to overwrite an existing file unless you pass `--force`. Placeholders you don't fill are listed so you can edit them by hand. The templates are built into the binary, so `init` works without a copy of this repository.

Every template is validated in CI, so they always match the current policy schema.
