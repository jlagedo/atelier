import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Type, bundled locally (CSP-safe — no CDN).
// Fraunces is the display voice (wordmark + hero headings) — a soft, crafted
// old-style serif that gives Atelier its "workshop" character. IBM Plex carries
// the working text: Sans for body/UI, Mono for code, Serif for in-prose headings.
import "@fontsource/fraunces/500.css";
import "@fontsource/fraunces/600.css";
import "@fontsource/fraunces/900.css";
import "@fontsource/fraunces/600-italic.css";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "@fontsource/ibm-plex-serif/500.css";
import "@fontsource/ibm-plex-serif/600.css";

import { App } from "./App";
import "./index.css";

const container = document.getElementById("root");
if (!container) throw new Error("Root element #root not found");

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
