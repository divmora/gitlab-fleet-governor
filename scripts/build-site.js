#!/usr/bin/env node

/**
 * GitLab Fleet Governor - Site & AI Manifest Bundler
 * Compiles the React + Vite Studio UI and bundles standard AI and schema manifests into site/
 */

import fs from 'node:fs';
import path from 'node:path';
import { execSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const rootDir = path.resolve(__dirname, '..');
const siteDir = path.join(rootDir, 'site');
const policiesDir = path.join(siteDir, 'policies');

console.log('🚀 Building GitLab Fleet Governor Studio UI (React + Vite + Tailwind)...');
execSync('npm run build --prefix ui', { stdio: 'inherit', cwd: rootDir });

fs.mkdirSync(siteDir, { recursive: true });
fs.mkdirSync(policiesDir, { recursive: true });

// Copy AI and Schema manifests
const manifests = ['llms.txt', 'llms-full.txt', 'schema.json'];
for (const m of manifests) {
  const src = path.join(rootDir, 'docs', m);
  if (fs.existsSync(src)) {
    fs.copyFileSync(src, path.join(siteDir, m));
    console.log(`✔ Bundled site/${m}`);
  }
}

// Copy sample policies
const sampleYaml = path.join(rootDir, 'examples', 'config.sample.yaml');
const sampleJson = path.join(rootDir, 'examples', 'config.sample.json');
if (fs.existsSync(sampleYaml)) fs.copyFileSync(sampleYaml, path.join(policiesDir, 'config.sample.yaml'));
if (fs.existsSync(sampleJson)) fs.copyFileSync(sampleJson, path.join(policiesDir, 'config.sample.json'));

console.log(`\n🎉 GitLab Fleet Governor Studio & AI Portal bundled successfully in '${siteDir}'!`);
