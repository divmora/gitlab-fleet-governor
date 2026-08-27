import { PolicyConfig } from '../types/policy';

export interface DAGNode {
  id: string;
  label: string;
  category: 'input' | 'discovery' | 'reconciler' | 'engine' | 'report';
  icon: string;
  status: 'active' | 'inactive' | 'error';
  reconcilerKey?: string;
  badge?: string;
  description: string;
  details: Record<string, any>;
  x: number;
  y: number;
}

export interface DAGEdge {
  id: string;
  from: string;
  to: string;
  label?: string;
  animated?: boolean;
}

export interface PolicyDAGGraph {
  nodes: DAGNode[];
  edges: DAGEdge[];
}

export function buildPolicyDAG(config?: PolicyConfig): PolicyDAGGraph {
  const nodes: DAGNode[] = [];
  const edges: DAGEdge[] = [];

  // Root Node: Policy Ingestion
  nodes.push({
    id: 'node-policy',
    label: `Policy Root (${config?.version || 'v1'})`,
    category: 'input',
    icon: '📄',
    status: config ? 'active' : 'inactive',
    badge: 'AST v1',
    description: 'Declarative YAML/JSON configuration input & offline schema validation.',
    details: {
      version: config?.version || 'v1',
      dry_run: config?.settings?.dry_run ?? true,
      concurrency: config?.settings?.concurrency ?? 10,
    },
    x: 30,
    y: 180,
  });

  // Discovery Nodes: Group BFS & Project Regex
  const groupCount = (config?.targets?.group_selector?.group_ids_include?.length || 0) +
    (config?.targets?.group_selector?.group_paths_include?.length || 0);

  nodes.push({
    id: 'node-discovery-group',
    label: 'Group BFS Discovery',
    category: 'discovery',
    icon: '🌳',
    status: config?.targets?.group_selector ? 'active' : 'inactive',
    badge: groupCount > 0 ? `${groupCount} group(s)` : 'BFS Auto',
    description: 'Recursive group hierarchy traversal with cycle detection.',
    details: config?.targets?.group_selector || { note: 'No group selector' },
    x: 220,
    y: 110,
  });

  nodes.push({
    id: 'node-discovery-project',
    label: 'Project Filter Pipeline',
    category: 'discovery',
    icon: '🎯',
    status: config?.targets?.project_selector ? 'active' : 'inactive',
    badge: config?.targets?.project_selector?.visibility || 'Fleet Filter',
    description: 'Filters target fleet by namespace, regex, visibility, and archive status.',
    details: config?.targets?.project_selector || { note: 'No project selector' },
    x: 220,
    y: 250,
  });

  edges.push({ id: 'e1', from: 'node-policy', to: 'node-discovery-group', animated: true });
  edges.push({ id: 'e2', from: 'node-policy', to: 'node-discovery-project', animated: true });

  // Helper to compute reconciler badge
  const getReconcilerBadge = (key: string, data: any): string => {
    if (!data) return 'off';
    switch (key) {
      case 'push_rules': {
        const count = [
          data.author_email_regex,
          data.branch_name_regex,
          data.commit_message_regex,
          data.prevent_secrets,
          data.reject_unsigned_commits,
          data.commit_committer_check,
          data.member_check,
          data.deny_delete_tag,
        ].filter(Boolean).length;
        return `${count} rule(s)`;
      }
      case 'protected_branches':
        return Array.isArray(data) ? `${data.length} branch(es)` : 'active';
      case 'approval_rules':
        return data.rules ? `${data.rules.length} rule(s)` : 'active';
      case 'project_settings':
        return data.merge_method ? `${data.merge_method}` : 'enforced';
      case 'pipeline_retention':
        return data.retention_days ? `${data.retention_days}d cleanup` : 'active';
      case 'compliance':
        return data.framework_id ? `${data.framework_id}` : 'active';
      default:
        return 'active';
    }
  };

  // Reconciler Modules
  const reconcilers = [
    {
      key: 'push_rules',
      label: 'Push Rules Enforcer',
      icon: '🔒',
      data: config?.policies?.push_rules,
    },
    {
      key: 'protected_branches',
      label: 'Protected Branches',
      icon: '🛡️',
      data: config?.policies?.protected_branches,
    },
    {
      key: 'approval_rules',
      label: 'MR Approval Rules',
      icon: '👥',
      data: config?.policies?.approval_rules,
    },
    {
      key: 'project_settings',
      label: 'Project Settings',
      icon: '⚙️',
      data: config?.policies?.project_settings,
    },
    {
      key: 'pipeline_retention',
      label: 'Native Retention',
      icon: '🧹',
      data: config?.policies?.pipeline_retention,
    },
    {
      key: 'compliance',
      label: 'Compliance Framework',
      icon: '🏷️',
      data: config?.policies?.compliance,
    },
  ];

  const reconcilerStartX = 420;
  const startY = 20;
  const gapY = 62;

  reconcilers.forEach((rec, idx) => {
    const nodeId = `node-rec-${rec.key}`;
    const isActive = rec.data !== undefined && rec.data !== null;
    nodes.push({
      id: nodeId,
      label: rec.label,
      category: 'reconciler',
      icon: rec.icon,
      status: isActive ? 'active' : 'inactive',
      reconcilerKey: rec.key,
      badge: getReconcilerBadge(rec.key, rec.data),
      description: `Reconciles ${rec.label} against target GitLab fleet.`,
      details: rec.data || { enabled: false },
      x: reconcilerStartX,
      y: startY + idx * gapY,
    });

    edges.push({ id: `e-g-${rec.key}`, from: 'node-discovery-group', to: nodeId, animated: isActive });
    edges.push({ id: `e-p-${rec.key}`, from: 'node-discovery-project', to: nodeId, animated: isActive });
  });

  // Diff & Mutation Engine Node
  nodes.push({
    id: 'node-engine',
    label: 'Parallel Execution Pool',
    category: 'engine',
    icon: '⚡',
    status: 'active',
    badge: `${config?.settings?.concurrency || 10} workers`,
    description: 'Channel-based bounded concurrency worker pool with jittered backoff retries.',
    details: {
      concurrency: config?.settings?.concurrency || 10,
      dry_run: config?.settings?.dry_run ?? true,
    },
    x: 630,
    y: 180,
  });

  reconcilers.forEach((rec) => {
    edges.push({ id: `e-eng-${rec.key}`, from: `node-rec-${rec.key}`, to: 'node-engine', animated: rec.data !== undefined && rec.data !== null });
  });

  // Report Node
  nodes.push({
    id: 'node-report',
    label: 'Summary & Attestation',
    category: 'report',
    icon: '📊',
    status: 'active',
    badge: config?.settings?.report_format || 'table',
    description: 'Aggregates scanned, matched, applied, skipped, and error totals.',
    details: {
      format: config?.settings?.report_format || 'table',
    },
    x: 820,
    y: 180,
  });

  edges.push({ id: 'e-rep', from: 'node-engine', to: 'node-report', animated: true });

  return { nodes, edges };
}
