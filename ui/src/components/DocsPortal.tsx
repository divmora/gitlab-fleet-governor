import React, { useState } from 'react';
import { BookOpen, Search, FileText } from 'lucide-react';
import { DOCS_DATA, DocItem } from '../data/docs';

export const DocsPortal: React.FC = () => {
  const [selectedDoc, setSelectedDoc] = useState<DocItem>(DOCS_DATA[0]);
  const [search, setSearch] = useState<string>('');

  const filteredDocs = DOCS_DATA.filter(
    (d) => d.title.toLowerCase().includes(search.toLowerCase()) || d.content.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div className="grid grid-cols-1 md:grid-cols-12 gap-8">
      {/* Sidebar List */}
      <div className="md:col-span-4 space-y-4">
        <div className="relative">
          <Search className="w-4 h-4 text-slate-400 absolute left-3.5 top-3" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search documentation..."
            className="w-full pl-10 pr-3.5 py-2.5 rounded-xl bg-slate-900 border border-slate-800 text-xs text-slate-200 focus:outline-none focus:border-indigo-500"
          />
        </div>

        <div className="space-y-1 bg-slate-900/60 p-2 rounded-2xl border border-slate-800">
          {filteredDocs.map((doc) => (
            <button
              key={doc.id}
              onClick={() => setSelectedDoc(doc)}
              className={`w-full text-left px-3.5 py-2.5 rounded-xl text-xs font-medium transition-all flex items-center justify-between ${
                selectedDoc.id === doc.id
                  ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/20 font-semibold'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/60'
              }`}
            >
              <div className="flex items-center gap-2.5">
                <FileText className="w-4 h-4" />
                <span>{doc.title}</span>
              </div>
              <span className="text-[10px] opacity-75">{doc.category}</span>
            </button>
          ))}
        </div>
      </div>

      {/* Main Document Reader */}
      <div className="md:col-span-8">
        <div className="p-8 rounded-2xl bg-slate-900/80 border border-slate-800 shadow-xl">
          <div className="flex items-center gap-2 text-xs font-semibold text-indigo-400 uppercase tracking-wider mb-2">
            <BookOpen className="w-4 h-4" />
            <span>{selectedDoc.category}</span>
          </div>
          <h1 className="text-2xl font-bold text-white mb-6 border-b border-slate-800 pb-4">{selectedDoc.title}</h1>
          <div className="prose prose-invert prose-indigo max-w-none text-sm text-slate-300 whitespace-pre-wrap leading-relaxed">
            {selectedDoc.content}
          </div>
        </div>
      </div>
    </div>
  );
};
