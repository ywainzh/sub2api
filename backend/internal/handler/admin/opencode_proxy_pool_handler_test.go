package admin

import (
	"context"
	"testing"
	"time"
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
