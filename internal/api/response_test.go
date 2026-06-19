package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRespSuccessReportsElapsedProcessingTime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set(requestStartTimeKey, time.Now().Add(-25*time.Millisecond))

	RespSuccess(ctx, gin.H{"ok": true})

	var resp struct {
		ProcessingTime int64 `json:"processingTime"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ProcessingTime < 20 {
		t.Fatalf("processingTime = %d, want >= 20ms", resp.ProcessingTime)
	}
	if resp.ProcessingTime > 2000 {
		t.Fatalf("processingTime = %d, looks like timestamp instead of elapsed ms", resp.ProcessingTime)
	}
}

func TestRespSuccessCodeFallsBackToSmallPositiveProcessingTime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	RespSuccessCode(ctx, MetaFileSuccessCode, gin.H{"ok": true})

	var resp struct {
		ProcessingTime int64 `json:"processingTime"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.ProcessingTime <= 0 || resp.ProcessingTime > 10 {
		t.Fatalf("processingTime fallback = %d, want small positive value", resp.ProcessingTime)
	}
}
