package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseOpenCodeSubscriptionClashYAML(t *testing.T) {
	payload := []byte("proxies:\n  - name: edge-a\n    type: trojan\n    server: edge.example.com\n    port: 443\n    password: secret\n")

	proxies, format, err := parseOpenCodeSubscription(payload)
	require.NoError(t, err)
	require.Equal(t, "clash_yaml", format)
	require.Len(t, proxies, 1)
	require.Equal(t, "edge-a", proxies[0]["name"])
	require.Equal(t, "trojan", proxies[0]["type"])

	encoded := base64.StdEncoding.EncodeToString(payload)
	proxies, format, err = parseOpenCodeSubscription([]byte(encoded))
	require.NoError(t, err)
	require.Equal(t, "base64_clash_yaml", format)
	require.Len(t, proxies, 1)
}

func TestParseOpenCodeSubscriptionBase64ShareLinks(t *testing.T) {
	links := "vless://11111111-1111-1111-1111-111111111111@vless.example.com:443?security=tls&type=ws&path=%2Fws#vless-node\n" +
		"trojan://secret@trojan.example.com:443?sni=trojan.example.com#trojan-node"
	payload := base64.RawStdEncoding.EncodeToString([]byte(links))

	proxies, format, err := parseOpenCodeSubscription([]byte(payload))
	require.NoError(t, err)
	require.Equal(t, "base64_share_links", format)
	require.Len(t, proxies, 2)
	require.Equal(t, "vless", proxies[0]["type"])
	require.Equal(t, true, proxies[0]["tls"])
	require.Equal(t, "trojan", proxies[1]["type"])
	require.Equal(t, "secret", proxies[1]["password"])
}

func TestOpenCodeShareLinkProtocols(t *testing.T) {
	vmessJSON, err := json.Marshal(map[string]any{
		"v": "2", "ps": "vmess-node", "add": "vmess.example.com", "port": "443",
		"id": "22222222-2222-2222-2222-222222222222", "net": "ws", "tls": "tls", "path": "/socket", "host": "vmess.example.com",
	})
	require.NoError(t, err)
	ssCredentials := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:ss-password"))
	tests := []struct {
		link     string
		protocol string
		field    string
		value    any
	}{
		{"vmess://" + base64.RawStdEncoding.EncodeToString(vmessJSON), "vmess", "uuid", "22222222-2222-2222-2222-222222222222"},
		{"ss://" + ssCredentials + "@ss.example.com:8388#ss-node", "ss", "cipher", "aes-128-gcm"},
		{"vless://11111111-1111-1111-1111-111111111111@vless.example.com:443#vless-node", "vless", "uuid", "11111111-1111-1111-1111-111111111111"},
		{"trojan://trojan-password@trojan.example.com:443#trojan-node", "trojan", "password", "trojan-password"},
		{"http://proxy-user:proxy-password@192.0.2.10:8080", "http", "username", "proxy-user"},
		{"proxy-user:proxy-password@192.0.2.11:8081", "http", "username", "proxy-user"},
		{"192.0.2.12:8082", "http", "server", "192.0.2.12"},
	}
	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			proxy, parseErr := openCodeShareLinkToMihomo(test.link)
			require.NoError(t, parseErr)
			require.Equal(t, test.protocol, proxy["type"])
			require.Equal(t, test.value, proxy[test.field])
		})
	}
}

func TestOpenCodeShareLinkRejectsInvalidSchemeLessHTTPProxy(t *testing.T) {
	tests := []string{"", "not-a-proxy", "user:password@missing-port"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := openCodeShareLinkToMihomo(input)
			require.Error(t, err)
		})
	}
}

func TestOpenCodeHTTPProxyCredentialsRemainPartOfFingerprint(t *testing.T) {
	first, err := openCodeShareLinkToMihomo("http://user-a:secret@192.0.2.10:8080")
	require.NoError(t, err)
	second, err := openCodeShareLinkToMihomo("http://user-b:secret@192.0.2.10:8080")
	require.NoError(t, err)
	firstKey, _, err := nodeFingerprint(first)
	require.NoError(t, err)
	secondKey, _, err := nodeFingerprint(second)
	require.NoError(t, err)
	require.NotEqual(t, firstKey, secondKey)
}

func TestOpenCodeRetryAtParsesRetryAfter(t *testing.T) {
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	response := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}
	response.Header.Set("Retry-After", "300")
	retryAt := openCodeRetryAt(response, now)
	require.NotNil(t, retryAt)
	require.Equal(t, now.Add(5*time.Minute), *retryAt)

	response.StatusCode = http.StatusServiceUnavailable
	require.Nil(t, openCodeRetryAt(response, now))
}

func TestNextOpenCodeMaintenanceRunUsesShanghaiMidnight(t *testing.T) {
	service := &OpenCodeProxyPoolService{}
	now := time.Date(2026, time.August, 9, 16, 30, 0, 0, time.UTC) // 2026-08-10 00:30 Asia/Shanghai
	next := service.setNextMaintenanceRun(now)
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, time.August, 11, 0, 0, 0, 0, location), next)
}

