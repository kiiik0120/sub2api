package service

import "testing"

func TestScheduledTestRunnerRejectsOverlappingRuns(t *testing.T) {
	runner := &ScheduledTestRunnerService{}

	release, ok := runner.tryAcquireScheduledRun()
	if !ok {
		t.Fatal("first run must acquire execution slot")
	}
	if _, ok := runner.tryAcquireScheduledRun(); ok {
		t.Fatal("overlapping run must not acquire execution slot")
	}

	release()
	release, ok = runner.tryAcquireScheduledRun()
	if !ok {
		t.Fatal("run must acquire execution slot after the prior run releases it")
	}
	release()
}
