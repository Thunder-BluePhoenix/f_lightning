package canal

import (
	"fmt"
	"strings"

	"frappe_lightning/config"

	gomysql "github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"go.uber.org/zap"
)

// RowEvent is the normalised event sent downstream to the sync engine.
type RowEvent struct {
	Site    string
	Table   string                   // e.g. "tabSales Invoice"
	Action  string                   // insert | update | delete
	Columns []string                 // Column names in order
	Rows    [][]interface{}          // Raw cell values
}

// Handler implements canal.EventHandler and forwards row events to a channel.
type Handler struct {
	site     string
	schemas  []config.IndexSchema
	eventsCh chan<- *RowEvent
	log      *zap.Logger
}

func newHandler(site string, schemas []config.IndexSchema, ch chan<- *RowEvent, log *zap.Logger) *Handler {
	return &Handler{site: site, schemas: schemas, eventsCh: ch, log: log}
}

// OnRow is called by go-mysql for every binlog row event.
func (h *Handler) OnRow(e *gomysql.RowsEvent) error {
	tableName := e.Table.Name
	if _, ok := config.SchemaByTable(h.schemas, tableName); !ok {
		return nil // Not a tracked DocType — ignore
	}

	// Extract column names
	cols := make([]string, len(e.Table.Columns))
	for i, c := range e.Table.Columns {
		cols[i] = c.Name
	}

	action := strings.ToLower(e.Action)
	h.log.Info("binlog event",
		zap.String("site", h.site),
		zap.String("table", tableName),
		zap.String("action", action),
		zap.Int("rows", len(e.Rows)),
	)

	h.eventsCh <- &RowEvent{
		Site:    h.site,
		Table:   tableName,
		Action:  action,
		Columns: cols,
		Rows:    e.Rows,
	}
	return nil
}

// --- Required stubs for canal.EventHandler interface ---

func (h *Handler) OnRotate(header *replication.EventHeader, rotateEvent *replication.RotateEvent) error { return nil }
func (h *Handler) OnTableChanged(header *replication.EventHeader, schema string, table string) error { return nil }
func (h *Handler) OnDDL(header *replication.EventHeader, nextPos mysql.Position, queryEvent *replication.QueryEvent) error { return nil }
func (h *Handler) OnXID(header *replication.EventHeader, nextPos mysql.Position) error { return nil }
func (h *Handler) OnGTID(header *replication.EventHeader, gtidEvent mysql.BinlogGTIDEvent) error { return nil }
func (h *Handler) OnPosSynced(header *replication.EventHeader, pos mysql.Position, set mysql.GTIDSet, force bool) error { return nil }
func (h *Handler) OnRowsQueryEvent(e *replication.RowsQueryEvent) error { return nil }
func (h *Handler) OnTableNotFound(header *replication.EventHeader, event *replication.RowsEvent) error { return nil }
func (h *Handler) String() string                                   { return "LightningHandler" }

// Start connects to MariaDB binlog and begins streaming events into ch.
// It is designed to be run as a goroutine. It blocks until the canal is closed.
func Start(site *config.SiteConfig, schemas []config.IndexSchema, ch chan<- *RowEvent, log *zap.Logger) error {
	cfg := gomysql.NewDefaultConfig()
	cfg.Addr = fmt.Sprintf("%s:%d", site.MariaDB.Host, site.MariaDB.Port)
	cfg.User = site.MariaDB.User
	cfg.Password = site.MariaDB.Password
	cfg.ServerID = site.MariaDB.ServerID
	cfg.Flavor = "mariadb"
	cfg.Dump.ExecutionPath = "" // Disable mysqldump — we use backfill CLI instead

	// Only listen to tables we care about
	var tableRegexes []string
	for _, s := range schemas {
		// go-mysql uses regex; escape spaces in table names
		tableRegexes = append(tableRegexes, strings.ReplaceAll(s.Table, " ", `\ `))
	}
	cfg.IncludeTableRegex = tableRegexes

	c, err := gomysql.NewCanal(cfg)
	if err != nil {
		return fmt.Errorf("canal: NewCanal failed: %w", err)
	}

	handler := newHandler(site.Name, schemas, ch, log)
	c.SetEventHandler(handler)

	// Try to resume from saved binlog position
	pos, err := LoadPosition(site.Name)
	if err != nil {
		// No saved position — start from the current tail of the binlog
		currentPos, err := c.GetMasterPos()
		if err != nil {
			return fmt.Errorf("canal: GetMasterPos failed: %w", err)
		}
		log.Info("starting from current binlog tail",
			zap.String("site", site.Name),
			zap.String("file", currentPos.Name),
			zap.Uint32("pos", currentPos.Pos),
		)
		return c.RunFrom(currentPos)
	}

	log.Info("resuming from saved binlog position",
		zap.String("site", site.Name),
		zap.String("file", pos.File),
		zap.Uint32("pos", pos.Pos),
	)
	return c.RunFrom(mysql.Position{Name: pos.File, Pos: pos.Pos})
}
