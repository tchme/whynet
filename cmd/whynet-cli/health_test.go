package main

import (
	"strings"
	"testing"

	"whynet/internal/health"
	"whynet/internal/probe"
	"whynet/locales"
)

func TestHealthTextDoesNotCallAnIncompleteCheckHealthy(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	res := &health.Result{Steps: []probe.Step{{ID: "health_hosts", Status: "error"}}}
	var out strings.Builder
	writeHealthText(&out, tx, res, nil)
	if got := out.String(); !strings.Contains(got, tx.Health["head_incomplete"]) || !strings.Contains(got, tx.Steps["health_hosts"].Name) || strings.Contains(got, tx.Health["head_ok"]) {
		t.Errorf("incomplete check output: %s", got)
	}
}
