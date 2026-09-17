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
	subAcctID := createTestAccountLabeled(t, s, subID, "sub-acct")

	// Deploy
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+srcID+"/deploy", bytes.NewBufferString(`{"accountId":"`+subAcctID+`"}`))
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
	deployerAcctID := createTestAccountLabeled(t, s, deployerID, "deployer-acct")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+srcID+"/deploy", bytes.NewBufferString(`{"accountId":"`+deployerAcctID+`"}`))
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
	if bot["accountId"] != deployerAcctID {
		t.Errorf("accountId mismatch: got %v, want %v", bot["accountId"], deployerAcctID)
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
	forkerAcctID := createTestAccountLabeled(t, s, forkerID, "forker-acct")

	// Deploy first
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+srcID+"/deploy", bytes.NewBufferString(`{"accountId":"`+forkerAcctID+`"}`))
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

// TestListBots_FiltersMineByAccountID pins the fix for bots leaking across a user's own
// accounts: /bots?accountId=X must only return the caller's bots bound to that account in
// "mine", never bots bound to their other accounts — and omitting accountId must still
// return everything (used by callers like TradeHistoryPage that intentionally show bots
// across all of a user's accounts).
func TestListBots_FiltersMineByAccountID(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_acctfilter@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	acctA := createTestAccountLabeled(t, s, userID, "acct-a")
	acctB := createTestAccountLabeled(t, s, userID, "acct-b")

	botA := createTestBot(t, s, userID, "Bot On A", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botA)
	botB := createTestBot(t, s, userID, "Bot On B", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botB)

	if _, err := s.pool.Exec(context.Background(), "UPDATE bots SET account_id=$1 WHERE id=$2", acctA, botA); err != nil {
		t.Fatalf("bind botA: %v", err)
	}
	if _, err := s.pool.Exec(context.Background(), "UPDATE bots SET account_id=$1 WHERE id=$2", acctB, botB); err != nil {
		t.Fatalf("bind botB: %v", err)
	}

	listMine := func(accountID string) []string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/bots?accountId="+accountID, nil)
		req = withUserID(req, userID)
		rec := httptest.NewRecorder()
		s.ListBots(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("ListBots: got %d: %s", rec.Code, rec.Body.String())
		}
		var resp listBotsResp
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		names := make([]string, len(resp.Mine))
		for i, b := range resp.Mine {
			names[i] = b.Name
		}
		return names
	}

	namesOnA := listMine(acctA)
	if !containsStr(namesOnA, "Bot On A") || containsStr(namesOnA, "Bot On B") {
		t.Errorf("accountId=A: got %v, want only Bot On A", namesOnA)
	}

	namesOnB := listMine(acctB)
	if !containsStr(namesOnB, "Bot On B") || containsStr(namesOnB, "Bot On A") {
		t.Errorf("accountId=B: got %v, want only Bot On B", namesOnB)
	}

	namesUnfiltered := listMine("")
	if !containsStr(namesUnfiltered, "Bot On A") || !containsStr(namesUnfiltered, "Bot On B") {
		t.Errorf("accountId omitted: got %v, want both bots", namesUnfiltered)
	}
}

