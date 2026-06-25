import { Fragment } from "react";

export function MarkdownPreview({ source }: { source: string }) {
  const blocks = parseBlocks(source);
  return (
    <div className="markdown-preview">
      {blocks.map((block, index) => renderBlock(block, index))}
    </div>
  );
}

function parseBlocks(source: string) {
  const blocks: string[] = [];
  const lines = source.split(/\n/);
  let current: string[] = [];
  let inFence = false;

  for (const line of lines) {
    if (line.startsWith("```")) {
      current.push(line);
      if (inFence) {
        blocks.push(current.join("\n"));
        current = [];
      }
      inFence = !inFence;
      continue;
    }
    if (!inFence && line.trim() === "") {
      if (current.length > 0) {
        blocks.push(current.join("\n"));
        current = [];
      }
      continue;
    }
    current.push(line);
  }
  if (current.length > 0) blocks.push(current.join("\n"));
  return blocks;
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
  if (/^```/.test(text)) return <pre key={index}><code>{text.replace(/^```[^\n]*\n?/, "").replace(/\n?```$/, "")}</code></pre>;
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
