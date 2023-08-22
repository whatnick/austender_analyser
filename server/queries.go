package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	clickhouse "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/shopspring/decimal"
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
	Success  QueryStatus = "success"
	Canceled QueryStatus = "canceled"
	Error    QueryStatus = "error"
)

func (q *Query) Run(ctx context.Context) error {
	return QueryRows()
}

func (q *Query) Cancel(at time.Time) {
}

func GetClickConn() (*sql.DB, error) {
	conn := clickhouse.OpenDB(&clickhouse.Options{
		Addr: []string{"127.0.0.1:9000"},
		Auth: clickhouse.Auth{
			Database: "austender",
			Username: "default",
			Password: "",
		},
		Settings: clickhouse.Settings{
			"max_execution_time": 60,
		},
		DialTimeout: time.Second * 30,
		Compression: &clickhouse.Compression{
			clickhouse.CompressionLZ4,
			0, // optional, parameter
		},
		Debug:                false,
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

	// TODO: Pass in filter parameters
	rows, err := conn.Query("SELECT * FROM contracts")
	if err != nil {
		return err
	}
	var (
		cnid            string
		agency          string
		publish_date    string
		contract_value  decimal.Decimal
		contract_period string
		atmid           string
		sonid           string
		supplier_name   string
	)
	for rows.Next() {
		if err := rows.Scan(
			&cnid,
			&agency,
			&publish_date,
			&contract_value,
			&contract_period,
			&atmid,
			&sonid,
			&supplier_name); err != nil {
			return err
		}
		fmt.Printf("row: CNID=%s, Agency=%s, PublishDate=%s, Contract Value=%s, Contract Period=%s, ATMID=%s, SONID=%s, Supplier Name=%s\n",
			cnid, agency, publish_date, &contract_value, contract_period, atmid, sonid, supplier_name)
	}

	return nil
}
