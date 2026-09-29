#!/usr/bin/env node

/**
 * GitLab Fleet Governor - Documentation & AI Portal Builder
 * Following the OwlFlow Architecture Pattern:
 * 1. Static HTML documentation website inside docs/ for GitHub Pages
 * 2. llms.txt (Standard curated AI sitemap & documentation manifest)
 * 3. llms-full.txt (Consolidated single-file documentation for 1-shot AI scraping)
 * 4. schema.json (Standard JSON Schema for IDE and LLM AST verification)
 * 5. Raw markdown mirror (.md endpoints) for direct AI fetching
 * 6. Preserves Vite's React Studio at outDir/index.html
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const rootDir = path.resolve(__dirname, '..');
// Output directory defaults to dist-docs or can be specified via command line (e.g. `ui/dist`)
const targetArg = process.argv[2];
const outDir = targetArg ? path.resolve(process.cwd(), targetArg) : path.resolve(rootDir, 'dist-docs');
const docsDir = path.join(outDir, 'docs');
const docsPoliciesDir = path.join(docsDir, 'policies');
const rootPoliciesDir = path.join(outDir, 'policies');

// Ensure output directories exist
fs.mkdirSync(outDir, { recursive: true });
fs.mkdirSync(docsDir, { recursive: true });
fs.mkdirSync(docsPoliciesDir, { recursive: true });
fs.mkdirSync(rootPoliciesDir, { recursive: true });

const docPages = [
  { file: 'README.md', slug: 'index', title: 'Home / Overview', section: 'Getting Started' },
  { file: 'AGENTS.md', slug: 'agents', title: 'Autonomous Agent Architecture', section: 'Architecture & AI' },
  { file: 'docs/getting-started.md', slug: 'getting-started', title: 'Quickstart & Installation', section: 'Getting Started' },
  { file: 'docs/configuration.md', slug: 'configuration', title: 'Configuration & Schema Reference', section: 'Core References' },
  { file: 'docs/operations.md', slug: 'operations', title: '11 Governance Reconcilers', section: 'Core References' },
  { file: 'docs/lambda.md', slug: 'lambda', title: 'AWS Lambda & Serverless Triggers', section: 'Operations' },
  { file: 'docs/ci-cd.md', slug: 'ci-cd', title: 'CI/CD Pipelines & Automation', section: 'Operations' },
  { file: 'docs/architecture.md', slug: 'architecture', title: 'Engine Architecture & Rate Limiting', section: 'Architecture & AI' },
  { file: 'docs/llms.md', slug: 'llms', title: 'LLM Integration & Prompt Library', section: 'Architecture & AI' },
];

// Helper to escape HTML
function escapeHtml(str) {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

// Lightweight Markdown to HTML parser
function renderMarkdown(md) {
  let html = md;

  // Code blocks with syntax highlight wrapper and copy button
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
  html = html.replace(/^### (.*$)/gim, '<h3 class="text-lg font-bold text-white mt-6 mb-2">$1</h3>');
  html = html.replace(/^## (.*$)/gim, '<h2 class="text-xl font-bold text-indigo-400 mt-8 mb-4 border-b border-slate-800 pb-2">$1</h2>');
  html = html.replace(/^# (.*$)/gim, '<h1 class="text-3xl font-extrabold text-white mb-6">$1</h1>');

  // GitHub Callout Alerts
  html = html.replace(/>\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*\n((?:>.*\n?)*)/gim, (match, type, content) => {
    const text = content.replace(/^>\s?/gm, '').trim();
    const colors = {
      NOTE: 'border-blue-500 bg-blue-500/10 text-blue-300',
      TIP: 'border-emerald-500 bg-emerald-500/10 text-emerald-300',
      IMPORTANT: 'border-indigo-500 bg-indigo-500/10 text-indigo-300',
      WARNING: 'border-amber-500 bg-amber-500/10 text-amber-300',
      CAUTION: 'border-rose-500 bg-rose-500/10 text-rose-300',
    };
    return `<div class="my-4 p-4 border-l-4 rounded-r-lg ${colors[type] || colors.NOTE}">
      <div class="font-bold text-xs uppercase tracking-wider mb-1">${type}</div>
      <div class="text-sm leading-relaxed">${text}</div>
    </div>`;
  });

  // Blockquotes
  html = html.replace(/^\> (.*$)/gim, '<blockquote class="border-l-4 border-indigo-500/50 pl-4 py-1 my-3 text-slate-400 italic bg-slate-900/30 rounded-r">$1</blockquote>');

  // Horizontal rules
  html = html.replace(/^---$/gim, '<hr class="my-8 border-slate-800" />');

  // Unordered list items
  html = html.replace(/^\s*-\s+(.*$)/gim, '<li class="ml-4 list-disc text-slate-300 my-1">$1</li>');

  // Bold & Italic
  html = html.replace(/\*\*([^*]+)\*\*/g, '<strong class="font-bold text-white">$1</strong>');
  html = html.replace(/\*([^*]+)\*/g, '<em class="italic text-slate-300">$1</em>');

  // Tables
  html = html.replace(/\|(.+)\|/g, (match) => {
    const cells = match.split('|').filter(c => c.trim() !== '');
    if (cells.some(c => c.includes('---'))) {
      return '';
    }
    const isHeader = match.includes('---');
    const tag = isHeader ? 'th' : 'td';
    const row = cells.map(c => `<${tag} class="border border-slate-800 px-3.5 py-2 text-sm ${tag === 'th' ? 'bg-slate-900 font-bold text-indigo-300' : 'text-slate-300'}">${c.trim()}</${tag}>`).join('');
    return `<tr>${row}</tr>`;
  });

  // Wrap tables
  html = html.replace(/((?:<tr>.*?<\/tr>\s*)+)/gs, '<div class="overflow-x-auto my-6 rounded-xl border border-slate-800"><table class="w-full border-collapse text-left">$1</table></div>');

  // Paragraphs
  const paragraphs = html.split(/\n\n+/);
  html = paragraphs.map(p => {
    p = p.trim();
    if (!p) return '';
    if (p.startsWith('<h') || p.startsWith('<div') || p.startsWith('<table') || p.startsWith('<hr') || p.startsWith('<li') || p.startsWith('<blockquote')) {
      return p;
    }
    return `<p class="my-3 text-slate-300 leading-relaxed">${p}</p>`;
  }).join('\n');

  return html;
}

