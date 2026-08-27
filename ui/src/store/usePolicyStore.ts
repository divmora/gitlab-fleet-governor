import { useState } from 'react';
import yaml from 'js-yaml';
import { PolicyConfig } from '../types/policy';
import { PRESETS } from '../data/presets';
import { validatePolicyContent, ValidationResult } from '../engine/validator';
import { buildPolicyDAG, PolicyDAGGraph } from '../engine/dag';

export const RECONCILER_DEFAULTS: Record<string, any> = {
  push_rules: {
    prevent_secrets: true,
    reject_unsigned_commits: true,
    commit_committer_check: true,
    author_email_regex: '@corp\\.io$',
    max_file_size: 25,
  },
  pipeline_retention: {
    retention_days: 30,
  },
  protected_branches: [
    {
      name: 'main',
      allowed_to_push: [{ access_level: 40 }],
      allowed_to_merge: [{ access_level: 30 }],
      allow_force_push: false,
      code_owner_approval_required: true,
    },
  ],
  approval_rules: {
    allow_author_approval: false,
    allow_committer_approval: false,
    rules: [
      {
        name: 'Security Approvers',
        approvals_required: 1,
      },
    ],
  },
  project_settings: {
    squash_option: 'always',
    merge_method: 'rebase_merge',
    only_allow_merge_if_pipeline_succeeds: true,
  },
  compliance: {
    framework_id: 'soc2-type-2',
  },
};

export function usePolicyState() {
  const [format, setFormat] = useState<'yaml' | 'json'>('yaml');
  const [rawYaml, setRawYaml] = useState<string>(
    yaml.dump(PRESETS.soc2.config, { indent: 2, lineWidth: -1, noRefs: true })
  );
  const [rawJson, setRawJson] = useState<string>(
    JSON.stringify(PRESETS.soc2.config, null, 2)
  );
  const [parsedPolicy, setParsedPolicy] = useState<PolicyConfig | undefined>(PRESETS.soc2.config);
  const [validation, setValidation] = useState<ValidationResult>(() =>
    validatePolicyContent(yaml.dump(PRESETS.soc2.config), 'yaml')
  );
  const [dag, setDag] = useState<PolicyDAGGraph>(() => buildPolicyDAG(PRESETS.soc2.config));
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>('node-policy');
  const [activeTab, setActiveTab] = useState<'studio' | 'docs' | 'llm'>('studio');

  const updateRawCode = (code: string, newFormat: 'yaml' | 'json') => {
    if (newFormat === 'yaml') {
      setRawYaml(code);
      const val = validatePolicyContent(code, 'yaml');
      setValidation(val);
      if (val.parsed) {
        setParsedPolicy(val.parsed);
        setRawJson(JSON.stringify(val.parsed, null, 2));
        setDag(buildPolicyDAG(val.parsed));
      }
    } else {
      setRawJson(code);
      const val = validatePolicyContent(code, 'json');
      setValidation(val);
      if (val.parsed) {
        setParsedPolicy(val.parsed);
        setRawYaml(yaml.dump(val.parsed, { indent: 2, lineWidth: -1, noRefs: true }));
        setDag(buildPolicyDAG(val.parsed));
      }
    }
  };

  const toggleReconciler = (reconcilerKey: string, enabled: boolean) => {
    const current = parsedPolicy ? JSON.parse(JSON.stringify(parsedPolicy)) : { version: 'v1', policies: {} };
    if (!current.policies) {
      current.policies = {};
    }

    if (enabled) {
      current.policies[reconcilerKey] = RECONCILER_DEFAULTS[reconcilerKey] || {};
    } else {
      delete current.policies[reconcilerKey];
    }

    const yStr = yaml.dump(current, { indent: 2, lineWidth: -1, noRefs: true });
    const jStr = JSON.stringify(current, null, 2);

    setParsedPolicy(current);
    setRawYaml(yStr);
    setRawJson(jStr);
    setValidation(validatePolicyContent(yStr, 'yaml'));
    setDag(buildPolicyDAG(current));
  };

  const loadPreset = (presetKey: string) => {
    const preset = PRESETS[presetKey];
    if (preset) {
      setParsedPolicy(preset.config);
      const yStr = yaml.dump(preset.config, { indent: 2, lineWidth: -1, noRefs: true });
      const jStr = JSON.stringify(preset.config, null, 2);
      setRawYaml(yStr);
      setRawJson(jStr);
      setValidation(validatePolicyContent(yStr, 'yaml'));
      setDag(buildPolicyDAG(preset.config));
    }
  };

  const switchFormat = (newFormat: 'yaml' | 'json') => {
    setFormat(newFormat);
  };

  return {
    format,
    switchFormat,
    rawCode: format === 'yaml' ? rawYaml : rawJson,
    rawYaml,
    rawJson,
    parsedPolicy,
    validation,
    dag,
    selectedNodeId,
    setSelectedNodeId,
    activeTab,
    setActiveTab,
    updateRawCode,
    toggleReconciler,
    loadPreset,
  };
}
