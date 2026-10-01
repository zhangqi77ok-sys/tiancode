<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { parseMcpConfig } from '../catalogImport'
import { useDialogs } from '../composables/useDialogs'
import { errText } from '../composables/errText'
import { useCatalogStore, type McpServer } from '../stores/catalog'
import { bridge } from '../wails'
import BaseModal from './BaseModal.vue'

defineEmits<{ (e: 'close'): void }>()
const catalog = useCatalogStore()
const dialogs = useDialogs()
const mode = ref<'list' | 'paste' | 'manual'>('list')
const error = ref('')
const paste = ref('')
const form = ref(blank())

// 面板打开期间 AI 通过对话添加/删除扩展时实时刷新（后端保存成功即广播）
let offChanged: (() => void) | undefined
onMounted(() => {
  const off = bridge().runtime.EventsOn('extensions:changed', () => {
    void catalog.load()
  })
  offChanged = off as unknown as (() => void) | undefined
})
onBeforeUnmount(() => offChanged?.())

function blank() {
  return {
    id: '',
    name: '',
    transport: 'stdio' as 'stdio' | 'http',
    command: '',
    args: '',
    env: '',
    url: '',
    headers: '',
    enabled: true,
  }
}

function startManual(row?: McpServer) {
  error.value = ''
  form.value = row ? { ...row } : blank()
  mode.value = 'manual'
}

async function applyParsed(raw: string) {
  error.value = ''
  const got = parseMcpConfig(raw)
  if (got.error) {
    error.value = got.error
    return
  }
  // 同名即覆盖是既有语义，但静默覆盖是危险操作——先让用户确认（0.2.27）
  const conflicts = catalog.mcpNameConflicts(got.servers.map((s) => s.name))
  if (conflicts.length) {
    const head = conflicts.slice(0, 3).join('、')
    const ok = await dialogs.confirm({
      title: '覆盖同名 MCP',
      message: `有 ${conflicts.length} 个同名项将被覆盖（${head}${conflicts.length > 3 ? ' 等' : ''}），继续？`,
      confirmText: '覆盖导入',
      danger: true,
    })
    if (!ok) return
  }
  const res = await catalog.importMcp(got.servers)
  if (res.error) {
    error.value = res.imported ? `已导入 ${res.imported} 个后失败：${res.error}` : res.error
    return
  }
  paste.value = ''
  mode.value = 'list'
}

async function pickFile() {
  error.value = ''
  try {
    const text = await bridge().app.PickImport('mcp')
    if (!text) return
    await applyParsed(text)
  } catch (e) {
    error.value = errText(e)
  }
}

// 启停：失败必须可见（store 失败已回滚乐观翻转，这里把原因显示出来）
async function toggle(row: McpServer) {
  error.value = ''
  const msg = await catalog.toggleMcp(row.id)
  if (msg) error.value = msg
}

async function save() {
  const msg = await catalog.upsertMcp(form.value)
  if (msg) {
    error.value = msg
    return
  }
  mode.value = 'list'
}

function line(row: McpServer) {
  return row.transport === 'http' ? row.url : [row.command, row.args].filter(Boolean).join(' ')
}

