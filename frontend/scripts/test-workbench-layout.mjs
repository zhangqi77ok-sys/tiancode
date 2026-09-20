import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'
import assert from 'assert'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const frontendDir = path.resolve(__dirname, '..')

console.log('--- [TDD] Running Workbench Layout Architecture Checks ---')

// 1. 检验 workbench.ts 状态与方法契约
const storePath = path.join(frontendDir, 'src/stores/workbench.ts')
const storeContent = fs.readFileSync(storePath, 'utf8')

assert(storeContent.includes('isLeftDrawerOpen'), 'workbench.ts must define isLeftDrawerOpen')
assert(storeContent.includes('editorSplitPercent'), 'workbench.ts must define editorSplitPercent')
assert(storeContent.includes('startSplitResize'), 'workbench.ts must implement startSplitResize')
assert(storeContent.includes('resetSplitRatio'), 'workbench.ts must implement resetSplitRatio')
assert(storeContent.includes('toggleEditorPanel'), 'workbench.ts must implement toggleEditorPanel')
assert(storeContent.includes('toggleEditorFullscreen'), 'workbench.ts must implement toggleEditorFullscreen')
assert(/activeActivity\s*=\s*ref<.*'files'.*>/.test(storeContent) || storeContent.includes("'files'"), 'workbench.ts must support files activity')
assert(storeContent.includes('toggleLeftDrawer'), 'workbench.ts must implement toggleLeftDrawer')

// 2. 检验 LeftDrawer.vue 文件树归位与单侧边栏设计
const leftDrawerPath = path.join(frontendDir, 'src/components/LeftDrawer.vue')
const leftDrawerContent = fs.readFileSync(leftDrawerPath, 'utf8')

assert(leftDrawerContent.includes("s.activeActivity === 'files'"), "LeftDrawer.vue must handle activeActivity === 'files'")
assert(leftDrawerContent.includes('FileTreeNode'), 'LeftDrawer.vue must render FileTreeNode')
assert(leftDrawerContent.includes('displayFileTree'), 'LeftDrawer.vue must use displayFileTree')

// 3. 检验 DiffWorkspace.vue 消除内嵌重复文件树，独占纯净编辑器
const diffWorkspacePath = path.join(frontendDir, 'src/components/DiffWorkspace.vue')
const diffWorkspaceContent = fs.readFileSync(diffWorkspacePath, 'utf8')

assert(!diffWorkspaceContent.includes('左侧工作区文件树与全盘检索面板'), 'DiffWorkspace.vue must NOT contain redundant embedded file tree')
assert(diffWorkspaceContent.includes('editorSplitPercent'), 'DiffWorkspace.vue must bind dynamic editorSplitPercent')

// 4. 检验 App.vue 顶栏与中间拖拽手柄 Sash
const appPath = path.join(frontendDir, 'src/App.vue')
const appContent = fs.readFileSync(appPath, 'utf8')

assert(appContent.includes('startSplitResize'), 'App.vue must bind startSplitResize on splitter sash')
assert(appContent.includes('resetSplitRatio'), 'App.vue must bind resetSplitRatio on splitter sash')
assert(appContent.includes('cursor-col-resize'), 'App.vue splitter sash must have cursor-col-resize')

// 5. 检验 ChatCockpit.vue 移除冲突重复代码开关
const chatCockpitPath = path.join(frontendDir, 'src/components/ChatCockpit.vue')
const chatCockpitContent = fs.readFileSync(chatCockpitPath, 'utf8')
assert(!chatCockpitContent.includes('💻 代码面板'), 'ChatCockpit.vue must NOT have redundant code panel toggle button')

console.log('✓ All Workbench Layout Architecture Checks PASSED!')
