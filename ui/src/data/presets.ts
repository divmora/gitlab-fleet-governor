import { PolicyConfig } from '../types/policy';

export const PRESETS: Record<string, { name: string; icon: string; description: string; config: PolicyConfig }> = {
  soc2: {
    name: 'SOC 2 Type II Baseline',
    icon: '🛡️',
    description: 'Strict security controls: mandatory branch protections, peer reviews, commit signing, and 90-day pipeline retention.',
    config: {
      version: 'v1',
      settings: {
        dry_run: true,
        concurrency: 10,
        log_level: 'info',
        log_format: 'text',
        report_format: 'table',
        gitlab: {
          base_url: '${GITLAB_BASE_URL:-https://gitlab.com}',
          token: '${GITLAB_TOKEN}',
          rate_limit_rps: 30,
          rate_limit_burst: 50,
        },
      },
      targets: {
        group_selector: {
          group_paths_include: ['enterprise-core', 'fintech-division'],
          recursive: true,
        },
        project_selector: {
          archived: false,
          visibility: 'all',
        },
      },
      policies: {
        push_rules: {
          author_email_regex: '@corp-fintech\\.io$',
          branch_name_regex: '^(main|develop|feat/.*)$',
          prevent_secrets: true,
          reject_unsigned_commits: true,
          commit_committer_check: true,
          member_check: true,
          deny_delete_tag: true,
          max_file_size: 25,
        },
        protected_branches: [
          {
            name: 'main',
            allowed_to_push: [{ access_level: 0 }],
            allowed_to_merge: [{ access_level: 40 }],
            allowed_to_unprotect: [{ access_level: 40 }],
            allow_force_push: false,
            code_owner_approval_required: true,
          },
        ],
        approval_rules: {
          settings: {
            allow_author_approval: false,
            allow_committer_approval: false,
            allow_overrides_to_approver_list_per_merge_request: false,
            retain_approvals_on_push: true,
          },
          rules: [
            {
              name: 'AppSec Required Reviewers',
              approvals_required: 2,
              user_usernames: ['security-lead', 'audit-officer'],
              protected_branch_names: ['main'],
            },
          ],
        },
        project_settings: {
          default_branch: 'main',
          squash_option: 'always',
          merge_method: 'rebase_merge',
          only_allow_merge_if_pipeline_succeeds: true,
          only_allow_merge_if_all_discussions_are_resolved: true,
          keep_latest_artifact: true,
        },
        pipeline_retention: {
          retention_days: 90,
        },
        compliance: {
          framework_name: 'SOC2',
        },
      },
    },
  },
  trunk: {
    name: 'Strict Trunk-Based Flow',
    icon: '🚀',
    description: 'Fast-forward rebase merge strategy with single approver, branch wildcard protection, and artifact preservation.',
    config: {
      version: 'v1',
      settings: {
        dry_run: true,
        concurrency: 15,
      },
      targets: {
        group_selector: {
          group_paths_include: ['platform-engineering'],
          recursive: true,
        },
        project_selector: {
          archived: false,
        },
      },
      policies: {
        protected_branches: [
          {
            name: 'main',
            allowed_to_push: [{ access_level: 0 }],
            allowed_to_merge: [{ access_level: 30 }],
            allow_force_push: false,
            code_owner_approval_required: true,
          },
        ],
        project_settings: {
          default_branch: 'main',
          squash_option: 'always',
          merge_method: 'rebase_merge',
          only_allow_merge_if_pipeline_succeeds: true,
          only_allow_merge_if_all_discussions_are_resolved: true,
          keep_latest_artifact: true,
        },
      },
    },
  },
  cleanup: {
    name: 'Pipeline Retention & Storage Cleanup',
    icon: '🧹',
    description: 'Automated 14-day pipeline cleanup across ephemeral and CI runner fleets to reclaim cluster and storage quota.',
    config: {
      version: 'v1',
      settings: {
        dry_run: true,
        concurrency: 25,
      },
      targets: {
        group_selector: {
          group_paths_include: ['ephemeral-ci', 'integration-tests'],
          recursive: true,
        },
        project_selector: {
          archived: false,
        },
      },
      policies: {
        pipeline_retention: {
          retention_days: 14,
        },
        project_settings: {
          keep_latest_artifact: true,
        },
      },
    },
  },
  members: {
    name: 'Member Access & Expiration Audit',
    icon: '👥',
    description: 'Enforce least-privilege role boundaries (max Maintainer) with mandatory 90-day expiration auditing.',
    config: {
      version: 'v1',
      settings: {
        dry_run: true,
        concurrency: 10,
      },
      targets: {
        group_selector: {
          group_paths_include: ['production-access'],
          recursive: true,
        },
      },
      policies: {
        members: {
          max_access_level: 40,
          enforce_expires_at: true,
          max_expiration_days: 90,
          denied_members: ['external-contractor-temp'],
        },
      },
    },
  },
};
