package core

// Scheduled-job dashboard cards (/cron, /timer) and failure alerts.
//
// The cards are the only "dashboard" the bridge has (no web UI): each job gets a
// status block, action buttons (run now / enable / mute / delete) and the list is
// paginated. Jobs failed repeatedly are badged and — when not muted — a failure
// card is pushed into the originating chat.

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	scheduleCardPageSize = 8
	// scheduleFailureNotifyEvery throttles repeated failure alerts of the same
	// job: notify the first failure, then every Nth consecutive one.
	scheduleFailureNotifyEvery = 5
)

// parseScheduleCardArgs reads "all", "<page>" or "all <page>" from a nav action.
func parseScheduleCardArgs(args string) (page int, all bool) {
	page = 1
	for _, field := range strings.Fields(strings.ToLower(args)) {
		if field == "all" {
			all = true
			continue
		}
		if n, err := strconv.Atoi(field); err == nil && n > 0 {
			page = n
		}
	}
	return page, all
}

// schedulePageSlice returns the requested page clamped into range.
func schedulePageSlice[T any](items []T, page, perPage int) (visible []T, pageOut, pages int) {
	if perPage <= 0 {
		perPage = scheduleCardPageSize
	}
	pages = (len(items) + perPage - 1) / perPage
	if pages < 1 {
		return nil, 1, 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * perPage
	end := start + perPage
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], page, pages
}

// shouldNotifyScheduleFailure reports whether a failure alert is worth sending
// for the given consecutive-failure count.
func shouldNotifyScheduleFailure(streak int) bool {
	if streak <= 0 {
		return false
	}
	return streak == 1 || streak%scheduleFailureNotifyEvery == 0
}

// sortCronJobs puts enabled jobs first, then by description, so the dashboard is
// stable across renders.
func sortCronJobs(jobs []*CronJob) {
	sort.SliceStable(jobs, func(i, j int) bool {
		if jobs[i].Enabled != jobs[j].Enabled {
			return jobs[i].Enabled
		}
		return cronJobLabel(jobs[i]) < cronJobLabel(jobs[j])
	})
}

func cronJobLabel(j *CronJob) string {
	if j.Description != "" {
		return j.Description
	}
	if j.IsShellJob() {
		return j.Exec
	}
	return j.Prompt
}

func timerJobLabel(j *TimerJob) string {
	if j.Description != "" {
		return j.Description
	}
	if j.IsShellJob() {
		return j.Exec
	}
	return j.Prompt
}

// renderCronCard keeps the /cron command entry point (first page, this chat only).
func (e *Engine) renderCronCard(sessionKey, _ string) *Card {
	return e.renderCronCardView(sessionKey, 1, false)
}

