package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func runStatusCheckDeleteRequest(t *testing.T, svc *stubAdminService, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	handler := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/api/v1/admin/accounts/status-check/delete-accounts", handler.DeleteStatusCheckAccounts)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/status-check/delete-accounts", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestDeleteStatusCheckAccountsValidatesAndReturnsPartialResults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parentID := int64(2)
	svc := newStubAdminService()
	svc.getGroupResult = &service.Group{ID: 10, Name: "openai", Platform: service.PlatformOpenAI}
	svc.getAccountsByIDsResult = []*service.Account{
		{ID: 2, Name: "parent", GroupIDs: []int64{10}},
		{ID: 1, Name: "shadow", GroupIDs: []int64{10}, ParentAccountID: &parentID},
		{ID: 3, Name: "moved", GroupIDs: []int64{99}},
	}
	svc.deleteAccountErrors = map[int64]error{2: errors.New("database busy")}

	recorder := runStatusCheckDeleteRequest(t, svc, map[string]any{
		"group_id":    10,
		"account_ids": []int64{2, 1, 1, 3, 4},
	})
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{1, 2}, svc.deletedAccountIDs, "shadows must be deleted before parents")

	var payload struct {
		Data struct {
			Requested  int                               `json:"requested"`
			Deleted    int                               `json:"deleted"`
			DeletedIDs []int64                           `json:"deleted_ids"`
			Failed     int                               `json:"failed"`
			Failures   []accountStatusCheckDeleteFailure `json:"failures"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, 4, payload.Data.Requested)
	require.Equal(t, 1, payload.Data.Deleted)
	require.Equal(t, []int64{1}, payload.Data.DeletedIDs)
	require.Equal(t, 3, payload.Data.Failed)
	require.ElementsMatch(t, []string{"account_group_changed", "account_not_found", "delete_failed"}, []string{
		payload.Data.Failures[0].Code,
		payload.Data.Failures[1].Code,
		payload.Data.Failures[2].Code,
	})
}

func TestDeleteStatusCheckAccountsRejectsInvalidRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body map[string]any
		svc  func() *stubAdminService
	}{
		{name: "invalid group", body: map[string]any{"group_id": 0, "account_ids": []int64{1}}, svc: newStubAdminService},
		{name: "empty accounts", body: map[string]any{"group_id": 1, "account_ids": []int64{}}, svc: newStubAdminService},
		{name: "invalid account", body: map[string]any{"group_id": 1, "account_ids": []int64{-1}}, svc: newStubAdminService},
		{
			name: "non openai group",
			body: map[string]any{"group_id": 1, "account_ids": []int64{1}},
			svc: func() *stubAdminService {
				svc := newStubAdminService()
				svc.getGroupResult = &service.Group{ID: 1, Platform: service.PlatformAnthropic}
				return svc
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := runStatusCheckDeleteRequest(t, test.svc(), test.body)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}

func TestDeleteStatusCheckAccountsRejectsOversizedBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ids := make([]int64, accountStatusCheckDeleteBatchLimit+1)
	for index := range ids {
		ids[index] = int64(index + 1)
	}
	recorder := runStatusCheckDeleteRequest(t, newStubAdminService(), map[string]any{
		"group_id":    1,
		"account_ids": ids,
	})
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestDeleteStatusCheckAccountsReturnsMissingGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.getGroupErr = service.ErrGroupNotFound
	recorder := runStatusCheckDeleteRequest(t, svc, map[string]any{
		"group_id":    404,
		"account_ids": []int64{1},
	})
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Empty(t, svc.deletedAccountIDs)
}
