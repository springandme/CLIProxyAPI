package codexinspection

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

type Worker struct {
	repository Repository
	service    *Service
}

func NewWorker(repository Repository, service *Service) *Worker {
	return &Worker{repository: repository, service: service}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.service == nil {
		return
	}
	go w.run(ctx)
}

func (w *Worker) run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	w.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	cfg, configured, err := w.service.ResolveConfig(ctx)
	if err != nil {
		log.WithError(err).Warn("resolve codex inspection config")
		return
	}
	if !configured || cfg.Enabled == nil || !*cfg.Enabled {
		return
	}
	now := time.Now()
	triggerKey := CodexInspectionTriggerKey(now, cfg)
	if triggerKey == "" || !CodexInspectionScheduleDue(now, w.lastScheduledRunTime(ctx), cfg) {
		return
	}
	if _, ok, err := w.repository.GetLatestCodexInspectionRunByTrigger(ctx, CodexInspectionTriggerScheduled, triggerKey); err != nil {
		log.WithError(err).Warn("load codex inspection trigger")
		return
	} else if ok {
		return
	}
	go func() {
		if _, err := w.service.Run(ctx, RunRequest{
			TriggerType: CodexInspectionTriggerScheduled,
			TriggerKey:  triggerKey,
		}); err != nil && err != ErrRunAlreadyActive {
			log.WithError(err).Warn("run scheduled codex inspection")
		}
	}()
}

func (w *Worker) lastScheduledRunTime(ctx context.Context) time.Time {
	runs, err := w.repository.ListCodexInspectionRuns(ctx, 20)
	if err != nil {
		return time.Time{}
	}
	for _, run := range runs {
		if run.TriggerType != CodexInspectionTriggerScheduled || run.StartedAtMS <= 0 {
			continue
		}
		return time.UnixMilli(run.StartedAtMS)
	}
	return time.Time{}
}
