import React from 'react';
import { DAGNode, DAGEdge } from '../../engine/dag';
import { Layers } from 'lucide-react';

interface PolicyDAGProps {
  nodes: DAGNode[];
  edges: DAGEdge[];
  selectedNodeId: string | null;
  onSelectNode: (nodeId: string) => void;
  onToggleReconciler?: (key: string, enabled: boolean) => void;
}

export const PolicyDAG: React.FC<PolicyDAGProps> = ({
  nodes,
  edges,
  selectedNodeId,
  onSelectNode,
  onToggleReconciler,
}) => {
  const getNode = (id: string) => nodes.find((n) => n.id === id);

  return (
    <div className="relative w-full h-[460px] rounded-2xl bg-slate-950 border border-slate-800 shadow-2xl overflow-hidden flex flex-col">
      {/* Top DAG Canvas Header */}
      <div className="px-6 py-3 bg-slate-900/80 border-b border-slate-800 flex items-center justify-between z-10 backdrop-blur-md">
        <div className="flex items-center gap-2">
          <Layers className="w-4 h-4 text-indigo-400" />
          <span className="font-bold text-xs uppercase tracking-wider text-slate-200">
            Governance DAG & Execution Pipeline
          </span>
        </div>
        <div className="flex items-center gap-4 text-xs">
          <div className="flex items-center gap-1.5 text-emerald-400">
            <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse"></span>
            <span>Active ({nodes.filter((n) => n.status === 'active' && n.category === 'reconciler').length})</span>
          </div>
          <div className="flex items-center gap-1.5 text-slate-500">
            <span className="w-2 h-2 rounded-full bg-slate-600"></span>
            <span>Disabled ({nodes.filter((n) => n.status === 'inactive' && n.category === 'reconciler').length})</span>
          </div>
        </div>
      </div>

      {/* SVG Canvas Area */}
      <div className="relative flex-1 overflow-auto bg-[radial-gradient(#1e293b_1px,transparent_1px)] [background-size:20px_20px] p-4">
        <svg className="absolute inset-0 w-[980px] h-[430px] pointer-events-none">
          <defs>
            <linearGradient id="edge-gradient" x1="0%" y1="0%" x2="100%" y2="0%">
              <stop offset="0%" stopColor="#6366f1" stopOpacity="0.9" />
              <stop offset="100%" stopColor="#06b6d4" stopOpacity="0.9" />
            </linearGradient>
            <filter id="glow" x="-20%" y="-20%" width="140%" height="140%">
              <feGaussianBlur stdDeviation="3" result="glow" />
              <feComposite in="SourceGraphic" in2="glow" operator="over" />
            </filter>
          </defs>

          {edges.map((edge) => {
            const fromNode = getNode(edge.from);
            const toNode = getNode(edge.to);
            if (!fromNode || !toNode) return null;

            const fromX = fromNode.x + 152;
            const fromY = fromNode.y + 24;
            const toX = toNode.x;
            const toY = toNode.y + 24;
            const midX = (fromX + toX) / 2;

            return (
              <g key={edge.id}>
                <path
                  d={`M ${fromX} ${fromY} C ${midX} ${fromY}, ${midX} ${toY}, ${toX} ${toY}`}
                  fill="none"
                  stroke={edge.animated ? 'url(#edge-gradient)' : '#334155'}
                  strokeWidth={edge.animated ? '2' : '1.5'}
                  strokeDasharray={edge.animated ? '6,4' : 'none'}
                  className={edge.animated ? 'animate-[dash_20s_linear_infinite]' : ''}
                />
              </g>
            );
          })}
        </svg>

        {/* Nodes Layer */}
        <div className="relative w-[980px] h-[430px]">
          {nodes.map((node) => {
            const isSelected = selectedNodeId === node.id;
            const isActive = node.status === 'active';
            const isReconciler = node.category === 'reconciler' && node.reconcilerKey;

            return (
              <div
                key={node.id}
                onClick={() => onSelectNode(node.id)}
                style={{ left: `${node.x}px`, top: `${node.y}px` }}
                className={`absolute w-38 p-2.5 rounded-xl border transition-all cursor-pointer select-none shadow-lg backdrop-blur-md ${
                  isSelected
                    ? 'border-indigo-500 bg-indigo-950/90 ring-2 ring-indigo-500/50 scale-105 z-20 shadow-indigo-500/10'
                    : isActive
                    ? 'border-slate-800 bg-slate-900/90 hover:border-slate-700 hover:scale-102 z-10'
                    : 'border-slate-800/40 bg-slate-950/60 opacity-50 hover:opacity-90 z-10'
                }`}
              >
                <div className="flex items-center justify-between gap-1 mb-1.5">
                  <div className="flex items-center gap-1.5 min-w-0">
                    <span className="text-sm flex-shrink-0">{node.icon}</span>
                    <span className="font-bold text-xs text-white truncate">{node.label}</span>
                  </div>

                  {/* Inline Reconciler Quick-Toggle Switch */}
                  {isReconciler ? (
                    <button
                      type="button"
                      title={isActive ? 'Disable rule' : 'Enable rule'}
                      onClick={(e) => {
                        e.stopPropagation();
                        onToggleReconciler?.(node.reconcilerKey!, !isActive);
                      }}
                      className={`w-7 h-4 flex items-center rounded-full p-0.5 transition-colors flex-shrink-0 ${
                        isActive ? 'bg-indigo-600 justify-end' : 'bg-slate-700 justify-start'
                      }`}
                    >
                      <span className="w-3 h-3 rounded-full bg-white shadow-sm transform transition-transform" />
                    </button>
                  ) : (
                    <span className="w-2 h-2 rounded-full bg-emerald-400 flex-shrink-0" />
                  )}
                </div>

                <div className="flex items-center justify-between gap-1 text-[10px] text-slate-400">
                  <span className="truncate">{node.description}</span>
                  {node.badge && (
                    <span
                      className={`px-1.5 py-0.2 rounded text-[9px] font-mono flex-shrink-0 ${
                        isActive
                          ? 'bg-indigo-500/20 text-indigo-300 border border-indigo-500/30'
                          : 'bg-slate-800 text-slate-500'
                      }`}
                    >
                      {node.badge}
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
};
