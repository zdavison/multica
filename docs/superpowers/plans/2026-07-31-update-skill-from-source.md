# Update (re-import) a skill from its source — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an "Update" action to a skill's `...` (kebab) menu that re-imports the skill in place from its stored external source, shown only for URL-sourced skills the current user created.

**Architecture:** A new dedicated backend endpoint `POST /api/skills/{id}/reimport` reads the skill's `config.origin.source_url` server-side, re-fetches via the existing per-source fetchers, and overwrites the skill in place through the existing `overwriteSkillWithFiles` (id-targeted, name preserved). The frontend adds an `api.reimportSkill(id)` call, a pure `canReimportSkill` gate, an "Update" kebab item, and a destructive confirm dialog.

**Tech Stack:** Go (Chi router, sqlc, pgx), React + TanStack Query + Zustand, Vitest, sonner toasts, i18next (en/ja/ko/zh-Hans).

## Global Constraints

- Backend: `gofmt`, `go vet`, checked errors. Handlers resolve path-param ids via `loadSkillForUser`; writes use the resolved `skill.ID` (Backend UUID Rules).
- No DB migration, no schema change — provenance already lives in `skill.config` JSONB.
- Do not add DB foreign keys / cascades. Not applicable here (no new tables).
- Re-import is creator-only, mirroring `canOverwriteSkillByLocalImport` (creator-only by design — MUL-2701/2800). The UI gate must match: creator-only, NOT `canEdit` (which also allows admins).
- i18n: add every new EN key to all four locales (`en`, `ja`, `ko`, `zh-Hans`) — `packages/views/locales/parity.test.ts` fails otherwise. Chinese voice rules (from `apps/docs/content/docs/developers/conventions.zh.mdx`): `skill` stays lowercase English (e.g. "更新 skill 失败"), use straight quotes `"..."` not `「」`, use three-dot `...` not `…`.
- Frontend: workspace-scoped query keys include `wsId`. Invalidate `workspaceKeys.skills(wsId)` (prefix-matches both the list key `["workspaces", wsId, "skills"]` and the detail key `["workspaces", wsId, "skills", skillId]`).
- Confirm-before-destructive: re-import replaces content/config/files and prunes files absent upstream; gate it behind a confirm dialog. This is NOT an optimistic update — await the server, then invalidate.
- Package boundaries: `packages/core` has no UI; origin helpers live in `packages/views/skills/lib/origin.ts`; the API method lives in `packages/core/api/client.ts`.

**Note on API-compat schema (deliberate deviation):** CLAUDE.md says new endpoints should add a zod schema + malformed-response test. The entire skills API surface (`getSkill`, `createSkill`, `importSkill`, `updateSkill`) currently returns `this.fetch<Skill>(...)` with **no** zod schema — there is no `SkillSchema` anywhere in `packages/core/api/`. `reimportSkill` mirrors `importSkill` exactly (same `Skill` response shape) for consistency; the UI already reads the response defensively (`readOrigin` optional-chains `config?.origin`, and cache invalidation re-fetches through the same untyped path). Introducing a one-off schema for only this method would be inconsistent with its siblings and is out of scope. Flag this to the reviewer; if they want schemas, that is a separate cross-cutting task for all skill methods.

---

## Task 1: Backend `POST /api/skills/{id}/reimport` endpoint

**Files:**
- Modify: `server/internal/handler/skill.go` (extract `fetchImportedSkill`; refactor `ImportSkill`; add origin helper + `ReimportSkill`)
- Modify: `server/cmd/server/router.go:1338-1348` (register the route inside the `/{id}` group)
- Test: `server/internal/handler/skill_reimport_test.go` (new)

