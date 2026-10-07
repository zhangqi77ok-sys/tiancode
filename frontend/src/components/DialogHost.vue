<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import BaseModal from './BaseModal.vue'
import { useDialogs } from '../composables/useDialogs'

// 确认 + 输入 + 方案编辑：全局只挂一个宿主，业务侧 useDialogs() 直接 await。
const { confirmState, promptState, planEditState, resolveConfirm, resolvePrompt, resolvePlanEdit } = useDialogs()

const promptValue = ref('')
const promptInput = ref<HTMLInputElement | null>(null)
const planValue = ref('')
const planBox = ref<HTMLTextAreaElement | null>(null)

// 打开时同步初始值并聚焦输入框（模态自身的焦点在面板上，这里要抢到输入框）
watch(promptState, async (s) => {
  if (!s) return
  promptValue.value = s.value ?? ''
  await nextTick()
  promptInput.value?.focus()
  promptInput.value?.select()
})

watch(planEditState, async (s) => {
  if (!s) return
  planValue.value = s.value
  await nextTick()
  planBox.value?.focus()
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

  <!-- 方案确认：全文可改。不用 form，Enter 是换行，不会误提交。 -->
  <BaseModal :open="!!planEditState" :title="planEditState?.title" @close="resolvePlanEdit(null)">
    <div class="px-5 py-4">
      <p v-if="planEditState?.message" class="mb-2 text-xs text-[var(--c-text-dim)]">
        {{ planEditState.message }}
      </p>
      <textarea
        ref="planBox"
        v-model="planValue"
        rows="12"
        class="max-h-[50vh] w-full resize-y rounded-[var(--r-input)] border border-[var(--c-border)] bg-[var(--c-surface-soft)] px-3 py-2 text-sm leading-6"
      />
      <div class="mt-4 flex justify-end gap-2">
        <button type="button" class="chip" @click="resolvePlanEdit(null)">取消</button>
        <button type="button" class="btn-primary px-4 py-2 text-sm" @click="resolvePlanEdit(planValue)">
          {{ planEditState?.confirmText ?? '按方案执行' }}
        </button>
      </div>
    </div>
  </BaseModal>
</template>
