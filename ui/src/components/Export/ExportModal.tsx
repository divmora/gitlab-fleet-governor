import React, { useState } from 'react';
import { X, Copy } from 'lucide-react';

interface ExportModalProps {
  isOpen: boolean;
  onClose: () => void;
  rawYaml: string;
  rawJson: string;
}

export const ExportModal: React.FC<ExportModalProps> = ({
  isOpen,
  onClose,
  rawYaml,
  rawJson,
}) => {
  const [tab, setTab] = useState<'yaml' | 'json' | 'cli' | 'ci' | 'docker'>('yaml');
  const [copied, setCopied] = useState(false);

  if (!isOpen) return null;

  const cliSnippet = `gitlab-fleet-governor run -c fleet-policy.yaml --dry-run`;

  const ciSnippet = `fleet-governance-audit:
  stage: test
  image: ghcr.io/divmora/gitlab-fleet-governor:latest
  variables:
    GITLAB_TOKEN: $GITLAB_FLEET_TOKEN
    CI_API_V4_URL: $CI_API_V4_URL
  script:
    - gitlab-fleet-governor run -c policy.yaml --dry-run=false --report-format=markdown > drift-report.md
  artifacts:
    reports:
      dotenv: drift.env
    paths:
      - drift-report.md`;

  const dockerSnippet = `docker run --rm -v $(pwd)/fleet-policy.yaml:/app/policy.yaml \\
  -e GITLAB_TOKEN="\${GITLAB_TOKEN}" \\
  ghcr.io/divmora/gitlab-fleet-governor:latest run -c /app/policy.yaml --dry-run`;

  const getContent = () => {
    switch (tab) {
      case 'yaml':
        return rawYaml;
      case 'json':
        return rawJson;
      case 'cli':
        return cliSnippet;
      case 'ci':
        return ciSnippet;
      case 'docker':
        return dockerSnippet;
      default:
        return rawYaml;
    }
  };

  const handleCopy = () => {
    navigator.clipboard.writeText(getContent());
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm">
      <div className="w-full max-w-2xl rounded-2xl bg-slate-900 border border-slate-800 shadow-2xl overflow-hidden flex flex-col max-h-[85vh]">
        {/* Modal Header */}
        <div className="px-6 py-4 bg-slate-950 border-b border-slate-800 flex items-center justify-between">
          <h3 className="font-bold text-white text-base">Export Policy & Automation Artefacts</h3>
          <button
            onClick={onClose}
            className="p-1 rounded-lg hover:bg-slate-800 text-slate-400 hover:text-white transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Tab Switcher */}
        <div className="px-6 pt-4 flex items-center gap-2 border-b border-slate-800 pb-3">
          {[
            { id: 'yaml', label: 'YAML Policy' },
            { id: 'json', label: 'JSON Policy' },
            { id: 'cli', label: 'CLI Command' },
            { id: 'ci', label: '.gitlab-ci.yml' },
            { id: 'docker', label: 'Docker Run' },
          ].map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id as any)}
              className={`px-3 py-1.5 rounded-lg text-xs font-semibold transition-all ${
                tab === t.id
                  ? 'bg-indigo-600 text-white shadow-sm'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>

        {/* Snippet Content */}
        <div className="p-6 flex-1 overflow-y-auto">
          <pre className="p-4 rounded-xl bg-slate-950 text-indigo-300 text-xs font-mono border border-slate-800 overflow-x-auto leading-relaxed whitespace-pre-wrap">
            {getContent()}
          </pre>
        </div>

        {/* Footer Actions */}
        <div className="px-6 py-4 bg-slate-950 border-t border-slate-800 flex items-center justify-end gap-3">
          <button
            onClick={handleCopy}
            className="px-4 py-2 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-semibold shadow-md transition-all flex items-center gap-1.5"
          >
            <Copy className="w-3.5 h-3.5" />
            <span>{copied ? 'Copied to Clipboard!' : 'Copy Snippet'}</span>
          </button>
        </div>
      </div>
    </div>
  );
};
