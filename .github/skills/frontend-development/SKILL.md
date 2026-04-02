---
name: frontend-development
description: 'Use when working on the austender frontend, static HTMX pages, frontend/index.html, frontend/search.html, config.local.js, browser-side fetch flows, or frontend smoke validation.'
argument-hint: 'Describe the page, user flow, or UI behavior to change.'
---

# Frontend Development

Repo-specific workflow for the static HTMX frontend in `frontend/`.

## When to Use
- Editing `frontend/index.html`, `frontend/search.html`, or `frontend/config.local.js`.
- Changing chat UI behavior, search UI behavior, model picker flows, or browser-side fetch logic.
- Updating frontend copy to match backend or storage changes.
- Verifying static pages and frontend smoke behavior.

## Constraints
- The frontend is static. Do not introduce a bundler, framework build step, or client runtime that changes deployment shape unless explicitly requested.
- Preserve the existing deployment model: static assets served directly, backend on `/api/*`.
- Keep configuration in `config.local.js` and deployment configuration, not hardcoded environment branches.
- Match the repo’s current stack: Bootstrap, HTMX-style static pages, deferred browser scripts.

## Workflow
1. Confirm which page owns the behavior: chat UI in `frontend/index.html`, search UI in `frontend/search.html`, local config in `frontend/config.local.js`.
2. Align request payloads and endpoint usage with the backend contract before editing browser code.
3. Keep UI text consistent with current backend/storage terminology, especially around ClickHouse-backed cache behavior.
4. Prefer small script changes inside the existing page structure instead of introducing new abstractions.
5. If backend behavior changed, ensure the frontend wording and endpoint expectations are updated in the same change.

## Validation
- Run `bash hack/test-frontend.sh` for the static smoke test.
- If the change depends on backend behavior, validate the matching server tests as well.
- Keep browser-side behavior resilient when remote libraries or backend endpoints are briefly unavailable.

## References
- Frontend overview: `frontend/README.md`
- Static smoke script: `hack/test-frontend.sh`