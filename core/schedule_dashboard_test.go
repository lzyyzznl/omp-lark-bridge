package core

import (
	"errors"
	"strings"
	"testing"
)

func TestParseScheduleCardArgs(t *testing.T) {
	cases := []struct {
		args string
		page int
		all  bool
	}{
		{"", 1, false},
		{"2", 2, false},
		{"all", 1, true},
		{"all 3", 3, true},
		{"3 all", 3, true},
		{"ALL 0", 1, true},
		{"bogus", 1, false},
	}
	for _, c := range cases {
		page, all := parseScheduleCardArgs(c.args)
		if page != c.page || all != c.all {
			t.Fatalf("parseScheduleCardArgs(%q) = (%d, %v), want (%d, %v)", c.args, page, all, c.page, c.all)
		}
	}
}

func TestSchedulePageSlice(t *testing.T) {
	items := make([]int, 9)
	for i := range items {
		items[i] = i + 1
	}

	visible, page, pages := schedulePageSlice(items, 1, 8)
	if page != 1 || pages != 2 || len(visible) != 8 {
		t.Fatalf("page1 = (%v, %d/%d), want 8 items page 1/2", visible, page, pages)
	}
	visible, page, pages = schedulePageSlice(items, 2, 8)
	if page != 2 || pages != 2 || len(visible) != 1 || visible[0] != 9 {
		t.Fatalf("page2 = (%v, %d/%d), want [9] page 2/2", visible, page, pages)
	}

	// Out-of-range pages clamp instead of rendering an empty card.
	if _, page, pages := schedulePageSlice(items, 99, 8); page != 2 || pages != 2 {
		t.Fatalf("page 99 clamped to %d/%d, want 2/2", page, pages)
	}
	if visible, page, pages := schedulePageSlice([]int(nil), 5, 8); len(visible) != 0 || page != 1 || pages != 1 {
		t.Fatalf("empty slice = (%v, %d/%d), want (nil, 1/1)", visible, page, pages)
	}
}

func TestShouldNotifyScheduleFailure(t *testing.T) {
	cases := map[int]bool{0: false, 1: true, 2: false, 4: false, 5: true, 9: false, 10: true}
	for streak, want := range cases {
		if got := shouldNotifyScheduleFailure(streak); got != want {
			t.Fatalf("shouldNotifyScheduleFailure(%d) = %v, want %v", streak, got, want)
		}
	}
}

