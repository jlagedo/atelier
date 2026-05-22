import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { App } from "./App";

describe("App", () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    // Stub the preload bridge the renderer reads from window. WORK methods are
    // no-ops here; CHAT mode (the asserted-on path) stays on mock data.
    window.atelier = {
      getVersion: () => Promise.resolve("0.0.0-test"),
      work: {
        listSessions: () => Promise.resolve([]),
        hostStatus: () => Promise.resolve(false),
        pickFolder: () => Promise.resolve(null),
        openSession: () => Promise.resolve("s0"),
        sendMessage: () => Promise.resolve(),
        resumeSession: () => Promise.resolve(),
        closeSession: () => Promise.resolve(),
        onStatus: () => () => {},
        onEvent: () => () => {},
        onFiles: () => () => {},
        onHost: () => () => {},
      },
    };
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

  it("switches to Work mode and shows the host-down placeholder with no live session", async () => {
    render(<App />);

    fireEvent.click(screen.getByRole("button", { name: "Work" }));

    expect(screen.getByRole("button", { name: "Work" }).getAttribute("aria-pressed")).toBe("true");
    // WORK mode is live now: the stub reports the host down and no sessions, so the
    // placeholder shows instead of mock folder context.
    expect(await screen.findByText(/host service isn't running/i)).toBeTruthy();
  });

  it("restores the previous Chat session after switching modes", () => {
    render(<App />);

    fireEvent.click(screen.getByRole("button", { name: "Work" }));
    fireEvent.click(screen.getByRole("button", { name: "Chat" }));

    expect(screen.getAllByText("Product launch ideas").length).toBeGreaterThan(0);
    expect(screen.getByText("Chat session · no work folder")).toBeTruthy();
  });
});
