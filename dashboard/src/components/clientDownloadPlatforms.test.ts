import { describe, expect, it } from "vitest";

import {
  architecturePlatform,
  detectClientPlatform,
} from "./clientDownloadPlatforms";

function navigatorWith(userAgent: string, platform = ""): Navigator {
  return { userAgent, platform } as Navigator;
}

describe("client download platform detection", () => {
  it("uses high-entropy macOS architecture when available", () => {
    expect(architecturePlatform("macOS", "arm", "64")).toBe("darwin-arm64");
  });

  it("offers no package to Intel Macs and says so", async () => {
    expect(architecturePlatform("macOS", "x86", "64")).toBeNull();
    const highEntropy = (architecture: string) =>
      ({
        userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
        platform: "MacIntel",
        userAgentData: {
          platform: "macOS",
          getHighEntropyValues: async () => ({
            platform: "macOS",
            architecture,
            bitness: "64",
          }),
        },
      }) as unknown as Navigator;
    await expect(detectClientPlatform(highEntropy("x86"))).resolves.toEqual({
      platform: null,
      intelMac: true,
    });
    await expect(detectClientPlatform(highEntropy("arm"))).resolves.toEqual({
      platform: "darwin-arm64",
      intelMac: false,
    });
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
      detectClientPlatform(
        navigatorWith(
          "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
          "MacIntel",
        ),
      ),
    ).resolves.toEqual({ platform: null, intelMac: false });
  });

  it("rejects Android instead of offering a Linux desktop package", async () => {
    await expect(
      detectClientPlatform(
        navigatorWith(
          "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36",
          "Linux armv8l",
        ),
      ),
    ).resolves.toEqual({ platform: null, intelMac: false });
  });

  it("detects Linux ARM64 from the visible fallback user agent", async () => {
    await expect(
      detectClientPlatform(
        navigatorWith(
          "Mozilla/5.0 (X11; Linux aarch64; rv:128.0) Gecko/20100101 Firefox/128.0",
          "Linux aarch64",
        ),
      ),
    ).resolves.toEqual({ platform: "linux-arm64", intelMac: false });
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
        detectClientPlatform(
          navigatorWith(
            `Mozilla/5.0 (X11; Linux ${architecture}) AppleWebKit/537.36`,
            `Linux ${architecture}`,
          ),
        ),
      ).resolves.toEqual({ platform: null, intelMac: false });
    }
  });
});