func TestOpenCodeNodeFingerprintIgnoresDisplayName(t *testing.T) {
	first := map[string]any{"name": "duplicate", "type": "trojan", "server": "one.example.com", "port": 443, "password": "secret"}
	renamed := map[string]any{"name": "renamed", "type": "trojan", "server": "one.example.com", "port": 443, "password": "secret"}
	differentWithSameName := map[string]any{"name": "duplicate", "type": "trojan", "server": "two.example.com", "port": 443, "password": "secret"}

	firstKey, _, err := nodeFingerprint(first)
	require.NoError(t, err)
	renamedKey, _, err := nodeFingerprint(renamed)
	require.NoError(t, err)
	differentKey, _, err := nodeFingerprint(differentWithSameName)
	require.NoError(t, err)

	require.Equal(t, firstKey, renamedKey)
	require.NotEqual(t, firstKey, differentKey)
}

func TestNextOpenCodeListenerPortReusesKnownReservation(t *testing.T) {
	used := map[int]struct{}{22000: {}, 22001: {}}
	port, err := nextListenerPort(used)
	require.NoError(t, err)
	require.Equal(t, 22002, port)
	_, reserved := used[port]
	require.True(t, reserved)
}

func TestReloadMihomoCandidateFailureRestoresLastKnownGood(t *testing.T) {
	requests := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			http.Error(w, "invalid candidate", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(controller.Close)

	configDir := t.TempDir()
	previous := []byte("mode: rule\nlog-level: warning\n")
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), previous, 0o600))
	service := &OpenCodeProxyPoolService{
		controller: controller.URL,
		configDir:  configDir,
		httpClient: controller.Client(),
	}
	draft := ManagedProxyNodeDraft{
		ManagedProxyNode: ManagedProxyNode{
			NodeKey: strings.Repeat("a", 64), MihomoName: "oc-test", ListenerPort: 22000,
			ListenerUsername: "user", ListenerPassword: "password",
		},
		ProxyConfig: map[string]any{"name": "oc-test", "type": "http", "server": "proxy.example.com", "port": 8080},
	}

	err := service.reloadMihomoWithRollback(context.Background(), []ManagedProxyNodeDraft{draft}, previous)
	require.Error(t, err)
	require.Equal(t, 2, requests)
	restored, readErr := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	require.NoError(t, readErr)
	require.Equal(t, previous, restored)
}

func TestReloadMihomoSkipsDirectHTTPNodes(t *testing.T) {
	requests := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(controller.Close)
	service := &OpenCodeProxyPoolService{controller: controller.URL, configDir: t.TempDir(), httpClient: controller.Client()}
	draft := ManagedProxyNodeDraft{
		ManagedProxyNode: ManagedProxyNode{NodeKey: strings.Repeat("b", 64), TransportMode: "direct_http"},
		ProxyConfig:      map[string]any{"name": "direct", "type": "http", "server": "192.0.2.10", "port": 8080},
	}
	require.NoError(t, service.reloadMihomoLocked(context.Background(), []ManagedProxyNodeDraft{draft}))
	require.Equal(t, 1, requests)
	config, err := os.ReadFile(filepath.Join(service.configDir, "config.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(config), "192.0.2.10")
}

func TestOpenCodeMihomoConcurrentReloadsKeepDiskMatchingRuntime(t *testing.T) {
	configDir := t.TempDir()
	var mu sync.Mutex
	var lastLoaded []byte
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Mihomo reads the candidate by path, so what it ends up running is
		// whatever is on disk at this instant — not necessarily what the caller
		// wrote.
		loaded, err := os.ReadFile(filepath.Join(configDir, filepath.Base(payload.Path)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		time.Sleep(time.Millisecond)
		mu.Lock()
		lastLoaded = loaded
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(controller.Close)

	service := &OpenCodeProxyPoolService{controller: controller.URL, configDir: configDir, httpClient: controller.Client()}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			name := fmt.Sprintf("oc-%d", index)
			draft := ManagedProxyNodeDraft{
				ManagedProxyNode: ManagedProxyNode{
					NodeKey: strings.Repeat(string(rune('a'+index)), 64), MihomoName: name,
					ListenerPort: 22000 + index, ListenerUsername: "user", ListenerPassword: "password",
				},
				ProxyConfig: map[string]any{"name": name, "type": "http", "server": "proxy.example.com", "port": 8080},
			}
			if err := service.reloadMihomoWithRollback(context.Background(), []ManagedProxyNodeDraft{draft}, nil); err != nil {
				t.Errorf("reload %d: %v", index, err)
			}
		}(i)
	}
	wg.Wait()

	onDisk, err := os.ReadFile(filepath.Join(configDir, "config.yaml"))
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, string(lastLoaded), string(onDisk))
}

func TestPopulateOpenCodeGeo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","country":"United States","regionName":"California","city":"Los Angeles"}`))
	}))
	t.Cleanup(server.Close)
	previousURL := openCodeGeoBaseURL
	openCodeGeoBaseURL = server.URL + "/"
	t.Cleanup(func() { openCodeGeoBaseURL = previousURL })
	result := OpenCodeNodeProbeResult{ExitIP: "203.0.113.10"}

	populateOpenCodeGeo(context.Background(), server.Client(), &result)

	require.Equal(t, "United States", result.Country)
	require.Equal(t, "California / Los Angeles", result.Region)
}

