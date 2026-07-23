package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

func newTestAgentIdentityKey(t *testing.T) (agentIdentityKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	return agentIdentityKey{
		runtimeID:  "runtime-test",
		privateKey: privateKey,
		taskID:     "task-test",
	}, base64.StdEncoding.EncodeToString(der)
}

func TestBuildAgentAssertionMatchesCodexEnvelopeAndSignature(t *testing.T) {
	key, _ := newTestAgentIdentityKey(t)
	now := time.Date(2026, 7, 14, 8, 9, 10, 0, time.FixedZone("UTC+8", 8*60*60))
	assertion, err := buildAgentAssertion(key, now)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(assertion, "AgentAssertion "))

	encoded := strings.TrimPrefix(assertion, "AgentAssertion ")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	var envelope struct {
		AgentRuntimeID string `json:"agent_runtime_id"`
		TaskID         string `json:"task_id"`
		Timestamp      string `json:"timestamp"`
		Signature      string `json:"signature"`
	}
	require.NoError(t, json.Unmarshal(decoded, &envelope))
	require.Equal(t, "runtime-test", envelope.AgentRuntimeID)
	require.Equal(t, "task-test", envelope.TaskID)
	require.Equal(t, "2026-07-14T00:09:10Z", envelope.Timestamp)
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	require.NoError(t, err)
	publicKey, ok := key.privateKey.Public().(ed25519.PublicKey)
	require.True(t, ok)
	require.True(t, ed25519.Verify(publicKey, []byte("runtime-test:task-test:2026-07-14T00:09:10Z"), signature))
}

func TestDecryptAgentTaskIDSupportsCodexSealedBoxResponse(t *testing.T) {
	key, _ := newTestAgentIdentityKey(t)
	digest := sha512.Sum512(key.privateKey.Seed())
	var curvePrivate [32]byte
	copy(curvePrivate[:], digest[:32])
	curvePrivate[0] &= 248
	curvePrivate[31] &= 127
	curvePrivate[31] |= 64
	curvePublicBytes, err := curve25519.X25519(curvePrivate[:], curve25519.Basepoint)
	require.NoError(t, err)
	var curvePublic [32]byte
	copy(curvePublic[:], curvePublicBytes)
	ciphertext, err := box.SealAnonymous(nil, []byte("task-sealed"), &curvePublic, rand.Reader)
	require.NoError(t, err)
	got, err := decryptAgentTaskID(key, base64.StdEncoding.EncodeToString(ciphertext))
	require.NoError(t, err)
	require.Equal(t, "task-sealed", got)
}

func TestRegisterAgentIdentityTaskAcceptsPlaintextAndEncryptedResponses(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/agent/runtime-test/task/register", r.URL.Path)
		var request map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.NotEmpty(t, request["timestamp"])
		require.NotEmpty(t, request["signature"])
		requestCount++
		if requestCount == 2 {
			digest := sha512.Sum512(key.privateKey.Seed())
			var curvePrivate [32]byte
			copy(curvePrivate[:], digest[:32])
			curvePrivate[0] &= 248
			curvePrivate[31] &= 127
			curvePrivate[31] |= 64
			curvePublicBytes, curveErr := curve25519.X25519(curvePrivate[:], curve25519.Basepoint)
			require.NoError(t, curveErr)
			var curvePublic [32]byte
			copy(curvePublic[:], curvePublicBytes)
			ciphertext, sealErr := box.SealAnonymous(nil, []byte("task-encrypted"), &curvePublic, rand.Reader)
			require.NoError(t, sealErr)
			_, _ = fmt.Fprintf(w, `{"encrypted_task_id":%q}`, base64.StdEncoding.EncodeToString(ciphertext))
			return
		}
		_, _ = w.Write([]byte(`{"task_id":"task-plain"}`))
	}))
	defer server.Close()
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

	account := &Account{ID: 1, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":         OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":  key.runtimeID,
		"agent_private_key": privateKey,
	}}
	taskID, err := registerAgentIdentityTask(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "task-plain", taskID)
	taskID, err = registerAgentIdentityTask(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "task-encrypted", taskID)
}

func TestRegisterAgentIdentityTaskPreservesBoundedErrorMetadataWithoutLeakingBody(t *testing.T) {
	_, privateKey := newTestAgentIdentityKey(t)
	secret := "refresh_token=must-not-leak"
	body := strings.Repeat("x", agentIdentityRegistrationBodyLimit) + secret
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "37")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

	account := &Account{ID: 2, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":         OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":  "runtime-test",
		"agent_private_key": privateKey,
	}}
	_, err := registerAgentIdentityTask(context.Background(), account)
	var registrationErr *AgentIdentityRegistrationError
	require.True(t, errors.As(err, &registrationErr))
	require.Equal(t, http.StatusForbidden, registrationErr.StatusCode)
	require.Equal(t, "37", registrationErr.ResponseHeaders.Get("Retry-After"))
	require.Len(t, registrationErr.ResponseBody, agentIdentityRegistrationBodyLimit)
	require.NotContains(t, err.Error(), secret)
	require.NotContains(t, err.Error(), "xxxxxxxx")
}

