package core

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// 自动生成会话标题。
//
// 时机由 web 会话列表的请求触发（页面打开/每 3s 轮询），后端只在满足条件时
// 入队，因此不需要额外定时器；不开页面就不会消耗模型。
//
// 节流规则：
//   - 只处理「已完成」且还没有模型标题的会话（进行中的内容还在变，生成了也会过时）
//   - 同一会话内容没有增长就不再生成；后续对话让文件变大才允许再生成一次
//   - 单 worker 串行，每条之间隔 10s（≈6 条/分钟），失败不重试同版本内容
const (
	titleFillQueueCap = 50
	titleFillTimeout  = 2 * time.Minute
)

// titleFillGap is a var so tests can shrink the throttle.
var titleFillGap = 10 * time.Second

type titleJob struct {
	id   string
	size int64
}

type titleBackfill struct {
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []titleJob
	queued map[string]bool
	gen    map[string]int64 // id → 上次生成时的文件大小
}

func newTitleBackfill() *titleBackfill {
	b := &titleBackfill{queued: map[string]bool{}, gen: map[string]int64{}}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// backfill lazily starts the single worker on first use.
func (m *ManagementServer) backfill() *titleBackfill {
	m.titleOnce.Do(func() {
		m.titleFill = newTitleBackfill()
		go m.runTitleBackfill()
	})
	return m.titleFill
}

// scheduleTitleBackfill enqueues sessions that need a title. Called from the
// library list handler, so only sessions the web page actually loads are queued.
func (m *ManagementServer) scheduleTitleBackfill(items []map[string]any, since time.Time) {
	if len(items) == 0 {
		return
	}
	b := m.backfill()
	b.mu.Lock()
	added := false
	for _, it := range items {
		if len(b.queue) >= titleFillQueueCap {
			break
		}
		id, _ := it["id"].(string)
		status, _ := it["status"].(string)
		hasTitle, _ := it["has_title"].(bool)
		size, _ := it["size"].(int64)
		if id == "" || status != sessionDone || hasTitle || b.queued[id] {
			continue
		}
		// 只补当前筛选范围内（web 页面真正显示的）会话，避免一次把历史全跑一遍。
		if !since.IsZero() {
			if t, ok := it["updated_at"].(time.Time); ok && t.Before(since) {
				continue
			}
		}
		if prev, ok := b.gen[id]; ok && size <= prev {
			continue
		}
		b.queued[id] = true
		b.queue = append(b.queue, titleJob{id: id, size: size})
		added = true
	}
	b.mu.Unlock()
	if added {
		b.cond.Signal()
	}
}

func (m *ManagementServer) runTitleBackfill() {
	b := m.titleFill
	for {
		b.mu.Lock()
		for len(b.queue) == 0 {
			b.cond.Wait()
		}
		job := b.queue[0]
		b.queue = b.queue[1:]
		b.mu.Unlock()

		m.fillOneTitle(job)

		b.mu.Lock()
		delete(b.queued, job.id)
		b.gen[job.id] = job.size
		b.mu.Unlock()

		time.Sleep(titleFillGap)
	}
}

func (m *ManagementServer) fillOneTitle(job titleJob) {
	dir := m.transcriptDir()
	gen := m.titleGenerator()
	if dir == "" || gen == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), titleFillTimeout)
	defer cancel()
	title, err := m.generateTranscriptTitle(ctx, gen, dir, job.id)
	if err != nil {
		// 失败记账后等内容增长再试，避免同版本内容反复调用模型。
		slog.Warn("title backfill: generate failed", "id", job.id, "error", err)
		return
	}
	slog.Info("title backfill: generated", "id", job.id, "title", title)
}

// titleGenerator returns any running project's agent that can generate titles.
func (m *ManagementServer) titleGenerator() TitleGenerator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.engines {
		if g, ok := e.agent.(TitleGenerator); ok {
			return g
		}
	}
	return nil
}

// pendingTitleJobs reports how many sessions are still waiting (tests/diagnostics).
func (m *ManagementServer) pendingTitleJobs() int {
	if m.titleFill == nil {
		return 0
	}
	m.titleFill.mu.Lock()
	defer m.titleFill.mu.Unlock()
	return len(m.titleFill.queue)
}