// 常用 coding MCP（第 8 批）：一键**填表**，不直接落盘。
// 为什么不直接保存：这些服务器几乎都带各自的参数/凭证（filesystem 要目录、
// github 要 PAT），静默存一份跑不起来的配置只会让人以为"加了没用"。
const PRESETS: { name: string; hint: string; command: string; args: string; env?: string }[] = [
  { name: 'playwright', hint: '浏览器自动化（官方 Playwright MCP）', command: 'npx', args: '-y @playwright/mcp@latest' },
  { name: 'chrome-devtools', hint: 'Chrome 调试与性能分析', command: 'npx', args: '-y chrome-devtools-mcp@latest' },
  { name: 'context7', hint: '按库名取最新文档（写代码时查 API）', command: 'npx', args: '-y @upstash/context7-mcp' },
  {
    name: 'sequential-thinking',
    hint: '分步推理草稿（复杂改动先列步骤）',
    command: 'npx',
    args: '-y @modelcontextprotocol/server-sequential-thinking',
  },
  { name: 'memory', hint: '跨会话知识图谱记忆', command: 'npx', args: '-y @modelcontextprotocol/server-memory' },
  {
    name: 'filesystem',
    hint: '读写指定目录（参数默认填当前工作区）',
    command: 'npx',
    args: '-y @modelcontextprotocol/server-filesystem',
  },
  {
    name: 'github',
    hint: '仓库 / Issue / PR（需要 PAT；本机需有 docker）',
    command: 'docker',
    args: 'run -i --rm -e GITHUB_PERSONAL_ACCESS_TOKEN ghcr.io/github/github-mcp-server',
    env: 'GITHUB_PERSONAL_ACCESS_TOKEN=',
  },
]

// usePreset 把预置填进"手动添加"表单：用户还能改参数与凭证，确认后再按保存。
// filesystem 的目录参数默认取当前工作区（取不到就留空，由用户自己填）。
async function usePreset(p: (typeof PRESETS)[number]) {
  error.value = ''
  let args = p.args
  if (p.name === 'filesystem') {
    const ws = await bridge()
      .app.GetWorkspace()
      .catch(() => '')
    if (ws) args = `${args} ${ws}`
  }
  startManual({ ...blank(), name: p.name, command: p.command, args, env: p.env ?? '' })
}

async function removeRow(row: McpServer) {
  const ok = await dialogs.confirm({
    title: '删除 MCP',
    message: `删除「${row.name}」？已保存的配置会去掉，正在使用的进程会在下次保存时关闭。`,
    confirmText: '删除',
    danger: true,
  })
  if (!ok) return
  error.value = ''
  const msg = await catalog.removeMcp(row.id)
  if (msg) error.value = msg
}
</script>

