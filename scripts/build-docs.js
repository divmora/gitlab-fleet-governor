#!/usr/bin/env node

/**
 * GitLab Fleet Governor - Documentation & Interactive AI Studio Portal Builder
 * Following the OwlFlow Architecture Pattern:
 * 1. Static HTML documentation website & Interactive Policy Studio for GitHub Pages
 * 2. llms.txt (Standard curated AI sitemap & documentation manifest)
 * 3. llms-full.txt (Consolidated single-file documentation for 1-shot AI scraping)
 * 4. schema.json (Standard JSON Schema for IDE and LLM AST verification)
 * 5. Raw markdown mirror (.md endpoints) for direct AI fetching
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const rootDir = path.resolve(__dirname, '..');
const targetArg = process.argv[2];
const outDir = targetArg ? path.resolve(process.cwd(), targetArg) : path.resolve(rootDir, 'site');
const docsOutDir = path.join(outDir, 'docs');
const policiesOutDir = path.join(outDir, 'policies');

// Ensure output directories exist
fs.mkdirSync(outDir, { recursive: true });
fs.mkdirSync(docsOutDir, { recursive: true });
fs.mkdirSync(policiesOutDir, { recursive: true });

const docPages = [
  { file: 'README.md', slug: 'overview', title: 'Overview & Features', section: 'Getting Started' },
  { file: 'AGENTS.md', slug: 'agents', title: 'Autonomous Agent Architecture', section: 'Architecture & AI' },
  { file: 'docs/getting-started.md', slug: 'getting-started', title: 'Quickstart & Installation', section: 'Getting Started' },
  { file: 'docs/configuration.md', slug: 'configuration', title: 'Configuration & Schema Reference', section: 'Core References' },
  { file: 'docs/operations.md', slug: 'operations', title: '10 Governance Reconcilers', section: 'Core References' },
  { file: 'docs/lambda.md', slug: 'lambda', title: 'AWS Lambda & Serverless Triggers', section: 'Operations' },
  { file: 'docs/ci-cd.md', slug: 'ci-cd', title: 'CI/CD Automation & Pipelines', section: 'Operations' },
  { file: 'docs/architecture.md', slug: 'architecture', title: 'Engine Architecture & Rate Limiting', section: 'Architecture & AI' },
  { file: 'docs/llms.md', slug: 'llms', title: 'LLM Integration & Prompt Library', section: 'Architecture & AI' },
];

function escapeHtml(str) {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

function renderMarkdown(md) {
  let html = md;

  // Code blocks with syntax highlight & copy button
  html = html.replace(/```([a-zA-Z0-9_-]*)\n([\s\S]*?)```/g, (match, lang, code) => {
    const escaped = escapeHtml(code.trim());
    return `<div class="code-block my-4 rounded-xl bg-slate-900 border border-slate-800 overflow-hidden shadow-lg">
      <div class="flex items-center justify-between px-4 py-2 bg-slate-950/80 border-b border-slate-800 text-xs font-mono text-slate-400">
        <span class="font-semibold text-indigo-400 uppercase tracking-wider">${lang || 'text'}</span>
        <button onclick="copyCode(this, decodeURIComponent('${encodeURIComponent(code.trim())}'))" class="px-2.5 py-1 rounded bg-slate-800 hover:bg-slate-700 text-slate-300 hover:text-white transition-all text-xs">Copy</button>
      </div>
      <pre class="p-4 overflow-x-auto text-sm text-slate-200 font-mono leading-relaxed"><code>${escaped}</code></pre>
    </div>`;
  });

  // Inline code
  html = html.replace(/`([^`]+)`/g, '<code class="px-1.5 py-0.5 rounded bg-slate-800 text-indigo-300 font-mono text-xs border border-slate-700">$1</code>');

  // Headings
  html = html.replace(/^### (.*$)/gim, '<h3 class="text-lg font-bold text-white mt-6 mb-2 flex items-center gap-2"><span class="text-indigo-400">#</span> $1</h3>');
  html = html.replace(/^## (.*$)/gim, '<h2 class="text-xl font-bold text-indigo-400 mt-8 mb-4 border-b border-slate-800 pb-2 flex items-center gap-2"><span class="text-cyan-400">##</span> $1</h2>');
  html = html.replace(/^# (.*$)/gim, '<h1 class="text-3xl font-extrabold text-white mb-6 tracking-tight">$1</h1>');

  // GitHub Callout Alerts
  html = html.replace(/>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*\n((?:>.*\n?)*)/gim, (match, type, content) => {
    const text = content.replace(/^>\s?/gm, '').trim();
    const colors = {
      NOTE: 'border-blue-500 bg-blue-500/10 text-blue-300',
      TIP: 'border-emerald-500 bg-emerald-500/10 text-emerald-300',
      IMPORTANT: 'border-purple-500 bg-purple-500/10 text-purple-300',
      WARNING: 'border-amber-500 bg-amber-500/10 text-amber-300',
      CAUTION: 'border-rose-500 bg-rose-500/10 text-rose-300',
    };
    return `<div class="my-4 p-4 border-l-4 rounded-r-xl ${colors[type] || colors.NOTE}">
      <div class="font-bold text-xs uppercase tracking-wider mb-1 flex items-center gap-1.5"><span>⚡</span> ${type}</div>
      <div class="text-sm leading-relaxed">${text}</div>
    </div>`;
  });

  // Blockquotes
  html = html.replace(/^\> (.*$)/gim, '<blockquote class="border-l-4 border-indigo-500/40 bg-slate-900/40 pl-4 py-2 my-3 text-slate-400 italic rounded-r-lg">$1</blockquote>');

  // Horizontal rules
  html = html.replace(/^---$/gim, '<hr class="my-8 border-slate-800" />');

  // Lists
  html = html.replace(/^\s*-\s+(.*$)/gim, '<li class="ml-4 list-disc text-slate-300 my-1">$1</li>');

  // Bold & Italic
  html = html.replace(/\*\*([^*]+)\*\*/g, '<strong class="font-bold text-white">$1</strong>');
  html = html.replace(/\*([^*]+)\*/g, '<em class="italic text-slate-300">$1</em>');

  // Tables
  const lines = html.split('\n');
  let inTable = false;
  let tableRows = [];
  const processedLines = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line.startsWith('|') && line.endsWith('|')) {
      if (line.includes('---')) continue;
      const isHeader = !inTable;
      inTable = true;
      const cells = line.split('|').slice(1, -1).map(c => c.trim());
      const tag = isHeader ? 'th' : 'td';
      const cellHtml = cells.map(c => `<${tag} class="border border-slate-800 px-4 py-2.5 text-sm ${tag === 'th' ? 'bg-slate-900 font-bold text-indigo-300 text-left' : 'text-slate-300'}">${c}</${tag}>`).join('');
      tableRows.push(`<tr>${cellHtml}</tr>`);
    } else {
      if (inTable) {
        processedLines.push(`<div class="overflow-x-auto my-6 rounded-xl border border-slate-800 shadow-md"><table class="w-full border-collapse bg-slate-950">${tableRows.join('')}</table></div>`);
        tableRows = [];
        inTable = false;
      }
      processedLines.push(lines[i]);
    }
  }
  if (inTable) {
    processedLines.push(`<div class="overflow-x-auto my-6 rounded-xl border border-slate-800 shadow-md"><table class="w-full border-collapse bg-slate-950">${tableRows.join('')}</table></div>`);
  }
  html = processedLines.join('\n');

  // Paragraphs
  html = html.replace(/\n\n/g, '</p><p class="my-3 text-slate-300 leading-relaxed">');

  return `<p class="my-3 text-slate-300 leading-relaxed">${html}</p>`;
}

