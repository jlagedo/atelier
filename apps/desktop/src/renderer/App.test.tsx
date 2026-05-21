import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { App } from "./App";

describe("App", () => {
  beforeEach(() => {
    // Stub the preload bridge the renderer reads from window.
    window.atelier = { getVersion: () => Promise.resolve("0.0.0-test") };
  });

  it("renders the Atelier wordmark", () => {
    render(<App />);
    expect(screen.getByText("Atelier")).toBeTruthy();
  });

  it("renders the active conversation title", () => {
    render(<App />);
    expect(screen.getAllByText("Orders reconciliation").length).toBeGreaterThan(0);
  });
});