function buildHtmlPage(page, contentHtml, rawMdName) {
  const sections = {};
  for (const p of docPages) {
    if (!sections[p.section]) sections[p.section] = [];
    sections[p.section].push(p);
  }

  let navHtml = '';
  for (const [secName, pages] of Object.entries(sections)) {
    navHtml += `<div class="mb-4">
      <h4 class="text-xs font-bold uppercase tracking-wider text-slate-400 mb-2 px-3">${escapeHtml(secName)}</h4>
      <ul class="space-y-1">`;
    for (const p of pages) {
      const isActive = p.slug === page.slug;
      const targetUrl = p.slug === 'index' ? 'index.html' : `${p.slug}.html`;
      navHtml += `<li>
        <a href="${targetUrl}" class="flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm transition-colors ${isActive ? 'bg-indigo-600/20 text-indigo-400 font-semibold border border-indigo-500/30' : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/50'}">
          <span>${escapeHtml(p.title)}</span>
        </a>
      </li>`;
    }
    navHtml += `</ul></div>`;
  }

  return `<!DOCTYPE html>
<html lang="en" class="dark bg-slate-950 text-slate-200">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${escapeHtml(page.title)} — GitLab Fleet Governor</title>
  <meta name="description" content="Production-grade declarative policy-as-code and governance automation engine for GitLab fleets.">
  <script src="https://cdn.tailwindcss.com"></script>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
    ::-webkit-scrollbar { width: 6px; height: 6px; }
    ::-webkit-scrollbar-track { background: #020617; }
    ::-webkit-scrollbar-thumb { background: #1e293b; border-radius: 3px; }
  </style>
</head>
<body class="min-h-screen flex flex-col bg-slate-950 text-slate-200 antialiased selection:bg-indigo-500 selection:text-white">
  <!-- Top Navigation Header -->
  <header class="sticky top-0 z-40 w-full border-b border-slate-800 bg-slate-950/90 backdrop-blur">
    <div class="max-w-7xl mx-auto flex h-14 items-center justify-between px-4 sm:px-6">
      <div class="flex items-center gap-3">
        <a href="index.html" class="flex items-center gap-2 font-bold text-lg text-white hover:opacity-90 transition-opacity">
          <span class="p-1 rounded bg-indigo-500/20 text-indigo-400 border border-indigo-500/30">🦊</span>
          <span>GitLab Fleet Governor <span class="text-xs px-2 py-0.5 rounded bg-slate-800 text-slate-400 font-mono font-normal">Docs</span></span>
        </a>
      </div>
      <div class="flex items-center gap-2.5 sm:gap-3 text-sm">
        <a href="../" class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-bold transition-all text-xs shadow-md shadow-indigo-500/20">
          <span>⚡</span>
          <span>Launch Studio</span>
        </a>
        <a href="../llms.txt" class="flex items-center gap-1.5 px-2.5 py-1 rounded bg-purple-500/10 text-purple-400 border border-purple-500/30 hover:bg-purple-500/20 transition-all font-mono text-xs font-semibold">
          <span>🤖</span>
          <span>llms.txt</span>
        </a>
        <a href="../llms-full.txt" class="hidden sm:flex items-center gap-1.5 px-2.5 py-1 rounded bg-emerald-500/10 text-emerald-400 border border-emerald-500/30 hover:bg-emerald-500/20 transition-all font-mono text-xs font-semibold">
          <span>📄</span>
          <span>llms-full.txt</span>
        </a>
        <a href="${rawMdName}" class="hidden lg:inline-block text-xs text-slate-400 hover:text-indigo-400 font-mono transition-colors">
          Raw Markdown
        </a>
        <a href="https://github.com/divmora/gitlab-fleet-governor" target="_blank" rel="noopener noreferrer" class="text-slate-400 hover:text-white transition-colors">
          GitHub ↗
        </a>
      </div>
    </div>
  </header>

  <!-- Main Container -->
  <div class="max-w-7xl mx-auto w-full flex-1 flex px-4 sm:px-6 py-6 gap-8">
    <!-- Sidebar Navigation -->
    <aside class="w-64 shrink-0 hidden md:block">
      <div class="sticky top-20 flex flex-col gap-6">
        <div>
          ${navHtml}
        </div>
        <div class="p-4 rounded-xl bg-slate-900 border border-slate-800 text-xs text-slate-400 space-y-2">
          <div class="font-bold text-white flex items-center gap-1.5">
            <span>🤖</span> AI Scraper Ready
          </div>
          <p>This site provides <a href="../llms.txt" class="text-indigo-400 underline">llms.txt</a> and <a href="../llms-full.txt" class="text-indigo-400 underline">llms-full.txt</a> for automated LLM ingestion.</p>
        </div>
      </div>
    </aside>

    <!-- Content Area -->
    <main class="flex-1 min-w-0 max-w-4xl pb-16">
      <article class="prose prose-invert max-w-none">
        ${contentHtml}
      </article>
    </main>
  </div>

  <!-- Footer -->
  <footer class="border-t border-slate-800 py-6 text-center text-xs text-slate-500">
    <p>GitLab Fleet Governor — Declarative Policy-as-Code & Fleet Governance for GitLab. Open Source under BSL 1.1.</p>
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
}

// 1. Process all documentation pages
const allDocsTextParts = [];

for (const page of docPages) {
  const filePath = path.join(rootDir, page.file);
  if (!fs.existsSync(filePath)) {
    console.warn(`File not found: ${filePath}`);
    continue;
  }

  const rawMd = fs.readFileSync(filePath, 'utf8');
  allDocsTextParts.push(`\n================================================================================\n# DOCUMENT: ${page.file} (${page.title})\n================================================================================\n\n${rawMd}\n`);

  // Write raw markdown mirror for direct scraping inside docs/
  const rawFileName = page.slug === 'index' ? 'README.md' : `${page.slug}.md`;
  fs.writeFileSync(path.join(docsDir, rawFileName), rawMd, 'utf8');

  // Render HTML inside docs/
  const renderedHtml = renderMarkdown(rawMd);
  const fullHtml = buildHtmlPage(page, renderedHtml, rawFileName);
  const outHtmlPath = path.join(docsDir, page.slug === 'index' ? 'index.html' : `${page.slug}.html`);
  fs.writeFileSync(outHtmlPath, fullHtml, 'utf8');
  console.log(`✔ Built docs/${page.slug === 'index' ? 'index.html' : `${page.slug}.html`}`);
}

