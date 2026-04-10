package webhook

import (
	"context"
	"fmt"
	"time"

	"frappe_lightning/config"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const workerPoolSize = 20

// Engine is the top-level webhook processing component for one Frappe site.
type Engine struct {
	site   string
	cons   *Consumer
	subs   *SubscriptionStore
	del    *Deliverer
	log    *Logger
	met    *WebhookMetrics
	pool   chan DeliveryTask
	retrier *Retrier
	zapLog *zap.Logger
}

// NewEngine constructs and wires all webhook components for the given site.
// dsn is the MariaDB DSN for the site's database (used to load subscriptions).
func NewEngine(site string, rdb *redis.Client, dsn string, cfg config.WebhookConfig, log *zap.Logger) (*Engine, error) {
	workerID := fmt.Sprintf("lightning-%s-%d", site, time.Now().UnixNano())

	cons, err := NewConsumer(rdb, site, workerID, log)
	if err != nil {
		return nil, fmt.Errorf("webhook consumer: %w", err)
	}

	pool := make(chan DeliveryTask, workerPoolSize*2)
	met := NewWebhookMetrics(site)

	e := &Engine{
		site:   site,
		cons:   cons,
		subs:   NewSubscriptionStore(site, dsn, rdb, log),
		del:    NewDeliverer(),
		log:    NewLogger(rdb, site),
		met:    met,
		pool:   pool,
		zapLog: log,
	}
	e.retrier = NewRetrier(cons, pool)
	return e, nil
}

// Start runs the engine until ctx is cancelled.
// It launches the subscription refresh loop, the delivery worker pool, and the
// main event consumer loop.
func (e *Engine) Start(ctx context.Context) {
	e.zapLog.Info("webhook engine started", zap.String("site", e.site))

	// Subscription refresh loop.
	go e.subs.Start(ctx)

	// Delivery worker pool.
	for i := 0; i < workerPoolSize; i++ {
		go e.deliveryWorker(ctx)
	}

	// DLQ depth gauge updater.
	go e.pollDLQDepth(ctx)

	// Main read loop.
	for {
		select {
		case <-ctx.Done():
			e.zapLog.Info("webhook engine stopped", zap.String("site", e.site))
			return
		default:
		}

		msgs, err := e.cons.ReadBatch(ctx)
		if err != nil {
			e.zapLog.Error("webhook stream read error",
				zap.String("site", e.site), zap.Error(err))
			continue
		}
		if len(msgs) == 0 {
			continue
		}

		e.met.pending.Add(float64(len(msgs)))

		for _, msg := range msgs {
			ev, err := ParseEvent(msg)
			if err != nil {
				e.zapLog.Warn("bad webhook event", zap.String("id", msg.ID), zap.Error(err))
				e.cons.Ack(ctx, msg.ID)
				continue
			}

			subs := e.subs.Match(ev.DocType, ev.Event)
			if len(subs) == 0 {
				e.cons.Ack(ctx, msg.ID)
				e.met.pending.Dec()
				continue
			}

			for _, sub := range subs {
				e.pool <- DeliveryTask{
					DeliveryID:  NewDeliveryID(),
					Event:       *ev,
					Sub:         sub,
					AttemptNum:  0,
					StreamMsgID: msg.ID,
				}
			}

			// Ack after dispatching all subs for this message.
			e.cons.Ack(ctx, msg.ID)
		}
	}
}

// deliveryWorker drains the pool channel and attempts HTTP delivery.
func (e *Engine) deliveryWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-e.pool:
			result := e.del.Send(ctx, task)
			e.log.Record(ctx, task, result)
			e.met.pending.Dec()

			status := "success"
			if !result.Success {
				status = "failed"
			}
			e.met.deliveriesTotal.WithLabelValues(task.Event.DocType, task.Event.Event, status).Inc()
			e.met.deliveryLatency.WithLabelValues(task.Sub.EndpointURL).
				Observe(float64(result.LatencyMs) / 1000)

			if !result.Success {
				if task.AttemptNum > 0 {
					e.met.retryTotal.WithLabelValues(fmt.Sprintf("%d", task.AttemptNum)).Inc()
				}
				e.retrier.Schedule(ctx, task)
			}
		}
	}
}

func (e *Engine) pollDLQDepth(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.met.dlqDepth.Set(float64(e.cons.DLQDepth(ctx)))
		}
	}
}

// Consumer exposes the Consumer so CLI commands can query DLQ / log.
func (e *Engine) Consumer() *Consumer { return e.cons }

// Log exposes the Logger for CLI replay commands.
func (e *Engine) Log() *Logger { return e.log }

// Subscriptions exposes the SubscriptionStore for CLI list commands.
func (e *Engine) Subscriptions() *SubscriptionStore { return e.subs }
