import { Fragment } from "react";

export function MarkdownPreview({ source }: { source: string }) {
  const blocks = source.split(/\n{2,}/);
  return (
    <div className="markdown-preview">
      {blocks.map((block, index) => renderBlock(block, index))}
    </div>
  );
}

function renderBlock(block: string, index: number) {
  const text = block.trim();
  if (!text) return null;
  if (text.startsWith("# ")) return <h1 key={index}>{text.slice(2)}</h1>;
  if (text.startsWith("## ")) return <h2 key={index}>{text.slice(3)}</h2>;
  if (text.startsWith("### ")) return <h3 key={index}>{text.slice(4)}</h3>;
  if (text.startsWith("- ")) {
    return <ul key={index}>{text.split(/\n/).map((line, i) => <li key={i}>{inline(line.replace(/^- /, ""))}</li>)}</ul>;
  }
  if (/^```/.test(text)) return <pre key={index}><code>{text.replace(/^```\w*\n?/, "").replace(/```$/, "")}</code></pre>;
  return <p key={index}>{inline(text)}</p>;
}

function inline(text: string) {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`|!\[[^\]]*]\([^)]+\)|\[[^\]]+]\([^)]+\))/g);
  return parts.map((part, index) => {
    if (part.startsWith("**") && part.endsWith("**")) return <strong key={index}>{part.slice(2, -2)}</strong>;
    if (part.startsWith("`") && part.endsWith("`")) return <code key={index}>{part.slice(1, -1)}</code>;
    const image = part.match(/^!\[([^\]]*)]\(([^)]+)\)$/);
    if (image) return <img key={index} alt={image[1]} src={image[2]} />;
    const link = part.match(/^\[([^\]]+)]\(([^)]+)\)$/);
    if (link) return <a key={index} href={link[2]} rel="noreferrer">{link[1]}</a>;
    return <Fragment key={index}>{part}</Fragment>;
  });
}
