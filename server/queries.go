package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"strconv"
	"sync"
	"time"

	clickhouse "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"
)

type Query struct {
	sync.Mutex
	cancel func()

	ID         uint16      `json:"id" form:"id" param:"id" query:"id"`
	Company    string      `json:"company" form:"company"`
	Department string      `json:"department" form:"department"`
	Keyword    string      `json:"keyword" form:"keyword"`
	Status     QueryStatus `json:"status"`
	Results    string      `json:"results"`
	Error      error
	Started    time.Time     `json:"started"`
	Duration   time.Duration `json:"duration" form:"duration"`
}

type QueryStatus string

const (
	Running  QueryStatus = "running"
	Success              = "success"
	Canceled             = "canceled"
	Error                = "error"
)

func (q *Query) Run(ctx context.Context) error {
	return nil
}

func (q *Query) Cancel(at time.Time) {
}

func GetClickConn() (*sql.DB, error) {
	conn := clickhouse.OpenDB(&clickhouse.Options{
		Addr: []string{"127.0.0.1:9999"},
		Auth: clickhouse.Auth{
			Database: "default",
			Username: "default",
			Password: "",
		},
		TLS: &tls.Config{
			InsecureSkipVerify: true,
		},
		Settings: clickhouse.Settings{
			"max_execution_time": 60,
		},
		DialTimeout: time.Second * 30,
		Compression: &clickhouse.Compression{
			clickhouse.CompressionLZ4,
			0, // optional, parameter
		},
		Debug:                true,
		BlockBufferSize:      10,
		MaxCompressionBuffer: 10240,
		ClientInfo: clickhouse.ClientInfo{ // optional, please see Client info section in the README.md
			Products: []struct {
				Name    string
				Version string
			}{
				{Name: "austender-client", Version: "0.1"},
			},
		},
	})
	conn.SetMaxIdleConns(5)
	conn.SetMaxOpenConns(10)
	conn.SetConnMaxLifetime(time.Hour)

	return conn, nil
}

func QueryRows() error {
	conn, err := GetClickConn()
	if err != nil {
		return err
	}
	if err != nil {
		return err
	}
	defer func() {
		conn.Exec("DROP TABLE example")
	}()
	conn.Exec("DROP TABLE IF EXISTS example")
	if _, err := conn.Exec(`
		CREATE TABLE example (
			  Col1 UInt8
			, Col2 String
			, Col3 FixedString(3)
			, Col4 UUID
			, Col5 Map(String, UInt8)
			, Col6 Array(String)
			, Col7 Tuple(String, UInt8, Array(Map(String, String)))
			, Col8 DateTime
		) Engine = Memory
	`); err != nil {
		return err
	}

	if err != nil {
		return err
	}
	scope, err := conn.Begin()
	if err != nil {
		return err
	}
	batch, err := scope.Prepare("INSERT INTO example")
	if err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		if _, err := batch.Exec(
			uint8(i),
			"ClickHouse",
			fmt.Sprintf("%03d", uint8(i)),
			uuid.New(),
			map[string]uint8{"key": uint8(i)},
			[]string{strconv.Itoa(i), strconv.Itoa(i + 1), strconv.Itoa(i + 2), strconv.Itoa(i + 3), strconv.Itoa(i + 4), strconv.Itoa(i + 5)},
			[]any{
				strconv.Itoa(i), uint8(i), []map[string]string{
					{"key": strconv.Itoa(i)},
					{"key": strconv.Itoa(i + 1)},
					{"key": strconv.Itoa(i + 2)},
				},
			},
			time.Now(),
		); err != nil {
			return err
		}
	}
	if err := scope.Commit(); err != nil {
		return err
	}
	rows, err := conn.Query("SELECT * FROM example")
	if err != nil {
		return err
	}
	var (
		col1             uint8
		col2, col3, col4 string
		col5             map[string]uint8
		col6             []string
		col7             any
		col8             time.Time
	)
	for rows.Next() {
		if err := rows.Scan(&col1, &col2, &col3, &col4, &col5, &col6, &col7, &col8); err != nil {
			return err
		}
		fmt.Printf("row: col1=%d, col2=%s, col3=%s, col4=%s, col5=%v, col6=%v, col7=%v, col8=%v\n", col1, col2, col3, col4, col5, col6, col7, col8)
	}

	return nil
}
