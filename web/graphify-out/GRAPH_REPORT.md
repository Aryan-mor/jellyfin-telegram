# Graph Report - web  (2026-11-09)

## Corpus Check
- 24 files · ~42,460 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 2 file(s) not represented in the graph (top: (none) 1, .css 1)

## Summary
- 320 nodes · 944 edges · 12 communities (11 shown, 1 thin omitted)
- Extraction: 99% EXTRACTED · 1% INFERRED · 0% AMBIGUOUS · INFERRED: 7 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `96d16864`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- pages.tsx
- ui.tsx
- RulesPage
- package.json
- shell.tsx
- dependencies
- compilerOptions
- compilerOptions
- fill-missing.mjs
- .oxlintrc.json
- React + TypeScript + Vite
- tsconfig.json

## God Nodes (most connected - your core abstractions)
1. `t()` - 54 edges
2. `RulesPage()` - 37 edges
3. `Mirrors()` - 30 edges
4. `Button()` - 29 edges
5. `Bots()` - 29 edges
6. `api()` - 28 edges
7. `cn()` - 27 edges
8. `Shared()` - 26 edges
9. `HomeVideos()` - 26 edges
10. `Local()` - 26 edges

## Surprising Connections (you probably didn't know these)
- `next()` --calls--> `t()`  [EXTRACTED]
  src/components/widgets.tsx → src/lib/i18n.tsx
- `Button()` --calls--> `cn()`  [EXTRACTED]
  src/components/ui.tsx → src/lib/utils.ts
- `ConfirmHost()` --calls--> `Button()`  [EXTRACTED]
  src/components/widgets.tsx → src/components/ui.tsx
- `LoginForm()` --calls--> `Button()`  [EXTRACTED]
  src/components/widgets.tsx → src/components/ui.tsx
- `MultiSelect()` --calls--> `Button()`  [EXTRACTED]
  src/components/widgets.tsx → src/components/ui.tsx

## Import Cycles
- None detected.

## Communities (12 total, 1 thin omitted)

### Community 0 - "pages.tsx"
Cohesion: 0.08
Nodes (46): clsx, tailwind-merge, Checkbox(), Disclosure(), Progress(), Toggles(), Tone, t() (+38 more)

### Community 1 - "ui.tsx"
Cohesion: 0.10
Nodes (40): lucide-react, react, sonner, buttonVariants, CardBody(), Command(), CommandEmpty(), CommandInput() (+32 more)

### Community 2 - "RulesPage"
Cohesion: 0.21
Nodes (47): Alert(), Button(), Card(), Collapse(), Empty(), FieldLabel(), Input(), Select() (+39 more)

### Community 3 - "package.json"
Cohesion: 0.05
Nodes (44): devDependencies, oxlint, tailwindcss, @tailwindcss/vite, tw-animate-css, @types/node, @types/react, @types/react-dom (+36 more)

### Community 4 - "shell.tsx"
Cohesion: 0.09
Nodes (34): react-dom, react-router, @tanstack/react-query, Sheet(), api(), ApiError, panelCode(), pollingPaused() (+26 more)

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
Cohesion: 0.15
Nodes (9): files(), keys, root, extra, files(), keys, missing, files (+1 more)

### Community 9 - ".oxlintrc.json"
Cohesion: 0.33
Nodes (5): plugins, rules, react/only-export-components, react/rules-of-hooks, $schema

### Community 10 - "React + TypeScript + Vite"
Cohesion: 0.50
Nodes (3): Expanding the Oxlint configuration, React Compiler, React + TypeScript + Vite

## Knowledge Gaps
- **10 isolated node(s):** `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip`, `@types/node`, `@types/react` (+5 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 139 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `dependencies` connect `dependencies` to `package.json`?**
  _High betweenness centrality (0.111) - this node is a cross-community bridge._
- **What connects `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip` to the rest of the system?**
  _10 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `pages.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.0797872340425532 - nodes in this community are weakly interconnected._
- **Why does `react` connect `ui.tsx` to `pages.tsx`, `package.json`, `shell.tsx`?**
  _High betweenness centrality (0.064) - this node is a cross-community bridge._
- **Should `ui.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.09528214616096208 - nodes in this community are weakly interconnected._
- **Why does `t()` connect `pages.tsx` to `ui.tsx`, `RulesPage`, `shell.tsx`?**
  _High betweenness centrality (0.052) - this node is a cross-community bridge._
- **Should `package.json` be split into smaller, more focused modules?**
  _Cohesion score 0.04541062801932367 - nodes in this community are weakly interconnected._