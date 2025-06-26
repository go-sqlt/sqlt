package sqlt_test

import (
	"context"
	"database/sql"
	"math/big"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/go-sqlt/sqlt"
	_ "modernc.org/sqlite"
)

func TestAll(t *testing.T) {
	type Data struct {
		Int    int64
		String string
		Bool   bool
		Time   time.Time
		Big    *big.Int
		URL    *url.URL
		Slice  []string
		JSON   map[string]any
	}

	time.Local = time.UTC
	date, err := time.Parse(time.DateOnly, "2000-12-31")
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse("https://example.com/path?query=yes")
	if err != nil {
		t.Fatal(err)
	}

	expect := Data{
		Int:    100,
		String: "value",
		Bool:   true,
		Time:   date,
		Big:    big.NewInt(300),
		URL:    u,
		Slice:  []string{"hello", "world"},
		JSON: map[string]any{
			"hello": "world",
		},
	}

	query := sqlt.All[any, Data](sqlt.Parse(`
		SELECT
			100                                    {{ Scan.Int.To "Int" }}
			, 'value'                              {{ Scan.String.To "String" }}
			, true                                 {{ Scan.To "Bool" }}
			, '2000-12-31'                         {{ (Scan.String.Time DateOnly).To "Time" }}
			, '300'                                {{ Scan.Text.To "Big" }}
			, 'https://example.com/path?query=yes' {{ Scan.Binary.To "URL" }}
			, 'hello,world'                        {{ (Scan.String.Split ",").To "Slice" }}
			, '{"hello":"world"}'                  {{ Scan.JSON.To "JSON" }}
	`))

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}

	data, err := query.Exec(context.Background(), db, time.Now().Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expect, data[0]) {
		t.Fatalf("all: \n expected: %v \n got: %v", expect, data[0])
	}
}

func TestOne(t *testing.T) {
	type Data struct {
		Int    int64
		String string
		Bool   bool
		Time   time.Time
		Big    *big.Int
		URL    *url.URL
		Slice  []string
		JSON   map[string]any
	}

	time.Local = time.UTC
	date, err := time.Parse(time.DateOnly, "2000-12-31")
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse("https://example.com/path?query=yes")
	if err != nil {
		t.Fatal(err)
	}

	expect := Data{
		Int:    100,
		String: "value",
		Bool:   true,
		Time:   date,
		Big:    big.NewInt(300),
		URL:    u,
		Slice:  []string{"hello", "world"},
		JSON: map[string]any{
			"hello": "world",
		},
	}

	var cached int

	query := sqlt.One[any, Data](
		sqlt.Logger(func(ctx context.Context, info sqlt.Info) {
			if info.Cached {
				cached++
			}
		}),
		sqlt.ExpressionSize(100),
		sqlt.ParseFiles("testquery.tpl"), sqlt.Lookup("query"))

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}

	data, err := query.Exec(context.Background(), db, time.Now().Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expect, data) {
		t.Fatalf("one: \n expected: %v \n got: %v", expect, data)
	}

	data2, err := query.Exec(context.Background(), db, time.Now().Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expect, data2) {
		t.Fatalf("one: \n expected: %v \n got: %v", expect, data)
	}

	if cached != 1 {
		t.Fatal("not cached")
	}
}

func TestFirst(t *testing.T) {
	type Data struct {
		Int    int64
		String string
		Bool   bool
		Time   time.Time
		Big    *big.Int
		URL    *url.URL
		Slice  []string
		JSON   map[string]any
	}

	time.Local = time.UTC
	date, err := time.Parse(time.DateOnly, "2000-12-31")
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse("https://example.com/path?query=yes")
	if err != nil {
		t.Fatal(err)
	}

	expect := Data{
		Int:    100,
		String: "value",
		Bool:   true,
		Time:   date,
		Big:    big.NewInt(300),
		URL:    u,
		Slice:  []string{"hello", "world"},
		JSON: map[string]any{
			"hello": "world",
		},
	}

	query := sqlt.First[any, Data](sqlt.ExpressionExpiration(time.Second), sqlt.ParseGlob("testquery.tpl"), sqlt.Lookup("query"))

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		panic(err)
	}

	data, err := query.Exec(context.Background(), db, time.Now().Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(expect, data) {
		t.Fatalf("one: \n expected: %v \n got: %v", expect, data)
	}
}
