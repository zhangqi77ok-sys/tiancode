import { describe, expect, it } from 'vitest'
import { parseMcpConfig, parseSkillMarkdown } from './catalogImport'

describe('parseMcpConfig', () => {
  it('读 Claude / Cursor 的 mcpServers', () => {
    const raw = JSON.stringify({
      mcpServers: {
        filesystem: { command: 'npx', args: ['-y', '@modelcontextprotocol/server-filesystem', 'C:\\work'] },
        docs: { url: 'https://example.com/mcp', headers: { Authorization: 'Bearer x' } },
      },
    })
    const got = parseMcpConfig(raw)
    expect(got.error).toBe('')
    expect(got.servers.map((s) => s.name)).toEqual(['filesystem', 'docs'])
    expect(got.servers[0].transport).toBe('stdio')
    expect(got.servers[0].args).toContain('server-filesystem')
    expect(got.servers[1].transport).toBe('http')
    expect(got.servers[1].headers).toBe('Authorization=Bearer x')
  })

  it('读 VS Code 的 servers', () => {
    const raw = JSON.stringify({
      servers: { remote: { type: 'sse', url: 'https://mcp.example/sse' } },
    })
    const got = parseMcpConfig(raw)
    expect(got.servers).toHaveLength(1)
    expect(got.servers[0].transport).toBe('http')
  })

  it('非法 JSON 给出可读错误', () => {
    expect(parseMcpConfig('{')).toMatchObject({ servers: [], error: expect.stringContaining('JSON') })
  })
})

describe('parseSkillMarkdown', () => {
  it('读 SKILL.md 的 frontmatter', () => {
    const raw = `---
name: code-review
description: 审查改动
---
按风险列出问题。`
    const got = parseSkillMarkdown(raw, 'fallback')
    expect(got?.name).toBe('code-review')
    expect(got?.description).toBe('审查改动')
    expect(got?.body).toBe('按风险列出问题。')
  })

  it('没有 frontmatter 时用文件名', () => {
    const got = parseSkillMarkdown('# 步骤\n先读测试。', 'my-skill')
    expect(got?.name).toBe('my-skill')
    expect(got?.body).toContain('先读测试')
  })
})
