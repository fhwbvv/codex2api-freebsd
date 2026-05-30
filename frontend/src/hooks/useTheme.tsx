import { createContext, useCallback, useContext, useEffect, useState } from 'react'
import type { MouseEvent, ReactNode } from 'react'

export type Theme = 'light' | 'dark'
export type ColorTheme =
  | 'default'
  | 'claude'
  | 'chatgpt'
  | 'deepseek'
  | 'graphite'
  | 'aurora'
  | 'rose'
  | 'mono'
  | 'one-dark-pro'
  | 'github-dimmed'
  | 'tokyo-night'
  | 'dracula'
  | 'monokai-pro'
  | 'nord'
  | 'catppuccin'
  | 'gruvbox'
  | 'solarized-light'
  | 'quiet-light'
  | 'ayu-light'
  | 'noctis-lux'

export interface ColorThemeDef {
  id: ColorTheme
  nameKey: string
  descriptionKey: string
  previewPrimary: string
  previewBg: string
  previewSurface: string
  previewMuted: string
}

export const COLOR_THEMES: ColorThemeDef[] = [
  {
    id: 'default',
    nameKey: 'common.theme.default',
    descriptionKey: 'themeSettings.themeDesc.default',
    previewPrimary: 'hsl(211 76% 45%)',
    previewBg: 'hsl(220 20% 97%)',
    previewSurface: 'hsl(0 0% 100%)',
    previewMuted: 'hsl(214 22% 92%)',
  },
  {
    id: 'claude',
    nameKey: 'common.theme.claude',
    descriptionKey: 'themeSettings.themeDesc.claude',
    previewPrimary: 'hsl(16 76% 50%)',
    previewBg: 'hsl(30 20% 97%)',
    previewSurface: 'hsl(30 17% 95%)',
    previewMuted: 'hsl(30 12% 91%)',
  },
  {
    id: 'chatgpt',
    nameKey: 'common.theme.chatgpt',
    descriptionKey: 'themeSettings.themeDesc.chatgpt',
    previewPrimary: 'hsl(160 84% 33%)',
    previewBg: 'hsl(0 0% 100%)',
    previewSurface: 'hsl(0 0% 97%)',
    previewMuted: 'hsl(0 0% 93%)',
  },
  {
    id: 'deepseek',
    nameKey: 'common.theme.deepseek',
    descriptionKey: 'themeSettings.themeDesc.deepseek',
    previewPrimary: 'hsl(220 100% 50%)',
    previewBg: 'hsl(214 30% 97%)',
    previewSurface: 'hsl(0 0% 100%)',
    previewMuted: 'hsl(214 20% 92%)',
  },
  {
    id: 'graphite',
    nameKey: 'common.theme.graphite',
    descriptionKey: 'themeSettings.themeDesc.graphite',
    previewPrimary: 'hsl(194 72% 38%)',
    previewBg: 'hsl(216 18% 96%)',
    previewSurface: 'hsl(0 0% 100%)',
    previewMuted: 'hsl(216 12% 90%)',
  },
  {
    id: 'aurora',
    nameKey: 'common.theme.aurora',
    descriptionKey: 'themeSettings.themeDesc.aurora',
    previewPrimary: 'hsl(173 78% 32%)',
    previewBg: 'hsl(166 33% 96%)',
    previewSurface: 'hsl(0 0% 100%)',
    previewMuted: 'hsl(192 35% 90%)',
  },
  {
    id: 'rose',
    nameKey: 'common.theme.rose',
    descriptionKey: 'themeSettings.themeDesc.rose',
    previewPrimary: 'hsl(347 70% 48%)',
    previewBg: 'hsl(350 35% 97%)',
    previewSurface: 'hsl(0 0% 100%)',
    previewMuted: 'hsl(342 28% 91%)',
  },
  {
    id: 'mono',
    nameKey: 'common.theme.mono',
    descriptionKey: 'themeSettings.themeDesc.mono',
    previewPrimary: 'hsl(222 10% 18%)',
    previewBg: 'hsl(0 0% 98%)',
    previewSurface: 'hsl(0 0% 100%)',
    previewMuted: 'hsl(0 0% 91%)',
  },
  {
    id: 'one-dark-pro',
    nameKey: 'common.theme.oneDarkPro',
    descriptionKey: 'themeSettings.themeDesc.oneDarkPro',
    previewPrimary: 'hsl(207 82% 66%)',
    previewBg: 'hsl(220 13% 18%)',
    previewSurface: 'hsl(220 13% 22%)',
    previewMuted: 'hsl(220 9% 30%)',
  },
  {
    id: 'github-dimmed',
    nameKey: 'common.theme.githubDimmed',
    descriptionKey: 'themeSettings.themeDesc.githubDimmed',
    previewPrimary: 'hsl(212 92% 64%)',
    previewBg: 'hsl(213 13% 16%)',
    previewSurface: 'hsl(214 13% 20%)',
    previewMuted: 'hsl(213 10% 28%)',
  },
  {
    id: 'tokyo-night',
    nameKey: 'common.theme.tokyoNight',
    descriptionKey: 'themeSettings.themeDesc.tokyoNight',
    previewPrimary: 'hsl(217 92% 73%)',
    previewBg: 'hsl(230 24% 16%)',
    previewSurface: 'hsl(229 24% 19%)',
    previewMuted: 'hsl(229 17% 28%)',
  },
  {
    id: 'dracula',
    nameKey: 'common.theme.dracula',
    descriptionKey: 'themeSettings.themeDesc.dracula',
    previewPrimary: 'hsl(265 89% 78%)',
    previewBg: 'hsl(231 15% 18%)',
    previewSurface: 'hsl(232 14% 23%)',
    previewMuted: 'hsl(232 14% 31%)',
  },
  {
    id: 'monokai-pro',
    nameKey: 'common.theme.monokaiPro',
    descriptionKey: 'themeSettings.themeDesc.monokaiPro',
    previewPrimary: 'hsl(349 100% 70%)',
    previewBg: 'hsl(290 6% 17%)',
    previewSurface: 'hsl(285 4% 22%)',
    previewMuted: 'hsl(285 3% 30%)',
  },
  {
    id: 'nord',
    nameKey: 'common.theme.nord',
    descriptionKey: 'themeSettings.themeDesc.nord',
    previewPrimary: 'hsl(193 43% 67%)',
    previewBg: 'hsl(220 16% 22%)',
    previewSurface: 'hsl(222 16% 28%)',
    previewMuted: 'hsl(220 13% 36%)',
  },
  {
    id: 'catppuccin',
    nameKey: 'common.theme.catppuccin',
    descriptionKey: 'themeSettings.themeDesc.catppuccin',
    previewPrimary: 'hsl(267 84% 81%)',
    previewBg: 'hsl(240 21% 15%)',
    previewSurface: 'hsl(240 21% 20%)',
    previewMuted: 'hsl(234 13% 31%)',
  },
  {
    id: 'gruvbox',
    nameKey: 'common.theme.gruvbox',
    descriptionKey: 'themeSettings.themeDesc.gruvbox',
    previewPrimary: 'hsl(40 78% 50%)',
    previewBg: 'hsl(0 0% 16%)',
    previewSurface: 'hsl(20 6% 22%)',
    previewMuted: 'hsl(25 7% 30%)',
  },
  {
    id: 'solarized-light',
    nameKey: 'common.theme.solarizedLight',
    descriptionKey: 'themeSettings.themeDesc.solarizedLight',
    previewPrimary: 'hsl(205 69% 49%)',
    previewBg: 'hsl(44 87% 94%)',
    previewSurface: 'hsl(46 42% 88%)',
    previewMuted: 'hsl(180 7% 80%)',
  },
  {
    id: 'quiet-light',
    nameKey: 'common.theme.quietLight',
    descriptionKey: 'themeSettings.themeDesc.quietLight',
    previewPrimary: 'hsl(283 35% 47%)',
    previewBg: 'hsl(0 0% 96%)',
    previewSurface: 'hsl(0 0% 98%)',
    previewMuted: 'hsl(0 0% 92%)',
  },
  {
    id: 'ayu-light',
    nameKey: 'common.theme.ayuLight',
    descriptionKey: 'themeSettings.themeDesc.ayuLight',
    previewPrimary: 'hsl(28 100% 56%)',
    previewBg: 'hsl(0 0% 98%)',
    previewSurface: 'hsl(0 0% 95%)',
    previewMuted: 'hsl(210 9% 90%)',
  },
  {
    id: 'noctis-lux',
    nameKey: 'common.theme.noctisLux',
    descriptionKey: 'themeSettings.themeDesc.noctisLux',
    previewPrimary: 'hsl(34 92% 44%)',
    previewBg: 'hsl(36 64% 88%)',
    previewSurface: 'hsl(50 84% 93%)',
    previewMuted: 'hsl(45 30% 84%)',
  },
]

