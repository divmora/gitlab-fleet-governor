# Security Policy & Responsible Disclosure

The GitLab Fleet Governor project team takes the security of our software and users seriously.

---

## Supported Versions

We support the current minor version and the preceding minor version with security patches:

| Version  | Supported          |
| -------- | ------------------ |
| `0.1.x`  | :white_check_mark: |
| `< 0.1`  | :x:                |

---

## Reporting a Vulnerability

If you discover a security vulnerability in GitLab Fleet Governor, please **do not open a public issue**. Instead, report it privately:

1. **Email**: Send detailed vulnerability information to `security@divmora.com`.
2. **GitHub Security Advisory**: Open a private draft security advisory at [github.com/divmora/gitlab-fleet-governor/security/advisories/new](https://github.com/divmora/gitlab-fleet-governor/security/advisories/new).

Please include:
- A description of the vulnerability and its potential impact.
- Steps to reproduce the issue (proof-of-concept configuration or code snippet).
- Any proposed remediation or patch.

---

## Response Timeline

- **Initial Acknowledgment**: Within 48 hours.
- **Vulnerability Assessment & Triage**: Within 5 business days.
- **Remediation & Advisory Release**: Coordinated with the reporter before public disclosure.

---

## Security Best Practices for Users

1. **Token Principle of Least Privilege**: When running `gitlab-fleet-governor` in CI/CD or Lambda, use dedicated service accounts or Group Access Tokens with strictly scoped permissions.
2. **Dry-Run by Default**: Always preview planned changes with `--dry-run` or in pull request pipelines before running in mutating enforcement mode.
3. **Secret Masking**: Do not commit plaintext secrets into policy YAML files. Use environment variable interpolation (`${VAR}`) or AWS Secrets Manager.
