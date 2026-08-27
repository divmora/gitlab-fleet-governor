# 🎮 Policy Studio & Interactive Validator

Welcome to the **GitLab Fleet Governor Policy Studio**. Design, customize, and validate production fleet governance policies visually in real-time with instant diagnostic validation.

---

<div class="governor-hero">
  <span class="governor-badge">✨ Live Policy Builder</span>
  <h2 style="margin:0.5rem 0 1rem 0;">Declarative Policy Generator & Schema Validator</h2>
  <p style="max-width:700px;margin:0 auto;font-size:0.95rem;opacity:0.85;">
    Configure fleet targets, branch protections, push rules, retention policies, and compliance baselines. Switch seamlessly between YAML and JSON, validate syntax offline, and export directly to CLI or CI/CD.
  </p>
  <div style="margin-top:1.5rem;display:flex;gap:0.75rem;justify-content:center;flex-wrap:wrap;">
    <button class="studio-btn" id="preset-soc2">🛡️ Preset: SOC 2 Baseline</button>
    <button class="studio-btn studio-btn-secondary" id="preset-trunk">🚀 Preset: Trunk-Based</button>
    <button class="studio-btn studio-btn-secondary" id="preset-cleanup">🧹 Preset: Pipeline Cleanup</button>
  </div>
</div>

