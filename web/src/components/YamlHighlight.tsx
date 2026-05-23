import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism';

interface Props {
  code: string;
  showLineNumbers?: boolean;
}

export default function YamlHighlight({ code, showLineNumbers = true }: Props) {
  return (
    <SyntaxHighlighter
      language="yaml"
      style={oneDark}
      showLineNumbers={showLineNumbers}
      customStyle={{ borderRadius: 6, fontSize: 13 }}
    >
      {code}
    </SyntaxHighlighter>
  );
}