// Copy assets (logo, banner, etc.) into docs/assets/ and outDir/assets/
const assetsSrcDir = path.join(rootDir, 'docs', 'assets');
if (fs.existsSync(assetsSrcDir)) {
  const docsAssetsDir = path.join(docsDir, 'assets');
  const rootAssetsDir = path.join(outDir, 'assets');
  fs.mkdirSync(docsAssetsDir, { recursive: true });
  fs.mkdirSync(rootAssetsDir, { recursive: true });
  for (const assetFile of fs.readdirSync(assetsSrcDir)) {
    const srcPath = path.join(assetsSrcDir, assetFile);
    if (fs.statSync(srcPath).isFile()) {
      fs.copyFileSync(srcPath, path.join(docsAssetsDir, assetFile));
      fs.copyFileSync(srcPath, path.join(rootAssetsDir, assetFile));
    }
  }
  console.log(`✔ Copied documentation assets to docs/assets`);
}

// 2. Copy sample policies
const sampleYaml = path.join(rootDir, 'examples', 'config.sample.yaml');
const sampleJson = path.join(rootDir, 'examples', 'config.sample.json');
if (fs.existsSync(sampleYaml)) {
  const content = fs.readFileSync(sampleYaml, 'utf8');
  fs.writeFileSync(path.join(docsPoliciesDir, 'config.sample.yaml'), content, 'utf8');
  fs.writeFileSync(path.join(rootPoliciesDir, 'config.sample.yaml'), content, 'utf8');
  allDocsTextParts.push(`\n================================================================================\n# SAMPLE POLICY: examples/config.sample.yaml\n================================================================================\n\n${content}\n`);
}
if (fs.existsSync(sampleJson)) {
  const content = fs.readFileSync(sampleJson, 'utf8');
  fs.writeFileSync(path.join(docsPoliciesDir, 'config.sample.json'), content, 'utf8');
  fs.writeFileSync(path.join(rootPoliciesDir, 'config.sample.json'), content, 'utf8');
  allDocsTextParts.push(`\n================================================================================\n# SAMPLE POLICY: examples/config.sample.json\n================================================================================\n\n${content}\n`);
}