func containsStr(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// TestCreateMultiBot_CreatesPairedSignalAndHedgeLegs pins the core Мультибот invariant:
// POST /bots/multi must create two ordinary bots rows (one signal, one hedge), link them
// bidirectionally via paired_bot_id, and lock the hedge leg's hedge_bot_whitelist to the
// signal leg's own id — this is what makes the hedge leg only ever react to its own twin's
// strategies instead of needing the coin/bot filter pickers a standalone hedge bot has.
func TestCreateMultiBot_CreatesPairedSignalAndHedgeLegs(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_multi@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	acctID := createTestAccountLabeled(t, s, userID, "multi-acct")

	body, _ := json.Marshal(map[string]interface{}{
		"name":                "My Multi",
		"description":         "desc",
		"accountId":           acctID,
		"strategyConfig":      map[string]interface{}{"symbol": "BTCUSDT", "direction": "long"},
		"hedgeStrategyConfig": map[string]interface{}{"direction": "both", "hedge_act_type": 1},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateMultiBot(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	signalID := signalBot["id"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR paired_bot_id=$1", signalID)

	pairedID, _ := signalBot["pairedBotId"].(string)
	if pairedID == "" {
		t.Fatalf("expected pairedBotId set on signal leg, got %v", signalBot["pairedBotId"])
	}

	var hedgeCfgRaw []byte
	var hedgeOwner, hedgeAccountID, hedgePairedID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT strategy_config, owner_id, account_id::text, paired_bot_id::text FROM bots WHERE id=$1`, pairedID,
	).Scan(&hedgeCfgRaw, &hedgeOwner, &hedgeAccountID, &hedgePairedID); err != nil {
		t.Fatalf("fetch hedge leg: %v", err)
	}
	if hedgeOwner != userID {
		t.Errorf("hedge leg owner = %s, want %s", hedgeOwner, userID)
	}
	if hedgeAccountID != acctID {
		t.Errorf("hedge leg account_id = %s, want %s", hedgeAccountID, acctID)
	}
	if hedgePairedID != signalID {
		t.Errorf("hedge leg paired_bot_id = %s, want %s", hedgePairedID, signalID)
	}
	var hedgeCfg struct {
		BotKind           string   `json:"bot_kind"`
		HedgeBotWhitelist []string `json:"hedge_bot_whitelist"`
	}
	if err := json.Unmarshal(hedgeCfgRaw, &hedgeCfg); err != nil {
		t.Fatalf("unmarshal hedge cfg: %v", err)
	}
	if hedgeCfg.BotKind != "hedge" {
		t.Errorf("hedge leg bot_kind = %q, want hedge", hedgeCfg.BotKind)
	}
	if len(hedgeCfg.HedgeBotWhitelist) != 1 || hedgeCfg.HedgeBotWhitelist[0] != signalID {
		t.Errorf("hedge leg hedge_bot_whitelist = %v, want [%s]", hedgeCfg.HedgeBotWhitelist, signalID)
	}

	var signalCfgRaw []byte
	s.pool.QueryRow(context.Background(), `SELECT strategy_config FROM bots WHERE id=$1`, signalID).Scan(&signalCfgRaw)
	var signalCfg struct {
		BotKind string `json:"bot_kind"`
	}
	json.Unmarshal(signalCfgRaw, &signalCfg)
	if signalCfg.BotKind != "signal" {
		t.Errorf("signal leg bot_kind = %q, want signal", signalCfg.BotKind)
	}
}

// TestCreateMultiBot_RejectsForeignAccount: accountId must belong to the caller, same
// ownership check as every other bot/account-scoped endpoint in this file.
func TestCreateMultiBot_RejectsForeignAccount(t *testing.T) {
	s := newTestServer(t)
	ownerA := createAdminTestUser(t, s, "bots_multi_a@example.com", "pass1234", false)
	ownerB := createAdminTestUser(t, s, "bots_multi_b@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", ownerA)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", ownerB)
	acctA := createTestAccountLabeled(t, s, ownerA, "acct-a-multi")

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Sneaky", "accountId": acctA,
		"strategyConfig": map[string]interface{}{}, "hedgeStrategyConfig": map[string]interface{}{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, ownerB) // B tries to create a multibot on A's account
	s.CreateMultiBot(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestStopBot_CascadesToPairedLeg / TestDeleteBot_CascadesToPairedLeg pin that Start/Stop/
// Delete issued against either leg's id act on the whole pair — a lone hedge leg left
// running (or existing at all) after its signal twin stops/deletes would be pointless: its
// hedge_bot_whitelist points at exactly that one bot's strategies.
func TestStopBot_CascadesToPairedLeg(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_multi_stop@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	acctID := createTestAccountLabeled(t, s, userID, "multi-stop-acct")

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Stop Pair", "accountId": acctID,
		"strategyConfig": map[string]interface{}{}, "hedgeStrategyConfig": map[string]interface{}{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateMultiBot(rec, req)
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	signalID := signalBot["id"].(string)
	hedgeID := signalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", signalID, hedgeID)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+signalID+"/start", nil)
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": signalID})
	s.StartBot(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("start: got %d: %s", rec2.Code, rec2.Body.String())
	}

	var signalStatus, hedgeStatus string
	s.pool.QueryRow(context.Background(), "SELECT status FROM bots WHERE id=$1", signalID).Scan(&signalStatus)
	s.pool.QueryRow(context.Background(), "SELECT status FROM bots WHERE id=$1", hedgeID).Scan(&hedgeStatus)
	if signalStatus != "active" || hedgeStatus != "active" {
		t.Fatalf("after start: signal=%s hedge=%s, want both active", signalStatus, hedgeStatus)
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/bots/"+signalID+"/stop", nil)
	req3 = withUserID(req3, userID)
	req3 = addChiParams(req3, map[string]string{"id": signalID})
	s.StopBot(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("stop: got %d: %s", rec3.Code, rec3.Body.String())
	}

	s.pool.QueryRow(context.Background(), "SELECT status FROM bots WHERE id=$1", signalID).Scan(&signalStatus)
	s.pool.QueryRow(context.Background(), "SELECT status FROM bots WHERE id=$1", hedgeID).Scan(&hedgeStatus)
	if signalStatus != "stopped" || hedgeStatus != "stopped" {
		t.Errorf("after stop: signal=%s hedge=%s, want both stopped", signalStatus, hedgeStatus)
	}
}

func TestDeleteBot_CascadesToPairedLeg(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_multi_del@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	acctID := createTestAccountLabeled(t, s, userID, "multi-del-acct")

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Delete Pair", "accountId": acctID,
		"strategyConfig": map[string]interface{}{}, "hedgeStrategyConfig": map[string]interface{}{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateMultiBot(rec, req)
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	signalID := signalBot["id"].(string)
	hedgeID := signalBot["pairedBotId"].(string)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodDelete, "/bots/"+signalID, nil)
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": signalID})
	s.DeleteBot(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d: %s", rec2.Code, rec2.Body.String())
	}

	var count int
	s.pool.QueryRow(context.Background(), "SELECT COUNT(*) FROM bots WHERE id IN ($1,$2)", signalID, hedgeID).Scan(&count)
	if count != 0 {
		t.Errorf("expected both legs deleted, %d rows remain", count)
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

func TestBotApprovalFlow(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "approval_user@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	adminID := createAdminTestUser(t, s, "approval_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	// Pre-cleanup: remove catalog copies left by interrupted previous runs.
	s.pool.Exec(context.Background(), `DELETE FROM bots WHERE name='Approval Bot' AND owner_id=$1`, catalogOwnerID)
	botID := createTestBot(t, s, userID, "Approval Bot", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE published_from_id=$1 OR id=$1", botID)

	// ── Case 1: insufficient time → 422 ──────────────────────────────────────
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/request-approval", nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.RequestBotApproval(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("case 1: expected 422, got %d: %s", rec.Code, rec.Body.String())
	}

	// ── Case 2: sufficient time → 204, status = pending ──────────────────────
	s.pool.Exec(context.Background(),
		`UPDATE bots SET active_seconds_acc = 16*86400 WHERE id = $1`, botID)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/request-approval", nil)
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": botID})
	s.RequestBotApproval(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("case 2: expected 204, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var gotStatus string
	s.pool.QueryRow(context.Background(),
		`SELECT approval_status FROM bots WHERE id = $1`, botID).Scan(&gotStatus)
	if gotStatus != "pending" {
		t.Errorf("case 2: expected approval_status=pending, got %q", gotStatus)
	}

	// ── Case 3: admin approves → 204, status = approved ──────────────────────
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/admin/bots/"+botID+"/approve", nil)
	req3 = withUserID(req3, adminID)
	req3 = addChiParams(req3, map[string]string{"id": botID})
	s.ApproveBotPublication(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Errorf("case 3: expected 204, got %d", rec3.Code)
	}
	var approvedStatus string
	s.pool.QueryRow(context.Background(),
		`SELECT approval_status FROM bots WHERE id = $1`, botID).Scan(&approvedStatus)
	if approvedStatus != "approved" {
		t.Errorf("case 3: expected approval_status=approved, got %q", approvedStatus)
	}

	// ── Case 4: publish now succeeds ─────────────────────────────────────────
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/publish", nil)
	req4 = withUserID(req4, userID)
	req4 = addChiParams(req4, map[string]string{"id": botID})
	s.PublishBot(rec4, req4)
	if rec4.Code != http.StatusNoContent {
		t.Errorf("case 4: expected 204, got %d", rec4.Code)
	}
}

func TestPublishBot_RequiresApproval(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "pub_gate@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	botID := createTestBot(t, s, userID, "Publish Gate", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/publish", nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PublishBot(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 without approval, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminRejectBot(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "reject_user@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	adminID := createAdminTestUser(t, s, "reject_admin@example.com", "pass1234", true)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", adminID)

	botID := createTestBot(t, s, userID, "Reject Bot", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// Set pending state directly
	s.pool.Exec(context.Background(),
		`UPDATE bots SET approval_status = 'pending' WHERE id = $1`, botID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/bots/"+botID+"/reject", nil)
	req = withUserID(req, adminID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.RejectBotPublication(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}

	var gotStatus string
	s.pool.QueryRow(context.Background(),
		`SELECT approval_status FROM bots WHERE id = $1`, botID).Scan(&gotStatus)
	if gotStatus != "rejected" {
		t.Errorf("expected rejected, got %q", gotStatus)
	}
}

func TestStartBot_CoinFilterBlocked(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "coinfilter@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	botID := createTestBot(t, s, userID, "Filter Test", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// Make it a signal bot with a symbol we will blacklist
	s.pool.Exec(context.Background(),
		`UPDATE bots SET strategy_config = $1 WHERE id = $2`,
		`{"bot_kind":"signal","symbol":"TRASHUSDT"}`, botID)

	// Add to blacklist
	s.pool.Exec(context.Background(),
		`UPDATE coin_filter_settings SET blacklist = ARRAY['TRASHUSDT'] WHERE id = 1`)
	defer s.pool.Exec(context.Background(),
		`UPDATE coin_filter_settings SET blacklist = '{}' WHERE id = 1`)

	// Should be blocked — expect 422
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/start", nil)
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.StartBot(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 (blocked by blacklist), got %d: %s", rec.Code, rec.Body.String())
	}

	// Enable override — should succeed (204)
	s.pool.Exec(context.Background(),
		`UPDATE bots SET ignore_coin_filter = true WHERE id = $1`, botID)
	defer s.pool.Exec(context.Background(),
		`UPDATE bots SET status = 'stopped' WHERE id = $1`, botID)

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+botID+"/start", nil)
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": botID})
	s.StartBot(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Errorf("expected 204 (override enabled), got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestPatchBot_StrategyLockWhileActive(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "strategy_lock@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	botID := createTestBot(t, s, userID, "Lock Test Bot", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// Manually set bot to active (skip coin filter logic)
	s.pool.Exec(context.Background(),
		`UPDATE bots SET status = 'active', active_since = NOW() WHERE id = $1`, botID)
	defer s.pool.Exec(context.Background(),
		`UPDATE bots SET status = 'stopped', active_since = NULL WHERE id = $1`, botID)

	// Attempt to change strategyConfig while active — should be 422
	body := `{"strategyConfig":{"strategy_type":"grid","direction":"long","leverage":5}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 when changing strategy while active, got %d: %s", rec.Code, rec.Body.String())
	}

	// Changing a non-strategy field (name) while active should still work (200)
	body2 := `{"name":"Updated Name"}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withUserID(req2, userID)
	req2 = addChiParams(req2, map[string]string{"id": botID})
	s.PatchBot(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200 for non-strategy patch while active, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestPatchBot_StrategyResetTimer(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "strategy_reset@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)

	botID := createTestBot(t, s, userID, "Timer Reset Bot", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1", botID)

	// Give the bot an accumulated timer
	s.pool.Exec(context.Background(),
		`UPDATE bots SET active_seconds_acc = 10*86400, status = 'stopped' WHERE id = $1`, botID)

	// Patch strategyConfig while stopped — should reset timer
	body := `{"strategyConfig":{"strategy_type":"grid","direction":"long","leverage":5}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/bots/"+botID, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	req = addChiParams(req, map[string]string{"id": botID})
	s.PatchBot(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var accSecs int64
	var activeSince *interface{}
	s.pool.QueryRow(context.Background(),
		`SELECT active_seconds_acc, active_since FROM bots WHERE id = $1`, botID,
	).Scan(&accSecs, &activeSince)

	if accSecs != 0 {
		t.Errorf("expected active_seconds_acc=0 after strategy change, got %d", accSecs)
	}
	if activeSince != nil {
		t.Error("expected active_since=NULL after strategy change")
	}
}

// TestDeployBot_ClonesMultiBotPair pins this plan's core fix: deploying a public
// Мультибот's signal leg must clone BOTH legs (not just the one that was deployed),
// link the two new clones via paired_bot_id, and re-point the new hedge clone's
// hedge_bot_whitelist at the new signal clone's id — not the original template's.
func TestDeployBot_ClonesMultiBotPair(t *testing.T) {
	s := newTestServer(t)
	authorID := createAdminTestUser(t, s, "bots_multi_deploy_author@example.com", "pass1234", false)
	deployerID := createAdminTestUser(t, s, "bots_multi_deploy_er@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", authorID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", deployerID)
	authorAcctID := createTestAccountLabeled(t, s, authorID, "multi-deploy-author-acct")
	deployerAcctID := createTestAccountLabeled(t, s, deployerID, "multi-deploy-deployer-acct")

	// Create a Мультибот (both legs private by default) and publish the signal leg.
	body, _ := json.Marshal(map[string]interface{}{
		"name": "Public Multi", "accountId": authorAcctID,
		"strategyConfig":      map[string]interface{}{"symbol": "BTCUSDT", "direction": "long"},
		"hedgeStrategyConfig": map[string]interface{}{"direction": "both"},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, authorID)
	s.CreateMultiBot(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create multi: got %d: %s", rec.Code, rec.Body.String())
	}
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	origSignalID := signalBot["id"].(string)
	origHedgeID := signalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", origSignalID, origHedgeID)

	if _, err := s.pool.Exec(context.Background(), "UPDATE bots SET is_public = true WHERE id=$1", origSignalID); err != nil {
		t.Fatalf("publish signal leg: %v", err)
	}

	// Deploy the signal leg (the only leg the catalog ever exposes, per Task 2).
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+origSignalID+"/deploy", bytes.NewBufferString(`{"accountId":"`+deployerAcctID+`"}`))
	req2 = withUserID(req2, deployerID)
	req2 = addChiParams(req2, map[string]string{"id": origSignalID})
	s.DeployBot(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("deploy: got %d: %s", rec2.Code, rec2.Body.String())
	}
	var newSignalBot map[string]interface{}
	json.NewDecoder(rec2.Body).Decode(&newSignalBot)
	newSignalID := newSignalBot["id"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=(SELECT paired_bot_id FROM bots WHERE id=$1)", newSignalID)

	newHedgeIDVal, _ := newSignalBot["pairedBotId"].(string)
	if newHedgeIDVal == "" {
		t.Fatalf("expected pairedBotId set on the new signal clone, got %v", newSignalBot["pairedBotId"])
	}
	if newHedgeIDVal == origHedgeID {
		t.Fatalf("new signal clone's pairedBotId still points at the ORIGINAL hedge leg (%s) — a new hedge clone should have been created", origHedgeID)
	}

	// The new hedge clone must exist, belong to the deployer, and be linked back.
	var hedgeOwner, hedgePairedID string
	if err := s.pool.QueryRow(context.Background(),
		`SELECT owner_id, paired_bot_id FROM bots WHERE id=$1`, newHedgeIDVal,
	).Scan(&hedgeOwner, &hedgePairedID); err != nil {
		t.Fatalf("new hedge clone not found: %v", err)
	}
	if hedgeOwner != deployerID {
		t.Errorf("new hedge clone owner = %s, want %s", hedgeOwner, deployerID)
	}
	if hedgePairedID != newSignalID {
		t.Errorf("new hedge clone's paired_bot_id = %s, want %s", hedgePairedID, newSignalID)
	}

	// The new hedge clone's hedge_bot_whitelist must point at the NEW signal clone, not the
	// original template's signal leg — otherwise it would watch a bot the deployer doesn't own.
	var hedgeCfgRaw []byte
	s.pool.QueryRow(context.Background(), `SELECT strategy_config FROM bots WHERE id=$1`, newHedgeIDVal).Scan(&hedgeCfgRaw)
	var hedgeCfg struct {
		HedgeBotWhitelist []string `json:"hedge_bot_whitelist"`
	}
	json.Unmarshal(hedgeCfgRaw, &hedgeCfg)
	if len(hedgeCfg.HedgeBotWhitelist) != 1 || hedgeCfg.HedgeBotWhitelist[0] != newSignalID {
		t.Errorf("new hedge clone's hedge_bot_whitelist = %v, want [%s]", hedgeCfg.HedgeBotWhitelist, newSignalID)
	}
}

// TestListBots_ExcludesMultiBotHedgeLeg pins Task 2's fix: even if a Мультибот's hedge
// leg somehow has is_public=true (e.g. an old row from before this fix, or the
// "Публичный бот" toggle setting it on both legs), it must never appear in the catalog
// on its own — only the signal leg represents the pair there.
func TestListBots_ExcludesMultiBotHedgeLeg(t *testing.T) {
	s := newTestServer(t)
	userID := createAdminTestUser(t, s, "bots_multi_catalog@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)
	acctID := createTestAccountLabeled(t, s, userID, "multi-catalog-acct")

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Catalog Multi", "accountId": acctID,
		"strategyConfig": map[string]interface{}{}, "hedgeStrategyConfig": map[string]interface{}{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, userID)
	s.CreateMultiBot(rec, req)
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	signalID := signalBot["id"].(string)
	hedgeID := signalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", signalID, hedgeID)

	// Publish BOTH legs, exactly as MultiBotForm.tsx's "Публичный бот" toggle does.
	if _, err := s.pool.Exec(context.Background(),
		"UPDATE bots SET is_public = true WHERE id=$1 OR id=$2", signalID, hedgeID,
	); err != nil {
		t.Fatalf("publish pair: %v", err)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/bots", nil)
	req2 = withUserID(req2, userID)
	s.ListBots(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp struct {
		Catalog []map[string]interface{} `json:"catalog"`
	}
	json.NewDecoder(rec2.Body).Decode(&resp)

	sawSignal, sawHedge := false, false
	for _, b := range resp.Catalog {
		if b["id"] == signalID {
			sawSignal = true
		}
		if b["id"] == hedgeID {
			sawHedge = true
		}
	}
	if !sawSignal {
		t.Error("expected the signal leg in the catalog")
	}
	if sawHedge {
		t.Error("hedge leg must NOT appear in the catalog on its own")
	}
}

// TestForkBot_CascadesToPairedLeg pins Task 3's fix — mirrors the existing
// TestStopBot_CascadesToPairedLeg/TestDeleteBot_CascadesToPairedLeg tests' structure.
func TestForkBot_CascadesToPairedLeg(t *testing.T) {
	s := newTestServer(t)
	authorID := createAdminTestUser(t, s, "bots_multi_fork_author@example.com", "pass1234", false)
	forkerID := createAdminTestUser(t, s, "bots_multi_forker@example.com", "pass1234", false)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", authorID)
	defer s.pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", forkerID)
	authorAcctID := createTestAccountLabeled(t, s, authorID, "multi-fork-author-acct")
	forkerAcctID := createTestAccountLabeled(t, s, forkerID, "multi-fork-forker-acct")

	body, _ := json.Marshal(map[string]interface{}{
		"name": "Fork Multi", "accountId": authorAcctID,
		"strategyConfig": map[string]interface{}{}, "hedgeStrategyConfig": map[string]interface{}{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bots/multi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserID(req, authorID)
	s.CreateMultiBot(rec, req)
	var signalBot map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&signalBot)
	origSignalID := signalBot["id"].(string)
	origHedgeID := signalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", origSignalID, origHedgeID)

	if _, err := s.pool.Exec(context.Background(), "UPDATE bots SET is_public = true WHERE id=$1", origSignalID); err != nil {
		t.Fatalf("publish signal leg: %v", err)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/bots/"+origSignalID+"/deploy", bytes.NewBufferString(`{"accountId":"`+forkerAcctID+`"}`))
	req2 = withUserID(req2, forkerID)
	req2 = addChiParams(req2, map[string]string{"id": origSignalID})
	s.DeployBot(rec2, req2)
	var depSignalBot map[string]interface{}
	json.NewDecoder(rec2.Body).Decode(&depSignalBot)
	depSignalID := depSignalBot["id"].(string)
	depHedgeID := depSignalBot["pairedBotId"].(string)
	defer s.pool.Exec(context.Background(), "DELETE FROM bots WHERE id=$1 OR id=$2", depSignalID, depHedgeID)

	// DeployBot's INSERT hardcodes is_fork=true at creation (a separate, pre-existing gap
	// unrelated to this plan — see TestDeployBot/TestPatchBot_LinkedSubscriptionBlocked, which
	// already fail against this same baseline quirk). Reset both legs to is_fork=false here so
	// this test exercises ForkBot's own paired-leg cascade (Task 3's actual fix) in isolation,
	// against the genuine "linked, not-yet-forked" subscription state ForkBot's guard expects.
	if _, err := s.pool.Exec(context.Background(),
		"UPDATE bots SET is_fork = false WHERE id=$1 OR id=$2", depSignalID, depHedgeID,
	); err != nil {
		t.Fatalf("reset is_fork for linked-subscription fixture: %v", err)
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/bots/"+depSignalID+"/fork", nil)
	req3 = withUserID(req3, forkerID)
	req3 = addChiParams(req3, map[string]string{"id": depSignalID})
	s.ForkBot(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("fork: got %d: %s", rec3.Code, rec3.Body.String())
	}

	var hedgeIsFork bool
	if err := s.pool.QueryRow(context.Background(), "SELECT is_fork FROM bots WHERE id=$1", depHedgeID).Scan(&hedgeIsFork); err != nil {
		t.Fatalf("hedge leg not found: %v", err)
	}
	if !hedgeIsFork {
		t.Error("expected the paired hedge leg's is_fork to also be true after forking the signal leg")
	}
}