// renderCronCardView renders one page of the cron dashboard.
func (e *Engine) renderCronCardView(sessionKey string, page int, all bool) *Card {
	if e.cronScheduler == nil {
		return e.simpleCard(e.i18n.T(MsgCardTitleCron), "orange", e.i18n.T(MsgCronNotAvailable))
	}

	jobs := e.cronScheduler.Store().ListBySessionKey(sessionKey)
	if all {
		jobs = e.cronScheduler.Store().ListByProject(e.name)
	}
	if len(jobs) == 0 {
		if all {
			return e.simpleCard(e.i18n.T(MsgCardTitleCron), "orange", e.i18n.T(MsgCronEmptyAll))
		}
		return e.simpleCard(e.i18n.T(MsgCardTitleCron), "orange", e.i18n.T(MsgCronEmpty))
	}
	sortCronJobs(jobs)

	visible, page, pages := schedulePageSlice(jobs, page, scheduleCardPageSize)
	lang := e.i18n.CurrentLang()
	now := time.Now()

	cb := NewCard().Title(e.i18n.T(MsgCardTitleCron), "orange")
	cb.Markdown(fmt.Sprintf(e.i18n.T(MsgCronListTitle), len(jobs)))
	if all {
		cb.Markdown(e.i18n.T(MsgScheduleScopeAllNote))
	}

	for _, j := range visible {
		status := "✅"
		switch {
		case j.FailStreak > 0:
			status = "❗"
		case !j.Enabled:
			status = "⏸"
		}

		desc := truncateStr(cronJobLabel(j), 60)
		if j.IsShellJob() {
			desc = "🖥 " + desc
		}
		if j.Mute {
			desc += " [静音]"
		}
		sessionBadge := e.i18n.T(MsgCronSessionReuse)
		if e.cronScheduler != nil && e.cronScheduler.UsesNewSession(j) {
			sessionBadge = e.i18n.T(MsgCronSessionNew)
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %s\n", status, desc)
		sb.WriteString(e.i18n.Tf(MsgCronIDLabel, j.ID))
		sb.WriteString(e.i18n.Tf(MsgCronScheduleLabel, CronExprToHuman(j.CronExpr, lang), j.CronExpr))
		if nextRun := e.cronScheduler.NextRun(j.ID); !nextRun.IsZero() {
			eta := formatDurationI18n(time.Until(nextRun), lang)
			// The label ships its own newline; the countdown rides on the same
			// line as "Next run: <when> (in 3h)".
			line := strings.TrimSuffix(e.i18n.Tf(MsgCronNextRunLabel, nextRun.Format(cronTimeFormat(nextRun, now))), "\n")
			sb.WriteString(line + e.i18n.Tf(MsgScheduleNextEta, eta) + "\n")
		}
		if !j.LastRun.IsZero() {
			sb.WriteString(e.i18n.Tf(MsgCronLastRunLabel, j.LastRun.Format(cronTimeFormat(j.LastRun, now))))
			if j.LastError != "" {
				sb.WriteString(e.i18n.Tf(MsgCronFailedSuffix, truncateStr(j.LastError, 40)))
			}
			sb.WriteString("\n")
		}
		// The streak shows even before any last-run stamp, e.g. right after a
		// failure was recorded from a manual run.
		if j.FailStreak > 0 {
			sb.WriteString(e.i18n.Tf(MsgScheduleFailStreak, j.FailStreak))
		}
		sb.WriteString(e.i18n.Tf(MsgCronSessionLabel, sessionBadge))
		cb.Markdown(sb.String())

		// Split the 5 actions across two equal-width rows — a single row of 5
		// fill columns makes every label too narrow on Feishu and renders as "…".
		btns := []CardButton{
			PrimaryBtn(e.i18n.T(MsgCronBtnRun), fmt.Sprintf("act:/cron run %s", j.ID)),
			DefaultBtn(e.i18n.T(MsgCronBtnDisable), fmt.Sprintf("act:/cron disable %s", j.ID)),
		}
		if !j.Enabled {
			btns[1] = DefaultBtn(e.i18n.T(MsgCronBtnEnable), fmt.Sprintf("act:/cron enable %s", j.ID))
		}
		// One click flips the per-run session policy; reuse is the default.
		if e.cronScheduler != nil && e.cronScheduler.UsesNewSession(j) {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnReuseSession), fmt.Sprintf("act:/cron reuse %s", j.ID)))
		} else {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgCronBtnNewSession), fmt.Sprintf("act:/cron newsession %s", j.ID)))
		}
		cb.ButtonsEqual(btns...)

		var danger []CardButton
		if j.Mute {
			danger = append(danger, DefaultBtn(e.i18n.T(MsgCronBtnUnmute), fmt.Sprintf("act:/cron unmute %s", j.ID)))
		} else {
			danger = append(danger, DefaultBtn(e.i18n.T(MsgCronBtnMute), fmt.Sprintf("act:/cron mute %s", j.ID)))
		}
		danger = append(danger, DangerBtn(e.i18n.T(MsgCronBtnDelete), fmt.Sprintf("act:/cron delete %s", j.ID)))
		cb.ButtonsEqual(danger...)
	}

	cb.Divider()
	cb.Note(e.i18n.T(MsgCronCardHint))

	var nav []CardButton
	if page > 1 {
		nav = append(nav, e.cardPrevButton(scheduleNavAction("/cron", page-1, all)))
	}
	if all {
		nav = append(nav, DefaultBtn(e.i18n.T(MsgScheduleScopeMineBtn), scheduleNavAction("/cron", 1, false)))
	} else {
		nav = append(nav, DefaultBtn(e.i18n.T(MsgScheduleScopeAllBtn), scheduleNavAction("/cron", 1, true)))
	}
	nav = append(nav, e.cardBackButton())
	if page < pages {
		nav = append(nav, e.cardNextButton(scheduleNavAction("/cron", page+1, all)))
	}
	cb.Buttons(nav...)
	if pages > 1 {
		cb.Note(fmt.Sprintf(e.i18n.T(MsgListPageHint), page, pages))
	}
	return cb.Build()
}

