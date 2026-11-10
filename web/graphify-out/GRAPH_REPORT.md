# Graph Report - web  (2026-11-10)

## Corpus Check
- 26 files · ~46,627 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 2 file(s) not represented in the graph (top: (none) 1, .css 1)

## Summary
- 352 nodes · 1077 edges · 15 communities (14 shown, 1 thin omitted)
- Extraction: 99% EXTRACTED · 1% INFERRED · 0% AMBIGUOUS · INFERRED: 6 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `e4e436af`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- t
- ui.tsx
- pages.tsx
- devDependencies
- shell.tsx
- dependencies
- compilerOptions
- compilerOptions
- fill-missing.mjs
- .oxlintrc.json
- React + TypeScript + Vite
- tsconfig.json
- panel/main.tsx
- setup/main.tsx
- scripts

## God Nodes (most connected - your core abstractions)
1. `t()` - 67 edges
2. `RulesPage()` - 38 edges
3. `Button()` - 36 edges
4. `api()` - 34 edges
5. `Mirrors()` - 30 edges
6. `cn()` - 29 edges
7. `Bots()` - 29 edges
8. `Setup()` - 28 edges
9. `Local()` - 27 edges
10. `Shared()` - 26 edges

## Surprising Connections (you probably didn't know these)
- `ToastHost()` --calls--> `useUiLang()`  [EXTRACTED]
  src/panel/main.tsx → src/lib/i18n.tsx
- `summary()` --calls--> `t()`  [EXTRACTED]
  src/components/autofilter.tsx → src/lib/i18n.tsx
- `Part()` --calls--> `FieldLabel()`  [EXTRACTED]
  src/components/autofilter.tsx → src/components/ui.tsx
- `Part()` --calls--> `MultiSelect()`  [EXTRACTED]
  src/components/autofilter.tsx → src/components/widgets.tsx
- `Part()` --calls--> `t()`  [EXTRACTED]
  src/components/autofilter.tsx → src/lib/i18n.tsx

## Import Cycles
- None detected.

## Communities (15 total, 1 thin omitted)

### Community 0 - "t"
Cohesion: 0.22
Nodes (21): clsx, tailwind-merge, save(), Disclosure(), loginCopy(), LoginForm(), next(), t() (+13 more)

### Community 1 - "ui.tsx"
Cohesion: 0.07
Nodes (46): name, private, type, version, class-variance-authority, cmdk, lucide-react, oxlint (+38 more)

### Community 2 - "pages.tsx"
Cohesion: 0.10
Nodes (78): Alert(), Button(), Card(), Checkbox(), Collapse(), Empty(), FieldLabel(), Input() (+70 more)

### Community 3 - "devDependencies"
Cohesion: 0.18
Nodes (11): devDependencies, oxlint, tailwindcss, @tailwindcss/vite, tw-animate-css, @types/node, @types/react, @types/react-dom (+3 more)

### Community 4 - "shell.tsx"
Cohesion: 0.11
Nodes (29): Sheet(), confirmDialog(), pausePolling(), pollingPaused(), adoptServerLanguage(), applyDom(), catalogs, current (+21 more)

### Community 5 - "dependencies"
Cohesion: 0.09
Nodes (23): dependencies, class-variance-authority, clsx, cmdk, lucide-react, @radix-ui/react-checkbox, @radix-ui/react-collapsible, @radix-ui/react-dialog (+15 more)

### Community 6 - "compilerOptions"
Cohesion: 0.09
Nodes (21): compilerOptions, allowArbitraryExtensions, allowImportingTsExtensions, erasableSyntaxOnly, jsx, lib, module, moduleDetection (+13 more)

### Community 7 - "compilerOptions"
Cohesion: 0.12
Nodes (16): compilerOptions, allowImportingTsExtensions, erasableSyntaxOnly, lib, module, moduleDetection, noEmit, noFallthroughCasesInSwitch (+8 more)

### Community 8 - "fill-missing.mjs"
Cohesion: 0.12
Nodes (12): @tailwindcss/vite, vite, @vitejs/plugin-react, files(), keys, root, extra, files() (+4 more)

### Community 9 - ".oxlintrc.json"
Cohesion: 0.33
Nodes (5): plugins, rules, react/only-export-components, react/rules-of-hooks, $schema

### Community 10 - "React + TypeScript + Vite"
Cohesion: 0.50
Nodes (3): Expanding the Oxlint configuration, React Compiler, React + TypeScript + Vite

### Community 12 - "panel/main.tsx"
Cohesion: 0.14
Nodes (13): react-dom, react-router, @tanstack/react-query, Group(), LogPage, LogRow, Logs(), Pick() (+5 more)

### Community 13 - "setup/main.tsx"
Cohesion: 0.10
Nodes (33): sonner, AutoFilterForm(), empty, Filter, norm(), Part(), PartKey, Rule (+25 more)

### Community 14 - "scripts"
Cohesion: 0.40
Nodes (5): scripts, build, dev, lint, preview

## Knowledge Gaps
- **10 isolated node(s):** `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip`, `@types/node`, `@types/react` (+5 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 151 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `dependencies` connect `dependencies` to `ui.tsx`?**
  _High betweenness centrality (0.103) - this node is a cross-community bridge._
- **What connects `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip` to the rest of the system?**
  _10 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `ui.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.06966618287373004 - nodes in this community are weakly interconnected._
- **Why does `t()` connect `t` to `ui.tsx`, `pages.tsx`, `shell.tsx`, `panel/main.tsx`, `setup/main.tsx`?**
  _High betweenness centrality (0.069) - this node is a cross-community bridge._
- **Should `pages.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.1002921129503408 - nodes in this community are weakly interconnected._
- **Why does `react` connect `ui.tsx` to `panel/main.tsx`, `pages.tsx`, `shell.tsx`, `setup/main.tsx`?**
  _High betweenness centrality (0.065) - this node is a cross-community bridge._
- **Should `shell.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.11229946524064172 - nodes in this community are weakly interconnected._