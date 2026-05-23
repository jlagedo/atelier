import { FileText, ChartBar, MagnifyingGlass } from "@phosphor-icons/react";

const EXAMPLES = [
  { icon: FileText, text: "Summarise a CSV and flag anomalies" },
  { icon: ChartBar, text: "Compare actuals vs budget in a workbook" },
  { icon: MagnifyingGlass, text: "Find duplicate records across two files" },
];

export function EmptyState() {
  return (
    <div className="hero-lamp relative flex h-full flex-col items-center justify-center px-6 text-center">
      <div className="animate-in fade-in slide-in-from-bottom-3 flex flex-col items-center duration-700">
        {/* Brass eyebrow — a small rule, the wordmark, a small rule */}
        <div className="text-primary flex items-center gap-2.5">
          <span className="from-primary/0 to-primary/70 h-px w-8 bg-gradient-to-r" />
          <span className="font-mono text-[11px] tracking-[0.34em] uppercase">Atelier</span>
          <span className="from-primary/70 to-primary/0 h-px w-8 bg-gradient-to-r" />
        </div>

        <h2 className="text-foreground font-display mt-5 text-4xl font-semibold tracking-tight">
          A quiet <span className="text-primary italic">workshop</span> for your files
        </h2>
        <p className="text-muted-foreground mt-4 max-w-md text-sm leading-relaxed">
          Ask in plain language. Atelier reads and edits files in your workspace and runs Python
          inside a contained sandbox — every change gated and audited.
        </p>
      </div>

      <div className="mt-11 grid w-full max-w-md gap-2.5">
        {EXAMPLES.map(({ icon: Icon, text }, i) => (
          <button
            key={text}
            type="button"
            style={{ animationDelay: `${160 + i * 90}ms` }}
            className="group border-border bg-card/50 hover:border-primary/45 hover:bg-card hover:shadow-lamp animate-in fade-in slide-in-from-bottom-2 flex items-center gap-3 rounded-xl border px-4 py-3.5 text-left text-sm fill-mode-backwards transition-all duration-500 hover:-translate-y-0.5"
          >
            <span className="bg-primary/10 text-primary ring-primary/15 group-hover:bg-primary/15 flex size-8 shrink-0 items-center justify-center rounded-lg ring-1 transition-colors">
              <Icon weight="bold" className="size-4" />
            </span>
            <span className="text-foreground/90 group-hover:text-foreground transition-colors">
              {text}
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
