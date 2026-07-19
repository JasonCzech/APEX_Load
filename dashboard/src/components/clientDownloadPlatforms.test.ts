import { describe, expect, it } from "vitest";

import {
  architecturePlatform,
  detectPlatformFromNavigator,
} from "./clientDownloadPlatforms";

function navigatorWith(userAgent: string, platform = ""): Navigator {
  return { userAgent, platform } as Navigator;
}

describe("client download platform detection", () => {
  it("uses high-entropy macOS architecture when available", () => {
    expect(architecturePlatform("macOS", "arm", "64")).toBe("darwin-arm64");
    expect(architecturePlatform("macOS", "x86", "64")).toBe("darwin-amd64");
  });

  it("requires 64-bit x86 and rejects unsupported high-entropy architectures", () => {
    expect(architecturePlatform("Windows", "x86", "32")).toBeNull();
    expect(architecturePlatform("Windows", "x86", "")).toBeNull();
    expect(architecturePlatform("Windows", "x86", "64")).toBe("windows-amd64");
    expect(architecturePlatform("Linux", "arm", "64")).toBe("linux-arm64");
    expect(architecturePlatform("Linux", "arm", "32")).toBeNull();
    expect(architecturePlatform("Linux", "riscv64", "64")).toBeNull();
  });

  it("requires an architecture choice for the ambiguous macOS compatibility UA", async () => {
    await expect(
      detectPlatformFromNavigator(
        navigatorWith(
          "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
          "MacIntel",
        ),
      ),
    ).resolves.toBeNull();
  });

  it("rejects Android instead of offering a Linux desktop package", async () => {
    await expect(
      detectPlatformFromNavigator(
        navigatorWith(
          "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36",
          "Linux armv8l",
        ),
      ),
    ).resolves.toBeNull();
  });

  it("detects Linux ARM64 from the visible fallback user agent", async () => {
    await expect(
      detectPlatformFromNavigator(
        navigatorWith(
          "Mozilla/5.0 (X11; Linux aarch64; rv:128.0) Gecko/20100101 Firefox/128.0",
          "Linux aarch64",
        ),
      ),
    ).resolves.toBe("linux-arm64");
  });

  it("does not guess amd64 for ambiguous or unsupported Linux user agents", async () => {
    for (const architecture of [
      "",
      "i686",
      "riscv64",
      "ppc64le",
      "s390x",
      "armv8l",
    ]) {
      await expect(
        detectPlatformFromNavigator(
          navigatorWith(
            `Mozilla/5.0 (X11; Linux ${architecture}) AppleWebKit/537.36`,
            `Linux ${architecture}`,
          ),
        ),
      ).resolves.toBeNull();
    }
  });
});
