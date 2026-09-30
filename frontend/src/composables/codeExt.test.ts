import { describe, expect, it } from 'vitest'
import { defaultFileName, langToExt } from './codeExt'

// 阶段 3-3：语言名当扩展名会写出 untitled.typescript / untitled.bash（系统不认）。
describe('langToExt（语言名 → 扩展名）', () => {
  it('常见语言按小映射表走', () => {
    expect(langToExt('typescript')).toBe('ts')
    expect(langToExt('ts')).toBe('ts')
    expect(langToExt('tsx')).toBe('tsx')
    expect(langToExt('javascript')).toBe('js')
    expect(langToExt('python')).toBe('py')
    expect(langToExt('go')).toBe('go')
    expect(langToExt('vue')).toBe('vue')
    expect(langToExt('json')).toBe('json')
    expect(langToExt('markdown')).toBe('md')
    expect(langToExt('yaml')).toBe('yml')
    expect(langToExt('bash')).toBe('sh')
    expect(langToExt('shell')).toBe('sh')
  })

  it('大小写与带参数的围栏语言名都认', () => {
    expect(langToExt('TypeScript')).toBe('ts')
    expect(langToExt('go title=main.go')).toBe('go')
    expect(langToExt(' sh ')).toBe('sh')
  })

  it('映射不到就退回 txt，绝不猜', () => {
    expect(langToExt('')).toBe('txt')
    expect(langToExt('weirdlang')).toBe('txt')
    expect(langToExt('text')).toBe('txt')
  })

  it('默认文件名（对话框里可改）用映射后的扩展名', () => {
    expect(defaultFileName('typescript')).toBe('untitled.ts')
    expect(defaultFileName('bash')).toBe('untitled.sh')
    expect(defaultFileName('')).toBe('untitled.txt')
  })
})
