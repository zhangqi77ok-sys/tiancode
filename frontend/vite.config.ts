import { writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// keepDistPlaceholder 在构建产物写入后，把入库的占位文件 frontend/dist/.gitkeep 补回。
//
// 为什么需要它：main.go 有 `//go:embed all:frontend/dist`，要求该目录**存在**才能编译；
// 而 vite build 默认清空 outDir，会把入库的 .gitkeep 一并删除。不补回的后果有两个：
//   1) 每次 `npm run build` 后 git 工作区都凭空多出一条"已删除"，而本仓库的提交纪律
//      要求 `git add` 前先审计 `git status`——常态性脏状态会淹没真正的改动；
//   2) 一旦有人把这条删除提交进去，全新克隆又回到"Go 侧因 go:embed 缺目录而无法编译"。
// 用 `import.meta.url` 而非 `__dirname`：本包是 "type": "module"（ESM），无 __dirname。
function keepDistPlaceholder(): Plugin {
  return {
    name: 'tiancode-keep-dist-placeholder',
    apply: 'build',
    closeBundle() {
      writeFileSync(fileURLToPath(new URL('./dist/.gitkeep', import.meta.url)), '')
    },
  }
}

// Vite 配置：Vue3 + Tailwind4。产物输出到 dist/（gitignore，构建时生成）。
export default defineConfig({
  plugins: [vue(), tailwindcss(), keepDistPlaceholder()],
})
