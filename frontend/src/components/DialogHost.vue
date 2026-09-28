<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import BaseModal from './BaseModal.vue'
import { useDialogs } from '../composables/useDialogs'

// 确认 + 输入两枚对话框的宿主：全局只挂一个，业务侧 useDialogs() 直接 await。
const { confirmState, promptState, resolveConfirm, resolvePrompt } = useDialogs()

const promptValue = ref('')
const promptInput = ref<HTMLInputElement | null>(null)

// 打开时同步初始值并聚焦输入框（模态自身的焦点在面板上，这里要抢到输入框）
watch(promptState, async (s) => {
  if (!s) return
  promptValue.value = s.value ?? ''
  await nextTick()
  promptInput.value?.focus()
  promptInput.value?.select()
})

// 输入对话框：空值也允许提交（调用方自行校验语义），Esc/取消返回 null
function submitPrompt() {
  resolvePrompt(promptValue.value)
}
</script>

<template>
  <BaseModal :open="!!confirmState" :title="confirmState?.title" @close="resolveConfirm(false)">
    <div class="px-5 py-4">
      <p class="whitespace-pre-wrap text-sm leading-6">{{ confirmState?.message }}</p>
      <div class="mt-4 flex justify-end gap-2">
        <button class="chip" @click="resolveConfirm(false)">取消</button>
        <button
          class="btn-primary px-4 py-2 text-sm"
          :class="confirmState?.danger ? 'btn-danger' : ''"
          @click="resolveConfirm(true)"
        >
          {{ confirmState?.confirmText ?? '确定' }}
        </button>
      </div>
    </div>
  </BaseModal>

  <BaseModal :open="!!promptState" :title="promptState?.title" @close="resolvePrompt(null)">
    <form class="px-5 py-4" @submit.prevent="submitPrompt">
      <p v-if="promptState?.message" class="mb-2 text-xs text-[var(--c-text-dim)]">
        {{ promptState.message }}
      </p>
      <input
        ref="promptInput"
        v-model="promptValue"
        class="w-full rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm"
        :placeholder="promptState?.placeholder"
        :maxlength="promptState?.maxlength"
      />
      <div class="mt-4 flex justify-end gap-2">
        <button type="button" class="chip" @click="resolvePrompt(null)">取消</button>
        <button type="submit" class="btn-primary px-4 py-2 text-sm">确定</button>
      </div>
    </form>
  </BaseModal>
</template>
