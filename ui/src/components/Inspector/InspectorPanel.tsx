import React from 'react';
import { DAGNode } from '../../engine/dag';
import { Sliders, CheckCircle, Info, Terminal } from 'lucide-react';

interface InspectorPanelProps {
  selectedNode: DAGNode | undefined;
}

export const InspectorPanel: React.FC<InspectorPanelProps> = ({ selectedNode }) => {
  if (!selectedNode) {
    return (
      <div className="h-full flex flex-col items-center justify-center p-8 text-center text-slate-500 rounded-2xl bg-slate-900/60 border border-slate-800">
        <Sliders className="w-8 h-8 mb-2 opacity-40" />
        <p className="text-xs">Click any node in the Policy DAG to inspect its properties and schema bindings.</p>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-[340px] rounded-2xl bg-slate-900/80 border border-slate-800 shadow-xl overflow-hidden">
      {/* Header */}
      <div className="px-6 py-4 bg-slate-950 border-b border-slate-800 flex items-center justify-between">
        <div className="flex items-center gap-2.5">
          <span className="text-xl">{selectedNode.icon}</span>
          <div>
            <h3 className="font-bold text-white text-sm tracking-tight">{selectedNode.label}</h3>
            <span className="text-[10px] font-semibold text-indigo-400 uppercase tracking-wider">
              {selectedNode.category}
            </span>
          </div>
        </div>
        <div>
          {selectedNode.status === 'active' ? (
            <span className="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
              <CheckCircle className="w-3 h-3" />
              <span>Active Invariant</span>
            </span>
          ) : (
            <span className="px-2.5 py-0.5 rounded-full text-[10px] font-semibold bg-slate-800 text-slate-400">
              Disabled
            </span>
          )}
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 p-6 overflow-y-auto space-y-6">
        <div>
          <h4 className="text-xs font-bold text-slate-300 uppercase tracking-wider mb-2 flex items-center gap-1.5">
            <Info className="w-3.5 h-3.5 text-indigo-400" />
            <span>Description</span>
          </h4>
          <p className="text-xs text-slate-300 bg-slate-950/80 p-3.5 rounded-xl border border-slate-800 leading-relaxed">
            {selectedNode.description}
          </p>
        </div>

        <div>
          <h4 className="text-xs font-bold text-slate-300 uppercase tracking-wider mb-2 flex items-center gap-1.5">
            <Terminal className="w-3.5 h-3.5 text-cyan-400" />
            <span>Configured Attributes & State</span>
          </h4>
          <pre className="p-4 rounded-xl bg-slate-950 text-indigo-300 text-xs font-mono border border-slate-800 overflow-x-auto leading-relaxed">
            {JSON.stringify(selectedNode.details, null, 2)}
          </pre>
        </div>
      </div>
    </div>
  );
};
