# 1. Purpose

`AGENTS.md` is the operational guide for coding agents working on InsightFlow AI. Follow it with product and technical references; it does not replace them.

InsightFlow AI is a planned analytics app for non-technical users: upload CSV/XLSX, inspect datasets, ask natural-language questions, and save verified analysis. Target architecture is a monorepo with React/TypeScript, Go/Gin API, internal Python/FastAPI analytics engine, PostgreSQL metadata, Redis ephemeral state, and Parquet files. The repository currently contains documentation only; this stack and structure are planned, not implemented.

Agents must understand the request, respect project boundaries and security controls, preserve existing work, validate changes, and report limitations honestly.

| Document | Purpose | When to read |
|---|---|---|
| `docs/core_docs/PRD_InsightFlow_AI.md` | Product requirements and decision authority | Before feature or behavior changes |
| `docs/core_docs/Architecture.md` | System architecture and target structure | Before technical or module changes |
| `docs/core_docs/Security.md` | Security requirements | For auth, API, data, storage, AI, or deployment work |
| `docs/core_docs/Design-system.md` | Code conventions | Before source changes |
| `docs/core_docs/Design.md` | Visual and UX rules | For frontend/UI work |
| `AGENTS.md` | Agent workflow | At task start |

## Agent skills

- Always use `caveman` and `karpathy-guidelines`.
- Before writing or editing code, use `ponytail`, `ponytail-review`, and `clean-code`.
- For codebase questions, architecture, or project content, use `graphify` first when its graph is available; otherwise inspect the relevant source and docs. Do not invent graph findings.
- Use `senior-backend` for Go/API/DB/Engine backend work; `senior-security` for security-sensitive work; `senior-architect` for architecture decisions; `senior-frontend` for React/UI work. Combine only skills relevant to task.

# 2. Before You Start

- Read the request, identify affected areas, risks, and relevant references.
- Inspect actual files and existing conventions before relying on target structure in docs.
- Check current changes before editing; do not overwrite user work or perform destructive actions without approval.
- Make a short plan for multi-file or high-risk work. Ask only when ambiguity blocks safe progress or changes product/architecture behavior materially.
- Treat PRD status labels as meaningful. Do not promote recommendations (`[R]`) into confirmed requirements.

# 3. General Rules

- PRD is product source of truth. For conflicts, use its stated authority: ADR > data/API/folder sections > other sections. Report material conflicts.
- Follow `Architecture.md`; do not add dependencies, services, layers, or scope without demonstrated need and required ADR.
- Make the smallest focused change. Reuse existing code and dependencies; avoid unrelated refactors.
- Preserve contracts and backward compatibility unless change is requested and justified.
- Never commit secrets, `.env` files, user datasets, or runtime storage. Do not commit automatically unless asked.
- Do not run destructive Git, database, or production operations without explicit approval.
- Report changed files, validation run and result, and unresolved limits. Never claim checks passed unless run.

# 4. Code Guidelines

Use `docs/core_docs/Design-system.md` as source for coding conventions and `docs/core_docs/Architecture.md` for boundaries. Actual repository patterns and configured tools take precedence over unimplemented target examples when they do not conflict with requirements.

- Keep frontend feature boundaries; use TanStack Query for server state and Zustand only for client/UI state.
- Keep Go dependencies directed `handler → service → repository`; keep HTTP concerns out of services and business rules out of handlers.
- Keep Python Engine internal; use validated Pydantic contracts and deterministic, allowlisted analytics tools.
- Validate untrusted input at boundaries. Use parameterized SQL and server-built storage paths.
- Add or update focused tests for behavior changes and failure cases. Use only configured test, lint, format, and type-check commands.
- No package manifest, source code, test runner, or project scripts are present yet. Do not invent runnable project commands.

# 5. Security & Best Practices

`docs/core_docs/Security.md` is the detailed security reference; its controls are requirements, not proof of implementation.

- Go owns authentication and authorization. Check workspace membership atomically with resource queries; never trust frontend role claims or Engine authorization.
- Never expose secrets, tokens, dataset contents, stack traces, or internal paths in logs or client errors.
- Treat uploads, dataset content, prompts, LLM output, and tool arguments as untrusted. Validate type, size, schema, permissions, and bounds.
- LLM must not calculate dataset metrics, execute arbitrary code, or issue free-form SQL. Use registered deterministic tools; validate plans, results, and grounding.
- Keep Redis ephemeral with TTL; PostgreSQL/storage remain sources of truth. Follow fail-open/fail-closed behavior specified per operation.
- Do not weaken security controls to make tests pass. Review access control, error paths, and secret exposure in security-sensitive changes.

# 6. Useful Commands

No package manifests, README, build/test configuration, or project scripts exist in the repository. No project command is verified; do not run guessed commands. Add commands here only after confirming them in project configuration or documentation.

| Task | Command | Description |
|---|---|---|
| Install, develop, test, lint, format, type-check, build | Not configured | No verified project scripts or manifests |

## Graphify

Use Graphify for repository exploration and keep generated artifacts out of version control. Run from repository root when Graphify is installed:

```powershell
graphify . --no-viz
```

Graphify may require a configured semantic backend for documentation extraction. Do not install dependencies or request credentials without user approval; report failure and continue by inspecting relevant files. Never present incomplete graph output as complete.

# 7. Need Help?

- If a reference is missing, inspect existing implementation and mark assumptions; do not invent requirements.
- If architecture or product behavior is unclear, describe options and impact, then ask before material or irreversible changes.
- If validation fails, identify whether change caused it, fix only within scope, rerun relevant checks, and report remaining failures.
- Stop and ask before destructive data changes, production operations, or security trade-offs.
- Final response must state changes, files, validation results, and known limitations.