// Generate Navigation Bar & Sidebar HTML
function getHeaderNav() {
  return `
  <header class="sticky top-0 z-50 bg-slate-950/85 backdrop-blur-md border-b border-slate-800/80">
    <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
      <div class="flex items-center gap-3">
        <a href="index.html" class="flex items-center gap-2.5 group">
          <div class="w-9 h-9 rounded-xl bg-gradient-to-br from-indigo-500 to-cyan-500 flex items-center justify-center text-white shadow-lg shadow-indigo-500/20 group-hover:scale-105 transition-transform">
            <span class="text-xl">🛡️</span>
          </div>
          <div>
            <span class="font-bold text-white text-base tracking-tight group-hover:text-indigo-400 transition-colors">GitLab Fleet Governor</span>
            <span class="hidden sm:inline-block ml-2 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">Studio UI</span>
          </div>
        </a>
      </div>

      <nav class="hidden md:flex items-center gap-1">
        <a href="index.html" class="px-3.5 py-1.5 rounded-lg text-sm font-medium text-slate-300 hover:text-white hover:bg-slate-800/60 transition-colors">🎮 Policy Studio</a>
        <a href="docs/overview.html" class="px-3.5 py-1.5 rounded-lg text-sm font-medium text-slate-300 hover:text-white hover:bg-slate-800/60 transition-colors">📚 Documentation</a>
        <a href="docs/llms.html" class="px-3.5 py-1.5 rounded-lg text-sm font-medium text-slate-300 hover:text-white hover:bg-slate-800/60 transition-colors">🤖 LLM Hub</a>
        <a href="llms.txt" class="px-3.5 py-1.5 rounded-lg text-sm font-medium text-cyan-400 hover:text-cyan-300 hover:bg-cyan-500/10 transition-colors">📄 llms.txt</a>
      </nav>

      <div class="flex items-center gap-3">
        <a href="https://github.com/divmora/gitlab-fleet-governor" target="_blank" rel="noopener noreferrer" class="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-lg bg-slate-800 hover:bg-slate-700 text-slate-200 text-sm font-medium transition-all shadow-sm border border-slate-700">
          <svg class="w-4 h-4 fill-current" viewBox="0 0 24 24"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0024 12c0-6.63-5.37-12-12-12z"/></svg>
          <span>GitHub</span>
        </a>
      </div>
    </div>
  </header>
  `;
}

