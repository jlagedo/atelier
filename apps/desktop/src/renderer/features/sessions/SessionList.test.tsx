import { describe, it, expect, afterEach, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";

import { SidebarProvider } from "@/components/ui/sidebar";
import { sessionsForMode, type Session } from "@/lib/mock-data";
import { SessionList } from "./SessionList";

const workSession = sessionsForMode("work")[0];
const live = (status: Session["status"]): Session => ({ ...workSession, status });

function renderList(session: Session, onKill = vi.fn(), onDelete = vi.fn()) {
  render(
    <SidebarProvider>
      <SessionList
        mode="work"
        sessions={[session]}
        activeId={session.id}
        onSelect={vi.fn()}
        onKill={onKill}
        onDelete={onDelete}
      />
    </SidebarProvider>,
  );
  return { onKill, onDelete };
}

describe("SessionList work actions", () => {
  afterEach(cleanup);

  it("offers Stop only for live sessions", () => {
    renderList(live("active"));
    expect(screen.getByLabelText("Stop session")).toBeTruthy();

    cleanup();
    renderList(live("inactive"));
    expect(screen.queryByLabelText("Stop session")).toBeNull();
    expect(screen.getByLabelText("Delete session")).toBeTruthy();
  });

  it("kills a session immediately (no confirmation)", () => {
    const { onKill } = renderList(live("running"));
    fireEvent.click(screen.getByLabelText("Stop session"));
    expect(onKill).toHaveBeenCalledWith(workSession.id);
  });

  it("deletes only after confirming in the dialog", () => {
    const { onDelete } = renderList(live("inactive"));
    fireEvent.click(screen.getByLabelText("Delete session"));

    // Dialog is up; nothing deleted yet.
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("Delete this session?")).toBeTruthy();
    expect(onDelete).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete session" }));
    expect(onDelete).toHaveBeenCalledWith(workSession.id);
  });
});
