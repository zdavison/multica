# Update (re-import) a skill from its source

Date: 2026-07-31
Status: Approved (design)
Branch: `update-skill-from-source` (PR into `fork/main`; a separate upstream PR to follow later)

## Problem

A skill imported from an external URL source (github.com, skills.sh, clawhub.ai) is a
point-in-time snapshot. When the upstream skill changes, there is currently no way to
pull the latest version — a user must delete the skill and re-import it, losing the
skill's identity (id) and its agent assignments.

We want a one-click **"Update"** action in a skill's `...` (kebab) menu that re-runs the
import from the skill's stored source, overwriting the skill in place.

This does not exist upstream (`origin/main` verified). The building blocks do:

- Skills store provenance in `config.origin = { type, source_url, ... }`, written by
  `finishSkillImport`. `type ∈ { github, skills_sh, clawhub, runtime_local }`; absent =
  manual.
- `POST /api/skills/import` already fetches a skill from a URL (`detectImportSource` +
  per-source fetchers) and, with `on_conflict: "overwrite"`, re-imports onto an existing
  skill via `overwriteSkillWithFiles` (creator-only).

## Scope

**In scope:** re-import for URL-based origins only — `github`, `skills_sh`, `clawhub` —
which store a re-fetchable `source_url`.

**Out of scope (explicit non-goals, possible follow-ups):**

- `runtime_local` re-import. It has no URL; it requires the original runtime to be
  connected *now* and goes through a different async initiate/poll flow, with its own
  edge cases (runtime offline, source file moved/deleted).
- `manual` skills (no origin) — nothing to update from.
- An "Update" button on the detail-page origin card. v1 is the list-row kebab only, per
  the request ("the `...` menu of a skill").

## Design

### Backend — dedicated endpoint

`POST /api/skills/{id}/reimport` → `Handler.ReimportSkill`, registered inside the existing
`r.Route("/api/skills", ...)` group.

The source URL is **read server-side from stored provenance**; the client never supplies a
URL. This means a caller cannot repoint "Update" at an arbitrary URL, and a locally
renamed skill is still updatable (we target by id, not by name collision).

Handler flow:

1. Resolve workspace (`resolveWorkspaceID`) and require user (`requireUserID`).
2. `loadSkillForUser(id)` — resolves a UUID or human id and enforces workspace
   membership; subsequent writes use the resolved `skill.ID` (per Backend UUID Rules).
3. Decode `skill.Config` JSONB and read `origin`. Require `origin.type ∈
   { github, skills_sh, clawhub }` **and** a non-empty `source_url`; otherwise respond
   with a clear 4xx ("this skill was not imported from an updatable source"). This is a
   defensive guard — the UI already hides the action for these skills.
4. Creator check: `canOverwriteSkillByLocalImport(userID, skill)` → 403 otherwise (same
   rule the existing overwrite path enforces).
5. `detectImportSource(source_url)` → fetch the bundle via the existing per-source
   fetchers. The fetch `switch` in `ImportSkill` (clawhub / skills_sh / github) is
   extracted into a small shared helper `fetchImportedSkill(ctx, client, source, url)` so
   `ImportSkill` and `ReimportSkill` share exactly one fetch path.
6. `overwriteSkillWithFiles(skillOverwriteInput{ WorkspaceID, TargetSkillID: skill.ID,
   UserID, ExpectedName: skill.Name, Description, Content, Config: {origin}, Files,
   Authz: overwriteAuthzCreatorOrManager, ExpectedUpdatedAt: skill.UpdatedAt })`.
   This replaces description / content / config(origin) / file set in place while
   preserving the skill's id and current name. Passing `ExpectedName = skill.Name`
   satisfies the existing name-match guard trivially (same skill), so an upstream rename
   does not block the update and the local name is preserved.

   The fetch in step 5 runs for up to `importFetchTimeout`. At write time, neither the
   permission verdict nor the skill snapshot from steps 1–4 still holds. The transaction
   therefore locks the target row `FOR UPDATE` and re-derives both. `Authz` re-reads
   workspace membership and re-applies "current owner/admin, or current creator".
   `ExpectedUpdatedAt` is a compare-and-set against the row read in step 2. A caller
   demoted or removed mid-fetch gets 403. A concurrent edit gets 409 and survives.
