export interface DownloadPlatform {
  value: string;
  label: string;
}

export const CLIENT_DOWNLOAD_PLATFORMS: DownloadPlatform[] = [
  { value: "darwin-arm64", label: "macOS (Apple Silicon)" },
  { value: "darwin-amd64", label: "macOS (Intel)" },
  { value: "windows-amd64", label: "Windows (x64)" },
  { value: "linux-amd64", label: "Linux (x64)" },
  { value: "linux-arm64", label: "Linux (ARM64)" },
];

interface NavigatorUserAgentData {
  platform?: string;
  getHighEntropyValues?: (
    hints: string[],
  ) => Promise<{ platform?: string; architecture?: string; bitness?: string }>;
}

export function architecturePlatform(
  osHint: string,
  architectureHint: string,
  bitnessHint = "",
): string | null {
  const os = osHint.toLowerCase();
  const architecture = architectureHint.toLowerCase();
  const bitness = bitnessHint.toLowerCase();
  const isArm64 =
    architecture === "arm64" ||
    architecture === "aarch64" ||
    (architecture === "arm" && bitness === "64");
  const isX64 =
    architecture === "amd64" ||
    architecture === "x64" ||
    architecture === "x86_64" ||
    (architecture === "x86" && bitness === "64");
  if (os.includes("mac")) {
    if (isArm64) return "darwin-arm64";
    if (isX64) return "darwin-amd64";
    return null;
  }
  if (os.includes("win")) return isX64 ? "windows-amd64" : null;
  if (os.includes("linux")) {
    if (isArm64) return "linux-arm64";
    if (isX64) return "linux-amd64";
    return null;
  }
  return null;
}

export async function detectPlatformFromNavigator(
  nav?: Navigator,
): Promise<string | null> {
  const currentNavigator =
    nav || (typeof navigator === "undefined" ? null : navigator);
  if (!currentNavigator) return null;
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
      const detected = architecturePlatform(
        values.platform || uaData.platform || "",
        values.architecture || "",
        values.bitness || "",
      );
      if (detected) return detected;
    } catch {
      // Fall back to lower-fidelity User-Agent checks below.
    }
  }
  const ua =
    `${currentNavigator.userAgent || ""} ${currentNavigator.platform || ""}`.toLowerCase();
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
