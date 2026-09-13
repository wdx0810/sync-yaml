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
