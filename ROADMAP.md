# GitLab Fleet Governor Product Roadmap

This document serves as the **living product roadmap** for GitLab Fleet Governor.
- **Adding Items**: Whenever a new capability, enhancement, or edge-case improvement is identified for the future, add it here under the appropriate category.
- **Removing Items**: Once a feature is fully implemented, verified, and committed, **remove it from this roadmap**.

---

## 1. Fleet Governance & Additional Reconcilers

- [ ] **Group-Level Push Rules Reconciler**
  - Extend push rule governance beyond individual projects to inspect and reconcile group-level push rules (`/groups/:id/push_rule`).
  - Allows top-level group owners to enforce organization-wide commit rules (author email regexes, branch naming, secret prevention) with automatic inheritance across all child subgroups and repositories.

- [ ] **Security Policy & Pipeline Scan Reconciler**
  - Enforce GitLab Security Policies (Scan Execution Policies & Scan Result Policies) to ensure all fleet projects maintain mandatory SAST, Container Scanning, Dependency Scanning, and Secret Detection pipelines.
  - Automatically associate compliant projects with centralized security policy project repositories.

- [ ] **Deploy Keys & Deploy Tokens Governance**
  - Audit and manage project/group deploy keys and deploy tokens.
  - Flag expired keys, enforce write-access restrictions, and automatically revoke unmanaged or non-compliant credentials across the fleet.

- [ ] **Standard CI/CD Component Catalog & Template Baseline**
  - Enforce mandatory `.gitlab-ci.yml` template inclusion or compliance pipeline execution across repositories matching specific compliance tier selectors.

- [ ] **Protected Tags Reconciler (`protected_tags`)**
  - Declaratively enforce tag protection tiers (`v*`, `release-*`, `production-*`) across fleet repositories.
  - Configure creation and update access levels (`allowed_to_create`) to prevent unauthorized tag overwrites or deletion of release artifacts.

- [ ] **Standard Repository File & `CODEOWNERS` Sync Reconciler**
  - Declaratively enforce and synchronize standardized files across repositories (e.g., `CODEOWNERS` with required security team reviewers, `SECURITY.md`, `.editorconfig`, or baseline `.gitlab-ci.yml` includes).
  - Detect file content drift and automatically stage compliant updates via direct commits or automated Merge Requests.

- [ ] **Container & Package Registry Cleanup Policy Reconciler**
  - Enforce automated tag expiration and retention rules on project container registries (`cadence`, `keep_n`, `older_than`, `name_regex_delete`, `name_regex_keep`).
  - Purge orphaned or untagged container images and manage package registry retention to eliminate storage waste and control cloud hosting costs.

- [ ] **Protected Environments & Deployment Approvals Reconciler (`protected_environments`)**
  - Declaratively govern GitLab `environments` across fleet repositories (e.g., `production`, `staging`, `dr-*`).
  - Enforce protected environment access tiers, required deployment approvers (`required_approval_count`, designated groups or users), and deployment freeze schedules.

- [ ] **Stale & Zombie Repository Lifecycle Reconciler**
  - Automatically detect inactive, abandoned, or stale repositories based on configurable inactivity thresholds (e.g., no commits or pipeline runs for >180 days).
  - Declaratively enforce lifecycle transition actions: apply archived flags, mark project as read-only, transfer into an `archive/` namespace, or dispatch review notifications to repository owners.

- [ ] **Stale & Merged / Unmerged Branch Lifecycle & Pruning Reconciler (`branch_pruning`)**
  - Declaratively scan, audit, and clean up abandoned and stale Git branches across fleet repositories.
  - Supports configurable inactivity age thresholds (e.g., `older_than_days: 30`, `older_than_days: 90`).
  - Differentiates between merged branches (`merged: true`) and unmerged branches (`unmerged: true`) with independent retention policies and safety thresholds.
  - Built-in safety rails: strictly exempts protected branches (`protected: true`), default branches (`main`/`master`), active release patterns, and branches associated with open Merge Requests.
  - Supports dry-run simulation reporting and configurable lifecycle actions (audit warning, automated deletion via `DELETE /projects/:id/repository/branches/:branch`, or tag archiving prior to deletion).

---

## 2. Performance, Discovery & Fleet Onboarding

- [ ] **Fleet State Reverse-Sync (`export` / `import`)**
  - Add a `gitlab-fleet-governor export` CLI command to inspect current live configuration across a target group or project hierarchy and emit a normalized declarative `policy.yaml` baseline.
  - Enables zero-touch onboarding for existing enterprise fleets by capturing actual fleet state as code (similar to `terraform import`).

