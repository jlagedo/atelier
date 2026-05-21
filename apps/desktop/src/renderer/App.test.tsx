import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { App } from "./App";

describe("App", () => {
  afterEach(() => {
    cleanup();
  });

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
    expect(screen.getAllByText("Product launch ideas").length).toBeGreaterThan(0);
  });

  it("defaults to Chat mode without a work folder", () => {
    render(<App />);

    expect(screen.getByRole("button", { name: "Chat" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByText("Chat session · no work folder")).toBeTruthy();
    expect(screen.queryByText("E:\\dev\\atelier")).toBeNull();
  });

  it("switches to Work mode and shows mapped folder context", () => {
    render(<App />);

    fireEvent.click(screen.getByRole("button", { name: "Work" }));

    expect(screen.getByRole("button", { name: "Work" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getAllByText("atelier").length).toBeGreaterThan(0);
    expect(screen.getAllByText("running").length).toBeGreaterThan(0);
    expect(screen.getAllByText("E:\\dev\\atelier").length).toBeGreaterThan(0);
  });

  it("restores the previous Chat session after switching modes", () => {
    render(<App />);

    fireEvent.click(screen.getByRole("button", { name: "Work" }));
    fireEvent.click(screen.getByRole("button", { name: "Chat" }));

    expect(screen.getAllByText("Product launch ideas").length).toBeGreaterThan(0);
    expect(screen.getByText("Chat session · no work folder")).toBeTruthy();
  });
});
