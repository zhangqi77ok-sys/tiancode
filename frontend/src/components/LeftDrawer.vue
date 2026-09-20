<template>
<aside v-show="s.isLeftDrawerOpen" class="w-[270px] min-w-[230px] bg-[#F4EFEA] border-r border-black/[0.08] flex flex-col justify-between select-none z-10 shrink-0 font-sans">
        <!-- 抽屉视图 1: 真实会话列表 (Chat Sessions) -->
        <div v-if="s.activeActivity === 'chat'" class="flex flex-col h-full overflow-hidden">
          <div class="p-3 border-b border-black/[0.06] flex items-center justify-between gap-2">
            <div class="flex items-center gap-1.5 min-w-0">
              <span class="text-xs font-bold text-[#18181B] tracking-tight">会话分支</span>
              <span class="text-[10px] text-[#71717A] bg-black/[0.04] px-1.5 py-0.2 rounded-full font-mono">{{ s.projects.length }} 个项目</span>
            </div>
            <button
              @click="s.openProjectFolder"
              class="flex items-center gap-1 px-2 py-1 rounded-md text-[11px] font-medium text-[#18181B] bg-white hover:bg-black/[0.04] border border-black/[0.08] shadow-2xs cursor-pointer shrink-0"
            >
              <span>📂</span><span>打开项目</span>
            </button>
          </div>

          <div class="p-2.5 space-y-2 border-b border-black/[0.06]">
            <input
              v-model="s.sessionSearch"
              type="text"
              placeholder="搜索会话标题…"
              class="w-full h-7 px-2 rounded-lg bg-white text-xs border border-black/[0.08] focus:border-[#D96B27] focus:outline-none placeholder:text-[#A1A1AA]"
            >
            <div v-if="s.availableTags.length > 1" class="flex items-center gap-1 overflow-x-auto no-scrollbar py-0.5 text-[10px]">
              <button
                v-for="tag in s.availableTags"
                :key="tag"
                @click="s.activeTag = tag"
                :class="[
                  'px-2 py-0.5 rounded-full font-medium cursor-pointer',
                  s.activeTag === tag ? 'bg-[#D96B27] text-white' : 'bg-white text-[#71717A] border border-black/[0.06]'
                ]"
              >
                {{ tag === '全部' ? '全部' : '#' + tag }}
              </button>
            </div>
          </div>

          <div class="flex-1 overflow-y-auto p-2 space-y-3">
            <div v-if="s.projectTree.length === 0" class="p-6 text-center text-[#A1A1AA] text-xs space-y-2">
              <div>还没有打开的项目</div>
              <button @click="s.openProjectFolder" class="text-[#D96B27] underline cursor-pointer">打开本地文件夹</button>
            </div>

            <div
              v-for="proj in s.projectTree"
              :key="proj.path"
              class="rounded-xl border border-black/[0.08] bg-white/70 shadow-2xs overflow-hidden"
            >
              <div
                class="p-2 flex items-center justify-between hover:bg-black/[0.02] cursor-pointer group"
                @click="s.toggleProjectCollapse(proj.path)"
              >
                <div class="flex items-center gap-1.5 min-w-0">
                  <span class="text-[10px] text-[#71717A]">{{ s.collapsedProjects[proj.path] ? '▶' : '▼' }}</span>
                  <span class="text-xs">📁</span>
                  <span
                    class="text-xs font-semibold truncate"
                    :class="s.workspacePath && proj.path.replace(/\\/g,'/').toLowerCase() === s.workspacePath.replace(/\\/g,'/').toLowerCase() ? 'text-[#D96B27]' : 'text-[#18181B]'"
                  >{{ proj.name }}</span>
                </div>
                <div class="flex items-center gap-0.5 shrink-0" @click.stop>
                  <button
                    @click="s.createSessionInProject(proj.path)"
                    title="在此项目下新建会话"
                    class="p-1 rounded hover:bg-black/[0.06] text-[#71717A] hover:text-[#D96B27] cursor-pointer text-xs"
                  >＋</button>
                  <button
                    @click="s.unpinProject(proj.path)"
                    title="从列表移除项目（不删磁盘和历史会话）"
                    class="p-1 rounded hover:bg-black/[0.06] text-[#A1A1AA] hover:text-red-500 cursor-pointer text-[10px]"
                  >✕</button>
                </div>
              </div>

              <div v-show="!s.collapsedProjects[proj.path]" class="p-1 space-y-1 border-t border-black/[0.04] bg-[#FAF8F5]">
                <div v-if="proj.sessions.length === 0" class="px-2 py-2 text-[10px] text-[#A1A1AA]">
                  此项目还没有已保存的对话，发送第一条消息后会出现在这里
                </div>
                <div
                  v-for="sess in proj.sessions"
                  :key="sess.id"
                  @click="s.activateSession(sess.id, sess.workspace)"
                  :class="[
                    'session-item p-2 rounded-lg flex flex-col gap-1 cursor-pointer transition-all border',
                    s.currentSessionId === sess.id
                      ? 'bg-white border-[#D96B27]/40 shadow-xs'
                      : 'hover:bg-white/80 border-transparent hover:border-black/[0.06]'
                  ]"
                >
                  <div class="flex items-center justify-between gap-1">
                    <span class="text-xs font-semibold text-[#18181B] truncate">{{ sess.title }}</span>
                    <button @click.stop="s.deleteSession(sess.id)" class="text-[#A1A1AA] hover:text-red-500 text-[10px] cursor-pointer" title="删除会话">🗑</button>
                  </div>
                  <div class="flex items-center gap-1 text-[10px] text-[#71717A]">
                    <button
                      class="bg-[#D96B27]/10 text-[#D96B27] px-1 py-0.2 rounded cursor-pointer"
                      @click.stop="s.setSessionTag(sess.id, window.prompt('会话标签（空则清除）', sess.tag || '') ?? sess.tag)"
                    >{{ sess.tag ? '#' + sess.tag : '#标签' }}</button>
                    <span class="truncate flex-1">{{ sess.desc }}</span>
                    <span class="font-mono text-[#A1A1AA] shrink-0">{{ sess.time }}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 抽屉视图 2: 工程文件资源管理器与全盘检索 (Files & Search Explorer) -->
        <div v-else-if="s.activeActivity === 'files'" class="flex flex-col h-full overflow-hidden">
          <div class="p-3 border-b border-black/[0.06] flex items-center justify-between">
            <span class="font-bold text-xs text-[#18181B] flex items-center gap-1.5">
              <span>📁</span><span>文件资源管理器</span>
            </span>
            <div class="flex items-center gap-1">
              <button
                @click="s.explorerTab = 'tree'"
                :class="['px-1.5 py-0.5 rounded text-[10px] cursor-pointer font-medium transition-colors', s.explorerTab === 'tree' ? 'bg-white shadow-2xs text-[#D96B27] font-bold' : 'text-[#71717A] hover:text-[#18181B]']"
                title="目录文件树"
              >
                目录
              </button>
              <button
                @click="s.explorerTab = 'search'"
                :class="['px-1.5 py-0.5 rounded text-[10px] cursor-pointer font-medium transition-colors', s.explorerTab === 'search' ? 'bg-white shadow-2xs text-[#D96B27] font-bold' : 'text-[#71717A] hover:text-[#18181B]']"
                title="工作区全盘检索 (grep/find)"
              >
                检索
              </button>
              <button
                v-if="s.explorerTab === 'tree'"
                class="cursor-pointer text-[#71717A] hover:text-[#18181B] p-1 rounded text-xs ml-0.5"
                @click="s.loadFileTree"
                title="刷新文件树"
              >↻</button>
            </div>
          </div>

          <!-- 模式一：目录树 -->
          <template v-if="s.explorerTab === 'tree'">
            <div class="p-2 border-b border-black/[0.06] bg-[#FAF8F5]">
              <div class="flex items-center gap-1">
                <input
                  v-model="s.fileTreeFilter"
                  @keydown.enter="s.searchFromFilter"
                  type="text"
                  placeholder="过滤已加载节点 / 回车搜..."
                  class="flex-1 h-6 px-2 rounded-md bg-white text-[10px] font-mono border border-black/[0.08] focus:border-[#D96B27] focus:outline-none placeholder:text-[#A1A1AA]"
                />
                <button
                  v-if="s.fileTreeFilter"
                  @click="s.searchFromFilter"
                  class="px-1.5 h-6 rounded bg-[#D96B27] text-white text-[10px] font-medium cursor-pointer shrink-0"
                  title="在全工程中搜索此关键词"
                >搜</button>
              </div>
            </div>
            <div class="flex-1 overflow-y-auto p-1.5 text-[11px] font-mono space-y-0.5">
              <div v-if="s.displayFileTree.length === 0" class="p-4 text-center text-[#A1A1AA] text-[10px]">
                <p>{{ s.fileTreeFilter ? '已加载节点中无匹配' : '工作区为空' }}</p>
                <button
                  v-if="s.fileTreeFilter"
                  @click="s.searchFromFilter"
                  class="mt-2 text-[#D96B27] underline text-[10px] cursor-pointer"
                >
                  🔍 在全工程中搜索 "{{ s.fileTreeFilter }}"
                </button>
              </div>
              <FileTreeNode
                v-for="node in s.displayFileTree"
                :key="node.path"
                :node="node"
                :depth="0"
              />
            </div>
          </template>

          <!-- 模式二：全局代码与文件检索 -->
          <template v-else>
            <div class="p-2 border-b border-black/[0.06] bg-[#FAF8F5] flex flex-col gap-1.5">
              <div class="flex items-center gap-1">
                <input
                  v-model="s.searchQuery"
                  @keydown.enter="s.runWorkspaceSearch()"
                  type="text"
                  placeholder="输入关键词回车检索..."
                  class="flex-1 h-7 px-2 rounded-md bg-white text-[11px] font-mono border border-black/[0.08] focus:border-[#D96B27] focus:outline-none placeholder:text-[#A1A1AA]"
                />
                <button
                  @click="s.runWorkspaceSearch()"
                  :disabled="s.isSearching"
                  class="px-2.5 h-7 rounded-md bg-[#D96B27] hover:bg-[#C25A1D] text-white text-xs font-medium cursor-pointer disabled:opacity-50 shrink-0"
                >
                  {{ s.isSearching ? '...' : '搜索' }}
                </button>
              </div>
              <div class="flex items-center gap-2 text-[10px] text-[#71717A]">
                <span>模式:</span>
                <label class="flex items-center gap-1 cursor-pointer">
                  <input type="radio" v-model="s.searchAction" value="find" class="accent-[#D96B27]" />
                  <span>文件名 (find)</span>
                </label>
                <label class="flex items-center gap-1 cursor-pointer">
                  <input type="radio" v-model="s.searchAction" value="grep" class="accent-[#D96B27]" />
                  <span>内容 (grep)</span>
                </label>
              </div>
            </div>

            <div class="flex-1 overflow-y-auto p-1.5 space-y-1 text-xs font-mono">
              <div v-if="s.isSearching" class="p-6 text-center text-[#71717A] text-[11px]">
                🔍 正在检索工作区...
              </div>
              <div v-else-if="s.searchResults.length === 0" class="p-6 text-center text-[11px]">
                <div v-if="s.searchError" class="text-red-500 font-semibold mb-1">
                  ⚠️ 检索异常: {{ s.searchError }}
                </div>
                <div v-else class="text-[#A1A1AA]">
                  {{ s.searchQuery ? `未找到匹配项: "${s.searchQuery}"` : '输入关键词检索全工程' }}
                </div>
              </div>
              <div
                v-else
                v-for="(res, idx) in s.searchResults"
                :key="idx"
                @click="s.openEditorTab(res.path, 'edit', res.line)"
                class="p-1.5 rounded hover:bg-white cursor-pointer border border-transparent hover:border-black/[0.06] transition-colors group"
                :title="res.path + (res.line ? ':' + res.line : '')"
              >
                <div class="flex items-center justify-between gap-1 text-[11px]">
                  <span class="font-bold text-[#18181B] truncate group-hover:text-[#D96B27]">{{ res.path }}</span>
                  <span v-if="res.line" class="text-[10px] text-[#71717A] font-mono shrink-0">L{{ res.line }}</span>
                </div>
                <div v-if="res.content" class="text-[10px] text-[#71717A] font-mono truncate mt-0.5 opacity-90 pl-1 border-l border-black/[0.1]">
                  {{ res.content }}
                </div>
              </div>
            </div>
          </template>
        </div>

        <!-- 抽屉视图 3: 真实 Git 变更管理 (Source Control) -->
        <div v-else-if="s.activeActivity === 'git'" class="flex flex-col h-full overflow-hidden">
          <div class="p-3 border-b border-black/[0.06] flex items-center justify-between">
            <span class="font-bold text-xs text-[#18181B] flex items-center gap-1.5">
              <span>🌿</span><span>源代码管理 (Git)</span>
            </span>
            <div class="flex items-center gap-1">
              <button class="text-[10px] px-1.5 py-0.5 rounded bg-white border border-black/[0.08] cursor-pointer" @click="s.gitPullAction">pull</button>
              <button class="text-[10px] px-1.5 py-0.5 rounded bg-white border border-black/[0.08] cursor-pointer" @click="s.gitPushAction">push</button>
              <span class="text-[10px] font-mono text-[#10A37F] bg-[#10A37F]/10 px-1.5 py-0.2 rounded font-bold">
                {{ s.gitCurrentBranch || s.gitBranchLabel }}
              </span>
            </div>
          </div>

          <div class="flex-1 overflow-y-auto p-3 space-y-3 text-xs">
            <div class="space-y-1.5">
              <div class="text-[10px] font-bold text-[#71717A] uppercase">分支</div>
              <select
                v-if="s.gitBranches.length > 0"
                class="w-full px-2 py-1.5 rounded-lg border border-black/[0.1] text-xs bg-white"
                :value="s.gitCurrentBranch"
                @change="s.checkoutBranch(($event.target as HTMLSelectElement).value)"
              >
                <option v-for="b in s.gitBranches" :key="b" :value="b">{{ b }}</option>
              </select>
              <div v-else class="text-[10px] text-[#A1A1AA]">当前工作区没有 Git 仓库</div>
              <div class="flex gap-1">
                <input v-model="s.newBranchName" type="text" placeholder="新分支名" class="flex-1 px-2 py-1 rounded-lg border border-black/[0.1] text-xs">
                <button @click="s.createBranchAction" class="px-2 py-1 rounded-lg bg-white border border-black/[0.08] text-[10px] cursor-pointer">创建</button>
              </div>
            </div>
            <div class="space-y-1.5">
              <div class="flex items-center justify-between">
                <span class="text-[10px] font-bold text-[#71717A] uppercase">Stash（储藏）</span>
                <button @click="s.createSnapshotAction" class="text-[10px] text-[#D96B27] cursor-pointer hover:underline" title="执行 git stash 暂存当前未提交的工作区变更">＋ 储藏 (Stash)</button>
              </div>
              <div v-if="s.gitSnapshots.length === 0" class="text-[10px] text-[#A1A1AA]">无暂存记录</div>
              <div v-for="snap in s.gitSnapshots" :key="snap.id" class="flex items-center justify-between p-1.5 rounded bg-black/[0.02]">
                <span class="truncate font-mono text-[10px] text-[#52525B]">{{ snap.message || snap.id }} · {{ snap.time }}</span>
                <button @click="s.restoreSnapshotAction(snap.id)" class="text-[10px] text-[#D96B27] cursor-pointer shrink-0 hover:underline" title="执行 git stash pop 恢复修改">恢复 (Pop)</button>
              </div>
            </div>
            <div v-if="s.isGitLoading" class="p-6 text-center text-[#A1A1AA] text-xs flex flex-col items-center justify-center gap-2">
              <span class="animate-spin text-lg">⏳</span>
              <span>正在获取 Git 状态...</span>
            </div>
            <div v-else class="space-y-3">
              <!-- 已暂存变更 -->
              <div v-if="s.stagedTreeFiles.length > 0">
                <div class="text-[10px] font-bold text-[#10A37F] uppercase mb-1 flex items-center justify-between">
                  <span>暂存文件 (STAGED)</span>
                  <span class="text-[10px] font-mono bg-[#10A37F]/10 px-1 rounded">{{ s.stagedTreeFiles.length }}</span>
                </div>
                <div class="space-y-1">
                  <div
                    v-for="file in s.stagedTreeFiles"
                    :key="file.path"
                    @click="s.openFileDiff(file.path)"
                    class="p-1.5 rounded hover:bg-black/[0.04] cursor-pointer flex items-center justify-between font-mono text-[11px] group"
                  >
                    <span class="truncate flex-1">{{ file.path }}</span>
                    <div class="flex items-center gap-1.5 shrink-0">
                      <span :class="file.color" class="font-bold">{{ file.type }}</span>
                      <button
                        @click="s.unstageFileAction(file.path, $event)"
                        class="opacity-0 group-hover:opacity-100 text-[#71717A] hover:text-red-500 px-1 rounded hover:bg-black/[0.05] transition-opacity cursor-pointer text-xs"
                        title="取消暂存此文件"
                      >
                        −
                      </button>
                    </div>
                  </div>
                </div>
              </div>

              <!-- 工作区变更 -->
              <div>
                <div class="text-[10px] font-bold text-[#71717A] uppercase mb-1 flex items-center justify-between">
                  <span>变更文件 (WORKING TREE)</span>
                  <span class="flex gap-1 normal-case">
                    <button v-if="s.workingTreeFiles.length" @click="s.stageAllWorking" class="text-[#10A37F] cursor-pointer">全部暂存</button>
                    <button v-if="s.workingTreeFiles.length" @click="s.revertAllWorking" class="text-red-500 cursor-pointer">全部还原</button>
                  </span>
                </div>
                <div v-if="s.workingTreeFiles.length === 0" class="p-3 text-center text-[#A1A1AA] text-xs bg-black/[0.02] rounded-lg">
                  ✓ 工作区干净，无未暂存改动
                </div>
                <div v-else class="space-y-1">
                  <div
                    v-for="file in s.workingTreeFiles"
                    :key="file.path"
                    @click="s.openFileDiff(file.path)"
                    class="p-1.5 rounded hover:bg-black/[0.04] cursor-pointer flex items-center justify-between font-mono text-[11px] group"
                  >
                    <span class="truncate">{{ file.path }}</span>
                    <div class="flex items-center gap-1 shrink-0">
                      <span :class="file.color" class="font-bold">{{ file.type }}</span>
                      <button @click="s.stagePath(file.path, $event)" class="opacity-0 group-hover:opacity-100 text-[#10A37F] cursor-pointer" title="暂存此文件">+</button>
                      <button @click="s.revertPath(file.path, $event)" class="opacity-0 group-hover:opacity-100 text-red-500 cursor-pointer" title="还原此文件">↶</button>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <div class="pt-2 border-t border-black/[0.06] space-y-2">
              <div class="relative">
                <textarea v-model="s.commitMessage" rows="2" placeholder="提交信息 (Ctrl+Enter 提交)..." class="w-full p-2 text-xs font-mono rounded-lg border border-black/[0.08] focus:outline-none focus:border-[#D96B27] resize-none" @keydown.ctrl.enter="s.handleGitCommit"></textarea>
                <button @click="s.suggestCommitMessage" class="absolute right-2 bottom-2 px-1.5 py-0.5 rounded text-[10px] bg-[#FAF8F5] border border-black/[0.06] text-[#71717A] hover:text-[#D96B27] cursor-pointer" title="根据 git status / diff --stat 生成 Conventional Commit">从 Diff 生成</button>
              </div>
              <div class="flex items-center gap-1">
                <button @click="s.handleGitCommit" class="flex-1 py-1.5 rounded-lg bg-[#18181B] hover:bg-[#D96B27] text-white text-xs cursor-pointer">✓ 提交更改</button>
                <button @click="s.gitPushAction" class="px-2 py-1.5 rounded-lg bg-[#18181B] hover:bg-[#D96B27] text-white text-xs cursor-pointer" title="git push">↑</button>
              </div>
            </div>
          </div>
        </div>
      </aside>
</template>

<script setup lang="ts">
import { useWorkbenchStore } from '../stores/workbench'
import FileTreeNode from './FileTreeNode.vue'
const s = useWorkbenchStore()
</script>

