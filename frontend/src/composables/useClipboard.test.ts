import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useClipboard } from './useClipboard'
import { useToast } from './useToast'

// 剪贴板统一壳（此前 6 处各写一遍 writeText + try/catch + toast）。
// 契约：成功可选提示、失败必有交代（默认 error toast；onFail 可接管到表单错误位），
// 绝不静默、也绝不假装成功。
const writeText = vi.fn(async () => {})

beforeEach(() => {
  writeText.mockClear()
  writeText.mockImplementation(async () => {})
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
  useToast().toasts.value = []
})

const toasts = () => useToast().toasts.value

describe('useClipboard 剪贴板统一壳', () => {
  it('成功写入并按文案提示', async () => {
    const ok = await useClipboard().copy('会话正文', { success: '已复制消息' })
    expect(ok).toBe(true)
    expect(writeText).toHaveBeenCalledWith('会话正文')
    expect(toasts().at(-1)).toMatchObject({ kind: 'info', text: '已复制消息' })
  })

  it('成功但未给文案则不提示', async () => {
    const ok = await useClipboard().copy('x')
    expect(ok).toBe(true)
    expect(toasts()).toEqual([])
  })

  it('失败弹默认错误提示，不假装成功', async () => {
    writeText.mockImplementation(async () => {
      throw new Error('denied')
    })
    const ok = await useClipboard().copy('x')
    expect(ok).toBe(false)
    expect(toasts().at(-1)).toMatchObject({ kind: 'error', text: '复制失败：剪贴板不可用' })
  })

  it('失败文案可定制（导出场景提示改用另存为文件）', async () => {
    writeText.mockImplementation(async () => {
      throw new Error('denied')
    })
    await useClipboard().copy('x', { fail: '剪贴板不可用——可改用「另存为文件」' })
    expect(toasts().at(-1)).toMatchObject({ kind: 'error', text: '剪贴板不可用——可改用「另存为文件」' })
  })

  it('onFail 接管失败出路（表单错误位），不再弹默认 toast', async () => {
    writeText.mockImplementation(async () => {
      throw new Error('denied')
    })
    let caught = ''
    const ok = await useClipboard().copy('x', { onFail: () => (caught = '复制失败：当前环境剪贴板不可用') })
    expect(ok).toBe(false)
    expect(caught).toBe('复制失败：当前环境剪贴板不可用')
    expect(toasts()).toEqual([])
  })
})