function getSidebarHtml(currentSlug) {
  const sections = {};
  docPages.forEach(p => {
    if (!sections[p.section]) sections[p.section] = [];
    sections[p.section].push(p);
  });

  let html = `<div class="w-64 flex-shrink-0 hidden lg:block pr-6 border-r border-slate-800">
    <div class="sticky top-24 space-y-6">`;

  for (const [secTitle, pages] of Object.entries(sections)) {
    html += `<div>
      <h4 class="text-xs font-bold text-slate-400 uppercase tracking-wider mb-2.5">${secTitle}</h4>
      <ul class="space-y-1">`;
    for (const page of pages) {
      const isCurrent = page.slug === currentSlug;
      const activeClass = isCurrent
        ? 'bg-indigo-500/15 text-indigo-400 font-semibold border-l-2 border-indigo-500 pl-3'
        : 'text-slate-400 hover:text-slate-200 hover:bg-slate-900 pl-3.5';
      const href = page.slug === 'overview' ? 'overview.html' : `${page.slug}.html`;
      html += `<li>
        <a href="${href}" class="block py-1.5 text-sm rounded-r-lg transition-colors ${activeClass}">${page.title}</a>
      </li>`;
    }
    html += `</ul></div>`;
  }

  html += `
    <div class="pt-4 border-t border-slate-800">
      <h4 class="text-xs font-bold text-slate-400 uppercase tracking-wider mb-2.5">AI Endpoints</h4>
      <ul class="space-y-1">
        <li><a href="../llms.txt" class="block py-1.5 text-sm text-cyan-400 hover:text-cyan-300 pl-3.5">📄 /llms.txt</a></li>
        <li><a href="../llms-full.txt" class="block py-1.5 text-sm text-cyan-400 hover:text-cyan-300 pl-3.5">📑 /llms-full.txt</a></li>
        <li><a href="../schema.json" class="block py-1.5 text-sm text-cyan-400 hover:text-cyan-300 pl-3.5">📋 /schema.json</a></li>
      </ul>
    </div>
  </div></div>`;
  return html;
}

