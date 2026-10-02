<script setup lang="ts">
import { onMounted, ref } from 'vue'
import BaseModal from './BaseModal.vue'
import AppIcon from './AppIcon.vue'
import { bridge, type MemoryViewDTO } from '../wails'
import { useChatStore } from '../stores/chat'
import { useWorkspaceStore } from '../stores/workspace'
import { useToast } from '../composables/useToast'
import { useDialogs } from '../composables/useDialogs'
import { errText } from '../composables/errText'

// 记忆管理面板（0.0.21）：模型能记的，用户必须看得见、删得掉——透明度红线。
// 两级列表（全局偏好 / 本项目），逐条删除走后端 1 基行号（与模型侧 memory 工具
// 同一锚点），清空是危险动作先确认。这里只展示与转发，记忆规则只有后端一份。

const emit = defineEmits<{ (e: 'close'): void }>()
const store = useChatStore()
const ws = useWorkspaceStore()
const { push: toast } = useToast()
const dialogs = useDialogs()

const view = ref<MemoryViewDTO>({ global: [], project: [] })
const error = ref('')
const loading = ref(false)
const busy = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const v = await bridge().app.MemoryLines(store.sessionId)
    if (v) view.value = v
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}
onMounted(() => void load())

// 逐条删除：行号 = 列表下标 + 1（后端 1 基，与模型侧同一锚点）
async function del(scope: 'global' | 'workspace', index: number) {
  if (busy.value) return
  busy.value = true
  try {
    await bridge().app.MemoryDelete(store.sessionId, scope, index + 1)
    await load()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    busy.value = false
  }
}

// 清空整个作用域：危险动作先确认（模型侧同样受限于"先清理再记"的容量纪律）
async function clearAll(scope: 'global' | 'workspace') {
  const ok = await dialogs.confirm({
    title: scope === 'global' ? '清空全局记忆' : '清空本项目记忆',
    message:
      scope === 'global'
        ? '全部全局偏好（如"回复用中文"）将被删除，模型下一轮就不再记得它们。确定清空吗？'
        : '本项目的全部约定将被删除（不影响其他项目）。确定清空吗？',
    confirmText: '清空',
    danger: true,
  })
  if (!ok) return
  busy.value = true
  try {
    await bridge().app.MemoryClear(store.sessionId, scope)
    toast('info', '已清空')
    await load()
  } catch (e) {
    toast('error', errText(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <BaseModal open :title="'记忆管理'" @close="emit('close')">
    <p class="px-1 pb-2 text-xs text-[var(--c-text-dim)]">
      这些内容每轮对话都会注入给模型（跨对话留存）。删除后模型从下一轮起就不再记得。
    </p>

    <p v-if="error" class="px-1 pb-2 text-xs text-[var(--c-err-text)]" role="alert">{{ error }}</p>
    <p v-else-if="loading" class="px-1 py-4 text-center text-xs text-[var(--c-text-faint)]">读取中…</p>

    <template v-else>
      <!-- 全局偏好：跟随应用，所有对话共享 -->
      <section class="mb-3">
        <div class="flex items-center gap-2 px-1 pb-1">
          <h3 class="min-w-0 flex-1 text-xs font-semibold text-[var(--c-text)]">全局（用户偏好）</h3>
          <button
            v-if="view.global.length"
            class="chip px-2 py-0.5 text-[10px]"
            :disabled="busy"
            title="删除全部全局记忆（先确认）"
            @click="clearAll('global')"
          >
            清空
          </button>
        </div>
        <p v-if="!view.global.length" class="rounded-lg bg-[var(--c-surface-soft)] px-2.5 py-2 text-xs text-[var(--c-text-faint)]">
          还没有全局记忆——在对话里让模型"记住"偏好后，这里就能看到。
        </p>
        <ul v-else class="space-y-1">
          <li
            v-for="(line, i) in view.global"
            :key="`g${i}`"
            class="flex items-start gap-2 rounded-lg border border-[var(--c-border)] bg-[var(--c-surface)] px-2.5 py-1.5"
          >
            <span class="min-w-0 flex-1 break-words text-xs leading-5 text-[var(--c-text)]">{{ line }}</span>
            <button
              class="shrink-0 rounded-lg px-1 py-0.5 text-[10px] text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-err-text)]"
              :disabled="busy"
              aria-label="删除这条记忆"
              @click="del('global', i)"
            >
              删除
            </button>
          </li>
        </ul>
      </section>

      <!-- 本项目：按工作区各存一份，纯对话时后端给空（不是错误） -->
      <section>
        <div class="flex items-center gap-2 px-1 pb-1">
          <h3 class="min-w-0 flex-1 text-xs font-semibold text-[var(--c-text)]">本项目（工作区约定）</h3>
          <button
            v-if="view.project.length"
            class="chip px-2 py-0.5 text-[10px]"
            :disabled="busy"
            title="删除本项目全部记忆（先确认）"
            @click="clearAll('workspace')"
          >
            清空
          </button>
        </div>
        <p v-if="!view.project.length" class="rounded-lg bg-[var(--c-surface-soft)] px-2.5 py-2 text-xs text-[var(--c-text-faint)]">
          <template v-if="ws.path">这个项目还没有记忆。</template>
          <template v-else>纯对话没有项目记忆；挂上工作区后再让模型记项目约定。</template>
        </p>
        <ul v-else class="space-y-1">
          <li
            v-for="(line, i) in view.project"
            :key="`p${i}`"
            class="flex items-start gap-2 rounded-lg border border-[var(--c-border)] bg-[var(--c-surface)] px-2.5 py-1.5"
          >
            <span class="min-w-0 flex-1 break-words text-xs leading-5 text-[var(--c-text)]">{{ line }}</span>
            <button
              class="shrink-0 rounded-lg px-1 py-0.5 text-[10px] text-[var(--c-text-faint)] transition-colors hover:text-[var(--c-err-text)]"
              :disabled="busy"
              aria-label="删除这条记忆"
              @click="del('workspace', i)"
            >
              删除
            </button>
          </li>
        </ul>
      </section>
    </template>
  </BaseModal>
</template>