- [ ] **GraphQL-Based Bulk Fleet Discovery**
  - Implement an alternative discovery provider utilizing GitLab's GraphQL API (`group.projects(includeSubgroups: true)`).
  - Batch query group hierarchies and project configurations in a single network round-trip, significantly accelerating discovery in fleets with >10,000 repositories.

- [ ] **Multi-Tenant & Multi-Instance Fleet Governance**
  - Support configuration blocks spanning multiple independent GitLab instances (e.g., hybrid environments managing both GitLab SaaS `gitlab.com` and private Self-Managed GitLab instances within a single policy manifest).

---

## 3. Real-Time Event-Driven Automation

- [ ] **GitLab System Hook & Webhook Receiver Daemon**
  - Add an event-driven HTTP server daemon / AWS Lambda endpoint listening for GitLab System Hooks (`project_create`, `project_transfer`, `group_create`, `user_add_to_team`).
  - Trigger instantaneous incremental drift reconciliation within seconds of entity creation or mutation, eliminating drift latency between scheduled runs.

- [ ] **Notification & Alert Dispatcher (Slack / Teams / SNS)**
  - Add native notification dispatchers to publish real-time drift alerts, reconciliation summaries, and compliance violation notices directly to Slack Incoming Webhooks, Microsoft Teams, or AWS SNS topics.

---

## 4. Enterprise Security & Secret Stores

- [ ] **Direct Cloud Secret Manager Resolution (Zero-Env Seeding)**
  - Support native URI-based secret resolution directly within policy YAML definitions (`aws-secrets://...`, `vault://...`, `gcp-secrets://...`, `azure-keyvault://...`) for CI/CD variables and webhook token provisioning.
  - Leverages passwordless cloud identity (AWS IAM Roles/IRSA, Vault AppRole/Kubernetes Auth, GCP Workload Identity, Azure Managed Identity).
  - Features just-in-time (JIT) lazy fetching, in-memory deduplication caching, zero-leak diff masking, and cryptographic memory zeroization (`memzero`) after mutation completion.

---

## 5. Compliance, Audit Trails & Standards

- [ ] **SARIF & CycloneDX Compliance Export**
  - Add `--report-format sarif` to output non-compliance findings (e.g., over-privileged maintainers, missing branch protections, unmasked secrets) in OASIS SARIF format for ingestion into GitLab Security Dashboards and GitHub Advanced Security.

- [ ] **Open Policy Agent (OPA) / Rego Constraint Engine**
  - Embed an in-process OPA/Rego evaluation engine allowing security and platform teams to author expressive, fine-grained declarative governance rules against GitLab fleet state graphs.

---

## 6. Developer Experience & Tooling

- [ ] **Interactive Terminal UI (TUI) Drift Dashboard**
  - Build an interactive terminal dashboard using Bubbletea (`gitlab-fleet-governor dashboard`) to visually explore discovered projects, view colorized side-by-side diffs, and selectively trigger reconciliations.

- [ ] **Kubernetes Operator (CRD) & Helm Chart Packaging**
  - Package a native Kubernetes Operator with Custom Resource Definitions (`GitLabFleetPolicy`) and a Helm chart to complement the static CronJob manifests in `deploy/kubernetes/`.

---

## 7. Commercial Licensing & Anti-Circumvention Roadmap

- [ ] **Layer 3: Cryptographic Release Attestation & Signed Release Metadata**
  - Distribute cryptographic release attestation manifests (`release.sig`) and SLSA Level 3 supply-chain provenance signed by DIVMORA's authoritative release key during GitHub Actions release workflows.
  - Embed the canonical release timestamp, git commit SHA, and semver tag within the signed release envelope.
  - The in-binary verification engine validates this signed release manifest offline; binaries claiming Apache 2.0 Change Date conversion without a valid DIVMORA cryptographic signature are treated as unverified and fall back to standard commercial BSL 1.1 enforcement.
  - Support Sigstore / Cosign keyless OIDC verification for container images and standalone binaries.

- [ ] **Layer 4: Enterprise Compliance Attestation & Anti-Circumvention Invariants**
  - Implement structured compliance attestation records for enterprise security audits (SOC 2 Type II, ISO 27001, FedRAMP, and internal IT governance) certifying fleet reconciliation occurred under valid commercial licensing.
  - Statutory BSL 1.1 Anti-Circumvention Protection: establish contractual and cryptographic invariants where intentional clock manipulation, build date spoofing, or signature bypassing constitutes a willful violation of the Business Source License 1.1 Additional Use Grant and circumvention of technological protection measures under applicable law.

---