// 1. Build Index / Interactive Studio Page (`site/index.html`)
const indexHtml = `<!DOCTYPE html>
<html lang="en" class="dark">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>GitLab Fleet Governor Studio — Declarative Policy Studio & Simulator</title>
  <meta name="description" content="Interactive Policy Studio, Schema Validator, and Topology Simulator for GitLab Fleet Governor" />
  <script src="https://cdn.tailwindcss.com"></script>
  <script>
    tailwind.config = {
      darkMode: 'class',
      theme: {
        extend: {
          colors: {
            brand: {
              50: '#eef2ff',
              500: '#6366f1',
              600: '#4f46e5',
              700: '#4338ca',
            }
          }
        }
      }
    }
  </script>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600&display=swap');
    body { font-family: 'Inter', sans-serif; }
    pre, code, textarea { font-family: 'JetBrains Mono', monospace; }
  </style>
</head>
<body class="bg-slate-950 text-slate-100 min-h-screen flex flex-col antialiased selection:bg-indigo-500 selection:text-white">

  ${getHeaderNav()}

  <!-- Hero Section -->
  <section class="relative overflow-hidden py-12 px-4 sm:px-6 lg:px-8 border-b border-slate-800/60 bg-gradient-to-b from-indigo-950/20 to-transparent">
    <div class="max-w-7xl mx-auto text-center relative z-10">
      <div class="inline-flex items-center gap-2 px-3 py-1 rounded-full text-xs font-semibold bg-indigo-500/10 text-indigo-400 border border-indigo-500/20 mb-4">
        <span>✨</span> Production Fleet Governance & AI Policy Studio
      </div>
      <h1 class="text-3xl sm:text-5xl font-extrabold tracking-tight text-white max-w-4xl mx-auto">
        Declarative Policy-as-Code & Simulator for <span class="bg-gradient-to-r from-indigo-400 via-cyan-400 to-emerald-400 bg-clip-text text-transparent">GitLab Fleets</span>
      </h1>
      <p class="mt-4 text-base sm:text-lg text-slate-400 max-w-2xl mx-auto leading-relaxed">
        Design, validate, and simulate push rules, branch protections, merge request approval matrices, and native pipeline retention across thousands of GitLab repositories.
      </p>

      <!-- Presets Bar -->
      <div class="mt-8 flex flex-wrap justify-center gap-2.5">
        <button onclick="loadPreset('soc2')" class="px-4 py-2 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white text-xs sm:text-sm font-semibold shadow-lg shadow-indigo-600/20 transition-all flex items-center gap-1.5">
          <span>🛡️</span> Preset: SOC 2 Baseline
        </button>
        <button onclick="loadPreset('trunk')" class="px-4 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs sm:text-sm font-semibold border border-slate-700 transition-all flex items-center gap-1.5">
          <span>🚀</span> Preset: Strict Trunk-Based
        </button>
        <button onclick="loadPreset('cleanup')" class="px-4 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs sm:text-sm font-semibold border border-slate-700 transition-all flex items-center gap-1.5">
          <span>🧹</span> Preset: Pipeline Cleanup
        </button>
        <button onclick="loadPreset('members')" class="px-4 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs sm:text-sm font-semibold border border-slate-700 transition-all flex items-center gap-1.5">
          <span>👥</span> Preset: Member Access Audit
        </button>
      </div>
    </div>
  </section>

  <!-- Interactive Studio Grid -->
  <main class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8 w-full flex-1">
    <div class="grid grid-cols-1 lg:grid-cols-12 gap-8">
      
      <!-- Left Column: Form Controls (7 cols) -->
      <div class="lg:col-span-7 space-y-6">
        
        <!-- Target Fleets -->
        <div class="p-6 rounded-2xl bg-slate-900/70 border border-slate-800/80 shadow-xl backdrop-blur-sm">
          <div class="flex items-center gap-2.5 text-base font-bold text-white mb-4">
            <div class="w-7 h-7 rounded-lg bg-indigo-500/20 text-indigo-400 flex items-center justify-center text-sm">🎯</div>
            <h3>1. Target Fleet Selectors</h3>
          </div>
          <div class="space-y-4">
            <div>
              <label class="block text-xs font-semibold text-slate-300 mb-1.5">Group Hierarchy Paths (comma-separated, recursive BFS)</label>
              <input type="text" id="in-group-paths" oninput="syncState()" value="enterprise-core, fintech-division" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors" placeholder="e.g. enterprise-core, security-division" />
            </div>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label class="block text-xs font-semibold text-slate-300 mb-1.5">Project Regex Include (RE2)</label>
                <input type="text" id="in-project-regex" oninput="syncState()" value="^svc-.*$" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors" placeholder="e.g. ^svc-.*$" />
              </div>
              <div class="flex items-end pb-1.5">
                <label class="flex items-center gap-2 text-sm text-slate-300 cursor-pointer select-none">
                  <input type="checkbox" id="in-archived" onchange="syncState()" class="w-4 h-4 rounded text-indigo-600 bg-slate-950 border-slate-800 focus:ring-indigo-500" />
                  <span>Exclude Archived Projects</span>
                </label>
              </div>
            </div>
          </div>
        </div>

        <!-- Push Rules Governance -->
        <div class="p-6 rounded-2xl bg-slate-900/70 border border-slate-800/80 shadow-xl backdrop-blur-sm">
          <div class="flex items-center gap-2.5 text-base font-bold text-white mb-4">
            <div class="w-7 h-7 rounded-lg bg-indigo-500/20 text-indigo-400 flex items-center justify-center text-sm">🔒</div>
            <h3>2. Push Rules & Commit Signatures</h3>
          </div>
          <div class="space-y-4">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label class="block text-xs font-semibold text-slate-300 mb-1.5">Author Email Regex</label>
                <input type="text" id="in-push-email" oninput="syncState()" value="@fleetcorp\\.io$" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors" />
              </div>
              <div>
                <label class="block text-xs font-semibold text-slate-300 mb-1.5">Max Commit File Size (MB)</label>
                <input type="number" id="in-push-max-size" oninput="syncState()" value="25" min="1" max="1000" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors" />
              </div>
            </div>
            <div class="grid grid-cols-2 sm:grid-cols-3 gap-3 pt-1">
              <label class="flex items-center gap-2 text-xs text-slate-300 cursor-pointer select-none">
                <input type="checkbox" id="in-push-secrets" onchange="syncState()" checked class="w-4 h-4 rounded text-indigo-600 bg-slate-950 border-slate-800" />
                <span>Prevent Secrets</span>
              </label>
              <label class="flex items-center gap-2 text-xs text-slate-300 cursor-pointer select-none">
                <input type="checkbox" id="in-push-unsigned" onchange="syncState()" checked class="w-4 h-4 rounded text-indigo-600 bg-slate-950 border-slate-800" />
                <span>Reject Unsigned</span>
              </label>
              <label class="flex items-center gap-2 text-xs text-slate-300 cursor-pointer select-none">
                <input type="checkbox" id="in-push-committer" onchange="syncState()" checked class="w-4 h-4 rounded text-indigo-600 bg-slate-950 border-slate-800" />
                <span>Committer Check</span>
              </label>
            </div>
          </div>
        </div>

        <!-- Protected Branches & Approval Rules -->
        <div class="p-6 rounded-2xl bg-slate-900/70 border border-slate-800/80 shadow-xl backdrop-blur-sm">
          <div class="flex items-center gap-2.5 text-base font-bold text-white mb-4">
            <div class="w-7 h-7 rounded-lg bg-indigo-500/20 text-indigo-400 flex items-center justify-center text-sm">🔀</div>
            <h3>3. Protected Branches & Approvals</h3>
          </div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <label class="block text-xs font-semibold text-slate-300 mb-1.5">Branch Name</label>
              <input type="text" id="in-branch-name" oninput="syncState()" value="main" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors" />
            </div>
            <div>
              <label class="block text-xs font-semibold text-slate-300 mb-1.5">Required Approvals</label>
              <input type="number" id="in-approvals-req" oninput="syncState()" value="1" min="0" max="10" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors" />
            </div>
          </div>
        </div>

        <!-- Native Pipeline Retention & Compliance -->
        <div class="p-6 rounded-2xl bg-slate-900/70 border border-slate-800/80 shadow-xl backdrop-blur-sm">
          <div class="flex items-center gap-2.5 text-base font-bold text-white mb-4">
            <div class="w-7 h-7 rounded-lg bg-indigo-500/20 text-indigo-400 flex items-center justify-center text-sm">🧹</div>
            <h3>4. Native Retention & Compliance</h3>
          </div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <label class="block text-xs font-semibold text-slate-300 mb-1.5">Retention (Days) &rarr; <code class="text-cyan-400">ci_delete_pipelines_in_seconds</code></label>
              <input type="number" id="in-retention-days" oninput="syncState()" value="30" min="0" max="3650" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors" />
            </div>
            <div>
              <label class="block text-xs font-semibold text-slate-300 mb-1.5">Compliance Framework (GraphQL)</label>
              <select id="in-compliance-fw" onchange="syncState()" class="w-full px-3.5 py-2.5 rounded-xl bg-slate-950 border border-slate-800 text-sm text-slate-200 focus:outline-none focus:border-indigo-500 transition-colors">
                <option value="SOC2">SOC 2 Type II</option>
                <option value="HIPAA">HIPAA Security</option>
                <option value="PCI-DSS">PCI-DSS 4.0</option>
                <option value="ISO27001">ISO 27001</option>
                <option value="None">None</option>
              </select>
            </div>
          </div>
        </div>

      </div>

      <!-- Right Column: Code Editor, Diagnostics & Live Exporter (5 cols) -->
      <div class="lg:col-span-5 space-y-6">
        
        <div class="p-6 rounded-2xl bg-slate-900/90 border border-slate-800 shadow-2xl sticky top-24 flex flex-col h-[calc(100vh-140px)]">
          <div class="flex items-center justify-between pb-4 border-b border-slate-800 mb-4">
            <div class="flex items-center gap-2">
              <span class="w-3 h-3 rounded-full bg-emerald-500 animate-pulse"></span>
              <h3 class="font-bold text-white text-sm">Policy Specification</h3>
            </div>
            <div class="flex items-center gap-1 bg-slate-950 p-1 rounded-lg border border-slate-800">
              <button onclick="setFormat('yaml')" id="tab-yaml" class="px-3 py-1 rounded-md text-xs font-semibold bg-indigo-600 text-white transition-all">YAML</button>
              <button onclick="setFormat('json')" id="tab-json" class="px-3 py-1 rounded-md text-xs font-semibold text-slate-400 hover:text-white transition-all">JSON</button>
            </div>
          </div>

          <!-- Code Output Area -->
          <textarea id="policy-editor" class="w-full flex-1 p-4 rounded-xl bg-slate-950 text-slate-200 border border-slate-800 text-xs font-mono resize-none focus:outline-none leading-relaxed overflow-y-auto" spellcheck="false"></textarea>

          <!-- Diagnostic Feedback Bar -->
          <div id="validation-bar" class="my-4 px-4 py-2.5 rounded-xl text-xs flex items-center gap-2 bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 font-medium">
            <span>✔</span> Valid Declarative Policy Schema
          </div>

          <!-- Action Buttons -->
          <div class="grid grid-cols-2 sm:grid-cols-3 gap-2">
            <button onclick="copyCode(this)" id="btn-copy" class="px-3 py-2 rounded-xl bg-indigo-600 hover:bg-indigo-500 text-white text-xs font-semibold transition-all flex items-center justify-center gap-1.5 shadow-md">
              <span>📋</span> Copy Code
            </button>
            <button onclick="downloadPolicy()" class="px-3 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-semibold border border-slate-700 transition-all flex items-center justify-center gap-1.5">
              <span>💾</span> Download
            </button>
            <button onclick="copyCliCommand(this)" class="col-span-2 sm:col-span-1 px-3 py-2 rounded-xl bg-slate-800 hover:bg-slate-700 text-cyan-300 text-xs font-semibold border border-slate-700 transition-all flex items-center justify-center gap-1.5">
              <span>💻</span> Copy CLI
            </button>
          </div>
        </div>

      </div>

    </div>
  </main>

  <!-- Footer -->
  <footer class="bg-slate-950 border-t border-slate-900 py-8 px-4 text-center text-xs text-slate-500">
    <div class="max-w-7xl mx-auto flex flex-col sm:flex-row items-center justify-between gap-4">
      <p>&copy; 2026 GitLab Fleet Governor Contributors & Divmora Team. Apache 2.0 License.</p>
      <div class="flex items-center gap-4">
        <a href="docs/overview.html" class="hover:text-slate-300">Docs</a>
        <a href="docs/llms.html" class="hover:text-slate-300">LLM Hub</a>
        <a href="llms.txt" class="hover:text-slate-300">llms.txt</a>
        <a href="https://github.com/divmora/gitlab-fleet-governor" class="hover:text-slate-300">GitHub</a>
      </div>
    </div>
  </footer>

  <script>
    let currentFormat = 'yaml';
    let state = {
      version: "v1",
      settings: {
        dry_run: true,
        concurrency: 10,
        log_level: "info",
        log_format: "text",
        report_format: "table"
      },
      targets: {
        group_selector: {
          group_paths_include: ["enterprise-core", "fintech-division"],
          recursive: true
        },
        project_selector: {
          project_name_regex_include: "^svc-.*$",
          archived: false
        }
      },
      policies: {
        push_rules: {
          author_email_regex: "@fleetcorp\\\\.io$",
          max_file_size: 25,
          prevent_secrets: true,
          reject_unsigned_commits: true,
          commit_committer_check: true
        },
        protected_branches: [
          {
            name: "main",
            allowed_to_push: [{ access_level: 0 }],
            allowed_to_merge: [{ access_level: 40 }],
            code_owner_approval_required: true
          }
        ],
        approval_rules: {
          settings: {
            allow_author_approval: false,
            allow_committer_approval: false
          },
          rules: [
            {
              name: "Mandatory Security Approval",
              approvals_required: 1
            }
          ]
        },
        pipeline_retention: {
          retention_days: 30
        },
        compliance: {
          framework_name: "SOC2"
        }
      }
    };

    function escapeYaml(str) {
      if (typeof str !== 'string') return str;
      if (str.includes(':') || str.includes('@') || str.includes('^') || str.includes('$') || str.includes('\\\\')) {
        return '"' + str.replace(/"/g, '\\\\"') + '"';
      }
      return str;
    }

    function toYAML(obj, indent = 0) {
      const sp = ' '.repeat(indent);
      let out = '';
      for (const [k, v] of Object.entries(obj)) {
        if (v === undefined || v === null) continue;
        if (Array.isArray(v)) {
          if (v.length === 0) continue;
          out += sp + k + ':\\n';
          for (const item of v) {
            if (typeof item === 'object' && item !== null) {
              const inner = toYAML(item, indent + 4).trimStart();
              out += sp + '  - ' + inner;
            } else {
              out += sp + '  - ' + escapeYaml(item) + '\\n';
            }
          }
        } else if (typeof v === 'object') {
          const inner = toYAML(v, indent + 2);
          if (inner.trim()) out += sp + k + ':\\n' + inner;
        } else {
          out += sp + k + ': ' + escapeYaml(v) + '\\n';
        }
      }
      return out;
    }

    function syncState() {
      const getVal = id => document.getElementById(id)?.value;
      const getChecked = id => document.getElementById(id)?.checked;

      const paths = (getVal('in-group-paths') || '').split(',').map(s => s.trim()).filter(Boolean);
      state.targets.group_selector.group_paths_include = paths;
      state.targets.project_selector.project_name_regex_include = getVal('in-project-regex') || '';
      state.targets.project_selector.archived = !getChecked('in-archived');

      state.policies.push_rules.author_email_regex = getVal('in-push-email') || '';
      state.policies.push_rules.max_file_size = parseInt(getVal('in-push-max-size') || '25', 10);
      state.policies.push_rules.prevent_secrets = !!getChecked('in-push-secrets');
      state.policies.push_rules.reject_unsigned_commits = !!getChecked('in-push-unsigned');
      state.policies.push_rules.commit_committer_check = !!getChecked('in-push-committer');

      state.policies.protected_branches[0].name = getVal('in-branch-name') || 'main';
      state.policies.approval_rules.rules[0].approvals_required = parseInt(getVal('in-approvals-req') || '1', 10);

      const days = parseInt(getVal('in-retention-days') || '0', 10);
      if (days > 0) state.policies.pipeline_retention = { retention_days: days };
      else delete state.policies.pipeline_retention;

      const fw = getVal('in-compliance-fw');
      if (fw && fw !== 'None') state.policies.compliance = { framework_name: fw };
      else delete state.policies.compliance;

      renderEditor();
    }

    function renderEditor() {
      const ed = document.getElementById('policy-editor');
      if (!ed) return;
      if (currentFormat === 'yaml') {
        ed.value = toYAML(state);
      } else {
        ed.value = JSON.stringify(state, null, 2);
      }
    }

    function setFormat(fmt) {
      currentFormat = fmt;
      document.getElementById('tab-yaml').className = fmt === 'yaml' ? 'px-3 py-1 rounded-md text-xs font-semibold bg-indigo-600 text-white transition-all' : 'px-3 py-1 rounded-md text-xs font-semibold text-slate-400 hover:text-white transition-all';
      document.getElementById('tab-json').className = fmt === 'json' ? 'px-3 py-1 rounded-md text-xs font-semibold bg-indigo-600 text-white transition-all' : 'px-3 py-1 rounded-md text-xs font-semibold text-slate-400 hover:text-white transition-all';
      renderEditor();
    }

    function loadPreset(name) {
      if (name === 'soc2') {
        document.getElementById('in-group-paths').value = 'fintech-core, banking';
        document.getElementById('in-push-email').value = '@corp-fintech\\\\.io$';
        document.getElementById('in-push-secrets').checked = true;
        document.getElementById('in-push-unsigned').checked = true;
        document.getElementById('in-retention-days').value = 90;
        document.getElementById('in-compliance-fw').value = 'SOC2';
        document.getElementById('in-approvals-req').value = 2;
      } else if (name === 'trunk') {
        document.getElementById('in-group-paths').value = 'engineering';
        document.getElementById('in-branch-name').value = 'main';
        document.getElementById('in-approvals-req').value = 1;
        document.getElementById('in-compliance-fw').value = 'None';
      } else if (name === 'cleanup') {
        document.getElementById('in-group-paths').value = 'ci-ephemeral, testing';
        document.getElementById('in-retention-days').value = 7;
        document.getElementById('in-compliance-fw').value = 'None';
      } else if (name === 'members') {
        document.getElementById('in-group-paths').value = 'security, audit';
        document.getElementById('in-retention-days').value = 30;
      }
      syncState();
    }

    function copyCode(btn) {
      const ed = document.getElementById('policy-editor');
      if (ed) {
        navigator.clipboard.writeText(ed.value);
        const prev = btn.innerHTML;
        btn.innerHTML = '<span>✔</span> Copied!';
        setTimeout(() => btn.innerHTML = prev, 2000);
      }
    }

    function copyCliCommand(btn) {
      const ext = currentFormat === 'yaml' ? 'yaml' : 'json';
      const cmd = 'gitlab-fleet-governor run -c policy.' + ext + ' --dry-run';
      navigator.clipboard.writeText(cmd);
      const prev = btn.innerHTML;
      btn.innerHTML = '<span>✔</span> Command Copied!';
      setTimeout(() => btn.innerHTML = prev, 2000);
    }

    function downloadPolicy() {
      const ed = document.getElementById('policy-editor');
      const ext = currentFormat === 'yaml' ? 'yaml' : 'json';
      const blob = new Blob([ed.value], { type: 'text/plain' });
      const a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      a.download = 'fleet-policy.' + ext;
      a.click();
    }

    renderEditor();
  </script>
</body>
</html>`;