**Interfaces:**
- Consumes (existing, verified): `func (h *Handler) loadSkillForUser(w, r, id string) (db.Skill, bool)`; `func requireUserID(w, r) (string, bool)`; `func canOverwriteSkillByLocalImport(userID string, skill db.Skill) bool`; `func detectImportSource(raw string) (importSource, string, error)`; `func fetchFromClawHub/fetchFromSkillsSh/fetchFromGitHub(ctx, *http.Client, string) (*importedSkill, error)`; `func (h *Handler) overwriteSkillWithFiles(ctx, skillOverwriteInput) (SkillWithFilesResponse, error)`; `func skillImportOverwriteFailure(err error) (int, string)`; `func importFetchErrorResponse(ctx, err) (int, string)`; `const importFetchTimeout`; `func validateFilePath(string) bool`; `func (h *Handler) resolveActor(r, userID, workspaceID string) (string, string)`; `func (h *Handler) publish(eventType, workspaceID, actorType, actorID string, payload any)`; `protocol.EventSkillUpdated`; `type CreateSkillFileRequest struct{ Path, Content string }`; `type skillOverwriteInput struct{ WorkspaceID, TargetSkillID pgtype.UUID; UserID, ExpectedName, Description, Content string; Config any; Files []CreateSkillFileRequest }`. `db.Skill` fields: `ID, WorkspaceID, CreatedBy pgtype.UUID`, `Name, Description, Content string`, `Config []byte`.
- Produces (for the frontend): `POST /api/skills/{id}/reimport` → `200` with a `SkillWithFilesResponse` (same shape `POST /api/skills/import` overwrite returns); `403` non-creator; `422` not updatable / stored source invalid; fetch failures via `importFetchErrorResponse` (413/502/503/504); `404`/overwrite failures via `skillImportOverwriteFailure`.

- [ ] **Step 1: Extract the fetch switch into a reusable helper (refactor, no behavior change)**

In `server/internal/handler/skill.go`, add this helper (place it just above `func (h *Handler) ImportSkill`, near line 2160):

```go
// fetchImportedSkill dispatches to the per-source fetcher for a detected import
// source. Shared by ImportSkill (hosted-URL body) and ReimportSkill (stored
// provenance) so both go through exactly one fetch path.
func fetchImportedSkill(ctx context.Context, client *http.Client, source importSource, normalized string) (*importedSkill, error) {
	switch source {
	case sourceClawHub:
		return fetchFromClawHub(ctx, client, normalized)
	case sourceSkillsSh:
		return fetchFromSkillsSh(ctx, client, normalized)
	case sourceGitHub:
		return fetchFromGitHub(ctx, client, normalized)
	}
	return nil, fmt.Errorf("unsupported import source")
}
```

Then replace the inline `switch source { ... }` block in `ImportSkill` (currently `skill.go:2214-2222`) with:

```go
	imported, err := fetchImportedSkill(ctx, httpClient, source, normalized)
```

(Delete the `var imported *importedSkill` line above it — the `:=` now declares it. Keep the `if err != nil { ... importFetchErrorResponse ... }` block that follows unchanged.)

- [ ] **Step 2: Run existing import tests to confirm the refactor is behavior-preserving**

Run: `(cd server && go test ./internal/handler/ -run 'ImportSkill|FetchFrom' -count=1)`
Expected: PASS (same as before the refactor). If the DB is not reachable the suite prints "Skipping tests" and exits 0 — that still means no compile break; verify with `(cd server && go build ./...)`.

- [ ] **Step 3: Add the origin-parsing helper**

In `server/internal/handler/skill.go`, add below `canOverwriteSkillByLocalImport` (near line 427):

```go
// updatableSkillOrigin is the subset of skill.config.origin that ReimportSkill
// needs: the source type and the re-fetchable URL. Only URL-based imports
// (github / skills_sh / clawhub) carry a source_url; runtime_local and manual
// skills do not, and are not updatable from a URL.
type updatableSkillOrigin struct {
	Type      string `json:"type"`
	SourceURL string `json:"source_url"`
}

// parseUpdatableSkillOrigin reads config.origin and reports whether the skill
// can be re-imported from a stored URL source. ok is false for manual skills,
// runtime_local skills, unparseable config, or a URL source missing its
// source_url.
func parseUpdatableSkillOrigin(config []byte) (updatableSkillOrigin, bool) {
	if len(config) == 0 {
		return updatableSkillOrigin{}, false
	}
	var wrapper struct {
		Origin updatableSkillOrigin `json:"origin"`
	}
	if err := json.Unmarshal(config, &wrapper); err != nil {
		return updatableSkillOrigin{}, false
	}
	o := wrapper.Origin
	switch o.Type {
	case "github", "skills_sh", "clawhub":
		if strings.TrimSpace(o.SourceURL) != "" {
			return o, true
		}
	}
	return o, false
}
```

