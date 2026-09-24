export interface DocItem {
  id: string;
  title: string;
  category: string;
  content: string;
}

export const DOCS_DATA: DocItem[] = [
  {
    id: 'overview',
    title: 'Overview & Architecture',
    category: 'Getting Started',
    content: `# GitLab Fleet Governor

GitLab Fleet Governor is a production-grade, declarative policy-as-code and governance automation engine in Go. It enables platform engineering and security teams to enforce unified standards across thousands of repositories and groups.

## Core Pillars
1. **Declarative Invariant Convergence**: Idempotent operations converge fleet state to your desired YAML/JSON configuration.
2. **Dry-Run Simulation First**: Simulates all changes before applying any live mutations.
3. **Transport Resilience**: Token-bucket proactive rate limiter and exponential backoff retry with jitter on HTTP 429 and 5xx.
4. **Zero-Trust Masking**: Secrets and credentials are automatically masked in all outputs.`,
  },
  {
    id: 'push-rules',
    title: 'Push Rules Governance',
    category: 'Operations',
    content: `# Push Rules Governance

Enforce commit and author constraints at both Project and Group levels.

## Key Attributes
- \`author_email_regex\`: Enforce corporate email domain (e.g. \`@corp\\.io$\`).
- \`branch_name_regex\`: Enforce standard branching conventions (\`^(main|develop|feat/.*)$\`).
- \`prevent_secrets\`: Scans and blocks secrets from being committed.
- \`reject_unsigned_commits\`: Requires GPG, SSH, or X.509 signatures on all commits.
- \`commit_committer_check\`: Ensures committer matches authenticated user.
- \`max_file_size\`: Caps maximum commit file size in MB.`,
  },
  {
    id: 'retention',
    title: 'Pipeline Retention & Cleanup',
    category: 'Operations',
    content: `# Pipeline Retention Policy

GitLab Fleet Governor configures GitLab's native automatic pipeline deletion setting (\`ci_delete_pipelines_in_seconds\`) without requiring slow manual loop deletions.

## Configuration
\`\`\`yaml
policies:
  pipeline_retention:
    retention_days: 30 # Converted automatically to 2,592,000 seconds
\`\`\`

When applied, GitLab automatically purges historical pipelines and ephemeral storage older than 30 days.`,
  },
  {
    id: 'target-branch-rules',
    title: 'Merge Request Target Branch Rules',
    category: 'Operations',
    content: `# Merge Request Target Branch Rules

Govern Merge Request branch routing rules across fleet repositories. Automatically directs Merge Requests from branches matching wildcard patterns to designated target branches.

## Configuration
\`\`\`yaml
policies:
  target_branch_rules:
    prune_unmanaged: true
    rules:
      - source_branch_pattern: "feat/*"
        target_branch_name: "staging"
      - source_branch_pattern: "hotfix/*"
        target_branch_name: "main"
      - source_branch_pattern: "*"
        target_branch_name: "staging"
\`\`\`

## Safety & Validation
- **GraphQL ID Specification**: Uses GitLab's GraphQL API (\`projectTargetBranchRuleCreate\`, \`projectTargetBranchRuleDestroy\`).
- **Conflict Prevention**: Rejects duplicate \`source_branch_pattern\` entries and self-targeting configurations (\`source == target\`).
- **Pruning**: When \`prune_unmanaged: true\`, purges legacy or out-of-spec routing rules.`,
  },
  {
    id: 'lambda',
    title: 'AWS Lambda & Serverless',
    category: 'Deployment',
    content: `# AWS Lambda & Serverless Deployment

GitLab Fleet Governor can be deployed as an AWS Lambda function triggered by:
- **EventBridge Schedules** (e.g., \`cron(0 2 * * ? *)\` nightly runs).
- **S3 ObjectCreated Events** (executing whenever a policy file is uploaded).
- **Direct JSON Invocations**.

The binary automatically detects \`AWS_LAMBDA_FUNCTION_NAME\` and starts the serverless runtime handler.`,
  },
];
