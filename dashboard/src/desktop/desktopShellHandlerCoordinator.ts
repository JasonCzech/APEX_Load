import type {
  DesktopRouteActivationKind,
  DesktopShellEventHandlers,
} from "./useDesktopShellEvents";

type HandlerInvocation = () => void | Promise<void>;

// The renderer handshake is owned above AuthGate, while workspace handlers
// only exist when AuthGate mounts AppShell. Keep native intents pending until a
// real handler is registered instead of acknowledging them through a no-op.
export class DesktopShellHandlerCoordinator {
  private workspaceHandlers: DesktopShellEventHandlers | null = null;
  private gatedHandlers: DesktopShellEventHandlers | null = null;
  private readonly waiters = new Set<() => void>();

  constructor(
    private readonly announceActivation: (
      kind: DesktopRouteActivationKind,
    ) => void = () => undefined,
  ) {}

  setWorkspaceHandlers(handlers: DesktopShellEventHandlers): void {
    this.workspaceHandlers = handlers;
    this.notifyWaiters();
  }

  clearWorkspaceHandlers(handlers: DesktopShellEventHandlers): void {
    if (this.workspaceHandlers === handlers) {
      this.workspaceHandlers = null;
    }
  }

  setGatedHandlers = (
    handlers: DesktopShellEventHandlers | null,
  ): void => {
    this.gatedHandlers = handlers;
    if (handlers) {
      // AuthGate installs these during layout, before AppShell's passive
      // cleanup. Retire the old workspace pointer immediately so a native event
      // in that commit gap cannot run against an unmounted shell.
      this.workspaceHandlers = null;
    }
    this.notifyWaiters();
  };

  dispatchMenuAbout = (): Promise<void> =>
    this.invokeWhenReady((handlers) =>
      handlers?.onMenuAbout ? () => handlers.onMenuAbout?.() : undefined,
    );

  dispatchMenuOpenBrowser = (): Promise<void> =>
    this.invokeWhenReady((handlers) => {
      const activeHandler = (
        this.gatedHandlers ?? handlers
      )?.onMenuOpenBrowser;
      return activeHandler
        ? () => activeHandler()
        : undefined;
    });

  dispatchMenuAction = (action: string): Promise<void> =>
    this.invokeWhenReady((handlers) => {
      const activeHandler = (this.gatedHandlers ?? handlers)?.onMenuAction;
      return activeHandler
        ? () => {
            this.announceActivation("menu-action");
            return activeHandler(action);
          }
        : undefined;
    });

  dispatchOpenFile = (path: string): Promise<void> =>
    this.invokeWhenReady((handlers) => {
      const activeHandler = (this.gatedHandlers ?? handlers)?.onOpenFile;
      return activeHandler
        ? () => {
            this.announceActivation("file");
            return activeHandler(path);
          }
        : undefined;
    });

  dispatchOpenURL = (url: string): Promise<void> =>
    this.invokeWhenReady((handlers) => {
      const activeHandler = (this.gatedHandlers ?? handlers)?.onOpenURL;
      return activeHandler
        ? () => {
            this.announceActivation("url");
            return activeHandler(url);
          }
        : undefined;
    });

  dispatchOpenRecent = (route: string): Promise<void> =>
    this.invokeWhenReady((handlers) => {
      const activeHandler = (this.gatedHandlers ?? handlers)?.onOpenRecent;
      return activeHandler
        ? () => {
            this.announceActivation("open-recent");
            return activeHandler(route);
          }
        : undefined;
    });

  dispatchRecentsChanged = (): Promise<void> =>
    this.invokeWhenReady((handlers) => {
      const activeHandler = (
        this.gatedHandlers ?? handlers
      )?.onRecentsChanged;
      return activeHandler ? () => activeHandler() : undefined;
    });

  private async invokeWhenReady(
    select: (
      handlers: DesktopShellEventHandlers | null,
    ) => HandlerInvocation | undefined,
  ): Promise<void> {
    for (;;) {
      const invocation = select(this.workspaceHandlers);
      if (invocation) {
        await invocation();
        return;
      }
      await this.waitForHandlerChange();
    }
  }

  private waitForHandlerChange(): Promise<void> {
    return new Promise((resolve) => {
      this.waiters.add(resolve);
    });
  }

  private notifyWaiters(): void {
    const waiters = Array.from(this.waiters);
    this.waiters.clear();
    waiters.forEach((resolve) => resolve());
  }
}
