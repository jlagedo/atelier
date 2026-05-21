import { FileText, ChartBar, MagnifyingGlass } from "@phosphor-icons/react";

const EXAMPLES = [
  { icon: FileText, text: "Summarise a CSV and flag anomalies" },
  { icon: ChartBar, text: "Compare actuals vs budget in a workbook" },
  { icon: MagnifyingGlass, text: "Find duplicate records across two files" },
];

export function EmptyState() {
  return (
    <div className="flex h-full flex-col items-center justify-center px-6 text-center">
      <p className="text-primary font-mono text-xs tracking-[0.3em] uppercase">Atelier</p>
      <h2 className="text-foreground mt-3 font-serif text-3xl font-semibold tracking-tight">
        A quiet workshop for your files
      </h2>
      <p className="text-muted-foreground mt-3 max-w-md text-sm leading-relaxed">
        Ask in plain language. Atelier reads and edits files in your workspace and runs Python
        inside a contained sandbox — every change gated and audited.
      </p>
      <div className="mt-8 grid w-full max-w-md gap-2">
        {EXAMPLES.map(({ icon: Icon, text }) => (
          <button
            key={text}
            type="button"
            className="border-border bg-card/50 hover:border-primary/40 hover:bg-accent/40 flex items-center gap-3 rounded-lg border px-3.5 py-3 text-left text-sm transition-colors"
          >
            <Icon className="text-primary size-4 shrink-0" />
            <span className="text-foreground/90">{text}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
