import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// Type, bundled locally (CSP-safe — no CDN).
// Fraunces is the display voice (wordmark + hero headings) — a soft, crafted
// old-style serif that gives Atelier its "workshop" character. Hanken Grotesk
// is the body/UI voice — a warm "bouba" grotesk that reads friendly at small
// sizes (variable weight + a real italic axis for prose emphasis). IBM Plex
// stays for code (Mono) and in-prose headings (Serif).
import "@fontsource/fraunces/500.css";
import "@fontsource/fraunces/600.css";
import "@fontsource/fraunces/900.css";
import "@fontsource/fraunces/600-italic.css";
import "@fontsource-variable/hanken-grotesk/index.css";
import "@fontsource-variable/hanken-grotesk/wght-italic.css";
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
