# Go Template SQL Builder & ORM

[![go.dev reference](https://img.shields.io/badge/go.dev-reference-007d9c?logo=go&logoColor=white)](https://pkg.go.dev/github.com/wroge/sqlt)
[![GitHub tag (latest SemVer)](https://img.shields.io/github/tag/wroge/sqlt.svg?style=social)](https://github.com/wroge/sqlt/tags)

This package uses Go’s template engine to create a flexible and powerful SQL builder and ORM.

```go
go get -u github.com/wroge/sqlt
```

## How does it work?

- All input values are safely escaped and replaced with the correct placeholders at execution time.
- Functions like ```ScanInt64``` generate ```sqlt.Scanner`s```, which hold pointers to the destination and optionally a mapper. These scanners are collected at execution time.
- The ```Dest``` function is a placeholder that is replaced at execution time with the appropriate generic type.
- SQL templates can be loaded from the filesystem using ```ParseFS``` or ```ParseFiles```.

## Example

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Masterminds/sprig/v3"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/wroge/sqlt"
)

type startKey struct{}

type Book struct {
	ID        uuid.UUID
	Title     string
	CreatedAt time.Time
}

var (
	t = sqlt.New("db").
		Dollar().
		Funcs(sprig.TxtFuncMap()).
		BeforeRun(func(op sqlt.Operation, runner *sqlt.Runner) {
			runner.Context = context.WithValue(runner.Context, startKey{}, time.Now())
		}).
		AfterRun(func(err error, op sqlt.Operation, runner *sqlt.Runner) error {
			var duration = time.Since(runner.Context.Value(startKey{}).(time.Time))

			if err != nil {
				// ignore sql.ErrNoRows
				if op == sqlt.FetchAllOperation && errors.Is(err, sql.ErrNoRows) {
					return nil
				}

				// apply error logging here
				fmt.Println(err, runner.Text.Name(), duration, runner.SQL, runner.Args)

				return err
			}

			// apply normal logging here
			fmt.Println(runner.Text.Name(), duration, runner.SQL, runner.Args)

			return nil
		})

		// INSERT INTO books (id, title, created_at) VALUES

	insert = t.New("insert").MustParse(`
		INSERT INTO books (id, title, created_at) VALUES
		{{ range $i, $t := . }} {{ if $i }}, {{ end }}
			({{ uuidv4 }}, {{ $t }}, {{ now }})
		{{ end }}
		RETURNING id;
	`)

	query = t.New("query").MustParse(`
		SELECT
			{{ Scan Dest.ID "id" }}
			{{ ScanString Dest.Title ", title" }}
			{{ ScanTime Dest.CreatedAt ", created_at" }}
		FROM books
		WHERE INSTR(title, {{ .Search }}) > 0
	`)
)

func main() {
	ctx := context.Background()

	db, err := sql.Open("sqlite3", "file:test.db?cache=shared&mode=memory")
	if err != nil {
		panic(err)
	}

	_, err = db.Exec("CREATE TABLE books (id, title, created_at DATE)")
	if err != nil {
		panic(err)
	}

	_, err = insert.Exec(ctx, db, []string{
		"The Bitcoin Standard",
		"Sapiens: A Brief History of Humankind",
		"100 Go Mistakes and How to Avoid Them",
		"Mastering Bitcoin",
	})
	if err != nil {
		panic(err)
	}
	// insert 426.75µs INSERT INTO books (id, title, created_at) VALUES ($1, $2, $3) , ($4, $5, $6) , ($7, $8, $9) , ($10, $11, $12) RETURNING id;

	books, err := sqlt.FetchAll[Book](ctx, query, db, map[string]any{
		"Search": "Bitcoin",
	})
	if err != nil {
		panic(err)
	}
	// query 137.041µs SELECT id , title , created_at FROM books WHERE INSTR(title, $1) > 0

	fmt.Println(books)
	// [{ac4ceabc-0091-4d2f-85e7-1ada0069ca7e The Bitcoin Standard 2024-08-04 11:20:17.821766 +0200 +0200} {7ea05e4d-1578-4088-b00b-3dbbf2f55576 Mastering Bitcoin 2024-08-04 11:20:17.821783 +0200 +0200}]
}
```

## Example & Benchmarks

[https://github.com/wroge/vertical-slice-architecture](https://github.com/wroge/vertical-slice-architecture)

```
go test -bench . -benchmem .
goos: darwin
goarch: arm64
pkg: github.com/wroge/sqlt
BenchmarkSqltFirst-12              27699             87936 ns/op            9742 B/op         90 allocs/op
BenchmarkSquirrelFirst-12          32630             89447 ns/op           11110 B/op         98 allocs/op
BenchmarkSqltAll-12                27032             85363 ns/op           10941 B/op        101 allocs/op
BenchmarkSquirrelAll-12            32100             89801 ns/op           12250 B/op        107 allocs/op
PASS
ok      github.com/wroge/sqlt   12.359s
```

## Inspiration

- [VauntDev/tqla](https://github.com/VauntDev/tqla)
- [mhilton/sqltemplate](https://github.com/mhilton/sqltemplate)