fs.writeFileSync(path.join(outDir, 'index.html'), indexHtml);
console.log('✔ Built site/index.html (Interactive Policy Studio UI)');

// 2. Build Documentation Pages (`site/docs/*.html`)
for (const doc of docPages) {
  const filePath = path.join(rootDir, doc.file);
  if (!fs.existsSync(filePath)) {
    console.warn(`⚠ Missing doc file: ${doc.file}`);
    continue;
  }
  const rawContent = fs.readFileSync(filePath, 'utf-8');
  const renderedHtml = renderMarkdown(rawContent);

  const pageHtml = `<!DOCTYPE html>
<html lang="en" class="dark">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>${escapeHtml(doc.title)} — GitLab Fleet Governor</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <style>
    @import url('https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800&family=JetBrains+Mono:wght@400;500;600&display=swap');
    body { font-family: 'Inter', sans-serif; }
    pre, code { font-family: 'JetBrains Mono', monospace; }
  </style>
</head>
<body class="bg-slate-950 text-slate-100 min-h-screen flex flex-col antialiased selection:bg-indigo-500 selection:text-white">

  ${getHeaderNav()}

  <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-8 w-full flex-1">
    <div class="flex gap-8">
      ${getSidebarHtml(doc.slug)}
      
      <main class="flex-1 min-w-0 max-w-4xl py-2">
        <article class="prose prose-invert prose-indigo max-w-none">
          ${renderedHtml}
        </article>
      </main>
    </div>
  </div>

  <footer class="bg-slate-950 border-t border-slate-900 py-8 px-4 text-center text-xs text-slate-500">
    <div class="max-w-7xl mx-auto flex flex-col sm:flex-row items-center justify-between gap-4">
      <p>&copy; 2026 GitLab Fleet Governor Contributors & Divmora Team. Apache 2.0 License.</p>
      <div class="flex items-center gap-4">
        <a href="../index.html" class="hover:text-slate-300">Policy Studio</a>
        <a href="../llms.txt" class="hover:text-slate-300">llms.txt</a>
        <a href="https://github.com/divmora/gitlab-fleet-governor" class="hover:text-slate-300">GitHub</a>
      </div>
    </div>
  </footer>

  <script>
    function copyCode(btn, text) {
      navigator.clipboard.writeText(text);
      const prev = btn.textContent;
      btn.textContent = 'Copied!';
      setTimeout(() => btn.textContent = prev, 2000);
    }
  </script>
</body>
</html>`;

  const outFileName = doc.slug === 'overview' ? 'overview.html' : `${doc.slug}.html`;
  fs.writeFileSync(path.join(docsOutDir, outFileName), pageHtml);
  console.log(`✔ Built site/docs/${outFileName}`);
}

// 3. Copy Standard AI & Schema Manifests (`llms.txt`, `llms-full.txt`, `schema.json`)
const filesToCopy = ['llms.txt', 'llms-full.txt', 'schema.json'];
for (const f of filesToCopy) {
  const src = path.join(rootDir, 'docs', f);
  if (fs.existsSync(src)) {
    fs.copyFileSync(src, path.join(outDir, f));
    console.log(`✔ Copied site/${f}`);
  }
}

// 4. Copy sample policies
const sampleYaml = path.join(rootDir, 'examples', 'config.sample.yaml');
const sampleJson = path.join(rootDir, 'examples', 'config.sample.json');
if (fs.existsSync(sampleYaml)) fs.copyFileSync(sampleYaml, path.join(policiesOutDir, 'config.sample.yaml'));
if (fs.existsSync(sampleJson)) fs.copyFileSync(sampleJson, path.join(policiesOutDir, 'config.sample.json'));

console.log(`\n🎉 GitLab Fleet Governor Documentation & Studio Portal generated successfully in '${outDir}'!`);
