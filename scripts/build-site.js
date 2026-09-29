#!/usr/bin/env node

/**
 * GitLab Fleet Governor - Site & AI Manifest Bundler
 * Following the OwlFlow Architecture Pattern:
 * Bundles standard AI manifests and schema into the target static output directory (default: ui/dist)
 */

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const rootDir = path.resolve(__dirname, '..');
const targetArg = process.argv[2] || path.join(rootDir, 'ui', 'dist');
const outDir = path.isAbsolute(targetArg) ? targetArg : path.resolve(process.cwd(), targetArg);
const policiesDir = path.join(outDir, 'policies');

fs.mkdirSync(outDir, { recursive: true });
fs.mkdirSync(policiesDir, { recursive: true });

// Copy AI and Schema manifests
const manifests = ['llms.txt', 'llms-full.txt', 'schema.json'];
for (const m of manifests) {
  const src = path.join(rootDir, 'docs', m);
  if (fs.existsSync(src)) {
    fs.copyFileSync(src, path.join(outDir, m));
    console.log(`✔ Bundled ${path.relative(rootDir, path.join(outDir, m))}`);
  }
}

// Copy sample policies
const sampleYaml = path.join(rootDir, 'examples', 'config.sample.yaml');
const sampleJson = path.join(rootDir, 'examples', 'config.sample.json');
if (fs.existsSync(sampleYaml)) {
  fs.copyFileSync(sampleYaml, path.join(policiesDir, 'config.sample.yaml'));
  console.log(`✔ Bundled ${path.relative(rootDir, path.join(policiesDir, 'config.sample.yaml'))}`);
}
if (fs.existsSync(sampleJson)) {
  fs.copyFileSync(sampleJson, path.join(policiesDir, 'config.sample.json'));
  console.log(`✔ Bundled ${path.relative(rootDir, path.join(policiesDir, 'config.sample.json'))}`);
}

console.log(`\n🎉 GitLab Fleet Governor Studio & AI Portal bundled successfully in '${outDir}'!`);
