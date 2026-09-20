<template>
<div
  v-show="s.isTerminalOpen"
  :style="{ height: s.isTerminalMaximized ? '60vh' : `${s.terminalHeight}px` }"
  class="min-h-[140px] max-h-[75vh] bg-[#161412] text-white flex flex-col border-t border-black/[0.3] shadow-2xl transition-all duration-150 z-30 shrink-0 select-none font-sans relative"
>
  <!-- 终端顶部可拖拽调节手柄 (Horizontal Resize Sash) -->
  <div
    v-if="!s.isTerminalMaximized"
    @mousedown="s.startTerminalResize($event)"
    @dblclick="s.resetTerminalHeight()"
    class="absolute left-0 right-0 -top-1.5 h-3 cursor-row-resize hover:bg-[#D96B27]/40 active:bg-[#D96B27] transition-colors z-40 select-none group flex items-center justify-center"
    title="双击恢复默认高度 (240px)，按住上下拖拽调整终端高度"
  >
    <div class="h-0.5 w-12 rounded-full bg-white/20 group-hover:bg-[#D96B27] transition-colors"></div>
  </div>

  <!-- 终端控制顶栏 -->
          <div class="h-8 bg-[#1E1C1A] border-b border-white/[0.08] px-3 flex items-center justify-between select-none shrink-0">
            <div class="flex items-center gap-2 text-xs">
              <span class="text-[#D96B27] font-bold font-mono">$_</span>
              <span class="font-bold text-white/90">终端控制台</span>
              <div v-if="s.isTerminalRunning" class="flex items-center gap-1 text-[11px] text-amber-400 bg-amber-400/10 px-2 py-0.5 rounded-full font-mono">
                <span class="w-1.5 h-1.5 rounded-full bg-amber-400 animate-ping"></span>
                <span>进程执行中...</span>
              </div>
            </div>

            <div class="flex items-center gap-1.5 text-white/50 text-xs">
              <button
                v-if="s.isTerminalRunning"
                @click="s.cancelTerminalAction"
                title="终止正在执行的命令 (Ctrl+C)"
                class="px-2 py-0.5 rounded bg-red-500/20 text-red-300 hover:bg-red-500/30 text-[10px] font-mono font-bold cursor-pointer transition-all"
              >
                ■ 终止
              </button>
              <button
                @click="s.clearTerminalLogs"
                title="清空终端屏幕"
                class="p-1 rounded hover:text-white hover:bg-white/10 cursor-pointer text-xs"
              >
                🗑️
              </button>
              <button
                @click="s.isTerminalMaximized = !s.isTerminalMaximized"
                :title="s.isTerminalMaximized ? '还原终端高度' : '最大化终端'"
                class="p-1 rounded hover:text-white hover:bg-white/10 cursor-pointer text-xs font-mono"
              >
                {{ s.isTerminalMaximized ? '🗗' : '🗖' }}
              </button>
              <button
                @click="s.isTerminalOpen = false"
                title="收起终端抽屉 (Ctrl+`)"
                class="p-1 rounded hover:text-white hover:bg-white/10 cursor-pointer text-xs"
              >
                ✕
              </button>
            </div>
          </div>

          <!-- 终端内容区 -->
          <div class="flex-1 overflow-hidden relative font-mono text-xs select-text">
            <!-- 视图 1: Shell 实时交互控制台 -->
            <div
              ref="terminalScrollRef"
              class="h-full flex flex-col p-3 overflow-y-auto space-y-1.5 bg-[#161412]"
            >
              <div class="text-white/40 mb-1 text-[11px]">
                湉码 受控静默终端 · 工作区: tiancode [Windows 安全沙箱就绪]
              </div>
              
              <!-- 历史流式输出块 -->
              <div v-for="(log, idx) in s.terminalOutputs" :key="idx" class="space-y-0.5">
                <div v-if="log.type === 'cmd'" class="text-white/60 flex items-center gap-1.5 font-bold">
                  <span class="text-[#D96B27]">PS></span>
                  <span class="text-white">{{ log.text }}</span>
                </div>
                <div
                  v-else-if="log.type === 'output'"
                  class="whitespace-pre-wrap leading-relaxed text-zinc-300 pl-4 border-l-2 border-white/10"
                >{{ log.text }}</div>
                <div
                  v-else-if="log.type === 'exit'"
                  :class="['text-[10px] pl-4', log.exitCode === 0 ? 'text-emerald-400' : 'text-rose-400']"
                >
                  ● 进程退出 · Exit Code: {{ log.exitCode }} (耗时 {{ log.durationMs }}ms)
                </div>
              </div>

              <!-- 正在运行时的流式增量输出缓冲 -->
              <div v-if="s.currentTerminalBuffer" class="whitespace-pre-wrap leading-relaxed text-zinc-300 pl-4 border-l-2 border-[#D96B27]">
                {{ s.currentTerminalBuffer }}
              </div>

              <!-- 命令行输入提示符 -->
              <div class="flex items-center gap-2 pt-2 border-t border-white/[0.06] mt-auto shrink-0">
                <span class="text-[#D96B27] font-bold font-mono select-none">PS></span>
                <input
                  v-model="s.terminalInputCmd"
                  @keydown.enter="s.submitTerminalCommand"
                  @keydown.up.prevent="s.navigateCommandHistory(-1)"
                  @keydown.down.prevent="s.navigateCommandHistory(1)"
                  :disabled="s.isTerminalRunning"
                  type="text"
                  placeholder="输入工作区命令回车执行 (如: go test ./..., git status, go build, clear)..."
                  class="flex-1 bg-transparent text-white font-mono text-xs focus:outline-none placeholder:text-white/20 disabled:opacity-50"
                />
              </div>
            </div>
          </div>
        </div>
</template>

<script setup lang="ts">
import { storeToRefs } from 'pinia'
import { useWorkbenchStore } from '../stores/workbench'
const s = useWorkbenchStore()
const { terminalScrollRef } = storeToRefs(s)
</script>