7. On success: publish `EventSkillUpdated` and return the updated
   `SkillWithFilesResponse` (mirrors the `on_conflict: overwrite` result).

Error handling reuses existing mappings:

- Fetch failures → `importFetchErrorResponse` (413 cap / 502 bad gateway / 503 retryable
  / 504 timeout).
- Skill deleted between load and write → the overwrite path's `errSkillOverwriteNotFound`
  → 404-class failure.
- Membership or role lost during the fetch → `errSkillOverwriteForbidden` → 403.
- Skill edited during the fetch → `errSkillOverwriteStale` → 409.
- Non-creator → 403. Non-URL / missing origin → 4xx from step 3.

No DB migration. No schema change (provenance already lives in `config`).

### Frontend

- **API client** (`packages/core/api/client.ts`): `reimportSkill(id: string)` →
  `POST /api/skills/{id}/reimport`. Parse the response through the existing skill zod
  schema with `parseWithFallback` (never cast network JSON), per API Compatibility rules;
  add a malformed-response test.
- **Origin helper** (`packages/views/skills/lib/origin.ts`): `isUpdatableOrigin(origin)` —
  true when `type ∈ { github, skills_sh, clawhub }` and `source_url` is present. Built on
  the existing `readOrigin(skill)`.
- **Kebab menu** (`packages/views/skills/components/skill-list-actions.tsx`,
  `SkillRowActions`): add an "Update" `DropdownMenuItem`, shown only when
  `row.canEdit && isUpdatableOrigin(readOrigin(row.skill))` — i.e. anyone who
  can edit the skill (workspace owner/admin, or its creator) and a URL origin —
  matching the server's `canManageSkill` authorization. Placed above the Delete
  separator/item.
- **Confirm dialog**: an `AlertDialog` with destructive framing — "Update from source?
  This replaces the skill's content and files with the latest from `<source_url>`; local
  changes will be lost." On confirm: call `api.reimportSkill(id)` with a visible pending
  state; on success show a success toast, invalidate `workspaceKeys.skills(wsId)` and
  patch the skill-detail cache; on failure show an error toast and leave the skill
  unchanged.
- **i18n**: add the new keys (action label, dialog title/body/confirm, toasts) to
  `packages/views/locales/`. Read
  `apps/docs/content/docs/developers/conventions.mdx` (and `.zh.mdx`) first for the
  glossary and Chinese product voice.

### Data flow

```
kebab "Update"
  -> confirm dialog (shows source_url)
    -> api.reimportSkill(id)  [POST /api/skills/{id}/reimport]
       -> loadSkillForUser -> read config.origin.source_url (server-side)
          -> detectImportSource + fetchImportedSkill
             -> overwriteSkillWithFiles(TargetSkillID = skill.ID)
                -> EventSkillUpdated + SkillWithFilesResponse
    -> parseWithFallback(schema) -> invalidate skills query + patch detail -> toast
```

## Testing

Backend (`server/internal/handler/skill_reimport_test.go`; follow how existing import
tests stub the HTTP source — confirmed during planning):

- Success: re-import overwrites description/content and the file set; id and name
  preserved; `EventSkillUpdated` published.
- Non-creator → 403.
- Manual skill (no origin) and `runtime_local` origin → 4xx (not updatable).
- Malformed / missing `source_url` in origin → 4xx.
- Source-fetch failure maps to the expected status (via `importFetchErrorResponse`).

Frontend (`packages/views/skills/*.test.tsx` and a core schema test):

- Kebab visibility matrix: `github` origin + editor → "Update" shown; `manual` → hidden;
  non-editor → hidden.
- Confirm → `reimportSkill` called → skills query invalidated; error path shows a toast
  and does not mutate cache.
- Malformed `reimportSkill` response handled by the schema (API-compat).

## Rollout / git

- Branch `update-skill-from-source` off the upstream-synced `main`.
- PR into `fork/main` (`zdavison/multica`). After that PR is approved, a separate PR of
  the same change is opened upstream (`multica-ai/multica`) as independent work.
