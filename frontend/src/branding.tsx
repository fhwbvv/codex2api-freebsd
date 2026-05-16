import { createContext, type PropsWithChildren, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { api } from './api'
import type { SiteBranding } from './types'
import defaultLogo from './assets/logo.png'

export const DEFAULT_SITE_NAME = 'CodexProxy'
export const DEFAULT_SITE_LOGO = defaultLogo
const DEFAULT_FAVICON = `${import.meta.env.BASE_URL}favicon.png`

type BrandingContextValue = {
  siteName: string
  siteLogo: string
  faviconHref: string
  refreshBranding: () => Promise<void>
  applyBranding: (branding: Partial<SiteBranding>) => void
}

const BrandingContext = createContext<BrandingContextValue | null>(null)

export function sanitizeBrandingLogo(value?: string | null): string {
  const trimmed = (value ?? '').trim()
  if (!trimmed) return ''
  const lower = trimmed.toLowerCase()
  if (lower.startsWith('data:image/') && lower.includes(';base64,')) return trimmed
  if (lower.startsWith('https://') || lower.startsWith('http://')) return trimmed
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) return trimmed
  return ''
}

function normalizeSiteName(value?: string | null): string {
  const trimmed = (value ?? '').trim()
  return trimmed || DEFAULT_SITE_NAME
}

function setIconLink(rel: string, href: string) {
  let link = document.querySelector<HTMLLinkElement>(`link[rel="${rel}"]`)
  if (!link) {
    link = document.createElement('link')
    link.rel = rel
    document.head.appendChild(link)
  }
  link.href = href
}

export function BrandingProvider({ children }: PropsWithChildren) {
  const [branding, setBranding] = useState<SiteBranding>({
    site_name: DEFAULT_SITE_NAME,
    site_logo: '',
  })

  const applyBranding = useCallback((next: Partial<SiteBranding>) => {
    setBranding((current) => ({
      site_name: normalizeSiteName(next.site_name ?? current.site_name),
      site_logo: next.site_logo === undefined ? current.site_logo : sanitizeBrandingLogo(next.site_logo),
    }))
  }, [])

  const refreshBranding = useCallback(async () => {
    try {
      const next = await api.getBranding()
      applyBranding(next)
    } catch {
      applyBranding({})
    }
  }, [applyBranding])

  useEffect(() => {
    void refreshBranding()
  }, [refreshBranding])

  const siteName = normalizeSiteName(branding.site_name)
  const siteLogo = sanitizeBrandingLogo(branding.site_logo)
  const faviconHref = siteLogo || DEFAULT_FAVICON

  useEffect(() => {
    document.title = `${siteName} 管理后台`
    setIconLink('icon', faviconHref)
    setIconLink('apple-touch-icon', faviconHref)
  }, [faviconHref, siteName])

  const value = useMemo<BrandingContextValue>(() => ({
    siteName,
    siteLogo,
    faviconHref,
    refreshBranding,
    applyBranding,
  }), [applyBranding, faviconHref, refreshBranding, siteLogo, siteName])

  return (
    <BrandingContext.Provider value={value}>
      {children}
    </BrandingContext.Provider>
  )
}

export function useBranding() {
  const context = useContext(BrandingContext)
  if (!context) {
    throw new Error('useBranding must be used inside BrandingProvider')
  }
  return context
}
