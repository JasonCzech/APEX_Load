import { describe, expect, it, vi } from "vitest";

import { DesktopShellHandlerCoordinator } from "./desktopShellHandlerCoordinator";

describe("DesktopShellHandlerCoordinator", () => {
  it("retains workspace intents until a real workspace handler exists", async () => {
    const announceActivation = vi.fn();
    const coordinator = new DesktopShellHandlerCoordinator(announceActivation);
    const onOpenFile = vi.fn().mockResolvedValue(undefined);
    let settled = false;

    const pending = coordinator
      .dispatchOpenFile("/tmp/queued.har")
      .then(() => {
        settled = true;
      });
    await Promise.resolve();
    expect(settled).toBe(false);
    expect(onOpenFile).not.toHaveBeenCalled();
    expect(announceActivation).not.toHaveBeenCalled();

    coordinator.setWorkspaceHandlers({ onOpenFile });
    await pending;

    expect(onOpenFile).toHaveBeenCalledWith("/tmp/queued.har");
    expect(announceActivation).toHaveBeenCalledWith("file");
  });

  it("awaits the authentication browser handler before completing delivery", async () => {
    const coordinator = new DesktopShellHandlerCoordinator();
    let finishLaunch: (() => void) | undefined;
    const onMenuOpenBrowser = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finishLaunch = resolve;
        }),
    );
    coordinator.setGatedHandlers({ onMenuOpenBrowser });

    let settled = false;
    const pending = coordinator.dispatchMenuOpenBrowser().then(() => {
      settled = true;
    });
    await Promise.resolve();

    expect(onMenuOpenBrowser).toHaveBeenCalledTimes(1);
    expect(settled).toBe(false);

    finishLaunch?.();
    await pending;
    expect(settled).toBe(true);
  });

  it("uses the workspace browser context after the auth gate is removed", async () => {
    const coordinator = new DesktopShellHandlerCoordinator();
    const authBrowser = vi.fn();
    const firstWorkspaceBrowser = vi.fn();
    const currentWorkspaceBrowser = vi.fn();
    const firstHandlers = { onMenuOpenBrowser: firstWorkspaceBrowser };
    const currentHandlers = { onMenuOpenBrowser: currentWorkspaceBrowser };

    coordinator.setGatedHandlers({ onMenuOpenBrowser: authBrowser });
    coordinator.setWorkspaceHandlers(firstHandlers);
    coordinator.setWorkspaceHandlers(currentHandlers);
    coordinator.clearWorkspaceHandlers(firstHandlers);
    coordinator.setGatedHandlers(null);

    await coordinator.dispatchMenuOpenBrowser();

    expect(currentWorkspaceBrowser).toHaveBeenCalledTimes(1);
    expect(firstWorkspaceBrowser).not.toHaveBeenCalled();
    expect(authBrowser).not.toHaveBeenCalled();
  });

  it("retires stale workspace handlers as soon as the auth gate mounts", async () => {
    const announceActivation = vi.fn();
    const coordinator = new DesktopShellHandlerCoordinator(announceActivation);
    const workspaceAction = vi.fn();
    const gatedAction = vi.fn();
    const workspaceHandlers = { onMenuAction: workspaceAction };

    coordinator.setWorkspaceHandlers(workspaceHandlers);
    coordinator.setGatedHandlers({ onMenuAction: gatedAction });
    await coordinator.dispatchMenuAction("new-test");
    coordinator.clearWorkspaceHandlers(workspaceHandlers);

    expect(gatedAction).toHaveBeenCalledWith("new-test");
    expect(workspaceAction).not.toHaveBeenCalled();
    expect(announceActivation).toHaveBeenCalledWith("menu-action");
  });

  it("announces an open-recent route before invoking its handler", async () => {
    const order: string[] = [];
    const coordinator = new DesktopShellHandlerCoordinator((kind) => {
      order.push(`announce:${kind}`);
    });
    coordinator.setWorkspaceHandlers({
      onOpenRecent: (route) => {
        order.push(`handler:${route}`);
      },
    });

    await coordinator.dispatchOpenRecent("#/results/run-7");

    expect(order).toEqual([
      "announce:open-recent",
      "handler:#/results/run-7",
    ]);
  });
});
