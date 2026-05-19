//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// createTestBot inserts a bot owned by ownerID and returns its id.
func createTestBot(t *testing.T, s *Server, ownerID, name string, isPublic bool) string {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"name": name, "isPublic": isPublic})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, ownerID)
	s.CreateBot(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("createTestBot: got %d: %s", rec.Code, rec.Body.String())
	}
	var bot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&bot)
	return bot["id"].(string)
}

func TestListBots(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_list@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	botID := createTestBot(t, s, userID, "Public Bot", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bots", nil)
	req = withUserID(req, userID)
	s.ListBots(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["catalog"] == nil {
		t.Error("missing catalog key")
	}
	if resp["mine"] == nil {
		t.Error("missing mine key")
	}
	mine := resp["mine"].([]interface{})
	if len(mine) == 0 {
		t.Error("expected bot in mine")
	}
	catalog := resp["catalog"].([]interface{})
	if len(catalog) == 0 {
		t.Error("expected public bot in catalog")
	}
}

func TestCreateBot(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_create@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	body := `{"name":"My Bot","description":"desc","isPublic":false}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateBot(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var bot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&bot)
	if bot["name"] != "My Bot" {
		t.Errorf("unexpected name: %v", bot["name"])
	}
	if bot["isOwn"] != true {
		t.Error("expected isOwn=true")
	}
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", bot["id"])
}

func TestPatchBot_OwnerCanUpdate(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_patch@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	botID := createTestBot(t, s, userID, "Old Name", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(`{"name":"New Name"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var bot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&bot)
	if bot["name"] != "New Name" {
		t.Errorf("unexpected name: %v", bot["name"])
	}
}

func TestPatchBot_LinkedSubscriptionBlocked(t *testing.T) {
	s := newTestServer(t)
	authorID := createAdminTestUser(t, s, "bots_author_p@example.com", "pass1234", false)
	subID := createAdminTestUser(t, s, "bots_sub_p@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", authorID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", subID)

	srcID := createTestBot(t, s, authorID, "Public Bot", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", srcID)

	// Deploy
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+srcID+"/deploy", nil)
	req = withUserID(req, subID)
	req = addChiParams(req, map[string]string{"id": srcID})
	s.DeployBot(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("deploy failed: %d %s", rec.Code, rec.Body.String())
	}
	var depBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&depBot)
	depID := depBot["id"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", depID)

	// Patch linked subscription → must get 403
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPatch, "/bots/"+depID, bytes.NewBufferString(`{"name":"Hacked"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withUserID(req2, subID)
	req2 = addChiParams(req2, map[string]string{"id": depID})
	s.PatchBot(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec2.Code)
	}
}

func TestDeployBot(t *testing.T) {
	s := newTestServer(t)
	authorID := createAdminTestUser(t, s, "bots_author_d@example.com", "pass1234", false)
	deployerID := createAdminTestUser(t, s, "bots_deployer@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", authorID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", deployerID)

	srcID := createTestBot(t, s, authorID, "Deploy Source", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", srcID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+srcID+"/deploy", nil)
	req = withUserID(req, deployerID)
	req = addChiParams(req, map[string]string{"id": srcID})
	s.DeployBot(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var bot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&bot)
	if bot["sourceBotId"] != srcID {
		t.Errorf("sourceBotId mismatch: %v", bot["sourceBotId"])
	}
	if bot["isFork"] != false {
		t.Error("expected isFork=false")
	}
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", bot["id"])

	// deploy_count incremented
	var count int
	s.pool.QueryRow(context.Background(), "SELECT deploy_count FROM bots WHERE id=$1", srcID).Scan(&count)
	if count != 1 {
		t.Errorf("expected deploy_count=1, got %d", count)
	}
}

func TestForkBot(t *testing.T) {
	s := newTestServer(t)
	authorID := createAdminTestUser(t, s, "bots_fauthor@example.com", "pass1234", false)
	forkerID := createAdminTestUser(t, s, "bots_forker@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", authorID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", forkerID)

	srcID := createTestBot(t, s, authorID, "Fork Source", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", srcID)

	// Deploy first
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+srcID+"/deploy", nil)
	req = withUserID(req, forkerID)
	req = addChiParams(req, map[string]string{"id": srcID})
	s.DeployBot(rec, req)
	var depBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&depBot)
	depID := depBot["id"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", depID)

	// Fork
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+depID+"/fork", nil)
	req2 = withUserID(req2, forkerID)
	req2 = addChiParams(req2, map[string]string{"id": depID})
	s.ForkBot(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec2.Code, rec2.Body.String())
	}
	var bot map[string]interface{}
	json.NewDecoder(rec2.Body).Decode(&bot)
	if bot["isFork"] != true {
		t.Error("expected isFork=true after fork")
	}
}

func TestStartStopBot(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_startstop@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	botID := createTestBot(t, s, userID, "Start Stop", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// Start
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/start", nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.StartBot(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("start: got %d", rec.Code)
	}
	var status string
	s.pool.QueryRow(context.Background(), "SELECT status FROM bots WHERE id=$1", botID).Scan(&status)
	if status != "active" {
		t.Errorf("expected active, got %s", status)
	}

	// Stop
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/stop", nil)
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": botID})
	s.StopBot(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("stop: got %d", rec2.Code)
	}
	s.pool.QueryRow(context.Background(), "SELECT status FROM bots WHERE id=$1", botID).Scan(&status)
	if status != "stopped" {
		t.Errorf("expected stopped, got %s", status)
	}
}

func TestDeleteBot(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_delete@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	botID := createTestBot(t, s, userID, "To Delete", false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/bots/"+botID, nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.DeleteBot(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("got %d: %s", rec.Code, rec.Body.String())
	}
	var count int
	s.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM bots WHERE id=$1", botID).Scan(&count)
	if count != 0 {
		t.Error("bot should have been deleted")
	}
}