func TestCronMarkRunTracksFailStreak(t *testing.T) {
	store, err := NewCronStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	if err := store.Add(&CronJob{ID: "job1", Project: "p", SessionKey: "test:ch", CronExpr: "0 9 * * *", Enabled: true}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	store.MarkRun("job1", errors.New("boom"))
	store.MarkRun("job1", errors.New("boom again"))
	if got := store.Get("job1").FailStreak; got != 2 {
		t.Fatalf("FailStreak = %d, want 2", got)
	}
	if got := store.Get("job1").LastError; !strings.Contains(got, "boom again") {
		t.Fatalf("LastError = %q, want latest error", got)
	}
	store.MarkRun("job1", nil)
	if got := store.Get("job1").FailStreak; got != 0 {
		t.Fatalf("FailStreak after success = %d, want 0", got)
	}
	if got := store.Get("job1").LastError; got != "" {
		t.Fatalf("LastError after success = %q, want empty", got)
	}
}

func newScheduleCardTestEngine(t *testing.T, jobs ...*CronJob) (*Engine, *stubCardPlatform, *CronScheduler) {
	t.Helper()
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store, err := NewCronStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	scheduler := NewCronScheduler(store)
	for _, job := range jobs {
		if err := scheduler.Store().Add(job); err != nil {
			t.Fatalf("add job: %v", err)
		}
	}
	e.SetCronScheduler(scheduler)
	return e, p, scheduler
}

func cardContainsButton(card *Card, data string) bool {
	for _, row := range card.CollectButtons() {
		for _, btn := range row {
			if btn.Data == data {
				return true
			}
		}
	}
	return false
}

func TestRenderCronCardView_RunButtonBadgeAndPaging(t *testing.T) {
	jobs := []*CronJob{
		{ID: "job-ok", Project: "test", SessionKey: "test:ch1", CronExpr: "0 9 * * *", Enabled: true, Description: "daily report"},
		{ID: "job-bad", Project: "test", SessionKey: "test:ch1", CronExpr: "*/5 * * * *", Enabled: true, Description: "flaky", FailStreak: 3, LastError: "session busy"},
		{ID: "job-off", Project: "test", SessionKey: "other:ch2", CronExpr: "0 10 * * *", Enabled: false, Description: "paused"},
	}
	e, _, _ := newScheduleCardTestEngine(t, jobs...)

	card := e.renderCronCardView("test:ch1", 1, false)
	if !cardContainsButton(card, "act:/cron run job-ok") {
		t.Fatalf("cron card must offer a run-now button: %+v", card.CollectButtons())
	}
	if !cardContainsButton(card, "nav:/cron all 1") {
		t.Fatalf("cron card must offer the all-scope toggle")
	}
	text := card.RenderText()
	if !strings.Contains(text, "daily report") {
		t.Fatalf("card missing job description: %s", text)
	}
	if strings.Contains(text, "paused") {
		t.Fatalf("session-scoped card must not list other chats' jobs: %s", text)
	}

	// Fail streak drives the badge and the visible counter.
	failedCard := e.renderCronCardView("test:ch1", 1, false)
	if !strings.Contains(failedCard.RenderText(), "❗") || !strings.Contains(failedCard.RenderText(), "3") {
		t.Fatalf("failed job must be badged with its streak: %s", failedCard.RenderText())
	}

	allCard := e.renderCronCardView("test:ch1", 1, true)
	if !strings.Contains(allCard.RenderText(), "paused") {
		t.Fatalf("all-scope card must list every project job: %s", allCard.RenderText())
	}
}

func TestRenderCronCardView_PaginationLinks(t *testing.T) {
	var jobs []*CronJob
	for i := range 10 {
		jobs = append(jobs, &CronJob{
			ID:          "job" + string(rune('a'+i)),
			Project:     "test",
			SessionKey:  "test:ch1",
			CronExpr:    "0 9 * * *",
			Enabled:     true,
			Description: "job " + string(rune('a'+i)),
		})
	}
	e, _, _ := newScheduleCardTestEngine(t, jobs...)

	first := e.renderCronCardView("test:ch1", 1, false)
	if !cardContainsButton(first, "nav:/cron 2") {
		t.Fatalf("first page must link to page 2: %+v", first.CollectButtons())
	}
	second := e.renderCronCardView("test:ch1", 2, false)
	if !cardContainsButton(second, "nav:/cron 1") {
		t.Fatalf("second page must link back to page 1: %+v", second.CollectButtons())
	}
	if strings.Count(second.RenderText(), "job ") != 2 {
		t.Fatalf("page 2 should hold the remaining 2 jobs: %s", second.RenderText())
	}
}

func TestNotifyScheduleFailurePushesCard(t *testing.T) {
	e, p, _ := newScheduleCardTestEngine(t) // stubCardPlatform implements ReconstructReplyCtx
	e.notifyScheduleFailure("cron", "job1", "daily report", "test:oc_chat:ou_user", false, 1, errors.New("session busy"))

	if len(p.sentCards) != 1 {
		t.Fatalf("sent cards = %d, want 1", len(p.sentCards))
	}
	text := p.sentCards[0].RenderText()
	for _, want := range []string{"cron", "job1", "daily report", "session busy"} {
		if !strings.Contains(text, want) {
			t.Fatalf("alert card missing %q: %s", want, text)
		}
	}

	// Muted jobs never send; the alert throttle skips non-notifying streaks.
	p.sentCards = nil
	e.notifyScheduleFailure("cron", "job1", "daily report", "test:oc_chat:ou_user", true, 1, errors.New("x"))
	e.notifyScheduleFailure("cron", "job1", "daily report", "test:oc_chat:ou_user", false, 2, errors.New("x"))
	if len(p.sentCards) != 0 {
		t.Fatalf("muted / throttled alerts must not send, got %d", len(p.sentCards))
	}

	// streak 5 is inside the notify cadence again.
	e.notifyScheduleFailure("cron", "job1", "daily report", "test:oc_chat:ou_user", false, 5, errors.New("x"))
	if len(p.sentCards) != 1 {
		t.Fatalf("streak 5 should notify, got %d cards", len(p.sentCards))
	}
}

func TestParseCronSessionFlags(t *testing.T) {
	cases := []struct {
		in   []string
		rest []string
		mode string
	}{
		{[]string{"0", "9", "*", "*", "*", "报告"}, []string{"0", "9", "*", "*", "*", "报告"}, ""},
		{[]string{"--new-session", "0", "9", "*", "*", "*", "报告"}, []string{"0", "9", "*", "*", "*", "报告"}, "new_per_run"},
		{[]string{"0", "9", "*", "*", "*", "--reuse", "报告"}, []string{"0", "9", "*", "*", "*", "报告"}, "reuse"},
		{[]string{"--session-mode", "new", "0", "9", "*", "*", "*", "报告"}, []string{"0", "9", "*", "*", "*", "报告"}, "new_per_run"},
		{[]string{"--session-mode=new-per-run", "0", "9", "*", "*", "*", "报告"}, []string{"0", "9", "*", "*", "*", "报告"}, "new_per_run"},
	}
	for _, c := range cases {
		rest, mode := parseCronSessionFlags(c.in)
		if mode != c.mode {
			t.Fatalf("parseCronSessionFlags(%v) mode = %q, want %q", c.in, mode, c.mode)
		}
		if strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Fatalf("parseCronSessionFlags(%v) rest = %v, want %v", c.in, rest, c.rest)
		}
	}
}

