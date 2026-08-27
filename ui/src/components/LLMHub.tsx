import React, { useState } from 'react';
import { Bot, Copy, FileCode, Sparkles, ExternalLink } from 'lucide-react';

export const LLMHub: React.FC = () => {
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const prompts = [
    {
      id: 'soc2',
      title: 'Enterprise SOC 2 Baseline Policy Prompt',
      prompt: `You are a GitLab Fleet Governance Architect. Using the official GitLab Fleet Governor YAML schema (https://divmora.github.io/gitlab-fleet-governor/llms-full.txt), generate a production-ready SOC 2 Type II compliance policy for group 'enterprise-core'.

Requirements:
1. Target all non-archived projects in 'enterprise-core' recursively.
2. Push Rules: Require corporate emails (@corp.io), prevent secrets, reject unsigned commits, and deny delete tag.
3. Protected Branches: Protect 'main' with push=0 (No one), merge=40 (Maintainer), and code owner approval required.
4. Approval Rules: 2 required approvals on 'main' from 'appsec-team', disallow author & committer approvals.
5. Project Settings: squash_option = 'always', merge_method = 'rebase_merge', only_allow_merge_if_pipeline_succeeds = true.
6. Pipeline Retention: Automatically clean up old pipelines after 90 days.
7. Compliance: Assign framework 'SOC2'.`,
    },
    {
      id: 'ephemeral',
      title: 'Ephemeral CI/CD Pipeline Cleanup Prompt',
      prompt: `Generate a GitLab Fleet Governor policy YAML for a fleet of ephemeral CI test groups in 'testing/ephemeral'.

Requirements:
1. Concurrency: 25 workers, dry_run: true.
2. Configure pipeline_retention to 7 days (converted to native ci_delete_pipelines_in_seconds).
3. Project Settings: keep_latest_artifact: true.
4. Match only projects matching regex '^e2e-.*$' and exclude archived repositories.`,
    },
    {
      id: 'cursorrules',
      title: '.cursorrules / System Prompt Setup',
      prompt: `# Add this instruction to your .cursorrules, Claude Projects, or ChatGPT custom GPT:

Always adhere to the declarative GitLab Fleet Governor policy schema and architectural guidelines defined at:
https://divmora.github.io/gitlab-fleet-governor/llms-full.txt

When writing policies:
- Version is always 'v1'.
- Use native pipeline_retention.retention_days instead of manual loop deletions.
- Prefer group-level BFS selectors (recursive: true).`,
    },
  ];

  const handleCopy = (id: string, text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  return (
    <div className="space-y-8">
      {/* Header */}
      <div className="p-6 rounded-2xl bg-gradient-to-r from-cyan-950/40 via-slate-900 to-slate-900 border border-cyan-500/20 shadow-xl">
        <div className="flex items-center gap-2.5 text-base font-bold text-white mb-2">
          <div className="w-8 h-8 rounded-xl bg-cyan-500/20 text-cyan-400 flex items-center justify-center">
            <Bot className="w-5 h-5" />
          </div>
          <h2 className="text-xl font-bold text-white tracking-tight">Large Language Model & Agent Integration</h2>
        </div>
        <p className="text-xs sm:text-sm text-slate-400 max-w-3xl leading-relaxed">
          GitLab Fleet Governor exposes standardized AI endpoints (<code className="text-cyan-300">/llms.txt</code>, <code className="text-cyan-300">/llms-full.txt</code>, and <code className="text-cyan-300">/schema.json</code>) and is indexed on DeepWiki for seamless integration with Cursor, Claude, ChatGPT, and autonomous coding agents.
        </p>
      </div>

      {/* Manifest & AI Wiki Endpoints */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <a
          href="https://deepwiki.com/divmora/gitlab-fleet-governor"
          target="_blank"
          rel="noopener noreferrer"
          className="p-5 rounded-2xl bg-gradient-to-br from-indigo-950/40 to-slate-900/80 border border-indigo-500/30 hover:border-indigo-400 transition-all shadow-lg group flex flex-col justify-between"
        >
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="font-bold text-white text-sm group-hover:text-indigo-300 transition-colors">Ask DeepWiki</span>
              <ExternalLink className="w-4 h-4 text-indigo-400 group-hover:translate-x-0.5 transition-transform" />
            </div>
            <p className="text-xs text-slate-400 leading-relaxed mb-3">
              Ask questions directly to the AI Wiki knowledge base indexed on this repository.
            </p>
          </div>
          <div>
            <img src="https://deepwiki.com/badge.svg" alt="Ask DeepWiki" className="h-5 rounded" />
          </div>
        </a>

        <a
          href="llms.txt"
          target="_blank"
          rel="noopener noreferrer"
          className="p-5 rounded-2xl bg-slate-900/80 border border-slate-800 hover:border-cyan-500/40 transition-all shadow-lg group flex flex-col justify-between"
        >
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="font-bold text-white text-sm group-hover:text-cyan-400 transition-colors">/llms.txt</span>
              <FileCode className="w-4 h-4 text-cyan-400" />
            </div>
            <p className="text-xs text-slate-400 leading-relaxed">
              Standard curated AI sitemap and structured policy index per the llmstxt.org specification.
            </p>
          </div>
        </a>

        <a
          href="llms-full.txt"
          target="_blank"
          rel="noopener noreferrer"
          className="p-5 rounded-2xl bg-slate-900/80 border border-slate-800 hover:border-cyan-500/40 transition-all shadow-lg group flex flex-col justify-between"
        >
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="font-bold text-white text-sm group-hover:text-cyan-400 transition-colors">/llms-full.txt</span>
              <FileCode className="w-4 h-4 text-cyan-400" />
            </div>
            <p className="text-xs text-slate-400 leading-relaxed">
              Consolidated single-file markdown specification for 1-shot LLM context injection & RAG.
            </p>
          </div>
        </a>

        <a
          href="schema.json"
          target="_blank"
          rel="noopener noreferrer"
          className="p-5 rounded-2xl bg-slate-900/80 border border-slate-800 hover:border-cyan-500/40 transition-all shadow-lg group flex flex-col justify-between"
        >
          <div>
            <div className="flex items-center justify-between mb-2">
              <span className="font-bold text-white text-sm group-hover:text-cyan-400 transition-colors">/schema.json</span>
              <FileCode className="w-4 h-4 text-cyan-400" />
            </div>
            <p className="text-xs text-slate-400 leading-relaxed">
              Machine-readable JSON Schema for IDE validation ($schema) in VS Code, IntelliJ, and Cursor.
            </p>
          </div>
        </a>
      </div>

      {/* Prompt Library */}
      <div className="space-y-4">
        <h3 className="text-lg font-bold text-white flex items-center gap-2">
          <Sparkles className="w-4 h-4 text-indigo-400" />
          <span>AI Pair Programming Prompt Library</span>
        </h3>

        <div className="space-y-4">
          {prompts.map((p) => (
            <div key={p.id} className="p-6 rounded-2xl bg-slate-900/80 border border-slate-800 shadow-xl space-y-3">
              <div className="flex items-center justify-between">
                <h4 className="font-bold text-white text-sm">{p.title}</h4>
                <button
                  onClick={() => handleCopy(p.id, p.prompt)}
                  className="px-3 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold flex items-center gap-1.5 border border-slate-700 transition-all"
                >
                  <Copy className="w-3.5 h-3.5" />
                  <span>{copiedId === p.id ? 'Copied!' : 'Copy Prompt'}</span>
                </button>
              </div>
              <pre className="p-4 rounded-xl bg-slate-950 text-slate-300 text-xs font-mono whitespace-pre-wrap leading-relaxed border border-slate-800/80">
                {p.prompt}
              </pre>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};
