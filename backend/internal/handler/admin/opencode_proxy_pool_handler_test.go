package admin

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenCodeAdminOperationContextOutlivesClientCancellation(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	operationCtx, cancelOperation := openCodeAdminOperationContext(requestCtx)
	defer cancelOperation()

	cancelRequest()

	select {
	case <-operationCtx.Done():
		t.Fatalf("operation context was canceled with the client: %v", operationCtx.Err())
	case <-time.After(20 * time.Millisecond):
	}

	deadline, ok := operationCtx.Deadline()
	if !ok {
		t.Fatal("operation context must remain bounded by a deadline")
	}
	remaining := time.Until(deadline)
	if remaining < openCodeAdminOperationTimeout-time.Second || remaining > openCodeAdminOperationTimeout {
		t.Fatalf("unexpected operation timeout: %s", remaining)
	}
}

func TestParseOpenCodeImportExpiration(t *testing.T) {
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	expiresAt, err := parseOpenCodeImportExpiration("7", now)
	require.NoError(t, err)
	require.Equal(t, now.UTC().Add(7*24*time.Hour), *expiresAt)

	expiresAt, err = parseOpenCodeImportExpiration("", now)
	require.NoError(t, err)
	require.Nil(t, expiresAt)

	for _, value := range []string{"0", "3651", "seven"} {
		_, err = parseOpenCodeImportExpiration(value, now)
		require.Error(t, err, value)
	}
}
