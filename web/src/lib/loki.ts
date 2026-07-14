// Loki / LogQL 相关的纯工具函数。
//
// 这些函数本身无 React / 状态依赖，从 pages/Logs.tsx 抽出来给其它
// 页面（如 IncidentDetail 的 Grafana explore 链接）复用，避免循环
// import 与页面模块的副作用求值成本。

// Escape a string for use inside a Loki line-filter string literal.
//
// Loki's query parser runs a string-literal lexer over "..." before
// handing the contents to the line-filter regex engine. The lexer treats
// `\X` as a Go-style escape sequence; only `\\`, `\"`, `\n`, `\r`, `\t`,
// `\f` etc. are recognised. A bare `\.`, `\+`, `\(`, `\[`, ... — the
// very escapes you would reach for when quoting RE2 metacharacters —
// gets rejected with `parse error at line 1, col N: invalid char
// escape` (verified on grafana/loki:3.4.0, the version we run on
// 192.168.25.30).
//
// To survive both passes we double-escape:
//   1) prefix every RE2 metachar (including `\` itself) with `\`,
//      producing a syntactically-correct RE2 pattern, then
//   2) prefix every literal `\` again with `\`, so the Loki string
//      lexer peels one layer off and leaves the RE2 pattern intact.
//
// Examples (final byte stream sent to Loki):
//   "error"         → "error"         (no change)
//   "192.168.33.91" → "192\\.168\\.33\\.91"
//   "a.b*c"         → "a\\.b\\*c"
//   "host[1]"       → "host\\[1\\]"
export function escapeLokiLineFilter(s: string): string {
  return s
    .replace(/[\\.*+?^${}()|[\]]/g, '\\$&')
    .replace(/\\/g, '\\\\');
}