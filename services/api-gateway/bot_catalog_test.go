//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// listCatalog returns the catalog (public library) portion of GET /bots for userID.
func listCatalog(t *testing.T, s *Server, userID string) []map[string]interface{} {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bots", nil)
	req = withUserID(req, userID)
	s.ListBots(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListBots: got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Catalog []map[string]interface{} `json:"catalog"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)
	return resp.Catalog
}

func inCatalog(catalog []map[string]interface{}, id string) map[string]interface{} {
	for _, b := range catalog {
		if b["id"] == id {
			return b
		}
	}
	return nil
}

// TestPublishBot_DetachedCopySurvivesDelete verifies that publishing creates an
// independent library copy owned by the Catalog account, that the catalog shows
// the copy (not the original), that the real author is preserved for attribution,
// and — the core fix — that deleting the creator's own bot leaves the library copy.
func TestPublishBot_DetachedCopySurvivesDelete(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createAdminTestUser(t, s, "catalog_detach@example.com", "pass1234", false)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", userID) })

	// Pre-cleanup: remove catalog copies left by interrupted previous runs.
	s.pool.Exec(ctx, `DELETE FROM bots WHERE name='Detach Bot' AND owner_id=$1`, catalogOwnerID)
	botID := createTestBot(t, s, userID, "Detach Bot", false)
	// Non-official bots require approval before publishing.
	s.pool.Exec(ctx, `UPDATE bots SET approval_status='approved' WHERE id=$1`, botID)
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DELETE FROM bots WHERE published_from_id=$1 OR id=$1", botID)
	})

	// Publish.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/publish", nil)
	req = withUserID(req, userID)
	req = withChiParams(req, map[string]string{"id": botID})
	s.PublishBot(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PublishBot: got %d: %s", rec.Code, rec.Body.String())
	}

	// A detached copy owned by the Catalog account must now exist.
	var copyID, copyOwner string
	var authorID *string
	if err := s.pool.QueryRow(ctx,
		`SELECT id, owner_id, original_author_id FROM bots WHERE published_from_id=$1`, botID,
	).Scan(&copyID, &copyOwner, &authorID); err != nil {
		t.Fatalf("no detached copy created: %v", err)
	}
	if copyOwner != catalogOwnerID {
		t.Errorf("copy owner = %s, want Catalog account %s", copyOwner, catalogOwnerID)
	}
	if authorID == nil || *authorID != userID {
		t.Errorf("copy original_author_id = %v, want %s", authorID, userID)
	}

	// Catalog shows the copy, hides the original (dedup).
	catalog := listCatalog(t, s, userID)
	if inCatalog(catalog, copyID) == nil {
		t.Error("catalog should contain the detached copy")
	}
	if inCatalog(catalog, botID) != nil {
		t.Error("catalog should hide the original that has a copy")
	}
	// Attribution: the copy shows the real author, not the Catalog account.
	if c := inCatalog(catalog, copyID); c != nil {
		if c["ownerName"] == "NovaBot Catalog" {
			t.Error("copy attribution should be the real author, not the Catalog account")
		}
	}

	// Core fix: delete the creator's own bot — the library copy must survive.
	delRec := httptest.NewRecorder()
	delReq := httptest.NewRequest(http.MethodDelete, "/bots/"+botID, nil)
	delReq = withUserID(delReq, userID)
	delReq = withChiParams(delReq, map[string]string{"id": botID})
	s.DeleteBot(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DeleteBot: got %d: %s", delRec.Code, delRec.Body.String())
	}

	var stillThere bool
	s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bots WHERE id=$1)`, copyID).Scan(&stillThere)
	if !stillThere {
		t.Fatal("library copy must survive deletion of the creator's bot")
	}
	if inCatalog(listCatalog(t, s, userID), copyID) == nil {
		t.Error("library copy must still appear in the catalog after original deleted")
	}
}

// TestPublishBot_RepublishUpdatesSameCopy verifies re-publishing refreshes the
// existing copy instead of creating duplicates.
func TestPublishBot_RepublishUpdatesSameCopy(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	userID := createAdminTestUser(t, s, "catalog_republish@example.com", "pass1234", false)
	t.Cleanup(func() { s.pool.Exec(ctx, "DELETE FROM users WHERE id=$1", userID) })

	// Pre-cleanup: remove catalog copies left by interrupted previous runs.
	s.pool.Exec(ctx, `DELETE FROM bots WHERE name='Republish Bot' AND owner_id=$1`, catalogOwnerID)
	botID := createTestBot(t, s, userID, "Republish Bot", false)
	s.pool.Exec(ctx, `UPDATE bots SET approval_status='approved' WHERE id=$1`, botID)
	t.Cleanup(func() {
		s.pool.Exec(ctx, "DELETE FROM bots WHERE published_from_id=$1 OR id=$1", botID)
	})

	publish := func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/publish", nil)
		req = withUserID(req, userID)
		req = withChiParams(req, map[string]string{"id": botID})
		s.PublishBot(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("PublishBot: got %d: %s", rec.Code, rec.Body.String())
		}
	}
	publish()
	publish()

	var n int
	s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM bots WHERE published_from_id=$1`, botID).Scan(&n)
	if n != 1 {
		t.Errorf("re-publish should keep a single copy, got %d", n)
	}
}
