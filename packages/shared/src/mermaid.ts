// Canonical, network-free, bidirectional mapping between a PipelineDefinition's
// graph (nodes + edges) and a Mermaid `flowchart`. Imported by both the API
// (AI builder) and the web app (Mermaid editor panel). Unit-tested for
// round-trip stability — see mermaid.test.ts.
//
// Lossiness contract: Mermaid carries node *structure* (ids, labels,
// activityType, edges, edge conditions) only. Node `config` values are NOT
// expressible in Mermaid; callers preserve config out-of-band (by node id).

import type { PipelineNode, PipelineEdge, NodeType } from './types';
import type { CatalogEntry } from './catalog-types';

export interface MermaidParseResult {
  nodes: PipelineNode[];
  edges: PipelineEdge[];
  warnings: string[];
}

// ─── definition → mermaid ──────────────────────────────────────────────────

function escapeLabel(s: string): string {
  // Mermaid breaks on double-quotes and parens (we reserve parens for the
  // activityType suffix). Replace them so the label stays a single token.
  return s.replace(/"/g, "'").replace(/[()]/g, ' ').replace(/\s+/g, ' ').trim();
}

export function definitionToMermaid(nodes: PipelineNode[], edges: PipelineEdge[]): string {
  const lines: string[] = ['flowchart LR'];
  for (const n of nodes) {
    const label = escapeLabel(n.label || n.activityType);
    lines.push(`  ${n.id}["${label} (${n.activityType})"]`);
  }
  for (const e of edges) {
    const cond = e.condition ? `|${e.condition.replace(/\|/g, '/')}|` : '';
    lines.push(`  ${e.source} -->${cond ? ' ' + cond : ''} ${e.target}`);
  }
  return lines.join('\n');
}

// ─── mermaid → definition ──────────────────────────────────────────────────

const ID = '[A-Za-z0-9_-]+';

function defaultNodeType(activityType: string): NodeType {
  if (activityType === 'agent.run') return 'agent';
  const head = activityType.split('.')[0];
  if (head === 'flow') return activityType.endsWith('merge') ? 'merge' : 'fork';
  if (head === 'transform') return 'transform';
  if (head === 'sink') return 'sink';
  return 'source';
}

// Linear-time equivalent of /\(([^)]+)\)\s*$/: the trailing "(activityType)".
// Regexes here run on user/model-supplied Mermaid and must not backtrack.
function activityTypeSuffix(label: string): { start: number; activityType: string } | null {
  const end = label.trimEnd().length - 1;
  if (end < 0 || label[end] !== ')') return null;
  const start = label.indexOf('(', label.lastIndexOf(')', end - 1) + 1);
  if (start < 0 || start >= end - 1) return null;
  return { start, activityType: label.slice(start + 1, end) };
}

// Linear-time equivalent of src.replace(/\[[^\]]*\]/g, '').
function stripBracketLabels(src: string): string {
  let out = '';
  let i = 0;
  for (;;) {
    const open = src.indexOf('[', i);
    const close = open < 0 ? -1 : src.indexOf(']', open + 1);
    if (close < 0) return out + src.slice(i);
    out += src.slice(i, open);
    i = close + 1;
  }
}

export function mermaidToDefinition(src: string, catalog: CatalogEntry[]): MermaidParseResult {
  const warnings: string[] = [];
  const byType = new Map(catalog.map(c => [c.activityType, c]));
  const byLabel = new Map(catalog.map(c => [c.label.toLowerCase(), c]));

  // Pass 1 — node declarations: `id["label (activityType)"]` anywhere in source.
  const declRe = new RegExp(`(${ID})\\s*\\[\\s*"([^"]*)"\\s*\\]`, 'g');
  const nodes = new Map<string, PipelineNode>();
  let m: RegExpExecArray | null;
  while ((m = declRe.exec(src))) {
    const [, id, rawLabel] = m;
    const suffix = activityTypeSuffix(rawLabel);
    let activityType = suffix?.activityType.trim() ?? '';
    const labelText = (suffix ? rawLabel.slice(0, suffix.start) : rawLabel).trim();

    let entry = activityType ? byType.get(activityType) : undefined;
    if (!entry && activityType !== 'agent.run') {
      // Fall back to fuzzy match on the human label.
      entry = byLabel.get(labelText.toLowerCase());
      if (entry) activityType = entry.activityType;
    }
    if (!entry) {
      warnings.push(`Node "${id}" (${labelText || activityType || '?'}) does not map to a known connector; set its type in the canvas.`);
    }
    nodes.set(id, {
      id,
      type: entry ? entry.nodeType : defaultNodeType(activityType || 'transform.map'),
      activityType: activityType || (entry?.activityType ?? ''),
      label: labelText || entry?.label || id,
      config: {},
    });
  }

  // Pass 2 — edges. Strip bracket labels first so only ids + arrows remain.
  const stripped = stripBracketLabels(src);
  const edges: PipelineEdge[] = [];
  let edgeN = 0;
  const ensureNode = (id: string) => {
    if (!nodes.has(id)) {
      nodes.set(id, { id, type: 'transform', activityType: '', label: id, config: {} });
      warnings.push(`Node "${id}" is referenced by an edge but never declared.`);
    }
  };
  for (const line of stripped.split('\n')) {
    if (!line.includes('-->')) continue;
    const segs = line.split('-->');
    for (let i = 0; i < segs.length - 1; i++) {
      const left = segs[i].match(new RegExp(`(${ID})\\s*$`));
      const right = segs[i + 1].match(new RegExp(`^\\s*(?:\\|([^|]*)\\|)?\\s*(${ID})`));
      if (!left || !right) continue;
      const source = left[1];
      const condition = right[1]?.trim() || undefined;
      const target = right[2];
      ensureNode(source);
      ensureNode(target);
      edges.push({ id: `e${++edgeN}`, source, target, ...(condition ? { condition } : {}) });
    }
  }

  return { nodes: [...nodes.values()], edges, warnings };
}
