<template>
  <div class="w-full h-full flex flex-col min-h-0 relative">
    <!-- Diff 控制辅助顶栏 -->
    <div class="h-7 bg-[#1A1816] border-b border-white/[0.08] px-3 flex items-center justify-between text-[11px] font-mono text-[#A1A1AA] select-none shrink-0">
      <div class="flex items-center gap-2">
        <span class="text-[#D96B27] font-bold">DIFF</span>
        <span class="text-white/40 truncate max-w-xs">{{ language }}</span>
      </div>
      <div class="flex items-center gap-2">
        <button
          @click="toggleSideBySide"
          class="px-2 py-0.5 rounded bg-white/5 hover:bg-white/10 hover:text-white transition-all cursor-pointer text-[10px] flex items-center gap-1"
          :title="sideBySide ? '切换为单栏内联对比 (Inline Diff)' : '切换为双栏并排对比 (Side-by-Side Diff)'"
        >
          <span>{{ sideBySide ? '◫ 并排对比' : '▯ 内联对比' }}</span>
        </button>
      </div>
    </div>
    <div ref="host" class="flex-1 w-full min-h-0"></div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import '../core/monacoEnv'
import * as monaco from 'monaco-editor'
import { useWorkbenchStore } from '../stores/workbench'

const props = defineProps<{
  original: string
  modified: string
  language: string
  readOnly?: boolean
}>()

const host = ref<HTMLDivElement | null>(null)
const bench = useWorkbenchStore()
let diffEditor: monaco.editor.IStandaloneDiffEditor | null = null
const sideBySide = ref(true)

function langOf(name: string) {
  const n = (name || '').toLowerCase()
  if (n === 'go' || n.endsWith('.go')) return 'go'
  if (n.endsWith('.ts') || n.endsWith('.tsx')) return 'typescript'
  if (n.endsWith('.js') || n.endsWith('.jsx')) return 'javascript'
  if (n.endsWith('.json')) return 'json'
  if (n.endsWith('.css')) return 'css'
  if (n.endsWith('.html') || n.endsWith('.vue')) return 'html'
  if (n.endsWith('.md')) return 'markdown'
  if (n.endsWith('.ps1') || n.endsWith('.sh')) return 'shell'
  return 'plaintext'
}

function toggleSideBySide() {
  sideBySide.value = !sideBySide.value
  diffEditor?.updateOptions({
    renderSideBySide: sideBySide.value,
  })
}

function updateModels() {
  if (!diffEditor) return
  const currentLang = langOf(props.language)
  const currentModel = diffEditor.getModel()
  if (currentModel) {
    currentModel.original.dispose()
    currentModel.modified.dispose()
  }
  const origModel = monaco.editor.createModel(props.original || '', currentLang)
  const modModel = monaco.editor.createModel(props.modified || '', currentLang)
  diffEditor.setModel({
    original: origModel,
    modified: modModel,
  })
}

onMounted(() => {
  if (!host.value) return
  diffEditor = monaco.editor.createDiffEditor(host.value, {
    theme: 'tcode-warm-charcoal',
    automaticLayout: true,
    readOnly: props.readOnly ?? true,
    renderSideBySide: sideBySide.value,
    fontSize: bench.uiPrefs.monaco_size || 14,
    fontFamily: `${bench.uiPrefs.monaco_font || 'JetBrains Mono'}, Consolas, monospace`,
    scrollBeyondLastLine: false,
    wordWrap: 'on',
    ignoreTrimWhitespace: false,
    diffWordWrap: 'on',
  })
  updateModels()
})

watch(() => [props.original, props.modified, props.language], () => {
  updateModels()
})

watch(() => [bench.uiPrefs.monaco_font, bench.uiPrefs.monaco_size, bench.uiPrefs.theme], () => {
  if (!diffEditor) return
  diffEditor.updateOptions({
    fontSize: bench.uiPrefs.monaco_size || 14,
    fontFamily: `${bench.uiPrefs.monaco_font || 'JetBrains Mono'}, Consolas, monospace`,
  })
  monaco.editor.setTheme(bench.uiPrefs.theme === 'light' ? 'vs' : 'tcode-warm-charcoal')
})

onBeforeUnmount(() => {
  const model = diffEditor?.getModel()
  model?.original?.dispose()
  model?.modified?.dispose()
  diffEditor?.dispose()
  diffEditor = null
})
</script>
