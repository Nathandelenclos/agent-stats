package command

import (
	"errors"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/infra/web"
)

func TestServeNamesTheRiskOfRemoteAccess(t *testing.T) {
	err := runServe([]string{"--addr", "0.0.0.0:7373"})
	if !errors.Is(err, web.ErrRemoteAddr) {
		t.Fatalf("error = %v", err)
	}
}
