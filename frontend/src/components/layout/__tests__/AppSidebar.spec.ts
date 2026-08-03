import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')
const appPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../App.vue')
const appSource = readFileSync(appPath, 'utf8')
const headerPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppHeader.vue')
const headerSource = readFileSync(headerPath, 'utf8')
const routerPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../router/index.ts')
const routerSource = readFileSync(routerPath, 'utf8')
const loginPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../views/auth/LoginView.vue')
const loginSource = readFileSync(loginPath, 'utf8')
const registerPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../views/auth/RegisterView.vue')
const registerSource = readFileSync(registerPath, 'utf8')
const profilePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../views/user/ProfileView.vue')
const profileSource = readFileSync(profilePath, 'utf8')
const groupsPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../views/admin/GroupsView.vue')
const groupsSource = readFileSync(groupsPath, 'utf8')
const settingsPath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../views/admin/SettingsView.vue')
const settingsSource = readFileSync(settingsPath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})

describe('single-user navigation', () => {
  it('removes unused admin management pages', () => {
    expect(componentSource).not.toContain("{ path: '/admin/users'")
    expect(componentSource).not.toContain("{ path: '/admin/announcements'")
    expect(componentSource).not.toContain("{ path: '/admin/promo-codes'")
    expect(componentSource).not.toContain("{ path: '/admin/redeem'")

    expect(routerSource).not.toContain("path: '/admin/users'")
    expect(routerSource).not.toContain("path: '/admin/announcements'")
    expect(routerSource).not.toContain("path: '/admin/promo-codes'")
    expect(routerSource).not.toContain("name: 'AdminRedeem'")
  })

  it('keeps only the admin usage entry for administrators', () => {
    expect(componentSource).toContain("{ path: '/admin/usage'")
    expect(componentSource).toContain("filter((item) => item.path !== '/usage')")
  })

  it('removes personal subscription and redeem entries', () => {
    expect(componentSource).not.toContain("{ path: '/subscriptions'")
    expect(componentSource).not.toContain("{ path: '/redeem'")
  })

  it('removes channel and subscription management pages', () => {
    expect(componentSource).not.toContain("{ path: '/available-channels'")
    expect(componentSource).not.toContain("{ path: '/monitor'")
    expect(componentSource).not.toContain("path: '/admin/channels'")
    expect(componentSource).not.toContain("{ path: '/admin/subscriptions'")

    expect(routerSource).not.toContain("name: 'UserAvailableChannels'")
    expect(routerSource).not.toContain("name: 'ChannelStatus'")
    expect(routerSource).not.toContain("name: 'AdminChannels'")
    expect(routerSource).not.toContain("name: 'AdminChannelMonitor'")
    expect(routerSource).not.toContain("name: 'AdminSubscriptions'")
  })

  it('disables announcement and subscription chrome', () => {
    expect(appSource).not.toContain('AnnouncementPopup')
    expect(appSource).not.toContain('useAnnouncementStore')
    expect(headerSource).not.toContain('AnnouncementBell')
    expect(headerSource).not.toContain('SubscriptionProgressMini')
  })

  it('keeps optional upstream features hidden in the personal edition', () => {
    expect(routerSource).not.toContain("name: 'ModelPlaza'")
    expect(headerSource).not.toContain("path: '/model-plaza'")
    expect(loginSource).toContain('const exposeOptionalAuthFeatures = false')
    expect(registerSource).toContain('const exposeOptionalRegistrationFeatures = false')
    expect(profileSource).toContain('const exposeOptionalProfileFeatures = false')
    expect(groupsSource).toContain('const exposeAdvancedGroupFeatures = false')
    expect(settingsSource).not.toContain('{ key: "agreement" as SettingsTab')
  })
})