func TestMarkDuplicateOpenCodeExitsKeepsOnlyFastestProbedNode(t *testing.T) {
	results := []OpenCodeNodeProbeResult{
		{NodeID: 10, Success: true, HealthStatus: "healthy", ExitIP: "203.0.113.10", LatencyMs: 300},
		{NodeID: 11, Success: true, HealthStatus: "healthy", ExitIP: "203.0.113.10", LatencyMs: 400},
		{NodeID: 12, Success: true, HealthStatus: "healthy", ExitIP: "203.0.113.10", LatencyMs: 100},
	}

	markDuplicateOpenCodeExits(results, nil)

	require.False(t, results[0].Success)
	require.False(t, results[1].Success)
	require.True(t, results[2].Success)
	require.Equal(t, "duplicate_exit", results[0].HealthStatus)
	require.Equal(t, "duplicate_exit", results[1].HealthStatus)
	require.Equal(t, int64(12), *results[0].DuplicateOfNodeID)
	require.Equal(t, int64(12), *results[1].DuplicateOfNodeID)
	require.Nil(t, results[2].DuplicateOfNodeID)
}

func TestMarkDuplicateOpenCodeExitsKeepsExistingHealthyOwner(t *testing.T) {
	results := []OpenCodeNodeProbeResult{
		{NodeID: 11, Success: true, HealthStatus: "healthy", ExitIP: "203.0.113.10", LatencyMs: 100},
		{NodeID: 12, Success: true, HealthStatus: "healthy", ExitIP: "203.0.113.10", LatencyMs: 200},
	}

	markDuplicateOpenCodeExits(results, map[string]int64{"203.0.113.10": 10})

	for i := range results {
		require.False(t, results[i].Success)
		require.Equal(t, "duplicate_exit", results[i].HealthStatus)
		require.Equal(t, int64(10), *results[i].DuplicateOfNodeID)
	}
}

type duplicateCleanupRepoStub struct {
	OpenCodeProxyPoolRepository
	deleted []struct {
		nodeID int64
		reason string
	}
	errors map[int64]error
}

func (r *duplicateCleanupRepoStub) DeleteManagedNode(_ context.Context, nodeID int64, reason string) error {
	r.deleted = append(r.deleted, struct {
		nodeID int64
		reason string
	}{nodeID: nodeID, reason: reason})
	return r.errors[nodeID]
}

func TestDeleteDuplicateProbeResultsDeletesOnlyLinkedDuplicates(t *testing.T) {
	repo := &duplicateCleanupRepoStub{}
	service := &OpenCodeProxyPoolService{repo: repo}
	canonicalID := int64(10)
	selfID := int64(14)

	deleted, err := service.deleteDuplicateProbeResults(context.Background(), []OpenCodeNodeProbeResult{
		{NodeID: canonicalID, HealthStatus: "healthy"},
		{NodeID: 11, HealthStatus: "duplicate_exit", DuplicateOfNodeID: &canonicalID},
		{NodeID: 12, HealthStatus: "duplicate_exit", DuplicateOfNodeID: &canonicalID},
		{NodeID: 13, HealthStatus: "duplicate_exit"},
		{NodeID: selfID, HealthStatus: "duplicate_exit", DuplicateOfNodeID: &selfID},
		{NodeID: 11, HealthStatus: "duplicate_exit", DuplicateOfNodeID: &canonicalID},
	})

	require.NoError(t, err)
	require.Equal(t, 2, deleted)
	require.Equal(t, []struct {
		nodeID int64
		reason string
	}{
		{nodeID: 11, reason: "duplicate_exit"},
		{nodeID: 12, reason: "duplicate_exit"},
	}, repo.deleted)
}

func TestDeleteDuplicateProbeResultsContinuesAfterIndividualFailure(t *testing.T) {
	repo := &duplicateCleanupRepoStub{errors: map[int64]error{11: errors.New("database busy")}}
	service := &OpenCodeProxyPoolService{repo: repo}
	canonicalID := int64(10)

	deleted, err := service.deleteDuplicateProbeResults(context.Background(), []OpenCodeNodeProbeResult{
		{NodeID: 11, HealthStatus: "duplicate_exit", DuplicateOfNodeID: &canonicalID},
		{NodeID: 12, HealthStatus: "duplicate_exit", DuplicateOfNodeID: &canonicalID},
	})

	require.ErrorContains(t, err, "delete duplicate OpenCode node 11")
	require.Equal(t, 1, deleted)
	require.Len(t, repo.deleted, 2)
}
