package main

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestAutomaticSyncWaitsSinceLastCompletionAndManualRemainsAvailable(t *testing.T) {
	i := &Importer{state: SyncStatus{Started: 1000, Finished: 1040}}
	if i.claimSync(true, 1339) {
		t.Fatal("automatic sync started before five minutes after completion")
	}
	if !i.claimSync(true, 1340) {
		t.Fatal("automatic sync did not start when due")
	}
	if !i.status().Automatic || i.status().Started != 1340 {
		t.Fatal("automatic run metadata missing")
	}
	if i.claimSync(false, 1341) || i.claimSync(true, 1341) {
		t.Fatal("running syncs overlapped")
	}
	i.update(func(s *SyncStatus) { s.Running = false; s.Finished = 1350 })
	if !i.claimSync(false, 1351) {
		t.Fatal("manual sync was rate limited")
	}
}

func TestAutomaticSyncAcrossConcurrentPages(t *testing.T) {
	i := &Importer{state: SyncStatus{Finished: 1000}}
	var group sync.WaitGroup
	var started atomic.Int32
	for range 20 {
		group.Go(func() {
			if i.claimSync(true, 1300) {
				started.Add(1)
			}
		})
	}
	group.Wait()
	if started.Load() != 1 {
		t.Fatalf("concurrent pages started %d runs", started.Load())
	}
}

func TestAutomaticSyncScheduleSurvivesRestartAndInterruptedRun(t *testing.T) {
	s := testStore(t)
	report := SyncStatus{Started: 1000, Finished: 1020, Automatic: true, Stage: "done"}
	_, err := s.Write.Exec("INSERT INTO sync_runs(started,finished,report) VALUES (?,?,?)", report.Started, report.Finished, jsonText(report))
	check(t, err)
	i := newImporter(s)
	if i.claimSync(true, 1319) {
		t.Fatal("restart forgot the last completed sync")
	}
	if !i.claimSync(true, 1320) {
		t.Fatal("restarted importer did not start when due")
	}
	i = &Importer{state: SyncStatus{Started: 2000}}
	if i.claimSync(true, 2299) {
		t.Fatal("incomplete run was retried before the cooldown")
	}
	if !i.claimSync(true, 2300) {
		t.Fatal("incomplete run was never retried")
	}
}
