import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { useCommandIntent } from "./useCommandIntent";

function Probe({
  intent,
  onIntent,
}: {
  intent: "new-account";
  onIntent: () => void;
}) {
  useCommandIntent(intent, onIntent);
  const location = useLocation();
  return (
    <div data-testid="state">{JSON.stringify(location.state)}</div>
  );
}

describe("useCommandIntent", () => {
  it("fires the callback and clears the intent state", async () => {
    const onIntent = vi.fn();
    render(
      <MemoryRouter
        initialEntries={[
          { pathname: "/accounts", state: { command: "new-account" } },
        ]}
      >
        <Probe intent="new-account" onIntent={onIntent} />
      </MemoryRouter>,
    );

    await waitFor(() => expect(onIntent).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByTestId("state")).toHaveTextContent("null"),
    );
  });

  it("ignores intents targeted at another page", () => {
    const onIntent = vi.fn();
    render(
      <MemoryRouter initialEntries={["/accounts"]}>
        <Probe intent="new-account" onIntent={onIntent} />
      </MemoryRouter>,
    );

    expect(onIntent).not.toHaveBeenCalled();
  });
});
