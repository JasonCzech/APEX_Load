export interface FrontendBuildComponentInfo {
  version: string
  buildTime: string
}

export interface FrontendBuildMetadata {
  viteServer: FrontendBuildComponentInfo
  ux: FrontendBuildComponentInfo
}

export const frontendBuildMetadata: FrontendBuildMetadata = Object.freeze({
  viteServer: Object.freeze({
    version: __APEXLOAD_VITE_VERSION__,
    buildTime: __APEXLOAD_VITE_BUILD_TIME__,
  }),
  ux: Object.freeze({
    version: __APEXLOAD_UX_VERSION__,
    buildTime: __APEXLOAD_UX_BUILD_TIME__,
  }),
})