func TestRegisterAgentIdentityTaskWrapsTransportAndCredentialFailures(t *testing.T) {
	_, privateKey := newTestAgentIdentityKey(t)
	account := &Account{ID: 3, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":         OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":  "runtime-test",
		"agent_private_key": privateKey,
	}}
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = "http://127.0.0.1:1"
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

	_, err := registerAgentIdentityTask(context.Background(), account)
	var registrationErr *AgentIdentityRegistrationError
	require.True(t, errors.As(err, &registrationErr))
	require.Zero(t, registrationErr.StatusCode)
	require.Equal(t, "agent identity task registration request failed", registrationErr.Error())

	invalid := &Account{ID: 4, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":        OpenAIAuthModeAgentIdentity,
		"agent_runtime_id": "runtime-test",
	}}
	_, err = registerAgentIdentityTask(context.Background(), invalid)
	var credentialErr *agentIdentityCredentialError
	require.True(t, errors.As(err, &credentialErr))
	require.NotContains(t, err.Error(), "private")
}

func TestAgentIdentityAuthenticationFailureUsesOpenAIAccountPoliciesAndFailover(t *testing.T) {
	t.Run("403 cooldown then permanent error", func(t *testing.T) {
		repo := &agentIdentityStateRepo{}
		rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		counter := &agentIdentity403CounterStub{counts: []int64{1, 3}}
		rateLimit.SetOpenAI403CounterCache(counter)
		gateway := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rateLimit}
		rateLimit.SetAccountRuntimeBlocker(gateway)
		account := testAgentIdentityAccount(11)

		first := gateway.handleAgentIdentityAuthenticationFailure(context.Background(), account, &AgentIdentityRegistrationError{
			StatusCode:      http.StatusForbidden,
			ResponseHeaders: http.Header{},
			ResponseBody:    []byte(`{"error":{"message":"registration forbidden"}}`),
		}, "gpt-5")
		require.Equal(t, NextAccountRetry, first.NextAccountAction)
		require.Equal(t, 1, repo.tempCalls)
		require.Equal(t, 0, repo.setErrorCalls)

		second := gateway.handleAgentIdentityAuthenticationFailure(context.Background(), account, &AgentIdentityRegistrationError{
			StatusCode:   http.StatusForbidden,
			ResponseBody: []byte(`{"error":{"message":"registration forbidden"}}`),
		}, "gpt-5")
		require.Equal(t, NextAccountRetry, second.NextAccountAction)
		require.Equal(t, 1, repo.setErrorCalls)
	})

	t.Run("429 cooldown", func(t *testing.T) {
		repo := &agentIdentityStateRepo{}
		rateLimit := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		gateway := &OpenAIGatewayService{accountRepo: repo, rateLimitService: rateLimit}
		rateLimit.SetAccountRuntimeBlocker(gateway)
		account := testAgentIdentityAccount(12)

		failover := gateway.handleAgentIdentityAuthenticationFailure(context.Background(), account, &AgentIdentityRegistrationError{
			StatusCode: http.StatusTooManyRequests,
		}, "gpt-5")
		require.Equal(t, NextAccountRetry, failover.NextAccountAction)
		require.Equal(t, 1, repo.rateLimitedCalls)
	})

	t.Run("5xx is temporary", func(t *testing.T) {
		repo := &agentIdentityStateRepo{}
		gateway := &OpenAIGatewayService{accountRepo: repo}
		account := testAgentIdentityAccount(13)

		failover := gateway.handleAgentIdentityAuthenticationFailure(context.Background(), account, &AgentIdentityRegistrationError{StatusCode: http.StatusBadGateway})
		require.Equal(t, NextAccountRetry, failover.NextAccountAction)
		require.Equal(t, 1, repo.tempCalls)
		require.Equal(t, 0, repo.setErrorCalls)
	})

	t.Run("local credentials are permanent", func(t *testing.T) {
		repo := &agentIdentityStateRepo{}
		gateway := &OpenAIGatewayService{accountRepo: repo}
		account := testAgentIdentityAccount(14)

		failover := gateway.handleAgentIdentityAuthenticationFailure(context.Background(), account, &agentIdentityCredentialError{cause: errors.New("missing key")})
		require.Equal(t, GatewayFailureStageAccountAuth, failover.Stage)
		require.Equal(t, GatewayFailureScopeAccount, failover.Scope)
		require.Equal(t, NextAccountRetry, failover.NextAccountAction)
		require.Equal(t, 1, repo.setErrorCalls)
		require.Equal(t, 0, repo.tempCalls)
	})
}