// 3. Generate llms-full.txt at root of outDir
const llmsFullHeader = `# GitLab Fleet Governor Complete Technical Documentation & Reference Manifest
# Generated for AI Assistants, LLMs, and Automated Scrapers
# Repository: https://github.com/divmora/gitlab-fleet-governor
#
# This file contains the complete, consolidated technical documentation for GitLab Fleet Governor,
# including all 11 governance reconcilers, YAML/JSON policy schemas, discovery engine,
# architecture specifications, and deployment guides.
`;

const fullTextContent = llmsFullHeader + '\n' + allDocsTextParts.join('\n');
fs.writeFileSync(path.join(outDir, 'llms-full.txt'), fullTextContent, 'utf8');
console.log(`✔ Generated llms-full.txt`);

// 4. Copy llms.txt & schema.json from docs/ to outDir root
for (const f of ['llms.txt', 'schema.json']) {
  const src = path.join(rootDir, 'docs', f);
  if (fs.existsSync(src)) {
    fs.copyFileSync(src, path.join(outDir, f));
    console.log(`✔ Bundled ${f}`);
  }
}

// 5. Generate robots.txt
const robotsTxt = `User-agent: *
Allow: /

Sitemap: https://divmora.github.io/gitlab-fleet-governor/sitemap.xml
`;
fs.writeFileSync(path.join(outDir, 'robots.txt'), robotsTxt, 'utf8');
console.log(`✔ Generated robots.txt`);

console.log(`\n🎉 GitLab Fleet Governor Documentation & Studio Portal generated successfully in '${outDir}'!`);
