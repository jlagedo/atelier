import * as React from "react";

// A labelled panel section. Encapsulates the repeated heading recipe
// (the small medium-weight title, plus an optional muted hint beside it) so
// context panels don't re-declare `mb-3 text-xs font-medium` everywhere.
function Section({
  title,
  hint,
  className,
  children,
}: {
  title: string;
  hint?: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section className={className}>
      <div className="mb-3 flex items-center gap-1.5">
        <h2 className="text-sidebar-foreground text-xs font-medium">{title}</h2>
        {hint && <span className="text-muted-foreground/70 text-[10px]">{hint}</span>}
      </div>
      {children}
    </section>
  );
}

export { Section };