- [ ] **Step 4: Write the failing test**

Create `server/internal/handler/skill_reimport_test.go`:

```go
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// insertReimportSkill seeds a skill owned by testUserID with a clawhub origin
// pointing at the given slug, plus an outdated body, and returns its id.
func insertReimportSkill(t *testing.T, slug, content string) string {
	t.Helper()
	name := "reimport-" + t.Name()
	config := `{"origin":{"type":"clawhub","source_url":"https://clawhub.ai/acme/` + slug + `","slug":"` + slug + `"}}`
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING id
	`, testWorkspaceID, name, "old description", content, config, testUserID).Scan(&id); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill_file WHERE skill_id = $1`, id)
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, id)
	})
	return id
}

// withMockClawHubReimport stands up a mock ClawHub API for the given slug and
// points clawHubAPIBase at it for the duration of the test.
func withMockClawHubReimport(t *testing.T, slug, displayName, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/" + slug:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"skill": map[string]any{
					"slug":        slug,
					"displayName": displayName,
					"summary":     "fresh summary",
					"tags":        map[string]string{"latest": "2.0.0"},
				},
			})
		case "/api/v1/skills/" + slug + "/versions/2.0.0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version": map[string]any{
					"version": "2.0.0",
					"files":   []map[string]any{{"path": "SKILL.md", "size": len(body)}},
				},
			})
		case "/api/v1/skills/" + slug + "/file":
			_, _ = w.Write([]byte(body))
		default:
			t.Fatalf("unexpected ClawHub path: %s", r.URL.String())
		}
	}))
	prev := clawHubAPIBase
	clawHubAPIBase = srv.URL + "/api/v1"
	t.Cleanup(func() {
		clawHubAPIBase = prev
		srv.Close()
	})
}

func callReimport(t *testing.T, userID, skillID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequestAsUser(userID, http.MethodPost, "/api/skills/"+skillID+"/reimport", nil)
	req = withURLParam(req, "id", skillID)
	testHandler.ReimportSkill(w, req)
	return w
}

func TestReimportSkill_OverwritesFromSource(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	slug := "review-helper"
	id := insertReimportSkill(t, slug, "# Old body\n")
	// The overwrite guard requires the target's name to equal the imported name,
	// so the mock's displayName must match the seeded skill name.
	var name string
	if err := testPool.QueryRow(context.Background(), `SELECT name FROM skill WHERE id = $1`, id).Scan(&name); err != nil {
		t.Fatalf("read name: %v", err)
	}
	withMockClawHubReimport(t, slug, name, "# Fresh body\n")

	w := callReimport(t, testUserID, id)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp SkillWithFilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != id {
		t.Fatalf("id changed: got %s want %s", resp.ID, id)
	}
	if resp.Content != "# Fresh body\n" {
		t.Fatalf("content = %q, want fresh body", resp.Content)
	}
	if resp.Name != name {
		t.Fatalf("name = %q, want preserved %q", resp.Name, name)
	}
}

func TestReimportSkill_NonCreatorForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	id := insertReimportSkill(t, "review-helper", "# Old\n")
	// A different (non-creator) user in the same workspace.
	w := callReimport(t, "00000000-0000-0000-0000-0000000000ff", id)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestReimportSkill_ManualSkillNotUpdatable(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	// insertHandlerTestSkill seeds config '{}' (manual, no origin), created by testUserID.
	id := insertHandlerTestSkill(t, "manual", "# Manual\n")
	w := callReimport(t, testUserID, id)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `(cd server && go test ./internal/handler/ -run 'TestReimportSkill' -count=1 -v)`
Expected: FAIL to compile — `testHandler.ReimportSkill` undefined.

- [ ] **Step 6: Implement `ReimportSkill`**

Add to `server/internal/handler/skill.go` (place it right after `ImportSkill`, near line 2230):

```go
// ReimportSkill re-runs a skill's original URL import in place. The source URL
// is read server-side from the skill's stored config.origin — the client never
// supplies it — so a caller cannot repoint the update at an arbitrary URL, and a
// locally renamed skill still updates (we target by id, not by name collision).
// Creator-only, mirroring canOverwriteSkillByLocalImport.
func (h *Handler) ReimportSkill(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	skill, ok := h.loadSkillForUser(w, r, id)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !canOverwriteSkillByLocalImport(userID, skill) {
		writeError(w, http.StatusForbidden, "only the skill creator can update it from its source")
		return
	}

	origin, ok := parseUpdatableSkillOrigin(skill.Config)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "this skill was not imported from an updatable source")
		return
	}

	source, normalized, err := detectImportSource(origin.SourceURL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "stored source is no longer valid: "+err.Error())
		return
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(r.Context(), importFetchTimeout)
	defer cancel()

	imported, err := fetchImportedSkill(ctx, httpClient, source, normalized)
	if err != nil {
		status, msg := importFetchErrorResponse(ctx, err)
		writeError(w, status, msg)
		return
	}

	// Map the fetched bundle exactly as finishSkillImport does.
	files := make([]CreateSkillFileRequest, 0, len(imported.files))
	for _, f := range imported.files {
		if !validateFilePath(f.path) {
			continue
		}
		files = append(files, CreateSkillFileRequest{Path: f.path, Content: f.content})
	}
	config := map[string]any{}
	if imported.origin != nil {
		config["origin"] = imported.origin
	}

	// Overwrite in place, targeting THIS skill by id. ExpectedName = the skill's
	// current name preserves identity and satisfies the overwrite name guard.
	resp, err := h.overwriteSkillWithFiles(r.Context(), skillOverwriteInput{
		WorkspaceID:   skill.WorkspaceID,
		TargetSkillID: skill.ID,
		UserID:        userID,
		ExpectedName:  skill.Name,
		Description:   imported.description,
		Content:       imported.content,
		Config:        config,
		Files:         files,
	})
	if err != nil {
		status, reason := skillImportOverwriteFailure(err)
		writeError(w, status, reason)
		return
	}

	workspaceID := uuidToString(skill.WorkspaceID)
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	h.publish(protocol.EventSkillUpdated, workspaceID, actorType, actorID, map[string]any{"skill": resp})
	writeJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 7: Register the route**

