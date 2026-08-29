import yaml from 'js-yaml';
import { PolicyConfig } from '../types/policy';

export interface ValidationError {
  path: string;
  message: string;
  severity: 'error' | 'warning' | 'info';
  line?: number;
}

export interface ValidationResult {
  isValid: boolean;
  errors: ValidationError[];
  warnings: ValidationError[];
  parsed?: PolicyConfig;
}

const VALID_ACCESS_LEVELS = [0, 5, 10, 20, 30, 40, 50, 60];

export function validatePolicyContent(raw: string, format: 'yaml' | 'json'): ValidationResult {
  const errors: ValidationError[] = [];
  const warnings: ValidationError[] = [];
  let parsed: any;

  if (!raw.trim()) {
    return {
      isValid: false,
      errors: [{ path: 'root', message: 'Policy configuration content is empty.', severity: 'error' }],
      warnings: [],
    };
  }

  // 1. Parse Syntax
  try {
    if (format === 'yaml') {
      parsed = yaml.load(raw);
    } else {
      parsed = JSON.parse(raw);
    }
  } catch (err: any) {
    return {
      isValid: false,
      errors: [
        {
          path: 'syntax',
          message: `${format.toUpperCase()} Parse Error: ${err.message}`,
          severity: 'error',
          line: err.mark?.line,
        },
      ],
      warnings: [],
    };
  }

  if (!parsed || typeof parsed !== 'object') {
    return {
      isValid: false,
      errors: [{ path: 'root', message: 'Policy configuration must be a valid object.', severity: 'error' }],
      warnings: [],
    };
  }

  // 2. Validate Root Version
  if (!parsed.version) {
    errors.push({ path: 'version', message: "Top-level 'version' is required (e.g. 'v1').", severity: 'error' });
  } else if (parsed.version !== 'v1') {
    warnings.push({ path: 'version', message: `Version '${parsed.version}' is unconventional. Expected 'v1'.`, severity: 'warning' });
  }

  // 3. Validate Settings
  if (parsed.settings) {
    const s = parsed.settings;
    if (s.concurrency !== undefined && (s.concurrency < 1 || s.concurrency > 100)) {
      errors.push({ path: 'settings.concurrency', message: 'Concurrency must be between 1 and 100.', severity: 'error' });
    }
    if (s.log_level && !['debug', 'info', 'warn', 'error'].includes(s.log_level)) {
      errors.push({ path: 'settings.log_level', message: "log_level must be one of: 'debug', 'info', 'warn', 'error'.", severity: 'error' });
    }
    if (s.log_format && !['text', 'json'].includes(s.log_format)) {
      errors.push({ path: 'settings.log_format', message: "log_format must be 'text' or 'json'.", severity: 'error' });
    }
    if (s.report_format && !['table', 'summary', 'json', 'csv', 'markdown'].includes(s.report_format)) {
      errors.push({ path: 'settings.report_format', message: "report_format must be 'table', 'summary', 'json', 'csv', or 'markdown'.", severity: 'error' });
    }
    if (s.gitlab?.rate_limit_rps !== undefined && s.gitlab.rate_limit_rps <= 0) {
      errors.push({ path: 'settings.gitlab.rate_limit_rps', message: 'rate_limit_rps must be greater than 0.', severity: 'error' });
    }
  }

  // 4. Validate Target Selectors
  if (parsed.targets) {
    const t = parsed.targets;
    const gs = t.group_selector;
    const ps = t.project_selector;

    if (!gs && !ps) {
      warnings.push({ path: 'targets', message: 'No group_selector or project_selector specified. Fleet will match 0 targets.', severity: 'warning' });
    }

    if (ps?.project_name_regex_include) {
      try {
        new RegExp(ps.project_name_regex_include);
      } catch (e: any) {
        errors.push({ path: 'targets.project_selector.project_name_regex_include', message: `Invalid RE2 regular expression: ${e.message}`, severity: 'error' });
      }
    }
    if (ps?.project_name_regex_exclude) {
      try {
        new RegExp(ps.project_name_regex_exclude);
      } catch (e: any) {
        errors.push({ path: 'targets.project_selector.project_name_regex_exclude', message: `Invalid RE2 regular expression: ${e.message}`, severity: 'error' });
      }
    }
    if (ps?.visibility && !['public', 'internal', 'private', 'any'].includes(ps.visibility)) {
      errors.push({ path: 'targets.project_selector.visibility', message: "visibility must be 'public', 'internal', 'private', or 'any'.", severity: 'error' });
    }
  }

  // 5. Validate Policies Modules
  if (parsed.policies) {
    const p = parsed.policies;

    // Push Rules
    if (p.push_rules) {
      const pr = p.push_rules;
      if (pr.author_email_regex) {
        try {
          new RegExp(pr.author_email_regex);
        } catch (e: any) {
          errors.push({ path: 'policies.push_rules.author_email_regex', message: `Invalid author_email_regex: ${e.message}`, severity: 'error' });
        }
      }
      if (pr.branch_name_regex) {
        try {
          new RegExp(pr.branch_name_regex);
        } catch (e: any) {
          errors.push({ path: 'policies.push_rules.branch_name_regex', message: `Invalid branch_name_regex: ${e.message}`, severity: 'error' });
        }
      }
      if (pr.max_file_size !== undefined && (pr.max_file_size < 0 || pr.max_file_size > 2048)) {
        errors.push({ path: 'policies.push_rules.max_file_size', message: 'max_file_size must be between 0 and 2048 MB.', severity: 'error' });
      }
    }

    // Protected Branches
    if (p.protected_branches && Array.isArray(p.protected_branches)) {
      p.protected_branches.forEach((pb: any, idx: number) => {
        if (!pb.name) {
          errors.push({ path: `policies.protected_branches[${idx}].name`, message: "Protected branch 'name' is required.", severity: 'error' });
        }
        if (pb.allowed_to_push) {
          pb.allowed_to_push.forEach((rule: any, rIdx: number) => {
            if (!VALID_ACCESS_LEVELS.includes(rule.access_level)) {
              errors.push({ path: `policies.protected_branches[${idx}].allowed_to_push[${rIdx}].access_level`, message: `Invalid access_level '${rule.access_level}'. Allowed: 0, 5, 10, 20, 30, 40, 50, 60.`, severity: 'error' });
            }
          });
        }
        if (pb.allowed_to_merge) {
          pb.allowed_to_merge.forEach((rule: any, rIdx: number) => {
            if (!VALID_ACCESS_LEVELS.includes(rule.access_level)) {
              errors.push({ path: `policies.protected_branches[${idx}].allowed_to_merge[${rIdx}].access_level`, message: `Invalid access_level '${rule.access_level}'. Allowed: 0, 5, 10, 20, 30, 40, 50, 60.`, severity: 'error' });
            }
          });
        }
      });
    }

    // Approval Rules
    if (p.approval_rules?.rules && Array.isArray(p.approval_rules.rules)) {
      p.approval_rules.rules.forEach((ar: any, idx: number) => {
        if (!ar.name) {
          errors.push({ path: `policies.approval_rules.rules[${idx}].name`, message: 'Approval rule name is required.', severity: 'error' });
        }
        if (ar.approvals_required !== undefined && ar.approvals_required < 0) {
          errors.push({ path: `policies.approval_rules.rules[${idx}].approvals_required`, message: 'approvals_required must be >= 0.', severity: 'error' });
        }
      });
    }

    // Pipeline Retention
    if (p.pipeline_retention) {
      const days = p.pipeline_retention.retention_days;
      if (days === undefined || days < 1 || days > 3650) {
        errors.push({ path: 'policies.pipeline_retention.retention_days', message: 'retention_days must be between 1 and 3650 days.', severity: 'error' });
      }
    }

    // Project Settings
    if (p.project_settings) {
      const ps = p.project_settings;
      if (ps.squash_option && !['always', 'never', 'default_on', 'default_off'].includes(ps.squash_option)) {
        errors.push({ path: 'policies.project_settings.squash_option', message: "squash_option must be 'always', 'never', 'default_on', or 'default_off'.", severity: 'error' });
      }
      if (ps.merge_method && !['merge', 'rebase_merge', 'ff'].includes(ps.merge_method)) {
        errors.push({ path: 'policies.project_settings.merge_method', message: "merge_method must be 'merge', 'rebase_merge', or 'ff'.", severity: 'error' });
      }
    }

    // Webhooks
    if (p.webhooks && Array.isArray(p.webhooks)) {
      p.webhooks.forEach((wh: any, idx: number) => {
        if (!wh.url || (!wh.url.startsWith('http://') && !wh.url.startsWith('https://'))) {
          errors.push({ path: `policies.webhooks[${idx}].url`, message: 'Webhook URL must start with http:// or https://.', severity: 'error' });
        }
      });
    }

    // Members
    if (p.members) {
      if (p.members.max_access_level !== undefined && !VALID_ACCESS_LEVELS.includes(p.members.max_access_level)) {
        errors.push({ path: 'policies.members.max_access_level', message: `Invalid max_access_level '${p.members.max_access_level}'. Allowed: 0, 5, 10, 20, 30, 40, 50, 60.`, severity: 'error' });
      }
    }
  }

  return {
    isValid: errors.length === 0,
    errors,
    warnings,
    parsed: errors.length === 0 ? (parsed as PolicyConfig) : undefined,
  };
}
