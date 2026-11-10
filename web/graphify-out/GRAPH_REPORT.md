# Graph Report - web  (2026-11-10)

## Corpus Check
- 26 files · ~47,263 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 2 file(s) not represented in the graph (top: (none) 1, .css 1)

## Summary
- 348 nodes · 1067 edges · 14 communities (13 shown, 1 thin omitted)
- Extraction: 99% EXTRACTED · 1% INFERRED · 0% AMBIGUOUS · INFERRED: 7 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `e0fcf41d`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- pages.tsx
- ui.tsx
- RulesPage
- devDependencies
- shell.tsx
- dependencies
- compilerOptions
- compilerOptions
- fill-missing.mjs
- .oxlintrc.json
- React + TypeScript + Vite
- tsconfig.json
- widgets.tsx
- setup/main.tsx

## God Nodes (most connected - your core abstractions)
1. `t()` - 66 edges
2. `RulesPage()` - 37 edges
3. `Button()` - 35 edges
4. `api()` - 33 edges
5. `Mirrors()` - 30 edges
6. `cn()` - 29 edges
7. `Bots()` - 29 edges
8. `Setup()` - 28 edges
9. `Local()` - 27 edges
10. `Shared()` - 26 edges

## Surprising Connections (you probably didn't know these)
- `next()` --calls--> `t()`  [EXTRACTED]
  src/components/widgets.tsx → src/lib/i18n.tsx
- `open()` --calls--> `t()`  [EXTRACTED]
  src/setup/main.tsx → src/lib/i18n.tsx
- `summary()` --calls--> `t()`  [EXTRACTED]
  src/components/autofilter.tsx → src/lib/i18n.tsx
- `Part()` --calls--> `FieldLabel()`  [EXTRACTED]
  src/components/autofilter.tsx → src/components/ui.tsx
- `Part()` --calls--> `t()`  [EXTRACTED]
  src/components/autofilter.tsx → src/lib/i18n.tsx

## Import Cycles
- None detected.

## Communities (14 total, 1 thin omitted)

### Community 0 - "pages.tsx"
Cohesion: 0.09
Nodes (45): clsx, tailwind-merge, Disclosure(), Progress(), Toggles(), panelCode(), t(), ago() (+37 more)

### Community 1 - "ui.tsx"
Cohesion: 0.07
Nodes (36): name, private, scripts, build, dev, lint, preview, type (+28 more)

### Community 2 - "RulesPage"
Cohesion: 0.18
Nodes (52): Alert(), Button(), Card(), Collapse(), Empty(), FieldLabel(), Input(), Select() (+44 more)

### Community 3 - "devDependencies"
Cohesion: 0.18
Nodes (11): devDependencies, oxlint, tailwindcss, @tailwindcss/vite, tw-animate-css, @types/node, @types/react, @types/react-dom (+3 more)

### Community 4 - "shell.tsx"
Cohesion: 0.10
Nodes (32): react-router, Sheet(), confirmDialog(), pollingPaused(), adoptServerLanguage(), applyDom(), catalogs, current (+24 more)

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

### Community 12 - "widgets.tsx"
Cohesion: 0.08
Nodes (37): sonner, @tanstack/react-query, AutoFilterForm(), save(), empty, Filter, norm(), Part() (+29 more)

### Community 13 - "setup/main.tsx"
Cohesion: 0.12
Nodes (22): react-dom, CardBody(), Checkbox(), Textarea(), ProgressSteps(), uiLang(), KindPicks(), client (+14 more)

## Knowledge Gaps
- **10 isolated node(s):** `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip`, `@types/node`, `@types/react` (+5 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 149 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `dependencies` connect `dependencies` to `ui.tsx`?**
  _High betweenness centrality (0.104) - this node is a cross-community bridge._
- **What connects `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip` to the rest of the system?**
  _10 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `pages.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.08695652173913043 - nodes in this community are weakly interconnected._
- **Why does `t()` connect `pages.tsx` to `ui.tsx`, `RulesPage`, `shell.tsx`, `widgets.tsx`, `setup/main.tsx`?**
  _High betweenness centrality (0.069) - this node is a cross-community bridge._
- **Should `ui.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.06585365853658537 - nodes in this community are weakly interconnected._
- **Why does `react` connect `ui.tsx` to `pages.tsx`, `RulesPage`, `shell.tsx`, `widgets.tsx`, `setup/main.tsx`?**
  _High betweenness centrality (0.065) - this node is a cross-community bridge._
- **Should `shell.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.10099573257467995 - nodes in this community are weakly interconnected._