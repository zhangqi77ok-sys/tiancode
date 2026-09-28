<script setup lang="ts">
import { ref } from 'vue'
import { parseSkillMarkdown } from '../catalogImport'
import { useDialogs } from '../composables/useDialogs'
import { useCatalogStore, type SkillItem } from '../stores/catalog'
import { bridge } from '../wails'
import BaseModal from './BaseModal.vue'

defineEmits<{ (e: 'close'): void }>()
const catalog = useCatalogStore()
const dialogs = useDialogs()
const mode = ref<'list' | 'paste' | 'manual'>('list')
const error = ref('')
const paste = ref('')
const form = ref(blank())

function blank() {
  return { id: '', name: '', description: '', body: '', enabled: true }
}

function startManual(row?: SkillItem) {
  error.value = ''
  form.value = row ? { ...row } : blank()
  mode.value = 'manual'
}

async function takeMarkdown(raw: string, fallback: string) {
  const skill = parseSkillMarkdown(raw, fallback)
  if (!skill) {
    error.value = '没有读到技能内容'
    return
  }
  const msg = await catalog.importSkills([skill])
  if (msg) error.value = msg
}

async function importPaste() {
  error.value = ''
  await takeMarkdown(paste.value, '未命名技能')
  if (!error.value) {
    paste.value = ''
    mode.value = 'list'
  }
}

async function pickFile() {
  error.value = ''
  try {
    const text = await bridge().app.PickImport('skill')
    if (!text) return
    const base = 'SKILL'
    await takeMarkdown(text, base)
    if (!error.value) mode.value = 'list'
  } catch (e) {
    error.value = String(e instanceof Error ? e.message : e)
  }
}

async function importHome() {
  error.value = ''
  try {
    const text = await bridge().app.PickImport('skill-home')
    if (!text) return
    const parsed = JSON.parse(text) as { files?: { name: string; body: string }[] }
    const drafts = (parsed.files ?? [])
      .map((f) => parseSkillMarkdown(f.body, f.name.replace(/\/SKILL\.md$/i, '').split('/').pop() || 'skill'))
      .filter((x): x is NonNullable<typeof x> => !!x)
    if (!drafts.length) {
      error.value = '本机技能目录里没有可用的 SKILL.md'
      return
    }
    const msg = await catalog.importSkills(drafts)
    if (msg) error.value = msg
  } catch (e) {
    error.value = String(e instanceof Error ? e.message : e)
  }
}

async function pickDir() {
  error.value = ''
  try {
    const text = await bridge().app.PickImport('skill-dir')
    if (!text) return
    const parsed = JSON.parse(text) as { files?: { name: string; body: string }[] }
    const drafts = (parsed.files ?? [])
      .map((f) => parseSkillMarkdown(f.body, f.name.replace(/\/SKILL\.md$/i, '').split('/').pop() || 'skill'))
      .filter((x): x is NonNullable<typeof x> => !!x)
    if (!drafts.length) {
      error.value = '目录里的 SKILL.md 没有可用内容'
      return
    }
    const msg = await catalog.importSkills(drafts)
    if (msg) {
      error.value = msg
      return
    }
    mode.value = 'list'
  } catch (e) {
    error.value = String(e instanceof Error ? e.message : e)
  }
}

async function removeRow(row: SkillItem) {
  const ok = await dialogs.confirm({
    title: '删除 Skill',
    message: `删除「${row.name}」？正文会从本机清单里去掉。`,
    confirmText: '删除',
    danger: true,
  })
  if (ok) await catalog.removeSkill(row.id)
}

async function save() {
  const msg = await catalog.upsertSkill(form.value)
  if (msg) {
    error.value = msg
    return
  }
  mode.value = 'list'
}
</script>

<template>
  <BaseModal open title="Skill 管理" panel-class="w-full max-w-[680px]" @close="$emit('close')">
    <p class="px-5 pt-4 text-xs text-[var(--c-text-dim)]">
      支持 Claude Code / Codex 的 SKILL.md（文首 --- name / description ---）。可以粘贴、选单个文件，或选一整个技能目录。
    </p>
    <p v-if="error" class="mx-5 mt-3 text-xs text-[var(--c-err-text)]">{{ error }}</p>

    <div v-if="mode === 'list'" class="space-y-2 p-5">
      <p v-if="!catalog.skills.length" class="rounded-xl border border-dashed border-[var(--c-border)] py-8 text-center text-sm text-[var(--c-text-dim)]">
        还没有 Skill
      </p>
      <div
        v-for="row in catalog.skills"
        :key="row.id"
        class="flex items-center gap-2 rounded-xl border border-[var(--c-border)] px-3 py-2"
      >
        <button
          class="chip shrink-0"
          :class="row.enabled ? 'border-[var(--c-primary)] text-[var(--c-primary)]' : ''"
          :aria-pressed="row.enabled"
          :title="row.enabled ? '点击停用' : '点击启用'"
          @click="catalog.toggleSkill(row.id)"
        >
          {{ row.enabled ? '已启用' : '已停用' }}
        </button>
        <div class="min-w-0 flex-1">
          <div class="truncate text-sm font-medium">{{ row.name }}</div>
          <div class="truncate text-[11px] text-[var(--c-text-faint)]">{{ row.description || '无说明' }}</div>
        </div>
        <button class="chip" @click="startManual(row)">编辑</button>
        <button class="chip text-[var(--c-err-text)]" @click="removeRow(row)">删除</button>
      </div>
      <div class="flex flex-wrap gap-2">
        <button class="btn-primary flex-1 py-2 text-sm" @click="mode = 'paste'; error = ''">粘贴 SKILL.md</button>
        <button class="chip flex-1 justify-center py-2" @click="pickFile">选择文件</button>
        <button class="chip flex-1 justify-center py-2" @click="pickDir">选择目录</button>
        <button class="chip flex-1 justify-center py-2" @click="importHome">导入本机技能</button>
        <button class="chip flex-1 justify-center py-2" @click="startManual()">手动添加</button>
      </div>
    </div>

    <div v-else-if="mode === 'paste'" class="space-y-3 p-5">
      <textarea
        v-model="paste"
        rows="12"
        class="field-input font-mono text-xs"
        placeholder="---&#10;name: code-review&#10;description: 审查改动&#10;---&#10;按风险列出问题。"
      ></textarea>
      <div class="flex justify-end gap-2">
        <button class="chip" @click="mode = 'list'">返回</button>
        <button class="btn-primary px-4 py-2 text-sm" @click="importPaste">导入</button>
      </div>
    </div>

    <div v-else class="space-y-3 p-5">
      <label class="block text-xs text-[var(--c-text-dim)]">名称<input v-model="form.name" class="field-input mt-1" /></label>
      <label class="block text-xs text-[var(--c-text-dim)]">说明<input v-model="form.description" class="field-input mt-1" /></label>
      <label class="block text-xs text-[var(--c-text-dim)]">内容<textarea v-model="form.body" rows="6" class="field-input mt-1"></textarea></label>
      <label class="flex items-center gap-2 text-xs text-[var(--c-text-dim)]"><input v-model="form.enabled" type="checkbox" /> 启用</label>
      <div class="flex justify-end gap-2">
        <button class="chip" @click="mode = 'list'">返回</button>
        <button class="btn-primary px-4 py-2 text-sm" @click="save">保存</button>
      </div>
    </div>
  </BaseModal>
</template>
