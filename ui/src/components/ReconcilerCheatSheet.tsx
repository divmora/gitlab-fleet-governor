import React from 'react';
import { Shield } from 'lucide-react';

export const ReconcilerCheatSheet: React.FC = () => {
  const reconcilers = [
    {
      name: 'push_rules',
      scope: 'Project & Group',
      desc: 'Author email regex, branch name regex, commit messages, max file size, prevent secrets, signed commits, and committer checks.',
      yamlSnippet: `push_rules:
  author_email_regex: '@corp\\.com$'
  prevent_secrets: true
  reject_unsigned_commits: true`,
    },
    {
      name: 'protected_branches',
      scope: 'Project',
      desc: 'Branch protections, access tiers (0, 30, 40), force push prevention, code owner approvals.',
      yamlSnippet: `protected_branches:
  - name: "main"
    allowed_to_push: [{ access_level: 0 }]
    allowed_to_merge: [{ access_level: 40 }]`,
    },
    {
      name: 'approval_rules',
      scope: 'Project',
      desc: 'Merge request approval rules, named reviewer lists, author/committer approval overrides.',
      yamlSnippet: `approval_rules:
  settings:
    allow_author_approval: false
  rules:
    - name: "Security"
      approvals_required: 1`,
    },
    {
      name: 'project_settings',
      scope: 'Project',
      desc: 'Merge method (rebase_merge, merge, ff), squash options, unresolved discussion blocking, artifact retention.',
      yamlSnippet: `project_settings:
  squash_option: "always"
  merge_method: "rebase_merge"
  only_allow_merge_if_pipeline_succeeds: true`,
    },
    {
      name: 'pipeline_retention',
      scope: 'Project',
      desc: 'Native GitLab automated pipeline deletion (retention_days -> ci_delete_pipelines_in_seconds).',
      yamlSnippet: `pipeline_retention:
  retention_days: 30 # 2,592,000s`,
    },
    {
      name: 'variables',
      scope: 'Project & Group',
      desc: 'CI/CD variables, masked tokens, protected flags, raw values, environment scoping.',
      yamlSnippet: `variables:
  - key: "GLOBAL_API"
    value: "https://api.corp.io"
    protected: true`,
    },
    {
      name: 'runners',
      scope: 'Project & Group',
      desc: 'Shared/group runner controls, tag enforcement, pause/lock maintenance status.',
      yamlSnippet: `runners:
  - id: 1
    locked: true
    tag_list: ["docker", "linux-amd64"]`,
    },
    {
      name: 'compliance',
      scope: 'Project',
      desc: 'GraphQL compliance framework labels (SOC2, HIPAA, PCI-DSS, ISO27001).',
      yamlSnippet: `compliance:
  framework_name: "SOC2"`,
    },
    {
      name: 'webhooks',
      scope: 'Project',
      desc: 'Event triggers, HMAC secret tokens, SSL verification.',
      yamlSnippet: `webhooks:
  - url: "https://events.corp.io/audit"
    push_events: true
    secret_token: "\${TOKEN}"`,
    },
    {
      name: 'members',
      scope: 'Project & Group',
      desc: 'Least privilege role audit, mandatory expiration dates, denied member cleanup.',
      yamlSnippet: `members:
  max_access_level: 40
  enforce_expires_at: true`,
    },
  ];

  return (
    <div className="space-y-8">
      <div className="p-6 rounded-2xl bg-gradient-to-r from-slate-900 via-indigo-950/30 to-slate-900 border border-slate-800 shadow-xl">
        <div className="flex items-center gap-2.5 text-base font-bold text-white mb-2">
          <div className="w-8 h-8 rounded-xl bg-indigo-500/20 text-indigo-400 flex items-center justify-center">
            <Shield className="w-5 h-5" />
          </div>
          <h2 className="text-xl font-bold text-white tracking-tight">10 Core Governance Reconcilers</h2>
        </div>
        <p className="text-xs sm:text-sm text-slate-400 max-w-3xl leading-relaxed">
          Detailed specification of all 10 reconcilers implemented by the GitLab Fleet Governor engine.
        </p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {reconcilers.map((r) => (
          <div key={r.name} className="p-6 rounded-2xl bg-slate-900/80 border border-slate-800 shadow-xl flex flex-col justify-between">
            <div>
              <div className="flex items-center justify-between gap-2 mb-2">
                <h3 className="text-base font-bold text-white font-mono">{r.name}</h3>
                <span className="px-2.5 py-0.5 rounded-full text-[10px] font-semibold bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
                  {r.scope}
                </span>
              </div>
              <p className="text-xs text-slate-300 mb-4 leading-relaxed">{r.desc}</p>
            </div>
            <pre className="p-3.5 rounded-xl bg-slate-950 text-indigo-300 text-xs font-mono border border-slate-800/80 overflow-x-auto">
              {r.yamlSnippet}
            </pre>
          </div>
        ))}
      </div>
    </div>
  );
};
