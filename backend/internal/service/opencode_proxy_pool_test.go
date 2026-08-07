package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
