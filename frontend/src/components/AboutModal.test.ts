import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, type App } from 'vue'

const h = vi.hoisted(() => ({
  version: '1.2.3',
  checkUpdateCalls: 0,
  updateInfo: {
    hasUpdate: false,
    current: '1.2.3',
    latest: '1.2.3',
  },
}))

vi.mock('../wails', () => ({
  bridge: () => ({
    app: {
      AppVersion: async () => h.version,
      CheckUpdate: async () => {
        h.checkUpdateCalls++
        return h.updateInfo
      },
      ApplyUpdate: async () => {},
    },
    runtime: { EventsOn: () => {} },
  }),
}))

const AboutModal = (await import('./AboutModal.vue')).default

let app: App | null = null

async function mountModal() {
  const host = document.createElement('div')
  document.body.appendChild(host)
  app = createApp(AboutModal)
  app.mount(host)
  await new Promise((r) => setTimeout(r, 0))
  await nextTick()
}

describe('AboutModal 关于面板', () => {
  beforeEach(() => {
    h.version = '1.2.3'
    h.checkUpdateCalls = 0
    h.updateInfo = {
      hasUpdate: false,
      current: '1.2.3',
      latest: '1.2.3',
    }
  })

  afterEach(() => {
    if (app) {
      app.unmount()
      app = null
    }
    document.body.innerHTML = ''
  })

  it('面板显示 mock 的版本号；点「检查更新」调用 CheckUpdate，无更新时显示"已是最新版本"', async () => {
    await mountModal()

    // 1. 面板显示 mock 的版本号
    expect(document.body.textContent).toContain('版本：v1.2.3')

    // 2. 点「检查更新」
    const checkBtn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent?.includes('检查更新'))!
    expect(checkBtn).toBeTruthy()
    checkBtn.click()

    await new Promise((r) => setTimeout(r, 0))
    await nextTick()

    // 3. CheckUpdate 被调用
    expect(h.checkUpdateCalls).toBe(1)
    // 4. 无更新时显示"已是最新版本"
    expect(document.body.textContent).toContain('已是最新版本')
  })
})
