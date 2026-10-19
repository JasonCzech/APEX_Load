export interface DownloadPlatform {
  value: string;
  label: string;
}

export const CLIENT_DOWNLOAD_PLATFORMS: DownloadPlatform[] = [
  { value: "darwin-arm64", label: "macOS (Apple Silicon)" },
  { value: "windows-amd64", label: "Windows (x64)" },
  { value: "linux-amd64", label: "Linux (x64)" },
  { value: "linux-arm64", label: "Linux (ARM64)" },
];

export const INTEL_MAC_UNSUPPORTED =
  "Intel Macs are not supported. The macOS packages run on Apple Silicon only.";

interface NavigatorUserAgentData {
  platform?: string;
  getHighEntropyValues?: (
    hints: string[],
  ) => Promise<{ platform?: string; architecture?: string; bitness?: string }>;
}

function architectureKind(
  architectureHint: string,
  bitnessHint: string,
): "arm64" | "x64" | null {
  const architecture = architectureHint.toLowerCase();
  const bitness = bitnessHint.toLowerCase();
  if (
    architecture === "arm64" ||
    architecture === "aarch64" ||
    (architecture === "arm" && bitness === "64")
  )
    return "arm64";
  if (
    architecture === "amd64" ||
    architecture === "x64" ||
    architecture === "x86_64" ||
    (architecture === "x86" && bitness === "64")
  )
    return "x64";
  return null;
}

export function architecturePlatform(
  osHint: string,
  architectureHint: string,
  bitnessHint = "",
): string | null {
  const os = osHint.toLowerCase();
  const kind = architectureKind(architectureHint, bitnessHint);
  const isArm64 = kind === "arm64";
  const isX64 = kind === "x64";
  // Intel Macs have no package.
  if (os.includes("mac")) return isArm64 ? "darwin-arm64" : null;
  if (os.includes("win")) return isX64 ? "windows-amd64" : null;
  if (os.includes("linux")) {
    if (isArm64) return "linux-arm64";
    if (isX64) return "linux-amd64";
    return null;
  }
  return null;
}

export interface DetectedClientPlatform {
  platform: string | null;
  // The browser reported an Intel Mac, which has no package.
  intelMac: boolean;
}

export async function detectClientPlatform(
  nav?: Navigator,
): Promise<DetectedClientPlatform> {
  const currentNavigator =
    nav || (typeof navigator === "undefined" ? null : navigator);
  if (!currentNavigator) return { platform: null, intelMac: false };
  const uaData = (
    currentNavigator as Navigator & { userAgentData?: NavigatorUserAgentData }
  ).userAgentData;
  if (uaData?.getHighEntropyValues) {
    try {
      const values = await uaData.getHighEntropyValues([
        "architecture",
        "bitness",
        "platform",
      ]);
      const os = values.platform || uaData.platform || "";
      const detected = architecturePlatform(
        os,
        values.architecture || "",
        values.bitness || "",
      );
      if (detected) return { platform: detected, intelMac: false };
      if (
        os.toLowerCase().includes("mac") &&
        architectureKind(values.architecture || "", values.bitness || "") ===
          "x64"
      )
        return { platform: null, intelMac: true };
    } catch {
      // Fall back to lower-fidelity User-Agent checks below.
    }
  }
  return {
    platform: userAgentPlatform(
      `${currentNavigator.userAgent || ""} ${currentNavigator.platform || ""}`.toLowerCase(),
    ),
    intelMac: false,
  };
}

// The visible user agent's platform, the lower-fidelity fallback.
function userAgentPlatform(ua: string): string | null {
  if (ua.includes("android")) return null;
  if (ua.includes("win")) {
    if (
      ua.includes("win64") ||
      ua.includes("x64") ||
      ua.includes("x86_64") ||
      ua.includes("amd64")
    )
      return "windows-amd64";
    return null;
  }
  if (ua.includes("mac")) return null;
  if (ua.includes("linux")) {
    if (ua.includes("aarch64") || ua.includes("arm64")) return "linux-arm64";
    if (ua.includes("x86_64") || ua.includes("amd64")) return "linux-amd64";
    return null;
  }
  return null;
}

export function platformLabel(value: string): string {
  return (
    CLIENT_DOWNLOAD_PLATFORMS.find((platform) => platform.value === value)
      ?.label || value
  );
}
