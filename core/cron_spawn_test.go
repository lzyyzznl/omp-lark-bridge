package core

import (
	"path/filepath"
	"testing"
)

// The web task-detail "执行记录 / 历史对话" views can only link into a session
// once it has a transcript id. The regression these tests pin: a crON job's
// in-flight run and session history must gain the transcript id as soon as the
// agent session spawns — not only when the run finishes — so a running session
// is directly openable.

func TestCronRunStore_AttachTranscriptID(t *testing.T) {
	s := NewCronRunStore(filepath.Join(t.TempDir(), "runs.json"))
	run := s.Start("j1", "schedule")
	if run.Status != "running" {
		t.Fatalf("run status = %q, want running", run.Status)
	}

	s.AttachTranscriptID("j1", "2026-a.jsonl")
	if got := s.List("j1")[0].TranscriptID; got != "2026-a.jsonl" {
		t.Fatalf("transcript_id = %q, want 2026-a.jsonl", got)
	}

	// Empty id is a no-op (keeps the attached one).
	s.AttachTranscriptID("j1", "")
	if got := s.List("j1")[0].TranscriptID; got != "2026-a.jsonl" {
		t.Fatalf("empty attach clobbered id = %q", got)
	}

	// After the run finishes there is no running entry to attach to; the
	// recorded id must survive.
	s.Finish("j1", run.ID, nil, "")
	s.AttachTranscriptID("j1", "2026-b.jsonl")
	if got := s.List("j1")[0].TranscriptID; got != "2026-a.jsonl" {
		t.Fatalf("attach after finish overwrote id = %q", got)
	}
}

func TestEngine_MarkCronSpawn_AttachesRunningRun(t *testing.T) {
	dir := t.TempDir()
	store, err := NewCronStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	cs := NewCronScheduler(store)
	runs := NewCronRunStore(filepath.Join(dir, "runs.json"))
	cs.AttachRunStore(runs)

	job := &CronJob{
		ID: "j1", Project: "p", SessionKey: "feishu:oc:c1:ou_1",
		CronExpr: "0 6 * * *", Prompt: "hi", SessionMode: "new_per_run",
	}
	if err := cs.AddJob(job); err != nil {
		t.Fatal(err)
	}

	e := &Engine{cronScheduler: cs}
	e.trackCronJob(job, job.SessionKey)
	// ExecuteCronJob additionally registers the interactive key the session
	// actually spawns under (sessionKey#cron:<sid>); replicate that here.
	iKey := job.SessionKey + "#cron:s10"
	e.trackCronJob(job, iKey)
	defer e.untrackCronJob(job)

	runs.Start(job.ID, "schedule")

	// The run is still in progress: spawned transcript becomes linkable now.
	// Spawn surfaces carry the interactive key (sessionKey#cron:<sid>), which
	// must also resolve back to the job.
	e.markCronSpawn(iKey, "2026-spawned.jsonl")
	if got := store.Get(job.ID).LastSessionID; got != "2026-spawned.jsonl" {
		t.Fatalf("last_session_id = %q, want 2026-spawned.jsonl", got)
	}
	if got := runs.List(job.ID)[0].TranscriptID; got != "2026-spawned.jsonl" {
		t.Fatalf("running transcript_id = %q, want 2026-spawned.jsonl", got)
	}

	// A session key that is not currently executing a cron job is untouched.
	e.markCronSpawn("feishu:other:ou_x", "stray.jsonl")
	if got := store.Get(job.ID).LastSessionID; got != "2026-spawned.jsonl" {
		t.Fatalf("stray spawn clobbered last_session_id = %q", got)
	}

	// Once the job is no longer in flight, late spawns no longer attach.
	e.untrackCronJob(job)
	e.markCronSpawn(job.SessionKey, "after.jsonl")
	if got := store.Get(job.ID).LastSessionID; got != "2026-spawned.jsonl" {
		t.Fatalf("post-flight spawn changed last_session_id = %q", got)
	}
}
