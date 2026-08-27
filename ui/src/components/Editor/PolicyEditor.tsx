import React, { useState } from 'react';
import { Copy, Download, Terminal, CheckCircle2, AlertCircle } from 'lucide-react';
import { ValidationResult } from '../../engine/validator';

interface PolicyEditorProps {
  code: string;
  format: 'yaml' | 'json';
  validation: ValidationResult;
  onChange: (newCode: string, format: 'yaml' | 'json') => void;
  onSwitchFormat: (format: 'yaml' | 'json') => void;
}

export const PolicyEditor: React.FC<PolicyEditorProps> = ({
  code,
  format,
  validation,
  onChange,
  onSwitchFormat,
}) => {
  const [copied, setCopied] = useState(false);
  const [cliCopied, setCliCopied] = useState(false);

  const handleCopyCode = () => {
    navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const handleCopyCli = () => {
    const ext = format;
    const cmd = `gitlab-fleet-governor run -c fleet-policy.${ext} --dry-run`;
    navigator.clipboard.writeText(cmd);
    setCliCopied(true);
    setTimeout(() => setCliCopied(false), 2000);
  };

  const handleDownload = () => {
    const ext = format;
    const blob = new Blob([code], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `fleet-policy.${ext}`;
    a.click();
  };

  return (
    <div className="flex flex-col h-[824px] rounded-2xl bg-slate-900/90 border border-slate-800 shadow-2xl overflow-hidden">
      {/* Editor Header */}
      <div className="px-6 py-3.5 bg-slate-950 border-b border-slate-800 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <div className="flex gap-1.5">
            <span className="w-2.5 h-2.5 rounded-full bg-rose-500/80"></span>
            <span className="w-2.5 h-2.5 rounded-full bg-amber-500/80"></span>
            <span className="w-2.5 h-2.5 rounded-full bg-emerald-500/80"></span>
          </div>
          <span className="ml-2 font-mono text-xs font-semibold text-slate-300">
            fleet-policy.{format}
          </span>
        </div>

        <div className="flex items-center gap-2">
          <div className="flex items-center gap-1 bg-slate-900 p-0.5 rounded-lg border border-slate-800">
            <button
              onClick={() => onSwitchFormat('yaml')}
              className={`px-3 py-1 rounded-md text-xs font-semibold transition-all ${
                format === 'yaml' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white'
              }`}
            >
              YAML
            </button>
            <button
              onClick={() => onSwitchFormat('json')}
              className={`px-3 py-1 rounded-md text-xs font-semibold transition-all ${
                format === 'json' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white'
              }`}
            >
              JSON
            </button>
          </div>
        </div>
      </div>

      {/* Code Textarea Area */}
      <div className="relative flex-1 bg-slate-950 p-4">
        <textarea
          value={code}
          onChange={(e) => onChange(e.target.value, format)}
          className="w-full h-full p-2 bg-transparent text-slate-200 text-xs font-mono resize-none focus:outline-none leading-relaxed overflow-y-auto"
          spellCheck={false}
        />
      </div>

      {/* Diagnostics & Bottom Action Toolbar */}
      <div className="p-4 bg-slate-950/80 border-t border-slate-800 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
        {/* Status Bar */}
        <div className="flex-1">
          {validation.isValid ? (
            <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 font-medium">
              <CheckCircle2 className="w-4 h-4 flex-shrink-0" />
              <span>Valid Policy Schema (0 errors)</span>
            </div>
          ) : (
            <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs bg-rose-500/10 border border-rose-500/30 text-rose-400 font-medium max-w-xl truncate">
              <AlertCircle className="w-4 h-4 flex-shrink-0" />
              <span className="truncate">{validation.errors[0]?.message || 'Schema errors detected.'}</span>
            </div>
          )}
        </div>

        {/* Action Buttons */}
        <div className="flex items-center gap-2">
          <button
            onClick={handleCopyCode}
            className="px-3.5 py-1.5 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-semibold shadow-sm transition-all flex items-center gap-1.5"
          >
            <Copy className="w-3.5 h-3.5" />
            <span>{copied ? 'Copied!' : 'Copy Code'}</span>
          </button>
          <button
            onClick={handleDownload}
            className="px-3.5 py-1.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold border border-slate-700 transition-all flex items-center gap-1.5"
          >
            <Download className="w-3.5 h-3.5" />
            <span>Download</span>
          </button>
          <button
            onClick={handleCopyCli}
            className="px-3.5 py-1.5 rounded-xl bg-slate-800 hover:bg-slate-700 text-cyan-300 text-xs font-semibold border border-slate-700 transition-all flex items-center gap-1.5"
          >
            <Terminal className="w-3.5 h-3.5" />
            <span>{cliCopied ? 'Command Copied!' : 'Copy CLI'}</span>
          </button>
        </div>
      </div>
    </div>
  );
};
