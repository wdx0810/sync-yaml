import CodeMirror from '@uiw/react-codemirror';
import { yaml as yamlLang } from '@codemirror/lang-yaml';
import { EditorView } from '@codemirror/view';

interface Props {
  value: string;
  onChange?: (v: string) => void;
  readOnly?: boolean;
  minHeight?: string;
  maxHeight?: string;
}

// YamlEditor is a CodeMirror-based editor with YAML syntax highlighting and
// line numbers. Used by the change-request page for viewing/editing ConfigMap YAML.
export default function YamlEditor({ value, onChange, readOnly, minHeight = '360px', maxHeight = '640px' }: Props) {
  return (
    <CodeMirror
      value={value}
      readOnly={readOnly}
      editable={!readOnly}
      extensions={[yamlLang(), EditorView.lineWrapping]}
      basicSetup={{
        lineNumbers: true,
        highlightActiveLine: !readOnly,
        foldGutter: true,
      }}
      onChange={(v) => onChange?.(v)}
      style={{
        fontSize: 13,
        border: '1px solid #e5e7eb',
        borderRadius: 6,
        overflow: 'auto',
        minHeight,
        maxHeight,
        background: readOnly ? '#f8fafc' : '#fff',
      }}
    />
  );
}
