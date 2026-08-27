import React, { useState } from 'react';
import { usePolicyState } from './store/usePolicyStore';
import { Header } from './components/Header/Header';
import { PolicyDAG } from './components/DAG/PolicyDAG';
import { PolicyEditor } from './components/Editor/PolicyEditor';
import { InspectorPanel } from './components/Inspector/InspectorPanel';
import { DocsPortal } from './components/DocsPortal';
import { LLMHub } from './components/LLMHub';
import { ExportModal } from './components/Export/ExportModal';

export const App: React.FC = () => {
  const {
    format,
    switchFormat,
    rawCode,
    rawYaml,
    rawJson,
    validation,
    dag,
    selectedNodeId,
    setSelectedNodeId,
    activeTab,
    setActiveTab,
    updateRawCode,
    toggleReconciler,
    loadPreset,
  } = usePolicyState();

  const [isExportOpen, setIsExportOpen] = useState(false);

  const selectedNode = dag.nodes.find((n) => n.id === selectedNodeId);

  return (
    <div className="min-h-screen flex flex-col bg-slate-950 text-slate-100 font-sans selection:bg-indigo-500 selection:text-white">
      {/* Top Application Header */}
      <Header
        activeTab={activeTab}
        setActiveTab={setActiveTab}
        onSelectPreset={loadPreset}
        onOpenExport={() => setIsExportOpen(true)}
      />

      {/* Main Workspace */}
      <main className="max-w-[1900px] mx-auto px-4 sm:px-6 lg:px-8 py-6 w-full flex-1">
        {activeTab === 'studio' && (
          <div className="space-y-6">
            {/* Side-by-Side Grid: Left (DAG & Inspector) | Right (Code Editor) */}
            <div className="grid grid-cols-1 lg:grid-cols-12 gap-6">
              
              {/* Left Column: Interactive Policy DAG & Inspector (7 cols) */}
              <div className="lg:col-span-7 space-y-6">
                <PolicyDAG
                  nodes={dag.nodes}
                  edges={dag.edges}
                  selectedNodeId={selectedNodeId}
                  onSelectNode={(id) => setSelectedNodeId(id)}
                  onToggleReconciler={toggleReconciler}
                />
                <InspectorPanel selectedNode={selectedNode} />
              </div>

              {/* Right Column: Live Code Editor & Diagnostics (5 cols) */}
              <div className="lg:col-span-5">
                <div className="sticky top-24">
                  <PolicyEditor
                    code={rawCode}
                    format={format}
                    validation={validation}
                    onChange={updateRawCode}
                    onSwitchFormat={switchFormat}
                  />
                </div>
              </div>

            </div>
          </div>
        )}

        {activeTab === 'docs' && <DocsPortal />}
        {activeTab === 'llm' && <LLMHub />}
      </main>

      {/* Footer */}
      <footer className="bg-slate-950 border-t border-slate-900 py-6 px-4 text-center text-xs text-slate-500">
        <div className="max-w-[1900px] mx-auto flex flex-col sm:flex-row items-center justify-between gap-4">
          <p>&copy; 2026 GitLab Fleet Governor Contributors & Divmora Team. Apache 2.0 License.</p>
          <div className="flex items-center gap-4">
            <button onClick={() => setActiveTab('studio')} className="hover:text-slate-300">Studio</button>
            <button onClick={() => setActiveTab('docs')} className="hover:text-slate-300">Docs</button>
            <button onClick={() => setActiveTab('llm')} className="hover:text-slate-300">LLM Hub</button>
            <a href="llms.txt" className="hover:text-slate-300">llms.txt</a>
            <a href="https://github.com/divmora/gitlab-fleet-governor" target="_blank" rel="noopener noreferrer" className="hover:text-slate-300">GitHub</a>
          </div>
        </div>
      </footer>

      {/* Export Modal */}
      <ExportModal
        isOpen={isExportOpen}
        onClose={() => setIsExportOpen(false)}
        rawYaml={rawYaml}
        rawJson={rawJson}
      />
    </div>
  );
};

export default App;
