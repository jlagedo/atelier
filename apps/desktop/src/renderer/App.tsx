import { useEffect, useState } from "react";
import { ChatView } from "./features/ChatView";

export function App() {
  const [version, setVersion] = useState("…");

  useEffect(() => {
    window.atelier
      .getVersion()
      .then(setVersion)
      .catch(() => setVersion("unknown"));
  }, []);

  return (
    <div className="flex h-screen flex-col bg-neutral-950 text-neutral-100">
      <header className="flex items-center justify-between border-b border-neutral-800 px-4 py-3">
        <h1 className="text-sm font-semibold tracking-wide">Atelier</h1>
        <span className="text-xs text-neutral-500">v{version}</span>
      </header>
      <ChatView />
    </div>
  );
}