In `server/cmd/server/router.go`, inside the `r.Route("/{id}", func(r chi.Router) {` block (after `r.Delete("/", h.DeleteSkill)`, near line 1341), add:

```go
					r.Post("/reimport", h.ReimportSkill)
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `(cd server && go test ./internal/handler/ -run 'TestReimportSkill' -count=1 -v)`
Expected: PASS (or "Skipping tests" if no DB — in that case also run `(cd server && go build ./... && go vet ./internal/handler/)` and confirm clean).

- [ ] **Step 9: Commit**

```bash
git add server/internal/handler/skill.go server/internal/handler/skill_reimport_test.go server/cmd/server/router.go
git commit -m "feat(skills): add POST /api/skills/{id}/reimport to update a URL-sourced skill in place"
```

---

## Task 2: Frontend API method + pure origin gate

**Files:**
- Modify: `packages/core/api/client.ts:2076-2081` (add `reimportSkill` after `importSkill`)
- Modify: `packages/views/skills/lib/origin.ts` (add `isUpdatableOrigin`, `canReimportSkill`)
- Test: `packages/views/skills/lib/origin.test.ts` (new)

**Interfaces:**
- Consumes: `readOrigin(skill): OriginInfo` and `OriginInfo` (existing, `origin.ts`); `SkillSummary` type (has `created_by: string | null`, `config`).
- Produces: `api.reimportSkill(id: string): Promise<Skill>`; `isUpdatableOrigin(origin: OriginInfo): boolean`; `canReimportSkill(skill: SkillSummary, currentUserId: string | null): boolean`.

- [ ] **Step 1: Add the API method**

In `packages/core/api/client.ts`, directly after the `importSkill` method (ends at line 2081), add:

```ts
  // Re-imports a URL-sourced skill in place from its stored config.origin.
  // No body — the server reads the source URL from the skill's provenance.
  async reimportSkill(id: string): Promise<Skill> {
    return this.fetch(`/api/skills/${id}/reimport`, { method: "POST" });
  }