// newCronCmdEngine wires a cron store + a recording platform for command tests.
func newCronCmdEngine(t *testing.T) (*Engine, *stubPlatformEngine, *CronScheduler) {
	t.Helper()
	store, err := NewCronStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	scheduler := NewCronScheduler(store)
	e.SetCronScheduler(scheduler)
	return e, p, scheduler
}

func TestCronAdd_DefaultsToReusingTheSession(t *testing.T) {
	e, p, scheduler := newCronCmdEngine(t)

	e.cmdCronAdd(p, &Message{SessionKey: "test:ch1", ReplyCtx: "ctx"}, strings.Fields("0 9 * * * 每日报告"))

	jobs := scheduler.Store().ListBySessionKey("test:ch1")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.SessionMode != "" || job.UsesNewSessionPerRun() {
		t.Fatalf("SessionMode = %q, want default reuse (empty)", job.SessionMode)
	}
	if scheduler.UsesNewSession(job) {
		t.Fatal("default job must reuse the chat session, not start a new one per run")
	}
	if job.SessionKey != "test:ch1" {
		t.Fatalf("SessionKey = %q, want the chat session", job.SessionKey)
	}
}

func TestCronAdd_NewSessionFlagOptsIn(t *testing.T) {
	e, p, scheduler := newCronCmdEngine(t)

	e.cmdCronAdd(p, &Message{SessionKey: "test:ch1", ReplyCtx: "ctx"}, strings.Fields("--new-session 0 9 * * * 每日报告"))

	jobs := scheduler.Store().ListBySessionKey("test:ch1")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if !jobs[0].UsesNewSessionPerRun() || !scheduler.UsesNewSession(jobs[0]) {
		t.Fatalf("SessionMode = %q, want new_per_run", jobs[0].SessionMode)
	}
	if jobs[0].Prompt != "每日报告" {
		t.Fatalf("Prompt = %q, want the positional prompt", jobs[0].Prompt)
	}
}

func TestCronSessionModeCommand_TogglesAndPersists(t *testing.T) {
	dataDir := t.TempDir()
	store, err := NewCronStore(dataDir)
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	scheduler := NewCronScheduler(store)
	e.SetCronScheduler(scheduler)
	if err := scheduler.Store().Add(&CronJob{
		ID: "job1", Project: "test", SessionKey: "test:ch1", CronExpr: "0 9 * * *",
		Prompt: "x", Enabled: true,
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	e.cmdCronSessionMode(p, &Message{SessionKey: "test:ch1", ReplyCtx: "ctx"}, []string{"job1", "new"})
	if !scheduler.UsesNewSession(scheduler.Store().Get("job1")) {
		t.Fatal("/cron session job1 new must switch the job to a fresh session per run")
	}

	e.cmdCronSessionMode(p, &Message{SessionKey: "test:ch1", ReplyCtx: "ctx"}, []string{"job1", "reuse"})
	if scheduler.UsesNewSession(scheduler.Store().Get("job1")) {
		t.Fatal("/cron session job1 reuse must switch back to the chat session")
	}

	// Reopening the store from disk keeps the choice.
	reloaded, err := NewCronStore(dataDir)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if got := reloaded.Get("job1").SessionMode; got != "reuse" {
		t.Fatalf("persisted SessionMode = %q, want reuse", got)
	}
}

func TestRenderCronCardView_SessionToggleButton(t *testing.T) {
	store, err := NewCronStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewCronStore: %v", err)
	}
	p := &stubCardPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangChinese)
	scheduler := NewCronScheduler(store)
	e.SetCronScheduler(scheduler)

	for _, job := range []*CronJob{
		{ID: "reuse-job", Project: "test", SessionKey: "test:ch1", CronExpr: "0 9 * * *", Enabled: true, Description: "复用"},
		{ID: "fresh-job", Project: "test", SessionKey: "test:ch1", CronExpr: "0 10 * * *", Enabled: true, Description: "新建", SessionMode: "new_per_run"},
	} {
		if err := store.Add(job); err != nil {
			t.Fatalf("add job: %v", err)
		}
	}

	card := e.renderCronCardView("test:ch1", 1, false)
	if !cardContainsButton(card, "act:/cron newsession reuse-job") {
		t.Fatalf("reuse job must offer the new-session toggle: %+v", card.CollectButtons())
	}
	if !cardContainsButton(card, "act:/cron reuse fresh-job") {
		t.Fatalf("per-run job must offer the reuse toggle: %+v", card.CollectButtons())
	}
	text := card.RenderText()
	if !strings.Contains(text, "每次新建会话") || !strings.Contains(text, "复用本会话") {
		t.Fatalf("card must state each job's session policy: %s", text)
	}
}
