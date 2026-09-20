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
      <div class="flex items-center gap-1 bg-black/40 p-0.5 rounded-md border border-white/10">
        <button
          @click="s.activeTerminalTab = 'shell'"
          :class="[
            'px-2 py-0.5 rounded text-[11px] font-mono cursor-pointer transition-all flex items-center gap-1',
            s.activeTerminalTab === 'shell' ? 'bg-[#D96B27] text-white font-bold' : 'text-white/60 hover:text-white'
          ]"
        >
          <span>$_</span><span>终端控制台</span>
        </button>
        <button
          @click="s.activeTerminalTab = 'daemon'; s.loadDaemonTasks()"
          :class="[
            'px-2 py-0.5 rounded text-[11px] font-mono cursor-pointer transition-all flex items-center gap-1.5',
            s.activeTerminalTab === 'daemon' ? 'bg-[#D96B27] text-white font-bold' : 'text-white/60 hover:text-white'
          ]"
        >
          <span>⚡</span><span>守护进程 (Daemons)</span>
          <span
            v-if="s.daemonTasks.length > 0"
            class="px-1.5 py-0.2 rounded-full text-[9px] bg-white/20 text-white font-bold"
          >{{ s.daemonTasks.length }}</span>
        </button>
      </div>

      <div v-if="s.isTerminalRunning && s.activeTerminalTab === 'shell'" class="flex items-center gap-1 text-[11px] text-amber-400 bg-amber-400/10 px-2 py-0.5 rounded-full font-mono">
        <span class="w-1.5 h-1.5 rounded-full bg-amber-400 animate-ping"></span>
        <span>进程执行中...</span>
      </div>
    </div>

    <div class="flex items-center gap-1.5 text-white/50 text-xs">
      <button
        v-if="s.activeTerminalTab === 'daemon'"
        @click="s.loadDaemonTasks()"
        title="刷新后台守护进程列表"
        class="p-1 rounded hover:text-white hover:bg-white/10 cursor-pointer text-xs"
      >
        🔄
      </button>
      <button
        v-if="s.isTerminalRunning && s.activeTerminalTab === 'shell'"
        @click="s.cancelTerminalAction"
        title="终止正在执行的命令 (Ctrl+C)"
        class="px-2 py-0.5 rounded bg-red-500/20 text-red-300 hover:bg-red-500/30 text-[10px] font-mono font-bold cursor-pointer transition-all"
      >
        ■ 终止
      </button>
      <button
        v-if="s.activeTerminalTab === 'shell'"
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
              v-show="s.activeTerminalTab === 'shell'"
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

            <!-- 视图 2: 守护进程 (Daemons) 监控与管理面板 -->
            <div
              v-show="s.activeTerminalTab === 'daemon'"
              class="h-full flex flex-col p-4 overflow-y-auto bg-[#161412] text-zinc-300"
            >
              <div class="flex items-center justify-between pb-3 border-b border-white/[0.08] mb-3 shrink-0">
                <div>
                  <h4 class="text-xs font-bold text-white flex items-center gap-2">
                    <span>⚡ 后台守护进程管理器 (Active Daemons)</span>
                    <span class="text-[10px] px-2 py-0.5 rounded bg-emerald-500/10 text-emerald-400 font-mono">微内核受控</span>
                  </h4>
                  <p class="text-[10px] text-white/40 mt-0.5">
                    监控由 AI Agent 或开发者发起的长驻后台任务（如开发服务器、文件监听服务等），支持一键强行终止与资源回收。
                  </p>
                </div>
                <button
                  @click="s.loadDaemonTasks()"
                  class="px-2.5 py-1 rounded bg-white/5 hover:bg-white/10 border border-white/10 text-white/80 hover:text-white text-[11px] cursor-pointer transition-all flex items-center gap-1"
                >
                  <span>🔄</span><span>刷新列表</span>
                </button>
              </div>

              <!-- 无守护进程空状态 -->
              <div v-if="s.daemonTasks.length === 0" class="flex-1 flex flex-col items-center justify-center text-center p-8">
                <span class="text-2xl mb-2 opacity-40">💤</span>
                <p class="text-xs text-white/60 font-semibold">暂无活动守护进程</p>
                <p class="text-[10px] text-white/40 mt-1 max-w-sm">
                  当 AI 或终端启动带有 <code>is_daemon: true</code> 的长驻任务时，任务将在此统一监控与受控管理。
                </p>
              </div>

              <!-- 守护进程列表表格 -->
              <div v-else class="overflow-x-auto border border-white/10 rounded-lg">
                <table class="w-full text-left text-[11px] font-mono border-collapse">
                  <thead>
                    <tr class="bg-white/5 text-white/60 border-b border-white/10">
                      <th class="p-2.5">任务 ID</th>
                      <th class="p-2.5">PID</th>
                      <th class="p-2.5">执行指令</th>
                      <th class="p-2.5">启动时间</th>
                      <th class="p-2.5">状态</th>
                      <th class="p-2.5 text-right">操作</th>
                    </tr>
                  </thead>
                  <tbody class="divide-y divide-white/5">
                    <tr v-for="task in s.daemonTasks" :key="task.task_id" class="hover:bg-white/[0.02] transition-colors">
                      <td class="p-2.5 font-bold text-[#FFA97A]">{{ task.task_id }}</td>
                      <td class="p-2.5 text-white/80">{{ task.pid || '-' }}</td>
                      <td class="p-2.5 max-w-[260px] truncate text-white" :title="task.command">{{ task.command }}</td>
                      <td class="p-2.5 text-white/50 text-[10px]">{{ task.start_time ? new Date(task.start_time).toLocaleTimeString() : '-' }}</td>
                      <td class="p-2.5">
                        <span class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-bold bg-emerald-500/10 text-emerald-400">
                          <span class="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse"></span>
                          {{ task.status || 'running' }}
                        </span>
                      </td>
                      <td class="p-2.5 text-right">
                        <button
                          @click="s.killDaemonTaskAction(task.task_id)"
                          class="px-2 py-0.5 rounded bg-rose-500/20 hover:bg-rose-500/30 text-rose-300 border border-rose-500/30 text-[10px] font-bold cursor-pointer transition-all"
                          title="终止此后台守护进程"
                        >
                          ■ 终止
                        </button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useWorkbenchStore } from '../stores/workbench'
const s = useWorkbenchStore()
const { terminalScrollRef } = storeToRefs(s)

onMounted(() => {
  s.loadDaemonTasks()
})
</script>