```

- [ ] **Step 2: Write the failing test for the pure gate**

Create `packages/views/skills/lib/origin.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import type { SkillSummary } from "@multica/core/types";
import { canReimportSkill, isUpdatableOrigin, readOrigin } from "./origin";

function skill(config: Record<string, unknown>, createdBy: string | null): SkillSummary {
  return {
    id: "s1",
    workspace_id: "ws1",
    name: "n",
    description: "",
    config,
    created_by: createdBy,
    created_at: "",
    updated_at: "",
  } as SkillSummary;
}

describe("isUpdatableOrigin", () => {
  it("is true for URL sources with a source_url", () => {
    expect(isUpdatableOrigin({ type: "github", source_url: "https://github.com/a/b" })).toBe(true);
    expect(isUpdatableOrigin({ type: "clawhub", source_url: "https://clawhub.ai/a/b" })).toBe(true);
    expect(isUpdatableOrigin({ type: "skills_sh", source_url: "https://skills.sh/a" })).toBe(true);
  });
  it("is false without a source_url, for runtime_local, and for manual", () => {
    expect(isUpdatableOrigin({ type: "github" })).toBe(false);
    expect(isUpdatableOrigin({ type: "runtime_local", source_path: "/x" })).toBe(false);
    expect(isUpdatableOrigin({ type: "manual" })).toBe(false);
  });
});

