import React, { useState, useEffect, useRef } from 'react';
import { Copy, Download, Terminal, CheckCircle2, AlertCircle, Check } from 'lucide-react';
import { ValidationResult } from '../../engine/validator';
import { Tooltip } from '../Common/Tooltip';

interface PolicyEditorProps {
  code: string;
  format: 'yaml' | 'json';
  selectedNodeId: string | null;
  validation: ValidationResult;
  onChange: (newCode: string, format: 'yaml' | 'json') => void;
  onSwitchFormat: (format: 'yaml' | 'json') => void;
}

export const PolicyEditor: React.FC<PolicyEditorProps> = ({
  code,
  format,
  selectedNodeId,
  validation,
  onChange,
  onSwitchFormat,
}) => {
  const [copied, setCopied] = useState(false);
  const [cliCopied, setCliCopied] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const highlightLayerRef = useRef<HTMLDivElement>(null);
  const lineNumRef = useRef<HTMLDivElement>(null);

  const lines = code.split('\n');

  // Compute full block line range covering the entire section
  const getBlockRange = (): { start: number; end: number; label: string } | null => {
    if (!selectedNodeId) return null;

    let searchKey = '';
    let label = '';

    if (selectedNodeId === 'node-policy') {
      searchKey = format === 'yaml' ? 'version:' : '"version"';
      label = 'Policy Root';
    } else if (selectedNodeId === 'node-discovery-group') {
      searchKey = format === 'yaml' ? 'group_selector:' : '"group_selector"';
      label = 'Group Selectors';
    } else if (selectedNodeId === 'node-discovery-project') {
      searchKey = format === 'yaml' ? 'project_selector:' : '"project_selector"';
      label = 'Project Selectors';
    } else if (selectedNodeId === 'node-engine' || selectedNodeId === 'node-report') {
      searchKey = format === 'yaml' ? 'settings:' : '"settings"';
      label = 'Settings';
    } else if (selectedNodeId.startsWith('node-rec-')) {
      const recKey = selectedNodeId.replace('node-rec-', '');
      searchKey = format === 'yaml' ? `${recKey}:` : `"${recKey}"`;
      label = recKey;
    }

    if (!searchKey) return null;

    const startIdx = lines.findIndex((l) => l.includes(searchKey));
    if (startIdx === -1) return null;

    if (format === 'yaml') {
      const baseIndent = lines[startIdx].search(/\S/);
      let endIdx = startIdx;
      for (let i = startIdx + 1; i < lines.length; i++) {
        const line = lines[i];
        if (!line.trim()) {
          let hasDeeperChild = false;
          for (let j = i + 1; j < lines.length; j++) {
            if (lines[j].trim()) {
              if (lines[j].search(/\S/) > baseIndent) hasDeeperChild = true;
              break;
            }
          }
          if (hasDeeperChild) {
            endIdx = i;
            continue;
          } else {
            break;
          }
        }
        const indent = line.search(/\S/);
        if (indent <= baseIndent) {
          break;
        }
        endIdx = i;
      }
      return { start: startIdx, end: endIdx, label };
    } else {
      let endIdx = startIdx;
      let braceCount = 0;
      let bracketCount = 0;
      let started = false;

      for (let i = startIdx; i < lines.length; i++) {
        const line = lines[i];
        for (const char of line) {
          if (char === '{') { braceCount++; started = true; }
          if (char === '}') { braceCount--; }
          if (char === '[') { bracketCount++; started = true; }
          if (char === ']') { bracketCount--; }
        }
        endIdx = i;
        if (started && braceCount <= 0 && bracketCount <= 0 && i > startIdx) {
          break;
        }
        if (!started && i === startIdx && (line.includes(',') || !line.includes('{'))) {
          break;
        }
      }
      return { start: startIdx, end: endIdx, label };
    }
  };

  const blockRange = getBlockRange();

  // Scroll to focused section
  const jumpToFocusedSection = () => {
    if (blockRange && textareaRef.current) {
      const lineHeight = 22;
      const targetScroll = Math.max(0, blockRange.start * lineHeight - 50);
      textareaRef.current.scrollTop = targetScroll;
      if (lineNumRef.current) lineNumRef.current.scrollTop = targetScroll;
      if (highlightLayerRef.current) highlightLayerRef.current.scrollTop = targetScroll;
    }
  };

  // Auto-scroll when selectedNodeId changes
  useEffect(() => {
    if (selectedNodeId && blockRange) {
      jumpToFocusedSection();
    }
  }, [selectedNodeId]);

  const handleScroll = (e: React.UIEvent<HTMLTextAreaElement>) => {
    const scrollTop = e.currentTarget.scrollTop;
    if (lineNumRef.current) lineNumRef.current.scrollTop = scrollTop;
    if (highlightLayerRef.current) highlightLayerRef.current.scrollTop = scrollTop;
  };

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
    <div className="flex flex-col h-[720px] rounded-2xl bg-slate-900/90 border border-slate-800 shadow-2xl overflow-hidden">
      {/* Unified IDE Header: Window Controls + Title + Status (Left) | Controls & Icon Actions (Right) */}
      <div className="px-5 py-2.5 bg-slate-950 border-b border-slate-800 flex items-center justify-between gap-3 select-none">
        {/* Left Side: Window Controls, Filename, Validation Pill */}
        <div className="flex items-center gap-3 min-w-0">
          <div className="flex gap-1.5 flex-shrink-0">
            <span className="w-2.5 h-2.5 rounded-full bg-rose-500/80"></span>
            <span className="w-2.5 h-2.5 rounded-full bg-amber-500/80"></span>
            <span className="w-2.5 h-2.5 rounded-full bg-emerald-500/80"></span>
          </div>

          <span className="font-mono text-xs font-semibold text-slate-300 truncate">
            fleet-policy.{format}
          </span>

          {validation.isValid ? (
            <Tooltip content="Policy configuration matches valid AST schema (0 errors)">
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 flex-shrink-0 cursor-default">
                <CheckCircle2 className="w-3 h-3 flex-shrink-0" />
                <span>Valid</span>
              </span>
            </Tooltip>
          ) : (
            <Tooltip content={validation.errors[0]?.message || 'Schema errors detected'}>
              <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium bg-rose-500/10 text-rose-400 border border-rose-500/20 max-w-[140px] truncate flex-shrink-0 cursor-help">
                <AlertCircle className="w-3 h-3 flex-shrink-0" />
                <span className="truncate">Invalid</span>
              </span>
            </Tooltip>
          )}
        </div>

        {/* Right Side: Format Switch & Pure Icon Action Buttons with Tooltips */}
        <div className="flex items-center gap-1.5 flex-shrink-0">
          {/* Format Toggle */}
          <div className="flex items-center p-0.5 rounded-lg bg-slate-900 border border-slate-800 mr-1">
            <Tooltip content="Edit in YAML syntax">
              <button
                onClick={() => onSwitchFormat('yaml')}
                className={`px-2.5 py-1 rounded-md text-[11px] font-semibold transition-all ${
                  format === 'yaml' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white'
                }`}
              >
                YAML
              </button>
            </Tooltip>
            <Tooltip content="Edit in JSON syntax">
              <button
                onClick={() => onSwitchFormat('json')}
                className={`px-2.5 py-1 rounded-md text-[11px] font-semibold transition-all ${
                  format === 'json' ? 'bg-indigo-600 text-white shadow-sm' : 'text-slate-400 hover:text-white'
                }`}
              >
                JSON
              </button>
            </Tooltip>
          </div>

          {/* Copy Code (Icon Only with Tooltip) */}
          <Tooltip content={copied ? 'Copied to clipboard!' : 'Copy policy configuration'}>
            <button
              onClick={handleCopyCode}
              aria-label="Copy code"
              className="p-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 active:scale-95 text-slate-300 hover:text-white border border-slate-700/60 transition-all shadow-sm flex items-center justify-center cursor-pointer"
            >
              {copied ? (
                <Check className="w-4 h-4 text-emerald-400" />
              ) : (
                <Copy className="w-4 h-4" />
              )}
            </button>
          </Tooltip>

          {/* Download File (Icon Only with Tooltip) */}
          <Tooltip content={`Download fleet-policy.${format}`}>
            <button
              onClick={handleDownload}
              aria-label={`Download fleet-policy.${format}`}
              className="p-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 active:scale-95 text-slate-300 hover:text-white border border-slate-700/60 transition-all shadow-sm flex items-center justify-center cursor-pointer"
            >
              <Download className="w-4 h-4" />
            </button>
          </Tooltip>

          {/* Copy CLI Dry-Run Command (Icon Only with Tooltip) */}
          <Tooltip content={cliCopied ? 'CLI dry-run command copied!' : 'Copy CLI dry-run command'}>
            <button
              onClick={handleCopyCli}
              aria-label="Copy CLI command"
              className="p-1.5 rounded-lg bg-slate-800/90 hover:bg-slate-700 active:scale-95 text-cyan-300 hover:text-cyan-200 border border-cyan-800/40 transition-all shadow-sm flex items-center justify-center cursor-pointer"
            >
              {cliCopied ? (
                <Check className="w-4 h-4 text-cyan-400" />
              ) : (
                <Terminal className="w-4 h-4 text-cyan-400" />
              )}
            </button>
          </Tooltip>
        </div>
      </div>

      {/* Synchronized Full-Height Code Area */}
      <div className="relative flex-1 bg-slate-950 flex overflow-hidden">
        {/* Line Numbers Column */}
        <div
          ref={lineNumRef}
          className="w-12 py-4 bg-slate-950/95 border-r border-slate-900 text-right pr-3 font-mono text-[11px] text-slate-600 select-none overflow-hidden leading-[22px]"
        >
          {lines.map((_, i) => {
            const isHighlighted = blockRange && i >= blockRange.start && i <= blockRange.end;
            return (
              <div
                key={i}
                className={`h-[22px] transition-colors duration-150 ${
                  isHighlighted
                    ? 'text-indigo-300 font-bold bg-indigo-500/20 -mr-3 pr-3 rounded-l border-r-2 border-indigo-400'
                    : ''
                }`}
              >
                {i + 1}
              </div>
            );
          })}
        </div>

        {/* Code Content Area */}
        <div className="relative flex-1 h-full overflow-hidden">
          {/* Background Highlighting Layer */}
          <div
            ref={highlightLayerRef}
            className="absolute inset-0 py-4 pointer-events-none font-mono text-xs leading-[22px] overflow-hidden select-none"
          >
            {lines.map((_, i) => {
              const isHighlighted = blockRange && i >= blockRange.start && i <= blockRange.end;
              const isFirst = blockRange && i === blockRange.start;
              const isLast = blockRange && i === blockRange.end;
              return (
                <div
                  key={i}
                  className={`h-[22px] w-full transition-all duration-150 ${
                    isHighlighted
                      ? `bg-indigo-500/15 border-l-4 border-indigo-400 ${
                          isFirst ? 'border-t border-indigo-500/30' : ''
                        } ${isLast ? 'border-b border-indigo-500/30' : ''}`
                      : ''
                  }`}
                />
              );
            })}
          </div>

          {/* Foreground Editable Textarea */}
          <textarea
            ref={textareaRef}
            value={code}
            onScroll={handleScroll}
            onChange={(e) => onChange(e.target.value, format)}
            className="relative z-10 w-full h-full p-4 bg-transparent text-slate-200 text-xs font-mono resize-none focus:outline-none leading-[22px] overflow-y-auto whitespace-pre selection:bg-indigo-500/30 selection:text-white"
            spellCheck={false}
          />
        </div>
      </div>
    </div>
  );
};
