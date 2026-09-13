import type { ReactNode, ReactElement } from 'react';

// Render helpers that make trailing whitespace VISIBLE without modifying the
// underlying content. Used purely for display in diff viewers / code blocks so
// users can spot trailing spaces in YAML values (e.g. multiline application.yml).
// The original strings (and the data sent back to the server) are never changed.

const MIDDLE_DOT = '\u00B7'; // · visible marker for a trailing space

// renderLineWithTrailing takes a single line of text and returns a ReactNode
// where any trailing spaces/tabs are shown as highlighted dot markers.
export function renderLineWithTrailing(line: string): ReactNode {
  // Match trailing spaces/tabs at end of the (single) line.
  const m = line.match(/[ \t]+$/);
  if (!m) return line;
  const head = line.slice(0, line.length - m[0].length);
  const marker = m[0].replace(/\t/g, '\u2192').replace(/ /g, MIDDLE_DOT); // tab -> →, space -> ·
  return (
    <>
      {head}
      <span
        style={{ background: '#fde68a', color: '#92400e', borderRadius: 2 }}
        title={`行尾有 ${m[0].length} 个空白字符`}
      >
        {marker}
      </span>
    </>
  );
}

// diffRenderContent is passed to ReactDiffViewer's renderContent prop. The diff
// viewer feeds it one line's source string at a time. Must return a ReactElement.
export function diffRenderContent(source: string): ReactElement {
  return <>{renderLineWithTrailing(source)}</>;
}

// renderTextWithTrailing renders a full multi-line string as a <pre>-friendly
// node with trailing whitespace highlighted on every line.
export function renderTextWithTrailing(text: string): ReactNode {
  const lines = text.split('\n');
  return (
    <>
      {lines.map((ln, i) => (
        <div key={i} style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
          {renderLineWithTrailing(ln)}
        </div>
      ))}
    </>
  );
}
