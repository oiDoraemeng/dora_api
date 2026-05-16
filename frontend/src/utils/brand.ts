export const DEFAULT_SITE_NAME = 'Dora2API'
const LEGACY_DEFAULT_SITE_NAME = 'Sub2API'

export function resolveDisplaySiteName(siteName?: string | null): string {
  const normalized = siteName?.trim()
  if (!normalized || normalized === LEGACY_DEFAULT_SITE_NAME) {
    return DEFAULT_SITE_NAME
  }
  return normalized
}
