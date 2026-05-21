import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { cn } from "@/lib/utils";

// Prose styling bound to our design tokens so it reads correctly in both themes
// (rather than relying on prose-invert, which assumes a neutral-grey dark canvas).
const prose = cn(
  "prose prose-sm max-w-none leading-relaxed",
  "prose-headings:font-serif prose-headings:text-foreground prose-headings:font-semibold",
  "prose-p:text-foreground/90 prose-li:text-foreground/90 prose-strong:text-foreground prose-strong:font-semibold",
  "prose-a:text-primary prose-a:font-medium prose-a:no-underline hover:prose-a:underline",
  "prose-code:rounded prose-code:bg-muted prose-code:px-1 prose-code:py-0.5 prose-code:font-mono prose-code:text-[0.85em] prose-code:text-foreground prose-code:before:content-[''] prose-code:after:content-['']",
  "prose-pre:rounded-lg prose-pre:border prose-pre:border-border prose-pre:bg-muted prose-pre:text-foreground",
  "prose-table:text-foreground prose-thead:border-border prose-th:text-foreground prose-th:font-semibold prose-td:border-border prose-tr:border-border",
  "prose-hr:border-border prose-blockquote:border-l-primary/50 prose-blockquote:text-muted-foreground",
);

export function Markdown({ children, className }: { children: string; className?: string }) {
  return (
    <div className={cn(prose, className)}>
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
    </div>
  );
}