<template>
  <BaseModal open title="MCP 管理" panel-class="w-full max-w-[680px]" @close="$emit('close')">
    <p class="px-5 pt-4 text-xs text-[var(--c-text-dim)]">
      支持 Claude Desktop、Claude Code、Cursor 的 mcp.json（mcpServers），以及 VS Code 的 servers。本地用 command，远程用 url。
    </p>
    <p v-if="error" class="mx-5 mt-3 text-xs text-[var(--c-err-text)]">{{ error }}</p>

    <div v-if="mode === 'list'" class="space-y-2 p-5">
      <!-- 常用 coding MCP（第 8 批）：点一下填进表单（参数/凭证按需改），保存后才生效 -->
      <div class="rounded-xl border border-[var(--c-border)] px-3 py-2">
        <p class="text-[11px] text-[var(--c-text-faint)]">常用服务器（点击填入表单，确认参数后保存）</p>
        <div class="mt-1.5 flex flex-wrap gap-1.5">
          <button
            v-for="p in PRESETS"
            :key="p.name"
            class="chip text-[11px]"
            :title="p.hint"
            @click="usePreset(p)"
          >
            {{ p.name }}
          </button>
        </div>
      </div>
      <p v-if="!catalog.mcp.length" class="rounded-xl border border-dashed border-[var(--c-border)] py-8 text-center text-sm text-[var(--c-text-dim)]">
        还没有 MCP 服务器
      </p>
      <div
        v-for="row in catalog.mcp"
        :key="row.id"
        class="flex items-center gap-2 rounded-xl border border-[var(--c-border)] px-3 py-2"
      >
        <button
          class="chip shrink-0 disabled:cursor-not-allowed disabled:opacity-40"
          :aria-pressed="row.enabled"
          :disabled="catalog.busy || row.builtin"
          :title="row.builtin ? '内置 MCP 不可停用' : row.enabled ? '点击停用' : '点击启用'"
          @click="toggle(row)"
        >
          {{ row.enabled ? '已启用' : '已停用' }}
        </button>
        <div class="min-w-0 flex-1">
          <div class="truncate text-sm font-medium">
            {{ row.name }}
            <span v-if="row.builtin" class="ml-1 rounded border border-[var(--c-border)] px-1 text-[10px] text-[var(--c-text-faint)]">内置</span>
          </div>
          <div class="truncate text-[11px] text-[var(--c-text-faint)]">{{ row.transport }} · {{ line(row) }}</div>
        </div>
        <button class="chip disabled:cursor-not-allowed disabled:opacity-40" :disabled="!!row.builtin" :title="row.builtin ? '内置 MCP 不可修改' : ''" @click="startManual(row)">编辑</button>
        <button
          class="chip text-[var(--c-err-text)] disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="!!row.builtin"
          :title="row.builtin ? '内置 MCP 不可删除' : ''"
          @click="removeRow(row)"
        >
          删除
        </button>
      </div>
      <div class="flex gap-2">
        <button class="btn-primary flex-1 py-2 text-sm" @click="mode = 'paste'; error = ''">粘贴 mcp.json</button>
        <button class="chip flex-1 justify-center py-2" @click="pickFile">选择文件</button>
        <button class="chip flex-1 justify-center py-2" @click="startManual()">手动添加</button>
      </div>
    </div>

    <div v-else-if="mode === 'paste'" class="space-y-3 p-5">
      <textarea
        v-model="paste"
        rows="10"
        class="field-input font-mono text-xs"
        placeholder='{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","C:\\work"]}}}'
      ></textarea>
      <div class="flex justify-end gap-2">
        <button class="chip" @click="mode = 'list'">返回</button>
        <button class="btn-primary px-4 py-2 text-sm" @click="applyParsed(paste)">导入</button>
      </div>
    </div>

    <div v-else class="space-y-3 p-5">
      <div class="flex gap-2">
        <button class="chip" :class="form.transport === 'stdio' ? 'border-[var(--c-primary)] text-[var(--c-primary)]' : ''" @click="form.transport = 'stdio'">本地命令 (stdio)</button>
        <button class="chip" :class="form.transport === 'http' ? 'border-[var(--c-primary)] text-[var(--c-primary)]' : ''" @click="form.transport = 'http'">远程 URL</button>
      </div>
      <label class="block text-xs text-[var(--c-text-dim)]">名称<input v-model="form.name" class="field-input mt-1" placeholder="filesystem" /></label>
      <template v-if="form.transport === 'stdio'">
        <label class="block text-xs text-[var(--c-text-dim)]">命令<input v-model="form.command" class="field-input mt-1" placeholder="npx" /></label>
        <label class="block text-xs text-[var(--c-text-dim)]">参数<input v-model="form.args" class="field-input mt-1" placeholder="-y @modelcontextprotocol/server-filesystem C:\work" /></label>
        <label class="block text-xs text-[var(--c-text-dim)]">环境变量（每行 KEY=VALUE）<textarea v-model="form.env" rows="3" class="field-input mt-1 font-mono text-xs"></textarea></label>
      </template>
      <template v-else>
        <label class="block text-xs text-[var(--c-text-dim)]">URL<input v-model="form.url" class="field-input mt-1" placeholder="https://example.com/mcp" /></label>
        <label class="block text-xs text-[var(--c-text-dim)]">请求头（每行 Key=Value）<textarea v-model="form.headers" rows="3" class="field-input mt-1 font-mono text-xs" placeholder="Authorization=Bearer …"></textarea></label>
      </template>
      <label class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]"><input v-model="form.enabled" type="checkbox" /> 启用</label>
      <div class="flex justify-end gap-2">
        <button class="chip" @click="mode = 'list'">返回</button>
        <button class="btn-primary px-4 py-2 text-sm" @click="save">保存</button>
      </div>
    </div>
  </BaseModal>
</template>
