import * as yaml from 'js-yaml';

// formatYaml re-serializes a YAML document into a clean, human-readable form:
// - multi-line string values (e.g. a ConfigMap's application.yml) are emitted as
//   block scalars ("|") instead of one long escaped "...\n..." string;
// - trailing whitespace on each line is stripped (block scalars can't preserve it
//   reliably anyway), which also removes the noise that made the raw file look garbled.
//
// If the input can't be parsed as YAML, the original text is returned unchanged so
// we never lose or corrupt content we don't understand.
export function formatYaml(raw: string): { text: string; changed: boolean; ok: boolean } {
  try {
    const doc = yaml.load(raw);
    if (doc === undefined || doc === null || typeof doc !== 'object') {
      return { text: raw, changed: false, ok: false };
    }
    // Strip trailing whitespace from every line inside string values so block
    // scalars serialize cleanly.
    const cleaned = stripTrailingInStrings(doc);
    const dumped = yaml.dump(cleaned, {
      lineWidth: -1,      // don't wrap long lines
      noRefs: true,
    });
    return { text: dumped, changed: dumped !== raw, ok: true };
  } catch {
    return { text: raw, changed: false, ok: false };
  }
}

// parseConfigMap parses a full ConfigMap YAML and returns its metadata and data keys.
export interface ParsedConfigMap {
  ok: boolean;
  kind?: string;
  name?: string;
  namespace?: string;
  dataKeys: string[];      // keys under `data`
  doc?: any;               // the parsed object (for round-trip)
  error?: string;
}

export function parseConfigMap(raw: string): ParsedConfigMap {
  try {
    const doc: any = yaml.load(raw);
    if (!doc || typeof doc !== 'object') {
      return { ok: false, dataKeys: [], error: '无法解析为 YAML 对象' };
    }
    const meta = doc.metadata || {};
    const data = doc.data || {};
    return {
      ok: true,
      kind: doc.kind,
      name: meta.name,
      namespace: meta.namespace,
      dataKeys: Object.keys(data),
      doc,
    };
  } catch (e: any) {
    return { ok: false, dataKeys: [], error: e?.message || 'YAML 解析失败' };
  }
}

// getDataValue returns the string value of a specific data key from a full ConfigMap YAML.
export function getDataValue(raw: string, key: string): string {
  const p = parseConfigMap(raw);
  if (!p.ok || !p.doc?.data) return '';
  const v = p.doc.data[key];
  return typeof v === 'string' ? v : '';
}

// setDataValue replaces one data key's value in a full ConfigMap YAML and returns
// the re-serialized (formatted) full YAML, keeping everything else intact.
export function setDataValue(raw: string, key: string, newVal: string): string {
  const p = parseConfigMap(raw);
  if (!p.ok || !p.doc) return raw;
  if (!p.doc.data) p.doc.data = {};
  // Strip trailing whitespace on each line of the new value for clean block scalars.
  p.doc.data[key] = newVal.indexOf('\n') === -1
    ? newVal
    : newVal.split('\n').map((l) => l.replace(/[ \t]+$/, '')).join('\n');
  const cleaned = stripTrailingInStrings(p.doc);
  return yaml.dump(cleaned, { lineWidth: -1, noRefs: true });
}

// Recursively strip trailing spaces/tabs from each line of every string value.
function stripTrailingInStrings(node: any): any {
  if (typeof node === 'string') {
    if (node.indexOf('\n') === -1) return node;
    return node
      .split('\n')
      .map((line) => line.replace(/[ \t]+$/, ''))
      .join('\n');
  }
  if (Array.isArray(node)) {
    return node.map(stripTrailingInStrings);
  }
  if (node && typeof node === 'object') {
    const out: Record<string, any> = {};
    for (const k of Object.keys(node)) {
      out[k] = stripTrailingInStrings(node[k]);
    }
    return out;
  }
  return node;
}
