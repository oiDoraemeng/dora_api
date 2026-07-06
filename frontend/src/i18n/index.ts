import { createI18n } from 'vue-i18n'

export type LocaleCode = 'en' | 'zh' | 'zh-Hant' | 'fr' | 'ru' | 'ja' | 'vi'

type LocaleMessages = Record<string, any>

const LOCALE_KEY = 'sub2api_locale'
const DEFAULT_LOCALE: LocaleCode = 'en'

const localeLoaders: Record<LocaleCode, () => Promise<{ default: LocaleMessages }>> = {
  en: () => import('./locales/en'),
  zh: () => import('./locales/zh'),
  'zh-Hant': () => import('./locales/zh-Hant'),
  fr: () => import('./locales/fr'),
  ru: () => import('./locales/ru'),
  ja: () => import('./locales/ja'),
  vi: () => import('./locales/vi')
}

function isLocaleCode(value: string): value is LocaleCode {
  return value === 'en' ||
    value === 'zh' ||
    value === 'zh-Hant' ||
    value === 'fr' ||
    value === 'ru' ||
    value === 'ja' ||
    value === 'vi'
}

function getDefaultLocale(): LocaleCode {
  const saved = localStorage.getItem(LOCALE_KEY)
  if (saved && isLocaleCode(saved)) {
    return saved
  }

  const browserLang = navigator.language.toLowerCase()
  if (browserLang === 'zh-tw' || browserLang === 'zh-hk' || browserLang === 'zh-mo' || browserLang.startsWith('zh-hant')) {
    return 'zh-Hant'
  }
  if (browserLang.startsWith('zh')) {
    return 'zh'
  }

  return DEFAULT_LOCALE
}

export const i18n = createI18n({
  legacy: false,
  locale: getDefaultLocale(),
  fallbackLocale: DEFAULT_LOCALE,
  messages: {},
  // 禁用 HTML 消息警告 - 引导步骤使用富文本内容（driver.js 支持 HTML）
  // 这些内容是内部定义的，不存在 XSS 风险
  warnHtmlMessage: false
})

const loadedLocales = new Set<LocaleCode>()

export async function loadLocaleMessages(locale: LocaleCode): Promise<void> {
  if (loadedLocales.has(locale)) {
    return
  }

  const loader = localeLoaders[locale]
  const module = await loader()
  i18n.global.setLocaleMessage(locale, module.default)
  loadedLocales.add(locale)
}

export async function initI18n(): Promise<void> {
  const current = getLocale()
  await loadLocaleMessages(current)
  document.documentElement.setAttribute('lang', current)
}

export async function setLocale(locale: string): Promise<void> {
  if (!isLocaleCode(locale)) {
    return
  }

  await loadLocaleMessages(locale)
  i18n.global.locale.value = locale
  localStorage.setItem(LOCALE_KEY, locale)
  document.documentElement.setAttribute('lang', locale)

  // 同步更新浏览器页签标题，使其跟随语言切换
  const { resolveRouteDocumentTitle } = await import('@/router/title')
  const { default: router } = await import('@/router')
  const { useAppStore } = await import('@/stores/app')
  const { useAuthStore } = await import('@/stores/auth')
  const { useAdminSettingsStore } = await import('@/stores/adminSettings')
  const route = router.currentRoute.value
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()
  const customMenuItems = [
    ...(appStore.cachedPublicSettings?.custom_menu_items ?? []),
    ...(authStore.isAdmin ? adminSettingsStore.customMenuItems : []),
  ]
  document.title = resolveRouteDocumentTitle(route, appStore.siteName, customMenuItems)
}

export function getLocale(): LocaleCode {
  const current = i18n.global.locale.value
  return isLocaleCode(current) ? current : DEFAULT_LOCALE
}

export function isChineseLocale(locale: string = getLocale()): boolean {
  return locale.toLowerCase().startsWith('zh')
}

export function getIntlLocale(locale: string = getLocale()): string {
  if (locale === 'zh') return 'zh-CN'
  if (locale === 'zh-Hant') return 'zh-TW'
  return locale
}

export const availableLocales = [
  { code: 'zh', region: 'CN', name: '简体中文' },
  { code: 'en', region: 'US', name: 'English' },
  { code: 'zh-Hant', region: 'TW', name: '繁體中文' },
  { code: 'fr', region: 'FR', name: 'Français' },
  { code: 'ru', region: 'RU', name: 'Русский' },
  { code: 'ja', region: 'JP', name: '日本語' },
  { code: 'vi', region: 'VN', name: 'Tiếng Việt' }
] as const

export default i18n