const STORAGE_KEY = 'theme'
const COLOR_THEME_STORAGE_KEY = 'color-theme'

function isColorTheme(value: string | null): value is ColorTheme {
  return COLOR_THEMES.some((theme) => theme.id === value)
}

function getInitialTheme(): Theme {
  const stored = localStorage.getItem(STORAGE_KEY) as Theme | null
  if (stored === 'light' || stored === 'dark') return stored
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function getInitialColorTheme(): ColorTheme {
  const stored = localStorage.getItem(COLOR_THEME_STORAGE_KEY)
  if (isColorTheme(stored)) return stored
  return 'default'
}

function persistTheme(nextTheme: Theme) {
  const root = document.documentElement
  if (nextTheme === 'dark') {
    root.classList.add('dark')
  } else {
    root.classList.remove('dark')
  }
  localStorage.setItem(STORAGE_KEY, nextTheme)
}

function persistColorTheme(nextColorTheme: ColorTheme) {
  const root = document.documentElement
  COLOR_THEMES.forEach((theme) => {
    root.classList.remove(`theme-${theme.id}`)
  })
  root.classList.add(`theme-${nextColorTheme}`)
  localStorage.setItem(COLOR_THEME_STORAGE_KEY, nextColorTheme)
}

interface ThemeContextValue {
  theme: Theme
  setTheme: (next: Theme, e?: MouseEvent) => void
  toggle: (e?: MouseEvent) => void
  colorTheme: ColorTheme
  setColorTheme: (next: ColorTheme) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

// Provider：把主题状态提升到全局，避免 Layout 与 ThemeSettings 各持一份导致 UI 不同步。
export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(getInitialTheme)
  const [colorTheme, setColorThemeState] = useState<ColorTheme>(getInitialColorTheme)

  useEffect(() => {
    persistTheme(theme)
  }, [theme])

  useEffect(() => {
    persistColorTheme(colorTheme)
  }, [colorTheme])

  const setTheme = useCallback((nextTheme: Theme, e?: MouseEvent) => {
    const root = document.documentElement
    const currentTheme: Theme = root.classList.contains('dark') ? 'dark' : 'light'
    if (currentTheme === nextTheme) return
    localStorage.setItem(STORAGE_KEY, nextTheme)

    // 获取点击坐标（默认左下角）
    const x = e?.clientX ?? 40
    const y = e?.clientY ?? window.innerHeight - 40

    // 计算扩散半径（到最远角的距离）
    const endRadius = Math.hypot(
      Math.max(x, window.innerWidth - x),
      Math.max(y, window.innerHeight - y),
    )

    // 优先使用 View Transition API（Chrome 111+, Safari 18+）
    if (document.startViewTransition) {
      const transition = document.startViewTransition(() => {
        persistTheme(nextTheme)
        setThemeState(nextTheme)
      })

      transition.ready.then(() => {
        root.animate(
          {
            clipPath: [
              `circle(0px at ${x}px ${y}px)`,
              `circle(${endRadius}px at ${x}px ${y}px)`,
            ],
          },
          {
            duration: 500,
            easing: 'ease-out',
            pseudoElement: '::view-transition-new(root)',
          },
        )
      })
    } else {
      // 降级：直接切换，无动画
      persistTheme(nextTheme)
      setThemeState(nextTheme)
    }
  }, [])

  const setColorTheme = useCallback((nextColorTheme: ColorTheme) => {
    persistColorTheme(nextColorTheme)
    setColorThemeState(nextColorTheme)
  }, [])

  const toggle = useCallback((e?: MouseEvent) => {
    const root = document.documentElement
    const nextTheme: Theme = root.classList.contains('dark') ? 'light' : 'dark'
    setTheme(nextTheme, e)
  }, [setTheme])

  return (
    <ThemeContext.Provider value={{ theme, setTheme, toggle, colorTheme, setColorTheme }}>
      {children}
    </ThemeContext.Provider>
  )
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext)
  if (!ctx) {
    throw new Error('useTheme must be used within <ThemeProvider>')
  }
  return ctx
}
