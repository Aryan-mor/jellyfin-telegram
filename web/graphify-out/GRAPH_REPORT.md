# Graph Report - web  (2026-11-17)

## Corpus Check
- 27 files · ~52,588 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 2 file(s) not represented in the graph (top: (none) 1, .css 1)

## Summary
- 386 nodes · 1228 edges · 15 communities (14 shown, 1 thin omitted)
- Extraction: 99% EXTRACTED · 1% INFERRED · 0% AMBIGUOUS · INFERRED: 9 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `df58f49a`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- pages.tsx
- ui.tsx
- t
- devDependencies
- setup/main.tsx
- dependencies
- compilerOptions
- compilerOptions
- fill-missing.mjs
- .oxlintrc.json
- React + TypeScript + Vite
- tsconfig.json
- logs.tsx
- autofilter.tsx
- scripts

## God Nodes (most connected - your core abstractions)
1. `t()` - 80 edges
2. `api()` - 43 edges
3. `Button()` - 39 edges
4. `RulesPage()` - 38 edges
5. `cn()` - 32 edges
6. `Mirrors()` - 29 edges
7. `Bots()` - 29 edges
8. `Setup()` - 28 edges
9. `Local()` - 27 edges
10. `Shared()` - 26 edges

## Surprising Connections (you probably didn't know these)
- `apiProxy()` --indirect_call--> `api()`  [INFERRED]
  vite.config.ts → src/lib/api.ts
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

### Community 0 - "pages.tsx"
Cohesion: 0.07
Nodes (42): Disclosure(), Progress(), Toggles(), Tone, bytes(), plural(), AppAccount, BotCheckResult (+34 more)

### Community 1 - "ui.tsx"
Cohesion: 0.06
Nodes (56): name, private, type, version, class-variance-authority, clsx, cmdk, lucide-react (+48 more)

### Community 2 - "t"
Cohesion: 0.17
Nodes (48): save(), Alert(), Button(), Checkbox(), Collapse(), FieldLabel(), Input(), CopyField() (+40 more)

### Community 3 - "devDependencies"
Cohesion: 0.18
Nodes (11): devDependencies, oxlint, tailwindcss, @tailwindcss/vite, tw-animate-css, @types/node, @types/react, @types/react-dom (+3 more)

### Community 4 - "setup/main.tsx"
Cohesion: 0.07
Nodes (56): react-dom, react-router, sonner, Sheet(), confirmDialog(), api(), ApiError, panelCode() (+48 more)

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

### Community 12 - "logs.tsx"
Cohesion: 0.13
Nodes (41): @tanstack/react-query, Card(), Empty(), Table(), TBody(), TD(), TH(), THead() (+33 more)

### Community 13 - "autofilter.tsx"
Cohesion: 0.23
Nodes (11): AutoFilterForm(), empty, Filter, norm(), Part(), PartKey, Rule, summary() (+3 more)

### Community 14 - "scripts"
Cohesion: 0.40
Nodes (5): scripts, build, dev, lint, preview

## Knowledge Gaps
- **10 isolated node(s):** `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip`, `@types/node`, `@types/react` (+5 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 160 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `dependencies` connect `dependencies` to `ui.tsx`?**
  _High betweenness centrality (0.096) - this node is a cross-community bridge._
- **What connects `@radix-ui/react-scroll-area`, `@radix-ui/react-separator`, `@radix-ui/react-tooltip` to the rest of the system?**
  _10 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `pages.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.07419712070874862 - nodes in this community are weakly interconnected._
- **Why does `t()` connect `t` to `pages.tsx`, `ui.tsx`, `setup/main.tsx`, `logs.tsx`, `autofilter.tsx`?**
  _High betweenness centrality (0.085) - this node is a cross-community bridge._
- **Should `ui.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.05673076923076923 - nodes in this community are weakly interconnected._
- **Why does `api()` connect `setup/main.tsx` to `pages.tsx`, `ui.tsx`, `t`, `logs.tsx`, `autofilter.tsx`?**
  _High betweenness centrality (0.077) - this node is a cross-community bridge._
- **Should `setup/main.tsx` be split into smaller, more focused modules?**
  _Cohesion score 0.06713286713286713 - nodes in this community are weakly interconnected._