// renderTimerCard keeps the /timer command entry point (first page, this chat only).
func (e *Engine) renderTimerCard(sessionKey, _ string) *Card {
	return e.renderTimerCardView(sessionKey, 1, false)
}

// renderTimerCardView renders one page of the pending-timer dashboard.
func (e *Engine) renderTimerCardView(sessionKey string, page int, all bool) *Card {
	if e.timerScheduler == nil {
		return e.simpleCard(e.i18n.T(MsgCardTitleTimer), "blue", e.i18n.T(MsgTimerNotAvailable))
	}

	jobs := e.timerScheduler.Store().ListPending()
	if !all {
		filtered := make([]*TimerJob, 0, len(jobs))
		for _, j := range jobs {
			if j.SessionKey == sessionKey {
				filtered = append(filtered, j)
			}
		}
		jobs = filtered
	}
	if len(jobs) == 0 {
		if all {
			return e.simpleCard(e.i18n.T(MsgCardTitleTimer), "blue", e.i18n.T(MsgTimerEmptyAll))
		}
		return e.simpleCard(e.i18n.T(MsgCardTitleTimer), "blue", e.i18n.T(MsgTimerEmpty))
	}
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].ScheduledAt.Before(jobs[j].ScheduledAt) })

	visible, page, pages := schedulePageSlice(jobs, page, scheduleCardPageSize)
	lang := e.i18n.CurrentLang()

	cb := NewCard().Title(e.i18n.T(MsgCardTitleTimer), "blue")
	cb.Markdown(fmt.Sprintf(e.i18n.T(MsgTimerListTitle), len(jobs)))
	if all {
		cb.Markdown(e.i18n.T(MsgScheduleScopeAllNote))
	}

	for _, j := range visible {
		desc := truncateStr(timerJobLabel(j), 60)
		if j.IsShellJob() {
			desc = "🖥 " + desc
		}
		if j.Mute {
			desc += " [静音]"
		}
		status := "⏰"
		if j.FailStreak > 0 {
			status = "❗"
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %s\n", status, desc)
		sb.WriteString(e.i18n.Tf(MsgTimerIDLabel, j.ID))
		sb.WriteString(e.i18n.Tf(MsgTimerScheduledLabel,
			j.ScheduledAt.Format("2006-01-02 15:04"), FormatTimerRemaining(j.ScheduledAt)))
		if j.FailStreak > 0 {
			// Keep the error on the streak line ("Consecutive failures: 1 (failed: …)").
			line := strings.TrimSuffix(e.i18n.Tf(MsgScheduleFailStreak, j.FailStreak), "\n")
			if j.LastError != "" {
				line += e.i18n.Tf(MsgTimerFailedSuffix, truncateStr(j.LastError, 40))
			}
			sb.WriteString(line + "\n")
		} else if j.LastError != "" {
			sb.WriteString(e.i18n.Tf(MsgTimerFailedSuffix, truncateStr(j.LastError, 40)) + "\n")
		}
		_ = lang
		cb.Markdown(sb.String())

		var btns []CardButton
		if j.Mute {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgTimerBtnUnmute), fmt.Sprintf("act:/timer unmute %s", j.ID)))
		} else {
			btns = append(btns, DefaultBtn(e.i18n.T(MsgTimerBtnMute), fmt.Sprintf("act:/timer mute %s", j.ID)))
		}
		btns = append(btns, DangerBtn(e.i18n.T(MsgTimerBtnDelete), fmt.Sprintf("act:/timer delete %s", j.ID)))
		cb.ButtonsEqual(btns...)
	}

	cb.Divider()
	cb.Note(e.i18n.T(MsgTimerCardHint))

	var nav []CardButton
	if page > 1 {
		nav = append(nav, e.cardPrevButton(scheduleNavAction("/timer", page-1, all)))
	}
	if all {
		nav = append(nav, DefaultBtn(e.i18n.T(MsgScheduleScopeMineBtn), scheduleNavAction("/timer", 1, false)))
	} else {
		nav = append(nav, DefaultBtn(e.i18n.T(MsgScheduleScopeAllBtn), scheduleNavAction("/timer", 1, true)))
	}
	nav = append(nav, e.cardBackButton())
	if page < pages {
		nav = append(nav, e.cardNextButton(scheduleNavAction("/timer", page+1, all)))
	}
	cb.Buttons(nav...)
	if pages > 1 {
		cb.Note(fmt.Sprintf(e.i18n.T(MsgListPageHint), page, pages))
	}
	return cb.Build()
}

