package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ScheduledTask mirrors the Frappe `tabScheduled Job Type` row.
type ScheduledTask struct {
	Method    string
	Frequency string // "Daily", "Hourly", "Weekly", "Monthly", "Cron", "All", "Yearly"
	CronExpr  string // only meaningful when Frequency = "Cron"
}

// Scheduler reads Frappe's scheduled tasks from MariaDB and enqueues them
// on their configured interval using Go tickers — no external cron library needed.
type Scheduler struct {
	site      string
	rdb       *redis.Client
	benchPath string
	dsn       string
	log       *zap.Logger
}

// NewScheduler creates a Scheduler for the given site.
// dsn is the MariaDB DSN for the site's database.
func NewScheduler(site string, rdb *redis.Client, benchPath string, log *zap.Logger) *Scheduler {
	return &Scheduler{
		site:      site,
		rdb:       rdb,
		benchPath: benchPath,
		log:       log,
	}
}

// NewSchedulerWithDSN creates a Scheduler with an explicit MariaDB DSN.
func NewSchedulerWithDSN(site string, rdb *redis.Client, benchPath, dsn string, log *zap.Logger) *Scheduler {
	s := NewScheduler(site, rdb, benchPath, log)
	s.dsn = dsn
	return s
}

// Start loads scheduled tasks from Frappe's DB and fires each on its interval.
// Blocks until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	if s.dsn == "" {
		s.log.Warn("scheduler has no DSN — skipping scheduled tasks", zap.String("site", s.site))
		return
	}

	tasks, err := s.loadTasks()
	if err != nil {
		s.log.Error("failed to load scheduled tasks", zap.String("site", s.site), zap.Error(err))
		return
	}

	if len(tasks) == 0 {
		s.log.Info("no scheduled tasks found", zap.String("site", s.site))
		return
	}

	s.log.Info("scheduler started", zap.String("site", s.site), zap.Int("tasks", len(tasks)))

	// Group tasks by their interval and start one ticker per unique interval.
	type tickerGroup struct {
		interval time.Duration
		methods  []string
	}
	groups := make(map[time.Duration]*tickerGroup)

	for _, t := range tasks {
		d := frequencyToDuration(t.Frequency)
		if d <= 0 {
			continue
		}
		if g, ok := groups[d]; ok {
			g.methods = append(g.methods, t.Method)
		} else {
			groups[d] = &tickerGroup{interval: d, methods: []string{t.Method}}
		}
	}

	for _, g := range groups {
		go func(group *tickerGroup) {
			ticker := time.NewTicker(group.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					for _, method := range group.methods {
						s.enqueue(ctx, method)
					}
				}
			}
		}(g)
	}

	<-ctx.Done()
	s.log.Info("scheduler stopped", zap.String("site", s.site))
}

// enqueue pushes a scheduled method onto the default RQ queue.
func (s *Scheduler) enqueue(ctx context.Context, method string) {
	jobID := fmt.Sprintf("scheduled_%s_%d", sanitizeMethod(method), time.Now().UnixNano())
	payload := fmt.Sprintf(`{"id":%q,"call_string":%q,"args":[],"kwargs":{},"timeout":300,"enqueued_at":%q}`,
		jobID, method, time.Now().Format(time.RFC3339))

	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, "rq:job:"+jobID, payload, 24*time.Hour)
	pipe.LPush(ctx, "rq:queue:default", jobID)
	if _, err := pipe.Exec(ctx); err != nil {
		s.log.Error("failed to enqueue scheduled job",
			zap.String("site", s.site),
			zap.String("method", method),
			zap.Error(err),
		)
		return
	}
	s.log.Debug("scheduled job enqueued", zap.String("site", s.site), zap.String("method", method))
}

// loadTasks reads all active Frappe scheduled tasks from MariaDB.
func (s *Scheduler) loadTasks() ([]ScheduledTask, error) {
	db, err := sql.Open("mysql", s.dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		"SELECT method, frequency, IFNULL(cron_format, '') FROM `tabScheduled Job Type` WHERE stopped = 0",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []ScheduledTask
	for rows.Next() {
		var t ScheduledTask
		if err := rows.Scan(&t.Method, &t.Frequency, &t.CronExpr); err != nil {
			continue
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// frequencyToDuration converts Frappe frequency strings to Go durations.
func frequencyToDuration(freq string) time.Duration {
	switch strings.ToLower(freq) {
	case "all":
		return 5 * time.Minute
	case "hourly", "hourly_long":
		return time.Hour
	case "daily", "daily_long":
		return 24 * time.Hour
	case "weekly", "weekly_long":
		return 7 * 24 * time.Hour
	case "monthly", "monthly_long":
		return 30 * 24 * time.Hour
	case "yearly":
		return 365 * 24 * time.Hour
	default:
		return 0 // "Cron" expressions are not yet parsed
	}
}

func sanitizeMethod(method string) string {
	r := strings.NewReplacer(".", "_", "/", "_")
	return r.Replace(method)
}