describe("canReimportSkill", () => {
  const origin = { origin: { type: "github", source_url: "https://github.com/a/b" } };
  it("is true only for the creator of a URL-sourced skill", () => {
    expect(canReimportSkill(skill(origin, "u1"), "u1")).toBe(true);
  });
  it("is false for a non-creator (admins must edit in-app, not re-import)", () => {
    expect(canReimportSkill(skill(origin, "u1"), "u2")).toBe(false);
  });
  it("is false for a manual skill even for its creator", () => {
    expect(canReimportSkill(skill({}, "u1"), "u1")).toBe(false);
  });
  it("is false when there is no current user", () => {
    expect(canReimportSkill(skill(origin, "u1"), null)).toBe(false);
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `pnpm --filter @multica/views test -- origin.test.ts`
Expected: FAIL — `canReimportSkill` / `isUpdatableOrigin` not exported.

- [ ] **Step 4: Implement the helpers**

In `packages/views/skills/lib/origin.ts`, add after `readOrigin` (after line 26):

```ts
const UPDATABLE_ORIGIN_TYPES = new Set<OriginInfo["type"]>([
  "github",
  "skills_sh",
  "clawhub",
]);

/**
 * Whether a skill's origin can be re-imported from a stored URL. True only for
 * the URL-based sources (github / skills_sh / clawhub) that carry a re-fetchable
 * `source_url`; runtime_local and manual skills return false.
 */
export function isUpdatableOrigin(origin: OriginInfo): boolean {
  return (
    UPDATABLE_ORIGIN_TYPES.has(origin.type) &&
    typeof origin.source_url === "string" &&
    origin.source_url.length > 0
  );
}

/**
 * Whether the current user may re-import (update) the skill. Creator-only,
 * mirroring the server rule (`canOverwriteSkillByLocalImport`): admins/owners
 * who did not create the skill must edit it in-app rather than re-import.
 */
export function canReimportSkill(
  skill: SkillSummary,
  currentUserId: string | null,
): boolean {
  return (
    isUpdatableOrigin(readOrigin(skill)) &&
    !!currentUserId &&
    skill.created_by === currentUserId
  );
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `pnpm --filter @multica/views test -- origin.test.ts`
Expected: PASS.

- [ ] **Step 6: Typecheck**

Run: `pnpm --filter @multica/core typecheck && pnpm --filter @multica/views typecheck`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add packages/core/api/client.ts packages/views/skills/lib/origin.ts packages/views/skills/lib/origin.test.ts
git commit -m "feat(skills): reimportSkill API method + creator/URL-source gate helpers"
```

---

## Task 3: "Update" kebab item, confirm dialog, and i18n

**Files:**
- Modify: `packages/views/skills/components/skill-list-actions.tsx` (add `UpdateSkillDialog` + wire into `SkillRowActions`)
- Modify: `packages/views/locales/en/skills.json`, `ja/skills.json`, `ko/skills.json`, `zh-Hans/skills.json` (add keys under `actions`)
- Test: `packages/views/skills/components/update-skill-dialog.test.tsx` (new)

**Interfaces:**
- Consumes: `canReimportSkill(skill, currentUserId)`, `readOrigin(skill)` from `../lib/origin`; `api.reimportSkill(id)`; `workspaceKeys.skills(wsId)`; `SkillActionsContext` (`{ wsId, agents, currentUserId, isAdmin }`); `SkillRow` (`{ skill: SkillSummary, ... }`).
- Produces: an "Update" `DropdownMenuItem` in the row kebab and an exported `UpdateSkillDialog` component.

- [ ] **Step 1: Add the i18n keys to EN**

In `packages/views/locales/en/skills.json`, inside the `"actions"` object (alongside `"delete"`), add:

```json
    "update": "Update",
    "update_dialog_title": "Update from source?",
    "update_dialog_desc": "This replaces \"{{name}}\" with the latest version from its source. Any local changes to this skill will be lost.",
    "update_dialog_source": "Source: {{url}}",
    "update_confirm": "Update",
    "updating": "Updating...",
    "updated_toast": "Updated {{name}} from source",
    "update_failed_toast": "Failed to update skill"
```

- [ ] **Step 2: Add the same keys to ja / ko / zh-Hans**

`packages/views/locales/ja/skills.json` → `"actions"`:

```json
    "update": "更新",
    "update_dialog_title": "ソースから更新しますか？",
    "update_dialog_desc": "「{{name}}」をソースの最新バージョンに置き換えます。このスキルへのローカルの変更は失われます。",
    "update_dialog_source": "ソース: {{url}}",
    "update_confirm": "更新",
    "updating": "更新中...",
    "updated_toast": "{{name}} をソースから更新しました",
    "update_failed_toast": "スキルの更新に失敗しました"
```

`packages/views/locales/ko/skills.json` → `"actions"`:

```json
    "update": "업데이트",
    "update_dialog_title": "소스에서 업데이트할까요?",
    "update_dialog_desc": "\"{{name}}\"을(를) 소스의 최신 버전으로 교체합니다. 이 스킬의 로컬 변경 사항은 사라집니다.",
    "update_dialog_source": "소스: {{url}}",
    "update_confirm": "업데이트",
    "updating": "업데이트 중...",
    "updated_toast": "{{name}}을(를) 소스에서 업데이트했습니다",
    "update_failed_toast": "스킬 업데이트에 실패했습니다"
```

`packages/views/locales/zh-Hans/skills.json` → `"actions"` (note: `skill` stays lowercase English, straight quotes, three-dot `...`):

```json
    "update": "更新",
    "update_dialog_title": "从来源更新这个 skill？",
    "update_dialog_desc": "这会用来源的最新版本替换 \"{{name}}\"。对该 skill 的本地修改将会丢失。",
    "update_dialog_source": "来源: {{url}}",
    "update_confirm": "更新",
    "updating": "更新中...",
    "updated_toast": "已从来源更新 {{name}}",
    "update_failed_toast": "更新 skill 失败"
```

- [ ] **Step 3: Run the locale parity test to confirm no drift**

Run: `pnpm --filter @multica/views test -- parity.test.ts`
Expected: PASS (every EN key has a counterpart in ja/ko/zh-Hans).

- [ ] **Step 4: Write the failing dialog test**

Create `packages/views/skills/components/update-skill-dialog.test.tsx`:

```tsx
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSkills from "../../locales/en/skills.json";
import type { SkillRow } from "./skills-page";

const mockReimportSkill = vi.hoisted(() => vi.fn());
vi.mock("@multica/core/api", () => ({
  api: { reimportSkill: (...a: unknown[]) => mockReimportSkill(...a) },
}));

import { UpdateSkillDialog } from "./skill-list-actions";

const TEST_RESOURCES = { en: { common: enCommon, skills: enSkills } };

function wrap(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        {children}
      </I18nProvider>
    </QueryClientProvider>
  );
}

function row(): SkillRow {
  return {
    skill: {
      id: "s1",
      workspace_id: "ws1",
      name: "My Skill",
      description: "",
      config: { origin: { type: "github", source_url: "https://github.com/a/b" } },
      created_by: "u1",
      created_at: "",
      updated_at: "",
    },
    agents: [],
    creator: null,
    runtime: null,
    originType: "github",
    canEdit: true,
  } as SkillRow;
}

const ctx = { wsId: "ws1", agents: [], currentUserId: "u1", isAdmin: false };

describe("UpdateSkillDialog", () => {
  beforeEach(() => mockReimportSkill.mockReset());

  it("calls reimportSkill and invalidates skills on confirm", async () => {
    mockReimportSkill.mockResolvedValue({});
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    render(<UpdateSkillDialog row={row()} ctx={ctx} open onOpenChange={() => {}} />, {
      wrapper: wrap(qc),
    });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(mockReimportSkill).toHaveBeenCalledWith("s1"));
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: ["workspaces", "ws1", "skills"],
    });
  });

  it("does not throw on failure (error toast path)", async () => {
    mockReimportSkill.mockRejectedValue(new Error("boom"));
    const qc = new QueryClient();
    render(<UpdateSkillDialog row={row()} ctx={ctx} open onOpenChange={() => {}} />, {
      wrapper: wrap(qc),
    });
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() => expect(mockReimportSkill).toHaveBeenCalled());
  });
});
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `pnpm --filter @multica/views test -- update-skill-dialog.test.tsx`
Expected: FAIL — `UpdateSkillDialog` is not exported from `skill-list-actions`.

- [ ] **Step 6: Implement `UpdateSkillDialog`**

In `packages/views/skills/components/skill-list-actions.tsx`:

Add `RefreshCw` to the `lucide-react` import (the block at lines 4-13):

```tsx
import {
  Check,
  ChevronRight,
  Loader2,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  X,
} from "lucide-react";
```

Add `readOrigin` and `canReimportSkill` to the origin import (add near the other imports, e.g. after line 18):

```tsx
import { canReimportSkill, readOrigin } from "../lib/origin";
```

Add the dialog component immediately after `DeleteSkillsDialog` (after line 505):

```tsx
// ---------------------------------------------------------------------------
// Update-from-source confirmation (single row; creator + URL origin only)
// ---------------------------------------------------------------------------

export function UpdateSkillDialog({
  row,
  ctx,
  open,
  onOpenChange,
}: {
  row: SkillRow;
  ctx: SkillActionsContext;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useT("skills");
  const qc = useQueryClient();
  const [updating, setUpdating] = useState(false);
  const sourceUrl = readOrigin(row.skill).source_url ?? "";

  const handleConfirm = async () => {
    setUpdating(true);
    try {
      await api.reimportSkill(row.skill.id);
      // Prefix key invalidates both the skills list and the skill detail; the
      // agent list carries each skill's name/description inline, so refresh it too.
      qc.invalidateQueries({ queryKey: workspaceKeys.skills(ctx.wsId) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(ctx.wsId) });
      toast.success(t(($) => $.actions.updated_toast, { name: row.skill.name }));
      onOpenChange(false);
    } catch (e) {
      toast.error(
        e instanceof Error && e.message
          ? e.message
          : t(($) => $.actions.update_failed_toast),
      );
    } finally {
      setUpdating(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!updating) onOpenChange(v);
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t(($) => $.actions.update_dialog_title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.actions.update_dialog_desc, { name: row.skill.name })}
          </DialogDescription>
        </DialogHeader>
        {sourceUrl && (
          <div className="truncate rounded-md bg-muted px-3 py-2 text-caption text-muted-foreground">
            {t(($) => $.actions.update_dialog_source, { url: sourceUrl })}
          </div>
        )}
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={updating}
          >
            {t(($) => $.actions.cancel)}
          </Button>
          <Button type="button" onClick={handleConfirm} disabled={updating}>
            {updating ? (
              <>
                <Loader2 className="h-3 w-3 animate-spin" />
                {t(($) => $.actions.updating)}
              </>
            ) : (
              <>
                <RefreshCw className="h-3 w-3" />
                {t(($) => $.actions.update_confirm)}
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 7: Wire the "Update" item into `SkillRowActions`**

In `SkillRowActions` (starts line 515), add an `updateOpen` state next to the others (line 523-524):

```tsx
  const [addOpen, setAddOpen] = useState(false);
  const [updateOpen, setUpdateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const canReimport = canReimportSkill(row.skill, ctx.currentUserId);
```

In the `DropdownMenuContent` (lines 543-560), add the Update item between the "Add to agent" item and the `row.canEdit` delete block:

```tsx
        <DropdownMenuContent align="end" className="w-52">
          <DropdownMenuItem onClick={() => setAddOpen(true)}>
            <Plus className="size-3.5" />
            {t(($) => $.actions.add_to_agent)}
          </DropdownMenuItem>
          {canReimport && (
            <DropdownMenuItem onClick={() => setUpdateOpen(true)}>
              <RefreshCw className="size-3.5" />
              {t(($) => $.actions.update)}
            </DropdownMenuItem>
          )}
          {row.canEdit && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="destructive"
                onClick={() => setDeleteOpen(true)}
              >
                <Trash2 className="size-3.5" />
                {t(($) => $.actions.delete)}
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
```

And render the dialog next to the others (after `AddToAgentDialog`, near line 567):

```tsx
      {canReimport && (
        <UpdateSkillDialog
          row={row}
          ctx={ctx}
          open={updateOpen}
          onOpenChange={setUpdateOpen}
        />
      )}
```

- [ ] **Step 8: Run the dialog test to verify it passes**

Run: `pnpm --filter @multica/views test -- update-skill-dialog.test.tsx`
Expected: PASS.

- [ ] **Step 9: Typecheck + lint the touched packages**

Run: `pnpm --filter @multica/views typecheck && pnpm --filter @multica/views lint`
Expected: no errors.

- [ ] **Step 10: Commit**

```bash
git add packages/views/skills/components/skill-list-actions.tsx packages/views/skills/components/update-skill-dialog.test.tsx packages/views/locales/en/skills.json packages/views/locales/ja/skills.json packages/views/locales/ko/skills.json packages/views/locales/zh-Hans/skills.json
git commit -m "feat(skills): Update-from-source kebab action with confirm dialog and i18n"
```

---

## Final verification (after all tasks)

- [ ] Backend: `(cd server && go build ./... && go vet ./internal/handler/ && go test ./internal/handler/ -run 'Skill' -count=1)`
- [ ] Frontend: `pnpm --filter @multica/core typecheck && pnpm --filter @multica/views typecheck && pnpm --filter @multica/views test`
- [ ] Manual smoke (optional, `make dev`): import a skill from a github/clawhub URL, open its row kebab → "Update" appears; for a manually-created skill it does not; confirm the dialog re-imports and toasts.

## PR

- [ ] Push branch `update-skill-from-source` to `fork` and open a PR into `fork/main` (`zdavison/multica`), NOT upstream. Body should note the follow-up: a separate upstream PR (`multica-ai/multica`) after this one is approved, and the out-of-scope items (runtime_local re-import, detail-page Update button, skills-API zod schema).
