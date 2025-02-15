package services

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/urfave/cli"
)

const (
	CLICKHOUSE_BATCH_SIZE = "clickhouse-batch-size"
	CLICKHOUSE_REPLICATED = "clickhouse-replicated"
)

func RegisterClickHouseFlags(c *cli.App) {
	c.Flags = append(c.Flags, cli.IntFlag{
		Name:   CLICKHOUSE_BATCH_SIZE,
		Usage:  "clickhouse batch size",
		Value:  1000,
		EnvVar: "CLICKHOUSE_BATCH_SIZE",
	})
	c.Flags = append(c.Flags, cli.BoolFlag{
		Name:   CLICKHOUSE_REPLICATED,
		Usage:  "clickhouse replication enabled",
		EnvVar: "CLICKHOUSE_REPLICATED",
	})
}

type ClickHouse struct {
	db         DBProvider
	batchSize  int
	batch      []*StatRecord
	mux        sync.Mutex
	storeMux   sync.Mutex
	init       sync.Once
	nodeName   string
	replicated bool
}

type StatRecord struct {
	Timestamp     time.Time
	ApiKey        string
	Client        string
	BytesWritten  uint64
	TTFB          uint64
	Duration      uint64
	Path          string
	InfoHash      string
	OriginalPath  string
	SessionID     string
	Domain        string
	Status        uint64
	GroupedStatus uint64
	Edge          string
	Source        string
	Role          string
	Ads           bool
}

func NewClickHouse(c *cli.Context, db DBProvider) *ClickHouse {

	return &ClickHouse{
		db:         db,
		batchSize:  c.Int(CLICKHOUSE_BATCH_SIZE),
		batch:      make([]*StatRecord, 0, c.Int(CLICKHOUSE_BATCH_SIZE)),
		nodeName:   c.String(MY_NODE_NAME),
		replicated: c.Bool(CLICKHOUSE_REPLICATED),
	}
}

func (s *ClickHouse) makeTable(db *sql.DB) error {
	table := "proxy_stat"
	tableExpr := table
	engine := "MergeTree()"
	ttl := "3 MONTH"
	if s.replicated {
		tableExpr += " on cluster '{cluster}'"
		engine = "ReplicatedMergeTree('/clickhouse/{installation}/{cluster}/tables/{shard}/{database}/{table}', '{replica}')"
	}
	_, err := db.Exec(fmt.Sprintf(strings.TrimSpace(`
		CREATE TABLE IF NOT EXISTS %v (
			timestamp      DateTime,
			api_key        String,
			client         String,
			bytes_written  UInt64,
			ttfb           UInt32,
			duration       UInt32,
			path           String,
			infohash       String,
			original_path  String,
			session_id     String,
			domain         String,
			status         UInt16,
			grouped_status UInt16,
			edge           String,
			source         String,
			role           String,
			ads            UInt8,
			node           String
		) engine = %v
		PARTITION BY toYYYYMM(timestamp)
		ORDER BY (timestamp)
		TTL timestamp + INTERVAL %v
	`), tableExpr, engine, ttl))
	if err != nil {
		return err
	}
	if s.replicated {
		_, err = db.Exec(fmt.Sprintf(strings.TrimSpace(`
			CREATE TABLE IF NOT EXISTS %v_all on cluster '{cluster}' as %v
			ENGINE = Distributed('{cluster}', default, %v, rand())
		`), table, table, table))
	}
	return err
}

func (s *ClickHouse) store(sr []*StatRecord) error {
	s.storeMux.Lock()
	if len(sr) == 0 {
		return nil
	}
	logrus.Infof("Storing %v rows to ClickHouse", len(sr))
	defer func() {
		logrus.Infof("Finish storing %v rows to ClickHouse", len(sr))
		s.storeMux.Unlock()
	}()
	db, err := s.db.Get()
	if err != nil {
		return errors.Wrapf(err, "Failed to get ClickHouse DB")
	}
	s.init.Do(func() {
		err = s.makeTable(db)
	})
	if err != nil {
		return errors.Wrapf(err, "Failed to create table")
	}
	err = db.Ping()
	if err != nil {
		return errors.Wrapf(err, "Failed to ping")
	}
	tx, err := db.Begin()
	if err != nil {
		return errors.Wrapf(err, "Failed to begin")
	}
	table := "proxy_stat"
	if s.replicated {
		table += "_all"
	}
	stmt, err := tx.Prepare(fmt.Sprintf(`INSERT INTO %v (timestamp, api_key, client, bytes_written, ttfb,
		duration, path, infohash, original_path, session_id, domain, status, grouped_status, edge,
		source, role, ads, node) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, table))
	if err != nil {
		return errors.Wrapf(err, "Failed to prepare")
	}
	defer stmt.Close()
	for _, r := range sr {
		var adsUInt uint8
		if r.Ads {
			adsUInt = 1
		}
		_, err = stmt.Exec(
			r.Timestamp, r.ApiKey, r.Client, r.BytesWritten, r.TTFB,
			r.Duration, r.Path, r.InfoHash, r.OriginalPath, r.SessionID,
			r.Domain, r.Status, r.GroupedStatus, r.Edge, r.Source,
			r.Role, adsUInt, s.nodeName,
		)
		if err != nil {
			return errors.Wrapf(err, "Failed to exec")
		}
	}
	err = tx.Commit()
	if err != nil {
		return errors.Wrapf(err, "Failed to commit")
	}
	return nil
}

func (s *ClickHouse) Add(sr *StatRecord) error {
	s.mux.Lock()
	s.batch = append(s.batch, sr)
	s.mux.Unlock()
	if len(s.batch) >= s.batchSize {
		go func(b []*StatRecord) {
			err := s.store(b)
			if err != nil {
				logrus.WithError(err).Warn("Failed to store to ClickHouse")
			}
		}(s.batch)
		s.mux.Lock()
		s.batch = make([]*StatRecord, 0, s.batchSize)
		s.mux.Unlock()
	}
	return nil
}

func (s *ClickHouse) Close() {
	s.store(s.batch)
	s.batch = []*StatRecord{}
}
