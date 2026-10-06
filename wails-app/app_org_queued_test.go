package main

import "testing"

func TestQueuedArgs(t *testing.T) {
	a, err := queuedArgs("growth")
	eqArgs(t, a, err, []string{"queued", "growth"})
	a, err = queuedArgs("  ")
	wantErr(t, a, err, "org name required")
}

func TestDocumentsArgs(t *testing.T) {
	a, err := documentsArgs("sec", "")
	eqArgs(t, a, err, []string{"documents", "sec"})
	a, err = documentsArgs("sec", "run-1")
	eqArgs(t, a, err, []string{"documents", "sec", "--run", "run-1"})
	a, err = documentsArgs(" ", "")
	wantErr(t, a, err, "org name required")
}

func TestScheduleAuditArgs(t *testing.T) {
	a, err := scheduleAuditArgs("sched")
	eqArgs(t, a, err, []string{"schedule-audit", "sched"})
	a, err = scheduleAuditArgs(" ")
	wantErr(t, a, err, "org name required")
}
