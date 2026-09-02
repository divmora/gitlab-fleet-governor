# 🤖 LLM & AI Agent Integration

GitLab Fleet Governor is engineered with first-class support for Large Language Models (LLMs) and autonomous AI coding agents (such as Claude 3.7 / 3.5 Sonnet, GPT-4o, Cursor, GitHub Copilot, and custom autonomous agents).

---

## 📄 Standard LLM Endpoints

We expose standardized machine-readable documentation endpoints following the [`llms.txt` specification](https://llmstxt.org/):

| Endpoint | Purpose | Link |
|---|---|---|
| **`/llms.txt`** | Compact, structured index of documentation and schema overview. | [`/llms.txt`](llms.txt) |
| **`/llms-full.txt`** | Full consolidated documentation, policy schemas, CLI specifications, and operational matrices. | [`/llms-full.txt`](llms-full.txt) |
| **`schema.json`** | Formal JSON Schema for IDE validation, code completion, and agent AST verification. | [`schema.json`](schema.json) |

---

## 🛠️ Configuring AI Tools & IDEs

### 1. Cursor & Claude Projects
To empower Cursor or Claude with full domain knowledge of GitLab Fleet Governor, point the system context or `.cursorrules` to the `llms-full.txt` endpoint:

```markdown
# Add to .cursorrules or System Prompt:
Always refer to the GitLab Fleet Governor schema and guidelines at:
https://divmora.github.io/gitlab-fleet-governor/llms-full.txt
```

### 2. IDE JSON/YAML Schema Association
In VS Code, IntelliJ, or Neovim, add the `$schema` directive at the top of your YAML files for instant inline validation and auto-completion:

```yaml
# yaml-language-server: $schema=https://divmora.github.io/gitlab-fleet-governor/schema.json
version: "v1"
settings:
  dry_run: true
targets:
  group_selector:
    group_paths_include: ["enterprise/core"]
```

---

## 💡 AI Prompt Library

Copy and paste these battle-tested prompts directly into your AI assistant:

### Prompt 1: Enterprise SOC 2 Baseline Generator
```text
You are a GitLab Governance Architect. Using the GitLab Fleet Governor YAML schema (https://divmora.github.io/gitlab-fleet-governor/llms-full.txt), generate a strict SOC 2 Type II compliance policy for the group hierarchy 'fintech-core'.

Requirements:
1. Target all non-archived projects in 'fintech-core' recursively.
2. Push Rules: Require corporate emails (@corp-fintech.io), prevent secrets, reject unsigned commits, and deny delete tag.
3. Protected Branches: Protect 'main' with push access = 0 (No one), merge access = 40 (Maintainer), and enable code_owner_approval_required.
4. Approval Rules: 2 required approvals on 'main' from security lead 'appsec-lead', disallow author approval, disallow committer approval.
5. Project Settings: squash_option = 'always', merge_method = 'rebase_merge', only_allow_merge_if_pipeline_succeeds = true.
6. Pipeline Retention: Automatically clean up old pipelines after 90 days.
7. Compliance: Assign framework 'SOC2'.
```

### Prompt 2: Ephemeral CI/CD Cleanup Policy
```text
Generate a GitLab Fleet Governor YAML policy for a fleet of ephemeral CI test groups located in 'testing/ephemeral-fleets'.

Requirements:
1. Set concurrency to 20 workers and dry_run: true.
2. Configure pipeline_retention to 7 days (converted automatically to ci_delete_pipelines_in_seconds).
3. Project Settings: keep_latest_artifact: true.
4. Project Selector: Match only projects with name regex '^e2e-.*$' and exclude archived repositories.
```

### Prompt 3: Drift Remediation & Policy Diff Analyzer
```text
I ran 'gitlab-fleet-governor run -c policy.yaml --dry-run --report-format json' and received the following execution report:
[PASTE JSON OUTPUT HERE]

Analyze the report:
1. Identify which repositories have drifted from the desired compliance baseline.
2. Group the root causes by category (Push Rules, Branch Protections, Approvals, Pipeline Retention).
3. Provide the exact mutating CLI command and risk evaluation for applying live updates.
```