func TestAccountTestAgentIdentityAuthenticationFailureUsesSharedHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := &agentIdentityFailureHandlerStub{}
	service := &AccountTestService{agentIdentityAuthFailures: handler}
	var event TestEvent
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = (&http.Request{}).WithContext(context.WithValue(context.Background(), accountTestEventSinkContextKey{}, AccountTestEventSink(func(got TestEvent) {
		event = got
	})))

	err := service.sendAgentIdentityAuthenticationErrorAndEnd(
		c,
		c.Request.Context(),
		testAgentIdentityAccount(15),
		"gpt-5",
		&AgentIdentityRegistrationError{StatusCode: http.StatusForbidden},
	)

	require.Error(t, err)
	require.Equal(t, 1, handler.calls)
	require.Equal(t, http.StatusForbidden, event.HTTPStatus)
	require.True(t, event.AccountStateHandled)
	require.NotContains(t, event.Error, "must-not-leak")
}

func testAgentIdentityAccount(id int64) *Account {
	return &Account{ID: id, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Status: "active", Schedulable: true, Credentials: map[string]any{
		"auth_mode":        OpenAIAuthModeAgentIdentity,
		"agent_runtime_id": "runtime-test",
	}}
}

type agentIdentityStateRepo struct {
	AccountRepository
	setErrorCalls    int
	tempCalls        int
	rateLimitedCalls int
}

func (r *agentIdentityStateRepo) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

func (r *agentIdentityStateRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}

func (r *agentIdentityStateRepo) SetRateLimited(context.Context, int64, time.Time) error {
	r.rateLimitedCalls++
	return nil
}

type agentIdentity403CounterStub struct {
	counts []int64
}

func (s *agentIdentity403CounterStub) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	if len(s.counts) == 0 {
		return 1, nil
	}
	count := s.counts[0]
	s.counts = s.counts[1:]
	return count, nil
}

func (s *agentIdentity403CounterStub) ResetOpenAI403Count(context.Context, int64) error {
	return nil
}

type agentIdentityFailureHandlerStub struct {
	calls int
}

func (s *agentIdentityFailureHandlerStub) handleAgentIdentityAuthenticationFailure(context.Context, *Account, error, ...string) *UpstreamFailoverError {
	s.calls++
	return &UpstreamFailoverError{
		StatusCode:        http.StatusForbidden,
		Stage:             GatewayFailureStageAccountAuth,
		Scope:             GatewayFailureScopeAccount,
		NextAccountAction: NextAccountRetry,
	}
}

func TestEnsureAgentIdentityTaskPersistsAndRedactsCredentials(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"task_id":"task-persisted"}`))
	}))
	defer server.Close()
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

	repo := &agentIdentityCredentialsRepo{}
	account := &Account{ID: 7, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":          OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":   key.runtimeID,
		"agent_private_key":  privateKey,
		"chatgpt_account_id": "account-test",
	}}
	service := &OpenAIGatewayService{accountRepo: repo}
	require.NoError(t, service.ensureAgentIdentityTask(context.Background(), account, ""))
	require.Equal(t, "task-persisted", account.GetCredential("task_id"))
	require.Equal(t, "task-persisted", repo.credentials["task_id"])
	require.True(t, IsSensitiveCredentialKey("agent_private_key"))
	redacted := make(map[string]any)
	for key, value := range account.Credentials {
		if !IsSensitiveCredentialKey(key) {
			redacted[key] = value
		}
	}
	require.NotContains(t, string(mustAgentIdentityJSON(t, redacted)), privateKey)
}

func TestEnsureAgentIdentityTaskSharesLockAcrossServicesForSameAccount(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	account := &Account{ID: 9001, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Credentials: map[string]any{
		"auth_mode":         OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":  key.runtimeID,
		"agent_private_key": privateKey,
	}}
	repo := &agentIdentityCredentialsRepo{account: account}
	registerCalls := 0
	var registerMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerMu.Lock()
		registerCalls++
		registerMu.Unlock()
		_, _ = w.Write([]byte(`{"task_id":"task-shared"}`))
	}))
	defer server.Close()
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

	start := make(chan struct{})
	errors := make(chan error, 2)
	requests := []*Account{cloneAgentIdentityTestAccount(account), cloneAgentIdentityTestAccount(account)}
	for _, request := range requests {
		go func() {
			<-start
			errors <- ensureAgentIdentityTaskForAccount(context.Background(), repo, nil, &sync.Mutex{}, request, "")
		}()
	}
	close(start)
	require.NoError(t, <-errors)
	require.NoError(t, <-errors)
	registerMu.Lock()
	defer registerMu.Unlock()
	require.Equal(t, 1, registerCalls)
	require.Equal(t, "task-shared", repo.account.GetCredential("task_id"))
}

func cloneAgentIdentityTestAccount(account *Account) *Account {
	copy := *account
	copy.Credentials = shallowCopyMap(account.Credentials)
	return &copy
}

type agentIdentityCredentialsRepo struct {
	AccountRepository
	credentials map[string]any
	account     *Account
	mu          sync.Mutex
}

func (r *agentIdentityCredentialsRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return r.account, nil
}

func (r *agentIdentityCredentialsRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.credentials = credentials
	return nil
}

func mustAgentIdentityJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}
