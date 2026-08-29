/**
 * GitLab Fleet Governor - Interactive Policy Studio & Validator Engine
 */

(function () {
  'use strict';

  // Default Policy State
  const defaultState = {
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
        archived: false,
        visibility: "any"
      }
    },
    policies: {
      push_rules: {
        author_email_regex: "@fleetcorp\\.io$",
        branch_name_regex: "^(main|develop|feat/.*)$",
        prevent_secrets: true,
        reject_unsigned_commits: true,
        deny_delete_tag: true,
        max_file_size: 25
      },
      protected_branches: [
        {
          name: "main",
          allowed_to_push: [{ access_level: 0 }],
          allowed_to_merge: [{ access_level: 40 }],
          code_owner_approval_required: true,
          allow_force_push: false
        }
      ],
      approval_rules: {
        settings: {
          allow_author_approval: false,
          allow_committer_approval: false,
          retain_approvals_on_push: true
        },
        rules: [
          {
            name: "AppSec Approval Rule",
            approvals_required: 1,
            user_usernames: ["security-officer"]
          }
        ]
      },
      project_settings: {
        default_branch: "main",
        squash_option: "always",
        merge_method: "rebase_merge",
        only_allow_merge_if_pipeline_succeeds: true,
        only_allow_merge_if_all_discussions_are_resolved: true
      },
      pipeline_retention: {
        retention_days: 30
      },
      compliance: {
        framework_name: "SOC2"
      }
    }
  };

  const templates = {
    soc2: {
      version: "v1",
      settings: { dry_run: true, concurrency: 10 },
      targets: {
        group_selector: { group_paths_include: ["fintech-core"], recursive: true },
        project_selector: { archived: false }
      },
      policies: {
        push_rules: {
          author_email_regex: "@corp-domain\\.com$",
          prevent_secrets: true,
          reject_unsigned_commits: true,
          deny_delete_tag: true,
          commit_committer_check: true,
          member_check: true
        },
        approval_rules: {
          settings: { allow_author_approval: false, allow_committer_approval: false },
          rules: [{ name: "Mandatory Peer Review", approvals_required: 2 }]
        },
        pipeline_retention: { retention_days: 90 },
        compliance: { framework_name: "SOC2" }
      }
    },
    trunk: {
      version: "v1",
      settings: { dry_run: true, concurrency: 10 },
      targets: {
        group_selector: { group_paths_include: ["engineering"], recursive: true },
        project_selector: { archived: false }
      },
      policies: {
        protected_branches: [
          {
            name: "main",
            allowed_to_push: [{ access_level: 0 }],
            allowed_to_merge: [{ access_level: 30 }],
            code_owner_approval_required: true,
            allow_force_push: false
          }
        ],
        project_settings: {
          default_branch: "main",
          squash_option: "always",
          merge_method: "rebase_merge",
          only_allow_merge_if_pipeline_succeeds: true,
          only_allow_merge_if_all_discussions_are_resolved: true
        }
      }
    },
    cleanup: {
      version: "v1",
      settings: { dry_run: true, concurrency: 25 },
      targets: {
        group_selector: { group_paths_include: ["ci-runners", "ephemeral-fleets"], recursive: true },
        project_selector: { archived: false }
      },
      policies: {
        pipeline_retention: { retention_days: 14 }
      }
    }
  };

  let currentState = JSON.parse(JSON.stringify(defaultState));
  let currentFormat = "yaml";

  function escapeYamlString(str) {
    if (typeof str !== "string") return str;
    if (str.includes(":") || str.includes("#") || str.includes("@") || str.includes("^") || str.includes("$") || str.includes("\\")) {
      return `"${str.replace(/"/g, '\\"')}"`;
    }
    return str;
  }

  function toYAML(obj, indent = 0) {
    const spaces = " ".repeat(indent);
    let out = "";

    for (const [key, val] of Object.entries(obj)) {
      if (val === undefined || val === null) continue;

      if (Array.isArray(val)) {
        if (val.length === 0) continue;
        out += `${spaces}${key}:\n`;
        for (const item of val) {
          if (typeof item === "object" && item !== null) {
            const inner = toYAML(item, indent + 4);
            const firstLine = inner.trimStart();
            out += `${spaces}  - ${firstLine}`;
          } else {
            out += `${spaces}  - ${escapeYamlString(item)}\n`;
          }
        }
      } else if (typeof val === "object") {
        const inner = toYAML(val, indent + 2);
        if (inner.trim() !== "") {
          out += `${spaces}${key}:\n${inner}`;
        }
      } else {
        out += `${spaces}${key}: ${escapeYamlString(val)}\n`;
      }
    }
    return out;
  }

  function validatePolicy(obj) {
    const errors = [];
    if (!obj.version) errors.push("Missing required top-level field 'version'");
    if (obj.policies) {
      if (obj.policies.pipeline_retention) {
        const days = obj.policies.pipeline_retention.retention_days;
        if (days !== undefined && (typeof days !== "number" || days < 1 || days > 3650)) {
          errors.push("pipeline_retention.retention_days must be an integer between 1 and 3650");
        }
      }
      if (obj.policies.push_rules && obj.policies.push_rules.author_email_regex) {
        try {
          new RegExp(obj.policies.push_rules.author_email_regex);
        } catch (e) {
          errors.push(`Invalid author_email_regex: ${e.message}`);
        }
      }
      if (obj.policies.push_rules && obj.policies.push_rules.max_file_size) {
        if (obj.policies.push_rules.max_file_size < 0 || obj.policies.push_rules.max_file_size > 1000) {
          errors.push("push_rules.max_file_size must be between 0 and 1000 MB");
        }
      }
    }
    return errors;
  }

  function renderOutput() {
    const editor = document.getElementById("studio-editor");
    const valBox = document.getElementById("studio-validation");
    if (!editor || !valBox) return;

    let content = "";
    if (currentFormat === "yaml") {
      content = toYAML(currentState);
    } else {
      content = JSON.stringify(currentState, null, 2);
    }
    editor.value = content;

    const errors = validatePolicy(currentState);
    if (errors.length === 0) {
      valBox.className = "validation-box validation-valid";
      valBox.innerHTML = `<span>✔</span> <strong>Valid Policy Schema</strong> — 0 errors detected.`;
    } else {
      valBox.className = "validation-box validation-invalid";
      valBox.innerHTML = `<span>✖</span> <strong>Schema Diagnostics:</strong><ul style="margin:0.25rem 0 0 1rem;padding:0;">${errors.map(e => `<li>${e}</li>`).join("")}</ul>`;
    }
  }

  function syncFormFromState() {
    const setVal = (id, val) => {
      const el = document.getElementById(id);
      if (!el) return;
      if (el.type === "checkbox") el.checked = !!val;
      else if (val !== undefined) el.value = val;
    };

    setVal("in-dry-run", currentState.settings?.dry_run !== false);
    setVal("in-concurrency", currentState.settings?.concurrency || 10);
    setVal("in-group-paths", (currentState.targets?.group_selector?.group_paths_include || []).join(", "));
    setVal("in-push-email", currentState.policies?.push_rules?.author_email_regex || "");
    setVal("in-push-secrets", !!currentState.policies?.push_rules?.prevent_secrets);
    setVal("in-push-unsigned", !!currentState.policies?.push_rules?.reject_unsigned_commits);
    setVal("in-push-max-size", currentState.policies?.push_rules?.max_file_size || 25);
    setVal("in-retention-days", currentState.policies?.pipeline_retention?.retention_days || 30);
    setVal("in-compliance-framework", currentState.policies?.compliance?.framework_name || "None");
    setVal("in-mr-squash", currentState.policies?.project_settings?.squash_option || "always");
    setVal("in-mr-method", currentState.policies?.project_settings?.merge_method || "rebase_merge");
    setVal("in-approvals-req", currentState.policies?.approval_rules?.rules?.[0]?.approvals_required || 1);
  }

  function bindEvents() {
    const getEl = id => document.getElementById(id);

    const updateState = () => {
      if (!currentState.settings) currentState.settings = {};
      currentState.settings.dry_run = getEl("in-dry-run")?.checked ?? true;
      currentState.settings.concurrency = parseInt(getEl("in-concurrency")?.value || "10", 10);

      const paths = (getEl("in-group-paths")?.value || "")
        .split(",")
        .map(s => s.trim())
        .filter(Boolean);
      if (!currentState.targets) currentState.targets = {};
      if (!currentState.targets.group_selector) currentState.targets.group_selector = {};
      currentState.targets.group_selector.group_paths_include = paths;
      currentState.targets.group_selector.recursive = true;

      if (!currentState.policies) currentState.policies = {};
      if (!currentState.policies.push_rules) currentState.policies.push_rules = {};
      currentState.policies.push_rules.author_email_regex = getEl("in-push-email")?.value || "";
      currentState.policies.push_rules.prevent_secrets = !!getEl("in-push-secrets")?.checked;
      currentState.policies.push_rules.reject_unsigned_commits = !!getEl("in-push-unsigned")?.checked;
      currentState.policies.push_rules.max_file_size = parseInt(getEl("in-push-max-size")?.value || "25", 10);

      const retDays = parseInt(getEl("in-retention-days")?.value || "0", 10);
      if (retDays > 0) {
        currentState.policies.pipeline_retention = { retention_days: retDays };
      } else {
        delete currentState.policies.pipeline_retention;
      }

      const fw = getEl("in-compliance-framework")?.value;
      if (fw && fw !== "None") {
        currentState.policies.compliance = { framework_name: fw };
      } else {
        delete currentState.policies.compliance;
      }

      if (!currentState.policies.project_settings) currentState.policies.project_settings = {};
      currentState.policies.project_settings.squash_option = getEl("in-mr-squash")?.value || "always";
      currentState.policies.project_settings.merge_method = getEl("in-mr-method")?.value || "rebase_merge";

      renderOutput();
    };

    const inputs = [
      "in-dry-run", "in-concurrency", "in-group-paths", "in-push-email",
      "in-push-secrets", "in-push-unsigned", "in-push-max-size", "in-retention-days",
      "in-compliance-framework", "in-mr-squash", "in-mr-method", "in-approvals-req"
    ];
    inputs.forEach(id => {
      const el = getEl(id);
      if (el) {
        el.addEventListener("input", updateState);
        el.addEventListener("change", updateState);
      }
    });

    getEl("preset-soc2")?.addEventListener("click", () => {
      currentState = JSON.parse(JSON.stringify(templates.soc2));
      syncFormFromState();
      renderOutput();
    });
    getEl("preset-trunk")?.addEventListener("click", () => {
      currentState = JSON.parse(JSON.stringify(templates.trunk));
      syncFormFromState();
      renderOutput();
    });
    getEl("preset-cleanup")?.addEventListener("click", () => {
      currentState = JSON.parse(JSON.stringify(templates.cleanup));
      syncFormFromState();
      renderOutput();
    });

    getEl("btn-format-yaml")?.addEventListener("click", () => {
      currentFormat = "yaml";
      renderOutput();
    });
    getEl("btn-format-json")?.addEventListener("click", () => {
      currentFormat = "json";
      renderOutput();
    });

    getEl("btn-copy-code")?.addEventListener("click", () => {
      const editor = getEl("studio-editor");
      if (editor) {
        navigator.clipboard.writeText(editor.value);
        const btn = getEl("btn-copy-code");
        btn.textContent = "✔ Copied!";
        setTimeout(() => (btn.textContent = "📋 Copy Code"), 2000);
      }
    });

    getEl("btn-download")?.addEventListener("click", () => {
      const editor = getEl("studio-editor");
      if (!editor) return;
      const ext = currentFormat === "yaml" ? "yaml" : "json";
      const blob = new Blob([editor.value], { type: "text/plain" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `fleet-policy.${ext}`;
      a.click();
    });

    getEl("btn-copy-cli")?.addEventListener("click", () => {
      const ext = currentFormat === "yaml" ? "yaml" : "json";
      const cmd = `gitlab-fleet-governor run -c fleet-policy.${ext} --dry-run`;
      navigator.clipboard.writeText(cmd);
      const btn = getEl("btn-copy-cli");
      btn.textContent = "✔ CLI Command Copied!";
      setTimeout(() => (btn.textContent = "💻 Copy CLI Command"), 2000);
    });
  }

  document.addEventListener("DOMContentLoaded", () => {
    if (document.getElementById("studio-editor")) {
      syncFormFromState();
      bindEvents();
      renderOutput();
    }
  });
})();