<div class="studio-container">
  <!-- Left Panel: Interactive Form Controls -->
  <div class="studio-panel">
    <h3 style="margin-top:0;margin-bottom:1rem;display:flex;align-items:center;gap:0.5rem;">
      <span>⚙️</span> Policy Configuration
    </h3>

    <!-- Section 1: Runtime Settings -->
    <div class="studio-section">
      <div class="studio-section-title"><span>⚡</span> Runtime Settings</div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.75rem;">
        <div class="studio-field">
          <label for="in-concurrency">Worker Concurrency</label>
          <input type="number" id="in-concurrency" class="studio-input" value="10" min="1" max="100">
        </div>
        <div class="studio-field" style="display:flex;align-items:flex-end;">
          <label class="studio-checkbox-label" style="margin-bottom:0.6rem;">
            <input type="checkbox" id="in-dry-run" checked>
            <strong>Dry Run Simulation</strong>
          </label>
        </div>
      </div>
    </div>

    <!-- Section 2: Fleet Targets -->
    <div class="studio-section">
      <div class="studio-section-title"><span>🎯</span> Fleet Target Selectors</div>
      <div class="studio-field">
        <label for="in-group-paths">Group Hierarchy Paths (comma-separated)</label>
        <input type="text" id="in-group-paths" class="studio-input" placeholder="e.g. enterprise-core, fintech-division">
      </div>
    </div>

    <!-- Section 3: Push Rules -->
    <div class="studio-section">
      <div class="studio-section-title"><span>🔒</span> Push Rules Governance</div>
      <div class="studio-field">
        <label for="in-push-email">Author Email Regex (RE2)</label>
        <input type="text" id="in-push-email" class="studio-input" placeholder="e.g. @fleetcorp\.io$">
      </div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.75rem;margin-top:0.5rem;">
        <div class="studio-field">
          <label for="in-push-max-size">Max File Size (MB)</label>
          <input type="number" id="in-push-max-size" class="studio-input" value="25" min="1" max="1000">
        </div>
        <div class="studio-field" style="display:flex;flex-direction:column;justify-content:center;gap:0.35rem;">
          <label class="studio-checkbox-label">
            <input type="checkbox" id="in-push-secrets" checked>
            Prevent Secrets
          </label>
          <label class="studio-checkbox-label">
            <input type="checkbox" id="in-push-unsigned" checked>
            Reject Unsigned Commits
          </label>
        </div>
      </div>
    </div>

    <!-- Section 4: Pipeline Retention & Storage -->
    <div class="studio-section">
      <div class="studio-section-title"><span>🧹</span> Native Pipeline Retention</div>
      <div class="studio-field">
        <label for="in-retention-days">Retention (Days) &rarr; <code>ci_delete_pipelines_in_seconds</code></label>
        <input type="number" id="in-retention-days" class="studio-input" value="30" min="0" max="3650">
        <small style="opacity:0.75;display:block;margin-top:0.25rem;">Set to 0 to disable automated deletion.</small>
      </div>
    </div>

    <!-- Section 5: Merge Requests & Project Settings -->
    <div class="studio-section">
      <div class="studio-section-title"><span>🔀</span> Merge Requests & Project Settings</div>
      <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.75rem;">
        <div class="studio-field">
          <label for="in-mr-squash">Squash Commits</label>
          <select id="in-mr-squash" class="studio-select">
            <option value="always">Always</option>
            <option value="default_on">Default On</option>
            <option value="default_off">Default Off</option>
            <option value="never">Never</option>
          </select>
        </div>
        <div class="studio-field">
          <label for="in-mr-method">Merge Method</label>
          <select id="in-mr-method" class="studio-select">
            <option value="rebase_merge">Fast-Forward (Rebase Merge)</option>
            <option value="merge">Merge Commit</option>
            <option value="ff">Fast-Forward Only</option>
          </select>
        </div>
      </div>
    </div>

    <!-- Section 6: Compliance Framework -->
    <div class="studio-section">
      <div class="studio-section-title"><span>🏛️</span> Compliance Framework</div>
      <div class="studio-field">
        <label for="in-compliance-framework">Assign Framework (GraphQL)</label>
        <select id="in-compliance-framework" class="studio-select">
          <option value="None">None</option>
          <option value="SOC2">SOC 2 Type II</option>
          <option value="HIPAA">HIPAA Security</option>
          <option value="PCI-DSS">PCI-DSS 4.0</option>
          <option value="ISO27001">ISO 27001</option>
        </select>
      </div>
    </div>
  </div>

  <!-- Right Panel: Live Code Editor & Diagnostics -->
  <div class="studio-panel" style="display:flex;flex-direction:column;">
    <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:0.75rem;">
      <h3 style="margin:0;display:flex;align-items:center;gap:0.5rem;">
        <span>📝</span> Generated Policy Document
      </h3>
      <div style="display:flex;gap:0.35rem;">
        <button class="studio-btn studio-btn-secondary" id="btn-format-yaml" style="padding:0.3rem 0.6rem;">YAML</button>
        <button class="studio-btn studio-btn-secondary" id="btn-format-json" style="padding:0.3rem 0.6rem;">JSON</button>
      </div>
    </div>

    <textarea id="studio-editor" class="studio-output-box" spellcheck="false"></textarea>

    <div id="studio-validation" class="validation-box validation-valid">
      <span>✔</span> <strong>Valid Policy Schema</strong> — 0 errors detected.
    </div>

    <div class="studio-actions">
      <button class="studio-btn" id="btn-copy-code">📋 Copy Code</button>
      <button class="studio-btn studio-btn-secondary" id="btn-download">💾 Download File</button>
      <button class="studio-btn studio-btn-secondary" id="btn-copy-cli">💻 Copy CLI Command</button>
    </div>
  </div>
</div>

---

### Executing Your Generated Policy

Once downloaded or copied to `policy.yaml`, execute the policy locally, in Docker, or via AWS Lambda:

=== "Standard CLI"
    ```bash
    # 1. Offline Syntax Validation
    gitlab-fleet-governor validate -c policy.yaml

    # 2. Dry-Run Simulation Diff
    gitlab-fleet-governor run -c policy.yaml --dry-run

    # 3. Apply Fleet Mutations
    gitlab-fleet-governor run -c policy.yaml --dry-run=false
    ```

=== "Docker"
    ```bash
    docker run --rm \
      -v $(pwd)/policy.yaml:/etc/governor/policy.yaml:ro \
      -e GITLAB_TOKEN="glpat-xxxxxxxxxxxx" \
      ghcr.io/divmora/gitlab-fleet-governor:latest \
      run -c /etc/governor/policy.yaml --dry-run
    ```

=== "AWS Lambda Direct Invocation"
    ```bash
    aws lambda invoke \
      --function-name gitlab-fleet-governor \
      --payload fileb://policy.json \
      response.json
    ```
