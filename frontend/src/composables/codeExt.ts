// 代码块语言名 → 文件扩展名（阶段 3-3）。
//
// 为什么不能直接把语言名当扩展名：`typescript` 会写出 untitled.typescript、
// `bash` 会写出 untitled.bash——系统不认这些扩展名，双击打不开、编辑器也不高亮。
// 只做一张小映射表，映射不到就退回 .txt（宁可让人自己改，也不猜错一个扩展名）。
const LANG_EXT: Record<string, string> = {
  ts: 'ts',
  typescript: 'ts',
  tsx: 'tsx',
  js: 'js',
  javascript: 'js',
  jsx: 'jsx',
  mjs: 'mjs',
  cjs: 'cjs',
  py: 'py',
  python: 'py',
  go: 'go',
  vue: 'vue',
  json: 'json',
  jsonc: 'json',
  md: 'md',
  markdown: 'md',
  yml: 'yml',
  yaml: 'yml',
  toml: 'toml',
  sh: 'sh',
  bash: 'sh',
  shell: 'sh',
  zsh: 'sh',
  ps1: 'ps1',
  powershell: 'ps1',
  bat: 'bat',
  cmd: 'bat',
  html: 'html',
  xml: 'xml',
  css: 'css',
  scss: 'scss',
  less: 'less',
  sql: 'sql',
  java: 'java',
  kt: 'kt',
  rs: 'rs',
  c: 'c',
  h: 'h',
  cpp: 'cpp',
  hpp: 'hpp',
  cs: 'cs',
  rb: 'rb',
  php: 'php',
  diff: 'diff',
  patch: 'patch',
  ini: 'ini',
  conf: 'conf',
  log: 'log',
  text: 'txt',
  txt: 'txt',
}

// langToExt 返回扩展名（不带点）。空语言名、未知语言名一律给 txt。
export function langToExt(lang: string): string {
  const key = (lang || '').trim().toLowerCase().split(/\s+/)[0] ?? ''
  return LANG_EXT[key] ?? 'txt'
}

// defaultFileName 生成对话框里的默认名（用户可在对话框里改）。
export function defaultFileName(lang: string): string {
  return `untitled.${langToExt(lang)}`
}