func scheduleNavAction(base string, page int, all bool) string {
	if all {
		return fmt.Sprintf("nav:%s all %d", base, page)
	}
	return fmt.Sprintf("nav:%s %d", base, page)
}

// renderScheduleFailureCard is the proactive alert pushed when a job fails.
func (e *Engine) renderScheduleFailureCard(kind, id, desc string, streak int, runErr error) *Card {
	title := e.i18n.T(MsgScheduleFailureTitle)
	if strings.EqualFold(kind, "timer") {
		title = e.i18n.T(MsgScheduleTimerFailureTitle)
	}
	body := e.i18n.Tf(MsgScheduleFailureBody,
		truncateStr(desc, 80),
		id,
		streak,
		truncateStr(errText(runErr), 300),
	)
	cb := NewCard().Title(title, "red")
	cb.Markdown(body)
	cb.Note(e.i18n.T(MsgScheduleFailureHint))
	return cb.Build()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// notifyScheduleFailure pushes a failure card into the job's chat. Muted jobs are
// skipped (mute suppresses every outbound message by design).
func (e *Engine) notifyScheduleFailure(kind, id, desc, sessionKey string, muted bool, streak int, runErr error) {
	if runErr == nil || muted || strings.TrimSpace(sessionKey) == "" {
		return
	}
	if !shouldNotifyScheduleFailure(streak) {
		return
	}
	platform := e.platformForScheduleKey(sessionKey)
	if platform == nil {
		slog.Warn("schedule failure: platform not found", "kind", kind, "id", id, "session_key", sessionKey)
		return
	}
	replyCtx, err := e.replyCtxForSchedule(platform, sessionKey, id)
	if err != nil {
		slog.Warn("schedule failure: no reply target", "kind", kind, "id", id, "error", err)
		return
	}
	slog.Info("schedule failure alert", "kind", kind, "id", id, "streak", streak, "session_key", sessionKey)
	e.sendWithCard(platform, replyCtx, e.renderScheduleFailureCard(kind, id, desc, streak, runErr))
}

func (e *Engine) platformForScheduleKey(sessionKey string) Platform {
	name := extractPlatformName(sessionKey)
	for _, p := range e.allPlatforms() {
		if strings.EqualFold(p.Name(), name) {
			return p
		}
	}
	// A workspace- or topic-prefixed key may not match by prefix; fall back to
	// the single configured platform when there is only one.
	if len(e.allPlatforms()) == 1 {
		return e.allPlatforms()[0]
	}
	return nil
}

func (e *Engine) replyCtxForSchedule(p Platform, sessionKey, title string) (any, error) {
	if resolver, ok := p.(CronReplyTargetResolver); ok {
		if _, ctx, err := resolver.ResolveCronReplyTarget(sessionKey, title); err == nil && ctx != nil {
			return ctx, nil
		}
	}
	if rec, ok := p.(ReplyContextReconstructor); ok {
		return rec.ReconstructReplyCtx(sessionKey)
	}
	return nil, fmt.Errorf("platform %q cannot resolve a reply target", p.Name())
}
